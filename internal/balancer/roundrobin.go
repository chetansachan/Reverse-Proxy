package balancer

import (
	"high-performance-proxy/internal/backend"
	"sync/atomic"
)

type RoundRobin struct {
	backends []*backend.Backend
	counter  atomic.Uint64
}

func NewRoundRobin(backends []*backend.Backend) *RoundRobin {
	return &RoundRobin{
		backends: backends,
	}
}

func (r *RoundRobin) NextBackend() *backend.Backend {
	totalBackends := len(r.backends)
	if totalBackends == 0 {
		return nil
	}

	for i := 0; i < totalBackends; i++ {
		currentCount := r.counter.Add(1)
		index := (currentCount - 1) % uint64(totalBackends)
		b := r.backends[index]

		if b.Healthy.Load() {
			return b
		} else {
			continue
		}
	}

	return nil
}
