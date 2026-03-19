// Package disk implements the Disk Manager — the lowest layer of the storage engine.
//
// All data is stored in fixed-size 8KB pages within a single file (data.db).
// Page 0 is a reserved header page containing database metadata.
package disk

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/seankohjs/neo4g/storage"
)

// Header page layout offsets.
const (
	headerMagicOffset      = 0
	headerVersionOffset    = 4
	headerPageCountOffset  = 8
	headerFreeHeadOffset   = 12
	headerNextNodeIDOffset = 16
	headerNextEdgeIDOffset = 24
	// B+Tree index root page IDs.
	headerNodeIndexRoot = 32
	headerEdgeIndexRoot = 36
	headerOutIndexRoot  = 40
	headerInIndexRoot   = 44
	// Record page chain head/tail pointers.
	headerNodePageHead = 48
	headerNodePageTail = 52
	headerEdgePageHead = 56
	headerEdgePageTail = 60
)

// Manager handles raw page I/O against a single data file.
// It manages the header page, page allocation/deallocation via the free list,
// and monotonically increasing ID counters for nodes and edges.
//
// Thread safety: Manager is safe for concurrent use.
type Manager struct {
	mu          sync.Mutex
	file        *os.File
	headerDirty bool

	pageCount  uint32
	freeHead   storage.PageID
	nextNodeID uint64
	nextEdgeID uint64

	nodeIndexRoot storage.PageID
	edgeIndexRoot storage.PageID
	outIndexRoot  storage.PageID
	inIndexRoot   storage.PageID

	nodePageHead storage.PageID
	nodePageTail storage.PageID
	edgePageHead storage.PageID
	edgePageTail storage.PageID
}

// Open opens or creates a database file at the given path.
func Open(path string) (*Manager, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open database file: %w", err)
	}

	dm := &Manager{file: file}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("stat database file: %w", err)
	}

	if info.Size() == 0 {
		if err := dm.initHeader(); err != nil {
			file.Close()
			return nil, fmt.Errorf("init header: %w", err)
		}
	} else {
		if err := dm.loadHeader(); err != nil {
			file.Close()
			return nil, fmt.Errorf("load header: %w", err)
		}
	}

	return dm, nil
}

// Close flushes the header to disk and closes the underlying file.
func (dm *Manager) Close() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if dm.headerDirty {
		if err := dm.flushHeader(); err != nil {
			dm.file.Close()
			return fmt.Errorf("flush header on close: %w", err)
		}
	}
	return dm.file.Close()
}

// FlushHeader writes the in-memory header to disk if it has been modified.
// Called by the transaction manager at commit time to batch header writes.
func (dm *Manager) FlushHeader() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if !dm.headerDirty {
		return nil
	}
	return dm.flushHeader()
}

// ReadPage reads the page with the given ID from disk.
func (dm *Manager) ReadPage(id storage.PageID) (*storage.Page, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if uint32(id) >= dm.pageCount {
		return nil, fmt.Errorf("page %d out of range (have %d pages)", id, dm.pageCount)
	}

	var p storage.Page
	offset := int64(id) * storage.PageSize
	n, err := dm.file.ReadAt(p[:], offset)
	if err != nil {
		return nil, fmt.Errorf("read page %d: %w", id, err)
	}
	if n != storage.PageSize {
		return nil, fmt.Errorf("read page %d: short read (%d bytes)", id, n)
	}
	return &p, nil
}

// WritePage writes a page to disk at the position determined by its ID.
func (dm *Manager) WritePage(id storage.PageID, p *storage.Page) error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if uint32(id) >= dm.pageCount {
		return fmt.Errorf("page %d out of range (have %d pages)", id, dm.pageCount)
	}

	offset := int64(id) * storage.PageSize
	n, err := dm.file.WriteAt(p[:], offset)
	if err != nil {
		return fmt.Errorf("write page %d: %w", id, err)
	}
	if n != storage.PageSize {
		return fmt.Errorf("write page %d: short write (%d bytes)", id, n)
	}
	return nil
}

// AllocatePage returns a fresh page ID. It first tries the free list; if empty,
// it extends the file by one page.
func (dm *Manager) AllocatePage() (storage.PageID, *storage.Page, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	var id storage.PageID

	if dm.freeHead != storage.InvalidPageID {
		id = dm.freeHead
		p, err := dm.readPageLocked(id)
		if err != nil {
			return 0, nil, fmt.Errorf("read free list head: %w", err)
		}
		dm.freeHead = storage.PageID(binary.LittleEndian.Uint32(p[0:4]))

		*p = storage.Page{}
		if err := dm.writePageLocked(id, p); err != nil {
			return 0, nil, fmt.Errorf("zero freed page: %w", err)
		}
		dm.headerDirty = true
		return id, p, nil
	}

	id = storage.PageID(dm.pageCount)
	dm.pageCount++

	p := &storage.Page{}
	if err := dm.writePageLocked(id, p); err != nil {
		dm.pageCount--
		return 0, nil, fmt.Errorf("extend file: %w", err)
	}
	dm.headerDirty = true
	return id, p, nil
}

// FreePage adds the given page to the free list for later reuse.
func (dm *Manager) FreePage(id storage.PageID) error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if id == storage.HeaderPageID {
		return errors.New("cannot free header page")
	}
	if uint32(id) >= dm.pageCount {
		return fmt.Errorf("page %d out of range", id)
	}

	var p storage.Page
	binary.LittleEndian.PutUint32(p[0:4], uint32(dm.freeHead))

	if err := dm.writePageLocked(id, &p); err != nil {
		return fmt.Errorf("write free page: %w", err)
	}

	dm.freeHead = id
	dm.headerDirty = true
	return nil
}

// NextNodeID atomically returns and increments the node ID counter.
func (dm *Manager) NextNodeID() (uint64, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	id := dm.nextNodeID
	dm.nextNodeID++
	dm.headerDirty = true
	return id, nil
}

// NextEdgeID atomically returns and increments the edge ID counter.
func (dm *Manager) NextEdgeID() (uint64, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	id := dm.nextEdgeID
	dm.nextEdgeID++
	dm.headerDirty = true
	return id, nil
}

// Sync forces all buffered writes to durable storage.
func (dm *Manager) Sync() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dm.headerDirty {
		if err := dm.flushHeader(); err != nil {
			return err
		}
	}
	return dm.file.Sync()
}

// PageCount returns the total number of pages in the database file.
func (dm *Manager) PageCount() uint32 {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.pageCount
}

// --- Index root persistence ---

func (dm *Manager) NodeIndexRoot() storage.PageID  { dm.mu.Lock(); defer dm.mu.Unlock(); return dm.nodeIndexRoot }
func (dm *Manager) EdgeIndexRoot() storage.PageID  { dm.mu.Lock(); defer dm.mu.Unlock(); return dm.edgeIndexRoot }
func (dm *Manager) OutIndexRoot() storage.PageID   { dm.mu.Lock(); defer dm.mu.Unlock(); return dm.outIndexRoot }
func (dm *Manager) InIndexRoot() storage.PageID    { dm.mu.Lock(); defer dm.mu.Unlock(); return dm.inIndexRoot }

func (dm *Manager) SetNodeIndexRoot(id storage.PageID) error { dm.mu.Lock(); defer dm.mu.Unlock(); dm.nodeIndexRoot = id; dm.headerDirty = true; return nil }
func (dm *Manager) SetEdgeIndexRoot(id storage.PageID) error { dm.mu.Lock(); defer dm.mu.Unlock(); dm.edgeIndexRoot = id; dm.headerDirty = true; return nil }
func (dm *Manager) SetOutIndexRoot(id storage.PageID) error  { dm.mu.Lock(); defer dm.mu.Unlock(); dm.outIndexRoot = id; dm.headerDirty = true; return nil }
func (dm *Manager) SetInIndexRoot(id storage.PageID) error   { dm.mu.Lock(); defer dm.mu.Unlock(); dm.inIndexRoot = id; dm.headerDirty = true; return nil }

// --- Record page chain persistence ---

func (dm *Manager) NodePageHead() storage.PageID { dm.mu.Lock(); defer dm.mu.Unlock(); return dm.nodePageHead }
func (dm *Manager) NodePageTail() storage.PageID { dm.mu.Lock(); defer dm.mu.Unlock(); return dm.nodePageTail }
func (dm *Manager) EdgePageHead() storage.PageID { dm.mu.Lock(); defer dm.mu.Unlock(); return dm.edgePageHead }
func (dm *Manager) EdgePageTail() storage.PageID { dm.mu.Lock(); defer dm.mu.Unlock(); return dm.edgePageTail }

func (dm *Manager) SetNodePageHead(id storage.PageID) error { dm.mu.Lock(); defer dm.mu.Unlock(); dm.nodePageHead = id; dm.headerDirty = true; return nil }
func (dm *Manager) SetNodePageTail(id storage.PageID) error { dm.mu.Lock(); defer dm.mu.Unlock(); dm.nodePageTail = id; dm.headerDirty = true; return nil }
func (dm *Manager) SetEdgePageHead(id storage.PageID) error { dm.mu.Lock(); defer dm.mu.Unlock(); dm.edgePageHead = id; dm.headerDirty = true; return nil }
func (dm *Manager) SetEdgePageTail(id storage.PageID) error { dm.mu.Lock(); defer dm.mu.Unlock(); dm.edgePageTail = id; dm.headerDirty = true; return nil }

// --- Internal helpers (caller must hold dm.mu) ---

func (dm *Manager) initHeader() error {
	dm.pageCount = 1
	dm.freeHead = storage.InvalidPageID
	dm.nextNodeID = 1
	dm.nextEdgeID = 1
	return dm.flushHeader()
}

func (dm *Manager) loadHeader() error {
	var p storage.Page
	n, err := dm.file.ReadAt(p[:], 0)
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	if n != storage.PageSize {
		return fmt.Errorf("header short read (%d bytes)", n)
	}

	magic := binary.LittleEndian.Uint32(p[headerMagicOffset:])
	if magic != storage.MagicNumber {
		return fmt.Errorf("invalid magic number: 0x%08X (expected 0x%08X)", magic, storage.MagicNumber)
	}

	version := binary.LittleEndian.Uint32(p[headerVersionOffset:])
	if version != storage.FormatVersion {
		return fmt.Errorf("unsupported format version: %d (expected %d)", version, storage.FormatVersion)
	}

	dm.pageCount = binary.LittleEndian.Uint32(p[headerPageCountOffset:])
	dm.freeHead = storage.PageID(binary.LittleEndian.Uint32(p[headerFreeHeadOffset:]))
	dm.nextNodeID = binary.LittleEndian.Uint64(p[headerNextNodeIDOffset:])
	dm.nextEdgeID = binary.LittleEndian.Uint64(p[headerNextEdgeIDOffset:])

	dm.nodeIndexRoot = storage.PageID(binary.LittleEndian.Uint32(p[headerNodeIndexRoot:]))
	dm.edgeIndexRoot = storage.PageID(binary.LittleEndian.Uint32(p[headerEdgeIndexRoot:]))
	dm.outIndexRoot = storage.PageID(binary.LittleEndian.Uint32(p[headerOutIndexRoot:]))
	dm.inIndexRoot = storage.PageID(binary.LittleEndian.Uint32(p[headerInIndexRoot:]))

	dm.nodePageHead = storage.PageID(binary.LittleEndian.Uint32(p[headerNodePageHead:]))
	dm.nodePageTail = storage.PageID(binary.LittleEndian.Uint32(p[headerNodePageTail:]))
	dm.edgePageHead = storage.PageID(binary.LittleEndian.Uint32(p[headerEdgePageHead:]))
	dm.edgePageTail = storage.PageID(binary.LittleEndian.Uint32(p[headerEdgePageTail:]))
	return nil
}

func (dm *Manager) flushHeader() error {
	var p storage.Page
	binary.LittleEndian.PutUint32(p[headerMagicOffset:], storage.MagicNumber)
	binary.LittleEndian.PutUint32(p[headerVersionOffset:], storage.FormatVersion)
	binary.LittleEndian.PutUint32(p[headerPageCountOffset:], dm.pageCount)
	binary.LittleEndian.PutUint32(p[headerFreeHeadOffset:], uint32(dm.freeHead))
	binary.LittleEndian.PutUint64(p[headerNextNodeIDOffset:], dm.nextNodeID)
	binary.LittleEndian.PutUint64(p[headerNextEdgeIDOffset:], dm.nextEdgeID)

	binary.LittleEndian.PutUint32(p[headerNodeIndexRoot:], uint32(dm.nodeIndexRoot))
	binary.LittleEndian.PutUint32(p[headerEdgeIndexRoot:], uint32(dm.edgeIndexRoot))
	binary.LittleEndian.PutUint32(p[headerOutIndexRoot:], uint32(dm.outIndexRoot))
	binary.LittleEndian.PutUint32(p[headerInIndexRoot:], uint32(dm.inIndexRoot))

	binary.LittleEndian.PutUint32(p[headerNodePageHead:], uint32(dm.nodePageHead))
	binary.LittleEndian.PutUint32(p[headerNodePageTail:], uint32(dm.nodePageTail))
	binary.LittleEndian.PutUint32(p[headerEdgePageHead:], uint32(dm.edgePageHead))
	binary.LittleEndian.PutUint32(p[headerEdgePageTail:], uint32(dm.edgePageTail))

	n, err := dm.file.WriteAt(p[:], 0)
	if err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if n != storage.PageSize {
		return fmt.Errorf("header short write (%d bytes)", n)
	}
	dm.headerDirty = false
	return nil
}

func (dm *Manager) readPageLocked(id storage.PageID) (*storage.Page, error) {
	var p storage.Page
	offset := int64(id) * storage.PageSize
	n, err := dm.file.ReadAt(p[:], offset)
	if err != nil {
		return nil, err
	}
	if n != storage.PageSize {
		return nil, fmt.Errorf("short read (%d bytes)", n)
	}
	return &p, nil
}

func (dm *Manager) writePageLocked(id storage.PageID, p *storage.Page) error {
	offset := int64(id) * storage.PageSize
	n, err := dm.file.WriteAt(p[:], offset)
	if err != nil {
		return err
	}
	if n != storage.PageSize {
		return fmt.Errorf("short write (%d bytes)", n)
	}
	return nil
}
