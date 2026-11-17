package intel

import (
	"errors"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/woo/nvidia-exporter/internal/sysfs"
)

// HwmonStats is one reading of the GPU's hwmon sensors. intel_gpu_top does not
// report power on discrete Arc cards, but the i915/xe drivers expose an energy
// counter (and sometimes a power limit, voltage, temperatures and fan speed)
// through hwmon. Optional values are nil when the driver does not provide them.
type HwmonStats struct {
	EnergyJoules *float64           // cumulative energy counter (energy1_input, µJ → J)
	PowerWatts   *float64           // derived from two consecutive energy readings
	PowerLimit   *float64           // W (power1_max, falling back to power1_cap)
	VoltageV     *float64           // V (in0_input, mV → V)
	Temps        map[string]float64 // °C by sensor name (temp<N>_label, else temp<N>)
	FanRPM       *float64           // fan1_input
}

// Hwmon reads the hwmon directory of one Intel GPU.
type Hwmon struct {
	dir string
	now func() time.Time

	mu         sync.Mutex
	prevEnergy float64
	prevAt     time.Time
	havePrev   bool
}

// ErrNoHwmon is returned by FindHwmon when no Intel GPU hwmon directory exists.
var ErrNoHwmon = errors.New("no Intel GPU hwmon directory found")

// NewHwmon returns a reader for the given hwmon directory (for example
// /sys/class/drm/card1/device/hwmon/hwmon5).
func NewHwmon(dir string) *Hwmon {
	return &Hwmon{dir: dir, now: time.Now}
}

// Dir returns the hwmon directory being read.
func (h *Hwmon) Dir() string { return h.dir }

// DeviceDir returns the device the hwmon belongs to (its "device" link).
func (h *Hwmon) DeviceDir() string { return filepath.Join(h.dir, "device") }

// FindHwmon locates the hwmon directory of an Intel GPU under sysRoot
// (normally "/sys"). It looks at /class/drm/card*/device, requires PCI vendor
// 0x8086 and an hwmon named i915 or xe, and returns the first match in card
// order. If pciSlot is non-empty (for example "0000:00:10.0") only that device
// matches.
func FindHwmon(sysRoot, pciSlot string) (string, error) {
	devs, _ := filepath.Glob(filepath.Join(sysRoot, "class", "drm", "card[0-9]*", "device"))
	sort.Strings(devs)
	for _, dev := range devs {
		if v, err := sysfs.ReadTrim(filepath.Join(dev, "vendor")); err != nil || v != "0x8086" {
			continue
		}
		if pciSlot != "" {
			if real, err := filepath.EvalSymlinks(dev); err != nil || filepath.Base(real) != pciSlot {
				continue
			}
		}
		mons, _ := filepath.Glob(filepath.Join(dev, "hwmon", "hwmon*"))
		sort.Strings(mons)
		for _, m := range mons {
			if n, _ := sysfs.ReadTrim(filepath.Join(m, "name")); n == "i915" || n == "xe" {
				return m, nil
			}
		}
	}
	return "", ErrNoHwmon
}

// Read takes one reading of each independent sensor. Missing or unreadable
// sensors are omitted; an error means no usable readings were available.
// Power is derived from consecutive energy readings at least 100ms apart.
// A missing/invalid energy reading or counter reset restarts the baseline.
func (h *Hwmon) Read() (HwmonStats, error) {
	var st HwmonStats
	// Serialize the energy read and timestamp with baseline updates so concurrent
	// scrapes cannot apply their readings in reverse order.
	h.mu.Lock()
	if uj, err := sysfs.ReadFloat(filepath.Join(h.dir, "energy1_input")); err == nil {
		at := h.now()
		joules := uj / 1e6
		st.EnergyJoules = &joules
		if h.havePrev {
			dt := at.Sub(h.prevAt).Seconds()
			if dt >= 0.1 && joules >= h.prevEnergy {
				w := (joules - h.prevEnergy) / dt
				st.PowerWatts = &w
			}
		}
		if !h.havePrev || joules < h.prevEnergy || at.Sub(h.prevAt) >= 100*time.Millisecond {
			h.prevEnergy, h.prevAt, h.havePrev = joules, at, true
		}
	} else {
		h.havePrev = false
	}
	h.mu.Unlock()

	// Power limit: power1_max, else power1_cap (µW).
	for _, f := range []string{"power1_max", "power1_cap"} {
		if v, err := sysfs.ReadFloat(filepath.Join(h.dir, f)); err == nil && v > 0 {
			w := v / 1e6
			st.PowerLimit = &w
			break
		}
	}
	if v, err := sysfs.ReadFloat(filepath.Join(h.dir, "in0_input")); err == nil {
		x := v / 1000
		st.VoltageV = &x
	}
	if v, err := sysfs.ReadFloat(filepath.Join(h.dir, "fan1_input")); err == nil {
		st.FanRPM = &v
	}
	st.Temps = sysfs.Temps(h.dir)
	if st.EnergyJoules == nil && st.PowerLimit == nil && st.VoltageV == nil &&
		st.FanRPM == nil && len(st.Temps) == 0 {
		return st, errors.New("no usable hwmon sensor readings")
	}
	return st, nil
}
