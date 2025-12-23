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
	"encoding/base64"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	imagerepositoryv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	"github.com/konflux-ci/image-controller/pkg/quay"
)

var _ = Describe("Application controller", func() {
	var appSecretTestNamespace = "application-secret-namespace-test"
	var applicationKey = types.NamespacedName{Name: "application-test", Namespace: appSecretTestNamespace}
	var component1Key = types.NamespacedName{Name: "component1-test", Namespace: appSecretTestNamespace}
	var component2Key = types.NamespacedName{Name: "component2-test", Namespace: appSecretTestNamespace}
	var imageRepository1Key = types.NamespacedName{Name: "ir1-test", Namespace: appSecretTestNamespace}
	var imageRepository2Key = types.NamespacedName{Name: "ir2-test", Namespace: appSecretTestNamespace}
	var namespacePullSecretName = types.NamespacedName{Name: getApplicationPullSecretName(applicationKey.Name), Namespace: appSecretTestNamespace}
	var pullSecret1 = "pull-secret1"
	var pushSecret1 = "push-secret1"
	var pullSecret2 = "pull-secret2"
	var pushSecret2 = "push-secret2"
	var authSecret1 = "user1:pass1"
	var authSecret2 = "user2:pass2"
	var registrySecret1 = "registry1.example.com"
	var registrySecret2 = "registry2.example.com"

	Context("Create application secret and link it to Integration ServiceAccount when it exists", func() {
		BeforeEach(func() {
			quay.ResetTestQuayClient()
		})

		AfterEach(func() {
			deleteServiceAccount(types.NamespacedName{Name: IntegrationServiceAccountName, Namespace: appSecretTestNamespace})
			deleteSecret(namespacePullSecretName)
			deleteImageRepository(imageRepository2Key)
			deleteImageRepository(imageRepository1Key)
			deleteComponent(component1Key)
			deleteComponent(component2Key)
			deleteApplication(applicationKey)
			deleteSecret(types.NamespacedName{Name: pullSecret1, Namespace: appSecretTestNamespace})
			deleteSecret(types.NamespacedName{Name: pullSecret2, Namespace: appSecretTestNamespace})
		})

		It("should prepare environment", func() {
			createNamespace(appSecretTestNamespace)
		})

		It("should create empty application pull secret and without link it to not existing namespace SA, because no components are owned by application", func() {
			createComponent(componentConfig{ComponentKey: component1Key, ComponentApplication: applicationKey.Name})
			createApplication(applicationConfig{ApplicationKey: applicationKey})

			// wait until application secret is created
			applicationSecret := waitSecretExist(namespacePullSecretName)

			// verify that namespace SA doesn't exist
			saList := getServiceAccountList(appSecretTestNamespace)
			Expect(len(saList)).Should(Equal(0))

			applicationSecretDockerConfigJson := string(applicationSecret.Data[corev1.DockerConfigJsonKey])

			var decodedSecret dockerConfigJson
			Expect(json.Unmarshal([]byte(applicationSecretDockerConfigJson), &decodedSecret)).To(Succeed())
			Expect(len(decodedSecret.Auths)).Should(Equal(0))
		})

		It("should create empty application pull secret and link it to namespace SA, because no components are owned by application", func() {
			createServiceAccount(appSecretTestNamespace, IntegrationServiceAccountName)
			createComponent(componentConfig{ComponentKey: component1Key, ComponentApplication: applicationKey.Name})
			createApplication(applicationConfig{ApplicationKey: applicationKey})

			// wait until empty secret is linked to namespace SA
			Eventually(func() bool {
				saList := getServiceAccountList(appSecretTestNamespace)
				saCount := len(saList)
				secretsCount := 0
				pullSecretsCount := 0
				if saCount > 0 {
					secretsCount = len(saList[0].Secrets)
					pullSecretsCount = len(saList[0].ImagePullSecrets)
				}
				return saCount == 1 && secretsCount == 1 && pullSecretsCount == 1
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			saList := getServiceAccountList(appSecretTestNamespace)
			Expect(len(saList)).Should(Equal(1))
			Expect(saList[0].Name).Should(Equal(IntegrationServiceAccountName))
			Expect(len(saList[0].Secrets)).Should(Equal(1))
			Expect(len(saList[0].ImagePullSecrets)).Should(Equal(1))

			applicationSecret := waitSecretExist(namespacePullSecretName)
			applicationSecretDockerConfigJson := string(applicationSecret.Data[corev1.DockerConfigJsonKey])

			var decodedSecret dockerConfigJson
			Expect(json.Unmarshal([]byte(applicationSecretDockerConfigJson), &decodedSecret)).To(Succeed())
			Expect(len(decodedSecret.Auths)).Should(Equal(0))
		})

		It("should create an application pull secret with 2 secrets and link it to namespace SA", func() {
			createServiceAccount(appSecretTestNamespace, IntegrationServiceAccountName)
			pullSecret1Data := generateDockerConfigJson(registrySecret1, "user1", "pass1")
			pullSecret1Key := types.NamespacedName{Name: pullSecret1, Namespace: appSecretTestNamespace}
			createDockerConfigSecret(pullSecret1Key, pullSecret1Data, true)

			pullSecret2Data := generateDockerConfigJson(registrySecret2, "user2", "pass2")
			pullSecret2Key := types.NamespacedName{Name: pullSecret2, Namespace: appSecretTestNamespace}
			createDockerConfigSecret(pullSecret2Key, pullSecret2Data, true)

			// will trigger application controller and create empty secret
			application := createApplication(applicationConfig{ApplicationKey: applicationKey})
			component1 := createComponent(componentConfig{ComponentKey: component1Key, ComponentApplication: applicationKey.Name})
			component2 := createComponent(componentConfig{ComponentKey: component2Key, ComponentApplication: applicationKey.Name})

			// wait until empty secret is linked to namespace SA
			Eventually(func() bool {
				saList := getServiceAccountList(appSecretTestNamespace)
				saCount := len(saList)
				secretsCount := 0
				pullSecretsCount := 0
				if saCount > 0 {
					secretsCount = len(saList[0].Secrets)
					pullSecretsCount = len(saList[0].ImagePullSecrets)
				}
				return saCount == 1 && secretsCount == 1 && pullSecretsCount == 1
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			// delete application SA and empty secret as it will be created on next reconcile
			deleteServiceAccount(types.NamespacedName{Name: IntegrationServiceAccountName, Namespace: appSecretTestNamespace})
			deleteSecret(namespacePullSecretName)
			// recreate empty SA
			createServiceAccount(appSecretTestNamespace, IntegrationServiceAccountName)

			// set component's owner to application
			component1.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: "appstudio.redhat.com/v1alpha1",
				Kind:       "Application",
				Name:       application.Name,
				UID:        application.UID,
			}}
			component2.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: "appstudio.redhat.com/v1alpha1",
				Kind:       "Application",
				Name:       application.Name,
				UID:        application.UID,
			}}
			Expect(k8sClient.Update(ctx, component1)).To(Succeed())
			Expect(k8sClient.Update(ctx, component2)).To(Succeed())

			// create image repository with finalizer so it won't try to provision repo
			// also without component & application labels so controller won't try any secrets linking
			imageConfig1 := imageRepositoryConfig{
				ResourceKey: &imageRepository1Key,
				Finalizers:  []string{ImageRepositoryFinalizer},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
					Name:       component1.Name,
					UID:        component1.UID,
				}},
			}
			imageConfig2 := imageRepositoryConfig{
				ResourceKey: &imageRepository2Key,
				Finalizers:  []string{ImageRepositoryFinalizer},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
					Name:       component2.Name,
					UID:        component2.UID,
				}},
			}
			ir1 := createImageRepository(imageConfig1)
			ir2 := createImageRepository(imageConfig2)

			// set image repository state to failed so controller will just stop
			// set also pull & push secret
			ir1.Status.State = imagerepositoryv1alpha1.ImageRepositoryStateFailed
			ir1.Status.Credentials.PullSecretName = pullSecret1
			ir1.Status.Credentials.PushSecretName = pushSecret1
			ir2.Status.State = imagerepositoryv1alpha1.ImageRepositoryStateFailed
			ir2.Status.Credentials.PullSecretName = pullSecret2
			ir2.Status.Credentials.PushSecretName = pushSecret2
			Expect(k8sClient.Status().Update(ctx, ir1)).To(Succeed())
			Expect(k8sClient.Status().Update(ctx, ir2)).To(Succeed())

			// set image repository component & application labels
			ir1.Labels = map[string]string{
				ApplicationNameLabelName: applicationKey.Name,
				ComponentNameLabelName:   component1.Name,
			}
			ir2.Labels = map[string]string{
				ApplicationNameLabelName: applicationKey.Name,
				ComponentNameLabelName:   component2.Name,
			}
			Expect(k8sClient.Update(ctx, ir1)).To(Succeed())
			Expect(k8sClient.Update(ctx, ir2)).To(Succeed())

			// Trigger reconciliation by updating the application
			// get application again because controller updated it with finalizer
			application = getApplication(applicationKey)
			application.Spec.DisplayName = "updated-name"
			Expect(k8sClient.Update(ctx, application)).To(Succeed())

			// wait until empty secret is linked to namespace SA
			Eventually(func() bool {
				saList := getServiceAccountList(appSecretTestNamespace)
				saCount := len(saList)
				secretsCount := 0
				pullSecretsCount := 0
				if saCount > 0 {
					secretsCount = len(saList[0].Secrets)
					pullSecretsCount = len(saList[0].ImagePullSecrets)
				}
				return saCount == 1 && secretsCount == 1 && pullSecretsCount == 1
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			applicationSecret := waitSecretExist(namespacePullSecretName)

			Expect(applicationSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			Expect(applicationSecret.OwnerReferences).To(HaveLen(1))
			Expect(applicationSecret.OwnerReferences[0].Name).To(Equal(applicationKey.Name))
			Expect(applicationSecret.Data).To(HaveKey(corev1.DockerConfigJsonKey))

			applicationSecretDockerConfigJson := string(applicationSecret.Data[corev1.DockerConfigJsonKey])
			var decodedSecret dockerConfigJson
			Expect(json.Unmarshal([]byte(applicationSecretDockerConfigJson), &decodedSecret)).To(Succeed())

			Expect(decodedSecret.Auths).To(HaveLen(2))
			Expect(decodedSecret.Auths).To(HaveKey(registrySecret1))
			Expect(decodedSecret.Auths[registrySecret1].Auth).To(Equal(base64.StdEncoding.EncodeToString([]byte(authSecret1))))
			Expect(decodedSecret.Auths).To(HaveKey(registrySecret2))
			Expect(decodedSecret.Auths[registrySecret2].Auth).To(Equal(base64.StdEncoding.EncodeToString([]byte(authSecret2))))

			konfluxSA := getServiceAccount(appSecretTestNamespace, IntegrationServiceAccountName)
			Expect(konfluxSA.ImagePullSecrets).To(HaveLen(1))
			Expect(konfluxSA.ImagePullSecrets[0].Name).To(Equal(namespacePullSecretName.Name))
			Expect(konfluxSA.Secrets).To(HaveLen(1))
			Expect(konfluxSA.Secrets[0].Name).To(Equal(namespacePullSecretName.Name))

			// verify that after application removal application secret is no longer linked in namespace SA
			deleteApplication(applicationKey)
			konfluxSA = getServiceAccount(appSecretTestNamespace, IntegrationServiceAccountName)
			Expect(konfluxSA.ImagePullSecrets).To(HaveLen(0))
			Expect(konfluxSA.Secrets).To(HaveLen(0))
		})

		It("should create an application pull secret with 1 secret because other secret isn't SecretTypeDockerConfigJson and link it to namespace SA", func() {
			createServiceAccount(appSecretTestNamespace, IntegrationServiceAccountName)
			pullSecret1Data := generateDockerConfigJson(registrySecret1, "user1", "pass1")
			pullSecret1Key := types.NamespacedName{Name: pullSecret1, Namespace: appSecretTestNamespace}
			createDockerConfigSecret(pullSecret1Key, pullSecret1Data, true)

			pullSecret2Data := generateDockerConfigJson(registrySecret2, "user2", "pass2")
			pullSecret2Key := types.NamespacedName{Name: pullSecret2, Namespace: appSecretTestNamespace}
			createDockerConfigSecret(pullSecret2Key, pullSecret2Data, false)

			// will trigger application controller and create empty secret
			application := createApplication(applicationConfig{ApplicationKey: applicationKey})
			component1 := createComponent(componentConfig{ComponentKey: component1Key, ComponentApplication: applicationKey.Name})
			component2 := createComponent(componentConfig{ComponentKey: component2Key, ComponentApplication: applicationKey.Name})

			// wait until empty secret is linked to namespace SA
			Eventually(func() bool {
				saList := getServiceAccountList(appSecretTestNamespace)
				saCount := len(saList)
				secretsCount := 0
				pullSecretsCount := 0
				if saCount > 0 {
					secretsCount = len(saList[0].Secrets)
					pullSecretsCount = len(saList[0].ImagePullSecrets)
				}
				return saCount == 1 && secretsCount == 1 && pullSecretsCount == 1
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			// delete application SA and empty secret as it will be created on next reconcile
			deleteServiceAccount(types.NamespacedName{Name: IntegrationServiceAccountName, Namespace: appSecretTestNamespace})
			deleteSecret(namespacePullSecretName)
			// recreate empty SA
			createServiceAccount(appSecretTestNamespace, IntegrationServiceAccountName)

			// set component's owner to application
			component1.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: "appstudio.redhat.com/v1alpha1",
				Kind:       "Application",
				Name:       application.Name,
				UID:        application.UID,
			}}
			component2.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: "appstudio.redhat.com/v1alpha1",
				Kind:       "Application",
				Name:       application.Name,
				UID:        application.UID,
			}}
			Expect(k8sClient.Update(ctx, component1)).To(Succeed())
			Expect(k8sClient.Update(ctx, component2)).To(Succeed())

			// create image repository with finalizer so it won't try to provision repo
			// also without component & application labels so controller won't try any secrets linking
			imageConfig1 := imageRepositoryConfig{
				ResourceKey: &imageRepository1Key,
				Finalizers:  []string{ImageRepositoryFinalizer},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
					Name:       component1.Name,
					UID:        component1.UID,
				}},
			}
			imageConfig2 := imageRepositoryConfig{
				ResourceKey: &imageRepository2Key,
				Finalizers:  []string{ImageRepositoryFinalizer},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
					Name:       component2.Name,
					UID:        component2.UID,
				}},
			}
			ir1 := createImageRepository(imageConfig1)
			ir2 := createImageRepository(imageConfig2)

			// set image repository state to failed so controller will just stop
			// set also pull & push secret
			ir1.Status.State = imagerepositoryv1alpha1.ImageRepositoryStateFailed
			ir1.Status.Credentials.PullSecretName = pullSecret1
			ir1.Status.Credentials.PushSecretName = pushSecret1
			ir2.Status.State = imagerepositoryv1alpha1.ImageRepositoryStateFailed
			ir2.Status.Credentials.PullSecretName = pullSecret2
			ir2.Status.Credentials.PushSecretName = pushSecret2
			Expect(k8sClient.Status().Update(ctx, ir1)).To(Succeed())
			Expect(k8sClient.Status().Update(ctx, ir2)).To(Succeed())

			// set image repository component & application labels
			ir1.Labels = map[string]string{
				ApplicationNameLabelName: applicationKey.Name,
				ComponentNameLabelName:   component1.Name,
			}
			ir2.Labels = map[string]string{
				ApplicationNameLabelName: applicationKey.Name,
				ComponentNameLabelName:   component2.Name,
			}
			Expect(k8sClient.Update(ctx, ir1)).To(Succeed())
			Expect(k8sClient.Update(ctx, ir2)).To(Succeed())

			// Trigger reconciliation by updating the application
			// get application again because controller updated it with finalizer
			application = getApplication(applicationKey)
			application.Spec.DisplayName = "updated-name"
			Expect(k8sClient.Update(ctx, application)).To(Succeed())

			// wait until empty secret is linked to namespace SA
			Eventually(func() bool {
				saList := getServiceAccountList(appSecretTestNamespace)
				saCount := len(saList)
				secretsCount := 0
				pullSecretsCount := 0
				if saCount > 0 {
					secretsCount = len(saList[0].Secrets)
					pullSecretsCount = len(saList[0].ImagePullSecrets)
				}
				return saCount == 1 && secretsCount == 1 && pullSecretsCount == 1
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			applicationSecret := waitSecretExist(namespacePullSecretName)

			Expect(applicationSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			Expect(applicationSecret.OwnerReferences).To(HaveLen(1))
			Expect(applicationSecret.OwnerReferences[0].Name).To(Equal(applicationKey.Name))
			Expect(applicationSecret.Data).To(HaveKey(corev1.DockerConfigJsonKey))

			applicationSecretDockerConfigJson := string(applicationSecret.Data[corev1.DockerConfigJsonKey])
			var decodedSecret dockerConfigJson
			Expect(json.Unmarshal([]byte(applicationSecretDockerConfigJson), &decodedSecret)).To(Succeed())

			Expect(decodedSecret.Auths).To(HaveLen(1))
			Expect(decodedSecret.Auths).To(HaveKey(registrySecret1))
			Expect(decodedSecret.Auths[registrySecret1].Auth).To(Equal(base64.StdEncoding.EncodeToString([]byte(authSecret1))))

			konfluxSA := getServiceAccount(appSecretTestNamespace, IntegrationServiceAccountName)
			Expect(konfluxSA.ImagePullSecrets).To(HaveLen(1))
			Expect(konfluxSA.ImagePullSecrets[0].Name).To(Equal(namespacePullSecretName.Name))
			Expect(konfluxSA.Secrets).To(HaveLen(1))
			Expect(konfluxSA.Secrets[0].Name).To(Equal(namespacePullSecretName.Name))
		})

	})
})
