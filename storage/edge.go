package storage

// edge.go defines the Edge type — a directed relationship between two nodes.
//
// An Edge represents a directed connection in the property graph. Each edge has:
//   - A unique EdgeID (monotonically increasing, assigned by the disk manager)
//   - A type string (e.g., "KNOWS", "WORKS_AT") describing the relationship
//   - Source and target NodeIDs
//   - Zero or more properties (same Value type as nodes)
//
// Edges are stored in edge-typed slotted pages, separate from node pages.
// The adjacency index (NodeID → []EdgeID) enables fast traversal from a node
// to its connected edges in either direction.
//
// Direction is used when querying edges from a node — callers can request
// outgoing edges (where the node is the source), incoming edges (where the
// node is the target), or both.

// EdgeID uniquely identifies an edge in the database.
// IDs start at 1; 0 is reserved as a sentinel meaning "no edge".
type EdgeID uint64

// Edge represents a directed relationship between two nodes in the property graph.
type Edge struct {
	ID         EdgeID
	Type       string
	SourceID   NodeID
	TargetID   NodeID
	Properties map[string]Value
}

// Direction specifies which edges to retrieve relative to a node.
type Direction int

const (
	// Outgoing selects edges where the node is the source.
	Outgoing Direction = iota
	// Incoming selects edges where the node is the target.
	Incoming
	// Both selects all edges connected to the node.
	Both
)
