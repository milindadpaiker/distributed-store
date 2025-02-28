package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
    // Get hostname to identify the container
    hostname, err := os.Hostname()
    if err != nil {
        hostname = "unknown"
    }
    
    // Configure logger with hostname prefix
    log.SetPrefix(fmt.Sprintf("[%s] ", hostname))
    
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        log.Printf("Request received on path: %s", r.URL.Path)
        fmt.Fprintf(w, "Welcome to the Distributed Store! Served by: %s", hostname)
    })

    http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        log.Printf("Health check request received")
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "Server is healthy! Node ID: %s", hostname)
    })

    port := "8080"
    log.Printf("Starting server on port %s...", port)
    if err := http.ListenAndServe(":"+port, nil); err != nil {
        log.Fatalf("Could not start server: %s\n", err.Error())
    }
}
