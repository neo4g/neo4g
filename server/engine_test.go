package server

import (
	"testing"

	"github.com/neo4g/neo4g/graph"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		DataDir:    t.TempDir(),
		DBFile:     "test.db",
		WALEnabled: true,
		WALFile:    "test.wal",
		PoolSize:   128,
	}
}

func TestOpenClose(t *testing.T) {
	cfg := testConfig(t)

	e, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	e2, err := Open(cfg)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer e2.Close()
}

func TestCRUDThroughEngine(t *testing.T) {
	cfg := testConfig(t)
	e, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer e.Close()

	s := e.Store()

	nid, err := s.CreateNode([]string{"Person"}, map[string]graph.Value{
		"name": graph.StringValue("Alice"),
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	node, err := s.GetNode(nid)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if node.Properties["name"].AsString() != "Alice" {
		t.Errorf("expected Alice, got %s", node.Properties["name"].AsString())
	}

	nid2, _ := s.CreateNode([]string{"Person"}, map[string]graph.Value{
		"name": graph.StringValue("Bob"),
	})

	eid, err := s.CreateEdge("KNOWS", nid, nid2, map[string]graph.Value{
		"since": graph.IntValue(2024),
	})
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}

	edge, err := s.GetEdge(eid)
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if edge.Type != "KNOWS" {
		t.Errorf("expected KNOWS, got %s", edge.Type)
	}

	edges, err := s.GetEdges(nid, graph.Outgoing)
	if err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 outgoing edge, got %d", len(edges))
	}

	sc := e.Scanner()
	nodes, err := sc.ScanNodes([]string{"Person"})
	if err != nil {
		t.Fatalf("ScanNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("expected 2 Person nodes, got %d", len(nodes))
	}
}

func TestWALDisabled(t *testing.T) {
	cfg := testConfig(t)
	cfg.WALEnabled = false

	e, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer e.Close()

	if e.w != nil {
		t.Error("expected nil WAL when disabled")
	}
	if e.txm != nil {
		t.Error("expected nil txm when WAL disabled")
	}

	nid, err := e.Store().CreateNode([]string{"X"}, map[string]graph.Value{})
	if err != nil {
		t.Fatalf("CreateNode without WAL: %v", err)
	}

	_, err = e.Store().GetNode(nid)
	if err != nil {
		t.Fatalf("GetNode without WAL: %v", err)
	}

	if err := e.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint should be no-op: %v", err)
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("NEO4G_DATA_DIR", "/tmp/mydb")
	t.Setenv("NEO4G_DB_FILE", "custom.db")
	t.Setenv("NEO4G_WAL_ENABLED", "false")
	t.Setenv("NEO4G_WAL_FILE", "custom.wal")
	t.Setenv("NEO4G_POOL_SIZE", "256")

	cfg := ConfigFromEnv()

	if cfg.DataDir != "/tmp/mydb" {
		t.Errorf("DataDir: got %q", cfg.DataDir)
	}
	if cfg.DBFile != "custom.db" {
		t.Errorf("DBFile: got %q", cfg.DBFile)
	}
	if cfg.WALEnabled {
		t.Error("expected WALEnabled=false")
	}
	if cfg.WALFile != "custom.wal" {
		t.Errorf("WALFile: got %q", cfg.WALFile)
	}
	if cfg.PoolSize != 256 {
		t.Errorf("PoolSize: got %d", cfg.PoolSize)
	}
}

func TestReopenPersistence(t *testing.T) {
	cfg := testConfig(t)

	e, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	nid, _ := e.Store().CreateNode([]string{"Person"}, map[string]graph.Value{
		"name": graph.StringValue("Charlie"),
	})
	nid2, _ := e.Store().CreateNode([]string{"Person"}, map[string]graph.Value{
		"name": graph.StringValue("Dana"),
	})
	e.Store().CreateEdge("FRIEND", nid, nid2, map[string]graph.Value{})

	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	e2, err := Open(cfg)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer e2.Close()

	node, err := e2.Store().GetNode(nid)
	if err != nil {
		t.Fatalf("GetNode after reopen: %v", err)
	}
	if node.Properties["name"].AsString() != "Charlie" {
		t.Errorf("expected Charlie, got %s", node.Properties["name"].AsString())
	}

	edges, err := e2.Store().GetEdges(nid, graph.Outgoing)
	if err != nil {
		t.Fatalf("GetEdges after reopen: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge after reopen, got %d", len(edges))
	}
	if edges[0].Type != "FRIEND" {
		t.Errorf("expected FRIEND, got %s", edges[0].Type)
	}
}
