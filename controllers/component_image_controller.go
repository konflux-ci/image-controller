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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/go-logr/logr"
	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
)

const (
	ImageAnnotationName         = "image.redhat.com/image"
	GenerateImageAnnotationName = "image.redhat.com/generate"

	ImageRepositoryFinalizer = "image-repository.component.appstudio.openshift.io/finalizer"
)

// RepositoryInfo defines the structure of the Repository information being exposed to external systems.
type RepositoryInfo struct {
	Image  string `json:"image"`
	Secret string `json:"secret"`
}

// ComponentReconciler reconciles a Controller object
type ComponentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger

	QuayClient       *quay.QuayClient
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
	log := r.Log.WithValues("Component", req.NamespacedName)

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
				log.Error(err, "failed to delete robot account")
				// Do not block Component deletion if failed to delete robot account
			}
			if isDeleted {
				log.Info(fmt.Sprintf("Deleted robot account %s", robotAccountName))
			}

			if err := r.Client.Get(ctx, req.NamespacedName, component); err != nil {
				log.Error(err, "failed to get Component")
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(component, ImageRepositoryFinalizer)
			if err := r.Client.Update(ctx, component); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Image repository finalizer removed from the Component")
		}

		return ctrl.Result{}, nil
	}

	if !shouldGenerateImage(component.Annotations) {
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

	repo, robotAccount, err := generateImageRepository(*component, r.QuayOrganization, *r.QuayClient)
	if err != nil {
		r.reportError(ctx, component)
		log.Error(err, "Error in the repository generation process")
		return ctrl.Result{}, nil
	}
	if repo == nil || robotAccount == nil {
		r.reportError(ctx, component)
		log.Error(err, "Unknown error in the repository generation process")
		return ctrl.Result{}, nil
	}

	// Create secret with the reposuitory credentials
	imageURL := fmt.Sprintf("quay.io/%s/%s", r.QuayOrganization, repo.Name)
	robotAccountSecret := generateSecret(*component, *robotAccount, imageURL)

	robotAccountSecretKey := types.NamespacedName{Namespace: robotAccountSecret.Namespace, Name: robotAccountSecret.Name}
	existingRobotAccountSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, robotAccountSecretKey, existingRobotAccountSecret); err == nil {
		if err := r.Client.Delete(ctx, existingRobotAccountSecret); err != nil {
			log.Error(err, fmt.Sprintf("failed to delete robot account secret %v", robotAccountSecretKey))
			return ctrl.Result{}, err
		}
	} else if !errors.IsNotFound(err) {
		log.Error(err, fmt.Sprintf("failed to read robot account secret %v", robotAccountSecretKey))
		return ctrl.Result{}, err
	}

	if err := r.Client.Create(ctx, &robotAccountSecret); err != nil {
		log.Error(err, fmt.Sprintf("error writing robot account token into Secret: %v", robotAccountSecretKey))
		return ctrl.Result{}, err
	}

	// Prepare data to update the component with
	generatedRepository := RepositoryInfo{
		Image:  fmt.Sprintf("quay.io/%s/%s", r.QuayOrganization, repo.Name),
		Secret: robotAccountSecret.Name,
	}
	generatedRepositoryBytes, _ := json.Marshal(generatedRepository)

	// Update component with the generated data and add finalizer
	err = r.Client.Get(ctx, req.NamespacedName, component)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error updating the Component's annotations: %w", err)
	}
	if component.ObjectMeta.DeletionTimestamp.IsZero() {
		component.Annotations[ImageAnnotationName] = string(generatedRepositoryBytes)
		component.Annotations[GenerateImageAnnotationName] = "false"

		if !controllerutil.ContainsFinalizer(component, ImageRepositoryFinalizer) {
			controllerutil.AddFinalizer(component, ImageRepositoryFinalizer)
		}

		if err := r.Client.Update(ctx, component); err != nil {
			return ctrl.Result{}, fmt.Errorf("error updating the component: %w", err)
		}
		log.Info("Image regipository finaliziler added to the Component")
		log.Info("Component updated successfully")
	}

	return ctrl.Result{}, nil
}

func (r *ComponentReconciler) reportError(ctx context.Context, component *appstudioredhatcomv1alpha1.Component) error {
	lookUpKey := types.NamespacedName{Name: component.Name, Namespace: component.Namespace}
	if err := r.Client.Get(ctx, lookUpKey, component); err != nil {
		return err
	}
	component.Annotations[GenerateImageAnnotationName] = "failed"
	return r.Client.Update(ctx, component)
}

func shouldGenerateImage(annotations map[string]string) bool {
	if generate, present := annotations[GenerateImageAnnotationName]; present && generate == "true" {
		return true
	}
	return false
}

// generateSecret dumps the robot account token into a Secret for future consumption.
func generateSecret(c appstudioredhatcomv1alpha1.Component, r quay.RobotAccount, quayImageURL string) corev1.Secret {
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
