package nvidia

import "context"

// Provider defines the interface for collecting GPU statistics.
type Provider interface {
	// Gather collects GPU statistics from all available GPUs.
	// Returns a slice of GPUStats, one per GPU, or an error if collection fails.
	Gather(ctx context.Context) ([]GPUStats, error)
}
