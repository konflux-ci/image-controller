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
	"strings"
	"testing"

	"github.com/h2non/gock"
)

const (
	org       = "test_org"
	repo      = "test_repo"
	robotName = "robot_name"
)

var responseUnauthorized = []byte(`{"detail": "Unauthorized", "error_message": "Unauthorized", "error_type": "insufficient_scope", "title": "insufficient_scope", "type": "https://quay.io/api/v1/error/insufficient_scope", "status": 403}`)

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

func TestQuayClient_CreateRobotAccountErrorHandling(t *testing.T) {
	defer gock.Off()

	gock.New("https://quay.io/api/v1").
		MatchHeader("Content-type", "application/json").
		MatchHeader("Authorization", "Bearer authtoken").
		Put("/organization/org/robots/robot").
		Reply(401).JSON(map[string]string{
		"message": "Unauthorised",
	})

	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	_, err := quayClient.CreateRobotAccount("org", "robot")

	if err == nil {
		t.Errorf("Failure was not reported")
	} else if strings.Contains(err.Error(), "Unauthorised") {
		t.Errorf("Unexpected error message %s should contain 'Unauthorised'", err.Error())
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

	err := quayClient.AddPermissionsForRepositoryToRobotAccount("org", "repository", "robot", true)

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

func TestQuayClient_DoesRepositoryExist(t *testing.T) {
	defer gock.Off()

	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	testCases := []struct {
		name        string
		shouldExist bool
		err         error
		statusCode  int
		response    []byte
	}{
		{
			name:        "Repository exists",
			shouldExist: true,
			err:         nil,
			statusCode:  200,
			response:    nil,
		},
		{
			name:        "Repository does not exist",
			shouldExist: false,
			err:         fmt.Errorf("repository %s does not exist in %s organization", repo, org),
			statusCode:  404,
			response:    []byte(`{"detail": "Not Found", "error_message": "Not Found", "error_type": "not_found", "title": "not_found", "type": "https://quay.io/api/v1/error/not_found", "status": 404}`),
		},
		{
			name:        "Unauthorized access",
			shouldExist: false,
			err:         errors.New("Unauthorized access"),
			statusCode:  403,
			response:    responseUnauthorized,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gock.New("https://quay.io/api/v1").
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get(fmt.Sprintf("repository/%s/%s", org, repo)).
				Reply(tc.statusCode).
				JSON(tc.response)

			exists, err := quayClient.DoesRepositoryExist(org, repo)
			if exists != tc.shouldExist {
				t.Errorf("expected result to be `%t`, got `%t`", tc.shouldExist, exists)
			}
			if (tc.err != nil && err == nil) || (tc.err == nil && err != nil) {
				t.Errorf("expected error to be `%v`, got `%v`", tc.err, err)
			}

		})
	}
}

func TestQuayClient_IsRepositoryPublic(t *testing.T) {
	defer gock.Off()

	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	testCases := []struct {
		name       string
		isPublic   bool
		err        error
		statusCode int
		response   []byte
	}{
		{
			name:       "Repository is public",
			isPublic:   true,
			err:        nil,
			statusCode: 200,
			response:   []byte(`{"namespace": "test_org", "name": "test_repo", "kind": "image", "description": "Test repository", "is_public": true, "is_organization": true, "is_starred": false, "status_token": "", "trust_enabled": false, "tag_expiration_s": 1209600, "is_free_account": false, "state": "NORMAL", "tags": {}, "can_write": true, "can_admin": true}`),
		},
		{
			name:       "Repository is private",
			isPublic:   false,
			err:        nil,
			statusCode: 200,
			response:   []byte(`{"namespace": "test_org", "name": "test_repo", "kind": "image", "description": "Test repository", "is_public": false, "is_organization": true, "is_starred": false, "status_token": "", "trust_enabled": false, "tag_expiration_s": 1209600, "is_free_account": false, "state": "NORMAL", "tags": {}, "can_write": true, "can_admin": true}`),
		},
		{
			name:       "Repository does not exist",
			isPublic:   false,
			err:        fmt.Errorf("repository %s does not exist in %s organization", repo, org),
			statusCode: 404,
			response:   []byte(`{"detail": "Not Found", "error_message": "Not Found", "error_type": "not_found", "title": "not_found", "type": "https://quay.io/api/v1/error/not_found", "status": 404}`),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gock.New("https://quay.io/api/v1").
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get(fmt.Sprintf("repository/%s/%s", org, repo)).
				Reply(tc.statusCode).
				JSON(tc.response)

			isPublic, err := quayClient.IsRepositoryPublic(org, repo)
			if isPublic != tc.isPublic {
				t.Errorf("expected result to be `%t`, got `%t`", tc.isPublic, isPublic)
			}
			if (tc.err != nil && err == nil) || (tc.err == nil && err != nil) {
				t.Errorf("expected error to be `%v`, got `%v`", tc.err, err)
			}

		})
	}
}

func TestQuayClient_DeleteRepository(t *testing.T) {
	defer gock.Off()

	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	testCases := []struct {
		name       string
		deleted    bool
		err        error
		statusCode int
		response   []byte
	}{
		{
			name:       "Repository is deleted",
			deleted:    true,
			err:        nil,
			statusCode: 204,
			response:   nil,
		},
		// Quay actually returns 204 even when repository does not exist and is not deleted
		{
			name:       "Unauthorized access",
			deleted:    false,
			err:        errors.New("Unauthorized access"),
			statusCode: 403,
			response:   responseUnauthorized,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gock.New("https://quay.io/api/v1").
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Delete(fmt.Sprintf("repository/%s/%s", org, repo)).
				Reply(tc.statusCode).
				JSON(tc.response)

			exists, err := quayClient.DeleteRepository(org, repo)
			if exists != tc.deleted {
				t.Errorf("expected result to be `%t`, got `%t`", tc.deleted, exists)
			}
			if (tc.err != nil && err == nil) || (tc.err == nil && err != nil) {
				t.Errorf("expected error to be `%v`, got `%v`", tc.err, err)
			}
		})
	}
}

func TestQuayClient_ChangeRepositoryVisibility(t *testing.T) {
	defer gock.Off()

	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	testCases := []struct {
		name       string
		visibility string
		err        error
		statusCode int
		response   []byte
	}{
		{
			name:       "Change visibility to public",
			visibility: "public",
			err:        nil,
			statusCode: 200, // Docs say it should be 201, but it is actually 200
			response:   []byte(`{"success": true}`),
		},
		{
			name:       "Change to private without payment",
			visibility: "private",
			err:        errors.New("payment required"),
			statusCode: 402, // Docs don't mention 402, but server actually returns 402
			response:   []byte(`{"detail": "Payment Required", "error_message": "Payment Required", "error_type": "exceeds_license", "title": "exceeds_license", "type": "https://quay.io/api/v1/error/exceeds_license", "status": 402}`),
		},
		{
			name:       "Unauthorized access",
			visibility: "private",
			err:        errors.New(`Unauthorized`),
			statusCode: 403,
			response:   responseUnauthorized,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gock.New("https://quay.io/api/v1").
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				BodyString(fmt.Sprintf(`{"visibility": "%s"}`, tc.visibility)).
				Post(fmt.Sprintf("repository/%s/%s/changevisibility", org, repo)).
				Reply(tc.statusCode).
				JSON(tc.response)

			err := quayClient.ChangeRepositoryVisibility(org, repo, tc.visibility)
			if (tc.err != nil && err == nil) || (tc.err == nil && err != nil) {
				t.Errorf("expected error to be `%v`, got `%v`", tc.err, err)
			}
		})
	}
}

func TestQuayClient_GetRobotAccount(t *testing.T) {
	defer gock.Off()

	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	sampleRobot := &RobotAccount{
		Description:  "",
		Created:      "Wed, 12 Jul 2023 10:25:41 -0000",
		LastAccessed: "",
		Token:        "abc123",
		Name:         fmt.Sprintf("%s+%s", org, robotName),
		Message:      "",
	}

	testCases := []struct {
		name       string
		robot      *RobotAccount
		err        error
		statusCode int
		response   []byte
	}{
		{
			name:       "Get existing robot account",
			robot:      sampleRobot,
			err:        nil,
			statusCode: 200,
			response:   []byte(fmt.Sprintf(`{"name": "%s+%s", "created": "Wed, 12 Jul 2023 10:25:41 -0000", "last_accessed": null, "description": "", "token": "abc123", "unstructured_metadata": {}}`, org, robotName)),
		},
		{
			name:       "Robot with specified username does not exist",
			robot:      nil,
			err:        errors.New("Could not find robot with specified username"),
			statusCode: 400,
			response:   []byte(`{"message":"Could not find robot with specified username"}`),
		},
		{
			name:       "Unauthorized access",
			robot:      nil,
			err:        errors.New("Unauthorized"),
			statusCode: 403,
			response:   responseUnauthorized,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gock.New("https://quay.io/api/v1").
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get(fmt.Sprintf("organization/%s/robots/%s", org, robotName)).
				Reply(tc.statusCode).
				JSON(tc.response)

			robot, err := quayClient.GetRobotAccount(org, robotName)
			if !reflect.DeepEqual(robot, tc.robot) {
				t.Error("robots are not the same")
			}
			if (tc.err != nil && err == nil) || (tc.err == nil && err != nil) {
				t.Errorf("expected error to be `%v`, got `%v`", tc.err, err)
			}
		})
	}
}

func TestQuayClient_DeleteRobotAccount(t *testing.T) {
	defer gock.Off()

	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	testCases := []struct {
		name            string
		shouldBeDeleted bool
		err             error
		statusCode      int
		response        []byte
	}{
		{
			name:            "Delete existing robot account",
			shouldBeDeleted: true,
			err:             nil,
			statusCode:      204,
			response:        nil,
		},
		{
			name:            "Try to delete non-existing robot account",
			shouldBeDeleted: false,
			err:             errors.New("Could not find robot with specified username"),
			statusCode:      400,
			response:        nil,
		},
		{
			name:            "Unauthorized access",
			shouldBeDeleted: false,
			err:             errors.New("Unauthorized"),
			statusCode:      403,
			response:        responseUnauthorized,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gock.New("https://quay.io/api/v1").
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Delete(fmt.Sprintf("organization/%s/robots/%s", org, robotName)).
				Reply(tc.statusCode).
				JSON(tc.response)

			deleted, err := quayClient.DeleteRobotAccount(org, robotName)
			if deleted != tc.shouldBeDeleted {
				t.Errorf("expected deleted to be `%t`, got `%t`", tc.shouldBeDeleted, deleted)
			}
			if (tc.err != nil && err == nil) || (tc.err == nil && err != nil) {
				t.Errorf("expected error to be `%v`, got `%v`", tc.err, err)
			}
		})
	}
}
