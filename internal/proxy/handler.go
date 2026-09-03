package proxy

import (
	"high-performance-proxy/internal/balancer"
	"net/http"
	"net/http/httputil"
)

type ProxyHandler struct {
	balancer.Balancer
	Transport *http.Transport
}

func NewProxyHandler(b balancer.Balancer, tr *http.Transport) *ProxyHandler {
	return &ProxyHandler{
		Balancer:  b,
		Transport: tr,
	}
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	nextBackend := h.NextBackend()
	if nextBackend == nil {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	backendURL := nextBackend.URL
	proxy := httputil.NewSingleHostReverseProxy(backendURL)

	proxy.Transport = h.Transport
	proxy.ServeHTTP(w, r)
}
