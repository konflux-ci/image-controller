package metrics

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/konflux-ci/image-controller/pkg/quay"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func clearRepositoryTimes() {
	repositoryTimesForMetricsMu.Lock()
	defer repositoryTimesForMetricsMu.Unlock()
	for k := range repositoryTimesForMetrics {
		delete(repositoryTimesForMetrics, k)
	}
}

func TestSetRepositoryTimeIfAbsent(t *testing.T) {
	t.Cleanup(clearRepositoryTimes)

	t1 := time.Now()
	t2 := t1.Add(time.Second)

	SetRepositoryTimeIfAbsent("key1", t1)
	got, ok := GetRepositoryTime("key1")
	if !ok || !got.Equal(t1) {
		t.Fatalf("expected %v, got %v (ok=%v)", t1, got, ok)
	}

	SetRepositoryTimeIfAbsent("key1", t2)
	got, ok = GetRepositoryTime("key1")
	if !ok || !got.Equal(t1) {
		t.Fatalf("expected original time %v to be preserved, got %v", t1, got)
	}
}

func TestGetRepositoryTime(t *testing.T) {
	t.Cleanup(clearRepositoryTimes)

	_, ok := GetRepositoryTime("missing")
	if ok {
		t.Fatal("expected ok=false for missing key")
	}

	now := time.Now()
	SetRepositoryTimeIfAbsent("present", now)
	got, ok := GetRepositoryTime("present")
	if !ok || !got.Equal(now) {
		t.Fatalf("expected %v, got %v (ok=%v)", now, got, ok)
	}
}

func TestDeleteRepositoryTime(t *testing.T) {
	t.Cleanup(clearRepositoryTimes)

	DeleteRepositoryTime("missing")

	now := time.Now()
	SetRepositoryTimeIfAbsent("key1", now)
	DeleteRepositoryTime("key1")
	_, ok := GetRepositoryTime("key1")
	if ok {
		t.Fatal("expected key to be deleted")
	}
}

func TestRepositoryTimeConcurrency(t *testing.T) {
	t.Cleanup(clearRepositoryTimes)

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			key := "key"
			now := time.Now()
			SetRepositoryTimeIfAbsent(key, now)
			GetRepositoryTime(key)
			DeleteRepositoryTime(key)
		}(i)
	}
	wg.Wait()
}

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

	t.Run("Should set availability according to the probe", func(t *testing.T) {
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
