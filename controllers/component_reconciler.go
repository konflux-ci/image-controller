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
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
)

const (
	quayOrganization string = "redhat-user-workloads"
)

var defaultQuayToken string = ""

// ComponentReconciler reconciles a Controller object
type ComponentReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	HttpClient *http.Client
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
	_ = log.FromContext(ctx)

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
	if !shouldGenerateImage(component.Annotations) {
		return ctrl.Result{}, nil
	}

	// Use of the defaultQuayToken is a temporary affair
	tokenPath := os.Getenv("DEV_TOKEN_PATH")
	if tokenPath == "" {
		// will be unset in prod
		tokenPath = "/workspace/quaytoken"
	}
	if defaultQuayToken == "" {
		file, err := ioutil.ReadFile(tokenPath)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("Error reading the quay token - requeue the request : %w", err)
		}
		defaultQuayToken = string(file)
	}

	quayClient := quay.NewQuayClient(r.HttpClient, defaultQuayToken, "https://quay.io/api/v1")
	repo, robot, err := generateImageRepository(*component, quayOrganization, quayClient)

	robotAccountSecret := generateSecret(*component, *robot)
	err = r.Client.Create(ctx, &robotAccountSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("Error writing robot account token into a Secret - requeue the request - %w", err)
	}

	generatedRepository := RepositoryInfo{
		Image:  fmt.Sprintf("quay.io/%s/%s", quayOrganization, repo.Name),
		Secret: robotAccountSecret.Name,
	}
	generatedRepositoryBytes, _ := json.Marshal(generatedRepository)

	lookUpKey := types.NamespacedName{Name: component.Name, Namespace: component.Namespace}
	r.Client.Get(ctx, lookUpKey, component)

	component.Annotations["image.redhat.com/image"] = string(generatedRepositoryBytes)
	component.Annotations["image.redhat.com/generate"] = "false"

	if err != nil {
		component.Annotations["image.redhat.com/generate-error"] = err.Error()
	}

	if err := r.Client.Update(ctx, component); err != nil {
		return ctrl.Result{}, fmt.Errorf("Error updating the component annotations - requeue the request : %w", err)
	}

	return ctrl.Result{}, nil
}

func generateSecretName(c appstudioredhatcomv1alpha1.Component) string {
	return fmt.Sprintf("%s-%s", c.Name, strings.Split(string(c.UID), "-")[0])
}

// generateSecret dumps the robot account token into a Secret for future consumption.
func generateSecret(c appstudioredhatcomv1alpha1.Component, r quay.RobotAccount) corev1.Secret {
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.Name + "-" + string(uuid.NewUUID()),
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
		"https://quay.io",
		r.Name,
		r.Token)

	secret.StringData = ret
	return secret
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

func generateRobotAccountName(component appstudioredhatcomv1alpha1.Component) string {
	// if uuid is 08a177d2-6707-4a5a-9f81-b73ec5a74047,
	// then the robot account name is redhat08a177d267074a5a9f81b73ec5a74047
	uuid := strings.Replace(string(component.UID), "-", "", -1)
	return fmt.Sprintf("redhat%s", uuid)
}

func generateImageRepository(component appstudioredhatcomv1alpha1.Component, quayNamespace string, quayClient quay.QuayClient) (*quay.Repository, *quay.RobotAccount, error) {
	repo, err := quayClient.CreateRepository(quay.RepositoryRequest{
		Namespace:   quayNamespace,
		Visibility:  "public",
		Description: "Stonesoup repository for the user",
		Repository:  component.Namespace + "/" + component.Spec.Application + "/" + component.Name,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("Error creating repository %v", err)
	}
	fmt.Println(fmt.Sprintf("Successfully created repo %v", repo))

	robot, err := quayClient.CreateRobotAccount(quayNamespace, generateRobotAccountName(component))
	if err != nil {
		return nil, nil, err
	}

	fmt.Println(fmt.Sprintf("Successfully created robot %v", robot))

	err = quayClient.AddPermissionsToRobotAccount(quayNamespace, repo.Name, robot.Name)
	if err != nil {
		return nil, nil, err
	}

	fmt.Println("Successfully added permission")

	return repo, robot, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appstudioredhatcomv1alpha1.Component{}).
		Complete(r)
}
