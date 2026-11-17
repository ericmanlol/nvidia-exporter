package intel

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSampleValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
	}{
		{"empty", `{}`},
		{"unrelated", `{"message":"GPU unavailable"}`},
		{"null", `null`},
		{"no period", `{"frequency":{"actual":100}}`},
		{"null duration", `{"period":{"duration":null},"frequency":{"actual":0}}`},
		{"zero duration", `{"period":{"duration":0},"frequency":{"actual":0}}`},
		{"negative duration", `{"period":{"duration":-1},"frequency":{"actual":0}}`},
		{"overflow duration", `{"period":{"duration":1e20},"frequency":{"actual":0}}`},
		{"subnanosecond duration", `{"period":{"duration":1e-10},"frequency":{"actual":0}}`},
		{"wrong duration type", `{"period":{"duration":"1000"},"frequency":{"actual":0}}`},
		{"no readings", `{"period":{"duration":1000}}`},
		{"empty groups", `{"period":{"duration":1000},"frequency":{},"interrupts":{},"rc6":{},"imc-bandwidth":{},"engines":{}}`},
		{"null reading", `{"period":{"duration":1000},"frequency":{"actual":null}}`},
		{"wrong reading type", `{"period":{"duration":1000},"frequency":{"actual":"zero"}}`},
		{"engine without busy", `{"period":{"duration":1000},"engines":{"Video/0":{"wait":0}}}`},
		{"null engine busy", `{"period":{"duration":1000},"engines":{"Video/0":{"busy":null}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseSample([]byte(tc.json)); err == nil {
				t.Fatalf("ParseSample(%s) accepted an unusable sample", tc.json)
			}
			for _, priorSuccess := range []bool{false, true} {
				p := NewGPUTopProvider("unused", "", time.Second, testLogger())
				if priorSuccess {
					if err := p.readStream(strings.NewReader(`[{"period":{"duration":1000},"frequency":{"actual":100}}]`)); err != nil {
						t.Fatal(err)
					}
				}
				before, at, _ := p.Latest()
				callbacks := 0
				if err := p.readSamples(strings.NewReader("["+tc.json+"]"), func() { callbacks++ }); err == nil {
					t.Fatal("stream accepted an unusable sample")
				}
				after, afterAt, err := p.Latest()
				if !afterAt.Equal(at) || after.FrequencyActual != before.FrequencyActual || callbacks != 0 {
					t.Fatal("unusable sample changed the last success or reset the watchdog")
				}
				if !priorSuccess && !errors.Is(err, ErrNoSample) {
					t.Fatalf("unusable first sample cleared initial error: %v", err)
				}
			}
		})
	}
}

func TestSampleAcceptsIndependentlyOptionalZeroReadings(t *testing.T) {
	for _, reading := range []string{
		`"frequency":{"actual":0}`,
		`"frequency":{"requested":0}`,
		`"interrupts":{"count":0}`,
		`"rc6":{"value":0}`,
		`"power":{"GPU":0}`,
		`"power":{"Package":0}`,
		`"imc-bandwidth":{"reads":0}`,
		`"imc-bandwidth":{"writes":0}`,
		`"engines":{"Video/0":{"busy":0}}`,
	} {
		t.Run(reading, func(t *testing.T) {
			data := []byte(`{"period":{"duration":1000},` + reading + `,"unknown":{"future":true}}`)
			s, err := ParseSample(data)
			if err != nil || s.Period != time.Second {
				t.Fatalf("valid zero reading rejected: %+v, %v", s, err)
			}
		})
	}
}

func TestProviderRecoversFromUnusableSample(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	t.Setenv("INTEL_VALIDATION_MARKER", marker)
	tool := streamTool(t, `if [ ! -e "$INTEL_VALIDATION_MARKER" ]; then
  touch "$INTEL_VALIDATION_MARKER"
  printf '[{},'
else
  printf '[{"period":{"duration":1000},"frequency":{"actual":900}},'
fi
exec sleep 60
`)
	p := NewGPUTopProvider(tool, "", time.Second, testLogger())
	p.restartDelay = 200 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("provider did not stop")
		}
	})
	waitIntel(t, "schema error", func() bool {
		_, at, err := p.Latest()
		return err != nil && !errors.Is(err, ErrNoSample) && at.IsZero()
	})
	waitIntel(t, "valid sample after restart", func() bool {
		s, at, err := p.Latest()
		return err == nil && !at.IsZero() && s.FrequencyActual != nil && *s.FrequencyActual == 900
	})
}
