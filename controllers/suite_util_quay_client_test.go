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

package controllers

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/redhat-appstudio/image-controller/pkg/quay"
)

const (
	testQuayOrg = "user-workloads"
)

// TestQuayClient is a QuayClient for testing the controller
type TestQuayClient struct{}

var _ quay.QuayService = (*TestQuayClient)(nil)

var (
	testQuayClient = &TestQuayClient{}

	CreateRepositoryFunc                          func(repository quay.RepositoryRequest) (*quay.Repository, error)
	DeleteRepositoryFunc                          func(organization, imageRepository string) (bool, error)
	ChangeRepositoryVisibilityFunc                func(organization, imageRepository string, visibility string) error
	GetRobotAccountFunc                           func(organization string, robotName string) (*quay.RobotAccount, error)
	CreateRobotAccountFunc                        func(organization string, robotName string) (*quay.RobotAccount, error)
	DeleteRobotAccountFunc                        func(organization string, robotName string) (bool, error)
	AddPermissionsForRepositoryToRobotAccountFunc func(organization, imageRepository, robotAccountName string, isWrite bool) error
	RegenerateRobotAccountTokenFunc               func(organization string, robotName string) (*quay.RobotAccount, error)
)

func ResetTestQuayClient() {
	CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) { return &quay.Repository{}, nil }
	DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) { return true, nil }
	ChangeRepositoryVisibilityFunc = func(organization, imageRepository string, visibility string) error { return nil }
	GetRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) { return &quay.RobotAccount{}, nil }
	CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) { return &quay.RobotAccount{}, nil }
	DeleteRobotAccountFunc = func(organization, robotName string) (bool, error) { return true, nil }
	AddPermissionsForRepositoryToRobotAccountFunc = func(organization, imageRepository, robotAccountName string, isWrite bool) error { return nil }
	RegenerateRobotAccountTokenFunc = func(organization, robotName string) (*quay.RobotAccount, error) { return &quay.RobotAccount{}, nil }
}

func ResetTestQuayClientToFails() {
	CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) {
		Fail("CreateRepositoryFunc invoked")
		return nil, nil
	}
	DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) {
		Fail("DeleteRepository invoked")
		return true, nil
	}
	ChangeRepositoryVisibilityFunc = func(organization, imageRepository string, visibility string) error {
		Fail("ChangeRepositoryVisibility invoked")
		return nil
	}
	GetRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
		Fail("GetRobotAccount invoked")
		return nil, nil
	}
	CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
		Fail("CreateRobotAccount invoked")
		return nil, nil
	}
	DeleteRobotAccountFunc = func(organization, robotName string) (bool, error) {
		Fail("DeleteRobotAccount invoked")
		return true, nil
	}
	AddPermissionsForRepositoryToRobotAccountFunc = func(organization, imageRepository, robotAccountName string, isWrite bool) error {
		Fail("AddPermissionsForRepositoryToRobotAccount invoked")
		return nil
	}
	RegenerateRobotAccountTokenFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
		Fail("RegenerateRobotAccountToken invoked")
		return nil, nil
	}
}

func (c *TestQuayClient) CreateRepository(repositoryRequest quay.RepositoryRequest) (*quay.Repository, error) {
	return CreateRepositoryFunc(repositoryRequest)
}
func (c *TestQuayClient) DeleteRepository(organization, imageRepository string) (bool, error) {
	return DeleteRepositoryFunc(organization, imageRepository)
}
func (*TestQuayClient) ChangeRepositoryVisibility(organization, imageRepository string, visibility string) error {
	return ChangeRepositoryVisibilityFunc(organization, imageRepository, visibility)
}
func (c *TestQuayClient) GetRobotAccount(organization string, robotName string) (*quay.RobotAccount, error) {
	return GetRobotAccountFunc(organization, robotName)
}
func (c *TestQuayClient) CreateRobotAccount(organization string, robotName string) (*quay.RobotAccount, error) {
	return CreateRobotAccountFunc(organization, robotName)
}
func (c *TestQuayClient) DeleteRobotAccount(organization string, robotName string) (bool, error) {
	return DeleteRobotAccountFunc(organization, robotName)
}
func (c *TestQuayClient) AddPermissionsForRepositoryToRobotAccount(organization, imageRepository, robotAccountName string, isWrite bool) error {
	return AddPermissionsForRepositoryToRobotAccountFunc(organization, imageRepository, robotAccountName, isWrite)
}
func (c *TestQuayClient) RegenerateRobotAccountToken(organization string, robotName string) (*quay.RobotAccount, error) {
	return RegenerateRobotAccountTokenFunc(organization, robotName)
}
func (c *TestQuayClient) GetAllRepositories(organization string) ([]quay.Repository, error) {
	return nil, nil
}
func (c *TestQuayClient) GetAllRobotAccounts(organization string) ([]quay.RobotAccount, error) {
	return nil, nil
}
func (*TestQuayClient) DeleteTag(organization string, repository string, tag string) (bool, error) {
	return true, nil
}
func (*TestQuayClient) GetTagsFromPage(organization string, repository string, page int) ([]quay.Tag, bool, error) {
	return nil, false, nil
}
