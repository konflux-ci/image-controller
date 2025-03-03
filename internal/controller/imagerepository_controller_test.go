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
	"fmt"
	"strings"
	"time"

	"github.com/konflux-ci/image-controller/pkg/quay"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	imagerepositoryv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
)

var _ = Describe("Image repository controller", func() {

	var (
		pushToken                  string
		pullToken                  string
		expectedRobotAccountPrefix string
		expectedImageName          string
		expectedImage              string
	)

	BeforeEach(func() {
		createNamespace(defaultNamespace)
	})

	Context("Image repository provision without component", func() {
		var resourceKey = types.NamespacedName{Name: defaultImageRepositoryName + "-isnotlinked", Namespace: defaultNamespace}

		BeforeEach(func() {
			quay.ResetTestQuayClientToFails()
		})

		It("should prepare environment", func() {
			pushToken = "push-token1234"
			pullToken = "pull-token1234"
			expectedImageName = fmt.Sprintf("%s/%s", defaultNamespace, resourceKey.Name)
			expectedImage = fmt.Sprintf("quay.io/%s/%s", quay.TestQuayOrg, expectedImageName)
			expectedRobotAccountPrefix = strings.ReplaceAll(strings.ReplaceAll(expectedImageName, "-", "_"), "/", "_")
			createServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
		})

		It("should provision image repository", func() {
			isCreateRepositoryInvoked := false
			quay.CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(quay.TestQuayOrg))
				Expect(repository.Visibility).To(Equal("public"))
				Expect(repository.Description).ToNot(BeEmpty())
				return &quay.Repository{Name: expectedImageName}, nil
			}
			isCreatePushRobotAccountInvoked := false
			isCreatePullRobotAccountInvoked := false
			quay.CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(strings.HasPrefix(robotName, expectedRobotAccountPrefix)).To(BeTrue())
				if strings.HasSuffix(robotName, "_pull") {
					isCreatePullRobotAccountInvoked = true
					return &quay.RobotAccount{Name: robotName, Token: pullToken}, nil
				}
				isCreatePushRobotAccountInvoked = true
				return &quay.RobotAccount{Name: robotName, Token: pushToken}, nil
			}
			isAddPushPermissionsToAccountInvoked := false
			isAddPullPermissionsToAccountInvoked := false
			quay.AddPermissionsForRepositoryToAccountFunc = func(organization, imageRepository, accountName string, isRobot, isWrite bool) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				Expect(strings.HasPrefix(accountName, expectedRobotAccountPrefix)).To(BeTrue())
				if strings.HasSuffix(accountName, "_pull") {
					Expect(isWrite).To(BeFalse())
					isAddPullPermissionsToAccountInvoked = true
				} else {
					Expect(isWrite).To(BeTrue())
					isAddPushPermissionsToAccountInvoked = true
				}
				return nil
			}

			isCreateNotificationInvoked := false
			quay.CreateNotificationFunc = func(organization, repository string, notification quay.Notification) (*quay.Notification, error) {
				isCreateNotificationInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				return &quay.Notification{UUID: "uuid"}, nil
			}

			createImageRepository(imageRepositoryConfig{ResourceKey: &resourceKey})

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePullRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePushRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPushPermissionsToAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPullPermissionsToAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreateNotificationInvoked }, timeout, interval).Should(BeFalse())

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			imageRepository := getImageRepository(resourceKey)
			Expect(imageRepository.Spec.Image.Name).To(Equal(expectedImageName))
			Expect(imageRepository.Spec.Image.Visibility).To(Equal(imagerepositoryv1alpha1.ImageVisibilityPublic))
			Expect(imageRepository.OwnerReferences).To(HaveLen(0))
			Expect(imageRepository.Status.State).To(Equal(imagerepositoryv1alpha1.ImageRepositoryStateReady))
			Expect(imageRepository.Status.Message).To(BeEmpty())
			Expect(imageRepository.Status.Image.URL).To(Equal(expectedImage))
			Expect(imageRepository.Status.Image.Visibility).To(Equal(imagerepositoryv1alpha1.ImageVisibilityPublic))
			Expect(imageRepository.Status.Credentials.PushRobotAccountName).To(HavePrefix(expectedRobotAccountPrefix))
			Expect(imageRepository.Status.Credentials.PushSecretName).To(Equal(imageRepository.Name + "-image-push"))
			Expect(imageRepository.Status.Credentials.PullRobotAccountName).To(HavePrefix(expectedRobotAccountPrefix))
			Expect(imageRepository.Status.Credentials.PullRobotAccountName).To(HaveSuffix("_pull"))
			Expect(imageRepository.Status.Credentials.PullSecretName).To(Equal(imageRepository.Name + "-image-pull"))
			Expect(imageRepository.Status.Credentials.GenerationTimestamp).ToNot(BeNil())
			Expect(imageRepository.Status.Notifications).To(HaveLen(0))

			pushSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PushSecretName, Namespace: imageRepository.Namespace}
			pushSecret := waitSecretExist(pushSecretKey)
			defer deleteSecret(pushSecretKey)
			verifySecretSpec(pushSecret, imageRepository.GetName(), pushSecret.Name)

			pullSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PullSecretName, Namespace: imageRepository.Namespace}
			pullSecret := waitSecretExist(pullSecretKey)
			defer deleteSecret(pullSecretKey)
			verifySecretSpec(pullSecret, imageRepository.GetName(), pullSecret.Name)

			sa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			Expect(sa.Secrets).To(HaveLen(1))
			Expect(sa.ImagePullSecrets).To(HaveLen(0))
			Expect(sa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pushSecret.Name}))

			pushSecretDockerconfigJson := string(pushSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pushSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PushRobotAccountName, pushToken)
			pullSecretDockerconfigJson := string(pullSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pullSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PullRobotAccountName, pullToken)
		})

		It("should regenerate token", func() {
			newPushToken := "push-token5678"
			newPullToken := "pull-token5678"

			// Wait just for case it takes less than a second to regenerate credentials
			time.Sleep(time.Second)

			isRegenerateRobotAccountTokenForPushInvoked := false
			isRegenerateRobotAccountTokenForPullInvoked := false
			quay.RegenerateRobotAccountTokenFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(strings.HasPrefix(robotName, expectedRobotAccountPrefix)).To(BeTrue())
				if strings.HasSuffix(robotName, "_pull") {
					isRegenerateRobotAccountTokenForPullInvoked = true
					return &quay.RobotAccount{Name: robotName, Token: newPullToken}, nil
				}
				isRegenerateRobotAccountTokenForPushInvoked = true
				return &quay.RobotAccount{Name: robotName, Token: newPushToken}, nil
			}

			imageRepository := getImageRepository(resourceKey)
			oldTokenGenerationTimestamp := *imageRepository.Status.Credentials.GenerationTimestamp
			regenerateToken := true
			imageRepository.Spec.Credentials = &imagerepositoryv1alpha1.ImageCredentials{RegenerateToken: &regenerateToken}
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			Eventually(func() bool { return isRegenerateRobotAccountTokenForPullInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isRegenerateRobotAccountTokenForPushInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Spec.Credentials.RegenerateToken == nil &&
					imageRepository.Status.Credentials.GenerationTimestamp != nil &&
					*imageRepository.Status.Credentials.GenerationTimestamp != oldTokenGenerationTimestamp
			}, timeout, interval).Should(BeTrue())

			pushSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PushSecretName, Namespace: imageRepository.Namespace}
			pushSecret := waitSecretExist(pushSecretKey)
			Expect(pushSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			pushSecretDockerconfigJson := string(pushSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pushSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PushRobotAccountName, newPushToken)

			pullSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PullSecretName, Namespace: imageRepository.Namespace}
			pullSecret := waitSecretExist(pullSecretKey)
			Expect(pullSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			pullSecretDockerconfigJson := string(pullSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pullSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PullRobotAccountName, newPullToken)
		})

		It("should update image visibility", func() {
			isChangeRepositoryVisibilityInvoked := false
			quay.ChangeRepositoryVisibilityFunc = func(organization, imageRepository, visibility string) error {
				defer GinkgoRecover()
				isChangeRepositoryVisibilityInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				Expect(visibility).To(Equal(string(imagerepositoryv1alpha1.ImageVisibilityPrivate)))
				return nil
			}

			imageRepository := getImageRepository(resourceKey)
			imageRepository.Spec.Image.Visibility = imagerepositoryv1alpha1.ImageVisibilityPrivate
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			Eventually(func() bool { return isChangeRepositoryVisibilityInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Spec.Image.Visibility == imagerepositoryv1alpha1.ImageVisibilityPrivate &&
					imageRepository.Status.Image.Visibility == imagerepositoryv1alpha1.ImageVisibilityPrivate &&
					imageRepository.Status.Message == ""
			}, timeout, interval).Should(BeTrue())
		})

		It("should revert image name if edited", func() {
			imageRepository := getImageRepository(resourceKey)
			imageRepository.Spec.Image.Name = "renamed"
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Spec.Image.Name == expectedImageName
			}, timeout, interval).Should(BeTrue())
		})

		It("verify and fix, secret is missing from SA", func() {
			// will add it to SA, but not to ImagePullSecrets
			sa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			sa.Secrets = []corev1.ObjectReference{}
			Expect(k8sClient.Update(ctx, &sa)).To(Succeed())

			imageRepository := getImageRepository(resourceKey)
			verifyLinking := true
			imageRepository.Spec.Credentials = &imagerepositoryv1alpha1.ImageCredentials{VerifyLinking: &verifyLinking}
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			waitImageRepositoryCredentialSectionRequestGone(resourceKey, "verify")

			secretName := fmt.Sprintf("%s-image-push", resourceKey.Name)
			sa = getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			Expect(sa.Secrets).To(HaveLen(1))
			Expect(sa.ImagePullSecrets).To(HaveLen(0))
			Expect(sa.Secrets).To(ContainElement(corev1.ObjectReference{Name: secretName}))
		})

		It("verify and fix, secret is duplicated in SA, also is in ImagePullSecrets", func() {
			// will remove duplicate, and remove it from ImagePullSecrets
			sa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			secretName := fmt.Sprintf("%s-image-push", resourceKey.Name)
			sa.Secrets = []corev1.ObjectReference{{Name: secretName}, {Name: secretName}}
			sa.ImagePullSecrets = []corev1.LocalObjectReference{{Name: secretName}, {Name: secretName}}
			Expect(k8sClient.Update(ctx, &sa)).To(Succeed())

			imageRepository := getImageRepository(resourceKey)
			verifyLinking := true
			imageRepository.Spec.Credentials = &imagerepositoryv1alpha1.ImageCredentials{VerifyLinking: &verifyLinking}
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			waitImageRepositoryCredentialSectionRequestGone(resourceKey, "verify")

			sa = getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			Expect(sa.Secrets).To(HaveLen(1))
			Expect(sa.ImagePullSecrets).To(HaveLen(0))
			Expect(sa.Secrets).To(ContainElement(corev1.ObjectReference{Name: secretName}))
		})

		It("should cleanup repository", func() {
			isDeleteRobotAccountForPushInvoked := false
			isDeleteRobotAccountForPullInvoked := false
			quay.DeleteRobotAccountFunc = func(organization, robotAccountName string) (bool, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(strings.HasPrefix(robotAccountName, expectedRobotAccountPrefix)).To(BeTrue())
				if strings.HasSuffix(robotAccountName, "_pull") {
					isDeleteRobotAccountForPushInvoked = true
				} else {
					isDeleteRobotAccountForPullInvoked = true
				}
				return true, nil
			}
			isDeleteRepositoryInvoked := false
			quay.DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				defer GinkgoRecover()
				isDeleteRepositoryInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				return true, nil
			}

			deleteImageRepository(resourceKey)

			Eventually(func() bool { return isDeleteRobotAccountForPullInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRobotAccountForPushInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRepositoryInvoked }, timeout, interval).Should(BeTrue())

			sa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			// verify that secret is unlinked
			Expect(sa.Secrets).To(HaveLen(0))
			Expect(sa.ImagePullSecrets).To(HaveLen(0))

			deleteServiceAccount(types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: defaultNamespace})
		})
	})

	Context("Image repository provision secret already linked in SA", func() {
		var resourceKey = types.NamespacedName{Name: defaultImageRepositoryName + "-islinked", Namespace: defaultNamespace}

		BeforeEach(func() {
			quay.ResetTestQuayClientToFails()
		})

		It("should prepare environment", func() {
			pushToken = "push-token1234"
			pullToken = "pull-token1234"
			expectedImageName = fmt.Sprintf("%s/%s", defaultNamespace, resourceKey.Name)
			expectedImage = fmt.Sprintf("quay.io/%s/%s", quay.TestQuayOrg, expectedImageName)
			expectedRobotAccountPrefix = strings.ReplaceAll(strings.ReplaceAll(expectedImageName, "-", "_"), "/", "_")
			createServiceAccount(defaultNamespace, buildPipelineServiceAccountName)

			// add push secret to SA
			sa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			secretName := fmt.Sprintf("%s-image-push", resourceKey.Name)
			sa.Secrets = append(sa.Secrets, corev1.ObjectReference{Name: secretName})
			Expect(k8sClient.Update(ctx, &sa)).To(Succeed())
		})

		It("should provision image repository", func() {
			isCreateRepositoryInvoked := false
			quay.CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(quay.TestQuayOrg))
				Expect(repository.Visibility).To(Equal("public"))
				Expect(repository.Description).ToNot(BeEmpty())
				return &quay.Repository{Name: expectedImageName}, nil
			}
			isCreatePullRobotAccountInvoked := false
			isCreatePushRobotAccountInvoked := false
			quay.CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(strings.HasPrefix(robotName, expectedRobotAccountPrefix)).To(BeTrue())
				if strings.HasSuffix(robotName, "_pull") {
					isCreatePullRobotAccountInvoked = true
					return &quay.RobotAccount{Name: robotName, Token: pullToken}, nil
				}
				isCreatePushRobotAccountInvoked = true
				return &quay.RobotAccount{Name: robotName, Token: pushToken}, nil
			}
			isAddPushPermissionsToAccountInvoked := false
			isAddPullPermissionsToAccountInvoked := false
			quay.AddPermissionsForRepositoryToAccountFunc = func(organization, imageRepository, accountName string, isRobot, isWrite bool) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				Expect(strings.HasPrefix(accountName, expectedRobotAccountPrefix)).To(BeTrue())
				if strings.HasSuffix(accountName, "_pull") {
					Expect(isWrite).To(BeFalse())
					isAddPullPermissionsToAccountInvoked = true
				} else {
					Expect(isWrite).To(BeTrue())
					isAddPushPermissionsToAccountInvoked = true
				}
				return nil
			}

			isCreateNotificationInvoked := false
			quay.CreateNotificationFunc = func(organization, repository string, notification quay.Notification) (*quay.Notification, error) {
				isCreateNotificationInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				return &quay.Notification{UUID: "uuid"}, nil
			}
			createImageRepository(imageRepositoryConfig{ResourceKey: &resourceKey})

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePullRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePushRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPushPermissionsToAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPullPermissionsToAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreateNotificationInvoked }, timeout, interval).Should(BeFalse())

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			imageRepository := getImageRepository(resourceKey)
			Expect(imageRepository.Spec.Image.Name).To(Equal(expectedImageName))
			Expect(imageRepository.Spec.Image.Visibility).To(Equal(imagerepositoryv1alpha1.ImageVisibilityPublic))
			Expect(imageRepository.OwnerReferences).To(HaveLen(0))
			Expect(imageRepository.Status.State).To(Equal(imagerepositoryv1alpha1.ImageRepositoryStateReady))
			Expect(imageRepository.Status.Message).To(BeEmpty())
			Expect(imageRepository.Status.Image.URL).To(Equal(expectedImage))
			Expect(imageRepository.Status.Image.Visibility).To(Equal(imagerepositoryv1alpha1.ImageVisibilityPublic))
			Expect(imageRepository.Status.Credentials.PushRobotAccountName).To(HavePrefix(expectedRobotAccountPrefix))
			Expect(imageRepository.Status.Credentials.PushSecretName).To(Equal(imageRepository.Name + "-image-push"))
			Expect(imageRepository.Status.Credentials.PullRobotAccountName).To(HavePrefix(expectedRobotAccountPrefix))
			Expect(imageRepository.Status.Credentials.PullRobotAccountName).To(HaveSuffix("_pull"))
			Expect(imageRepository.Status.Credentials.PullSecretName).To(Equal(imageRepository.Name + "-image-pull"))
			Expect(imageRepository.Status.Credentials.GenerationTimestamp).ToNot(BeNil())
			Expect(imageRepository.Status.Notifications).To(HaveLen(0))

			pushSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PushSecretName, Namespace: imageRepository.Namespace}
			pushSecret := waitSecretExist(pushSecretKey)
			defer deleteSecret(pushSecretKey)
			verifySecretSpec(pushSecret, imageRepository.GetName(), pushSecret.Name)

			pullSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PullSecretName, Namespace: imageRepository.Namespace}
			pullSecret := waitSecretExist(pullSecretKey)
			defer deleteSecret(pullSecretKey)
			verifySecretSpec(pullSecret, imageRepository.GetName(), pullSecret.Name)

			sa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			Expect(sa.Secrets).To(HaveLen(1))
			Expect(sa.ImagePullSecrets).To(HaveLen(0))
			Expect(sa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pushSecret.Name}))

			pushSecretDockerconfigJson := string(pushSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pushSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PushRobotAccountName, pushToken)

			pullSecretDockerconfigJson := string(pullSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pullSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PullRobotAccountName, pullToken)
		})

		It("should cleanup repository", func() {
			isDeleteRobotAccountForPushInvoked := false
			isDeleteRobotAccountForPullInvoked := false
			quay.DeleteRobotAccountFunc = func(organization, robotAccountName string) (bool, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(strings.HasPrefix(robotAccountName, expectedRobotAccountPrefix)).To(BeTrue())
				if strings.HasSuffix(robotAccountName, "_pull") {
					isDeleteRobotAccountForPushInvoked = true
				} else {
					isDeleteRobotAccountForPullInvoked = true
				}
				return true, nil
			}
			isDeleteRepositoryInvoked := false
			quay.DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				defer GinkgoRecover()
				isDeleteRepositoryInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				return true, nil
			}

			deleteImageRepository(resourceKey)

			Eventually(func() bool { return isDeleteRobotAccountForPullInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRobotAccountForPushInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRepositoryInvoked }, timeout, interval).Should(BeTrue())

			sa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			// verify that secret is unlinked
			Expect(sa.Secrets).To(HaveLen(0))
			Expect(sa.ImagePullSecrets).To(HaveLen(0))

			deleteServiceAccount(types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: defaultNamespace})
		})
	})

	Context("Image repository for component provision", func() {
		var resourceKey = types.NamespacedName{Name: defaultImageRepositoryName + "-componentprovision", Namespace: defaultNamespace}
		var componentKey = types.NamespacedName{Name: defaultComponentName, Namespace: defaultNamespace}
		var applicationKey = types.NamespacedName{Name: defaultComponentApplication, Namespace: defaultNamespace}
		var applicationSaName = getApplicationSaName(defaultComponentApplication)
		var componentSaName = getComponentSaName(defaultComponentName)

		BeforeEach(func() {
			quay.ResetTestQuayClientToFails()
			createApplication(applicationConfig{})
			createComponent(componentConfig{})
		})

		AfterEach(func() {
			deleteComponent(componentKey)
			deleteApplication(applicationKey)
		})

		It("should prepare environment", func() {
			pushToken = "push-token1234"
			pullToken = "pull-token1234"
			expectedImageName = fmt.Sprintf("%s/%s", defaultNamespace, defaultComponentName)
			expectedImage = fmt.Sprintf("quay.io/%s/%s", quay.TestQuayOrg, expectedImageName)
			expectedRobotAccountPrefix = strings.ReplaceAll(strings.ReplaceAll(expectedImageName, "-", "_"), "/", "_")

			createServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			createServiceAccount(defaultNamespace, componentSaName)

			// wait for application SA to be created
			Eventually(func() bool {
				saList := getServiceAccountList(defaultNamespace)
				// there will be 3 service accounts
				// appstudio-pipeline SA, component's SA and application's SA
				return len(saList) == 3
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
		})

		assertProvisionRepository := func(updateComponentAnnotation, grantRepoPermission bool) {
			quay.RepositoryExistsFunc = func(organization, imageRepository string) (bool, error) {
				return true, nil
			}
			isCreateRepositoryInvoked := false
			quay.CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(quay.TestQuayOrg))
				Expect(repository.Visibility).To(Equal("public"))
				Expect(repository.Description).ToNot(BeEmpty())
				return &quay.Repository{Name: expectedImageName}, nil
			}
			isCreatePushRobotAccountInvoked := false
			isCreatePullRobotAccountInvoked := false
			quay.CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(strings.HasPrefix(robotName, expectedRobotAccountPrefix)).To(BeTrue())
				if strings.HasSuffix(robotName, "_pull") {
					isCreatePullRobotAccountInvoked = true
					return &quay.RobotAccount{Name: robotName, Token: pullToken}, nil
				}
				isCreatePushRobotAccountInvoked = true
				return &quay.RobotAccount{Name: robotName, Token: pushToken}, nil
			}
			isAddPushPermissionsToAccountInvoked := false
			isAddPullPermissionsToAccountInvoked := false
			quay.AddPermissionsForRepositoryToAccountFunc = func(organization, imageRepository, accountName string, isRobot, isWrite bool) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				Expect(strings.HasPrefix(accountName, expectedRobotAccountPrefix)).To(BeTrue())
				if strings.HasSuffix(accountName, "_pull") {
					Expect(isWrite).To(BeFalse())
					isAddPullPermissionsToAccountInvoked = true
				} else {
					Expect(isWrite).To(BeTrue())
					isAddPushPermissionsToAccountInvoked = true
				}
				return nil
			}
			isEnsureTeamInvoked := false
			isAddReadPermissionsForRepositoryToTeamInvoked := false
			if grantRepoPermission {
				quay.EnsureTeamFunc = func(organization, teamName string) ([]quay.Member, error) {
					defer GinkgoRecover()
					Expect(organization).To(Equal(quay.TestQuayOrg))
					expectedTeamName := getQuayTeamName(resourceKey.Namespace)
					Expect(teamName).To(Equal(expectedTeamName))
					isEnsureTeamInvoked = true
					return nil, nil
				}
				quay.AddReadPermissionsForRepositoryToTeamFunc = func(organization, imageRepository, teamName string) error {
					defer GinkgoRecover()
					Expect(organization).To(Equal(quay.TestQuayOrg))
					Expect(imageRepository).To(Equal(expectedImageName))
					expectedTeamName := getQuayTeamName(resourceKey.Namespace)
					Expect(teamName).To(Equal(expectedTeamName))
					isAddReadPermissionsForRepositoryToTeamInvoked = true
					return nil
				}
			}
			isCreateNotificationInvoked := false
			quay.CreateNotificationFunc = func(organization, repository string, notification quay.Notification) (*quay.Notification, error) {
				isCreateNotificationInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				return &quay.Notification{UUID: "uuid"}, nil
			}
			isGetNotificationsInvoked := false
			quay.GetNotificationsFunc = func(organization, repository string) ([]quay.Notification, error) {
				isGetNotificationsInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				return []quay.Notification{
					{
						Title:  "test-notification",
						Event:  string(imagerepositoryv1alpha1.NotificationEventRepoPush),
						Method: string(imagerepositoryv1alpha1.NotificationMethodWebhook),
						Config: quay.NotificationConfig{
							Url: "http://test-url",
						},
					},
				}, nil
			}

			imageRepositoryConfigObject := imageRepositoryConfig{
				ResourceKey: &resourceKey,
				Labels: map[string]string{
					ApplicationNameLabelName: defaultComponentApplication,
					ComponentNameLabelName:   defaultComponentName,
				},
				Notifications: []imagerepositoryv1alpha1.Notifications{
					{
						Title:  "test-notification",
						Event:  imagerepositoryv1alpha1.NotificationEventRepoPush,
						Method: imagerepositoryv1alpha1.NotificationMethodWebhook,
						Config: imagerepositoryv1alpha1.NotificationConfig{
							Url: "http://test-url",
						},
					},
				},
			}

			if updateComponentAnnotation {
				imageRepositoryConfigObject.Annotations = map[string]string{updateComponentAnnotationName: "true"}
			}

			createImageRepository(imageRepositoryConfigObject)

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePushRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePullRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPushPermissionsToAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPullPermissionsToAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreateNotificationInvoked }, timeout, interval).Should(BeTrue())
			if grantRepoPermission {
				Eventually(func() bool { return isEnsureTeamInvoked }, timeout, interval).Should(BeTrue())
				Eventually(func() bool { return isAddReadPermissionsForRepositoryToTeamInvoked }, timeout, interval).Should(BeTrue())
			}

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			component := getComponent(componentKey)
			imageRepository := getImageRepository(resourceKey)

			Eventually(func() bool { return isGetNotificationsInvoked }, timeout, interval).Should(BeTrue())
			if updateComponentAnnotation {
				Expect(component.Spec.ContainerImage).To(Equal(imageRepository.Status.Image.URL))
				Expect(imageRepository.Annotations).To(HaveLen(0))
			} else {
				Expect(component.Spec.ContainerImage).To(BeEmpty())
			}

			Expect(imageRepository.Spec.Image.Name).To(Equal(expectedImageName))
			Expect(imageRepository.Spec.Image.Visibility).To(Equal(imagerepositoryv1alpha1.ImageVisibilityPublic))
			Expect(imageRepository.OwnerReferences).To(HaveLen(1))
			Expect(imageRepository.OwnerReferences[0].Name).To(Equal(defaultComponentName))
			Expect(imageRepository.OwnerReferences[0].Kind).To(Equal("Component"))
			Expect(imageRepository.OwnerReferences[0].UID).ToNot(BeEmpty())
			Expect(imageRepository.Status.State).To(Equal(imagerepositoryv1alpha1.ImageRepositoryStateReady))
			Expect(imageRepository.Status.Message).To(BeEmpty())
			Expect(imageRepository.Status.Image.URL).To(Equal(expectedImage))
			Expect(imageRepository.Status.Image.Visibility).To(Equal(imagerepositoryv1alpha1.ImageVisibilityPublic))
			Expect(imageRepository.Status.Credentials.PushRobotAccountName).To(HavePrefix(expectedRobotAccountPrefix))
			Expect(imageRepository.Status.Credentials.PushSecretName).To(Equal(imageRepository.Name + "-image-push"))
			Expect(imageRepository.Status.Credentials.PullRobotAccountName).To(HavePrefix(expectedRobotAccountPrefix))
			Expect(imageRepository.Status.Credentials.PullRobotAccountName).To(HaveSuffix("_pull"))
			Expect(imageRepository.Status.Credentials.PullSecretName).To(Equal(imageRepository.Name + "-image-pull"))
			Expect(imageRepository.Status.Credentials.GenerationTimestamp).ToNot(BeNil())
			Expect(imageRepository.Status.Notifications).To(HaveLen(1))
			Expect(imageRepository.Status.Notifications[0].UUID).To(Equal("uuid"))
			Expect(imageRepository.Status.Notifications[0].Title).To(Equal("test-notification"))

			pushSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PushSecretName, Namespace: imageRepository.Namespace}
			pushSecret := waitSecretExist(pushSecretKey)
			defer deleteSecret(pushSecretKey)
			verifySecretSpec(pushSecret, imageRepository.GetName(), pushSecret.Name)

			pullSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PullSecretName, Namespace: imageRepository.Namespace}
			pullSecret := waitSecretExist(pullSecretKey)
			defer deleteSecret(pullSecretKey)
			verifySecretSpec(pullSecret, imageRepository.GetName(), pullSecret.Name)

			pushSecretDockerconfigJson := string(pushSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pushSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PushRobotAccountName, pushToken)
			pullSecretDockerconfigJson := string(pullSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pullSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PullRobotAccountName, pullToken)

			pipelineSa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			Expect(pipelineSa.Secrets).To(HaveLen(1))
			Expect(pipelineSa.ImagePullSecrets).To(HaveLen(0))
			Expect(pipelineSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pushSecret.Name}))
			componentSa := getServiceAccount(defaultNamespace, componentSaName)
			Expect(componentSa.Secrets).To(HaveLen(1))
			Expect(componentSa.ImagePullSecrets).To(HaveLen(0))
			Expect(componentSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pushSecret.Name}))
			applicationSa := getServiceAccount(defaultNamespace, applicationSaName)
			Expect(applicationSa.Secrets).To(HaveLen(1))
			Expect(applicationSa.ImagePullSecrets).To(HaveLen(1))
			Expect(applicationSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pullSecret.Name}))
			Expect(applicationSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: pullSecret.Name}))
		}

		assertSecretsGoneFromServiceAccounts := func() {
			pipelineSa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			Expect(pipelineSa.Secrets).To(HaveLen(0))
			Expect(pipelineSa.ImagePullSecrets).To(HaveLen(0))
			componentSa := getServiceAccount(defaultNamespace, componentSaName)
			Expect(componentSa.Secrets).To(HaveLen(0))
			Expect(componentSa.ImagePullSecrets).To(HaveLen(0))
			applicationSa := getServiceAccount(defaultNamespace, applicationSaName)
			Expect(applicationSa.Secrets).To(HaveLen(0))
			Expect(applicationSa.ImagePullSecrets).To(HaveLen(0))
		}

		It("should provision image repository for component, without update component annotation", func() {
			assertProvisionRepository(false, false)

			quay.DeleteRobotAccountFunc = func(organization, robotAccountName string) (bool, error) {
				return true, nil
			}
			quay.DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				return true, nil
			}

			deleteImageRepository(resourceKey)
			assertSecretsGoneFromServiceAccounts()
		})

		It("should provision image repository for component, with update component annotation and grant permission to team", func() {
			usersConfigMapKey := types.NamespacedName{Name: additionalUsersConfigMapName, Namespace: resourceKey.Namespace}
			expectedTeamName := getQuayTeamName(resourceKey.Namespace)
			isEnsureTeamInvoked := false
			isListRepositoryPermissionsForTeamInvoked := false
			countAddUserToTeamInvoked := 0
			isDeleteTeamInvoked := false

			quay.EnsureTeamFunc = func(organization, teamName string) ([]quay.Member, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isEnsureTeamInvoked = true
				return []quay.Member{}, nil
			}
			quay.ListRepositoryPermissionsForTeamFunc = func(organization, teamName string) ([]quay.TeamPermission, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isListRepositoryPermissionsForTeamInvoked = true
				return []quay.TeamPermission{}, nil
			}
			quay.AddUserToTeamFunc = func(organization, teamName, userName string) (bool, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				Expect(userName).To(BeElementOf([]string{"user1", "user2"}))
				countAddUserToTeamInvoked++
				return false, nil
			}

			createUsersConfigMap(usersConfigMapKey, []string{"user1", "user2"})
			Eventually(func() bool { return isEnsureTeamInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isListRepositoryPermissionsForTeamInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() int { return countAddUserToTeamInvoked }, timeout, interval).Should(Equal(2))
			waitQuayTeamUsersFinalizerOnConfigMap(usersConfigMapKey)

			assertProvisionRepository(true, true)

			quay.DeleteTeamFunc = func(organization, teamName string) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(teamName).To(Equal(expectedTeamName))
				isDeleteTeamInvoked = true
				return nil
			}
			deleteUsersConfigMap(usersConfigMapKey)
			Eventually(func() bool { return isDeleteTeamInvoked }, timeout, interval).Should(BeTrue())

			quay.DeleteRobotAccountFunc = func(organization, robotAccountName string) (bool, error) {
				return true, nil
			}
			quay.DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				return true, nil
			}
			deleteImageRepository(resourceKey)
			assertSecretsGoneFromServiceAccounts()
		})

		It("should provision image repository for component, with update component annotation", func() {
			assertProvisionRepository(true, false)
		})

		It("should regenerate tokens and update secrets", func() {
			newPushToken := "push-token5678"
			newPullToken := "pull-token5678"

			// Wait just for case it takes less than a second to regenerate credentials
			time.Sleep(time.Second)

			quay.RepositoryExistsFunc = func(organization, imageRepository string) (bool, error) {
				return true, nil
			}
			isRegenerateRobotAccountTokenForPushInvoked := false
			isRegenerateRobotAccountTokenForPullInvoked := false
			quay.RegenerateRobotAccountTokenFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(strings.HasPrefix(robotName, expectedRobotAccountPrefix)).To(BeTrue())
				if strings.HasSuffix(robotName, "_pull") {
					isRegenerateRobotAccountTokenForPullInvoked = true
					return &quay.RobotAccount{Name: robotName, Token: newPullToken}, nil
				}
				isRegenerateRobotAccountTokenForPushInvoked = true
				return &quay.RobotAccount{Name: robotName, Token: newPushToken}, nil
			}
			isGetNotificationsInvoked := false
			quay.GetNotificationsFunc = func(organization, repository string) ([]quay.Notification, error) {
				isGetNotificationsInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				return []quay.Notification{
					{
						Title:  "test-notification",
						Event:  string(imagerepositoryv1alpha1.NotificationEventRepoPush),
						Method: string(imagerepositoryv1alpha1.NotificationMethodWebhook),
						Config: quay.NotificationConfig{
							Url: "http://test-url",
						},
					},
				}, nil
			}

			imageRepository := getImageRepository(resourceKey)
			oldTokenGenerationTimestamp := *imageRepository.Status.Credentials.GenerationTimestamp
			regenerateToken := true
			imageRepository.Spec.Credentials = &imagerepositoryv1alpha1.ImageCredentials{RegenerateToken: &regenerateToken}
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			Eventually(func() bool { return isRegenerateRobotAccountTokenForPushInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isRegenerateRobotAccountTokenForPullInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Spec.Credentials.RegenerateToken == nil &&
					imageRepository.Status.Credentials.GenerationTimestamp != nil &&
					*imageRepository.Status.Credentials.GenerationTimestamp != oldTokenGenerationTimestamp
			}, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isGetNotificationsInvoked }, timeout, interval).Should(BeTrue())

			pushSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PushSecretName, Namespace: imageRepository.Namespace}
			pushSecret := waitSecretExist(pushSecretKey)

			pullSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PullSecretName, Namespace: imageRepository.Namespace}
			pullSecret := waitSecretExist(pullSecretKey)

			Expect(pushSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			pushSecretDockerconfigJson := string(pushSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pushSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PushRobotAccountName, newPushToken)

			Expect(pullSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			pullSecretDockerconfigJson := string(pullSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pullSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PullRobotAccountName, newPullToken)
		})

		It("verify and fix, secret is missing from SAs", func() {
			quay.ResetTestQuayClient()

			// will add it to SA, but not to ImagePullSecrets
			pipelineSa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			pipelineSa.Secrets = []corev1.ObjectReference{}
			Expect(k8sClient.Update(ctx, &pipelineSa)).To(Succeed())

			applicationSa := getServiceAccount(defaultNamespace, applicationSaName)
			applicationSa.Secrets = []corev1.ObjectReference{}
			applicationSa.ImagePullSecrets = []corev1.LocalObjectReference{}
			Expect(k8sClient.Update(ctx, &applicationSa)).To(Succeed())

			componentSa := getServiceAccount(defaultNamespace, componentSaName)
			componentSa.Secrets = []corev1.ObjectReference{}
			Expect(k8sClient.Update(ctx, &componentSa)).To(Succeed())

			imageRepository := getImageRepository(resourceKey)
			verifyLinking := true
			imageRepository.Spec.Credentials = &imagerepositoryv1alpha1.ImageCredentials{VerifyLinking: &verifyLinking}
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			waitImageRepositoryCredentialSectionRequestGone(resourceKey, "verify")

			pushSecretName := fmt.Sprintf("%s-image-push", resourceKey.Name)
			pullSecretName := fmt.Sprintf("%s-image-pull", resourceKey.Name)
			pipelineSa = getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			applicationSa = getServiceAccount(defaultNamespace, applicationSaName)
			componentSa = getServiceAccount(defaultNamespace, componentSaName)
			Expect(pipelineSa.Secrets).To(HaveLen(1))
			Expect(pipelineSa.ImagePullSecrets).To(HaveLen(0))
			Expect(pipelineSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pushSecretName}))
			Expect(componentSa.Secrets).To(HaveLen(1))
			Expect(componentSa.ImagePullSecrets).To(HaveLen(0))
			Expect(componentSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pushSecretName}))
			Expect(applicationSa.Secrets).To(HaveLen(1))
			Expect(applicationSa.ImagePullSecrets).To(HaveLen(1))
			Expect(applicationSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pullSecretName}))
			Expect(applicationSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: pullSecretName}))
		})

		It("verify and fix, secret is duplicated in SA, also is in ImagePullSecrets", func() {
			quay.ResetTestQuayClient()
			pushSecretName := fmt.Sprintf("%s-image-push", resourceKey.Name)
			pullSecretName := fmt.Sprintf("%s-image-pull", resourceKey.Name)

			// will remove duplicate, and remove it from ImagePullSecrets
			pipelineSa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			pipelineSa.Secrets = []corev1.ObjectReference{{Name: pushSecretName}, {Name: pushSecretName}}
			pipelineSa.ImagePullSecrets = []corev1.LocalObjectReference{{Name: pushSecretName}, {Name: pushSecretName}}
			Expect(k8sClient.Update(ctx, &pipelineSa)).To(Succeed())

			applicationSa := getServiceAccount(defaultNamespace, applicationSaName)
			applicationSa.Secrets = []corev1.ObjectReference{{Name: pullSecretName}, {Name: pullSecretName}}
			applicationSa.ImagePullSecrets = []corev1.LocalObjectReference{{Name: pullSecretName}, {Name: pullSecretName}}
			Expect(k8sClient.Update(ctx, &applicationSa)).To(Succeed())

			componentSa := getServiceAccount(defaultNamespace, componentSaName)
			componentSa.Secrets = []corev1.ObjectReference{{Name: pushSecretName}, {Name: pushSecretName}}
			componentSa.ImagePullSecrets = []corev1.LocalObjectReference{{Name: pushSecretName}, {Name: pushSecretName}}
			Expect(k8sClient.Update(ctx, &componentSa)).To(Succeed())

			imageRepository := getImageRepository(resourceKey)
			verifyLinking := true
			imageRepository.Spec.Credentials = &imagerepositoryv1alpha1.ImageCredentials{VerifyLinking: &verifyLinking}
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			waitImageRepositoryCredentialSectionRequestGone(resourceKey, "verify")

			pipelineSa = getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			applicationSa = getServiceAccount(defaultNamespace, applicationSaName)
			componentSa = getServiceAccount(defaultNamespace, componentSaName)
			Expect(pipelineSa.Secrets).To(HaveLen(1))
			Expect(pipelineSa.ImagePullSecrets).To(HaveLen(0))
			Expect(pipelineSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pushSecretName}))
			Expect(componentSa.Secrets).To(HaveLen(1))
			Expect(componentSa.ImagePullSecrets).To(HaveLen(0))
			Expect(componentSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pushSecretName}))
			Expect(applicationSa.Secrets).To(HaveLen(1))
			Expect(applicationSa.ImagePullSecrets).To(HaveLen(1))
			Expect(applicationSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pullSecretName}))
			Expect(applicationSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: pullSecretName}))
		})

		It("should cleanup component repository", func() {
			isDeleteRobotAccountForPushInvoked := false
			isDeleteRobotAccountForPullInvoked := false
			quay.DeleteRobotAccountFunc = func(organization, robotAccountName string) (bool, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(strings.HasPrefix(robotAccountName, expectedRobotAccountPrefix)).To(BeTrue())
				if strings.HasSuffix(robotAccountName, "_pull") {
					isDeleteRobotAccountForPushInvoked = true
				} else {
					isDeleteRobotAccountForPullInvoked = true
				}
				return true, nil
			}
			isDeleteRepositoryInvoked := false
			quay.DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				defer GinkgoRecover()
				isDeleteRepositoryInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				return true, nil
			}

			deleteImageRepository(resourceKey)

			Eventually(func() bool { return isDeleteRobotAccountForPushInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRobotAccountForPullInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRepositoryInvoked }, timeout, interval).Should(BeTrue())

			// verify that secret is unlinked from SAs
			pipelinesSa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			Expect(pipelinesSa.Secrets).To(HaveLen(0))
			Expect(pipelinesSa.ImagePullSecrets).To(HaveLen(0))

			applicationSa := getServiceAccount(defaultNamespace, applicationSaName)
			Expect(applicationSa.Secrets).To(HaveLen(0))
			Expect(applicationSa.ImagePullSecrets).To(HaveLen(0))

			componentSa := getServiceAccount(defaultNamespace, componentSaName)
			Expect(componentSa.Secrets).To(HaveLen(0))
			Expect(componentSa.ImagePullSecrets).To(HaveLen(0))

			deleteServiceAccount(types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: defaultNamespace})
			deleteServiceAccount(types.NamespacedName{Name: componentSaName, Namespace: defaultNamespace})
			deleteServiceAccount(types.NamespacedName{Name: applicationSaName, Namespace: defaultNamespace})
		})
	})

	Context("Notifications", func() {
		var resourceKey = types.NamespacedName{Name: defaultImageRepositoryName + "-notification", Namespace: defaultNamespace}

		BeforeEach(func() {
			quay.ResetTestQuayClient()
		})

		It("should prepare environment", func() {
			pushToken = "push-token1234"
			expectedImageName = fmt.Sprintf("%s/%s", defaultNamespace, resourceKey.Name)
			expectedImage = fmt.Sprintf("quay.io/%s/%s", quay.TestQuayOrg, expectedImageName)
			expectedRobotAccountPrefix = strings.ReplaceAll(strings.ReplaceAll(expectedImageName, "-", "_"), "/", "_")
			createServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
		})

		It("should provision image repository", func() {
			isCreateRepositoryInvoked := false
			quay.CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(quay.TestQuayOrg))
				Expect(repository.Visibility).To(Equal("public"))
				Expect(repository.Description).ToNot(BeEmpty())
				return &quay.Repository{Name: expectedImageName}, nil
			}

			createImageRepository(imageRepositoryConfig{
				ResourceKey:   &resourceKey,
				Notifications: []imagerepositoryv1alpha1.Notifications{},
			})

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			imageRepository := getImageRepository(resourceKey)
			Expect(imageRepository.Spec.Image.Name).To(Equal(expectedImageName))
			Expect(imageRepository.Spec.Image.Visibility).To(Equal(imagerepositoryv1alpha1.ImageVisibilityPublic))
			Expect(imageRepository.OwnerReferences).To(HaveLen(0))
			Expect(imageRepository.Status.State).To(Equal(imagerepositoryv1alpha1.ImageRepositoryStateReady))
			Expect(imageRepository.Status.Message).To(BeEmpty())
			Expect(imageRepository.Status.Image.URL).To(Equal(expectedImage))
			Expect(imageRepository.Status.Image.Visibility).To(Equal(imagerepositoryv1alpha1.ImageVisibilityPublic))
			Expect(imageRepository.Status.Credentials.PushRobotAccountName).To(HavePrefix(expectedRobotAccountPrefix))
			Expect(imageRepository.Status.Credentials.PushSecretName).To(Equal(imageRepository.Name + "-image-push"))
			Expect(imageRepository.Status.Credentials.GenerationTimestamp).ToNot(BeNil())
			Expect(imageRepository.Status.Notifications).To(HaveLen(0))
		})

		It("should add notification", func() {
			notifications := []quay.Notification{}
			isCreateNotificationInvoked := false
			quay.CreateNotificationFunc = func(organization, repository string, notification quay.Notification) (*quay.Notification, error) {
				notifications = append(
					notifications,
					quay.Notification{
						UUID:   "uuid",
						Title:  notification.Title,
						Event:  notification.Event,
						Method: notification.Method,
						Config: notification.Config,
					},
				)
				isCreateNotificationInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				return &quay.Notification{UUID: "uuid", Title: notification.Title}, nil
			}
			isGetNotificationsInvoked := false
			quay.GetNotificationsFunc = func(organization, repository string) ([]quay.Notification, error) {
				isGetNotificationsInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				return notifications, nil
			}

			newNotification := imagerepositoryv1alpha1.Notifications{
				Title:  "test-notification",
				Event:  imagerepositoryv1alpha1.NotificationEventRepoPush,
				Method: imagerepositoryv1alpha1.NotificationMethodWebhook,
				Config: imagerepositoryv1alpha1.NotificationConfig{
					Url: "http://test-url",
				},
			}
			imageRepository := getImageRepository(resourceKey)
			imageRepository.Spec.Notifications = append(imageRepository.Spec.Notifications, newNotification)
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			Eventually(func() bool { return isCreateNotificationInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isGetNotificationsInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return len(imageRepository.Status.Notifications) == 1
			}, timeout, interval).Should(BeTrue())
		})

		It("should update notification", func() {
			updatedUrl := "http://test-url_new"
			isUpdateNotificationInvoked := false
			quay.UpdateNotificationFunc = func(organization, repository string, notificationUuid string, notification quay.Notification) (*quay.Notification, error) {
				isUpdateNotificationInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				return &quay.Notification{
					UUID:   "uuid_new",
					Title:  "test-notification",
					Event:  string(imagerepositoryv1alpha1.NotificationEventRepoPush),
					Method: string(imagerepositoryv1alpha1.NotificationMethodWebhook),
					Config: quay.NotificationConfig{
						Url: updatedUrl,
					},
				}, nil
			}
			isGetNotificationsInvoked := false
			quay.GetNotificationsFunc = func(organization, repository string) ([]quay.Notification, error) {
				isGetNotificationsInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				notifications := []quay.Notification{
					{
						UUID:   "uuid",
						Title:  "test-notification",
						Event:  string(imagerepositoryv1alpha1.NotificationEventRepoPush),
						Method: string(imagerepositoryv1alpha1.NotificationMethodWebhook),
						Config: quay.NotificationConfig{
							Url: "http://test-url",
						},
					},
				}
				return notifications, nil
			}
			imageRepository := getImageRepository(resourceKey)
			imageRepository.Spec.Notifications[0].Config.Url = updatedUrl
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			Eventually(func() bool { return isUpdateNotificationInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isGetNotificationsInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return len(imageRepository.Status.Notifications) == 1 && imageRepository.Spec.Notifications[0].Config.Url == updatedUrl && imageRepository.Status.Notifications[0].UUID == "uuid_new"
			}, timeout, interval).Should(BeTrue())
		})

		It("should delete notification", func() {
			notifications := []quay.Notification{
				{
					UUID:   "uuid_new",
					Title:  "test-notification",
					Event:  string(imagerepositoryv1alpha1.NotificationEventRepoPush),
					Method: string(imagerepositoryv1alpha1.NotificationMethodWebhook),
					Config: quay.NotificationConfig{
						Url: "http://test-url_new",
					},
				},
			}
			isDeleteNotificationInvoked := false
			quay.DeleteNotificationFunc = func(organization, repository string, notificationUuid string) (bool, error) {
				isDeleteNotificationInvoked = true
				notifications = notifications[:1]
				Expect(organization).To(Equal(quay.TestQuayOrg))
				return true, nil
			}
			isGetNotificationsInvoked := false
			quay.GetNotificationsFunc = func(organization, repository string) ([]quay.Notification, error) {
				isGetNotificationsInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				return notifications, nil
			}

			imageRepository := getImageRepository(resourceKey)
			Expect(imageRepository.Status.Notifications).To(HaveLen(1))
			imageRepository.Spec.Notifications = imageRepository.Spec.Notifications[:len(imageRepository.Spec.Notifications)-1]
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			Eventually(func() bool { return isDeleteNotificationInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isGetNotificationsInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return len(imageRepository.Status.Notifications) == 0
			}, timeout, interval).Should(BeTrue())
		})

		It("should provision image repository with some notifications to create", func() {
			deleteImageRepository(resourceKey)
			isCreateNotificationInvoked := false
			quay.CreateNotificationFunc = func(organization, repository string, notification quay.Notification) (*quay.Notification, error) {
				isCreateNotificationInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				return &quay.Notification{UUID: "uuid"}, nil
			}
			createImageRepository(imageRepositoryConfig{
				ResourceKey: &resourceKey,
				Notifications: []imagerepositoryv1alpha1.Notifications{
					{
						Title:  "test-notification",
						Event:  imagerepositoryv1alpha1.NotificationEventRepoPush,
						Method: imagerepositoryv1alpha1.NotificationMethodWebhook,
						Config: imagerepositoryv1alpha1.NotificationConfig{
							Url: "http://test-url",
						},
					},
					{
						Title:  "test-notification-2",
						Event:  imagerepositoryv1alpha1.NotificationEventRepoPush,
						Method: imagerepositoryv1alpha1.NotificationMethodWebhook,
						Config: imagerepositoryv1alpha1.NotificationConfig{
							Url: "http://test-url-2",
						},
					},
				},
			})
			Eventually(func() bool { return isCreateNotificationInvoked }, timeout, interval).Should(BeTrue())

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			imageRepository := getImageRepository(resourceKey)
			Expect(imageRepository.Status.Notifications).To(HaveLen(2))
			Expect(imageRepository.Status.Notifications[0].Title).To(Equal("test-notification"))
			Expect(imageRepository.Status.Notifications[1].Title).To(Equal("test-notification-2"))
		})

		It("should clean environment", func() {
			deleteServiceAccount(types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: defaultNamespace})
		})
	})

	Context("Other image repository scenarios", func() {
		var resourceKey = types.NamespacedName{Name: defaultImageRepositoryName + "-other", Namespace: defaultNamespace}

		BeforeEach(func() {
			quay.ResetTestQuayClient()
			deleteImageRepository(resourceKey)
		})

		It("should prepare environment", func() {
			createServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
		})

		It("should create image repository with requested name", func() {
			customImageName := "my-image"
			expectedImageName = defaultNamespace + "/" + customImageName

			isCreateRepositoryInvoked := false
			quay.CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(quay.TestQuayOrg))
				Expect(repository.Visibility).To(Equal("public"))
				Expect(repository.Description).ToNot(BeEmpty())
				return &quay.Repository{Name: expectedImageName}, nil
			}

			createImageRepository(imageRepositoryConfig{ImageName: customImageName, ResourceKey: &resourceKey})
			defer deleteImageRepository(resourceKey)

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			imageRepository := getImageRepository(resourceKey)
			Expect(imageRepository.Spec.Image.Name).To(Equal(expectedImageName))
		})

		It("should create image repository with requested name that already includes namespace", func() {
			customImageName := defaultNamespace + "/" + "my-image-with-namespace"
			expectedImageName = customImageName

			isCreateRepositoryInvoked := false
			quay.CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(quay.TestQuayOrg))
				Expect(repository.Visibility).To(Equal("public"))
				Expect(repository.Description).ToNot(BeEmpty())
				return &quay.Repository{Name: expectedImageName}, nil
			}

			createImageRepository(imageRepositoryConfig{ImageName: customImageName, ResourceKey: &resourceKey})
			defer deleteImageRepository(resourceKey)

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			imageRepository := getImageRepository(resourceKey)
			Expect(imageRepository.Spec.Image.Name).To(Equal(expectedImageName))
		})

		It("don't remove repository if 2 imageRepositories use the same repo and one is removed", func() {
			customImageName := defaultNamespace + "/" + "my-image-used-by-multiple-imagerepositories"
			expectedImageName = customImageName
			expectedRobotAccountPrefix = strings.ReplaceAll(strings.ReplaceAll(expectedImageName, "-", "_"), "/", "_")

			isCreateRepositoryInvoked := false
			quay.CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(quay.TestQuayOrg))
				return &quay.Repository{Name: expectedImageName}, nil
			}

			createImageRepository(imageRepositoryConfig{ImageName: customImageName, ResourceKey: &resourceKey})
			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			anotherImageRepositoryKey := types.NamespacedName{Name: defaultImageRepositoryName + "-other2", Namespace: defaultNamespace}
			isCreateRepositoryInvoked = false

			createImageRepository(imageRepositoryConfig{ImageName: customImageName, ResourceKey: &anotherImageRepositoryKey})
			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			waitImageRepositoryFinalizerOnImageRepository(anotherImageRepositoryKey)

			imageRepository1 := getImageRepository(resourceKey)
			Expect(imageRepository1.Spec.Image.Name).To(Equal(expectedImageName))
			imageRepository2 := getImageRepository(anotherImageRepositoryKey)
			Expect(imageRepository2.Spec.Image.Name).To(Equal(expectedImageName))
			Expect(imageRepository1.Status.Image.URL).To(Equal(imageRepository2.Status.Image.URL))

			isDeleteRobotAccountInvoked := false
			quay.DeleteRobotAccountFunc = func(organization, robotAccountName string) (bool, error) {
				defer GinkgoRecover()
				isDeleteRobotAccountInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(strings.HasPrefix(robotAccountName, expectedRobotAccountPrefix)).To(BeTrue())
				return true, nil
			}
			quay.DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				defer GinkgoRecover()
				if imageRepository == expectedImage {
					Fail("Delete repository should not be invoked")
				}
				return true, nil
			}

			deleteImageRepository(resourceKey)
			// should not delete repository, because it is used by other ImageRepository
			Eventually(func() bool { return isDeleteRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			waitImageRepositoryGone(resourceKey)

			isDeleteRepositoryInvoked := false
			quay.DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				defer GinkgoRecover()
				isDeleteRepositoryInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(customImageName))
				return true, nil
			}

			isDeleteRobotAccountInvoked = false
			deleteImageRepository(anotherImageRepositoryKey)
			// should delete repository, because no other ImageRepository uses the repository
			Eventually(func() bool { return isDeleteRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRepositoryInvoked }, timeout, interval).Should(BeTrue())
		})

		It("should clean environment", func() {
			deleteServiceAccount(types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: defaultNamespace})
		})
	})

	Context("Skip quay repository deletion", func() {
		var resourceKey = types.NamespacedName{Name: defaultImageRepositoryName + "-skip-delete", Namespace: defaultNamespace}

		BeforeEach(func() {
			quay.ResetTestQuayClient()
			deleteImageRepository(resourceKey)
		})

		It("should prepare environment", func() {
			createServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
		})

		It("don't remove repository if explicitly requested using annotation", func() {
			customImageName := defaultNamespace + "/" + "my-image-should-not-be-removed"
			expectedImageName = customImageName
			expectedRobotAccountPrefix = strings.ReplaceAll(strings.ReplaceAll(expectedImageName, "-", "_"), "/", "_")

			isCreateRepositoryInvoked := false
			quay.CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(quay.TestQuayOrg))
				return &quay.Repository{Name: expectedImageName}, nil
			}

			createImageRepository(imageRepositoryConfig{
				ImageName:   customImageName,
				ResourceKey: &resourceKey,
				Annotations: map[string]string{skipRepositoryDeletionAnnotationName: "true"},
			})
			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			imageRepository := getImageRepository(resourceKey)
			Expect(imageRepository.Spec.Image.Name).To(Equal(expectedImageName))

			isDeleteRobotAccountInvoked := false
			quay.DeleteRobotAccountFunc = func(organization, robotAccountName string) (bool, error) {
				defer GinkgoRecover()
				isDeleteRobotAccountInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(strings.HasPrefix(robotAccountName, expectedRobotAccountPrefix)).To(BeTrue())
				return true, nil
			}

			isDeleteRepositoryInvoked := false
			quay.DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				isDeleteRepositoryInvoked = true
				return true, nil
			}

			deleteImageRepository(resourceKey)
			// should not delete repository, because of the annotation
			Expect(isDeleteRobotAccountInvoked).To(BeTrue())
			Expect(isDeleteRepositoryInvoked).To(BeFalse())
		})

		It("should clean environment", func() {
			deleteServiceAccount(types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: defaultNamespace})
		})
	})

	Context("Image repository error scenarios", func() {
		var resourceKey = types.NamespacedName{Name: defaultImageRepositoryName + "-error", Namespace: defaultNamespace}

		BeforeEach(func() {
			quay.ResetTestQuayClient()
			deleteImageRepository(resourceKey)
		})

		It("should prepare environment", func() {
			pushToken = "push-token1234"
			expectedImageName = fmt.Sprintf("%s/%s", defaultNamespace, resourceKey.Name)
			expectedImage = fmt.Sprintf("quay.io/%s/%s", quay.TestQuayOrg, expectedImageName)
			expectedRobotAccountPrefix = strings.ReplaceAll(strings.ReplaceAll(expectedImageName, "-", "_"), "/", "_")

			createServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
		})

		It("should permanently fail if private image repository requested on creation but quota exceeded", func() {
			isCreateRepositoryInvoked := false
			quay.CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(quay.TestQuayOrg))
				Expect(repository.Visibility).To(Equal("private"))
				Expect(repository.Description).ToNot(BeEmpty())
				return nil, fmt.Errorf("payment required")
			}

			createImageRepository(imageRepositoryConfig{Visibility: "private", ResourceKey: &resourceKey})

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())

			imageRepository := &imagerepositoryv1alpha1.ImageRepository{}
			Eventually(func() bool {
				imageRepository = getImageRepository(resourceKey)
				return string(imageRepository.Status.State) != ""
			}, timeout, interval).Should(BeTrue())
			Expect(imageRepository.Status.State).To(Equal(imagerepositoryv1alpha1.ImageRepositoryStateFailed))
			Expect(imageRepository.Status.Message).ToNot(BeEmpty())
			Expect(imageRepository.Status.Message).To(ContainSubstring("exceeds current quay plan limit"))

			deleteImageRepository(resourceKey)
		})

		It("should add error message and revert visibility in spec if private visibility requested after provision but quota exceeded", func() {
			quay.ResetTestQuayClient()

			quay.CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				return &quay.Repository{Name: expectedImageName}, nil
			}
			quay.CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				return &quay.RobotAccount{Name: robotName, Token: pushToken}, nil
			}
			createImageRepository(imageRepositoryConfig{ResourceKey: &resourceKey})
			defer deleteImageRepository(resourceKey)

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			quay.ResetTestQuayClientToFails()

			isChangeRepositoryVisibilityInvoked := false
			quay.ChangeRepositoryVisibilityFunc = func(organization, imageRepository, visibility string) error {
				defer GinkgoRecover()
				isChangeRepositoryVisibilityInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				Expect(visibility).To(Equal(string(imagerepositoryv1alpha1.ImageVisibilityPrivate)))
				return fmt.Errorf("payment required")
			}

			imageRepository := getImageRepository(resourceKey)
			imageRepository.Spec.Image.Visibility = imagerepositoryv1alpha1.ImageVisibilityPrivate
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			Eventually(func() bool { return isChangeRepositoryVisibilityInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Spec.Image.Visibility == imagerepositoryv1alpha1.ImageVisibilityPublic &&
					imageRepository.Status.Image.Visibility == imagerepositoryv1alpha1.ImageVisibilityPublic &&
					imageRepository.Status.Message != ""
			}, timeout, interval).Should(BeTrue())

			quay.ResetTestQuayClient()
			deleteImageRepository(resourceKey)
		})

		It("should fail if invalid image repository linked by annotation to unexisting component", func() {
			quay.ResetTestQuayClientToFails()

			createImageRepository(imageRepositoryConfig{
				ResourceKey: &resourceKey,
				ImageName:   fmt.Sprintf("%s/%s", defaultComponentApplication, defaultComponentName),
				Labels: map[string]string{
					ApplicationNameLabelName: defaultComponentApplication,
					ComponentNameLabelName:   defaultComponentName,
				},
			})
			defer deleteImageRepository(resourceKey)

			errorMessage := fmt.Sprintf("Component '%s' does not exist", defaultComponentName)
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Status.Message == errorMessage
			}, timeout, interval).Should(BeTrue())
		})

		It("should fail if invalid image repository name given", func() {
			imageRepository := getImageRepositoryConfig(imageRepositoryConfig{
				ImageName: "wrong&name",
			})
			Expect(k8sClient.Create(ctx, imageRepository)).ToNot(Succeed())
		})

		It("should clean environment", func() {
			deleteServiceAccount(types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: defaultNamespace})
		})
	})
})
