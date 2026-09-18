package balancer

import (
	"testing"
)

func TestLeastConn_SelectsLowestActive(t *testing.T) {
	backends := makeTestBackends(3)
	lc := NewLeastConn(backends)

	backends[0].ActiveConns.Store(5)
	backends[1].ActiveConns.Store(1)
	backends[2].ActiveConns.Store(3)

	b := lc.NextBackend()
	if b == nil {
		t.Fatal("expected backend, got nil")
	}
	if b.URL.String() != backends[1].URL.String() {
		t.Errorf("expected backend 1 with lowest active conns (1), got %s", b.URL.String())
	}
}

func TestLeastConn_SkipsUnhealthy(t *testing.T) {
	backends := makeTestBackends(3)
	lc := NewLeastConn(backends)

	backends[0].ActiveConns.Store(10)
	backends[1].ActiveConns.Store(0) // lowest, but unhealthy
	backends[1].SetHealthy(false)
	backends[2].ActiveConns.Store(2)

	b := lc.NextBackend()
	if b == nil {
		t.Fatal("expected backend, got nil")
	}
	if b.URL.String() != backends[2].URL.String() {
		t.Errorf("expected backend 2 (active 2), got %s", b.URL.String())
	}
}

func TestLeastConn_AllUnhealthy(t *testing.T) {
	backends := makeTestBackends(2)
	lc := NewLeastConn(backends)

	backends[0].SetHealthy(false)
	backends[1].SetHealthy(false)

	b := lc.NextBackend()
	if b != nil {
		t.Fatalf("expected nil when all backends are unhealthy, got %v", b)
	}
}

func TestNewBalancer_Factory(t *testing.T) {
	backends := makeTestBackends(2)

	b1 := NewBalancer("round_robin", backends)
	if _, ok := b1.(*RoundRobin); !ok {
		t.Errorf("expected *RoundRobin, got %T", b1)
	}

	b2 := NewBalancer("least_conn", backends)
	if _, ok := b2.(*LeastConn); !ok {
		t.Errorf("expected *LeastConn, got %T", b2)
	}

	b3 := NewBalancer("ip_hash", backends)
	if _, ok := b3.(*IPHash); !ok {
		t.Errorf("expected *IPHash, got %T", b3)
	}
}

func BenchmarkLeastConn_NextBackend(b *testing.B) {
	backends := makeTestBackends(8)
	lc := NewLeastConn(backends)
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = lc.NextBackend()
		}
	})
}
