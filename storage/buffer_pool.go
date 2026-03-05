package storage

// buffer_pool.go implements the Buffer Pool — an in-memory page cache that sits
// between the disk manager and all higher layers.
//
// Design:
//   - The pool holds a fixed number of page "frames" in memory. Each frame can
//     hold one disk page.
//   - Pages are fetched via FetchPage, which returns a pinned page. The caller
//     MUST call UnpinPage when done. A pinned page cannot be evicted.
//   - When the pool is full and a new page is needed, the clock-sweep algorithm
//     selects a victim frame (unpinned, least-recently referenced) for eviction.
//     If the victim is dirty, it is flushed to disk first.
//   - Dirty tracking: callers mark pages dirty via UnpinPage(id, true). Dirty
//     pages are written back to disk on eviction or explicit flush.
//
// Why a buffer pool instead of relying on OS page cache:
//   - Control over flush ordering (required for WAL: log before data).
//   - Pin counting prevents evicting pages mid-transaction.
//   - Predictable, bounded memory usage.
//
// Clock-sweep eviction:
//   Clock-sweep is a single-pointer approximation of LRU. It sweeps circularly
//   through frames. Each frame has a "reference bit" set on access. The sweep
//   clears reference bits; a frame with an already-clear bit and zero pin count
//   is chosen as the victim. This gives O(1) amortized eviction with good
//   cache behavior in practice.

import (
	"errors"
	"fmt"
	"sync"
)

// frame holds one cached page along with its metadata.
type frame struct {
	page     Page
	pageID   PageID
	pinCount int
	dirty    bool
	refBit   bool
	valid    bool // true if this frame holds a page (false = empty frame)
}

// BufferPool is an in-memory cache of disk pages with clock-sweep eviction.
//
// Thread safety: BufferPool is safe for concurrent use. All operations are
// protected by a mutex.
type BufferPool struct {
	mu    sync.Mutex
	disk  *DiskManager
	pages []frame          // fixed-size frame array
	index map[PageID]int   // pageID → frame index for O(1) lookup
	clock int              // clock hand position for eviction sweep
}

// NewBufferPool creates a buffer pool with the given number of page frames,
// backed by the provided DiskManager.
//
// poolSize determines the maximum number of pages held in memory simultaneously.
// A typical value is 1024 (8MB) to 32768 (256MB).
func NewBufferPool(disk *DiskManager, poolSize int) *BufferPool {
	return &BufferPool{
		disk:  disk,
		pages: make([]frame, poolSize),
		index: make(map[PageID]int, poolSize),
	}
}

// FetchPage returns the page with the given ID, loading it from disk if not
// already cached. The page is pinned (pin count incremented) — the caller MUST
// call UnpinPage when done to allow eventual eviction.
//
// Returns a pointer to the page data within the frame. The pointer is valid
// until the page is evicted (which cannot happen while pinned).
func (bp *BufferPool) FetchPage(id PageID) (*Page, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	// Check if already in pool.
	if idx, ok := bp.index[id]; ok {
		f := &bp.pages[idx]
		f.pinCount++
		f.refBit = true
		return &f.page, nil
	}

	// Not cached — find a frame and load from disk.
	idx, err := bp.findVictim()
	if err != nil {
		return nil, err
	}

	f := &bp.pages[idx]

	// Evict current occupant if any.
	if f.valid {
		if err := bp.evict(idx); err != nil {
			return nil, fmt.Errorf("evict frame %d: %w", idx, err)
		}
	}

	// Load from disk.
	p, err := bp.disk.ReadPage(id)
	if err != nil {
		return nil, fmt.Errorf("fetch page %d: %w", id, err)
	}

	f.page = *p
	f.pageID = id
	f.pinCount = 1
	f.dirty = false
	f.refBit = true
	f.valid = true
	bp.index[id] = idx

	return &f.page, nil
}

// UnpinPage decrements the pin count on a page. If dirty is true, the page is
// marked dirty so it will be flushed to disk before eviction.
//
// It is a programming error to unpin a page that is not pinned. This will return
// an error rather than panicking, to aid debugging.
func (bp *BufferPool) UnpinPage(id PageID, dirty bool) error {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	idx, ok := bp.index[id]
	if !ok {
		return fmt.Errorf("page %d not in buffer pool", id)
	}

	f := &bp.pages[idx]
	if f.pinCount <= 0 {
		return fmt.Errorf("page %d already unpinned (pin count = %d)", id, f.pinCount)
	}

	f.pinCount--
	if dirty {
		f.dirty = true
	}
	return nil
}

// NewPage allocates a new page via the disk manager, loads it into the buffer
// pool, and returns it pinned. The caller must UnpinPage when done.
func (bp *BufferPool) NewPage() (PageID, *Page, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	id, diskPage, err := bp.disk.AllocatePage()
	if err != nil {
		return 0, nil, fmt.Errorf("allocate page: %w", err)
	}

	idx, err := bp.findVictim()
	if err != nil {
		return 0, nil, err
	}

	f := &bp.pages[idx]

	if f.valid {
		if err := bp.evict(idx); err != nil {
			return 0, nil, fmt.Errorf("evict for new page: %w", err)
		}
	}

	f.page = *diskPage
	f.pageID = id
	f.pinCount = 1
	f.dirty = false
	f.refBit = true
	f.valid = true
	bp.index[id] = idx

	return id, &f.page, nil
}

// FlushPage writes a specific page to disk if it is dirty.
// The page remains in the pool and its pin count is unchanged.
func (bp *BufferPool) FlushPage(id PageID) error {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	idx, ok := bp.index[id]
	if !ok {
		return fmt.Errorf("page %d not in buffer pool", id)
	}

	f := &bp.pages[idx]
	if f.dirty {
		if err := bp.disk.WritePage(id, &f.page); err != nil {
			return fmt.Errorf("flush page %d: %w", id, err)
		}
		f.dirty = false
	}
	return nil
}

// FlushAll writes all dirty pages to disk. Used for checkpointing.
func (bp *BufferPool) FlushAll() error {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	for i := range bp.pages {
		f := &bp.pages[i]
		if f.valid && f.dirty {
			if err := bp.disk.WritePage(f.pageID, &f.page); err != nil {
				return fmt.Errorf("flush page %d: %w", f.pageID, err)
			}
			f.dirty = false
		}
	}
	return nil
}

// --- Internal helpers (caller must hold bp.mu) ---

// findVictim uses the clock-sweep algorithm to find a frame that can be used
// for a new page. It looks for an empty frame first, then sweeps for an
// unpinned frame with a cleared reference bit.
func (bp *BufferPool) findVictim() (int, error) {
	n := len(bp.pages)

	// First pass: look for an empty (invalid) frame.
	for i := 0; i < n; i++ {
		if !bp.pages[i].valid {
			return i, nil
		}
	}

	// Clock sweep: at most 2 full rotations to find a victim.
	// First rotation clears ref bits; second rotation finds the victim.
	for range 2 * n {
		f := &bp.pages[bp.clock]
		if f.pinCount == 0 {
			if !f.refBit {
				victim := bp.clock
				bp.clock = (bp.clock + 1) % n
				return victim, nil
			}
			f.refBit = false
		}
		bp.clock = (bp.clock + 1) % n
	}

	return 0, errors.New("buffer pool exhausted: all pages are pinned")
}

// evict removes a page from the pool. If dirty, it is flushed to disk first.
func (bp *BufferPool) evict(idx int) error {
	f := &bp.pages[idx]
	if f.dirty {
		if err := bp.disk.WritePage(f.pageID, &f.page); err != nil {
			return err
		}
	}
	delete(bp.index, f.pageID)
	f.valid = false
	f.dirty = false
	f.pinCount = 0
	return nil
}
