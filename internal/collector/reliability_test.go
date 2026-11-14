package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/woo/nvidia-exporter/internal/nvidia"
)

type gatherFunc func(context.Context) ([]nvidia.GPUStats, error)

func (f gatherFunc) Gather(ctx context.Context) ([]nvidia.GPUStats, error) { return f(ctx) }

func metricValue(t *testing.T, c prometheus.Collector, name string) float64 {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == name && len(family.Metric) == 1 {
			return family.Metric[0].GetGauge().GetValue()
		}
	}
	t.Fatalf("expected one metric for %s", name)
	return 0
}

func assertCollectionError(t *testing.T, c *Collector, want float64) {
	t.Helper()
	for _, name := range []string{"gpu_exporter_collection_error", "nvidia_exporter_collection_error"} {
		if got := metricValue(t, c, name); got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
}

func TestCollectorHealthAndRecovery(t *testing.T) {
	mock := nvidia.NewMockProvider()
	mock.SetStats([]nvidia.GPUStats{{UUID: "GPU-test", UtilizationGPU: 50}})
	now := time.Unix(1700000000, 0)
	c := New(mock, time.Second, testLogger()).WithTimeout(time.Second)
	c.now = func() time.Time { return now }
	assertCollectionError(t, c, 1)
	if n := testutil.CollectAndCount(c, "gpu_exporter_last_collection_timestamp_seconds"); n != 0 {
		t.Fatal("timestamp present before first success")
	}
	c.collect(context.Background())
	assertCollectionError(t, c, 0)
	last := metricValue(t, c, "gpu_exporter_last_collection_timestamp_seconds")
	now = now.Add(4 * time.Second)
	assertCollectionError(t, c, 0) // Exactly at the freshness deadline.
	now = now.Add(time.Nanosecond)
	assertCollectionError(t, c, 1)
	if got := metricValue(t, c, "gpu_utilization_ratio"); got != .5 {
		t.Errorf("cached utilization = %v", got)
	}
	if got := metricValue(t, c, "gpu_exporter_last_collection_timestamp_seconds"); got != last {
		t.Errorf("timestamp advanced on stale data")
	}
	mock.SetError(errors.New("disconnected"))
	c.collect(context.Background())
	assertCollectionError(t, c, 1)
	if got := metricValue(t, c, "gpu_exporter_last_collection_timestamp_seconds"); got != last {
		t.Errorf("timestamp advanced on failure")
	}
	mock.SetError(nil)
	mock.SetStats([]nvidia.GPUStats{{UUID: "GPU-test", UtilizationGPU: 75}})
	c.collect(context.Background())
	assertCollectionError(t, c, 0)
	if got := metricValue(t, c, "gpu_utilization_ratio"); got != .75 {
		t.Errorf("recovered utilization = %v", got)
	}
	if got := metricValue(t, c, "gpu_exporter_last_collection_timestamp_seconds"); got <= last {
		t.Errorf("timestamp did not advance on recovery")
	}
	c.SetLegacy(false)
	now = now.Add(time.Minute)
	if got := metricValue(t, c, "gpu_exporter_collection_error"); got != 1 {
		t.Error("common health must work with legacy disabled")
	}
}

func TestCollectorTimeoutKeepsScrapesResponsive(t *testing.T) {
	for _, haveSample := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial hang", true: "hang after success"}[haveSample], func(t *testing.T) {
			entered := make(chan struct{})
			provider := gatherFunc(func(ctx context.Context) ([]nvidia.GPUStats, error) {
				close(entered)
				<-ctx.Done()
				return nil, ctx.Err()
			})
			c := New(provider, time.Second, testLogger()).WithTimeout(100 * time.Millisecond)
			if haveSample {
				mock := nvidia.NewMockProvider()
				mock.SetStats([]nvidia.GPUStats{{UUID: "GPU-test", UtilizationGPU: 50}})
				c.provider = mock
				c.collect(context.Background())
				c.provider = provider
			}
			before := c.lastUpdate
			done := make(chan struct{})
			go func() { defer close(done); c.collect(context.Background()) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("provider not called")
			}
			scraped := make(chan struct{})
			go func() { defer close(scraped); testutil.CollectAndCount(c) }()
			select {
			case <-scraped:
			case <-time.After(time.Second):
				t.Error("scrape blocked behind collection")
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("collection did not time out")
			}
			assertCollectionError(t, c, 1)
			if !errors.Is(c.lastError, context.DeadlineExceeded) {
				t.Errorf("error = %v", c.lastError)
			}
			if !c.lastUpdate.Equal(before) {
				t.Error("timeout changed last successful timestamp")
			}
			if haveSample && metricValue(t, c, "gpu_utilization_ratio") != .5 {
				t.Error("lost cached sample")
			}
		})
	}
}

func TestCollectorRunStopsDuringCollection(t *testing.T) {
	entered := make(chan struct{})
	provider := gatherFunc(func(ctx context.Context) ([]nvidia.GPUStats, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	c := New(provider, time.Hour, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("collector leaked after cancellation")
		}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not called")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop during collection")
	}
}

func TestCollectorRunRetriesAfterTimeout(t *testing.T) {
	retry := make(chan struct{})
	release := make(chan struct{})
	recovered := make(chan struct{})
	calls := 0
	provider := gatherFunc(func(ctx context.Context) ([]nvidia.GPUStats, error) {
		calls++
		switch calls {
		case 1:
			<-ctx.Done()
			return nil, ctx.Err()
		case 2:
			close(retry)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case 4:
			// Reaching this call proves a success after the retry has been committed.
			close(recovered)
		}
		return []nvidia.GPUStats{{UUID: "GPU-test", UtilizationGPU: 75}}, nil
	})
	c := New(provider, 10*time.Millisecond, testLogger()).WithTimeout(200 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not stop")
		}
	})
	select {
	case <-retry:
	case <-time.After(5 * time.Second):
		t.Fatal("polling did not retry after timeout")
	}
	close(release)
	select {
	case <-recovered:
	case <-time.After(5 * time.Second):
		t.Fatal("polling did not recover")
	}
	assertCollectionError(t, c, 0)
	if got := metricValue(t, c, "gpu_utilization_ratio"); got != .75 {
		t.Errorf("recovered utilization = %v", got)
	}
}
