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
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
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

	buildPipelineServiceAccountName      = "appstudio-pipeline"
	updateComponentAnnotationName        = "image-controller.appstudio.redhat.com/update-component-image"
	additionalUsersConfigMapName         = "image-controller-additional-users"
	additionalUsersConfigMapKey          = "quay.io"
	skipRepositoryDeletionAnnotationName = "image-controller.appstudio.redhat.com/skip-repository-deletion"
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
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;update;patch

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

		if err := r.unlinkSecretFromServiceAccount(ctx, imageRepository.Status.Credentials.PushSecretName, imageRepository.Namespace); err != nil {
			log.Error(err, "failed to unlink secret from service account", "SecretName", imageRepository.Status.Credentials.PushSecretName, l.Action, l.ActionUpdate)
			return ctrl.Result{}, err
		}

		if controllerutil.ContainsFinalizer(imageRepository, ImageRepositoryFinalizer) {
			// Check if there isn't other ImageRepository for the same repository from other component
			imageRepositoryFound, err := r.ImageRepositoryForSameUrlExists(ctx, imageRepository)
			if err != nil {
				return ctrl.Result{}, err
			}

			if imageRepositoryFound {
				log.Info("Found another image repository for", "RepoURL", imageRepository.Status.Image.URL)
			}

			skipDeletion := imageRepository.Annotations[skipRepositoryDeletionAnnotationName] == "true"
			if skipDeletion {
				log.Info("Skip deletion was configured for image repository", "ImageRepository", imageRepository.Name)
			}
			// Do not block deletion on failures
			r.CleanupImageRepository(ctx, imageRepository, !(imageRepositoryFound || skipDeletion))

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

				log.Error(err, "failed to get component", "ComponentName", componentName, l.Action, l.ActionView)
				return ctrl.Result{}, err
			}

			component.Spec.ContainerImage = imageRepository.Status.Image.URL

			if err := r.Client.Update(ctx, component); err != nil {
				log.Error(err, "failed to update Component after provision", "ComponentName", componentName, l.Action, l.ActionUpdate)
				return ctrl.Result{}, err
			}
			log.Info("Updated component's ContainerImage", "ComponentName", componentName)
			delete(imageRepository.Annotations, updateComponentAnnotationName)

			if err := r.Client.Update(ctx, imageRepository); err != nil {
				log.Error(err, "failed to update imageRepository annotation", l.Action, l.ActionUpdate)
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

		// Check and fix linking is requested
		verifyLinking := imageRepository.Spec.Credentials.VerifyLinking
		if verifyLinking != nil && *verifyLinking {
			if err := r.VerifyAndFixSecretsLinking(ctx, imageRepository); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}

	if err = r.HandleNotifications(ctx, imageRepository); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
		log.Error(err, "failed to update imageRepository status", l.Action, l.ActionUpdate)
		return ctrl.Result{}, err
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

// ProvisionImageRepository creates image repository, robot account(s) and secret(s) to access the image repository.
// If labels with Application and Component name are present, robot account with pull only access
// will be created and pull token will be propagated Secret.
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
				imageRepository.Status.Message = fmt.Sprintf("Component '%s' does not exist", componentName)
				if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
					log.Error(err, "failed to update imageRepository status", l.Action, l.ActionUpdate)
					return err
				}
				log.Info("attempt to create image repository related to non existing component", "Component", componentName)
			}
			log.Error(err, "failed to get component", "ComponentName", componentName, l.Action, l.ActionView)
			return err
		}
	}

	imageRepositoryName := ""
	if imageRepository.Spec.Image.Name == "" {
		if isComponentLinked(imageRepository) {
			componentName := imageRepository.Labels[ComponentNameLabelName]
			imageRepositoryName = imageRepository.Namespace + "/" + componentName
		} else {
			imageRepositoryName = imageRepository.Namespace + "/" + imageRepository.Name
		}
	} else {
		imageRepositoryName = strings.TrimPrefix(imageRepository.Spec.Image.Name, "/")
		if !strings.HasPrefix(imageRepositoryName, imageRepository.Namespace+"/") {
			imageRepositoryName = imageRepository.Namespace + "/" + imageRepositoryName
		}
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
			log.Error(err, "failed to update imageRepository status", l.Action, l.ActionUpdate)
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

	if err = r.GrantRepositoryAccessToTeam(ctx, imageRepository); err != nil {
		return err
	}

	var notificationStatus []imagerepositoryv1alpha1.NotificationStatus
	if notificationStatus, err = r.SetNotifications(ctx, imageRepository); err != nil {
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
			// Do not fail provision because of failed owner reference
		}
	}

	if err := r.Client.Update(ctx, imageRepository); err != nil {
		log.Error(err, "failed to update imageRepository after provision", l.Action, l.ActionUpdate)
		return err
	}
	log.Info("Finished provision of image repository and added finalizer")

	imageRepository.Status = status
	if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
		log.Error(err, "failed to update imageRepository status after provision", l.Action, l.ActionUpdate)
		return err
	}

	return nil
}

type imageRepositoryAccessData struct {
	RobotAccountName string
	SecretName       string
}

// ProvisionImageRepositoryAccess makes existing quay image repository accessible
// by creating robot account and storing its token in a Secret.
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

	err = r.QuayClient.AddPermissionsForRepositoryToAccount(r.QuayOrganization, imageRepositoryName, robotAccount.Name, true, !isPullOnly)
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

// GrantRepositoryAccessToTeam will add additional repository access to team, based on config map
func (r *ImageRepositoryReconciler) GrantRepositoryAccessToTeam(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository) error {
	log := ctrllog.FromContext(ctx).WithName("GrantAdditionalRepositoryAccessToTeam")

	additionalUsersConfigMap := &corev1.ConfigMap{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: additionalUsersConfigMapName, Namespace: imageRepository.Namespace}, additionalUsersConfigMap); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Config map with additional users doesn't exist", "ConfigMapName", additionalUsersConfigMapName, l.Action, l.ActionView)
			return nil
		}
		log.Error(err, "failed to read config map with additional users", "ConfigMapName", additionalUsersConfigMapName, l.Action, l.ActionView)
		return err
	}
	_, usersExist := additionalUsersConfigMap.Data[additionalUsersConfigMapKey]
	if !usersExist {
		log.Info("Config map with additional users doesn't have the key", "ConfigMapName", additionalUsersConfigMapName, "ConfigMapKey", additionalUsersConfigMapKey, l.Action, l.ActionView)
		return nil
	}

	imageRepositoryName := imageRepository.Spec.Image.Name
	teamName := getQuayTeamName(imageRepository.Namespace)

	// get team, if team doesn't exist it will be created, we don't care about users as that will be taken care of by config map controller
	// so in this case if config map exists, team already exists as well with appropriate users
	log.Info("Ensure team", "TeamName", teamName)
	if _, err := r.QuayClient.EnsureTeam(r.QuayOrganization, teamName); err != nil {
		log.Error(err, "failed to get or create team", "TeamName", teamName, l.Action, l.ActionView)
		return err
	}

	// add repo permission to the team
	log.Info("Adding repository permission to the team", "TeamName", teamName, "RepositoryName", imageRepositoryName)
	if err := r.QuayClient.AddReadPermissionsForRepositoryToTeam(r.QuayOrganization, imageRepositoryName, teamName); err != nil {
		log.Error(err, "failed to grant repo permission to the team", "TeamName", teamName, "RepositoryName", imageRepositoryName, l.Action, l.ActionAdd)
		return err
	}

	return nil
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
		log.Error(err, "failed to update imageRepository", l.Action, l.ActionUpdate)
		return err
	}

	imageRepository.Status.Credentials.GenerationTimestamp = &metav1.Time{Time: time.Now()}
	if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
		log.Error(err, "failed to update imageRepository status", l.Action, l.ActionUpdate)
		return err
	}

	return nil
}

// RegenerateImageRepositoryAccessToken rotates robot account token and updates new one to the corresponding Secret.
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
func (r *ImageRepositoryReconciler) CleanupImageRepository(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository, removeRepository bool) {
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

	if !removeRepository {
		log.Info("Skipping the removal of image repository", "RepoName", imageRepository.Status.Image.URL)
		return
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
			log.Error(err, "failed to update imageRepository status", l.Action, l.ActionUpdate)
			return err
		}
		log.Info("changed image repository visibility", "visibility", imageRepository.Spec.Image.Visibility)
		return nil
	}

	if err.Error() == "payment required" {
		log.Info("failed to make image repository private due to quay plan limit", l.Audit, "true")

		imageRepository.Spec.Image.Visibility = imageRepository.Status.Image.Visibility
		if err := r.Client.Update(ctx, imageRepository); err != nil {
			log.Error(err, "failed to update imageRepository", l.Action, l.ActionUpdate)
			return err
		}

		imageRepository.Status.Message = "Quay organization plan private repositories limit exceeded"
		if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
			log.Error(err, "failed to update imageRepository status", l.Action, l.ActionUpdate)
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
			log.Error(err, "failed to get secret", "SecretName", secretName, l.Action, l.ActionView)
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
	}
	if !isPull {
		if err := r.linkSecretToServiceAccount(ctx, secretName, imageRepository.Namespace); err != nil {
			log.Error(err, "failed to link secret to service account", "SecretName", secretName, l.Action, l.ActionUpdate)
			return err
		}
	}
	return nil
}

// linkSecretToServiceAccount ensures that the given secret is linked with the provided service account.
func (r *ImageRepositoryReconciler) linkSecretToServiceAccount(ctx context.Context, secretNameToAdd, namespace string) error {
	log := ctrllog.FromContext(ctx).WithValues("ServiceAccountName", buildPipelineServiceAccountName)

	serviceAccount := &corev1.ServiceAccount{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: namespace}, serviceAccount)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Error(err, "pipeline service account doesn't exist yet", l.Action, l.ActionView)
			return err
		}
		log.Error(err, "failed to read pipeline service account", l.Action, l.ActionView)
		return err
	}

	// check if secret is already linked and add it only if it isn't to avoid duplication
	secretLinked := false
	for _, serviceAccountSecret := range serviceAccount.Secrets {
		if serviceAccountSecret.Name == secretNameToAdd {
			secretLinked = true
			break
		}
	}
	if !secretLinked {
		// add it only to Secrets and not in imagePullSecrets which is used only for the image pod itself needs to run
		serviceAccount.Secrets = append(serviceAccount.Secrets, corev1.ObjectReference{Name: secretNameToAdd})

		if err := r.Client.Update(ctx, serviceAccount); err != nil {
			log.Error(err, "failed to update pipeline service account", l.Action, l.ActionUpdate)
			return err
		}
		log.Info("Added secret link to pipeline service account", "SecretName", secretNameToAdd, l.Action, l.ActionUpdate)
	}
	return nil
}

// unlinkSecretFromServiceAccount ensures that the given secret is not linked with the provided service account.
func (r *ImageRepositoryReconciler) unlinkSecretFromServiceAccount(ctx context.Context, secretNameToRemove, namespace string) error {
	log := ctrllog.FromContext(ctx).WithValues("ServiceAccountName", buildPipelineServiceAccountName)

	serviceAccount := &corev1.ServiceAccount{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: namespace}, serviceAccount)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "failed to read pipeline service account", l.Action, l.ActionView)
		return err
	}

	unlinkSecret := false
	// Remove secret from secrets list
	pushSecrets := []corev1.ObjectReference{}
	for _, credentialSecret := range serviceAccount.Secrets {
		// don't break and search for duplicities
		if credentialSecret.Name == secretNameToRemove {
			unlinkSecret = true
			continue
		}
		pushSecrets = append(pushSecrets, credentialSecret)
	}
	serviceAccount.Secrets = pushSecrets

	// Remove secret from pull secrets list
	// we aren't adding them anymore in imagePullSecrets but just to be sure check and remove them from there
	imagePullSecrets := []corev1.LocalObjectReference{}
	for _, pullSecret := range serviceAccount.ImagePullSecrets {
		// don't break and search for duplicities
		if pullSecret.Name == secretNameToRemove {
			unlinkSecret = true
			continue
		}
		imagePullSecrets = append(imagePullSecrets, pullSecret)
	}
	serviceAccount.ImagePullSecrets = imagePullSecrets

	if unlinkSecret {
		if err := r.Client.Update(ctx, serviceAccount); err != nil {
			log.Error(err, "failed to update pipeline service account", l.Action, l.ActionUpdate)
			return err
		}
		log.Info("Removed secret link from pipeline service account", "SecretName", secretNameToRemove, l.Action, l.ActionUpdate)
	}
	return nil
}

// cleanUpSecretInServiceAccount ensures that the given secret is linked with the provided service account just once
// and remove the secret from ImagePullSecrets
func (r *ImageRepositoryReconciler) cleanUpSecretInServiceAccount(ctx context.Context, secretName, namespace string) error {
	log := ctrllog.FromContext(ctx).WithValues("ServiceAccountName", buildPipelineServiceAccountName)

	serviceAccount := &corev1.ServiceAccount{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: namespace}, serviceAccount)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "failed to read pipeline service account", l.Action, l.ActionView)
		return err
	}

	linksModified := false

	// Check for duplicates for the secret and remove them
	pushSecrets := []corev1.ObjectReference{}
	foundSecret := false
	for _, credentialSecret := range serviceAccount.Secrets {
		if credentialSecret.Name == secretName {
			if !foundSecret {
				pushSecrets = append(pushSecrets, credentialSecret)
				foundSecret = true
			} else {
				linksModified = true
			}
		} else {
			pushSecrets = append(pushSecrets, credentialSecret)
		}
	}
	serviceAccount.Secrets = pushSecrets

	// Remove secret from pull secrets list
	imagePullSecrets := []corev1.LocalObjectReference{}
	for _, pullSecret := range serviceAccount.ImagePullSecrets {
		// don't break and search for duplicities
		if pullSecret.Name == secretName {
			linksModified = true
			continue
		}
		imagePullSecrets = append(imagePullSecrets, pullSecret)
	}
	serviceAccount.ImagePullSecrets = imagePullSecrets

	if linksModified {
		if err := r.Client.Update(ctx, serviceAccount); err != nil {
			log.Error(err, "failed to update pipeline service account", l.Action, l.ActionUpdate)
			return err
		}
		log.Info("Cleaned up secret links in pipeline service account", "SecretName", secretName, l.Action, l.ActionUpdate)
	}
	return nil
}

// VerifyAndFixSecretsLinking ensures that the given secret is linked to the provided service account, and also removes duplicated link for the secret.
// when secret doesn't exist unlink it from service account
func (r *ImageRepositoryReconciler) VerifyAndFixSecretsLinking(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository) error {
	secretName := imageRepository.Status.Credentials.PushSecretName
	log := ctrllog.FromContext(ctx).WithValues("SecretName", secretName)

	// check secret existence and if secret doesn't exist unlink it from service account
	// users can recreate it by using regenerate-token
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Namespace: imageRepository.Namespace, Name: secretName}
	secretExists := true
	if err := r.Client.Get(ctx, secretKey, secret); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "failed to get secret", l.Action, l.ActionView)
			return err
		}

		secretExists = false
		log.Info("Secret doesn't exist, will unlink secret from service account")

		if err := r.unlinkSecretFromServiceAccount(ctx, secretName, imageRepository.Namespace); err != nil {
			log.Error(err, "failed to unlink secret from service account", l.Action, l.ActionUpdate)
			return err
		}
	}

	if secretExists {
		// link secret to service account if isn't linked already
		if err := r.linkSecretToServiceAccount(ctx, secretName, imageRepository.Namespace); err != nil {
			log.Error(err, "failed to link secret to service account", l.Action, l.ActionUpdate)
			return err
		}

		// clean duplicate secret links and remove secret from ImagePullSecrets
		if err := r.cleanUpSecretInServiceAccount(ctx, secretName, imageRepository.Namespace); err != nil {
			log.Error(err, "failed to clean up secret in service account", l.Action, l.ActionUpdate)
			return err
		}
	}

	imageRepository.Spec.Credentials.VerifyLinking = nil
	if err := r.Client.Update(ctx, imageRepository); err != nil {
		log.Error(err, "failed to update imageRepository", l.Action, l.ActionUpdate)
		return err
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
	robotAccountName = removeDuplicateUnderscores(robotAccountName)
	return robotAccountName
}

// removeDuplicateUnderscores replaces sequence of underscores with only one.
// Example: ab__cd___e => ab_cd_e
func removeDuplicateUnderscores(s string) string {
	return regexp.MustCompile("_+").ReplaceAllString(s, "_")
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
		panic("failed to read from random generator")
	}
	return hex.EncodeToString(bytes)[0:length]
}

func (r *ImageRepositoryReconciler) UpdateImageRepositoryStatusMessage(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository, statusMessage string) error {
	log := ctrllog.FromContext(ctx)
	imageRepository.Status.Message = statusMessage
	if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
		log.Error(err, "failed to update imageRepository status", l.Action, l.ActionUpdate)
		return err
	}

	return nil
}

func generateDockerconfigSecretData(quayImageURL string, robotAccount *quay.RobotAccount) map[string]string {
	secretData := map[string]string{}
	authString := fmt.Sprintf("%s:%s", robotAccount.Name, robotAccount.Token)
	secretData[corev1.DockerConfigJsonKey] = fmt.Sprintf(`{"auths":{"%s":{"auth":"%s"}}}`,
		quayImageURL, base64.StdEncoding.EncodeToString([]byte(authString)))
	return secretData
}

func (r *ImageRepositoryReconciler) ImageRepositoryForSameUrlExists(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository) (bool, error) {
	log := ctrllog.FromContext(ctx)
	imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
	if err := r.Client.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: imageRepository.Namespace}); err != nil {
		log.Error(err, "failed to list image repositories")
		return false, err
	}

	imageRepositoryUrl := imageRepository.Status.Image.URL
	imageRepositoryName := imageRepository.ObjectMeta.Name
	for _, imageRepo := range imageRepositoriesList.Items {
		if imageRepositoryUrl == imageRepo.Status.Image.URL {
			// skipping the original ImageRepository which is in the list as well
			if imageRepositoryName == imageRepo.ObjectMeta.Name {
				continue
			}
			return true, nil
		}
	}

	return false, nil
}
