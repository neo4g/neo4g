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
)

// Manager handles raw page I/O against a single data file.
// It manages the header page, page allocation/deallocation via the free list,
// and monotonically increasing ID counters for nodes and edges.
//
// Thread safety: Manager is safe for concurrent use.
type Manager struct {
	mu   sync.Mutex
	file *os.File

	pageCount  uint32
	freeHead   storage.PageID
	nextNodeID uint64
	nextEdgeID uint64
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

	if err := dm.flushHeader(); err != nil {
		dm.file.Close()
		return fmt.Errorf("flush header on close: %w", err)
	}
	return dm.file.Close()
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
		if err := dm.flushHeader(); err != nil {
			return 0, nil, fmt.Errorf("flush header after alloc: %w", err)
		}
		return id, p, nil
	}

	id = storage.PageID(dm.pageCount)
	dm.pageCount++

	p := &storage.Page{}
	if err := dm.writePageLocked(id, p); err != nil {
		dm.pageCount--
		return 0, nil, fmt.Errorf("extend file: %w", err)
	}
	if err := dm.flushHeader(); err != nil {
		return 0, nil, fmt.Errorf("flush header after extend: %w", err)
	}
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
	return dm.flushHeader()
}

// NextNodeID atomically returns and increments the node ID counter.
func (dm *Manager) NextNodeID() (uint64, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	id := dm.nextNodeID
	dm.nextNodeID++
	if err := dm.flushHeader(); err != nil {
		dm.nextNodeID--
		return 0, err
	}
	return id, nil
}

// NextEdgeID atomically returns and increments the edge ID counter.
func (dm *Manager) NextEdgeID() (uint64, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	id := dm.nextEdgeID
	dm.nextEdgeID++
	if err := dm.flushHeader(); err != nil {
		dm.nextEdgeID--
		return 0, err
	}
	return id, nil
}

// Sync forces all buffered writes to durable storage.
func (dm *Manager) Sync() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.file.Sync()
}

// PageCount returns the total number of pages in the database file.
func (dm *Manager) PageCount() uint32 {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.pageCount
}

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

	n, err := dm.file.WriteAt(p[:], 0)
	if err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if n != storage.PageSize {
		return fmt.Errorf("header short write (%d bytes)", n)
	}
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
