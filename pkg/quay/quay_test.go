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
	"regexp"
	"strings"
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

	testQuayApiUrl = "https://test.registry/api/v1"
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
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

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
		{
			name:        "stop if http request fails",
			expectedErr: "failed to Do request:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Post("/repository")
			req.Reply(tc.statusCode).JSON(tc.responseData)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Put("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
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
			name:       "robot account to be created already exists, error in message field",
			statusCode: 400,
			responseData: map[string]string{
				"name":    "robot",
				"message": "Existing robot with name",
			},
			expectedErr: "",
		},
		{
			name:       "robot account to be created already exists, error in error_message field",
			statusCode: 400,
			responseData: map[string]string{
				"name":          "robot",
				"error_message": "Existing robot with name",
			},
			expectedErr: "",
		},
		{
			name:       "robot account to be created already exists, error in error field",
			statusCode: 400,
			responseData: map[string]string{
				"name":  "robot",
				"error": "Existing robot with name",
			},
			expectedErr: "",
		},
		{
			name:        "stop if http request fails",
			expectedErr: "failed to Do request:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Put("/organization/org/robots/robot")
			req.Reply(tc.statusCode).JSON(tc.responseData)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Post("another-path")
			}

			if strings.HasPrefix(tc.name, "robot account to be created already exists") {
				gock.New(testQuayApiUrl).
					MatchHeader("Content-Type", "application/json").
					MatchHeader("Authorization", "Bearer authtoken").
					Get("organization/org/robots/robot").
					Reply(200).
					JSON(map[string]string{"name": "robot", "token": "1234"})
			}

			client := &http.Client{Transport: &http.Transport{}}
			gock.InterceptClient(client)

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)

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

				if strings.HasPrefix(tc.name, "robot account to be created already exists") {
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
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

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
			expectedErr:  "failed to add permissions to the account",
		},
		{
			name:        "stop if http request fails",
			robotName:   robotName,
			expectedErr: "failed to Do request:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				Put("/repository/org/repository/permissions/user/org\\+robot")
			req.Reply(tc.statusCode).JSON(tc.responseData)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Put("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			err := quayClient.AddPermissionsForRepositoryToAccount("org", "repository", tc.robotName, true, true)

			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestQuayClient_AddReadPermissionsForRepositoryToTeam(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	testCases := []struct {
		name         string
		statusCode   int
		responseData interface{}
		expectedErr  string // Empty string means that no error is expected
	}{
		{
			name:         "add permissions normally",
			statusCode:   200,
			responseData: "",
			expectedErr:  "",
		},
		// The following test cases are for testing non-200 response code from server
		{
			name:         "return error got from error field within response",
			statusCode:   400,
			responseData: map[string]string{"error": "something is wrong"},
			expectedErr:  "something is wrong",
		},
		{
			name:         "return error got from error_message field within response",
			statusCode:   400,
			responseData: map[string]string{"error_message": "something is wrong"},
			expectedErr:  "something is wrong",
		},
		{
			name:         "server responds an invalid JSON string",
			statusCode:   400,
			responseData: "{\"name: \"info\"}",
			expectedErr:  "failed to unmarshal response body",
		},
		{
			name:        "stop if http request fails",
			expectedErr: "failed to Do request:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				Put("/repository/org/repository/permissions/team/teamname")
			req.Reply(tc.statusCode).JSON(tc.responseData)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Put("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			err := quayClient.AddReadPermissionsForRepositoryToTeam("org", "repository", "teamname")

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
			expectedErr: "failed to unmarshal response body",
		},
		{
			name:        "stop if http request fails",
			statusCode:  200,
			expectedErr: "failed to Do request",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			// First page
			response := Response{Repositories: []Repository{{Name: "test1"}}, NextPage: "next_page_token"}

			resp := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get("/repository").
				Reply(tc.statusCode)

			if tc.expectedErr == "failed to unmarshal response body" {
				resp.JSON("{\"next_page\": \"page_token}")
			} else {
				resp.JSON(response)
			}

			client := &http.Client{Transport: &http.Transport{}}
			gock.InterceptClient(client)

			// Second page
			response.Repositories = []Repository{{Name: "test2"}}
			response.NextPage = "next_page_token2"

			gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				MatchParam("next_page", "next_page_token").
				Get("/repository").
				Reply(200).
				JSON(response)

			// Last page
			response.Repositories = []Repository{{Name: "test3"}}

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				MatchParam("next_page", "next_page_token2").
				Get("/repository")
			req.Reply(200).JSON(response)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Get("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
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
		{
			name:        "stop if http request fails",
			expectedErr: "failed to Do request:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get("/organization/test_org/robots")
			req.Reply(tc.statusCode).JSON(tc.responseData)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Get("another-path")
			}

			client := &http.Client{Transport: &http.Transport{}}
			gock.InterceptClient(client)

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
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

func TestQuayClient_ListPermisssionsForRepository(t *testing.T) {
	testCases := []struct {
		name                string
		statusCode          int
		responseData        interface{}
		expectedPermissions map[string]UserAccount
		expectedErr         string
	}{
		{
			name:                "list permissions for repository normally",
			statusCode:          200,
			responseData:        "{\"permissions\": {\"user1\": {\"name\": \"user1\", \"role\": \"read\", \"is_robot\": false, \"is_org_member\": true}}}",
			expectedPermissions: map[string]UserAccount{"user1": UserAccount{Name: "user1", Role: "read", IsRobot: false, IsOrgMember: true}},
			expectedErr:         "",
		},
		{
			name:                "server does not respond 200",
			statusCode:          400,
			expectedErr:         "failed to get permissions for repository",
			responseData:        "",
			expectedPermissions: nil,
		},
		{
			name:                "server does not respond invalid a JSON string",
			statusCode:          200,
			responseData:        "{\"permissions\": {\"user1\": {\"name}}}",
			expectedPermissions: nil,
			expectedErr:         "failed to unmarshal response body",
		},
		{
			name:        "stop if http request fails",
			expectedErr: "failed to Do request:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get("/repository/test_org/test_repository/permissions/user")
			req.Reply(tc.statusCode).JSON(tc.responseData)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Get("another-path")
			}

			client := &http.Client{Transport: &http.Transport{}}
			gock.InterceptClient(client)

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			permissions, err := quayClient.ListPermissionsForRepository("test_org", "test_repository")

			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}

			assert.DeepEqual(t, tc.expectedPermissions, permissions)
		})
	}
}

func TestQuayClient_ListPermisssionsForTeam(t *testing.T) {
	testCases := []struct {
		name                string
		statusCode          int
		responseData        interface{}
		expectedPermissions []TeamPermission
		expectedErr         string
	}{
		{
			name:                "list permissions for repository normally",
			statusCode:          200,
			responseData:        "{\"permissions\": [{\"repository\": {\"name\": \"repository1\", \"is_public\": true}, \"role\": \"read\"}, {\"repository\": {\"name\": \"repository2\", \"is_public\": false}, \"role\": \"read\"}]}",
			expectedPermissions: []TeamPermission{{Repository: TeamPermissionRepository{Name: "repository1", IsPublic: true}, Role: "read"}, {Repository: TeamPermissionRepository{Name: "repository2", IsPublic: false}, Role: "read"}},
			expectedErr:         "",
		},
		{
			name:                "server does not respond 200",
			statusCode:          400,
			expectedErr:         "failed to get permissions for team",
			responseData:        "",
			expectedPermissions: nil,
		},
		{
			name:                "server does not respond invalid a JSON string 200",
			statusCode:          200,
			responseData:        "{\"permissions\": {\"repository\": {\"name}}}",
			expectedPermissions: nil,
			expectedErr:         "failed to unmarshal response body",
		},
		{
			name:                "server does not respond invalid a JSON string 400",
			statusCode:          200,
			responseData:        "{\"}",
			expectedPermissions: nil,
			expectedErr:         "failed to unmarshal response body",
		},
		{
			name:        "stop if http request fails",
			expectedErr: "failed to Do request:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get("/organization/test_org/team/test_team/permissions")
			req.Reply(tc.statusCode).JSON(tc.responseData)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Get("another-path")
			}

			client := &http.Client{Transport: &http.Transport{}}
			gock.InterceptClient(client)

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			permissions, err := quayClient.ListRepositoryPermissionsForTeam("test_org", "test_team")

			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}

			assert.DeepEqual(t, tc.expectedPermissions, permissions)
		})
	}
}

func TestQuayClient_EnsureTeam(t *testing.T) {
	testCases := []struct {
		name            string
		statusCode1     int
		responseData1   interface{}
		statusCode2     int
		responseData2   interface{}
		statusCode3     int
		responseData3   interface{}
		expectedMembers []Member
		expectedErr     string
		requestCalls    int
	}{
		{
			name:            "team already exists and has members",
			statusCode1:     200,
			responseData1:   "{\"members\": [{\"name\": \"user1\", \"kind\": \"user\", \"is_robot\": false, \"invited\": false}, {\"name\": \"user2\", \"kind\": \"user\", \"is_robot\": true, \"invited\": false}]}",
			expectedMembers: []Member{{Name: "user1", Kind: "user", IsRobot: false, Invited: false}, {Name: "user2", Kind: "user", IsRobot: true, Invited: false}},
			expectedErr:     "",
			requestCalls:    1,
		},
		{
			name:            "team already exists and has no members",
			statusCode1:     200,
			responseData1:   "{\"members\": []}",
			expectedMembers: []Member{},
			expectedErr:     "",
			requestCalls:    1,
		},
		{
			name:            "get members fails 200",
			statusCode1:     400,
			responseData1:   "",
			expectedErr:     "failed to get team members for team",
			expectedMembers: nil,
			requestCalls:    1,
		},
		{
			name:            "team doesn't exist, will be created",
			statusCode1:     404,
			responseData1:   "",
			statusCode2:     200,
			responseData2:   "",
			statusCode3:     200,
			responseData3:   "{\"members\": [{\"name\": \"user1\", \"kind\": \"user\", \"is_robot\": false, \"invited\": false}, {\"name\": \"user2\", \"kind\": \"user\", \"is_robot\": true, \"invited\": false}]}",
			expectedMembers: []Member{{Name: "user1", Kind: "user", IsRobot: false, Invited: false}, {Name: "user2", Kind: "user", IsRobot: true, Invited: false}},
			expectedErr:     "",
			requestCalls:    3,
		},
		{
			name:            "team doesn't exist, create fails, error in error field",
			statusCode1:     404,
			responseData1:   "",
			statusCode2:     400,
			responseData2:   "{\"error\": \"something is wrong in the server\"}",
			expectedMembers: nil,
			expectedErr:     "something is wrong in the server",
			requestCalls:    2,
		},
		{
			name:            "team doesn't exist, create fails, error in error_message field",
			statusCode1:     404,
			responseData1:   "",
			statusCode2:     400,
			responseData2:   "{\"error_message\": \"something is wrong\"}",
			expectedMembers: nil,
			expectedErr:     "something is wrong",
			requestCalls:    2,
		},
		{
			name:            "team doesn't exist, create fails, invalid a JSON string",
			statusCode1:     404,
			responseData1:   "",
			statusCode2:     400,
			responseData2:   "{\"error_message\": \"}",
			expectedMembers: nil,
			expectedErr:     "failed to unmarshal response body",
			requestCalls:    2,
		},
		{
			name:          "stop if http request fails",
			statusCode1:   404,
			responseData1: "",
			expectedErr:   "failed to Do request:",
			requestCalls:  2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req1 := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get("/organization/test_org/team/test_team/members")
			req1.Reply(tc.statusCode1).JSON(tc.responseData1)

			if tc.requestCalls >= 1 {
				req2 := gock.New(testQuayApiUrl).
					Put("/organization/test_org/team/test_team")
				req2.Reply(tc.statusCode2).JSON(tc.responseData2)

				if tc.name == "stop if http request fails" {
					req2.AddMatcher(gock.MatchPath).Get("another-path")
				}
			}
			if tc.requestCalls >= 2 {
				req3 := gock.New(testQuayApiUrl).
					MatchHeader("Content-Type", "application/json").
					MatchHeader("Authorization", "Bearer authtoken").
					Get("/organization/test_org/team/test_team/members")
				req3.Reply(tc.statusCode3).JSON(tc.responseData3)
			}

			client := &http.Client{Transport: &http.Transport{}}
			gock.InterceptClient(client)

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			members, err := quayClient.EnsureTeam("test_org", "test_team")

			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}

			assert.DeepEqual(t, tc.expectedMembers, members)
		})
	}
}

func TestQuayClient_GetTeamMembers(t *testing.T) {
	testCases := []struct {
		name            string
		statusCode      int
		responseData    interface{}
		expectedMembers []Member
		expectedErr     string
	}{
		{
			name:            "list members for team normally",
			statusCode:      200,
			responseData:    "{\"members\": [{\"name\": \"user1\", \"kind\": \"user\", \"is_robot\": false, \"invited\": false}, {\"name\": \"user2\", \"kind\": \"user\", \"is_robot\": true, \"invited\": false}]}",
			expectedMembers: []Member{{Name: "user1", Kind: "user", IsRobot: false, Invited: false}, {Name: "user2", Kind: "user", IsRobot: true, Invited: false}},
			expectedErr:     "",
		},
		{
			name:            "list members for team normally, no members",
			statusCode:      200,
			responseData:    "{\"members\": []}",
			expectedMembers: []Member{},
			expectedErr:     "",
		},
		{
			name:            "team doesn't exist responds 404",
			statusCode:      404,
			responseData:    "",
			expectedMembers: nil,
			expectedErr:     "",
		},
		{
			name:            "server does not respond 200",
			statusCode:      400,
			expectedErr:     "failed to get team members for team",
			responseData:    "",
			expectedMembers: nil,
		},
		{
			name:            "server does not respond invalid a JSON string 200",
			statusCode:      200,
			responseData:    "{\"members\": [{\"name\": \"}}]",
			expectedMembers: nil,
			expectedErr:     "failed to unmarshal response body",
		},
		{
			name:            "server does not respond invalid a JSON string 400",
			statusCode:      500,
			responseData:    "{\"]",
			expectedMembers: nil,
			expectedErr:     "failed to unmarshal response body",
		},

		{
			name:        "stop if http request fails",
			expectedErr: "failed to Do request:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get("/organization/test_org/team/test_team/members")
			req.Reply(tc.statusCode).JSON(tc.responseData)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Get("another-path")
			}

			client := &http.Client{Transport: &http.Transport{}}
			gock.InterceptClient(client)

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			members, err := quayClient.GetTeamMembers("test_org", "test_team")

			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}

			assert.DeepEqual(t, tc.expectedMembers, members)
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
		{
			name:        "stop if http request fails",
			statusCode:  200,
			pages:       1,
			tagsPerPage: 2,
			expectedErr: "failed to Do request:",
		},
	}

	for _, tc := range testCases {
		client := &http.Client{Transport: &http.Transport{}}
		gock.InterceptClient(client)

		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)

			for page := 1; page <= tc.pages; page++ {
				mockTags := make([]Tag, tc.tagsPerPage)
				for i := 0; i < tc.tagsPerPage; i++ {
					mockTags[i] = Tag{
						Name: fmt.Sprintf("tag%d", (page-1)*tc.tagsPerPage+i),
					}
				}

				req := gock.New(testQuayApiUrl).
					MatchHeader("Authorization", "Bearer authtoken").
					MatchHeader("Content-Type", "application/json").
					Get(fmt.Sprintf("repository/%s/%s/tag/", org, repo)).
					MatchParam("page", fmt.Sprintf("%d", page))

				var responseData interface{}
				if tc.expectedErr == "" {
					responseData = map[string]interface{}{
						"tags":           mockTags,
						"has_additional": tc.hasAdditional[page-1],
					}
				} else {
					responseData = "{\"has_additional: false}"
				}
				req.Reply(tc.statusCode).JSON(responseData)

				if tc.name == "stop if http request fails" {
					req.AddMatcher(gock.MatchPath).Get("another-path")
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
		{
			name:        "stop if http request fails",
			expectedErr: "failed to Do request:",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			client := &http.Client{Transport: &http.Transport{}}
			gock.InterceptClient(client)

			req := gock.New(testQuayApiUrl).
				MatchHeader("Authorization", "Bearer authtoken").
				MatchHeader("Content-Type", "application/json").
				Delete(fmt.Sprintf("repository/%s/%s/tag/%s", org, repo, tc.tag))
			req.Reply(tc.statusCode).JSON(tc.response)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Delete("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
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
		{
			name:        "stop if http request fails",
			expectedErr: "failed to Do request:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get(fmt.Sprintf("repository/%s/%s", org, repo))
			req.Reply(tc.statusCode).JSON(tc.response)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Get("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
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
		{
			name: "stop if http request fails",
			err:  fmt.Errorf("failed to Do request"),
		},
		{
			name:       "server responds an invalid JSON string for 200",
			err:        fmt.Errorf("failed to unmarshal response body"),
			statusCode: 200,
			response:   []byte("{\"error\": \"something is wrong}"),
		},
		{
			name:       "server responds an invalid JSON string for other status code",
			err:        fmt.Errorf("failed to unmarshal response body"),
			statusCode: 400,
			response:   []byte("{\"error\": \"something is wrong}"),
		},
		{
			name:       "return error got from error field within response",
			isPublic:   false,
			statusCode: 400,
			response:   []byte(`{"error": "something is wrong"}`),
			err:        fmt.Errorf("something is wrong"),
		},
		{
			name:       "return error got from error_message field within response",
			isPublic:   false,
			statusCode: 400,
			response:   []byte(`{"error_message": "something is wrong"}`),
			err:        fmt.Errorf("something is wrong"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get(fmt.Sprintf("repository/%s/%s", org, repo))
			req.Reply(tc.statusCode).JSON(tc.response)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Get("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			isPublic, err := quayClient.IsRepositoryPublic(org, repo)
			assert.Equal(t, tc.isPublic, isPublic)
			if tc.err == nil {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.err.Error())
			}
		})
	}
}

func TestQuayClient_DeleteRepository(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

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
		{
			name:        "stop if http request fails",
			expectedErr: "failed to Do request:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Delete(fmt.Sprintf("repository/%s/%s", org, repo))
			req.Reply(tc.statusCode).JSON(tc.response)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Delete("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
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
		{
			name:       "stop if http request fails",
			visibility: "public",
			err:        "failed to Do request:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				BodyString(fmt.Sprintf(`{"visibility": "%s"}`, tc.visibility)).
				Post(fmt.Sprintf("repository/%s/%s/changevisibility", org, repo))
			req.Reply(tc.statusCode).JSON(tc.response)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Post("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
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
		{
			name:        "stop if http request fails",
			expectedErr: "failed to Do request:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get(fmt.Sprintf("organization/%s/robots/%s", org, robotName))
			req.Reply(tc.statusCode).JSON(tc.response)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Get("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
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
		{
			name:        "stop if http request fails",
			robotName:   robotName,
			expectedErr: "failed to Do request:",
		},
		{
			name:        "server responds an invalid JSON string",
			robotName:   robotName,
			expectedErr: "failed to unmarshal response body",
			response:    "{\"error\": \"something is wrong}",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Delete(fmt.Sprintf("organization/%s/robots/%s", org, tc.robotName))
			req.Reply(tc.statusCode).JSON(tc.response)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Delete("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
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

func TestQuayClient_RegenerateRobotAccountToken(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

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
			name:        "Regenerate credentials for existing robot account",
			robot:       sampleRobot,
			expectedErr: "",
			statusCode:  200,
			response:    fmt.Sprintf(`{"name": "%s+%s", "created": "Wed, 12 Jul 2023 10:25:41 -0000", "last_accessed": null, "description": "", "token": "abc123", "unstructured_metadata": {}}`, org, robotName),
		},
		{
			name:        "return error when server responds non-200",
			robot:       nil,
			expectedErr: "Could not find robot with specified name",
			statusCode:  404,
			response:    map[string]string{"message": "Could not find robot with specified name"},
		},
		{
			name:        "server responds an invalid JSON string",
			robot:       nil,
			expectedErr: "failed to unmarshal response body",
			statusCode:  200, // this field can be ignored for this case
			response:    `{"name": "robotname", "token": "token1234}`,
		},
		{
			name:        "stop if http request fails",
			expectedErr: "failed to Do request:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Post(fmt.Sprintf("organization/%s/robots/%s/regenerate", org, robotName))
			req.Reply(tc.statusCode).JSON(tc.response)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Get("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			robot, err := quayClient.RegenerateRobotAccountToken(org, robotName)
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

func TestQuayClient_CreateNotification(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	testCases := []struct {
		name         string
		notification *Notification
		expectedErr  string
		statusCode   int
		response     interface{}
	}{
		{
			name:         "Notification is created",
			notification: &Notification{UUID: "1234", Title: "notification title", Config: NotificationConfig{}, EventConfig: NotificationEventConfig{}},
			expectedErr:  "",
			statusCode:   201,
			response:     "{\"uuid\": \"1234\", \"title\": \"notification title\"}",
		},
		{
			name:         "Notification to be created already exists",
			notification: &Notification{UUID: "1234", Title: "notification title", Config: NotificationConfig{}, EventConfig: NotificationEventConfig{}},
			expectedErr:  "",
			statusCode:   201,
			response:     "{\"uuid\": \"1234\", \"title\": \"notification title\"}",
		},
		{
			name:         "Repository is not found",
			notification: nil,
			expectedErr:  "ailed to create repository notification. Status code: 404, error",
			statusCode:   404,
			response:     nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Post(fmt.Sprintf("repository/%s/%s/notification/", org, repo))
			req.Reply(tc.statusCode).JSON(tc.response)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Delete("another-path")
			}

			if strings.HasPrefix(tc.name, "Notification to be created already exists") {
				gock.New(testQuayApiUrl).
					MatchHeader("Content-Type", "application/json").
					MatchHeader("Authorization", "Bearer authtoken").
					Get(fmt.Sprintf("repository/%s/%s/notification/", org, repo)).
					Reply(200).
					JSON(map[string][]Notification{"notifications": []Notification{*tc.notification}})
			} else {
				gock.New(testQuayApiUrl).
					MatchHeader("Content-Type", "application/json").
					MatchHeader("Authorization", "Bearer authtoken").
					Get(fmt.Sprintf("repository/%s/%s/notification/", org, repo)).
					Reply(200).
					JSON(map[string][]Notification{"notifications": []Notification{}})
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			notification, err := quayClient.CreateNotification(org, repo, Notification{})
			if !reflect.DeepEqual(notification, tc.notification) {
				t.Error("notifications are not the same")
			}
			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestQuayClient_DeleteNotification(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	testCases := []struct {
		name             string
		notificationUuid string
		expectedErr      string
		statusCode       int
		deleted          bool
		response         interface{}
	}{
		{
			name:             "Notification is deleted",
			notificationUuid: "1234",
			expectedErr:      "",
			statusCode:       204,
			deleted:          true,
			response:         nil,
		},
		{
			name:             "Notification does not exist",
			notificationUuid: "12345",
			expectedErr:      "ailed to delete repository notification. Status code: 400, error",
			statusCode:       400,
			deleted:          false,
			response:         nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Delete(fmt.Sprintf("repository/%s/%s/notification/%s", org, repo, tc.notificationUuid))
			req.Reply(tc.statusCode).JSON(tc.response)

			gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Delete(fmt.Sprintf("repository/%s/%s/notification/%s", org, repo, tc.notificationUuid)).
				Reply(200)

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			res, err := quayClient.DeleteNotification(org, repo, tc.notificationUuid)
			if !reflect.DeepEqual(res, tc.deleted) {
				t.Error("results are not the same")
			}
			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestQuayClient_UpdateNotification(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	testCases := []struct {
		name             string
		notification     *Notification
		notificationUuid string
		expectedErr      string
		statusCodeDelete int
		statusCodeCreate int
		responseDelete   interface{}
		responseCreate   interface{}
	}{
		{
			name:             "Notification is updated",
			notification:     &Notification{UUID: "1234", Title: "new notification title", Config: NotificationConfig{}, EventConfig: NotificationEventConfig{}},
			notificationUuid: "1234",
			expectedErr:      "",
			statusCodeDelete: 204,
			statusCodeCreate: 201,
			responseDelete:   nil,
			responseCreate:   "{\"uuid\": \"1234\", \"title\": \"new notification title\"}",
		},
		{
			name:             "Notification does not exist",
			notification:     nil,
			notificationUuid: "12345",
			expectedErr:      "ailed to delete repository notification. Status code: 400, error",
			statusCodeDelete: 400,
			statusCodeCreate: 201, // not applicable
			responseDelete:   nil,
			responseCreate:   nil, // not applicable
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			reqDelete := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Delete(fmt.Sprintf("repository/%s/%s/notification/%s", org, repo, tc.notificationUuid))
			reqDelete.Reply(tc.statusCodeDelete).JSON(tc.responseDelete)

			reqCreate := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Post(fmt.Sprintf("repository/%s/%s/notification/", org, repo))
			reqCreate.Reply(tc.statusCodeCreate).JSON(tc.responseCreate)

			if strings.HasPrefix(tc.name, "Notification does not exist") {
				gock.New(testQuayApiUrl).
					MatchHeader("Content-Type", "application/json").
					MatchHeader("Authorization", "Bearer authtoken").
					Get(fmt.Sprintf("repository/%s/%s/notification/", org, repo)).
					Reply(200).
					JSON(map[string][]Notification{"notifications": []Notification{}})
			} else {
				gock.New(testQuayApiUrl).
					MatchHeader("Content-Type", "application/json").
					MatchHeader("Authorization", "Bearer authtoken").
					Get(fmt.Sprintf("repository/%s/%s/notification/", org, repo)).
					Reply(200).
					JSON(map[string][]Notification{"notifications": []Notification{*tc.notification}})
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			res, err := quayClient.UpdateNotification(org, repo, tc.notificationUuid, Notification{})
			if !reflect.DeepEqual(res, tc.notification) {
				t.Error("results are not the same")
				t.Error(res)
			}
			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestMakeRequest(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)

	testCases := []struct {
		name       string
		httpMethod string
		expectErr  string
	}{
		{
			name:       "create a new request",
			httpMethod: http.MethodOptions,
			expectErr:  "",
		},
		{
			name:       "fail to create a new request",
			httpMethod: "(get)",
			expectErr:  "failed to create request:.+invalid method.*",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := quayClient.makeRequest("http://quay.registry.io/", tc.httpMethod, nil)
			if tc.expectErr == "" {
				assert.NilError(t, err)
				assert.Assert(t, req != nil)
				val, exists := req.Header["Content-Type"]
				assert.Equal(t, true, exists)
				assert.Equal(t, "application/json", val[0])
				val, exists = req.Header["Authorization"]
				assert.Equal(t, true, exists)
				assert.Equal(t, "Bearer authtoken", val[0])
			} else {
				assert.Assert(t, req == nil, fmt.Sprintf("expected nil request, got %v", req))
				reg := regexp.MustCompile(tc.expectErr)
				assert.Assert(t, reg.MatchString(err.Error()))
			}
		})
	}
}

func TestDoRequest(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	testCases := []struct {
		name       string
		httpMethod string
		expectErr  string
	}{
		{
			name:       "stop if fail to create a request",
			httpMethod: "(get)", // make it fail
			expectErr:  "failed to create request:.+",
		},
		{
			name:       "fail to do a request",
			httpMethod: http.MethodPost,
			expectErr:  "failed to Do request:.+cannot match any request.*",
		},
		{
			name:       "success to do a request",
			httpMethod: http.MethodGet,
			expectErr:  "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Get("/")
			req.Reply(403)

			if tc.name == "fail to do a request" {
				req.AddMatcher(gock.MatchPath).Post("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			resp, err := quayClient.doRequest(testQuayApiUrl, tc.httpMethod, nil)
			if tc.expectErr == "" {
				assert.NilError(t, err)
				assert.Assert(t, resp != nil)
				assert.Equal(t, 403, resp.response.StatusCode)
			} else {
				assert.Assert(t, resp == nil, fmt.Sprintf("expected nil QuayResponse object, got %v", resp))
				re := regexp.MustCompile(tc.expectErr)
				assert.Assert(t, re.MatchString(err.Error()), fmt.Sprintf("got %s", err))
			}
		})
	}
}

func TestQuayClient_DeleteTeam(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	testCases := []struct {
		name        string
		expectedErr string
		statusCode  int
		response    interface{}
	}{
		{
			name:        "Delete existing robot account",
			expectedErr: "",
			statusCode:  204,
			response:    nil,
		},
		{
			name:        "Unauthorized access",
			expectedErr: "Unauthorized",
			statusCode:  403,
			response:    responseUnauthorized,
		},
		{
			name:        "team doesn't exist 400",
			expectedErr: "",
			statusCode:  400,
			response:    "",
		},
		{
			name:        "team doesn't exist 404",
			expectedErr: "",
			statusCode:  404,
			response:    nil,
		},
		{
			name:        "server responds error in error field",
			expectedErr: "something is wrong in the server",
			statusCode:  500, // can be any status code except 204 and 404
			response:    "{\"error\": \"something is wrong in the server\"}",
		},
		{
			name:        "return error got from error_message field within response",
			expectedErr: "something is wrong",
			statusCode:  500,
			response:    "{\"error_message\": \"something is wrong\"}",
		},

		{
			name:        "stop if http request fails",
			expectedErr: "failed to Do request:",
		},
		{
			name:        "server responds an invalid JSON string",
			expectedErr: "failed to unmarshal response body",
			response:    "{\"error\": \"something is wrong}",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Delete(fmt.Sprintf("organization/%s/team/%s", org, "teamname"))
			req.Reply(tc.statusCode).JSON(tc.response)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Delete("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			err := quayClient.DeleteTeam(org, "teamname")
			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestQuayClient_AddUserToTeam(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	testCases := []struct {
		name              string
		statusCode        int
		responseData      interface{}
		expectedErr       string // Empty string means that no error is expected
		expectedPermanent bool
	}{
		{
			name:              "add user normally",
			statusCode:        200,
			responseData:      "",
			expectedErr:       "",
			expectedPermanent: false,
		},
		// The following test cases are for testing non-200 response code from server
		{
			name:              "user doesn't exist 400",
			statusCode:        400,
			responseData:      "",
			expectedErr:       "user doesn't exist",
			expectedPermanent: true,
		},
		{
			name:              "user doesn't exist 404",
			statusCode:        404,
			responseData:      "",
			expectedErr:       "user doesn't exist",
			expectedPermanent: true,
		},
		{
			name:              "return error got from error field within response",
			statusCode:        500,
			responseData:      map[string]string{"error": "something is wrong"},
			expectedErr:       "something is wrong",
			expectedPermanent: false,
		},
		{
			name:              "return error got from error_message field within response",
			statusCode:        500,
			responseData:      map[string]string{"error_message": "something is wrong"},
			expectedErr:       "something is wrong",
			expectedPermanent: false,
		},

		{
			name:              "server responds an invalid JSON string",
			statusCode:        500,
			responseData:      "{\"name: \"info}",
			expectedErr:       "failed to unmarshal response",
			expectedPermanent: false,
		},
		{
			name:              "stop if http request fails",
			expectedErr:       "failed to Do request:",
			expectedPermanent: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				Put("/organization/org/team/teamname/members/user1")
			req.Reply(tc.statusCode).JSON(tc.responseData)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Put("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			permanent, err := quayClient.AddUserToTeam("org", "teamname", "user1")

			assert.DeepEqual(t, tc.expectedPermanent, permanent)

			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestQuayClient_RemoveUserFromTeam(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{}}
	gock.InterceptClient(client)

	testCases := []struct {
		name        string
		expectedErr string
		statusCode  int
		response    interface{}
	}{
		{
			name:        "Delete user from team",
			expectedErr: "",
			statusCode:  204,
			response:    nil,
		},
		{
			name:        "Unauthorized access",
			expectedErr: "Unauthorized",
			statusCode:  403,
			response:    responseUnauthorized,
		},
		{
			name:        "user isn't anymore in the team 400",
			expectedErr: "",
			statusCode:  400,
			response:    "",
		},
		{
			name:        "user doesn't exist 404",
			expectedErr: "",
			statusCode:  404,
			response:    nil,
		},
		{
			name:        "server responds error in error field",
			expectedErr: "something is wrong in the server",
			statusCode:  500, // can be any status code except 204 and 404
			response:    "{\"error\": \"something is wrong in the server\"}",
		},
		{
			name:        "return error got from error_message field within response",
			expectedErr: "something is wrong",
			statusCode:  500,
			response:    "{\"error_message\": \"something is wrong\"}",
		},
		{
			name:        "stop if http request fails",
			expectedErr: "failed to Do request:",
		},
		{
			name:        "server responds an invalid JSON string",
			expectedErr: "failed to unmarshal response body",
			response:    "{\"error\": \"something is wrong}",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer gock.Off()

			req := gock.New(testQuayApiUrl).
				MatchHeader("Content-Type", "application/json").
				MatchHeader("Authorization", "Bearer authtoken").
				Delete(fmt.Sprintf("organization/%s/team/%s/members/%s", org, "teamname", "user1"))
			req.Reply(tc.statusCode).JSON(tc.response)

			if tc.name == "stop if http request fails" {
				req.AddMatcher(gock.MatchPath).Delete("another-path")
			}

			quayClient := NewQuayClient(client, "authtoken", testQuayApiUrl)
			err := quayClient.RemoveUserFromTeam(org, "teamname", "user1")
			if tc.expectedErr == "" {
				assert.NilError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}
