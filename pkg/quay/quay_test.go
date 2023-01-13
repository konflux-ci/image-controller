package quay

import (
	"net/http"
	"testing"

	"github.com/h2non/gock"
)

func TestQuayClient_CreateRepository(t *testing.T) {
	defer gock.Off()

	gock.New("https://quay.io/api/v1").
		MatchHeader("Content-type", "application/json").
		MatchHeader("Authorization", "Bearer authtoken").
		Post("/repository").
		Reply(200).JSON(map[string]string{
		"description": "description",
		"namespace":   "redhat-appstudio-user",
		"name":        "test-repo-using-api",
	})

	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	r, err := quayClient.CreateRepository(RepositoryRequest{
		Namespace:   "redhat-appstudio-user",
		Description: "description",
		Visibility:  "public",
		Repository:  "test-repo-using-api",
	})

	if err != nil {
		t.Errorf("Error creating repository, Expected nil, got %v", err)
	} else if r.Name != "test-repo-using-api" {
		t.Errorf("Error creating repository, Expected %s, got %v", "test-repo-using-api", r)
	}
}

func TestQuayClient_CreateRobotAccount(t *testing.T) {
	defer gock.Off()

	gock.New("https://quay.io/api/v1").
		MatchHeader("Content-type", "application/json").
		MatchHeader("Authorization", "Bearer authtoken").
		Put("/organization/org/robots/robot").
		Reply(200).JSON(map[string]string{
		// really the only thing we care about
		"token": "robotaccountoken",
		"name":  "robot",
	})

	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	r, err := quayClient.CreateRobotAccount("org", "robot")

	if err != nil {
		t.Errorf("Error creating repository, Expected nil, got %v", err)
	} else if r.Token != "robotaccountoken" {
		t.Errorf("Error creating repository, Expected %s, got %v", "robotaccountoken", r)
	} else if r.Name != "robot" {
		t.Errorf("Error creating repository, Expected %s, got %v", "robot", r)
	}
}

func TestQuayClient_AddPermissions(t *testing.T) {
	defer gock.Off()

	gock.New("https://quay.io/api/v1").
		Put("/repository/org/repository/permissions/user/robot").
		Reply(200)

	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	err := quayClient.AddPermissionsToRobotAccount("org", "repository", "robot")

	if err != nil {
		t.Errorf("Error creating repository, Expected nil, got %v", err)
	}
}
