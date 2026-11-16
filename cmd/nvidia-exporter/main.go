package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/woo/nvidia-exporter/internal/amd"
	"github.com/woo/nvidia-exporter/internal/collector"
	"github.com/woo/nvidia-exporter/internal/intel"
	"github.com/woo/nvidia-exporter/internal/nvidia"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		slog.Error("exporter stopped", "error", err)
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
	flags := flag.NewFlagSet("nvidia-exporter", flag.ContinueOnError)
	var (
		listenAddress   = flags.String("listen-address", ":9835", "Address to listen on for HTTP requests")
		metricsPath     = flags.String("metrics-path", "/metrics", "Path under which to expose metrics")
		backend         = flags.String("backend", "nvidia", "GPU backend to collect from: nvidia (nvidia-smi), amd (amdgpu sysfs) or intel (intel_gpu_top)")
		nvidiaTimeout   = flags.Duration("nvidia-smi-timeout", collector.DefaultCollectionTimeout, "Timeout for each NVIDIA collection")
		nvidiaSMIPath   = flags.String("nvidia-smi-path", "nvidia-smi", "Path to nvidia-smi binary")
		intelGPUTopPath = flags.String("intel-gpu-top-path", "intel_gpu_top", "Path to intel_gpu_top binary (backend=intel)")
		intelDevice     = flags.String("intel-device", "", "intel_gpu_top device filter, e.g. drm:/dev/dri/renderD128 (backend=intel; default: first GPU)")
		intelHwmon      = flags.String("intel-hwmon", "auto", "Intel GPU hwmon directory for power/energy/temperature/fan (backend=intel): auto, off, or a path like /sys/class/drm/card1/device/hwmon/hwmon5")
		intelXPUSMI     = flags.String("intel-xpu-smi-path", "", "Path to xpu-smi; when set, GPU memory used/total is exported (backend=intel; needs root)")
		intelXPUDevice  = flags.String("intel-xpu-device", "", "xpu-smi device id for --intel-xpu-smi-path (backend=intel; default: the device at the selected GPU's PCI address)")
		amdSysfsRoot    = flags.String("amd-sysfs-root", "/sys", "sysfs mount point to read amdgpu devices from (backend=amd)")
		legacyMetrics   = flags.Bool("legacy-metrics", true, "Also export the vendor-prefixed nvidia_*/intel_* metrics that duplicate the common gpu_* metrics")
		collectInterval = flags.Duration("collect-interval", 10*time.Second, "Interval between metric collections (sampling period for backend=intel)")
		logLevel        = flags.String("log-level", "info", "Log level (debug, info, warn, error)")
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *collectInterval <= 0 {
		return fmt.Errorf("--collect-interval must be greater than zero")
	}
	if *nvidiaTimeout <= 0 {
		return fmt.Errorf("--nvidia-smi-timeout must be greater than zero")
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *backend != "nvidia" && *backend != "intel" && *backend != "amd" {
		return fmt.Errorf("unknown backend %q, want nvidia, amd or intel", *backend)
	}
	// Bind before starting workers: a startup failure must leave no collectors running.
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()

	// Setup logger
	level := parseLogLevel(*logLevel)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	logger.Info("starting nvidia-exporter",
		"backend", *backend,
		"listen_address", *listenAddress,
		"metrics_path", *metricsPath,
		"nvidia_smi_path", *nvidiaSMIPath,
		"nvidia_smi_timeout", *nvidiaTimeout,
		"intel_gpu_top_path", *intelGPUTopPath,
		"amd_sysfs_root", *amdSysfsRoot,
		"legacy_metrics", *legacyMetrics,
		"collect_interval", *collectInterval,
		"log_level", *logLevel,
	)

	// Create context that cancels on SIGINT/SIGTERM
	ctx, cancel := context.WithCancel(parent)
	var workers sync.WaitGroup
	start := func(fn func(context.Context)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			fn(ctx)
		}()
	}
	defer cancel()

	// Create the collector for the selected backend and start it
	reg := prometheus.NewRegistry()
	title := "NVIDIA Exporter"
	switch *backend {
	case "nvidia":
		provider := nvidia.NewSMIProvider(*nvidiaSMIPath)
		coll := collector.New(provider, *collectInterval, logger).WithTimeout(*nvidiaTimeout).SetLegacy(*legacyMetrics)
		reg.MustRegister(coll)
		start(coll.Run)
	case "intel":
		title = "Intel GPU Exporter"
		device := *intelDevice
		if device == "" {
			device = "default"
		}
		// Resolve every source to the same GPU so readings are never mixed.
		sel := intel.Select("/sys", *intelDevice, *intelHwmon, logger)
		provider := intel.NewGPUTopProvider(*intelGPUTopPath, sel.GPUTopDevice, *collectInterval, logger)
		// A sample is stale after three sampling periods plus a startup margin.
		coll := collector.NewIntel(provider, device, intel.SampleTimeout(*collectInterval)).SetLegacy(*legacyMetrics)
		if hw := openHwmon(sel.HwmonDir, logger); hw != nil {
			coll.WithHwmon(hw)
		}
		if *intelXPUSMI != "" {
			xpu := intel.NewXPU(*intelXPUSMI, *intelXPUDevice, sel.PCISlot, *collectInterval, logger)
			coll.WithXPU(xpu)
			start(xpu.Run)
			logger.Info("xpu-smi memory metrics enabled", "path", *intelXPUSMI, "device", *intelXPUDevice, "pci_slot", sel.PCISlot)
		}
		reg.MustRegister(coll)
		start(provider.Run)
	case "amd":
		title = "AMD GPU Exporter"
		cards := amd.Discover(*amdSysfsRoot)
		if len(cards) == 0 {
			logger.Warn("no amdgpu devices found; gpu_exporter_collection_error will be 1 until one appears", "sysfs_root", *amdSysfsRoot)
		}
		for _, c := range cards {
			logger.Info("found amdgpu device", "card", c.Index, "pci", c.PCISlot, "name", c.Name, "hwmon", c.HwmonDir)
		}
		reg.MustRegister(collector.NewAMD(*amdSysfsRoot))
	default:
		return fmt.Errorf("unknown backend %q", *backend)
	}

	workersDone := make(chan struct{})
	go func() { workers.Wait(); close(workersDone) }()

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
<head><title>%s</title></head>
<body>
<h1>%s</h1>
<p><a href="%s">Metrics</a></p>
</body>
</html>
`, title, title, *metricsPath)
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

	// Serve errors and shutdown signals both terminate the application.
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.Serve(listener) }()
	logger.Info("starting HTTP server", "address", listener.Addr())
	var serveErr error
	select {
	case serveErr = <-serverErr:
	case <-ctx.Done():
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		// Shutdown does not close active connections after its deadline.
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	if serveErr == nil {
		serveErr = <-serverErr
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	select {
	case <-workersDone:
	case <-shutdownCtx.Done():
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("wait for collectors: %w", shutdownCtx.Err()))
	}
	if shutdownErr == nil {
		logger.Info("shutdown complete")
	}
	return errors.Join(serveErr, shutdownErr)
}

// openHwmon opens the hwmon directory chosen by intel.Select. It returns nil
// (hwmon metrics disabled) if dir is empty or cannot be read.
func openHwmon(dir string, logger *slog.Logger) *intel.Hwmon {
	if dir == "" {
		return nil
	}
	hw := intel.NewHwmon(dir)
	if _, err := hw.Read(); err != nil {
		logger.Warn("hwmon not readable, hwmon metrics disabled", "dir", dir, "error", err)
		return nil
	}
	logger.Info("hwmon metrics enabled", "dir", dir)
	return hw
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
