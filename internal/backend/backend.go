package backend

import (
	"net/http/httputil"
	"net/url"
	"sync/atomic"
)

// Backend represents an upstream server target with atomic health and connection counters.
type Backend struct {
	URL           *url.URL
	ReverseProxy  *httputil.ReverseProxy
	Healthy       atomic.Bool
	ActiveConns   atomic.Int32
	Failures      atomic.Int32
	TotalRequests atomic.Uint64
}

// NewBackend creates a new Backend with healthy status set to true by default.
func NewBackend(u *url.URL, proxy *httputil.ReverseProxy) *Backend {
	b := &Backend{
		URL:          u,
		ReverseProxy: proxy,
	}
	b.Healthy.Store(true)
	return b
}

// IsHealthy returns true if the backend is marked healthy.
func (b *Backend) IsHealthy() bool {
	return b.Healthy.Load()
}

// SetHealthy updates the backend's health status.
func (b *Backend) SetHealthy(healthy bool) {
	b.Healthy.Store(healthy)
	if healthy {
		b.Failures.Store(0)
	}
}

// IncActiveConns atomically increments active connection counter.
func (b *Backend) IncActiveConns() int32 {
	return b.ActiveConns.Add(1)
}

// DecActiveConns atomically decrements active connection counter.
func (b *Backend) DecActiveConns() int32 {
	return b.ActiveConns.Add(-1)
}

// GetActiveConns returns the current active connection count.
func (b *Backend) GetActiveConns() int32 {
	return b.ActiveConns.Load()
}

// IncFailures increments the consecutive failure count and returns the new value.
func (b *Backend) IncFailures() int32 {
	return b.Failures.Add(1)
}

// ResetFailures resets the consecutive failure count to zero.
func (b *Backend) ResetFailures() {
	b.Failures.Store(0)
}

// GetFailures returns the current consecutive failure count.
func (b *Backend) GetFailures() int32 {
	return b.Failures.Load()
}

// IncRequests increments the total requests counter.
func (b *Backend) IncRequests() uint64 {
	return b.TotalRequests.Add(1)
}

// GetTotalRequests returns the total requests served by this backend.
func (b *Backend) GetTotalRequests() uint64 {
	return b.TotalRequests.Load()
}
