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
		{
			name:                           "Should prevent multiple underscores in Quay robot account name",
			imageRepositoryName:            "my__app_tenant_component____name",
			isPull:                         false,
			expectedRobotAccountNamePrefix: "my_app_tenant_component_name",
		},
		{
			name:                           "Should prevent multiple underscores in Quay robot account name cause be other symbols replacement",
			imageRepositoryName:            "my_._image_/_repository_-_name_",
			isPull:                         true,
			expectedRobotAccountNamePrefix: "my_image_repository_name",
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

func TestRemoveDuplicateUnderscores(t *testing.T) {
	testCases := []struct {
		name     string
		arg      string
		expected string
	}{
		{
			name:     "Should not modify string without repeating underscores",
			arg:      "my_test_string",
			expected: "my_test_string",
		},
		{
			name:     "Should handle double underscores",
			arg:      "my_test__string",
			expected: "my_test_string",
		},
		{
			name:     "Should handle multiple underscores",
			arg:      "my_test____________string",
			expected: "my_test_string",
		},
		{
			name:     "Should handle underscores in many places",
			arg:      "my____test__string",
			expected: "my_test_string",
		},
		{
			name:     "Should handle underscores at the beginning and end",
			arg:      "__my_test__string__",
			expected: "_my_test_string_",
		},
		{
			name:     "Should handle empty string",
			arg:      "",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := removeDuplicateUnderscores(tc.arg)

			if got != tc.expected {
				t.Errorf("Expected %s, but got %s", tc.expected, got)
			}
		})
	}
}

func TestGetSecretName(t *testing.T) {
	longImageRepositoryCrName := getRandomString(300)
	expectedSecretLongPrefix := longImageRepositoryCrName[0:220]

	testCases := []struct {
		name                  string
		imageRepositoryCrName string
		IsPullOnly            bool
		expectedSecretName    string
	}{
		{
			name:                  "Should generate push secret name",
			imageRepositoryCrName: "my-image-repo",
			IsPullOnly:            false,
			expectedSecretName:    "my-image-repo-image-push",
		},
		{
			name:                  "Should generate push secret name if component name is too long",
			imageRepositoryCrName: longImageRepositoryCrName,
			IsPullOnly:            false,
			expectedSecretName:    expectedSecretLongPrefix + "-image-push",
		},
		{
			name:                  "Should generate pull secret name",
			imageRepositoryCrName: "my-image-repo",
			IsPullOnly:            true,
			expectedSecretName:    "my-image-repo-image-pull",
		},
		{
			name:                  "Should generate pull secret name if component name is too long",
			imageRepositoryCrName: longImageRepositoryCrName,
			IsPullOnly:            true,
			expectedSecretName:    expectedSecretLongPrefix + "-image-pull",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			imageRepository := &imagerepositoryv1alpha1.ImageRepository{
				ObjectMeta: v1.ObjectMeta{
					Name: tc.imageRepositoryCrName,
				},
			}

			secretName := getSecretName(imageRepository, tc.IsPullOnly)

			if len(secretName) > 253 {
				t.Error("secret name is longer than allowed")
			}
			if secretName != tc.expectedSecretName {
				t.Errorf("Expected secret name %s, but got %s", tc.expectedSecretName, secretName)
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
