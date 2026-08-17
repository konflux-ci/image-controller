/*
Copyright 2025 Red Hat, Inc.

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

// remove after component v2 migration - entire file
// This test file is for application_controller.go which won't be used with component v2.

package controllers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"

	"github.com/konflux-ci/image-controller/pkg/quay"
)

var _ = Describe("Application controller", func() {
	var appSecretTestNamespace = "application-secret-namespace-test"
	var applicationKey = types.NamespacedName{Name: "application-test", Namespace: appSecretTestNamespace}
	var applicationPullSecretName = getApplicationPullSecretName(applicationKey.Name)

	Context("Remove finalizer and unlink application pull secret from integration ServiceAccount on application removal", func() {
		BeforeEach(func() {
			quay.ResetTestQuayClient()
			createNamespace(appSecretTestNamespace)
		})

		It("should remove finalizer and unlink application pull secret from integration SA when application is deleted", func() {
			createServiceAccountWithSecrets(appSecretTestNamespace, IntegrationServiceAccountName,
				[]string{applicationPullSecretName}, []string{applicationPullSecretName})

			application := createApplicationOldModel(applicationConfig{ApplicationKey: applicationKey})
			application.ObjectMeta.Finalizers = append(application.ObjectMeta.Finalizers, ApplicationSecretLinkToSaFinalizer)
			Expect(k8sClient.Update(ctx, application)).To(Succeed())

			deleteApplication(applicationKey)

			Eventually(func() bool {
				integrationSa := getServiceAccount(appSecretTestNamespace, IntegrationServiceAccountName)
				return len(integrationSa.Secrets) == 0 && len(integrationSa.ImagePullSecrets) == 0
			}, timeout, interval).Should(BeTrue())

			deleteServiceAccount(types.NamespacedName{Name: IntegrationServiceAccountName, Namespace: appSecretTestNamespace})
		})
	})
})
