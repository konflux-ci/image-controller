package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/h2non/gock"
	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/image-controller/controllers"
	"k8s.io/apimachinery/pkg/types"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Component controller", func() {

	Context("Upon creation of Component", func() {
		It("Should be annotated", func() {

			appComponent := &appstudioredhatcomv1alpha1.Component{
				TypeMeta: v1.TypeMeta{
					APIVersion: "appstudio.redhat.com/v1alpha1",
					Kind:       "Component",
				},
				ObjectMeta: v1.ObjectMeta{
					Name:      "foo",
					Namespace: "default",
					Annotations: map[string]string{
						"foo": "bar",
						"appstudio.redhat.com/generate-image-repo": "true",
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

			Expect(k8sClient).Should(Not(BeNil()))

			Expect(k8sClient.Create(ctx, appComponent)).Should(BeNil())
			hasCompLookupKey := types.NamespacedName{Name: appComponent.Name, Namespace: appComponent.Namespace}
			createdHasComp := &appstudioredhatcomv1alpha1.Component{}

			Eventually(func() bool {
				k8sClient.Get(context.Background(), hasCompLookupKey, createdHasComp)
				return createdHasComp.ResourceVersion != ""
			}).Should(BeTrue())

			/*
				Setup mock http client for Quay.io connections.
			*/

			quayOrganization := "redhat-appstudio-user"
			uuidArray := strings.Replace(string(createdHasComp.UID), "-", "", -1)
			expectedRobotAccountName := fmt.Sprintf("redhat%s", uuidArray)
			returnedRobotAccountName := quayOrganization + "+" + expectedRobotAccountName
			expectedToken := "token"
			expectedRepoName := fmt.Sprintf("%s/%s/%s", appComponent.Namespace, appComponent.Spec.Application, appComponent.Name)

			defer gock.Off()
			defer gock.Observe(gock.DumpRequest)

			gock.New("https://quay.io").
				Post("/api/v1/repository").
				MatchHeader("Content-type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Reply(200).JSON(map[string]string{
				"description": "description",
				"namespace":   quayOrganization,
				"name":        expectedRepoName, //fmt.Sprintf("%s/%s/%s", appComponent.Namespace, appComponent.Spec.Application, appComponent.Name),
			})

			gock.New("https://quay.io").
				Put(fmt.Sprintf("/api/v1/organization/%s/robots/%s", quayOrganization, expectedRobotAccountName)).
				MatchHeader("Content-type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Reply(200).JSON(map[string]string{
				// really the only thing we care about
				"name":  returnedRobotAccountName,
				"token": expectedToken,
			})

			Eventually(func() string {

				k8sClient.Get(context.Background(), hasCompLookupKey, createdHasComp)
				annotations := createdHasComp.Annotations
				imageRepo := annotations["appstudio.redhat.com/generated-image-repository"]

				imageRepoObj := controllers.RepositoryInfo{}
				json.Unmarshal([]byte(imageRepo), &imageRepoObj)

				return imageRepoObj.ImageRepositoryURL

			}).ShouldNot(BeEmpty())

		})
	})
})
