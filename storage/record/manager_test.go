package record

import (
	"path/filepath"
	"testing"

	"github.com/seankohjs/neo4g/graph"
	"github.com/seankohjs/neo4g/storage/disk"
	"github.com/seankohjs/neo4g/storage/index"
	"github.com/seankohjs/neo4g/storage/pool"
	"github.com/seankohjs/neo4g/storage/txn"
	"github.com/seankohjs/neo4g/storage/wal"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	dm, err := disk.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { dm.Close() })

	bp := pool.New(dm, 100)
	idx, err := index.NewManager(bp, dm)
	if err != nil {
		t.Fatalf("index.NewManager: %v", err)
	}
	rm, err := NewManager(bp, dm, idx)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return rm
}

func newTestManagerWithTx(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	dm, err := disk.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { dm.Close() })

	walPath := filepath.Join(dir, "test.wal")
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	t.Cleanup(func() { w.Close() })

	bp := pool.New(dm, 100)
	idx, err := index.NewManager(bp, dm)
	if err != nil {
		t.Fatalf("index.NewManager: %v", err)
	}
	tm := txn.NewManager(w, bp, dm)

	rm, err := NewManager(bp, dm, idx)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	rm.SetTxManager(tm)
	return rm, dir
}

func TestCRUDWithTransactions(t *testing.T) {
	rm, _ := newTestManagerWithTx(t)

	id, err := rm.CreateNode([]string{"Person"}, map[string]graph.Value{"name": graph.StringValue("Alice")})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	node, err := rm.GetNode(id)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if node.Properties["name"].AsString() != "Alice" {
		t.Errorf("expected Alice, got %s", node.Properties["name"].AsString())
	}

	err = rm.UpdateNode(id, map[string]graph.Value{"name": graph.StringValue("Bob")})
	if err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}

	node, _ = rm.GetNode(id)
	if node.Properties["name"].AsString() != "Bob" {
		t.Errorf("expected Bob, got %s", node.Properties["name"].AsString())
	}

	id2, _ := rm.CreateNode([]string{"X"}, map[string]graph.Value{})
	eid, err := rm.CreateEdge("KNOWS", id, id2, map[string]graph.Value{})
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}

	if err := rm.DeleteEdge(eid); err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}
	if err := rm.DeleteNode(id); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
}

func TestCreateAndGetNode(t *testing.T) {
	rm := newTestManager(t)

	id, err := rm.CreateNode(
		[]string{"Person"},
		map[string]graph.Value{"name": graph.StringValue("Alice"), "age": graph.IntValue(30)},
	)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero node ID")
	}

	node, err := rm.GetNode(id)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if node.ID != id {
		t.Errorf("expected ID %d, got %d", id, node.ID)
	}
	if node.Labels[0] != "Person" {
		t.Errorf("expected label 'Person', got %q", node.Labels[0])
	}
	if node.Properties["name"].AsString() != "Alice" {
		t.Errorf("expected name 'Alice', got %q", node.Properties["name"].AsString())
	}
}

func TestGetNodeNotFound(t *testing.T) {
	rm := newTestManager(t)
	_, err := rm.GetNode(999)
	if err == nil {
		t.Fatal("expected error for non-existent node")
	}
}

func TestUpdateNode(t *testing.T) {
	rm := newTestManager(t)

	id, _ := rm.CreateNode([]string{"Person"}, map[string]graph.Value{"name": graph.StringValue("Bob")})

	err := rm.UpdateNode(id, map[string]graph.Value{"name": graph.StringValue("Robert"), "age": graph.IntValue(25)})
	if err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}

	node, _ := rm.GetNode(id)
	if node.Properties["name"].AsString() != "Robert" {
		t.Errorf("expected name 'Robert', got %q", node.Properties["name"].AsString())
	}
	if node.Properties["age"].AsInt() != 25 {
		t.Errorf("expected age 25, got %d", node.Properties["age"].AsInt())
	}
}

func TestDeleteNode(t *testing.T) {
	rm := newTestManager(t)
	id, _ := rm.CreateNode([]string{"Temp"}, map[string]graph.Value{})

	if err := rm.DeleteNode(id); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}

	_, err := rm.GetNode(id)
	if err == nil {
		t.Fatal("expected error getting deleted node")
	}
}

func TestDeleteNodeWithEdges(t *testing.T) {
	rm := newTestManager(t)
	n1, _ := rm.CreateNode([]string{"A"}, map[string]graph.Value{})
	n2, _ := rm.CreateNode([]string{"B"}, map[string]graph.Value{})
	rm.CreateEdge("LINK", n1, n2, map[string]graph.Value{})

	err := rm.DeleteNode(n1)
	if err == nil {
		t.Fatal("expected error deleting node with edges")
	}
}

func TestCreateAndGetEdge(t *testing.T) {
	rm := newTestManager(t)
	n1, _ := rm.CreateNode([]string{"Person"}, map[string]graph.Value{"name": graph.StringValue("Alice")})
	n2, _ := rm.CreateNode([]string{"Person"}, map[string]graph.Value{"name": graph.StringValue("Bob")})

	eid, err := rm.CreateEdge("KNOWS", n1, n2, map[string]graph.Value{"since": graph.IntValue(2020)})
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}

	edge, err := rm.GetEdge(eid)
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if edge.Type != "KNOWS" {
		t.Errorf("expected type 'KNOWS', got %q", edge.Type)
	}
	if edge.SourceID != n1 || edge.TargetID != n2 {
		t.Errorf("expected %d→%d, got %d→%d", n1, n2, edge.SourceID, edge.TargetID)
	}
	if edge.Properties["since"].AsInt() != 2020 {
		t.Errorf("expected since=2020, got %d", edge.Properties["since"].AsInt())
	}
}

func TestCreateEdgeMissingNode(t *testing.T) {
	rm := newTestManager(t)
	n1, _ := rm.CreateNode([]string{"A"}, map[string]graph.Value{})

	_, err := rm.CreateEdge("LINK", n1, 999, map[string]graph.Value{})
	if err == nil {
		t.Fatal("expected error for missing target node")
	}

	_, err = rm.CreateEdge("LINK", 999, n1, map[string]graph.Value{})
	if err == nil {
		t.Fatal("expected error for missing source node")
	}
}

func TestGetEdges(t *testing.T) {
	rm := newTestManager(t)
	n1, _ := rm.CreateNode([]string{"A"}, map[string]graph.Value{})
	n2, _ := rm.CreateNode([]string{"B"}, map[string]graph.Value{})
	n3, _ := rm.CreateNode([]string{"C"}, map[string]graph.Value{})

	rm.CreateEdge("R1", n1, n2, map[string]graph.Value{})
	rm.CreateEdge("R2", n1, n3, map[string]graph.Value{})
	rm.CreateEdge("R3", n2, n1, map[string]graph.Value{})

	out, err := rm.GetEdges(n1, graph.Outgoing)
	if err != nil {
		t.Fatalf("GetEdges outgoing: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("expected 2 outgoing edges, got %d", len(out))
	}

	in, err := rm.GetEdges(n1, graph.Incoming)
	if err != nil {
		t.Fatalf("GetEdges incoming: %v", err)
	}
	if len(in) != 1 {
		t.Errorf("expected 1 incoming edge, got %d", len(in))
	}

	both, err := rm.GetEdges(n1, graph.Both)
	if err != nil {
		t.Fatalf("GetEdges both: %v", err)
	}
	if len(both) != 3 {
		t.Errorf("expected 3 total edges, got %d", len(both))
	}
}

func TestDeleteEdge(t *testing.T) {
	rm := newTestManager(t)
	n1, _ := rm.CreateNode([]string{"A"}, map[string]graph.Value{})
	n2, _ := rm.CreateNode([]string{"B"}, map[string]graph.Value{})
	eid, _ := rm.CreateEdge("LINK", n1, n2, map[string]graph.Value{})

	if err := rm.DeleteEdge(eid); err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}

	_, err := rm.GetEdge(eid)
	if err == nil {
		t.Fatal("expected error getting deleted edge")
	}

	out, _ := rm.GetEdges(n1, graph.Outgoing)
	if len(out) != 0 {
		t.Errorf("expected 0 outgoing edges after delete, got %d", len(out))
	}

	if err := rm.DeleteNode(n1); err != nil {
		t.Fatalf("DeleteNode after edge removal: %v", err)
	}
}

func TestManyNodes(t *testing.T) {
	rm := newTestManager(t)

	ids := make([]graph.NodeID, 200)
	for i := range 200 {
		id, err := rm.CreateNode(
			[]string{"Node"},
			map[string]graph.Value{"index": graph.IntValue(int64(i))},
		)
		if err != nil {
			t.Fatalf("CreateNode %d: %v", i, err)
		}
		ids[i] = id
	}

	for i, id := range ids {
		node, err := rm.GetNode(id)
		if err != nil {
			t.Fatalf("GetNode %d: %v", id, err)
		}
		if node.Properties["index"].AsInt() != int64(i) {
			t.Errorf("node %d: expected index %d, got %d", id, i, node.Properties["index"].AsInt())
		}
	}
}

func TestScanNodes(t *testing.T) {
	rm := newTestManager(t)
	rm.CreateNode([]string{"Person"}, map[string]graph.Value{"name": graph.StringValue("Alice")})
	rm.CreateNode([]string{"Person", "Employee"}, map[string]graph.Value{"name": graph.StringValue("Bob")})
	rm.CreateNode([]string{"Company"}, map[string]graph.Value{"name": graph.StringValue("Acme")})

	all, err := rm.ScanNodes(nil)
	if err != nil {
		t.Fatalf("ScanNodes(nil): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(all))
	}

	persons, err := rm.ScanNodes([]string{"Person"})
	if err != nil {
		t.Fatalf("ScanNodes(Person): %v", err)
	}
	if len(persons) != 2 {
		t.Errorf("expected 2 Person nodes, got %d", len(persons))
	}

	employees, err := rm.ScanNodes([]string{"Employee"})
	if err != nil {
		t.Fatalf("ScanNodes(Employee): %v", err)
	}
	if len(employees) != 1 {
		t.Errorf("expected 1 Employee node, got %d", len(employees))
	}
}

func TestScanEdges(t *testing.T) {
	rm := newTestManager(t)
	n1, _ := rm.CreateNode([]string{"A"}, map[string]graph.Value{})
	n2, _ := rm.CreateNode([]string{"B"}, map[string]graph.Value{})
	n3, _ := rm.CreateNode([]string{"C"}, map[string]graph.Value{})

	rm.CreateEdge("KNOWS", n1, n2, map[string]graph.Value{})
	rm.CreateEdge("WORKS_AT", n1, n3, map[string]graph.Value{})
	rm.CreateEdge("KNOWS", n2, n3, map[string]graph.Value{})

	all, err := rm.ScanEdges("")
	if err != nil {
		t.Fatalf("ScanEdges(''): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 edges, got %d", len(all))
	}

	knows, err := rm.ScanEdges("KNOWS")
	if err != nil {
		t.Fatalf("ScanEdges('KNOWS'): %v", err)
	}
	if len(knows) != 2 {
		t.Errorf("expected 2 KNOWS edges, got %d", len(knows))
	}
}

func TestExpand(t *testing.T) {
	rm := newTestManager(t)
	n1, _ := rm.CreateNode([]string{"A"}, map[string]graph.Value{})
	n2, _ := rm.CreateNode([]string{"B"}, map[string]graph.Value{})
	n3, _ := rm.CreateNode([]string{"C"}, map[string]graph.Value{})

	rm.CreateEdge("KNOWS", n1, n2, map[string]graph.Value{})
	rm.CreateEdge("WORKS_AT", n1, n3, map[string]graph.Value{})
	rm.CreateEdge("KNOWS", n2, n1, map[string]graph.Value{})

	outAll, _ := rm.Expand(n1, graph.Outgoing, "")
	if len(outAll) != 2 {
		t.Errorf("expected 2 outgoing, got %d", len(outAll))
	}

	outKnows, _ := rm.Expand(n1, graph.Outgoing, "KNOWS")
	if len(outKnows) != 1 {
		t.Errorf("expected 1 outgoing KNOWS, got %d", len(outKnows))
	}

	inAll, _ := rm.Expand(n1, graph.Incoming, "")
	if len(inAll) != 1 {
		t.Errorf("expected 1 incoming, got %d", len(inAll))
	}

	bothAll, _ := rm.Expand(n1, graph.Both, "")
	if len(bothAll) != 3 {
		t.Errorf("expected 3 both, got %d", len(bothAll))
	}
}

func TestManyEdges(t *testing.T) {
	rm := newTestManager(t)

	nodes := make([]graph.NodeID, 50)
	for i := range 50 {
		id, _ := rm.CreateNode([]string{"N"}, map[string]graph.Value{"i": graph.IntValue(int64(i))})
		nodes[i] = id
	}

	for i := 0; i < 49; i++ {
		_, err := rm.CreateEdge("NEXT", nodes[i], nodes[i+1], map[string]graph.Value{
			"order": graph.IntValue(int64(i)),
		})
		if err != nil {
			t.Fatalf("CreateEdge %d→%d: %v", nodes[i], nodes[i+1], err)
		}
	}

	mid := nodes[25]
	out, _ := rm.GetEdges(mid, graph.Outgoing)
	in, _ := rm.GetEdges(mid, graph.Incoming)
	if len(out) != 1 || len(in) != 1 {
		t.Errorf("node %d: expected 1 out + 1 in, got %d out + %d in", mid, len(out), len(in))
	}

	firstOut, _ := rm.GetEdges(nodes[0], graph.Outgoing)
	firstIn, _ := rm.GetEdges(nodes[0], graph.Incoming)
	lastOut, _ := rm.GetEdges(nodes[49], graph.Outgoing)
	lastIn, _ := rm.GetEdges(nodes[49], graph.Incoming)

	if len(firstOut) != 1 || len(firstIn) != 0 {
		t.Errorf("first node: expected 1/0, got %d/%d", len(firstOut), len(firstIn))
	}
	if len(lastOut) != 0 || len(lastIn) != 1 {
		t.Errorf("last node: expected 0/1, got %d/%d", len(lastOut), len(lastIn))
	}
}
