// Package record implements the Record Manager — the CRUD interface for nodes
// and edges in the property graph.
package record

import (
	"fmt"
	"sync"

	"github.com/seankohjs/neo4g/graph"
	"github.com/seankohjs/neo4g/storage"
	"github.com/seankohjs/neo4g/storage/disk"
	"github.com/seankohjs/neo4g/storage/encoding"
	"github.com/seankohjs/neo4g/storage/index"
	"github.com/seankohjs/neo4g/storage/page"
	"github.com/seankohjs/neo4g/storage/pool"
	"github.com/seankohjs/neo4g/storage/txn"
)

var (
	_ storage.Store   = (*Manager)(nil)
	_ storage.Scanner = (*Manager)(nil)
)

// Manager provides CRUD operations for nodes and edges.
//
// Thread safety: Manager is safe for concurrent use.
type Manager struct {
	mu   sync.Mutex
	pool *pool.Pool
	disk *disk.Manager
	idx  *index.Manager
	txm  *txn.Manager // optional; nil = no transactional wrapping

	nodePageHead storage.PageID
	edgePageHead storage.PageID
	nodePageTail storage.PageID
	edgePageTail storage.PageID
}

// NewManager creates a Manager backed by the given buffer pool, disk manager,
// and index manager. If the disk header already has page chain pointers
// (from a prior run), those are restored; otherwise fresh pages are allocated.
func NewManager(p *pool.Pool, dm *disk.Manager, idx *index.Manager) (*Manager, error) {
	rm := &Manager{
		pool: p,
		disk: dm,
		idx:  idx,
	}

	nph := dm.NodePageHead()
	if nph != storage.InvalidPageID {
		rm.nodePageHead = nph
		rm.nodePageTail = dm.NodePageTail()
		rm.edgePageHead = dm.EdgePageHead()
		rm.edgePageTail = dm.EdgePageTail()
		return rm, nil
	}

	nodeID, nodePage, err := p.NewPage()
	if err != nil {
		return nil, fmt.Errorf("allocate node page: %w", err)
	}
	page.Init(nodePage, nodeID, storage.PageTypeNode)
	p.UnpinPage(nodeID, true)
	rm.nodePageHead = nodeID
	rm.nodePageTail = nodeID

	edgeID, edgePage, err := p.NewPage()
	if err != nil {
		return nil, fmt.Errorf("allocate edge page: %w", err)
	}
	page.Init(edgePage, edgeID, storage.PageTypeEdge)
	p.UnpinPage(edgeID, true)
	rm.edgePageHead = edgeID
	rm.edgePageTail = edgeID

	if err := rm.persistPageChain(); err != nil {
		return nil, err
	}
	return rm, nil
}

func (rm *Manager) persistPageChain() error {
	if err := rm.disk.SetNodePageHead(rm.nodePageHead); err != nil {
		return err
	}
	if err := rm.disk.SetNodePageTail(rm.nodePageTail); err != nil {
		return err
	}
	if err := rm.disk.SetEdgePageHead(rm.edgePageHead); err != nil {
		return err
	}
	return rm.disk.SetEdgePageTail(rm.edgePageTail)
}

// SetTxManager enables transactional wrapping for all mutating operations.
// When set, each Create/Update/Delete call auto-begins and auto-commits
// (or aborts on error). When nil (default), writes go directly to the pool.
func (rm *Manager) SetTxManager(tm *txn.Manager) {
	rm.txm = tm
}

func (rm *Manager) beginTx() storage.TxID {
	if rm.txm == nil {
		return 0
	}
	return rm.txm.Begin()
}

func (rm *Manager) commitOrAbort(txID storage.TxID, errp *error) {
	if rm.txm == nil {
		return
	}
	if *errp != nil {
		rm.txm.Abort(txID)
	} else {
		if cerr := rm.txm.Commit(txID); cerr != nil {
			*errp = cerr
		}
	}
}

func (rm *Manager) logPageWrite(txID storage.TxID, pageID storage.PageID, before, after *storage.Page) error {
	if rm.txm == nil {
		return nil
	}
	return rm.txm.WritePage(txID, pageID, before, after)
}

// --- Node operations ---

// CreateNode creates a new node with the given labels and properties.
func (rm *Manager) CreateNode(labels []string, props map[string]graph.Value) (nid graph.NodeID, err error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	txID := rm.beginTx()
	defer rm.commitOrAbort(txID, &err)

	id, err := rm.disk.NextNodeID()
	if err != nil {
		return 0, fmt.Errorf("allocate node ID: %w", err)
	}

	node := &graph.Node{
		ID:         graph.NodeID(id),
		Labels:     labels,
		Properties: props,
	}

	data := encoding.EncodeNode(node)
	rid, err := rm.insertRecord(txID, data, rm.nodePageTail, storage.PageTypeNode, &rm.nodePageTail)
	if err != nil {
		return 0, fmt.Errorf("insert node record: %w", err)
	}

	if err := rm.idx.NodeIndex.Insert(uint64(node.ID), rid); err != nil {
		return 0, fmt.Errorf("index node %d: %w", node.ID, err)
	}
	return node.ID, nil
}

// GetNode retrieves a node by its ID.
func (rm *Manager) GetNode(id graph.NodeID) (*graph.Node, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rid, ok := rm.idx.NodeIndex.Search(uint64(id))
	if !ok {
		return nil, fmt.Errorf("node %d not found", id)
	}

	data, err := rm.readRecord(rid)
	if err != nil {
		return nil, fmt.Errorf("read node %d: %w", id, err)
	}

	return encoding.DecodeNode(data)
}

// UpdateNode updates the properties of an existing node.
func (rm *Manager) UpdateNode(id graph.NodeID, props map[string]graph.Value) (err error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	txID := rm.beginTx()
	defer rm.commitOrAbort(txID, &err)

	rid, ok := rm.idx.NodeIndex.Search(uint64(id))
	if !ok {
		return fmt.Errorf("node %d not found", id)
	}

	data, err := rm.readRecord(rid)
	if err != nil {
		return fmt.Errorf("read node %d for update: %w", id, err)
	}
	node, err := encoding.DecodeNode(data)
	if err != nil {
		return fmt.Errorf("decode node %d for update: %w", id, err)
	}

	for k, v := range props {
		node.Properties[k] = v
	}

	newData := encoding.EncodeNode(node)

	p, err := rm.pool.FetchPage(rid.PageID)
	if err != nil {
		return fmt.Errorf("fetch page for update: %w", err)
	}

	var before storage.Page
	copy(before[:], p[:])

	sp := page.New(p)
	err = sp.UpdateRecord(rid.Slot, newData)
	if err == nil {
		if logErr := rm.logPageWrite(txID, rid.PageID, &before, p); logErr != nil {
			rm.pool.UnpinPage(rid.PageID, false)
			return fmt.Errorf("log update page: %w", logErr)
		}
		rm.pool.UnpinPage(rid.PageID, true)
		return nil
	}

	sp.DeleteRecord(rid.Slot)
	if logErr := rm.logPageWrite(txID, rid.PageID, &before, p); logErr != nil {
		rm.pool.UnpinPage(rid.PageID, false)
		return fmt.Errorf("log delete page: %w", logErr)
	}
	rm.pool.UnpinPage(rid.PageID, true)

	newRID, err := rm.insertRecord(txID, newData, rm.nodePageTail, storage.PageTypeNode, &rm.nodePageTail)
	if err != nil {
		return fmt.Errorf("reinsert node %d: %w", id, err)
	}
	rm.idx.NodeIndex.Delete(uint64(id))
	if err := rm.idx.NodeIndex.Insert(uint64(id), newRID); err != nil {
		return fmt.Errorf("re-index node %d: %w", id, err)
	}
	return nil
}

// DeleteNode removes a node by its ID.
func (rm *Manager) DeleteNode(id graph.NodeID) (err error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	txID := rm.beginTx()
	defer rm.commitOrAbort(txID, &err)

	outRIDs := rm.idx.OutIndex.RangeScan(index.AdjacencyRangeStart(id), index.AdjacencyRangeEnd(id))
	inRIDs := rm.idx.InIndex.RangeScan(index.AdjacencyRangeStart(id), index.AdjacencyRangeEnd(id))
	if len(outRIDs) > 0 || len(inRIDs) > 0 {
		return fmt.Errorf("node %d has connected edges; delete edges first", id)
	}

	rid, ok := rm.idx.NodeIndex.Search(uint64(id))
	if !ok {
		return fmt.Errorf("node %d not found", id)
	}

	if err := rm.deleteRecord(txID, rid); err != nil {
		return fmt.Errorf("delete node %d: %w", id, err)
	}

	rm.idx.NodeIndex.Delete(uint64(id))
	return nil
}

// --- Edge operations ---

// CreateEdge creates a new directed edge between two nodes.
func (rm *Manager) CreateEdge(edgeType string, src, dst graph.NodeID, props map[string]graph.Value) (eid graph.EdgeID, err error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	txID := rm.beginTx()
	defer rm.commitOrAbort(txID, &err)

	if _, ok := rm.idx.NodeIndex.Search(uint64(src)); !ok {
		return 0, fmt.Errorf("source node %d not found", src)
	}
	if _, ok := rm.idx.NodeIndex.Search(uint64(dst)); !ok {
		return 0, fmt.Errorf("target node %d not found", dst)
	}

	id, err := rm.disk.NextEdgeID()
	if err != nil {
		return 0, fmt.Errorf("allocate edge ID: %w", err)
	}

	edge := &graph.Edge{
		ID:         graph.EdgeID(id),
		Type:       edgeType,
		SourceID:   src,
		TargetID:   dst,
		Properties: props,
	}

	data := encoding.EncodeEdge(edge)
	rid, err := rm.insertRecord(txID, data, rm.edgePageTail, storage.PageTypeEdge, &rm.edgePageTail)
	if err != nil {
		return 0, fmt.Errorf("insert edge record: %w", err)
	}

	if err := rm.idx.EdgeIndex.Insert(uint64(edge.ID), rid); err != nil {
		return 0, fmt.Errorf("index edge %d: %w", edge.ID, err)
	}
	if err := rm.idx.OutIndex.Insert(index.AdjacencyKey(src, edge.ID), rid); err != nil {
		return 0, fmt.Errorf("index out-edge %d: %w", edge.ID, err)
	}
	if err := rm.idx.InIndex.Insert(index.AdjacencyKey(dst, edge.ID), rid); err != nil {
		return 0, fmt.Errorf("index in-edge %d: %w", edge.ID, err)
	}
	return edge.ID, nil
}

// GetEdge retrieves an edge by its ID.
func (rm *Manager) GetEdge(id graph.EdgeID) (*graph.Edge, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rid, ok := rm.idx.EdgeIndex.Search(uint64(id))
	if !ok {
		return nil, fmt.Errorf("edge %d not found", id)
	}

	data, err := rm.readRecord(rid)
	if err != nil {
		return nil, fmt.Errorf("read edge %d: %w", id, err)
	}

	return encoding.DecodeEdge(data)
}

// GetEdges returns all edges connected to a node in the specified direction.
func (rm *Manager) GetEdges(nodeID graph.NodeID, dir graph.Direction) ([]*graph.Edge, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	start := index.AdjacencyRangeStart(nodeID)
	end := index.AdjacencyRangeEnd(nodeID)

	var rids []storage.RID
	switch dir {
	case graph.Outgoing:
		rids = rm.idx.OutIndex.RangeScan(start, end)
	case graph.Incoming:
		rids = rm.idx.InIndex.RangeScan(start, end)
	case graph.Both:
		rids = rm.idx.OutIndex.RangeScan(start, end)
		rids = append(rids, rm.idx.InIndex.RangeScan(start, end)...)
	}

	edges := make([]*graph.Edge, 0, len(rids))
	for _, rid := range rids {
		data, err := rm.readRecord(rid)
		if err != nil {
			return nil, fmt.Errorf("read edge at %v: %w", rid, err)
		}
		edge, err := encoding.DecodeEdge(data)
		if err != nil {
			return nil, fmt.Errorf("decode edge at %v: %w", rid, err)
		}
		edges = append(edges, edge)
	}
	return edges, nil
}

// DeleteEdge removes an edge by its ID.
func (rm *Manager) DeleteEdge(id graph.EdgeID) (err error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	txID := rm.beginTx()
	defer rm.commitOrAbort(txID, &err)

	rid, ok := rm.idx.EdgeIndex.Search(uint64(id))
	if !ok {
		return fmt.Errorf("edge %d not found", id)
	}

	data, err := rm.readRecord(rid)
	if err != nil {
		return fmt.Errorf("read edge %d for delete: %w", id, err)
	}
	edge, err := encoding.DecodeEdge(data)
	if err != nil {
		return fmt.Errorf("decode edge %d for delete: %w", id, err)
	}

	if err := rm.deleteRecord(txID, rid); err != nil {
		return fmt.Errorf("delete edge %d: %w", id, err)
	}

	rm.idx.EdgeIndex.Delete(uint64(id))
	rm.idx.OutIndex.Delete(index.AdjacencyKey(edge.SourceID, id))
	rm.idx.InIndex.Delete(index.AdjacencyKey(edge.TargetID, id))
	return nil
}

// --- Scanner operations ---

// ScanNodes walks the node page chain and returns all nodes whose labels
// intersect with the given set. An empty labels slice returns all nodes.
func (rm *Manager) ScanNodes(labels []string) ([]*graph.Node, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	labelSet := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		labelSet[l] = struct{}{}
	}

	var nodes []*graph.Node
	pageID := rm.nodePageHead
	for pageID != storage.InvalidPageID {
		p, err := rm.pool.FetchPage(pageID)
		if err != nil {
			return nil, fmt.Errorf("fetch node page %d: %w", pageID, err)
		}
		sp := page.New(p)

		for slot := uint16(0); slot < sp.SlotCount(); slot++ {
			if sp.IsDeleted(slot) {
				continue
			}
			data, err := sp.GetRecord(slot)
			if err != nil {
				rm.pool.UnpinPage(pageID, false)
				return nil, err
			}
			node, err := encoding.DecodeNode(data)
			if err != nil {
				rm.pool.UnpinPage(pageID, false)
				return nil, err
			}
			if len(labelSet) == 0 || nodeMatchesLabels(node, labelSet) {
				nodes = append(nodes, node)
			}
		}

		nextPage := sp.GetNextPage()
		rm.pool.UnpinPage(pageID, false)
		pageID = nextPage
	}
	return nodes, nil
}

func nodeMatchesLabels(node *graph.Node, labelSet map[string]struct{}) bool {
	for _, l := range node.Labels {
		if _, ok := labelSet[l]; ok {
			return true
		}
	}
	return false
}

// ScanEdges walks the edge page chain and returns all edges matching the
// given type. An empty typ returns all edges.
func (rm *Manager) ScanEdges(typ string) ([]*graph.Edge, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	var edges []*graph.Edge
	pageID := rm.edgePageHead
	for pageID != storage.InvalidPageID {
		p, err := rm.pool.FetchPage(pageID)
		if err != nil {
			return nil, fmt.Errorf("fetch edge page %d: %w", pageID, err)
		}
		sp := page.New(p)

		for slot := uint16(0); slot < sp.SlotCount(); slot++ {
			if sp.IsDeleted(slot) {
				continue
			}
			data, err := sp.GetRecord(slot)
			if err != nil {
				rm.pool.UnpinPage(pageID, false)
				return nil, err
			}
			edge, err := encoding.DecodeEdge(data)
			if err != nil {
				rm.pool.UnpinPage(pageID, false)
				return nil, err
			}
			if typ == "" || edge.Type == typ {
				edges = append(edges, edge)
			}
		}

		nextPage := sp.GetNextPage()
		rm.pool.UnpinPage(pageID, false)
		pageID = nextPage
	}
	return edges, nil
}

// Expand returns edges connected to nodeID in the specified direction,
// optionally filtered by edgeType (empty = all types).
// Uses the adjacency B+Tree index for efficient lookup.
func (rm *Manager) Expand(nodeID graph.NodeID, dir graph.Direction, edgeType string) ([]*graph.Edge, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	start := index.AdjacencyRangeStart(nodeID)
	end := index.AdjacencyRangeEnd(nodeID)

	var rids []storage.RID
	switch dir {
	case graph.Outgoing:
		rids = rm.idx.OutIndex.RangeScan(start, end)
	case graph.Incoming:
		rids = rm.idx.InIndex.RangeScan(start, end)
	case graph.Both:
		rids = rm.idx.OutIndex.RangeScan(start, end)
		rids = append(rids, rm.idx.InIndex.RangeScan(start, end)...)
	}

	edges := make([]*graph.Edge, 0, len(rids))
	for _, rid := range rids {
		data, err := rm.readRecord(rid)
		if err != nil {
			return nil, fmt.Errorf("read edge at %v: %w", rid, err)
		}
		edge, err := encoding.DecodeEdge(data)
		if err != nil {
			return nil, fmt.Errorf("decode edge at %v: %w", rid, err)
		}
		if edgeType == "" || edge.Type == edgeType {
			edges = append(edges, edge)
		}
	}
	return edges, nil
}

// --- Internal helpers ---

func (rm *Manager) insertRecord(txID storage.TxID, data []byte, tailPageID storage.PageID, pageType uint16, tail *storage.PageID) (storage.RID, error) {
	p, err := rm.pool.FetchPage(tailPageID)
	if err != nil {
		return storage.InvalidRID, err
	}

	var before storage.Page
	copy(before[:], p[:])

	sp := page.New(p)
	slot, err := sp.InsertRecord(data)
	if err == nil {
		if logErr := rm.logPageWrite(txID, tailPageID, &before, p); logErr != nil {
			rm.pool.UnpinPage(tailPageID, false)
			return storage.InvalidRID, logErr
		}
		rm.pool.UnpinPage(tailPageID, true)
		return storage.RID{PageID: tailPageID, Slot: slot}, nil
	}

	rm.pool.UnpinPage(tailPageID, false)

	newPageID, newPage, err := rm.pool.NewPage()
	if err != nil {
		return storage.InvalidRID, fmt.Errorf("allocate new page: %w", err)
	}

	var newBefore storage.Page
	copy(newBefore[:], newPage[:])

	newSP := page.Init(newPage, newPageID, pageType)

	oldPage, err := rm.pool.FetchPage(tailPageID)
	if err != nil {
		rm.pool.UnpinPage(newPageID, false)
		return storage.InvalidRID, err
	}
	var oldBefore storage.Page
	copy(oldBefore[:], oldPage[:])

	oldSP := page.New(oldPage)
	oldSP.SetNextPage(newPageID)
	if logErr := rm.logPageWrite(txID, tailPageID, &oldBefore, oldPage); logErr != nil {
		rm.pool.UnpinPage(tailPageID, false)
		rm.pool.UnpinPage(newPageID, false)
		return storage.InvalidRID, logErr
	}
	rm.pool.UnpinPage(tailPageID, true)

	slot, err = newSP.InsertRecord(data)
	if err != nil {
		rm.pool.UnpinPage(newPageID, false)
		return storage.InvalidRID, fmt.Errorf("insert into new page: %w", err)
	}

	if logErr := rm.logPageWrite(txID, newPageID, &newBefore, newPage); logErr != nil {
		rm.pool.UnpinPage(newPageID, false)
		return storage.InvalidRID, logErr
	}
	rm.pool.UnpinPage(newPageID, true)
	*tail = newPageID
	rm.persistPageChain()
	return storage.RID{PageID: newPageID, Slot: slot}, nil
}

func (rm *Manager) readRecord(rid storage.RID) ([]byte, error) {
	p, err := rm.pool.FetchPage(rid.PageID)
	if err != nil {
		return nil, err
	}
	defer rm.pool.UnpinPage(rid.PageID, false)

	sp := page.New(p)
	return sp.GetRecord(rid.Slot)
}

func (rm *Manager) deleteRecord(txID storage.TxID, rid storage.RID) error {
	p, err := rm.pool.FetchPage(rid.PageID)
	if err != nil {
		return err
	}

	var before storage.Page
	copy(before[:], p[:])

	sp := page.New(p)
	if err := sp.DeleteRecord(rid.Slot); err != nil {
		rm.pool.UnpinPage(rid.PageID, false)
		return err
	}

	if logErr := rm.logPageWrite(txID, rid.PageID, &before, p); logErr != nil {
		rm.pool.UnpinPage(rid.PageID, false)
		return logErr
	}
	rm.pool.UnpinPage(rid.PageID, true)
	return nil
}
