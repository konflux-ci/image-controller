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
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	imagerepositoryv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	"github.com/konflux-ci/image-controller/pkg/quay"
)

var _ = Describe("Component image controller", func() {
	var imageTestNamespace = "component-image-controller-test"

	BeforeEach(func() {
		createNamespace(imageTestNamespace)
	})

	Context("Image repository provision flow", func() {
		var resourceImageProvisionKey = types.NamespacedName{Name: defaultComponentName + "-imageprovision", Namespace: imageTestNamespace}
		var imageRepositoryName = types.NamespacedName{
			Name:      fmt.Sprintf("imagerepository-for-%s-%s", defaultComponentApplication, resourceImageProvisionKey.Name),
			Namespace: resourceImageProvisionKey.Namespace,
		}
		var applicationKey = types.NamespacedName{Name: defaultComponentApplication, Namespace: imageTestNamespace}
		var componentSaName = getComponentSaName(resourceImageProvisionKey.Name)

		BeforeEach(func() {
			quay.ResetTestQuayClient()
			createApplication(applicationConfig{ApplicationKey: applicationKey})
		})

		AfterEach(func() {
			deleteApplication(applicationKey)
			deleteComponent(resourceImageProvisionKey)
		})

		It("should prepare environment", func() {
			createServiceAccount(imageTestNamespace, buildPipelineServiceAccountName)
			createServiceAccount(imageTestNamespace, componentSaName)
			createServiceAccount(imageTestNamespace, NamespaceServiceAccountName)

			// wait for application SA to be created
			Eventually(func() bool {
				saList := getServiceAccountList(imageTestNamespace)
				// there will be 3 service accounts
				// appstudio-pipeline SA, component's SA and application's SA
				return len(saList) == 3
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

		})

		It("should do image repository provision", func() {
			expectedVisibility := imagerepositoryv1alpha1.ImageVisibility("private")
			createComponent(componentConfig{
				ComponentKey: resourceImageProvisionKey,
				Annotations: map[string]string{
					GenerateImageAnnotationName: "{\"visibility\": \"private\"}",
				},
			})
			// wait for component_image_controller to finish
			waitComponentAnnotationGone(resourceImageProvisionKey, GenerateImageAnnotationName)

			imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
			Expect(k8sClient.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: resourceImageProvisionKey.Namespace})).To(Succeed())
			Expect(imageRepositoriesList.Items).To(HaveLen(1))

			component := getComponent(resourceImageProvisionKey)
			// wait for imagerepository_controller to finish
			waitImageRepositoryFinalizerOnImageRepository(imageRepositoryName)
			imageRepository := getImageRepository(imageRepositoryName)

			Expect(imageRepository.ObjectMeta.Labels[ApplicationNameLabelName]).To(Equal(component.Spec.Application))
			Expect(imageRepository.ObjectMeta.Labels[ComponentNameLabelName]).To(Equal(component.Name))
			Expect(imageRepository.Spec.Image.Visibility).To(Equal(expectedVisibility))
			Expect(imageRepository.ObjectMeta.OwnerReferences[0].UID).To(Equal(component.UID))
			Expect(imageRepository.ObjectMeta.Annotations[updateComponentAnnotationName]).To(BeEmpty())

			component = getComponent(resourceImageProvisionKey)
			Expect(component.Annotations[ImageAnnotationName]).To(BeEmpty())
			Expect(component.Spec.ContainerImage).ToNot(BeEmpty())

			deleteImageRepository(imageRepositoryName)
		})

		It("should accept deprecated true value for repository options", func() {
			expectedVisibility := imagerepositoryv1alpha1.ImageVisibility("public")
			createComponent(componentConfig{
				ComponentKey: resourceImageProvisionKey,
				Annotations: map[string]string{
					GenerateImageAnnotationName: "true",
				},
			})

			// wait for component_image_controller to finish
			waitComponentAnnotationGone(resourceImageProvisionKey, GenerateImageAnnotationName)

			imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
			Expect(k8sClient.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: resourceImageProvisionKey.Namespace})).To(Succeed())
			Expect(imageRepositoriesList.Items).To(HaveLen(1))

			component := getComponent(resourceImageProvisionKey)
			// wait for imagerepository_controller to finish
			waitImageRepositoryFinalizerOnImageRepository(imageRepositoryName)
			imageRepository := getImageRepository(imageRepositoryName)

			Expect(imageRepository.ObjectMeta.Labels[ApplicationNameLabelName]).To(Equal(component.Spec.Application))
			Expect(imageRepository.ObjectMeta.Labels[ComponentNameLabelName]).To(Equal(component.Name))
			Expect(imageRepository.Spec.Image.Visibility).To(Equal(expectedVisibility))
			Expect(imageRepository.ObjectMeta.OwnerReferences[0].UID).To(Equal(component.UID))
			Expect(imageRepository.ObjectMeta.Annotations[updateComponentAnnotationName]).To(BeEmpty())

			component = getComponent(resourceImageProvisionKey)
			Expect(component.Annotations[ImageAnnotationName]).To(BeEmpty())
			Expect(component.Spec.ContainerImage).ToNot(BeEmpty())

			deleteImageRepository(imageRepositoryName)
			deleteServiceAccount(types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: imageTestNamespace})
			deleteServiceAccount(types.NamespacedName{Name: componentSaName, Namespace: imageTestNamespace})
			deleteServiceAccount(types.NamespacedName{Name: NamespaceServiceAccountName, Namespace: imageTestNamespace})
		})
	})

	Context("Image repository provision error cases", func() {
		var resourceImageErrorKey = types.NamespacedName{Name: defaultComponentName + "-imageerrors", Namespace: imageTestNamespace}
		var applicationKey = types.NamespacedName{Name: defaultComponentApplication, Namespace: imageTestNamespace}
		var componentSaName = getComponentSaName(resourceImageErrorKey.Name)

		It("should prepare environment", func() {
			deleteComponent(resourceImageErrorKey)
			quay.ResetTestQuayClient()
			createApplication(applicationConfig{ApplicationKey: applicationKey})

			createServiceAccount(imageTestNamespace, buildPipelineServiceAccountName)
			createServiceAccount(imageTestNamespace, componentSaName)
			createServiceAccount(imageTestNamespace, NamespaceServiceAccountName)

			// wait for application SA to be created
			Eventually(func() bool {
				saList := getServiceAccountList(imageTestNamespace)
				return len(saList) == 3
			}, timeout, interval).WithTimeout(ensureTimeout).Should(BeTrue())

		})

		It("should do nothing if generate annotation is not set", func() {
			createComponent(componentConfig{ComponentKey: resourceImageErrorKey})

			time.Sleep(ensureTimeout)
			waitComponentAnnotationGone(resourceImageErrorKey, GenerateImageAnnotationName)
			waitComponentAnnotationGone(resourceImageErrorKey, ImageAnnotationName)

			imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
			Expect(k8sClient.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: resourceImageErrorKey.Namespace})).To(Succeed())
			Expect(imageRepositoriesList.Items).To(HaveLen(0))
		})

		It("should do nothing if imageRepository for the component already exists, with expected name", func() {
			component := getComponent(resourceImageErrorKey)
			imageRepositoryName := fmt.Sprintf("imagerepository-for-%s-%s", component.Spec.Application, component.Name)
			imageRepository := types.NamespacedName{Name: imageRepositoryName, Namespace: component.Namespace}
			ownerReferences := []metav1.OwnerReference{
				{Kind: "Component", Name: component.Name, UID: types.UID(component.UID), APIVersion: "appstudio.redhat.com/v1alpha1"},
			}

			createImageRepository(imageRepositoryConfig{
				ResourceKey:     &imageRepository,
				OwnerReferences: ownerReferences,
			})
			// wait for imagerepository_controller to finish
			waitImageRepositoryFinalizerOnImageRepository(imageRepository)
			// add generate annotation and it will not create new ImageRepository
			setComponentAnnotationValue(resourceImageErrorKey, GenerateImageAnnotationName, `{"visibility": "public"}`)
			waitComponentAnnotationGone(resourceImageErrorKey, GenerateImageAnnotationName)

			component = getComponent(resourceImageErrorKey)
			Expect(component.Annotations[ImageAnnotationName]).To(BeEmpty())
			// just to double check that new ImageRepository wasn't created, which would add ContainerImage
			Expect(component.Spec.ContainerImage).To(BeEmpty())

			imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
			Expect(k8sClient.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: resourceImageErrorKey.Namespace})).To(Succeed())
			Expect(imageRepositoriesList.Items).To(HaveLen(1))

			deleteImageRepository(imageRepository)
		})

		It("should do nothing if imageRepository for the component already exists, with different name", func() {
			component := getComponent(resourceImageErrorKey)
			imageRepositoryName := fmt.Sprintf("differently-named-%s-%s", component.Spec.Application, component.Name)
			imageRepository := types.NamespacedName{Name: imageRepositoryName, Namespace: component.Namespace}
			ownerReferences := []metav1.OwnerReference{
				{Kind: "Component", Name: component.Name, UID: types.UID(component.UID), APIVersion: "appstudio.redhat.com/v1alpha1"},
			}

			createImageRepository(imageRepositoryConfig{
				ResourceKey:     &imageRepository,
				OwnerReferences: ownerReferences,
			})
			// wait for imagerepository_controller to finish
			waitImageRepositoryFinalizerOnImageRepository(imageRepository)
			// add generate annotation and it will not create new ImageRepository
			setComponentAnnotationValue(resourceImageErrorKey, GenerateImageAnnotationName, `{"visibility": "public"}`)
			waitComponentAnnotationGone(resourceImageErrorKey, GenerateImageAnnotationName)

			component = getComponent(resourceImageErrorKey)
			Expect(component.Annotations[ImageAnnotationName]).To(BeEmpty())
			// just to double check that new ImageRepository wasn't created, which would add ContainerImage
			Expect(component.Spec.ContainerImage).To(BeEmpty())

			imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
			Expect(k8sClient.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: resourceImageErrorKey.Namespace})).To(Succeed())
			Expect(imageRepositoriesList.Items).To(HaveLen(1))

			deleteImageRepository(imageRepository)
		})

		It("should do nothing and set error if generate annotation is invalid JSON", func() {
			setComponentAnnotationValue(resourceImageErrorKey, GenerateImageAnnotationName, `{"visibility": "public"`)

			waitComponentAnnotationGone(resourceImageErrorKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceImageErrorKey, ImageAnnotationName)

			repoImageInfo := &ImageRepositoryStatus{}
			component := getComponent(resourceImageErrorKey)
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(ContainSubstring("invalid JSON"))

			imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
			Expect(k8sClient.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: resourceImageErrorKey.Namespace})).To(Succeed())
			Expect(imageRepositoriesList.Items).To(HaveLen(0))
		})

		It("should do nothing and set error if generate annotation has invalid visibility value", func() {
			setComponentAnnotationValue(resourceImageErrorKey, GenerateImageAnnotationName, `{"visibility": "none"}`)

			waitComponentAnnotationGone(resourceImageErrorKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceImageErrorKey, ImageAnnotationName)

			repoImageInfo := &ImageRepositoryStatus{}
			component := getComponent(resourceImageErrorKey)
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(ContainSubstring("invalid value: none in visibility field"))

			imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
			Expect(k8sClient.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: resourceImageErrorKey.Namespace})).To(Succeed())
			Expect(imageRepositoriesList.Items).To(HaveLen(0))
		})

		It("should clean environment", func() {
			deleteComponent(resourceImageErrorKey)
			deleteApplication(applicationKey)
			deleteServiceAccount(types.NamespacedName{Name: buildPipelineServiceAccountName, Namespace: imageTestNamespace})
			deleteServiceAccount(types.NamespacedName{Name: componentSaName, Namespace: imageTestNamespace})
			deleteServiceAccount(types.NamespacedName{Name: NamespaceServiceAccountName, Namespace: imageTestNamespace})
		})
	})
})
