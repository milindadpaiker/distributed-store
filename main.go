package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
    // Get the hostname (container ID)
    hostname, err := os.Hostname()
    if err != nil {
        log.Printf("Error getting hostname: %v", err)
        hostname = "unknown"
    }
    
    // Configure logger to include hostname
    log.SetPrefix(fmt.Sprintf("[%s] ", hostname))
    
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        log.Printf("Request received on path: %s", r.URL.Path)
        fmt.Fprintf(w, "%s Welcome to the Distributed Store!", hostname)
    })

    http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        log.Printf("Health check request received")
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "Server is healthy11")
    })

    port := "8080"
    log.Printf("Starting server on port %s...", port)
    if err := http.ListenAndServe(":"+port, nil); err != nil {
        log.Fatalf("Could not start server: %s\n", err.Error())
    }
}
