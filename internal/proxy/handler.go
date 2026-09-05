package proxy

import (
	"high-performance-proxy/internal/balancer"
	"net/http"
	"net/http/httputil"
	"sync/atomic"
)

type ProxyHandler struct {
	CurrentBalancer atomic.Value // stores balancer.Balancer
	Transport       *http.Transport
}

func NewProxyHandler(b balancer.Balancer, tr *http.Transport) *ProxyHandler {
	h := &ProxyHandler{
		Transport: tr,
	}
	h.CurrentBalancer.Store(b)
	return h
}

func (h *ProxyHandler) SetBalancer(b balancer.Balancer) {
	h.CurrentBalancer.Store(b)
}

func (h *ProxyHandler) GetBalancer() balancer.Balancer {
	if b, ok := h.CurrentBalancer.Load().(balancer.Balancer); ok {
		return b
	}
	return nil
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b := h.GetBalancer()
	if b == nil {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	backend := b.NextBackend()
	if backend == nil {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	backendURL := backend.URL
	proxy := httputil.NewSingleHostReverseProxy(backendURL)

	proxy.Transport = h.Transport
	proxy.ServeHTTP(w, r)
}
