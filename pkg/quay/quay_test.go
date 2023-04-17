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

func TestQuayClient_GetAllRepositories(t *testing.T) {
	defer gock.Off()

	type Response struct {
		Repositories []Repository `json:"repositories"`
		NextPage     string       `json:"next_page"`
	}
	// First page
	response := Response{Repositories: []Repository{{Name: "test1"}}, NextPage: "next_page_token"}

	gock.New("https://quay.io/api/v1").
		MatchHeader("Content-Type", "application/json").
		MatchHeader("Authorization", "Bearer authtoken").
		Get("/repository").
		Reply(200).
		JSON(response)

	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	// Second page
	response.Repositories = []Repository{{Name: "test2"}}
	response.NextPage = "next_page_token2"

	gock.New("https://quay.io/api/v1").
		MatchHeader("Content-Type", "application/json").
		MatchHeader("Authorization", "Bearer authtoken").
		MatchParam("next_page", "next_page_token").
		Get("/repository").
		Reply(200).
		JSON(response)

	// Last page
	response.Repositories = []Repository{{Name: "test3"}}

	gock.New("https://quay.io/api/v1").
		MatchHeader("Content-Type", "application/json").
		MatchHeader("Authorization", "Bearer authtoken").
		MatchParam("next_page", "next_page_token2").
		Get("/repository").
		Reply(200).
		JSON(response)

	receivedRepos, err := quayClient.GetAllRepositories("test_org")

	if err != nil {
		t.Errorf("Error getting all repositories, Expected nil, got %v", err)
	}
	if len(receivedRepos) != 3 {
		t.Errorf("Possible pagination error, expected 3 repos, got %d repos", len(receivedRepos))
	}
}

func TestQuayClient_GetAllRobotAccounts(t *testing.T) {
	defer gock.Off()

	gock.New("https://quay.io/api/v1").
		MatchHeader("Content-Type", "application/json").
		MatchHeader("Authorization", "Bearer authtoken").
		Get("/organization/test_org/robots").
		Reply(200).
		JSON(map[string]string{})

	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	_, err := quayClient.GetAllRobotAccounts("test_org")

	if err != nil {
		t.Errorf("Error getting all robot accounts, Expected nil, got %v", err)
	}
}

func TestQuayClient_handleRobotName(t *testing.T) {
	shortName, err := handleRobotName("robot")
	if err != nil {
		t.Errorf("error handling shortname, got: %s", err)
	}
	longName, err := handleRobotName("org+robot")
	if err != nil {
		t.Errorf("error handling longname, got: %s", err)
	}
	if shortName != longName {
		t.Errorf("expected shortname `%s` to be the same as longname `%s`", shortName, longName)
	}

	_, err = handleRobotName("")
	if err == nil {
		t.Error("false match for empty string")
	}
	_, err = handleRobotName("org+second+test")
	if err == nil {
		t.Error("false match for two plus signs")
	}
	_, err = handleRobotName("+")
	if err == nil {
		t.Error("false match for `+`")
	}
}
