package balancer

import (
	"high-performance-proxy/internal/backend"
	"net/url"
	"sync"
	"testing"
)

func makeTestBackends(count int) []*backend.Backend {
	backends := make([]*backend.Backend, count)
	for i := 0; i < count; i++ {
		u, _ := url.Parse("http://127.0.0.1:808" + string(rune('0'+i)))
		backends[i] = backend.NewBackend(u, nil)
	}
	return backends
}

func TestRoundRobin_NextBackend(t *testing.T) {
	backends := makeTestBackends(3)
	rr := NewRoundRobin(backends)

	// In order: 0, 1, 2, 0, 1, 2
	for i := 0; i < 6; i++ {
		b := rr.NextBackend()
		if b == nil {
			t.Fatalf("expected backend, got nil at step %d", i)
		}
		expectedURL := backends[i%3].URL.String()
		if b.URL.String() != expectedURL {
			t.Errorf("step %d: expected %s, got %s", i, expectedURL, b.URL.String())
		}
	}
}

func TestRoundRobin_SkipUnhealthy(t *testing.T) {
	backends := makeTestBackends(3)
	rr := NewRoundRobin(backends)

	// Mark backend 1 as unhealthy
	backends[1].SetHealthy(false)

	// Should cycle through 0, 2, 0, 2...
	for i := 0; i < 4; i++ {
		b := rr.NextBackend()
		if b == nil {
			t.Fatalf("expected backend, got nil at step %d", i)
		}
		if b.URL.String() == backends[1].URL.String() {
			t.Fatalf("unhealthy backend %s was selected", b.URL.String())
		}
	}
}

func TestRoundRobin_AllUnhealthy(t *testing.T) {
	backends := makeTestBackends(2)
	rr := NewRoundRobin(backends)

	backends[0].SetHealthy(false)
	backends[1].SetHealthy(false)

	b := rr.NextBackend()
	if b != nil {
		t.Fatalf("expected nil when all backends are unhealthy, got %v", b)
	}
}

func TestRoundRobin_Empty(t *testing.T) {
	rr := NewRoundRobin([]*backend.Backend{})
	if b := rr.NextBackend(); b != nil {
		t.Fatalf("expected nil for empty balancer, got %v", b)
	}
}

func TestRoundRobin_Concurrent(t *testing.T) {
	backends := makeTestBackends(4)
	rr := NewRoundRobin(backends)

	var wg sync.WaitGroup
	iterations := 1000
	workers := 10

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				b := rr.NextBackend()
				if b == nil {
					t.Errorf("got nil backend in concurrent test")
				}
			}
		}()
	}

	wg.Wait()
}

func BenchmarkRoundRobin_NextBackend(b *testing.B) {
	backends := makeTestBackends(8)
	rr := NewRoundRobin(backends)
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = rr.NextBackend()
		}
	})
}
