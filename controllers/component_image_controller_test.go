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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/redhat-appstudio/image-controller/pkg/quay"
)

var _ = Describe("Component image controller", func() {

	var (
		resourceKey = types.NamespacedName{Name: defaultComponentName, Namespace: defaultComponentNamespace}

		token                    string
		expectedRobotAccountName string
		expectedRepoName         string
		expectedImage            string
	)

	Context("Image repository provision flow", func() {

		It("should prepare environment", func() {
			deleteNamespace(defaultComponentNamespace)
			createNamespace(defaultComponentNamespace)

			ResetTestQuayClient()

			token = "token1234"
			expectedRobotAccountName = fmt.Sprintf("%s%s%s", defaultComponentNamespace, defaultComponentApplication, defaultComponentName)
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
			isCreateRobotAccountInvoked := false
			CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				isCreateRobotAccountInvoked = true
				Expect(robotName).To(Equal(expectedRobotAccountName))
				Expect(organization).To(Equal(testQuayOrg))
				return &quay.RobotAccount{
					Name:  expectedRobotAccountName,
					Token: token,
				}, nil
			}
			isAddWritePermissionsToRobotAccountInvoked := false
			AddWritePermissionsToRobotAccountFunc = func(organization, imageRepository, robotAccountName string) error {
				isAddWritePermissionsToRobotAccountInvoked = true
				Expect(organization).To(Equal(testQuayOrg))
				Expect(imageRepository).To(Equal(expectedRepoName))
				Expect(robotAccountName).To(Equal(expectedRobotAccountName))
				return nil
			}

			createComponent(componentConfig{
				ComponentKey: resourceKey,
				Annotations: map[string]string{
					GenerateImageAnnotationName:         "{\"visibility\": \"private\"}",
					DeleteImageRepositoryAnnotationName: "true",
				},
			})

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreateRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddWritePermissionsToRobotAccountInvoked }, timeout, interval).Should(BeTrue())

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

			waitSecretExist(resourceKey)
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, resourceKey, secret)).To(Succeed())
			dockerconfigJson := string(secret.Data[corev1.DockerConfigJsonKey])
			var authDataJson interface{}
			Expect(json.Unmarshal([]byte(dockerconfigJson), &authDataJson)).To(Succeed())
			Expect(dockerconfigJson).To(ContainSubstring(expectedImage))
			Expect(dockerconfigJson).To(ContainSubstring(base64.StdEncoding.EncodeToString([]byte(token))))
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
			AddWritePermissionsToRobotAccountFunc = func(organization, imageRepository, robotAccountName string) error {
				defer GinkgoRecover()
				Fail("Should not invoke permission adding on clean up")
				return nil
			}

			isDeleteRobotAccountInvoked := false
			DeleteRobotAccountFunc = func(organization, robotAccountName string) (bool, error) {
				isDeleteRobotAccountInvoked = true
				Expect(organization).To(Equal(testQuayOrg))
				Expect(robotAccountName).To(Equal(expectedRobotAccountName))
				return true, nil
			}
			isDeleteRepositoryInvoked := false
			DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
				isDeleteRepositoryInvoked = true
				Expect(organization).To(Equal(testQuayOrg))
				Expect(imageRepository).To(Equal(expectedRepoName))
				return true, nil
			}

			deleteComponent(resourceKey)

			Eventually(func() bool { return isDeleteRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRepositoryInvoked }, timeout, interval).Should(BeTrue())
		})
	})

	Context("Image repository provision error cases", func() {

		It("should prepare environment", func() {
			createNamespace(defaultComponentNamespace)

			ResetTestQuayClient()

			deleteComponent(resourceKey)

			token = "token1234"
			expectedRobotAccountName = fmt.Sprintf("%s%s%s", defaultComponentNamespace, defaultComponentApplication, defaultComponentName)
			expectedRepoName = fmt.Sprintf("%s/%s/%s", defaultComponentNamespace, defaultComponentApplication, defaultComponentName)
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

		It("should not block component deletion if clean up fails", func() {
			waitImageRepositoryFinalizerOnComponent(resourceKey)

			setComponentAnnotationValue(resourceKey, DeleteImageRepositoryAnnotationName, "true")

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

			expectedRobotAccountName = fmt.Sprintf("%s%s%s", defaultComponentNamespace, defaultComponentApplication, defaultComponentName)
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
	})
})
