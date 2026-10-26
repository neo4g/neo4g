package index

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/neo4g/neo4g/graph"
	"github.com/neo4g/neo4g/storage"
	"github.com/neo4g/neo4g/storage/disk"
	"github.com/neo4g/neo4g/storage/pool"
	"github.com/neo4g/neo4g/storage/txn"
	"github.com/neo4g/neo4g/storage/wal"
)

func newTestBTree(t *testing.T) *BTree {
	t.Helper()
	dir := t.TempDir()
	dm, err := disk.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dm.Close() })

	bp := pool.New(dm, 500)
	bt, err := NewBTree(bp, dm)
	if err != nil {
		t.Fatal(err)
	}
	return bt
}

func TestBTreeInsertAndSearch(t *testing.T) {
	bt := newTestBTree(t)

	rid := storage.RID{PageID: 10, Slot: 5}
	if err := bt.Insert(42, rid); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, found := bt.Search(42)
	if !found {
		t.Fatal("expected to find key 42")
	}
	if got.PageID != 10 || got.Slot != 5 {
		t.Errorf("expected RID{10,5}, got RID{%d,%d}", got.PageID, got.Slot)
	}
}

func TestBTreeSearchMissing(t *testing.T) {
	bt := newTestBTree(t)

	_, found := bt.Search(999)
	if found {
		t.Fatal("expected not to find missing key")
	}
}

func TestBTreeUpdateExisting(t *testing.T) {
	bt := newTestBTree(t)

	bt.Insert(1, storage.RID{PageID: 10, Slot: 0})
	bt.Insert(1, storage.RID{PageID: 20, Slot: 3})

	got, found := bt.Search(1)
	if !found {
		t.Fatal("expected to find key 1")
	}
	if got.PageID != 20 || got.Slot != 3 {
		t.Errorf("expected updated RID{20,3}, got RID{%d,%d}", got.PageID, got.Slot)
	}
}

func TestBTreeDelete(t *testing.T) {
	bt := newTestBTree(t)

	bt.Insert(10, storage.RID{PageID: 1, Slot: 0})
	bt.Insert(20, storage.RID{PageID: 2, Slot: 0})

	if !bt.Delete(10) {
		t.Fatal("expected Delete to return true")
	}
	if _, found := bt.Search(10); found {
		t.Fatal("expected key 10 to be deleted")
	}

	if _, found := bt.Search(20); !found {
		t.Fatal("expected key 20 to still exist")
	}
}

func TestBTreeDeleteMissing(t *testing.T) {
	bt := newTestBTree(t)
	if bt.Delete(999) {
		t.Fatal("expected Delete to return false for missing key")
	}
}

func TestBTreeManyInserts(t *testing.T) {
	bt := newTestBTree(t)

	n := 2000
	for i := range n {
		key := uint64(i * 3)
		rid := storage.RID{PageID: storage.PageID(i + 1), Slot: uint16(i % 100)}
		if err := bt.Insert(key, rid); err != nil {
			t.Fatalf("Insert key %d: %v", key, err)
		}
	}

	for i := range n {
		key := uint64(i * 3)
		rid, found := bt.Search(key)
		if !found {
			t.Fatalf("key %d not found", key)
		}
		if rid.PageID != storage.PageID(i+1) {
			t.Errorf("key %d: expected page %d, got %d", key, i+1, rid.PageID)
		}
	}

	_, found := bt.Search(1)
	if found {
		t.Error("expected key 1 (not inserted) to be missing")
	}
}

func TestBTreeRangeScan(t *testing.T) {
	bt := newTestBTree(t)

	for i := uint64(0); i < 100; i++ {
		bt.Insert(i, storage.RID{PageID: storage.PageID(i + 1), Slot: 0})
	}

	results := bt.RangeScan(20, 30)
	if len(results) != 11 {
		t.Errorf("expected 11 results, got %d", len(results))
	}

	if results[0].PageID != 21 {
		t.Errorf("first result: expected page 21, got %d", results[0].PageID)
	}
}

func TestBTreeRangeScanEmpty(t *testing.T) {
	bt := newTestBTree(t)

	for i := uint64(0); i < 10; i++ {
		bt.Insert(i, storage.RID{PageID: storage.PageID(i + 1), Slot: 0})
	}

	results := bt.RangeScan(100, 200)
	if len(results) != 0 {
		t.Errorf("expected 0 results for out-of-range scan, got %d", len(results))
	}
}

func TestBTreeOrderedInsert(t *testing.T) {
	bt := newTestBTree(t)

	for i := uint64(1); i <= 1000; i++ {
		if err := bt.Insert(i, storage.RID{PageID: storage.PageID(i), Slot: 0}); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	for i := uint64(1); i <= 1000; i++ {
		rid, found := bt.Search(i)
		if !found {
			t.Fatalf("key %d not found", i)
		}
		if rid.PageID != storage.PageID(i) {
			t.Errorf("key %d: expected page %d, got %d", i, i, rid.PageID)
		}
	}
}

func TestBTreeReverseInsert(t *testing.T) {
	bt := newTestBTree(t)

	for i := uint64(1000); i >= 1; i-- {
		if err := bt.Insert(i, storage.RID{PageID: storage.PageID(i), Slot: 0}); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	for i := uint64(1); i <= 1000; i++ {
		_, found := bt.Search(i)
		if !found {
			t.Fatalf("key %d not found", i)
		}
	}
}

func TestAdjacencyKeyEncoding(t *testing.T) {
	key := AdjacencyKey(graph.NodeID(5), graph.EdgeID(10))
	expected := (uint64(5) << 32) | 10
	if key != expected {
		t.Errorf("expected %d, got %d", expected, key)
	}

	start := AdjacencyRangeStart(graph.NodeID(5))
	end := AdjacencyRangeEnd(graph.NodeID(5))
	if key < start || key > end {
		t.Errorf("key %d not in range [%d, %d]", key, start, end)
	}

	otherKey := AdjacencyKey(graph.NodeID(6), graph.EdgeID(1))
	if otherKey >= start && otherKey <= end {
		t.Error("key from different node should be outside range")
	}
}

func TestBTreeRootSplitPersisted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	dm, err := disk.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	bp := pool.New(dm, 16)
	bt, err := NewBTree(bp, dm)
	if err != nil {
		t.Fatal(err)
	}
	if err := dm.SetNodeIndexRoot(bt.RootPageID()); err != nil {
		t.Fatal(err)
	}
	bt.SetRootUpdater(dm.SetNodeIndexRoot)

	n := btreeMaxLeafKeys + 1
	for i := range n {
		rid := storage.RID{PageID: storage.PageID(i + 1), Slot: uint16(i)}
		if err := bt.Insert(uint64(i), rid); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}
	rightKey := uint64(n - 1)
	if _, ok := bt.Search(rightKey); !ok {
		t.Fatal("right-sibling key missing before close")
	}
	if err := bp.FlushAll(); err != nil {
		t.Fatal(err)
	}
	if err := dm.Close(); err != nil {
		t.Fatal(err)
	}

	dm2, err := disk.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dm2.Close()
	bp2 := pool.New(dm2, 16)
	loaded := LoadBTree(bp2, dm2, dm2.NodeIndexRoot())
	rid, ok := loaded.Search(rightKey)
	if !ok {
		t.Fatalf("key %d missing after reload from header root %d", rightKey, dm2.NodeIndexRoot())
	}
	if rid.PageID != storage.PageID(n) {
		t.Fatalf("key %d page = %d, want %d", rightKey, rid.PageID, n)
	}
	if _, ok := loaded.Search(0); !ok {
		t.Fatal("left-sibling key missing after reload")
	}
}

func TestBTreeRootRestoredOnAbort(t *testing.T) {
	dir := t.TempDir()
	dm, err := disk.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dm.Close()
	w, err := wal.Open(filepath.Join(dir, "test.wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	bp := pool.New(dm, 32)
	tm := txn.NewManager(w, bp, dm)
	bt, err := NewBTree(bp, dm)
	if err != nil {
		t.Fatal(err)
	}
	old := bt.RootPageID()
	if err := dm.SetNodeIndexRoot(old); err != nil {
		t.Fatal(err)
	}
	bt.SetRootUpdater(dm.SetNodeIndexRoot)

	tx := tm.Begin()
	bt.SetPageLogger(func(pageID storage.PageID, before, after *storage.Page) error {
		return tm.WritePage(tx, pageID, before, after)
	})
	n := btreeMaxLeafKeys + 1
	for i := range n {
		if err := bt.Insert(uint64(i), storage.RID{PageID: 1, Slot: 0}); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}
	if bt.RootPageID() == old {
		t.Fatal("expected the root to split")
	}
	if err := tm.Abort(tx); err != nil {
		t.Fatal(err)
	}
	bt.RevertRoot()
	if dm.NodeIndexRoot() != old {
		t.Fatalf("header root = %d, want %d", dm.NodeIndexRoot(), old)
	}
	if bt.RootPageID() != old {
		t.Fatalf("btree root = %d, want %d", bt.RootPageID(), old)
	}
	if _, ok := bt.Search(uint64(n - 1)); ok {
		t.Fatal("aborted key is still visible")
	}
}

func TestBTreeLogFailureRestoresLeaf(t *testing.T) {
	bt := newTestBTree(t)
	bt.SetPageLogger(func(storage.PageID, *storage.Page, *storage.Page) error {
		return errors.New("boom")
	})
	err := bt.Insert(1, storage.RID{PageID: 3, Slot: 1})
	if err == nil {
		t.Fatal("expected log failure")
	}
	if _, ok := bt.Search(1); ok {
		t.Fatal("key visible after a failed log")
	}
}
