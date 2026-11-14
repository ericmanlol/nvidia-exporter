package intel

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/woo/nvidia-exporter/internal/sysfs"
)

// Selection is the GPU the Intel backend reads. intel_gpu_top, hwmon and
// xpu-smi each pick a device in their own way; Select resolves them to one
// PCI device so that a scrape never combines readings from different GPUs.
type Selection struct {
	// GPUTopDevice is the -d filter passed to intel_gpu_top ("" lets it
	// choose). It is the --intel-device value, or, when that is empty, a
	// pci:slot= filter for the GPU whose hwmon is read.
	GPUTopDevice string
	// PCISlot is the PCI address of the selected GPU (for example
	// "0000:03:00.0"), or "" if it cannot be determined.
	PCISlot string
	// HwmonDir is the hwmon directory to read, or "" to disable hwmon.
	HwmonDir string
}

// Select resolves the --intel-device filter and the --intel-hwmon setting
// ("auto", "off" or a directory) under sysRoot (normally "/sys").
//
//   - If device is set, hwmon "auto" only uses the hwmon of the PCI device the
//     filter selects. If that device cannot be determined from the filter,
//     hwmon is disabled instead of falling back to another GPU.
//   - If device is empty and a hwmon is found (or given), intel_gpu_top is
//     pinned to the hwmon's PCI device with -d pci:slot=.
//   - An explicit hwmon directory is always used. If it belongs to a different
//     GPU than the device filter, a warning is logged.
func Select(sysRoot, device, hwmon string, logger *slog.Logger) Selection {
	sel := Selection{GPUTopDevice: device}
	if device != "" {
		sel.PCISlot = ResolvePCISlot(sysRoot, device)
	}

	switch hwmon {
	case "off":
		return sel
	case "auto":
		if device != "" && sel.PCISlot == "" {
			logger.Warn("cannot determine the PCI device selected by --intel-device; hwmon metrics disabled so they are not read from a different GPU. Use a pci:slot= filter, or set --intel-hwmon to the GPU's hwmon directory",
				"intel_device", device)
			return sel
		}
		dir, err := FindHwmon(sysRoot, sel.PCISlot)
		if err != nil {
			logger.Info("no Intel GPU hwmon found, hwmon metrics disabled", "pci_slot", sel.PCISlot, "error", err)
			return sel
		}
		sel.HwmonDir = dir
	default:
		sel.HwmonDir = hwmon
		hwSlot := hwmonPCISlot(hwmon)
		switch {
		case sel.PCISlot == "":
			sel.PCISlot = hwSlot
		case hwSlot != "" && !strings.EqualFold(hwSlot, sel.PCISlot):
			logger.Warn("--intel-hwmon belongs to a different GPU than --intel-device; using both as configured",
				"intel_device", device, "device_pci_slot", sel.PCISlot, "hwmon", hwmon, "hwmon_pci_slot", hwSlot)
		}
	}

	if device == "" {
		if sel.PCISlot == "" {
			sel.PCISlot = hwmonPCISlot(sel.HwmonDir)
		}
		if sel.PCISlot != "" {
			sel.GPUTopDevice = "pci:slot=" + sel.PCISlot
			logger.Info("pinning intel_gpu_top to the GPU whose hwmon is read", "device", sel.GPUTopDevice)
		}
	}
	return sel
}

// ResolvePCISlot returns the PCI address of the GPU selected by an
// intel_gpu_top device filter, looking up DRM device nodes under sysRoot
// (normally "/sys"). It returns "" when the filter does not identify a single
// PCI device, for example "pci:vendor=8086,device=56a6" or "pci:card=1".
//
// Supported filters:
//
//	pci:slot=0000:03:00.0[,...]
//	drm:/dev/dri/renderD129, drm:/dev/dri/card1, drm:/dev/dri/by-path/pci-0000:03:00.0-render
//	sys:/sys/devices/pci0000:00/.../0000:03:00.0[/drm/card1], sys:/sys/class/drm/card1
func ResolvePCISlot(sysRoot, filter string) string {
	switch {
	case strings.HasPrefix(filter, "pci:"):
		for _, kv := range strings.Split(strings.TrimPrefix(filter, "pci:"), ",") {
			if v, ok := strings.CutPrefix(kv, "slot="); ok {
				if len(v) == 7 { // bus:dev.fn without the PCI domain
					v = "0000:" + v
				}
				return pciSlotOrEmpty(strings.ToLower(v))
			}
		}
	case strings.HasPrefix(filter, "drm:"):
		node := strings.TrimPrefix(filter, "drm:")
		if real, err := filepath.EvalSymlinks(node); err == nil {
			node = real
		}
		dev := filepath.Join(sysRoot, "class", "drm", filepath.Base(node), "device")
		if _, err := os.Stat(dev); err == nil {
			return pciSlotOrEmpty(sysfs.PCISlot(dev))
		}
	case strings.HasPrefix(filter, "sys:"):
		p := strings.TrimPrefix(filter, "sys:")
		if rest, ok := strings.CutPrefix(p, "/sys/"); ok {
			p = filepath.Join(sysRoot, rest)
		}
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		return lastPCISlot(p)
	}
	return ""
}

// hwmonPCISlot returns the PCI address of the device a hwmon directory belongs
// to. Its real path is /sys/devices/.../<pci address>/hwmon/hwmonN.
func hwmonPCISlot(dir string) string {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return lastPCISlot(dir)
}

// lastPCISlot returns the last path element that looks like a PCI address. In
// a sysfs device path the bridges come first and the GPU last.
func lastPCISlot(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if isPCISlot(parts[i]) {
			return parts[i]
		}
	}
	return ""
}

func pciSlotOrEmpty(s string) string {
	if isPCISlot(s) {
		return s
	}
	return ""
}

// isPCISlot reports whether s looks like a PCI address, "dddd:bb:dd.f".
func isPCISlot(s string) bool {
	return len(s) == 12 && s[4] == ':' && s[7] == ':' && s[10] == '.'
}
