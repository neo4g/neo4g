// Package storage defines the shared types used across all storage engine
// sub-packages (disk, pool, page, index, record, txn, wal, encoding).
//
// # Architecture
//
// The storage layer is organized in 6 layers, each depending only on the one below it:
//
//	┌─────────────────────────────────┐
//	│  6. Transaction Manager (ACID)  │  txn/
//	├─────────────────────────────────┤
//	│  5. Index Manager (B+Tree)      │  index/
//	├─────────────────────────────────┤
//	│  4. Record Manager              │  record/
//	│     (Node/Edge CRUD)            │
//	├─────────────────────────────────┤
//	│  3. Slotted Page Layout         │  page/
//	│     (variable-size records)     │
//	├─────────────────────────────────┤
//	│  2. Buffer Pool + Page Cache    │  pool/
//	├─────────────────────────────────┤
//	│  1. Disk Manager + WAL          │  disk/ + wal/
//	│     (pages, free list, log)     │
//	└─────────────────────────────────┘
package storage

const (
	// PageSize is the fixed size of every page in the database file (8KB).
	PageSize = 8192

	// MagicNumber identifies a valid Neo4g data file.
	MagicNumber uint32 = 0x4E454F34 // "NEO4" in ASCII

	// FormatVersion is incremented when the on-disk format changes.
	FormatVersion uint32 = 1
)

// PageID uniquely identifies a page within the data file.
type PageID uint32

// InvalidPageID represents a null/invalid page reference.
const InvalidPageID PageID = 0

// HeaderPageID is the page ID of the metadata header page (always page 0).
const HeaderPageID PageID = 0

// Page is a fixed-size byte array representing one page of on-disk storage.
type Page [PageSize]byte

// Page type constants identify what kind of records a page holds.
const (
	PageTypeFree     uint16 = 0
	PageTypeNode     uint16 = 1
	PageTypeEdge     uint16 = 2
	PageTypeBTreeInt uint16 = 3
	PageTypeBTreeLf  uint16 = 4
)

// Common page header offsets shared by slotted pages and B+Tree nodes.
const (
	OffPageID   = 0
	OffPageType = 4
)

// RID uniquely identifies a record's physical location as (PageID, SlotIndex).
type RID struct {
	PageID PageID
	Slot   uint16
}

// InvalidRID is a sentinel value representing no record.
var InvalidRID = RID{PageID: InvalidPageID, Slot: 0}

// IsValid returns true if this RID points to a potentially valid location.
func (r RID) IsValid() bool {
	return r.PageID != InvalidPageID
}

// TxID uniquely identifies a transaction.
type TxID uint64
