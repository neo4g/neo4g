package pool

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/neo4g/neo4g/storage"
	"github.com/neo4g/neo4g/storage/disk"
)

func newTestPool(t *testing.T, poolSize int) (*Pool, *disk.Manager) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	dm, err := disk.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { dm.Close() })
	bp := New(dm, poolSize)
	return bp, dm
}

func TestPoolNewPage(t *testing.T) {
	bp, _ := newTestPool(t, 10)

	id, p, err := bp.NewPage()
	if err != nil {
		t.Fatalf("NewPage: %v", err)
	}
	if id == storage.HeaderPageID {
		t.Error("NewPage should not return header page ID")
	}
	if p == nil {
		t.Fatal("NewPage returned nil page")
	}

	if err := bp.UnpinPage(id, false); err != nil {
		t.Fatalf("UnpinPage: %v", err)
	}
}

func TestPoolFetchPage(t *testing.T) {
	bp, _ := newTestPool(t, 10)

	id, p, _ := bp.NewPage()
	copy(p[:], "test data")
	bp.UnpinPage(id, true)

	bp.FlushPage(id)

	p2, err := bp.FetchPage(id)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	defer bp.UnpinPage(id, false)

	if string(p2[:9]) != "test data" {
		t.Errorf("expected 'test data', got %q", string(p2[:9]))
	}
}

func TestPoolDirtyFlush(t *testing.T) {
	bp, dm := newTestPool(t, 10)

	id, p, _ := bp.NewPage()
	copy(p[:], "dirty page")
	bp.UnpinPage(id, true)

	bp.FlushAll()

	diskPage, err := dm.ReadPage(id)
	if err != nil {
		t.Fatalf("ReadPage from disk: %v", err)
	}
	if string(diskPage[:10]) != "dirty page" {
		t.Errorf("expected 'dirty page' on disk, got %q", string(diskPage[:10]))
	}
}

func TestPoolEviction(t *testing.T) {
	bp, dm := newTestPool(t, 3)

	ids := make([]storage.PageID, 4)
	for i := range 4 {
		id, p, err := bp.NewPage()
		if err != nil {
			t.Fatalf("NewPage %d: %v", i, err)
		}
		copy(p[:], []byte{byte(i + 1)})
		bp.UnpinPage(id, true)
		ids[i] = id
	}

	bp.FlushAll()

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

func TestPoolPinPreventsEviction(t *testing.T) {
	bp, _ := newTestPool(t, 2)

	_, _, err := bp.NewPage() // pinned
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = bp.NewPage() // pinned
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = bp.NewPage()
	if err == nil {
		t.Fatal("expected error when all frames are pinned")
	}
}

func TestPoolDoubleUnpin(t *testing.T) {
	bp, _ := newTestPool(t, 10)

	id, _, _ := bp.NewPage()
	bp.UnpinPage(id, false)

	err := bp.UnpinPage(id, false)
	if err == nil {
		t.Fatal("expected error on double unpin")
	}
}

func TestPoolUnpinUnknown(t *testing.T) {
	bp, _ := newTestPool(t, 10)

	err := bp.UnpinPage(999, false)
	if err == nil {
		t.Fatal("expected error unpinning unknown page")
	}
}

func TestPoolCacheHit(t *testing.T) {
	bp, _ := newTestPool(t, 10)

	id, p1, _ := bp.NewPage()
	copy(p1[:], "cached")
	bp.UnpinPage(id, false)

	p2, err := bp.FetchPage(id)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	defer bp.UnpinPage(id, false)

	if string(p2[:6]) != "cached" {
		t.Errorf("cache miss: expected 'cached', got %q", string(p2[:6]))
	}
}

func TestPoolFlushPage(t *testing.T) {
	bp, dm := newTestPool(t, 10)

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

func TestConcurrentFetchUnpin(t *testing.T) {
	bp, _ := newTestPool(t, 64)

	// Pre-populate pages
	const numPages = 20
	ids := make([]storage.PageID, numPages)
	for i := range numPages {
		id, p, err := bp.NewPage()
		if err != nil {
			t.Fatalf("NewPage %d: %v", i, err)
		}
		copy(p[:], fmt.Sprintf("page-%02d", i))
		bp.UnpinPage(id, true)
		ids[i] = id
	}
	bp.FlushAll()

	// Hammer concurrent reads
	const goroutines = 50
	const iters = 200
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*iters)

	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range iters {
				idx := (g*iters + i) % numPages
				p, err := bp.FetchPage(ids[idx])
				if err != nil {
					errs <- fmt.Errorf("goroutine %d iter %d FetchPage(%d): %w", g, i, ids[idx], err)
					continue
				}
				expected := fmt.Sprintf("page-%02d", idx)
				if string(p[:len(expected)]) != expected {
					errs <- fmt.Errorf("goroutine %d: expected %q, got %q", g, expected, string(p[:len(expected)]))
				}
				if err := bp.UnpinPage(ids[idx], false); err != nil {
					errs <- fmt.Errorf("goroutine %d UnpinPage: %w", g, err)
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
