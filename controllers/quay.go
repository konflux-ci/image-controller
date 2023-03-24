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
	"fmt"

	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
)

func generateRobotAccountName(component *appstudioredhatcomv1alpha1.Component) string {
	//TODO: replace component.Namespace with the name of the Space
	return component.Namespace + component.Spec.Application + component.Name
}

func generateImageRepository(component appstudioredhatcomv1alpha1.Component, quayNamespace string, quayClient quay.QuayClient) (*quay.Repository, *quay.RobotAccount, error) {
	repo, err := quayClient.CreateRepository(quay.RepositoryRequest{
		Namespace:   quayNamespace,
		Visibility:  "public",
		Description: "AppStudio repository for the user",
		Repository:  component.Namespace + "/" + component.Spec.Application + "/" + component.Name,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("error creating repository %v", err)
	}
	fmt.Sprintln("Successfully created a repository ", repo)

	robotAccount, err := quayClient.CreateRobotAccount(quayNamespace, generateRobotAccountName(&component))
	if err != nil {
		return nil, nil, err
	}
	fmt.Sprintln("Successfully created robot account ", robotAccount)

	err = quayClient.AddPermissionsToRobotAccount(quayNamespace, repo.Name, robotAccount.Name)
	if err != nil {
		fmt.Println("error adding permissions " + err.Error())
		return nil, nil, err
	}
	fmt.Println("Successfully added permission")

	return repo, robotAccount, nil
}
