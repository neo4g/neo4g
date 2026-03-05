package storage

// disk.go implements the Disk Manager — the lowest layer of the storage engine.
//
// Design:
//   - All data is stored in fixed-size 8KB pages within a single file (data.db).
//   - Page 0 is a reserved header page containing database metadata: magic number,
//     format version, total page count, free list head pointer, and ID counters.
//   - Pages are read/written at explicit offsets using ReadAt/WriteAt (pread/pwrite
//     semantics), giving full control over I/O ordering — critical for WAL correctness.
//   - New pages are allocated from the free list (see freelist.go). If the free list
//     is empty, the data file is extended.
//
// Why not mmap:
//   - mmap delegates flush ordering to the OS, making it impossible to enforce the
//     WAL protocol (log record must be durable before its data page).
//   - mmap provides no control over eviction, which the buffer pool needs.
//   - Explicit I/O with os.File is portable and straightforward in Go.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"
)

const (
	// PageSize is the fixed size of every page in the database file (8KB).
	// Chosen to match common OS page sizes and provide good B+Tree fan-out.
	PageSize = 8192

	// MagicNumber identifies a valid Neo4g data file.
	MagicNumber uint32 = 0x4E454F34 // "NEO4" in ASCII

	// FormatVersion is incremented when the on-disk format changes.
	FormatVersion uint32 = 1

	// HeaderPageID is the page ID of the metadata header page (always page 0).
	HeaderPageID PageID = 0
)

// PageID uniquely identifies a page within the data file.
// A uint32 supports up to ~34 billion bytes (4B pages × 8KB) which is sufficient
// for the target scale of millions of nodes/edges.
type PageID uint32

// InvalidPageID represents a null/invalid page reference, used as a sentinel
// (e.g., the free list tail points to InvalidPageID).
const InvalidPageID PageID = 0

// Page is a fixed-size byte array representing one page of on-disk storage.
// All I/O operates on whole pages — there is no partial page read/write.
type Page [PageSize]byte

// Header page layout offsets.
// The header page (page 0) stores database-wide metadata.
//
// Layout:
//
//	Bytes 0-3:   Magic number (0x4E454F34)
//	Bytes 4-7:   Format version
//	Bytes 8-11:  Total page count (including header)
//	Bytes 12-15: Free list head page ID
//	Bytes 16-23: Next Node ID counter
//	Bytes 24-31: Next Edge ID counter
const (
	headerMagicOffset      = 0
	headerVersionOffset    = 4
	headerPageCountOffset  = 8
	headerFreeHeadOffset   = 12
	headerNextNodeIDOffset = 16
	headerNextEdgeIDOffset = 24
)

// DiskManager handles raw page I/O against a single data file.
// It manages the header page, page allocation/deallocation via the free list,
// and monotonically increasing ID counters for nodes and edges.
//
// Thread safety: DiskManager is safe for concurrent use. All operations are
// protected by a mutex. For higher concurrency, the buffer pool layer above
// should be used instead of calling DiskManager directly.
type DiskManager struct {
	mu   sync.Mutex
	file *os.File

	// Cached header fields — kept in sync with the on-disk header page.
	pageCount  uint32
	freeHead   PageID
	nextNodeID uint64
	nextEdgeID uint64
}

// OpenDiskManager opens or creates a database file at the given path.
// If the file does not exist, it is created with an initialized header page.
// If it exists, the header is read and validated (magic number, version).
//
// Returns an error if the file exists but has an invalid or incompatible format.
func OpenDiskManager(path string) (*DiskManager, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open database file: %w", err)
	}

	dm := &DiskManager{file: file}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("stat database file: %w", err)
	}

	if info.Size() == 0 {
		// New database — initialize header page.
		if err := dm.initHeader(); err != nil {
			file.Close()
			return nil, fmt.Errorf("init header: %w", err)
		}
	} else {
		// Existing database — read and validate header.
		if err := dm.loadHeader(); err != nil {
			file.Close()
			return nil, fmt.Errorf("load header: %w", err)
		}
	}

	return dm, nil
}

// Close flushes the header to disk and closes the underlying file.
func (dm *DiskManager) Close() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if err := dm.flushHeader(); err != nil {
		dm.file.Close()
		return fmt.Errorf("flush header on close: %w", err)
	}
	return dm.file.Close()
}

// ReadPage reads the page with the given ID from disk into a Page buffer.
// Returns an error if the page ID is out of range or the read fails.
func (dm *DiskManager) ReadPage(id PageID) (*Page, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if uint32(id) >= dm.pageCount {
		return nil, fmt.Errorf("page %d out of range (have %d pages)", id, dm.pageCount)
	}

	var p Page
	offset := int64(id) * PageSize
	n, err := dm.file.ReadAt(p[:], offset)
	if err != nil {
		return nil, fmt.Errorf("read page %d: %w", id, err)
	}
	if n != PageSize {
		return nil, fmt.Errorf("read page %d: short read (%d bytes)", id, n)
	}
	return &p, nil
}

// WritePage writes a page to disk at the position determined by its ID.
// Returns an error if the page ID is out of range or the write fails.
//
// Note: WritePage does NOT call fsync. The caller (buffer pool or transaction
// manager) is responsible for durability guarantees.
func (dm *DiskManager) WritePage(id PageID, p *Page) error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if uint32(id) >= dm.pageCount {
		return fmt.Errorf("page %d out of range (have %d pages)", id, dm.pageCount)
	}

	offset := int64(id) * PageSize
	n, err := dm.file.WriteAt(p[:], offset)
	if err != nil {
		return fmt.Errorf("write page %d: %w", id, err)
	}
	if n != PageSize {
		return fmt.Errorf("write page %d: short write (%d bytes)", id, n)
	}
	return nil
}

// AllocatePage returns a fresh page ID. It first tries the free list; if empty,
// it extends the file by one page. The returned page's contents are zeroed.
func (dm *DiskManager) AllocatePage() (PageID, *Page, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	var id PageID

	if dm.freeHead != InvalidPageID {
		// Pop from free list.
		id = dm.freeHead
		p, err := dm.readPageLocked(id)
		if err != nil {
			return 0, nil, fmt.Errorf("read free list head: %w", err)
		}
		// The first 4 bytes of a free page store the next free page pointer.
		dm.freeHead = PageID(binary.LittleEndian.Uint32(p[0:4]))

		// Zero the page for reuse.
		*p = Page{}
		if err := dm.writePageLocked(id, p); err != nil {
			return 0, nil, fmt.Errorf("zero freed page: %w", err)
		}
		if err := dm.flushHeader(); err != nil {
			return 0, nil, fmt.Errorf("flush header after alloc: %w", err)
		}
		return id, p, nil
	}

	// No free pages — extend the file.
	id = PageID(dm.pageCount)
	dm.pageCount++

	p := &Page{}
	if err := dm.writePageLocked(id, p); err != nil {
		dm.pageCount-- // rollback
		return 0, nil, fmt.Errorf("extend file: %w", err)
	}
	if err := dm.flushHeader(); err != nil {
		return 0, nil, fmt.Errorf("flush header after extend: %w", err)
	}
	return id, p, nil
}

// FreePage adds the given page to the free list for later reuse.
// The page contents are overwritten with the free list pointer.
func (dm *DiskManager) FreePage(id PageID) error {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if id == HeaderPageID {
		return errors.New("cannot free header page")
	}
	if uint32(id) >= dm.pageCount {
		return fmt.Errorf("page %d out of range", id)
	}

	// Write next-free pointer into the page.
	var p Page
	binary.LittleEndian.PutUint32(p[0:4], uint32(dm.freeHead))

	if err := dm.writePageLocked(id, &p); err != nil {
		return fmt.Errorf("write free page: %w", err)
	}

	dm.freeHead = id
	return dm.flushHeader()
}

// NextNodeID atomically returns and increments the node ID counter.
func (dm *DiskManager) NextNodeID() (uint64, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	id := dm.nextNodeID
	dm.nextNodeID++
	if err := dm.flushHeader(); err != nil {
		dm.nextNodeID-- // rollback
		return 0, err
	}
	return id, nil
}

// NextEdgeID atomically returns and increments the edge ID counter.
func (dm *DiskManager) NextEdgeID() (uint64, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	id := dm.nextEdgeID
	dm.nextEdgeID++
	if err := dm.flushHeader(); err != nil {
		dm.nextEdgeID-- // rollback
		return 0, err
	}
	return id, nil
}

// Sync forces all buffered writes to durable storage.
func (dm *DiskManager) Sync() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.file.Sync()
}

// PageCount returns the total number of pages in the database file (including header).
func (dm *DiskManager) PageCount() uint32 {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.pageCount
}

// --- Internal helpers (caller must hold dm.mu) ---

// initHeader creates the header page for a new database file.
func (dm *DiskManager) initHeader() error {
	dm.pageCount = 1 // header page itself
	dm.freeHead = InvalidPageID
	dm.nextNodeID = 1 // IDs start at 1; 0 is reserved as "no node"
	dm.nextEdgeID = 1
	return dm.flushHeader()
}

// loadHeader reads and validates the header page from an existing file.
func (dm *DiskManager) loadHeader() error {
	var p Page
	n, err := dm.file.ReadAt(p[:], 0)
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	if n != PageSize {
		return fmt.Errorf("header short read (%d bytes)", n)
	}

	magic := binary.LittleEndian.Uint32(p[headerMagicOffset:])
	if magic != MagicNumber {
		return fmt.Errorf("invalid magic number: 0x%08X (expected 0x%08X)", magic, MagicNumber)
	}

	version := binary.LittleEndian.Uint32(p[headerVersionOffset:])
	if version != FormatVersion {
		return fmt.Errorf("unsupported format version: %d (expected %d)", version, FormatVersion)
	}

	dm.pageCount = binary.LittleEndian.Uint32(p[headerPageCountOffset:])
	dm.freeHead = PageID(binary.LittleEndian.Uint32(p[headerFreeHeadOffset:]))
	dm.nextNodeID = binary.LittleEndian.Uint64(p[headerNextNodeIDOffset:])
	dm.nextEdgeID = binary.LittleEndian.Uint64(p[headerNextEdgeIDOffset:])
	return nil
}

// flushHeader writes the current in-memory header state to page 0 on disk.
func (dm *DiskManager) flushHeader() error {
	var p Page
	binary.LittleEndian.PutUint32(p[headerMagicOffset:], MagicNumber)
	binary.LittleEndian.PutUint32(p[headerVersionOffset:], FormatVersion)
	binary.LittleEndian.PutUint32(p[headerPageCountOffset:], dm.pageCount)
	binary.LittleEndian.PutUint32(p[headerFreeHeadOffset:], uint32(dm.freeHead))
	binary.LittleEndian.PutUint64(p[headerNextNodeIDOffset:], dm.nextNodeID)
	binary.LittleEndian.PutUint64(p[headerNextEdgeIDOffset:], dm.nextEdgeID)

	n, err := dm.file.WriteAt(p[:], 0)
	if err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if n != PageSize {
		return fmt.Errorf("header short write (%d bytes)", n)
	}
	return nil
}

// readPageLocked reads a page without acquiring the mutex (caller must hold it).
func (dm *DiskManager) readPageLocked(id PageID) (*Page, error) {
	var p Page
	offset := int64(id) * PageSize
	n, err := dm.file.ReadAt(p[:], offset)
	if err != nil {
		return nil, err
	}
	if n != PageSize {
		return nil, fmt.Errorf("short read (%d bytes)", n)
	}
	return &p, nil
}

// writePageLocked writes a page without acquiring the mutex (caller must hold it).
func (dm *DiskManager) writePageLocked(id PageID, p *Page) error {
	offset := int64(id) * PageSize
	n, err := dm.file.WriteAt(p[:], offset)
	if err != nil {
		return err
	}
	if n != PageSize {
		return fmt.Errorf("short write (%d bytes)", n)
	}
	return nil
}
