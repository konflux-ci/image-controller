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

	"github.com/go-logr/logr"
	appstudioapiv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
	appstudiospiapiv1beta1 "github.com/redhat-appstudio/service-provider-integration-operator/api/v1beta1"
)

const (
	ImageAnnotationName                 = "image.redhat.com/image"
	GenerateImageAnnotationName         = "image.redhat.com/generate"
	DeleteImageRepositoryAnnotationName = "image.redhat.com/delete-image-repo"

	ImageRepositoryFinalizer = "image-controller.appstudio.openshift.io/image-repository"
)

// RepositoryInfo defines the structure of the Repository information being exposed to external systems.
type RepositoryInfo struct {
	Image  string `json:"image"`
	Secret string `json:"secret"`
}

type ImageControllerAction int

const (
	NoAction ImageControllerAction = iota
	ProvisionImageRepositoryAction
	RegenerateImageRepositoryTokenAction
)

type ImageRepositoryProvisionStatus struct {
	// Image repository flow
	isImageRepositoryCreated bool
	isRobotAccountCreated    bool
	isRobotAccountConfigured bool

	// Image repository robot account token flow
	isTokenSubmittedToSPI        bool
	isSPIAccessTokenReady        bool
	isSPIAccessTokenOwnerSet     bool
	isSPIAccessTokenBindingReady bool
}

func NewImageRepositoryProvisionStatus() *ImageRepositoryProvisionStatus {
	return &ImageRepositoryProvisionStatus{}
}

// ComponentReconciler reconciles a Controller object
type ComponentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger

	QuayClient       quay.QuayService
	QuayOrganization string

	ImageRepositoryProvision map[string]*ImageRepositoryProvisionStatus
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appstudioapiv1alpha1.Component{}).
		Complete(r)
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;delete
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=spiaccesstokens,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=spiaccesstokenbindings,verbs=get;list;watch;create;

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ComponentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("Component", req.NamespacedName)

	// Fetch the Component instance
	component := &appstudioapiv1alpha1.Component{}
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

			if val, exists := component.Annotations[DeleteImageRepositoryAnnotationName]; exists && val == "true" {
				imageRepo := generateImageRepositoryName(component)
				isRepoDeleted, err := r.QuayClient.DeleteRepository(r.QuayOrganization, imageRepo)
				if err != nil {
					log.Error(err, "failed to delete image repository")
					// Do not block Component deletion if failed to delete image repository
				}
				if isRepoDeleted {
					log.Info(fmt.Sprintf("Deleted image repository %s", imageRepo))
				}
			}

			if err := r.Client.Get(ctx, req.NamespacedName, component); err != nil {
				log.Error(err, "failed to get Component")
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(component, ImageRepositoryFinalizer)
			if err := r.Client.Update(ctx, component); err != nil {
				log.Error(err, "failed to remove image repository finalizer")
				return ctrl.Result{}, err
			}
			log.Info("Image repository finalizer removed from the Component")
		}

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

	switch getRequestedAction(component.Annotations) {
	case ProvisionImageRepositoryAction:
		done, err := r.ProvisionImageRepository(ctx, component)
		if err != nil {
			if done {
				// Permanent error
				log.Error(err, "failed to provision image repository for the Component")
				if err := r.reportError(ctx, component); err != nil {
					log.Error(err, "failed to set error on the Component")
				}
				return ctrl.Result{}, nil
			}
			// Continue provision flow with a new retry
			return ctrl.Result{}, err
		}
		if !done {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		log.Info("Image repository provision successfully finished")

	case RegenerateImageRepositoryTokenAction:
		if err := r.RegenerateImageRepositoryToken(ctx, component); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *ComponentReconciler) reportError(ctx context.Context, component *appstudioapiv1alpha1.Component) error {
	lookUpKey := types.NamespacedName{Name: component.Name, Namespace: component.Namespace}
	if err := r.Client.Get(ctx, lookUpKey, component); err != nil {
		return err
	}
	component.Annotations[GenerateImageAnnotationName] = "failed"
	return r.Client.Update(ctx, component)
}

// getRequestedAction returns what jobs is requested for the component
func getRequestedAction(annotations map[string]string) ImageControllerAction {
	if generateValue, present := annotations[GenerateImageAnnotationName]; present {
		if generateValue == "true" {
			return ProvisionImageRepositoryAction
		}
		if generateValue == "regenerate-token" {
			return RegenerateImageRepositoryTokenAction
		}
	}
	return NoAction
}

// generateRobotAccountName generates predictable robot account name for given component
func generateRobotAccountName(component *appstudioapiv1alpha1.Component) string {
	//TODO: replace component.Namespace with the name of the Space
	return strings.ToLower(component.Namespace + component.Spec.Application + component.Name)
}

// generateImageRepositoryName generates predictable image repository name for given component
func generateImageRepositoryName(component *appstudioapiv1alpha1.Component) string {
	return strings.ToLower(component.Namespace + "/" + component.Spec.Application + "/" + component.Name)
}

// ProvisionImageRepository does complete provision of image repositroy for given component,
// including uploading credentials to SPI and binding and linking them to pipeline service account.
func (r *ComponentReconciler) ProvisionImageRepository(ctx context.Context, component *appstudioapiv1alpha1.Component) (bool, error) {
	componentKey := types.NamespacedName{Namespace: component.Namespace, Name: component.Name}
	log := r.Log.WithValues("ImageRepoProvisonForComponent", componentKey)
	var err error

	// Get or create repository provision status
	imageRepoName := generateImageRepositoryName(component)
	imageURL := fmt.Sprintf("quay.io/%s/%s", r.QuayOrganization, imageRepoName)
	rps, exists := r.ImageRepositoryProvision[imageRepoName]
	if !exists {
		rps = NewImageRepositoryProvisionStatus()
		r.ImageRepositoryProvision[imageRepoName] = rps
	}

	if !rps.isImageRepositoryCreated {
		repo, err := r.QuayClient.CreateRepository(quay.RepositoryRequest{
			Namespace:   r.QuayOrganization,
			Visibility:  "public",
			Description: "AppStudio repository for the user",
			Repository:  imageRepoName,
		})
		if err != nil {
			r.Log.Error(err, fmt.Sprintf("failed to create image repository %s", imageRepoName))
			return false, err
		}
		if repo == nil {
			return false, fmt.Errorf("unknown error in the image repository creation process")
		}
		log.Info(fmt.Sprintf("Image repository %s created for %v component", repo.Name, componentKey))

		rps.isImageRepositoryCreated = true
	}

	var robotAccount *quay.RobotAccount
	robotAccountName := generateRobotAccountName(component)

	if !rps.isRobotAccountCreated {
		robotAccount, err = r.QuayClient.CreateRobotAccount(r.QuayOrganization, robotAccountName)
		if err != nil {
			r.Log.Error(err, fmt.Sprintf("failed to create robot account %s", robotAccountName))
			return false, err
		}
		if robotAccount == nil {
			return false, fmt.Errorf("unknown error in the robot account creation process")
		}
		log.Info(fmt.Sprintf("Robot account %s created for %s image repository", robotAccount.Name, imageURL))

		rps.isRobotAccountCreated = true
	}

	if !rps.isRobotAccountConfigured {
		err := r.QuayClient.AddWritePermissionsToRobotAccount(r.QuayOrganization, imageRepoName, robotAccountName)
		if err != nil {
			r.Log.Error(err, fmt.Sprintf("failed to add permissions to robot account %s", robotAccountName))
			return false, err
		}

		rps.isRobotAccountConfigured = true
	}

	if !rps.isTokenSubmittedToSPI {
		if robotAccount == nil {
			robotAccount, err = r.QuayClient.GetRobotAccount(r.QuayOrganization, robotAccountName)
			if err != nil {
				r.Log.Error(err, fmt.Sprintf("failed to get robot account %s", robotAccountName))
				return false, err
			}
			log.Info(fmt.Sprintf("Submitted robot account %s token to SPI", robotAccountName))
		}

		// Name SPIAccessToken the same as robot account
		robotAccountTokenSecret := generateUploadToSPISecret(component, robotAccount, imageURL)
		if err := r.Client.Create(ctx, robotAccountTokenSecret); err != nil {
			log.Error(err, fmt.Sprintf("error writing robot account token into Secret: %s", robotAccountTokenSecret.Name))
			return false, err
		}

		rps.isTokenSubmittedToSPI = true
	}

	spiAccessToken := &appstudiospiapiv1beta1.SPIAccessToken{}
	spiAccessTokenKey := types.NamespacedName{Namespace: component.Namespace, Name: robotAccountName}
	if err := r.Client.Get(ctx, spiAccessTokenKey, spiAccessToken); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, fmt.Sprintf("failed to get SPIAccessToken %v", spiAccessTokenKey))
			return false, err
		}
		// Wait for the token object to be created by SPI
		log.Info(fmt.Sprintf("waiting for SPIAccessToken %v existance", spiAccessTokenKey))
		return false, nil
	}

	if !rps.isSPIAccessTokenReady {
		if spiAccessToken.Status.Phase != appstudiospiapiv1beta1.SPIAccessTokenPhaseReady {
			log.Info(fmt.Sprintf("waiting for SPIAccessToken %v readiness", spiAccessTokenKey))
			return false, nil
		}

		rps.isSPIAccessTokenReady = true
	}

	if !rps.isSPIAccessTokenOwnerSet {
		// Add owner reference to ensure SPIAccessToken is deleted together with the component
		spiAccessToken.ObjectMeta.OwnerReferences = append(spiAccessToken.ObjectMeta.OwnerReferences,
			metav1.OwnerReference{
				Name:       component.Name,
				Kind:       component.Kind,
				APIVersion: component.APIVersion,
				UID:        component.UID,
			},
		)

		if err := r.Client.Update(ctx, spiAccessToken); err != nil {
			log.Error(err, fmt.Sprintf("failed to update owner reference of SPIAccessToken %v", spiAccessTokenKey))
			return false, err
		}

		rps.isSPIAccessTokenOwnerSet = true
	}

	spiAccessTokenBinding := &appstudiospiapiv1beta1.SPIAccessTokenBinding{}
	spiAccessTokenBindingKey := types.NamespacedName{Namespace: component.Namespace, Name: robotAccountName}
	if err := r.Client.Get(ctx, spiAccessTokenBindingKey, spiAccessTokenBinding); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, fmt.Sprintf("failed to get SPIAccessTokenBinding %v", spiAccessTokenBindingKey))
			return false, err
		}
		// SPIAccessTokenBinding does not exists, create it
		spiAccessTokenBinding = generateSPIAccessTokenBinding(component, imageURL, robotAccountName)
		if err := r.Client.Create(ctx, spiAccessTokenBinding); err != nil {
			log.Error(err, fmt.Sprintf("failed to create SPIAccessTokenBinding %v", spiAccessTokenBindingKey))
			return false, err
		}

		return false, nil
	}

	if !rps.isSPIAccessTokenBindingReady {
		if spiAccessTokenBinding.Status.Phase != appstudiospiapiv1beta1.SPIAccessTokenBindingPhaseInjected {
			log.Info(fmt.Sprintf("waiting for SPIAccessTokenBinding %v readiness", spiAccessTokenBindingKey))
			return false, nil
		}

		rps.isSPIAccessTokenBindingReady = true
	}

	// Update component with the generated data and add finalizer
	if err := r.Client.Get(ctx, componentKey, component); err != nil {
		log.Error(err, "failed to get component")
		return false, err
	}

	if component.ObjectMeta.DeletionTimestamp.IsZero() {
		generatedRepository := RepositoryInfo{
			Image:  imageURL,
			Secret: spiAccessTokenBinding.Status.SyncedObjectRef.Name,
		}
		generatedRepositoryBytes, _ := json.Marshal(generatedRepository)

		component.Annotations[ImageAnnotationName] = string(generatedRepositoryBytes)
		component.Annotations[GenerateImageAnnotationName] = "false"

		isFinalizerAdded := false
		if !controllerutil.ContainsFinalizer(component, ImageRepositoryFinalizer) {
			controllerutil.AddFinalizer(component, ImageRepositoryFinalizer)
			isFinalizerAdded = true
		}

		if err := r.Client.Update(ctx, component); err != nil {
			log.Error(err, "failed to update component")
			return false, err
		}

		if isFinalizerAdded {
			log.Info("Image regipository finaliziler added to the Component")
		}
		log.Info("Component updated successfully")
	}

	// Mark the provision completely finished
	delete(r.ImageRepositoryProvision, imageRepoName)

	return true, nil
}

// RegenerateImageRepositoryToken refreshes token of robot account connected to the image repository.
// Uploads the new token to SPI, so the used by client pipeline secret gets updated as well.
func (r *ComponentReconciler) RegenerateImageRepositoryToken(ctx context.Context, component *appstudioapiv1alpha1.Component) error {
	componentKey := types.NamespacedName{Namespace: component.Namespace, Name: component.Name}
	log := r.Log.WithValues("RegenerateImageRepositoryToken", componentKey)

	imageRepoName := generateImageRepositoryName(component)
	imageURL := fmt.Sprintf("quay.io/%s/%s", r.QuayOrganization, imageRepoName)
	robotAccountName := generateRobotAccountName(component)

	robotAccount, err := r.QuayClient.RegenerateRobotAccountToken(r.QuayOrganization, robotAccountName)
	if err != nil {
		log.Error(err, "failed to regenerate quayrobot account token")
		return err
	}
	log.Info(fmt.Sprintf("Refreshed token of %s robot account", robotAccount.Name))

	robotAccountTokenSecret := generateUploadToSPISecret(component, robotAccount, imageURL)
	if err := r.Client.Create(ctx, robotAccountTokenSecret); err != nil {
		log.Error(err, fmt.Sprintf("error writing robot account token into Secret: %s", robotAccountTokenSecret.Name))
		return err
	}
	log.Info(fmt.Sprintf("Submitted update of robot account %s token to SPI", robotAccountName))

	// There is no point in waiting for the token readiness

	if err := r.Client.Get(ctx, componentKey, component); err != nil {
		return err
	}
	component.Annotations[GenerateImageAnnotationName] = "false"
	return r.Client.Update(ctx, component)
}

// generateUploadToSPISecret generates a secret to upload robot account credentials to SPI.
func generateUploadToSPISecret(component *appstudioapiv1alpha1.Component, robotAccount *quay.RobotAccount, quayImageURL string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      component.Name + "-upload-image-registry-secret",
			Namespace: component.Namespace,
			Labels: map[string]string{
				"spi.appstudio.redhat.com/upload-secret": "token",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			// Name SPIAccessToken the same as robot account, because the token is singleton for the robot account.
			"spiTokenName": robotAccount.Name,
			"providerUrl":  quayImageURL,
			"userName":     robotAccount.Name,
			"tokenData":    robotAccount.Token,
		},
	}
}

func generateSPIAccessTokenBinding(component *appstudioapiv1alpha1.Component, imageURL, robotAccountName string) *appstudiospiapiv1beta1.SPIAccessTokenBinding {
	pipelineServiceAccountName := "pipeline"

	return &appstudiospiapiv1beta1.SPIAccessTokenBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      robotAccountName,
			Namespace: component.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					Name:       component.Name,
					Kind:       component.Kind,
					APIVersion: component.APIVersion,
					UID:        component.UID,
				},
			},
		},
		Spec: appstudiospiapiv1beta1.SPIAccessTokenBindingSpec{
			RepoUrl:  imageURL,
			Lifetime: "-1",
			Secret: appstudiospiapiv1beta1.SecretSpec{
				Type: corev1.SecretTypeDockerConfigJson,
				LinkedTo: []appstudiospiapiv1beta1.SecretLink{
					{
						ServiceAccount: appstudiospiapiv1beta1.ServiceAccountLink{
							As: appstudiospiapiv1beta1.ServiceAccountLinkTypeSecret,
							Reference: corev1.LocalObjectReference{
								Name: pipelineServiceAccountName,
							},
						},
					},
					{
						ServiceAccount: appstudiospiapiv1beta1.ServiceAccountLink{
							As: appstudiospiapiv1beta1.ServiceAccountLinkTypeImagePullSecret,
							Reference: corev1.LocalObjectReference{
								Name: pipelineServiceAccountName,
							},
						},
					},
				},
			},
		},
	}
}
