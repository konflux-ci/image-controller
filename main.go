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

package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	uberzap "go.uber.org/zap"
	uberzapcore "go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/go-logr/logr"
	appstudioredhatcomv1alpha1 "github.com/redhat-appstudio/application-api/api/v1alpha1"
	remotesecretv1beta1 "github.com/redhat-appstudio/remote-secret/api/v1beta1"

	imagerepositoryv1beta1 "github.com/redhat-appstudio/image-controller/api/v1beta1"
	"github.com/redhat-appstudio/image-controller/controllers"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
	//+kubebuilder:scaffold:imports
)

const (
	/* #nosec it's the path to the token, not the token itself */
	quayTokenPath string = "/workspace/quaytoken"
	quayOrgPath   string = "/workspace/organization"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(appstudioredhatcomv1alpha1.AddToScheme(scheme))
	utilruntime.Must(remotesecretv1beta1.AddToScheme(scheme))
	utilruntime.Must(imagerepositoryv1beta1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	zapOpts := zap.Options{
		TimeEncoder: uberzapcore.ISO8601TimeEncoder,
		ZapOpts:     []uberzap.Option{uberzap.WithCaller(true)},
	}
	zapOpts.BindFlags(flag.CommandLine)
	klog.InitFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))
	setupLog := ctrl.Log.WithName("setup")
	klog.SetLogger(setupLog)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "ed4c18c3.appstudio.redhat.com",
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
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
