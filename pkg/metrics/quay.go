package metrics

import (
	"context"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

type QuayAvailabilityProbe struct {
	BuildQuayClient  func(logr.Logger) quay.QuayService
	QuayOrganization string
	gauge            prometheus.Gauge
}

func NewQuayAvailabilityProbe(clientBuilder func(logr.Logger) quay.QuayService, quayOrganization string) *QuayAvailabilityProbe {
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
	}
}

func (q *QuayAvailabilityProbe) CheckAvailability(ctx context.Context) error {
	client := q.BuildQuayClient(ctrllog.FromContext(ctx))
	_, err := client.GetAllRepositories(q.QuayOrganization)
	return err
}

func (q *QuayAvailabilityProbe) AvailabilityGauge() prometheus.Gauge {
	return q.gauge
}
