package controllers

import (
	"net/http"
	"os"
	"testing"

	"github.com/h2non/gock"
	"github.com/redhat-appstudio/application-api/api/v1alpha1"
	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenerateRemoteImage(t *testing.T) {
	defer gock.Off()
	defer gock.Observe(gock.DumpRequest)

	testComponent := v1alpha1.Component{
		ObjectMeta: v1.ObjectMeta{
			Name:      "automation-repo", // required for repo name generation
			Namespace: "shbose",          // required for repo name generation
		},
		Spec: appstudioredhatcomv1alpha1.ComponentSpec{
			Application: "applicationname", //  required for repo name generation
		},
	}

	expectedRepoName := testComponent.Namespace + "/" + testComponent.Spec.Application + "/" + testComponent.Name
	expectedRobotAccountName := testComponent.Namespace + testComponent.Spec.Application + testComponent.Name
	returnedRobotAccountName := quayOrganization + "+" + expectedRobotAccountName

	client := &http.Client{Transport: &http.Transport{}}

	quayToken := os.Getenv("DEV_QUAY_TOKEN")
	if quayToken == "" {
		//skip test.
		return
	}

	quayClient := quay.NewQuayClient(client, quayToken, "https://quay.io/api/v1")

	createdRepository, createdRobotAccount, err := generateImageRepository(testComponent, "redhat-user-workloads", quayClient)

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
