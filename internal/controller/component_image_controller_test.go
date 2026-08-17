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

// remove after component v2 migration - entire file
// Test file for component_image_controller.go which won't be used with component v2.

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
	var (
		imageTestNamespace = "component-image-controller-test"
	)

	BeforeEach(func() {
		createNamespace(imageTestNamespace)
		createNamespace(kubeSystemNamespace)
	})

	Context("Image repository provision flow", func() {
		var resourceImageProvisionKey = types.NamespacedName{Name: defaultComponentName + "-imageprovision", Namespace: imageTestNamespace}
		var imageRepositoryName = types.NamespacedName{
			Name:      fmt.Sprintf("imagerepository-for-%s-%s", defaultComponentApplication, resourceImageProvisionKey.Name),
			Namespace: resourceImageProvisionKey.Namespace,
		}
		var imageRepositoryWithoutApplicationName = types.NamespacedName{
			Name:      fmt.Sprintf("imagerepository-for-%s", resourceImageProvisionKey.Name),
			Namespace: resourceImageProvisionKey.Namespace,
		}
		var applicationKey = types.NamespacedName{Name: defaultComponentApplication, Namespace: imageTestNamespace}
		var componentSaName = getComponentSaName(resourceImageProvisionKey.Name)

		BeforeEach(func() {
			quay.ResetTestQuayClient()
			createApplicationOldModel(applicationConfig{ApplicationKey: applicationKey})
		})

		AfterEach(func() {
			deleteApplication(applicationKey)
			deleteComponentOldModel(resourceImageProvisionKey)
		})

		It("should prepare environment", func() {
			createServiceAccount(imageTestNamespace, componentSaName)
		})

		It("should do image repository provision", func() {
			expectedVisibility := imagerepositoryv1alpha1.ImageVisibility("private")
			createComponentOldModel(componentConfig{
				ComponentKey:         resourceImageProvisionKey,
				ComponentApplication: defaultComponentApplication,
				Annotations: map[string]string{
					GenerateImageAnnotationName: "{\"visibility\": \"private\"}",
				},
			})
			// wait for component_image_controller to finish
			waitComponentAnnotationGone(resourceImageProvisionKey, GenerateImageAnnotationName)

			imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
			Expect(k8sClient.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: resourceImageProvisionKey.Namespace})).To(Succeed())
			Expect(imageRepositoriesList.Items).To(HaveLen(1))

			component := getComponentOldModel(resourceImageProvisionKey)
			// wait for imagerepository_controller to finish
			waitImageRepositoryFinalizerOnImageRepositoryOldModel(imageRepositoryName)
			imageRepository := getImageRepositoryOldModel(imageRepositoryName)

			Expect(imageRepository.ObjectMeta.Labels[ApplicationNameLabelName]).To(Equal(component.Spec.Application))
			Expect(imageRepository.ObjectMeta.Labels[ComponentNameLabelNameOldModel]).To(Equal(component.Name))
			Expect(imageRepository.Spec.Image.Visibility).To(Equal(expectedVisibility))
			Expect(imageRepository.ObjectMeta.OwnerReferences[0].UID).To(Equal(component.UID))
			Expect(imageRepository.ObjectMeta.Annotations[updateComponentAnnotationNameOldModel]).To(BeEmpty())

			component = getComponentOldModel(resourceImageProvisionKey)
			Expect(component.Annotations[ImageAnnotationName]).To(BeEmpty())
			Expect(component.Spec.ContainerImage).ToNot(BeEmpty())

			deleteImageRepositoryOldModel(imageRepositoryName)
		})

		It("should do image repository provision when component doesn't have application", func() {
			expectedVisibility := imagerepositoryv1alpha1.ImageVisibility("private")
			createComponentOldModel(componentConfig{
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

			component := getComponentOldModel(resourceImageProvisionKey)
			// wait for imagerepository_controller to finish
			waitImageRepositoryFinalizerOnImageRepositoryOldModel(imageRepositoryWithoutApplicationName)
			imageRepository := getImageRepositoryOldModel(imageRepositoryWithoutApplicationName)

			_, applicationLabelExists := imageRepository.ObjectMeta.Labels[ApplicationNameLabelName]
			Expect(applicationLabelExists).To(BeFalse())
			Expect(imageRepository.ObjectMeta.Labels[ComponentNameLabelNameOldModel]).To(Equal(component.Name))
			Expect(imageRepository.Spec.Image.Visibility).To(Equal(expectedVisibility))
			Expect(imageRepository.ObjectMeta.OwnerReferences[0].UID).To(Equal(component.UID))
			Expect(imageRepository.ObjectMeta.Annotations[updateComponentAnnotationNameOldModel]).To(BeEmpty())

			component = getComponentOldModel(resourceImageProvisionKey)
			Expect(component.Annotations[ImageAnnotationName]).To(BeEmpty())
			Expect(component.Spec.ContainerImage).ToNot(BeEmpty())

			deleteImageRepositoryOldModel(imageRepositoryWithoutApplicationName)
		})

		It("should accept deprecated true value for repository options", func() {
			expectedVisibility := imagerepositoryv1alpha1.ImageVisibility("public")
			createComponentOldModel(componentConfig{
				ComponentKey:         resourceImageProvisionKey,
				ComponentApplication: defaultComponentApplication,
				Annotations: map[string]string{
					GenerateImageAnnotationName: "true",
				},
			})

			// wait for component_image_controller to finish
			waitComponentAnnotationGone(resourceImageProvisionKey, GenerateImageAnnotationName)

			imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
			Expect(k8sClient.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: resourceImageProvisionKey.Namespace})).To(Succeed())
			Expect(imageRepositoriesList.Items).To(HaveLen(1))

			component := getComponentOldModel(resourceImageProvisionKey)
			// wait for imagerepository_controller to finish
			waitImageRepositoryFinalizerOnImageRepositoryOldModel(imageRepositoryName)
			imageRepository := getImageRepositoryOldModel(imageRepositoryName)

			Expect(imageRepository.ObjectMeta.Labels[ApplicationNameLabelName]).To(Equal(component.Spec.Application))
			Expect(imageRepository.ObjectMeta.Labels[ComponentNameLabelNameOldModel]).To(Equal(component.Name))
			Expect(imageRepository.Spec.Image.Visibility).To(Equal(expectedVisibility))
			Expect(imageRepository.ObjectMeta.OwnerReferences[0].UID).To(Equal(component.UID))
			Expect(imageRepository.ObjectMeta.Annotations[updateComponentAnnotationNameOldModel]).To(BeEmpty())

			component = getComponentOldModel(resourceImageProvisionKey)
			Expect(component.Annotations[ImageAnnotationName]).To(BeEmpty())
			Expect(component.Spec.ContainerImage).ToNot(BeEmpty())

			deleteImageRepositoryOldModel(imageRepositoryName)
			deleteServiceAccount(types.NamespacedName{Name: componentSaName, Namespace: imageTestNamespace})
		})
	})

	Context("Image repository provision error cases", func() {
		var resourceImageErrorKey = types.NamespacedName{Name: defaultComponentName + "-imageerrors", Namespace: imageTestNamespace}
		var applicationKey = types.NamespacedName{Name: defaultComponentApplication, Namespace: imageTestNamespace}
		var componentSaName = getComponentSaName(resourceImageErrorKey.Name)

		It("should prepare environment", func() {
			deleteComponentOldModel(resourceImageErrorKey)
			quay.ResetTestQuayClient()
			createApplicationOldModel(applicationConfig{ApplicationKey: applicationKey})

			createServiceAccount(imageTestNamespace, componentSaName)
		})

		It("should do nothing if generate annotation is not set", func() {
			createComponentOldModel(componentConfig{ComponentKey: resourceImageErrorKey, ComponentApplication: defaultComponentApplication})

			time.Sleep(ensureTimeout)
			waitComponentAnnotationGone(resourceImageErrorKey, GenerateImageAnnotationName)
			waitComponentAnnotationGone(resourceImageErrorKey, ImageAnnotationName)

			imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
			Expect(k8sClient.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: resourceImageErrorKey.Namespace})).To(Succeed())
			Expect(imageRepositoriesList.Items).To(BeEmpty())
		})

		It("should do nothing if imageRepository for the component already exists, with expected name", func() {
			component := getComponentOldModel(resourceImageErrorKey)
			imageRepositoryName := fmt.Sprintf("imagerepository-for-%s-%s", component.Spec.Application, component.Name)
			imageRepositoryKey := types.NamespacedName{Name: imageRepositoryName, Namespace: component.Namespace}

			createImageRepositoryOldModel(imageRepositoryConfigOldModel{ResourceKey: &imageRepositoryKey})
			// wait for imagerepository_controller to finish
			waitImageRepositoryFinalizerOnImageRepositoryOldModel(imageRepositoryKey)
			// add generate annotation and it will not create new ImageRepository
			setComponentAnnotationValue(resourceImageErrorKey, GenerateImageAnnotationName, `{"visibility": "public"}`)
			waitComponentAnnotationGone(resourceImageErrorKey, GenerateImageAnnotationName)

			component = getComponentOldModel(resourceImageErrorKey)
			Expect(component.Annotations[ImageAnnotationName]).To(BeEmpty())
			// just to double check that new ImageRepository wasn't created, which would add ContainerImage
			Expect(component.Spec.ContainerImage).To(BeEmpty())

			imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
			Expect(k8sClient.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: resourceImageErrorKey.Namespace})).To(Succeed())
			Expect(imageRepositoriesList.Items).To(HaveLen(1))

			imageRepository := imageRepositoriesList.Items[0]
			Expect(imageRepository.OwnerReferences).ToNot(BeEmpty())
			Expect(imageRepository.OwnerReferences).To(ContainElement(metav1.OwnerReference{
				Name:       component.Name,
				Kind:       "Component",
				UID:        component.UID,
				APIVersion: "appstudio.redhat.com/v1alpha1",
			}))

			deleteImageRepositoryOldModel(imageRepositoryKey)
		})

		It("should do nothing if imageRepository for the component already exists, with different name", func() {
			component := getComponentOldModel(resourceImageErrorKey)
			imageRepositoryName := fmt.Sprintf("differently-named-%s-%s", component.Spec.Application, component.Name)
			imageRepository := types.NamespacedName{Name: imageRepositoryName, Namespace: component.Namespace}
			ownerReferences := []metav1.OwnerReference{
				{Kind: "Component", Name: component.Name, UID: component.UID, APIVersion: "appstudio.redhat.com/v1alpha1"},
			}

			createImageRepositoryOldModel(imageRepositoryConfigOldModel{
				ResourceKey:     &imageRepository,
				OwnerReferences: ownerReferences,
			})
			// wait for imagerepository_controller to finish
			waitImageRepositoryFinalizerOnImageRepositoryOldModel(imageRepository)
			// add generate annotation and it will not create new ImageRepository
			setComponentAnnotationValue(resourceImageErrorKey, GenerateImageAnnotationName, `{"visibility": "public"}`)
			waitComponentAnnotationGone(resourceImageErrorKey, GenerateImageAnnotationName)

			component = getComponentOldModel(resourceImageErrorKey)
			Expect(component.Annotations[ImageAnnotationName]).To(BeEmpty())
			// just to double check that new ImageRepository wasn't created, which would add ContainerImage
			Expect(component.Spec.ContainerImage).To(BeEmpty())

			imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
			Expect(k8sClient.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: resourceImageErrorKey.Namespace})).To(Succeed())
			Expect(imageRepositoriesList.Items).To(HaveLen(1))

			deleteImageRepositoryOldModel(imageRepository)
		})

		It("should do nothing and set error if generate annotation is invalid JSON", func() {
			setComponentAnnotationValue(resourceImageErrorKey, GenerateImageAnnotationName, `{"visibility": "public"`)

			waitComponentAnnotationGone(resourceImageErrorKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceImageErrorKey, ImageAnnotationName)

			repoImageInfo := &ImageRepositoryStatus{}
			component := getComponentOldModel(resourceImageErrorKey)
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(ContainSubstring("invalid JSON"))

			imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
			Expect(k8sClient.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: resourceImageErrorKey.Namespace})).To(Succeed())
			Expect(imageRepositoriesList.Items).To(BeEmpty())
		})

		It("should do nothing and set error if generate annotation has invalid visibility value", func() {
			setComponentAnnotationValue(resourceImageErrorKey, GenerateImageAnnotationName, `{"visibility": "none"}`)

			waitComponentAnnotationGone(resourceImageErrorKey, GenerateImageAnnotationName)
			waitComponentAnnotation(resourceImageErrorKey, ImageAnnotationName)

			repoImageInfo := &ImageRepositoryStatus{}
			component := getComponentOldModel(resourceImageErrorKey)
			Expect(json.Unmarshal([]byte(component.Annotations[ImageAnnotationName]), repoImageInfo)).To(Succeed())
			Expect(repoImageInfo.Message).To(ContainSubstring("invalid value: none in visibility field"))

			imageRepositoriesList := &imagerepositoryv1alpha1.ImageRepositoryList{}
			Expect(k8sClient.List(ctx, imageRepositoriesList, &client.ListOptions{Namespace: resourceImageErrorKey.Namespace})).To(Succeed())
			Expect(imageRepositoriesList.Items).To(BeEmpty())
		})

		It("should clean environment", func() {
			deleteComponentOldModel(resourceImageErrorKey)
			deleteApplication(applicationKey)
			deleteServiceAccount(types.NamespacedName{Name: componentSaName, Namespace: imageTestNamespace})
		})
	})
})
