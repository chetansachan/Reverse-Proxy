package proxy

import (
	"context"
	"high-performance-proxy/internal/backend"
	"high-performance-proxy/internal/balancer"
	"high-performance-proxy/internal/health"
	"high-performance-proxy/internal/pool"
	"high-performance-proxy/pkg/logger"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
)

type contextKey string

const backendKey contextKey = "proxy_backend"

// ProxyHandler routes incoming requests to upstream backends.
type ProxyHandler struct {
	CurrentBalancer atomic.Value // stores balancer.Balancer
	Transport       *http.Transport
	BufferPool      *pool.BufferPool
	Passive         *health.PassiveChecker
	MaxRetries      int
}

// NewProxyHandler initializes a ProxyHandler.
func NewProxyHandler(b balancer.Balancer, tr *http.Transport, bp *pool.BufferPool, passive *health.PassiveChecker, maxRetries int) *ProxyHandler {
	if tr == nil {
		tr = pool.NewTransport()
	}
	if bp == nil {
		bp = pool.NewBufferPool()
	}
	if passive == nil {
		passive = health.NewPassiveChecker(3)
	}
	if maxRetries <= 0 {
		maxRetries = 2
	}

	h := &ProxyHandler{
		Transport:  tr,
		BufferPool: bp,
		Passive:    passive,
		MaxRetries: maxRetries,
	}

	if b != nil {
		h.CurrentBalancer.Store(b)
	}

	return h
}

// SetBalancer updates the active load balancer atomically.
func (h *ProxyHandler) SetBalancer(b balancer.Balancer) {
	h.CurrentBalancer.Store(b)
}

// GetBalancer returns the active load balancer.
func (h *ProxyHandler) GetBalancer() balancer.Balancer {
	if b, ok := h.CurrentBalancer.Load().(balancer.Balancer); ok {
		return b
	}
	return nil
}

// BuildReverseProxy constructs and configures a dedicated ReverseProxy for a backend target.
func (h *ProxyHandler) BuildReverseProxy(target *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = h.Transport
	proxy.BufferPool = h.BufferPool

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host

		// Set standard forwarding headers
		clientIP, _, err := net.SplitHostPort(req.RemoteAddr)
		if err == nil {
			if prior := req.Header.Get("X-Forwarded-For"); prior != "" {
				clientIP = prior + ", " + clientIP
			}
			req.Header.Set("X-Forwarded-For", clientIP)
			req.Header.Set("X-Real-IP", clientIP)
		}

		if req.TLS != nil {
			req.Header.Set("X-Forwarded-Proto", "https")
		} else {
			req.Header.Set("X-Forwarded-Proto", "http")
		}
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		if reqBackend, ok := resp.Request.Context().Value(backendKey).(*backend.Backend); ok {
			h.Passive.RecordSuccess(reqBackend)
		}
		return nil
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Error("Proxy forwarding error",
			slog.String("path", r.URL.Path),
			slog.String("target", target.String()),
			slog.Any("error", err),
		)

		if reqBackend, ok := r.Context().Value(backendKey).(*backend.Backend); ok {
			h.Passive.RecordFailure(reqBackend, err)
		}

		http.Error(w, "Bad Gateway: Upstream server unavailable", http.StatusBadGateway)
	}

	return proxy
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b := h.GetBalancer()
	if b == nil {
		http.Error(w, "Service Unavailable: No load balancer configured", http.StatusServiceUnavailable)
		return
	}

	var targetBackend *backend.Backend

	// If using IP hash strategy and method is available, pass client IP
	if ipBalancer, ok := b.(*balancer.IPHash); ok {
		clientIP := getClientIP(r)
		targetBackend = ipBalancer.NextBackendForIP(clientIP)
	} else {
		targetBackend = b.NextBackend()
	}

	if targetBackend == nil {
		http.Error(w, "Service Unavailable: No healthy backends available", http.StatusServiceUnavailable)
		return
	}

	// Track concurrency and request count
	targetBackend.IncActiveConns()
	defer targetBackend.DecActiveConns()
	targetBackend.IncRequests()

	// Inject backend pointer into request context for error/success tracking
	ctxWithBackend := context.WithValue(r.Context(), backendKey, targetBackend)
	reqWithBackend := r.WithContext(ctxWithBackend)

	// Forward request using the pre-allocated cached reverse proxy
	targetBackend.ReverseProxy.ServeHTTP(w, reqWithBackend)
}

func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return strings.TrimSpace(xrip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
