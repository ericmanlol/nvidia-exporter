package collector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/woo/nvidia-exporter/internal/nvidia"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCollector_EmitsCorrectMetrics(t *testing.T) {
	mock := nvidia.NewMockProvider()
	mock.SetStats([]nvidia.GPUStats{
		{
			Index:            0,
			Name:             "Test GPU",
			UUID:             "GPU-test-uuid",
			UtilizationGPU:   50,
			UtilizationMem:   30,
			MemoryUsed:       4294967296,  // 4 GiB
			MemoryTotal:      10737418240, // 10 GiB
			Temperature:      65,
			PowerDraw:        200,
			PowerLimit:       300,
			FanSpeed:         70,
			ClockGraphics:    1800,
			ClockMemory:      9000,
			PerformanceState: 0,
		},
	})

	c := New(mock, time.Second, testLogger())

	// Trigger initial collection
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	go c.Run(ctx)
	time.Sleep(50 * time.Millisecond) // Allow collection to complete

	// Check a few key metrics
	expected := `
# HELP nvidia_gpu_utilization_ratio GPU utilization ratio (0-1)
# TYPE nvidia_gpu_utilization_ratio gauge
nvidia_gpu_utilization_ratio{gpu="0",name="Test GPU",uuid="GPU-test-uuid"} 0.5
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "nvidia_gpu_utilization_ratio"); err != nil {
		t.Errorf("unexpected metrics: %v", err)
	}

	expected = `
# HELP nvidia_memory_used_bytes Memory used in bytes
# TYPE nvidia_memory_used_bytes gauge
nvidia_memory_used_bytes{gpu="0",name="Test GPU",uuid="GPU-test-uuid"} 4.294967296e+09
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "nvidia_memory_used_bytes"); err != nil {
		t.Errorf("unexpected metrics: %v", err)
	}

	expected = `
# HELP nvidia_gpu_temperature_celsius GPU temperature in Celsius
# TYPE nvidia_gpu_temperature_celsius gauge
nvidia_gpu_temperature_celsius{gpu="0",name="Test GPU",uuid="GPU-test-uuid"} 65
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "nvidia_gpu_temperature_celsius"); err != nil {
		t.Errorf("unexpected metrics: %v", err)
	}
}

func TestCollector_HandlesProviderError(t *testing.T) {
	mock := nvidia.NewMockProvider()
	mock.SetError(errors.New("nvidia-smi not found"))

	c := New(mock, time.Second, testLogger())

	// Trigger initial collection (will fail)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	go c.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	// Error metric should be 1
	expected := `
# HELP nvidia_exporter_collection_error 1 if last collection failed, 0 otherwise
# TYPE nvidia_exporter_collection_error gauge
nvidia_exporter_collection_error 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "nvidia_exporter_collection_error"); err != nil {
		t.Errorf("unexpected error metric: %v", err)
	}
}

func TestCollector_ServesStaleDataOnError(t *testing.T) {
	mock := nvidia.NewMockProvider()
	mock.SetStats([]nvidia.GPUStats{
		{
			Index:            0,
			Name:             "Test GPU",
			UUID:             "GPU-test-uuid",
			UtilizationGPU:   50,
			UtilizationMem:   30,
			MemoryUsed:       4294967296,
			MemoryTotal:      10737418240,
			Temperature:      65,
			PowerDraw:        200,
			PowerLimit:       300,
			FanSpeed:         70,
			ClockGraphics:    1800,
			ClockMemory:      9000,
			PerformanceState: 0,
		},
	})

	c := New(mock, 50*time.Millisecond, testLogger())

	// Start collection loop
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	// Wait for first successful collection
	time.Sleep(30 * time.Millisecond)

	// Verify we have data
	metrics := collectMetrics(t, c)
	if len(metrics) == 0 {
		t.Fatal("expected metrics after first collection")
	}

	// Now make provider return error
	mock.SetError(errors.New("gpu disconnected"))

	// Wait for next collection (which will fail)
	time.Sleep(70 * time.Millisecond)

	// Error metric should be 1 but we should still have GPU metrics (stale data)
	expected := `
# HELP nvidia_exporter_collection_error 1 if last collection failed, 0 otherwise
# TYPE nvidia_exporter_collection_error gauge
nvidia_exporter_collection_error 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "nvidia_exporter_collection_error"); err != nil {
		t.Errorf("unexpected error metric: %v", err)
	}

	// GPU metrics should still be present (stale)
	expected = `
# HELP nvidia_gpu_utilization_ratio GPU utilization ratio (0-1)
# TYPE nvidia_gpu_utilization_ratio gauge
nvidia_gpu_utilization_ratio{gpu="0",name="Test GPU",uuid="GPU-test-uuid"} 0.5
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "nvidia_gpu_utilization_ratio"); err != nil {
		t.Errorf("stale data not preserved: %v", err)
	}
}

func collectMetrics(t *testing.T, c prometheus.Collector) []prometheus.Metric {
	t.Helper()
	ch := make(chan prometheus.Metric, 100)
	c.Collect(ch)
	close(ch)

	var metrics []prometheus.Metric
	for m := range ch {
		metrics = append(metrics, m)
	}
	return metrics
}
