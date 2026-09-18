package health

import (
	"context"
	"high-performance-proxy/internal/backend"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestHealthChecker_CheckAll(t *testing.T) {
	var healthyHits atomic.Int32
	var unhealthyHits atomic.Int32

	healthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			healthyHits.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer healthyServer.Close()

	unhealthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			unhealthyHits.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer unhealthyServer.Close()

	u1, _ := url.Parse(healthyServer.URL)
	u2, _ := url.Parse(unhealthyServer.URL)

	b1 := backend.NewBackend(u1, nil)
	b2 := backend.NewBackend(u2, nil)

	checker := NewHealthChecker([]*backend.Backend{b1, b2}, "/health", 100*time.Millisecond, 500*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	checker.CheckAll(ctx)

	if !b1.IsHealthy() {
		t.Errorf("expected b1 to be healthy")
	}
	if b2.IsHealthy() {
		t.Errorf("expected b2 to be unhealthy")
	}

	if healthyHits.Load() == 0 || unhealthyHits.Load() == 0 {
		t.Errorf("expected health probes to hit endpoints")
	}
}

func TestHealthChecker_StartStopsOnContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	b := backend.NewBackend(u, nil)

	checker := NewHealthChecker([]*backend.Backend{b}, "/health", 20*time.Millisecond, 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		checker.Start(ctx)
		close(done)
	}()

	time.Sleep(60 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("health checker did not stop after context cancel")
	}
}

func TestHealthChecker_UnreachableBackend(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:59999") // Unreachable port
	b := backend.NewBackend(u, nil)

	checker := NewHealthChecker([]*backend.Backend{b}, "/health", 50*time.Millisecond, 100*time.Millisecond)
	checker.CheckBackend(context.Background(), b)

	if b.IsHealthy() {
		t.Error("expected unreachable backend to be marked unhealthy")
	}
}
