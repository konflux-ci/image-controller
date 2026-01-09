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
	"encoding/json"
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

	updateComponentAnnotationName        = "image-controller.appstudio.redhat.com/update-component-image"
	additionalUsersConfigMapName         = "image-controller-additional-users"
	additionalUsersConfigMapKey          = "quay.io"
	skipRepositoryDeletionAnnotationName = "image-controller.appstudio.redhat.com/skip-repository-deletion"

	waitForRelatedComponentInitialDelay           = 5
	waitForRelatedComponentFallbackDelay          = 60
	waitForRelatedComponentInitialWindowDuration  = 2 * 60
	waitForRelatedComponentFallbackWindowDuration = 60 * 60

	componentSaNamePrefix                   = "build-pipeline-"
	imageRepositoryNameChangedMessagePrefix = "Image repository name changed after creation"
	imageRepositoryNameChangedMessageSuffix = "That doesn't change image name in the registry. To do that, delete ImageRepository object and re-create it with new image name."
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

		if isComponentLinked(imageRepository) {
			// unlink secret from component SA
			componentSaName := getComponentSaName(imageRepository.Labels[ComponentNameLabelName])
			if err := r.unlinkSecretFromServiceAccount(ctx, componentSaName, imageRepository.Status.Credentials.PushSecretName, imageRepository.Namespace); err != nil {
				log.Error(err, "failed to unlink secret from service account", "SaName", componentSaName, "SecretName", imageRepository.Status.Credentials.PushSecretName, l.Action, l.ActionUpdate)
				return ctrl.Result{}, err
			}

			// remove pull secret entry from application pull secret
			err := r.removePullSecretFromApplicationPullSecret(ctx, imageRepository)
			if err != nil {
				log.Error(err, "failed to remove entry from application pull secret", "application", imageRepository.Labels[ApplicationNameLabelName], "secret", imageRepository.Status.Credentials.PullSecretName)
				return ctrl.Result{}, err
			}

			// unlink pull secret for nudging component from nudged components SA
			if err := r.unlinkPullSecretFromNudgedComponentSAs(ctx, imageRepository.Status.Credentials.PullSecretName, imageRepository.Namespace); err != nil {
				log.Error(err, "failed to unlink pull secret from nudging service accounts", "SecretName", imageRepository.Status.Credentials.PullSecretName, l.Action, l.ActionUpdate)
				return ctrl.Result{}, err
			}
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
		if isComponentLinked(imageRepository) {
			componentExists, requeueAfterSeconds, err := r.CheckComponentExistence(ctx, imageRepository)
			if err != nil {
				// getting component failed
				return ctrl.Result{}, err
			}
			if requeueAfterSeconds > 0 {
				// wait for component to appear, requeue without error
				return ctrl.Result{RequeueAfter: time.Duration(requeueAfterSeconds) * time.Second}, nil
			}
			if !componentExists {
				// component doesn't exist and we won't requeue anymore, 2 cases:
				// 1st we are updating status for the 1st time, which will do another reconcile
				// 2nd status was already updated, but wait timeout for component elapsed
				return ctrl.Result{}, nil
			}
		}

		if err := r.ProvisionImageRepository(ctx, imageRepository); err != nil {
			log.Error(err, "provision of image repository failed")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Update component
	if isComponentLinked(imageRepository) {
		// update application pull secret
		applicationName := imageRepository.Labels[ApplicationNameLabelName]
		pullSecretName := getSecretName(imageRepository, true)
		err := r.addPullSecretAuthToApplicationPullSecret(ctx, applicationName, imageRepository.Namespace, pullSecretName, imageRepository.Status.Image.URL, false)
		if err != nil {
			log.Error(err, "failed to update application pull secret with individual pull secret", "application", applicationName, "secret", pullSecretName)
			return ctrl.Result{}, err
		}

		// link secret to component SA
		pushSecretName := getSecretName(imageRepository, false)
		componentSaName := getComponentSaName(imageRepository.Labels[ComponentNameLabelName])
		if err := r.linkSecretToServiceAccount(ctx, componentSaName, pushSecretName, imageRepository.Namespace, false); err != nil {
			log.Error(err, "failed to link secret to service account", "SaName", componentSaName, "SecretName", pushSecretName, l.Action, l.ActionUpdate)
			return ctrl.Result{}, err
		}

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
	// If it isn't set message
	imageRepositoryName := strings.TrimPrefix(imageRepository.Status.Image.URL, fmt.Sprintf("quay.io/%s/", r.QuayOrganization))
	if imageRepository.Spec.Image.Name != imageRepositoryName {
		imageNameDiffersMessage := fmt.Sprintf("%s from '%s' to '%s'. %s", imageRepositoryNameChangedMessagePrefix, imageRepositoryName, imageRepository.Spec.Image.Name, imageRepositoryNameChangedMessageSuffix)

		if imageRepository.Status.Message != imageNameDiffersMessage {
			imageRepository.Status.Message = imageNameDiffersMessage
			if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
				log.Error(err, "failed to update imageRepository status message with image name change", l.Action, l.ActionUpdate)
				return ctrl.Result{}, err
			}
			log.Info("added message about image change to imageRepository", "ImageRepository", imageRepository.ObjectMeta.Name)
			return ctrl.Result{}, nil
		}
	} else {
		// Remove message about image changed, if it is the same
		if strings.HasPrefix(imageRepository.Status.Message, imageRepositoryNameChangedMessagePrefix) {
			imageRepository.Status.Message = ""
			if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
				log.Error(err, "failed to update imageRepository remove message with image name change", l.Action, l.ActionUpdate)
				return ctrl.Result{}, err
			}
			log.Info("removed message about image change from imageRepository", "ImageRepository", imageRepository.ObjectMeta.Name)
			return ctrl.Result{}, nil
		}
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

// CheckComponentExistence checks if component for ImageRepository exists
// if not it will request requeue and wait for component to be created
// returns componentExists bool, requeueAfterSeconds int, error
func (r *ImageRepositoryReconciler) CheckComponentExistence(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository) (bool, int, error) {
	log := ctrllog.FromContext(ctx).WithName("CheckComponentExistence")

	componentName := imageRepository.Labels[ComponentNameLabelName]
	component := &appstudioredhatcomv1alpha1.Component{}
	componentKey := types.NamespacedName{Namespace: imageRepository.Namespace, Name: componentName}
	if err := r.Client.Get(ctx, componentKey, component); err != nil {
		if errors.IsNotFound(err) {
			log.Info("component related to image repository doesn't exist, will wait for component", "Component", componentName)
			componentDoesNotExistMessage := fmt.Sprintf("Component '%s' does not exist", componentName)

			if imageRepository.Status.Message != componentDoesNotExistMessage {
				imageRepository.Status.Message = componentDoesNotExistMessage
				if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
					log.Error(err, "failed to update imageRepository status", l.Action, l.ActionUpdate)
					return false, -1, err
				}
				// status update will trigger new reconcile
				return false, -1, nil
			}
			// when status message is the same status update won't trigger new reconcile, so we will explicitly request requeue
			timeAfterCreation := time.Now().Unix() - imageRepository.GetCreationTimestamp().Unix()
			if timeAfterCreation < waitForRelatedComponentInitialWindowDuration {
				return false, waitForRelatedComponentInitialDelay, nil
			}
			if timeAfterCreation < waitForRelatedComponentFallbackWindowDuration {
				if imageRepository.Status.State == "" {
					imageRepository.Status.State = imagerepositoryv1alpha1.ImageRepositoryStateWaiting
					if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
						log.Error(err, "failed to update imageRepository status", l.Action, l.ActionUpdate)
						return false, -1, err
					}
					// status update will trigger new reconcile
					return false, -1, nil
				}
				return false, waitForRelatedComponentFallbackDelay, nil
			}
			return false, -1, nil
		}
		log.Error(err, "failed to get component", "ComponentName", componentName, l.Action, l.ActionView)
		return false, -1, err
	}
	return true, -1, nil
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
				log.Info("attempt to create image repository related to non existing component", "Component", componentName)
				imageRepository.Status.Message = fmt.Sprintf("Component '%s' does not exist", componentName)
				if err := r.Client.Status().Update(ctx, imageRepository); err != nil {
					log.Error(err, "failed to update imageRepository status", l.Action, l.ActionUpdate)
					return err
				}
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

	// Build repository description with priority order:
	// 1. User-specified description in ImageRepository spec
	// 2. Component Git source URL
	// 3. Default fallback text
	description := "AppStudio repository for the user" // Default fallback

	if imageRepository.Spec.Image.Description != "" {
		// Priority 1: Use user-provided description
		description = imageRepository.Spec.Image.Description
	} else if component != nil && component.Spec.Source.GitSource != nil && component.Spec.Source.GitSource.URL != "" {
		// Priority 2: Build description from component Git source URL
		gitURL := component.Spec.Source.GitSource.URL
		description = fmt.Sprintf("This container is built from: %s\nPlease get more details there.", gitURL)
	}

	repository, err := r.QuayClient.CreateRepository(quay.RepositoryRequest{
		Namespace:   r.QuayOrganization,
		Repository:  imageRepositoryName,
		Visibility:  visibility,
		Description: description,
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

	// Update repository description via PUT endpoint
	// This is done separately because POST may not reliably set the description
	if err := r.QuayClient.UpdateRepositoryDescription(r.QuayOrganization, imageRepositoryName, description); err != nil {
		log.Error(err, "failed to update repository description", "repository", imageRepositoryName, l.Action, l.ActionUpdate)
		// Non-critical error - repository is still usable without a description
		// Continue with provisioning
	}

	pushCredentialsInfo, err := r.ProvisionImageRepositoryAccess(ctx, imageRepository, false)
	if err != nil {
		return err
	}

	pullCredentialsInfo, err := r.ProvisionImageRepositoryAccess(ctx, imageRepository, true)
	if err != nil {
		return err
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
	status.Credentials.PullRobotAccountName = pullCredentialsInfo.RobotAccountName
	status.Credentials.PullSecretName = pullCredentialsInfo.SecretName
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
	if err := r.EnsureSecret(ctx, imageRepository, secretName, robotAccount, quayImageURL); err != nil {
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
	if err := r.RegenerateImageRepositoryAccessToken(ctx, imageRepository, true); err != nil {
		return err
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
	if err := r.EnsureSecret(ctx, imageRepository, secretName, robotAccount, quayImageURL); err != nil {
		return err
	}

	// update also secret in application secret
	if isComponentLinked(imageRepository) && isPullOnly {
		applicationName := imageRepository.Labels[ApplicationNameLabelName]
		err := r.addPullSecretAuthToApplicationPullSecret(ctx, applicationName, imageRepository.Namespace, secretName, imageRepository.Status.Image.URL, true)
		if err != nil {
			log.Error(err, "failed to update application pull secret after individual pull secret change", "application", applicationName, "secret", secretName)
			return err
		}
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

	pullRobotAccountName := imageRepository.Status.Credentials.PullRobotAccountName
	isPullRobotAccountDeleted, err := r.QuayClient.DeleteRobotAccount(r.QuayOrganization, pullRobotAccountName)
	if err != nil {
		log.Error(err, "failed to delete pull robot account", l.Action, l.ActionDelete, l.Audit, "true")
	}
	if isPullRobotAccountDeleted {
		log.Info("Deleted pull robot account", "RobotAccountName", pullRobotAccountName, l.Action, l.ActionDelete)
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

func (r *ImageRepositoryReconciler) EnsureSecret(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository, secretName string, robotAccount *quay.RobotAccount, imageURL string) error {
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
		}
		log.Info("Image repository secret created")

	} else {
		secret.StringData = generateDockerconfigSecretData(imageURL, robotAccount)
		if err := r.Client.Update(ctx, secret); err != nil {
			log.Error(err, "failed to update image repository secret", l.Action, l.ActionUpdate, l.Audit, "true")
			return err
		}
		log.Info("Image repository secret updated")
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

// getComponentSaName returns name of component SA
func getComponentSaName(componentName string) string {
	return fmt.Sprintf("%s%s", componentSaNamePrefix, componentName)
}

// addPullSecretAuthToApplicationPullSecret updates the application pull secret when new image repository pull secret is created
// or when an existing one is updated.
func (r *ImageRepositoryReconciler) addPullSecretAuthToApplicationPullSecret(ctx context.Context, applicationName, namespace, pullSecretName, imageURL string, overwrite bool) error {
	log := ctrllog.FromContext(ctx)

	application := &appstudioredhatcomv1alpha1.Application{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: applicationName, Namespace: namespace}, application); err != nil {
		log.Error(err, "failed to get Application", "application", applicationName)
		return err
	}

	applicationPullSecretName := getApplicationPullSecretName(applicationName)
	applicationPullSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: applicationPullSecretName, Namespace: namespace}, applicationPullSecret); err != nil {
		log.Error(err, "failed to get application pull secret", "secretName", applicationPullSecretName)
		return err
	}

	var existingAuths dockerConfigJson
	if err := json.Unmarshal(applicationPullSecret.Data[corev1.DockerConfigJsonKey], &existingAuths); err != nil {
		log.Error(err, "failed to unmarshal existing .dockerconfigjson", "secretName", applicationPullSecretName)
		return err
	}

	if !overwrite && imageURL != "" {
		if _, authAlreadyExists := existingAuths.Auths[imageURL]; authAlreadyExists {
			return nil
		}
	}

	pullSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: pullSecretName, Namespace: namespace}, pullSecret); err != nil {
		if errors.IsNotFound(err) {
			log.Info("individual pull secret doesn't exist, nothing to add to application secret", "secretName", pullSecretName)
			return nil
		}
		log.Error(err, "failed to get individual pull secret", "secretName", pullSecretName)
		return err
	}
	if pullSecret.Type != corev1.SecretTypeDockerConfigJson {
		log.Info("Skipping secret due to unexpected type", "secretName", pullSecretName, "type", pullSecret.Type)
		return nil
	}

	dockerConfigDataBytes, ok := pullSecret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		log.Info("Missing .dockerconfigjson key", "secretName", pullSecretName)
		return nil
	}

	var newAuths dockerConfigJson
	if err := json.Unmarshal(dockerConfigDataBytes, &newAuths); err != nil {
		log.Error(err, "failed to unmarshal .dockerconfigjson", "secretName", pullSecretName)
		return err
	}

	changed := false
	if existingAuths.Auths == nil {
		existingAuths.Auths = map[string]dockerConfigAuth{}
	}

	// If there are multiple pullsecrets for the same registry,
	// keep the first one that was already present. Do not override unless explicitly requested (for rotation)
	for reg, entry := range newAuths.Auths {
		if _, authAlreadyExists := existingAuths.Auths[reg]; !authAlreadyExists {
			existingAuths.Auths[reg] = entry
			changed = true
		} else {
			if overwrite {
				existingAuths.Auths[reg] = entry
				changed = true
			}
		}
	}

	if !changed {
		return nil
	}

	// Marshal and update
	mergedData, err := json.Marshal(existingAuths)
	if err != nil {
		log.Error(err, "failed to marshal updated docker config json")
		return err
	}
	applicationPullSecret.Data[corev1.DockerConfigJsonKey] = mergedData

	if err := r.Client.Update(ctx, applicationPullSecret); err != nil {
		log.Error(err, "failed to update application pull secret", "secretName", applicationPullSecretName)
		return err
	}

	log.Info("Application pull secret updated with new registry auth", "application", applicationName)
	return nil

}

func (r *ImageRepositoryReconciler) removePullSecretFromApplicationPullSecret(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository) error {
	log := ctrllog.FromContext(ctx)

	applicationPullSecretName := getApplicationPullSecretName(imageRepository.Labels[ApplicationNameLabelName])
	applicationPullSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: applicationPullSecretName, Namespace: imageRepository.Namespace}, applicationPullSecret); err != nil {
		if errors.IsNotFound(err) {
			// Nothing to remove the pullsecret from
			return nil
		}
		log.Error(err, "failed to get application pull secret", "secretName", applicationPullSecretName)
		return err
	}

	// Get the pull secret that is being removed
	pullSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: imageRepository.Status.Credentials.PullSecretName, Namespace: imageRepository.Namespace}, pullSecret); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Pull secret already deleted, cannot identify which registry auths to remove", "secretName", imageRepository.Status.Credentials.PullSecretName)
			return nil
		}
		log.Error(err, "failed to get pull secret", "secretName", imageRepository.Status.Credentials.PullSecretName)
		return err
	}
	if pullSecret.Type != corev1.SecretTypeDockerConfigJson {
		log.Info("Skipping secret due to unexpected type", "secretName", imageRepository.Status.Credentials.PullSecretName, "type", pullSecret.Type)
		return nil
	}

	dockerConfigDataBytes, ok := pullSecret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		log.Info("Missing .dockerconfigjson key", "secretName", imageRepository.Status.Credentials.PullSecretName)
		return nil
	}

	var toRemoveAuths dockerConfigJson
	if err := json.Unmarshal(dockerConfigDataBytes, &toRemoveAuths); err != nil {
		log.Error(err, "failed to unmarshal .dockerconfigjson", "secretName", imageRepository.Status.Credentials.PullSecretName)
		return err
	}

	var existingAuths dockerConfigJson
	if err := json.Unmarshal(applicationPullSecret.Data[corev1.DockerConfigJsonKey], &existingAuths); err != nil {
		log.Error(err, "failed to unmarshal application .dockerconfigjson", "secretName", applicationPullSecretName)
		return err
	}

	if existingAuths.Auths == nil {
		return nil
	}

	imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
	if err := r.Client.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: imageRepository.Namespace}); err != nil {
		log.Error(err, "failed to list image repositories")
		return err
	}

	changed := false
	for reg := range toRemoveAuths.Auths {
		// Check if there’s another IR with the same repo URL. In that case
		// we don't remove the record for the registry pullsecret completely, but replace it
		// with pullsecret from this other IR.
		foundImageRepositoryWithSameUrl := false

		for _, otherIR := range imageRepositoriesList.Items {
			// Skip the current IR that contains the secret we are removing
			if otherIR.ObjectMeta.Name == imageRepository.ObjectMeta.Name {
				continue
			}
			// Must match the same registry URL
			if otherIR.Status.Image.URL != imageRepository.Status.Image.URL {
				continue
			}

			// Get the other IR pull secret
			otherIRPullSecret := &corev1.Secret{}
			if err := r.Client.Get(ctx, types.NamespacedName{Name: otherIR.Status.Credentials.PullSecretName, Namespace: imageRepository.Namespace}, otherIRPullSecret); err != nil {
				if errors.IsNotFound(err) {
					log.Info("Pull secret not found", "secretName", otherIR.Status.Credentials.PullSecretName)
					// We can continue looking for another alternative
					continue
				}
				log.Error(err, "failed to get pull secret", "secretName", otherIR.Status.Credentials.PullSecretName)
				return err
			}
			if otherIRPullSecret.Type != corev1.SecretTypeDockerConfigJson {
				continue
			}

			otherIRDockerConfigDataBytes, ok := otherIRPullSecret.Data[corev1.DockerConfigJsonKey]
			if !ok {
				continue
			}

			var otherIRConfig dockerConfigJson
			if err := json.Unmarshal(otherIRDockerConfigDataBytes, &otherIRConfig); err != nil {
				continue
			}

			// We found another Image Repository with the same registry URL and viable pullsecret to
			// replace the secret being removed.
			if replacement, ok := otherIRConfig.Auths[reg]; ok {
				existingAuths.Auths[reg] = replacement
				foundImageRepositoryWithSameUrl = true
				changed = true
				break
			}
		}

		if !foundImageRepositoryWithSameUrl {
			// No other IR with the same url found, safe to remove the auth entry
			if _, exists := existingAuths.Auths[reg]; exists {
				delete(existingAuths.Auths, reg)
				changed = true
			}
		}
	}

	if !changed {
		return nil
	}

	// Marshal and update
	updatedData, err := json.Marshal(existingAuths)
	if err != nil {
		log.Error(err, "failed to marshal updated docker config json after deletion")
		return err
	}
	applicationPullSecret.Data[corev1.DockerConfigJsonKey] = updatedData

	if err := r.Client.Update(ctx, applicationPullSecret); err != nil {
		log.Error(err, "failed to update application pull secret after removing registry auth", "secretName", applicationPullSecretName)
		return err
	}

	log.Info("Application pull secret updated after removing registry auth", "application", imageRepository.Labels[ApplicationNameLabelName])
	return nil
}
