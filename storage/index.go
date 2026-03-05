package storage

// index.go implements the Index Manager that maintains B+Tree indexes for
// fast lookups in the record manager.
//
// Primary indexes (always maintained):
//   - NodeID → RID:  find a node's physical page location by its ID
//   - EdgeID → RID:  find an edge's physical page location by its ID
//   - Adjacency (source NodeID → EdgeIDs): find outgoing edges of a node
//   - Adjacency (target NodeID → EdgeIDs): find incoming edges of a node
//
// The adjacency indexes use a composite key encoding:
//   NodeID (upper 32 bits) | EdgeID (lower 32 bits)
// This allows range scanning all edges for a given node by scanning the
// key range [nodeID<<32, (nodeID+1)<<32).
//
// Secondary indexes (property-based, user-created) will be added in a future
// iteration. The B+Tree infrastructure supports them already.

// IndexManager holds the B+Tree indexes used by the record manager.
type IndexManager struct {
	NodeIndex *BTree // NodeID → RID
	EdgeIndex *BTree // EdgeID → RID
	OutIndex  *BTree // composite(sourceNodeID, edgeID) → RID (edge RID)
	InIndex   *BTree // composite(targetNodeID, edgeID) → RID (edge RID)
}

// NewIndexManager creates all primary indexes.
func NewIndexManager(pool *BufferPool, disk *DiskManager) (*IndexManager, error) {
	nodeIdx, err := NewBTree(pool, disk)
	if err != nil {
		return nil, err
	}
	edgeIdx, err := NewBTree(pool, disk)
	if err != nil {
		return nil, err
	}
	outIdx, err := NewBTree(pool, disk)
	if err != nil {
		return nil, err
	}
	inIdx, err := NewBTree(pool, disk)
	if err != nil {
		return nil, err
	}

	return &IndexManager{
		NodeIndex: nodeIdx,
		EdgeIndex: edgeIdx,
		OutIndex:  outIdx,
		InIndex:   inIdx,
	}, nil
}

// --- Adjacency index composite key helpers ---

// AdjacencyKey encodes a (nodeID, edgeID) pair into a single uint64 key for
// the adjacency B+Tree indexes. NodeID occupies the upper 32 bits and EdgeID
// the lower 32 bits, enabling range scans for all edges of a given node.
//
// Limitation: this encoding supports NodeID and EdgeID up to 2^32-1.
// For larger IDs, a wider key format or separate index would be needed.
func AdjacencyKey(nodeID NodeID, edgeID EdgeID) uint64 {
	return (uint64(nodeID) << 32) | uint64(edgeID)
}

// AdjacencyRangeStart returns the start key for scanning all edges of a node.
func AdjacencyRangeStart(nodeID NodeID) uint64 {
	return uint64(nodeID) << 32
}

// AdjacencyRangeEnd returns the end key (inclusive) for scanning all edges of a node.
func AdjacencyRangeEnd(nodeID NodeID) uint64 {
	return (uint64(nodeID) << 32) | 0xFFFFFFFF
}
