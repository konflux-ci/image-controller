/*
Copyright 2023.

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
	"testing"

	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func Test_shouldGenerateImage(t *testing.T) {
	type args struct {
		annotations map[string]string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "dont generate image repo",
			args: args{
				annotations: map[string]string{
					"something-that-doesnt-matter": "",
					"image.redhat.com/generate":    "false",
				},
			},
			want: false,
		},
		{
			name: "generate image repo",
			args: args{
				annotations: map[string]string{
					"something-that-doesnt-matter": "",
					"image.redhat.com/generate":    "true",
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldGenerateImage(tt.args.annotations); got != tt.want {
				t.Errorf("name: %s, shouldGenerateImage() = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
func Test_generateSecretName(t *testing.T) {
	type args struct {
		c appstudioredhatcomv1alpha1.Component
	}

	tests := []struct {
		name string
		args args
		want string
	}{
		{
			"simle test for name generation of the secret",
			args{
				appstudioredhatcomv1alpha1.Component{
					ObjectMeta: v1.ObjectMeta{
						Name:      "componentname", // required for repo name generation
						Namespace: "shbose",        // required for repo name generation
						UID:       types.UID("473afb73-648b-4d3a-975f-a5bc48771885"),
					},
				},
			},
			"componentname-473afb73",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := generateSecretName(tt.args.c); got != tt.want {
				t.Errorf("generateSecretName() = %v, want %v", got, tt.want)
			}
		})
	}
}
