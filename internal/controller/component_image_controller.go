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
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	applicationapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	imagerepositoryv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	l "github.com/konflux-ci/image-controller/pkg/logs"
	"github.com/konflux-ci/image-controller/pkg/quay"
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
		For(&applicationapiv1alpha1.Component{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ComponentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithName("ComponentImageRepository")
	ctx = ctrllog.IntoContext(ctx, log)

	// Fetch the Component instance
	component := &applicationapiv1alpha1.Component{}
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
		if controllerutil.ContainsFinalizer(component, ImageRepositoryComponentFinalizer) {
			controllerutil.RemoveFinalizer(component, ImageRepositoryComponentFinalizer)
			if err := r.Client.Update(ctx, component); err != nil {
				log.Error(err, "failed to remove image repository finalizer", l.Action, l.ActionUpdate, "componentName", component.Name)
				return ctrl.Result{}, err
			}
			log.Info("Image repository finalizer removed from the Component", l.Action, l.ActionDelete, "componentName", component.Name)

			r.waitComponentUpdateInCache(ctx, req.NamespacedName, func(component *applicationapiv1alpha1.Component) bool {
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

	// Search if imageRepository for the component exists already
	imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
	if err := r.Client.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: component.Namespace}); err != nil {
		log.Error(err, "failed to list image repositories")
		return ctrl.Result{}, err
	}

	imageRepositoryFound := ""
	for _, imageRepository := range imageRepositoriesList.Items {
		for _, owner := range imageRepository.ObjectMeta.OwnerReferences {
			if owner.UID == component.UID {
				imageRepositoryFound = imageRepository.Name
				break
			}
		}
	}

	if imageRepositoryFound == "" {
		imageRepositoryName := ""
		if component.Spec.Application != "" {
			imageRepositoryName = fmt.Sprintf("imagerepository-for-%s-%s", component.Spec.Application, component.Name)
		} else {
			imageRepositoryName = fmt.Sprintf("imagerepository-for-%s", component.Name)
		}
		log.Info("Will create image repository", "ImageRepositoryName", imageRepositoryName, "ComponentName", component.Name)

		imageRepository := &imagerepositoryv1alpha1.ImageRepository{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ImageRepository",
				APIVersion: "pipelinesascode.tekton.dev/v1alpha1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      imageRepositoryName,
				Namespace: component.Namespace,
				Labels: map[string]string{
					ComponentNameLabelName: component.Name,
				},
				Annotations: map[string]string{
					updateComponentAnnotationName: "true",
				},
			},
			Spec: imagerepositoryv1alpha1.ImageRepositorySpec{
				Image: imagerepositoryv1alpha1.ImageParameters{
					Visibility: imagerepositoryv1alpha1.ImageVisibility(requestRepositoryOpts.Visibility),
				},
			},
		}

		if component.Spec.Application != "" {
			imageRepository.ObjectMeta.Labels[ApplicationNameLabelName] = component.Spec.Application
		}

		if err := r.Client.Create(ctx, imageRepository); err != nil {
			log.Error(err, "failed to create image repository", "ImageRepositoryName", imageRepositoryName, "ComponentName", component.Name)
			return ctrl.Result{}, err
		}
		log.Info("Image repository created", "ImageRepositoryName", imageRepositoryName, "ComponentName", component.Name)
	} else {
		log.Info("Image repository already exists", "ImageRepositoryName", imageRepositoryFound, "ComponentName", component.Name)
	}

	err = r.Client.Get(ctx, req.NamespacedName, component)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error reading component: %w", err)
	}
	delete(component.Annotations, GenerateImageAnnotationName)

	if err := r.Client.Update(ctx, component); err != nil {
		log.Error(err, "failed to update Component after 'generate' annotation removal", "ComponentName", component.Name)
		return ctrl.Result{}, fmt.Errorf("error updating the component: %w", err)
	}
	log.Info("Component updated successfully, 'generate' annotation removed", "ComponentName", component.Name)

	r.waitComponentUpdateInCache(ctx, req.NamespacedName, func(component *applicationapiv1alpha1.Component) bool {
		_, exists := component.Annotations[GenerateImageAnnotationName]
		return !exists
	})

	return ctrl.Result{}, nil
}

func (r *ComponentReconciler) reportError(ctx context.Context, component *applicationapiv1alpha1.Component, messsage string) error {
	lookUpKey := types.NamespacedName{Name: component.Name, Namespace: component.Namespace}
	if err := r.Client.Get(ctx, lookUpKey, component); err != nil {
		return err
	}
	messageBytes, _ := json.Marshal(&ImageRepositoryStatus{Message: messsage})
	component.Annotations[ImageAnnotationName] = string(messageBytes)
	delete(component.Annotations, GenerateImageAnnotationName)

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
func (r *ComponentReconciler) waitComponentUpdateInCache(ctx context.Context, componentKey types.NamespacedName, componentUpdated func(component *applicationapiv1alpha1.Component) bool) {
	log := ctrllog.FromContext(ctx).WithName("waitComponentUpdateInCache")

	component := &applicationapiv1alpha1.Component{}
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
