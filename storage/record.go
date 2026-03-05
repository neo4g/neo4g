package storage

// record.go implements the Record Manager — the CRUD interface for nodes and
// edges in the property graph.
//
// The Record Manager sits on top of the buffer pool and slotted page layers.
// It manages:
//   - Page chains: separate linked lists of pages for nodes and edges.
//     When a page is full, a new page is allocated and linked to the chain.
//   - ID-to-RID mapping: an in-memory map from NodeID/EdgeID to RID (the
//     physical location in a slotted page). This will be replaced by B+Tree
//     indexes in Layer 5.
//   - Adjacency tracking: an in-memory map from NodeID to connected EdgeIDs.
//     This will also be replaced by the B+Tree adjacency index.
//
// This is the primary API surface for the query layer. The interface is
// designed so that the in-memory maps can be transparently replaced by
// disk-resident B+Trees without changing callers.

import (
	"fmt"
	"sync"
)

// RecordManager provides CRUD operations for nodes and edges.
//
// Thread safety: RecordManager is safe for concurrent use.
type RecordManager struct {
	mu   sync.Mutex
	pool *BufferPool
	disk *DiskManager

	// Page chain heads — the first page in each chain.
	nodePageHead PageID
	edgePageHead PageID

	// Current page being inserted into (tail of chain).
	nodePageTail PageID
	edgePageTail PageID

	// In-memory indexes (replaced by B+Tree in Layer 5).
	nodeIndex map[NodeID]RID
	edgeIndex map[EdgeID]RID

	// Adjacency: nodeID → list of edge IDs connected to it.
	// Stored as both outgoing and incoming for fast bidirectional lookup.
	outEdges map[NodeID][]EdgeID
	inEdges  map[NodeID][]EdgeID
}

// NewRecordManager creates a RecordManager backed by the given buffer pool
// and disk manager. It initializes page chains for nodes and edges.
func NewRecordManager(pool *BufferPool, disk *DiskManager) (*RecordManager, error) {
	rm := &RecordManager{
		pool:      pool,
		disk:      disk,
		nodeIndex: make(map[NodeID]RID),
		edgeIndex: make(map[EdgeID]RID),
		outEdges:  make(map[NodeID][]EdgeID),
		inEdges:   make(map[NodeID][]EdgeID),
	}

	// Allocate initial pages for node and edge chains.
	nodeID, nodePage, err := pool.NewPage()
	if err != nil {
		return nil, fmt.Errorf("allocate node page: %w", err)
	}
	InitSlottedPage(nodePage, nodeID, PageTypeNode)
	pool.UnpinPage(nodeID, true)
	rm.nodePageHead = nodeID
	rm.nodePageTail = nodeID

	edgeID, edgePage, err := pool.NewPage()
	if err != nil {
		return nil, fmt.Errorf("allocate edge page: %w", err)
	}
	InitSlottedPage(edgePage, edgeID, PageTypeEdge)
	pool.UnpinPage(edgeID, true)
	rm.edgePageHead = edgeID
	rm.edgePageTail = edgeID

	return rm, nil
}

// --- Node operations ---

// CreateNode creates a new node with the given labels and properties.
// Returns the assigned NodeID.
func (rm *RecordManager) CreateNode(labels []string, props map[string]Value) (NodeID, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	id, err := rm.disk.NextNodeID()
	if err != nil {
		return 0, fmt.Errorf("allocate node ID: %w", err)
	}

	node := &Node{
		ID:         NodeID(id),
		Labels:     labels,
		Properties: props,
	}

	data := EncodeNode(node)
	rid, err := rm.insertRecord(data, rm.nodePageTail, PageTypeNode, &rm.nodePageTail)
	if err != nil {
		return 0, fmt.Errorf("insert node record: %w", err)
	}

	rm.nodeIndex[node.ID] = rid
	return node.ID, nil
}

// GetNode retrieves a node by its ID. Returns an error if not found.
func (rm *RecordManager) GetNode(id NodeID) (*Node, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rid, ok := rm.nodeIndex[id]
	if !ok {
		return nil, fmt.Errorf("node %d not found", id)
	}

	data, err := rm.readRecord(rid)
	if err != nil {
		return nil, fmt.Errorf("read node %d: %w", id, err)
	}

	return DecodeNode(data)
}

// UpdateNode updates the properties of an existing node. Labels are not
// changed — use UpdateNodeLabels for that.
func (rm *RecordManager) UpdateNode(id NodeID, props map[string]Value) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rid, ok := rm.nodeIndex[id]
	if !ok {
		return fmt.Errorf("node %d not found", id)
	}

	// Read current node to preserve labels.
	data, err := rm.readRecord(rid)
	if err != nil {
		return fmt.Errorf("read node %d for update: %w", id, err)
	}
	node, err := DecodeNode(data)
	if err != nil {
		return fmt.Errorf("decode node %d for update: %w", id, err)
	}

	// Merge properties.
	for k, v := range props {
		node.Properties[k] = v
	}

	newData := EncodeNode(node)

	// Try in-place update.
	page, err := rm.pool.FetchPage(rid.PageID)
	if err != nil {
		return fmt.Errorf("fetch page for update: %w", err)
	}

	sp := NewSlottedPage(page)
	err = sp.UpdateRecord(rid.Slot, newData)
	if err == nil {
		rm.pool.UnpinPage(rid.PageID, true)
		return nil
	}

	// Doesn't fit — delete old, insert new.
	sp.DeleteRecord(rid.Slot)
	rm.pool.UnpinPage(rid.PageID, true)

	newRID, err := rm.insertRecord(newData, rm.nodePageTail, PageTypeNode, &rm.nodePageTail)
	if err != nil {
		return fmt.Errorf("reinsert node %d: %w", id, err)
	}
	rm.nodeIndex[id] = newRID
	return nil
}

// DeleteNode removes a node by its ID. Returns an error if the node has
// connected edges (caller must delete edges first).
func (rm *RecordManager) DeleteNode(id NodeID) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if len(rm.outEdges[id]) > 0 || len(rm.inEdges[id]) > 0 {
		return fmt.Errorf("node %d has connected edges; delete edges first", id)
	}

	rid, ok := rm.nodeIndex[id]
	if !ok {
		return fmt.Errorf("node %d not found", id)
	}

	if err := rm.deleteRecord(rid); err != nil {
		return fmt.Errorf("delete node %d: %w", id, err)
	}

	delete(rm.nodeIndex, id)
	return nil
}

// --- Edge operations ---

// CreateEdge creates a new directed edge between two nodes.
// Returns the assigned EdgeID. Returns an error if source or target don't exist.
func (rm *RecordManager) CreateEdge(edgeType string, src, dst NodeID, props map[string]Value) (EdgeID, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Verify both endpoints exist.
	if _, ok := rm.nodeIndex[src]; !ok {
		return 0, fmt.Errorf("source node %d not found", src)
	}
	if _, ok := rm.nodeIndex[dst]; !ok {
		return 0, fmt.Errorf("target node %d not found", dst)
	}

	id, err := rm.disk.NextEdgeID()
	if err != nil {
		return 0, fmt.Errorf("allocate edge ID: %w", err)
	}

	edge := &Edge{
		ID:         EdgeID(id),
		Type:       edgeType,
		SourceID:   src,
		TargetID:   dst,
		Properties: props,
	}

	data := EncodeEdge(edge)
	rid, err := rm.insertRecord(data, rm.edgePageTail, PageTypeEdge, &rm.edgePageTail)
	if err != nil {
		return 0, fmt.Errorf("insert edge record: %w", err)
	}

	rm.edgeIndex[edge.ID] = rid
	rm.outEdges[src] = append(rm.outEdges[src], edge.ID)
	rm.inEdges[dst] = append(rm.inEdges[dst], edge.ID)
	return edge.ID, nil
}

// GetEdge retrieves an edge by its ID.
func (rm *RecordManager) GetEdge(id EdgeID) (*Edge, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rid, ok := rm.edgeIndex[id]
	if !ok {
		return nil, fmt.Errorf("edge %d not found", id)
	}

	data, err := rm.readRecord(rid)
	if err != nil {
		return nil, fmt.Errorf("read edge %d: %w", id, err)
	}

	return DecodeEdge(data)
}

// GetEdges returns all edges connected to a node in the specified direction.
func (rm *RecordManager) GetEdges(nodeID NodeID, dir Direction) ([]*Edge, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	var edgeIDs []EdgeID
	switch dir {
	case Outgoing:
		edgeIDs = rm.outEdges[nodeID]
	case Incoming:
		edgeIDs = rm.inEdges[nodeID]
	case Both:
		edgeIDs = append(edgeIDs, rm.outEdges[nodeID]...)
		edgeIDs = append(edgeIDs, rm.inEdges[nodeID]...)
	}

	edges := make([]*Edge, 0, len(edgeIDs))
	for _, eid := range edgeIDs {
		rid, ok := rm.edgeIndex[eid]
		if !ok {
			continue // edge was deleted
		}
		data, err := rm.readRecord(rid)
		if err != nil {
			return nil, fmt.Errorf("read edge %d: %w", eid, err)
		}
		edge, err := DecodeEdge(data)
		if err != nil {
			return nil, fmt.Errorf("decode edge %d: %w", eid, err)
		}
		edges = append(edges, edge)
	}
	return edges, nil
}

// DeleteEdge removes an edge by its ID.
func (rm *RecordManager) DeleteEdge(id EdgeID) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rid, ok := rm.edgeIndex[id]
	if !ok {
		return fmt.Errorf("edge %d not found", id)
	}

	// Read edge to get source/target for adjacency cleanup.
	data, err := rm.readRecord(rid)
	if err != nil {
		return fmt.Errorf("read edge %d for delete: %w", id, err)
	}
	edge, err := DecodeEdge(data)
	if err != nil {
		return fmt.Errorf("decode edge %d for delete: %w", id, err)
	}

	if err := rm.deleteRecord(rid); err != nil {
		return fmt.Errorf("delete edge %d: %w", id, err)
	}

	delete(rm.edgeIndex, id)
	rm.outEdges[edge.SourceID] = removeEdgeID(rm.outEdges[edge.SourceID], id)
	rm.inEdges[edge.TargetID] = removeEdgeID(rm.inEdges[edge.TargetID], id)
	return nil
}

// --- Internal helpers ---

// insertRecord inserts encoded data into the page chain, allocating a new page
// if the current tail page is full.
func (rm *RecordManager) insertRecord(data []byte, tailPageID PageID, pageType uint16, tail *PageID) (RID, error) {
	page, err := rm.pool.FetchPage(tailPageID)
	if err != nil {
		return InvalidRID, err
	}

	sp := NewSlottedPage(page)
	slot, err := sp.InsertRecord(data)
	if err == nil {
		rm.pool.UnpinPage(tailPageID, true)
		return RID{PageID: tailPageID, Slot: slot}, nil
	}

	// Page full — allocate a new page and link it.
	rm.pool.UnpinPage(tailPageID, false)

	newPageID, newPage, err := rm.pool.NewPage()
	if err != nil {
		return InvalidRID, fmt.Errorf("allocate new page: %w", err)
	}
	newSP := InitSlottedPage(newPage, newPageID, pageType)

	// Link old tail → new page.
	oldPage, err := rm.pool.FetchPage(tailPageID)
	if err != nil {
		rm.pool.UnpinPage(newPageID, false)
		return InvalidRID, err
	}
	oldSP := NewSlottedPage(oldPage)
	oldSP.SetNextPage(newPageID)
	rm.pool.UnpinPage(tailPageID, true)

	slot, err = newSP.InsertRecord(data)
	if err != nil {
		rm.pool.UnpinPage(newPageID, false)
		return InvalidRID, fmt.Errorf("insert into new page: %w", err)
	}

	rm.pool.UnpinPage(newPageID, true)
	*tail = newPageID
	return RID{PageID: newPageID, Slot: slot}, nil
}

// readRecord reads raw record data from a slotted page at the given RID.
func (rm *RecordManager) readRecord(rid RID) ([]byte, error) {
	page, err := rm.pool.FetchPage(rid.PageID)
	if err != nil {
		return nil, err
	}
	defer rm.pool.UnpinPage(rid.PageID, false)

	sp := NewSlottedPage(page)
	return sp.GetRecord(rid.Slot)
}

// deleteRecord marks a record as deleted in its slotted page.
func (rm *RecordManager) deleteRecord(rid RID) error {
	page, err := rm.pool.FetchPage(rid.PageID)
	if err != nil {
		return err
	}

	sp := NewSlottedPage(page)
	if err := sp.DeleteRecord(rid.Slot); err != nil {
		rm.pool.UnpinPage(rid.PageID, false)
		return err
	}
	rm.pool.UnpinPage(rid.PageID, true)
	return nil
}

// removeEdgeID removes an edge ID from a slice (used for adjacency cleanup).
func removeEdgeID(ids []EdgeID, target EdgeID) []EdgeID {
	for i, id := range ids {
		if id == target {
			return append(ids[:i], ids[i+1:]...)
		}
	}
	return ids
}
