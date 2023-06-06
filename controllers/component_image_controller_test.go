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
package controllers_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/h2non/gock"
	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/image-controller/controllers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Component image controller", func() {

	Context("Upon creation of Component", func() {
		It("Should be annotated and Secret should be created", func() {
			appComponent := &appstudioredhatcomv1alpha1.Component{
				TypeMeta: v1.TypeMeta{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
				},
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "default",
					Annotations: map[string]string{
						"foo":                                   "bar",
						controllers.GenerateImageAnnotationName: "true",
						controllers.DeleteImageRepositoryAnnotationName: "true",
					},
				},
				Spec: appstudioredhatcomv1alpha1.ComponentSpec{
					ComponentName: "foo",
					Application:   "bar",
					Source: appstudioredhatcomv1alpha1.ComponentSource{
						ComponentSourceUnion: appstudioredhatcomv1alpha1.ComponentSourceUnion{
							GitSource: &appstudioredhatcomv1alpha1.GitSource{
								URL: "github.com/foo/bar",
							},
						},
					},
				},
			}

			secretLookupKey := types.NamespacedName{Name: appComponent.Name, Namespace: appComponent.Namespace}

			defer gock.Off()
			defer gock.Observe(gock.DumpRequest)
			gock.InterceptClient(httpClient)

			// response from Repository API
			quayOrganization := "redhat-user-workloads"
			expectedRepoName := fmt.Sprintf("%s/%s/%s", appComponent.Namespace, appComponent.Spec.Application, appComponent.Name)

			// request & response from Robot API
			userProvidedRobotAccountName := appComponent.Namespace + appComponent.Spec.Application + appComponent.Name
			returnedRobotAccountName := quayOrganization + "+" + userProvidedRobotAccountName
			expectedToken := "token"

			gock.New("https://quay.io").
				Post("/api/v1/repository").
				Reply(200).JSON(map[string]string{
				"description": "description",
				"namespace":   quayOrganization,
				"name":        expectedRepoName,
			})

			gock.New("https://quay.io").
				Put(fmt.Sprintf("/api/v1/organization/%s/robots/%s", quayOrganization, userProvidedRobotAccountName)).
				Reply(200).JSON(map[string]string{
				"name":  returnedRobotAccountName,
				"token": expectedToken,
			})

			gock.New("https://quay.io").
				Put(fmt.Sprintf("/api/v1/repository/redhat-user-workloads/default/bar/foo/permissions/user/%s", returnedRobotAccountName)).
				Reply(200).JSON(map[string]string{})

			Expect(k8sClient.Create(ctx, appComponent)).Should(BeNil())
			hasCompLookupKey := types.NamespacedName{Name: appComponent.Name, Namespace: appComponent.Namespace}
			createdHasComp := &appstudioredhatcomv1alpha1.Component{}

			Eventually(func() bool {
				Expect(k8sClient.Get(context.Background(), hasCompLookupKey, createdHasComp)).Should(Succeed())
				return createdHasComp.ResourceVersion != ""
			}).Should(BeTrue())

			createdHasComp.Status.Devfile = "devfile"
			Expect(k8sClient.Status().Update(context.Background(), createdHasComp)).Should(BeNil())
			Eventually(func() bool {
				Expect(k8sClient.Get(context.Background(), hasCompLookupKey, createdHasComp)).Should(Succeed())
				return createdHasComp.Status.Devfile == "devfile"
			}).Should(BeTrue())

			Eventually(func() controllers.RepositoryInfo {
				Expect(k8sClient.Get(context.Background(), hasCompLookupKey, createdHasComp)).Should(Succeed())
				annotations := createdHasComp.Annotations
				imageRepo := annotations[controllers.ImageAnnotationName]

				if imageRepo == "" {
					// The controller may not finish the work to set the annotation yet.
					// Return empty object for continuing wait
					return controllers.RepositoryInfo{}
				}

				imageRepoObj := controllers.RepositoryInfo{}
				Expect(json.Unmarshal([]byte(imageRepo), &imageRepoObj)).Should(Succeed())

				return imageRepoObj
			}).Should(Equal(controllers.RepositoryInfo{
				Image:  "quay.io/redhat-user-workloads/default/bar/foo",
				Secret: "foo",
			}))

			Eventually(func() string {
				Expect(k8sClient.Get(context.Background(), hasCompLookupKey, createdHasComp)).Should(BeNil())
				annotations := createdHasComp.Annotations
				return annotations["image.redhat.com/generate"]
			}).Should(Equal("false"))

			Eventually(func() string {
				createdSecret := corev1.Secret{}
				Expect(k8sClient.Get(context.Background(), secretLookupKey, &createdSecret)).Should(BeNil())
				return string(createdSecret.Data[corev1.DockerConfigJsonKey][:])
			}).Should(Equal(fmt.Sprintf(`{"auths":{"%s":{"auth":"%s"}}}`,
				"quay.io/redhat-user-workloads/default/bar/foo",
				base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", returnedRobotAccountName, expectedToken))))))

			// Now that everything is verified, we shall try to regenerate the images.

			gock.Clean()

			// API Call to the Repository API should return a message that the Repository already exists
			gock.New("https://quay.io").
				Post("/api/v1/repository").
				Reply(400).JSON(map[string]string{
				"error_message": "Repository already exists",
			})

			// API Call to the Robot account API should return a message that the Robot account already exists
			gock.New("https://quay.io").
				Put(fmt.Sprintf("/api/v1/organization/%s/robots/%s", quayOrganization, userProvidedRobotAccountName)).
				Reply(400).JSON(map[string]string{
				"message": "Existing robot with name",
			})
			// Should return existing Robot account
			gock.New("https://quay.io").
				Get(fmt.Sprintf("/api/v1/organization/%s/robots/%s", quayOrganization, userProvidedRobotAccountName)).
				Reply(200).JSON(map[string]string{
				"name":  returnedRobotAccountName,
				"token": expectedToken,
			})

			// API Call to the Permissions API remains business as usual.
			gock.New("https://quay.io").
				Put(fmt.Sprintf("/api/v1/repository/redhat-user-workloads/default/bar/foo/permissions/user/%s", returnedRobotAccountName)).
				Reply(200).JSON(map[string]string{})

			// trigger reconcile again.
			createdHasComp.Annotations["image.redhat.com/generate"] = "true"
			Expect(k8sClient.Update(ctx, createdHasComp)).Should(BeNil())

			Eventually(func() controllers.RepositoryInfo {
				Expect(k8sClient.Get(context.Background(), hasCompLookupKey, createdHasComp)).Should(Succeed())
				annotations := createdHasComp.Annotations
				imageRepo := annotations[controllers.ImageAnnotationName]

				imageRepoObj := controllers.RepositoryInfo{}
				Expect(json.Unmarshal([]byte(imageRepo), &imageRepoObj)).Should(Succeed())

				return imageRepoObj
			}).Should(Equal(controllers.RepositoryInfo{
				Image:  "quay.io/redhat-user-workloads/default/bar/foo",
				Secret: "foo",
			}))

			// Check robot account deletion on Component deletion

			gock.New("https://quay.io").
				Delete(fmt.Sprintf("/api/v1/organization/%s/robots/%s", quayOrganization, userProvidedRobotAccountName)).
				Reply(204).JSON(map[string]string{})

			gock.New("https://quay.io").
				Delete(fmt.Sprintf("/api/v1/repository/%s/%s", quayOrganization, expectedRepoName)).
				Reply(204).JSON(map[string]string{})

			Expect(k8sClient.Delete(ctx, appComponent)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), hasCompLookupKey, createdHasComp)
				return errors.IsNotFound(err)
			}).Should(BeTrue())
		})
	})
})
