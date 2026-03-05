package storage

// page_layout.go implements the slotted page format for storing variable-size
// records within fixed 8KB pages.
//
// Page Layout:
//
//	┌──────────────────────────────────────────────┐  offset 0
//	│ Page Header (20 bytes)                       │
//	│   - PageID      (4 bytes)                    │
//	│   - PageType    (2 bytes)                    │
//	│   - SlotCount   (2 bytes)                    │
//	│   - FreeOffset  (2 bytes) ← end of slots     │
//	│   - FreeEnd     (2 bytes) ← start of records │
//	│   - NextPage    (4 bytes) ← page chain link  │
//	│   - Reserved    (4 bytes)                    │
//	├──────────────────────────────────────────────┤
//	│ Slot Directory (grows downward, 4 bytes each)│
//	│   Slot 0: [offset:2][length:2]               │
//	│   Slot 1: [offset:2][length:2]               │
//	│   ...                                        │
//	├──────────────────────────────────────────────┤
//	│              Free Space                       │
//	├──────────────────────────────────────────────┤
//	│ Record Data (grows upward from page end)     │
//	│   Record N: [...]                            │
//	│   Record 1: [...]                            │
//	│   Record 0: [...]                            │
//	└──────────────────────────────────────────────┘  offset PageSize
//
// The slot directory grows downward from the header, while record data grows
// upward from the end of the page. When they meet, the page is full.
//
// Deletion uses tombstones: the slot's length is set to 0 and its offset to
// 0xFFFF. Compaction reclaims the space occupied by deleted records by
// repacking live records toward the end of the page.
//
// Design rationale:
//   - Slotted pages are the industry standard for variable-size records (used
//     by PostgreSQL, MySQL InnoDB, SQLite). Well-understood and proven.
//   - In-page reorganization (compaction) doesn't change slot indices, so RIDs
//     remain stable — critical for index correctness.
//   - The NextPage field enables page chains for sequential scans of all
//     nodes or all edges.

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Page types identify what kind of records a page holds.
const (
	PageTypeNode     uint16 = 1
	PageTypeEdge     uint16 = 2
	PageTypeBTreeInt uint16 = 3 // B+Tree internal node
	PageTypeBTreeLf  uint16 = 4 // B+Tree leaf node
	PageTypeFree     uint16 = 0 // free/unallocated page
)

// Page header layout constants.
const (
	pHeaderSize       = 20 // total header size in bytes
	pOffPageID        = 0
	pOffPageType      = 4
	pOffSlotCount     = 6
	pOffFreeOffset    = 8  // first byte after the last slot entry
	pOffFreeEnd       = 10 // first byte of the first (topmost) record
	pOffNextPage      = 12
	pOffReserved      = 16
	slotEntrySize     = 4  // each slot: 2 bytes offset + 2 bytes length
	tombstoneOffset   = 0xFFFF
)

// SlottedPage provides methods to read and write variable-size records within
// a Page using the slotted page format.
//
// SlottedPage operates directly on a *Page (a byte array). It does not own the
// page memory — the buffer pool does. This means all reads/writes through
// SlottedPage immediately mutate the underlying Page buffer.
type SlottedPage struct {
	page *Page
}

// NewSlottedPage wraps an existing Page buffer for slotted page operations.
// The page should already be formatted (via InitSlottedPage) or be a freshly
// allocated page about to be initialized.
func NewSlottedPage(p *Page) *SlottedPage {
	return &SlottedPage{page: p}
}

// InitSlottedPage formats a page as an empty slotted page with the given type.
// This must be called once when a page is first allocated for record storage.
func InitSlottedPage(p *Page, id PageID, pageType uint16) *SlottedPage {
	sp := &SlottedPage{page: p}
	binary.LittleEndian.PutUint32(p[pOffPageID:], uint32(id))
	binary.LittleEndian.PutUint16(p[pOffPageType:], pageType)
	binary.LittleEndian.PutUint16(p[pOffSlotCount:], 0)
	binary.LittleEndian.PutUint16(p[pOffFreeOffset:], pHeaderSize)
	binary.LittleEndian.PutUint16(p[pOffFreeEnd:], PageSize)
	binary.LittleEndian.PutUint32(p[pOffNextPage:], uint32(InvalidPageID))
	return sp
}

// --- Header accessors ---

// GetPageID returns the page ID stored in the page header.
func (sp *SlottedPage) GetPageID() PageID {
	return PageID(binary.LittleEndian.Uint32(sp.page[pOffPageID:]))
}

// GetPageType returns the page type (node, edge, btree, etc).
func (sp *SlottedPage) GetPageType() uint16 {
	return binary.LittleEndian.Uint16(sp.page[pOffPageType:])
}

// SlotCount returns the number of slots in the directory (including tombstones).
func (sp *SlottedPage) SlotCount() uint16 {
	return binary.LittleEndian.Uint16(sp.page[pOffSlotCount:])
}

// FreeSpace returns the number of bytes available for new records and slots.
func (sp *SlottedPage) FreeSpace() uint16 {
	freeOffset := binary.LittleEndian.Uint16(sp.page[pOffFreeOffset:])
	freeEnd := binary.LittleEndian.Uint16(sp.page[pOffFreeEnd:])
	if freeEnd <= freeOffset {
		return 0
	}
	return freeEnd - freeOffset
}

// GetNextPage returns the next page ID in the page chain, or InvalidPageID.
func (sp *SlottedPage) GetNextPage() PageID {
	return PageID(binary.LittleEndian.Uint32(sp.page[pOffNextPage:]))
}

// SetNextPage sets the next page pointer in the page chain.
func (sp *SlottedPage) SetNextPage(id PageID) {
	binary.LittleEndian.PutUint32(sp.page[pOffNextPage:], uint32(id))
}

// --- Record operations ---

// InsertRecord adds a record to the page. Returns the slot index on success.
// Returns an error if there is not enough free space for the record + slot entry.
//
// The record data is copied into the page — the caller can reuse the slice after.
func (sp *SlottedPage) InsertRecord(data []byte) (uint16, error) {
	needed := uint16(len(data)) + slotEntrySize
	if sp.FreeSpace() < needed {
		return 0, errors.New("not enough free space in page")
	}

	slotCount := sp.SlotCount()
	freeEnd := binary.LittleEndian.Uint16(sp.page[pOffFreeEnd:])

	// Place record at the end of free space (growing upward from bottom).
	recordOffset := freeEnd - uint16(len(data))
	copy(sp.page[recordOffset:], data)

	// Append slot entry to the directory.
	slotPos := pHeaderSize + int(slotCount)*slotEntrySize
	binary.LittleEndian.PutUint16(sp.page[slotPos:], recordOffset)
	binary.LittleEndian.PutUint16(sp.page[slotPos+2:], uint16(len(data)))

	// Update header.
	binary.LittleEndian.PutUint16(sp.page[pOffSlotCount:], slotCount+1)
	binary.LittleEndian.PutUint16(sp.page[pOffFreeOffset:], uint16(slotPos+slotEntrySize))
	binary.LittleEndian.PutUint16(sp.page[pOffFreeEnd:], recordOffset)

	return slotCount, nil
}

// GetRecord returns the record data at the given slot index.
// Returns nil if the slot is a tombstone (deleted). Returns an error if the
// slot index is out of range.
func (sp *SlottedPage) GetRecord(slot uint16) ([]byte, error) {
	if slot >= sp.SlotCount() {
		return nil, fmt.Errorf("slot %d out of range (have %d slots)", slot, sp.SlotCount())
	}

	offset, length := sp.slotEntry(slot)
	if offset == tombstoneOffset {
		return nil, nil // deleted
	}

	data := make([]byte, length)
	copy(data, sp.page[offset:offset+length])
	return data, nil
}

// UpdateRecord replaces the record at the given slot with new data.
// If the new data fits in the existing space, it is updated in-place.
// If it's larger, the old space is wasted and the record is appended (like an
// insert). Returns an error if there is not enough space.
func (sp *SlottedPage) UpdateRecord(slot uint16, data []byte) error {
	if slot >= sp.SlotCount() {
		return fmt.Errorf("slot %d out of range", slot)
	}

	oldOffset, oldLength := sp.slotEntry(slot)
	if oldOffset == tombstoneOffset {
		return fmt.Errorf("slot %d is deleted", slot)
	}

	if uint16(len(data)) <= oldLength {
		// Fits in place — overwrite (remaining bytes stay as padding).
		copy(sp.page[oldOffset:], data)
		sp.setSlotEntry(slot, oldOffset, uint16(len(data)))
		return nil
	}

	// Doesn't fit — allocate new space at the end (old space becomes garbage).
	// We need space for the new record data only (slot already exists).
	freeEnd := binary.LittleEndian.Uint16(sp.page[pOffFreeEnd:])
	freeOffset := binary.LittleEndian.Uint16(sp.page[pOffFreeOffset:])
	available := freeEnd - freeOffset
	if uint16(len(data)) > available {
		return errors.New("not enough free space for updated record")
	}

	newOffset := freeEnd - uint16(len(data))
	copy(sp.page[newOffset:], data)
	sp.setSlotEntry(slot, newOffset, uint16(len(data)))
	binary.LittleEndian.PutUint16(sp.page[pOffFreeEnd:], newOffset)
	return nil
}

// DeleteRecord marks a slot as a tombstone. The space is not immediately
// reclaimed — call Compact to defragment.
func (sp *SlottedPage) DeleteRecord(slot uint16) error {
	if slot >= sp.SlotCount() {
		return fmt.Errorf("slot %d out of range", slot)
	}

	offset, _ := sp.slotEntry(slot)
	if offset == tombstoneOffset {
		return fmt.Errorf("slot %d already deleted", slot)
	}

	sp.setSlotEntry(slot, tombstoneOffset, 0)
	return nil
}

// IsDeleted returns true if the slot is a tombstone.
func (sp *SlottedPage) IsDeleted(slot uint16) bool {
	if slot >= sp.SlotCount() {
		return true
	}
	offset, _ := sp.slotEntry(slot)
	return offset == tombstoneOffset
}

// Compact defragments the page by repacking all live records toward the end
// of the page, reclaiming space left by deleted or updated records.
// Slot indices are preserved — only the internal offsets change.
func (sp *SlottedPage) Compact() {
	slotCount := sp.SlotCount()
	writePos := uint16(PageSize) // start packing from the end

	for i := uint16(0); i < slotCount; i++ {
		offset, length := sp.slotEntry(i)
		if offset == tombstoneOffset || length == 0 {
			continue // skip deleted slots
		}

		// Copy record to new position.
		newOffset := writePos - length
		if newOffset != offset {
			copy(sp.page[newOffset:newOffset+length], sp.page[offset:offset+length])
			sp.setSlotEntry(i, newOffset, length)
		}
		writePos = newOffset
	}

	// Update free space end pointer.
	binary.LittleEndian.PutUint16(sp.page[pOffFreeEnd:], writePos)
}

// --- Internal helpers ---

// slotEntry reads the (offset, length) pair for a slot from the directory.
func (sp *SlottedPage) slotEntry(slot uint16) (offset, length uint16) {
	pos := pHeaderSize + int(slot)*slotEntrySize
	offset = binary.LittleEndian.Uint16(sp.page[pos:])
	length = binary.LittleEndian.Uint16(sp.page[pos+2:])
	return
}

// setSlotEntry writes the (offset, length) pair for a slot in the directory.
func (sp *SlottedPage) setSlotEntry(slot uint16, offset, length uint16) {
	pos := pHeaderSize + int(slot)*slotEntrySize
	binary.LittleEndian.PutUint16(sp.page[pos:], offset)
	binary.LittleEndian.PutUint16(sp.page[pos+2:], length)
}
