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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/konflux-ci/image-controller/pkg/quay"
)

const KonfluxIntegrationRunnerSAName = "konflux-integration-runner"

var _ = Describe("Application controller", func() {
	var testNamespace string
	var applicationKey types.NamespacedName
	var component1Key types.NamespacedName
	var component2Key types.NamespacedName
	var imageRepository1Key types.NamespacedName
	var imageRepository2Key types.NamespacedName

	var aggregatedPullSecretName types.NamespacedName

	Context("ServiceAccount 'konflux-integration-runner' exists", func() {

		BeforeEach(func() {
			quay.ResetTestQuayClient()
			testNamespace = fmt.Sprintf("test-ns-%d", time.Now().UnixNano())
			createNamespace(testNamespace)
			createServiceAccount(testNamespace, KonfluxIntegrationRunnerSAName)
			applicationKey = types.NamespacedName{Name: "application-test", Namespace: testNamespace}
			component1Key = types.NamespacedName{Name: "component1-test", Namespace: testNamespace}
			component2Key = types.NamespacedName{Name: "component2-test", Namespace: testNamespace}
			imageRepository1Key = types.NamespacedName{Name: "ir1-test", Namespace: testNamespace}
			imageRepository2Key = types.NamespacedName{Name: "ir2-test", Namespace: testNamespace}
			createApplication(applicationConfig{ApplicationKey: applicationKey})
			aggregatedPullSecretName = types.NamespacedName{Name: getApplicationPullSecretName(applicationKey.Name), Namespace: testNamespace}

		})

		AfterEach(func() {
			deleteImageRepository(imageRepository2Key)
			deleteImageRepository(imageRepository1Key)
			deleteComponent(component1Key)
			deleteComponent(component2Key)
			deleteApplication(applicationKey)
			deleteSecret(aggregatedPullSecretName)
			deleteSecret(types.NamespacedName{Name: "pull-secret-comp1", Namespace: testNamespace})
			deleteSecret(types.NamespacedName{Name: "pull-secret-comp2", Namespace: testNamespace})
			deleteSecret(types.NamespacedName{Name: "pull-secret-comp1-rotated", Namespace: testNamespace})
			deleteServiceAccount(types.NamespacedName{Name: KonfluxIntegrationRunnerSAName, Namespace: testNamespace})
			deleteNamespace(testNamespace)
		})

		It("should create an aggregated pull secret and link it to 'konflux-integration-runner' SA", func() {
			pullSecret1Data := generateDockerConfigJson("registry1.example.com", "user1", "pass1")
			pullSecret1Key := types.NamespacedName{Name: "pull-secret-comp1", Namespace: testNamespace}
			createDockerConfigSecret(pullSecret1Key, pullSecret1Data)

			pullSecret2Data := generateDockerConfigJson("registry2.example.com", "user2", "pass2")
			pullSecret2Key := types.NamespacedName{Name: "pull-secret-comp2", Namespace: testNamespace}
			createDockerConfigSecret(pullSecret2Key, pullSecret2Data)

			component1 := createComponent(componentConfig{ComponentKey: component1Key, ComponentApplication: applicationKey.Name})
			component2 := createComponent(componentConfig{ComponentKey: component2Key, ComponentApplication: applicationKey.Name})

			application := getApplication(applicationKey)
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

			ir1 := createImageRepository(imageRepositoryConfig{
				ResourceKey:     &imageRepository1Key,
				Finalizers:      []string{ImageRepositoryFinalizer},
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "appstudio.redhat.com/v1alpha1", Kind: "Component", Name: component1.Name, UID: component1.UID}},
			})
			ir1.Status.Credentials.PullSecretName = pullSecret1Key.Name
			Expect(k8sClient.Status().Update(ctx, ir1)).To(Succeed())

			ir2 := createImageRepository(imageRepositoryConfig{
				ResourceKey:     &imageRepository2Key,
				Finalizers:      []string{ImageRepositoryFinalizer},
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "appstudio.redhat.com/v1alpha1", Kind: "Component", Name: component2.Name, UID: component2.UID}},
			})
			ir2.Status.Credentials.PullSecretName = pullSecret2Key.Name
			Expect(k8sClient.Status().Update(ctx, ir2)).To(Succeed())

			// Trigger reconciliation by updating the application
			application.Spec.DisplayName = "updated-name"
			Expect(k8sClient.Update(ctx, application)).To(Succeed())

			// Assert the aggregated secret is created and its content is correct
			aggregatedSecret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, aggregatedPullSecretName, aggregatedSecret)
			}, timeout, interval).Should(Succeed(), "Expected aggregated pull secret to be created")

			Expect(aggregatedSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			Expect(aggregatedSecret.OwnerReferences).To(HaveLen(1))
			Expect(aggregatedSecret.OwnerReferences[0].Name).To(Equal(applicationKey.Name))
			Expect(aggregatedSecret.Data).To(HaveKey(corev1.DockerConfigJsonKey))

			aggregatedSecretDockerConfigJson := string(aggregatedSecret.Data[corev1.DockerConfigJsonKey])

			var decodedAggregated dockerConfigJson
			Expect(json.Unmarshal([]byte(aggregatedSecretDockerConfigJson), &decodedAggregated)).To(Succeed())

			Expect(decodedAggregated.Auths).To(HaveLen(2))
			Expect(decodedAggregated.Auths).To(HaveKey("registry1.example.com"))
			Expect(decodedAggregated.Auths["registry1.example.com"].Auth).To(Equal(base64.StdEncoding.EncodeToString([]byte("user1:pass1"))))
			Expect(decodedAggregated.Auths).To(HaveKey("registry2.example.com"))
			Expect(decodedAggregated.Auths["registry2.example.com"].Auth).To(Equal(base64.StdEncoding.EncodeToString([]byte("user2:pass2"))))

			// Assert 'konflux-integration-runner' SA links the aggregated secret
			konfluxSA := getServiceAccount(testNamespace, KonfluxIntegrationRunnerSAName)
			Expect(konfluxSA.ImagePullSecrets).To(HaveLen(1))
			Expect(konfluxSA.ImagePullSecrets[0].Name).To(Equal(aggregatedPullSecretName.Name))
		})

		It("should update the aggregated pull secret on individual secret rotation", func() {
			pullSecret1Data := generateDockerConfigJson("registry1.example.com", "user1", "pass1")
			pullSecret1Key := types.NamespacedName{Name: "pull-secret-comp1", Namespace: testNamespace}
			createDockerConfigSecret(pullSecret1Key, pullSecret1Data)

			component1 := createComponent(componentConfig{ComponentKey: component1Key, ComponentApplication: applicationKey.Name})
			application := getApplication(applicationKey)
			component1.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: "appstudio.redhat.com/v1alpha1", Kind: "Application", Name: application.Name, UID: application.UID,
			}}
			Expect(k8sClient.Update(ctx, component1)).To(Succeed())

			ir1 := createImageRepository(imageRepositoryConfig{
				ResourceKey:     &imageRepository1Key,
				Finalizers:      []string{ImageRepositoryFinalizer},
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "appstudio.redhat.com/v1alpha1", Kind: "Component", Name: component1.Name, UID: component1.UID}},
			})
			ir1.Status.Credentials.PullSecretName = pullSecret1Key.Name
			Expect(k8sClient.Status().Update(ctx, ir1)).To(Succeed())

			// Trigger initial reconciliation
			application.Spec.DisplayName = "initial-reconcile"
			Expect(k8sClient.Update(ctx, application)).To(Succeed())

			// Wait for aggregated secret to be created
			aggregatedSecret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, aggregatedPullSecretName, aggregatedSecret)
			}, timeout, interval).Should(Succeed())

			// Rotate credentials: update the individual secret
			rotatedPullSecret1Data := generateDockerConfigJson("registry1.example.com", "user1-new", "pass1-new")
			updatedSecret := waitSecretExist(pullSecret1Key)
			updatedSecret.Data[corev1.DockerConfigJsonKey] = []byte(rotatedPullSecret1Data)
			Expect(k8sClient.Update(ctx, updatedSecret)).To(Succeed())

			// Trigger reconciliation by updating the application
			application.Spec.DisplayName = "rotated-reconcile"
			Expect(k8sClient.Update(ctx, application)).To(Succeed())

			// Assert aggregated secret is updated with new credentials
			Eventually(func() bool {
				currentAggregatedSecret := &corev1.Secret{}
				err := k8sClient.Get(ctx, aggregatedPullSecretName, currentAggregatedSecret)
				if err != nil {
					return false
				}
				aggregatedSecretDockerConfigJson := string(currentAggregatedSecret.Data[corev1.DockerConfigJsonKey])

				var decodedAggregated dockerConfigJson
				Expect(json.Unmarshal([]byte(aggregatedSecretDockerConfigJson), &decodedAggregated)).To(Succeed())

				return decodedAggregated.Auths["registry1.example.com"].Auth == base64.StdEncoding.EncodeToString([]byte("user1-new:pass1-new"))
			}, timeout, interval).Should(BeTrue(), "Expected aggregated pull secret to be updated with rotated credentials")

			// Ensure SA still links the same aggregated secret
			konfluxSA := getServiceAccount(testNamespace, KonfluxIntegrationRunnerSAName)
			Expect(konfluxSA.ImagePullSecrets).To(HaveLen(1))
			Expect(konfluxSA.ImagePullSecrets[0].Name).To(Equal(aggregatedPullSecretName.Name))
		})

		It("should remove credentials from aggregated pull secret on individual secret deletion", func() {
			pullSecret1Data := generateDockerConfigJson("registry1.example.com", "user1", "pass1")
			pullSecret1Key := types.NamespacedName{Name: "pull-secret-comp1", Namespace: testNamespace}
			createDockerConfigSecret(pullSecret1Key, pullSecret1Data)

			pullSecret2Data := generateDockerConfigJson("registry2.example.com", "user2", "pass2")
			pullSecret2Key := types.NamespacedName{Name: "pull-secret-comp2", Namespace: testNamespace}
			createDockerConfigSecret(pullSecret2Key, pullSecret2Data)

			component1 := createComponent(componentConfig{ComponentKey: component1Key, ComponentApplication: applicationKey.Name})
			component2 := createComponent(componentConfig{ComponentKey: component2Key, ComponentApplication: applicationKey.Name})
			application := getApplication(applicationKey)
			component1.OwnerReferences = []metav1.OwnerReference{{APIVersion: "appstudio.redhat.com/v1alpha1", Kind: "Application", Name: application.Name, UID: application.UID}}
			component2.OwnerReferences = []metav1.OwnerReference{{APIVersion: "appstudio.redhat.com/v1alpha1", Kind: "Application", Name: application.Name, UID: application.UID}}
			Expect(k8sClient.Update(ctx, component1)).To(Succeed())
			Expect(k8sClient.Update(ctx, component2)).To(Succeed())

			ir1 := createImageRepository(imageRepositoryConfig{
				ResourceKey:     &imageRepository1Key,
				Finalizers:      []string{ImageRepositoryFinalizer},
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "appstudio.redhat.com/v1alpha1", Kind: "Component", Name: component1.Name, UID: component1.UID}},
			})
			ir1.Status.Credentials.PullSecretName = pullSecret1Key.Name
			Expect(k8sClient.Status().Update(ctx, ir1)).To(Succeed())

			ir2 := createImageRepository(imageRepositoryConfig{
				ResourceKey:     &imageRepository2Key,
				Finalizers:      []string{ImageRepositoryFinalizer},
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "appstudio.redhat.com/v1alpha1", Kind: "Component", Name: component2.Name, UID: component2.UID}},
			})
			ir2.Status.Credentials.PullSecretName = pullSecret2Key.Name
			Expect(k8sClient.Status().Update(ctx, ir2)).To(Succeed())

			// Trigger initial reconciliation
			application.Spec.DisplayName = "initial-reconcile-delete"
			Expect(k8sClient.Update(ctx, application)).To(Succeed())

			// Wait for aggregated secret to be created with both entries
			aggregatedSecret := &corev1.Secret{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, aggregatedPullSecretName, aggregatedSecret)
				if err != nil {
					return false
				}
				aggregatedSecretDockerConfigJson := string(aggregatedSecret.Data[corev1.DockerConfigJsonKey])

				var decodedAggregated dockerConfigJson
				Expect(json.Unmarshal([]byte(aggregatedSecretDockerConfigJson), &decodedAggregated)).To(Succeed())
				return len(decodedAggregated.Auths) == 2 &&
					decodedAggregated.Auths["registry1.example.com"].Auth == base64.StdEncoding.EncodeToString([]byte("user1:pass1")) &&
					decodedAggregated.Auths["registry2.example.com"].Auth == base64.StdEncoding.EncodeToString([]byte("user2:pass2"))
			}, timeout, interval).Should(BeTrue(), "Expected aggregated secret to have both initial entries")

			// Delete one of the individual secrets
			deleteSecret(pullSecret1Key)

			// Trigger reconciliation by updating the application
			application.Spec.DisplayName = "delete-reconcile"
			Expect(k8sClient.Update(ctx, application)).To(Succeed())

			// Assert aggregated secret no longer contains the deleted entry
			Eventually(func() bool {
				currentAggregatedSecret := &corev1.Secret{}
				err := k8sClient.Get(ctx, aggregatedPullSecretName, currentAggregatedSecret)
				if err != nil {
					return false
				}
				aggregatedSecretDockerConfigJson := string(currentAggregatedSecret.Data[corev1.DockerConfigJsonKey])

				var decodedAggregated dockerConfigJson
				Expect(json.Unmarshal([]byte(aggregatedSecretDockerConfigJson), &decodedAggregated)).To(Succeed())
				// Should only have one entry left (registry2.example.com)
				_, registry1Exists := decodedAggregated.Auths["registry1.example.com"]
				return len(decodedAggregated.Auths) == 1 &&
					decodedAggregated.Auths["registry2.example.com"].Auth == base64.StdEncoding.EncodeToString([]byte("user2:pass2")) &&
					!registry1Exists
			}, timeout, interval).Should(BeTrue(), "Expected aggregated pull secret to remove deleted entry")

			// Ensure SA still links the same aggregated secret
			konfluxSA := getServiceAccount(testNamespace, KonfluxIntegrationRunnerSAName)
			Expect(konfluxSA.ImagePullSecrets).To(HaveLen(1))
			Expect(konfluxSA.ImagePullSecrets[0].Name).To(Equal(aggregatedPullSecretName.Name))
		})

		It("should handle empty list of individual secrets gracefully", func() {
			// Trigger reconciliation by updating the application
			application := getApplication(applicationKey)
			application.Spec.DisplayName = "empty-reconcile"
			Expect(k8sClient.Update(ctx, application)).To(Succeed())

			// Assert aggregated secret is created, but its data is empty
			aggregatedSecret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, aggregatedPullSecretName, aggregatedSecret)
			}, timeout, interval).Should(Succeed(), "Expected aggregated pull secret to be created")

			Expect(aggregatedSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			Expect(aggregatedSecret.Data).To(HaveKey(corev1.DockerConfigJsonKey))

			aggregatedSecretDockerConfigJson := string(aggregatedSecret.Data[corev1.DockerConfigJsonKey])

			var decodedAggregated dockerConfigJson
			Expect(json.Unmarshal([]byte(aggregatedSecretDockerConfigJson), &decodedAggregated)).To(Succeed())
			Expect(decodedAggregated.Auths).To(BeEmpty())

			// Assert 'konflux-integration-runner' SA links the empty aggregated secret
			konfluxSA := getServiceAccount(testNamespace, KonfluxIntegrationRunnerSAName)
			Expect(konfluxSA.ImagePullSecrets).To(HaveLen(1))
			Expect(konfluxSA.ImagePullSecrets[0].Name).To(Equal(aggregatedPullSecretName.Name))
		})
	})
})
