package main

import (
	"high-performance-proxy/internal/config"
	"log"
	"net/http"
)

func main() {
	config, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		log.Printf("failed to load file: %v", err)
		return
	}

	log.Printf("Server is running on port %s...\n", config.Port)
	if err := http.ListenAndServe(config.Port, nil); err != nil {
		log.Fatal("Server failed to start: ", err)
	}
}
