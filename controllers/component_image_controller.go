/*
Copyright 2023 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	l "github.com/redhat-appstudio/image-controller/pkg/logs"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
)

const (
	ImageAnnotationName                 = "image.redhat.com/image"
	GenerateImageAnnotationName         = "image.redhat.com/generate"
	DeleteImageRepositoryAnnotationName = "image.redhat.com/delete-image-repo"

	ImageRepositoryFinalizer = "image-controller.appstudio.openshift.io/image-repository"
)

// GenerateRepositoryOpts defines patameters for image repository to be generated.
// The opts are read from "image.redhat.com/generate" annotation.
type GenerateRepositoryOpts struct {
	Visibility string `json:"visibility,omitempty"`
}

// ImageRepositoryStatus defines the structure of the Repository information being exposed to external systems.
type ImageRepositoryStatus struct {
	Image      string `json:"image,omitempty"`
	Visibility string `json:"visibility,omitempty"`
	Secret     string `json:"secret,omitempty"`

	Message string `json:"message,omitempty"`
}

// ComponentReconciler reconciles a Controller object
type ComponentReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	QuayClient       quay.QuayService
	QuayOrganization string
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appstudioredhatcomv1alpha1.Component{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ComponentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithName("ComponentImageRepository")
	ctx = ctrllog.IntoContext(ctx, log)

	// Fetch the Component instance
	component := &appstudioredhatcomv1alpha1.Component{}
	err := r.Client.Get(ctx, req.NamespacedName, component)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, fmt.Errorf("error reading component: %w", err)
	}

	if !component.ObjectMeta.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(component, ImageRepositoryFinalizer) {
			robotAccountName := generateRobotAccountName(component)
			isDeleted, err := r.QuayClient.DeleteRobotAccount(r.QuayOrganization, robotAccountName)
			if err != nil {
				log.Error(err, "failed to delete robot account", l.Action, l.ActionDelete, l.Audit, "true")
				// Do not block Component deletion if failed to delete robot account
			}
			if isDeleted {
				log.Info(fmt.Sprintf("Deleted robot account %s", robotAccountName), l.Action, l.ActionDelete)
			}

			if val, exists := component.Annotations[DeleteImageRepositoryAnnotationName]; exists && val == "true" {
				imageRepo := generateRepositoryName(component)
				isRepoDeleted, err := r.QuayClient.DeleteRepository(r.QuayOrganization, imageRepo)
				if err != nil {
					log.Error(err, "failed to delete image repository", l.Action, l.ActionDelete, l.Audit, "true")
					// Do not block Component deletion if failed to delete image repository
				}
				if isRepoDeleted {
					log.Info(fmt.Sprintf("Deleted image repository %s", imageRepo), l.Action, l.ActionDelete)
				}
			}

			if err := r.Client.Get(ctx, req.NamespacedName, component); err != nil {
				log.Error(err, "failed to get Component", l.Action, l.ActionView)
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(component, ImageRepositoryFinalizer)
			if err := r.Client.Update(ctx, component); err != nil {
				log.Error(err, "failed to remove image repository finalizer", l.Action, l.ActionUpdate)
				return ctrl.Result{}, err
			}
			log.Info("Image repository finalizer removed from the Component", l.Action, l.ActionDelete)
		}

		return ctrl.Result{}, nil
	}

	generateRepositoryOptsStr, exists := component.Annotations[GenerateImageAnnotationName]
	if !exists {
		// Nothing to do
		return ctrl.Result{}, nil
	}

	// This is workaround for Application Service that doesn't properly handle component updates
	// while initial operations with the Component are in progress.
	if component.Status.Devfile == "" {
		// The Component has been just created.
		// Component controller (from Application Service) must set devfile model, wait for it.
		log.Info("Waiting for devfile model in component")
		// Do not requeue as after model update a new update event will trigger a new reconcile
		return ctrl.Result{}, nil
	}

	// Read repository options from the annotations
	requestRepositoryOpts := &GenerateRepositoryOpts{}
	if err := json.Unmarshal([]byte(generateRepositoryOptsStr), requestRepositoryOpts); err != nil {
		// Check "true" value for backward compatibility
		if generateRepositoryOptsStr == "true" {
			requestRepositoryOpts.Visibility = "public"
		} else {
			message := fmt.Sprintf("invalid JSON in %s annotation", GenerateImageAnnotationName)
			return ctrl.Result{}, r.reportError(ctx, component, message)
		}
	}

	// Validate image repository creation options
	if !(requestRepositoryOpts.Visibility == "public" || requestRepositoryOpts.Visibility == "private") {
		message := fmt.Sprintf("invalid value: %s in visibility field in %s annotation", requestRepositoryOpts.Visibility, GenerateImageAnnotationName)
		return ctrl.Result{}, r.reportError(ctx, component, message)
	}

	repositoryInfo := ImageRepositoryStatus{}
	if imageRepoInfo, imageRepoExist := component.Annotations[ImageAnnotationName]; imageRepoExist {
		// Image repository exists
		if err := json.Unmarshal([]byte(imageRepoInfo), &repositoryInfo); err == nil {
			if repositoryInfo.Visibility != requestRepositoryOpts.Visibility {
				// quay.io/org/reposito/ryName
				imageUrlParts := strings.SplitN(repositoryInfo.Image, "/", 3)
				if len(imageUrlParts) > 2 {
					repositoryName := imageUrlParts[2]
					if err := r.QuayClient.ChangeRepositoryVisibility(r.QuayOrganization, repositoryName, requestRepositoryOpts.Visibility); err == nil {
						repositoryInfo.Visibility = requestRepositoryOpts.Visibility
					} else {
						if err.Error() == "payment required" {
							repositoryInfo.Message = "Quay organization plan doesn't allow private image repositories"
						} else {
							log.Error(err, "failed to change image repository visibility")
							return ctrl.Result{}, err
						}
					}
				} else {
					// Invalid image url
					repositoryInfo.Message = "Invalid image url"
				}
			}
		} else {
			// Image repository info annotation contains invalid JSON.
			// This means that the annotation was edited manually.
			repositoryInfo.Message = "Invalid image status annotation"
		}
	} else {
		// Image repository doesn't exist, create it.
		repo, robotAccount, err := r.generateImageRepository(ctx, component, requestRepositoryOpts)
		if err != nil {
			log.Error(err, "Error in the repository generation process", l.Audit, "true")
			return ctrl.Result{}, r.reportError(ctx, component, "failed to generete image repository")
		}
		if repo == nil || robotAccount == nil {
			log.Error(nil, "Unknown error in the repository generation process", l.Audit, "true")
			return ctrl.Result{}, r.reportError(ctx, component, "failed to generete image repository: unknown error")
		}

		// Create secret with the repository credentials
		imageURL := fmt.Sprintf("quay.io/%s/%s", r.QuayOrganization, repo.Name)
		robotAccountSecret := generateSecret(component, robotAccount, imageURL)

		robotAccountSecretKey := types.NamespacedName{Namespace: robotAccountSecret.Namespace, Name: robotAccountSecret.Name}
		existingRobotAccountSecret := &corev1.Secret{}
		if err := r.Client.Get(ctx, robotAccountSecretKey, existingRobotAccountSecret); err == nil {
			if err := r.Client.Delete(ctx, existingRobotAccountSecret); err != nil {
				log.Error(err, fmt.Sprintf("failed to delete robot account secret %v", robotAccountSecretKey), l.Action, l.ActionDelete)
				return ctrl.Result{}, err
			} else {
				log.Info(fmt.Sprintf("Deleted old robot account secret %v", robotAccountSecretKey), l.Action, l.ActionDelete)
			}
		} else if !errors.IsNotFound(err) {
			log.Error(err, fmt.Sprintf("failed to read robot account secret %v", robotAccountSecretKey), l.Action, l.ActionView)
			return ctrl.Result{}, err
		}

		if err := r.Client.Create(ctx, &robotAccountSecret); err != nil {
			log.Error(err, fmt.Sprintf("error writing robot account token into Secret: %v", robotAccountSecretKey), l.Action, l.ActionAdd)
			return ctrl.Result{}, err
		}
		log.Info(fmt.Sprintf("Created image registry secret %s for Component", robotAccountSecretKey.Name), l.Action, l.ActionAdd)

		// Prepare data to update the component with
		repositoryInfo = ImageRepositoryStatus{
			Image:      imageURL,
			Visibility: requestRepositoryOpts.Visibility,
			Secret:     robotAccountSecret.Name,
		}
	}
	repositoryInfoBytes, _ := json.Marshal(repositoryInfo)

	// Update component with the generated data and add finalizer
	err = r.Client.Get(ctx, req.NamespacedName, component)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error updating the Component's annotations: %w", err)
	}
	if component.ObjectMeta.DeletionTimestamp.IsZero() {
		component.Annotations[ImageAnnotationName] = string(repositoryInfoBytes)
		delete(component.Annotations, GenerateImageAnnotationName)

		if !controllerutil.ContainsFinalizer(component, ImageRepositoryFinalizer) {
			controllerutil.AddFinalizer(component, ImageRepositoryFinalizer)
			log.Info("Image repository finalizer added to the Component update", l.Action, l.ActionUpdate)
		}

		if err := r.Client.Update(ctx, component); err != nil {
			return ctrl.Result{}, fmt.Errorf("error updating the component: %w", err)
		}
		log.Info("Component updated successfully", l.Action, l.ActionUpdate)

		// Here we do some trick.
		// The problem is that the component update triggers both: a new reconcile and operator cache update.
		// In other words we are getting race condition. If a new reconcile is triggered before cache update,
		// requested build action will be repeated, because the last update has not yet visible for the operator.
		// For example, instead of one initial pipeline run we could get two.
		// To resolve the problem above, instead of just ending the reconcile loop here,
		// we are waiting for the cache update. This approach prevents next reconciles with outdated cache.
		isComponentInCacheUpToDate := false
		for i := 0; i < 5; i++ {
			if err = r.Client.Get(ctx, req.NamespacedName, component); err == nil {
				if component.Annotations[GenerateImageAnnotationName] == "false" {
					isComponentInCacheUpToDate = true
					break
				}
				// Outdated version of the component, wait more.
			} else {
				if errors.IsNotFound(err) {
					// The component was deleted
					isComponentInCacheUpToDate = true
					break
				}
				log.Error(err, "failed to get the component for annotation update check", l.Action, l.ActionView)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !isComponentInCacheUpToDate {
			log.Info("failed to wait for updated cache. Requested action could be repeated.", l.Audit, "true")
		}
	}

	return ctrl.Result{}, nil
}

func (r *ComponentReconciler) reportError(ctx context.Context, component *appstudioredhatcomv1alpha1.Component, messsage string) error {
	lookUpKey := types.NamespacedName{Name: component.Name, Namespace: component.Namespace}
	if err := r.Client.Get(ctx, lookUpKey, component); err != nil {
		return err
	}
	messageBytes, _ := json.Marshal(&ImageRepositoryStatus{Message: messsage})
	component.Annotations[ImageAnnotationName] = string(messageBytes)
	delete(component.Annotations, GenerateImageAnnotationName)
	return r.Client.Update(ctx, component)
}

func generateRobotAccountName(component *appstudioredhatcomv1alpha1.Component) string {
	//TODO: replace component.Namespace with the name of the Space
	return component.Namespace + component.Spec.Application + component.Name
}

func generateRepositoryName(component *appstudioredhatcomv1alpha1.Component) string {
	return component.Namespace + "/" + component.Spec.Application + "/" + component.Name
}

func (r *ComponentReconciler) generateImageRepository(ctx context.Context, component *appstudioredhatcomv1alpha1.Component, opts *GenerateRepositoryOpts) (*quay.Repository, *quay.RobotAccount, error) {
	log := ctrllog.FromContext(ctx)

	imageRepositoryName := generateRepositoryName(component)
	repo, err := r.QuayClient.CreateRepository(quay.RepositoryRequest{
		Namespace:   r.QuayOrganization,
		Visibility:  opts.Visibility,
		Description: "AppStudio repository for the user",
		Repository:  imageRepositoryName,
	})
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to create image repository %s", imageRepositoryName), l.Action, l.ActionAdd, l.Audit, "true")
		return nil, nil, err
	}

	robotAccountName := generateRobotAccountName(component)
	robotAccount, err := r.QuayClient.CreateRobotAccount(r.QuayOrganization, robotAccountName)
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to create robot account %s", robotAccountName), l.Action, l.ActionAdd, l.Audit, "true")
		return nil, nil, err
	}

	err = r.QuayClient.AddWritePermissionsToRobotAccount(r.QuayOrganization, repo.Name, robotAccount.Name)
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to add permissions to robot account %s", robotAccountName), l.Action, l.ActionUpdate, l.Audit, "true")
		return nil, nil, err
	}

	return repo, robotAccount, nil
}

// generateSecret dumps the robot account token into a Secret for future consumption.
func generateSecret(c *appstudioredhatcomv1alpha1.Component, r *quay.RobotAccount, quayImageURL string) corev1.Secret {
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.Name,
			Namespace: c.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					Name:       c.Name,
					APIVersion: c.APIVersion,
					Kind:       c.Kind,
					UID:        c.UID,
				},
			},
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}

	secretData := map[string]string{}
	authString := fmt.Sprintf("%s:%s", r.Name, r.Token)
	secretData[corev1.DockerConfigJsonKey] = fmt.Sprintf(`{"auths":{"%s":{"auth":"%s"}}}`,
		quayImageURL,
		base64.StdEncoding.EncodeToString([]byte(authString)),
	)

	secret.StringData = secretData
	return secret
}
