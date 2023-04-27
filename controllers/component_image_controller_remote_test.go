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
	"net/http"
	"os"
	"testing"

	"github.com/h2non/gock"
	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenerateRemoteImage(t *testing.T) {
	defer gock.Off()
	defer gock.Observe(gock.DumpRequest)

	testComponent := appstudioredhatcomv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "automation-repo", // required for repo name generation
			Namespace: "shbose",          // required for repo name generation
		},
		Spec: appstudioredhatcomv1alpha1.ComponentSpec{
			Application: "applicationname", //  required for repo name generation
		},
	}

	expectedRepoName := testComponent.Namespace + "/" + testComponent.Spec.Application + "/" + testComponent.Name
	expectedRobotAccountName := testComponent.Namespace + testComponent.Spec.Application + testComponent.Name
	returnedRobotAccountName := "redhat-user-workloads" + "+" + expectedRobotAccountName

	client := &http.Client{Transport: &http.Transport{}}

	quayToken := os.Getenv("DEV_QUAY_TOKEN")
	if quayToken == "" {
		//skip test.
		return
	}

	quayClient := quay.NewQuayClient(client, quayToken, "https://quay.io/api/v1")

	r := ComponentReconciler{
		QuayClient:       &quayClient,
		QuayOrganization: "redhat-user-workloads",
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
}
