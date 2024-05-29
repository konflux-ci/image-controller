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
	"strings"
	"testing"
)

// THIS FILE IS NOT UNIT TESTS
// Put your own data below and run corresponding test to debug interactions with quay.io
var (
	quayToken  = ""
	quayApiUrl = "https://quay.io/api/v1"

	quayOrgName          = "test-org"
	quayImageRepoName    = "namespace/application/component"
	quayRobotAccountName = "test-robot-account"
)

func TestCreateRepository(t *testing.T) {
	if quayToken == "" {
		return
	}
	quayClient := NewQuayClient(&http.Client{Transport: &http.Transport{}}, quayToken, quayApiUrl)

	repositoryRequest := RepositoryRequest{
		Namespace:   quayOrgName,
		Visibility:  "public",
		Description: "Test repository",
		Repository:  quayImageRepoName,
	}
	repo, err := quayClient.CreateRepository(repositoryRequest)
	if err != nil {
		t.Fatal(err)
	}
	if repo == nil {
		t.Fatal("Created repository should not be nil")
	}
}

func TestDoesRepositoryExist(t *testing.T) {
	if quayToken == "" {
		return
	}
	quayClient := NewQuayClient(&http.Client{Transport: &http.Transport{}}, quayToken, quayApiUrl)
	exists, err := quayClient.DoesRepositoryExist(quayOrgName, quayImageRepoName)
	if exists == true && err == nil {
		t.Log("Repository exists")
	} else if exists == false && strings.Contains(err.Error(), "does not exist") {
		t.Log("Repository does not exists")
	} else {
		t.Fatalf("Unexpected error: %s\n", err.Error())
	}
}

func TestDeleteRepository(t *testing.T) {
	if quayToken == "" {
		return
	}
	quayClient := NewQuayClient(&http.Client{Transport: &http.Transport{}}, quayToken, quayApiUrl)

	_, err := quayClient.DeleteRepository(quayOrgName, quayImageRepoName)
	if err != nil {
		t.Fatal(err)
	}
}

func TestIsRepositoryPublic(t *testing.T) {
	if quayToken == "" {
		return
	}
	quayClient := NewQuayClient(&http.Client{Transport: &http.Transport{}}, quayToken, quayApiUrl)
	isPublic, err := quayClient.IsRepositoryPublic(quayOrgName, quayImageRepoName)
	if isPublic && err == nil {
		t.Log("Repository is public")
	} else if isPublic == false && err == nil {
		t.Log("Repository is private")
	} else {
		t.Fatalf("Unexpected error: %s\n", err.Error())
	}
}

func TestChangeRepositoryVisibility(t *testing.T) {
	if quayToken == "" {
		return
	}
	visibility := "public"

	quayClient := NewQuayClient(&http.Client{Transport: &http.Transport{}}, quayToken, quayApiUrl)

	err := quayClient.ChangeRepositoryVisibility(quayOrgName, quayImageRepoName, visibility)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateRobotAccount(t *testing.T) {
	if quayToken == "" {
		return
	}
	quayClient := NewQuayClient(&http.Client{Transport: &http.Transport{}}, quayToken, quayApiUrl)

	robotAccount, err := quayClient.CreateRobotAccount(quayOrgName, quayRobotAccountName)
	if err != nil {
		t.Fatal(err)
	}
	if robotAccount == nil {
		t.Fatal("Created robot account should not be nil")
	}
}

func TestGetRobotAccount(t *testing.T) {
	if quayToken == "" {
		return
	}
	quayClient := NewQuayClient(&http.Client{Transport: &http.Transport{}}, quayToken, quayApiUrl)

	robotAccount, err := quayClient.GetRobotAccount(quayOrgName, quayRobotAccountName)
	if err != nil {
		if err.Error() == "Could not find robot with specified username" {
			t.Logf("Robot account %s does not exists", quayRobotAccountName)
		} else {
			t.Fatalf("Unknown error: %s\n", err.Error())
		}
	} else if robotAccount.Name == quayOrgName+"+"+quayRobotAccountName {
		t.Logf("Robot account %s exists", quayRobotAccountName)
	} else {
		t.Fatalf("Unexpected response: %v\n", robotAccount)
	}
}

func TestDeleteRobotAccount(t *testing.T) {
	if quayToken == "" {
		return
	}
	quayClient := NewQuayClient(&http.Client{Transport: &http.Transport{}}, quayToken, quayApiUrl)

	_, err := quayClient.DeleteRobotAccount(quayOrgName, quayRobotAccountName)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAddPermissionsToRobotAccount(t *testing.T) {
	if quayToken == "" {
		return
	}
	quayClient := NewQuayClient(&http.Client{Transport: &http.Transport{}}, quayToken, quayApiUrl)

	err := quayClient.AddPermissionsForRepositoryToRobotAccount(quayOrgName, quayImageRepoName, quayRobotAccountName, true)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRegenerateRobotAccountToken(t *testing.T) {
	if quayToken == "" {
		return
	}

	quayClient := NewQuayClient(&http.Client{Transport: &http.Transport{}}, quayToken, quayApiUrl)

	robotAccount, err := quayClient.RegenerateRobotAccountToken(quayOrgName, quayRobotAccountName)
	if err != nil {
		t.Fatal(err)
	}
	if robotAccount == nil {
		t.Fatal("Updated robot account should not be nil")
	}
	if robotAccount.Token == "" {
		t.Fatal("Token must be updated")
	}
}

func TestCreateNotification(t *testing.T) {
	if quayToken == "" {
		return
	}

	quayClient := NewQuayClient(&http.Client{Transport: &http.Transport{}}, quayToken, quayApiUrl)
	notification := Notification{
		Title:  "Test notification",
		Event:  "repo_push",
		Method: "webhook",
		Config: NotificationConfig{
			Url: "https://example.com",
		},
	}

	quayNotification, err := quayClient.CreateNotification(quayOrgName, quayImageRepoName, notification)
	if err != nil {
		t.Fatal(err)
	}
	if quayNotification == nil {
		t.Fatal("Notification should not be nil")
	}
	if quayNotification.UUID == "" {
		t.Fatal("Notification UUID should not be empty")
	}

	allNotifications, err := quayClient.GetNotifications(quayOrgName, quayImageRepoName)
	if err != nil {
		t.Fatal(err)
	}
	if len(allNotifications) == 0 {
		t.Fatal("No notifications found")
	}

	found := false
	for _, n := range allNotifications {
		if n.UUID == quayNotification.UUID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Notification %s not found", quayNotification.UUID)
	}

}
