package e2e

import (
	"fmt"
	"time"

	"github.com/devfile/library/v2/pkg/util"
	appservice "github.com/konflux-ci/application-api/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
	. "github.com/onsi/gomega"    //nolint:staticcheck
	pipeline "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"

	"github.com/konflux-ci/e2e-tests/pkg/clients/git"
	"github.com/konflux-ci/e2e-tests/pkg/clients/has"
	"github.com/konflux-ci/e2e-tests/pkg/constants"
	"github.com/konflux-ci/e2e-tests/pkg/framework"
	"github.com/konflux-ci/e2e-tests/pkg/utils"
	"github.com/konflux-ci/e2e-tests/pkg/utils/build"
)

var _ = Describe("Image Controller E2E tests", Label("image-controller"), func() {

	var f *framework.Framework
	AfterEach(framework.ReportFailure(&f))
	var err error
	defer GinkgoRecover()

	var gitClient git.Client

	Describe("using generate annotation", Ordered, func() {
		var applicationName, customDefaultComponentName, customBranchComponentName, componentBaseBranchName string
		var testNamespace, imageRepoName, pullRobotAccountName, pushRobotAccountName string
		var imageRepositoryCRName, firstGenerateTimestamp string
		var helloWorldComponentGitSourceURL string
		var component *appservice.Component
		var plr *pipeline.PipelineRun

		var buildPipelineAnnotation map[string]string

		var helloWorldRepository string

		BeforeAll(func() {

			f, err = framework.NewFramework(utils.GetGeneratedNamespace("image-controller-e2e"))
			Expect(err).NotTo(HaveOccurred())
			testNamespace = f.UserNamespace

			if utils.IsPrivateHostname(f.OpenshiftConsoleHost) {
				Skip("Using private cluster (not reachable from Github), skipping...")
			}

			quayOrg := utils.GetEnv("DEFAULT_QUAY_ORG", "")
			supports, err := build.DoesQuayOrgSupportPrivateRepo()
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("error while checking if quay org supports private repo: %+v", err))
			if !supports {
				if quayOrg == "redhat-appstudio-qe" {
					Fail("Failed to create private image repo in redhat-appstudio-qe org")
				} else {
					Skip("Quay org does not support private quay repository creation, please add support for private repo creation before running this test")
				}
			}
			Expect(err).ShouldNot(HaveOccurred())

			applicationName = fmt.Sprintf("build-suite-test-application-%s", util.GenerateRandomString(4))
			_, err = f.AsKubeAdmin.HasController.CreateApplication(applicationName, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			customDefaultComponentName = fmt.Sprintf("gh-%s-%s", "test-custom-default", util.GenerateRandomString(6))
			customBranchComponentName = fmt.Sprintf("gh-%s-%s", "test-custom-branch", util.GenerateRandomString(6))
			componentBaseBranchName = fmt.Sprintf("base-%s", util.GenerateRandomString(6))
			imageRepositoryCRName = "imagerepository-for-" + applicationName + "-" + customDefaultComponentName

			gitClient, helloWorldComponentGitSourceURL, helloWorldRepository = setupGitProvider(f)
			buildPipelineAnnotation = build.GetBuildPipelineBundleAnnotation(constants.DockerBuildOciTAMin)

			err = gitClient.CreateBranch(helloWorldRepository, helloWorldComponentDefaultBranch, helloWorldComponentRevision, componentBaseBranchName)
			Expect(err).ShouldNot(HaveOccurred())
		})

		AfterAll(func() {
			if !CurrentSpecReport().Failed() {
				Eventually(func() error {
					return f.AsKubeAdmin.HasController.DeleteAllComponentsInASpecificNamespace(testNamespace, time.Minute*2)
				}, 2*time.Minute, 10*time.Second).Should(Succeed())
				Eventually(func() error {
					return f.AsKubeAdmin.HasController.DeleteAllApplicationsInASpecificNamespace(testNamespace, time.Minute*2)
				}, 2*time.Minute, 10*time.Second).Should(Succeed())
				Expect(gitClient.DeleteRepositoryIfExists(helloWorldRepository)).To(Succeed())
			}

		})

		When("a new component is created with visibility private", Label("private"), func() {
			var componentObj appservice.ComponentSpec

			BeforeAll(func() {
				componentObj = appservice.ComponentSpec{
					ComponentName: customDefaultComponentName,
					Application:   applicationName,
					Source: appservice.ComponentSource{
						ComponentSourceUnion: appservice.ComponentSourceUnion{
							GitSource: &appservice.GitSource{
								URL:           helloWorldComponentGitSourceURL,
								Revision:      "",
								DockerfileURL: constants.DockerFilePath,
							},
						},
					},
				}

				component, err = f.AsKubeAdmin.HasController.CreateComponentCheckImageRepository(componentObj, testNamespace, "", "", applicationName, false, utils.MergeMaps(utils.MergeMaps(constants.ComponentPaCRequestAnnotation, constants.ImageControllerAnnotationRequestPrivateRepo), buildPipelineAnnotation))
				Expect(err).ShouldNot(HaveOccurred())
			})
			AfterAll(func() {
				if !CurrentSpecReport().Failed() {
					Eventually(func() error {
						return f.AsKubeAdmin.HasController.DeleteComponent(customDefaultComponentName, testNamespace, true)
					}, 2*time.Minute, 10*time.Second).Should(Succeed())
				}
			})

			It("triggers a PipelineRun", func() {
				Eventually(func() error {
					plr, err = f.AsKubeAdmin.HasController.GetComponentPipelineRun(customDefaultComponentName, applicationName, testNamespace, "")
					if err != nil {
						GinkgoWriter.Printf("PipelineRun has not been created yet for the component %s/%s\n", testNamespace, customBranchComponentName)
						return err
					}
					if !plr.HasStarted() {
						return fmt.Errorf("pipelinerun %s/%s hasn't started yet", plr.GetNamespace(), plr.GetName())
					}
					return nil
				}, time.Minute*30, constants.PipelineRunPollingInterval).Should(Succeed(), fmt.Sprintf("timed out when waiting for the PipelineRun to start for the component %s/%s", customBranchComponentName, testNamespace))
			})

			It("build pipeline uses the correct serviceAccount", func() {
				serviceAccountName := "build-pipeline-" + customDefaultComponentName
				Expect(plr.Spec.TaskRunTemplate.ServiceAccountName).Should(Equal(serviceAccountName))
			})

			It("the PipelineRun should eventually finish successfully", func() {
				Expect(f.AsKubeAdmin.HasController.WaitForComponentPipelineToBeFinished(component, "", "", "",
					f.AsKubeAdmin.TektonController, &has.RetryOptions{Retries: 2, Always: true}, plr)).To(Succeed())
			})

			It("image repo and robot accounts created successfully", func() {
				imageRepoName, err = f.AsKubeAdmin.ImageController.GetImageName(testNamespace, customDefaultComponentName)
				Expect(err).ShouldNot(HaveOccurred(), "failed to read image repo for component %s", customDefaultComponentName)
				Expect(imageRepoName).ShouldNot(BeEmpty(), "image repo name is empty")

				imageExist, err := build.DoesImageRepoExistInQuay(imageRepoName)
				Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image repo exists in quay with error: %+v", err)
				Expect(imageExist).To(BeTrue(), "quay image does not exists")

				pullRobotAccountName, pushRobotAccountName, err = f.AsKubeAdmin.ImageController.GetRobotAccounts(testNamespace, customDefaultComponentName)
				Expect(err).ShouldNot(HaveOccurred(), "failed to get robot account names")
				pullRobotAccountExist, err := build.DoesRobotAccountExistInQuay(pullRobotAccountName)
				Expect(err).ShouldNot(HaveOccurred(), "failed while checking if pull robot account exists in quay with error: %+v", err)
				Expect(pullRobotAccountExist).To(BeTrue(), "pull robot account does not exists in quay")
				pushRobotAccountExist, err := build.DoesRobotAccountExistInQuay(pushRobotAccountName)
				Expect(err).ShouldNot(HaveOccurred(), "failed while checking if push robot account exists in quay with error: %+v", err)
				Expect(pushRobotAccountExist).To(BeTrue(), "push robot account does not exists in quay")
			})
			It("created image repo is private", func() {
				isPublic, err := build.IsImageRepoPublic(imageRepoName)
				Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while checking if the image repo %s is private", imageRepoName))
				Expect(isPublic).To(BeFalse(), "Expected image repo to be private, but it is public")
			})
			It("credential rotaion is successful", func() {
				firstGenerateTimestamp, err = f.AsKubeAdmin.ImageController.GetGenerateTimestamp(imageRepositoryCRName, testNamespace)
				Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to get generate timestamp of image repository: %q", imageRepositoryCRName))
				GinkgoWriter.Printf("Image Repository initial generateTimestamp: %s\n", firstGenerateTimestamp)

				err = f.AsKubeAdmin.ImageController.RegenerateToken(imageRepositoryCRName, testNamespace)
				Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to regenerate token for image repository: %q", imageRepositoryCRName))

				Eventually(func() error {
					currentGenerateTimestamp, err := f.AsKubeAdmin.ImageController.GetGenerateTimestamp(imageRepositoryCRName, testNamespace)
					if err != nil {
						GinkgoWriter.Printf("failed to get generate timestamp after rotation with error %v\n", err)
						return err
					}
					if currentGenerateTimestamp == firstGenerateTimestamp {
						return fmt.Errorf("Current generate timestamp %q is not equal to earlier generate timestamp %q\n", currentGenerateTimestamp, firstGenerateTimestamp)
					}
					return nil
				}, time.Second*30, time.Second*2).Should(Succeed(), fmt.Sprintf("timed out when checking generate timestamp is updated for %s/%s", testNamespace, imageRepositoryCRName))
			})
		})

		When("a new component is created and with visibility public", Label("public"), func() {
			var componentObj appservice.ComponentSpec

			BeforeAll(func() {
				componentObj = appservice.ComponentSpec{
					ComponentName: customBranchComponentName,
					Application:   applicationName,
					Source: appservice.ComponentSource{
						ComponentSourceUnion: appservice.ComponentSourceUnion{
							GitSource: &appservice.GitSource{
								URL:           helloWorldComponentGitSourceURL,
								Revision:      componentBaseBranchName,
								DockerfileURL: constants.DockerFilePath,
							},
						},
					},
				}
				component, err = f.AsKubeAdmin.HasController.CreateComponentCheckImageRepository(componentObj, testNamespace, "", "", applicationName, false, utils.MergeMaps(utils.MergeMaps(constants.ComponentPaCRequestAnnotation, constants.ImageControllerAnnotationRequestPublicRepo), buildPipelineAnnotation))
				Expect(err).ShouldNot(HaveOccurred())
			})

			It("triggers a PipelineRun", func() {
				Eventually(func() error {
					plr, err = f.AsKubeAdmin.HasController.GetComponentPipelineRun(customBranchComponentName, applicationName, testNamespace, "")
					if err != nil {
						GinkgoWriter.Printf("PipelineRun has not been created yet for the component %s/%s\n", testNamespace, customBranchComponentName)
						return err
					}
					if !plr.HasStarted() {
						return fmt.Errorf("pipelinerun %s/%s hasn't started yet", plr.GetNamespace(), plr.GetName())
					}
					return nil
				}, time.Minute*30, constants.PipelineRunPollingInterval).Should(Succeed(), fmt.Sprintf("timed out when waiting for the PipelineRun to start for the component %s/%s", testNamespace, customBranchComponentName))
			})

			It("the PipelineRun should eventually finish successfully", func() {
				Expect(f.AsKubeAdmin.HasController.WaitForComponentPipelineToBeFinished(component, "", "", "",
					f.AsKubeAdmin.TektonController, &has.RetryOptions{Retries: 2, Always: true}, plr)).To(Succeed())
			})
			It("image repo and robot accounts created successfully", func() {
				imageRepoName, err = f.AsKubeAdmin.ImageController.GetImageName(testNamespace, customBranchComponentName)
				Expect(err).ShouldNot(HaveOccurred(), "failed to read image repo for component %s", customBranchComponentName)
				Expect(imageRepoName).ShouldNot(BeEmpty(), "image repo name is empty")

				imageExist, err := build.DoesImageRepoExistInQuay(imageRepoName)
				Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image repo exists in quay with error: %+v", err)
				Expect(imageExist).To(BeTrue(), "quay image does not exists")

				pullRobotAccountName, pushRobotAccountName, err = f.AsKubeAdmin.ImageController.GetRobotAccounts(testNamespace, customBranchComponentName)
				Expect(err).ShouldNot(HaveOccurred(), "failed to get robot account names")
				pullRobotAccountExist, err := build.DoesRobotAccountExistInQuay(pullRobotAccountName)
				Expect(err).ShouldNot(HaveOccurred(), "failed while checking if pull robot account exists in quay with error: %+v", err)
				Expect(pullRobotAccountExist).To(BeTrue(), "pull robot account does not exists in quay")
				pushRobotAccountExist, err := build.DoesRobotAccountExistInQuay(pushRobotAccountName)
				Expect(err).ShouldNot(HaveOccurred(), "failed while checking if push robot account exists in quay with error: %+v", err)
				Expect(pushRobotAccountExist).To(BeTrue(), "push robot account does not exists in quay")
			})

			It("created image repo is public", func() {
				isPublic, err := build.IsImageRepoPublic(imageRepoName)
				Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while checking if the image repo %s is public", imageRepoName))
				Expect(isPublic).To(BeTrue(), fmt.Sprintf("Expected image repo '%s' to be changed to public, but it is private", imageRepoName))
			})

			It("update image repository visibility to private", func() {
				Eventually(func() error {
					_, err := f.AsKubeAdmin.ImageController.ChangeVisibilityToPrivate(testNamespace, applicationName, customBranchComponentName)
					if err != nil {
						GinkgoWriter.Printf("failed to change visibility to private with error %v\n", err)
						return err
					}
					return nil
				}, time.Second*20, time.Second*1).Should(Succeed(), fmt.Sprintf("timed out when trying to change visibility of the image repos to private in %s/%s", testNamespace, customBranchComponentName))

				GinkgoWriter.Printf("waiting for one minute and expecting it to change")
				time.Sleep(1 * time.Minute)

				isPublic, err := build.IsImageRepoPublic(imageRepoName)
				Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while checking if the image repo %s is private", imageRepoName))
				Expect(isPublic).To(BeFalse(), "Expected image repo to changed to private, but it is public")
			})

		})
	})
	Describe("general puspose image repository", Ordered, Label("without-component"), func() {
		var testNamespace, imageRepoName string
		var imageRepositoryCRName = "sample-image-repo"

		BeforeAll(func() {
			f, err = framework.NewFramework(utils.GetGeneratedNamespace("image-controller-e2e"))
			Expect(err).NotTo(HaveOccurred())
			testNamespace = f.UserNamespace
		})
		It("create an image repository", func() {
			_, err = f.AsKubeAdmin.ImageController.CreateImageRepositoryCR(imageRepositoryCRName, testNamespace, "public", "", "", "", false)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", imageRepositoryCRName))

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.ImageController.WaitForImageRepositoryToBeReady(imageRepositoryCRName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", imageRepositoryCRName))
		})
		It("image repo and robot accounts created successfully", func() {
			imageRepoName, err = f.AsKubeAdmin.ImageController.GetImageNameFromImageRepositoryCR(testNamespace, imageRepositoryCRName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to read image repo from image repository: %s", imageRepositoryCRName)
			Expect(imageRepoName).ShouldNot(BeEmpty(), "image repo name is empty")

			imageExist, err := build.DoesImageRepoExistInQuay(imageRepoName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if image repo exists in quay with error: %+v", err)
			Expect(imageExist).To(BeTrue(), "quay image does not exists")

			pullRobotAccountName, pushRobotAccountName, err := f.AsKubeAdmin.ImageController.GetRobotAccountsFromImageRepositoryCR(testNamespace, imageRepositoryCRName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get robot account names")
			pullRobotAccountExist, err := build.DoesRobotAccountExistInQuay(pullRobotAccountName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if pull robot account exists in quay with error: %+v", err)
			Expect(pullRobotAccountExist).To(BeTrue(), "pull robot account does not exists in quay")
			pushRobotAccountExist, err := build.DoesRobotAccountExistInQuay(pushRobotAccountName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while checking if push robot account exists in quay with error: %+v", err)
			Expect(pushRobotAccountExist).To(BeTrue(), "push robot account does not exists in quay")

			pullSecretName, pushSecretName, err := f.AsKubeAdmin.ImageController.GetSecretsFromImageRepositoryCR(testNamespace, imageRepositoryCRName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to get secrets from image repository")
			_, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pullSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting pull secret: %+v", err)
			_, err = f.AsKubeAdmin.CommonController.GetSecret(testNamespace, pushSecretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed while getting push secret: %+v", err)
		})
		It("image repo deletion is successful", func() {
			err = f.AsKubeAdmin.ImageController.DeleteImageRepositoryCR(imageRepositoryCRName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed to delete image repository")

			Eventually(func() (bool, error) {
				imageExist, err := build.DoesImageRepoExistInQuay(imageRepoName)
				if err != nil {
					GinkgoWriter.Printf("failed to check if image exists in quay with error %v\n", err)
					return false, err
				}
				return !imageExist, nil
			}, time.Second*60, time.Second*2).Should(BeTrue(), fmt.Sprintf("even after deleting image repository, quay repo %q still exists", imageRepoName))

		})
	})

	Describe("verify secret linking", Ordered, Label("secret-linking"), func() {
		var componentObj appservice.ComponentSpec
		var buildPipelineAnnotation map[string]string
		var helloWorldComponentGitSourceURL, helloWorldRepository, testNamespace string

		var imageRepositoryCRName = "image-repository" + "-" + util.GenerateRandomString(4)
		var userDefinedImageName = "image-name" + "-" + util.GenerateRandomString(4)
		var applicationName = fmt.Sprintf("build-suite-test-application-%s", util.GenerateRandomString(4))
		var componentName = fmt.Sprintf("%s-%s", "test-secret-linking", util.GenerateRandomString(6))
		var componentBaseBranchName = fmt.Sprintf("base-%s", util.GenerateRandomString(6))
		var serviceAccountName = "build-pipeline-" + componentName
		var secretName = "components-namespace-pull"

		BeforeAll(func() {
			f, err = framework.NewFramework(utils.GetGeneratedNamespace("image-controller-e2e"))
			Expect(err).NotTo(HaveOccurred())
			testNamespace = f.UserNamespace

			_, err = f.AsKubeAdmin.HasController.CreateApplication(applicationName, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			gitClient, helloWorldComponentGitSourceURL, helloWorldRepository = setupGitProvider(f)
			err = gitClient.CreateBranch(helloWorldRepository, helloWorldComponentDefaultBranch, helloWorldComponentRevision, componentBaseBranchName)
			Expect(err).ShouldNot(HaveOccurred())

			buildPipelineAnnotation = build.GetBuildPipelineBundleAnnotation(constants.DockerBuildOciTAMin)
		})
		AfterAll(func() {
			if !CurrentSpecReport().Failed() {
				Eventually(func() error {
					return f.AsKubeAdmin.HasController.DeleteAllComponentsInASpecificNamespace(testNamespace, time.Minute*2)
				}, 2*time.Minute, 10*time.Second).Should(Succeed())
				Eventually(func() error {
					return f.AsKubeAdmin.HasController.DeleteAllApplicationsInASpecificNamespace(testNamespace, time.Minute*2)
				}, 2*time.Minute, 10*time.Second).Should(Succeed())
				Expect(gitClient.DeleteRepositoryIfExists(helloWorldRepository)).To(Succeed())
			}
		})
		It("create an image repository with user defined image name", func() {
			_, err = f.AsKubeAdmin.ImageController.CreateImageRepositoryCR(imageRepositoryCRName, testNamespace, "public", userDefinedImageName, applicationName, componentName, true)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", imageRepositoryCRName))
		})
		It("create a component linking the image repository", func() {
			componentObj = appservice.ComponentSpec{
				ComponentName: componentName,
				Application:   applicationName,
				Source: appservice.ComponentSource{
					ComponentSourceUnion: appservice.ComponentSourceUnion{
						GitSource: &appservice.GitSource{
							URL:           helloWorldComponentGitSourceURL,
							Revision:      componentBaseBranchName,
							DockerfileURL: constants.DockerFilePath,
						},
					},
				},
			}
			_, err = f.AsKubeAdmin.HasController.CreateComponentCheckImageRepository(componentObj, testNamespace, "", "", applicationName, false, utils.MergeMaps(constants.ComponentPaCRequestAnnotation, buildPipelineAnnotation))
			Expect(err).ShouldNot(HaveOccurred())
		})
		It("check secret is linked to service accounts", func() {
			secretLinked, err := f.AsKubeAdmin.CommonController.IsSecretLinkedToServiceAccount(serviceAccountName, testNamespace, secretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to check if secret is linked to service account")
			Expect(secretLinked).To(BeTrue(), fmt.Sprintf("secret %q is not linked to service account %q", secretName, serviceAccountName))
		})
		It("remove the secret linking", func() {
			err = f.AsKubeAdmin.CommonController.UnlinkSecretFromServiceAccount(testNamespace, secretName, serviceAccountName, false)
			Expect(err).ShouldNot(HaveOccurred(), "failed to remove secret linked to service account")

			secretLinked, err := f.AsKubeAdmin.CommonController.IsSecretLinkedToServiceAccount(serviceAccountName, testNamespace, secretName)
			Expect(err).ShouldNot(HaveOccurred(), "failed to check if secret is linked to service account after removing the link")
			Expect(secretLinked).To(BeFalse(), fmt.Sprintf("secret %q is linked to service account %q, which is unexpected", secretName, serviceAccountName))
		})
		It("set verify linking to true in image repo", func() {
			err = f.AsKubeAdmin.ImageController.VerifyLinking(imageRepositoryCRName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), "failed to set verify linking to true")

			Eventually(func() (bool, error) {
				secretLinked, err := f.AsKubeAdmin.CommonController.IsSecretLinkedToServiceAccount(serviceAccountName, testNamespace, secretName)
				if err != nil {
					GinkgoWriter.Printf("failed to check if secret is linked to service account with error %v\n", err)
					return false, err
				}
				return secretLinked, nil
			}, time.Second*60, time.Second*2).Should(BeTrue(), fmt.Sprintf("secret %q is not linked to service account %q after verify linking", secretName, serviceAccountName))
		})
	})
})
