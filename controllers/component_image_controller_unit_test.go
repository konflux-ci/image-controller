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
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"testing"

	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func deepCopyComponent(src *appstudioredhatcomv1alpha1.Component) *appstudioredhatcomv1alpha1.Component {
	var copy appstudioredhatcomv1alpha1.Component
	data, _ := json.Marshal(*src)
	json.Unmarshal(data, &copy)
	return &copy
}

func TestGenerateRobotAccountName(t *testing.T) {
	NameGeneratorTest(t, generateRobotAccountName)
}

func TestGenerateImageRepositoryName(t *testing.T) {
	NameGeneratorTest(t, generateImageRepositoryName)
}

type NameGeneratorFunc func(*appstudioredhatcomv1alpha1.Component) string

func NameGeneratorTest(t *testing.T, nameGenFunc NameGeneratorFunc) {
	funcFQN := runtime.FuncForPC(reflect.ValueOf(nameGenFunc).Pointer()).Name()
	funcFQNParts := strings.Split(funcFQN, ".")
	funcName := funcFQNParts[len(funcFQNParts)-1]

	component := &appstudioredhatcomv1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "componentName",
			Namespace: "componentNamespace",
		},
		Spec: appstudioredhatcomv1alpha1.ComponentSpec{
			Application: "applicationName",
		},
	}

	t.Run("should generate valid k8s name", func(t *testing.T) {
		name := nameGenFunc(component)
		if strings.ToLower(name) != name {
			t.Error(funcName + ": k8s name should not have capital letters")
		}
	})

	t.Run("should generate deterministic name", func(t *testing.T) {
		name1 := nameGenFunc(component)
		name2 := nameGenFunc(component)
		if name1 != name2 {
			t.Error(funcName + ": should return deterministic names")
		}
	})

	t.Run("should generate different name for component with different name", func(t *testing.T) {
		component2 := deepCopyComponent(component)
		component2.Name = "anotherName"

		name1 := nameGenFunc(component)
		name2 := nameGenFunc(component2)
		if name1 == name2 {
			t.Error(funcName + ": should return different names")
		}
	})

	t.Run("should generate different name for component with different namespace", func(t *testing.T) {
		component2 := deepCopyComponent(component)
		component2.Namespace = "anotherNamespace"

		name1 := nameGenFunc(component)
		name2 := nameGenFunc(component2)
		if name1 == name2 {
			t.Error(funcName + ": should return different names")
		}
	})

	t.Run("should generate different name for component with different application", func(t *testing.T) {
		component2 := deepCopyComponent(component)
		component2.Spec.Application = "anotherApp"

		name1 := nameGenFunc(component)
		name2 := nameGenFunc(component2)
		if name1 == name2 {
			t.Error(funcName + ": should return different names")
		}
	})
}
