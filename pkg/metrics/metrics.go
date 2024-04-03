package metrics

import (
	"context"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"time"
)

const (
	MetricsNamespace = "redhat_appstudio"
	MetricsSubsystem = "imagecontroller"
)

var (
	HistogramBuckets                   = []float64{5, 10, 15, 20, 30, 60, 120, 300}
	ImageRepositoryProvisionTimeMetric = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: MetricsNamespace,
		Subsystem: MetricsSubsystem,
		Buckets:   HistogramBuckets,
		Name:      "image_repository_provision_time",
		Help:      "The time in seconds spent from the moment of Image repository provision request to Image repository is ready to use.",
	})

	ImageRepositoryProvisionFailureTimeMetric = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: MetricsNamespace,
		Subsystem: MetricsSubsystem,
		Buckets:   HistogramBuckets,
		Name:      "image_repository_provision_failure_time",
		Help:      "The time in seconds spent from the moment of Image repository provision request to Image repository failure.",
	})

	RepositoryTimesForMetrics = map[string]time.Time{}
)

func (m *ImageControllerMetrics) InitMetrics(registerer prometheus.Registerer) error {
	// controller metrics
	registerer.MustRegister(ImageRepositoryProvisionTimeMetric, ImageRepositoryProvisionFailureTimeMetric)
	// availability metrics
	for _, probe := range m.probes {
		if err := registerer.Register(probe.AvailabilityGauge()); err != nil {
			return fmt.Errorf("failed to register the availability metric: %w", err)
		}
	}
	return nil
}

// ImageControllerMetrics represents a collection of metrics to be registered on a
// Prometheus metrics registry for a image controller service.
type ImageControllerMetrics struct {
	probes []AvailabilityProbe
}

func NewImageControllerMetrics(probes []AvailabilityProbe) *ImageControllerMetrics {
	return &ImageControllerMetrics{probes: probes}
}

func (m *ImageControllerMetrics) StartMetrics(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	log := ctrllog.FromContext(ctx)
	log.Info("Starting image controller metrics")
	go func() {
		for {
			select {
			case <-ctx.Done(): // Shutdown if context is canceled
				log.Info("Shutting down metrics")
				ticker.Stop()
				return
			case <-ticker.C:
				m.checkProbes(ctx)
			}
		}
	}()
}

func (m *ImageControllerMetrics) checkProbes(ctx context.Context) {
	for _, probe := range m.probes {
		pingErr := probe.CheckAvailability(ctx)
		if pingErr != nil {
			log := ctrllog.FromContext(ctx)
			log.Error(pingErr, "Error checking availability probe", "probe", probe)
			probe.AvailabilityGauge().Set(0)
		} else {
			probe.AvailabilityGauge().Set(1)
		}
	}
}

// AvailabilityProbe represents a probe that checks the availability of a certain aspects of the service
type AvailabilityProbe interface {
	CheckAvailability(ctx context.Context) error
	AvailabilityGauge() prometheus.Gauge
}
