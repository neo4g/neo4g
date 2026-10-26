package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/neo4g/neo4g/gateway"
)

func main() {
	cfg := gateway.ConfigFromEnv()

	if len(cfg.Nodes) == 0 {
		log.Fatal("neo4g-gateway: NEO4G_GW_NODES is required (e.g. node1=127.0.0.1:9474,node2=127.0.0.1:9475)")
	}

	cluster := gateway.NewCluster(cfg.Nodes)
	dialer := gateway.NewDialer()
	election := gateway.NewElection(cfg, cluster, dialer)
	gw := gateway.New(cfg, cluster, dialer)

	hb := gateway.NewHeartbeat(cfg, cluster, dialer, func() {
		log.Println("neo4g-gateway: primary is dead, triggering election")
		election.Trigger()
	})
	hb.OnStalePrimary(func(staleNodeID string) {
		election.TriggerDemote(staleNodeID)
	})
	hb.OnWrongPrimary(func(replicaNodeID, shouldFollow string) {
		election.TriggerRedirect(replicaNodeID, shouldFollow)
	})
	hb.Start()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- gw.ListenAndServe()
	}()

	var failed error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		failed = err
		if err != nil {
			log.Printf("neo4g-gateway: server error: %v", err)
		}
		stop()
	}

	log.Println("neo4g-gateway: shutting down...")
	hb.Stop()
	election.Shutdown()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := gw.Server().Shutdown(shutdownCtx); err != nil {
		log.Fatalf("neo4g-gateway: shutdown error: %v", err)
	}
	dialer.Close()

	log.Println("neo4g-gateway: stopped")
	if failed != nil {
		os.Exit(1)
	}
}
