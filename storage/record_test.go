package storage

import (
	"fmt"
	"path/filepath"
	"testing"
)

func newTestRecordManager(t *testing.T) *RecordManager {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	dm, err := OpenDiskManager(path)
	if err != nil {
		t.Fatalf("OpenDiskManager: %v", err)
	}
	t.Cleanup(func() { dm.Close() })

	bp := NewBufferPool(dm, 100)
	rm, err := NewRecordManager(bp, dm)
	if err != nil {
		t.Fatalf("NewRecordManager: %v", err)
	}
	return rm
}

func TestCreateAndGetNode(t *testing.T) {
	rm := newTestRecordManager(t)

	id, err := rm.CreateNode(
		[]string{"Person"},
		map[string]Value{"name": StringValue("Alice"), "age": IntValue(30)},
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
	rm := newTestRecordManager(t)
	_, err := rm.GetNode(999)
	if err == nil {
		t.Fatal("expected error for non-existent node")
	}
}

func TestUpdateNode(t *testing.T) {
	rm := newTestRecordManager(t)

	id, _ := rm.CreateNode([]string{"Person"}, map[string]Value{"name": StringValue("Bob")})

	err := rm.UpdateNode(id, map[string]Value{"name": StringValue("Robert"), "age": IntValue(25)})
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
	rm := newTestRecordManager(t)
	id, _ := rm.CreateNode([]string{"Temp"}, map[string]Value{})

	if err := rm.DeleteNode(id); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}

	_, err := rm.GetNode(id)
	if err == nil {
		t.Fatal("expected error getting deleted node")
	}
}

func TestDeleteNodeWithEdges(t *testing.T) {
	rm := newTestRecordManager(t)
	n1, _ := rm.CreateNode([]string{"A"}, map[string]Value{})
	n2, _ := rm.CreateNode([]string{"B"}, map[string]Value{})
	rm.CreateEdge("LINK", n1, n2, map[string]Value{})

	err := rm.DeleteNode(n1)
	if err == nil {
		t.Fatal("expected error deleting node with edges")
	}
}

func TestCreateAndGetEdge(t *testing.T) {
	rm := newTestRecordManager(t)
	n1, _ := rm.CreateNode([]string{"Person"}, map[string]Value{"name": StringValue("Alice")})
	n2, _ := rm.CreateNode([]string{"Person"}, map[string]Value{"name": StringValue("Bob")})

	eid, err := rm.CreateEdge("KNOWS", n1, n2, map[string]Value{"since": IntValue(2020)})
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
	rm := newTestRecordManager(t)
	n1, _ := rm.CreateNode([]string{"A"}, map[string]Value{})

	_, err := rm.CreateEdge("LINK", n1, 999, map[string]Value{})
	if err == nil {
		t.Fatal("expected error for missing target node")
	}

	_, err = rm.CreateEdge("LINK", 999, n1, map[string]Value{})
	if err == nil {
		t.Fatal("expected error for missing source node")
	}
}

func TestGetEdges(t *testing.T) {
	rm := newTestRecordManager(t)
	n1, _ := rm.CreateNode([]string{"A"}, map[string]Value{})
	n2, _ := rm.CreateNode([]string{"B"}, map[string]Value{})
	n3, _ := rm.CreateNode([]string{"C"}, map[string]Value{})

	rm.CreateEdge("R1", n1, n2, map[string]Value{})
	rm.CreateEdge("R2", n1, n3, map[string]Value{})
	rm.CreateEdge("R3", n2, n1, map[string]Value{})

	// Outgoing from n1.
	out, err := rm.GetEdges(n1, Outgoing)
	if err != nil {
		t.Fatalf("GetEdges outgoing: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("expected 2 outgoing edges, got %d", len(out))
	}

	// Incoming to n1.
	in, err := rm.GetEdges(n1, Incoming)
	if err != nil {
		t.Fatalf("GetEdges incoming: %v", err)
	}
	if len(in) != 1 {
		t.Errorf("expected 1 incoming edge, got %d", len(in))
	}

	// Both directions for n1.
	both, err := rm.GetEdges(n1, Both)
	if err != nil {
		t.Fatalf("GetEdges both: %v", err)
	}
	if len(both) != 3 {
		t.Errorf("expected 3 total edges, got %d", len(both))
	}
}

func TestDeleteEdge(t *testing.T) {
	rm := newTestRecordManager(t)
	n1, _ := rm.CreateNode([]string{"A"}, map[string]Value{})
	n2, _ := rm.CreateNode([]string{"B"}, map[string]Value{})
	eid, _ := rm.CreateEdge("LINK", n1, n2, map[string]Value{})

	if err := rm.DeleteEdge(eid); err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}

	_, err := rm.GetEdge(eid)
	if err == nil {
		t.Fatal("expected error getting deleted edge")
	}

	// Adjacency should be cleaned up.
	out, _ := rm.GetEdges(n1, Outgoing)
	if len(out) != 0 {
		t.Errorf("expected 0 outgoing edges after delete, got %d", len(out))
	}

	// Node should be deletable now.
	if err := rm.DeleteNode(n1); err != nil {
		t.Fatalf("DeleteNode after edge removal: %v", err)
	}
}

func TestManyNodes(t *testing.T) {
	rm := newTestRecordManager(t)

	// Insert enough nodes to force page chain growth.
	ids := make([]NodeID, 200)
	for i := range 200 {
		id, err := rm.CreateNode(
			[]string{"Node"},
			map[string]Value{"index": IntValue(int64(i))},
		)
		if err != nil {
			t.Fatalf("CreateNode %d: %v", i, err)
		}
		ids[i] = id
	}

	// Verify all are readable.
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

func TestManyEdges(t *testing.T) {
	rm := newTestRecordManager(t)

	// Create a chain of nodes with edges.
	nodes := make([]NodeID, 50)
	for i := range 50 {
		id, _ := rm.CreateNode([]string{"N"}, map[string]Value{"i": IntValue(int64(i))})
		nodes[i] = id
	}

	for i := 0; i < 49; i++ {
		_, err := rm.CreateEdge("NEXT", nodes[i], nodes[i+1], map[string]Value{
			"order": IntValue(int64(i)),
		})
		if err != nil {
			t.Fatalf("CreateEdge %d→%d: %v", nodes[i], nodes[i+1], err)
		}
	}

	// Middle node should have 1 in + 1 out.
	mid := nodes[25]
	out, _ := rm.GetEdges(mid, Outgoing)
	in, _ := rm.GetEdges(mid, Incoming)
	if len(out) != 1 || len(in) != 1 {
		t.Errorf("node %d: expected 1 out + 1 in, got %d out + %d in", mid, len(out), len(in))
	}

	// First node: 1 out, 0 in. Last node: 0 out, 1 in.
	firstOut, _ := rm.GetEdges(nodes[0], Outgoing)
	firstIn, _ := rm.GetEdges(nodes[0], Incoming)
	lastOut, _ := rm.GetEdges(nodes[49], Outgoing)
	lastIn, _ := rm.GetEdges(nodes[49], Incoming)

	if len(firstOut) != 1 || len(firstIn) != 0 {
		t.Errorf("first node: expected 1/0, got %d/%d", len(firstOut), len(firstIn))
	}
	if len(lastOut) != 0 || len(lastIn) != 1 {
		t.Errorf("last node: expected 0/1, got %d/%d", len(lastOut), len(lastIn))
	}

	_ = fmt.Sprint("test")
}
