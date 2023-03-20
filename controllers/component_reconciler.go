/*
Copyright 2023.

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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
)

// ComponentReconciler reconciles a Controller object
type ComponentReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	QuayClient       *quay.QuayClient
	QuayOrganization string
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Controller object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *ComponentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

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
		return ctrl.Result{}, fmt.Errorf("Error reading the object - requeue the request : %w", err)
	}

	if component.Status.Devfile == "" {
		// The Component has been just created.
		// Component controller (from Application Service) must set devfile model, wait for it.
		log.Log.Info("Waiting for devfile model in component")
		// Do not requeue as after model update a new update event will trigger a new reconcile
		return ctrl.Result{}, nil
	}

	if !shouldGenerateImage(component.Annotations) {
		return ctrl.Result{}, nil
	}

	repo, robot, err := generateImageRepository(*component, r.QuayOrganization, *r.QuayClient)

	if err != nil {
		r.reportError(ctx, component)
		log.Log.Error(err, "Error in the repository generation process ")
		return ctrl.Result{}, nil
	}
	if repo == nil || robot == nil {
		r.reportError(ctx, component)
		log.Log.Error(err, "Unknown error in the repository generation process ")
		return ctrl.Result{}, nil

	}

	imageURL := fmt.Sprintf("quay.io/%s/%s", r.QuayOrganization, repo.Name)
	robotAccountSecret := generateSecret(*component, *robot, imageURL)

	err = r.Client.Create(ctx, &robotAccountSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("Error writing robot account token into a Secret - requeue the request - %w", err)
	}

	generatedRepository := RepositoryInfo{
		Image:  fmt.Sprintf("quay.io/%s/%s", r.QuayOrganization, repo.Name),
		Secret: robotAccountSecret.Name,
	}
	generatedRepositoryBytes, _ := json.Marshal(generatedRepository)

	lookUpKey := types.NamespacedName{Name: component.Name, Namespace: component.Namespace}
	err = r.Client.Get(ctx, lookUpKey, component)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("Error updating the Component's annotations - %w", err)
	}

	component.Annotations["image.redhat.com/image"] = string(generatedRepositoryBytes)
	component.Annotations["image.redhat.com/generate"] = "false"

	if err := r.Client.Update(ctx, component); err != nil {
		return ctrl.Result{}, fmt.Errorf("Error updating the component annotations - requeue the request : %w", err)
	}

	return ctrl.Result{}, nil
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

	ret := map[string]string{}
	ret[corev1.DockerConfigJsonKey] = fmt.Sprintf(`{"auths":{"%s":{"username":"%s","password":"%s"}}}`,
		quayImageURL,
		r.Name,
		r.Token)

	secret.StringData = ret
	return secret
}

func (r *ComponentReconciler) reportError(ctx context.Context, component *appstudioredhatcomv1alpha1.Component) (ctrl.Result, error) {
	lookUpKey := types.NamespacedName{Name: component.Name, Namespace: component.Namespace}
	r.Client.Get(ctx, lookUpKey, component)
	component.Annotations["image.redhat.io/generate"] = "failed"
	return ctrl.Result{}, r.Client.Status().Update(ctx, component)
}

// RepositoryInfo defines the structure of the Repository information being exposed to
// external systems.
type RepositoryInfo struct {
	Image  string `json:"image"`
	Secret string `json:"secret"`
}

func shouldGenerateImage(annotations map[string]string) bool {
	if generate, present := annotations["image.redhat.com/generate"]; present && generate == "true" {
		return true
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appstudioredhatcomv1alpha1.Component{}).
		Complete(r)
}
