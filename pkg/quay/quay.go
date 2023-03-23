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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type QuayService interface {
	CreateRepository(r RepositoryRequest) (*Repository, error)
	CreateRobotAccount(organization string, robotName string) (*RobotAccount, error)
	DeleteRobotAccount(organization string, robotName string) (bool, error)
	AddPermissionsToRobotAccount(organization, imageRepository, robotAccountName string) error
}

var _ QuayService = (*QuayClient)(nil)

type QuayClient struct {
	url        string
	httpClient *http.Client
	AuthToken  string
}

func NewQuayClient(c *http.Client, authToken, url string) QuayClient {
	return QuayClient{
		httpClient: c,
		AuthToken:  authToken,
		url:        url,
	}
}

// CreateRepository creates a new Quay.io image repository.
func (c *QuayClient) CreateRepository(r RepositoryRequest) (*Repository, error) {

	url := fmt.Sprintf("%s/%s", c.url, "repository")

	b, err := json.Marshal(r)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))

	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	req.Header.Add("Authorization", fmt.Sprintf("%s %s", "Bearer", c.AuthToken))
	req.Header.Add("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	data := &Repository{}
	err = json.Unmarshal(body, data)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	if res.StatusCode == 400 && data.ErrorMessage == "Repository already exists" {
		data.Name = r.Repository
	}
	fmt.Println(string(body))
	return data, nil
}

// CreateRobotAccount creates a new Quay.io robot account in the organization.
func (c *QuayClient) CreateRobotAccount(organization string, robotName string) (*RobotAccount, error) {
	url := fmt.Sprintf("%s/%s/%s/%s/%s", c.url, "organization", organization, "robots", robotName)

	payload := strings.NewReader(`{
  "description": "Robot account for AppStudio Component"
}`)

	req, err := http.NewRequest(http.MethodPut, url, payload)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	req.Header.Add("Authorization", fmt.Sprintf("%s %s", "Bearer", c.AuthToken))
	req.Header.Add("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	data := &RobotAccount{}
	err = json.Unmarshal(body, data)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	if res.StatusCode == 400 && strings.Contains(data.Message, "Existing robot with name") {
		req, err = http.NewRequest(http.MethodGet, url, &bytes.Buffer{})
		if err != nil {
			fmt.Println(err)
			return nil, err
		}
		req.Header.Add("Authorization", fmt.Sprintf("%s %s", "Bearer", c.AuthToken))
		req.Header.Add("Content-Type", "application/json")

		res, err := c.httpClient.Do(req)
		if err != nil {
			fmt.Println(err)
			return nil, err
		}
		defer res.Body.Close()

		body, err := io.ReadAll(res.Body)
		if err != nil {
			fmt.Println(err)
			return nil, err
		}

		data = &RobotAccount{}
		err = json.Unmarshal(body, data)
		if err != nil {
			fmt.Println(err)
			return nil, err
		}
	}
	fmt.Println(string(body))
	return data, nil
}

// DeleteRobotAccount deletes given Quay.io robot account in the organization.
func (c *QuayClient) DeleteRobotAccount(organization string, robotName string) (bool, error) {
	url := fmt.Sprintf("%s/organization/%s/robots/%s", c.url, organization, robotName)

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Add("Authorization", fmt.Sprintf("%s %s", "Bearer", c.AuthToken))
	req.Header.Add("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()

	if res.StatusCode == 204 {
		return true, nil
	}
	if res.StatusCode == 404 {
		return false, nil
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return false, err
	}
	data := &QuayError{}
	err = json.Unmarshal(body, data)
	if err != nil {
		return false, err
	}
	return false, errors.New(data.ErrorMessage)
}

// AddPermissionsToRobotAccount enables "robotAccountName" to "write" to "repository"
func (c *QuayClient) AddPermissionsToRobotAccount(organization, imageRepository, robotAccountName string) error {

	// example,
	// url := "https://quay.io/api/v1/repository/redhat-appstudio/test-repo-using-api/permissions/user/redhat-appstudio+createdbysbose"

	url := fmt.Sprintf("%s/repository/%s/%s/permissions/user/%s", c.url, organization, imageRepository, robotAccountName)
	fmt.Println(url)
	payload := strings.NewReader(`{
	"role": "write"
  }`)

	req, err := http.NewRequest(http.MethodPut, url, payload)

	if err != nil {
		fmt.Println(err)
		return err
	}
	req.Header.Add("Authorization", fmt.Sprintf("%s %s", "Bearer", c.AuthToken))
	req.Header.Add("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	fmt.Println(req)
	if err != nil {
		return err
	}
	if res.StatusCode != 200 {
		return fmt.Errorf("error adding permissions to the robot account, got status code %d", res.StatusCode)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Println(err)
		return err
	}
	fmt.Println(string(body))
	return nil
}
