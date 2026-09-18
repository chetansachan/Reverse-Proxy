package health

import (
	"context"
	"fmt"
	"high-performance-proxy/internal/backend"
	"high-performance-proxy/pkg/logger"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// HealthChecker actively monitors the health of registered upstream backends concurrently.
type HealthChecker struct {
	Backends []*backend.Backend
	client   *http.Client
	path     string
	interval time.Duration
	timeout  time.Duration
}

// NewHealthChecker creates a HealthChecker with custom interval, timeout, and health check path.
func NewHealthChecker(backends []*backend.Backend, path string, interval, timeout time.Duration) *HealthChecker {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if path == "" {
		path = "/health"
	}

	return &HealthChecker{
		Backends: backends,
		path:     path,
		interval: interval,
		timeout:  timeout,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Start begins periodic active health checking until the context is cancelled.
func (h *HealthChecker) Start(ctx context.Context) {
	logger.Info("Active health checker started",
		slog.Int("backends", len(h.Backends)),
		slog.Duration("interval", h.interval),
		slog.Duration("timeout", h.timeout),
		slog.String("path", h.path),
	)

	// Perform immediate initial check
	h.CheckAll(ctx)

	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Active health checker stopped")
			return
		case <-ticker.C:
			h.CheckAll(ctx)
		}
	}
}

// CheckAll checks all backends concurrently.
func (h *HealthChecker) CheckAll(ctx context.Context) {
	var wg sync.WaitGroup

	for _, b := range h.Backends {
		wg.Add(1)
		go func(target *backend.Backend) {
			defer wg.Done()
			h.CheckBackend(ctx, target)
		}(b)
	}

	wg.Wait()
}

// CheckBackend executes a health probe against a single backend and logs state changes.
func (h *HealthChecker) CheckBackend(ctx context.Context, b *backend.Backend) {
	healthURL := fmt.Sprintf("%s%s", b.URL.String(), h.path)

	probeCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		h.updateHealth(b, false, err.Error())
		return
	}
	req.Header.Set("User-Agent", "HighPerformanceProxy-HealthCheck/1.0")

	resp, err := h.client.Do(req)
	if err != nil {
		h.updateHealth(b, false, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusBadRequest {
		h.updateHealth(b, true, "")
	} else {
		h.updateHealth(b, false, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}
}

func (h *HealthChecker) updateHealth(b *backend.Backend, healthy bool, reason string) {
	prev := b.IsHealthy()
	b.SetHealthy(healthy)

	if prev != healthy {
		if healthy {
			logger.Info("Backend recovered to healthy state",
				slog.String("backend", b.URL.String()),
			)
		} else {
			logger.Warn("Backend marked unhealthy",
				slog.String("backend", b.URL.String()),
				slog.String("reason", reason),
			)
		}
	}
}
