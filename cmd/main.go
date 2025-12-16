/*
Copyright 2023-2025 Red Hat, Inc.

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

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	uberzap "go.uber.org/zap"
	uberzapcore "go.uber.org/zap/zapcore"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/go-logr/logr"
	imagerepositoryv1alpha1 "github.com/konflux-ci/image-controller/api/v1alpha1"
	controllers "github.com/konflux-ci/image-controller/internal/controller"
	controllermetrics "github.com/konflux-ci/image-controller/pkg/metrics"
	"github.com/konflux-ci/image-controller/pkg/quay"
	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	// +kubebuilder:scaffold:imports
)

const (
	/* #nosec it's the path to the token, not the token itself */
	quayTokenPath string = "/workspace/quaytoken"
	quayOrgPath   string = "/workspace/organization"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(appstudioredhatcomv1alpha1.AddToScheme(scheme))
	utilruntime.Must(imagerepositoryv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	zapOpts := zap.Options{
		TimeEncoder: uberzapcore.ISO8601TimeEncoder,
		ZapOpts:     []uberzap.Option{uberzap.WithCaller(true)},
	}
	zapOpts.BindFlags(flag.CommandLine)
	klog.InitFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))
	klog.SetLogger(setupLog)

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	clientOpts := client.Options{
		Cache: &client.CacheOptions{
			DisableFor: getCacheExcludedObjectsTypes(),
		},
	}

	// The values are set according to
	// https://github.com/openshift/enhancements/blob/master/CONVENTIONS.md#handling-kube-apiserver-disruption
	leaseDuration := 137 * time.Second
	renewDeadline := 107 * time.Second
	retryPeriod := 26 * time.Second

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Client:                 clientOpts,
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "ed4c18c3.appstudio.redhat.com",
		LeaseDuration:          &leaseDuration,
		RenewDeadline:          &renewDeadline,
		RetryPeriod:            &retryPeriod,
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	readConfig := func(l logr.Logger, path string) string {
		/* #nosec we are sure the input path is clean */
		tokenContent, err := os.ReadFile(path)
		if err != nil {
			l.Error(err, fmt.Sprintf("unable to read %s", path))
		}
		return strings.TrimSpace(string(tokenContent))
	}
	quayOrganization := readConfig(setupLog, quayOrgPath)
	buildQuayClientFunc := func(l logr.Logger) quay.QuayService {
		token := readConfig(l, quayTokenPath)
		quayClient := quay.NewQuayClient(&http.Client{Transport: &http.Transport{}}, token, "https://quay.io/api/v1")
		return quayClient
	}

	if err = (&controllers.ComponentReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		BuildQuayClient:  buildQuayClientFunc,
		QuayOrganization: quayOrganization,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Controller")
		os.Exit(1)
	}

	if err = (&controllers.ImageRepositoryReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		BuildQuayClient:  buildQuayClientFunc,
		QuayOrganization: quayOrganization,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ImageRepository")
		os.Exit(1)
	}

	if err = (&controllers.QuayUsersConfigMapReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		BuildQuayClient:  buildQuayClientFunc,
		QuayOrganization: quayOrganization,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ConfigMap")
		os.Exit(1)
	}

	if err = (&controllers.ApplicationPullSecretCreator{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Application")
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()
	quayProbe, err := controllermetrics.NewQuayAvailabilityProbe(ctx, buildQuayClientFunc, quayOrganization)
	if err != nil {
		setupLog.Error(err, "unable to register quay availability probe")
		os.Exit(1)
	}
	imageControllerMetrics := controllermetrics.NewImageControllerMetrics([]controllermetrics.AvailabilityProbe{quayProbe})
	if err := imageControllerMetrics.InitMetrics(metrics.Registry); err != nil {
		setupLog.Error(err, "unable to initialize metrics")
		os.Exit(1)
	}
	imageControllerMetrics.StartMetrics(ctx)

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func getCacheExcludedObjectsTypes() []client.Object {
	return []client.Object{
		&imagerepositoryv1alpha1.ImageRepository{},
		&corev1.Secret{},
		&corev1.ConfigMap{},
	}
}
