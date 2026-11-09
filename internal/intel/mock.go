package intel

import (
	"sync"
	"time"
)

// MockProvider is a test implementation of Provider.
type MockProvider struct {
	mu     sync.RWMutex
	sample Sample
	at     time.Time
	err    error
}

// NewMockProvider creates a MockProvider that has no sample yet.
func NewMockProvider() *MockProvider {
	return &MockProvider{err: ErrNoSample}
}

// SetSample sets the sample and the time it was received, and clears any error.
func (p *MockProvider) SetSample(s Sample, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sample, p.at, p.err = s, at, nil
}

// SetError sets the error Latest returns.
func (p *MockProvider) SetError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

// Latest implements Provider.
func (p *MockProvider) Latest() (Sample, time.Time, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sample, p.at, p.err
}
