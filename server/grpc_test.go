package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/neo4g/neo4g/rpcpb"
)

func startGRPCServer(t *testing.T) (*Server, rpcpb.NodeClient) {
	t.Helper()
	cfg := testConfig(t)
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.GRPCAddr = "127.0.0.1:0"
	cfg.NodeID = "node1"
	cfg.Role = "primary"

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	})

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			t.Errorf("ListenAndServe: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	conn, err := rpcpb.Dial(srv.BoundGRPCAddr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return srv, rpcpb.NewNodeClient(conn)
}

func TestInternalGRPC(t *testing.T) {
	srv, client := startGRPCServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	health, err := client.Health(ctx, &rpcpb.HealthRequest{})
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.GetRole() != "primary" || health.GetNodeId() != "node1" || health.GetStatus() != "ok" {
		t.Fatalf("health: %+v", health)
	}

	created, err := client.ExecuteQuery(ctx, &rpcpb.ExecuteQueryRequest{
		Query: `CREATE (n:Person {name: "Alice"})`,
	})
	if err != nil {
		t.Fatalf("ExecuteQuery: %v", err)
	}
	if created.GetHttpStatus() != http.StatusOK {
		t.Fatalf("create status %d body %s", created.GetHttpStatus(), created.GetJsonBody())
	}
	if !json.Valid(created.GetJsonBody()) {
		t.Fatalf("create body is not JSON: %s", created.GetJsonBody())
	}

	pulled, err := client.PullWAL(ctx, &rpcpb.PullWALRequest{FromLsn: 1})
	if err != nil {
		t.Fatalf("PullWAL: %v", err)
	}
	if len(pulled.GetRecords()) == 0 {
		t.Fatal("expected WAL records after CREATE")
	}

	sum, err := client.Checksum(ctx, &rpcpb.ChecksumRequest{})
	if err != nil {
		t.Fatalf("Checksum: %v", err)
	}
	if sum.GetChecksum() == "" || sum.GetPageCount() == 0 {
		t.Fatalf("checksum: %+v", sum)
	}

	if _, err := client.SetRole(ctx, &rpcpb.SetRoleRequest{Role: "nope"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SetRole invalid: code=%v err=%v", status.Code(err), err)
	}

	if _, err := client.SetRole(ctx, &rpcpb.SetRoleRequest{Role: "replica"}); err != nil {
		t.Fatalf("SetRole replica: %v", err)
	}
	if srv.Role() != "replica" {
		t.Fatalf("role = %s", srv.Role())
	}
	_, err = client.PullWAL(ctx, &rpcpb.PullWALRequest{FromLsn: 1})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("PullWAL on replica: code=%v err=%v", status.Code(err), err)
	}

	denied, err := client.ExecuteQuery(ctx, &rpcpb.ExecuteQueryRequest{
		Query: `CREATE (n:Person {name: "Bob"})`,
	})
	if err != nil {
		t.Fatalf("replica ExecuteQuery: %v", err)
	}
	if denied.GetHttpStatus() != http.StatusForbidden {
		t.Fatalf("replica write status %d body %s", denied.GetHttpStatus(), denied.GetJsonBody())
	}

	resp, err := http.Get("http://" + srv.BoundHTTPAddr() + "/api/health")
	if err != nil {
		t.Fatalf("http health: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("http health status %d body %s", resp.StatusCode, body)
	}

	missing, err := http.Get("http://" + srv.BoundHTTPAddr() + "/api/wal/pull?from_lsn=1")
	if err != nil {
		t.Fatal(err)
	}
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("internal HTTP route still served: %d", missing.StatusCode)
	}
}
