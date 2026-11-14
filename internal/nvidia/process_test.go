package nvidia

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// The test executable doubles as a portable fake nvidia-smi. TestMain handles
// the mode before testing parses the query flags supplied by Gather.
func TestMain(m *testing.M) {
	switch os.Getenv("NVIDIA_EXPORTER_TEST_SMI") {
	case "success":
		if len(os.Args) != 3 || !strings.HasPrefix(os.Args[1], "--query-gpu=index,name,uuid,") || os.Args[2] != "--format=csv,noheader,nounits" {
			os.Exit(2)
		}
		b, err := os.ReadFile("../../testdata/single-gpu.csv")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Stdout.Write(b)
		os.Exit(0)
	case "failure":
		fmt.Fprintln(os.Stderr, "GPU disconnected")
		os.Exit(3)
	case "hang":
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestGatherCommand(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := NewSMIProvider(exe)
	t.Run("success", func(t *testing.T) {
		t.Setenv("NVIDIA_EXPORTER_TEST_SMI", "success")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stats, err := p.Gather(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(stats) != 1 || stats[0].UtilizationGPU != 45 || stats[0].MemoryUsed != 4294967296 {
			t.Fatalf("unexpected stats: %+v", stats)
		}
	})
	t.Run("failure", func(t *testing.T) {
		t.Setenv("NVIDIA_EXPORTER_TEST_SMI", "failure")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := p.Gather(ctx); err == nil || !strings.Contains(err.Error(), "GPU disconnected") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		t.Setenv("NVIDIA_EXPORTER_TEST_SMI", "hang")
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := p.Gather(ctx); done <- err }()
		select {
		case err := <-done:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("hung child was not reaped after deadline")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		t.Setenv("NVIDIA_EXPORTER_TEST_SMI", "hang")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := p.Gather(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	})
}
