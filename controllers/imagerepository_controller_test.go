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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
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
		authRegexp = regexp.MustCompile(`.*{"auth":"([A-Za-z0-9+/=]*)"}.*`)

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
			isCreateRobotAccountInvoked := false
			quay.CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				isCreateRobotAccountInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(strings.HasPrefix(robotName, expectedRobotAccountPrefix)).To(BeTrue())
				return &quay.RobotAccount{Name: robotName, Token: pushToken}, nil
			}
			isAddPushPermissionsToRobotAccountInvoked := false
			quay.AddPermissionsForRepositoryToRobotAccountFunc = func(organization, imageRepository, robotAccountName string, isWrite bool) error {
				defer GinkgoRecover()
				isAddPushPermissionsToRobotAccountInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				Expect(isWrite).To(BeTrue())
				Expect(strings.HasPrefix(robotAccountName, expectedRobotAccountPrefix)).To(BeTrue())
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
			Eventually(func() bool { return isCreateRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPushPermissionsToRobotAccountInvoked }, timeout, interval).Should(BeTrue())
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
			Expect(imageRepository.Status.Credentials.GenerationTimestamp).ToNot(BeNil())
			Expect(imageRepository.Status.Notifications).To(HaveLen(0))

			pushSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PushSecretName, Namespace: imageRepository.Namespace}
			pushSecret := waitSecretExist(pushSecretKey)
			defer deleteSecret(pushSecretKey)
			Expect(pushSecret.OwnerReferences).To(HaveLen(1))
			Expect(pushSecret.OwnerReferences[0].Kind).To(Equal("ImageRepository"))
			Expect(pushSecret.OwnerReferences[0].Name).To(Equal(imageRepository.GetName()))
			Expect(pushSecret.Labels[InternalSecretLabelName]).To(Equal("true"))
			Expect(pushSecret.Name).To(Equal(pushSecret.Name))
			Expect(pushSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))

			sa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			Expect(sa.Secrets).To(HaveLen(1))
			Expect(sa.ImagePullSecrets).To(HaveLen(0))
			Expect(sa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pushSecret.Name}))

			pushSecretDockerconfigJson := string(pushSecret.Data[corev1.DockerConfigJsonKey])
			var authDataJson interface{}
			Expect(json.Unmarshal([]byte(pushSecretDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(pushSecretDockerconfigJson).To(ContainSubstring(expectedImage))
			pushSecretAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(pushSecretDockerconfigJson)[1])
			Expect(err).To(Succeed())
			pushRobotAccountName := imageRepository.Status.Credentials.PushRobotAccountName
			Expect(string(pushSecretAuthString)).To(Equal(fmt.Sprintf("%s:%s", pushRobotAccountName, pushToken)))
		})

		It("should regenerate token", func() {
			newToken := "push-token5678"

			// Wait just for case it takes less than a second to regenerate credentials
			time.Sleep(time.Second)

			isRegenerateRobotAccountTokenInvoked := false
			quay.RegenerateRobotAccountTokenFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				isRegenerateRobotAccountTokenInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(strings.HasPrefix(robotName, expectedRobotAccountPrefix)).To(BeTrue())
				return &quay.RobotAccount{Name: robotName, Token: newToken}, nil
			}

			imageRepository := getImageRepository(resourceKey)
			oldTokenGenerationTimestamp := *imageRepository.Status.Credentials.GenerationTimestamp
			regenerateToken := true
			imageRepository.Spec.Credentials = &imagerepositoryv1alpha1.ImageCredentials{RegenerateToken: &regenerateToken}
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			Eventually(func() bool { return isRegenerateRobotAccountTokenInvoked }, timeout, interval).Should(BeTrue())
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
			var authDataJson interface{}
			Expect(json.Unmarshal([]byte(pushSecretDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(pushSecretDockerconfigJson).To(ContainSubstring(expectedImage))
			pushSecretAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(pushSecretDockerconfigJson)[1])
			Expect(err).To(Succeed())
			pushRobotAccountName := imageRepository.Status.Credentials.PushRobotAccountName
			Expect(string(pushSecretAuthString)).To(Equal(fmt.Sprintf("%s:%s", pushRobotAccountName, newToken)))
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

		It("verify and fix, secret is missing", func() {
			// will remove it from SA
			secretName := types.NamespacedName{Name: fmt.Sprintf("%s-image-push", resourceKey.Name), Namespace: defaultNamespace}
			deleteSecret(secretName)

			imageRepository := getImageRepository(resourceKey)
			verifyLinking := true
			imageRepository.Spec.Credentials = &imagerepositoryv1alpha1.ImageCredentials{VerifyLinking: &verifyLinking}
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			waitImageRepositoryCredentialSectionRequestGone(resourceKey, "verify")

			sa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			Expect(sa.Secrets).To(HaveLen(0))
			Expect(sa.ImagePullSecrets).To(HaveLen(0))
		})

		It("should cleanup repository", func() {
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
				defer GinkgoRecover()
				isDeleteRepositoryInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				return true, nil
			}

			deleteImageRepository(resourceKey)

			Eventually(func() bool { return isDeleteRobotAccountInvoked }, timeout, interval).Should(BeTrue())
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
			isCreateRobotAccountInvoked := false
			quay.CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				isCreateRobotAccountInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(strings.HasPrefix(robotName, expectedRobotAccountPrefix)).To(BeTrue())
				return &quay.RobotAccount{Name: robotName, Token: pushToken}, nil
			}
			isAddPushPermissionsToRobotAccountInvoked := false
			quay.AddPermissionsForRepositoryToRobotAccountFunc = func(organization, imageRepository, robotAccountName string, isWrite bool) error {
				defer GinkgoRecover()
				isAddPushPermissionsToRobotAccountInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				Expect(isWrite).To(BeTrue())
				Expect(strings.HasPrefix(robotAccountName, expectedRobotAccountPrefix)).To(BeTrue())
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
			Eventually(func() bool { return isCreateRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPushPermissionsToRobotAccountInvoked }, timeout, interval).Should(BeTrue())
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
			Expect(imageRepository.Status.Credentials.GenerationTimestamp).ToNot(BeNil())
			Expect(imageRepository.Status.Notifications).To(HaveLen(0))

			pushSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PushSecretName, Namespace: imageRepository.Namespace}
			pushSecret := waitSecretExist(pushSecretKey)

			defer deleteSecret(pushSecretKey)
			Expect(pushSecret.OwnerReferences).To(HaveLen(1))
			Expect(pushSecret.OwnerReferences[0].Kind).To(Equal("ImageRepository"))
			Expect(pushSecret.OwnerReferences[0].Name).To(Equal(imageRepository.GetName()))
			Expect(pushSecret.Labels[InternalSecretLabelName]).To(Equal("true"))
			Expect(pushSecret.Name).To(Equal(pushSecret.Name))
			Expect(pushSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))

			sa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			Expect(sa.Secrets).To(HaveLen(1))
			Expect(sa.ImagePullSecrets).To(HaveLen(0))
			Expect(sa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pushSecret.Name}))

			pushSecretDockerconfigJson := string(pushSecret.Data[corev1.DockerConfigJsonKey])
			var authDataJson interface{}
			Expect(json.Unmarshal([]byte(pushSecretDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(pushSecretDockerconfigJson).To(ContainSubstring(expectedImage))
			pushSecretAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(pushSecretDockerconfigJson)[1])
			Expect(err).To(Succeed())
			pushRobotAccountName := imageRepository.Status.Credentials.PushRobotAccountName
			Expect(string(pushSecretAuthString)).To(Equal(fmt.Sprintf("%s:%s", pushRobotAccountName, pushToken)))
		})

		It("should cleanup repository", func() {
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
				defer GinkgoRecover()
				isDeleteRepositoryInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				return true, nil
			}

			deleteImageRepository(resourceKey)

			Eventually(func() bool { return isDeleteRobotAccountInvoked }, timeout, interval).Should(BeTrue())
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
		componentKey := types.NamespacedName{Name: defaultComponentName, Namespace: defaultNamespace}

		BeforeEach(func() {
			quay.ResetTestQuayClientToFails()
			createComponent(componentConfig{})
		})

		AfterEach(func() {
			deleteComponent(componentKey)
		})

		It("should prepare environment", func() {
			pushToken = "push-token1234"
			pullToken = "pull-token1234"
			expectedImageName = fmt.Sprintf("%s/%s/%s", defaultNamespace, defaultComponentApplication, defaultComponentName)
			expectedImage = fmt.Sprintf("quay.io/%s/%s", quay.TestQuayOrg, expectedImageName)
			expectedRobotAccountPrefix = strings.ReplaceAll(strings.ReplaceAll(expectedImageName, "-", "_"), "/", "_")

			createServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
		})

		assertProvisionRepository := func(updateComponentAnnotation bool) {
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
			isAddPushPermissionsToRobotAccountInvoked := false
			isAddPullPermissionsToRobotAccountInvoked := false
			quay.AddPermissionsForRepositoryToRobotAccountFunc = func(organization, imageRepository, robotAccountName string, isWrite bool) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				Expect(strings.HasPrefix(robotAccountName, expectedRobotAccountPrefix)).To(BeTrue())
				if strings.HasSuffix(robotAccountName, "_pull") {
					Expect(isWrite).To(BeFalse())
					isAddPullPermissionsToRobotAccountInvoked = true
				} else {
					Expect(isWrite).To(BeTrue())
					isAddPushPermissionsToRobotAccountInvoked = true
				}
				return nil
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
			Eventually(func() bool { return isAddPushPermissionsToRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPullPermissionsToRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreateNotificationInvoked }, timeout, interval).Should(BeTrue())

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			component := getComponent(componentKey)
			imageRepository := getImageRepository(resourceKey)

			if updateComponentAnnotation {
				Expect(component.Spec.ContainerImage).To(Equal(imageRepository.Status.Image.URL))
				Expect(imageRepository.Annotations).To(HaveLen(0))
				Eventually(func() bool { return isGetNotificationsInvoked }, timeout, interval).Should(BeTrue())
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
			Expect(pushSecret.Labels[InternalSecretLabelName]).To(Equal("true"))
			Expect(pushSecret.OwnerReferences).To(HaveLen(1))
			Expect(pushSecret.OwnerReferences[0].Kind).To(Equal("ImageRepository"))
			Expect(pushSecret.OwnerReferences[0].Name).To(Equal(imageRepository.GetName()))
			Expect(pushSecret.Name).To(Equal(pushSecret.Name))
			Expect(pushSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))

			pullSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PullSecretName, Namespace: imageRepository.Namespace}
			pullSecret := waitSecretExist(pullSecretKey)
			defer deleteSecret(pullSecretKey)

			Expect(pullSecret.Labels[InternalSecretLabelName]).To(Equal("true"))
			Expect(pullSecret.OwnerReferences).To(HaveLen(1))
			Expect(pullSecret.OwnerReferences[0].Name).To(Equal(imageRepository.Name))
			Expect(pullSecret.OwnerReferences[0].Kind).To(Equal("ImageRepository"))
			Expect(pullSecret.Name).To(Equal(pullSecretKey.Name))
			Expect(pullSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))

			var authDataJson interface{}

			pushSecretDockerconfigJson := string(pushSecret.Data[corev1.DockerConfigJsonKey])
			Expect(json.Unmarshal([]byte(pushSecretDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(pushSecretDockerconfigJson).To(ContainSubstring(expectedImage))
			pushSecretAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(pushSecretDockerconfigJson)[1])
			Expect(err).To(Succeed())
			pushRobotAccountName := imageRepository.Status.Credentials.PushRobotAccountName
			Expect(string(pushSecretAuthString)).To(Equal(fmt.Sprintf("%s:%s", pushRobotAccountName, pushToken)))

			pullSecretDockerconfigJson := string(pullSecret.Data[corev1.DockerConfigJsonKey])
			Expect(json.Unmarshal([]byte(pullSecretDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(pullSecretDockerconfigJson).To(ContainSubstring(expectedImage))
			pullSecretAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(pullSecretDockerconfigJson)[1])
			Expect(err).To(Succeed())
			pullRobotAccountName := imageRepository.Status.Credentials.PullRobotAccountName
			Expect(string(pullSecretAuthString)).To(Equal(fmt.Sprintf("%s:%s", pullRobotAccountName, pullToken)))

			sa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			Expect(sa.Secrets).To(HaveLen(1))
			Expect(sa.ImagePullSecrets).To(HaveLen(0))
			Expect(sa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pushSecret.Name}))
		}

		It("should provision image repository for component, without update component annotation", func() {
			assertProvisionRepository(false)

			quay.DeleteRobotAccountFunc = func(organization, robotAccountName string) (bool, error) {
				return true, nil
			}
			quay.DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				return true, nil
			}

			deleteImageRepository(resourceKey)
		})

		It("should provision image repository for component, with update component annotation", func() {
			assertProvisionRepository(true)
		})

		It("should regenerate tokens and update secrets", func() {
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
			defer deleteSecret(pushSecretKey)

			pullSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PullSecretName, Namespace: imageRepository.Namespace}
			pullSecret := waitSecretExist(pullSecretKey)
			defer deleteSecret(pullSecretKey)

			var authDataJson interface{}

			Expect(pushSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			pushSecretDockerconfigJson := string(pushSecret.Data[corev1.DockerConfigJsonKey])
			Expect(json.Unmarshal([]byte(pushSecretDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(pushSecretDockerconfigJson).To(ContainSubstring(expectedImage))
			pushSecretAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(pushSecretDockerconfigJson)[1])
			Expect(err).To(Succeed())
			pushRobotAccountName := imageRepository.Status.Credentials.PushRobotAccountName
			Expect(string(pushSecretAuthString)).To(Equal(fmt.Sprintf("%s:%s", pushRobotAccountName, newPushToken)))

			Expect(pullSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			pullSecretDockerconfigJson := string(pullSecret.Data[corev1.DockerConfigJsonKey])
			Expect(json.Unmarshal([]byte(pullSecretDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(pullSecretDockerconfigJson).To(ContainSubstring(expectedImage))
			pullSecretAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(pullSecretDockerconfigJson)[1])
			Expect(err).To(Succeed())
			pullRobotAccountName := imageRepository.Status.Credentials.PullRobotAccountName
			Expect(string(pullSecretAuthString)).To(Equal(fmt.Sprintf("%s:%s", pullRobotAccountName, newPullToken)))
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

			sa := getServiceAccount(defaultNamespace, buildPipelineServiceAccountName)
			// verify that secret is unlinked
			Expect(sa.Secrets).To(HaveLen(0))
			Expect(sa.ImagePullSecrets).To(HaveLen(0))

			deleteServiceAccount(types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: defaultNamespace})
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
