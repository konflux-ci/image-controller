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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	appstudioapiv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	appstudiospiapiv1beta1 "github.com/redhat-appstudio/service-provider-integration-operator/api/v1beta1"
)

const (
	// timeout is used as a limit until condition become true
	// Usually used in Eventually statements
	timeout = time.Second * 15
	// ensureTimeout is used as a period of time during which the condition should not be changed
	// Usually used in Consistently statements
	ensureTimeout = time.Second * 4
	interval      = time.Millisecond * 250
)

const (
	defaultComponentName        = "test-component"
	defaultComponentNamespace   = "test-namespace"
	defaultComponentApplication = "test-application"
)

type componentConfig struct {
	ComponentName        string
	ComponentNamespace   string
	ComponentApplication string
}

func getSampleComponentData(config componentConfig) *appstudioapiv1alpha1.Component {
	name := config.ComponentName
	if name == "" {
		name = defaultComponentName
	}
	namespace := config.ComponentNamespace
	if namespace == "" {
		namespace = defaultComponentNamespace
	}
	application := config.ComponentApplication
	if application == "" {
		application = defaultComponentApplication
	}

	return &appstudioapiv1alpha1.Component{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "appstudio.redhat.com/v1alpha1",
			Kind:       "Component",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: map[string]string{},
		},
		Spec: appstudioapiv1alpha1.ComponentSpec{
			ComponentName: name,
			Application:   application,
		},
	}
}

// createComponent creates sample component resource and verifies it was properly created
// if generateValue is not an empty string adds `image.redhat.com/generate` annotation with given value.
func createComponent(config componentConfig, generateValue string) *appstudioapiv1alpha1.Component {
	component := getSampleComponentData(config)
	if generateValue != "" {
		component.Annotations[GenerateImageAnnotationName] = generateValue
	}

	Expect(k8sClient.Create(ctx, component)).Should(Succeed())

	componentKey := types.NamespacedName{Namespace: component.Namespace, Name: component.Name}
	return getComponent(componentKey)
}

func getComponent(componentKey types.NamespacedName) *appstudioapiv1alpha1.Component {
	component := &appstudioapiv1alpha1.Component{}
	Eventually(func() bool {
		Expect(k8sClient.Get(ctx, componentKey, component)).Should(Succeed())
		return component.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
	return component
}

// deleteComponent deletes the specified component resource and verifies it was properly deleted
func deleteComponent(componentKey types.NamespacedName) {
	component := &appstudioapiv1alpha1.Component{}

	// Check if the component exists
	if err := k8sClient.Get(ctx, componentKey, component); errors.IsNotFound(err) {
		return
	}

	// Delete
	Expect(k8sClient.Delete(ctx, component)).To(Succeed())

	// Wait for delete to finish
	Eventually(func() bool {
		return errors.IsNotFound(k8sClient.Get(ctx, componentKey, component))
	}, timeout, interval).Should(BeTrue())
}

func setComponentDevfile(componentKey types.NamespacedName, devfile string) {
	component := &appstudioapiv1alpha1.Component{}
	Eventually(func() error {
		Expect(k8sClient.Get(ctx, componentKey, component)).To(Succeed())
		component.Status.Devfile = devfile
		return k8sClient.Status().Update(ctx, component)
	}, timeout, interval).Should(Succeed())

	component = getComponent(componentKey)
	Expect(component.Status.Devfile).Should(Not(Equal("")))
}

func getMinimalDevfile() string {
	return `        
        schemaVersion: 2.2.0
        metadata:
            name: minimal-devfile
    `
}

func setComponentDevfileModel(componentKey types.NamespacedName) {
	devfile := getMinimalDevfile()
	setComponentDevfile(componentKey, devfile)
}

func setComponentAnnotationValue(componentKey types.NamespacedName, annotationName string, annotationValue string) {
	component := getComponent(componentKey)
	if component.Annotations == nil {
		component.Annotations = make(map[string]string)
	}
	component.Annotations[annotationName] = annotationValue
	Expect(k8sClient.Update(ctx, component)).To(Succeed())
	waitComponentAnnotationValue(componentKey, annotationName, annotationValue)
}

func waitComponentAnnotation(componentKey types.NamespacedName, annotationName string) {
	Eventually(func() bool {
		component := getComponent(componentKey)
		annotations := component.GetAnnotations()
		if annotations == nil {
			return false
		}
		_, exists := annotations[annotationName]
		return exists
	}, timeout, interval).Should(BeTrue())
}

func waitComponentAnnotationValue(componentKey types.NamespacedName, annotationName string, annotationValue string) {
	Eventually(func() bool {
		component := getComponent(componentKey)
		annotations := component.GetAnnotations()
		return annotations != nil && annotations[annotationName] == annotationValue
	}, timeout, interval).Should(BeTrue())
}

func createNamespace(name string) {
	namespace := corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	if err := k8sClient.Create(ctx, &namespace); err != nil && !errors.IsAlreadyExists(err) {
		Fail(err.Error())
	}
}

func deleteNamespace(name string) {
	namespace := corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	if err := k8sClient.Delete(ctx, &namespace); err != nil && !errors.IsNotFound(err) {
		Fail(err.Error())
	}
}

func waitSecretCreated(secretKey types.NamespacedName) *corev1.Secret {
	secret := &corev1.Secret{}
	Eventually(func() bool {
		err := k8sClient.Get(ctx, secretKey, secret)
		return err == nil && secret.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
	return secret
}

func createSPIAccessToken(resourceKey types.NamespacedName) {
	spiAccesToken := appstudiospiapiv1beta1.SPIAccessToken{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceKey.Name,
			Namespace: resourceKey.Namespace,
		},
	}
	Expect(k8sClient.Create(ctx, &spiAccesToken)).To(Succeed())
}

func waitSPIAccessToken(resourceKey types.NamespacedName) *appstudiospiapiv1beta1.SPIAccessToken {
	spiAccesToken := &appstudiospiapiv1beta1.SPIAccessToken{}
	Eventually(func() bool {
		Expect(k8sClient.Get(ctx, resourceKey, spiAccesToken)).Should(Succeed())
		return spiAccesToken.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
	return spiAccesToken
}

func makeSPIAccessTokenReady(resourceKey types.NamespacedName) {
	spiAccesToken := waitSPIAccessToken(resourceKey)
	spiAccesToken.Status.Phase = appstudiospiapiv1beta1.SPIAccessTokenPhaseReady
	Expect(k8sClient.Status().Update(ctx, spiAccesToken)).To(Succeed())
}

func waitSPIAccessTokenBinding(resourceKey types.NamespacedName) *appstudiospiapiv1beta1.SPIAccessTokenBinding {
	spiAccesTokenBinding := &appstudiospiapiv1beta1.SPIAccessTokenBinding{}
	Eventually(func() bool {
		Expect(k8sClient.Get(ctx, resourceKey, spiAccesTokenBinding)).Should(Succeed())
		return spiAccesTokenBinding.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
	return spiAccesTokenBinding
}

func makeSPIAccessTokenBindingReady(resourceKey types.NamespacedName) {
	spiAccesTokenBinding := waitSPIAccessTokenBinding(resourceKey)
	spiAccesTokenBinding.Status.Phase = appstudiospiapiv1beta1.SPIAccessTokenBindingPhaseInjected
	spiAccesTokenBinding.Status.SyncedObjectRef = appstudiospiapiv1beta1.TargetObjectRef{
		Name: spiAccesTokenBinding.Name + "-secret-cdt73",
		Kind: "Secret",
	}
	Expect(k8sClient.Status().Update(ctx, spiAccesTokenBinding)).To(Succeed())
}
