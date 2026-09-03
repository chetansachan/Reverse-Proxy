package main

import (
	"fmt"
	"high-performance-proxy/internal/config"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

func main() {
	config, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		log.Printf("failed to load file: %v", err)
		return
	}

	backendURL, err := url.Parse(config.Backends[0])
	if err != nil {
		log.Printf("failed to parse: %v", err)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(backendURL)	

	mux := http.NewServeMux()
	mux.HandleFunc("/greet", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("Hello World")
	})
	mux.Handle("/", proxy)

	log.Printf("Server is running on port %s...\n", config.Port)
	if err := http.ListenAndServe(config.Port, mux); err != nil {
		log.Fatal("Server failed to start: ", err)
	}
}
