package intel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNoSample is returned by Latest before the first sample has arrived.
var ErrNoSample = errors.New("no intel_gpu_top sample received yet")

// restartDelay is how long Run waits before restarting a failed intel_gpu_top.
const restartDelay = 5 * time.Second

// waitDelay bounds how long runOnce waits for intel_gpu_top to exit, and for
// its output pipes to close, after it has been killed or has exited.
const waitDelay = 5 * time.Second

// SampleTimeout is the maximum time to wait for the first or next complete
// intel_gpu_top sample: three sampling periods plus a five-second margin.
// It accounts for the tool's minimum period and saturates instead of overflowing.
func SampleTimeout(interval time.Duration) time.Duration {
	interval = max(interval, 100*time.Millisecond)
	const margin = 5 * time.Second
	const maximum = time.Duration(1<<63 - 1)
	if interval > (maximum-margin)/3 {
		return maximum
	}
	return 3*interval + margin
}

// Provider returns the most recent intel_gpu_top sample.
type Provider interface {
	// Latest returns the newest sample and when it was received. The error is
	// non-nil when intel_gpu_top is failing or has not produced a sample yet.
	Latest() (Sample, time.Time, error)
}

// GPUTopProvider keeps one long-running `intel_gpu_top -J` process and exposes
// the newest sample it printed. Unlike nvidia-smi, intel_gpu_top measures over
// a period, so it is streamed instead of being run once per scrape.
type GPUTopProvider struct {
	Path     string        // intel_gpu_top binary
	Device   string        // optional -d filter, e.g. "drm:/dev/dri/renderD128"
	Interval time.Duration // sampling period (-s)
	Logger   *slog.Logger

	restartDelay  time.Duration // tests shorten it
	sampleTimeout time.Duration

	mu     sync.RWMutex
	sample Sample
	at     time.Time
	err    error
}

// NewGPUTopProvider creates a GPUTopProvider. Call Run to start sampling.
func NewGPUTopProvider(path, device string, interval time.Duration, logger *slog.Logger) *GPUTopProvider {
	return &GPUTopProvider{
		Path:     path,
		Device:   device,
		Interval: interval,
		Logger:   logger,
		err:      ErrNoSample,

		restartDelay:  restartDelay,
		sampleTimeout: SampleTimeout(interval),
	}
}

func (p *GPUTopProvider) args() []string {
	ms := p.Interval.Milliseconds()
	if ms < 100 {
		ms = 100
	}
	args := []string{"-J", "-s", strconv.FormatInt(ms, 10)}
	if p.Device != "" {
		args = append(args, "-d", p.Device)
	}
	return args
}

// Latest implements Provider.
func (p *GPUTopProvider) Latest() (Sample, time.Time, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sample, p.at, p.err
}

// Run starts intel_gpu_top and restarts it if it exits or stops producing
// complete samples, until ctx is cancelled.
func (p *GPUTopProvider) Run(ctx context.Context) {
	for {
		err := p.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		p.Logger.Error("intel_gpu_top stopped, restarting", "error", err, "delay", p.restartDelay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(p.restartDelay):
		}
	}
}

func (p *GPUTopProvider) runOnce(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, p.Path, p.args()...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting intel_gpu_top: %w", err)
	}
	p.Logger.Info("started intel_gpu_top", "args", strings.Join(p.args(), " "))

	// Decoding can block even when the child remains alive. Supervise complete
	// samples, rather than bytes, so a truncated JSON object cannot keep it alive.
	samples := make(chan struct{}, 1)
	readDone := make(chan error, 1)
	go func() {
		readDone <- p.readSamples(stdout, func() {
			select {
			case samples <- struct{}{}:
			default:
			}
		})
	}()
	timer := time.NewTimer(p.sampleTimeout)
	defer timer.Stop()
	var readErr error
	reading := true
stream:
	for {
		select {
		case readErr = <-readDone:
			reading = false
			break stream
		case <-samples:
			timer.Reset(p.sampleTimeout)
		case <-timer.C:
			readErr = fmt.Errorf("no complete intel_gpu_top sample for %s", p.sampleTimeout)
			break stream
		case <-ctx.Done():
			readErr = ctx.Err()
			break stream
		}
	}
	// Close our read end as well as killing the child: descendants may have
	// inherited stdout. Join the reader before Wait closes the command's pipes.
	_ = cmd.Process.Kill()
	_ = stdout.Close()
	if reading {
		<-readDone
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}

	msg := strings.TrimSpace(stderr.String())
	switch {
	case readErr != nil:
		return fmt.Errorf("reading intel_gpu_top output: %w: %s", readErr, msg)
	case waitErr != nil:
		return fmt.Errorf("intel_gpu_top exited: %w: %s", waitErr, msg)
	default:
		return fmt.Errorf("intel_gpu_top exited unexpectedly: %s", msg)
	}
}

// readStream decodes the JSON array that intel_gpu_top -J prints, one object
// per sampling period, and stores each object as the latest sample.
func (p *GPUTopProvider) readStream(r io.Reader) error {
	return p.readSamples(r, func() {})
}

func (p *GPUTopProvider) readSamples(r io.Reader, onSample func()) error {
	dec := json.NewDecoder(r)

	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return fmt.Errorf("expected JSON array, got %v", tok)
	}

	for dec.More() {
		var raw rawSample
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		s, err := raw.sample()
		if err != nil {
			return err
		}

		p.mu.Lock()
		p.sample = s
		p.at = time.Now()
		p.err = nil
		p.mu.Unlock()
		onSample()
	}
	return nil
}
