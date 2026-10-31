package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"webroon/internal/roon"
	"webroon/internal/server"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	if envPort := os.Getenv("PORT"); envPort != "" && *addr == ":8080" {
		*addr = ":" + envPort
	}

	roonClient := roon.NewClient()
	roonClient.Start()
	defer roonClient.Stop()

	srv := server.New(roonClient)
	httpServer := &http.Server{
		Addr:    *addr,
		Handler: srv.Routes(),
	}

	go func() {
		log.Printf("[WebRoon] Web UI listening on http://localhost%s", *addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[WebRoon] HTTP server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	log.Println("[WebRoon] Shutting down...")
}
