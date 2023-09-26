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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
	remotesecretv1beta1 "github.com/redhat-appstudio/remote-secret/api/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	imagerepositoryv1alpha1 "github.com/redhat-appstudio/image-controller/api/v1alpha1"
)

var _ = Describe("Image repository controller", func() {

	var (
		authRegexp = regexp.MustCompile(`.*{"auth":"([A-Za-z0-9+/=]*)"}.*`)

		resourceKey = types.NamespacedName{Name: defaultImageRepositoryName, Namespace: defaultNamespace}

		pushToken                  string
		pullToken                  string
		expectedRobotAccountPrefix string
		expectedImageName          string
		expectedImage              string
	)

	Context("Image repository provision", func() {

		BeforeEach(func() {
			ResetTestQuayClientToFails()
			deleteUploadSecrets(defaultNamespace)
		})

		It("should prepare environment", func() {
			createNamespace(defaultNamespace)

			pushToken = "push-token1234"
			expectedImageName = fmt.Sprintf("%s/%s", defaultNamespace, defaultImageRepositoryName)
			expectedImage = fmt.Sprintf("quay.io/%s/%s", testQuayOrg, expectedImageName)
			expectedRobotAccountPrefix = strings.ReplaceAll(strings.ReplaceAll(expectedImageName, "-", "_"), "/", "_")
		})

		It("should provision image repository", func() {
			isCreateRepositoryInvoked := false
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(testQuayOrg))
				Expect(repository.Visibility).To(Equal("public"))
				Expect(repository.Description).ToNot(BeEmpty())
				return &quay.Repository{Name: expectedImageName}, nil
			}
			isCreateRobotAccountInvoked := false
			CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				isCreateRobotAccountInvoked = true
				Expect(organization).To(Equal(testQuayOrg))
				Expect(strings.HasPrefix(robotName, expectedRobotAccountPrefix)).To(BeTrue())
				return &quay.RobotAccount{Name: robotName, Token: pushToken}, nil
			}
			isAddPushPermissionsToRobotAccountInvoked := false
			AddPermissionsForRepositoryToRobotAccountFunc = func(organization, imageRepository, robotAccountName string, isWrite bool) error {
				defer GinkgoRecover()
				isAddPushPermissionsToRobotAccountInvoked = true
				Expect(organization).To(Equal(testQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				Expect(isWrite).To(BeTrue())
				Expect(strings.HasPrefix(robotAccountName, expectedRobotAccountPrefix)).To(BeTrue())
				return nil
			}

			createImageRepository(imageRepositoryConfig{})

			uploadSecretKey := types.NamespacedName{Name: "upload-secret-" + resourceKey.Name + "-image-push", Namespace: resourceKey.Namespace}
			defer deleteSecret(uploadSecretKey)

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreateRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPushPermissionsToRobotAccountInvoked }, timeout, interval).Should(BeTrue())

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
			Expect(imageRepository.Status.Credentials.PushRemoteSecretName).To(Equal(imageRepository.Name + "-image-push"))
			Expect(imageRepository.Status.Credentials.PushSecretName).To(Equal(imageRepository.Name + "-image-push"))
			Expect(imageRepository.Status.Credentials.GenerationTimestamp).ToNot(BeNil())

			remoteSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PushRemoteSecretName, Namespace: imageRepository.Namespace}
			remoteSecret := waitRemoteSecretExist(remoteSecretKey)
			Expect(remoteSecret.OwnerReferences).To(HaveLen(1))
			Expect(remoteSecret.OwnerReferences[0].Kind).To(Equal("ImageRepository"))
			Expect(remoteSecret.OwnerReferences[0].Name).To(Equal(imageRepository.GetName()))
			Expect(remoteSecret.Labels[InternalRemoteSecretLabelName]).To(Equal("true"))
			Expect(remoteSecret.Spec.Secret.Name).To(Equal(remoteSecret.Name))
			Expect(remoteSecret.Spec.Secret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			Expect(remoteSecret.Spec.Secret.LinkedTo).To(HaveLen(1))
			Expect(remoteSecret.Spec.Secret.LinkedTo[0].ServiceAccount.Reference.Name).To(Equal(buildPipelineServiceAccountName))
			Expect(remoteSecret.Spec.Targets).To(HaveLen(1))
			Expect(remoteSecret.Spec.Targets[0].Namespace).To(Equal(imageRepository.Namespace))

			uploadSecret := waitSecretExist(uploadSecretKey)
			Expect(uploadSecret.Labels[remotesecretv1beta1.UploadSecretLabel]).To(Equal("remotesecret"))
			Expect(uploadSecret.Annotations[remotesecretv1beta1.RemoteSecretNameAnnotation]).To(Equal(remoteSecret.Name))
			Expect(uploadSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			uploadSecretDockerconfigJson := string(uploadSecret.Data[corev1.DockerConfigJsonKey])
			var authDataJson interface{}
			Expect(json.Unmarshal([]byte(uploadSecretDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(uploadSecretDockerconfigJson).To(ContainSubstring(expectedImage))
			uploadSecretAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(uploadSecretDockerconfigJson)[1])
			Expect(err).To(Succeed())
			pushRobotAccountName := imageRepository.Status.Credentials.PushRobotAccountName
			Expect(string(uploadSecretAuthString)).To(Equal(fmt.Sprintf("%s:%s", pushRobotAccountName, pushToken)))
		})

		It("should regenerate token", func() {
			newToken := "push-token5678"

			uploadSecretKey := types.NamespacedName{Name: "upload-secret-" + resourceKey.Name + "-image-push", Namespace: resourceKey.Namespace}

			// Wait just for case it takes less than a second to regenerate credentials
			time.Sleep(time.Second)

			isRegenerateRobotAccountTokenInvoked := false
			RegenerateRobotAccountTokenFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				isRegenerateRobotAccountTokenInvoked = true
				Expect(organization).To(Equal(testQuayOrg))
				Expect(strings.HasPrefix(robotName, expectedRobotAccountPrefix)).To(BeTrue())
				return &quay.RobotAccount{Name: robotName, Token: newToken}, nil
			}

			imageRepository := getImageRepository(resourceKey)
			oldTokenGenerationTimestamp := *imageRepository.Status.Credentials.GenerationTimestamp
			regenerateToken := true
			imageRepository.Spec.Credentials = &imagerepositoryv1alpha1.ImageCredentials{RegenerateToken: &regenerateToken}
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())
			defer deleteSecret(uploadSecretKey)

			Eventually(func() bool { return isRegenerateRobotAccountTokenInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Spec.Credentials.RegenerateToken == nil &&
					imageRepository.Status.Credentials.GenerationTimestamp != nil &&
					*imageRepository.Status.Credentials.GenerationTimestamp != oldTokenGenerationTimestamp
			}, timeout, interval).Should(BeTrue())

			remoteSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PushRemoteSecretName, Namespace: imageRepository.Namespace}
			remoteSecret := waitRemoteSecretExist(remoteSecretKey)

			uploadSecret := waitSecretExist(uploadSecretKey)
			Expect(uploadSecret.Labels[remotesecretv1beta1.UploadSecretLabel]).To(Equal("remotesecret"))
			Expect(uploadSecret.Annotations[remotesecretv1beta1.RemoteSecretNameAnnotation]).To(Equal(remoteSecret.Name))
			Expect(uploadSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			uploadSecretDockerconfigJson := string(uploadSecret.Data[corev1.DockerConfigJsonKey])
			var authDataJson interface{}
			Expect(json.Unmarshal([]byte(uploadSecretDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(uploadSecretDockerconfigJson).To(ContainSubstring(expectedImage))
			uploadSecretAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(uploadSecretDockerconfigJson)[1])
			Expect(err).To(Succeed())
			pushRobotAccountName := imageRepository.Status.Credentials.PushRobotAccountName
			Expect(string(uploadSecretAuthString)).To(Equal(fmt.Sprintf("%s:%s", pushRobotAccountName, newToken)))
		})

		It("should update image visibility", func() {
			isChangeRepositoryVisibilityInvoked := false
			ChangeRepositoryVisibilityFunc = func(organization, imageRepository, visibility string) error {
				defer GinkgoRecover()
				isChangeRepositoryVisibilityInvoked = true
				Expect(organization).To(Equal(testQuayOrg))
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

		It("should cleanup repository", func() {
			isDeleteRobotAccountInvoked := false
			DeleteRobotAccountFunc = func(organization, robotAccountName string) (bool, error) {
				defer GinkgoRecover()
				isDeleteRobotAccountInvoked = true
				Expect(organization).To(Equal(testQuayOrg))
				Expect(strings.HasPrefix(robotAccountName, expectedRobotAccountPrefix)).To(BeTrue())
				return true, nil
			}
			isDeleteRepositoryInvoked := false
			DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				defer GinkgoRecover()
				isDeleteRepositoryInvoked = true
				Expect(organization).To(Equal(testQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				return true, nil
			}

			deleteImageRepository(resourceKey)

			Eventually(func() bool { return isDeleteRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRepositoryInvoked }, timeout, interval).Should(BeTrue())
		})
	})

	Context("Image repository for component provision", func() {

		BeforeEach(func() {
			ResetTestQuayClientToFails()
			deleteUploadSecrets(defaultNamespace)
		})

		It("should prepare environment", func() {
			pushToken = "push-token1234"
			pullToken = "pull-token1234"
			expectedImageName = fmt.Sprintf("%s/%s/%s", defaultNamespace, defaultComponentApplication, defaultComponentName)
			expectedImage = fmt.Sprintf("quay.io/%s/%s", testQuayOrg, expectedImageName)
			expectedRobotAccountPrefix = strings.ReplaceAll(strings.ReplaceAll(expectedImageName, "-", "_"), "/", "_")

			createComponent(componentConfig{})
		})

		It("should provision image repository for component", func() {
			isCreateRepositoryInvoked := false
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(testQuayOrg))
				Expect(repository.Visibility).To(Equal("public"))
				Expect(repository.Description).ToNot(BeEmpty())
				return &quay.Repository{Name: expectedImageName}, nil
			}
			isCreatePushRobotAccountInvoked := false
			isCreatePullRobotAccountInvoked := false
			CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(testQuayOrg))
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
			AddPermissionsForRepositoryToRobotAccountFunc = func(organization, imageRepository, robotAccountName string, isWrite bool) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(testQuayOrg))
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

			createImageRepository(imageRepositoryConfig{
				Labels: map[string]string{
					ApplicationNameLabelName: defaultComponentApplication,
					ComponentNameLabelName:   defaultComponentName,
				},
			})

			pushUploadSecretKey := types.NamespacedName{Name: "upload-secret-" + resourceKey.Name + "-image-push", Namespace: resourceKey.Namespace}
			pullUploadSecretKey := types.NamespacedName{Name: "upload-secret-" + resourceKey.Name + "-image-pull", Namespace: resourceKey.Namespace}
			defer deleteSecret(pushUploadSecretKey)
			defer deleteSecret(pullUploadSecretKey)

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePushRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePullRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPushPermissionsToRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPullPermissionsToRobotAccountInvoked }, timeout, interval).Should(BeTrue())

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			imageRepository := getImageRepository(resourceKey)
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
			Expect(imageRepository.Status.Credentials.PushRemoteSecretName).To(Equal(imageRepository.Name + "-image-push"))
			Expect(imageRepository.Status.Credentials.PushSecretName).To(Equal(imageRepository.Name + "-image-push"))
			Expect(imageRepository.Status.Credentials.PullRobotAccountName).To(HavePrefix(expectedRobotAccountPrefix))
			Expect(imageRepository.Status.Credentials.PullRobotAccountName).To(HaveSuffix("_pull"))
			Expect(imageRepository.Status.Credentials.PullRemoteSecretName).To(Equal(imageRepository.Name + "-image-pull"))
			Expect(imageRepository.Status.Credentials.PullSecretName).To(Equal(imageRepository.Name + "-image-pull"))
			Expect(imageRepository.Status.Credentials.GenerationTimestamp).ToNot(BeNil())

			pushRemoteSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PushRemoteSecretName, Namespace: imageRepository.Namespace}
			pushRemoteSecret := waitRemoteSecretExist(pushRemoteSecretKey)
			Expect(pushRemoteSecret.Labels[InternalRemoteSecretLabelName]).To(Equal("true"))
			Expect(pushRemoteSecret.OwnerReferences).To(HaveLen(1))
			Expect(pushRemoteSecret.OwnerReferences[0].Kind).To(Equal("ImageRepository"))
			Expect(pushRemoteSecret.OwnerReferences[0].Name).To(Equal(imageRepository.GetName()))
			Expect(pushRemoteSecret.Spec.Secret.Name).To(Equal(pushRemoteSecret.Name))
			Expect(pushRemoteSecret.Spec.Secret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			Expect(pushRemoteSecret.Spec.Secret.LinkedTo).To(HaveLen(1))
			Expect(pushRemoteSecret.Spec.Secret.LinkedTo[0].ServiceAccount.Reference.Name).To(Equal(buildPipelineServiceAccountName))
			Expect(pushRemoteSecret.Spec.Targets).To(HaveLen(1))
			Expect(pushRemoteSecret.Spec.Targets[0].Namespace).To(Equal(imageRepository.Namespace))

			pullRemoteSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PullRemoteSecretName, Namespace: imageRepository.Namespace}
			pullRemoteSecret := waitRemoteSecretExist(pullRemoteSecretKey)
			Expect(pullRemoteSecret.Labels[ApplicationNameLabelName]).To(Equal(defaultComponentApplication))
			Expect(pullRemoteSecret.Labels[ComponentNameLabelName]).To(Equal(defaultComponentName))
			Expect(pullRemoteSecret.Labels[InternalRemoteSecretLabelName]).To(Equal("true"))
			Expect(pullRemoteSecret.OwnerReferences).To(HaveLen(1))
			Expect(pullRemoteSecret.OwnerReferences[0].Name).To(Equal(imageRepository.Name))
			Expect(pullRemoteSecret.OwnerReferences[0].Kind).To(Equal("ImageRepository"))
			Expect(pullRemoteSecret.Spec.Secret.Name).To(Equal(pullRemoteSecretKey.Name))
			Expect(pullRemoteSecret.Spec.Secret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			Expect(pullRemoteSecret.Spec.Secret.LinkedTo).To(HaveLen(1))
			Expect(pullRemoteSecret.Spec.Secret.LinkedTo[0].ServiceAccount.Reference.Name).To(Equal(defaultServiceAccountName))
			Expect(pullRemoteSecret.Spec.Targets).To(HaveLen(0))

			var authDataJson interface{}

			pushUploadSecret := waitSecretExist(pushUploadSecretKey)
			Expect(pushUploadSecret.Labels[remotesecretv1beta1.UploadSecretLabel]).To(Equal("remotesecret"))
			Expect(pushUploadSecret.Annotations[remotesecretv1beta1.RemoteSecretNameAnnotation]).To(Equal(pushRemoteSecret.Name))
			Expect(pushUploadSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			pushUploadSecretDockerconfigJson := string(pushUploadSecret.Data[corev1.DockerConfigJsonKey])
			Expect(json.Unmarshal([]byte(pushUploadSecretDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(pushUploadSecretDockerconfigJson).To(ContainSubstring(expectedImage))
			pushUploadSecretAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(pushUploadSecretDockerconfigJson)[1])
			Expect(err).To(Succeed())
			pushRobotAccountName := imageRepository.Status.Credentials.PushRobotAccountName
			Expect(string(pushUploadSecretAuthString)).To(Equal(fmt.Sprintf("%s:%s", pushRobotAccountName, pushToken)))

			pullUploadSecret := waitSecretExist(pullUploadSecretKey)
			Expect(pullUploadSecret.Labels[remotesecretv1beta1.UploadSecretLabel]).To(Equal("remotesecret"))
			Expect(pullUploadSecret.Annotations[remotesecretv1beta1.RemoteSecretNameAnnotation]).To(Equal(pullRemoteSecret.Name))
			Expect(pullUploadSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			pullUploadSecretDockerconfigJson := string(pullUploadSecret.Data[corev1.DockerConfigJsonKey])
			Expect(json.Unmarshal([]byte(pullUploadSecretDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(pullUploadSecretDockerconfigJson).To(ContainSubstring(expectedImage))
			pullUploadSecretAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(pullUploadSecretDockerconfigJson)[1])
			Expect(err).To(Succeed())
			pullRobotAccountName := imageRepository.Status.Credentials.PullRobotAccountName
			Expect(string(pullUploadSecretAuthString)).To(Equal(fmt.Sprintf("%s:%s", pullRobotAccountName, pullToken)))
		})

		It("should regenerate tokens and update remote secret", func() {
			newPushToken := "push-token5678"
			newPullToken := "pull-token5678"

			pushUploadSecretKey := types.NamespacedName{Name: "upload-secret-" + resourceKey.Name + "-image-push", Namespace: resourceKey.Namespace}
			pullUploadSecretKey := types.NamespacedName{Name: "upload-secret-" + resourceKey.Name + "-image-pull", Namespace: resourceKey.Namespace}
			defer deleteSecret(pushUploadSecretKey)
			defer deleteSecret(pullUploadSecretKey)

			// Wait just for case it takes less than a second to regenerate credentials
			time.Sleep(time.Second)

			isRegenerateRobotAccountTokenForPushInvoked := false
			isRegenerateRobotAccountTokenForPullInvoked := false
			RegenerateRobotAccountTokenFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(testQuayOrg))
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

			Eventually(func() bool { return isRegenerateRobotAccountTokenForPushInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isRegenerateRobotAccountTokenForPullInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Spec.Credentials.RegenerateToken == nil &&
					imageRepository.Status.Credentials.GenerationTimestamp != nil &&
					*imageRepository.Status.Credentials.GenerationTimestamp != oldTokenGenerationTimestamp
			}, timeout, interval).Should(BeTrue())

			pushRemoteSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PushRemoteSecretName, Namespace: imageRepository.Namespace}
			pushRemoteSecret := waitRemoteSecretExist(pushRemoteSecretKey)

			pullRemoteSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PullRemoteSecretName, Namespace: imageRepository.Namespace}
			pullRemoteSecret := waitRemoteSecretExist(pullRemoteSecretKey)

			var authDataJson interface{}

			pushUploadSecret := waitSecretExist(pushUploadSecretKey)
			Expect(pushUploadSecret.Labels[remotesecretv1beta1.UploadSecretLabel]).To(Equal("remotesecret"))
			Expect(pushUploadSecret.Annotations[remotesecretv1beta1.RemoteSecretNameAnnotation]).To(Equal(pushRemoteSecret.Name))
			Expect(pushUploadSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			pushUploadSecretDockerconfigJson := string(pushUploadSecret.Data[corev1.DockerConfigJsonKey])
			Expect(json.Unmarshal([]byte(pushUploadSecretDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(pushUploadSecretDockerconfigJson).To(ContainSubstring(expectedImage))
			pushUploadSecretAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(pushUploadSecretDockerconfigJson)[1])
			Expect(err).To(Succeed())
			pushRobotAccountName := imageRepository.Status.Credentials.PushRobotAccountName
			Expect(string(pushUploadSecretAuthString)).To(Equal(fmt.Sprintf("%s:%s", pushRobotAccountName, newPushToken)))

			pullUploadSecret := waitSecretExist(pullUploadSecretKey)
			Expect(pullUploadSecret.Labels[remotesecretv1beta1.UploadSecretLabel]).To(Equal("remotesecret"))
			Expect(pullUploadSecret.Annotations[remotesecretv1beta1.RemoteSecretNameAnnotation]).To(Equal(pullRemoteSecret.Name))
			Expect(pullUploadSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			pullUploadSecretDockerconfigJson := string(pullUploadSecret.Data[corev1.DockerConfigJsonKey])
			Expect(json.Unmarshal([]byte(pullUploadSecretDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(pullUploadSecretDockerconfigJson).To(ContainSubstring(expectedImage))
			pullUploadSecretAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(pullUploadSecretDockerconfigJson)[1])
			Expect(err).To(Succeed())
			pullRobotAccountName := imageRepository.Status.Credentials.PullRobotAccountName
			Expect(string(pullUploadSecretAuthString)).To(Equal(fmt.Sprintf("%s:%s", pullRobotAccountName, newPullToken)))
		})

		It("should cleanup component repository", func() {
			isDeleteRobotAccountForPushInvoked := false
			isDeleteRobotAccountForPullInvoked := false
			DeleteRobotAccountFunc = func(organization, robotAccountName string) (bool, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(testQuayOrg))
				Expect(strings.HasPrefix(robotAccountName, expectedRobotAccountPrefix)).To(BeTrue())
				if strings.HasSuffix(robotAccountName, "_pull") {
					isDeleteRobotAccountForPushInvoked = true
				} else {
					isDeleteRobotAccountForPullInvoked = true
				}
				return true, nil
			}
			isDeleteRepositoryInvoked := false
			DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				defer GinkgoRecover()
				isDeleteRepositoryInvoked = true
				Expect(organization).To(Equal(testQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				return true, nil
			}

			deleteImageRepository(resourceKey)

			Eventually(func() bool { return isDeleteRobotAccountForPushInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRobotAccountForPullInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRepositoryInvoked }, timeout, interval).Should(BeTrue())

			componentKey := types.NamespacedName{Name: defaultComponentName, Namespace: defaultNamespace}
			deleteComponent(componentKey)
		})
	})

	Context("Other image repository scenarios", func() {

		BeforeEach(func() {
			ResetTestQuayClient()
			deleteImageRepository(resourceKey)
			deleteUploadSecrets(defaultNamespace)
		})

		It("should create image repository with requested name", func() {
			customImageName := "my-image"
			expectedImageName = defaultNamespace + "/" + customImageName

			isCreateRepositoryInvoked := false
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(testQuayOrg))
				Expect(repository.Visibility).To(Equal("public"))
				Expect(repository.Description).ToNot(BeEmpty())
				return &quay.Repository{Name: expectedImageName}, nil
			}

			createImageRepository(imageRepositoryConfig{ImageName: customImageName})
			defer deleteImageRepository(resourceKey)

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			imageRepository := getImageRepository(resourceKey)
			Expect(imageRepository.Spec.Image.Name).To(Equal(expectedImageName))
		})
	})

	Context("Image repository error scenarios", func() {

		BeforeEach(func() {
			ResetTestQuayClient()
			deleteImageRepository(resourceKey)
			deleteUploadSecrets(defaultNamespace)
		})

		It("should prepare environment", func() {
			pushToken = "push-token1234"
			expectedImageName = fmt.Sprintf("%s/%s", defaultNamespace, defaultImageRepositoryName)
			expectedImage = fmt.Sprintf("quay.io/%s/%s", testQuayOrg, expectedImageName)
			expectedRobotAccountPrefix = strings.ReplaceAll(strings.ReplaceAll(expectedImageName, "-", "_"), "/", "_")
		})

		It("should permanently fail if private image repository requested on creation but quota exceeded", func() {
			isCreateRepositoryInvoked := false
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(testQuayOrg))
				Expect(repository.Visibility).To(Equal("private"))
				Expect(repository.Description).ToNot(BeEmpty())
				return nil, fmt.Errorf("payment required")
			}

			createImageRepository(imageRepositoryConfig{Visibility: "private"})

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
			ResetTestQuayClient()

			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				return &quay.Repository{Name: expectedImageName}, nil
			}
			CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				return &quay.RobotAccount{Name: robotName, Token: pushToken}, nil
			}
			createImageRepository(imageRepositoryConfig{})
			defer deleteImageRepository(resourceKey)

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			ResetTestQuayClientToFails()

			isChangeRepositoryVisibilityInvoked := false
			ChangeRepositoryVisibilityFunc = func(organization, imageRepository, visibility string) error {
				defer GinkgoRecover()
				isChangeRepositoryVisibilityInvoked = true
				Expect(organization).To(Equal(testQuayOrg))
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

			ResetTestQuayClient()
			deleteImageRepository(resourceKey)
		})

		It("should fail if invalid image repository linked by annotation to unexisting component", func() {
			ResetTestQuayClientToFails()

			createImageRepository(imageRepositoryConfig{
				ImageName: fmt.Sprintf("%s/%s", defaultComponentApplication, defaultComponentName),
				Labels: map[string]string{
					ApplicationNameLabelName: defaultComponentApplication,
					ComponentNameLabelName:   defaultComponentName,
				},
			})
			defer deleteImageRepository(resourceKey)

			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Status.State == imagerepositoryv1alpha1.ImageRepositoryStateFailed &&
					imageRepository.Status.Message != ""
			}, timeout, interval).Should(BeTrue())
		})

		It("should fail if invalid image repository name given", func() {
			imageRepository := getImageRepositoryConfig(imageRepositoryConfig{
				ImageName: "wrong&name",
			})
			Expect(k8sClient.Create(ctx, imageRepository)).ToNot(Succeed())
		})
	})

})
