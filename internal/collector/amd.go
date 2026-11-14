package collector

import (
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/woo/nvidia-exporter/internal/amd"
)

const amdNamespace = "amd"

// AMDCollector implements prometheus.Collector for AMD GPUs. It reads the
// amdgpu sysfs files at scrape time; there is no background polling. Cards are
// discovered on every scrape, so hot-plugged or rebound GPUs are picked up.
type AMDCollector struct {
	sysRoot string
	now     func() time.Time
	common  *commonDescs

	mu     sync.Mutex
	lastOK time.Time // last scrape that found a card

	gttUsed   *prometheus.Desc
	gttTotal  *prometheus.Desc
	suspended *prometheus.Desc
}

// NewAMD creates an AMDCollector reading the sysfs tree at sysRoot (normally
// "/sys").
func NewAMD(sysRoot string) *AMDCollector {
	labels := []string{"gpu", "name", "id"}
	desc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(amdNamespace, "gpu", name), help, labels, nil)
	}
	return &AMDCollector{
		sysRoot:   sysRoot,
		now:       time.Now,
		common:    newCommonDescs(),
		gttUsed:   desc("gtt_used_bytes", "System memory mapped for the GPU (GTT) in use in bytes"),
		gttTotal:  desc("gtt_total_bytes", "System memory mappable for the GPU (GTT) in bytes"),
		suspended: desc("runtime_suspended", "1 if the GPU is runtime-suspended (sensors are not read so as not to wake it), 0 otherwise"),
	}
}

// Describe implements prometheus.Collector.
func (c *AMDCollector) Describe(ch chan<- *prometheus.Desc) {
	c.common.describe(ch)
	ch <- c.gttUsed
	ch <- c.gttTotal
	ch <- c.suspended
}

// Collect implements prometheus.Collector.
// gpu_exporter_collection_error is 1 when no amdgpu card is found.
func (c *AMDCollector) Collect(ch chan<- prometheus.Metric) {
	cards := amd.Discover(c.sysRoot)
	for _, card := range cards {
		st := card.Read()
		cs := amdCommon(card, st)
		c.common.emit(ch, cs)

		l := []string{cs.Index, cs.Name, cs.ID}
		susp := 0.0
		if st.Suspended {
			susp = 1
		}
		ch <- prometheus.MustNewConstMetric(c.suspended, prometheus.GaugeValue, susp, l...)
		if st.GTTUsed != nil {
			ch <- prometheus.MustNewConstMetric(c.gttUsed, prometheus.GaugeValue, *st.GTTUsed, l...)
		}
		if st.GTTTotal != nil {
			ch <- prometheus.MustNewConstMetric(c.gttTotal, prometheus.GaugeValue, *st.GTTTotal, l...)
		}
	}
	c.mu.Lock()
	if len(cards) > 0 {
		c.lastOK = c.now()
	}
	last := c.lastOK
	c.mu.Unlock()
	// The timestamp of the last successful scrape is kept while no card is
	// found, so the collection age keeps growing instead of disappearing.
	c.common.emitExporter(ch, "amd", last, len(cards) == 0)
}

func amdCommon(card amd.Card, st amd.Stats) gpuStats {
	pct := func(v *float64) *float64 {
		if v == nil {
			return nil
		}
		return f64(*v / 100)
	}
	id := card.PCISlot
	if id == "" {
		id = "card" + strconv.Itoa(card.Index)
	}
	return gpuStats{
		Index:             strconv.Itoa(card.Index),
		Vendor:            "amd",
		Name:              card.Name,
		ID:                id,
		Utilization:       pct(st.BusyPercent),
		MemoryUtilization: pct(st.MemBusyPercent),
		MemoryUsed:        st.VRAMUsed,
		MemoryTotal:       st.VRAMTotal,
		Temps:             st.Temps,
		PowerDraw:         st.PowerWatts,
		PowerLimit:        st.PowerCapWatts,
		FanRatio:          st.FanRatio,
		FanRPM:            st.FanRPM,
		ClockGraphics:     st.SCLKHz,
		ClockMemory:       st.MCLKHz,
		Voltage:           st.VoltageGFX,
	}
}
