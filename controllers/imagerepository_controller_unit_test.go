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
	"strings"
	"testing"

	imagerepositoryv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenerateQuayRobotAccountName(t *testing.T) {
	longRandomString := getRandomString(300)
	expectedRobotAccountLongPrefix := longRandomString[0:220]

	testCases := []struct {
		name                           string
		imageRepositoryName            string
		isPull                         bool
		expectedRobotAccountNamePrefix string
	}{
		{
			name:                           "Should generate push Quay robot account name",
			imageRepositoryName:            "my-image/some.name",
			isPull:                         false,
			expectedRobotAccountNamePrefix: "my_image_some_name",
		},
		{
			name:                           "Should limit length of push Quay robot account name",
			imageRepositoryName:            longRandomString,
			isPull:                         false,
			expectedRobotAccountNamePrefix: expectedRobotAccountLongPrefix,
		},
		{
			name:                           "Should generate pull Quay robot account name",
			imageRepositoryName:            "my-image/some.name",
			isPull:                         true,
			expectedRobotAccountNamePrefix: "my_image_some_name",
		},
		{
			name:                           "Should limit length of pull Quay robot account name",
			imageRepositoryName:            longRandomString,
			isPull:                         true,
			expectedRobotAccountNamePrefix: expectedRobotAccountLongPrefix,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			robotAccountName := generateQuayRobotAccountName(tc.imageRepositoryName, tc.isPull)

			if len(robotAccountName) > 253 {
				t.Error("robot account name is longer than allowed")
			}
			if !strings.HasPrefix(robotAccountName, tc.expectedRobotAccountNamePrefix+"_") {
				t.Errorf("Expected to have %s prefix in robot account %s", tc.expectedRobotAccountNamePrefix, robotAccountName)
			}
			if tc.isPull {
				if !strings.HasSuffix(robotAccountName, "_pull") {
					t.Error("Expecting '_pull' suffix for pull robot account name")
				}
			}
		})
	}
}

func TestGetRemoteSecretName(t *testing.T) {
	longImageRepositoryCrName := getRandomString(300)
	expectedRemoteSecretLongPrefix := longImageRepositoryCrName[0:220]

	testCases := []struct {
		name                     string
		imageRepositoryCrName    string
		IsPullOnly               bool
		expectedRemoteSecretName string
	}{
		{
			name:                     "Should generate push remote secret name",
			imageRepositoryCrName:    "my-image-repo",
			IsPullOnly:               false,
			expectedRemoteSecretName: "my-image-repo-image-push",
		},
		{
			name:                     "Should generate push remote secret name if component name is too long",
			imageRepositoryCrName:    longImageRepositoryCrName,
			IsPullOnly:               false,
			expectedRemoteSecretName: expectedRemoteSecretLongPrefix + "-image-push",
		},
		{
			name:                     "Should generate pull remote secret name",
			imageRepositoryCrName:    "my-image-repo",
			IsPullOnly:               true,
			expectedRemoteSecretName: "my-image-repo-image-pull",
		},
		{
			name:                     "Should generate pull remote secret name if component name is too long",
			imageRepositoryCrName:    longImageRepositoryCrName,
			IsPullOnly:               true,
			expectedRemoteSecretName: expectedRemoteSecretLongPrefix + "-image-pull",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			imageRepository := &imagerepositoryv1alpha1.ImageRepository{
				ObjectMeta: v1.ObjectMeta{
					Name: tc.imageRepositoryCrName,
				},
			}

			remoteSecretName := getRemoteSecretName(imageRepository, tc.IsPullOnly)

			if len(remoteSecretName) > 253 {
				t.Error("remote secret name is longer than allowed")
			}
			if remoteSecretName != tc.expectedRemoteSecretName {
				t.Errorf("Expected remote secret name %s, but got %s", tc.expectedRemoteSecretName, remoteSecretName)
			}
		})
	}
}

func TestIsComponentLinked(t *testing.T) {
	testCases := []struct {
		name            string
		imageRepository *imagerepositoryv1alpha1.ImageRepository
		expect          bool
	}{
		{
			name: "Should recognize linked component",
			imageRepository: &imagerepositoryv1alpha1.ImageRepository{
				ObjectMeta: v1.ObjectMeta{
					Labels: map[string]string{
						ApplicationNameLabelName: "application-name",
						ComponentNameLabelName:   "component-name",
					},
				},
			},
			expect: true,
		},
		{
			name:            "Should not be linked to component if labels missing",
			imageRepository: &imagerepositoryv1alpha1.ImageRepository{},
			expect:          false,
		},
		{
			name: "Should not be linked to component if application label missing",
			imageRepository: &imagerepositoryv1alpha1.ImageRepository{
				ObjectMeta: v1.ObjectMeta{
					Labels: map[string]string{
						ComponentNameLabelName: "component-name",
					},
				},
			},
			expect: false,
		},
		{
			name: "Should not be linked to component if component label missing",
			imageRepository: &imagerepositoryv1alpha1.ImageRepository{
				ObjectMeta: v1.ObjectMeta{
					Labels: map[string]string{
						ApplicationNameLabelName: "application-name",
					},
				},
			},
			expect: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := isComponentLinked(tc.imageRepository)

			if got != tc.expect {
				t.Errorf("isComponentLinked() for %v: expected %t but got %t", tc.imageRepository, tc.expect, got)
			}
		})
	}
}
