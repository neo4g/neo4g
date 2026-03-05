package storage

import (
	"path/filepath"
	"testing"
)

func newTestBTree(t *testing.T) *BTree {
	t.Helper()
	dir := t.TempDir()
	dm, err := OpenDiskManager(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dm.Close() })

	bp := NewBufferPool(dm, 500)
	bt, err := NewBTree(bp, dm)
	if err != nil {
		t.Fatal(err)
	}
	return bt
}

func TestBTreeInsertAndSearch(t *testing.T) {
	bt := newTestBTree(t)

	rid := RID{PageID: 10, Slot: 5}
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

	bt.Insert(1, RID{PageID: 10, Slot: 0})
	bt.Insert(1, RID{PageID: 20, Slot: 3}) // update

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

	bt.Insert(10, RID{PageID: 1, Slot: 0})
	bt.Insert(20, RID{PageID: 2, Slot: 0})

	if !bt.Delete(10) {
		t.Fatal("expected Delete to return true")
	}
	if _, found := bt.Search(10); found {
		t.Fatal("expected key 10 to be deleted")
	}

	// Key 20 should still exist.
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

	// Insert enough keys to force multiple leaf splits and at least one
	// internal node split. Max leaf keys ≈ 585 for 8KB pages.
	n := 2000
	for i := range n {
		key := uint64(i * 3) // non-sequential to test ordering
		rid := RID{PageID: PageID(i + 1), Slot: uint16(i % 100)}
		if err := bt.Insert(key, rid); err != nil {
			t.Fatalf("Insert key %d: %v", key, err)
		}
	}

	// Verify all keys are findable.
	for i := range n {
		key := uint64(i * 3)
		rid, found := bt.Search(key)
		if !found {
			t.Fatalf("key %d not found", key)
		}
		if rid.PageID != PageID(i+1) {
			t.Errorf("key %d: expected page %d, got %d", key, i+1, rid.PageID)
		}
	}

	// Verify missing keys are not found.
	_, found := bt.Search(1) // not a multiple of 3
	if found {
		t.Error("expected key 1 (not inserted) to be missing")
	}
}

func TestBTreeRangeScan(t *testing.T) {
	bt := newTestBTree(t)

	for i := uint64(0); i < 100; i++ {
		bt.Insert(i, RID{PageID: PageID(i + 1), Slot: 0})
	}

	// Scan [20, 30]
	results := bt.RangeScan(20, 30)
	if len(results) != 11 { // 20,21,...,30
		t.Errorf("expected 11 results, got %d", len(results))
	}

	// First result should be for key 20 → page 21
	if results[0].PageID != 21 {
		t.Errorf("first result: expected page 21, got %d", results[0].PageID)
	}
}

func TestBTreeRangeScanEmpty(t *testing.T) {
	bt := newTestBTree(t)

	for i := uint64(0); i < 10; i++ {
		bt.Insert(i, RID{PageID: PageID(i + 1), Slot: 0})
	}

	results := bt.RangeScan(100, 200)
	if len(results) != 0 {
		t.Errorf("expected 0 results for out-of-range scan, got %d", len(results))
	}
}

func TestBTreeOrderedInsert(t *testing.T) {
	bt := newTestBTree(t)

	// Sequential insert (worst case for some tree implementations).
	for i := uint64(1); i <= 1000; i++ {
		if err := bt.Insert(i, RID{PageID: PageID(i), Slot: 0}); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	for i := uint64(1); i <= 1000; i++ {
		rid, found := bt.Search(i)
		if !found {
			t.Fatalf("key %d not found", i)
		}
		if rid.PageID != PageID(i) {
			t.Errorf("key %d: expected page %d, got %d", i, i, rid.PageID)
		}
	}
}

func TestBTreeReverseInsert(t *testing.T) {
	bt := newTestBTree(t)

	for i := uint64(1000); i >= 1; i-- {
		if err := bt.Insert(i, RID{PageID: PageID(i), Slot: 0}); err != nil {
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
	key := AdjacencyKey(NodeID(5), EdgeID(10))
	expected := (uint64(5) << 32) | 10
	if key != expected {
		t.Errorf("expected %d, got %d", expected, key)
	}

	start := AdjacencyRangeStart(NodeID(5))
	end := AdjacencyRangeEnd(NodeID(5))
	if key < start || key > end {
		t.Errorf("key %d not in range [%d, %d]", key, start, end)
	}

	// Different node should be outside range.
	otherKey := AdjacencyKey(NodeID(6), EdgeID(1))
	if otherKey >= start && otherKey <= end {
		t.Error("key from different node should be outside range")
	}
}
