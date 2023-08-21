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

	"gotest.tools/v3/assert"

	"github.com/h2non/gock"
)

const (
	org       = "test_org"
	repo      = "test_repo"
	robotName = "robot_name"

	testRepoNamespace   = "redhat-appstudio-user"
	testRepoDescription = "test repo"
)

var responseUnauthorized = map[string]string{
	"detail":        "Unauthorized",
	"error_message": "Unauthorized",
	"error_type":    "insufficient_scope",
	"title":         "insufficient_scope",
	"type":          "https://quay.io/api/v1/error/insufficient_scope",
	"status":        "403",
}

func TestQuayClient_CreateRepository(t *testing.T) {
	testCases := []struct {
		name               string
		statusCode         int
		responseData       interface{}
		expectedRepository *Repository
		expectedErr        string // Empty string means that no error is expected
	}{
		{
			name:               "successful repository creation",
			statusCode:         200,
			responseData:       map[string]string{"name": repo},
			expectedRepository: &Repository{Name: repo},
			expectedErr:        "",
		},
		{
			name:       "repository exists already",
			statusCode: 400,
			responseData: map[string]string{
				"name":          repo,
				"error_message": "Repository already exists",
			},
			expectedRepository: &Repository{
				Name:         repo,
				ErrorMessage: "Repository already exists",
			},
			expectedErr: "",
		},
		{
			name:               "payment required",
			statusCode:         402,
			responseData:       map[string]string{"name": repo},
			expectedRepository: nil,
			expectedErr:        "payment required",
		},
		{
			name:       "not handled status with error message",
			statusCode: 500, // can be any status code not handled by CreateRepository explicitly
			responseData: map[string]string{
				"name":          repo,
				"error_message": "something is wrong in the server",
			},
			expectedRepository: &Repository{
				Name:         repo,
				ErrorMessage: "something is wrong in the server",
			},
			expectedErr: "something is wrong in the server",
		},
		{
			name:               "response data can't be encoded to a JSON data",
			statusCode:         200,
			responseData:       "{\"name\": \"repo}",
			expectedRepository: nil,
			expectedErr:        "failed to unmarshal response",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			gock.New("https://quay.io/api/v1").
				MatchHeader("Content-type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Post("/repository").
				Reply(tc.statusCode).
				JSON(tc.responseData)

			client := &http.Client{Transport: &http.Transport{}}
			gock.InterceptClient(client)

			quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

			repoInfo, err := quayClient.CreateRepository(RepositoryRequest{
				Namespace:   testRepoNamespace,
				Description: testRepoDescription,
				Visibility:  "public",
				Repository:  repo,
			})

			if tc.expectedErr == "" {
				assert.NilError(t, err, fmt.Sprintf("expected error to be nil, got '%v'", err))
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}

			if tc.expectedRepository == nil {
				assert.Assert(t, repoInfo == nil, fmt.Sprintf("expected repository is nil, got %v", repoInfo))
			} else {
				assert.Assert(t, repoInfo != nil, "expected repository info is returned, got nil")
				assert.DeepEqual(t, tc.expectedRepository, repoInfo)
			}
		})
	}
}

func TestQuayClient_CreateRobotAccount(t *testing.T) {
	defer gock.Off()

	testCases := []struct {
		name         string
		statusCode   int
		responseData interface{}
		expectedErr  string // Empty string means that no error is expected
	}{
		{
			name:       "create one successfully",
			statusCode: 200,
			responseData: map[string]string{
				"name":  "robot",
				"token": "robotaccountoken",
			},
			expectedErr: "",
		},
		{
			name:         "robot name is invalid",
			expectedErr:  "robot name is invalid, must match",
			statusCode:   0, // these two fields can be ignored for this case
			responseData: "",
		},
		{
			name:         "server responds error in error field for status codes that greater than 400",
			statusCode:   401,
			responseData: map[string]string{"error": "Unauthorised"},
			expectedErr:  "Unauthorised",
		},
		{
			name:         "server responds error in error_message field for status codes that greater than 400",
			statusCode:   401,
			responseData: map[string]string{"error_message": "Unauthorised"},
			expectedErr:  "Unauthorised",
		},
		{
			name:         "server responds an invalid JSON string",
			statusCode:   200,
			responseData: "{\"name\": \"robot}",
			expectedErr:  "failed to unmarshal response body",
		},
		{
			name:       "fail to create a robot account with status code 400",
			statusCode: 400,
			responseData: map[string]string{
				"name":    "robot",
				"message": "failed to create the robot account",
			},
			expectedErr: "failed to create robot account",
		},
		{
			name:       "robot account to be created already exists",
			statusCode: 400,
			responseData: map[string]string{
				"name":    "robot",
				"message": "Existing robot with name",
			},
			expectedErr: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			gock.New("https://quay.io/api/v1").
				MatchHeader("Content-type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Put("/organization/org/robots/robot").
				Reply(tc.statusCode).
				JSON(tc.responseData)

			if tc.name == "robot account to be created already exists" {
				gock.New("https://quay.io/api/v1").
					MatchHeader("Content-Type", "application/json").
					MatchHeader("Authorization", "Bearer authtoken").
					Get("organization/org/robots/robot").
					Reply(200).
					JSON(map[string]string{"name": "robot", "token": "1234"})
			}

			client := &http.Client{Transport: &http.Transport{}}
			gock.InterceptClient(client)

			quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

			var robotName string
			if tc.name == "robot name is invalid" {
				robotName = "robot+"
			} else {
				robotName = "robot"
			}

			robotAcc, err := quayClient.CreateRobotAccount("org", robotName)

			if tc.expectedErr == "" {
				assert.NilError(t, err)

				if tc.name == "create one successfully" {
					assert.Equal(t, robotAcc.Token, "robotaccountoken")
				}

				if tc.name == "robot account to be created already exists" {
					// Ensure the returned robot account is got by calling GetRobotAccount func
					assert.Equal(t, robotAcc.Token, "1234")
				}
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestQuayClient_AddPermissions(t *testing.T) {
	testCases := []struct {
		name         string
		robotName    string
		statusCode   int
		responseData interface{}
		expectedErr  string // Empty string means that no error is expected
	}{
		{
			name:         "add permissions normally",
			robotName:    robotName,
			statusCode:   200,
			responseData: "",
			expectedErr:  "",
		},
		{
			name:         "robot name is invalid",
			robotName:    "robot++robot",
			statusCode:   200,
			responseData: "",
			expectedErr:  "robot name is invalid",
		},
		// The following test cases are for testing non-200 response code from server
		{
			name:         "return error got from error field within response",
			robotName:    robotName,
			statusCode:   400,
			responseData: map[string]string{"error": "something is wrong"},
			expectedErr:  "something is wrong",
		},
		{
			name:         "return error got from error_message field within response",
			robotName:    robotName,
			statusCode:   400,
			responseData: map[string]string{"error_message": "something is wrong"},
			expectedErr:  "something is wrong",
		},
		{
			name:         "server responds an invalid JSON string",
			robotName:    robotName,
			statusCode:   400,
			responseData: "{\"name: \"info\"}",
			expectedErr:  "failed to add permissions to the robot account",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			gock.New("https://quay.io/api/v1").
				Put("/repository/org/repository/permissions/user/org\\+robot").
				Reply(tc.statusCode).
				JSON(tc.responseData)

			client := &http.Client{Transport: &http.Transport{}}
			gock.InterceptClient(client)

			quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

			err := quayClient.AddPermissionsForRepositoryToRobotAccount("org", "repository", tc.robotName, true)

			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestQuayClient_GetAllRepositories(t *testing.T) {
	type Response struct {
		Repositories []Repository `json:"repositories"`
		NextPage     string       `json:"next_page"`
	}

	testCases := []struct {
		name        string
		statusCode  int
		expectedErr string // Empty string means that no error is expected
	}{
		{
			name:        "get repositories normally",
			statusCode:  200,
			expectedErr: "",
		},
		{
			name:        "cannot get repositories once server does not respond 200",
			statusCode:  400,
			expectedErr: "error getting repositories",
		},
		{
			name:        "server responds invalid a JSON string",
			statusCode:  200,
			expectedErr: "failed to unmarshal body",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			// First page
			response := Response{Repositories: []Repository{{Name: "test1"}}, NextPage: "next_page_token"}

			resp := gock.New("https://quay.io/api/v1").
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get("/repository").
				Reply(tc.statusCode)

			if tc.expectedErr == "failed to unmarshal body" {
				resp.JSON("{\"next_page\": \"page_token}")
			} else {
				resp.JSON(response)
			}

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

			if tc.expectedErr == "" {
				assert.NilError(t, err)

				expected := 3
				msg := fmt.Sprintf("Possible pagination error, expected %d repos, got %d repos", expected, len(receivedRepos))
				assert.Equal(t, expected, len(receivedRepos), msg)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestQuayClient_GetAllRobotAccounts(t *testing.T) {
	testCases := []struct {
		name           string
		statusCode     int
		responseData   interface{}
		expectedRobots []RobotAccount
		expectedErr    string
	}{
		{
			name:           "get robot accounts normally",
			statusCode:     200,
			responseData:   "{\"robots\": [{\"name\": \"robot1\"}]}",
			expectedRobots: []RobotAccount{{Name: "robot1"}},
			expectedErr:    "",
		},
		{
			name:           "server does not respond 200",
			statusCode:     400,
			expectedErr:    "failed to get robot accounts.",
			responseData:   "", // these two fields can be ignored for this case
			expectedRobots: nil,
		},
		{
			name:           "server does not respond invalid a JSON string",
			statusCode:     200,
			responseData:   "{\"robots\": [{\"name\": \"robot1}]}",
			expectedRobots: nil,
			expectedErr:    "failed to unmarshal response body",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			gock.New("https://quay.io/api/v1").
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get("/organization/test_org/robots").
				Reply(tc.statusCode).
				JSON(tc.responseData)

			client := &http.Client{Transport: &http.Transport{}}
			gock.InterceptClient(client)

			quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

			robots, err := quayClient.GetAllRobotAccounts("test_org")

			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}

			assert.DeepEqual(t, tc.expectedRobots, robots)
		})
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
			name:         "ending with lone plus sign",
			input:        "robot+",
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
	testCases := []struct {
		name          string
		statusCode    int
		pages         int
		tagsPerPage   int
		hasAdditional []bool
		expectedErr   string
	}{
		{
			name:          "Single Page",
			statusCode:    200,
			pages:         1,
			tagsPerPage:   2,
			hasAdditional: []bool{false},
			expectedErr:   "",
		},
		{
			name:          "Multiple Pages",
			statusCode:    200,
			pages:         3,
			tagsPerPage:   2,
			hasAdditional: []bool{true, true, false},
			expectedErr:   "",
		},
		{
			name:          "server does not respond 200",
			statusCode:    400,
			pages:         1,
			tagsPerPage:   2,
			hasAdditional: []bool{false},
			expectedErr:   "failed to get repository tags",
		},
		{
			name:          "server responds an invalid JSON string",
			statusCode:    200,
			pages:         1,
			tagsPerPage:   2,
			hasAdditional: []bool{false},
			expectedErr:   "failed to unmarshal response body",
		},
	}

	for _, tc := range testCases {
		client := &http.Client{Transport: &http.Transport{}}
		gock.InterceptClient(client)

		quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			for page := 1; page <= tc.pages; page++ {
				mockTags := make([]Tag, tc.tagsPerPage)
				for i := 0; i < tc.tagsPerPage; i++ {
					mockTags[i] = Tag{
						Name: fmt.Sprintf("tag%d", (page-1)*tc.tagsPerPage+i),
					}
				}

				resp := gock.New("https://quay.io/api/v1").
					MatchHeader("Authorization", "Bearer authtoken").
					MatchHeader("Content-Type", "application/json").
					Get(fmt.Sprintf("repository/%s/%s/tag/", org, repo)).
					MatchParam("page", fmt.Sprintf("%d", page)).
					Reply(tc.statusCode)

				if tc.expectedErr == "" {
					resp.JSON(map[string]interface{}{
						"tags":           mockTags,
						"has_additional": tc.hasAdditional[page-1],
					})
				} else {
					resp.JSON("{\"has_additional: false}")
				}

				tags, hasAdditional, err := quayClient.GetTagsFromPage(org, repo, page)
				if tc.expectedErr == "" {
					assert.NilError(t, err)
					assert.DeepEqual(t, mockTags, tags)

					msg := fmt.Sprintf("hasAdditional is not the same, expected `%t`, got `%t`",
						tc.hasAdditional[page-1], hasAdditional)
					assert.Equal(t, tc.hasAdditional[page-1], hasAdditional, msg)
				} else {
					assert.ErrorContains(t, err, tc.expectedErr)
				}
			}
		})
	}
}

func TestQuayClient_DeleteTag(t *testing.T) {
	testCases := []struct {
		name        string
		tag         string
		deleted     bool
		expectedErr string
		statusCode  int
		response    interface{}
	}{
		{
			name:        "tag deleted succesfully",
			tag:         "tag",
			deleted:     true,
			expectedErr: "",
			statusCode:  204,
		},
		{
			name:        "tag not found",
			tag:         "tag",
			deleted:     false,
			expectedErr: "",
			statusCode:  404,
		},
		{
			name:        "error deleting tag",
			tag:         "tag",
			deleted:     false,
			expectedErr: "error deleting tag",
			statusCode:  500,
			response:    map[string]string{"error": "error deleting tag"},
		},
		{
			name:        "error message deleting tag",
			tag:         "tag",
			deleted:     false,
			expectedErr: "error deleting tag",
			statusCode:  500,
			response:    map[string]string{"error_message": "error deleting tag"},
		},
		{
			name:        "server responds an invalid JSON string",
			tag:         "tag",
			deleted:     false,
			expectedErr: "failed to unmarshal response body",
			statusCode:  500,
			response:    "{\"error_message\": \"error deleting tag}",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

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
			assert.Equal(t, tc.deleted, deleted)
			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestQuayClient_DoesRepositoryExist(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	testCases := []struct {
		name        string
		shouldExist bool
		expectedErr string
		statusCode  int
		response    interface{}
	}{
		{
			name:        "Repository exists",
			shouldExist: true,
			expectedErr: "",
			statusCode:  200,
			response:    nil,
		},
		{
			name:        "Repository does not exist",
			shouldExist: false,
			expectedErr: fmt.Sprintf("repository %s does not exist in %s organization", repo, org),
			statusCode:  404,
			response: map[string]string{
				"detail":        "Not Found",
				"error_message": "Not Found",
				"error_type":    "not_found",
				"title":         "not_found",
				"type":          "https://quay.io/api/v1/error/not_found",
				"status":        "404",
			},
		},
		{
			name:        "Unauthorized access",
			shouldExist: false,
			expectedErr: "Unauthorized",
			statusCode:  403,
			response:    responseUnauthorized,
		},
		{
			name:        "server responds an error in Error field",
			shouldExist: false,
			expectedErr: "something is wrong",
			statusCode:  403, // can be any status code
			response:    map[string]string{"error": "something is wrong"},
		},
		{
			name:        "server responds an invalid JSON string",
			shouldExist: false,
			expectedErr: "failed to unmarshal response body:",
			statusCode:  403, // must not be 404 and 200
			response:    "{\"error\": \"something is wrong}",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			gock.New("https://quay.io/api/v1").
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get(fmt.Sprintf("repository/%s/%s", org, repo)).
				Reply(tc.statusCode).
				JSON(tc.response)

			exists, err := quayClient.DoesRepositoryExist(org, repo)
			assert.Equal(t, tc.shouldExist, exists)
			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
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
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	testCases := []struct {
		name        string
		deleted     bool
		expectedErr string
		statusCode  int
		response    interface{}
	}{
		{
			name:        "Repository is deleted",
			deleted:     true,
			expectedErr: "",
			statusCode:  204,
			response:    nil,
		},
		{
			name:        "Repository is not found",
			deleted:     false,
			expectedErr: "",
			statusCode:  404,
			response:    nil,
		},
		// Quay actually returns 204 even when repository does not exist and is not deleted
		{
			name:        "Unauthorized access",
			deleted:     false,
			expectedErr: "Unauthorized",
			statusCode:  403,
			response:    responseUnauthorized,
		},
		{
			name:        "server responds an error in Error field",
			deleted:     false,
			expectedErr: "something is wrong",
			statusCode:  403, // can be any status code
			response:    map[string]string{"error": "something is wrong"},
		},
		{
			name:        "server responds an invalid JSON string",
			deleted:     false,
			expectedErr: "failed to unmarshal response",
			statusCode:  403, // must not be 404 and 200
			response:    "{\"error\": \"something is wrong}",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			gock.New("https://quay.io/api/v1").
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Delete(fmt.Sprintf("repository/%s/%s", org, repo)).
				Reply(tc.statusCode).
				JSON(tc.response)

			exists, err := quayClient.DeleteRepository(org, repo)
			assert.Equal(t, tc.deleted, exists)
			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestQuayClient_ChangeRepositoryVisibility(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	testCases := []struct {
		name       string
		visibility string
		err        string
		statusCode int
		response   interface{}
	}{
		{
			name:       "Change visibility to public",
			visibility: "public",
			err:        "",
			statusCode: 200, // Docs say it should be 201, but it is actually 200
			response:   map[string]string{"success": "true"},
		},
		{
			name:       "Change to private without payment",
			visibility: "private",
			err:        "payment required",
			statusCode: 402, // Docs don't mention 402, but server actually returns 402
			response: map[string]string{
				"detail":        "Payment Required",
				"error_message": "Payment Required",
				"error_type":    "exceeds_license",
				"title":         "exceeds_license",
				"type":          "https://quay.io/api/v1/error/exceeds_license",
				"status":        "402",
			},
		},
		{
			name:       "Unauthorized access",
			visibility: "private",
			err:        "Unauthorized",
			statusCode: 403,
			response:   responseUnauthorized,
		},
		{
			name:       "invalid visibility",
			visibility: "publish",
			err:        "invalid repository visibility: publish",
			statusCode: 500, // these two fields can be ignored for this case
			response:   "",
		},
		{
			name:       "server responds an invalid JSON string",
			visibility: "public",
			err:        "failed to unmarshal response body",
			statusCode: 400,
			response:   "{\"error\": \"something is wrong}",
		},
		{
			name:       "return res.Status as error",
			visibility: "public",
			err:        "500 ",
			statusCode: 500,
			response:   map[string]string{"error": "something is wrong"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			gock.New("https://quay.io/api/v1").
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				BodyString(fmt.Sprintf(`{"visibility": "%s"}`, tc.visibility)).
				Post(fmt.Sprintf("repository/%s/%s/changevisibility", org, repo)).
				Reply(tc.statusCode).
				JSON(tc.response)

			err := quayClient.ChangeRepositoryVisibility(org, repo, tc.visibility)
			if tc.err == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.err)
			}
		})
	}
}

func TestQuayClient_GetRobotAccount(t *testing.T) {
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
		name        string
		robot       *RobotAccount
		expectedErr string
		statusCode  int
		response    interface{}
	}{
		{
			name:        "Get existing robot account",
			robot:       sampleRobot,
			expectedErr: "",
			statusCode:  200,
			response:    fmt.Sprintf(`{"name": "%s+%s", "created": "Wed, 12 Jul 2023 10:25:41 -0000", "last_accessed": null, "description": "", "token": "abc123", "unstructured_metadata": {}}`, org, robotName),
		},
		{
			name:        "return error when server responds non-200",
			robot:       nil,
			expectedErr: "Could not find robot with specified username",
			statusCode:  400,
			response:    map[string]string{"message": "Could not find robot with specified username"},
		},
		{
			name:        "server responds an invalid JSON string",
			robot:       nil,
			expectedErr: "failed to unmarshal response body",
			statusCode:  400, // this field can be ignored for this case
			response:    "{\"error\": \"something is wrong}",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

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
			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestQuayClient_DeleteRobotAccount(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", "https://quay.io/api/v1")

	testCases := []struct {
		name            string
		robotName       string
		shouldBeDeleted bool
		expectedErr     string
		statusCode      int
		response        interface{}
	}{
		{
			name:            "Delete existing robot account",
			robotName:       robotName,
			shouldBeDeleted: true,
			expectedErr:     "",
			statusCode:      204,
			response:        nil,
		},
		{
			name:            "Unauthorized access",
			robotName:       robotName,
			shouldBeDeleted: false,
			expectedErr:     "Unauthorized",
			statusCode:      403,
			response:        responseUnauthorized,
		},
		{
			name:            "invalid roboto name",
			robotName:       "robot+Name+invalid",
			shouldBeDeleted: false,
			expectedErr:     "robot name is invalid",
			statusCode:      400, // these two fields can be ignored for this case
			response:        "",
		},
		{
			name:            "robot name does not exist",
			robotName:       robotName,
			shouldBeDeleted: false,
			expectedErr:     "",
			statusCode:      404,
			response:        nil,
		},
		{
			name:            "server responds error in error field",
			robotName:       robotName,
			shouldBeDeleted: false,
			expectedErr:     "something is wrong in the server",
			statusCode:      500, // can be any status code except 204 and 404
			response:        "{\"error\": \"something is wrong in the server\"}",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			gock.New("https://quay.io/api/v1").
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Delete(fmt.Sprintf("organization/%s/robots/%s", org, tc.robotName)).
				Reply(tc.statusCode).
				JSON(tc.response)

			deleted, err := quayClient.DeleteRobotAccount(org, tc.robotName)
			assert.Equal(t, tc.shouldBeDeleted, deleted)
			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}
