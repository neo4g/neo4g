package storage

// btree.go implements a disk-resident B+Tree index stored in buffer pool pages.
//
// B+Tree properties:
//   - All values (RIDs) are stored in leaf nodes only.
//   - Internal nodes store keys and child page pointers for routing.
//   - Leaf nodes are linked (next-leaf pointer) for efficient range scans.
//   - The tree is balanced: all leaves are at the same depth.
//
// Page layout for B+Tree nodes:
//
//	Internal node:
//	  [Header: pageID(4), pageType(2), keyCount(2), padding(12)]
//	  [Child0(4)][Key0(8)][Child1(4)][Key1(8)]...[ChildN(4)]
//	  Children = keyCount + 1
//
//	Leaf node:
//	  [Header: pageID(4), pageType(2), keyCount(2), nextLeaf(4), padding(8)]
//	  [Key0(8)][PageID0(4)][Slot0(2)][Key1(8)][PageID1(4)][Slot1(2)]...
//
// Keys are uint64 (NodeID or EdgeID). Values in leaves are RIDs (PageID + Slot).
// This fixed-size key/value layout avoids the complexity of variable-size keys
// in the B+Tree itself. For secondary (property) indexes, we use a separate
// composite key encoding.
//
// Why B+Tree:
//   - Proven for disk-based indexes with predictable I/O (O(log_B N) per lookup)
//   - Leaf linking enables efficient range scans (adjacency traversal)
//   - Better than LSM for read-heavy graph workloads (no compaction overhead)
//   - Fan-out of ~300+ keys per 8KB page keeps tree height ≤ 3-4 for millions of entries

import (
	"encoding/binary"
	"fmt"
)

// B+Tree page layout constants.
const (
	btreeHeaderSize = 20 // matches slotted page header size for consistency

	// Internal node: after header, stores [child0][key0][child1][key1]...[childN]
	// Each key is 8 bytes (uint64), each child pointer is 4 bytes (PageID).
	btreeKeySize      = 8
	btreeChildSize    = 4
	btreeInternalSlot = btreeKeySize + btreeChildSize // key + right child

	// Leaf node: after header, stores [key][pageID][slot] entries.
	// Key=8, PageID=4, Slot=2 → 14 bytes per entry.
	btreeLeafEntrySize = 8 + 4 + 2 // key + RID

	// Maximum keys per node.
	btreeMaxInternalKeys = (PageSize - btreeHeaderSize - btreeChildSize) / btreeInternalSlot
	btreeMaxLeafKeys     = (PageSize - btreeHeaderSize) / btreeLeafEntrySize

	// Offsets within the header for B+Tree-specific fields.
	btreeOffKeyCount = 6  // uint16
	btreeOffNextLeaf = 8  // uint32 (leaf only)
)

// BTree is a disk-resident B+Tree index.
// It maps uint64 keys to RIDs using buffer pool pages.
type BTree struct {
	pool   *BufferPool
	disk   *DiskManager
	rootID PageID
}

// NewBTree creates a new B+Tree with an empty leaf root page.
func NewBTree(pool *BufferPool, disk *DiskManager) (*BTree, error) {
	rootID, rootPage, err := pool.NewPage()
	if err != nil {
		return nil, fmt.Errorf("allocate btree root: %w", err)
	}

	// Initialize as empty leaf.
	initBTreeLeaf(rootPage, rootID)
	pool.UnpinPage(rootID, true)

	return &BTree{pool: pool, disk: disk, rootID: rootID}, nil
}

// LoadBTree loads an existing B+Tree given its root page ID.
func LoadBTree(pool *BufferPool, disk *DiskManager, rootID PageID) *BTree {
	return &BTree{pool: pool, disk: disk, rootID: rootID}
}

// RootPageID returns the page ID of the B+Tree root, for persistence.
func (bt *BTree) RootPageID() PageID {
	return bt.rootID
}

// Search finds the RID associated with the given key.
// Returns InvalidRID and false if the key is not found.
func (bt *BTree) Search(key uint64) (RID, bool) {
	pageID := bt.rootID

	for {
		page, err := bt.pool.FetchPage(pageID)
		if err != nil {
			return InvalidRID, false
		}

		pageType := binary.LittleEndian.Uint16(page[pOffPageType:])

		if pageType == PageTypeBTreeLf {
			// Leaf — search for key.
			rid, found := bt.searchLeaf(page, key)
			bt.pool.UnpinPage(pageID, false)
			return rid, found
		}

		// Internal — find child to follow.
		childID := bt.findChild(page, key)
		bt.pool.UnpinPage(pageID, false)
		pageID = childID
	}
}

// Insert adds a key-RID pair to the B+Tree.
// If the key already exists, the RID is updated.
func (bt *BTree) Insert(key uint64, rid RID) error {
	// insertRecursive returns a split key and new page ID if the child split.
	splitKey, splitPageID, err := bt.insertRecursive(bt.rootID, key, rid)
	if err != nil {
		return err
	}

	if splitPageID != InvalidPageID {
		// Root split — create new root.
		newRootID, newRoot, err := bt.pool.NewPage()
		if err != nil {
			return fmt.Errorf("allocate new root: %w", err)
		}
		initBTreeInternal(newRoot, newRootID)

		// New root has one key and two children: old root and split page.
		binary.LittleEndian.PutUint16(newRoot[btreeOffKeyCount:], 1)
		off := btreeHeaderSize
		binary.LittleEndian.PutUint32(newRoot[off:], uint32(bt.rootID)) // left child
		off += btreeChildSize
		binary.LittleEndian.PutUint64(newRoot[off:], splitKey) // key
		off += btreeKeySize
		binary.LittleEndian.PutUint32(newRoot[off:], uint32(splitPageID)) // right child

		bt.pool.UnpinPage(newRootID, true)
		bt.rootID = newRootID
	}

	return nil
}

// Delete removes a key from the B+Tree. Returns false if the key was not found.
// This is a simplified delete that does not rebalance/merge underfull nodes —
// sufficient for the target scale where deletes are relatively infrequent.
func (bt *BTree) Delete(key uint64) bool {
	return bt.deleteFromLeaf(bt.rootID, key)
}

// RangeScan returns all RIDs with keys in [startKey, endKey] (inclusive).
// Results are returned in key order. Useful for adjacency index range scans.
func (bt *BTree) RangeScan(startKey, endKey uint64) []RID {
	// Find the leaf containing startKey.
	pageID := bt.rootID
	for {
		page, err := bt.pool.FetchPage(pageID)
		if err != nil {
			return nil
		}
		pageType := binary.LittleEndian.Uint16(page[pOffPageType:])
		if pageType == PageTypeBTreeLf {
			bt.pool.UnpinPage(pageID, false)
			break
		}
		childID := bt.findChild(page, startKey)
		bt.pool.UnpinPage(pageID, false)
		pageID = childID
	}

	// Scan leaf chain.
	var results []RID
	for pageID != InvalidPageID {
		page, err := bt.pool.FetchPage(pageID)
		if err != nil {
			break
		}

		keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))
		nextLeaf := PageID(binary.LittleEndian.Uint32(page[btreeOffNextLeaf:]))

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

func initBTreeInternal(p *Page, id PageID) {
	binary.LittleEndian.PutUint32(p[pOffPageID:], uint32(id))
	binary.LittleEndian.PutUint16(p[pOffPageType:], PageTypeBTreeInt)
	binary.LittleEndian.PutUint16(p[btreeOffKeyCount:], 0)
}

func initBTreeLeaf(p *Page, id PageID) {
	binary.LittleEndian.PutUint32(p[pOffPageID:], uint32(id))
	binary.LittleEndian.PutUint16(p[pOffPageType:], PageTypeBTreeLf)
	binary.LittleEndian.PutUint16(p[btreeOffKeyCount:], 0)
	binary.LittleEndian.PutUint32(p[btreeOffNextLeaf:], uint32(InvalidPageID))
}

// findChild returns the child page ID to follow for the given key in an internal node.
func (bt *BTree) findChild(page *Page, key uint64) PageID {
	keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))
	off := btreeHeaderSize

	// Layout: [child0][key0][child1][key1]...[childN]
	for i := range keyCount {
		off += btreeChildSize // skip child_i
		k := binary.LittleEndian.Uint64(page[off:])
		if key < k {
			// Go to child_i (left of key_i).
			childOff := btreeHeaderSize + i*btreeInternalSlot
			return PageID(binary.LittleEndian.Uint32(page[childOff:]))
		}
		off += btreeKeySize
	}
	// key >= all keys, go to rightmost child.
	return PageID(binary.LittleEndian.Uint32(page[off:]))
}

// readInternalKey reads the i-th key from an internal node.
func (bt *BTree) readInternalKey(page *Page, i int) uint64 {
	off := btreeHeaderSize + i*btreeInternalSlot + btreeChildSize
	return binary.LittleEndian.Uint64(page[off:])
}

// --- Leaf node helpers ---

// searchLeaf does a linear scan for a key in a leaf page.
func (bt *BTree) searchLeaf(page *Page, key uint64) (RID, bool) {
	keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))

	for i := range keyCount {
		k, rid := bt.readLeafEntry(page, i)
		if k == key {
			return rid, true
		}
		if k > key {
			break // keys are sorted
		}
	}
	return InvalidRID, false
}

// readLeafEntry reads the i-th (key, RID) pair from a leaf page.
func (bt *BTree) readLeafEntry(page *Page, i int) (uint64, RID) {
	off := btreeHeaderSize + i*btreeLeafEntrySize
	key := binary.LittleEndian.Uint64(page[off:])
	pageID := PageID(binary.LittleEndian.Uint32(page[off+8:]))
	slot := binary.LittleEndian.Uint16(page[off+12:])
	return key, RID{PageID: pageID, Slot: slot}
}

// writeLeafEntry writes a (key, RID) pair at position i in a leaf page.
func (bt *BTree) writeLeafEntry(page *Page, i int, key uint64, rid RID) {
	off := btreeHeaderSize + i*btreeLeafEntrySize
	binary.LittleEndian.PutUint64(page[off:], key)
	binary.LittleEndian.PutUint32(page[off+8:], uint32(rid.PageID))
	binary.LittleEndian.PutUint16(page[off+12:], rid.Slot)
}

// --- Recursive insert ---

// insertRecursive inserts into the subtree rooted at pageID.
// Returns (splitKey, newPageID) if the node split, or (0, InvalidPageID) if not.
func (bt *BTree) insertRecursive(pageID PageID, key uint64, rid RID) (uint64, PageID, error) {
	page, err := bt.pool.FetchPage(pageID)
	if err != nil {
		return 0, InvalidPageID, err
	}

	pageType := binary.LittleEndian.Uint16(page[pOffPageType:])

	if pageType == PageTypeBTreeLf {
		return bt.insertIntoLeaf(pageID, page, key, rid)
	}

	// Internal node — find child and recurse.
	childID := bt.findChild(page, key)
	bt.pool.UnpinPage(pageID, false)

	splitKey, splitPageID, err := bt.insertRecursive(childID, key, rid)
	if err != nil {
		return 0, InvalidPageID, err
	}

	if splitPageID == InvalidPageID {
		return 0, InvalidPageID, nil // no split, done
	}

	// Child split — insert split key into this internal node.
	page, err = bt.pool.FetchPage(pageID)
	if err != nil {
		return 0, InvalidPageID, err
	}
	return bt.insertIntoInternal(pageID, page, splitKey, splitPageID)
}

// insertIntoLeaf inserts a key-RID into a leaf page. Splits if full.
// The page must be pinned by the caller. This function unpins it.
func (bt *BTree) insertIntoLeaf(pageID PageID, page *Page, key uint64, rid RID) (uint64, PageID, error) {
	keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))

	// Find insertion position (maintain sorted order).
	pos := keyCount
	for i := range keyCount {
		k, _ := bt.readLeafEntry(page, i)
		if k == key {
			// Key exists — update RID in place.
			bt.writeLeafEntry(page, i, key, rid)
			bt.pool.UnpinPage(pageID, true)
			return 0, InvalidPageID, nil
		}
		if k > key {
			pos = i
			break
		}
	}

	if keyCount < btreeMaxLeafKeys {
		// Room available — shift entries right and insert.
		for i := keyCount; i > pos; i-- {
			k, r := bt.readLeafEntry(page, i-1)
			bt.writeLeafEntry(page, i, k, r)
		}
		bt.writeLeafEntry(page, pos, key, rid)
		binary.LittleEndian.PutUint16(page[btreeOffKeyCount:], uint16(keyCount+1))
		bt.pool.UnpinPage(pageID, true)
		return 0, InvalidPageID, nil
	}

	// Leaf is full — split.
	return bt.splitLeaf(pageID, page, pos, key, rid)
}

// splitLeaf splits a full leaf page. The new entry (key, rid) to be inserted
// at position pos is included in the split calculation.
func (bt *BTree) splitLeaf(pageID PageID, page *Page, pos int, key uint64, rid RID) (uint64, PageID, error) {
	keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))

	// Collect all entries including the new one.
	type entry struct {
		key uint64
		rid RID
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

	// Split at midpoint.
	mid := len(all) / 2

	// Rewrite current page with left half.
	binary.LittleEndian.PutUint16(page[btreeOffKeyCount:], uint16(mid))
	for i := range mid {
		bt.writeLeafEntry(page, i, all[i].key, all[i].rid)
	}
	// Clear old entries beyond mid.
	for i := mid; i < keyCount; i++ {
		bt.writeLeafEntry(page, i, 0, InvalidRID)
	}

	// Create new leaf with right half.
	newID, newPage, err := bt.pool.NewPage()
	if err != nil {
		bt.pool.UnpinPage(pageID, true)
		return 0, InvalidPageID, err
	}
	initBTreeLeaf(newPage, newID)

	rightCount := len(all) - mid
	binary.LittleEndian.PutUint16(newPage[btreeOffKeyCount:], uint16(rightCount))
	for i := range rightCount {
		bt.writeLeafEntry(newPage, i, all[mid+i].key, all[mid+i].rid)
	}

	// Link leaves: old.next → new, new.next → old.next
	oldNext := PageID(binary.LittleEndian.Uint32(page[btreeOffNextLeaf:]))
	binary.LittleEndian.PutUint32(page[btreeOffNextLeaf:], uint32(newID))
	binary.LittleEndian.PutUint32(newPage[btreeOffNextLeaf:], uint32(oldNext))

	bt.pool.UnpinPage(pageID, true)
	bt.pool.UnpinPage(newID, true)

	// Split key = first key in the new (right) leaf.
	splitKey := all[mid].key
	return splitKey, newID, nil
}

// insertIntoInternal inserts a split key + right child into an internal node.
// The page must be pinned. This function unpins it.
func (bt *BTree) insertIntoInternal(pageID PageID, page *Page, splitKey uint64, rightChild PageID) (uint64, PageID, error) {
	keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))

	// Find insertion position.
	pos := keyCount
	for i := range keyCount {
		k := bt.readInternalKey(page, i)
		if splitKey < k {
			pos = i
			break
		}
	}

	if keyCount < btreeMaxInternalKeys {
		// Room available — shift right and insert.
		// Layout: [c0][k0][c1][k1]...[cN]
		// Inserting at pos means: insert key at pos, right child after it.

		// Shift from end.
		for i := keyCount; i > pos; i-- {
			// Copy key[i-1] → key[i]
			srcOff := btreeHeaderSize + (i-1)*btreeInternalSlot + btreeChildSize
			dstOff := btreeHeaderSize + i*btreeInternalSlot + btreeChildSize
			copy(page[dstOff:dstOff+btreeKeySize], page[srcOff:srcOff+btreeKeySize])

			// Copy child[i] → child[i+1]
			srcChild := btreeHeaderSize + i*btreeInternalSlot
			dstChild := btreeHeaderSize + (i+1)*btreeInternalSlot
			copy(page[dstChild:dstChild+btreeChildSize], page[srcChild:srcChild+btreeChildSize])
		}

		// Write new key at pos.
		keyOff := btreeHeaderSize + pos*btreeInternalSlot + btreeChildSize
		binary.LittleEndian.PutUint64(page[keyOff:], splitKey)

		// Write new right child at pos+1.
		childOff := btreeHeaderSize + (pos+1)*btreeInternalSlot
		binary.LittleEndian.PutUint32(page[childOff:], uint32(rightChild))

		binary.LittleEndian.PutUint16(page[btreeOffKeyCount:], uint16(keyCount+1))
		bt.pool.UnpinPage(pageID, true)
		return 0, InvalidPageID, nil
	}

	// Internal node full — split it.
	return bt.splitInternal(pageID, page, pos, splitKey, rightChild)
}

// splitInternal splits a full internal node.
func (bt *BTree) splitInternal(pageID PageID, page *Page, pos int, newKey uint64, newChild PageID) (uint64, PageID, error) {
	keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))

	// Collect all keys and children, including the new entry.
	type kc struct {
		key   uint64
		child PageID // right child of this key
	}
	allKeys := make([]kc, 0, keyCount+1)
	firstChild := PageID(binary.LittleEndian.Uint32(page[btreeHeaderSize:]))

	for i := range keyCount {
		k := bt.readInternalKey(page, i)
		cOff := btreeHeaderSize + (i+1)*btreeInternalSlot
		c := PageID(binary.LittleEndian.Uint32(page[cOff:]))
		if i == pos {
			allKeys = append(allKeys, kc{newKey, newChild})
		}
		allKeys = append(allKeys, kc{k, c})
	}
	if pos == keyCount {
		allKeys = append(allKeys, kc{newKey, newChild})
	}

	// Split: left gets [0, mid), promote key[mid], right gets [mid+1, end).
	mid := len(allKeys) / 2
	promoteKey := allKeys[mid].key

	// Rewrite current page as left node.
	*page = Page{} // clear
	initBTreeInternal(page, pageID)
	binary.LittleEndian.PutUint16(page[btreeOffKeyCount:], uint16(mid))
	binary.LittleEndian.PutUint32(page[btreeHeaderSize:], uint32(firstChild))
	for i := range mid {
		keyOff := btreeHeaderSize + i*btreeInternalSlot + btreeChildSize
		binary.LittleEndian.PutUint64(page[keyOff:], allKeys[i].key)
		childOff := btreeHeaderSize + (i+1)*btreeInternalSlot
		binary.LittleEndian.PutUint32(page[childOff:], uint32(allKeys[i].child))
	}

	// Create new right internal node.
	newID, newPage, err := bt.pool.NewPage()
	if err != nil {
		bt.pool.UnpinPage(pageID, true)
		return 0, InvalidPageID, err
	}
	initBTreeInternal(newPage, newID)

	rightKeys := allKeys[mid+1:]
	binary.LittleEndian.PutUint16(newPage[btreeOffKeyCount:], uint16(len(rightKeys)))
	// First child of right node = right child of promoted key.
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

// deleteFromLeaf traverses to the leaf containing key and removes it.
func (bt *BTree) deleteFromLeaf(pageID PageID, key uint64) bool {
	page, err := bt.pool.FetchPage(pageID)
	if err != nil {
		return false
	}

	pageType := binary.LittleEndian.Uint16(page[pOffPageType:])

	if pageType == PageTypeBTreeLf {
		keyCount := int(binary.LittleEndian.Uint16(page[btreeOffKeyCount:]))
		for i := range keyCount {
			k, _ := bt.readLeafEntry(page, i)
			if k == key {
				// Shift entries left to remove.
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

	// Internal — find child and recurse.
	childID := bt.findChild(page, key)
	bt.pool.UnpinPage(pageID, false)
	return bt.deleteFromLeaf(childID, key)
}
