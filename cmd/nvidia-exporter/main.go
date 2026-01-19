package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/woo/nvidia-exporter/internal/collector"
	"github.com/woo/nvidia-exporter/internal/nvidia"
)

func main() {
	var (
		listenAddress   = flag.String("listen-address", ":9835", "Address to listen on for HTTP requests")
		metricsPath     = flag.String("metrics-path", "/metrics", "Path under which to expose metrics")
		nvidiaSMIPath   = flag.String("nvidia-smi-path", "nvidia-smi", "Path to nvidia-smi binary")
		collectInterval = flag.Duration("collect-interval", 10*time.Second, "Interval between metric collections")
		logLevel        = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	)
	flag.Parse()

	// Setup logger
	level := parseLogLevel(*logLevel)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	logger.Info("starting nvidia-exporter",
		"listen_address", *listenAddress,
		"metrics_path", *metricsPath,
		"nvidia_smi_path", *nvidiaSMIPath,
		"collect_interval", *collectInterval,
		"log_level", *logLevel,
	)

	// Create provider and collector
	provider := nvidia.NewSMIProvider(*nvidiaSMIPath)
	coll := collector.New(provider, *collectInterval, logger)

	// Register collector with prometheus
	reg := prometheus.NewRegistry()
	reg.MustRegister(coll)

	// Create HTTP server
	mux := http.NewServeMux()

	// Landing page
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>NVIDIA Exporter</title></head>
<body>
<h1>NVIDIA Exporter</h1>
<p><a href="%s">Metrics</a></p>
</body>
</html>
`, *metricsPath)
	})

	// Metrics endpoint
	mux.Handle(*metricsPath, promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})

	server := &http.Server{
		Addr:    *listenAddress,
		Handler: mux,
	}

	// Create context that cancels on SIGINT/SIGTERM
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start collector goroutine
	go coll.Run(ctx)

	// Start HTTP server goroutine
	go func() {
		logger.Info("starting HTTP server", "address", *listenAddress)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
			cancel()
		}
	}()

	// Wait for signal
	sig := <-sigCh
	logger.Info("received signal, shutting down", "signal", sig)

	// Cancel collector context
	cancel()

	// Graceful shutdown of HTTP server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	}

	logger.Info("shutdown complete")
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
