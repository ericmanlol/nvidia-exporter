package intel

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeTwoGPUs builds a sysfs tree with two discrete Intel GPUs:
//
//	card0, renderD128 -> 0000:03:00.0 (hwmon2)
//	card1, renderD129 -> 0000:04:00.0 (hwmon3)
//
// and a /dev/dri directory with device nodes and by-path links. It returns the
// sysfs root and the /dev/dri directory.
func fakeTwoGPUs(t *testing.T) (sys, dri string) {
	t.Helper()
	root := t.TempDir()
	sys = filepath.Join(root, "sys")
	dri = filepath.Join(root, "dev", "dri")
	link := func(target, name string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}
	for _, gpu := range []struct{ slot, card, render, hwmon string }{
		{"0000:03:00.0", "card0", "renderD128", "hwmon2"},
		{"0000:04:00.0", "card1", "renderD129", "hwmon3"},
	} {
		// Laid out like real sysfs: the device directories live under
		// /sys/devices and /sys/class/drm/<node> links to them.
		pci := filepath.Join(sys, "devices", "pci0000:00", "0000:00:01.0", gpu.slot)
		write(t, filepath.Join(pci, "vendor"), "0x8086")
		write(t, filepath.Join(pci, "device"), "0x56a5")
		mon := filepath.Join(pci, "hwmon", gpu.hwmon)
		write(t, filepath.Join(mon, "name"), "i915")
		write(t, filepath.Join(mon, "energy1_input"), "1000")
		link("../..", filepath.Join(mon, "device"))
		for _, node := range []string{gpu.card, gpu.render} {
			if err := os.MkdirAll(filepath.Join(pci, "drm", node), 0o755); err != nil {
				t.Fatal(err)
			}
			link("../..", filepath.Join(pci, "drm", node, "device"))
			link(filepath.Join(pci, "drm", node), filepath.Join(sys, "class", "drm", node))
			write(t, filepath.Join(dri, node), "")
		}
		link("../"+gpu.render, filepath.Join(dri, "by-path", "pci-"+gpu.slot+"-render"))
		link("../"+gpu.card, filepath.Join(dri, "by-path", "pci-"+gpu.slot+"-card"))
	}
	return sys, dri
}

func TestResolvePCISlot(t *testing.T) {
	sys, dri := fakeTwoGPUs(t)
	cases := map[string]string{
		"":                      "",
		"pci:slot=0000:04:00.0": "0000:04:00.0",
		"pci:slot=04:00.0":      "0000:04:00.0",
		"pci:vendor=8086,slot=0000:04:00.0,card=0":                        "0000:04:00.0",
		"pci:vendor=8086,device=56a5":                                     "",
		"pci:card=1":                                                      "",
		"drm:" + dri + "/renderD129":                                      "0000:04:00.0",
		"drm:" + dri + "/card1":                                           "0000:04:00.0",
		"drm:" + dri + "/renderD128":                                      "0000:03:00.0",
		"drm:" + dri + "/by-path/pci-0000:04:00.0-render":                 "0000:04:00.0",
		"drm:" + dri + "/renderD200":                                      "", // no such node
		"sys:/sys/class/drm/card1":                                        "0000:04:00.0",
		"sys:/sys/devices/pci0000:00/0000:00:01.0/0000:04:00.0":           "0000:04:00.0",
		"sys:/sys/devices/pci0000:00/0000:00:01.0/0000:03:00.0/drm/card0": "0000:03:00.0",
	}
	for in, want := range cases {
		if got := ResolvePCISlot(sys, in); got != want {
			t.Errorf("ResolvePCISlot(%q) = %q, want %q", in, got, want)
		}
	}
}

func hwmonOf(sys, card, hwmon string) string {
	return filepath.Join(sys, "class", "drm", card, "device", "hwmon", hwmon)
}

// Selecting the second GPU with a drm: filter must read that GPU's hwmon, not
// the first one found.
func TestSelect_DRMFilterUsesThatGPUsHwmon(t *testing.T) {
	sys, dri := fakeTwoGPUs(t)
	for _, dev := range []string{
		"drm:" + dri + "/renderD129",
		"drm:" + dri + "/card1",
		"drm:" + dri + "/by-path/pci-0000:04:00.0-render",
	} {
		sel := Select(sys, dev, "auto", testLogger())
		want := Selection{GPUTopDevice: dev, PCISlot: "0000:04:00.0", HwmonDir: hwmonOf(sys, "card1", "hwmon3")}
		if sel != want {
			t.Errorf("Select(%q):\n got %+v\nwant %+v", dev, sel, want)
		}
		if got := filepath.Base(NewHwmon(sel.HwmonDir).Dir()); got != "hwmon3" {
			t.Errorf("Select(%q): hwmon %s, want hwmon3", dev, got)
		}
	}
}

// A filter that does not identify one PCI device must disable automatic hwmon
// instead of falling back to the first GPU.
func TestSelect_UnresolvableFilterDisablesAutoHwmon(t *testing.T) {
	sys, _ := fakeTwoGPUs(t)
	for _, dev := range []string{"pci:vendor=8086,device=56a5", "pci:card=1", "drm:/dev/dri/renderD200"} {
		sel := Select(sys, dev, "auto", testLogger())
		if sel.HwmonDir != "" || sel.PCISlot != "" || sel.GPUTopDevice != dev {
			t.Errorf("Select(%q) = %+v, want no hwmon and no PCI slot", dev, sel)
		}
	}
}

// An explicit hwmon directory is honoured even when the filter is unresolvable,
// and identifies the GPU.
func TestSelect_ExplicitHwmon(t *testing.T) {
	sys, _ := fakeTwoGPUs(t)
	dir := hwmonOf(sys, "card1", "hwmon3")
	sel := Select(sys, "pci:card=1", dir, testLogger())
	want := Selection{GPUTopDevice: "pci:card=1", PCISlot: "0000:04:00.0", HwmonDir: dir}
	if sel != want {
		t.Errorf("got %+v\nwant %+v", sel, want)
	}

	// Without a filter, intel_gpu_top is pinned to the hwmon's GPU.
	sel = Select(sys, "", dir, testLogger())
	want = Selection{GPUTopDevice: "pci:slot=0000:04:00.0", PCISlot: "0000:04:00.0", HwmonDir: dir}
	if sel != want {
		t.Errorf("no filter: got %+v\nwant %+v", sel, want)
	}
}

// Without a filter, intel_gpu_top is pinned to the GPU whose hwmon was found,
// so it cannot pick a different one.
func TestSelect_NoFilterPinsIntelGPUTop(t *testing.T) {
	sys, _ := fakeTwoGPUs(t)
	sel := Select(sys, "", "auto", testLogger())
	want := Selection{GPUTopDevice: "pci:slot=0000:03:00.0", PCISlot: "0000:03:00.0", HwmonDir: hwmonOf(sys, "card0", "hwmon2")}
	if sel != want {
		t.Errorf("got %+v\nwant %+v", sel, want)
	}

	// No hwmon found: intel_gpu_top chooses, as before.
	if sel := Select(t.TempDir(), "", "auto", testLogger()); sel != (Selection{}) {
		t.Errorf("no hwmon: got %+v, want zero Selection", sel)
	}
}

func TestSelect_HwmonOff(t *testing.T) {
	sys, dri := fakeTwoGPUs(t)
	dev := "drm:" + dri + "/renderD129"
	sel := Select(sys, dev, "off", testLogger())
	if want := (Selection{GPUTopDevice: dev, PCISlot: "0000:04:00.0"}); sel != want {
		t.Errorf("got %+v\nwant %+v", sel, want)
	}
	if sel := Select(sys, "", "off", testLogger()); sel != (Selection{}) {
		t.Errorf("no filter: got %+v, want zero Selection", sel)
	}
}
