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
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	imagerepositoryv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	l "github.com/konflux-ci/image-controller/pkg/logs"
)

// linkSecretToServiceAccount ensures that the given secret is linked with the provided service account,
// add also to ImagePullSecrets if requested
func (r *ImageRepositoryReconciler) linkSecretToServiceAccount(ctx context.Context, saName, secretNameToAdd, namespace string, addImagePullSecret bool) error {
	log := ctrllog.FromContext(ctx).WithValues("ServiceAccountName", saName, "SecretName", secretNameToAdd)

	serviceAccount := &corev1.ServiceAccount{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: saName, Namespace: namespace}, serviceAccount)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Error(err, "service account doesn't exist yet", l.Action, l.ActionView)
			return err
		}
		log.Error(err, "failed to read service account", l.Action, l.ActionView)
		return err
	}

	// check if secret is already linked and add it only if it isn't to avoid duplication
	secretLinked := false
	shouldUpdateServiceAccount := false
	for _, serviceAccountSecret := range serviceAccount.Secrets {
		if serviceAccountSecret.Name == secretNameToAdd {
			secretLinked = true
			break
		}
	}
	if !secretLinked {
		serviceAccount.Secrets = append(serviceAccount.Secrets, corev1.ObjectReference{Name: secretNameToAdd})
		shouldUpdateServiceAccount = true
	}

	secretLinked = false
	if addImagePullSecret {
		for _, serviceAccountSecret := range serviceAccount.ImagePullSecrets {
			if serviceAccountSecret.Name == secretNameToAdd {
				secretLinked = true
				break
			}
		}
	} else {
		secretLinked = true
	}
	if !secretLinked {
		serviceAccount.ImagePullSecrets = append(serviceAccount.ImagePullSecrets, corev1.LocalObjectReference{Name: secretNameToAdd})
		shouldUpdateServiceAccount = true
	}

	if shouldUpdateServiceAccount {
		if err := r.Client.Update(ctx, serviceAccount); err != nil {
			log.Error(err, "failed to update service account", l.Action, l.ActionUpdate)
			return err
		}
		log.Info("Added secret link to service account", l.Action, l.ActionUpdate)
	}

	return nil
}

// unlinkSecretFromServiceAccount ensures that the given secret is not linked with the provided service account.
func (r *ImageRepositoryReconciler) unlinkSecretFromServiceAccount(ctx context.Context, saName, secretNameToRemove, namespace string) error {
	log := ctrllog.FromContext(ctx).WithValues("ServiceAccountName", saName, "SecretName", secretNameToRemove)

	serviceAccount := &corev1.ServiceAccount{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: saName, Namespace: namespace}, serviceAccount)
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
			log.Error(err, "failed to update service account", l.Action, l.ActionUpdate)
			return err
		}
		log.Info("Removed secret link from service account", l.Action, l.ActionUpdate)
	}

	return nil
}

// unlinkPullSecretFromNudgedComponentSAs unlinks pull secret for nudging component from nudged components SA
func (r *ImageRepositoryReconciler) unlinkPullSecretFromNudgedComponentSAs(ctx context.Context, secretName, namespace string) error {
	log := ctrllog.FromContext(ctx)

	serviceAccountList := &corev1.ServiceAccountList{}
	if err := r.Client.List(ctx, serviceAccountList, &client.ListOptions{Namespace: namespace}); err != nil {
		log.Error(err, "failed to list service accounts")
		return err
	}

	for _, serviceAccount := range serviceAccountList.Items {
		// check only service account from components
		if !strings.HasPrefix(serviceAccount.Name, componentSaNamePrefix) {
			continue
		}

		for _, secret := range serviceAccount.Secrets {
			if secret.Name == secretName {
				if err := r.unlinkSecretFromServiceAccount(ctx, serviceAccount.Name, secretName, namespace); err != nil {
					log.Error(err, "failed to unlink pull secret from service account", "SaName", serviceAccount.Name, "SecretName", secretName, l.Action, l.ActionUpdate)
					return err
				}
				break
			}
		}
	}
	return nil
}

// cleanUpSecretInServiceAccount ensures that the given secret is linked with the provided service account just once
// and remove the secret from ImagePullSecrets unless requested to keep
func (r *ImageRepositoryReconciler) cleanUpSecretInServiceAccount(ctx context.Context, saName, secretName, namespace string, keepImagePullSecrets bool) error {
	log := ctrllog.FromContext(ctx).WithValues("ServiceAccountName", saName)

	serviceAccount := &corev1.ServiceAccount{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: saName, Namespace: namespace}, serviceAccount)
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

	// Remove secret from pull secrets list unless requested to keep
	imagePullSecrets := []corev1.LocalObjectReference{}
	foundSecret = false
	for _, pullSecret := range serviceAccount.ImagePullSecrets {
		if pullSecret.Name == secretName {
			if keepImagePullSecrets {
				if !foundSecret {
					imagePullSecrets = append(imagePullSecrets, pullSecret)
					foundSecret = true
				} else {
					linksModified = true
				}
			} else {
				linksModified = true
			}
		} else {
			imagePullSecrets = append(imagePullSecrets, pullSecret)
		}
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
func (r *ImageRepositoryReconciler) VerifyAndFixSecretsLinking(ctx context.Context, imageRepository *imagerepositoryv1alpha1.ImageRepository) error {
	log := ctrllog.FromContext(ctx)

	componentSaName := getComponentSaName(imageRepository.Labels[ComponentNameLabelName])
	pushSecretName := imageRepository.Status.Credentials.PushSecretName
	applicationName := imageRepository.Labels[ApplicationNameLabelName]
	applicationPullSecretName := getApplicationPullSecretName(applicationName)

	if isComponentLinked(imageRepository) {
		// link secret to service account if isn't linked already
		if err := r.linkSecretToServiceAccount(ctx, componentSaName, pushSecretName, imageRepository.Namespace, false); err != nil {
			log.Error(err, "failed to link secret to service account", componentSaName, "SecretName", pushSecretName, l.Action, l.ActionUpdate)
			return err
		}

		// clean duplicate secret links and remove secret from ImagePullSecrets
		if err := r.cleanUpSecretInServiceAccount(ctx, componentSaName, pushSecretName, imageRepository.Namespace, false); err != nil {
			log.Error(err, "failed to clean up secret in service account", "saName", componentSaName, "SecretName", pushSecretName, l.Action, l.ActionUpdate)
			return err
		}

		// link secret to service account if isn't linked already
		if err := r.linkSecretToServiceAccount(ctx, IntegrationTestsServiceAccountName, applicationPullSecretName, imageRepository.Namespace, true); err != nil {
			log.Error(err, "failed to link secret to service account", "saName", IntegrationTestsServiceAccountName, "SecretName", applicationPullSecretName, l.Action, l.ActionUpdate)
			return err
		}

		// clean duplicate secret links
		if err := r.cleanUpSecretInServiceAccount(ctx, IntegrationTestsServiceAccountName, applicationPullSecretName, imageRepository.Namespace, true); err != nil {
			log.Error(err, "failed to clean up secret in service account", "saName", IntegrationTestsServiceAccountName, "SecretName", applicationPullSecretName, l.Action, l.ActionUpdate)
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
