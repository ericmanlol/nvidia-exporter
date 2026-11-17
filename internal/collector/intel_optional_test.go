package collector

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/woo/nvidia-exporter/internal/intel"
)

func TestIntelCollectorOmitsAbsentReadingsAndPreservesZero(t *testing.T) {
	s, err := intel.ParseSample([]byte(`{"period":{"duration":1000},"power":{"GPU":0}}`))
	if err != nil {
		t.Fatal(err)
	}
	mock := intel.NewMockProvider()
	mock.SetSample(s, time.Now())
	c := NewIntel(mock, "default", time.Minute)
	if got := metricValue(t, c, "gpu_power_draw_watts"); got != 0 {
		t.Errorf("zero power = %v, want 0", got)
	}
	if got := metricValue(t, c, "gpu_exporter_collection_error"); got != 0 {
		t.Errorf("valid partial sample health = %v, want 0", got)
	}
	for _, name := range []string{
		"gpu_utilization_ratio", "gpu_clock_graphics_hz",
		"intel_gpu_frequency_actual_hz", "intel_gpu_frequency_requested_hz",
		"intel_gpu_interrupts_per_second", "intel_gpu_rc6_ratio",
		"intel_gpu_imc_read_bytes_per_second", "intel_gpu_imc_write_bytes_per_second",
		"intel_gpu_engine_busy_ratio", "intel_gpu_engine_semaphore_wait_ratio", "intel_gpu_engine_wait_ratio",
	} {
		if n := testutil.CollectAndCount(c, name); n != 0 {
			t.Errorf("%s: got %d series without a reading", name, n)
		}
	}

	s, err = intel.ParseSample([]byte(`{"period":{"duration":1000},"frequency":{"actual":0,"requested":0},"interrupts":{"count":0},"engines":{"Video/0":{"busy":0}}}`))
	if err != nil {
		t.Fatal(err)
	}
	mock.SetSample(s, time.Now())
	for _, name := range []string{
		"gpu_utilization_ratio", "gpu_clock_graphics_hz", "intel_gpu_frequency_actual_hz",
		"intel_gpu_frequency_requested_hz", "intel_gpu_interrupts_per_second", "intel_gpu_engine_busy_ratio",
	} {
		if got := metricValue(t, c, name); got != 0 {
			t.Errorf("%s = %v, want an exported zero", name, got)
		}
	}
	for _, name := range []string{"intel_gpu_engine_semaphore_wait_ratio", "intel_gpu_engine_wait_ratio"} {
		if n := testutil.CollectAndCount(c, name); n != 0 {
			t.Errorf("%s: got %d series for an absent engine counter", name, n)
		}
	}
	c.SetLegacy(false)
	if got := metricValue(t, c, "gpu_clock_graphics_hz"); got != 0 {
		t.Errorf("common zero clock without legacy = %v", got)
	}
	if n := testutil.CollectAndCount(c, "intel_gpu_frequency_actual_hz"); n != 0 {
		t.Error("legacy clock emitted when disabled")
	}
}
