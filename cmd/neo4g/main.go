package main

import (
	"context"
	"log"
	"os"
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

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()

	var failed error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		failed = err
		if err != nil {
			log.Printf("neo4g: server error: %v", err)
		}
		stop()
	}

	log.Println("neo4g: shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("neo4g: shutdown error: %v", err)
	}

	log.Println("neo4g: stopped")
	if failed != nil {
		os.Exit(1)
	}
}
