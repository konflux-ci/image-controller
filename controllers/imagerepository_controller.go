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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	imagerepositoryv1alpha1 "github.com/redhat-appstudio/image-controller/api/v1alpha1"
	l "github.com/redhat-appstudio/image-controller/pkg/logs"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
	remotesecretv1beta1 "github.com/redhat-appstudio/remote-secret/api/v1beta1"
)

const (
	InternalRemoteSecretLabelName = "appstudio.redhat.com/internal"

	ImageRepositoryFinalizer = "appstudio.openshift.io/image-repository"

	buildPipelineServiceAccountName = "appstudio-pipeline"

	metricsNamespace = "redhat_appstudio"
	metricsSubsystem = "imagecontroller"
)

var (
	imageRepositoryProvisionTimeMetric        prometheus.Histogram
	imageRepositoryProvisionFailureTimeMetric prometheus.Histogram
	repositoryTimesForMetrics                 = map[string]time.Time{}
)

// ImageRepositoryReconciler reconciles a ImageRepository object
type ImageRepositoryReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	QuayClient       quay.QuayService
	BuildQuayClient  func(logr.Logger) quay.QuayService
	QuayOrganization string
}

func initMetrics() error {
	buckets := getProvisionTimeMetricsBuckets()

	// don't register it if it was already registered by another controller
	if imageRepositoryProvisionTimeMetric != nil {
		return nil
	}

	imageRepositoryProvisionTimeMetric = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Buckets:   buckets,
		Name:      "image_repository_provision_time",
		Help:      "The time in seconds spent from the moment of Image repository provision request to Image repository is ready to use.",
	})

	imageRepositoryProvisionFailureTimeMetric = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: metricsNamespace,
		Subsystem: metricsSubsystem,
		Buckets:   buckets,
		Name:      "image_repository_provision_failure_time",
		Help:      "The time in seconds spent from the moment of Image repository provision request to Image repository failure.",
	})

	if err := metrics.Registry.Register(imageRepositoryProvisionTimeMetric); err != nil {
		return fmt.Errorf("failed to register the image_repository_provision_time metric: %w", err)
	}
	if err := metrics.Registry.Register(imageRepositoryProvisionFailureTimeMetric); err != nil {
		return fmt.Errorf("failed to register the image_repository_provision_failure_time metric: %w", err)
	}

	return nil
}

func getProvisionTimeMetricsBuckets() []float64 {
	return []float64{5, 10, 15, 20, 30, 60, 120, 300}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageRepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := initMetrics(); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&imagerepositoryv1alpha1.ImageRepository{}).
		Complete(r)
}

func setMetricsTime(idForMetrics string, reconcileStartTime time.Time) {
	_, timeRecorded := repositoryTimesForMetrics[idForMetrics]
	if !timeRecorded {
		repositoryTimesForMetrics[idForMetrics] = reconcileStartTime
	}
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories/finalizers,verbs=update
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=remotesecrets,verbs=get;list;watch;create
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch

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
		delete(repositoryTimesForMetrics, repositoryIdForMetrics)

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
		provisionTime, timeRecorded := repositoryTimesForMetrics[repositoryIdForMetrics]
		if timeRecorded {
			imageRepositoryProvisionFailureTimeMetric.Observe(time.Since(provisionTime).Seconds())

			// remove component from metrics map
			delete(repositoryTimesForMetrics, repositoryIdForMetrics)
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
	provisionTime, timeRecorded := repositoryTimesForMetrics[repositoryIdForMetrics]
	if timeRecorded {
		imageRepositoryProvisionTimeMetric.Observe(time.Since(provisionTime).Seconds())
	}
	// remove component from metrics map
	delete(repositoryTimesForMetrics, repositoryIdForMetrics)

	return ctrl.Result{}, nil
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

	status := imagerepositoryv1alpha1.ImageRepositoryStatus{}
	status.State = imagerepositoryv1alpha1.ImageRepositoryStateReady
	status.Image.URL = quayImageURL
	status.Image.Visibility = imageRepository.Spec.Image.Visibility
	status.Credentials.GenerationTimestamp = &metav1.Time{Time: time.Now()}
	status.Credentials.PushRobotAccountName = pushCredentialsInfo.RobotAccountName
	status.Credentials.PushRemoteSecretName = pushCredentialsInfo.RemoteSecretName
	status.Credentials.PushSecretName = pushCredentialsInfo.SecretName
	if isComponentLinked(imageRepository) {
		status.Credentials.PullRobotAccountName = pullCredentialsInfo.RobotAccountName
		status.Credentials.PullRemoteSecretName = pullCredentialsInfo.RemoteSecretName
		status.Credentials.PullSecretName = pullCredentialsInfo.SecretName
	}

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
	RemoteSecretName string
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

	remoteSecretName := getRemoteSecretName(imageRepository, isPullOnly)
	if err := r.EnsureRemoteSecret(ctx, imageRepository, remoteSecretName, isPullOnly); err != nil {
		return nil, err
	}

	if err := r.CreateRemoteSecretUploadSecret(ctx, robotAccount, imageRepository.Namespace, remoteSecretName, quayImageURL); err != nil {
		return nil, err
	}

	data := &imageRepositoryAccessData{
		RobotAccountName: robotAccountName,
		RemoteSecretName: remoteSecretName,
		SecretName:       remoteSecretName,
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

	remoteSecretName := imageRepository.Status.Credentials.PushRemoteSecretName
	if isPullOnly {
		remoteSecretName = imageRepository.Status.Credentials.PullRemoteSecretName
	}
	if err := r.EnsureRemoteSecret(ctx, imageRepository, remoteSecretName, isPullOnly); err != nil {
		return err
	}
	if err := r.CreateRemoteSecretUploadSecret(ctx, robotAccount, imageRepository.Namespace, remoteSecretName, quayImageURL); err != nil {
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

func (r *ImageRepositoryReconciler) EnsureRemoteSecret(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository, remoteSecretName string, isPull bool) error {
	log := ctrllog.FromContext(ctx).WithValues("RemoteSecretName", remoteSecretName)

	remoteSecret := &remotesecretv1beta1.RemoteSecret{}
	remoteSecretKey := types.NamespacedName{Namespace: imageRepository.Namespace, Name: remoteSecretName}
	if err := r.Client.Get(ctx, remoteSecretKey, remoteSecret); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "failed to get remote secret", l.Action, l.ActionView)
			return err
		}

		serviceAccountName := buildPipelineServiceAccountName
		if isPull {
			serviceAccountName = defaultServiceAccountName
		}

		remoteSecret := &remotesecretv1beta1.RemoteSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      remoteSecretName,
				Namespace: imageRepository.Namespace,
				Labels: map[string]string{
					InternalRemoteSecretLabelName: "true",
				},
			},
			Spec: remotesecretv1beta1.RemoteSecretSpec{
				Secret: remotesecretv1beta1.LinkableSecretSpec{
					Name: remoteSecretName,
					Type: corev1.SecretTypeDockerConfigJson,
					LinkedTo: []remotesecretv1beta1.SecretLink{
						{
							ServiceAccount: remotesecretv1beta1.ServiceAccountLink{
								Reference: corev1.LocalObjectReference{
									Name: serviceAccountName,
								},
							},
						},
					},
				},
			},
		}

		if isPull {
			remoteSecret.Labels[ApplicationNameLabelName] = imageRepository.Labels[ApplicationNameLabelName]
			remoteSecret.Labels[ComponentNameLabelName] = imageRepository.Labels[ComponentNameLabelName]
		} else {
			remoteSecret.Spec.Targets = []remotesecretv1beta1.RemoteSecretTarget{{Namespace: imageRepository.Namespace}}
		}

		if err := controllerutil.SetOwnerReference(imageRepository, remoteSecret, r.Scheme); err != nil {
			log.Error(err, "failed to set owner for remote secret")
			return err
		}

		if err := r.Client.Create(ctx, remoteSecret); err != nil {
			log.Error(err, "failed to create remote secret", l.Action, l.ActionAdd, l.Audit, "true")
			return err
		} else {
			log.Info("Remote Secret created")
		}
	}

	return nil
}

// CreateRemoteSecretUploadSecret propagates credentials from given robot account to corresponding remote secret.
func (r *ImageRepositoryReconciler) CreateRemoteSecretUploadSecret(ctx context.Context, robotAccount *quay.RobotAccount, namespace, remoteSecretName, imageURL string) error {
	uploadSecretName := "upload-secret-" + remoteSecretName
	log := ctrllog.FromContext(ctx).WithValues("RemoteSecretName", remoteSecretName).WithValues("UploadSecretName", uploadSecretName)

	uploadSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      uploadSecretName,
			Namespace: namespace,
			Labels: map[string]string{
				remotesecretv1beta1.UploadSecretLabel: "remotesecret",
			},
			Annotations: map[string]string{
				remotesecretv1beta1.RemoteSecretNameAnnotation: remoteSecretName,
			},
		},
		Type:       corev1.SecretTypeDockerConfigJson,
		StringData: generateDockerconfigSecretData(imageURL, robotAccount),
	}
	if err := r.Client.Create(ctx, uploadSecret); err != nil {
		log.Error(err, "failed to create upload secret", l.Action, l.ActionAdd, l.Audit, "true")
		return err
	} else {
		log.Info("Created upload Secret for Remote Secret")
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

func getRemoteSecretName(imageRepository *imagerepositoryv1alpha1.ImageRepository, isPullOnly bool) string {
	remoteSecretName := imageRepository.Name
	if len(remoteSecretName) > 220 {
		remoteSecretName = remoteSecretName[:220]
	}
	if isPullOnly {
		remoteSecretName += "-image-pull"
	} else {
		remoteSecretName += "-image-push"
	}
	return remoteSecretName
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
