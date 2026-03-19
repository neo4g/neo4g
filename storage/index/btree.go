// Package index implements B+Tree indexes and the Index Manager for fast
// lookups in the record manager.
package index

import (
	"encoding/binary"
	"fmt"

	"github.com/seankohjs/neo4g/storage"
	"github.com/seankohjs/neo4g/storage/disk"
	"github.com/seankohjs/neo4g/storage/pool"
)

// B+Tree page layout constants.
const (
	btreeHeaderSize = 20

	btreeKeySize      = 8
	btreeChildSize    = 4
	btreeInternalSlot = btreeKeySize + btreeChildSize

	btreeLeafEntrySize = 8 + 4 + 2 // key + RID

	btreeMaxInternalKeys = (storage.PageSize - btreeHeaderSize - btreeChildSize) / btreeInternalSlot
	btreeMaxLeafKeys     = (storage.PageSize - btreeHeaderSize) / btreeLeafEntrySize

	btreeOffKeyCount = 6
	btreeOffNextLeaf = 8
)

// BTree is a disk-resident B+Tree index.
type BTree struct {
	pool   *pool.Pool
	disk   *disk.Manager
	rootID storage.PageID
}

// NewBTree creates a new B+Tree with an empty leaf root page.
func NewBTree(p *pool.Pool, dm *disk.Manager) (*BTree, error) {
	rootID, rootPage, err := p.NewPage()
	if err != nil {
		return nil, fmt.Errorf("allocate btree root: %w", err)
	}

	initBTreeLeaf(rootPage, rootID)
	p.UnpinPage(rootID, true)

	return &BTree{pool: p, disk: dm, rootID: rootID}, nil
}

// LoadBTree loads an existing B+Tree given its root page ID.
func LoadBTree(p *pool.Pool, dm *disk.Manager, rootID storage.PageID) *BTree {
	return &BTree{pool: p, disk: dm, rootID: rootID}
}

// RootPageID returns the page ID of the B+Tree root.
func (bt *BTree) RootPageID() storage.PageID {
	return bt.rootID
}

// Search finds the RID associated with the given key.
func (bt *BTree) Search(key uint64) (storage.RID, bool) {
	pageID := bt.rootID

	for {
		page, err := bt.pool.FetchPage(pageID)
		if err != nil {
			return storage.InvalidRID, false
		}

		pageType := binary.LittleEndian.Uint16(page[storage.OffPageType:])

		if pageType == storage.PageTypeBTreeLf {
			rid, found := bt.searchLeaf(page, key)
			bt.pool.UnpinPage(pageID, false)
			return rid, found
		}

		childID := bt.findChild(page, key)
		bt.pool.UnpinPage(pageID, false)
		pageID = childID
	}
}

// Insert adds a key-RID pair to the B+Tree. If the key exists, the RID is updated.
func (bt *BTree) Insert(key uint64, rid storage.RID) error {
	splitKey, splitPageID, err := bt.insertRecursive(bt.rootID, key, rid)
	if err != nil {
		return err
	}

	if splitPageID != storage.InvalidPageID {
		newRootID, newRoot, err := bt.pool.NewPage()
		if err != nil {
			return fmt.Errorf("allocate new root: %w", err)
		}
		initBTreeInternal(newRoot, newRootID)

		binary.LittleEndian.PutUint16(newRoot[btreeOffKeyCount:], 1)
		off := btreeHeaderSize
		binary.LittleEndian.PutUint32(newRoot[off:], uint32(bt.rootID))
		off += btreeChildSize
		binary.LittleEndian.PutUint64(newRoot[off:], splitKey)
		off += btreeKeySize
		binary.LittleEndian.PutUint32(newRoot[off:], uint32(splitPageID))

		bt.pool.UnpinPage(newRootID, true)
		bt.rootID = newRootID
	}

	return nil
}

// Delete removes a key from the B+Tree. Returns false if the key was not found.
func (bt *BTree) Delete(key uint64) bool {
	return bt.deleteFromLeaf(bt.rootID, key)
}

// RangeScan returns all RIDs with keys in [startKey, endKey] (inclusive).
func (bt *BTree) RangeScan(startKey, endKey uint64) []storage.RID {
	pageID := bt.rootID
	for {
		page, err := bt.pool.FetchPage(pageID)
		if err != nil {
			return nil
		}
		pageType := binary.LittleEndian.Uint16(page[storage.OffPageType:])
		if pageType == storage.PageTypeBTreeLf {
			bt.pool.UnpinPage(pageID, false)
			break
		}
		childID := bt.findChild(page, startKey)
		bt.pool.UnpinPage(pageID, false)
		pageID = childID
	}

	var results []storage.RID
	for pageID != storage.InvalidPageID {
		page, err := bt.pool.FetchPage(pageID)
		if err != nil {
			break
		}

		keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))
		nextLeaf := storage.PageID(binary.LittleEndian.Uint32(page[btreeOffNextLeaf:]))

		for i := range keyCount {
			k, r := bt.readLeafEntry(page, i)
			if k > endKey {
				bt.pool.UnpinPage(pageID, false)
				return results
			}
			if k >= startKey {
				results = append(results, r)
			}
		}

		bt.pool.UnpinPage(pageID, false)
		pageID = nextLeaf
	}
	return results
}

// --- Internal node helpers ---

func initBTreeInternal(p *storage.Page, id storage.PageID) {
	binary.LittleEndian.PutUint32(p[storage.OffPageID:], uint32(id))
	binary.LittleEndian.PutUint16(p[storage.OffPageType:], storage.PageTypeBTreeInt)
	binary.LittleEndian.PutUint16(p[btreeOffKeyCount:], 0)
}

func initBTreeLeaf(p *storage.Page, id storage.PageID) {
	binary.LittleEndian.PutUint32(p[storage.OffPageID:], uint32(id))
	binary.LittleEndian.PutUint16(p[storage.OffPageType:], storage.PageTypeBTreeLf)
	binary.LittleEndian.PutUint16(p[btreeOffKeyCount:], 0)
	binary.LittleEndian.PutUint32(p[btreeOffNextLeaf:], uint32(storage.InvalidPageID))
}

func (bt *BTree) findChild(page *storage.Page, key uint64) storage.PageID {
	keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))
	off := btreeHeaderSize

	for i := range keyCount {
		off += btreeChildSize
		k := binary.LittleEndian.Uint64(page[off:])
		if key < k {
			childOff := btreeHeaderSize + i*btreeInternalSlot
			return storage.PageID(binary.LittleEndian.Uint32(page[childOff:]))
		}
		off += btreeKeySize
	}
	return storage.PageID(binary.LittleEndian.Uint32(page[off:]))
}

func (bt *BTree) readInternalKey(page *storage.Page, i int) uint64 {
	off := btreeHeaderSize + i*btreeInternalSlot + btreeChildSize
	return binary.LittleEndian.Uint64(page[off:])
}

// --- Leaf node helpers ---

func (bt *BTree) searchLeaf(page *storage.Page, key uint64) (storage.RID, bool) {
	keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))

	for i := range keyCount {
		k, rid := bt.readLeafEntry(page, i)
		if k == key {
			return rid, true
		}
		if k > key {
			break
		}
	}
	return storage.InvalidRID, false
}

func (bt *BTree) readLeafEntry(page *storage.Page, i int) (uint64, storage.RID) {
	off := btreeHeaderSize + i*btreeLeafEntrySize
	key := binary.LittleEndian.Uint64(page[off:])
	pageID := storage.PageID(binary.LittleEndian.Uint32(page[off+8:]))
	slot := binary.LittleEndian.Uint16(page[off+12:])
	return key, storage.RID{PageID: pageID, Slot: slot}
}

func (bt *BTree) writeLeafEntry(page *storage.Page, i int, key uint64, rid storage.RID) {
	off := btreeHeaderSize + i*btreeLeafEntrySize
	binary.LittleEndian.PutUint64(page[off:], key)
	binary.LittleEndian.PutUint32(page[off+8:], uint32(rid.PageID))
	binary.LittleEndian.PutUint16(page[off+12:], rid.Slot)
}

// --- Recursive insert ---

func (bt *BTree) insertRecursive(pageID storage.PageID, key uint64, rid storage.RID) (uint64, storage.PageID, error) {
	page, err := bt.pool.FetchPage(pageID)
	if err != nil {
		return 0, storage.InvalidPageID, err
	}

	pageType := binary.LittleEndian.Uint16(page[storage.OffPageType:])

	if pageType == storage.PageTypeBTreeLf {
		return bt.insertIntoLeaf(pageID, page, key, rid)
	}

	childID := bt.findChild(page, key)
	bt.pool.UnpinPage(pageID, false)

	splitKey, splitPageID, err := bt.insertRecursive(childID, key, rid)
	if err != nil {
		return 0, storage.InvalidPageID, err
	}

	if splitPageID == storage.InvalidPageID {
		return 0, storage.InvalidPageID, nil
	}

	page, err = bt.pool.FetchPage(pageID)
	if err != nil {
		return 0, storage.InvalidPageID, err
	}
	return bt.insertIntoInternal(pageID, page, splitKey, splitPageID)
}

func (bt *BTree) insertIntoLeaf(pageID storage.PageID, page *storage.Page, key uint64, rid storage.RID) (uint64, storage.PageID, error) {
	keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))

	pos := keyCount
	for i := range keyCount {
		k, _ := bt.readLeafEntry(page, i)
		if k == key {
			bt.writeLeafEntry(page, i, key, rid)
			bt.pool.UnpinPage(pageID, true)
			return 0, storage.InvalidPageID, nil
		}
		if k > key {
			pos = i
			break
		}
	}

	if keyCount < btreeMaxLeafKeys {
		for i := keyCount; i > pos; i-- {
			k, r := bt.readLeafEntry(page, i-1)
			bt.writeLeafEntry(page, i, k, r)
		}
		bt.writeLeafEntry(page, pos, key, rid)
		binary.LittleEndian.PutUint16(page[btreeOffKeyCount:], uint16(keyCount+1))
		bt.pool.UnpinPage(pageID, true)
		return 0, storage.InvalidPageID, nil
	}

	return bt.splitLeaf(pageID, page, pos, key, rid)
}

func (bt *BTree) splitLeaf(pageID storage.PageID, page *storage.Page, pos int, key uint64, rid storage.RID) (uint64, storage.PageID, error) {
	keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))

	type entry struct {
		key uint64
		rid storage.RID
	}
	all := make([]entry, 0, keyCount+1)
	for i := range keyCount {
		k, r := bt.readLeafEntry(page, i)
		if i == pos {
			all = append(all, entry{key, rid})
		}
		all = append(all, entry{k, r})
	}
	if pos == keyCount {
		all = append(all, entry{key, rid})
	}

	mid := len(all) / 2

	binary.LittleEndian.PutUint16(page[btreeOffKeyCount:], uint16(mid))
	for i := range mid {
		bt.writeLeafEntry(page, i, all[i].key, all[i].rid)
	}
	for i := mid; i < keyCount; i++ {
		bt.writeLeafEntry(page, i, 0, storage.InvalidRID)
	}

	newID, newPage, err := bt.pool.NewPage()
	if err != nil {
		bt.pool.UnpinPage(pageID, true)
		return 0, storage.InvalidPageID, err
	}
	initBTreeLeaf(newPage, newID)

	rightCount := len(all) - mid
	binary.LittleEndian.PutUint16(newPage[btreeOffKeyCount:], uint16(rightCount))
	for i := range rightCount {
		bt.writeLeafEntry(newPage, i, all[mid+i].key, all[mid+i].rid)
	}

	oldNext := storage.PageID(binary.LittleEndian.Uint32(page[btreeOffNextLeaf:]))
	binary.LittleEndian.PutUint32(page[btreeOffNextLeaf:], uint32(newID))
	binary.LittleEndian.PutUint32(newPage[btreeOffNextLeaf:], uint32(oldNext))

	bt.pool.UnpinPage(pageID, true)
	bt.pool.UnpinPage(newID, true)

	splitKey := all[mid].key
	return splitKey, newID, nil
}

func (bt *BTree) insertIntoInternal(pageID storage.PageID, page *storage.Page, splitKey uint64, rightChild storage.PageID) (uint64, storage.PageID, error) {
	keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))

	pos := keyCount
	for i := range keyCount {
		k := bt.readInternalKey(page, i)
		if splitKey < k {
			pos = i
			break
		}
	}

	if keyCount < btreeMaxInternalKeys {
		for i := keyCount; i > pos; i-- {
			srcOff := btreeHeaderSize + (i-1)*btreeInternalSlot + btreeChildSize
			dstOff := btreeHeaderSize + i*btreeInternalSlot + btreeChildSize
			copy(page[dstOff:dstOff+btreeKeySize], page[srcOff:srcOff+btreeKeySize])

			srcChild := btreeHeaderSize + i*btreeInternalSlot
			dstChild := btreeHeaderSize + (i+1)*btreeInternalSlot
			copy(page[dstChild:dstChild+btreeChildSize], page[srcChild:srcChild+btreeChildSize])
		}

		keyOff := btreeHeaderSize + pos*btreeInternalSlot + btreeChildSize
		binary.LittleEndian.PutUint64(page[keyOff:], splitKey)

		childOff := btreeHeaderSize + (pos+1)*btreeInternalSlot
		binary.LittleEndian.PutUint32(page[childOff:], uint32(rightChild))

		binary.LittleEndian.PutUint16(page[btreeOffKeyCount:], uint16(keyCount+1))
		bt.pool.UnpinPage(pageID, true)
		return 0, storage.InvalidPageID, nil
	}

	return bt.splitInternal(pageID, page, pos, splitKey, rightChild)
}

func (bt *BTree) splitInternal(pageID storage.PageID, page *storage.Page, pos int, newKey uint64, newChild storage.PageID) (uint64, storage.PageID, error) {
	keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))

	type kc struct {
		key   uint64
		child storage.PageID
	}
	allKeys := make([]kc, 0, keyCount+1)
	firstChild := storage.PageID(binary.LittleEndian.Uint32(page[btreeHeaderSize:]))

	for i := range keyCount {
		k := bt.readInternalKey(page, i)
		cOff := btreeHeaderSize + (i+1)*btreeInternalSlot
		c := storage.PageID(binary.LittleEndian.Uint32(page[cOff:]))
		if i == pos {
			allKeys = append(allKeys, kc{newKey, newChild})
		}
		allKeys = append(allKeys, kc{k, c})
	}
	if pos == keyCount {
		allKeys = append(allKeys, kc{newKey, newChild})
	}

	mid := len(allKeys) / 2
	promoteKey := allKeys[mid].key

	*page = storage.Page{}
	initBTreeInternal(page, pageID)
	binary.LittleEndian.PutUint16(page[btreeOffKeyCount:], uint16(mid))
	binary.LittleEndian.PutUint32(page[btreeHeaderSize:], uint32(firstChild))
	for i := range mid {
		keyOff := btreeHeaderSize + i*btreeInternalSlot + btreeChildSize
		binary.LittleEndian.PutUint64(page[keyOff:], allKeys[i].key)
		childOff := btreeHeaderSize + (i+1)*btreeInternalSlot
		binary.LittleEndian.PutUint32(page[childOff:], uint32(allKeys[i].child))
	}

	newID, newPage, err := bt.pool.NewPage()
	if err != nil {
		bt.pool.UnpinPage(pageID, true)
		return 0, storage.InvalidPageID, err
	}
	initBTreeInternal(newPage, newID)

	rightKeys := allKeys[mid+1:]
	binary.LittleEndian.PutUint16(newPage[btreeOffKeyCount:], uint16(len(rightKeys)))
	binary.LittleEndian.PutUint32(newPage[btreeHeaderSize:], uint32(allKeys[mid].child))
	for i, kc := range rightKeys {
		keyOff := btreeHeaderSize + i*btreeInternalSlot + btreeChildSize
		binary.LittleEndian.PutUint64(newPage[keyOff:], kc.key)
		childOff := btreeHeaderSize + (i+1)*btreeInternalSlot
		binary.LittleEndian.PutUint32(newPage[childOff:], uint32(kc.child))
	}

	bt.pool.UnpinPage(pageID, true)
	bt.pool.UnpinPage(newID, true)

	return promoteKey, newID, nil
}

// --- Delete ---

func (bt *BTree) deleteFromLeaf(pageID storage.PageID, key uint64) bool {
	page, err := bt.pool.FetchPage(pageID)
	if err != nil {
		return false
	}

	pageType := binary.LittleEndian.Uint16(page[storage.OffPageType:])

	if pageType == storage.PageTypeBTreeLf {
		keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))
		for i := range keyCount {
			k, _ := bt.readLeafEntry(page, i)
			if k == key {
				for j := i; j < keyCount-1; j++ {
					nk, nr := bt.readLeafEntry(page, j+1)
					bt.writeLeafEntry(page, j, nk, nr)
				}
				binary.LittleEndian.PutUint16(page[btreeOffKeyCount:], uint16(keyCount-1))
				bt.pool.UnpinPage(pageID, true)
				return true
			}
			if k > key {
				break
			}
		}
		bt.pool.UnpinPage(pageID, false)
		return false
	}

	childID := bt.findChild(page, key)
	bt.pool.UnpinPage(pageID, false)
	return bt.deleteFromLeaf(childID, key)
}
