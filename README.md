# nvidia-exporter

A Prometheus exporter for NVIDIA GPU metrics. Collects metrics via `nvidia-smi` and exposes them on an HTTP endpoint for Prometheus scraping.

## Requirements

- Go 1.22+
- NVIDIA GPU with drivers installed
- `nvidia-smi` available in PATH (or specify path with `--nvidia-smi-path`)

## Build

```bash
# Build
make build

# Run tests
make test

# Clean build artifacts
make clean
```

Or directly with Go:

```bash
go build -o bin/nvidia-exporter ./cmd/nvidia-exporter
```

## Usage

```bash
./bin/nvidia-exporter [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--listen-address` | `:9835` | Address to listen on for HTTP requests |
| `--metrics-path` | `/metrics` | Path under which to expose metrics |
| `--nvidia-smi-path` | `nvidia-smi` | Path to nvidia-smi binary |
| `--collect-interval` | `10s` | Interval between metric collections |
| `--log-level` | `info` | Log level (debug, info, warn, error) |

### Endpoints

| Endpoint | Description |
|----------|-------------|
| `/` | Landing page with link to metrics |
| `/metrics` | Prometheus metrics endpoint |
| `/health` | Health check (returns 200 OK) |

## Metrics

### GPU Metrics

All GPU metrics are labeled with `gpu` (index), `name`, and `uuid`.

| Metric | Type | Description |
|--------|------|-------------|
| `nvidia_gpu_utilization_ratio` | gauge | GPU utilization (0-1) |
| `nvidia_memory_utilization_ratio` | gauge | Memory utilization (0-1) |
| `nvidia_memory_used_bytes` | gauge | Memory used in bytes |
| `nvidia_memory_total_bytes` | gauge | Total memory in bytes |
| `nvidia_gpu_temperature_celsius` | gauge | GPU temperature in Celsius |
| `nvidia_gpu_power_draw_watts` | gauge | Power draw in Watts |
| `nvidia_gpu_power_limit_watts` | gauge | Power limit in Watts |
| `nvidia_gpu_fan_speed_ratio` | gauge | Fan speed (0-1) |
| `nvidia_gpu_clock_graphics_hz` | gauge | Graphics clock in Hz |
| `nvidia_gpu_clock_memory_hz` | gauge | Memory clock in Hz |
| `nvidia_gpu_performance_state` | gauge | Performance state (0=P0 max, 15=P15 min) |

### Exporter Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `nvidia_exporter_last_collection_timestamp_seconds` | gauge | Unix timestamp of last successful collection |
| `nvidia_exporter_collection_error` | gauge | 1 if last collection failed, 0 otherwise |

## systemd Unit Example

```ini
[Unit]
Description=NVIDIA GPU Prometheus Exporter
After=network.target

[Service]
Type=simple
User=nobody
ExecStart=/usr/local/bin/nvidia-exporter --listen-address=:9835
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

## Prometheus Configuration

```yaml
scrape_configs:
  - job_name: 'nvidia'
    static_configs:
      - targets: ['localhost:9835']
```

## License

MIT
