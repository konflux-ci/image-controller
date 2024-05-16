package metrics

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/image-controller/pkg/quay"
	"github.com/prometheus/client_golang/prometheus"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

type QuayAvailabilityProbe struct {
	BuildQuayClient  func(logr.Logger) quay.QuayService
	QuayOrganization string
	gauge            prometheus.Gauge
}

const testRobotAccountName = "robot_konflux_api_healthcheck"

func NewQuayAvailabilityProbe(ctx context.Context, clientBuilder func(logr.Logger) quay.QuayService, quayOrganization string) (*QuayAvailabilityProbe, error) {
	client := clientBuilder(ctrllog.FromContext(ctx))
	_, err := client.CreateRobotAccount(quayOrganization, testRobotAccountName)
	if err != nil {
		return nil, fmt.Errorf("could not create test robot account: %w", err)
	}
	return &QuayAvailabilityProbe{
		BuildQuayClient:  clientBuilder,
		QuayOrganization: quayOrganization,
		gauge: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: MetricsNamespace,
				Subsystem: MetricsSubsystem,
				Name:      "global_quay_app_available",
				Help:      "The availability of the Quay App",
			}),
	}, nil
}

func (q *QuayAvailabilityProbe) CheckAvailability(ctx context.Context) error {
	client := q.BuildQuayClient(ctrllog.FromContext(ctx))
	_, err := client.GetRobotAccount(q.QuayOrganization, testRobotAccountName)
	return err
}

func (q *QuayAvailabilityProbe) AvailabilityGauge() prometheus.Gauge {
	return q.gauge
}
