package e2e

import (
	"fmt"
	"time"

	"github.com/devfile/library/v2/pkg/util"
	applicationApi "github.com/konflux-ci/application-api/api/konflux/v1alpha1"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/constants"
	"github.com/konflux-ci/build-service/e2e-tests/pkg/framework"
	"github.com/konflux-ci/e2e-tests/pkg/utils"
	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
	. "github.com/onsi/gomega"    //nolint:staticcheck
	pipeline "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = framework.ImageControllerSuiteDescribe("Image Controller E2E tests", Label("image-controller-e2e"), func() {

	var f *framework.Framework
	var err error
	defer GinkgoRecover()
	Describe("image repository and component connection", Label("smoke"), Ordered, func() {
		var testNamespace, imageRepositoryName, componentName string
		var testVersionName string
		var targetRepoName, testRepoUrl string
		var component *applicationApi.Component
		var plr *pipeline.PipelineRun

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
			component, err = f.AsKubeAdmin.CommonController.CreateComponent(componentObject)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create component: %s", componentName))
		})
		It("image repository is created successfully", func() {
			_, err = f.AsKubeAdmin.CommonController.CreateImageRepositoryCR(imageRepositoryName, testNamespace, "public", "", componentName, true, false)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to create image repository cr: %q", imageRepositoryName))

			// Wait for image repository to be ready
			err = f.AsKubeAdmin.CommonController.WaitForImageRepositoryToBeReady(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while waiting for image repository %q to be ready", imageRepositoryName))
		})
		It("check component version onboarding status", func() {
			// Wait for component test version status to succeed
			err = f.AsKubeAdmin.CommonController.WaitForComponentVersionOnboardingToSucceed(componentName, testNamespace, testVersionName)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed while checking component %q version status to succeeded", componentName))
		})
		It("containerImage is correctly set in component", func() {
			componentObj, err := f.AsKubeAdmin.CommonController.GetComponent(componentName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to get component %s with error: %v", componentName, err))
			containerImageInComponent := componentObj.Spec.ContainerImage
			imageRepoURLInIR, err := f.AsKubeAdmin.CommonController.GetImageURLFromIR(imageRepositoryName, testNamespace)
			Expect(err).ShouldNot(HaveOccurred(), fmt.Sprintf("failed to read image url from IR %s with error: %v", imageRepositoryName, err))
			Expect(imageRepoURLInIR).To(Equal(containerImageInComponent), "containerImage in component and image repository does not match, which is unexpected")
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
		It("the PipelineRun should eventually finish successfully", func() {
			Expect(f.AsKubeAdmin.CommonController.WaitForComponentPipelineToBeFinished(component, "build", "pull_request", "")).To(Succeed())
		})
	})
})
