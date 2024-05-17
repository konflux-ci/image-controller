package metrics

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/image-controller/pkg/quay"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRegisterMetrics(t *testing.T) {
	t.Run("Should register and record availability metric", func(t *testing.T) {
		probe, err := NewQuayAvailabilityProbe(context.Background(), getTestClient, quay.TestQuayOrg)
		if err != nil {
			t.Errorf("Fail to register probe: %v", err)
		}
		buildMetrics := NewImageControllerMetrics([]AvailabilityProbe{probe})
		registry := prometheus.NewPedanticRegistry()
		err = buildMetrics.InitMetrics(registry)
		if err != nil {
			t.Errorf("Fail to register metrics: %v", err)
		}

		buildMetrics.checkProbes(context.Background())

		count, err := testutil.GatherAndCount(registry, "redhat_appstudio_imagecontroller_global_quay_app_available")
		if err != nil {
			t.Errorf("Fail to gather metrics: %v", err)
		}

		if count != 1 {
			t.Errorf("Fail to record metric. Expected 1 got : %v", count)
		}
	})
}

func getTestClient(logger logr.Logger) quay.QuayService {
	quay.ResetTestQuayClient()
	return &quay.TestQuayClient{}
}
