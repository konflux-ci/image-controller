package metrics

import (
	"context"
	"fmt"

	"github.com/konflux-ci/image-controller/pkg/quay"
	"github.com/prometheus/client_golang/prometheus"
)

type QuayAvailabilityProbe struct {
	BuildQuayClient  func() (quay.QuayService, error)
	QuayOrganization string
	gauge            prometheus.Gauge
}

const testRobotAccountName = "robot_konflux_api_healthcheck"

func NewQuayAvailabilityProbe(ctx context.Context, clientBuilder func() (quay.QuayService, error), quayOrganization string) (*QuayAvailabilityProbe, error) {
	client, err := clientBuilder()
	if err != nil {
		return nil, fmt.Errorf("could not create quay client: %w", err)
	}

	_, err = client.CreateRobotAccount(quayOrganization, testRobotAccountName)
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
	client, err := q.BuildQuayClient()
	if err != nil {
		return fmt.Errorf("could not create quay client: %w", err)
	}
	_, err = client.GetRobotAccount(q.QuayOrganization, testRobotAccountName)
	return err
}

func (q *QuayAvailabilityProbe) AvailabilityGauge() prometheus.Gauge {
	return q.gauge
}
