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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	imagerepositoryv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	appstudioapiv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
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
	defaultNamespace = "test-namespace"

	defaultImageRepositoryName = "image-repository"

	defaultComponentName        = "test-component"
	defaultComponentApplication = "test-application"
)

type imageRepositoryConfig struct {
	ResourceKey     *types.NamespacedName
	ImageName       string
	Visibility      string
	Labels          map[string]string
	Annotations     map[string]string
	Notifications   []imagerepositoryv1alpha1.Notifications
	OwnerReferences []metav1.OwnerReference
	Finalizers      []string
}

func getImageRepositoryConfig(config imageRepositoryConfig) *imagerepositoryv1alpha1.ImageRepository {
	name := defaultImageRepositoryName
	namespace := defaultNamespace
	if config.ResourceKey != nil {
		name = config.ResourceKey.Name
		namespace = config.ResourceKey.Namespace
	}
	visibility := ""
	if config.Visibility == "private" {
		visibility = "private"
	} else if config.Visibility == "public" {
		visibility = "public"
	}
	annotations := make(map[string]string)
	if config.Annotations != nil {
		annotations = config.Annotations
	}
	finalizers := []string{}
	if config.Finalizers != nil {
		finalizers = config.Finalizers
	}

	return &imagerepositoryv1alpha1.ImageRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			Labels:          config.Labels,
			Annotations:     annotations,
			OwnerReferences: config.OwnerReferences,
			Finalizers:      finalizers,
		},
		Spec: imagerepositoryv1alpha1.ImageRepositorySpec{
			Image: imagerepositoryv1alpha1.ImageParameters{
				Name:       config.ImageName,
				Visibility: imagerepositoryv1alpha1.ImageVisibility(visibility),
			},
			Notifications: config.Notifications,
		},
	}
}

func createImageRepository(config imageRepositoryConfig) *imagerepositoryv1alpha1.ImageRepository {
	imageRepository := getImageRepositoryConfig(config)
	Expect(k8sClient.Create(ctx, imageRepository)).To(Succeed())

	imageRepositoryKey := types.NamespacedName{Namespace: imageRepository.Namespace, Name: imageRepository.Name}
	return getImageRepository(imageRepositoryKey)
}

func getImageRepository(imageRepositoryKey types.NamespacedName) *imagerepositoryv1alpha1.ImageRepository {
	imageRepository := &imagerepositoryv1alpha1.ImageRepository{}
	Eventually(func() bool {
		Expect(k8sClient.Get(ctx, imageRepositoryKey, imageRepository)).Should(Succeed())
		return imageRepository.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
	return imageRepository
}

func deleteImageRepository(imageRepositoryKey types.NamespacedName) {
	imageRepository := &imagerepositoryv1alpha1.ImageRepository{}
	if err := k8sClient.Get(ctx, imageRepositoryKey, imageRepository); err != nil {
		if k8sErrors.IsNotFound(err) {
			return
		}
		Fail("Failed to get image repository")
	}
	Expect(k8sClient.Delete(ctx, imageRepository)).To(Succeed())
	waitImageRepositoryGone(imageRepositoryKey)
}

func waitImageRepositoryGone(resourceKey types.NamespacedName) {
	imageRepository := &imagerepositoryv1alpha1.ImageRepository{}
	Eventually(func() bool {
		return k8sErrors.IsNotFound(k8sClient.Get(ctx, resourceKey, imageRepository))
	}, timeout, interval).Should(BeTrue())
}

type applicationConfig struct {
	ApplicationKey types.NamespacedName
}

func getSampleApplicationData(config applicationConfig) *appstudioredhatcomv1alpha1.Application {
	name := config.ApplicationKey.Name
	if name == "" {
		name = defaultComponentApplication
	}
	namespace := config.ApplicationKey.Namespace
	if namespace == "" {
		namespace = defaultNamespace
	}

	return &appstudioredhatcomv1alpha1.Application{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "appstudio.redhat.com/v1alpha1",
			Kind:       "Application",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

// createApplication creates sample application resource and verifies it was properly created.
func createApplication(config applicationConfig) *appstudioredhatcomv1alpha1.Application {
	application := getSampleApplicationData(config)

	Expect(k8sClient.Create(ctx, application)).Should(Succeed())

	applicationKey := types.NamespacedName{Namespace: application.Namespace, Name: application.Name}
	return getApplication(applicationKey)
}

func getApplication(applicationKey types.NamespacedName) *appstudioredhatcomv1alpha1.Application {
	application := &appstudioredhatcomv1alpha1.Application{}
	Eventually(func() bool {
		Expect(k8sClient.Get(ctx, applicationKey, application)).Should(Succeed())
		return application.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
	return application
}

// deleteApplication deletes the specified application resource and verifies it was properly deleted
func deleteApplication(applicationKey types.NamespacedName) {
	application := &appstudioredhatcomv1alpha1.Application{}

	// Check if the applicaiton exists
	if err := k8sClient.Get(ctx, applicationKey, application); k8sErrors.IsNotFound(err) {
		return
	}

	// Delete
	Expect(k8sClient.Delete(ctx, application)).To(Succeed())

	// Wait for delete to finish
	Eventually(func() bool {
		return k8sErrors.IsNotFound(k8sClient.Get(ctx, applicationKey, application))
	}, timeout, interval).Should(BeTrue())
}

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
		namespace = defaultNamespace
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
func createComponent(config componentConfig) *appstudioapiv1alpha1.Component {
	component := getSampleComponentData(config)

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

func waitImageRepositoryFinalizerOnImageRepository(imageRepositoryKey types.NamespacedName) {
	imageRepository := &imagerepositoryv1alpha1.ImageRepository{}
	Eventually(func() bool {
		if err := k8sClient.Get(ctx, imageRepositoryKey, imageRepository); err != nil {
			return false
		}
		return controllerutil.ContainsFinalizer(imageRepository, ImageRepositoryFinalizer)
	}, timeout, interval).Should(BeTrue())
}

// waitImageRepositoryCredentialSectionRequestGone waits until Spec.Credentials section is gone
func waitImageRepositoryCredentialSectionRequestGone(imageRepositoryKey types.NamespacedName, operationName string) {
	Eventually(func() bool {
		imageRepository := getImageRepository(imageRepositoryKey)
		switch operationName {
		case "regenerate":
			if imageRepository.Spec.Credentials.RegenerateToken == nil {
				return true
			}
			return false
		case "verify":
			if imageRepository.Spec.Credentials.VerifyLinking == nil {
				return true
			}
			return false
		default:
			return true
		}
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

func createServiceAccount(namespace, name string) corev1.ServiceAccount {
	serviceAccount := corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
	Expect(k8sClient.Create(ctx, &serviceAccount)).To(Succeed())
	return getServiceAccount(namespace, name)
}

func createServiceAccountWithSecrets(namespace, name string, pushSecrets, imagePullSecrets []string) corev1.ServiceAccount {
	secretsReferences := []corev1.ObjectReference{}
	imagePullSecretsReferences := []corev1.LocalObjectReference{}
	for _, secretName := range pushSecrets {
		secretsReferences = append(secretsReferences, corev1.ObjectReference{Name: secretName})
	}
	for _, secretName := range imagePullSecrets {
		imagePullSecretsReferences = append(imagePullSecretsReferences, corev1.LocalObjectReference{Name: secretName})
	}

	serviceAccount := corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Secrets:          secretsReferences,
		ImagePullSecrets: imagePullSecretsReferences,
	}
	Expect(k8sClient.Create(ctx, &serviceAccount)).To(Succeed())
	return getServiceAccount(namespace, name)
}

func getServiceAccount(namespace string, name string) corev1.ServiceAccount {
	sa := corev1.ServiceAccount{}
	key := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
	Eventually(func() bool {
		Expect(k8sClient.Get(ctx, key, &sa)).To(Succeed())
		return sa.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())
	return sa
}

func getServiceAccountList(namespace string) []corev1.ServiceAccount {
	saList := &corev1.ServiceAccountList{}
	Expect(k8sClient.List(ctx, saList, &client.ListOptions{Namespace: namespace})).To(Succeed())

	return saList.Items
}

func deleteServiceAccount(serviceAccountKey types.NamespacedName) {
	serviceAccount := &corev1.ServiceAccount{}
	if err := k8sClient.Get(ctx, serviceAccountKey, serviceAccount); err != nil {
		if k8sErrors.IsNotFound(err) {
			return
		}
		Fail("Failed to get service account")
	}
	Expect(k8sClient.Delete(ctx, serviceAccount)).To(Succeed())
	Eventually(func() bool {
		return k8sErrors.IsNotFound(k8sClient.Get(ctx, serviceAccountKey, serviceAccount))
	}, timeout, interval).Should(BeTrue())
}

func createUsersConfigMap(configMapKey types.NamespacedName, users []string) {
	configMapData := map[string]string{}
	configMapData[additionalUsersConfigMapKey] = strings.Join(users, " ")

	usersConfigMap := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: configMapKey.Name, Namespace: configMapKey.Namespace},
		Data:       configMapData,
	}

	if err := k8sClient.Create(ctx, &usersConfigMap); err != nil && !k8sErrors.IsAlreadyExists(err) {
		Fail(err.Error())
	}
}

func deleteUsersConfigMap(configMapKey types.NamespacedName) {
	usersConfigMap := corev1.ConfigMap{}
	if err := k8sClient.Get(ctx, configMapKey, &usersConfigMap); err != nil {
		if k8sErrors.IsNotFound(err) {
			return
		}
		Fail(err.Error())
	}
	if err := k8sClient.Delete(ctx, &usersConfigMap); err != nil && !k8sErrors.IsNotFound(err) {
		Fail(err.Error())
	}
	Eventually(func() bool {
		return k8sErrors.IsNotFound(k8sClient.Get(ctx, configMapKey, &usersConfigMap))
	}, timeout, interval).Should(BeTrue())
}

func addUsersToUsersConfigMap(configMapKey types.NamespacedName, addUsers []string) {
	usersConfigMap := corev1.ConfigMap{}
	Eventually(func() bool {
		Expect(k8sClient.Get(ctx, configMapKey, &usersConfigMap)).Should(Succeed())
		return usersConfigMap.ResourceVersion != ""
	}, timeout, interval).Should(BeTrue())

	currentUsers, usersExist := usersConfigMap.Data[additionalUsersConfigMapKey]
	if !usersExist {
		Fail("users config map is missing key")
	}

	newUsers := strings.Join(addUsers, " ")
	allUsers := fmt.Sprintf("%s %s", currentUsers, newUsers)
	usersConfigMap.Data[additionalUsersConfigMapKey] = allUsers

	Expect(k8sClient.Update(ctx, &usersConfigMap)).Should(Succeed())
}

func waitQuayTeamUsersFinalizerOnConfigMap(usersConfigMapKey types.NamespacedName) {
	usersConfigMap := &corev1.ConfigMap{}
	Eventually(func() bool {
		if err := k8sClient.Get(ctx, usersConfigMapKey, usersConfigMap); err != nil {
			return false
		}
		return controllerutil.ContainsFinalizer(usersConfigMap, ConfigMapFinalizer)
	}, timeout, interval).Should(BeTrue())
}

func verifySecretAuth(secretDockerconfigJson, expectedImage, robotAccountName, token string) {
	var authDataJson dockerConfigJson
	Expect(json.Unmarshal([]byte(secretDockerconfigJson), &authDataJson)).To(Succeed())
	Expect(authDataJson.Auths).To(HaveKey(expectedImage))
	secretAuthString, err := base64.StdEncoding.DecodeString(authDataJson.Auths[expectedImage].Auth)
	Expect(err).To(Succeed())
	Expect(string(secretAuthString)).To(Equal(fmt.Sprintf("%s:%s", robotAccountName, token)))
}

func verifySecretAuthEmpty(secretDockerconfigJson string) {
	var authDataJson dockerConfigJson
	Expect(json.Unmarshal([]byte(secretDockerconfigJson), &authDataJson)).To(Succeed())
	Expect(authDataJson.Auths).To(HaveLen(0))
}

func verifySecretSpec(secret *corev1.Secret, ownerKind, ownerName, secretName string) {
	Expect(secret.OwnerReferences).To(HaveLen(1))
	Expect(secret.OwnerReferences[0].Kind).To(Equal(ownerKind))
	Expect(secret.OwnerReferences[0].Name).To(Equal(ownerName))
	Expect(secret.Labels[InternalSecretLabelName]).To(Equal("true"))
	Expect(secret.Name).To(Equal(secretName))
	Expect(secret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
}

// createDockerConfigSecret creates secret, either SecretTypeDockerConfigJson or SecretTypeBasicAuth
func createDockerConfigSecret(secretKey types.NamespacedName, dockerConfigData string, dockerCofingJsonSecret bool) {
	var secret *corev1.Secret
	if dockerCofingJsonSecret {
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretKey.Name,
				Namespace: secretKey.Namespace,
			},
			Type: corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{
				corev1.DockerConfigJsonKey: []byte(dockerConfigData),
			},
		}
	} else {
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretKey.Name,
				Namespace: secretKey.Namespace,
			},
			Type: corev1.SecretTypeBasicAuth,
			Data: map[string][]byte{
				corev1.BasicAuthUsernameKey: []byte("user"),
				corev1.BasicAuthPasswordKey: []byte("password"),
			},
		}

	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
}

// generateDockerConfigJson creates the raw JSON string for .dockerconfigjson
func generateDockerConfigJson(registry, username, password string) string {
	authString := fmt.Sprintf("%s:%s", username, password)
	encodedAuth := base64.StdEncoding.EncodeToString([]byte(authString))

	auths := map[string]dockerConfigAuth{}
	auths[registry] = dockerConfigAuth{encodedAuth}

	dcj := dockerConfigJson{Auths: auths}
	marshaled, err := json.Marshal(dcj)
	Expect(err).To(Succeed())
	return string(marshaled)
}
