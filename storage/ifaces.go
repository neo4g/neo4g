package storage

import "github.com/neo4g/neo4g/graph"

// Store is the top-level CRUD interface for the property graph.
// Implemented by record.Manager.
type Store interface {
	CreateNode(labels []string, props map[string]graph.Value) (graph.NodeID, error)
	GetNode(id graph.NodeID) (*graph.Node, error)
	UpdateNode(id graph.NodeID, props map[string]graph.Value) error
	DeleteNode(id graph.NodeID) error

	CreateEdge(typ string, src, dst graph.NodeID, props map[string]graph.Value) (graph.EdgeID, error)
	GetEdge(id graph.EdgeID) (*graph.Edge, error)
	GetEdges(nodeID graph.NodeID, dir graph.Direction) ([]*graph.Edge, error)
	DeleteEdge(id graph.EdgeID) error
}

// Scanner provides read-path operations for the query engine.
// Implemented by record.Manager.
type Scanner interface {
	ScanNodes(labels []string) ([]*graph.Node, error)
	ScanEdges(typ string) ([]*graph.Edge, error)
	Expand(nodeID graph.NodeID, dir graph.Direction, edgeType string) ([]*graph.Edge, error)
}

// Index defines the B+Tree lookup interface.
// Implemented by index.BTree.
type Index interface {
	Search(key uint64) (RID, bool)
	Insert(key uint64, rid RID) error
	Delete(key uint64) bool
	RangeScan(startKey, endKey uint64) []RID
}

// PageManager abstracts the buffer pool for testing and decoupling.
// Implemented by pool.Pool.
type PageManager interface {
	FetchPage(id PageID) (*Page, error)
	UnpinPage(id PageID, dirty bool) error
	NewPage() (PageID, *Page, error)
	FlushPage(id PageID) error
	FlushAll() error
}
