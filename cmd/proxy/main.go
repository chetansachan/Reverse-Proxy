package main

import (
	"context"
	"encoding/json"
	"fmt"
	"high-performance-proxy/internal/backend"
	"high-performance-proxy/internal/balancer"
	"high-performance-proxy/internal/config"
	"high-performance-proxy/internal/health"
	"high-performance-proxy/internal/pool"
	"high-performance-proxy/internal/proxy"
	"high-performance-proxy/pkg/logger"
	"log/slog"
	"net/http"
	"net/url"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var (
	healthCancel context.CancelFunc
	healthMu     sync.Mutex
)

type BackendStatus struct {
	URL           string `json:"url"`
	Healthy       bool   `json:"healthy"`
	ActiveConns   int32  `json:"active_conns"`
	Failures      int32  `json:"consecutive_failures"`
	TotalRequests uint64 `json:"total_requests"`
}

type StatusResponse struct {
	Strategy string          `json:"strategy"`
	Backends []BackendStatus `json:"backends"`
}

func main() {
	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		logger.Error("Failed to load initial config", slog.Any("error", err))
		return
	}

	logger.Info("Starting High-Performance Reverse Proxy",
		slog.String("listen_address", cfg.Port),
		slog.String("strategy", cfg.Strategy),
		slog.Int("backends_count", len(cfg.Backends)),
	)

	// Listen for Ctrl+C / termination signals
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tr := pool.NewTransport()
	bp := pool.NewBufferPool()
	passive := health.NewPassiveChecker(cfg.Retry.PassiveFailureThreshold)
	proxyHandler := proxy.NewProxyHandler(nil, tr, bp, passive, cfg.Retry.MaxRetries)

	// Initial configuration load
	if err := reloadConfig(proxyHandler, ctx); err != nil {
		logger.Error("Failed to initialize backends and balancer", slog.Any("error", err))
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/greet", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello World from Proxy")
	})

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		currentBalancer := proxyHandler.GetBalancer()
		if currentBalancer == nil {
			http.Error(w, `{"error": "no active balancer"}`, http.StatusServiceUnavailable)
			return
		}

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
			Strategy: cfg.Strategy,
			Backends: statusList,
		})
	})

	mux.HandleFunc("/reload", func(w http.ResponseWriter, r *http.Request) {
		if err := reloadConfig(proxyHandler, ctx); err != nil {
			http.Error(w, fmt.Sprintf("Reload failed: %v", err), http.StatusInternalServerError)
			return
		}
		fmt.Fprintln(w, "Config reloaded successfully")
	})

	mux.Handle("/", proxyHandler)

	srv := &http.Server{
		Addr:              cfg.Port,
		Handler:           logger.HTTPMiddleware(mux),
		ReadTimeout:       cfg.Timeouts.ReadTimeout,
		ReadHeaderTimeout: cfg.Timeouts.ReadHeaderTimeout,
		WriteTimeout:      cfg.Timeouts.WriteTimeout,
		IdleTimeout:       cfg.Timeouts.IdleTimeout,
	}

	// Run the server in a goroutine
	go func() {
		logger.Info("Proxy server listening", slog.String("addr", cfg.Port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server encountered fatal error", slog.Any("error", err))
		}
	}()

	// Block here until we receive SIGINT/SIGTERM
	<-ctx.Done()
	logger.Info("Shutting down gracefully...")

	// Stop health checker
	healthMu.Lock()
	if healthCancel != nil {
		healthCancel()
	}
	healthMu.Unlock()

	// Give active requests up to 30 seconds to finish
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("Shutdown forced due to error", slog.Any("error", err))
		return
	}
	logger.Info("Server exited cleanly")
}

func reloadConfig(proxyHandler *proxy.ProxyHandler, parentCtx context.Context) error {
	// 1. Parse the new config.yaml
	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		logger.Error("Failed to reload config file", slog.Any("error", err))
		return err
	}

	// 2. Create Backends with pre-allocated ReverseProxy instances
	var newBackends []*backend.Backend
	for _, rawURL := range cfg.Backends {
		u, err := url.Parse(rawURL)
		if err != nil {
			logger.Error("Failed to parse backend URL", slog.String("url", rawURL), slog.Any("error", err))
			return err
		}

		backendProxy := proxyHandler.BuildReverseProxy(u)
		b := backend.NewBackend(u, backendProxy)
		newBackends = append(newBackends, b)
	}

	// 3. Instantiate the configured load balancer strategy
	newBalancer := balancer.NewBalancer(cfg.Strategy, newBackends)

	// 4. Stop old HealthChecker (if any) and start the new HealthChecker
	healthMu.Lock()
	if healthCancel != nil {
		healthCancel()
	}
	healthCtx, cancel := context.WithCancel(parentCtx)
	healthCancel = cancel
	healthMu.Unlock()

	newHealthChecker := health.NewHealthChecker(
		newBackends,
		cfg.HealthCheck.Path,
		cfg.HealthCheck.Interval,
		cfg.HealthCheck.Timeout,
	)
	go newHealthChecker.Start(healthCtx)

	// 5. Atomically swap the balancer
	proxyHandler.SetBalancer(newBalancer)
	logger.Info("Config reloaded successfully",
		slog.String("strategy", cfg.Strategy),
		slog.Int("active_backends", len(newBackends)),
	)
	return nil
}
