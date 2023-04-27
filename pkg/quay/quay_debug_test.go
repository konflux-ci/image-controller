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

	err := quayClient.AddWritePermissionsToRobotAccount(quayOrgName, quayImageRepoName, quayRobotAccountName)
	if err != nil {
		t.Fatal(err)
	}
}
