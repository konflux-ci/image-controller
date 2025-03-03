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

	imagerepositoryv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	l "github.com/konflux-ci/image-controller/pkg/logs"
	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
)

// ApplicationPullServiceAccountCreator reconciles a Application object
type ApplicationPullServiceAccountCreator struct {
	client.Client
	Scheme *runtime.Scheme
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApplicationPullServiceAccountCreator) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appstudioredhatcomv1alpha1.Application{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=applications,verbs=get;list;watch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=components,verbs=get;list;watch
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=imagerepositories,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch

func (r *ApplicationPullServiceAccountCreator) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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

	// do nothing when application is to be deleted
	if !application.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// fetch application SA
	applicationSaName := getApplicationSaName(application.Name)
	applicationServiceAccount := &corev1.ServiceAccount{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: applicationSaName, Namespace: application.Namespace}, applicationServiceAccount)
	if err == nil {
		// service account already exists nothing to do
		return ctrl.Result{}, nil
	}
	if !errors.IsNotFound(err) {
		log.Error(err, "failed to read application service account", "serviceAccountName", applicationSaName, "namespace", application.Namespace, l.Action, l.ActionView)
		return ctrl.Result{}, err
	}

	componentIds, err := r.getComponentIdsForApplication(ctx, application.UID, application.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	pullSecretNames, err := r.getImageRepositoryPullSecretNamesForComponents(ctx, componentIds, application.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	// service account doesn't exist we will have to create it
	err = r.createApplicationServiceAccount(ctx, applicationSaName, application, pullSecretNames)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// getApplicationSaName returns name of application SA
func getApplicationSaName(applicationName string) string {
	return fmt.Sprintf("%s-pull", applicationName)
}

// getComponentIdsForApplication returns components id for all components owned by the application
func (r *ApplicationPullServiceAccountCreator) getComponentIdsForApplication(ctx context.Context, applicationId types.UID, namespace string) ([]types.UID, error) {
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
func (r *ApplicationPullServiceAccountCreator) getImageRepositoryPullSecretNamesForComponents(ctx context.Context, componentIds []types.UID, namespaceName string) ([]string, error) {
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

// createApplicationServiceAccount creates application service account with provided pull secrets
func (r *ApplicationPullServiceAccountCreator) createApplicationServiceAccount(ctx context.Context, serviceAccountName string, application *appstudioredhatcomv1alpha1.Application, pullSecretNames []string) error {
	log := ctrllog.FromContext(ctx).WithValues("serviceAccountName", serviceAccountName, "namespace", application.Namespace)

	applicationServiceAccount := &corev1.ServiceAccount{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: serviceAccountName, Namespace: application.Namespace}, applicationServiceAccount)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "failed to read application service account", l.Action, l.ActionView)
			return err
		}
	} else {
		return nil
	}

	// Create service account for application
	secretsReferences := []corev1.ObjectReference{}
	imagePullSecretsReferences := []corev1.LocalObjectReference{}
	for _, secretName := range pullSecretNames {
		secretsReferences = append(secretsReferences, corev1.ObjectReference{Name: secretName})
		imagePullSecretsReferences = append(imagePullSecretsReferences, corev1.LocalObjectReference{Name: secretName})
	}

	applicationSA := corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: application.Namespace,
		},
		Secrets:          secretsReferences,
		ImagePullSecrets: imagePullSecretsReferences,
	}

	// set serviceAccount ownership to application
	if err := controllerutil.SetOwnerReference(application, &applicationSA, r.Scheme); err != nil {
		log.Error(err, "failed to set application as owner of SA", "applicationName", application.Name)
		return err
	}

	if err := r.Client.Create(ctx, &applicationSA); err != nil {
		log.Error(err, "failed to create service account", "serviceAccountName", l.Action, l.ActionAdd)
		return err
	}

	log.Info("application service account created", "SecretNames", pullSecretNames)
	return nil
}
