package e2e

import (
	"fmt"
	"time"

	"github.com/devfile/library/v2/pkg/util"
	applicationApi "github.com/konflux-ci/application-api/api/konflux/v1alpha1"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/constants"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/framework"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/utils"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/utils/build"
	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
	. "github.com/onsi/gomega"    //nolint:staticcheck
	pipeline "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = framework.ImageControllerSuiteDescribe("Image Controller E2E tests", Label("image-controller-e2e"), func() {

	var f *framework.Framework
	var err error
	defer GinkgoRecover()

	Describe("create an image repository without spec", Label("no-spec"), Ordered, func() {
		var testNamespace, imageRepositoryCRName, imageRepoName string
		var pullRobotAccountName, pushRobotAccountName, pullSecretName, pushSecretName string
		var pushSecret *corev1.Secret

		BeforeAll(func() {
			f, err = framework.NewFramework(utils.GetGeneratedNamespace(constants.ImageControllerE2ETestNamesapcePrefix))
			Expect(err).NotTo(HaveOccurred())
			testNamespace = f.TestNamespace
			imageRepositoryCRName = "sample-image-repo-" + util.GenerateRandomString(4)
		})

		AfterAll(func() {
			if !CurrentSpecReport().Failed() {
				Expect(f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(imageRepositoryCRName, testNamespace)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete imagerepository %s", imageRepositoryCRName))
			}
		})

		It("image repository is created successfully", func() {
			_, err = f.AsKubeAdmin.CommonController.CreateImageRepositoryCR(imageRepositoryCRName, testNamespace, "", "", "", false, false)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", imageRepositoryCRName))

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(imageRepositoryCRName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", imageRepositoryCRName))
		})
		It("registry side image repo and robot accounts created successfully", func() {
			imageRepoName, err = f.AsKubeAdmin.CommonController.GetImageNameFromImageRepositoryCR(testNamespace, imageRepositoryCRName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to read image repo from image repository CR: %s", imageRepositoryCRName)
			Expect(imageRepoName).ShouldNot(BeEmpty(), "image repo name is empty")

			imageExist, err := build.DoesImageRepoExistInQuay(imageRepoName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image repo exists in quay with error: %+v", err)
			Expect(imageExist).To(BeTrue(), "quay image does not exists")

			pullRobotAccountName, pushRobotAccountName, err = f.AsKubeAdmin.CommonController.GetRobotAccountsFromImageRepositoryCR(testNamespace, imageRepositoryCRName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get robot account names")
			pullRobotAccountExist, err := build.DoesRobotAccountExistInQuay(pullRobotAccountName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if pull robot account exists in quay with error: %+v", err)
			Expect(pullRobotAccountExist).To(BeTrue(), "pull robot account does not exists in quay")
			pushRobotAccountExist, err := build.DoesRobotAccountExistInQuay(pushRobotAccountName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if push robot account exists in quay with error: %+v", err)
			Expect(pushRobotAccountExist).To(BeTrue(), "push robot account does not exists in quay")
		})
		It("in cluster secrets are created successfully", func() {
			pullSecretName, pushSecretName, err = f.AsKubeAdmin.CommonController.GetSecretsFromImageRepositoryCR(testNamespace, imageRepositoryCRName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get secrets from image repository")
			_, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting pull secret: %+v", err)
			pushSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting push secret: %+v", err)
			_, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, constants.ComponentNamespacePullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting namespace pull secret: %+v", err)
		})
		It("pushing image to the registry is successful", func() {
			imageRepoURL, err := f.AsKubeAdmin.CommonController.GetImageURLFromIR(imageRepositoryCRName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while reading image repo URL from IR: %+v", err)
			err = build.BuildMockImageAndPush(pushSecret, imageRepoURL+":latest")
			Expect(err).ShouldNot(HaveOccurred(), "failed while build and push the image: %+v", err)
		})
		It("image repository CR is deleted successfully", func() {
			err = f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(imageRepositoryCRName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete image repository: %s", imageRepositoryCRName))
		})
		It("registry side resources cleaned successfully", func() {
			Eventually(func() bool {
				imageExist, err := build.DoesImageRepoExistInQuay(imageRepoName)
				if err != nil {
					fmt.Printf("failed while checking if image repo exists in quay: %v", err)
					return true
				}
				return imageExist
			}, 2*time.Minute, 10*time.Second).Should(BeFalse(), "quay image still exists which is unexpected")

			Eventually(func() bool {
				robotExist, err := build.DoesRobotAccountExistInQuay(pullRobotAccountName)
				if err != nil {
					fmt.Printf("failed while checking if pull robot account exists in quay: %v", err)
					return true
				}
				return robotExist
			}, 2*time.Minute, 10*time.Second).Should(BeFalse(), "quay pull robot account still exists, which is unexpected")

			Eventually(func() bool {
				robotExist, err := build.DoesRobotAccountExistInQuay(pushRobotAccountName)
				if err != nil {
					fmt.Printf("failed while checking if push robot account exists in quay: %v", err)
					return true
				}
				return robotExist
			}, 2*time.Minute, 10*time.Second).Should(BeFalse(), "quay push robot account still exists, which is unexpected")
		})
		It("in cluster resources cleaned successfully", func() {
			_, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pullSecretName)
			Expect(err).To(MatchError(ContainSubstring("not found")))
			_, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pushSecretName)
			Expect(err).To(MatchError(ContainSubstring("not found")))
		})
	})
	Describe("creates an image repository with custom name", Label("custom-image"), Ordered, func() {
		var testNamespace, imageRepositoryName, customImageName string
		var imageRepoName, pullRobotAccountName, pushRobotAccountName, pullSecretName, pushSecretName, imageRepoURL string
		var pushSecret *corev1.Secret

		BeforeAll(func() {
			f, err = framework.NewFramework(utils.GetGeneratedNamespace(constants.ImageControllerE2ETestNamesapcePrefix))
			Expect(err).NotTo(HaveOccurred())
			testNamespace = f.TestNamespace
			imageRepositoryName = "image-repository-" + util.GenerateRandomString(4)
			customImageName = "custom-image-repo-" + util.GenerateRandomString(4)
		})

		AfterAll(func() {
			if !CurrentSpecReport().Failed() {
				Expect(f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(imageRepositoryName, testNamespace)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete imagerepository %s", imageRepositoryName))
			}
		})

		It("image repository is created successfully", func() {
			_, err = f.AsKubeAdmin.CommonController.CreateImageRepositoryCR(imageRepositoryName, testNamespace, "public", customImageName, "", false, false)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", imageRepositoryName))

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", imageRepositoryName))
		})
		It("validate image repo URL has tenant namespace", func() {
			imageRepoURL, err = f.AsKubeAdmin.CommonController.GetImageURLFromIR(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while reading image repo URL from IR: %+v", err)
			Expect(imageRepoURL).To(ContainSubstring(testNamespace))
		})
		It("registry side image repo and robot accounts created successfully", func() {
			imageRepoName, err = f.AsKubeAdmin.CommonController.GetImageNameFromImageRepositoryCR(testNamespace, imageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to read image repo from image repository CR: %s", imageRepositoryName)
			Expect(imageRepoName).ShouldNot(BeEmpty(), "image repo name is empty")

			imageExist, err := build.DoesImageRepoExistInQuay(testNamespace + "/" + imageRepoName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image repo exists in quay with error: %+v", err)
			Expect(imageExist).To(BeTrue(), "quay image does not exists")

			pullRobotAccountName, pushRobotAccountName, err = f.AsKubeAdmin.CommonController.GetRobotAccountsFromImageRepositoryCR(testNamespace, imageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get robot account names")
			pullRobotAccountExist, err := build.DoesRobotAccountExistInQuay(pullRobotAccountName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if pull robot account exists in quay with error: %+v", err)
			Expect(pullRobotAccountExist).To(BeTrue(), "pull robot account does not exists in quay")
			pushRobotAccountExist, err := build.DoesRobotAccountExistInQuay(pushRobotAccountName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if push robot account exists in quay with error: %+v", err)
			Expect(pushRobotAccountExist).To(BeTrue(), "push robot account does not exists in quay")
		})
		It("in cluster secrets are created successfully", func() {
			pullSecretName, pushSecretName, err = f.AsKubeAdmin.CommonController.GetSecretsFromImageRepositoryCR(testNamespace, imageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get secrets from image repository")
			_, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting pull secret: %+v", err)
			pushSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting push secret: %+v", err)
			_, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, constants.ComponentNamespacePullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting namespace pull secret: %+v", err)
		})
		It("pushing image to the registry is successful", func() {
			err = build.BuildMockImageAndPush(pushSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while build and push the image: %+v", err)
		})
		It("when tring to update image name, it should fail", func() {
			err = f.AsKubeAdmin.CommonController.UpdateImageName(imageRepositoryName, testNamespace, "dummy-name")
			Expect(err).To(MatchError(ContainSubstring("Image repository name cannot be changed")))
		})
	})
	Describe("creates an image repository with visibility private", Label("private"), Ordered, func() {
		var testNamespace, imageRepositoryName string
		var imageRepoName, pullRobotAccountName, pushRobotAccountName, pullSecretName, pushSecretName, imageRepoURL string
		var pushSecret, pullSecret, namespacePullSecret *corev1.Secret

		BeforeAll(func() {
			f, err = framework.NewFramework(utils.GetGeneratedNamespace(constants.ImageControllerE2ETestNamesapcePrefix))
			Expect(err).NotTo(HaveOccurred())
			testNamespace = f.TestNamespace
			imageRepositoryName = "image-repository-" + util.GenerateRandomString(4)
		})

		AfterAll(func() {
			if !CurrentSpecReport().Failed() {
				Expect(f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(imageRepositoryName, testNamespace)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete imagerepository %s", imageRepositoryName))
			}
		})

		It("image repository is created successfully", func() {
			_, err = f.AsKubeAdmin.CommonController.CreateImageRepositoryCR(imageRepositoryName, testNamespace, "private", "", "", false, false)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", imageRepositoryName))

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", imageRepositoryName))
		})
		It("registry side image repo and robot accounts created successfully", func() {
			imageRepoName, err = f.AsKubeAdmin.CommonController.GetImageNameFromImageRepositoryCR(testNamespace, imageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to read image repo from image repository CR: %s", imageRepositoryName)
			Expect(imageRepoName).ShouldNot(BeEmpty(), "image repo name is empty")

			imageExist, err := build.DoesImageRepoExistInQuay(imageRepoName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image repo exists in quay with error: %+v", err)
			Expect(imageExist).To(BeTrue(), "quay image does not exists")

			pullRobotAccountName, pushRobotAccountName, err = f.AsKubeAdmin.CommonController.GetRobotAccountsFromImageRepositoryCR(testNamespace, imageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get robot account names")
			pullRobotAccountExist, err := build.DoesRobotAccountExistInQuay(pullRobotAccountName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if pull robot account exists in quay with error: %+v", err)
			Expect(pullRobotAccountExist).To(BeTrue(), "pull robot account does not exists in quay")
			pushRobotAccountExist, err := build.DoesRobotAccountExistInQuay(pushRobotAccountName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if push robot account exists in quay with error: %+v", err)
			Expect(pushRobotAccountExist).To(BeTrue(), "push robot account does not exists in quay")
		})
		It("registry side image repo is private", func() {
			isPublic, err := build.IsImageRepoPublic(imageRepoName)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while checking if the image repo %s is private", imageRepoName))
			Expect(isPublic).To(BeFalse(), "Expected image repo to be private, but it is public")
		})
		It("in cluster secrets are created successfully", func() {
			pullSecretName, pushSecretName, err = f.AsKubeAdmin.CommonController.GetSecretsFromImageRepositoryCR(testNamespace, imageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get secrets from image repository")
			pullSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting pull secret: %+v", err)
			pushSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting push secret: %+v", err)
			namespacePullSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, constants.ComponentNamespacePullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting namespace pull secret: %+v", err)
		})
		It("pushing image to the registry is successful", func() {
			imageRepoURL, err = f.AsKubeAdmin.CommonController.GetImageURLFromIR(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while reading image repo URL from IR: %+v", err)
			err = build.BuildMockImageAndPush(pushSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while build and push the image: %+v", err)
		})
		It("try to pull image anonymously, it should fail", func() {
			isPullable, err := build.IsImagePullableAnonymously(imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable: %+v", err)
			Expect(isPullable).To(BeFalse(), "image is pullable, which is unexpected")
		})
		It("try to pull the image using pull secrets, it should work", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(pullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable using pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeTrue(), "image is not pullable with pull secret, which is unexpected")

			isPullableWithNamespacePullSecret, err := build.IsImagePullableWithSecret(namespacePullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable using namespace pull secret: %+v", err)
			Expect(isPullableWithNamespacePullSecret).To(BeTrue(), "image is not pullable with namespace pull secret, which is unexpected")
		})
		It("change the visibility to public", func() {
			err = f.AsKubeAdmin.CommonController.UpdateVisibility(imageRepositoryName, testNamespace, "public")
			Expect(err).ShouldNot(HaveOccurred(), "failed while updating visibility to public: %+v", err)
			time.Sleep(2 * time.Second) // Wait for 2 seconds for change to take effect
		})
		It("try to pull image anonymously, it should work", func() {
			isPullable, err := build.IsImagePullableAnonymously(imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable: %+v", err)
			Expect(isPullable).To(BeTrue(), "image is not pullable, which is unexpected")
		})
		It("change the visibility to private again", func() {
			err = f.AsKubeAdmin.CommonController.UpdateVisibility(imageRepositoryName, testNamespace, "private")
			Expect(err).ShouldNot(HaveOccurred(), "failed while updating visibility to private: %+v", err)
			time.Sleep(2 * time.Second) // Wait for 2 seconds for change to take effect
		})
		It("try to pull image anonymously again, it should fail", func() {
			isPullable, err := build.IsImagePullableAnonymously(imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable again: %+v", err)
			Expect(isPullable).To(BeFalse(), "image is pullable, which is unexpected")
		})
		It("try to pull the image using pull secret again, it should work", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(pullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable again using pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeTrue(), "image is not pullable again with pull secret, which is unexpected")
		})
	})
	Describe("credential rotations", Label("credential-rotation"), Ordered, func() {
		var testNamespace, componentName, imageRepositoryName, serviceAccountName string
		var targetRepoName, testRepoUrl, testVersionName string
		var pullSecretName, pushSecretName, imageRepoURL string
		var oldPushSecret, oldPullSecret, oldNamespacePullSecret *corev1.Secret
		var oldPushSecretContent, oldPullSecretContent, oldNamespacePullSecretContent []byte
		var newPushSecret, newPullSecret, newNamespacePullSecret *corev1.Secret
		var newPushSecretContent, newPullSecretContent, newNamespacePullSecretContent []byte
		var firstGenerateTimestamp string
		var plr *pipeline.PipelineRun

		BeforeAll(func() {
			f, err = framework.NewFramework(utils.GetGeneratedNamespace(constants.ImageControllerE2ETestNamesapcePrefix))
			Expect(err).NotTo(HaveOccurred())
			testNamespace = f.TestNamespace
			imageRepositoryName = "image-repository-" + util.GenerateRandomString(4)
			componentName = "test-comp-" + util.GenerateRandomString(4)
			serviceAccountName = "build-pipeline-" + componentName
			testVersionName = "latest"
		})

		AfterAll(func() {
			if !CurrentSpecReport().Failed() {
				Expect(f.AsKubeAdmin.CommonController.DeleteComponent(componentName, testNamespace)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete component %s", componentName))
				Expect(f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(imageRepositoryName, testNamespace)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete imagerepository %s", imageRepositoryName))
				Expect(f.AsKubeAdmin.CommonController.Github.DeleteRepositoryIfExists(targetRepoName)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete repository: %s", targetRepoName))
			}
		})
		It("component is created successfully", func() {
			// Fork the github repository before creating component
			targetRepoName = fmt.Sprintf("%s-%s", constants.SampleTestRepoName, util.GenerateRandomString(4))
			_, err = f.AsKubeAdmin.CommonController.Github.ForkRepository(constants.SampleTestRepoName, targetRepoName)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to fork repository: %s", targetRepoName))
			testRepoUrl = fmt.Sprintf("https://github.com/%s/%s", githubOrg, targetRepoName)

			// Create the component
			componentObject := &applicationApi.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:      componentName,
					Namespace: testNamespace,
				},
				Spec: applicationApi.ComponentSpec{
					Source: applicationApi.ComponentSource{
						GitURL: testRepoUrl,
						Versions: []applicationApi.ComponentVersion{
							{
								Name:     testVersionName,
								Revision: "main",
							},
						},
					},
					Actions: applicationApi.ComponentActions{
						CreateConfiguration: applicationApi.ComponentCreatePipelineConfiguration{
							Version: testVersionName,
						},
					},
					DefaultBuildPipeline: &applicationApi.ComponentBuildPipeline{
						PullAndPush: &applicationApi.PipelineDefinition{
							PipelineSpecFromBundle: &applicationApi.PipelineSpecFromBundle{
								Name:   string(constants.DockerBuildOciTAMin),
								Bundle: "latest",
							},
						},
					},
				},
			}
			_, err = f.AsKubeAdmin.CommonController.CreateComponent(componentObject)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create component: %s", componentName))
		})
		It("image repository is created successfully", func() {
			_, err = f.AsKubeAdmin.CommonController.CreateImageRepositoryCR(imageRepositoryName, testNamespace, "private", "", componentName, true, false)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", imageRepositoryName))

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", imageRepositoryName))
			// Read the generate timestamp to compare later
			firstGenerateTimestamp, err = f.AsKubeAdmin.CommonController.GetGenerateTimestamp(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting generate timestamp from image reposiotry: %+v", err)
			Expect(firstGenerateTimestamp).ShouldNot(BeEmpty(), "generate timestamp is empty, which is unexpected")
		})
		It("check component version onboarding status", func() {
			// Wait for component test version status to succeed
			err = f.AsKubeAdmin.CommonController.WaitForComponentVersionOnboardingToSucceed(componentName, testNamespace, testVersionName)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while checking component %q version status to succeeded", componentName))
		})
		It("triggers a PipelineRun", func() {
			Eventually(func() error {
				plr, err = f.AsKubeAdmin.CommonController.GetComponentPipelineRun(componentName, testNamespace, "build", "pull_request", "")
				if err != nil {
					GinkgoWriter.Printf("PipelineRun has not been created yet for the component %s/%s\n", testNamespace, componentName)
					return err
				}
				if !plr.HasStarted() {
					return fmt.Errorf("pipelinerun %s/%s hasn't started yet", plr.GetNamespace(), plr.GetName())
				}
				return nil
			}, time.Minute*10, constants.PipelineRunPollingInterval).Should(Succeed(), fmt.Sprintf("timed out when waiting for the PipelineRun to start for the component %s/%s", testNamespace, componentName))
		})
		It("in cluster secrets are created successfully", func() {
			pullSecretName, pushSecretName, err = f.AsKubeAdmin.CommonController.GetSecretsFromImageRepositoryCR(testNamespace, imageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get secrets from image repository")
			oldPullSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting pull secret: %+v", err)
			oldPullSecretContent = oldPullSecret.Data[corev1.DockerConfigJsonKey]
			Expect(oldPullSecretContent).ShouldNot(BeEmpty(), "pull secret data is empty, which is unexpected")
			oldPushSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting push secret: %+v", err)
			oldPushSecretContent = oldPushSecret.Data[corev1.DockerConfigJsonKey]
			Expect(oldPushSecretContent).ShouldNot(BeEmpty(), "push secret data is empty, which is unexpected")
			oldNamespacePullSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, constants.ComponentNamespacePullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting namespace pull secret: %+v", err)
			oldNamespacePullSecretContent = oldNamespacePullSecret.Data[corev1.DockerConfigJsonKey]
			Expect(oldNamespacePullSecretContent).ShouldNot(BeEmpty(), "namespace pull secret data is empty, which is unexpected")
		})
		It("pushing image to the registry is successful", func() {
			imageRepoURL, err = f.AsKubeAdmin.CommonController.GetImageURLFromIR(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while reading image repo URL from IR: %+v", err)
			err = build.BuildMockImageAndPush(oldPushSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while build and push the image: %+v", err)
		})
		It("try to pull the image using pull secrets, it should work", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(oldPullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable using pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeTrue(), "image is not pullable with pull secret, which is unexpected")

			isPullableWithNamespacePullSecret, err := build.IsImagePullableWithSecret(oldNamespacePullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable using namespace pull secret: %+v", err)
			Expect(isPullableWithNamespacePullSecret).To(BeTrue(), "image is not pullable with namespace pull secret, which is unexpected")
		})
		It("check credential rotaion is successful", func() {
			err = f.AsKubeAdmin.CommonController.RegenerateToken(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while regenerating pull and push robot tokens: %+v", err)
			Eventually(func() error {
				currentGenerateTimestamp, err := f.AsKubeAdmin.CommonController.GetGenerateTimestamp(imageRepositoryName, testNamespace)
				if err != nil {
					GinkgoWriter.Printf("failed to get generate timestamp after rotation with error %v\n", err)
					return err
				}
				if currentGenerateTimestamp == firstGenerateTimestamp {
					return fmt.Errorf("Current generate timestamp %q is not equal to earlier generate timestamp %q\n", currentGenerateTimestamp, firstGenerateTimestamp)
				}
				return nil
			}, 2*time.Minute, 5*time.Second).Should(Succeed(), fmt.Sprintf("timed out when checking generate timestamp is updated for %s/%s", testNamespace, imageRepositoryName))

			// check regenerate-token is removed from the IR spec
			ir, err := f.AsKubeAdmin.CommonController.GetImageRepository(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting image repository: %+v", err)
			Expect(ir.Spec.Credentials.RegenerateToken).Should(BeNil(), "regenerate-token is not nil after the rotation, unexpected")
		})
		It("check the secret content is changed", func() {
			// Fetch the secrets again and compare with old content
			newPullSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting pull secret: %+v", err)
			newPullSecretContent = newPullSecret.Data[corev1.DockerConfigJsonKey]
			Expect(newPullSecretContent).ShouldNot(Equal(oldPullSecretContent), "pull secret content is equal, unexpected")

			newPushSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting push secret: %+v", err)
			newPushSecretContent = newPushSecret.Data[corev1.DockerConfigJsonKey]
			Expect(newPushSecretContent).ShouldNot(Equal(oldPushSecretContent), "push secret content is equal, unexpected")
		})
		It("try to pull the image using old pull secret, it should not work", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(oldPullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable using pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeFalse(), "image is pullable with old pull secret, which is unexpected")
		})
		It("try to pull the image using new pull secret, it should work", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(newPullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable using pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeTrue(), "image is not pullable with new pull secret, which is unexpected")
		})
		It("push an image to the registry using old secret, it should not work", func() {
			err = build.BuildMockImageAndPush(oldPushSecret, imageRepoURL)
			Expect(err).To(MatchError(ContainSubstring("UNAUTHORIZED: Could not find robot with username")))
		})
		It("push an image to the registry using new secret, it should work", func() {
			err = build.BuildMockImageAndPush(newPushSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while build and push the image: %+v", err)
		})
		It("namespace pull token rotaion is successful", func() {
			err = f.AsKubeAdmin.CommonController.RegenerateNamespacePullToken(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while regenerating namespace pull token: %+v", err)

			Eventually(func() bool {
				ir, err := f.AsKubeAdmin.CommonController.GetImageRepository(imageRepositoryName, testNamespace)
				if err != nil {
					fmt.Printf("failed to get image repository %s with error: %v", imageRepositoryName, err)
					return false
				}
				return ir.Spec.Credentials.RegenerateNamespacePullToken == nil

			}, 2*time.Minute, 5*time.Second).Should(BeTrue(), fmt.Sprintf("timed out when checking regenerate-namespace-token is nil for %s/%s", testNamespace, imageRepositoryName))

			// Fetch the namespace pull secrets content again and compare with old content
			newNamespacePullSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, constants.ComponentNamespacePullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting namespace pull secret: %+v", err)
			newNamespacePullSecretContent = newNamespacePullSecret.Data[corev1.DockerConfigJsonKey]
			Expect(newNamespacePullSecretContent).ShouldNot(Equal(oldNamespacePullSecretContent), "namespace pull secret content is equal, unexpected")
		})
		It("try to pull the image using old namespace pull secret, it should not work", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(oldNamespacePullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable using old namespace pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeFalse(), "image is pullable with old namespace pull secret, which is unexpected")
		})
		It("try to pull the image using new namespace pull secret, it should work", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(newNamespacePullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable using new namespace pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeTrue(), "image is not pullable with new namespace pull secret, which is unexpected")
		})
		It("check push secret is linked to service accounts", func() {
			secretLinked, err := f.AsKubeAdmin.CommonController.IsSecretLinkedToServiceAccount(testNamespace, serviceAccountName, pushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to check if push secret is linked to service account")
			Expect(secretLinked).To(BeTrue(), fmt.Sprintf("secret %q is not linked to service account %q", pushSecretName, serviceAccountName))
		})
		It("remove the push secret linking", func() {
			err = f.AsKubeAdmin.CommonController.UnlinkSecretFromServiceAccount(testNamespace, pushSecretName, serviceAccountName, false)
			Expect(err).ShouldNot(HaveOccurred(), "failed to remove secret linked to service account")

			secretLinked, err := f.AsKubeAdmin.CommonController.IsSecretLinkedToServiceAccount(testNamespace, serviceAccountName, pushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to check if secret is linked to service account after removing the link")
			Expect(secretLinked).To(BeFalse(), fmt.Sprintf("secret %q is linked to service account %q, which is unexpected", pushSecretName, serviceAccountName))
		})
		It("set verify linking to true and secret should be linked back again", func() {
			err = f.AsKubeAdmin.CommonController.SetVerifyLinking(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed to set verify linking to true")
			Eventually(func() (bool, error) {
				secretLinked, err := f.AsKubeAdmin.CommonController.IsSecretLinkedToServiceAccount(testNamespace, serviceAccountName, pushSecretName)
				if err != nil {
					GinkgoWriter.Printf("failed to check if push secret is linked to service account with error %v\n", err)
					return false, err
				}
				return secretLinked, nil
			}, 2*time.Minute, time.Second*5).Should(BeTrue(), fmt.Sprintf("secret %q is not linked to service account %q after verify linking", pushSecretName, serviceAccountName))

			// check verify-linking is removed from the IR spec
			ir, err := f.AsKubeAdmin.CommonController.GetImageRepository(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting image repository: %+v", err)
			Expect(ir.Spec.Credentials.VerifyLinking).Should(BeNil(), "verify-linking is not nil after the secret linking, unexpected")
		})
	})
	Describe("skip quay repository deletion", Label("skip-quay-deletion"), Ordered, func() {
		var testNamespace, firstImageRepositoryName, secondImageRepositoryName string
		var pullSecretName, pushSecretName, imageRepoName, imageRepoURL string
		var pushSecret, pullSecret *corev1.Secret

		BeforeAll(func() {
			f, err = framework.NewFramework(utils.GetGeneratedNamespace(constants.ImageControllerE2ETestNamesapcePrefix))
			Expect(err).NotTo(HaveOccurred())
			testNamespace = f.TestNamespace
			firstImageRepositoryName = "image-repository-one-" + util.GenerateRandomString(4)
			secondImageRepositoryName = "image-repository-two-" + util.GenerateRandomString(4)
		})

		AfterAll(func() {
			if !CurrentSpecReport().Failed() {
				Expect(f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(firstImageRepositoryName, testNamespace)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete imagerepository %s", firstImageRepositoryName))
				Expect(f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(secondImageRepositoryName, testNamespace)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete imagerepository %s", secondImageRepositoryName))
			}
		})

		It("image repository is created successfully", func() {
			// create a image repository with skip repository deleltion annotation set to true
			_, err = f.AsKubeAdmin.CommonController.CreateImageRepositoryCR(firstImageRepositoryName, testNamespace, "public", "", "", false, true)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", firstImageRepositoryName))

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(firstImageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", firstImageRepositoryName))
		})
		It("registry side image repo created successfully", func() {
			imageRepoName, err = f.AsKubeAdmin.CommonController.GetImageNameFromImageRepositoryCR(testNamespace, firstImageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to read image repo from image repository CR: %s", firstImageRepositoryName)
			Expect(imageRepoName).ShouldNot(BeEmpty(), "image repo name is empty")

			imageExist, err := build.DoesImageRepoExistInQuay(imageRepoName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image repo exists in quay with error: %+v", err)
			Expect(imageExist).To(BeTrue(), "quay image does not exists")
		})
		It("in cluster secrets are created successfully", func() {
			pullSecretName, pushSecretName, err = f.AsKubeAdmin.CommonController.GetSecretsFromImageRepositoryCR(testNamespace, firstImageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get secrets from image repository")
			pullSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting pull secret: %+v", err)
			pushSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting push secret: %+v", err)
		})
		It("pushing image to the registry is successful", func() {
			imageRepoURL, err = f.AsKubeAdmin.CommonController.GetImageURLFromIR(firstImageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while reading image repo URL from IR: %+v", err)
			err = build.BuildMockImageAndPush(pushSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while build and push the image: %+v", err)
		})
		It("try to pull the image using pull secrets, it should work", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(pullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable using pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeTrue(), "image is not pullable with pull secret, which is unexpected")
		})
		It("delete the first imageRepository CR, check quay side image repository is not deleted", func() {
			err = f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(firstImageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete image repository: %s", firstImageRepositoryName))

			GinkgoWriter.Printf("waiting for one minute and expecting the quay repository not to be deleted")
			Consistently(func() bool {
				imageExist, err := build.DoesImageRepoExistInQuay(imageRepoName)
				if err != nil {
					GinkgoWriter.Printf("While trying to check if quay repo exists got err: %v\n", err)
				}
				return imageExist
			}, time.Minute, time.Second*10).Should(BeTrue(), fmt.Sprintf("the quay reposiotry %s does to exists, unexpected", imageRepoName))
		})
		It("create another image reposiotry, poiting to the same quay repo", func() {
			// create IR pointing to the same imageRepoName and skip repository deleltion annotation should be set to false
			_, err = f.AsKubeAdmin.CommonController.CreateImageRepositoryCR(secondImageRepositoryName, testNamespace, "public", imageRepoName, "", false, false)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", secondImageRepositoryName))

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(secondImageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", secondImageRepositoryName))
		})
		It("in cluster secrets are created for second image repository successfully", func() {
			pullSecretName, pushSecretName, err = f.AsKubeAdmin.CommonController.GetSecretsFromImageRepositoryCR(testNamespace, secondImageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get secrets from image repository")
			pullSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting pull secret: %+v", err)
			pushSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting push secret: %+v", err)
		})
		It("pushing image to the registry again is successful", func() {
			err = build.BuildMockImageAndPush(pushSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while build and push the image: %+v", err)
		})
		It("try to pull the image using new pull secret, it should work", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(pullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable using new pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeTrue(), "image is not pullable with pull secret, which is unexpected")
		})
		It("delete the second imageRepository CR, check quay side image repository got deleted", func() {
			err = f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(secondImageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete image repository: %s", secondImageRepositoryName))

			GinkgoWriter.Printf("waiting for one minute and expecting the quay repository to be deleted")
			Eventually(func() bool {
				imageExist, err := build.DoesImageRepoExistInQuay(imageRepoName)
				if err != nil {
					GinkgoWriter.Printf("While trying to check if quay repo exists got err: %v\n", err)
					return true
				}
				return imageExist
			}, time.Minute, time.Second*10).Should(BeFalse(), fmt.Sprintf("the quay reposiotry %s still exists, unexpected", imageRepoName))
		})
	})
	Describe("two image repository pointing to the same quay repo", Label("single-quay-repo"), Ordered, func() {
		var testNamespace, firstImageRepositoryName, secondImageRepositoryName, imageRepoName, imageRepoURL string
		var firstPullSecretName, firstPushSecretName, secondPullSecretName, secondPushSecretName string
		var firstPushSecret, firstPullSecret, secondPushSecret, secondPullSecret *corev1.Secret

		BeforeAll(func() {
			f, err = framework.NewFramework(utils.GetGeneratedNamespace(constants.ImageControllerE2ETestNamesapcePrefix))
			Expect(err).NotTo(HaveOccurred())
			testNamespace = f.TestNamespace
			firstImageRepositoryName = "image-repository-one-" + util.GenerateRandomString(4)
			secondImageRepositoryName = "image-repository-two-" + util.GenerateRandomString(4)
		})

		AfterAll(func() {
			if !CurrentSpecReport().Failed() {
				Expect(f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(firstImageRepositoryName, testNamespace)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete imagerepository %s", firstImageRepositoryName))
				Expect(f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(secondImageRepositoryName, testNamespace)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete imagerepository %s", secondImageRepositoryName))
			}
		})
		It("first image repository is created successfully", func() {
			// create a image repository with skip repository deleltion annotation set to true
			_, err = f.AsKubeAdmin.CommonController.CreateImageRepositoryCR(firstImageRepositoryName, testNamespace, "private", "", "", false, true)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", firstImageRepositoryName))

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(firstImageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", firstImageRepositoryName))
		})
		It("registry side image repo created successfully", func() {
			imageRepoName, err = f.AsKubeAdmin.CommonController.GetImageNameFromImageRepositoryCR(testNamespace, firstImageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to read image repo from image repository CR: %s", firstImageRepositoryName)
			Expect(imageRepoName).ShouldNot(BeEmpty(), "image repo name is empty")

			imageExist, err := build.DoesImageRepoExistInQuay(imageRepoName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image repo exists in quay with error: %+v", err)
			Expect(imageExist).To(BeTrue(), "quay image does not exists")
		})
		It("create second image reposiotry, poiting to the same quay repo", func() {
			// create IR pointing to the same imageRepoName and skip repository deleltion annotation should be set to false
			_, err = f.AsKubeAdmin.CommonController.CreateImageRepositoryCR(secondImageRepositoryName, testNamespace, "private", imageRepoName, "", false, false)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", secondImageRepositoryName))

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(secondImageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", secondImageRepositoryName))
		})
		It("in cluster secrets for first IR are created successfully", func() {
			firstPullSecretName, firstPushSecretName, err = f.AsKubeAdmin.CommonController.GetSecretsFromImageRepositoryCR(testNamespace, firstImageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get secrets from image repository")
			firstPullSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, firstPullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting pull secret: %+v", err)
			firstPushSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, firstPushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting push secret: %+v", err)
		})
		It("pushing image using first push secret to the registry is successful", func() {
			imageRepoURL, err = f.AsKubeAdmin.CommonController.GetImageURLFromIR(firstImageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while reading image repo URL from IR: %+v", err)
			err = build.BuildMockImageAndPush(firstPushSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while build and push the image: %+v", err)
		})
		It("try to pull the image using first pull secret, it should work", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(firstPullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable using pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeTrue(), "image is not pullable with pull secret, which is unexpected")
		})
		It("in cluster secrets for second IR are created successfully", func() {
			secondPullSecretName, secondPushSecretName, err = f.AsKubeAdmin.CommonController.GetSecretsFromImageRepositoryCR(testNamespace, secondImageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get secrets from image repository")
			secondPullSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, secondPullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting pull secret: %+v", err)
			secondPushSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, secondPushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting push secret: %+v", err)
		})
		It("pushing image using second push secret to the registry is successful", func() {
			err = build.BuildMockImageAndPush(secondPushSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while build and push the image: %+v", err)
		})
		It("try to pull the image using second pull secret, it should work", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(secondPullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable using pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeTrue(), "image is not pullable with pull secret, which is unexpected")
		})
		It("delete the first imageRepository CR, check quay side image repository is not deleted", func() {
			err = f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(firstImageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete image repository: %s", firstImageRepositoryName))

			GinkgoWriter.Printf("waiting for one minute and expecting the quay repository not to be deleted")
			Consistently(func() bool {
				imageExist, err := build.DoesImageRepoExistInQuay(imageRepoName)
				if err != nil {
					GinkgoWriter.Printf("While trying to check if quay repo exists got err: %v\n", err)
				}
				return imageExist
			}, time.Minute, time.Second*10).Should(BeTrue(), fmt.Sprintf("the quay reposiotry %s does to exists, unexpected", imageRepoName))
		})
		It("try to pull the image using first pull secret, it should fail", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(firstPullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable using pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeFalse(), "image is pullable with the pull secret, which is unexpected")
		})
		It("pushing image using first push secret to the registry, it should fail", func() {
			err = build.BuildMockImageAndPush(firstPushSecret, imageRepoURL)
			Expect(err).To(MatchError(ContainSubstring("UNAUTHORIZED: Could not find robot")))
		})
		It("pushing image using second push secret again to the registry is successful", func() {
			err = build.BuildMockImageAndPush(secondPushSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while build and push the image: %+v", err)
		})
		It("try to pull the image using second pull secret again, it should work", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(secondPullSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image is pullable using pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeTrue(), "image is not pullable with pull secret, which is unexpected")
		})
		It("delete the second imageRepository CR, check quay side image repository got deleted", func() {
			err = f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(secondImageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete image repository: %s", secondImageRepositoryName))

			GinkgoWriter.Printf("waiting for one minute and expecting the quay repository to be deleted")
			Eventually(func() bool {
				imageExist, err := build.DoesImageRepoExistInQuay(imageRepoName)
				if err != nil {
					GinkgoWriter.Printf("While trying to check if quay repo exists got err: %v\n", err)
					return true
				}
				return imageExist
			}, time.Minute, time.Second*10).Should(BeFalse(), fmt.Sprintf("the quay reposiotry %s still exists, unexpected", imageRepoName))
		})
	})
	Describe("namespace wide pull secrets", Label("namespace-pull"), Ordered, func() {
		var testNamespace, firstImageRepositoryName, secondImageRepositoryName string
		var firstImageRepoURL, secondImageRepoURL, firstPushSecretName, secondPushSecretName string
		var firstPushSecret, secondPushSecret, namespacePullSecret *corev1.Secret

		BeforeAll(func() {
			f, err = framework.NewFramework(utils.GetGeneratedNamespace(constants.ImageControllerE2ETestNamesapcePrefix))
			Expect(err).NotTo(HaveOccurred())
			testNamespace = f.TestNamespace
			firstImageRepositoryName = "image-repository-one-" + util.GenerateRandomString(4)
			secondImageRepositoryName = "image-repository-two-" + util.GenerateRandomString(4)
		})

		AfterAll(func() {
			if !CurrentSpecReport().Failed() {
				Expect(f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(firstImageRepositoryName, testNamespace)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete imagerepository %s", firstImageRepositoryName))
				Expect(f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(secondImageRepositoryName, testNamespace)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete imagerepository %s", secondImageRepositoryName))
			}
		})
		It("first image repository is created successfully", func() {
			_, err = f.AsKubeAdmin.CommonController.CreateImageRepositoryCR(firstImageRepositoryName, testNamespace, "private", "", "", false, false)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", firstImageRepositoryName))

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(firstImageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", firstImageRepositoryName))
		})
		It("second image reposiotry is created successfully", func() {
			_, err = f.AsKubeAdmin.CommonController.CreateImageRepositoryCR(secondImageRepositoryName, testNamespace, "private", "", "", false, false)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", secondImageRepositoryName))

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(secondImageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", secondImageRepositoryName))
		})
		It("in cluster secrets for first IR are created successfully", func() {
			_, firstPushSecretName, err = f.AsKubeAdmin.CommonController.GetSecretsFromImageRepositoryCR(testNamespace, firstImageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get secrets from image repository")
			firstPushSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, firstPushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting push secret: %+v", err)
			// Read the namespace pull secret
			namespacePullSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, constants.ComponentNamespacePullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting namespace pull secret: %+v", err)
		})
		It("pushing image using first push secret to the registry is successful", func() {
			firstImageRepoURL, err = f.AsKubeAdmin.CommonController.GetImageURLFromIR(firstImageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while reading image repo URL from IR: %+v", err)
			err = build.BuildMockImageAndPush(firstPushSecret, firstImageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while build and push the image: %+v", err)
		})
		It("try to pull first image using namespace pull secret, it should work", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(namespacePullSecret, firstImageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if first image is pullable using namespace pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeTrue(), "image is not pullable with pull secret, which is unexpected")
		})
		It("in cluster secrets for second IR are created successfully", func() {
			_, secondPushSecretName, err = f.AsKubeAdmin.CommonController.GetSecretsFromImageRepositoryCR(testNamespace, secondImageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get secrets from image repository")
			secondPushSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, secondPushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting push secret: %+v", err)
		})
		It("pushing image using second push secret to the registry is successful", func() {
			secondImageRepoURL, err = f.AsKubeAdmin.CommonController.GetImageURLFromIR(secondImageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while reading image repo URL from IR: %+v", err)
			err = build.BuildMockImageAndPush(secondPushSecret, secondImageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while build and push the image: %+v", err)
		})
		It("try to pull second image using namespace pull secret, it should work", func() {
			isPullableWithPullSecret, err := build.IsImagePullableWithSecret(namespacePullSecret, secondImageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if second image is pullable using namespace pull secret: %+v", err)
			Expect(isPullableWithPullSecret).To(BeTrue(), "image is not pullable with pull secret, which is unexpected")
		})
	})
	Describe("image repository different status states", Label("status-state"), Ordered, func() {
		var testNamespace, imageRepositoryName, componentName, imageRepoName, imageRepoURL, pushSecretName string
		var testVersionName, targetRepoName, testRepoUrl string
		var pushSecret *corev1.Secret

		BeforeAll(func() {
			f, err = framework.NewFramework(utils.GetGeneratedNamespace(constants.ImageControllerE2ETestNamesapcePrefix))
			Expect(err).NotTo(HaveOccurred())
			testNamespace = f.TestNamespace
			imageRepositoryName = "image-repository-" + util.GenerateRandomString(4)
			componentName = "test-comp-" + util.GenerateRandomString(4)
			testVersionName = "latest"
		})
		AfterAll(func() {
			if !CurrentSpecReport().Failed() {
				Expect(f.AsKubeAdmin.CommonController.DeleteComponent(componentName, testNamespace)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete component %s", componentName))
				Expect(f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(imageRepositoryName, testNamespace)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete imagerepository %s", imageRepositoryName))
				Expect(f.AsKubeAdmin.CommonController.Github.DeleteRepositoryIfExists(targetRepoName)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete repository: %s", targetRepoName))
			}
		})
		It("create an image repository and check state is waiting", func() {
			// Create an image repository referencing a component which is yet to be created
			_, err = f.AsKubeAdmin.CommonController.CreateImageRepositoryCR(imageRepositoryName, testNamespace, "public", "", componentName, true, false)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", imageRepositoryName))

			// IR state should updated to waiting after 2 minutes
			GinkgoWriter.Println("Waiting for 2 minutes to image repository state to be changed to waiting...")
			time.Sleep(120 * time.Second)

			// Check the image repository status is waiting
			Eventually(func() bool {
				ir, err := f.AsKubeAdmin.CommonController.GetImageRepository(imageRepositoryName, testNamespace)
				if err != nil {
					GinkgoWriter.Printf("While trying to check state of image repository, got err: %v\n", err)
					return false
				}
				return ir.Status.State == "waiting"
			}, time.Minute, time.Second*10).Should(BeTrue(), "image reposiotry current state is not waiting")
		})
		It("component is created successfully", func() {
			// Fork the github repository before creating component
			targetRepoName = fmt.Sprintf("%s-%s", constants.SampleTestRepoName, util.GenerateRandomString(4))
			_, err = f.AsKubeAdmin.CommonController.Github.ForkRepository(constants.SampleTestRepoName, targetRepoName)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to fork repository: %s", targetRepoName))
			testRepoUrl = fmt.Sprintf("https://github.com/%s/%s", githubOrg, targetRepoName)

			// Create the component
			componentObject := &applicationApi.Component{
				ObjectMeta: metav1.ObjectMeta{
					Name:      componentName,
					Namespace: testNamespace,
				},
				Spec: applicationApi.ComponentSpec{
					Source: applicationApi.ComponentSource{
						GitURL: testRepoUrl,
						Versions: []applicationApi.ComponentVersion{
							{
								Name:     testVersionName,
								Revision: "main",
							},
						},
					},
					Actions: applicationApi.ComponentActions{
						CreateConfiguration: applicationApi.ComponentCreatePipelineConfiguration{
							Version: testVersionName,
						},
					},
					DefaultBuildPipeline: &applicationApi.ComponentBuildPipeline{
						PullAndPush: &applicationApi.PipelineDefinition{
							PipelineSpecFromBundle: &applicationApi.PipelineSpecFromBundle{
								Name:   string(constants.DockerBuildOciTAMin),
								Bundle: "latest",
							},
						},
					},
				},
			}
			_, err = f.AsKubeAdmin.CommonController.CreateComponent(componentObject)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create component: %s", componentName))
		})
		It("wait for image repository to be ready", func() {
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", imageRepositoryName))
		})
		It("update push secret name to a empty string and check state is changed to damaged", func() {
			err = f.AsKubeAdmin.CommonController.UpdatePushSecretName(imageRepositoryName, testNamespace, "")
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while trying to update push secret name in image repository %q", imageRepositoryName))

			// add a dummy annotation to trigger the reconcilation
			annotations := map[string]string{"dummy_key": "dummy_value"}
			err = f.AsKubeAdmin.CommonController.AddAnnotationsToIR(imageRepositoryName, testNamespace, annotations)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while trying to add a dummy annotation to the image repository %q", imageRepositoryName))

			// Check the image repository status is changed to damaged
			Eventually(func() bool {
				ir, err := f.AsKubeAdmin.CommonController.GetImageRepository(imageRepositoryName, testNamespace)
				if err != nil {
					GinkgoWriter.Printf("While trying to check state of image repository, got err: %v\n", err)
					return false
				}
				return ir.Status.State == "damaged"
			}, 1*time.Minute, time.Second*10).Should(BeTrue(), "image reposiotry current state is not damaged")
		})
		It("remove finalizer from IR, so that the IR is back to ready again", func() {
			err = f.AsKubeAdmin.CommonController.RemoveFinalizerFromIR(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while removing the finalizer from image repository: %+v", err)

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", imageRepositoryName))

			// Check that push secret is reverted
			_, pushSecretName, err = f.AsKubeAdmin.CommonController.GetSecretsFromImageRepositoryCR(testNamespace, imageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get secrets from image repository")
			Expect(pushSecretName).ShouldNot(BeEmpty(), "push secret name is empty, which is unexpected")

		})
		It("remove the repo from quay side, check IR state is changed to missing", func() {
			imageRepoName, err = f.AsKubeAdmin.CommonController.GetImageNameFromImageRepositoryCR(testNamespace, imageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while reading image repo name from IR: %+v", err)

			isDeleted, err := build.DeleteImageRepo(imageRepoName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while deleting the image repo from quay: %+v", err)
			Expect(isDeleted).To(BeTrue(), "image repo from quay is not deleted, unexpected")

			// add dummy annotation to trigger the reconcilation
			annotations := map[string]string{"some_key": "some_value"}
			err = f.AsKubeAdmin.CommonController.AddAnnotationsToIR(imageRepositoryName, testNamespace, annotations)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while trying to add a dummy annotation to the image repository %q", imageRepositoryName))

			// Check the image repository status is missing
			Eventually(func() bool {
				ir, err := f.AsKubeAdmin.CommonController.GetImageRepository(imageRepositoryName, testNamespace)
				if err != nil {
					GinkgoWriter.Printf("While trying to check state of image repository, got err: %v\n", err)
					return false
				}
				return ir.Status.State == "missing"
			}, 2*time.Minute, time.Second*10).Should(BeTrue(), "image reposiotry current state is not missing")
		})
		It("remove finalizer from IR, check the IR is back to ready again", func() {
			err = f.AsKubeAdmin.CommonController.RemoveFinalizerFromIR(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while removing the finalizer from image repository: %+v", err)

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", imageRepositoryName))
		})
		It("try to push the image to the quay repo, it should work", func() {
			pushSecret, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting push secret: %+v", err)

			imageRepoURL, err = f.AsKubeAdmin.CommonController.GetImageURLFromIR(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while reading image repo URL from IR: %+v", err)
			err = build.BuildMockImageAndPush(pushSecret, imageRepoURL)
			Expect(err).ShouldNot(HaveOccurred(), "failed while build and push the image: %+v", err)
		})
	})
	Describe("image repository notifications", Label("notification"), Ordered, func() {
		var testNamespace, imageRepositoryName, imageRepoName, firstNotificationTitle, secondNotificationTitle string

		BeforeAll(func() {
			f, err = framework.NewFramework(utils.GetGeneratedNamespace(constants.ImageControllerE2ETestNamesapcePrefix))
			Expect(err).NotTo(HaveOccurred())
			testNamespace = f.TestNamespace
			imageRepositoryName = "image-repository-" + util.GenerateRandomString(4)
		})
		AfterAll(func() {
			if !CurrentSpecReport().Failed() {
				Expect(f.AsKubeAdmin.CommonController.DeleteImageRepositoryCR(imageRepositoryName, testNamespace)).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to delete imagerepository %s", imageRepositoryName))
			}
		})
		It("image repository is created successfully", func() {
			_, err = f.AsKubeAdmin.CommonController.CreateImageRepositoryCR(imageRepositoryName, testNamespace, "public", "", "", false, false)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", imageRepositoryName))

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", imageRepositoryName))

			// Read the image repo name
			imageRepoName, err = f.AsKubeAdmin.CommonController.GetImageNameFromImageRepositoryCR(testNamespace, imageRepositoryName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while reading image repo name from IR: %+v", err)
		})
		It("add a notification and check configured correctly", func() {
			firstNotificationTitle = "First Notification"
			firstWebhookUrl := "https://webhook1.com"
			err = f.AsKubeAdmin.CommonController.AddNotifictionToIR(imageRepositoryName, testNamespace, firstNotificationTitle, firstWebhookUrl)
			Expect(err).ShouldNot(HaveOccurred(), "failed to add first notification to the image repository")

			Eventually(func() bool {
				notificationStatus, err := f.AsKubeAdmin.CommonController.GetMatchingNotificationStatus(imageRepositoryName, testNamespace, firstNotificationTitle)
				if err != nil {
					GinkgoWriter.Printf("while trying to read notification status from the image repository, got err: %v\n", err)
					return false
				}
				return notificationStatus.Title == firstNotificationTitle
			}, 2*time.Minute, time.Second*10).Should(BeTrue(), "notification title does not match in image repository")

			// Check its created on quay side
			isExists, err := build.DoesNotificationExists(imageRepoName, firstNotificationTitle)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if notification exists in quay repository")
			Expect(isExists).To(BeTrue(), "notification does not exists in quay side")
		})
		It("add a second notification and check it also configured correctly", func() {
			secondNotificationTitle = "Second Notification"
			secondWebhookUrl := "https://webhook2.com"
			err = f.AsKubeAdmin.CommonController.AddNotifictionToIR(imageRepositoryName, testNamespace, secondNotificationTitle, secondWebhookUrl)
			Expect(err).ShouldNot(HaveOccurred(), "failed to add second notification to the image repository")

			Eventually(func() bool {
				notificationStatus, err := f.AsKubeAdmin.CommonController.GetMatchingNotificationStatus(imageRepositoryName, testNamespace, secondNotificationTitle)
				if err != nil {
					GinkgoWriter.Printf("while trying to read notification status from the image repository, got err: %v\n", err)
					return false
				}
				return notificationStatus.Title == secondNotificationTitle
			}, 2*time.Minute, time.Second*10).Should(BeTrue(), "notification title does not match in image repository")

			// Check its created on quay side
			isExists, err := build.DoesNotificationExists(imageRepoName, firstNotificationTitle)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if notification exists in quay repository")
			Expect(isExists).To(BeTrue(), "notification does not exists in quay side")
		})
		It("update first notification, it should be correctly updated", func() {
			// change the webhook url
			newWebhookUrl := "https://updated.webhook.com"
			err = f.AsKubeAdmin.CommonController.UpdateWebhookUrlInNotification(imageRepositoryName, testNamespace, firstNotificationTitle, newWebhookUrl)
			Expect(err).ShouldNot(HaveOccurred(), "failed while updating webhook url in notification")

			Eventually(func() bool {
				notificationStatus, err := f.AsKubeAdmin.CommonController.GetMatchingNotificationStatus(imageRepositoryName, testNamespace, firstNotificationTitle)
				if err != nil {
					GinkgoWriter.Printf("while trying to read notification status from the image repository, got err: %v\n", err)
					return false
				}
				return notificationStatus.Title == firstNotificationTitle
			}, 2*time.Minute, time.Second*10).Should(BeTrue(), "notification title does not match in image repository")

			// Check its created on quay side
			Eventually(func() bool {
				hasCorrectWebhook, err := build.DoesNotificationHasCorrectWebhookUrl(imageRepoName, firstNotificationTitle, newWebhookUrl)
				if err != nil {
					GinkgoWriter.Printf("fwhile checking if notification has correct webhook url, got error: %v", err)
					return false
				}
				return hasCorrectWebhook
			}, 2*time.Minute, time.Second*10).Should(BeTrue(), "notification url does not match in quay")
		})
		It("delete a notification from image repository", func() {
			err = f.AsKubeAdmin.CommonController.DeleteNotificationFromIR(imageRepositoryName, testNamespace, firstNotificationTitle)
			Expect(err).ShouldNot(HaveOccurred(), "failed while deleting notification from image repository")

			// check its removed from the status
			Eventually(func() bool {
				_, err := f.AsKubeAdmin.CommonController.GetMatchingNotificationStatus(imageRepositoryName, testNamespace, firstNotificationTitle)
				if err != nil && err.Error() != "does not find any notification matching the title" {
					GinkgoWriter.Printf("while trying to read notification status from the image repository, got err: %v\n", err)
					return false
				}
				return err != nil && err.Error() == "does not find any notification matching the title"
			}, 2*time.Minute, time.Second*10).Should(BeTrue(), "notification still exists in the image repository")

			// check its removed from quay side
			isExists, err := build.DoesNotificationExists(imageRepoName, firstNotificationTitle)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if notification exists in quay repository")
			Expect(isExists).To(BeFalse(), "notification still exists in quay side, unexpected")
		})

		It("remove a notification from quay side", func() {
			err = build.DeleteNotification(imageRepoName, secondNotificationTitle)
			Expect(err).ShouldNot(HaveOccurred(), "failed to delete notification from quay")

			// check its removed from quay side
			isExists, err := build.DoesNotificationExists(imageRepoName, secondNotificationTitle)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if notification exists in quay repository")
			Expect(isExists).To(BeFalse(), "notification still exists in quay side, unexpected")

			// reconcile
			err = f.AsKubeAdmin.CommonController.RemoveFinalizerFromIR(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed while removing the finalizer from image repository: %+v", err)

			// check the notification is configured in quay side
			Eventually(func() bool {
				isExists, err = build.DoesNotificationExists(imageRepoName, secondNotificationTitle)
				if err != nil {
					GinkgoWriter.Printf("while trying to check if notification exists in quay, got err: %v\n", err)
					return false
				}
				return isExists
			}, 2*time.Minute, time.Second*10).Should(BeTrue(), "notification does not exists in the quay, unexpected")
		})

	})
})
