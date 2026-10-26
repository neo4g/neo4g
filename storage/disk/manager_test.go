package disk

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neo4g/neo4g/storage"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	dm, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { dm.Close() })
	return dm
}

func TestOpenNewDatabase(t *testing.T) {
	dm := newTestManager(t)

	if dm.PageCount() != 1 {
		t.Errorf("expected 1 page (header), got %d", dm.PageCount())
	}
}

func TestOpenExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	dm, err := Open(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	dm.AllocatePage()
	dm.AllocatePage()
	dm.Close()

	dm2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer dm2.Close()

	if dm2.PageCount() != 3 {
		t.Errorf("expected 3 pages after reopen, got %d", dm2.PageCount())
	}
}

func TestOpenInvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.db")

	os.WriteFile(path, make([]byte, storage.PageSize), 0644)

	_, err := Open(path)
	if err == nil {
		t.Fatal("expected error opening file with invalid magic number")
	}
}

func TestReadWritePage(t *testing.T) {
	dm := newTestManager(t)

	id, p, err := dm.AllocatePage()
	if err != nil {
		t.Fatalf("AllocatePage: %v", err)
	}

	copy(p[:], "hello neo4g")
	if err := dm.WritePage(id, p); err != nil {
		t.Fatalf("WritePage: %v", err)
	}

	p2, err := dm.ReadPage(id)
	if err != nil {
		t.Fatalf("ReadPage: %v", err)
	}
	if string(p2[:11]) != "hello neo4g" {
		t.Errorf("expected 'hello neo4g', got %q", string(p2[:11]))
	}
}

func TestReadOutOfRange(t *testing.T) {
	dm := newTestManager(t)
	_, err := dm.ReadPage(999)
	if err == nil {
		t.Fatal("expected error reading out-of-range page")
	}
}

func TestAllocateAndFree(t *testing.T) {
	dm := newTestManager(t)

	id1, _, _ := dm.AllocatePage()
	id2, _, _ := dm.AllocatePage()
	id3, _, _ := dm.AllocatePage()

	if dm.PageCount() != 4 {
		t.Fatalf("expected 4 pages, got %d", dm.PageCount())
	}

	if err := dm.FreePage(id2); err != nil {
		t.Fatalf("FreePage: %v", err)
	}

	reused, _, err := dm.AllocatePage()
	if err != nil {
		t.Fatalf("AllocatePage after free: %v", err)
	}
	if reused != id2 {
		t.Errorf("expected reuse of page %d, got %d", id2, reused)
	}

	if dm.PageCount() != 4 {
		t.Errorf("expected 4 pages (no growth), got %d", dm.PageCount())
	}

	_ = id1
	_ = id3
}

func TestFreeHeaderPage(t *testing.T) {
	dm := newTestManager(t)
	err := dm.FreePage(storage.HeaderPageID)
	if err == nil {
		t.Fatal("expected error freeing header page")
	}
}

func TestFreeListChain(t *testing.T) {
	dm := newTestManager(t)

	id1, _, _ := dm.AllocatePage()
	id2, _, _ := dm.AllocatePage()
	id3, _, _ := dm.AllocatePage()

	dm.FreePage(id1)
	dm.FreePage(id2)
	dm.FreePage(id3)

	r1, _, _ := dm.AllocatePage()
	r2, _, _ := dm.AllocatePage()
	r3, _, _ := dm.AllocatePage()

	if r1 != id3 || r2 != id2 || r3 != id1 {
		t.Errorf("expected LIFO order [%d,%d,%d], got [%d,%d,%d]",
			id3, id2, id1, r1, r2, r3)
	}
}

func TestNodeIDCounter(t *testing.T) {
	dm := newTestManager(t)

	id1, err := dm.NextNodeID()
	if err != nil {
		t.Fatalf("NextNodeID: %v", err)
	}
	id2, _ := dm.NextNodeID()
	id3, _ := dm.NextNodeID()

	if id1 != 1 || id2 != 2 || id3 != 3 {
		t.Errorf("expected [1,2,3], got [%d,%d,%d]", id1, id2, id3)
	}
}

func TestEdgeIDCounter(t *testing.T) {
	dm := newTestManager(t)

	id1, _ := dm.NextEdgeID()
	id2, _ := dm.NextEdgeID()

	if id1 != 1 || id2 != 2 {
		t.Errorf("expected [1,2], got [%d,%d]", id1, id2)
	}
}

func TestIDCountersPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	dm, _ := Open(path)
	dm.NextNodeID() // 1
	dm.NextNodeID() // 2
	dm.NextEdgeID() // 1
	dm.Close()

	dm2, _ := Open(path)
	defer dm2.Close()

	nid, _ := dm2.NextNodeID()
	eid, _ := dm2.NextEdgeID()
	if nid != 3 {
		t.Errorf("expected next node ID 3, got %d", nid)
	}
	if eid != 2 {
		t.Errorf("expected next edge ID 2, got %d", eid)
	}
}
