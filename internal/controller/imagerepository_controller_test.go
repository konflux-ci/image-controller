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
	const (
		namespaceRobotToken           = "namespace_token"
		namespaceRobotTokenRefreshed1 = "namespace_token_new1"
		namespaceRobotTokenRefreshed2 = "namespace_token_new2"
	)

	var (
		pushToken                         string
		pullToken                         string
		expectedRobotAccountPrefix        string
		expectedNamespaceRobotAccountName string
		expectedNamespaceImage            string
		expectedImageName                 string
		expectedImage                     string
	)

	BeforeEach(func() {
		createNamespace(defaultNamespace)
		createNamespace(kubeSystemNamespace)
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
			expectedImage = fmt.Sprintf("%s/%s/%s", quay.TestQuayDomain, quay.TestQuayOrg, expectedImageName)
			expectedRobotAccountPrefix = sanitizeNameForQuay(expectedImageName)
			kubeSystemNamespace := getNamespace(kubeSystemNamespace)
			expectedNamespaceRobotAccountName = fmt.Sprintf("%s_%s", resourceKey.Namespace, kubeSystemNamespace.UID)
			expectedNamespaceRobotAccountName = sanitizeNameForQuay(expectedNamespaceRobotAccountName)
			expectedNamespaceImage = fmt.Sprintf("%s/%s/%s", quay.TestQuayDomain, quay.TestQuayOrg, resourceKey.Namespace)
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
			isCreateNamespaceRobotAccountInvoked := false
			quay.CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				if robotName == expectedNamespaceRobotAccountName {
					isCreateNamespaceRobotAccountInvoked = true
					return &quay.RobotAccount{Name: robotName, Token: namespaceRobotToken}, nil
				}

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
			isAddPullPermissionsToNamespaceAccountInvoked := false
			quay.AddPermissionsForRepositoryToAccountFunc = func(organization, imageRepository, accountName string, isRobot, isWrite bool) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))

				if strings.HasPrefix(accountName, expectedRobotAccountPrefix) || accountName == expectedNamespaceRobotAccountName {
					if strings.HasPrefix(accountName, expectedRobotAccountPrefix) {
						// method is called for pull or push robot account
						if strings.HasSuffix(accountName, "_pull") {
							Expect(isWrite).To(BeFalse())
							isAddPullPermissionsToAccountInvoked = true
						} else {
							Expect(isWrite).To(BeTrue())
							isAddPushPermissionsToAccountInvoked = true
						}
					} else {
						// method is called for namespace pull robot account
						Expect(isWrite).To(BeFalse())
						isAddPullPermissionsToNamespaceAccountInvoked = true
					}

				} else {
					Fail("AddPermissionsForRepositoryToAccountFunc was invoked for unknown robot account")
				}

				return nil
			}

			isGetRobotAccountInvoked := false
			quay.GetRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				isGetRobotAccountInvoked = true
				return nil, nil
			}

			createImageRepository(imageRepositoryConfig{ResourceKey: &resourceKey})

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePullRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePushRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isGetRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreateNamespaceRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPushPermissionsToAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPullPermissionsToAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPullPermissionsToNamespaceAccountInvoked }, timeout, interval).Should(BeTrue())

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			imageRepository := getImageRepository(resourceKey)
			Expect(imageRepository.Annotations).To(HaveLen(1))
			Expect(imageRepository.Annotations[namespacePullSecretEnsuredAnnotation]).To(Equal("true"))
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
			verifySecretSpec(pushSecret, "ImageRepository", imageRepository.GetName(), pushSecret.Name)

			pullSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PullSecretName, Namespace: imageRepository.Namespace}
			pullSecret := waitSecretExist(pullSecretKey)
			defer deleteSecret(pullSecretKey)
			verifySecretSpec(pullSecret, "ImageRepository", imageRepository.GetName(), pullSecret.Name)

			namespaceSecretKey := types.NamespacedName{Name: namespacePullSecretName, Namespace: imageRepository.Namespace}
			namespaceSecret := waitSecretExist(namespaceSecretKey)
			defer deleteSecret(namespaceSecretKey)
			verifySecretSpec(namespaceSecret, "", "", namespacePullSecretName)

			pushSecretDockerconfigJson := string(pushSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pushSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PushRobotAccountName, pushToken)
			pullSecretDockerconfigJson := string(pullSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pullSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PullRobotAccountName, pullToken)
			namespaceSecretDockerconfigJson := string(namespaceSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(namespaceSecretDockerconfigJson, expectedNamespaceImage, expectedNamespaceRobotAccountName, namespaceRobotToken)
		})

		It("should regenerate pull & push tokens and create pull && push secrets when they don't exist", func() {
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
			verifySecretSpec(pushSecret, "ImageRepository", imageRepository.GetName(), pushSecret.Name)
			pushSecretDockerconfigJson := string(pushSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pushSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PushRobotAccountName, newPushToken)

			pullSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PullSecretName, Namespace: imageRepository.Namespace}
			pullSecret := waitSecretExist(pullSecretKey)
			verifySecretSpec(pullSecret, "ImageRepository", imageRepository.GetName(), pullSecret.Name)
			pullSecretDockerconfigJson := string(pullSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pullSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PullRobotAccountName, newPullToken)
		})

		It("should regenerate namespace pull token and create namespace pull secret when it doesn't exists", func() {
			newNamespaceToken := namespaceRobotTokenRefreshed1

			isRegenerateNamespaceRobotAccountTokenInvoked := false
			quay.RegenerateRobotAccountTokenFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(robotName).To(Equal(expectedNamespaceRobotAccountName))
				isRegenerateNamespaceRobotAccountTokenInvoked = true
				return &quay.RobotAccount{Name: robotName, Token: newNamespaceToken}, nil
			}

			imageRepository := getImageRepository(resourceKey)
			regenerateToken := true
			imageRepository.Spec.Credentials = &imagerepositoryv1alpha1.ImageCredentials{RegenerateNamespacePullToken: &regenerateToken}
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			Eventually(func() bool { return isRegenerateNamespaceRobotAccountTokenInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Spec.Credentials.RegenerateNamespacePullToken == nil
			}, timeout, interval).Should(BeTrue())

			namespaceSecretKey := types.NamespacedName{Name: namespacePullSecretName, Namespace: imageRepository.Namespace}
			namespaceSecret := waitSecretExist(namespaceSecretKey)
			verifySecretSpec(namespaceSecret, "", "", namespacePullSecretName)
			namespaceSecretDockerconfigJson := string(namespaceSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(namespaceSecretDockerconfigJson, expectedNamespaceImage, expectedNamespaceRobotAccountName, newNamespaceToken)
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

		It("should add message when image name was edited", func() {
			imageRepository := getImageRepository(resourceKey)
			imageRepository.Spec.Image.Name = "renamed"
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return strings.HasPrefix(imageRepository.Status.Message, imageRepositoryNameChangedMessagePrefix)
			}, timeout, interval).Should(BeTrue())
		})

		It("should remove message when image name is the same again", func() {
			imageRepository := getImageRepository(resourceKey)
			imageRepository.Spec.Image.Name = expectedImageName
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Status.Message == ""
			}, timeout, interval).Should(BeTrue())
		})

		// TODO: remove after all IRs are processed and all have new namespace pull ensured annotation
		It("old already provisioned image repository without annotation will on next reconcile create namespace pull secret and robot account", func() {
			// Delete the namespace secret to verify it gets recreated
			namespaceSecretKey := types.NamespacedName{Name: namespacePullSecretName, Namespace: resourceKey.Namespace}
			deleteSecret(namespaceSecretKey)

			isGetRobotAccountInvoked := false
			quay.GetRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				isGetRobotAccountInvoked = true
				return nil, nil
			}
			isCreateNamespaceRobotAccountInvoked := false
			quay.CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				if robotName == expectedNamespaceRobotAccountName {
					isCreateNamespaceRobotAccountInvoked = true
					return &quay.RobotAccount{Name: robotName, Token: namespaceRobotTokenRefreshed1}, nil
				}
				return nil, nil
			}
			isAddPullPermissionsToNamespaceAccountInvoked := false
			quay.AddPermissionsForRepositoryToAccountFunc = func(organization, imageRepository, accountName string, isRobot, isWrite bool) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				if accountName == expectedNamespaceRobotAccountName {
					Expect(isWrite).To(BeFalse())
					isAddPullPermissionsToNamespaceAccountInvoked = true
				}
				return nil
			}

			// remove namespace pull secret ensured annotation to force new reconcile and simulate old IR
			imageRepository := getImageRepository(resourceKey)
			delete(imageRepository.Annotations, namespacePullSecretEnsuredAnnotation)
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			// Wait for the annotation to be set to true
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Annotations[namespacePullSecretEnsuredAnnotation] == "true"
			}, timeout, interval).Should(BeTrue())

			Eventually(func() bool { return isGetRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreateNamespaceRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPullPermissionsToNamespaceAccountInvoked }, timeout, interval).Should(BeTrue())

			namespaceSecret := waitSecretExist(namespaceSecretKey)
			verifySecretSpec(namespaceSecret, "", "", namespacePullSecretName)
			namespaceSecretDockerconfigJson := string(namespaceSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(namespaceSecretDockerconfigJson, expectedNamespaceImage, expectedNamespaceRobotAccountName, namespaceRobotTokenRefreshed1)
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
			isGetRobotAccountInvoked := false
			quay.GetRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				isGetRobotAccountInvoked = true
				return &quay.RobotAccount{Name: expectedNamespaceRobotAccountName, Token: namespaceRobotToken}, nil
			}
			isRemovePermissionsToRepositoryForAccountInvoked := false
			quay.RemovePermissionsForRepositoryFromAccountFunc = func(organization, imageRepository, accountName string, isRobot bool) error {
				isRemovePermissionsToRepositoryForAccountInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				Expect(accountName).To(Equal(expectedNamespaceRobotAccountName))
				return nil
			}

			deleteImageRepository(resourceKey)

			Eventually(func() bool { return isDeleteRobotAccountForPullInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRobotAccountForPushInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRepositoryInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isGetRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isRemovePermissionsToRepositoryForAccountInvoked }, timeout, interval).Should(BeTrue())

			namespaceSecretKey := types.NamespacedName{Name: namespacePullSecretName, Namespace: resourceKey.Namespace}
			namespaceSecret := waitSecretExist(namespaceSecretKey)
			verifySecretSpec(namespaceSecret, "", "", namespacePullSecretName)
			namespaceSecretDockerconfigJson := string(namespaceSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(namespaceSecretDockerconfigJson, expectedNamespaceImage, expectedNamespaceRobotAccountName, namespaceRobotTokenRefreshed1)
		})
	})

	Context("Image repository for component provision", func() {
		var resourceKey = types.NamespacedName{Name: defaultImageRepositoryName + "-componentprovision", Namespace: defaultNamespace}
		var componentKey = types.NamespacedName{Name: defaultComponentName, Namespace: defaultNamespace}
		var applicationKey = types.NamespacedName{Name: defaultComponentApplication, Namespace: defaultNamespace}
		var componentSaName = getComponentSaName(defaultComponentName)
		var applicationSecretName = getApplicationPullSecretName(applicationKey.Name)

		BeforeEach(func() {
			quay.ResetTestQuayClientToFails()
			createApplication(applicationConfig{})
			createComponent(componentConfig{ComponentApplication: defaultComponentApplication})
		})

		AfterEach(func() {
			deleteComponent(componentKey)
			deleteApplication(applicationKey)
		})

		It("should prepare environment", func() {
			pushToken = "push-token1234"
			pullToken = "pull-token1234"
			expectedImageName = fmt.Sprintf("%s/%s", defaultNamespace, defaultComponentName)
			expectedImage = fmt.Sprintf("%s/%s/%s", quay.TestQuayDomain, quay.TestQuayOrg, expectedImageName)
			expectedRobotAccountPrefix = sanitizeNameForQuay(expectedImageName)
			kubeSystemNamespace := getNamespace(kubeSystemNamespace)
			expectedNamespaceRobotAccountName = fmt.Sprintf("%s_%s", resourceKey.Namespace, kubeSystemNamespace.UID)
			expectedNamespaceRobotAccountName = sanitizeNameForQuay(expectedNamespaceRobotAccountName)

			createServiceAccount(defaultNamespace, componentSaName)
			createServiceAccount(defaultNamespace, IntegrationServiceAccountName)

			// wait for application SA to be created
			Eventually(func() bool {
				saList := getServiceAccountList(defaultNamespace)
				// there will be 2 service accounts
				// component's SA and application's SA
				return len(saList) == 2
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())
		})

		assertProvisionRepository := func(updateComponentAnnotation, setNotification bool) {
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
			isCreateNamespaceRobotAccountInvoked := false
			quay.CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				if robotName == expectedNamespaceRobotAccountName {
					isCreateNamespaceRobotAccountInvoked = true
					return &quay.RobotAccount{Name: robotName, Token: namespaceRobotToken}, nil
				}

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
			isAddPullPermissionsToNamespaceAccountInvoked := false
			quay.AddPermissionsForRepositoryToAccountFunc = func(organization, imageRepository, accountName string, isRobot, isWrite bool) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				Expect(isRobot).To(BeTrue())
				if strings.HasPrefix(accountName, expectedRobotAccountPrefix) || accountName == expectedNamespaceRobotAccountName {
					if strings.HasPrefix(accountName, expectedRobotAccountPrefix) {
						// method is called for pull or push robot account
						if strings.HasSuffix(accountName, "_pull") {
							Expect(isWrite).To(BeFalse())
							isAddPullPermissionsToAccountInvoked = true
						} else {
							Expect(isWrite).To(BeTrue())
							isAddPushPermissionsToAccountInvoked = true
						}
					} else {
						// method is called for namespace pull robot account
						Expect(isWrite).To(BeFalse())
						isAddPullPermissionsToNamespaceAccountInvoked = true
					}

				} else {
					Fail("AddPermissionsForRepositoryToAccountFunc was invoked for unknown robot account")
				}
				return nil
			}
			isCreateNotificationInvoked := false
			isGetNotificationsInvoked := false
			if setNotification {
				quay.CreateNotificationFunc = func(organization, repository string, notification quay.Notification) (*quay.Notification, error) {
					isCreateNotificationInvoked = true
					Expect(organization).To(Equal(quay.TestQuayOrg))
					return &quay.Notification{UUID: "uuid"}, nil
				}
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
			}
			isGetRobotAccountInvoked := false
			quay.GetRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				isGetRobotAccountInvoked = true
				return nil, nil
			}
			quay.RemovePermissionsForRepositoryFromAccountFunc = func(organization, imageRepository, accountName string, isRobot bool) error {
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				Expect(accountName).To(Equal(expectedNamespaceRobotAccountName))
				return nil
			}

			imageRepositoryConfigObject := imageRepositoryConfig{
				ResourceKey: &resourceKey,
				Labels: map[string]string{
					ApplicationNameLabelName: defaultComponentApplication,
					ComponentNameLabelName:   defaultComponentName,
				},
			}
			if setNotification {
				imageRepositoryConfigObject.Notifications = []imagerepositoryv1alpha1.Notifications{
					{
						Title:  "test-notification",
						Event:  imagerepositoryv1alpha1.NotificationEventRepoPush,
						Method: imagerepositoryv1alpha1.NotificationMethodWebhook,
						Config: imagerepositoryv1alpha1.NotificationConfig{
							Url: "http://test-url",
						},
					},
				}
			}

			if updateComponentAnnotation {
				imageRepositoryConfigObject.Annotations = map[string]string{updateComponentAnnotationName: "true"}
			}

			createImageRepository(imageRepositoryConfigObject)

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePushRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreatePullRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreateNamespaceRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPushPermissionsToAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPullPermissionsToAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPullPermissionsToNamespaceAccountInvoked }, timeout, interval).Should(BeTrue())
			if setNotification {
				Eventually(func() bool { return isCreateNotificationInvoked }, timeout, interval).Should(BeTrue())
			}
			Eventually(func() bool { return isGetRobotAccountInvoked }, timeout, interval).Should(BeTrue())

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			component := getComponent(componentKey)
			imageRepository := getImageRepository(resourceKey)

			if setNotification {
				Eventually(func() bool { return isGetNotificationsInvoked }, timeout, interval).Should(BeTrue())
			}
			if updateComponentAnnotation {
				Expect(component.Spec.ContainerImage).To(Equal(imageRepository.Status.Image.URL))
			} else {
				Expect(component.Spec.ContainerImage).To(BeEmpty())
			}
			Expect(imageRepository.Annotations).To(HaveLen(1))
			Expect(imageRepository.Annotations[namespacePullSecretEnsuredAnnotation]).To(Equal("true"))
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
			if setNotification {
				Expect(imageRepository.Status.Notifications).To(HaveLen(1))
				Expect(imageRepository.Status.Notifications[0].UUID).To(Equal("uuid"))
				Expect(imageRepository.Status.Notifications[0].Title).To(Equal("test-notification"))
			} else {
				Expect(imageRepository.Status.Notifications).To(HaveLen(0))
			}

			pushSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PushSecretName, Namespace: imageRepository.Namespace}
			pushSecret := waitSecretExist(pushSecretKey)
			defer deleteSecret(pushSecretKey)
			verifySecretSpec(pushSecret, "ImageRepository", imageRepository.GetName(), pushSecret.Name)

			pullSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PullSecretName, Namespace: imageRepository.Namespace}
			pullSecret := waitSecretExist(pullSecretKey)
			defer deleteSecret(pullSecretKey)
			verifySecretSpec(pullSecret, "ImageRepository", imageRepository.GetName(), pullSecret.Name)

			applicationSecretKey := types.NamespacedName{Name: applicationSecretName, Namespace: imageRepository.Namespace}
			applicationSecret := waitSecretExist(applicationSecretKey)
			defer deleteSecret(applicationSecretKey)
			verifySecretSpec(applicationSecret, "Application", applicationKey.Name, applicationSecretName)

			namespaceSecretKey := types.NamespacedName{Name: namespacePullSecretName, Namespace: imageRepository.Namespace}
			namespaceSecret := waitSecretExist(namespaceSecretKey)
			defer deleteSecret(namespaceSecretKey)
			verifySecretSpec(namespaceSecret, "", "", namespacePullSecretName)

			pushSecretDockerconfigJson := string(pushSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pushSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PushRobotAccountName, pushToken)
			pullSecretDockerconfigJson := string(pullSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pullSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PullRobotAccountName, pullToken)
			applicationSecretDockerconfigJson := string(applicationSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(applicationSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PullRobotAccountName, pullToken)
			namespaceSecretDockerconfigJson := string(namespaceSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(namespaceSecretDockerconfigJson, expectedNamespaceImage, expectedNamespaceRobotAccountName, namespaceRobotToken)

			componentSa := getServiceAccount(defaultNamespace, componentSaName)
			Expect(componentSa.Secrets).To(HaveLen(2))
			Expect(componentSa.ImagePullSecrets).To(HaveLen(0))
			Expect(componentSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pushSecret.Name}))
			Expect(componentSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: namespacePullSecretName}))
			integrationSa := getServiceAccount(defaultNamespace, IntegrationServiceAccountName)
			Expect(integrationSa.Secrets).To(HaveLen(2))
			Expect(integrationSa.ImagePullSecrets).To(HaveLen(2))
			Expect(integrationSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: applicationSecretName}))
			Expect(integrationSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: namespacePullSecretName}))
			Expect(integrationSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: applicationSecretName}))
			Expect(integrationSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: namespacePullSecretName}))
		}

		assertSecretsGoneFromServiceAccounts := func() {
			componentSa := getServiceAccount(defaultNamespace, componentSaName)
			Expect(componentSa.Secrets).To(HaveLen(1))
			Expect(componentSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: namespacePullSecretName}))
			Expect(componentSa.ImagePullSecrets).To(HaveLen(0))
			integrationSa := getServiceAccount(defaultNamespace, IntegrationServiceAccountName)
			Expect(integrationSa.Secrets).To(HaveLen(2))
			Expect(integrationSa.ImagePullSecrets).To(HaveLen(2))
			Expect(integrationSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: applicationSecretName}))
			Expect(integrationSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: namespacePullSecretName}))
			Expect(integrationSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: applicationSecretName}))
			Expect(integrationSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: namespacePullSecretName}))
		}

		It("should provision image repository for component, without update component annotation", func() {
			assertProvisionRepository(false, true)

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

		It("should regenerate pull & push tokens and create pull && push secrets when they don't exist", func() {
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

			pushSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PushSecretName, Namespace: imageRepository.Namespace}
			pushSecret := waitSecretExist(pushSecretKey)
			verifySecretSpec(pushSecret, "ImageRepository", imageRepository.GetName(), pushSecret.Name)

			pullSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PullSecretName, Namespace: imageRepository.Namespace}
			pullSecret := waitSecretExist(pullSecretKey)
			verifySecretSpec(pullSecret, "ImageRepository", imageRepository.GetName(), pullSecret.Name)

			applicationSecretKey := types.NamespacedName{Name: applicationSecretName, Namespace: imageRepository.Namespace}
			applicationSecret := waitSecretExist(applicationSecretKey)
			verifySecretSpec(applicationSecret, "Application", imageRepository.Labels[ApplicationNameLabelName], applicationSecretName)

			pushSecretDockerconfigJson := string(pushSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pushSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PushRobotAccountName, newPushToken)

			pullSecretDockerconfigJson := string(pullSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pullSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PullRobotAccountName, newPullToken)

			applicationSecretDockerconfigJson := string(applicationSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(applicationSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PullRobotAccountName, newPullToken)
		})

		It("should regenerate pull & push tokens and update pull && push secrets when they exist", func() {
			newPushToken := "push-token98765"
			newPullToken := "pull-token98765"

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

			pushSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PushSecretName, Namespace: imageRepository.Namespace}
			pushSecret := waitSecretExist(pushSecretKey)
			verifySecretSpec(pushSecret, "ImageRepository", imageRepository.GetName(), pushSecret.Name)

			pullSecretKey := types.NamespacedName{Name: imageRepository.Status.Credentials.PullSecretName, Namespace: imageRepository.Namespace}
			pullSecret := waitSecretExist(pullSecretKey)
			verifySecretSpec(pullSecret, "ImageRepository", imageRepository.GetName(), pullSecret.Name)

			applicationSecretKey := types.NamespacedName{Name: applicationSecretName, Namespace: imageRepository.Namespace}
			applicationSecret := waitSecretExist(applicationSecretKey)
			verifySecretSpec(applicationSecret, "Application", imageRepository.Labels[ApplicationNameLabelName], applicationSecretName)

			pushSecretDockerconfigJson := string(pushSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pushSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PushRobotAccountName, newPushToken)

			pullSecretDockerconfigJson := string(pullSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(pullSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PullRobotAccountName, newPullToken)

			applicationSecretDockerconfigJson := string(applicationSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(applicationSecretDockerconfigJson, expectedImage, imageRepository.Status.Credentials.PullRobotAccountName, newPullToken)
		})

		It("should regenerate namespace pull token and create namespace pull secret when it doesn't exists", func() {
			newNamespaceToken := namespaceRobotTokenRefreshed1

			isRegenerateNamespaceRobotAccountTokenInvoked := false
			quay.RegenerateRobotAccountTokenFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(robotName).To(Equal(expectedNamespaceRobotAccountName))
				isRegenerateNamespaceRobotAccountTokenInvoked = true
				return &quay.RobotAccount{Name: robotName, Token: newNamespaceToken}, nil
			}

			imageRepository := getImageRepository(resourceKey)
			regenerateToken := true
			imageRepository.Spec.Credentials = &imagerepositoryv1alpha1.ImageCredentials{RegenerateNamespacePullToken: &regenerateToken}
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			Eventually(func() bool { return isRegenerateNamespaceRobotAccountTokenInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Spec.Credentials.RegenerateNamespacePullToken == nil
			}, timeout, interval).Should(BeTrue())

			namespaceSecretKey := types.NamespacedName{Name: namespacePullSecretName, Namespace: imageRepository.Namespace}
			namespaceSecret := waitSecretExist(namespaceSecretKey)
			verifySecretSpec(namespaceSecret, "", "", namespacePullSecretName)
			namespaceSecretDockerconfigJson := string(namespaceSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(namespaceSecretDockerconfigJson, expectedNamespaceImage, expectedNamespaceRobotAccountName, newNamespaceToken)
		})

		It("should regenerate namespace pull token and update namespace pull secret when it exists", func() {
			newNamespaceToken := namespaceRobotTokenRefreshed2

			isRegenerateNamespaceRobotAccountTokenInvoked := false
			quay.RegenerateRobotAccountTokenFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(robotName).To(Equal(expectedNamespaceRobotAccountName))
				isRegenerateNamespaceRobotAccountTokenInvoked = true
				return &quay.RobotAccount{Name: robotName, Token: newNamespaceToken}, nil
			}

			imageRepository := getImageRepository(resourceKey)
			regenerateToken := true
			imageRepository.Spec.Credentials = &imagerepositoryv1alpha1.ImageCredentials{RegenerateNamespacePullToken: &regenerateToken}
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			Eventually(func() bool { return isRegenerateNamespaceRobotAccountTokenInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Spec.Credentials.RegenerateNamespacePullToken == nil
			}, timeout, interval).Should(BeTrue())

			namespaceSecretKey := types.NamespacedName{Name: namespacePullSecretName, Namespace: imageRepository.Namespace}
			namespaceSecret := waitSecretExist(namespaceSecretKey)
			verifySecretSpec(namespaceSecret, "", "", namespacePullSecretName)
			namespaceSecretDockerconfigJson := string(namespaceSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(namespaceSecretDockerconfigJson, expectedNamespaceImage, expectedNamespaceRobotAccountName, newNamespaceToken)
		})

		It("verify and fix, secret is missing from SAs", func() {
			quay.ResetTestQuayClient()

			applicationSa := getServiceAccount(defaultNamespace, IntegrationServiceAccountName)
			applicationSa.Secrets = []corev1.ObjectReference{}
			applicationSa.ImagePullSecrets = []corev1.LocalObjectReference{}
			Expect(k8sClient.Update(ctx, &applicationSa)).To(Succeed())

			// will add it to SA, but not to ImagePullSecrets
			componentSa := getServiceAccount(defaultNamespace, componentSaName)
			componentSa.Secrets = []corev1.ObjectReference{}
			Expect(k8sClient.Update(ctx, &componentSa)).To(Succeed())

			imageRepository := getImageRepository(resourceKey)
			verifyLinking := true
			imageRepository.Spec.Credentials = &imagerepositoryv1alpha1.ImageCredentials{VerifyLinking: &verifyLinking}
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			waitImageRepositoryCredentialSectionRequestGone(resourceKey, "verify")

			pushSecretName := fmt.Sprintf("%s-image-push", resourceKey.Name)
			applicationSa = getServiceAccount(defaultNamespace, IntegrationServiceAccountName)
			componentSa = getServiceAccount(defaultNamespace, componentSaName)
			Expect(componentSa.Secrets).To(HaveLen(2))
			Expect(componentSa.ImagePullSecrets).To(HaveLen(0))
			Expect(componentSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pushSecretName}))
			Expect(componentSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: namespacePullSecretName}))
			Expect(applicationSa.Secrets).To(HaveLen(2))
			Expect(applicationSa.ImagePullSecrets).To(HaveLen(2))
			Expect(applicationSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: applicationSecretName}))
			Expect(applicationSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: namespacePullSecretName}))
			Expect(applicationSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: applicationSecretName}))
			Expect(applicationSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: namespacePullSecretName}))
		})

		It("verify and fix, secret is duplicated in SA, also is in ImagePullSecrets", func() {
			quay.ResetTestQuayClient()
			pushSecretName := fmt.Sprintf("%s-image-push", resourceKey.Name)

			applicationSa := getServiceAccount(defaultNamespace, IntegrationServiceAccountName)
			applicationSa.Secrets = []corev1.ObjectReference{{Name: applicationSecretName}, {Name: applicationSecretName}}
			applicationSa.ImagePullSecrets = []corev1.LocalObjectReference{{Name: applicationSecretName}, {Name: applicationSecretName}}
			Expect(k8sClient.Update(ctx, &applicationSa)).To(Succeed())

			// will remove duplicate, and remove it from ImagePullSecrets
			componentSa := getServiceAccount(defaultNamespace, componentSaName)
			componentSa.Secrets = []corev1.ObjectReference{{Name: pushSecretName}, {Name: pushSecretName}}
			componentSa.ImagePullSecrets = []corev1.LocalObjectReference{{Name: pushSecretName}, {Name: pushSecretName}}
			Expect(k8sClient.Update(ctx, &componentSa)).To(Succeed())

			imageRepository := getImageRepository(resourceKey)
			verifyLinking := true
			imageRepository.Spec.Credentials = &imagerepositoryv1alpha1.ImageCredentials{VerifyLinking: &verifyLinking}
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			waitImageRepositoryCredentialSectionRequestGone(resourceKey, "verify")

			applicationSa = getServiceAccount(defaultNamespace, IntegrationServiceAccountName)
			componentSa = getServiceAccount(defaultNamespace, componentSaName)
			Expect(componentSa.Secrets).To(HaveLen(2))
			Expect(componentSa.ImagePullSecrets).To(HaveLen(0))
			Expect(componentSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: pushSecretName}))
			Expect(componentSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: namespacePullSecretName}))
			Expect(applicationSa.Secrets).To(HaveLen(2))
			Expect(applicationSa.ImagePullSecrets).To(HaveLen(2))
			Expect(applicationSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: applicationSecretName}))
			Expect(applicationSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: namespacePullSecretName}))
			Expect(applicationSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: applicationSecretName}))
			Expect(applicationSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: namespacePullSecretName}))
		})

		// TODO: remove after all IRs are processed and all have new namespace pull ensured annotation
		It("old already provisioned image repository without annotation will on next reconcile create namespace pull secret and robot account", func() {
			// Delete the namespace secret to verify it gets recreated
			namespaceSecretKey := types.NamespacedName{Name: namespacePullSecretName, Namespace: resourceKey.Namespace}
			deleteSecret(namespaceSecretKey)

			isGetRobotAccountInvoked := false
			quay.GetRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				isGetRobotAccountInvoked = true
				return nil, nil
			}
			isCreateNamespaceRobotAccountInvoked := false
			quay.CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				if robotName == expectedNamespaceRobotAccountName {
					isCreateNamespaceRobotAccountInvoked = true
					return &quay.RobotAccount{Name: robotName, Token: namespaceRobotTokenRefreshed2}, nil
				}
				return nil, nil
			}
			isAddPullPermissionsToNamespaceAccountInvoked := false
			quay.AddPermissionsForRepositoryToAccountFunc = func(organization, imageRepository, accountName string, isRobot, isWrite bool) error {
				defer GinkgoRecover()
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				if accountName == expectedNamespaceRobotAccountName {
					Expect(isWrite).To(BeFalse())
					isAddPullPermissionsToNamespaceAccountInvoked = true
				}
				return nil
			}

			componentSaName := getComponentSaName(defaultComponentName)

			// remove namespace pull secret ensured annotation to force new reconcile and simulate old IR
			imageRepository := getImageRepository(resourceKey)
			delete(imageRepository.Annotations, namespacePullSecretEnsuredAnnotation)
			Expect(k8sClient.Update(ctx, imageRepository)).To(Succeed())

			// Wait for the annotation to be set to true
			Eventually(func() bool {
				imageRepository := getImageRepository(resourceKey)
				return imageRepository.Annotations[namespacePullSecretEnsuredAnnotation] == "true"
			}, timeout, interval).Should(BeTrue())

			Eventually(func() bool { return isGetRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isCreateNamespaceRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isAddPullPermissionsToNamespaceAccountInvoked }, timeout, interval).Should(BeTrue())

			namespaceSecret := waitSecretExist(namespaceSecretKey)
			verifySecretSpec(namespaceSecret, "", "", namespacePullSecretName)
			namespaceSecretDockerconfigJson := string(namespaceSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(namespaceSecretDockerconfigJson, expectedNamespaceImage, expectedNamespaceRobotAccountName, namespaceRobotTokenRefreshed2)

			componentSa := getServiceAccount(defaultNamespace, componentSaName)
			Expect(componentSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: namespacePullSecretName}))
			integrationSa := getServiceAccount(defaultNamespace, IntegrationServiceAccountName)
			Expect(integrationSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: namespacePullSecretName}))
			Expect(integrationSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: namespacePullSecretName}))
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
			isGetRobotAccountInvoked := false
			quay.GetRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
				isGetRobotAccountInvoked = true
				return &quay.RobotAccount{Name: expectedNamespaceRobotAccountName, Token: namespaceRobotToken}, nil
			}
			isRemovePermissionsToRepositoryForAccountInvoked := false
			quay.RemovePermissionsForRepositoryFromAccountFunc = func(organization, imageRepository, accountName string, isRobot bool) error {
				isRemovePermissionsToRepositoryForAccountInvoked = true
				Expect(organization).To(Equal(quay.TestQuayOrg))
				Expect(imageRepository).To(Equal(expectedImageName))
				Expect(accountName).To(Equal(expectedNamespaceRobotAccountName))
				return nil
			}

			deleteImageRepository(resourceKey)

			Eventually(func() bool { return isDeleteRobotAccountForPushInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRobotAccountForPullInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRepositoryInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isGetRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isRemovePermissionsToRepositoryForAccountInvoked }, timeout, interval).Should(BeTrue())

			applicationSecretKey := types.NamespacedName{Name: applicationSecretName, Namespace: defaultNamespace}
			applicationSecret := waitSecretExist(applicationSecretKey)
			applicationSecretDockerconfigJson := string(applicationSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuthEmpty(applicationSecretDockerconfigJson)

			namespaceSecretKey := types.NamespacedName{Name: namespacePullSecretName, Namespace: resourceKey.Namespace}
			namespaceSecret := waitSecretExist(namespaceSecretKey)
			verifySecretSpec(namespaceSecret, "", "", namespacePullSecretName)
			namespaceSecretDockerconfigJson := string(namespaceSecret.Data[corev1.DockerConfigJsonKey])
			verifySecretAuth(namespaceSecretDockerconfigJson, expectedNamespaceImage, expectedNamespaceRobotAccountName, namespaceRobotTokenRefreshed2)

			assertSecretsGoneFromServiceAccounts()

			deleteServiceAccount(types.NamespacedName{Name: componentSaName, Namespace: defaultNamespace})
			deleteServiceAccount(types.NamespacedName{Name: IntegrationServiceAccountName, Namespace: defaultNamespace})
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
			expectedImage = fmt.Sprintf("%s/%s/%s", quay.TestQuayDomain, quay.TestQuayOrg, expectedImageName)
			expectedRobotAccountPrefix = sanitizeNameForQuay(expectedImageName)
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
			deleteImageRepository(resourceKey)
		})
	})

	Context("Other image repository scenarios", func() {
		var resourceKey = types.NamespacedName{Name: defaultImageRepositoryName + "-other", Namespace: defaultNamespace}

		BeforeEach(func() {
			quay.ResetTestQuayClient()
			deleteImageRepository(resourceKey)
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
			expectedRobotAccountPrefix = sanitizeNameForQuay(expectedImageName)

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

		It("don't remove nudging pull secret from component's SA, if ImageRepository is not for component", func() {
			serviceAccountForSomeComponent := fmt.Sprintf("%s%s", componentSaNamePrefix, "sa-for-some-component")
			serviceAccountCommon := "common-sa"

			customImageName := defaultNamespace + "/" + "my-image-for-nudging-component"
			expectedImageName = customImageName
			expectedRobotAccountPrefix = sanitizeNameForQuay(expectedImageName)

			isCreateRepositoryInvoked := false
			quay.CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(quay.TestQuayOrg))
				return &quay.Repository{Name: expectedImageName}, nil
			}

			// create imageRepository without component
			createImageRepository(imageRepositoryConfig{
				ResourceKey: &resourceKey,
				ImageName:   customImageName,
			})

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			nudgingImageRepository := getImageRepository(resourceKey)
			nudgedPullSecretName := nudgingImageRepository.Status.Credentials.PullSecretName
			commonPullSecretName := "common-pull-secret"
			commonPushSecretName := "common-push-secret"

			// create 2 SAs, one common, another for some component, both will include pullSecret from nudging component's imageRepository
			// and one common pull & push secret
			pullSecrets := []string{commonPullSecretName, nudgedPullSecretName}
			pushSecrets := []string{commonPushSecretName, nudgedPullSecretName}
			createServiceAccountWithSecrets(defaultNamespace, serviceAccountForSomeComponent, pushSecrets, pullSecrets)
			createServiceAccountWithSecrets(defaultNamespace, serviceAccountCommon, pushSecrets, pullSecrets)
			defer deleteServiceAccount(types.NamespacedName{Name: serviceAccountForSomeComponent, Namespace: defaultNamespace})
			defer deleteServiceAccount(types.NamespacedName{Name: serviceAccountCommon, Namespace: defaultNamespace})

			someComponentSa := getServiceAccount(defaultNamespace, serviceAccountForSomeComponent)
			commonSa := getServiceAccount(defaultNamespace, serviceAccountCommon)
			// verify that created SAs have 2 secrets in each section
			Expect(len(someComponentSa.Secrets)).To(Equal(2))
			Expect(len(someComponentSa.ImagePullSecrets)).To(Equal(2))
			Expect(len(commonSa.Secrets)).To(Equal(2))
			Expect(len(commonSa.ImagePullSecrets)).To(Equal(2))
			// and also both contain pullSecret from nudging component's imageRepository
			Expect(someComponentSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: nudgedPullSecretName}))
			Expect(someComponentSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: nudgedPullSecretName}))
			Expect(commonSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: nudgedPullSecretName}))
			Expect(commonSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: nudgedPullSecretName}))

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
				Expect(imageRepository).To(Equal(customImageName))
				return true, nil
			}

			deleteImageRepository(resourceKey)
			Eventually(func() bool { return isDeleteRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRepositoryInvoked }, timeout, interval).Should(BeTrue())

			// deleting imageRepository won't remove imageRepository's pull secret, because it wasn't for any component
			someComponentSa = getServiceAccount(defaultNamespace, serviceAccountForSomeComponent)
			commonSa = getServiceAccount(defaultNamespace, serviceAccountCommon)

			// common SA and some component SA will still have 2 secrets linked and one of them is pull secret from nudged Component
			Expect(len(commonSa.Secrets)).To(Equal(2))
			Expect(len(commonSa.ImagePullSecrets)).To(Equal(2))
			Expect(commonSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: nudgedPullSecretName}))
			Expect(commonSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: nudgedPullSecretName}))

			Expect(len(someComponentSa.Secrets)).To(Equal(2))
			Expect(len(someComponentSa.ImagePullSecrets)).To(Equal(2))
			Expect(someComponentSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: nudgedPullSecretName}))
			Expect(someComponentSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: nudgedPullSecretName}))
		})

		It("remove nudging pull secret from nudged components SA", func() {
			serviceAccountForNudgedComponent := fmt.Sprintf("%s%s", componentSaNamePrefix, "sa-for-nudged-component")
			serviceAccountCommon := "common-sa"
			applicationKey := types.NamespacedName{Name: "nudging-application", Namespace: defaultNamespace}
			componentKey := types.NamespacedName{Name: "nudging-component", Namespace: defaultNamespace}
			componentSaName := getComponentSaName(componentKey.Name)
			createApplication(applicationConfig{ApplicationKey: applicationKey})
			createComponent(componentConfig{ComponentKey: componentKey, ComponentApplication: defaultComponentApplication})
			createServiceAccount(defaultNamespace, componentSaName)
			defer deleteComponent(componentKey)
			defer deleteApplication(applicationKey)
			defer deleteServiceAccount(types.NamespacedName{Name: componentSaName, Namespace: defaultNamespace})

			customImageName := defaultNamespace + "/" + "my-image-for-nudging-component"
			expectedImageName = customImageName
			expectedRobotAccountPrefix = sanitizeNameForQuay(expectedImageName)

			isCreateRepositoryInvoked := false
			quay.CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()
				isCreateRepositoryInvoked = true
				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(quay.TestQuayOrg))
				return &quay.Repository{Name: expectedImageName}, nil
			}

			// create imageRepository for component
			createImageRepository(imageRepositoryConfig{
				ResourceKey: &resourceKey,
				ImageName:   customImageName,
				Labels: map[string]string{
					ApplicationNameLabelName: applicationKey.Name,
					ComponentNameLabelName:   componentKey.Name,
				},
			})

			Eventually(func() bool { return isCreateRepositoryInvoked }, timeout, interval).Should(BeTrue())
			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			nudgingImageRepository := getImageRepository(resourceKey)
			nudgedPullSecretName := nudgingImageRepository.Status.Credentials.PullSecretName
			commonPullSecretName := "common-pull-secret"
			commonPushSecretName := "common-push-secret"

			// create 2 SAs, one common, another for nudged component, both will include pullSecret from nudging component's imageRepository
			// and one common pull & push secret
			pullSecrets := []string{commonPullSecretName, nudgedPullSecretName}
			pushSecrets := []string{commonPushSecretName, nudgedPullSecretName}
			createServiceAccountWithSecrets(defaultNamespace, serviceAccountForNudgedComponent, pushSecrets, pullSecrets)
			createServiceAccountWithSecrets(defaultNamespace, serviceAccountCommon, pushSecrets, pullSecrets)
			defer deleteServiceAccount(types.NamespacedName{Name: serviceAccountForNudgedComponent, Namespace: defaultNamespace})
			defer deleteServiceAccount(types.NamespacedName{Name: serviceAccountCommon, Namespace: defaultNamespace})

			nudgedComponentSa := getServiceAccount(defaultNamespace, serviceAccountForNudgedComponent)
			commonSa := getServiceAccount(defaultNamespace, serviceAccountCommon)
			// verify that created SAs have 2 secrets in each section
			Expect(len(nudgedComponentSa.Secrets)).To(Equal(2))
			Expect(len(nudgedComponentSa.ImagePullSecrets)).To(Equal(2))
			Expect(len(commonSa.Secrets)).To(Equal(2))
			Expect(len(commonSa.ImagePullSecrets)).To(Equal(2))
			// and also both contain pullSecret from nudging component's imageRepository
			Expect(nudgedComponentSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: nudgedPullSecretName}))
			Expect(nudgedComponentSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: nudgedPullSecretName}))
			Expect(commonSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: nudgedPullSecretName}))
			Expect(commonSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: nudgedPullSecretName}))

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
				Expect(imageRepository).To(Equal(customImageName))
				return true, nil
			}

			deleteImageRepository(resourceKey)
			Eventually(func() bool { return isDeleteRobotAccountInvoked }, timeout, interval).Should(BeTrue())
			Eventually(func() bool { return isDeleteRepositoryInvoked }, timeout, interval).Should(BeTrue())

			// deleting imageRepository will also remove nudging pull secret from nudged component's SA
			// but will leave common secret
			// and won't remove anything from common SA
			nudgedComponentSa = getServiceAccount(defaultNamespace, serviceAccountForNudgedComponent)
			commonSa = getServiceAccount(defaultNamespace, serviceAccountCommon)

			// common SA will still have 2 secrets linked and one of them is pull secret from nudged Component
			Expect(len(commonSa.Secrets)).To(Equal(2))
			Expect(len(commonSa.ImagePullSecrets)).To(Equal(2))
			Expect(commonSa.Secrets).To(ContainElement(corev1.ObjectReference{Name: nudgedPullSecretName}))
			Expect(commonSa.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: nudgedPullSecretName}))

			// nudged component SA will have only 1 secret and pull secret from nudged Component will be gone
			Expect(len(nudgedComponentSa.Secrets)).To(Equal(1))
			Expect(len(nudgedComponentSa.ImagePullSecrets)).To(Equal(1))
			Expect(nudgedComponentSa.Secrets).To(Equal([]corev1.ObjectReference{corev1.ObjectReference{Name: commonPushSecretName}}))
			Expect(nudgedComponentSa.ImagePullSecrets).To(Equal([]corev1.LocalObjectReference{corev1.LocalObjectReference{Name: commonPullSecretName}}))
		})

		It("should create image repository if 502 Bad Gateway is returned by Quay intermittently", func() {
			expectedImageName = fmt.Sprintf("%s/%s", defaultNamespace, resourceKey.Name)

			createRepositoryInvocationCount := 0
			quay.CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
				defer GinkgoRecover()

				Expect(repository.Repository).To(Equal(expectedImageName))
				Expect(repository.Namespace).To(Equal(quay.TestQuayOrg))
				Expect(repository.Visibility).To(Equal("public"))
				Expect(repository.Description).ToNot(BeEmpty())

				createRepositoryInvocationCount++
				if createRepositoryInvocationCount <= 2 {
					return nil, fmt.Errorf("502 Bad Gateway. failed to unmarshal response body: invalid character '<'")
				}
				return &quay.Repository{Name: expectedImageName}, nil
			}

			createImageRepository(imageRepositoryConfig{ResourceKey: &resourceKey})
			defer deleteImageRepository(resourceKey)

			Eventually(func() bool { return createRepositoryInvocationCount == 3 }, timeout, interval).Should(BeTrue())

			waitImageRepositoryFinalizerOnImageRepository(resourceKey)

			imageRepository := getImageRepository(resourceKey)
			Expect(imageRepository.Spec.Image.Name).To(Equal(expectedImageName))
		})
	})

	Context("Skip quay repository deletion", func() {
		var resourceKey = types.NamespacedName{Name: defaultImageRepositoryName + "-skip-delete", Namespace: defaultNamespace}

		BeforeEach(func() {
			quay.ResetTestQuayClient()
			deleteImageRepository(resourceKey)
		})

		It("don't remove repository if explicitly requested using annotation", func() {
			customImageName := defaultNamespace + "/" + "my-image-should-not-be-removed"
			expectedImageName = customImageName
			expectedRobotAccountPrefix = sanitizeNameForQuay(expectedImageName)

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
			expectedImage = fmt.Sprintf("%s/%s/%s", quay.TestQuayDomain, quay.TestQuayOrg, expectedImageName)
			expectedRobotAccountPrefix = sanitizeNameForQuay(expectedImageName)
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
	})
})
