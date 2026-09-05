package health

import (
	"context"
	"high-performance-proxy/internal/backend"
	"log"
	"net/http"
	"time"
)

type HealthChecker struct {
	Backends []*backend.Backend
	client   *http.Client
}

func NewHealthChecker(backends []*backend.Backend) *HealthChecker {
	return &HealthChecker{
		Backends: backends,
		client: &http.Client{
			Timeout: 2 * time.Second,
		},
	}
}

func (h *HealthChecker) Start(ctx context.Context) {
	h.checkAll(ctx)

	// 2. Create ticker once outside the loop
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Health checker stopped")
			return
		case <-ticker.C:
			h.checkAll(ctx)
		}
	}
}

func (h *HealthChecker) checkAll(ctx context.Context) {
	for _, b := range h.Backends {
		h.checkBackend(ctx, b)
	}
}

func (h *HealthChecker) checkBackend(ctx context.Context, b *backend.Backend) {
	healthURL := b.URL.JoinPath("/health").String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		b.SetHealthy(false)
		return
	}

	resp, err := h.client.Do(req)
	if err != nil {
		b.SetHealthy(false)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		b.SetHealthy(true)
	} else {
		b.SetHealthy(false)
	}
}
