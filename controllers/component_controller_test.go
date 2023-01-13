package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/h2non/gock"
	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/image-controller/controllers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Component controller", func() {

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
						"foo":                       "bar",
						"image.redhat.com/generate": "true",
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
				k8sClient.Get(context.Background(), hasCompLookupKey, createdHasComp)
				return createdHasComp.ResourceVersion != ""
			}).Should(BeTrue())

			Eventually(func() controllers.RepositoryInfo {

				k8sClient.Get(context.Background(), hasCompLookupKey, createdHasComp)
				annotations := createdHasComp.Annotations
				imageRepo := annotations["image.redhat.com/image"]

				imageRepoObj := controllers.RepositoryInfo{}
				json.Unmarshal([]byte(imageRepo), &imageRepoObj)

				return imageRepoObj

			}).Should((Equal(controllers.RepositoryInfo{
				Image:  "quay.io/redhat-user-workloads/default/bar/foo",
				Secret: "foo",
			})))

			Eventually(func() string {
				Expect(k8sClient.Get(context.Background(), hasCompLookupKey, createdHasComp)).Should(BeNil())
				annotations := createdHasComp.Annotations
				return annotations["image.redhat.com/generate"]

			}).Should((Equal("false")))

			Eventually(func() string {

				secretLookupKey := types.NamespacedName{Name: appComponent.Name, Namespace: appComponent.Namespace}
				createdSecret := corev1.Secret{}
				Expect(k8sClient.Get(context.Background(), secretLookupKey, &createdSecret)).Should(BeNil())

				return string(createdSecret.Data[corev1.DockerConfigJsonKey][:])
			}).Should(Equal(fmt.Sprintf(`{"auths":{"%s":{"username":"%s","password":"%s"}}}`,
				"https://quay.io",
				returnedRobotAccountName,
				expectedToken)))

			// Now that everthing is verified, we shall try to regenerate the images.

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

			// API Call to the Permissions API remains business as usual.
			gock.New("https://quay.io").
				Put(fmt.Sprintf("/api/v1/repository/redhat-user-workloads/default/bar/foo/permissions/user/%s", returnedRobotAccountName)).
				Reply(200).JSON(map[string]string{})

			// trigger reconcile again.
			createdHasComp.Annotations["image.redhat.com/generate"] = "true"
			Expect(k8sClient.Update(ctx, createdHasComp)).Should(BeNil())

			Eventually(func() controllers.RepositoryInfo {

				k8sClient.Get(context.Background(), hasCompLookupKey, createdHasComp)
				annotations := createdHasComp.Annotations
				imageRepo := annotations["image.redhat.com/image"]

				imageRepoObj := controllers.RepositoryInfo{}
				json.Unmarshal([]byte(imageRepo), &imageRepoObj)

				return imageRepoObj

			}).Should((Equal(controllers.RepositoryInfo{
				Image:  "quay.io/redhat-user-workloads/default/bar/foo",
				Secret: "foo",
			})))

		})
	})
})
