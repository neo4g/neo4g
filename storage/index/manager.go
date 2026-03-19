package index

import (
	"fmt"

	"github.com/seankohjs/neo4g/graph"
	"github.com/seankohjs/neo4g/storage"
	"github.com/seankohjs/neo4g/storage/disk"
	"github.com/seankohjs/neo4g/storage/pool"
)

// Manager holds the B+Tree indexes used by the record manager.
type Manager struct {
	NodeIndex *BTree // NodeID → RID
	EdgeIndex *BTree // EdgeID → RID
	OutIndex  *BTree // composite(sourceNodeID, edgeID) → RID
	InIndex   *BTree // composite(targetNodeID, edgeID) → RID
}

// NewManager creates fresh B+Tree indexes and persists their roots in the header.
func NewManager(p *pool.Pool, dm *disk.Manager) (*Manager, error) {
	nodeIdx, err := NewBTree(p, dm)
	if err != nil {
		return nil, err
	}
	edgeIdx, err := NewBTree(p, dm)
	if err != nil {
		return nil, err
	}
	outIdx, err := NewBTree(p, dm)
	if err != nil {
		return nil, err
	}
	inIdx, err := NewBTree(p, dm)
	if err != nil {
		return nil, err
	}

	m := &Manager{
		NodeIndex: nodeIdx,
		EdgeIndex: edgeIdx,
		OutIndex:  outIdx,
		InIndex:   inIdx,
	}

	if err := m.persistRoots(dm); err != nil {
		return nil, fmt.Errorf("persist index roots: %w", err)
	}
	return m, nil
}

// LoadManager loads existing B+Tree indexes from root page IDs stored in the header.
// Returns nil, nil if no indexes have been persisted yet (all roots are zero).
func LoadManager(p *pool.Pool, dm *disk.Manager) (*Manager, error) {
	nr := dm.NodeIndexRoot()
	er := dm.EdgeIndexRoot()
	or := dm.OutIndexRoot()
	ir := dm.InIndexRoot()

	if nr == storage.InvalidPageID && er == storage.InvalidPageID &&
		or == storage.InvalidPageID && ir == storage.InvalidPageID {
		return nil, nil
	}

	return &Manager{
		NodeIndex: LoadBTree(p, dm, nr),
		EdgeIndex: LoadBTree(p, dm, er),
		OutIndex:  LoadBTree(p, dm, or),
		InIndex:   LoadBTree(p, dm, ir),
	}, nil
}

func (m *Manager) persistRoots(dm *disk.Manager) error {
	if err := dm.SetNodeIndexRoot(m.NodeIndex.RootPageID()); err != nil {
		return err
	}
	if err := dm.SetEdgeIndexRoot(m.EdgeIndex.RootPageID()); err != nil {
		return err
	}
	if err := dm.SetOutIndexRoot(m.OutIndex.RootPageID()); err != nil {
		return err
	}
	return dm.SetInIndexRoot(m.InIndex.RootPageID())
}

// --- Adjacency index composite key helpers ---

// AdjacencyKey encodes a (nodeID, edgeID) pair into a single uint64 key.
func AdjacencyKey(nodeID graph.NodeID, edgeID graph.EdgeID) uint64 {
	return (uint64(nodeID) << 32) | uint64(edgeID)
}

// AdjacencyRangeStart returns the start key for scanning all edges of a node.
func AdjacencyRangeStart(nodeID graph.NodeID) uint64 {
	return uint64(nodeID) << 32
}

// AdjacencyRangeEnd returns the end key (inclusive) for scanning all edges of a node.
func AdjacencyRangeEnd(nodeID graph.NodeID) uint64 {
	return (uint64(nodeID) << 32) | 0xFFFFFFFF
}
