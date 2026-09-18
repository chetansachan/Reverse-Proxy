package balancer

import (
	"high-performance-proxy/internal/backend"
	"sync/atomic"
)

// RoundRobin distributes requests sequentially across healthy backends.
type RoundRobin struct {
	backends []*backend.Backend
	counter  atomic.Uint64
}

// NewRoundRobin creates a new RoundRobin balancer.
func NewRoundRobin(backends []*backend.Backend) *RoundRobin {
	return &RoundRobin{
		backends: backends,
	}
}

// NextBackend selects the next available healthy backend using atomic increment.
func (r *RoundRobin) NextBackend() *backend.Backend {
	totalBackends := len(r.backends)
	if totalBackends == 0 {
		return nil
	}

	for i := 0; i < totalBackends; i++ {
		currentCount := r.counter.Add(1)
		index := (currentCount - 1) % uint64(totalBackends)
		b := r.backends[index]

		if b.IsHealthy() {
			return b
		}
	}

	return nil
}

// GetBackends returns the underlying backend pool.
func (r *RoundRobin) GetBackends() []*backend.Backend {
	return r.backends
}
