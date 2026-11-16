package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/woo/nvidia-exporter/internal/nvidia"
)

func TestCollectionDuration(t *testing.T) {
	const metric = "nvidia_exporter_collection_duration_seconds"
	for _, firstFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial success", true: "initial failure"}[firstFails], func(t *testing.T) {
			now := time.Unix(1700000000, 0)
			elapsed := 250 * time.Millisecond
			var gatherErr error
			if firstFails {
				gatherErr = errors.New("GPU unavailable")
			}
			provider := gatherFunc(func(context.Context) ([]nvidia.GPUStats, error) {
				now = now.Add(elapsed)
				return nil, gatherErr
			})
			c := New(provider, time.Second, testLogger())
			c.now = func() time.Time { return now }
			if n := testutil.CollectAndCount(c, metric); n != 0 {
				t.Fatal("duration present before the first completed attempt")
			}
			c.collect(context.Background())
			if got := metricValue(t, c, metric); got != 0.25 {
				t.Errorf("first duration = %v, want 0.25", got)
			}
			// A failed attempt updates duration even though the last-success time
			// and cached GPU values must not advance.
			gatherErr = errors.New("GPU disconnected")
			elapsed = 750 * time.Millisecond
			c.collect(context.Background())
			if got := metricValue(t, c, metric); got != 0.75 {
				t.Errorf("failed duration = %v, want 0.75", got)
			}
			now = now.Add(time.Hour)
			if got := metricValue(t, c, metric); got != 0.75 {
				t.Errorf("scraping changed duration = %v, want 0.75", got)
			}
			c.SetLegacy(false)
			if n := testutil.CollectAndCount(c, metric); n != 0 {
				t.Fatal("legacy duration emitted with legacy metrics disabled")
			}
		})
	}
}

func TestCollectionDurationKeepsCompletedAttemptWhileCollecting(t *testing.T) {
	now := time.Unix(1700000000, 0)
	c := New(gatherFunc(func(context.Context) ([]nvidia.GPUStats, error) {
		now = now.Add(250 * time.Millisecond)
		return nil, nil
	}), time.Second, testLogger())
	c.now = func() time.Time { return now }
	c.collect(context.Background())

	entered := make(chan struct{})
	release := make(chan struct{})
	c.provider = gatherFunc(func(ctx context.Context) ([]nvidia.GPUStats, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		now = now.Add(750 * time.Millisecond)
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.collect(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("collection did not stop")
		}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("next attempt did not start")
	}
	const metric = "nvidia_exporter_collection_duration_seconds"
	if got := metricValue(t, c, metric); got != 0.25 {
		t.Errorf("duration while collecting = %v, want previous attempt's 0.25", got)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("next attempt did not complete")
	}
	if got := metricValue(t, c, metric); got != 0.75 {
		t.Errorf("duration after completion = %v, want 0.75", got)
	}
}
