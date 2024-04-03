package metrics

import (
	"context"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redhat-appstudio/image-controller/pkg/quay"
	"testing"
)

func TestRegisterMetrics(t *testing.T) {
	t.Run("Should register and record availability metric", func(t *testing.T) {
		probe := NewQuayAvailabilityProbe(getTestClient, quay.TestQuayOrg)
		buildMetrics := NewImageControllerMetrics([]AvailabilityProbe{probe})
		registry := prometheus.NewPedanticRegistry()
		err := buildMetrics.InitMetrics(registry)
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
	return &quay.TestQuayClient{}
}
