// Package page implements the slotted page format for storing variable-size
// records within fixed 8KB pages.
package page

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/seankohjs/neo4g/storage"
)

// Page header layout constants.
const (
	HeaderSize    = 20
	offSlotCount  = 6
	offFreeOffset = 8
	offFreeEnd    = 10
	offNextPage   = 12
	SlotEntrySize = 4 // each slot: 2 bytes offset + 2 bytes length

	tombstoneOffset = 0xFFFF
)

// SlottedPage provides methods to read and write variable-size records within
// a Page using the slotted page format.
type SlottedPage struct {
	page *storage.Page
}

// New wraps an existing Page buffer for slotted page operations.
func New(p *storage.Page) *SlottedPage {
	return &SlottedPage{page: p}
}

// Init formats a page as an empty slotted page with the given type.
func Init(p *storage.Page, id storage.PageID, pageType uint16) *SlottedPage {
	sp := &SlottedPage{page: p}
	binary.LittleEndian.PutUint32(p[storage.OffPageID:], uint32(id))
	binary.LittleEndian.PutUint16(p[storage.OffPageType:], pageType)
	binary.LittleEndian.PutUint16(p[offSlotCount:], 0)
	binary.LittleEndian.PutUint16(p[offFreeOffset:], HeaderSize)
	binary.LittleEndian.PutUint16(p[offFreeEnd:], storage.PageSize)
	binary.LittleEndian.PutUint32(p[offNextPage:], uint32(storage.InvalidPageID))
	return sp
}

// --- Header accessors ---

func (sp *SlottedPage) GetPageID() storage.PageID {
	return storage.PageID(binary.LittleEndian.Uint32(sp.page[storage.OffPageID:]))
}

func (sp *SlottedPage) GetPageType() uint16 {
	return binary.LittleEndian.Uint16(sp.page[storage.OffPageType:])
}

func (sp *SlottedPage) SlotCount() uint16 {
	return binary.LittleEndian.Uint16(sp.page[offSlotCount:])
}

func (sp *SlottedPage) FreeSpace() uint16 {
	freeOffset := binary.LittleEndian.Uint16(sp.page[offFreeOffset:])
	freeEnd := binary.LittleEndian.Uint16(sp.page[offFreeEnd:])
	if freeEnd <= freeOffset {
		return 0
	}
	return freeEnd - freeOffset
}

func (sp *SlottedPage) GetNextPage() storage.PageID {
	return storage.PageID(binary.LittleEndian.Uint32(sp.page[offNextPage:]))
}

func (sp *SlottedPage) SetNextPage(id storage.PageID) {
	binary.LittleEndian.PutUint32(sp.page[offNextPage:], uint32(id))
}

// --- Record operations ---

// InsertRecord adds a record to the page. Returns the slot index on success.
func (sp *SlottedPage) InsertRecord(data []byte) (uint16, error) {
	needed := uint16(len(data)) + SlotEntrySize
	if sp.FreeSpace() < needed {
		return 0, errors.New("not enough free space in page")
	}

	slotCount := sp.SlotCount()
	freeEnd := binary.LittleEndian.Uint16(sp.page[offFreeEnd:])

	recordOffset := freeEnd - uint16(len(data))
	copy(sp.page[recordOffset:], data)

	slotPos := HeaderSize + int(slotCount)*SlotEntrySize
	binary.LittleEndian.PutUint16(sp.page[slotPos:], recordOffset)
	binary.LittleEndian.PutUint16(sp.page[slotPos+2:], uint16(len(data)))

	binary.LittleEndian.PutUint16(sp.page[offSlotCount:], slotCount+1)
	binary.LittleEndian.PutUint16(sp.page[offFreeOffset:], uint16(slotPos+SlotEntrySize))
	binary.LittleEndian.PutUint16(sp.page[offFreeEnd:], recordOffset)

	return slotCount, nil
}

// GetRecord returns the record data at the given slot index.
func (sp *SlottedPage) GetRecord(slot uint16) ([]byte, error) {
	if slot >= sp.SlotCount() {
		return nil, fmt.Errorf("slot %d out of range (have %d slots)", slot, sp.SlotCount())
	}

	offset, length := sp.slotEntry(slot)
	if offset == tombstoneOffset {
		return nil, nil
	}

	data := make([]byte, length)
	copy(data, sp.page[offset:offset+length])
	return data, nil
}

// UpdateRecord replaces the record at the given slot with new data.
func (sp *SlottedPage) UpdateRecord(slot uint16, data []byte) error {
	if slot >= sp.SlotCount() {
		return fmt.Errorf("slot %d out of range", slot)
	}

	oldOffset, oldLength := sp.slotEntry(slot)
	if oldOffset == tombstoneOffset {
		return fmt.Errorf("slot %d is deleted", slot)
	}

	if uint16(len(data)) <= oldLength {
		copy(sp.page[oldOffset:], data)
		sp.setSlotEntry(slot, oldOffset, uint16(len(data)))
		return nil
	}

	freeEnd := binary.LittleEndian.Uint16(sp.page[offFreeEnd:])
	freeOffset := binary.LittleEndian.Uint16(sp.page[offFreeOffset:])
	available := freeEnd - freeOffset
	if uint16(len(data)) > available {
		return errors.New("not enough free space for updated record")
	}

	newOffset := freeEnd - uint16(len(data))
	copy(sp.page[newOffset:], data)
	sp.setSlotEntry(slot, newOffset, uint16(len(data)))
	binary.LittleEndian.PutUint16(sp.page[offFreeEnd:], newOffset)
	return nil
}

// DeleteRecord marks a slot as a tombstone.
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

// Compact defragments the page by repacking all live records.
func (sp *SlottedPage) Compact() {
	slotCount := sp.SlotCount()
	writePos := uint16(storage.PageSize)

	for i := uint16(0); i < slotCount; i++ {
		offset, length := sp.slotEntry(i)
		if offset == tombstoneOffset || length == 0 {
			continue
		}

		newOffset := writePos - length
		if newOffset != offset {
			copy(sp.page[newOffset:newOffset+length], sp.page[offset:offset+length])
			sp.setSlotEntry(i, newOffset, length)
		}
		writePos = newOffset
	}

	binary.LittleEndian.PutUint16(sp.page[offFreeEnd:], writePos)
}

// --- Internal helpers ---

func (sp *SlottedPage) slotEntry(slot uint16) (offset, length uint16) {
	pos := HeaderSize + int(slot)*SlotEntrySize
	offset = binary.LittleEndian.Uint16(sp.page[pos:])
	length = binary.LittleEndian.Uint16(sp.page[pos+2:])
	return
}

func (sp *SlottedPage) setSlotEntry(slot uint16, offset, length uint16) {
	pos := HeaderSize + int(slot)*SlotEntrySize
	binary.LittleEndian.PutUint16(sp.page[pos:], offset)
	binary.LittleEndian.PutUint16(sp.page[pos+2:], length)
}
