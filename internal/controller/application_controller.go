/*
Copyright 2025 Red Hat, Inc.

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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	appstudioredhatcomv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	imagerepositoryv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	l "github.com/konflux-ci/image-controller/pkg/logs"
)

const (
	IntegrationTestsServiceAccountName = "konflux-integration-runner"
	ApplicationSecretLinkToSaFinalizer = "application-secret-link-to-integration-tests-sa.appstudio.openshift.io/finalizer"
)

// dockerConfigJson represents the structure of a .dockerconfigjson secret
type dockerConfigJson struct {
	Auths map[string]dockerConfigAuth `json:"auths"`
}
type dockerConfigAuth struct {
	Auth string `json:"auth"`
}

// ApplicationPullSecretCreator reconciles an Application object
type ApplicationPullSecretCreator struct {
	client.Client
	Scheme *runtime.Scheme
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApplicationPullSecretCreator) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appstudioredhatcomv1alpha1.Application{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=applications,verbs=get;list;watch;update
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=applications/finalizers,verbs=update
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch

func (r *ApplicationPullSecretCreator) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithName("Application")
	ctx = ctrllog.IntoContext(ctx, log)

	// fetch the application instance
	application := &appstudioredhatcomv1alpha1.Application{}
	err := r.Client.Get(ctx, req.NamespacedName, application)
	if err != nil {
		if errors.IsNotFound(err) {
			// The object is deleted, nothing to do
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to get application", l.Action, l.ActionView)
		return ctrl.Result{}, err
	}

	applicationPullSecretName := getApplicationPullSecretName(application.Name)

	if !application.DeletionTimestamp.IsZero() {
		// remove application secret from SA
		if err := r.unlinkApplicationSecretFromIntegrationTestsSa(ctx, applicationPullSecretName, application.Namespace); err != nil {
			return ctrl.Result{}, err
		}

		if controllerutil.ContainsFinalizer(application, ApplicationSecretLinkToSaFinalizer) {
			controllerutil.RemoveFinalizer(application, ApplicationSecretLinkToSaFinalizer)
			if err := r.Client.Update(ctx, application); err != nil {
				log.Error(err, "failed to remove application finalizer", l.Action, l.ActionUpdate)
				return ctrl.Result{}, err
			}
			log.Info("Application finalizer removed", l.Action, l.ActionDelete)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(application, ApplicationSecretLinkToSaFinalizer) {
		controllerutil.AddFinalizer(application, ApplicationSecretLinkToSaFinalizer)
		if err := r.Client.Update(ctx, application); err != nil {
			log.Error(err, "failed to add application finalizer", l.Action, l.ActionUpdate)
			return ctrl.Result{}, err
		}
		log.Info("Application finalizer added", l.Action, l.ActionDelete)
	}

	applicationPullSecretExists, err := r.doesApplicationPullSecretExist(ctx, applicationPullSecretName, application)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !applicationPullSecretExists {
		componentIds, err := r.getComponentIdsForApplication(ctx, application.UID, application.Namespace)
		if err != nil {
			return ctrl.Result{}, err
		}

		pullSecretNames, err := r.getImageRepositoryPullSecretNamesForComponents(ctx, componentIds, application.Namespace)
		if err != nil {
			return ctrl.Result{}, err
		}

		if err := r.createApplicationPullSecret(ctx, applicationPullSecretName, application, pullSecretNames); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.updateServiceAccountWithApplicationPullSecret(ctx, applicationPullSecretName, application.Namespace); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// getApplicationPullSecretName returns name for the application pull dockerconfigjson secret
func getApplicationPullSecretName(applicationName string) string {
	return fmt.Sprintf("%s-pull", applicationName)
}

// getComponentIdsForApplication returns components id for all components owned by the application
func (r *ApplicationPullSecretCreator) getComponentIdsForApplication(ctx context.Context, applicationId types.UID, namespace string) ([]types.UID, error) {
	log := ctrllog.FromContext(ctx)
	componentsList := &appstudioredhatcomv1alpha1.ComponentList{}
	if err := r.Client.List(ctx, componentsList, &client.ListOptions{Namespace: namespace}); err != nil {
		log.Error(err, "failed to list components")
		return nil, err
	}

	allComponentsId := []types.UID{}
	for _, component := range componentsList.Items {
		for _, owner := range component.ObjectMeta.OwnerReferences {
			if owner.UID == applicationId {
				allComponentsId = append(allComponentsId, component.UID)
				break
			}
		}
	}

	return allComponentsId, nil
}

// getImageRepositoryPullSecretNamesForComponents returns pull secret names from imagerepositories owned by provided components
func (r *ApplicationPullSecretCreator) getImageRepositoryPullSecretNamesForComponents(ctx context.Context, componentIds []types.UID, namespaceName string) ([]string, error) {
	log := ctrllog.FromContext(ctx)
	imageRepositoryList := &imagerepositoryv1alpha1.ImageRepositoryList{}
	if err := r.Client.List(ctx, imageRepositoryList, &client.ListOptions{Namespace: namespaceName}); err != nil {
		log.Error(err, "failed to list ImageRepositories", l.Action, l.ActionView)
		return nil, err
	}

	pullSecretNames := []string{}
	for _, imageRepository := range imageRepositoryList.Items {
		for _, owner := range imageRepository.ObjectMeta.OwnerReferences {
			found := false
			for _, componentId := range componentIds {
				if owner.UID == componentId {
					if imageRepository.Status.Credentials.PullSecretName != "" {
						pullSecretNames = append(pullSecretNames, imageRepository.Status.Credentials.PullSecretName)
					}
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
	return pullSecretNames, nil
}

// createApplicationPullSecret creates or updates a single kubernetes.io/dockerconfigjson secret
// by combining data from individual pull secrets.
func (r *ApplicationPullSecretCreator) createApplicationPullSecret(ctx context.Context, applicationPullSecretName string, application *appstudioredhatcomv1alpha1.Application, individualSecretNames []string) error {
	log := ctrllog.FromContext(ctx)

	log.Info("Creating application pull secret", "secretName", applicationPullSecretName)
	combinedAuths := dockerConfigJson{Auths: map[string]dockerConfigAuth{}}

	secretList := &corev1.SecretList{}
	if err := r.Client.List(ctx, secretList, &client.ListOptions{Namespace: application.Namespace}); err != nil {
		log.Error(err, "failed to list secrets", l.Action, l.ActionView)
		return err
	}

	for _, secret := range secretList.Items {
		shouldProcess := false
		for _, name := range individualSecretNames {
			if secret.Name == name {
				shouldProcess = true
				break
			}
		}
		if !shouldProcess {
			continue
		}

		// Only process secrets of type kubernetes.io/dockerconfigjson
		if secret.Type != corev1.SecretTypeDockerConfigJson {
			continue
		}

		// Secret missing .dockerconfigjson key
		dockerConfigDataBytes, ok := secret.Data[corev1.DockerConfigJsonKey]
		if !ok {
			continue
		}

		var dcj dockerConfigJson
		if err := json.Unmarshal(dockerConfigDataBytes, &dcj); err != nil {
			log.Error(err, "failed to unmarshal .dockerconfigjson data from secret", "secretName", secret.Name)
			continue
		}

		for registry, authEntry := range dcj.Auths {
			combinedAuths.Auths[registry] = authEntry
		}
	}

	// Marshal combined auths back into .dockerconfigjson format
	combinedDockerConfig := dockerConfigJson{Auths: combinedAuths.Auths}
	marshaledData, err := json.Marshal(combinedDockerConfig)
	if err != nil {
		log.Error(err, "failed to marshal combined docker config json")
		return err
	}

	// Create the application pull secret
	applicationPullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      applicationPullSecretName,
			Namespace: application.Namespace,
			Labels: map[string]string{
				InternalSecretLabelName: "true",
			},
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: marshaledData,
		},
	}

	// Set pull secret ownership to application
	if err := controllerutil.SetOwnerReference(application, applicationPullSecret, r.Scheme); err != nil {
		log.Error(err, "failed to set application as owner of application pull secret", "applicationName", application.Name)
		return err
	}

	if err := r.Client.Create(ctx, applicationPullSecret); err != nil {
		log.Error(err, "failed to create application pull secret", "secretName", applicationPullSecretName, l.Action, l.ActionAdd)
		return err
	}

	log.Info("Application pull secret created successfully", "secretName", applicationPullSecretName)
	return nil
}

// updateServiceAccountWithApplicationPullSecret updates the ServiceAccount to include
// the application pull secret as an imagePullSecret and as a Secret
func (r *ApplicationPullSecretCreator) updateServiceAccountWithApplicationPullSecret(ctx context.Context, applicationPullSecretName string, namespace string) error {
	log := ctrllog.FromContext(ctx)

	// fetch namespace SA
	namespaceServiceAccount := &corev1.ServiceAccount{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: IntegrationTestsServiceAccountName, Namespace: namespace}, namespaceServiceAccount); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Namespace ServiceAccount not found", "serviceAccountName", IntegrationTestsServiceAccountName, "namespace", namespace)
			return nil
		}
		log.Error(err, "failed to read namespace ServiceAccount", "serviceAccountName", IntegrationTestsServiceAccountName, "namespace", namespace, l.Action, l.ActionView)
		return err
	}

	// Check and update Secrets
	secretLinked := false
	shouldUpdateServiceAccount := false
	for _, secret := range namespaceServiceAccount.Secrets {
		if secret.Name == applicationPullSecretName {
			secretLinked = true
			break
		}
	}
	if !secretLinked {
		namespaceServiceAccount.Secrets = append(namespaceServiceAccount.Secrets, corev1.ObjectReference{Name: applicationPullSecretName})
		shouldUpdateServiceAccount = true
	}

	// Check and update imagePullSecrets
	secretLinked = false
	for _, pullSecret := range namespaceServiceAccount.ImagePullSecrets {
		if pullSecret.Name == applicationPullSecretName {
			secretLinked = true
			break
		}
	}

	if !secretLinked {
		namespaceServiceAccount.ImagePullSecrets = append(namespaceServiceAccount.ImagePullSecrets, corev1.LocalObjectReference{Name: applicationPullSecretName})
		shouldUpdateServiceAccount = true
	}

	if shouldUpdateServiceAccount {
		if err := r.Client.Update(ctx, namespaceServiceAccount); err != nil {
			log.Error(err, "failed to update Service Account with application pull secret", l.Action, l.ActionUpdate)
			return err
		}
		log.Info("Service Account updated successfully with application pull secret.", "secretName", applicationPullSecretName)
	}

	return nil
}

func (r *ApplicationPullSecretCreator) doesApplicationPullSecretExist(ctx context.Context, applicationPullSecretName string, application *appstudioredhatcomv1alpha1.Application) (bool, error) {
	log := ctrllog.FromContext(ctx)

	applicationPullSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: applicationPullSecretName, Namespace: application.Namespace}, applicationPullSecret); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}

		log.Error(err, "failed to get application pull secret", "secretName", applicationPullSecretName, l.Action, l.ActionView)
		return false, err
	}

	return true, nil
}

// unlinkApplicationSecretFromIntegrationTestsSa ensures that the given secret is not linked with the integration tests service account.
func (r *ApplicationPullSecretCreator) unlinkApplicationSecretFromIntegrationTestsSa(ctx context.Context, secretNameToRemove, namespace string) error {
	log := ctrllog.FromContext(ctx).WithValues("ServiceAccountName", IntegrationTestsServiceAccountName, "SecretName", secretNameToRemove)

	serviceAccount := &corev1.ServiceAccount{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: IntegrationTestsServiceAccountName, Namespace: namespace}, serviceAccount)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "failed to read namespace service account", l.Action, l.ActionView)
		return err
	}

	shouldUpdate := false
	// Remove secret from secrets list
	pushSecrets := []corev1.ObjectReference{}
	for _, credentialSecret := range serviceAccount.Secrets {
		// don't break and search for duplicities
		if credentialSecret.Name == secretNameToRemove {
			shouldUpdate = true
			continue
		}
		pushSecrets = append(pushSecrets, credentialSecret)
	}
	serviceAccount.Secrets = pushSecrets

	// Remove secret from pull secrets list
	imagePullSecrets := []corev1.LocalObjectReference{}
	for _, pullSecret := range serviceAccount.ImagePullSecrets {
		// don't break and search for duplicities
		if pullSecret.Name == secretNameToRemove {
			shouldUpdate = true
			continue
		}
		imagePullSecrets = append(imagePullSecrets, pullSecret)
	}
	serviceAccount.ImagePullSecrets = imagePullSecrets

	if shouldUpdate {
		if err := r.Client.Update(ctx, serviceAccount); err != nil {
			log.Error(err, "failed to update service account", l.Action, l.ActionUpdate)
			return err
		}
		log.Info("Removed secret link from service account", l.Action, l.ActionUpdate)
	}

	return nil
}
