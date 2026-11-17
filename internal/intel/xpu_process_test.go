package intel

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestXPUCommandBoundsInheritedPipes(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		for _, mode := range []string{"cancel", "exit"} {
			t.Run(stream+"/"+mode, func(t *testing.T) {
				if _, err := exec.LookPath("sh"); err != nil {
					t.Skip("sh not available")
				}
				dir := t.TempDir()
				pidFile := filepath.Join(dir, "child.pid")
				tool := filepath.Join(dir, "xpu-smi")
				redirect := ">/dev/null"
				if stream == "stdout" {
					redirect = "2>/dev/null"
				}
				script := "#!/bin/sh\nsleep 60 " + redirect + " &\necho $! > \"$XPU_TEST_CHILD_PID\"\n"
				if mode == "cancel" {
					script += "wait\n"
				}
				if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("XPU_TEST_CHILD_PID", pidFile)
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				var child *os.Process
				finished := false
				t.Cleanup(func() {
					cancel()
					if child != nil {
						_ = child.Kill()
						_ = child.Release()
					}
					if !finished {
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("XPU command did not stop during cleanup")
						}
					}
				})
				x := NewXPU(tool, "0", "", time.Second, testLogger())
				go func() { _, err := x.run(ctx, "stats", "-j"); done <- err }()
				waitIntel(t, "descendant startup", func() bool {
					b, err := os.ReadFile(pidFile)
					if err != nil {
						return false
					}
					pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
					if err != nil || pid <= 0 {
						return false
					}
					child, err = os.FindProcess(pid)
					return err == nil
				})
				if mode == "cancel" {
					cancel()
				}
				select {
				case err := <-done:
					finished = true
					want := exec.ErrWaitDelay
					if mode == "cancel" {
						want = context.Canceled
					}
					if !errors.Is(err, want) {
						t.Errorf("run error = %v, want %v", err, want)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("XPU command blocked on descendant output pipes")
				}
			})
		}
	}
}
