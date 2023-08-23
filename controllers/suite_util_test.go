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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	appstudioapiv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	remotesecretv1beta1 "github.com/redhat-appstudio/remote-secret/api/v1beta1"
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
	ComponentKey         types.NamespacedName
	ComponentApplication string
	Annotations          map[string]string
}

func getSampleComponentData(config componentConfig) *appstudioapiv1alpha1.Component {
	name := config.ComponentKey.Name
	if name == "" {
		name = defaultComponentName
	}
	namespace := config.ComponentKey.Namespace
	if namespace == "" {
		namespace = defaultComponentNamespace
	}
	application := config.ComponentApplication
	if application == "" {
		application = defaultComponentApplication
	}
	annotations := make(map[string]string)
	if config.Annotations != nil {
		annotations = config.Annotations
	}

	return &appstudioapiv1alpha1.Component{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "appstudio.redhat.com/v1alpha1",
			Kind:       "Component",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: annotations,
		},
		Spec: appstudioapiv1alpha1.ComponentSpec{
			ComponentName: name,
			Application:   application,
		},
	}
}

// createComponent creates sample component resource and verifies it was properly created.
// Sets devfile model, so the component can be processed right after creation.
func createComponent(config componentConfig) *appstudioapiv1alpha1.Component {
	component := getSampleComponentData(config)

	Expect(k8sClient.Create(ctx, component)).Should(Succeed())
	setComponentDevfileModel(config.ComponentKey)

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
	if err := k8sClient.Get(ctx, componentKey, component); k8sErrors.IsNotFound(err) {
		return
	}

	// Delete
	Expect(k8sClient.Delete(ctx, component)).To(Succeed())

	// Wait for delete to finish
	Eventually(func() bool {
		return k8sErrors.IsNotFound(k8sClient.Get(ctx, componentKey, component))
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

// waitComponentAnnotationWithValue waits for a component have had an annotation with a specific value.
func waitComponentAnnotationWithValue(componentKey types.NamespacedName, annotationName, value string) {
	Eventually(func() bool {
		component := getComponent(componentKey)
		annotations := component.GetAnnotations()
		if annotations == nil {
			return false
		}
		val, exists := annotations[annotationName]
		if exists {
			return val == value
		} else {
			return false
		}
	}, timeout, interval).Should(BeTrue())
}

func ensureComponentAnnotationUnchangedWithValue(componentKey types.NamespacedName, annotationName, value string) {
	Consistently(func() bool {
		component := getComponent(componentKey)
		annotations := component.GetAnnotations()
		if annotations == nil {
			return false
		}
		val, exists := annotations[annotationName]
		if exists {
			return val == value
		} else {
			return false
		}
	}, timeout, interval).Should(BeTrue())
}

func waitComponentAnnotationGone(componentKey types.NamespacedName, annotationName string) {
	Eventually(func() bool {
		component := getComponent(componentKey)
		annotations := component.GetAnnotations()
		if annotations == nil {
			return true
		}
		_, exists := annotations[annotationName]
		return !exists
	}, timeout, interval).Should(BeTrue())
}

func waitFinalizerOnComponent(componentKey types.NamespacedName, finalizerName string, finalizerShouldBePresent bool) {
	component := &appstudioapiv1alpha1.Component{}
	Eventually(func() bool {
		if err := k8sClient.Get(ctx, componentKey, component); err != nil {
			return false
		}

		if finalizerShouldBePresent {
			return controllerutil.ContainsFinalizer(component, finalizerName)
		} else {
			return !controllerutil.ContainsFinalizer(component, finalizerName)
		}
	}, timeout, interval).Should(BeTrue())
}

func waitImageRepositoryFinalizerOnComponent(componentKey types.NamespacedName) {
	waitFinalizerOnComponent(componentKey, ImageRepositoryFinalizer, true)
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

	if err := k8sClient.Create(ctx, &namespace); err != nil && !k8sErrors.IsAlreadyExists(err) {
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

	if err := k8sClient.Delete(ctx, &namespace); err != nil && !k8sErrors.IsNotFound(err) {
		Fail(err.Error())
	}
}

func waitSecretExist(secretKey types.NamespacedName) *corev1.Secret {
	secret := &corev1.Secret{}
	Eventually(func() bool {
		err := k8sClient.Get(ctx, secretKey, secret)
		return err == nil && secret.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
	return secret
}

func deleteSecret(resourceKey types.NamespacedName) {
	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, resourceKey, secret); err != nil {
		if k8sErrors.IsNotFound(err) {
			return
		}
		Fail(err.Error())
	}
	if err := k8sClient.Delete(ctx, secret); err != nil {
		if !k8sErrors.IsNotFound(err) {
			Fail(err.Error())
		}
		return
	}
	Eventually(func() bool {
		return k8sErrors.IsNotFound(k8sClient.Get(ctx, resourceKey, secret))
	}, timeout, interval).Should(BeTrue())
}

func waitRemoteSecretExist(remoteSecretKey types.NamespacedName) *remotesecretv1beta1.RemoteSecret {
	remoteSecret := &remotesecretv1beta1.RemoteSecret{}
	Eventually(func() bool {
		err := k8sClient.Get(ctx, remoteSecretKey, remoteSecret)
		return err == nil && remoteSecret.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
	return remoteSecret
}
