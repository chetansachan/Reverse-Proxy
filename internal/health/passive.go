package health

import (
	"high-performance-proxy/internal/backend"
	"high-performance-proxy/pkg/logger"
	"log/slog"
)

// PassiveChecker manages passive failure counting and automatic degradation of failing backends.
type PassiveChecker struct {
	FailureThreshold int
}

// NewPassiveChecker creates a PassiveChecker with a configurable failure threshold.
func NewPassiveChecker(failureThreshold int) *PassiveChecker {
	if failureThreshold <= 0 {
		failureThreshold = 3
	}
	return &PassiveChecker{
		FailureThreshold: failureThreshold,
	}
}

// RecordSuccess resets the failure count on a successful request.
func (p *PassiveChecker) RecordSuccess(b *backend.Backend) {
	if b == nil {
		return
	}
	b.ResetFailures()
}

// RecordFailure increments failure count and marks the backend unhealthy if threshold is exceeded.
func (p *PassiveChecker) RecordFailure(b *backend.Backend, err error) {
	if b == nil {
		return
	}

	failures := b.IncFailures()
	if int(failures) >= p.FailureThreshold && b.IsHealthy() {
		b.SetHealthy(false)
		logger.Warn("Backend trip to unhealthy state by passive health checker",
			slog.String("backend", b.URL.String()),
			slog.Int("consecutive_failures", int(failures)),
			slog.Any("error", err),
		)
	}
}
