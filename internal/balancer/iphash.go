package balancer

import (
	"hash/fnv"
	"high-performance-proxy/internal/backend"
	"sync/atomic"
)

// IPHash provides consistent hashing distribution across healthy backends.
type IPHash struct {
	backends []*backend.Backend
	fallback *RoundRobin
	counter  atomic.Uint64
}

// NewIPHash creates a new IPHash balancer.
func NewIPHash(backends []*backend.Backend) *IPHash {
	return &IPHash{
		backends: backends,
		fallback: NewRoundRobin(backends),
	}
}

// NextBackend returns the next available healthy backend.
func (ip *IPHash) NextBackend() *backend.Backend {
	return ip.fallback.NextBackend()
}

// NextBackendForIP hashes the provided client IP to select a consistent backend.
func (ip *IPHash) NextBackendForIP(clientIP string) *backend.Backend {
	totalBackends := len(ip.backends)
	if totalBackends == 0 {
		return nil
	}

	h := fnv.New32a()
	h.Write([]byte(clientIP))
	hashVal := uint64(h.Sum32())

	for i := 0; i < totalBackends; i++ {
		idx := (hashVal + uint64(i)) % uint64(totalBackends)
		b := ip.backends[idx]
		if b.IsHealthy() {
			return b
		}
	}

	return nil
}

// GetBackends returns the underlying backend pool.
func (ip *IPHash) GetBackends() []*backend.Backend {
	return ip.backends
}
