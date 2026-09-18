package proxy

import (
	"fmt"
	"high-performance-proxy/internal/backend"
	"high-performance-proxy/internal/balancer"
	"high-performance-proxy/internal/health"
	"high-performance-proxy/internal/pool"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestProxyHandler_RoutesToBackend(t *testing.T) {
	var requestCount atomic.Int32

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		// Check headers
		if xff := r.Header.Get("X-Forwarded-For"); xff == "" {
			t.Errorf("expected X-Forwarded-For header")
		}
		w.Header().Set("X-Custom-Header", "proxy-response")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "response-from-backend")
	}))
	defer backendServer.Close()

	u, _ := url.Parse(backendServer.URL)
	tr := pool.NewTransport()
	bp := pool.NewBufferPool()
	passive := health.NewPassiveChecker(3)

	proxyHandler := NewProxyHandler(nil, tr, bp, passive, 2)
	rp := proxyHandler.BuildReverseProxy(u)
	b := backend.NewBackend(u, rp)

	lb := balancer.NewRoundRobin([]*backend.Backend{b})
	proxyHandler.SetBalancer(lb)

	req := httptest.NewRequest(http.MethodGet, "/test-path", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	rec := httptest.NewRecorder()

	proxyHandler.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.StatusCode)
	}

	body, _ := io.ReadAll(res.Body)
	if string(body) != "response-from-backend" {
		t.Fatalf("unexpected body: %s", string(body))
	}

	if requestCount.Load() != 1 {
		t.Fatalf("expected 1 request to backend, got %d", requestCount.Load())
	}

	if b.GetTotalRequests() != 1 {
		t.Fatalf("expected backend total requests 1, got %d", b.GetTotalRequests())
	}
}

func TestProxyHandler_NoHealthyBackends(t *testing.T) {
	tr := pool.NewTransport()
	bp := pool.NewBufferPool()
	proxyHandler := NewProxyHandler(balancer.NewRoundRobin(nil), tr, bp, nil, 2)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	proxyHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable, got %d", rec.Code)
	}
}

func TestProxyHandler_BufferPool(t *testing.T) {
	bp := pool.NewBufferPool()
	buf := bp.Get()
	if len(buf) != pool.DefaultBufferSize {
		t.Fatalf("expected buffer size %d, got %d", pool.DefaultBufferSize, len(buf))
	}
	bp.Put(buf)
}

func BenchmarkProxyHandler_ServeHTTP(b *testing.B) {
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer backendServer.Close()

	u, _ := url.Parse(backendServer.URL)
	tr := pool.NewTransport()
	bp := pool.NewBufferPool()
	proxyHandler := NewProxyHandler(nil, tr, bp, nil, 2)
	rp := proxyHandler.BuildReverseProxy(u)
	backendObj := backend.NewBackend(u, rp)

	lb := balancer.NewRoundRobin([]*backend.Backend{backendObj})
	proxyHandler.SetBalancer(lb)

	req := httptest.NewRequest(http.MethodGet, "/bench", nil)
	req.RemoteAddr = "127.0.0.1:54321"

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		proxyHandler.ServeHTTP(rec, req)
	}
}

func BenchmarkBufferPool_GetPut(b *testing.B) {
	bp := pool.NewBufferPool()
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := bp.Get()
			bp.Put(buf)
		}
	})
}
