package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var exporterBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "exporter-command-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	exporterBinary = filepath.Join(dir, "nvidia-exporter")
	build := exec.Command("go", "build", "-race", "-o", exporterBinary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build exporter: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func command(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, exporterBinary, args...)
	cmd.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0")
	return cmd
}

func TestCommandRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, flag, want string }{
		{"zero interval", "--collect-interval=0", "--collect-interval must be greater than zero"},
		{"negative interval", "--collect-interval=-1s", "--collect-interval must be greater than zero"},
		{"zero timeout", "--nvidia-smi-timeout=0", "--nvidia-smi-timeout must be greater than zero"},
		{"negative timeout", "--nvidia-smi-timeout=-1s", "--nvidia-smi-timeout must be greater than zero"},
		{"unknown backend", "--backend=unknown", "unknown backend"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := command(t, tc.flag, "--listen-address=invalid-address").CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 {
				t.Fatalf("exit = %v, output = %s", err, out)
			}
			if !strings.Contains(string(out), tc.want) || strings.Contains(string(out), "panic:") {
				t.Fatalf("output = %s, want %q", out, tc.want)
			}
		})
	}
}

func TestCommandHelp(t *testing.T) {
	out, err := command(t, "--help").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "nvidia-smi-timeout") {
		t.Fatalf("help: %v\n%s", err, out)
	}
}

func TestCommandOccupiedPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	out, err := command(t, "--backend=amd", "--listen-address="+listener.Addr().String()).CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 {
		t.Fatalf("exit = %v, output = %s", err, out)
	}
	if !strings.Contains(string(out), "listen:") {
		t.Fatalf("missing listen diagnostic: %s", out)
	}
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

// Retry readiness checks under a deadline instead of assuming startup finishes
// within a fixed sleep. The condition, rather than elapsed time, wins.
func waitFor(t *testing.T, description string, ready func() bool) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatalf("timed out waiting for %s", description)
		}
	}
}

func TestCommandShutdownReapsHungNvidia(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	tool := filepath.Join(dir, "nvidia-smi")
	// exec gives the sleeping process the same PID, with no background children.
	script := "#!/bin/sh\necho $$ > \"$EXPORTER_TEST_PID_FILE\"\nexec sleep 60\n"
	if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	address := unusedAddress(t)
	cmd := command(t, "--listen-address="+address, "--nvidia-smi-path="+tool, "--nvidia-smi-timeout=1m")
	cmd.Env = append(cmd.Env, "EXPORTER_TEST_PID_FILE="+pidFile)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	finished := false
	var child *os.Process
	t.Cleanup(func() {
		if !finished {
			cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				cmd.Process.Kill()
				<-done
			}
		}
		if child != nil {
			// Also clean up the fake on a failing regression, when the exporter may
			// have exited without reaping it. The process handle avoids PID reuse.
			child.Kill()
			child.Release()
		}
	})
	client := &http.Client{Timeout: 200 * time.Millisecond}
	defer client.CloseIdleConnections()
	waitFor(t, "HTTP readiness", func() bool {
		resp, err := client.Get("http://" + address + "/health")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	var childPID int
	waitFor(t, "NVIDIA child startup", func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		childPID, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && childPID > 0
	})
	child, err := os.FindProcess(childPID)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get("http://" + address + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `gpu_exporter_collection_error{vendor="nvidia"} 1`) {
		t.Fatalf("initial hung collection advertised healthy:\n%s", body)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		finished = true
		if err != nil {
			t.Fatalf("shutdown exit = %v\n%s", err, output.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown blocked on NVIDIA command")
	}
	if err := syscall.Kill(childPID, 0); err != syscall.ESRCH {
		t.Fatalf("NVIDIA child %d was not reaped: %v", childPID, err)
	}
}
