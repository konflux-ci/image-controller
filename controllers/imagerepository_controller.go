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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	imagerepositoryv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	l "github.com/konflux-ci/image-controller/pkg/logs"
	"github.com/konflux-ci/image-controller/pkg/metrics"
	"github.com/konflux-ci/image-controller/pkg/quay"
	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	InternalSecretLabelName = "appstudio.redhat.com/internal"

	ImageRepositoryFinalizer = "appstudio.openshift.io/image-repository"

	buildPipelineServiceAccountName = "appstudio-pipeline"
	updateComponentAnnotationName   = "image-controller.appstudio.redhat.com/update-component-image"
)

// ImageRepositoryReconciler reconciles a ImageRepository object
type ImageRepositoryReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	QuayClient       quay.QuayService
	BuildQuayClient  func(logr.Logger) quay.QuayService
	QuayOrganization string
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageRepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&imagerepositoryv1alpha1.ImageRepository{}).
		Complete(r)
}

func setMetricsTime(idForMetrics string, reconcileStartTime time.Time) {
	_, timeRecorded := metrics.RepositoryTimesForMetrics[idForMetrics]
	if !timeRecorded {
		metrics.RepositoryTimesForMetrics[idForMetrics] = reconcileStartTime
	}
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories/finalizers,verbs=update
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;update;patch

func (r *ImageRepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithName("ImageRepository")
	ctx = ctrllog.IntoContext(ctx, log)
	reconcileStartTime := time.Now()

	// Fetch the image repository instance
	imageRepository := &imagerepositoryv1alpha1.ImageRepository{}
	err := r.Client.Get(ctx, req.NamespacedName, imageRepository)
	if err != nil {
		if errors.IsNotFound(err) {
			// The object is deleted, nothing to do
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to get image repository", l.Action, l.ActionView)
		return ctrl.Result{}, err
	}

	repositoryIdForMetrics := fmt.Sprintf("%s=%s", imageRepository.Name, imageRepository.Namespace)

	if !imageRepository.DeletionTimestamp.IsZero() {
		// remove component from metrics map
		delete(metrics.RepositoryTimesForMetrics, repositoryIdForMetrics)

		// Reread quay token
		r.QuayClient = r.BuildQuayClient(log)

		if controllerutil.ContainsFinalizer(imageRepository, ImageRepositoryFinalizer) {
			// Do not block deletion on failures
			r.CleanupImageRepository(ctx, imageRepository)

			controllerutil.RemoveFinalizer(imageRepository, ImageRepositoryFinalizer)
			if err := r.Client.Update(ctx, imageRepository); err != nil {
				log.Error(err, "failed to remove image repository finalizer", l.Action, l.ActionUpdate)
				return ctrl.Result{}, err
			}
			log.Info("Image repository finalizer removed", l.Action, l.ActionDelete)
		}
		return ctrl.Result{}, nil
	}

	if imageRepository.Status.State == imagerepositoryv1alpha1.ImageRepositoryStateFailed {
		provisionTime, timeRecorded := metrics.RepositoryTimesForMetrics[repositoryIdForMetrics]
		if timeRecorded {
			metrics.ImageRepositoryProvisionFailureTimeMetric.Observe(time.Since(provisionTime).Seconds())

			// remove component from metrics map
			delete(metrics.RepositoryTimesForMetrics, repositoryIdForMetrics)
		}

		return ctrl.Result{}, nil
	}

	// Reread quay token
	r.QuayClient = r.BuildQuayClient(log)

	// Provision image repository if it hasn't been done yet
	if !controllerutil.ContainsFinalizer(imageRepository, ImageRepositoryFinalizer) {
		setMetricsTime(repositoryIdForMetrics, reconcileStartTime)
		if err := r.ProvisionImageRepository(ctx, imageRepository); err != nil {
			log.Error(err, "provision of image repository failed")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Update component
	if isComponentLinked(imageRepository) {
		updateComponentAnnotation, updateComponentAnnotationExists := imageRepository.Annotations[updateComponentAnnotationName]
		if updateComponentAnnotationExists && updateComponentAnnotation == "true" {

			componentName := imageRepository.Labels[ComponentNameLabelName]
			component := &appstudioredhatcomv1alpha1.Component{}
			componentKey := types.NamespacedName{Namespace: imageRepository.Namespace, Name: componentName}
			if err := r.Client.Get(ctx, componentKey, component); err != nil {
				if errors.IsNotFound(err) {
					log.Info("attempt to update non existing component", "ComponentName", componentName)
					return ctrl.Result{}, nil
				}

				log.Error(err, "failed to get component", "ComponentName", componentName)
				return ctrl.Result{}, err
			}

			component.Spec.ContainerImage = imageRepository.Status.Image.URL

			if err := r.Client.Update(ctx, component); err != nil {
				log.Error(err, "failed to update Component after provision", "ComponentName", componentName)
				return ctrl.Result{}, err
			}
			log.Info("Updated component's ContainerImage", "ComponentName", componentName)
			delete(imageRepository.Annotations, updateComponentAnnotationName)

			if err := r.Client.Update(ctx, imageRepository); err != nil {
				log.Error(err, "failed to update imageRepository annotation")
				return ctrl.Result{}, err
			}
			log.Info("Updated image repository annotation")
		}
	}

	if imageRepository.Status.State != imagerepositoryv1alpha1.ImageRepositoryStateReady {
		return ctrl.Result{}, nil
	}

	// Make sure, that image repository name is the same as on creation.
	// Do it here to avoid webhook creation.
	imageRepositoryName := strings.TrimPrefix(imageRepository.Status.Image.URL, fmt.Sprintf("quay.io/%s/", r.QuayOrganization))
	if imageRepository.Spec.Image.Name != imageRepositoryName {
		oldName := imageRepository.Spec.Image.Name
		imageRepository.Spec.Image.Name = imageRepositoryName
		if err := r.Client.Update(ctx, imageRepository); err != nil {
			log.Error(err, "failed to revert image repository name", "OldName", oldName, "ExpectedName", imageRepositoryName, l.Action, l.ActionUpdate)
			return ctrl.Result{}, err
		}
		log.Info("reverted image repository name", "OldName", oldName, "ExpectedName", imageRepositoryName, l.Action, l.ActionUpdate)
		return ctrl.Result{}, nil
	}

	// Change image visibility if requested
	if imageRepository.Spec.Image.Visibility != imageRepository.Status.Image.Visibility && imageRepository.Spec.Image.Visibility != "" {
		if err := r.ChangeImageRepositoryVisibility(ctx, imageRepository); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if imageRepository.Spec.Credentials != nil {
		// Rotate credentials if requested
		regenerateToken := imageRepository.Spec.Credentials.RegenerateToken
		if regenerateToken != nil && *regenerateToken {
			if err := r.RegenerateImageRepositoryCredentials(ctx, imageRepository); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}

	// we are adding to map only for new provision, not for some partial actions,
	// so report time only if time was recorded
	provisionTime, timeRecorded := metrics.RepositoryTimesForMetrics[repositoryIdForMetrics]
	if timeRecorded {
		metrics.ImageRepositoryProvisionTimeMetric.Observe(time.Since(provisionTime).Seconds())
	}
	// remove component from metrics map
	delete(metrics.RepositoryTimesForMetrics, repositoryIdForMetrics)

	return ctrl.Result{}, nil
}

func (r *ImageRepositoryReconciler) AddNotifications(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository) ([]imagerepositoryv1alpha1.NotificationStatus, error) {
	log := ctrllog.FromContext(ctx).WithName("ConfigureNotifications")

	if imageRepository.Spec.Notifications == nil {
		// No notifications to configure
		return nil, nil
	}

	log.Info("Configuring notifications")
	notificationStatus := []imagerepositoryv1alpha1.NotificationStatus{}

	for _, notification := range imageRepository.Spec.Notifications {
		log.Info("Creating notification in Quay", "Title", notification.Title, "Event", notification.Event, "Method", notification.Method)
		quayNotification, err := r.QuayClient.CreateNotification(
			r.QuayOrganization,
			imageRepository.Spec.Image.Name,
			quay.Notification{
				Title:  notification.Title,
				Event:  string(notification.Event),
				Method: string(notification.Method),
				Config: quay.NotificationConfig{
					Url: notification.Config.Url,
				},
				EventConfig: quay.NotificationEventConfig{},
			})
		if err != nil {
			log.Error(err, "failed to create notification", "Title", notification.Title, "Event", notification.Event, "Method", notification.Method)
			return nil, err
		}
		notificationStatus = append(
			notificationStatus,
			imagerepositoryv1alpha1.NotificationStatus{
				UUID:  quayNotification.UUID,
				Title: notification.Title,
			})

		log.Info("Notification added",
			"Title", notification.Title,
			"Event", notification.Event,
			"Method", notification.Method,
			"QuayNotification", quayNotification)
	}
	return notificationStatus, nil
}

// ProvisionImageRepository creates image repository, robot account(s) and secret(s) to access the image repository.
// If labels with Application and Component name are present, robot account with pull only access
// will be created and pull token will be propagated to all environments via Remote Secret.
func (r *ImageRepositoryReconciler) ProvisionImageRepository(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository) error {
	log := ctrllog.FromContext(ctx).WithName("ImageRepositoryProvision")
	ctx = ctrllog.IntoContext(ctx, log)

	var component *appstudioredhatcomv1alpha1.Component
	if isComponentLinked(imageRepository) {
		componentName := imageRepository.Labels[ComponentNameLabelName]
		component = &appstudioredhatcomv1alpha1.Component{}
		componentKey := types.NamespacedName{Namespace: imageRepository.Namespace, Name: componentName}
		if err := r.Client.Get(ctx, componentKey, component); err != nil {
			if errors.IsNotFound(err) {
				imageRepository.Status.State = imagerepositoryv1alpha1.ImageRepositoryStateFailed
				imageRepository.Status.Message = fmt.Sprintf("Component '%s' does not exist", componentName)
				if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
					log.Error(err, "failed to update image repository status")
					return err
				}
				log.Info("attempt to create image repository related to non existing component", "Component", componentName)
				return nil
			}
			log.Error(err, "failed to get component", "ComponentName", componentName)
			return err
		}
	}

	imageRepositoryName := ""
	if imageRepository.Spec.Image.Name == "" {
		if isComponentLinked(imageRepository) {
			applicationName := imageRepository.Labels[ApplicationNameLabelName]
			componentName := imageRepository.Labels[ComponentNameLabelName]
			imageRepositoryName = imageRepository.Namespace + "/" + applicationName + "/" + componentName
		} else {
			imageRepositoryName = imageRepository.Namespace + "/" + imageRepository.Name
		}
	} else {
		imageRepositoryName = imageRepository.Namespace + "/" + imageRepository.Spec.Image.Name
	}
	imageRepository.Spec.Image.Name = imageRepositoryName

	quayImageURL := fmt.Sprintf("quay.io/%s/%s", r.QuayOrganization, imageRepositoryName)
	imageRepository.Status.Image.URL = quayImageURL

	if imageRepository.Spec.Image.Visibility == "" {
		imageRepository.Spec.Image.Visibility = imagerepositoryv1alpha1.ImageVisibilityPublic
	}
	visibility := string(imageRepository.Spec.Image.Visibility)

	repository, err := r.QuayClient.CreateRepository(quay.RepositoryRequest{
		Namespace:   r.QuayOrganization,
		Repository:  imageRepositoryName,
		Visibility:  visibility,
		Description: "AppStudio repository for the user",
	})
	if err != nil {
		log.Error(err, "failed to create image repository", l.Action, l.ActionAdd, l.Audit, "true")
		imageRepository.Status.State = imagerepositoryv1alpha1.ImageRepositoryStateFailed
		if err.Error() == "payment required" {
			imageRepository.Status.Message = "Number of private repositories exceeds current quay plan limit"
		} else {
			imageRepository.Status.Message = err.Error()
		}
		if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
			log.Error(err, "failed to update image repository status")
		}
		return nil
	}
	if repository == nil {
		err := fmt.Errorf("unexpected response from Quay: created image repository data object is nil")
		log.Error(err, "nil repository")
		return err
	}

	pushCredentialsInfo, err := r.ProvisionImageRepositoryAccess(ctx, imageRepository, false)
	if err != nil {
		return err
	}

	var pullCredentialsInfo *imageRepositoryAccessData
	if isComponentLinked(imageRepository) {
		pullCredentialsInfo, err = r.ProvisionImageRepositoryAccess(ctx, imageRepository, true)
		if err != nil {
			return err
		}
	}

	var notificationStatus []imagerepositoryv1alpha1.NotificationStatus
	if notificationStatus, err = r.AddNotifications(ctx, imageRepository); err != nil {
		return err
	}

	status := imagerepositoryv1alpha1.ImageRepositoryStatus{}
	status.State = imagerepositoryv1alpha1.ImageRepositoryStateReady
	status.Image.URL = quayImageURL
	status.Image.Visibility = imageRepository.Spec.Image.Visibility
	status.Credentials.GenerationTimestamp = &metav1.Time{Time: time.Now()}
	status.Credentials.PushRobotAccountName = pushCredentialsInfo.RobotAccountName
	status.Credentials.PushSecretName = pushCredentialsInfo.SecretName
	if isComponentLinked(imageRepository) {
		status.Credentials.PullRobotAccountName = pullCredentialsInfo.RobotAccountName
		status.Credentials.PullSecretName = pullCredentialsInfo.SecretName
	}
	status.Notifications = notificationStatus

	imageRepository.Spec.Image.Name = imageRepositoryName
	controllerutil.AddFinalizer(imageRepository, ImageRepositoryFinalizer)
	if isComponentLinked(imageRepository) {
		if err := controllerutil.SetOwnerReference(component, imageRepository, r.Scheme); err != nil {
			log.Error(err, "failed to set component as owner", "ComponentName", component.Name)
			// Do not brake provision because of failed owner reference
		}
	}

	if err := r.Client.Update(ctx, imageRepository); err != nil {
		log.Error(err, "failed to update CR after provision")
		return err
	} else {
		log.Info("Finished provision of image repository and added finalizer")
	}

	imageRepository.Status = status
	if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
		log.Error(err, "failed to update CR status after provision")
		return err
	}

	return nil
}

type imageRepositoryAccessData struct {
	RobotAccountName string
	SecretName       string
}

// ProvisionImageRepositoryAccess makes existing quay image repository accessible
// by creating robot account and storing its token in a RemoteSecret.
func (r *ImageRepositoryReconciler) ProvisionImageRepositoryAccess(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository, isPullOnly bool) (*imageRepositoryAccessData, error) {
	log := ctrllog.FromContext(ctx).WithName("ProvisionImageRepositoryAccess").WithValues("IsPullOnly", isPullOnly)
	ctx = ctrllog.IntoContext(ctx, log)

	imageRepositoryName := imageRepository.Spec.Image.Name
	quayImageURL := imageRepository.Status.Image.URL

	robotAccountName := generateQuayRobotAccountName(imageRepositoryName, isPullOnly)
	robotAccount, err := r.QuayClient.CreateRobotAccount(r.QuayOrganization, robotAccountName)
	if err != nil {
		log.Error(err, "failed to create robot account", "RobotAccountName", robotAccountName, l.Action, l.ActionAdd, l.Audit, "true")
		return nil, err
	}
	if robotAccount == nil {
		err := fmt.Errorf("unexpected response from Quay: robot account data object is nil")
		log.Error(err, "nil robot account")
		return nil, err
	}

	err = r.QuayClient.AddPermissionsForRepositoryToRobotAccount(r.QuayOrganization, imageRepositoryName, robotAccount.Name, !isPullOnly)
	if err != nil {
		log.Error(err, "failed to add permissions to robot account", "RobotAccountName", robotAccountName, l.Action, l.ActionUpdate, l.Audit, "true")
		return nil, err
	}

	secretName := getSecretName(imageRepository, isPullOnly)
	if err := r.EnsureSecret(ctx, imageRepository, secretName, robotAccount, quayImageURL, isPullOnly); err != nil {
		return nil, err
	}

	data := &imageRepositoryAccessData{
		RobotAccountName: robotAccountName,
		SecretName:       secretName,
	}
	return data, nil
}

// RegenerateImageRepositoryCredentials rotates robot account(s) token and updates corresponding secret(s)
func (r *ImageRepositoryReconciler) RegenerateImageRepositoryCredentials(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository) error {
	log := ctrllog.FromContext(ctx)

	if err := r.RegenerateImageRepositoryAccessToken(ctx, imageRepository, false); err != nil {
		return err
	}

	if isComponentLinked(imageRepository) {
		if err := r.RegenerateImageRepositoryAccessToken(ctx, imageRepository, true); err != nil {
			return err
		}
	}

	imageRepository.Spec.Credentials.RegenerateToken = nil
	if err := r.Client.Update(ctx, imageRepository); err != nil {
		log.Error(err, "failed to update image repository", l.Action, l.ActionUpdate)
		return err
	}

	imageRepository.Status.Credentials.GenerationTimestamp = &metav1.Time{Time: time.Now()}
	if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
		log.Error(err, "failed to update image repository status", l.Action, l.ActionUpdate)
		return err
	}

	return nil
}

// RegenerateImageRepositoryAccessToken rotates robot account token and updates new one to the corresponding Remote Secret.
func (r *ImageRepositoryReconciler) RegenerateImageRepositoryAccessToken(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository, isPullOnly bool) error {
	log := ctrllog.FromContext(ctx).WithName("RegenerateImageRepositoryAccessToken").WithValues("IsPullOnly", isPullOnly)
	ctx = ctrllog.IntoContext(ctx, log)

	quayImageURL := imageRepository.Status.Image.URL

	robotAccountName := imageRepository.Status.Credentials.PushRobotAccountName
	if isPullOnly {
		robotAccountName = imageRepository.Status.Credentials.PullRobotAccountName
	}
	robotAccount, err := r.QuayClient.RegenerateRobotAccountToken(r.QuayOrganization, robotAccountName)
	if err != nil {
		log.Error(err, "failed to refresh robot account token")
		return err
	} else {
		log.Info("Refreshed quay robot account token")
	}

	secretName := imageRepository.Status.Credentials.PushSecretName
	if isPullOnly {
		secretName = imageRepository.Status.Credentials.PullSecretName
	}
	if err := r.EnsureSecret(ctx, imageRepository, secretName, robotAccount, quayImageURL, isPullOnly); err != nil {
		return err
	}
	return nil
}

// CleanupImageRepository deletes image repository and corresponding robot account(s).
func (r *ImageRepositoryReconciler) CleanupImageRepository(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository) {
	log := ctrllog.FromContext(ctx).WithName("RepositoryCleanup")

	robotAccountName := imageRepository.Status.Credentials.PushRobotAccountName
	isRobotAccountDeleted, err := r.QuayClient.DeleteRobotAccount(r.QuayOrganization, robotAccountName)
	if err != nil {
		log.Error(err, "failed to delete push robot account", l.Action, l.ActionDelete, l.Audit, "true")
	}
	if isRobotAccountDeleted {
		log.Info("Deleted push robot account", "RobotAccountName", robotAccountName, l.Action, l.ActionDelete)
	}

	if isComponentLinked(imageRepository) {
		pullRobotAccountName := imageRepository.Status.Credentials.PullRobotAccountName
		isPullRobotAccountDeleted, err := r.QuayClient.DeleteRobotAccount(r.QuayOrganization, pullRobotAccountName)
		if err != nil {
			log.Error(err, "failed to delete pull robot account", l.Action, l.ActionDelete, l.Audit, "true")
		}
		if isPullRobotAccountDeleted {
			log.Info("Deleted pull robot account", "RobotAccountName", pullRobotAccountName, l.Action, l.ActionDelete)
		}
	}

	imageRepositoryName := imageRepository.Spec.Image.Name
	isImageRepositoryDeleted, err := r.QuayClient.DeleteRepository(r.QuayOrganization, imageRepositoryName)
	if err != nil {
		log.Error(err, "failed to delete image repository", l.Action, l.ActionDelete, l.Audit, "true")
	}
	if isImageRepositoryDeleted {
		log.Info("Deleted image repository", "ImageRepository", imageRepositoryName, l.Action, l.ActionDelete)
	}
}

func (r *ImageRepositoryReconciler) ChangeImageRepositoryVisibility(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository) error {
	if imageRepository.Status.Image.Visibility == imageRepository.Spec.Image.Visibility {
		return nil
	}

	log := ctrllog.FromContext(ctx)

	imageRepositoryName := imageRepository.Spec.Image.Name
	requestedVisibility := string(imageRepository.Spec.Image.Visibility)
	err := r.QuayClient.ChangeRepositoryVisibility(r.QuayOrganization, imageRepositoryName, requestedVisibility)
	if err == nil {
		imageRepository.Status.Image.Visibility = imageRepository.Spec.Image.Visibility
		imageRepository.Status.Message = ""
		if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
			log.Error(err, "failed to update image repository name", l.Action, l.ActionUpdate)
			return err
		}
		log.Info("changed image repository visibility", "visibility", imageRepository.Spec.Image.Visibility)
		return nil
	}

	if err.Error() == "payment required" {
		log.Info("failed to make image repository private due to quay plan limit", l.Audit, "true")

		imageRepository.Spec.Image.Visibility = imageRepository.Status.Image.Visibility
		if err := r.Client.Update(ctx, imageRepository); err != nil {
			log.Error(err, "failed to update image repository", l.Action, l.ActionUpdate)
			return err
		}

		imageRepository.Status.Message = "Quay organization plan private repositories limit exceeded"
		if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
			log.Error(err, "failed to update image repository", l.Action, l.ActionUpdate)
			return err
		}

		// Do not trigger a new reconcile since the error handled
		return nil
	}

	log.Error(err, "failed to change image repository visibility")
	return err
}

func (r *ImageRepositoryReconciler) EnsureSecret(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository, secretName string, robotAccount *quay.RobotAccount, imageURL string, isPull bool) error {
	log := ctrllog.FromContext(ctx).WithValues("SecretName", secretName)

	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Namespace: imageRepository.Namespace, Name: secretName}
	if err := r.Client.Get(ctx, secretKey, secret); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "failed to get remote secret", l.Action, l.ActionView)
			return err
		}

		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: imageRepository.Namespace,
				Labels: map[string]string{
					InternalSecretLabelName: "true",
				},
			},
			Type:       corev1.SecretTypeDockerConfigJson,
			StringData: generateDockerconfigSecretData(imageURL, robotAccount),
		}

		if err := controllerutil.SetOwnerReference(imageRepository, secret, r.Scheme); err != nil {
			log.Error(err, "failed to set owner for image repository secret")
			return err
		}

		if err := r.Client.Create(ctx, secret); err != nil {
			log.Error(err, "failed to create image repository secret", l.Action, l.ActionAdd, l.Audit, "true")
			return err
		} else {
			log.Info("Image repository secret created")
		}

		if !isPull {
			serviceAccount := &corev1.ServiceAccount{}
			serviceAccountKey := types.NamespacedName{Namespace: imageRepository.Namespace, Name: buildPipelineServiceAccountName}
			if err := r.Client.Get(ctx, serviceAccountKey, serviceAccount); err != nil {
				log.Error(err, "failed to get service account", l.Action, l.ActionView)
				return err
			}
			serviceAccount.Secrets = append(serviceAccount.Secrets, corev1.ObjectReference{Name: secretName})
			if err := r.Client.Update(ctx, serviceAccount); err != nil {
				log.Error(err, "failed to update service account", l.Action, l.ActionUpdate)
				return err
			}
		}
	}
	return nil
}

// generateQuayRobotAccountName generates valid robot account name for given image repository name.
func generateQuayRobotAccountName(imageRepositoryName string, isPullOnly bool) string {
	// Robot account name must match ^[a-z][a-z0-9_]{1,254}$

	imageNamePrefix := imageRepositoryName
	if len(imageNamePrefix) > 220 {
		imageNamePrefix = imageNamePrefix[:220]
	}
	imageNamePrefix = strings.ReplaceAll(imageNamePrefix, "/", "_")
	imageNamePrefix = strings.ReplaceAll(imageNamePrefix, ".", "_")
	imageNamePrefix = strings.ReplaceAll(imageNamePrefix, "-", "_")

	randomSuffix := getRandomString(10)

	robotAccountName := fmt.Sprintf("%s_%s", imageNamePrefix, randomSuffix)
	if isPullOnly {
		robotAccountName += "_pull"
	}
	return robotAccountName
}

func getSecretName(imageRepository *imagerepositoryv1alpha1.ImageRepository, isPullOnly bool) string {
	secretName := imageRepository.Name
	if len(secretName) > 220 {
		secretName = secretName[:220]
	}
	if isPullOnly {
		secretName += "-image-pull"
	} else {
		secretName += "-image-push"
	}
	return secretName
}

func isComponentLinked(imageRepository *imagerepositoryv1alpha1.ImageRepository) bool {
	return imageRepository.Labels[ApplicationNameLabelName] != "" && imageRepository.Labels[ComponentNameLabelName] != ""
}

func getRandomString(length int) string {
	bytes := make([]byte, length/2+1)
	if _, err := rand.Read(bytes); err != nil {
		panic("Failed to read from random generator")
	}
	return hex.EncodeToString(bytes)[0:length]
}
