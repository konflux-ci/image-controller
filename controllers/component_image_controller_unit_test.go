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
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/h2non/gock"
	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
)

func TestShouldGenerateImage(t *testing.T) {
	type args struct {
		annotations map[string]string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "dont generate image repo",
			args: args{
				annotations: map[string]string{
					"something-that-doesnt-matter": "",
					GenerateImageAnnotationName:    "false",
				},
			},
			want: false,
		},
		{
			name: "generate image repo",
			args: args{
				annotations: map[string]string{
					"something-that-doesnt-matter": "",
					GenerateImageAnnotationName:    "true",
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldGenerateImage(tt.args.annotations); got != tt.want {
				t.Errorf("name: %s, shouldGenerateImage() = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestGenerateImageRepository(t *testing.T) {
	defer gock.Off()
	defer gock.GetUnmatchedRequests()

	testComponent := appstudioredhatcomv1alpha1.Component{
		ObjectMeta: v1.ObjectMeta{
			Name:      "componentname", // required for repo name generation
			Namespace: "shbose",        // required for repo name generation
			UID:       uuid.NewUUID(),
		},
		Spec: appstudioredhatcomv1alpha1.ComponentSpec{
			Application: "applicationname", //  required for repo name generation
		},
	}

	expectedNamespace := "redhat-appstudio-user"
	expectedRepoName := testComponent.Namespace + "/" + testComponent.Spec.Application + "/" + testComponent.Name

	expectedRobotAccountName := testComponent.Namespace + testComponent.Spec.Application + testComponent.Name
	returnedRobotAccountName := expectedNamespace + "+" + expectedRobotAccountName
	expectedToken := "token"

	gock.New("https://quay.io/api/v1").
		MatchHeader("Content-type", "application/json").
		MatchHeader("Authorization", "Bearer authtoken").
		Post("/repository").
		Reply(200).JSON(map[string]string{
		"description": "description",
		"namespace":   expectedNamespace,
		"name":        expectedRepoName,
	})

	gock.New("https://quay.io/api/v1").
		MatchHeader("Content-type", "application/json").
		MatchHeader("Authorization", "Bearer authtoken").
		Put(fmt.Sprintf("/organization/redhat-appstudio-user/robots/%s", expectedRobotAccountName)).
		Reply(200).JSON(map[string]string{
		// really the only thing we care about
		"name":  returnedRobotAccountName,
		"token": expectedToken,
	})

	gock.New("https://quay.io/api/v1").
		// TODO: Fix me,
		// The code commented out below is a workaround for Gock not being able to match the URL.
		// Given only one HTTP call remains, we can be sure that this is that gets called.
		//Put(fmt.Sprintf("/repository/redhat-appstudio-user/shbose/applicationname/componentname/permissions/user/%s", returnedRobotAccountName)).
		Reply(200).JSON(map[string]string{})

	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := quay.NewQuayClient(client, "authtoken", "https://quay.io/api/v1")
	r := ComponentReconciler{
		QuayClient:       &quayClient,
		QuayOrganization: expectedNamespace,
	}
	createdRepository, createdRobotAccount, err := r.generateImageRepository(context.TODO(), &testComponent)

	if err != nil {
		t.Errorf("Error generating repository and setting up robot account, Expected nil, got %v", err)
	}
	if createdRepository.Name != expectedRepoName {
		t.Errorf("Error creating repository, Expected %s, got %v", expectedRepoName, createdRepository.Name)
	}
	if createdRobotAccount.Name != returnedRobotAccountName {
		t.Errorf("Error creating robot account, Expected %s, got %v", returnedRobotAccountName, createdRobotAccount.Name)
	}
	if createdRobotAccount.Token != expectedToken {
		t.Errorf("Error creating robot account, Expected %s, got %v", expectedToken, createdRobotAccount.Token)
	}
}
