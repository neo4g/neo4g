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
	"github.com/seankohjs/neo4g/storage/page"
	"github.com/seankohjs/neo4g/storage/pool"
)

// Manager provides CRUD operations for nodes and edges.
//
// Thread safety: Manager is safe for concurrent use.
type Manager struct {
	mu   sync.Mutex
	pool *pool.Pool
	disk *disk.Manager

	nodePageHead storage.PageID
	edgePageHead storage.PageID
	nodePageTail storage.PageID
	edgePageTail storage.PageID

	// In-memory indexes (replaced by B+Tree in the index layer).
	nodeIndex map[graph.NodeID]storage.RID
	edgeIndex map[graph.EdgeID]storage.RID

	outEdges map[graph.NodeID][]graph.EdgeID
	inEdges  map[graph.NodeID][]graph.EdgeID
}

// NewManager creates a Manager backed by the given buffer pool and disk manager.
func NewManager(p *pool.Pool, dm *disk.Manager) (*Manager, error) {
	rm := &Manager{
		pool:      p,
		disk:      dm,
		nodeIndex: make(map[graph.NodeID]storage.RID),
		edgeIndex: make(map[graph.EdgeID]storage.RID),
		outEdges:  make(map[graph.NodeID][]graph.EdgeID),
		inEdges:   make(map[graph.NodeID][]graph.EdgeID),
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

	return rm, nil
}

// --- Node operations ---

// CreateNode creates a new node with the given labels and properties.
func (rm *Manager) CreateNode(labels []string, props map[string]graph.Value) (graph.NodeID, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

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
	rid, err := rm.insertRecord(data, rm.nodePageTail, storage.PageTypeNode, &rm.nodePageTail)
	if err != nil {
		return 0, fmt.Errorf("insert node record: %w", err)
	}

	rm.nodeIndex[node.ID] = rid
	return node.ID, nil
}

// GetNode retrieves a node by its ID.
func (rm *Manager) GetNode(id graph.NodeID) (*graph.Node, error) {
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

	return encoding.DecodeNode(data)
}

// UpdateNode updates the properties of an existing node.
func (rm *Manager) UpdateNode(id graph.NodeID, props map[string]graph.Value) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rid, ok := rm.nodeIndex[id]
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

	sp := page.New(p)
	err = sp.UpdateRecord(rid.Slot, newData)
	if err == nil {
		rm.pool.UnpinPage(rid.PageID, true)
		return nil
	}

	sp.DeleteRecord(rid.Slot)
	rm.pool.UnpinPage(rid.PageID, true)

	newRID, err := rm.insertRecord(newData, rm.nodePageTail, storage.PageTypeNode, &rm.nodePageTail)
	if err != nil {
		return fmt.Errorf("reinsert node %d: %w", id, err)
	}
	rm.nodeIndex[id] = newRID
	return nil
}

// DeleteNode removes a node by its ID.
func (rm *Manager) DeleteNode(id graph.NodeID) error {
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
func (rm *Manager) CreateEdge(edgeType string, src, dst graph.NodeID, props map[string]graph.Value) (graph.EdgeID, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

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

	edge := &graph.Edge{
		ID:         graph.EdgeID(id),
		Type:       edgeType,
		SourceID:   src,
		TargetID:   dst,
		Properties: props,
	}

	data := encoding.EncodeEdge(edge)
	rid, err := rm.insertRecord(data, rm.edgePageTail, storage.PageTypeEdge, &rm.edgePageTail)
	if err != nil {
		return 0, fmt.Errorf("insert edge record: %w", err)
	}

	rm.edgeIndex[edge.ID] = rid
	rm.outEdges[src] = append(rm.outEdges[src], edge.ID)
	rm.inEdges[dst] = append(rm.inEdges[dst], edge.ID)
	return edge.ID, nil
}

// GetEdge retrieves an edge by its ID.
func (rm *Manager) GetEdge(id graph.EdgeID) (*graph.Edge, error) {
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

	return encoding.DecodeEdge(data)
}

// GetEdges returns all edges connected to a node in the specified direction.
func (rm *Manager) GetEdges(nodeID graph.NodeID, dir graph.Direction) ([]*graph.Edge, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	var edgeIDs []graph.EdgeID
	switch dir {
	case graph.Outgoing:
		edgeIDs = rm.outEdges[nodeID]
	case graph.Incoming:
		edgeIDs = rm.inEdges[nodeID]
	case graph.Both:
		edgeIDs = append(edgeIDs, rm.outEdges[nodeID]...)
		edgeIDs = append(edgeIDs, rm.inEdges[nodeID]...)
	}

	edges := make([]*graph.Edge, 0, len(edgeIDs))
	for _, eid := range edgeIDs {
		rid, ok := rm.edgeIndex[eid]
		if !ok {
			continue
		}
		data, err := rm.readRecord(rid)
		if err != nil {
			return nil, fmt.Errorf("read edge %d: %w", eid, err)
		}
		edge, err := encoding.DecodeEdge(data)
		if err != nil {
			return nil, fmt.Errorf("decode edge %d: %w", eid, err)
		}
		edges = append(edges, edge)
	}
	return edges, nil
}

// DeleteEdge removes an edge by its ID.
func (rm *Manager) DeleteEdge(id graph.EdgeID) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rid, ok := rm.edgeIndex[id]
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

	if err := rm.deleteRecord(rid); err != nil {
		return fmt.Errorf("delete edge %d: %w", id, err)
	}

	delete(rm.edgeIndex, id)
	rm.outEdges[edge.SourceID] = removeEdgeID(rm.outEdges[edge.SourceID], id)
	rm.inEdges[edge.TargetID] = removeEdgeID(rm.inEdges[edge.TargetID], id)
	return nil
}

// --- Internal helpers ---

func (rm *Manager) insertRecord(data []byte, tailPageID storage.PageID, pageType uint16, tail *storage.PageID) (storage.RID, error) {
	p, err := rm.pool.FetchPage(tailPageID)
	if err != nil {
		return storage.InvalidRID, err
	}

	sp := page.New(p)
	slot, err := sp.InsertRecord(data)
	if err == nil {
		rm.pool.UnpinPage(tailPageID, true)
		return storage.RID{PageID: tailPageID, Slot: slot}, nil
	}

	rm.pool.UnpinPage(tailPageID, false)

	newPageID, newPage, err := rm.pool.NewPage()
	if err != nil {
		return storage.InvalidRID, fmt.Errorf("allocate new page: %w", err)
	}
	newSP := page.Init(newPage, newPageID, pageType)

	oldPage, err := rm.pool.FetchPage(tailPageID)
	if err != nil {
		rm.pool.UnpinPage(newPageID, false)
		return storage.InvalidRID, err
	}
	oldSP := page.New(oldPage)
	oldSP.SetNextPage(newPageID)
	rm.pool.UnpinPage(tailPageID, true)

	slot, err = newSP.InsertRecord(data)
	if err != nil {
		rm.pool.UnpinPage(newPageID, false)
		return storage.InvalidRID, fmt.Errorf("insert into new page: %w", err)
	}

	rm.pool.UnpinPage(newPageID, true)
	*tail = newPageID
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

func (rm *Manager) deleteRecord(rid storage.RID) error {
	p, err := rm.pool.FetchPage(rid.PageID)
	if err != nil {
		return err
	}

	sp := page.New(p)
	if err := sp.DeleteRecord(rid.Slot); err != nil {
		rm.pool.UnpinPage(rid.PageID, false)
		return err
	}
	rm.pool.UnpinPage(rid.PageID, true)
	return nil
}

func removeEdgeID(ids []graph.EdgeID, target graph.EdgeID) []graph.EdgeID {
	for i, id := range ids {
		if id == target {
			return append(ids[:i], ids[i+1:]...)
		}
	}
	return ids
}
