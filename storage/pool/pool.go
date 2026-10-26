// Package pool implements the Buffer Pool — an in-memory page cache that sits
// between the disk manager and all higher layers.
package pool

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/neo4g/neo4g/storage"
	"github.com/neo4g/neo4g/storage/disk"
)

var _ storage.PageManager = (*Pool)(nil)

type frame struct {
	page     storage.Page
	pageID   storage.PageID
	pinCount atomic.Int32
	dirty    atomic.Bool
	refBit   atomic.Bool
	hdrGen   uint64 // header generation observed when page 0 was loaded
	valid    bool   // only mutated under write lock
}

// Pool is an in-memory cache of disk pages with clock-sweep eviction.
//
// Thread safety: Pool is safe for concurrent use. Cache hits on FetchPage use
// a read lock for maximum concurrency; misses, evictions, and mutations take
// the write lock.
type Pool struct {
	mu    sync.RWMutex
	disk  *disk.Manager
	pages []frame
	index map[storage.PageID]int
	clock int
}

// New creates a buffer pool with the given number of page frames.
func New(dm *disk.Manager, poolSize int) *Pool {
	return &Pool{
		disk:  dm,
		pages: make([]frame, poolSize),
		index: make(map[storage.PageID]int, poolSize),
	}
}

// FetchPage returns the page with the given ID, loading it from disk if not
// already cached. The page is pinned — the caller MUST call UnpinPage when done.
func (bp *Pool) FetchPage(id storage.PageID) (*storage.Page, error) {
	// Fast path: cache hit under read lock. A stale header frame falls
	// through so it can be reloaded from the disk manager.
	bp.mu.RLock()
	if idx, ok := bp.index[id]; ok {
		f := &bp.pages[idx]
		headerFresh := id != storage.HeaderPageID || f.hdrGen == bp.disk.HeaderGen() || f.pinCount.Load() > 0
		if headerFresh {
			f.pinCount.Add(1)
			f.refBit.Store(true)
			bp.mu.RUnlock()
			return &f.page, nil
		}
	}
	bp.mu.RUnlock()

	// Slow path: cache miss — acquire write lock
	bp.mu.Lock()
	defer bp.mu.Unlock()

	// Double-check: another goroutine may have loaded it
	if idx, ok := bp.index[id]; ok {
		f := &bp.pages[idx]
		if id == storage.HeaderPageID && f.pinCount.Load() == 0 && f.hdrGen != bp.disk.HeaderGen() {
			f.page = bp.disk.HeaderImage()
			f.hdrGen = bp.disk.HeaderGen()
			f.dirty.Store(false)
		}
		f.pinCount.Add(1)
		f.refBit.Store(true)
		return &f.page, nil
	}

	idx, err := bp.findVictim()
	if err != nil {
		return nil, err
	}

	f := &bp.pages[idx]

	if f.valid {
		if err := bp.evict(idx); err != nil {
			return nil, fmt.Errorf("evict frame %d: %w", idx, err)
		}
	}

	if id == storage.HeaderPageID {
		f.page = bp.disk.HeaderImage()
		f.hdrGen = bp.disk.HeaderGen()
	} else {
		p, err := bp.disk.ReadPage(id)
		if err != nil {
			return nil, fmt.Errorf("fetch page %d: %w", id, err)
		}
		f.page = *p
		f.hdrGen = 0
	}
	f.pageID = id
	f.pinCount.Store(1)
	f.dirty.Store(false)
	f.refBit.Store(true)
	f.valid = true
	bp.index[id] = idx

	return &f.page, nil
}

// UnpinPage decrements the pin count on a page. If dirty is true, the page is
// marked dirty so it will be flushed to disk before eviction.
func (bp *Pool) UnpinPage(id storage.PageID, dirty bool) error {
	bp.mu.RLock()
	idx, ok := bp.index[id]
	if !ok {
		bp.mu.RUnlock()
		return fmt.Errorf("page %d not in buffer pool", id)
	}

	f := &bp.pages[idx]
	newPin := f.pinCount.Add(-1)
	if dirty {
		f.dirty.Store(true)
	}
	bp.mu.RUnlock()

	if newPin < 0 {
		// Restore and report error
		f.pinCount.Add(1)
		return fmt.Errorf("page %d already unpinned (pin count = %d)", id, newPin)
	}
	return nil
}

// NewPage allocates a new page via the disk manager, loads it into the buffer
// pool, and returns it pinned.
func (bp *Pool) NewPage() (storage.PageID, *storage.Page, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	id, diskPage, err := bp.disk.AllocatePage()
	if err != nil {
		return 0, nil, fmt.Errorf("allocate page: %w", err)
	}

	if idx, ok := bp.index[id]; ok && bp.pages[idx].pinCount.Load() == 0 {
		f := &bp.pages[idx]
		f.page = *diskPage
		f.pageID = id
		f.pinCount.Store(1)
		f.dirty.Store(false)
		f.refBit.Store(true)
		f.valid = true
		f.hdrGen = 0
		return id, &f.page, nil
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
	f.pinCount.Store(1)
	f.dirty.Store(false)
	f.refBit.Store(true)
	f.valid = true
	f.hdrGen = 0
	bp.index[id] = idx

	return id, &f.page, nil
}

// FlushPage writes a specific page to disk if it is dirty.
func (bp *Pool) FlushPage(id storage.PageID) error {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	idx, ok := bp.index[id]
	if !ok {
		return fmt.Errorf("page %d not in buffer pool", id)
	}

	f := &bp.pages[idx]
	if err := bp.writeFrame(f); err != nil {
		return fmt.Errorf("flush page %d: %w", id, err)
	}
	return nil
}

// FlushAll writes all dirty pages to disk.
func (bp *Pool) FlushAll() error {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	for i := range bp.pages {
		f := &bp.pages[i]
		if !f.valid {
			continue
		}
		if err := bp.writeFrame(f); err != nil {
			return fmt.Errorf("flush page %d: %w", f.pageID, err)
		}
	}
	return nil
}

// Install copies src into the cached frame for id and marks it dirty.
// Returns false when id is not cached. Page 0 is not installed here; the
// disk manager is the only writer of the header.
func (bp *Pool) Install(id storage.PageID, src *storage.Page) bool {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	idx, ok := bp.index[id]
	if !ok || id == storage.HeaderPageID {
		return false
	}
	f := &bp.pages[idx]
	f.page = *src
	f.dirty.Store(true)
	return true
}

// Cached returns a copy of a cached page. The bool is false on a miss.
func (bp *Pool) Cached(id storage.PageID) (storage.Page, bool) {
	bp.mu.RLock()
	defer bp.mu.RUnlock()
	idx, ok := bp.index[id]
	if !ok {
		return storage.Page{}, false
	}
	return bp.pages[idx].page, true
}

// --- Internal helpers (caller must hold bp.mu write lock) ---

func (bp *Pool) findVictim() (int, error) {
	n := len(bp.pages)

	for i := 0; i < n; i++ {
		if !bp.pages[i].valid {
			return i, nil
		}
	}

	for range 2 * n {
		f := &bp.pages[bp.clock]
		if f.pinCount.Load() == 0 {
			if !f.refBit.Load() {
				victim := bp.clock
				bp.clock = (bp.clock + 1) % n
				return victim, nil
			}
			f.refBit.Store(false)
		}
		bp.clock = (bp.clock + 1) % n
	}

	return 0, errors.New("buffer pool exhausted: all pages are pinned")
}

func (bp *Pool) evict(idx int) error {
	f := &bp.pages[idx]
	if err := bp.writeFrame(f); err != nil {
		return err
	}
	delete(bp.index, f.pageID)
	f.valid = false
	f.dirty.Store(false)
	f.pinCount.Store(0)
	return nil
}

// writeFrame persists a dirty frame. Page 0 is adopted by the disk manager
// when the frame still matches the catalog generation; a stale header frame
// is dropped so it cannot overwrite a newer catalog.
func (bp *Pool) writeFrame(f *frame) error {
	if !f.valid || !f.dirty.Load() {
		return nil
	}
	if f.pageID == storage.HeaderPageID {
		if f.hdrGen != bp.disk.HeaderGen() {
			f.dirty.Store(false)
			return nil
		}
		img := f.page
		if err := bp.disk.InstallHeaderImage(&img); err != nil {
			return err
		}
		f.dirty.Store(false)
		f.hdrGen = bp.disk.HeaderGen()
		return nil
	}
	if err := bp.disk.WritePage(f.pageID, &f.page); err != nil {
		return err
	}
	f.dirty.Store(false)
	return nil
}
