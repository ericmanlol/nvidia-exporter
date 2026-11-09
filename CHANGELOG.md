# Changelog

Release notes for GitHub releases are taken from the section matching the tag
(`## v0.3.0 ...`). Add the new section here before tagging.

## v0.3.0 - 2026-11-08

### Added
- **Intel hwmon sensors** (`--backend=intel`). `intel_gpu_top` reports no power on discrete Arc cards, so the exporter now also reads the i915/xe hwmon interface:
  - `intel_gpu_hwmon_energy_joules_total` (counter, from `energy1_input`)
  - `intel_gpu_hwmon_power_watts` (derived from the energy counter between scrapes; appears from the second scrape)
  - `intel_gpu_hwmon_power_limit_watts`, `intel_gpu_hwmon_voltage_volts`, `intel_gpu_hwmon_temperature_celsius{sensor}`, `intel_gpu_hwmon_fan_rpm` (only when the driver provides them)
  - `intel_exporter_hwmon_error` (only present when hwmon is enabled)
- `--intel-hwmon` flag: `auto` (default), `off`, or an explicit hwmon directory. In `auto` mode, `--intel-device pci:slot=...` or a `sys:` path selects that PCI device.

### Notes
- hwmon is read at scrape time and is independent of `intel_gpu_top`, so power still reports if `intel_gpu_top` is failing.
- No change for the NVIDIA backend or for existing metric names.
- Upgrade: replace the binary; no flags are required. Use `--intel-hwmon=off` to keep the v0.2.0 metric set.

## v0.2.0 - 2026-11-08

### Added
- **Intel GPU backend** (`--backend=intel`) reading `intel_gpu_top -J`: per-engine busy, semaphore-wait and wait ratios, requested and actual frequency, RC6 residency, interrupts per second, power and memory-bandwidth values when `intel_gpu_top` reports them, plus `intel_exporter_collection_error` and `intel_exporter_last_collection_timestamp_seconds`.
- `--intel-gpu-top-path` and `--intel-device` flags.

### Notes
- The NVIDIA backend remains the default; Intel metrics use the `intel_` prefix.
- `intel_gpu_top` needs root or `CAP_PERFMON`, and access to `/dev/dri`.

## v0.1.0 - 2026-11-08

### Added
- CI workflow (`go vet`, `go test`) and a release workflow that builds static linux amd64 and arm64 binaries with a checksums file.
