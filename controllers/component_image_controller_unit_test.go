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
	"testing"
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
					GenerateImageAnnotationName:    "false",
				},
			},
			want: false,
		},
		{
			name: "generate image repo",
			args: args{
				annotations: map[string]string{
					"something-that-doesnt-matter": "",
					GenerateImageAnnotationName:    "true",
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
