package balancer

import (
	"high-performance-proxy/internal/backend"
	"math"
	"sync/atomic"
)

// LeastConn selects the healthy backend with the lowest number of active connections.
type LeastConn struct {
	backends []*backend.Backend
	offset   atomic.Uint64
}

// NewLeastConn creates a new LeastConn balancer.
func NewLeastConn(backends []*backend.Backend) *LeastConn {
	return &LeastConn{
		backends: backends,
	}
}

// NextBackend returns the healthy backend with the minimum active connections.
// Ties are broken in round-robin fashion using the offset counter.
func (lc *LeastConn) NextBackend() *backend.Backend {
	totalBackends := len(lc.backends)
	if totalBackends == 0 {
		return nil
	}

	start := lc.offset.Add(1)
	var bestBackend *backend.Backend
	minConns := int32(math.MaxInt32)

	for i := 0; i < totalBackends; i++ {
		idx := (int(start) + i) % totalBackends
		b := lc.backends[idx]

		if !b.IsHealthy() {
			continue
		}

		conns := b.GetActiveConns()
		if conns < minConns {
			minConns = conns
			bestBackend = b
			// If we found an idle backend with 0 connections, pick immediately for speed
			if conns == 0 {
				return bestBackend
			}
		}
	}

	return bestBackend
}

// GetBackends returns the underlying backend pool.
func (lc *LeastConn) GetBackends() []*backend.Backend {
	return lc.backends
}
