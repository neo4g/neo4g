package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/neo4g/neo4g/server"
)

func main() {
	cfg := server.ConfigFromEnv()

	srv, err := server.NewServer(cfg)
	if err != nil {
		log.Fatalf("neo4g: failed to start: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Fatalf("neo4g: server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("neo4g: shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("neo4g: shutdown error: %v", err)
	}

	log.Println("neo4g: stopped")
}
