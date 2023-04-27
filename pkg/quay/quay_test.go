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
	"errors"
	"fmt"
	"net/http"
	"reflect"
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
		Put("/repository/org/repository/permissions/user/org\\+robot").
		Reply(200)

	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	err := quayClient.AddWritePermissionsToRobotAccount("org", "repository", "robot")

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
	invalidRobotNameErr := fmt.Errorf("robot name is invalid, must match `^([a-z0-9]+(?:[._-][a-z0-9]+)*)$` (one plus sign in the middle is also allowed)")
	testCases := []struct {
		name         string
		input        string
		expectedName string
		expectedErr  error
	}{
		{
			name:         "valid short name",
			input:        "robot",
			expectedName: "robot",
			expectedErr:  nil,
		},
		{
			name:         "valid long name",
			input:        "org+robot",
			expectedName: "robot",
			expectedErr:  nil,
		},
		{
			name:         "empty input",
			input:        "",
			expectedName: "",
			expectedErr:  invalidRobotNameErr,
		},
		{
			name:         "two plus signs in name",
			input:        "org+second+test",
			expectedName: "",
			expectedErr:  invalidRobotNameErr,
		},
		{
			name:         "lone plus sign",
			input:        "+",
			expectedName: "",
			expectedErr:  invalidRobotNameErr,
		},
		{
			name:         "special character in name",
			input:        "robot!robot",
			expectedName: "",
			expectedErr:  invalidRobotNameErr,
		},
		{
			name:         "uppercase character in name",
			input:        "RobOt",
			expectedName: "",
			expectedErr:  invalidRobotNameErr,
		},
		{
			name:         "leading spaces in name",
			input:        "  robot  ",
			expectedName: "robot",
			expectedErr:  nil,
		},
		{
			name:         "non-alphanumeric character in name",
			input:        "róbot",
			expectedName: "",
			expectedErr:  invalidRobotNameErr,
		},
		{
			name:         "allowed characters in name",
			input:        "robot_robot-robot.robot",
			expectedName: "robot_robot-robot.robot",
			expectedErr:  nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualName, actualErr := handleRobotName(tc.input)
			if actualName != tc.expectedName {
				t.Errorf("expected robot name `%s` but got `%s`", tc.expectedName, actualName)
			}
			if (actualErr != nil || tc.expectedErr != nil) && errors.Is(actualErr, tc.expectedErr) {
				t.Errorf("expected error `%s`, but got `%s`", tc.expectedErr, actualErr)
			}
		})
	}
}

func TestQuayClient_GetTagsFromPage(t *testing.T) {
	defer gock.Off()

	org := "test_org"
	repo := "test_repo"

	testCases := []struct {
		name          string
		pages         int
		tagsPerPage   int
		hasAdditional []bool
	}{
		{
			name:          "Single Page",
			pages:         1,
			tagsPerPage:   2,
			hasAdditional: []bool{false},
		},
		{
			name:          "Multiple Pages",
			pages:         3,
			tagsPerPage:   2,
			hasAdditional: []bool{true, true, false},
		},
	}

	for _, tc := range testCases {
		client := &http.Client{Transport: &http.Transport{}}
		gock.InterceptClient(client)

		quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

		t.Run(tc.name, func(t *testing.T) {

			for page := 1; page <= tc.pages; page++ {
				mockTags := make([]Tag, tc.tagsPerPage)
				for i := 0; i < tc.tagsPerPage; i++ {
					mockTags[i] = Tag{
						Name: fmt.Sprintf("tag%d", (page-1)*tc.tagsPerPage+i),
					}
				}

				gock.New("https://quay.io/api/v1").
					MatchHeader("Authorization", "Bearer authtoken").
					MatchHeader("Content-Type", "application/json").
					Get(fmt.Sprintf("repository/%s/%s/tag/", org, repo)).
					MatchParam("page", fmt.Sprintf("%d", page)).
					Reply(200).
					JSON(map[string]interface{}{
						"tags":           mockTags,
						"has_additional": tc.hasAdditional[page-1],
					})
				tags, hasAdditional, err := quayClient.GetTagsFromPage(org, repo, page)
				if err != nil {
					t.Errorf("error getting all tags from page, expected `nil`, got `%s`", err)
				}
				if !reflect.DeepEqual(mockTags, tags) {
					t.Errorf("tags are not the same, expected `%v`, got `%v`", mockTags, tags)
				}
				if hasAdditional != tc.hasAdditional[page-1] {
					t.Errorf("hasAdditional is not the same, expected `%t`, got `%t`", tc.hasAdditional[page-1], hasAdditional)
				}
			}
		})
	}
}

func TestQuayClient_DeleteTag(t *testing.T) {
	defer gock.Off()

	org := "test_org"
	repo := "test_repo"

	testCases := []struct {
		name       string
		tag        string
		deleted    bool
		err        error
		statusCode int
		response   []byte
	}{
		{
			name:       "tag deleted succesfully",
			tag:        "tag",
			deleted:    true,
			err:        nil,
			statusCode: 204,
		},
		{
			name:       "tag not found",
			tag:        "tag",
			deleted:    false,
			err:        nil,
			statusCode: 404,
		},
		{
			name:       "error deleting tag",
			tag:        "tag",
			deleted:    false,
			err:        fmt.Errorf("error deleting tag"),
			statusCode: 500,
			response:   []byte(`{"error":"error deleting tag"}`),
		},
		{
			name:       "error message deleting tag",
			tag:        "tag",
			deleted:    false,
			err:        fmt.Errorf("error deleting tag"),
			statusCode: 500,
			response:   []byte(`{"error_message":"error deleting tag"}`),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: &http.Transport{}}
			gock.InterceptClient(client)

			quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")
			gock.New("https://quay.io/api/v1").
				MatchHeader("Authorization", "Bearer authtoken").
				MatchHeader("Content-Type", "application/json").
				Delete(fmt.Sprintf("repository/%s/%s/tag/%s", org, repo, tc.tag)).
				Reply(tc.statusCode).
				JSON(tc.response)

			deleted, err := quayClient.DeleteTag(org, repo, tc.tag)
			if tc.deleted != deleted {
				t.Errorf("expected deleted to be `%v`, got `%v`", tc.deleted, deleted)
			}
			if (tc.err != nil && err == nil) || (tc.err == nil && err != nil) {
				t.Errorf("expected error to be `%v`, got `%v`", tc.err, err)
			}
		})
	}
}
