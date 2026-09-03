package balancer

import "high-performance-proxy/internal/backend"

type Balancer interface {
	NextBackend() *backend.Backend
}
