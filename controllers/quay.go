package controllers

import (
	"fmt"

	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
)

func generateRobotAccountName(component appstudioredhatcomv1alpha1.Component) string {

	//TODO: replace component.Namespace with the name of the Space
	return component.Namespace + component.Spec.Application + component.Name
}

func generateImageRepository(component appstudioredhatcomv1alpha1.Component, quayNamespace string, quayClient quay.QuayClient) (*quay.Repository, *quay.RobotAccount, error) {
	repo, err := quayClient.CreateRepository(quay.RepositoryRequest{
		Namespace:   quayNamespace,
		Visibility:  "public",
		Description: "Stonesoup repository for the user",
		Repository:  component.Namespace + "/" + component.Spec.Application + "/" + component.Name,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("Error creating repository %v", err)
	}

	fmt.Sprintln("Successfully created a repository ", repo)

	robot, err := quayClient.CreateRobotAccount(quayNamespace, generateRobotAccountName(component))
	if err != nil {
		return nil, nil, err
	}
	fmt.Sprintln("Successfully created robot ", robot)

	err = quayClient.AddPermissionsToRobotAccount(quayNamespace, repo.Name, robot.Name)
	if err != nil {
		fmt.Println("error adding permissions " + err.Error())
		return nil, nil, err
	}

	fmt.Println("Successfully added permission")

	return repo, robot, err
}
