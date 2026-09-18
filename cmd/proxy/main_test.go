package main

import (
	"context"
	"encoding/json"
	"fmt"
	"high-performance-proxy/internal/backend"
	"high-performance-proxy/internal/balancer"
	"high-performance-proxy/internal/health"
	"high-performance-proxy/internal/pool"
	"high-performance-proxy/internal/proxy"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// MockBackend creates a mock upstream HTTP server simulating healthy/unhealthy states and tracking hits.
type MockBackend struct {
	ID         int
	Server     *httptest.Server
	URL        *url.URL
	HitCount   atomic.Uint64
	IsAlive    atomic.Bool
	Delay      time.Duration
	BackendObj *backend.Backend
}

func newMockBackend(id int, delay time.Duration) *MockBackend {
	mb := &MockBackend{
		ID:    id,
		Delay: delay,
	}
	mb.IsAlive.Store(true)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			if mb.IsAlive.Load() {
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprintf(w, "backend-%d healthy", mb.ID)
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = fmt.Fprintf(w, "backend-%d dead", mb.ID)
			}
			return
		}

		if !mb.IsAlive.Load() {
			http.Error(w, fmt.Sprintf("backend %d is down", mb.ID), http.StatusInternalServerError)
			return
		}

		if mb.Delay > 0 {
			time.Sleep(mb.Delay)
		}

		mb.HitCount.Add(1)

		// Validate that standard forwarding headers are present
		if r.Header.Get("X-Forwarded-For") == "" {
			http.Error(w, "missing X-Forwarded-For", http.StatusBadRequest)
			return
		}

		w.Header().Set("X-Backend-ID", fmt.Sprintf("%d", mb.ID))
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "response-from-backend-%d", mb.ID)
	})

	mb.Server = httptest.NewServer(handler)
	mb.URL, _ = url.Parse(mb.Server.URL)
	return mb
}

func (mb *MockBackend) Close() {
	mb.Server.Close()
}

// TestProxy_1000ConcurrentUsers_EndToEnd tests 1,000 concurrent simulated users sending 10,000 requests across 8 backend instances.
func TestProxy_1000ConcurrentUsers_EndToEnd(t *testing.T) {
	const (
		numBackends = 8
		numUsers    = 1000
		reqsPerUser = 10
		totalReqs   = numUsers * reqsPerUser
	)

	// 1. Create 8 mock upstream backend instances
	mocks := make([]*MockBackend, numBackends)
	var backendObjs []*backend.Backend

	tr := pool.NewTransport()
	bp := pool.NewBufferPool()
	passive := health.NewPassiveChecker(3)
	proxyHandler := proxy.NewProxyHandler(nil, tr, bp, passive, 2)

	for i := 0; i < numBackends; i++ {
		mocks[i] = newMockBackend(i+1, 0)
		defer mocks[i].Close()

		rp := proxyHandler.BuildReverseProxy(mocks[i].URL)
		b := backend.NewBackend(mocks[i].URL, rp)
		mocks[i].BackendObj = b
		backendObjs = append(backendObjs, b)
	}

	// 2. Set RoundRobin load balancer
	lb := balancer.NewRoundRobin(backendObjs)
	proxyHandler.SetBalancer(lb)

	// 3. Start proxy server
	proxyServer := httptest.NewServer(proxyHandler)
	defer proxyServer.Close()

	// 4. Launch 1,000 concurrent simulated users with connection pooling
	var wg sync.WaitGroup
	var successCount atomic.Uint64
	var failureCount atomic.Uint64
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        numUsers * 2,
			MaxIdleConnsPerHost: numUsers * 2,
			MaxConnsPerHost:     numUsers * 2,
			IdleConnTimeout:     90 * time.Second,
			DisableKeepAlives:   false,
		},
	}

	for user := 0; user < numUsers; user++ {
		wg.Add(1)
		go func(userID int) {
			defer wg.Done()

			// 500 microsecond ramp-up to allow Windows OS TCP socket stack to establish Keep-Alive connections
			time.Sleep(time.Duration(userID) * 500 * time.Microsecond)

			for r := 0; r < reqsPerUser; r++ {
				// Simulated user think time between successive page/API requests
				time.Sleep(time.Duration(5+(userID%10)) * time.Millisecond)

				reqURL := fmt.Sprintf("%s/api/user/%d/resource/%d", proxyServer.URL, userID, r)
				var resp *http.Response
				var err error

				// Retry up to 5 times to handle local OS TCP handshake backlog bursts
				for attempt := 0; attempt < 5; attempt++ {
					req, reqErr := http.NewRequest(http.MethodGet, reqURL, nil)
					if reqErr != nil {
						err = reqErr
						break
					}
					req.Header.Set("User-Agent", fmt.Sprintf("SimulatedUser-%d", userID))
					resp, err = client.Do(req)
					if err == nil {
						break
					}
					time.Sleep(15 * time.Millisecond)
				}

				if err != nil {
					failureCount.Add(1)
					if failureCount.Load() <= 5 {
						t.Logf("Client Do error: %v", err)
					}
					continue
				}

				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()

				if resp.StatusCode == http.StatusOK && len(body) > 0 {
					successCount.Add(1)
				} else {
					failureCount.Add(1)
					if failureCount.Load() <= 5 {
						t.Logf("Status error: %d, body: %s", resp.StatusCode, string(body))
					}
				}
			}
		}(user)
	}

	wg.Wait()

	// 5. Verification & Assertions
	if failures := failureCount.Load(); failures > 0 {
		t.Fatalf("Load test failed: %d failures encountered out of %d total requests", failures, totalReqs)
	}

	if successes := successCount.Load(); successes != totalReqs {
		t.Fatalf("Expected %d successful requests, got %d", totalReqs, successes)
	}

	t.Logf("SUCCESS: Processed %d requests across %d concurrent users (100%% success rate)!", totalReqs, numUsers)

	// Verify balanced distribution across all 8 backend instances
	expectedPerBackend := totalReqs / numBackends
	for _, mb := range mocks {
		hits := mb.HitCount.Load()
		t.Logf("Backend Instance %d served: %d requests (expected ~%d)", mb.ID, hits, expectedPerBackend)
		if hits == 0 {
			t.Errorf("Backend Instance %d received 0 requests!", mb.ID)
		}
		diff := int(hits) - expectedPerBackend
		if diff < -100 || diff > 100 {
			t.Errorf("Backend Instance %d hit count %d deviated too much from expected %d", mb.ID, hits, expectedPerBackend)
		}
	}
}

// TestProxy_1000Users_UnderBackendFailover verifies failover for 1,000 users when 4 backends crash mid-traffic.
func TestProxy_1000Users_UnderBackendFailover(t *testing.T) {
	const (
		numBackends = 8
		numUsers    = 1000
		reqsPerUser = 5
		totalReqs   = numUsers * reqsPerUser
	)

	mocks := make([]*MockBackend, numBackends)
	var backendObjs []*backend.Backend

	tr := pool.NewTransport()
	bp := pool.NewBufferPool()
	passive := health.NewPassiveChecker(2)
	proxyHandler := proxy.NewProxyHandler(nil, tr, bp, passive, 2)

	for i := 0; i < numBackends; i++ {
		mocks[i] = newMockBackend(i+1, 0)
		defer mocks[i].Close()

		rp := proxyHandler.BuildReverseProxy(mocks[i].URL)
		b := backend.NewBackend(mocks[i].URL, rp)
		mocks[i].BackendObj = b
		backendObjs = append(backendObjs, b)
	}

	lb := balancer.NewRoundRobin(backendObjs)
	proxyHandler.SetBalancer(lb)

	// Run active health checker with fast tick for the test
	checker := health.NewHealthChecker(backendObjs, "/health", 40*time.Millisecond, 40*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go checker.Start(ctx)

	proxyServer := httptest.NewServer(proxyHandler)
	defer proxyServer.Close()

	// Halfway through, kill backends 1, 2, 3, 4
	go func() {
		time.Sleep(30 * time.Millisecond)
		for i := 0; i < 4; i++ {
			mocks[i].IsAlive.Store(false)
		}
		checker.CheckAll(ctx) // trigger active health check
	}()

	var wg sync.WaitGroup
	var successCount atomic.Uint64
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        numUsers,
			MaxIdleConnsPerHost: numUsers,
			MaxConnsPerHost:     numUsers,
		},
	}

	for user := 0; user < numUsers; user++ {
		wg.Add(1)
		go func(userID int) {
			defer wg.Done()
			for r := 0; r < reqsPerUser; r++ {
				time.Sleep(2 * time.Millisecond)
				resp, err := client.Get(proxyServer.URL + "/failover-test")
				if err == nil {
					if resp.StatusCode == http.StatusOK {
						successCount.Add(1)
					}
					_ = resp.Body.Close()
				}
			}
		}(user)
	}

	wg.Wait()

	t.Logf("Completed failover test with %d successful requests", successCount.Load())

	// Verify that surviving backends (5, 6, 7, 8) handled traffic
	for i := 4; i < numBackends; i++ {
		hits := mocks[i].HitCount.Load()
		t.Logf("Surviving Backend Instance %d served: %d requests", mocks[i].ID, hits)
		if hits == 0 {
			t.Errorf("Surviving Backend Instance %d received 0 requests!", mocks[i].ID)
		}
	}
}

// TestProxy_LeastConnections_1000Users verifies traffic shifts to faster instances under 1000 users.
func TestProxy_LeastConnections_1000Users(t *testing.T) {
	const numUsers = 1000

	// Backend 1 is slow (20ms), Backend 2 is medium (5ms), Backend 3 is super fast (0ms)
	slowBackend := newMockBackend(1, 20*time.Millisecond)
	medBackend := newMockBackend(2, 5*time.Millisecond)
	fastBackend := newMockBackend(3, 0)
	defer slowBackend.Close()
	defer medBackend.Close()
	defer fastBackend.Close()

	tr := pool.NewTransport()
	bp := pool.NewBufferPool()
	proxyHandler := proxy.NewProxyHandler(nil, tr, bp, nil, 2)

	b1 := backend.NewBackend(slowBackend.URL, proxyHandler.BuildReverseProxy(slowBackend.URL))
	b2 := backend.NewBackend(medBackend.URL, proxyHandler.BuildReverseProxy(medBackend.URL))
	b3 := backend.NewBackend(fastBackend.URL, proxyHandler.BuildReverseProxy(fastBackend.URL))

	lb := balancer.NewLeastConn([]*backend.Backend{b1, b2, b3})
	proxyHandler.SetBalancer(lb)

	proxyServer := httptest.NewServer(proxyHandler)
	defer proxyServer.Close()

	var wg sync.WaitGroup
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        numUsers,
			MaxIdleConnsPerHost: numUsers,
			MaxConnsPerHost:     numUsers,
		},
	}

	for u := 0; u < numUsers; u++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				resp, err := client.Get(proxyServer.URL + "/leastconn")
				if err == nil {
					_ = resp.Body.Close()
				}
			}
		}()
	}

	wg.Wait()

	slowHits := slowBackend.HitCount.Load()
	medHits := medBackend.HitCount.Load()
	fastHits := fastBackend.HitCount.Load()

	t.Logf("LeastConn (1000 users) Hits - Slow: %d, Med: %d, Fast: %d", slowHits, medHits, fastHits)

	if fastHits <= slowHits {
		t.Errorf("Expected fast backend to handle more requests than slow backend: fast=%d, slow=%d", fastHits, slowHits)
	}
}

// TestProxy_HotReload_Under1000Users verifies zero dropped requests during atomic config swap under 1000 users.
func TestProxy_HotReload_Under1000Users(t *testing.T) {
	const numUsers = 1000

	b1 := newMockBackend(1, 0)
	b2 := newMockBackend(2, 0)
	b3 := newMockBackend(3, 0)
	b4 := newMockBackend(4, 0)
	defer b1.Close()
	defer b2.Close()
	defer b3.Close()
	defer b4.Close()

	tr := pool.NewTransport()
	bp := pool.NewBufferPool()
	proxyHandler := proxy.NewProxyHandler(nil, tr, bp, nil, 2)

	obj1 := backend.NewBackend(b1.URL, proxyHandler.BuildReverseProxy(b1.URL))
	obj2 := backend.NewBackend(b2.URL, proxyHandler.BuildReverseProxy(b2.URL))
	proxyHandler.SetBalancer(balancer.NewRoundRobin([]*backend.Backend{obj1, obj2}))

	proxyServer := httptest.NewServer(proxyHandler)
	defer proxyServer.Close()

	var wg sync.WaitGroup
	var successCount atomic.Uint64
	var failureCount atomic.Uint64
	stopTraffic := make(chan struct{})

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        numUsers,
			MaxIdleConnsPerHost: numUsers,
			MaxConnsPerHost:     numUsers,
		},
	}

	// Start 1,000 concurrent workers
	for u := 0; u < numUsers; u++ {
		wg.Add(1)
		go func(userID int) {
			defer wg.Done()

			// 500 microsecond ramp-up to allow Windows OS TCP socket accept queue to establish Keep-Alive connections
			time.Sleep(time.Duration(userID) * 500 * time.Microsecond)

			for {
				select {
				case <-stopTraffic:
					return
				default:
					var resp *http.Response
					var err error

					for attempt := 0; attempt < 5; attempt++ {
						resp, err = client.Get(proxyServer.URL + "/live-test")
						if err == nil {
							break
						}
						time.Sleep(15 * time.Millisecond)
					}

					if err != nil {
						failureCount.Add(1)
					} else {
						if resp.StatusCode == http.StatusOK {
							successCount.Add(1)
						} else {
							failureCount.Add(1)
						}
						_ = resp.Body.Close()
					}
					time.Sleep(time.Duration(15+(userID%10)) * time.Millisecond)
				}
			}
		}(u)
	}

	// Let traffic ramp up
	time.Sleep(60 * time.Millisecond)

	// Trigger hot swap to new backends 3 & 4
	obj3 := backend.NewBackend(b3.URL, proxyHandler.BuildReverseProxy(b3.URL))
	obj4 := backend.NewBackend(b4.URL, proxyHandler.BuildReverseProxy(b4.URL))
	newLB := balancer.NewRoundRobin([]*backend.Backend{obj3, obj4})
	proxyHandler.SetBalancer(newLB)

	// Let traffic run on new backends
	time.Sleep(60 * time.Millisecond)

	close(stopTraffic)
	wg.Wait()

	if failures := failureCount.Load(); failures > 0 {
		t.Fatalf("Hot reload dropped %d requests under 1000 users!", failures)
	}

	t.Logf("Hot reload succeeded under 1000 users with %d successful requests and 0 drops!", successCount.Load())
	t.Logf("New backends received hits: B3=%d, B4=%d", b3.HitCount.Load(), b4.HitCount.Load())

	if b3.HitCount.Load() == 0 || b4.HitCount.Load() == 0 {
		t.Errorf("New backends should have received traffic after hot reload!")
	}
}

// TestProxy_StatusEndpoint_JSON verifies status observability payload.
func TestProxy_StatusEndpoint_JSON(t *testing.T) {
	mb := newMockBackend(1, 0)
	defer mb.Close()

	tr := pool.NewTransport()
	bp := pool.NewBufferPool()
	proxyHandler := proxy.NewProxyHandler(nil, tr, bp, nil, 2)

	b := backend.NewBackend(mb.URL, proxyHandler.BuildReverseProxy(mb.URL))
	proxyHandler.SetBalancer(balancer.NewRoundRobin([]*backend.Backend{b}))

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		currentBalancer := proxyHandler.GetBalancer()
		backends := currentBalancer.GetBackends()
		statusList := make([]BackendStatus, 0, len(backends))
		for _, b := range backends {
			statusList = append(statusList, BackendStatus{
				URL:           b.URL.String(),
				Healthy:       b.IsHealthy(),
				ActiveConns:   b.GetActiveConns(),
				Failures:      b.GetFailures(),
				TotalRequests: b.GetTotalRequests(),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(StatusResponse{
			Strategy: "round_robin",
			Backends: statusList,
		})
	})
	mux.Handle("/", proxyHandler)

	server := httptest.NewServer(mux)
	defer server.Close()

	// Send a proxy request first
	resp1, err := http.Get(server.URL + "/test")
	if err != nil || resp1.StatusCode != http.StatusOK {
		t.Fatalf("initial request failed: %v", err)
	}
	_ = resp1.Body.Close()

	// Query /status
	resp2, err := http.Get(server.URL + "/status")
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}
	defer resp2.Body.Close()

	var status StatusResponse
	if err := json.NewDecoder(resp2.Body).Decode(&status); err != nil {
		t.Fatalf("failed to decode status json: %v", err)
	}

	if status.Strategy != "round_robin" {
		t.Errorf("expected strategy round_robin, got %s", status.Strategy)
	}
	if len(status.Backends) != 1 {
		t.Fatalf("expected 1 backend in status, got %d", len(status.Backends))
	}
	if status.Backends[0].TotalRequests != 1 {
		t.Errorf("expected total requests 1, got %d", status.Backends[0].TotalRequests)
	}
	if !status.Backends[0].Healthy {
		t.Errorf("expected backend to be healthy")
	}
}

// TestProxy_100kRequests_AcrossAllServers stress-tests the reverse proxy with 1 Lakh (100,000) requests across 8 backend servers.
func TestProxy_100kRequests_AcrossAllServers(t *testing.T) {
	const (
		numBackends = 8
		totalReqs   = 100_000 // 1 Lakh requests
		numWorkers  = 100
		reqsPerWorker = totalReqs / numWorkers // 1,000 requests per worker
	)

	// 1. Create 8 mock upstream backend instances
	mocks := make([]*MockBackend, numBackends)
	var backendObjs []*backend.Backend

	tr := pool.NewTransport()
	bp := pool.NewBufferPool()
	passive := health.NewPassiveChecker(5)
	proxyHandler := proxy.NewProxyHandler(nil, tr, bp, passive, 2)

	for i := 0; i < numBackends; i++ {
		mocks[i] = newMockBackend(i+1, 0)
		defer mocks[i].Close()

		rp := proxyHandler.BuildReverseProxy(mocks[i].URL)
		b := backend.NewBackend(mocks[i].URL, rp)
		mocks[i].BackendObj = b
		backendObjs = append(backendObjs, b)
	}

	// 2. Set RoundRobin load balancer across all 8 servers
	lb := balancer.NewRoundRobin(backendObjs)
	proxyHandler.SetBalancer(lb)

	// 3. Start proxy server
	proxyServer := httptest.NewServer(proxyHandler)
	defer proxyServer.Close()

	// 4. Create high-throughput HTTP client with connection pooling
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        numWorkers * 4,
			MaxIdleConnsPerHost: numWorkers * 4,
			MaxConnsPerHost:     numWorkers * 4,
			IdleConnTimeout:     90 * time.Second,
			DisableKeepAlives:   false,
		},
	}

	var wg sync.WaitGroup
	var successCount atomic.Uint64
	var failureCount atomic.Uint64

	startTime := time.Now()

	// 5. Spawn 100 concurrent workers processing 1,000 requests each
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for i := 0; i < reqsPerWorker; i++ {
				reqURL := fmt.Sprintf("%s/stress/worker/%d/req/%d", proxyServer.URL, workerID, i)
				req, err := http.NewRequest(http.MethodGet, reqURL, nil)
				if err != nil {
					failureCount.Add(1)
					continue
				}
				req.Header.Set("User-Agent", fmt.Sprintf("StressWorker-%d", workerID))

				resp, err := client.Do(req)
				if err != nil {
					failureCount.Add(1)
					continue
				}

				if resp.StatusCode == http.StatusOK {
					successCount.Add(1)
				} else {
					failureCount.Add(1)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}(w)
	}

	wg.Wait()
	duration := time.Since(startTime)
	rps := float64(totalReqs) / duration.Seconds()

	// 6. Assertions & Verification
	if failures := failureCount.Load(); failures > 0 {
		t.Fatalf("1 Lakh test encountered %d failures out of %d total requests", failures, totalReqs)
	}

	if successes := successCount.Load(); successes != totalReqs {
		t.Fatalf("Expected %d successful requests, got %d", totalReqs, successes)
	}

	t.Logf("=== 1 LAKH (100,000) REQUESTS BENCHMARK SUMMARY ===")
	t.Logf("Total Requests Processed: %d", totalReqs)
	t.Logf("Total Time Taken:        %v", duration)
	t.Logf("Throughput (RPS):        %.2f requests/second", rps)
	t.Logf("Success Rate:            100%% (0 errors)")

	// 7. Verify perfectly balanced distribution across all 8 backend servers (12,500 requests each)
	expectedPerBackend := uint64(totalReqs / numBackends)
	for _, mb := range mocks {
		hits := mb.HitCount.Load()
		t.Logf("Backend Server %d: %d hits (Expected: %d)", mb.ID, hits, expectedPerBackend)
		if hits != expectedPerBackend {
			t.Errorf("Backend %d served %d requests, expected exactly %d", mb.ID, hits, expectedPerBackend)
		}
	}
}

// BenchmarkProxy_1000ConcurrentUsers benchmarks proxy throughput with 1,000 parallel workers.
func BenchmarkProxy_1000ConcurrentUsers(b *testing.B) {
	mb := newMockBackend(1, 0)
	defer mb.Close()

	tr := pool.NewTransport()
	bp := pool.NewBufferPool()
	proxyHandler := proxy.NewProxyHandler(nil, tr, bp, nil, 2)

	backendObj := backend.NewBackend(mb.URL, proxyHandler.BuildReverseProxy(mb.URL))
	proxyHandler.SetBalancer(balancer.NewRoundRobin([]*backend.Backend{backendObj}))

	proxyServer := httptest.NewServer(proxyHandler)
	defer proxyServer.Close()

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        2000,
			MaxIdleConnsPerHost: 2000,
		},
	}

	b.SetParallelism(1000)
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := client.Get(proxyServer.URL + "/bench")
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}
	})
}

