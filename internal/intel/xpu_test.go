package intel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseXPUStats(t *testing.T) {
	b, err := os.ReadFile("../../testdata/xpu-smi-stats.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseXPUStats(b)
	if err != nil {
		t.Fatal(err)
	}
	if want := 378.34765625 * 1048576; got != want {
		t.Errorf("used = %v, want %v", got, want)
	}

	// Leading warning lines before the JSON are ignored.
	if _, err := parseXPUStats(append([]byte("WARNING: something\n"), b...)); err != nil {
		t.Errorf("with warning prefix: %v", err)
	}

	// Multiple tiles are summed.
	multi := `{"memory":{"used_mib":{"tile_0":{"current":100},"tile_1":{"current":50}}}}`
	if got, _ := parseXPUStats([]byte(multi)); got != 150*1048576 {
		t.Errorf("multi-tile = %v", got)
	}

	for _, bad := range []string{`{}`, `{"memory":{}}`, `not json`, ``} {
		if _, err := parseXPUStats([]byte(bad)); err == nil {
			t.Errorf("parseXPUStats(%q): want error", bad)
		}
	}
}

func TestParseXPUDiscovery(t *testing.T) {
	b, err := os.ReadFile("../../testdata/xpu-smi-discovery.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseXPUDiscovery(b)
	if err != nil || got != 4244635648 {
		t.Errorf("got %v, %v", got, err)
	}
	if got, err := parseXPUDiscovery([]byte(`{"memory_physical_size_byte":"4244635648"}`)); err != nil || got != 4244635648 {
		t.Errorf("string form: got %v, %v", got, err)
	}
	for _, bad := range []string{`{}`, `{"memory_physical_size_byte":0}`, `{"memory_physical_size_byte":"x"}`} {
		if _, err := parseXPUDiscovery([]byte(bad)); err == nil {
			t.Errorf("parseXPUDiscovery(%q): want error", bad)
		}
	}
}

func TestXPU_RunPollsFakeTool(t *testing.T) {
	fake, _ := filepath.Abs("../../testdata/fake-xpu-smi")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_XPU_ARGS_FILE", argsFile)

	x := NewXPU(fake, "0", "", time.Second, testLogger())
	if _, _, err := x.Latest(); err != ErrNoXPUSample {
		t.Fatalf("before first poll: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go x.Run(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for {
		st, _, err := x.Latest()
		if err == nil {
			if st.MemoryTotalBytes == nil || *st.MemoryTotalBytes != 4244635648 {
				t.Errorf("total = %v", st.MemoryTotalBytes)
			}
			if st.MemoryUsedBytes != 378.34765625*1048576 {
				t.Errorf("used = %v", st.MemoryUsedBytes)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reading: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	args, _ := os.ReadFile(argsFile)
	for _, want := range []string{"discovery -d 0 -j", "stats -d 0 -j"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("xpu-smi not called with %q; calls:\n%s", want, args)
		}
	}
}

func TestXPU_ReportsToolFailure(t *testing.T) {
	fake, _ := filepath.Abs("../../testdata/fake-xpu-smi")
	t.Setenv("FAKE_XPU_FAIL", "1")
	x := NewXPU(fake, "0", "", time.Second, testLogger())
	x.poll(context.Background())
	_, _, err := x.Latest()
	if err == nil || err == ErrNoXPUSample || !strings.Contains(err.Error(), "XPU Manager daemon") {
		t.Errorf("err = %v, want the tool's stderr", err)
	}
}

func TestParseXPUDeviceList(t *testing.T) {
	b, err := os.ReadFile("../../testdata/xpu-smi-discovery-list.json")
	if err != nil {
		t.Fatal(err)
	}
	devs, err := parseXPUDeviceList(append([]byte("WARNING: something\n"), b...))
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || devs[0] != (xpuDevice{ID: "0", BDF: "0000:00:10.0"}) {
		t.Errorf("got %+v", devs)
	}
	if _, err := parseXPUDeviceList([]byte("not json")); err == nil {
		t.Error("want error for invalid JSON")
	}
}

func TestPickXPUDevice(t *testing.T) {
	two := []xpuDevice{{ID: "0", BDF: "0000:03:00.0"}, {ID: "1", BDF: "0000:04:00.0"}}
	one := two[:1]
	cases := []struct {
		name       string
		devs       []xpuDevice
		want, slot string
		dev        string // "" = error
	}{
		{"match by slot", two, "", "0000:04:00.0", "1"},
		{"match by slot, upper case", []xpuDevice{{ID: "1", BDF: "0000:0a:00.0"}}, "", "0000:0A:00.0", "1"},
		{"no device at slot", two, "", "0000:05:00.0", ""},
		{"explicit, same GPU", two, "1", "0000:04:00.0", "1"},
		{"explicit, different GPU", two, "0", "0000:04:00.0", ""},
		{"explicit, missing", two, "7", "", ""},
		{"explicit, slot unknown", two, "0", "", "0"},
		{"auto, slot unknown, one device", one, "", "", "0"},
		{"auto, slot unknown, two devices", two, "", "", ""},
		{"no devices", nil, "", "", ""},
	}
	for _, c := range cases {
		dev, err := pickXPUDevice(c.devs, c.want, c.slot)
		if c.dev == "" {
			if err == nil {
				t.Errorf("%s: got device %q, want an error", c.name, dev)
			}
			continue
		}
		if err != nil || dev != c.dev {
			t.Errorf("%s: got %q, %v; want %q", c.name, dev, err, c.dev)
		}
	}
}

func writeDeviceList(t *testing.T, devs string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "list.json")
	if err := os.WriteFile(f, []byte(`{"device_list":[`+devs+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

// With no --intel-xpu-device, the xpu-smi device at the selected GPU's PCI
// address is polled.
func TestXPU_SelectsDeviceByPCISlot(t *testing.T) {
	fake, _ := filepath.Abs("../../testdata/fake-xpu-smi")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_XPU_ARGS_FILE", argsFile)
	t.Setenv("FAKE_XPU_LIST_FILE", writeDeviceList(t,
		`{"device_id":0,"pci_bdf_address":"0000:03:00.0"},{"device_id":1,"pci_bdf_address":"0000:04:00.0"}`))

	x := NewXPU(fake, "", "0000:04:00.0", time.Second, testLogger())
	x.poll(context.Background())
	if _, _, err := x.Latest(); err != nil {
		t.Fatalf("Latest: %v", err)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "stats -d 1 -j") {
		t.Errorf("want stats for device 1; calls:\n%s", args)
	}
}

// A configured xpu-smi device that is a different GPU than the other sources
// read is an error, and is not polled.
func TestXPU_RejectsDeviceOfAnotherGPU(t *testing.T) {
	fake, _ := filepath.Abs("../../testdata/fake-xpu-smi")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_XPU_ARGS_FILE", argsFile)
	t.Setenv("FAKE_XPU_LIST_FILE", writeDeviceList(t,
		`{"device_id":0,"pci_bdf_address":"0000:03:00.0"},{"device_id":1,"pci_bdf_address":"0000:04:00.0"}`))

	x := NewXPU(fake, "0", "0000:04:00.0", time.Second, testLogger())
	x.poll(context.Background())
	_, _, err := x.Latest()
	if err == nil || !strings.Contains(err.Error(), "0000:03:00.0") {
		t.Errorf("err = %v, want a device mismatch error", err)
	}
	if args, _ := os.ReadFile(argsFile); strings.Contains(string(args), "stats") {
		t.Errorf("xpu-smi stats was called for the wrong GPU; calls:\n%s", args)
	}
}
