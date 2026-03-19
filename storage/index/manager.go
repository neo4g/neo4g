package index

import (
	"github.com/seankohjs/neo4g/graph"
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

// NewManager creates all primary indexes.
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

	return &Manager{
		NodeIndex: nodeIdx,
		EdgeIndex: edgeIdx,
		OutIndex:  outIdx,
		InIndex:   inIdx,
	}, nil
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
