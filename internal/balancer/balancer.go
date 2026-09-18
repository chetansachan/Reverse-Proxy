package balancer

import (
	"high-performance-proxy/internal/backend"
	"strings"
)

// Balancer defines the interface for selecting upstream backends.
type Balancer interface {
	NextBackend() *backend.Backend
	GetBackends() []*backend.Backend
}

// Strategy constants
const (
	StrategyRoundRobin = "round_robin"
	StrategyLeastConn  = "least_conn"
	StrategyIPHash     = "ip_hash"
)

// NewBalancer creates a Balancer implementation based on the requested strategy.
func NewBalancer(strategy string, backends []*backend.Backend) Balancer {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case StrategyLeastConn, "least_connections", "leastconn":
		return NewLeastConn(backends)
	case StrategyIPHash, "iphash":
		return NewIPHash(backends)
	case StrategyRoundRobin, "roundrobin", "":
		fallthrough
	default:
		return NewRoundRobin(backends)
	}
}
