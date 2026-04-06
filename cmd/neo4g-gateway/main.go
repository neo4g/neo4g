package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/seankohjs/neo4g/gateway"
)

func main() {
	cfg := gateway.ConfigFromEnv()

	if len(cfg.Nodes) == 0 {
		log.Fatal("neo4g-gateway: NEO4G_GW_NODES is required (e.g. node1=http://localhost:7474,node2=http://localhost:7475)")
	}

	cluster := gateway.NewCluster(cfg.Nodes)
	election := gateway.NewElection(cfg, cluster)
	gw := gateway.New(cfg, cluster)

	hb := gateway.NewHeartbeat(cfg, cluster, func() {
		log.Println("neo4g-gateway: primary is dead, triggering election")
		go election.Run()
	})
	hb.Start()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := gw.ListenAndServe(); err != nil {
			log.Fatalf("neo4g-gateway: server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("neo4g-gateway: shutting down...")

	hb.Stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := gw.Server().Shutdown(shutdownCtx); err != nil {
		log.Fatalf("neo4g-gateway: shutdown error: %v", err)
	}

	log.Println("neo4g-gateway: stopped")
}
