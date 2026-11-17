package collector

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/woo/nvidia-exporter/internal/intel"
)

func TestIntelHwmonPartialReadAndRecovery(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		t.Run(map[bool]string{true: "legacy", false: "common"}[legacy], func(t *testing.T) {
			dir := t.TempDir()
			c := NewIntel(intel.NewMockProvider(), "default", time.Minute).
				WithHwmon(intel.NewHwmon(dir)).SetLegacy(legacy)
			if got := metricValue(t, c, "intel_exporter_hwmon_error"); got != 1 {
				t.Fatalf("initial health = %v, want 1", got)
			}
			if err := os.WriteFile(filepath.Join(dir, "temp1_input"), []byte("45000\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if got := metricValue(t, c, "gpu_temperature_celsius"); got != 45 {
				t.Errorf("temperature = %v, want 45", got)
			}
			if got := metricValue(t, c, "intel_exporter_hwmon_error"); got != 0 {
				t.Errorf("temperature-only health = %v, want 0", got)
			}
			if n := testutil.CollectAndCount(c, "gpu_energy_joules_total", "intel_gpu_hwmon_energy_joules_total",
				"gpu_power_draw_watts", "intel_gpu_hwmon_power_watts"); n != 0 {
				t.Errorf("energy/power emitted without an energy reading: %d metrics", n)
			}
			wantLegacy := 0
			if legacy {
				wantLegacy = 1
			}
			if n := testutil.CollectAndCount(c, "intel_gpu_hwmon_temperature_celsius"); n != wantLegacy {
				t.Errorf("legacy temperature count = %d, want %d", n, wantLegacy)
			}
			if err := os.Remove(filepath.Join(dir, "temp1_input")); err != nil {
				t.Fatal(err)
			}
			if got := metricValue(t, c, "intel_exporter_hwmon_error"); got != 1 {
				t.Errorf("all sensors unavailable: health = %v, want 1", got)
			}
			if err := os.WriteFile(filepath.Join(dir, "energy1_input"), []byte("0\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if got := metricValue(t, c, "gpu_energy_joules_total"); got != 0 {
				t.Errorf("zero counter = %v, want 0", got)
			}
			if got := metricValue(t, c, "intel_exporter_hwmon_error"); got != 0 {
				t.Errorf("recovered energy-only health = %v, want 0", got)
			}
		})
	}
}
