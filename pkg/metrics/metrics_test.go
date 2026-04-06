package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/konflux-ci/image-controller/pkg/quay"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestInitMetrics(t *testing.T) {
	getTestClient := func() (quay.QuayService, error) {
		quay.ResetTestQuayClient()
		return &quay.TestQuayClient{}, nil
	}

	t.Run("Should register and record availability metric", func(t *testing.T) {
		probe, err := NewQuayAvailabilityProbe(context.Background(), getTestClient, quay.TestQuayOrg)
		if err != nil {
			t.Errorf("Fail to register probe: %v", err)
		}
		metrics := NewImageControllerMetrics([]AvailabilityProbe{probe})
		registry := prometheus.NewPedanticRegistry()
		err = metrics.InitMetrics(registry)
		if err != nil {
			t.Errorf("Fail to register metrics: %v", err)
		}

		metrics.checkProbes(context.Background())

		count, err := testutil.GatherAndCount(registry, "redhat_appstudio_imagecontroller_global_quay_app_available")
		if err != nil {
			t.Errorf("Fail to gather metrics: %v", err)
		}

		if count != 1 {
			t.Errorf("Fail to record metric. Expected 1 got : %v", count)
		}
	})
}

func TestStartMetrics(t *testing.T) {
	RegisterTestingT(t)

	t.Run("Should start and stop metrics", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		metrics := NewImageControllerMetrics([]AvailabilityProbe{})
		metrics.StartMetrics(ctx, 10*time.Millisecond)
		cancel()
	})

	t.Run("Should set avaliability according to the probe", func(t *testing.T) {
		quay.ResetTestQuayClient()
		testQuayClient := &quay.TestQuayClient{}
		getTestClient := func() (quay.QuayService, error) {
			return testQuayClient, nil
		}

		ctx, cancel := context.WithCancel(context.Background())
		probe, err := NewQuayAvailabilityProbe(ctx, getTestClient, quay.TestQuayOrg)
		if err != nil {
			t.Errorf("Failed to register probe: %v", err)
		}

		metrics := NewImageControllerMetrics([]AvailabilityProbe{probe})
		metrics.StartMetrics(ctx, 10*time.Millisecond)

		// Wait metrics to report success
		Eventually(func() bool {
			return testutil.ToFloat64(probe.gauge) == 1.0
		}, time.Second, 10*time.Millisecond).WithTimeout(time.Second).Should(BeTrue())

		// Make the probe fail and wait metrics to report failure
		quay.GetRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
			Expect(organization).To(Equal(quay.TestQuayOrg))
			return nil, errors.New("error")
		}
		Eventually(func() bool {
			return testutil.ToFloat64(probe.gauge) == 0.0
		}, time.Second, 10*time.Millisecond).WithTimeout(time.Second).Should(BeTrue())

		// Restore the probe and wait metrics to report success again
		quay.GetRobotAccountFunc = func(organization, robotName string) (*quay.RobotAccount, error) {
			Expect(organization).To(Equal(quay.TestQuayOrg))
			return &quay.RobotAccount{}, nil
		}
		Eventually(func() bool {
			return testutil.ToFloat64(probe.gauge) == 1.0
		}, time.Second, 10*time.Millisecond).WithTimeout(time.Second).Should(BeTrue())

		cancel()
	})
}
