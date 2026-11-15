package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCustomMetricsPath(t *testing.T) {
	for _, metricsPath := range []string{"/telemetry/metrics", "/telemetry/"} {
		t.Run(metricsPath, func(t *testing.T) {
			address := unusedAddress(t)
			sysRoot := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- run(ctx, []string{
					"--backend=amd", "--amd-sysfs-root=" + sysRoot,
					"--listen-address=" + address, "--metrics-path=" + metricsPath,
				})
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("run: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Error("server did not stop")
				}
			})
			client := &http.Client{Timeout: time.Second}
			t.Cleanup(client.CloseIdleConnections)
			waitFor(t, "custom metrics endpoint", func() bool {
				resp, err := client.Get("http://" + address + metricsPath)
				if err != nil {
					return false
				}
				defer resp.Body.Close()
				b, err := io.ReadAll(resp.Body)
				return err == nil && resp.StatusCode == http.StatusOK &&
					strings.Contains(string(b), `gpu_exporter_collection_error{vendor="amd"} 1`)
			})
			for _, tc := range []struct {
				path   string
				status int
				body   string
			}{
				{"/", http.StatusOK, `href="` + metricsPath + `"`},
				{"/health", http.StatusOK, "OK"},
				{"/metrics", http.StatusNotFound, "404"},
			} {
				resp, err := client.Get("http://" + address + tc.path)
				if err != nil {
					t.Fatal(err)
				}
				b, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil || resp.StatusCode != tc.status || !strings.Contains(string(b), tc.body) {
					t.Errorf("GET %s: status=%d body=%q err=%v", tc.path, resp.StatusCode, b, err)
				}
			}
		})
	}
}
