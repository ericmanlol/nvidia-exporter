package intel

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// rawSample mirrors one object of intel_gpu_top's JSON output (-J).
// Pointers distinguish an omitted/null counter from a legitimate zero.
type rawSample struct {
	Period struct {
		Duration *float64 `json:"duration"` // milliseconds
	} `json:"period"`
	Frequency struct {
		Requested *float64 `json:"requested"`
		Actual    *float64 `json:"actual"`
	} `json:"frequency"`
	Interrupts struct {
		Count *float64 `json:"count"`
	} `json:"interrupts"`
	RC6 *struct {
		Value *float64 `json:"value"`
	} `json:"rc6"`
	Power struct {
		GPU     *float64 `json:"GPU"`
		Package *float64 `json:"Package"`
	} `json:"power"`
	IMC *struct {
		Reads  *float64 `json:"reads"`
		Writes *float64 `json:"writes"`
	} `json:"imc-bandwidth"`
	Engines map[string]struct {
		Busy *float64 `json:"busy"`
		Sema *float64 `json:"sema"`
		Wait *float64 `json:"wait"`
	} `json:"engines"`
}

func (r *rawSample) sample() (Sample, error) {
	// The producer emits a sampling period even when optional PMU counters are
	// unavailable. Require that envelope and at least one recognized reading,
	// rather than requiring every GPU/tool version to expose the same counters.
	const maxPeriodMS = float64(1<<63-1) / float64(time.Millisecond)
	if r.Period.Duration == nil || *r.Period.Duration <= 0 || *r.Period.Duration >= maxPeriodMS {
		return Sample{}, errors.New("intel_gpu_top sample requires a positive representable period.duration")
	}
	period := time.Duration(*r.Period.Duration * float64(time.Millisecond))
	if period <= 0 {
		return Sample{}, errors.New("intel_gpu_top sample period.duration is below one nanosecond")
	}
	s := Sample{
		Period:             period,
		FrequencyActual:    r.Frequency.Actual,
		FrequencyRequested: r.Frequency.Requested,
		Interrupts:         r.Interrupts.Count,
		PowerGPU:           r.Power.GPU,
		PowerPackage:       r.Power.Package,
		Engines:            make(map[string]EngineStats, len(r.Engines)),
	}
	if r.RC6 != nil && r.RC6.Value != nil {
		s.RC6, s.HasRC6 = *r.RC6.Value, true
	}
	if r.IMC != nil {
		s.IMCReads, s.IMCWrites = r.IMC.Reads, r.IMC.Writes
	}
	for name, e := range r.Engines {
		if e.Busy == nil {
			return Sample{}, fmt.Errorf("intel_gpu_top engine %q requires a numeric busy reading", name)
		}
		s.Engines[name] = EngineStats{Busy: *e.Busy, Sema: e.Sema, Wait: e.Wait}
	}
	if s.FrequencyActual == nil && s.FrequencyRequested == nil && s.Interrupts == nil &&
		!s.HasRC6 && s.PowerGPU == nil && s.PowerPackage == nil &&
		s.IMCReads == nil && s.IMCWrites == nil && len(s.Engines) == 0 {
		return Sample{}, errors.New("intel_gpu_top sample contains no supported readings")
	}
	return s, nil
}

// ParseSample decodes and validates a single intel_gpu_top JSON object.
// A usable sample has a positive period.duration and at least one supported
// numeric reading. Independently optional counters remain absent when omitted.
func ParseSample(data []byte) (Sample, error) {
	var raw rawSample
	if err := json.Unmarshal(data, &raw); err != nil {
		return Sample{}, err
	}
	return raw.sample()
}
