package intel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func streamTool(t *testing.T, script string) string {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	path := filepath.Join(t.TempDir(), "intel_gpu_top")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitIntel(t *testing.T, description string, ready func() bool) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for !ready() {
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatalf("timed out waiting for %s", description)
		}
	}
}

func TestProviderRecoversFromSilentStream(t *testing.T) {
	for _, mode := range []string{"before first sample", "after sample", "partial sample"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			pids := filepath.Join(dir, "pids")
			t.Setenv("INTEL_TEST_PIDS", pids)
			t.Setenv("INTEL_TEST_STARTED", filepath.Join(dir, "started"))
			t.Setenv("INTEL_TEST_MODE", mode)
			tool := streamTool(t, `echo $$ >> "$INTEL_TEST_PIDS"
if [ ! -e "$INTEL_TEST_STARTED" ]; then
  touch "$INTEL_TEST_STARTED"
  case "$INTEL_TEST_MODE" in
    "after sample") printf '[{"frequency":{"actual":100}},' ;;
    "partial sample") printf '[{"frequency":' ;;
  esac
  exec sleep 60
fi
printf '[{"frequency":{"actual":900}},'
exec sleep 60
`)
			p := NewGPUTopProvider(tool, "", time.Second, testLogger())
			p.sampleTimeout = 250 * time.Millisecond
			// Keep the error visible long enough to assert the failure transition.
			p.restartDelay = 100 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); p.Run(ctx) }()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("provider did not stop after cancellation")
				}
			})
			if mode == "after sample" {
				waitIntel(t, "initial sample", func() bool {
					s, _, err := p.Latest()
					return err == nil && s.FrequencyActual == 100
				})
			}
			waitIntel(t, "inactivity error", func() bool {
				_, _, err := p.Latest()
				return err != nil && strings.Contains(err.Error(), "no complete intel_gpu_top sample")
			})
			waitIntel(t, "sample from restarted process", func() bool {
				s, _, err := p.Latest()
				return err == nil && s.FrequencyActual == 900
			})
			b, err := os.ReadFile(pids)
			if err != nil {
				t.Fatal(err)
			}
			ids := strings.Fields(string(b))
			if len(ids) < 2 {
				t.Fatalf("process starts = %q, want at least two", b)
			}
			pid, err := strconv.Atoi(ids[0])
			if err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
				t.Fatalf("stalled child %d was not reaped: %v", pid, err)
			}
		})
	}
}

func TestProviderResetsDeadlineForCompleteSamples(t *testing.T) {
	tool := streamTool(t, `printf '['
for n in 1 2 3 4 5 6 7 8 9 10; do
  printf '{"frequency":{"actual":%s}},' "$n"
  sleep 0.05
done
exec sleep 60
`)
	p := NewGPUTopProvider(tool, "", time.Second, testLogger())
	p.sampleTimeout = 300 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.runOnce(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("stream reader did not stop after cancellation")
		}
	})
	waitIntel(t, "tenth sample beyond the initial deadline", func() bool {
		s, _, err := p.Latest()
		return err == nil && s.FrequencyActual == 10
	})
	select {
	case err := <-done:
		// Restore the result for cleanup's join.
		done <- err
		t.Fatalf("stream ended while samples were arriving: %v", err)
	default:
	}
}

func TestProviderCancellationBeforeFirstSample(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	t.Setenv("INTEL_TEST_READY", ready)
	tool := streamTool(t, "touch \"$INTEL_TEST_READY\"\nexec sleep 60\n")
	p := NewGPUTopProvider(tool, "", time.Hour, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	waitIntel(t, "silent child startup", func() bool {
		_, err := os.Stat(ready)
		return err == nil
	})
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked on a silent stream")
	}
}

func TestSampleTimeout(t *testing.T) {
	for _, tc := range []struct{ interval, want time.Duration }{
		{time.Millisecond, 5300 * time.Millisecond},
		{time.Second, 8 * time.Second},
		{10 * time.Second, 35 * time.Second},
		{time.Duration(1<<63 - 1), time.Duration(1<<63 - 1)},
	} {
		t.Run(fmt.Sprint(tc.interval), func(t *testing.T) {
			if got := SampleTimeout(tc.interval); got != tc.want {
				t.Errorf("SampleTimeout(%v) = %v, want %v", tc.interval, got, tc.want)
			}
		})
	}
}
