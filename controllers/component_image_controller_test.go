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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	remotesecretv1beta1 "github.com/redhat-appstudio/remote-secret/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/redhat-appstudio/image-controller/pkg/quay"
)

var _ = Describe("Component image controller", func() {

	var (
		authRegexp = regexp.MustCompile(`.*{"auth":"([A-Za-z0-9+/=]*)"}.*`)

		resourceKey     = types.NamespacedName{Name: defaultComponentName, Namespace: defaultComponentNamespace}
		uploadSecretKey = types.NamespacedName{Name: "upload-secret-" + defaultComponentName + "-pull", Namespace: defaultComponentNamespace}

		pushToken                    string
		pullToken                    string
		expectedPushRobotAccountName string
		expectedPullRobotAccountName string
		expectedRemoteSecretName     string
		expectedRepoName             string
		expectedImage                string
	)

	Context("Image repository provision flow", func() {

		It("should prepare environment", func() {
			deleteNamespace(defaultComponentNamespace)
			createNamespace(defaultComponentNamespace)

			ResetTestQuayClient()

			pushToken = "push-token1234"
			pullToken = "pull-token1234"
			expectedPushRobotAccountName = fmt.Sprintf("%s%s%s", defaultComponentNamespace, defaultComponentApplication, defaultComponentName)
			expectedPullRobotAccountName = expectedPushRobotAccountName + "-pull"
			expectedRemoteSecretName = resourceKey.Name + "-pull"
			expectedRepoName = fmt.Sprintf("%s/%s/%s", defaultComponentNamespace, defaultComponentApplication, defaultComponentName)
			expectedImage = fmt.Sprintf("quay.io/%s/%s", testQuayOrg, expectedRepoName)
		})

		It("should do image repository provision", func() {
			isCreateRepositoryInvoked := false
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedRepoName))
				Expect(repository.Namespace).To(Equal(testQuayOrg))
				Expect(repository.Visibility).To(Equal("private"))
				Expect(repository.Description).ToNot(BeEmpty())
				return &quay.Repository{Name: expectedRepoName}, nil
			}
			isCreatePushRobotAccountInvoked := false
			isCreatePullRobotAccountInvoked := false
			CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(testQuayOrg))
				switch robotName {
				case expectedPushRobotAccountName:
					isCreatePushRobotAccountInvoked = true
					return &quay.RobotAccount{
						Name:  expectedPushRobotAccountName,
						Token: pushToken,
					}, nil
				case expectedPullRobotAccountName:
					isCreatePullRobotAccountInvoked = true
					return &quay.RobotAccount{
						Name:  expectedPullRobotAccountName,
						Token: pullToken,
					}, nil
				}
				Fail("Unexpected robot account name: " + robotName)
				return nil, nil
			}
			isAddPushPermissionsToRobotAccountInvoked := false
			isAddPullPermissionsToRobotAccountInvoked := false
			AddPermissionsForRepositoryToRobotAccountFunc = func(organization, imageRepository, robotAccountName string, isWrite bool) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(testQuayOrg))
				Expect(imageRepository).To(Equal(expectedRepoName))
				if isWrite {
					isAddPushPermissionsToRobotAccountInvoked = true
					Expect(robotAccountName).To(Equal(expectedPushRobotAccountName))
				} else {
					isAddPullPermissionsToRobotAccountInvoked = true
					Expect(robotAccountName).To(Equal(expectedPullRobotAccountName))
				}
				return nil
			}

			createComponent(componentConfig{
				ComponentKey: resourceKey,
				Annotations: map[string]string{
					GenerateImageAnnotationName: "{\"visibility\": \"private\"}",
				},
			})

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePushRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePullRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPushPermissionsToRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPullPermissionsToRobotAccountInvoked }, timeout, interval).Should(BeTrue())

			waitImageRepositoryFinalizerOnComponent(resourceKey)

			waitComponentAnnotationGone(resourceKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceKey, ImageAnnotationName)

			repoImageInfo := &ImageRepositoryStatus{}
			component := getComponent(resourceKey)
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(BeEmpty())
			Expect(repoImageInfo.Image).To(Equal(expectedImage))
			Expect(repoImageInfo.Visibility).To(Equal("private"))
			Expect(repoImageInfo.Secret).To(Equal(resourceKey.Name))

			secret := &corev1.Secret{}
			var authDataJson interface{}

			pushSecretKey := resourceKey
			waitSecretExist(pushSecretKey)
			Expect(k8sClient.Get(ctx, pushSecretKey, secret)).To(Succeed())
			pushDockerconfigJson := string(secret.Data[corev1.DockerConfigJsonKey])
			Expect(json.Unmarshal([]byte(pushDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(pushDockerconfigJson).To(ContainSubstring(expectedImage))
			pushAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(pushDockerconfigJson)[1])
			Expect(err).To(Succeed())
			Expect(string(pushAuthString)).To(Equal(fmt.Sprintf("%s:%s", expectedPushRobotAccountName, pushToken)))
		})

		It("should propagate pull secret to environments", func() {
			component := getComponent(resourceKey)

			remoteSecretKey := types.NamespacedName{Name: expectedRemoteSecretName, Namespace: defaultComponentNamespace}
			remoteSecret := waitRemoteSecretExist(remoteSecretKey)
			Expect(remoteSecret.Labels[ApplicationNameLabelName]).To(Equal(component.Spec.Application))
			Expect(remoteSecret.Labels[ComponentNameLabelName]).To(Equal(component.Spec.ComponentName))
			Expect(remoteSecret.OwnerReferences).To(HaveLen(1))
			Expect(remoteSecret.OwnerReferences[0].Name).To(Equal(component.Name))
			Expect(remoteSecret.OwnerReferences[0].Kind).To(Equal("Component"))

			Expect(remoteSecret.Spec.Secret.Name).To(Equal(remoteSecretKey.Name))
			Expect(remoteSecret.Spec.Secret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			Expect(remoteSecret.Spec.Secret.LinkedTo).To(HaveLen(1))
			Expect(remoteSecret.Spec.Secret.LinkedTo[0].ServiceAccount.Reference.Name).To(Equal(defaultServiceAccountName))

			waitSecretExist(uploadSecretKey)
			uploadSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, uploadSecretKey, uploadSecret)).To(Succeed())

			Expect(uploadSecret.Labels[remotesecretv1beta1.UploadSecretLabel]).To(Equal("remotesecret"))
			Expect(uploadSecret.Annotations[remotesecretv1beta1.RemoteSecretNameAnnotation]).To(Equal(remoteSecretKey.Name))
			Expect(uploadSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))

			uploadSecretDockerconfigJson := string(uploadSecret.Data[corev1.DockerConfigJsonKey])
			var authDataJson interface{}
			Expect(json.Unmarshal([]byte(uploadSecretDockerconfigJson), &authDataJson)).To(Succeed())
			Expect(uploadSecretDockerconfigJson).To(ContainSubstring(expectedImage))
			uploadSecretAuthString, err := base64.StdEncoding.DecodeString(authRegexp.FindStringSubmatch(uploadSecretDockerconfigJson)[1])
			Expect(err).To(Succeed())
			Expect(string(uploadSecretAuthString)).To(Equal(fmt.Sprintf("%s:%s", expectedPullRobotAccountName, pullToken)))

			deleteSecret(uploadSecretKey)
		})

		It("should be able to switch image visibility", func() {
			isChangeRepositoryVisibilityInvoked := false
			ChangeRepositoryVisibilityFunc = func(organization, imageRepository, visibility string) error {
				isChangeRepositoryVisibilityInvoked = true
				Expect(organization).To(Equal(testQuayOrg))
				Expect(imageRepository).To(Equal(expectedRepoName))
				Expect(visibility).To(Equal("public"))
				return nil
			}
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				Fail("Should not invoke repository creation on clean up")
				return nil, nil
			}

			setComponentAnnotationValue(resourceKey, GenerateImageAnnotationName, `{"visibility": "public"}`)

			Eventually(func() bool { return isChangeRepositoryVisibilityInvoked }, timeout, interval).Should(BeTrue())

			waitComponentAnnotationGone(resourceKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceKey, ImageAnnotationName)

			repoImageInfo := &ImageRepositoryStatus{}
			component := getComponent(resourceKey)
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(BeEmpty())
			Expect(repoImageInfo.Image).To(Equal(expectedImage))
			Expect(repoImageInfo.Visibility).To(Equal("public"))
			Expect(repoImageInfo.Secret).To(Equal(resourceKey.Name))
		})

		It("should do nothing if the same as current visibility requested", func() {
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				Fail("Image repository creation should not be invoked")
				return nil, nil
			}
			ChangeRepositoryVisibilityFunc = func(organization, imageRepository, visibility string) error {
				defer GinkgoRecover()
				Fail("Image repository visibility changing should not be invoked")
				return nil
			}

			setComponentAnnotationValue(resourceKey, GenerateImageAnnotationName, `{"visibility": "public"}`)

			waitComponentAnnotationGone(resourceKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceKey, ImageAnnotationName)

			repoImageInfo := &ImageRepositoryStatus{}
			component := getComponent(resourceKey)
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(BeEmpty())
			Expect(repoImageInfo.Image).To(Equal(expectedImage))
			Expect(repoImageInfo.Visibility).To(Equal("public"))
			Expect(repoImageInfo.Secret).To(Equal(resourceKey.Name))
		})

		It("should delete robot account and image repository on component deletion", func() {
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				Fail("Should not invoke repository creation on clean up")
				return nil, nil
			}
			CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Fail("Should not invoke robot account creation on clean up")
				return nil, nil
			}
			AddPermissionsForRepositoryToRobotAccountFunc = func(organization, imageRepository, robotAccountName string, isWrite bool) error {
				defer GinkgoRecover()
				Fail("Should not invoke permission adding on clean up")
				return nil
			}

			isDeletePushRobotAccountInvoked := false
			isDeletePullRobotAccountInvoked := false
			DeleteRobotAccountFunc = func(organization, robotAccountName string) (bool, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(testQuayOrg))
				switch robotAccountName {
				case expectedPushRobotAccountName:
					isDeletePushRobotAccountInvoked = true
					return true, nil
				case expectedPullRobotAccountName:
					isDeletePullRobotAccountInvoked = true
					return true, nil
				}
				Fail("Unexpected robot account name: " + robotAccountName)
				return false, nil
			}
			isDeleteRepositoryInvoked := false
			DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				isDeleteRepositoryInvoked = true
				Expect(organization).To(Equal(testQuayOrg))
				Expect(imageRepository).To(Equal(expectedRepoName))
				return true, nil
			}

			deleteComponent(resourceKey)

			Eventually(func() bool { return isDeletePushRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeletePullRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRepositoryInvoked }, timeout, interval).Should(BeTrue())
		})
	})

	Context("Image repository provision error cases", func() {

		It("should prepare environment", func() {
			createNamespace(defaultComponentNamespace)

			ResetTestQuayClient()

			deleteComponent(resourceKey)

			expectedImage = fmt.Sprintf("quay.io/%s/%s", testQuayOrg, expectedRepoName)
		})

		It("should do nothing if generate annotation is not set", func() {
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				Fail("Image repository creation should not be invoked")
				return nil, nil
			}

			createComponent(componentConfig{ComponentKey: resourceKey})

			time.Sleep(ensureTimeout)
			waitComponentAnnotationGone(resourceKey, GenerateImageAnnotationName)
			waitComponentAnnotationGone(resourceKey, ImageAnnotationName)
		})

		It("should do nothing and set error if generate annotation is invalid JSON", func() {
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				Fail("Image repository creation should not be invoked")
				return nil, nil
			}

			setComponentAnnotationValue(resourceKey, GenerateImageAnnotationName, `{"visibility": "public"`)

			waitComponentAnnotationGone(resourceKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceKey, ImageAnnotationName)

			repoImageInfo := &ImageRepositoryStatus{}
			component := getComponent(resourceKey)
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(ContainSubstring("invalid JSON"))
			Expect(repoImageInfo.Image).To(BeEmpty())
			Expect(repoImageInfo.Visibility).To(BeEmpty())
			Expect(repoImageInfo.Secret).To(BeEmpty())

			Expect(controllerutil.ContainsFinalizer(component, ImageRepositoryFinalizer)).To(BeFalse())
		})

		It("should do nothing and set error if generate annotation has invalid visibility value", func() {
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				Fail("Image repository creation should not be invoked")
				return nil, nil
			}

			setComponentAnnotationValue(resourceKey, GenerateImageAnnotationName, `{"visibility": "none"}`)

			waitComponentAnnotationGone(resourceKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceKey, ImageAnnotationName)

			repoImageInfo := &ImageRepositoryStatus{}
			component := getComponent(resourceKey)
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(ContainSubstring("invalid value: none in visibility field"))
			Expect(repoImageInfo.Image).To(BeEmpty())
			Expect(repoImageInfo.Visibility).To(BeEmpty())
			Expect(repoImageInfo.Secret).To(BeEmpty())

			Expect(controllerutil.ContainsFinalizer(component, ImageRepositoryFinalizer)).To(BeFalse())
		})

		It("should set error if quay organization plan doesn't allow private repositories", func() {
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				Expect(repository.Visibility).To(Equal("private"))
				return nil, fmt.Errorf("payment required")
			}
			ChangeRepositoryVisibilityFunc = func(organization, imageRepository, visibility string) error {
				defer GinkgoRecover()
				Fail("Image repository visibility change should not be invoked")
				return nil
			}

			setComponentAnnotationValue(resourceKey, GenerateImageAnnotationName, `{"visibility": "private"}`)

			waitComponentAnnotationGone(resourceKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceKey, ImageAnnotationName)

			repoImageInfo := &ImageRepositoryStatus{}
			component := getComponent(resourceKey)
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(ContainSubstring("organization plan doesn't allow private image repositories"))
			Expect(repoImageInfo.Image).To(BeEmpty())
			Expect(repoImageInfo.Visibility).To(BeEmpty())
			Expect(repoImageInfo.Secret).To(BeEmpty())

			Expect(controllerutil.ContainsFinalizer(component, ImageRepositoryFinalizer)).To(BeFalse())
		})

		It("should add message and stop if it's not possible to switch image repository visibility", func() {
			isChangeRepositoryVisibilityInvoked := false
			ChangeRepositoryVisibilityFunc = func(organization, imageRepository, visibility string) error {
				if isChangeRepositoryVisibilityInvoked {
					defer GinkgoRecover()
					Fail("Image repository visibility change should not be invoked second time")
				}
				isChangeRepositoryVisibilityInvoked = true
				return fmt.Errorf("payment required")
			}
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				Fail("Should not invoke repository creation")
				return nil, nil
			}

			repositoryInfo := ImageRepositoryStatus{
				Image:      expectedImage,
				Visibility: "public",
				Secret:     resourceKey.Name,
			}
			repositoryInfoJsonBytes, _ := json.Marshal(repositoryInfo)
			setComponentAnnotationValue(resourceKey, ImageAnnotationName, string(repositoryInfoJsonBytes))
			setComponentAnnotationValue(resourceKey, GenerateImageAnnotationName, `{"visibility": "private"}`)

			Eventually(func() bool { return isChangeRepositoryVisibilityInvoked }, timeout, interval).Should(BeTrue())

			waitComponentAnnotationGone(resourceKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceKey, ImageAnnotationName)

			repoImageInfo := &ImageRepositoryStatus{}
			component := getComponent(resourceKey)
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(ContainSubstring("organization plan doesn't allow private image repositories"))
			Expect(repoImageInfo.Image).To(Equal(expectedImage))
			Expect(repoImageInfo.Visibility).To(Equal("public"))
			Expect(repoImageInfo.Secret).To(Equal(resourceKey.Name))
		})

		It("should stop and report error if image repository creation fails", func() {
			isCreateRepositoryInvoked := false
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				isCreateRepositoryInvoked = true
				return nil, fmt.Errorf("fail to marshal data")
			}

			repositoryInfoJsonBytes, _ := json.Marshal(ImageRepositoryStatus{})
			setComponentAnnotationValue(resourceKey, ImageAnnotationName, string(repositoryInfoJsonBytes))
			setComponentAnnotationValue(resourceKey, GenerateImageAnnotationName, `{"visibility": "public"}`)

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())

			expectedValue, _ := json.Marshal(&ImageRepositoryStatus{Message: "failed to generate image repository"})
			waitComponentAnnotationWithValue(resourceKey, ImageAnnotationName, string(expectedValue))
		})

		It("should do nothing and set error for changing visibility if image is invalid in image annotation", func() {
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				Fail("Image repository creation should not be invoked")
				return nil, nil
			}

			// An invalid image is set, which does not include registry.
			setComponentAnnotationValue(resourceKey, ImageAnnotationName, `{"image": "ns/img:tag", "secret": "1234"}`)
			setComponentAnnotationValue(resourceKey, GenerateImageAnnotationName, `{"visibility": "private"}`)

			waitComponentAnnotationGone(resourceKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceKey, ImageAnnotationName)

			repoImageInfo := &ImageRepositoryStatus{}
			component := getComponent(resourceKey)
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(Equal("Invalid image url"))
		})

		It("nothing is changed and keep doing reconcile if fail to change repository visibility", func() {
			// Work with a specific component in order to avoid potential conflict error happened in any subsequent test.
			testComponentKey := types.NamespacedName{
				Name:      defaultComponentName + "-stop-if-fail-to-change-repo-visibility",
				Namespace: defaultComponentNamespace,
			}
			createComponent(componentConfig{ComponentKey: testComponentKey})

			isChangeRepositoryVisibilityInvoked := false
			ChangeRepositoryVisibilityFunc = func(string, string, string) error {
				isChangeRepositoryVisibilityInvoked = true
				return fmt.Errorf("failed to change repository visibility")
			}

			repoInfo := map[string]string{
				"name":       "img",
				"image":      "registry/ns/img:0.1",
				"secret":     "1234",
				"visibility": "public",
			}
			imageAnnotationValue, _ := json.Marshal(repoInfo)
			setComponentAnnotationValue(testComponentKey, ImageAnnotationName, string(imageAnnotationValue))

			// Start to change visibility to private
			generateAnnotationValue := `{"visibility": "private"}`
			setComponentAnnotationValue(testComponentKey, GenerateImageAnnotationName, generateAnnotationValue)

			Eventually(func() bool { return isChangeRepositoryVisibilityInvoked }, timeout, interval).Should(BeTrue())

			// Failed to change the visibility, reconciler should return immediately and annotations are not changed
			ensureComponentAnnotationUnchangedWithValue(testComponentKey, ImageAnnotationName, string(imageAnnotationValue))
			ensureComponentAnnotationUnchangedWithValue(testComponentKey, GenerateImageAnnotationName, generateAnnotationValue)

			deleteComponent(testComponentKey)
		})

		It("should do nothing and set error if image annotation is invalid JSON", func() {
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				Fail("Image repository creation should not be invoked")
				return nil, nil
			}

			setComponentAnnotationValue(resourceKey, ImageAnnotationName, `{"image": "registry/ns/img:tag}`)
			setComponentAnnotationValue(resourceKey, GenerateImageAnnotationName, `{"visibility": "private"}`)

			waitComponentAnnotationGone(resourceKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceKey, ImageAnnotationName)

			repoImageInfo := &ImageRepositoryStatus{}
			component := getComponent(resourceKey)
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(Equal("Invalid image status annotation"))
		})

		It("should not block component deletion if clean up fails", func() {
			waitImageRepositoryFinalizerOnComponent(resourceKey)

			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				Fail("Should not invoke repository creation")
				return nil, nil
			}
			DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				return false, fmt.Errorf("failed to delete repository")
			}
			DeleteRobotAccountFunc = func(organization, robotName string) (bool, error) {
				return false, fmt.Errorf("failed to delete robot account")
			}

			deleteComponent(resourceKey)
		})
	})

	Context("Image repository provision other cases", func() {

		_ = BeforeEach(func() {
			createNamespace(defaultComponentNamespace)

			ResetTestQuayClient()

			deleteComponent(resourceKey)
			deleteSecret(uploadSecretKey)

			pushToken = "push-token1234"
			pullToken = "pull-token1234"
			expectedPushRobotAccountName = fmt.Sprintf("%s%s%s", defaultComponentNamespace, defaultComponentApplication, defaultComponentName)
			expectedPullRobotAccountName = expectedPushRobotAccountName + "-pull"
			expectedRepoName = fmt.Sprintf("%s/%s/%s", defaultComponentNamespace, defaultComponentApplication, defaultComponentName)
			expectedImage = fmt.Sprintf("quay.io/%s/%s", testQuayOrg, expectedRepoName)
		})

		_ = AfterEach(func() {
			deleteComponent(resourceKey)

			deleteSecret(resourceKey)
		})

		It("should accept deprecated true value for repository options", func() {
			isCreateRepositoryInvoked := false
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				isCreateRepositoryInvoked = true
				return &quay.Repository{Name: "repo-name"}, nil
			}

			createComponent(componentConfig{
				ComponentKey: resourceKey,
				Annotations: map[string]string{
					GenerateImageAnnotationName: "true",
				},
			})

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())

			waitImageRepositoryFinalizerOnComponent(resourceKey)

			waitComponentAnnotationGone(resourceKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceKey, ImageAnnotationName)

			repoImageInfo := &ImageRepositoryStatus{}
			component := getComponent(resourceKey)
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(BeEmpty())
			Expect(repoImageInfo.Image).ToNot(BeEmpty())
			Expect(repoImageInfo.Visibility).To(Equal("public"))
			Expect(repoImageInfo.Secret).ToNot(BeEmpty())
		})

		It("should create pull robot account for existing image repositories with only push robot account and propagate it via remote secret", func() {
			deleteSecret(types.NamespacedName{Name: expectedRemoteSecretName, Namespace: resourceKey.Namespace})

			remoteSecretKey := types.NamespacedName{Name: expectedRemoteSecretName, Namespace: defaultComponentNamespace}
			Expect(k8sErrors.IsNotFound(k8sClient.Get(ctx, remoteSecretKey, &remotesecretv1beta1.RemoteSecret{})))

			isCreateRepositoryInvoked := false
			CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedRepoName))
				Expect(repository.Namespace).To(Equal(testQuayOrg))
				Expect(repository.Visibility).To(Equal("public"))
				Expect(repository.Description).ToNot(BeEmpty())
				return &quay.Repository{Name: expectedRepoName}, nil
			}
			isCreatePushRobotAccountInvoked := false
			isCreatePullRobotAccountInvoked := false
			CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(testQuayOrg))
				switch robotName {
				case expectedPushRobotAccountName:
					isCreatePushRobotAccountInvoked = true
					return &quay.RobotAccount{
						Name:  expectedPushRobotAccountName,
						Token: pushToken,
					}, nil
				case expectedPullRobotAccountName:
					isCreatePullRobotAccountInvoked = true
					return &quay.RobotAccount{
						Name:  expectedPullRobotAccountName,
						Token: pullToken,
					}, nil
				}
				Fail("Unexpected robot account name: " + robotName)
				return nil, nil
			}
			isAddPushPermissionsToRobotAccountInvoked := false
			isAddPullPermissionsToRobotAccountInvoked := false
			AddPermissionsForRepositoryToRobotAccountFunc = func(organization, imageRepository, robotAccountName string, isWrite bool) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(testQuayOrg))
				Expect(imageRepository).To(Equal(expectedRepoName))
				if isWrite {
					isAddPushPermissionsToRobotAccountInvoked = true
					Expect(robotAccountName).To(Equal(expectedPushRobotAccountName))
				} else {
					isAddPullPermissionsToRobotAccountInvoked = true
					Expect(robotAccountName).To(Equal(expectedPullRobotAccountName))
				}
				return nil
			}

			createComponent(componentConfig{ComponentKey: resourceKey})
			setComponentAnnotationValue(resourceKey, GenerateImageAnnotationName, `{"visibility": "public"}`)

			waitComponentAnnotationGone(resourceKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceKey, ImageAnnotationName)
			waitImageRepositoryFinalizerOnComponent(resourceKey)
			deleteSecret(uploadSecretKey)

			setComponentAnnotationValue(resourceKey, GenerateImageAnnotationName, `{"visibility": "public"}`)

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePushRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePullRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPushPermissionsToRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPullPermissionsToRobotAccountInvoked }, timeout, interval).Should(BeTrue())

			waitComponentAnnotationGone(resourceKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceKey, ImageAnnotationName)

			component := getComponent(resourceKey)
			repoImageInfo := &ImageRepositoryStatus{}
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(BeEmpty())
			Expect(repoImageInfo.Image).To(Equal(expectedImage))
			Expect(repoImageInfo.Visibility).To(Equal("public"))
			Expect(repoImageInfo.Secret).To(Equal(resourceKey.Name))

			remoteSecret := waitRemoteSecretExist(remoteSecretKey)
			Expect(remoteSecret.Labels[ApplicationNameLabelName]).To(Equal(component.Spec.Application))
			Expect(remoteSecret.Labels[ComponentNameLabelName]).To(Equal(component.Spec.ComponentName))
			Expect(remoteSecret.OwnerReferences).To(HaveLen(1))
			Expect(remoteSecret.OwnerReferences[0].Name).To(Equal(component.Name))
			Expect(remoteSecret.OwnerReferences[0].Kind).To(Equal("Component"))

			Expect(remoteSecret.Spec.Secret.Name).To(Equal(remoteSecretKey.Name))
			Expect(remoteSecret.Spec.Secret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			Expect(remoteSecret.Spec.Secret.LinkedTo).To(HaveLen(1))
			Expect(remoteSecret.Spec.Secret.LinkedTo[0].ServiceAccount.Reference.Name).To(Equal(defaultServiceAccountName))
		})
	})

})
