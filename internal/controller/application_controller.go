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

// remove after component v2 migration - entire file
// This controller is only used with Application CRD and old component model.
// With component v2, Application CRD won't be used, so this can be removed.

package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	compapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	l "github.com/konflux-ci/image-controller/pkg/logs"
)

const (
	IntegrationServiceAccountName      = "konflux-integration-runner"
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
		For(&compapiv1alpha1.Application{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=applications,verbs=get;list;watch;update
//+kubebuilder:rbac:groups=appstudio.redhat.com,resources=applications/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch

func (r *ApplicationPullSecretCreator) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithName("Application")
	ctx = ctrllog.IntoContext(ctx, log)

	// fetch the application instance
	application := &compapiv1alpha1.Application{}
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

	return ctrl.Result{}, nil
}

// getApplicationPullSecretName returns name for the application pull dockerconfigjson secret
func getApplicationPullSecretName(applicationName string) string {
	return fmt.Sprintf("%s-pull", applicationName)
}

// unlinkApplicationSecretFromIntegrationTestsSa ensures that the given secret is not linked with the integration tests service account.
//
//nolint:dupl // This is deprecated controller, no need to refactor.
func (r *ApplicationPullSecretCreator) unlinkApplicationSecretFromIntegrationTestsSa(ctx context.Context, secretNameToRemove, namespace string) error {
	log := ctrllog.FromContext(ctx).WithValues("ServiceAccountName", IntegrationServiceAccountName, "SecretName", secretNameToRemove)

	serviceAccount := &corev1.ServiceAccount{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: IntegrationServiceAccountName, Namespace: namespace}, serviceAccount)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "failed to read integration service account", l.Action, l.ActionView)
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
