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

	"github.com/go-logr/logr"
	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	l "github.com/redhat-appstudio/image-controller/pkg/logs"
	"github.com/redhat-appstudio/image-controller/pkg/metrics"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
)

const (
	ImageAnnotationName         = "image.redhat.com/image"
	GenerateImageAnnotationName = "image.redhat.com/generate"

	ImageRepositoryComponentFinalizer = "image-controller.appstudio.openshift.io/image-repository"

	ApplicationNameLabelName = "appstudio.redhat.com/application"
	ComponentNameLabelName   = "appstudio.redhat.com/component"
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

	BuildQuayClient  func(logr.Logger) quay.QuayService
	QuayOrganization string
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appstudioredhatcomv1alpha1.Component{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=remotesecrets,verbs=get;list;watch;create
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ComponentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithName("ComponentImageRepository")
	ctx = ctrllog.IntoContext(ctx, log)
	reconcileStartTime := time.Now()

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

	componentIdForMetrics := getComponentIdForMetrics(component)

	if !component.ObjectMeta.DeletionTimestamp.IsZero() {
		// remove component from metrics map
		delete(metrics.RepositoryTimesForMetrics, componentIdForMetrics)

		if controllerutil.ContainsFinalizer(component, ImageRepositoryComponentFinalizer) {
			pushRobotAccountName, pullRobotAccountName := generateRobotAccountsNames(component)

			quayClient := r.BuildQuayClient(log)
			isPushRobotAccountDeleted, err := quayClient.DeleteRobotAccount(r.QuayOrganization, pushRobotAccountName)
			if err != nil {
				log.Error(err, "failed to delete push robot account", l.Action, l.ActionDelete, l.Audit, "true")
				// Do not block Component deletion if failed to delete robot account
			}
			if isPushRobotAccountDeleted {
				log.Info(fmt.Sprintf("Deleted push robot account %s", pushRobotAccountName), l.Action, l.ActionDelete)
			}

			isPullRobotAccountDeleted, err := quayClient.DeleteRobotAccount(r.QuayOrganization, pullRobotAccountName)
			if err != nil {
				log.Error(err, "failed to delete pull robot account", l.Action, l.ActionDelete, l.Audit, "true")
				// Do not block Component deletion if failed to delete robot account
			}
			if isPullRobotAccountDeleted {
				log.Info(fmt.Sprintf("Deleted pull robot account %s", pullRobotAccountName), l.Action, l.ActionDelete)
			}

			imageRepo := generateRepositoryName(component)
			isRepoDeleted, err := quayClient.DeleteRepository(r.QuayOrganization, imageRepo)
			if err != nil {
				log.Error(err, "failed to delete image repository", l.Action, l.ActionDelete, l.Audit, "true")
				// Do not block Component deletion if failed to delete image repository
			}
			if isRepoDeleted {
				log.Info(fmt.Sprintf("Deleted image repository %s", imageRepo), l.Action, l.ActionDelete)
			}

			if err := r.Client.Get(ctx, req.NamespacedName, component); err != nil {
				log.Error(err, "failed to get Component", l.Action, l.ActionView)
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(component, ImageRepositoryComponentFinalizer)
			if err := r.Client.Update(ctx, component); err != nil {
				log.Error(err, "failed to remove image repository finalizer", l.Action, l.ActionUpdate)
				return ctrl.Result{}, err
			}
			log.Info("Image repository finalizer removed from the Component", l.Action, l.ActionDelete)

			r.waitComponentUpdateInCache(ctx, req.NamespacedName, func(component *appstudioredhatcomv1alpha1.Component) bool {
				return !controllerutil.ContainsFinalizer(component, ImageRepositoryComponentFinalizer)
			})
		}

		return ctrl.Result{}, nil
	}

	generateRepositoryOptsStr, exists := component.Annotations[GenerateImageAnnotationName]
	if !exists {
		// Nothing to do
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

	setMetricsTime(componentIdForMetrics, reconcileStartTime)

	imageRepositoryExists := false
	repositoryInfo := ImageRepositoryStatus{}
	repositoryInfoStr, imageAnnotationExist := component.Annotations[ImageAnnotationName]
	if imageAnnotationExist {
		if err := json.Unmarshal([]byte(repositoryInfoStr), &repositoryInfo); err == nil {
			imageRepositoryExists = repositoryInfo.Image != "" && repositoryInfo.Secret != ""
			repositoryInfo.Message = ""
		} else {
			// Image repository info annotation contains invalid JSON.
			// This means that the annotation was edited manually.
			repositoryInfo.Message = "Invalid image status annotation"
		}
	}

	// Do something only if no error has been detected before
	if repositoryInfo.Message == "" {
		if imageRepositoryExists {
			// Check if need to change image repository  visibility
			if repositoryInfo.Visibility != requestRepositoryOpts.Visibility {
				// quay.io/org/reposito/ryName
				imageUrlParts := strings.SplitN(repositoryInfo.Image, "/", 3)
				if len(imageUrlParts) > 2 {
					repositoryName := imageUrlParts[2]
					quayClient := r.BuildQuayClient(log)
					if err := quayClient.ChangeRepositoryVisibility(r.QuayOrganization, repositoryName, requestRepositoryOpts.Visibility); err == nil {
						repositoryInfo.Visibility = requestRepositoryOpts.Visibility
					} else {
						if err.Error() == "payment required" {
							log.Info("failed to make image repository private due to quay plan limit", l.Audit, "true")
							repositoryInfo.Message = "Quay organization plan doesn't allow private image repositories"
						} else {
							log.Error(err, "failed to change image repository visibility")
							return ctrl.Result{}, err
						}
					}
				} else {
					repositoryInfo.Message = "Invalid image url"
				}
			}
		} else {
			// Image repository doesn't exist, create it.
			quayClient := r.BuildQuayClient(log)
			repo, pushRobotAccount, pullRobotAccount, err := r.generateImageRepository(ctx, quayClient, component, requestRepositoryOpts)
			if err != nil {
				if err.Error() == "payment required" {
					log.Info("failed to create private image repository due to quay plan limit", l.Audit, "true")
					repositoryInfo.Message = "Quay organization plan doesn't allow private image repositories"
				} else {
					log.Error(err, "Error in the repository generation process", l.Audit, "true")
					return ctrl.Result{}, r.reportError(ctx, component, "failed to generate image repository")
				}
			} else {
				if repo == nil || pushRobotAccount == nil || pullRobotAccount == nil {
					log.Error(nil, "Unknown error in the repository generation process", l.Audit, "true")
					return ctrl.Result{}, r.reportError(ctx, component, "failed to generate image repository: unknown error")
				}
				log.Info(fmt.Sprintf("Prepared image repository %s for Component", repo.Name), l.Action, l.ActionAdd)

				imageURL := fmt.Sprintf("quay.io/%s/%s", r.QuayOrganization, repo.Name)

				// Create secrets with the repository credentials
				pushSecretName := component.Name
				_, err := r.ensureRobotAccountSecret(ctx, component, pushRobotAccount, pushSecretName, imageURL)
				if err != nil {
					return ctrl.Result{}, err
				}
				log.Info(fmt.Sprintf("Prepared image registry push secret %s for Component", pushRobotAccount.Name), l.Action, l.ActionUpdate)

				// Propagate the pull secret into all environments
				pullSecretName := pushSecretName + "-pull"
				if err := r.ensureComponentPullSecret(ctx, component, pullSecretName, pullRobotAccount, imageURL); err != nil {
					return ctrl.Result{}, err
				}
				log.Info(fmt.Sprintf("Prepared remote secret %s for Component", pullSecretName), l.Action, l.ActionUpdate)

				// Prepare data to update the component with
				repositoryInfo = ImageRepositoryStatus{
					Image:      imageURL,
					Visibility: requestRepositoryOpts.Visibility,
					Secret:     pushSecretName,
				}
			}
		}
	}
	repositoryInfoBytes, _ := json.Marshal(repositoryInfo)

	// Update component with the generated data and add finalizer
	err = r.Client.Get(ctx, req.NamespacedName, component)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error reading component: %w", err)
	}
	if component.ObjectMeta.DeletionTimestamp.IsZero() {
		component.Annotations[ImageAnnotationName] = string(repositoryInfoBytes)
		delete(component.Annotations, GenerateImageAnnotationName)

		if repositoryInfo.Image != "" && !controllerutil.ContainsFinalizer(component, ImageRepositoryComponentFinalizer) {
			controllerutil.AddFinalizer(component, ImageRepositoryComponentFinalizer)
			log.Info("Image repository finalizer added to the Component update", l.Action, l.ActionUpdate)
		}

		if err := r.Client.Update(ctx, component); err != nil {
			return ctrl.Result{}, fmt.Errorf("error updating the component: %w", err)
		}
		log.Info("Component updated successfully", l.Action, l.ActionUpdate)

		r.waitComponentUpdateInCache(ctx, req.NamespacedName, func(component *appstudioredhatcomv1alpha1.Component) bool {
			_, exists := component.Annotations[GenerateImageAnnotationName]
			return !exists
		})
	}

	metrics.ImageRepositoryProvisionTimeMetric.Observe(time.Since(metrics.RepositoryTimesForMetrics[componentIdForMetrics]).Seconds())
	// remove component from metrics map
	delete(metrics.RepositoryTimesForMetrics, componentIdForMetrics)

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

	componentIdForMetrics := getComponentIdForMetrics(component)
	// remove component from metrics map, permanent error
	delete(metrics.RepositoryTimesForMetrics, componentIdForMetrics)

	return r.Client.Update(ctx, component)
}

// waitComponentUpdateInCache waits for operator cache update with newer version of the component.
// Here we do some trick.
// The problem is that the component update triggers both: a new reconcile and operator cache update.
// In other words we are getting race condition. If a new reconcile is triggered before cache update,
// requested build action will be repeated, because the last update has not yet visible for the operator.
// For example, instead of one initial pipeline run we could get two.
// To resolve the problem above, instead of just ending the reconcile loop here,
// we are waiting for the cache update. This approach prevents next reconciles with outdated cache.
func (r *ComponentReconciler) waitComponentUpdateInCache(ctx context.Context, componentKey types.NamespacedName, componentUpdated func(component *appstudioredhatcomv1alpha1.Component) bool) {
	log := ctrllog.FromContext(ctx).WithName("waitComponentUpdateInCache")

	component := &appstudioredhatcomv1alpha1.Component{}
	isComponentInCacheUpToDate := false
	for i := 0; i < 10; i++ {
		if err := r.Client.Get(ctx, componentKey, component); err == nil {
			if componentUpdated(component) {
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

// ensureRobotAccountSecret creates or updates robot account secret.
// Returns secret string data.
func (r *ComponentReconciler) ensureRobotAccountSecret(ctx context.Context, component *appstudioredhatcomv1alpha1.Component, robotAccount *quay.RobotAccount, secretName, imageURL string) (map[string]string, error) {
	log := ctrllog.FromContext(ctx)

	robotAccountSecret := generateSecret(component, robotAccount, secretName, imageURL)
	secretData := robotAccountSecret.StringData

	robotAccountSecretKey := types.NamespacedName{Namespace: robotAccountSecret.Namespace, Name: robotAccountSecret.Name}
	existingRobotAccountSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, robotAccountSecretKey, existingRobotAccountSecret); err == nil {
		existingRobotAccountSecret.StringData = secretData
		if err := r.Client.Update(ctx, existingRobotAccountSecret); err != nil {
			log.Error(err, fmt.Sprintf("failed to update robot account secret %v", robotAccountSecretKey), l.Action, l.ActionUpdate)
			return nil, err
		}
	} else {
		if !errors.IsNotFound(err) {
			log.Error(err, fmt.Sprintf("failed to read robot account secret %v", robotAccountSecretKey), l.Action, l.ActionView)
			return nil, err
		}
		if err := r.Client.Create(ctx, robotAccountSecret); err != nil {
			log.Error(err, fmt.Sprintf("error writing robot account token into Secret: %v", robotAccountSecretKey), l.Action, l.ActionAdd)
			return nil, err
		}
	}

	return secretData, nil
}

// ensureComponentPullSecret creates secret for component image repository pull token.
func (r *ComponentReconciler) ensureComponentPullSecret(ctx context.Context, component *appstudioredhatcomv1alpha1.Component, secretName string, robotAccount *quay.RobotAccount, imageURL string) error {
	log := ctrllog.FromContext(ctx)

	pullSecret := &corev1.Secret{}
	pullSecretKey := types.NamespacedName{Namespace: component.Namespace, Name: secretName}
	if err := r.Client.Get(ctx, pullSecretKey, pullSecret); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, fmt.Sprintf("failed to get pull secret: %v", pullSecretKey), l.Action, l.ActionView)
			return err
		}

		pullSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: component.Namespace,
				Labels: map[string]string{
					InternalSecretLabelName: "true",
				},
			},
			Type:       corev1.SecretTypeDockerConfigJson,
			StringData: generateDockerconfigSecretData(imageURL, robotAccount),
		}

		if err := controllerutil.SetOwnerReference(component, pullSecret, r.Scheme); err != nil {
			log.Error(err, "failed to set owner for pull secret")
			return err
		}

		if err := r.Client.Create(ctx, pullSecret); err != nil {
			log.Error(err, fmt.Sprintf("failed to create pull secret: %v", pullSecretKey), l.Action, l.ActionAdd, l.Audit, "true")
			return err
		}

	}
	return nil
}

// generateRobotAccountsNames returns push and pull robot account names for the given Component
func generateRobotAccountsNames(component *appstudioredhatcomv1alpha1.Component) (string, string) {
	pushRobotAccountName := component.Namespace + component.Spec.Application + component.Name
	pullRobotAccountName := pushRobotAccountName + "-pull"
	return pushRobotAccountName, pullRobotAccountName
}

func generateRepositoryName(component *appstudioredhatcomv1alpha1.Component) string {
	return component.Namespace + "/" + component.Spec.Application + "/" + component.Name
}

func (r *ComponentReconciler) generateImageRepository(
	ctx context.Context,
	quayClient quay.QuayService,
	component *appstudioredhatcomv1alpha1.Component,
	opts *GenerateRepositoryOpts,
) (
	*quay.Repository,
	*quay.RobotAccount,
	*quay.RobotAccount,
	error,
) {
	log := ctrllog.FromContext(ctx)

	imageRepositoryName := generateRepositoryName(component)
	repo, err := quayClient.CreateRepository(quay.RepositoryRequest{
		Namespace:   r.QuayOrganization,
		Visibility:  opts.Visibility,
		Description: "AppStudio repository for the user",
		Repository:  imageRepositoryName,
	})
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to create image repository %s", imageRepositoryName), l.Action, l.ActionAdd, l.Audit, "true")
		return nil, nil, nil, err
	}

	pushRobotAccountName, pullRobotAccountName := generateRobotAccountsNames(component)

	pushRobotAccount, err := quayClient.CreateRobotAccount(r.QuayOrganization, pushRobotAccountName)
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to create robot account %s", pushRobotAccountName), l.Action, l.ActionAdd, l.Audit, "true")
		return nil, nil, nil, err
	}
	err = quayClient.AddPermissionsForRepositoryToRobotAccount(r.QuayOrganization, repo.Name, pushRobotAccount.Name, true)
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to add permissions to robot account %s", pushRobotAccount.Name), l.Action, l.ActionUpdate, l.Audit, "true")
		return nil, nil, nil, err
	}

	pullRobotAccount, err := quayClient.CreateRobotAccount(r.QuayOrganization, pullRobotAccountName)
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to create robot account %s", pullRobotAccountName), l.Action, l.ActionAdd, l.Audit, "true")
		return nil, nil, nil, err
	}
	err = quayClient.AddPermissionsForRepositoryToRobotAccount(r.QuayOrganization, repo.Name, pullRobotAccount.Name, false)
	if err != nil {
		log.Error(err, fmt.Sprintf("failed to add permissions to robot account %s", pullRobotAccount.Name), l.Action, l.ActionUpdate, l.Audit, "true")
		return nil, nil, nil, err
	}

	return repo, pushRobotAccount, pullRobotAccount, nil
}

// generateSecret dumps the robot account token into a Secret for future consumption.
func generateSecret(c *appstudioredhatcomv1alpha1.Component, robotAccount *quay.RobotAccount, secretName, quayImageURL string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
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
		Type:       corev1.SecretTypeDockerConfigJson,
		StringData: generateDockerconfigSecretData(quayImageURL, robotAccount),
	}
}

func generateDockerconfigSecretData(quayImageURL string, robotAccount *quay.RobotAccount) map[string]string {
	secretData := map[string]string{}
	authString := fmt.Sprintf("%s:%s", robotAccount.Name, robotAccount.Token)
	secretData[corev1.DockerConfigJsonKey] = fmt.Sprintf(`{"auths":{"%s":{"auth":"%s"}}}`,
		quayImageURL, base64.StdEncoding.EncodeToString([]byte(authString)))
	return secretData
}

func getComponentIdForMetrics(component *appstudioredhatcomv1alpha1.Component) string {
	return component.Name + "=" + component.Namespace
}
