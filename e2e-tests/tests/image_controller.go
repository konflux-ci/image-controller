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

	Describe("PaC component build", Ordered, func() {
		var applicationName, customDefaultComponentName, customBranchComponentName, componentBaseBranchName string
		var testNamespace, imageRepoName, pullRobotAccountName, pushRobotAccountName string
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

		When("a new component is created with visibility private", Label("pac-custom-default-branch"), func() {
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

		})

		When("a new Component with specified custom branch is created and with visibility public", Label("build-custom-branch"), func() {
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
})
