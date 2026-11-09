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

// Run starts intel_gpu_top and restarts it if it exits, until ctx is cancelled.
func (p *GPUTopProvider) Run(ctx context.Context) {
	for {
		err := p.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		p.Logger.Error("intel_gpu_top stopped, restarting", "error", err, "delay", restartDelay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(restartDelay):
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

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting intel_gpu_top: %w", err)
	}
	p.Logger.Info("started intel_gpu_top", "args", strings.Join(p.args(), " "))

	readErr := p.readStream(stdout)
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
		s := raw.sample()

		p.mu.Lock()
		p.sample = s
		p.at = time.Now()
		p.err = nil
		p.mu.Unlock()
	}
	return nil
}
