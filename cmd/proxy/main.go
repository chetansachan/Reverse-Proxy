package main

import (
	"context"
	"fmt"
	"high-performance-proxy/internal/backend"
	"high-performance-proxy/internal/balancer"
	"high-performance-proxy/internal/config"
	"high-performance-proxy/internal/health"
	"high-performance-proxy/internal/pool"
	"high-performance-proxy/internal/proxy"
	"log"
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

func main() {
	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Listen for Ctrl+C / termination signals
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	proxyHandler := proxy.NewProxyHandler(nil, pool.NewTransport())

	// Initial configuration load
	if err := reloadConfig(proxyHandler, ctx); err != nil {
		log.Fatalf("failed to initialize config: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/greet", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello World")
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
		Addr:    cfg.Port,
		Handler: mux,
	}

	// Run the server in a goroutine
	go func() {
		log.Printf("Server is running on port %s...\n", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Block here until we get the signal
	<-ctx.Done()
	log.Println("Shutting down gracefully...")

	// Stop health checker
	healthMu.Lock()
	if healthCancel != nil {
		healthCancel()
	}
	healthMu.Unlock()

	// Give active requests 30 seconds to finish
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// This blocks until all requests finish or timeout
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Shutdown forced: %v", err)
	}
	log.Println("Server exited cleanly")
}

func reloadConfig(proxyHandler *proxy.ProxyHandler, parentCtx context.Context) error {
	// 1. Parse the new config.yaml
	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		log.Printf("failed to reload config: %v", err)
		return err
	}

	// 2. Create a brand new list of Backends and a brand new Balancer
	var newBackends []*backend.Backend
	for _, rawURL := range cfg.Backends {
		u, err := url.Parse(rawURL)
		if err != nil {
			log.Printf("failed to parse backend URL %s: %v", rawURL, err)
			return err
		}
		b := &backend.Backend{
			URL: u,
		}
		b.SetHealthy(true)
		newBackends = append(newBackends, b)
	}

	newBalancer := balancer.NewRoundRobin(newBackends)

	// 3. Stop old HealthChecker (if any) and start a new HealthChecker
	healthMu.Lock()
	if healthCancel != nil {
		healthCancel()
	}
	healthCtx, cancel := context.WithCancel(parentCtx)
	healthCancel = cancel
	healthMu.Unlock()

	newHealthChecker := health.NewHealthChecker(newBackends)
	go newHealthChecker.Start(healthCtx)

	// 4. Atomically store the new balancer
	proxyHandler.CurrentBalancer.Store(newBalancer)
	log.Printf("Config reloaded successfully: %d backends active\n", len(newBackends))
	return nil
}
