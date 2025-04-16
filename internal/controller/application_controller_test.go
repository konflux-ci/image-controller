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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	imagerepositoryv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	"github.com/konflux-ci/image-controller/pkg/quay"
)

var _ = Describe("Application controller", func() {
	Context("Application service account doesn't exist", func() {
		var appSaTestNamespace = "application-sa-namespace-test"
		var applicationKey = types.NamespacedName{Name: "application-sa-test", Namespace: appSaTestNamespace}
		var component1Key = types.NamespacedName{Name: "component1-test", Namespace: appSaTestNamespace}
		var component2Key = types.NamespacedName{Name: "component2-test", Namespace: appSaTestNamespace}
		var imageRepository1Key = types.NamespacedName{Name: "ir1-test", Namespace: appSaTestNamespace}
		var imageRepository2Key = types.NamespacedName{Name: "ir2-test", Namespace: appSaTestNamespace}
		var pullSecret1 = "pull-secret1"
		var pushSecret1 = "push-secret1"
		var pullSecret2 = "pull-secret2"
		var pushSecret2 = "push-secret2"
		var applicationSaName = getApplicationSaName(applicationKey.Name)

		BeforeEach(func() {
			quay.ResetTestQuayClient()
		})

		AfterEach(func() {
			deleteImageRepository(imageRepository2Key)
			deleteImageRepository(imageRepository1Key)
			deleteComponent(component1Key)
			deleteComponent(component2Key)
			deleteApplication(applicationKey)
		})

		It("should prepare environment", func() {
			createNamespace(appSaTestNamespace)
		})

		It("application SA will be created with no secrets because no components are owned by application", func() {
			createComponent(componentConfig{ComponentKey: component1Key, ComponentApplication: applicationKey.Name})
			createApplication(applicationConfig{ApplicationKey: applicationKey})

			// wait until application SA is created with no secrets
			Eventually(func() bool {
				saList := getServiceAccountList(appSaTestNamespace)
				saCount := len(saList)
				secretsCount := 0
				if saCount > 0 {
					secretsCount = len(saList[0].Secrets)
				}
				return saCount == 1 && secretsCount == 0
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			saList := getServiceAccountList(appSaTestNamespace)
			Expect(len(saList)).Should(Equal(1))
			Expect(saList[0].Name).Should(Equal(applicationSaName))
			Expect(len(saList[0].Secrets)).Should(Equal(0))
			Expect(len(saList[0].ImagePullSecrets)).Should(Equal(0))
			Expect(len(saList[0].OwnerReferences)).Should(Equal(1))
			Expect(saList[0].OwnerReferences[0].Name).Should(Equal(applicationKey.Name))
		})

		It("application SA will be created with no secrets because component owned by application doesn't own any image repository", func() {
			component1 := createComponent(componentConfig{ComponentKey: component1Key, ComponentApplication: applicationKey.Name})

			// create image repository with finalizer so it won't try to provision repo
			imageConfig1 := imageRepositoryConfig{
				ResourceKey: &imageRepository1Key,
				Finalizers:  []string{ImageRepositoryFinalizer},
			}
			ir1 := createImageRepository(imageConfig1)

			// set image repository state to failed so controller will just stop
			// set also pull & push secret
			ir1.Status.State = imagerepositoryv1alpha1.ImageRepositoryStateFailed
			ir1.Status.Credentials.PullSecretName = pullSecret1
			ir1.Status.Credentials.PushSecretName = pushSecret1
			Expect(k8sClient.Status().Update(ctx, ir1)).To(Succeed())

			application := createApplication(applicationConfig{ApplicationKey: applicationKey})

			// wait until application SA is created with no secrets
			Eventually(func() bool {
				saList := getServiceAccountList(appSaTestNamespace)
				saCount := len(saList)
				secretsCount := 0
				if saCount > 0 {
					secretsCount = len(saList[0].Secrets)
				}
				return saCount == 1 && secretsCount == 0
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			// set component's owner to application
			component1.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: "appstudio.redhat.com/v1alpha1",
				Kind:       "Application",
				Name:       application.Name,
				UID:        application.UID,
			}}
			Expect(k8sClient.Update(ctx, component1)).To(Succeed())

			// delete application service account so it gets created again and with secret
			deleteServiceAccount(types.NamespacedName{Name: applicationSaName, Namespace: appSaTestNamespace})

			// update application to trigger application controller
			application.Spec.DisplayName = "test-name"
			Expect(k8sClient.Update(ctx, application)).To(Succeed())

			// wait for application SA to contain secret
			Eventually(func() bool {
				saList := getServiceAccountList(appSaTestNamespace)
				saCount := len(saList)
				secretsCount := 0
				if saCount > 0 {
					secretsCount = len(saList[0].Secrets)
				}
				return saCount == 1 && secretsCount == 0
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			saList := getServiceAccountList(appSaTestNamespace)
			Expect(len(saList)).Should(Equal(1))
			Expect(saList[0].Name).Should(Equal(applicationSaName))
			Expect(len(saList[0].Secrets)).Should(Equal(0))
			Expect(len(saList[0].ImagePullSecrets)).Should(Equal(0))
			Expect(len(saList[0].OwnerReferences)).Should(Equal(1))
			Expect(saList[0].OwnerReferences[0].Name).Should(Equal(applicationKey.Name))
		})

		It("application SA will be created with secrets for two components owned by application", func() {
			component1 := createComponent(componentConfig{ComponentKey: component1Key, ComponentApplication: applicationKey.Name})
			component2 := createComponent(componentConfig{ComponentKey: component2Key, ComponentApplication: applicationKey.Name})

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

			application := createApplication(applicationConfig{ApplicationKey: applicationKey})

			// wait until application SA is created with no secrets
			Eventually(func() bool {
				saList := getServiceAccountList(appSaTestNamespace)
				saCount := len(saList)
				secretsCount := 0
				if saCount > 0 {
					secretsCount = len(saList[0].Secrets)
				}
				return saCount == 1 && secretsCount == 0
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

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

			// delete application service account so it gets created again and with secret
			deleteServiceAccount(types.NamespacedName{Name: applicationSaName, Namespace: appSaTestNamespace})

			// update application to trigger application controller
			application.Spec.DisplayName = "test-name"
			Expect(k8sClient.Update(ctx, application)).To(Succeed())

			// wait for application SA to contain secret
			Eventually(func() bool {
				saList := getServiceAccountList(appSaTestNamespace)
				saCount := len(saList)
				secretsCount := 0
				imagePullSecretsCount := 0
				if saCount > 0 {
					secretsCount = len(saList[0].Secrets)
					imagePullSecretsCount = len(saList[0].ImagePullSecrets)
				}
				return saCount == 1 && secretsCount == 2 && imagePullSecretsCount == 2
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

			saList := getServiceAccountList(appSaTestNamespace)
			Expect(len(saList)).Should(Equal(1))
			Expect(saList[0].Name).Should(Equal(applicationSaName))
			Expect(len(saList[0].Secrets)).Should(Equal(2))
			Expect(len(saList[0].ImagePullSecrets)).Should(Equal(2))

			secretCounter := map[string]int{}
			imagePullSecretCounter := map[string]int{}
			for _, secret := range saList[0].Secrets {
				secretCounter[secret.Name]++
			}
			for _, secret := range saList[0].ImagePullSecrets {
				imagePullSecretCounter[secret.Name]++
			}

			Expect(secretCounter[pullSecret1]).Should(Equal(1))
			Expect(secretCounter[pullSecret2]).Should(Equal(1))
			Expect(imagePullSecretCounter[pullSecret1]).Should(Equal(1))
			Expect(imagePullSecretCounter[pullSecret2]).Should(Equal(1))
			Expect(len(saList[0].OwnerReferences)).Should(Equal(1))
			Expect(saList[0].OwnerReferences[0].Name).Should(Equal(application.Name))
		})

		It("should cleanup environment", func() {
			deleteNamespace(appSaTestNamespace)
		})
	})
})
