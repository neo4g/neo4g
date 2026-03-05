package storage

import (
	"path/filepath"
	"testing"
)

func newTestBufferPool(t *testing.T, poolSize int) (*BufferPool, *DiskManager) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	dm, err := OpenDiskManager(path)
	if err != nil {
		t.Fatalf("OpenDiskManager: %v", err)
	}
	t.Cleanup(func() { dm.Close() })
	bp := NewBufferPool(dm, poolSize)
	return bp, dm
}

func TestBufferPoolNewPage(t *testing.T) {
	bp, _ := newTestBufferPool(t, 10)

	id, p, err := bp.NewPage()
	if err != nil {
		t.Fatalf("NewPage: %v", err)
	}
	if id == HeaderPageID {
		t.Error("NewPage should not return header page ID")
	}
	if p == nil {
		t.Fatal("NewPage returned nil page")
	}

	if err := bp.UnpinPage(id, false); err != nil {
		t.Fatalf("UnpinPage: %v", err)
	}
}

func TestBufferPoolFetchPage(t *testing.T) {
	bp, _ := newTestBufferPool(t, 10)

	// Create a page with data.
	id, p, _ := bp.NewPage()
	copy(p[:], "test data")
	bp.UnpinPage(id, true)

	// Flush to disk.
	bp.FlushPage(id)

	// Fetch it back.
	p2, err := bp.FetchPage(id)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	defer bp.UnpinPage(id, false)

	if string(p2[:9]) != "test data" {
		t.Errorf("expected 'test data', got %q", string(p2[:9]))
	}
}

func TestBufferPoolDirtyFlush(t *testing.T) {
	bp, dm := newTestBufferPool(t, 10)

	id, p, _ := bp.NewPage()
	copy(p[:], "dirty page")
	bp.UnpinPage(id, true) // mark dirty

	bp.FlushAll()

	// Verify on disk.
	diskPage, err := dm.ReadPage(id)
	if err != nil {
		t.Fatalf("ReadPage from disk: %v", err)
	}
	if string(diskPage[:10]) != "dirty page" {
		t.Errorf("expected 'dirty page' on disk, got %q", string(diskPage[:10]))
	}
}

func TestBufferPoolEviction(t *testing.T) {
	// Pool of 3 frames — allocate 4 pages to force eviction.
	bp, dm := newTestBufferPool(t, 3)

	ids := make([]PageID, 4)
	for i := range 4 {
		id, p, err := bp.NewPage()
		if err != nil {
			t.Fatalf("NewPage %d: %v", i, err)
		}
		copy(p[:], []byte{byte(i + 1)})
		bp.UnpinPage(id, true)
		ids[i] = id
	}

	// Flush remaining dirty pages so we can verify via disk.
	bp.FlushAll()

	// All 4 pages should have correct data on disk (evicted ones were flushed
	// during eviction, remaining ones were flushed by FlushAll).
	for i, id := range ids {
		diskPage, err := dm.ReadPage(id)
		if err != nil {
			t.Fatalf("ReadPage %d: %v", id, err)
		}
		if diskPage[0] != byte(i+1) {
			t.Errorf("page %d: expected byte %d, got %d", id, i+1, diskPage[0])
		}
	}
}

func TestBufferPoolPinPreventsEviction(t *testing.T) {
	// Pool of 2 frames. Pin both, then try to allocate a third.
	bp, _ := newTestBufferPool(t, 2)

	_, _, err := bp.NewPage() // pinned
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = bp.NewPage() // pinned
	if err != nil {
		t.Fatal(err)
	}

	// Both frames are pinned — this should fail.
	_, _, err = bp.NewPage()
	if err == nil {
		t.Fatal("expected error when all frames are pinned")
	}
}

func TestBufferPoolDoubleUnpin(t *testing.T) {
	bp, _ := newTestBufferPool(t, 10)

	id, _, _ := bp.NewPage()
	bp.UnpinPage(id, false)

	err := bp.UnpinPage(id, false)
	if err == nil {
		t.Fatal("expected error on double unpin")
	}
}

func TestBufferPoolUnpinUnknown(t *testing.T) {
	bp, _ := newTestBufferPool(t, 10)

	err := bp.UnpinPage(999, false)
	if err == nil {
		t.Fatal("expected error unpinning unknown page")
	}
}

func TestBufferPoolCacheHit(t *testing.T) {
	bp, _ := newTestBufferPool(t, 10)

	id, p1, _ := bp.NewPage()
	copy(p1[:], "cached")
	bp.UnpinPage(id, false)

	// Fetch again — should return same data from cache (no disk read needed).
	p2, err := bp.FetchPage(id)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	defer bp.UnpinPage(id, false)

	if string(p2[:6]) != "cached" {
		t.Errorf("cache miss: expected 'cached', got %q", string(p2[:6]))
	}
}

func TestBufferPoolFlushPage(t *testing.T) {
	bp, dm := newTestBufferPool(t, 10)

	id, p, _ := bp.NewPage()
	copy(p[:], "flush me")
	bp.UnpinPage(id, true)

	if err := bp.FlushPage(id); err != nil {
		t.Fatalf("FlushPage: %v", err)
	}

	diskPage, _ := dm.ReadPage(id)
	if string(diskPage[:8]) != "flush me" {
		t.Errorf("expected 'flush me' on disk, got %q", string(diskPage[:8]))
	}
}
