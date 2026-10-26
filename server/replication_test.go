package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/neo4g/neo4g/graph"
	"github.com/neo4g/neo4g/rpcpb"
)

func TestInvalidRoleRejected(t *testing.T) {
	cfg := testConfig(t)
	cfg.Role = "leader"
	if _, err := NewServer(cfg); err == nil {
		t.Fatal("expected invalid role to fail startup")
	}
	cfg.Role = "replica"
	cfg.PrimaryAddr = ""
	if _, err := NewServer(cfg); err == nil {
		t.Fatal("expected replica without NEO4G_PRIMARY_ADDR to fail startup")
	}
}

func TestContinuePrimaryLSNFromCursorFile(t *testing.T) {
	cfg := testConfig(t)
	if err := os.WriteFile(filepath.Join(cfg.DataDir, appliedLSNFile), []byte("40\n"), 0644); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	})
	if got := srv.Engine().WAL().NextLSN(); got != 41 {
		t.Fatalf("NextLSN = %d, want 41", got)
	}
}

func TestReplicatorCursorIgnoresLocalWAL(t *testing.T) {
	cfg := testConfig(t)
	e, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if _, err := e.Store().CreateNode([]string{"Person"}, map[string]graph.Value{
		"name": graph.StringValue("Ada"),
	}); err != nil {
		t.Fatal(err)
	}
	if e.WAL().NextLSN() <= 1 {
		t.Fatal("expected local WAL to advance")
	}
	r := NewReplicator(e, "127.0.0.1:1", time.Hour)
	if got := r.Applied(); got != 0 {
		t.Fatalf("applied LSN seeded from local WAL: %d", got)
	}
	if err := writeAppliedLSN(cfg.DataDir, 12); err != nil {
		t.Fatal(err)
	}
	r2 := NewReplicator(e, "127.0.0.1:1", time.Hour)
	if got := r2.Applied(); got != 12 {
		t.Fatalf("applied LSN = %d, want 12", got)
	}
}

func TestReplicatorStopIsIdempotent(t *testing.T) {
	r := &Replicator{
		primaryAddr:  "",
		pollInterval: time.Hour,
		stopCh:       make(chan struct{}),
	}
	r.Start()
	r.Stop()
	r.Stop()
}

func TestSetRoleRejectsStaleGeneration(t *testing.T) {
	srv, client := startGRPCServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	promote := rpcpb.WithGeneration(ctx, 2)
	if _, err := client.SetRole(promote, &rpcpb.SetRoleRequest{Role: "primary"}); err != nil {
		t.Fatalf("promote gen 2: %v", err)
	}
	stale := rpcpb.WithGeneration(ctx, 1)
	if _, err := client.SetRole(stale, &rpcpb.SetRoleRequest{Role: "primary"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale promote: code=%v err=%v", status.Code(err), err)
	}
	flip := rpcpb.WithGeneration(ctx, 2)
	_, err := client.SetRole(flip, &rpcpb.SetRoleRequest{Role: "replica", PrimaryAddr: srv.BoundGRPCAddr()})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("same generation role flip: code=%v err=%v", status.Code(err), err)
	}
	next := rpcpb.WithGeneration(ctx, 3)
	if _, err := client.SetRole(next, &rpcpb.SetRoleRequest{Role: "replica", PrimaryAddr: srv.BoundGRPCAddr()}); err != nil {
		t.Fatalf("demote gen 3: %v", err)
	}
	if srv.Role() != "replica" {
		t.Fatalf("role = %s", srv.Role())
	}
}

func TestPullWALReportsGapAfterCheckpoint(t *testing.T) {
	srv, client := startGRPCServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	created, err := client.ExecuteQuery(ctx, &rpcpb.ExecuteQueryRequest{
		Query: `CREATE (n:Person {name: "Ada"})`,
	})
	if err != nil || created.GetHttpStatus() != http.StatusOK {
		t.Fatalf("create: status %d err %v", created.GetHttpStatus(), err)
	}
	health, err := client.Health(ctx, &rpcpb.HealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	tip := health.GetLsn()
	if tip == 0 {
		t.Fatal("expected a primary tip after CREATE")
	}

	if err := srv.Engine().Checkpoint(); err != nil {
		t.Fatal(err)
	}
	_, err = client.PullWAL(ctx, &rpcpb.PullWALRequest{FromLsn: 1})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("PullWAL gap: code=%v err=%v", status.Code(err), err)
	}
	after, err := client.Health(ctx, &rpcpb.HealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if after.GetLsn() < tip {
		t.Fatalf("published tip dropped from %d to %d", tip, after.GetLsn())
	}
	if after.GetStatus() == "ok" && after.GetLsn() == 0 {
		t.Fatal("health hid the truncated log")
	}
}

func TestPullWALCapsBatch(t *testing.T) {
	srv, client := startGRPCServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.ExecuteQuery(ctx, &rpcpb.ExecuteQueryRequest{
		Query: `CREATE (n:Person {name: "Ada"})`,
	}); err != nil {
		t.Fatal(err)
	}
	all, err := srv.Engine().WAL().ReadFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 2 {
		t.Fatalf("expected multiple WAL records, got %d", len(all))
	}
	prev := maxPullWALBytes
	maxPullWALBytes = 1
	t.Cleanup(func() { maxPullWALBytes = prev })

	pulled, err := client.PullWAL(ctx, &rpcpb.PullWALRequest{FromLsn: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(pulled.GetRecords()) != 1 {
		t.Fatalf("capped batch = %d records, want 1", len(pulled.GetRecords()))
	}
	if pulled.GetRecords()[0].GetLsn() != uint64(all[0].LSN) {
		t.Fatalf("first record LSN %d, want %d", pulled.GetRecords()[0].GetLsn(), all[0].LSN)
	}
}

func TestAppliedLSNRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := writeAppliedLSN(dir, 7); err != nil {
		t.Fatal(err)
	}
	if got := readAppliedLSN(dir); got != 7 {
		t.Fatalf("cursor %d", got)
	}
}
