package nvidia

import (
	"context"
	"sync"
)

// MockProvider is a test implementation of Provider.
type MockProvider struct {
	mu sync.RWMutex
	// Stats to return from Gather.
	stats []GPUStats
	// Err to return from Gather.
	err error
}

// NewMockProvider creates a new MockProvider.
func NewMockProvider() *MockProvider {
	return &MockProvider{}
}

// SetStats sets the stats to return from Gather.
func (p *MockProvider) SetStats(stats []GPUStats) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stats = stats
}

// SetError sets the error to return from Gather.
func (p *MockProvider) SetError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

// Gather returns the configured Stats and Err.
func (p *MockProvider) Gather(ctx context.Context) ([]GPUStats, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.err != nil {
		return nil, p.err
	}
	return p.stats, nil
}
