package backend

import (
	"net/url"
	"sync"
	"sync/atomic"
)

type Backend struct {
	URL         *url.URL
	Healthy     atomic.Bool
	ActiveConns atomic.Int32
	mu          sync.Mutex
}

// IsHealthy returns true if the backend is marked healthy.
func (b *Backend) IsHealthy() bool {
	return b.Healthy.Load()
}

// SetHealthy updates the backend's health status.
func (b *Backend) SetHealthy(healthy bool) {
	b.Healthy.Store(healthy)
}
