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
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/redhat-appstudio/image-controller/pkg/quay"
	appstudiospiapiv1beta1 "github.com/redhat-appstudio/service-provider-integration-operator/api/v1beta1"
)

var _ = Describe("Component image controller", func() {

	var (
		componentKey = types.NamespacedName{Name: defaultComponentName, Namespace: defaultComponentNamespace}
	)

	Context("Image repository provision flow", func() {

		It("Should prepare environment", func() {
			ResetTestQuayClient()

			createNamespace(defaultComponentNamespace)

			createComponent(componentConfig{}, "")
		})

		It("Should do nothing if generate annotation is not set", func() {
			ResetTestQuayClient()
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				Fail("Image repository creation should not be invoked")
				return nil, nil
			}

			setComponentDevfileModel(componentKey)

			time.Sleep(ensureTimeout)
		})

		It("Should do nothing if generate annotation is set to false", func() {
			ResetTestQuayClient()
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				Fail("Image repository creation should not be invoked")
				return nil, nil
			}

			setComponentAnnotationValue(componentKey, GenerateImageAnnotationName, "false")

			time.Sleep(ensureTimeout)
		})

		It("Should do image repository provision", func() {
			component := getComponent(componentKey)

			expectedImageRepositoryName := generateImageRepositoryName(component)
			expectedRobotAccountName := generateRobotAccountName(component)
			expectedImageRepoURL := fmt.Sprintf("quay.io/%s/%s", TestQuayOrganization, expectedImageRepositoryName)

			generatedRobotAccount := &quay.RobotAccount{
				Name:  TestQuayOrganization + "+" + expectedRobotAccountName,
				Token: "token1234",
			}

			ResetTestQuayClient()

			isCreateRepositoryInvoked := false
			CreateRepositoryFunc = func(repositoryRequest quay.RepositoryRequest) (*quay.Repository, error) {
				isCreateRepositoryInvoked = true
				Expect(repositoryRequest.Namespace).To(Equal(TestQuayOrganization))
				Expect(repositoryRequest.Visibility).To(Equal("public"))
				Expect(repositoryRequest.Description).ToNot(BeEmpty())
				Expect(repositoryRequest.Repository).To(Equal(expectedImageRepositoryName))
				return &quay.Repository{}, nil
			}

			isCreateRobotAccountInvoked := false
			CreateRobotAccountFunc = func(organization, robotAccountName string) (*quay.RobotAccount, error) {
				isCreateRobotAccountInvoked = true
				Expect(organization).To(Equal(TestQuayOrganization))
				Expect(robotAccountName).To(Equal(expectedRobotAccountName))
				return generatedRobotAccount, nil
			}

			isAddWritePermissionsToRobotAccountInvoked := false
			AddWritePermissionsToRobotAccountFunc = func(organization, imageRepository, robotAccountName string) error {
				isAddWritePermissionsToRobotAccountInvoked = true
				Expect(organization).To(Equal(TestQuayOrganization))
				Expect(imageRepository).To(Equal(expectedImageRepositoryName))
				Expect(robotAccountName).To(Equal(expectedRobotAccountName))
				return nil
			}

			setComponentAnnotationValue(componentKey, GenerateImageAnnotationName, "true")

			Eventually(func() bool {
				return isCreateRepositoryInvoked
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				return isCreateRobotAccountInvoked
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				return isAddWritePermissionsToRobotAccountInvoked
			}, timeout, interval).Should(BeTrue())

			Eventually(func() bool {
				imageRepositoryFinalizerFound := false
				component = getComponent(componentKey)
				for _, finalizer := range component.Finalizers {
					if finalizer == ImageRepositoryFinalizer {
						imageRepositoryFinalizerFound = true
						break
					}
				}
				return imageRepositoryFinalizerFound
			}, timeout, interval).Should(BeTrue())

			// Check the token submision via secret to SPI
			uploadToSPISecret := generateUploadToSPISecret(component, generatedRobotAccount, expectedImageRepoURL)
			uploadToSPISecretKey := types.NamespacedName{Namespace: uploadToSPISecret.Namespace, Name: uploadToSPISecret.Name}
			uploadToSPISecret = waitSecretCreated(uploadToSPISecretKey)
			Expect(uploadToSPISecret.Labels["spi.appstudio.redhat.com/upload-secret"]).To(Equal("token"))
			Expect(string(uploadToSPISecret.Data["spiTokenName"])).To(Equal(expectedRobotAccountName))
			Expect(string(uploadToSPISecret.Data["providerUrl"])).To(Equal("https://" + expectedImageRepoURL))
			Expect(string(uploadToSPISecret.Data["userName"])).To(Equal(generatedRobotAccount.Name))
			Expect(string(uploadToSPISecret.Data["tokenData"])).To(Equal(generatedRobotAccount.Token))

			// Mimic SPI by deleting the secret and creating SPIAccessToken
			Expect(k8sClient.Delete(ctx, uploadToSPISecret)).To(Succeed())

			spiAccessTokenKey := types.NamespacedName{Namespace: component.Namespace, Name: expectedRobotAccountName}
			createSPIAccessToken(spiAccessTokenKey)
			// Simulate SPI working
			time.Sleep(time.Second)
			makeSPIAccessTokenReady(spiAccessTokenKey)

			// Check that the owner reference is set to the Component
			Eventually(func() bool {
				spiAccessToken := &appstudiospiapiv1beta1.SPIAccessToken{}
				Expect(k8sClient.Get(ctx, spiAccessTokenKey, spiAccessToken)).To(Succeed())
				return len(spiAccessToken.OwnerReferences) == 1 &&
					spiAccessToken.OwnerReferences[0].Name == component.Name &&
					spiAccessToken.OwnerReferences[0].Kind == "Component"
			}, timeout, interval).Should(BeTrue())

			spiAccessTokenBindingKey := spiAccessTokenKey
			spiAccessTokenBinding := waitSPIAccessTokenBinding(spiAccessTokenBindingKey)
			Expect(spiAccessTokenBinding.Spec.RepoUrl).To(Equal("https://" + expectedImageRepoURL))
			Expect(spiAccessTokenBinding.Spec.Lifetime).To(Equal("-1"))
			Expect(len(spiAccessTokenBinding.ObjectMeta.OwnerReferences)).To(Equal(1))
			Expect(spiAccessTokenBinding.ObjectMeta.OwnerReferences[0].Name).To(Equal(component.Name))
			Expect(spiAccessTokenBinding.ObjectMeta.OwnerReferences[0].Kind).To(Equal("Component"))

			makeSPIAccessTokenBindingReady(spiAccessTokenBindingKey)

			waitComponentAnnotation(componentKey, ImageAnnotationName)
			component = getComponent(componentKey)
			Expect(component.Annotations[ImageAnnotationName]).ToNot(BeEmpty())
			var repositoryInfo RepositoryInfo
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), &repositoryInfo)).To(Succeed())
			Expect(repositoryInfo.Image).To(Equal(expectedImageRepoURL))
			Expect(repositoryInfo.Secret).To(HavePrefix(spiAccessTokenBinding.Name))

			waitComponentAnnotationValue(componentKey, GenerateImageAnnotationName, "false")
		})

		It("Should regenerate image repository token", func() {
			component := getComponent(componentKey)

			expectedImageRepositoryName := generateImageRepositoryName(component)
			expectedRobotAccountName := generateRobotAccountName(component)
			expectedImageRepoURL := fmt.Sprintf("quay.io/%s/%s", TestQuayOrganization, expectedImageRepositoryName)

			regeneratedRobotAccount := &quay.RobotAccount{
				Name:  TestQuayOrganization + "+" + expectedRobotAccountName,
				Token: "token5678",
			}

			ResetTestQuayClient()

			isRegenerateRobotAccountTokenInvoked := false
			RegenerateRobotAccountTokenFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				isRegenerateRobotAccountTokenInvoked = true
				Expect(organization).To(Equal(TestQuayOrganization))
				Expect(robotName).To(Equal(expectedRobotAccountName))
				return regeneratedRobotAccount, nil
			}

			// Ensure the upload to SPI secret doesn't exist
			uploadToSPISecret := generateUploadToSPISecret(component, regeneratedRobotAccount, expectedImageRepoURL)
			uploadToSPISecretKey := types.NamespacedName{Namespace: uploadToSPISecret.Namespace, Name: uploadToSPISecret.Name}
			Expect(errors.IsNotFound(k8sClient.Get(ctx, uploadToSPISecretKey, uploadToSPISecret))).To(BeTrue())

			// Trigger token regeneration
			setComponentAnnotationValue(componentKey, GenerateImageAnnotationName, "regenerate-token")

			Eventually(func() bool {
				return isRegenerateRobotAccountTokenInvoked
			}, timeout, interval).Should(BeTrue())

			uploadToSPISecret = waitSecretCreated(uploadToSPISecretKey)
			Expect(string(uploadToSPISecret.Data["spiTokenName"])).To(Equal(expectedRobotAccountName))
			Expect(string(uploadToSPISecret.Data["providerUrl"])).To(Equal("https://" + expectedImageRepoURL))
			Expect(string(uploadToSPISecret.Data["userName"])).To(Equal(regeneratedRobotAccount.Name))
			Expect(string(uploadToSPISecret.Data["tokenData"])).To(Equal(regeneratedRobotAccount.Token))

			waitComponentAnnotationValue(componentKey, GenerateImageAnnotationName, "false")
		})

		It("Should delete robot account and image repository on component deletion", func() {
			setComponentAnnotationValue(componentKey, DeleteImageRepositoryAnnotationName, "true")

			component := getComponent(componentKey)
			expectedImageRepositoryName := generateImageRepositoryName(component)
			expectedRobotAccountName := generateRobotAccountName(component)

			ResetTestQuayClient()

			isDeleteRobotAccountInvoked := false
			DeleteRobotAccountFunc = func(organization, robotName string) (bool, error) {
				isDeleteRobotAccountInvoked = true
				Expect(organization).To(Equal(TestQuayOrganization))
				Expect(robotName).To(Equal(expectedRobotAccountName))
				return true, nil
			}

			isDeleteRepositoryInvoked := false
			DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				isDeleteRepositoryInvoked = true
				Expect(organization).To(Equal(TestQuayOrganization))
				Expect(imageRepository).To(Equal(expectedImageRepositoryName))
				return true, nil
			}

			deleteComponent(componentKey)

			Eventually(func() bool {
				return isDeleteRobotAccountInvoked
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				return isDeleteRepositoryInvoked
			}, timeout, interval).Should(BeTrue())
		})

		It("Should clean up environment", func() {
			ResetTestQuayClient()

			deleteNamespace(defaultComponentNamespace)
		})
	})
})
