package health

import (
	"errors"
	"high-performance-proxy/internal/backend"
	"net/url"
	"testing"
)

func TestPassiveChecker_ThresholdTripping(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8080")
	b := backend.NewBackend(u, nil)

	checker := NewPassiveChecker(3)

	// Initial state
	if !b.IsHealthy() {
		t.Fatal("expected initially healthy")
	}

	// 1st failure
	checker.RecordFailure(b, errors.New("conn reset"))
	if !b.IsHealthy() {
		t.Fatal("should remain healthy after 1 failure")
	}

	// 2nd failure
	checker.RecordFailure(b, errors.New("timeout"))
	if !b.IsHealthy() {
		t.Fatal("should remain healthy after 2 failures")
	}

	// 3rd failure (trips threshold)
	checker.RecordFailure(b, errors.New("refused"))
	if b.IsHealthy() {
		t.Fatal("expected backend to be marked unhealthy after 3 failures")
	}

	// Success resets
	b.SetHealthy(true)
	checker.RecordSuccess(b)
	if b.GetFailures() != 0 {
		t.Errorf("expected failures to be reset to 0, got %d", b.GetFailures())
	}
}
