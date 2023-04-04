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
	"context"
	"go/build"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	ctrl "sigs.k8s.io/controller-runtime"

	appstudioapiv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
	appstudiospiapiv1beta1 "github.com/redhat-appstudio/service-provider-integration-operator/api/v1beta1"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

const (
	TestQuayOrganization = "redhat-user-workloads"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	cancel    context.CancelFunc
	ctx       context.Context
	log       logr.Logger
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())
	log = ctrl.Log.WithName("testdebug")

	By("bootstrapping test environment")

	applicationApiDepVersion := "v0.0.0-20221220162402-c1e887791dac"
	spiApiDepVersion := "v0.9.1-0.20230330120320-157da860c84c"

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join(build.Default.GOPATH, "pkg", "mod", "github.com", "redhat-appstudio", "application-api@"+applicationApiDepVersion, "config", "crd", "bases"),
			filepath.Join(build.Default.GOPATH, "pkg", "mod", "github.com", "redhat-appstudio", "service-provider-integration-operator@"+spiApiDepVersion, "config", "crd", "bases"),
		},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	cfg.Timeout = 5 * time.Second

	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = appstudioapiv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = appstudiospiapiv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	err = (&ComponentReconciler{
		Client:                   k8sManager.GetClient(),
		Scheme:                   k8sManager.GetScheme(),
		Log:                      ctrl.Log.WithName("controllers").WithName("ComponentImage"),
		QuayClient:               &TestQuayClient{},
		QuayOrganization:         TestQuayOrganization,
		ImageRepositoryProvision: make(map[string]*ImageRepositoryProvisionStatus),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()

})

var _ = AfterSuite(func() {
	cancel()

	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// TestQuayClient is a QuayClient for testing the controller
type TestQuayClient struct{}

var _ quay.QuayService = (*TestQuayClient)(nil)

var (
	CreateRepositoryFunc             func(repository quay.RepositoryRequest) (*quay.Repository, error)
	DeleteRepositoryFunc             func(organization, imageRepository string) (bool, error)
	GetRobotAccountFunc              func(organization string, robotName string) (*quay.RobotAccount, error)
	CreateRobotAccountFunc           func(organization string, robotName string) (*quay.RobotAccount, error)
	DeleteRobotAccountFunc           func(organization string, robotName string) (bool, error)
	AddPermissionsToRobotAccountFunc func(organization, imageRepository, robotAccountName string) error
	RegenerateRobotAccountTokenFunc  func(organization string, robotName string) (*quay.RobotAccount, error)
)

func ResetTestQuayClient() {
	CreateRepositoryFunc = func(repository quay.RepositoryRequest) (*quay.Repository, error) { return &quay.Repository{}, nil }
	DeleteRepositoryFunc = func(organization, imageRepository string) (bool, error) { return true, nil }
	GetRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) { return &quay.RobotAccount{}, nil }
	CreateRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) { return &quay.RobotAccount{}, nil }
	DeleteRobotAccountFunc = func(organization, robotName string) (bool, error) { return true, nil }
	AddPermissionsToRobotAccountFunc = func(organization, imageRepository, robotAccountName string) error { return nil }
	RegenerateRobotAccountTokenFunc = func(organization, robotName string) (*quay.RobotAccount, error) { return &quay.RobotAccount{}, nil }
}

func (c *TestQuayClient) CreateRepository(repositoryRequest quay.RepositoryRequest) (*quay.Repository, error) {
	return CreateRepositoryFunc(repositoryRequest)
}
func (c *TestQuayClient) DeleteRepository(organization, imageRepository string) (bool, error) {
	return DeleteRepositoryFunc(organization, imageRepository)
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
func (c *TestQuayClient) AddPermissionsToRobotAccount(organization, imageRepository, robotAccountName string) error {
	return AddPermissionsToRobotAccountFunc(organization, imageRepository, robotAccountName)
}
func (c *TestQuayClient) RegenerateRobotAccountToken(organization string, robotName string) (*quay.RobotAccount, error) {
	return RegenerateRobotAccountTokenFunc(organization, robotName)
}
