package gateway

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// NodeState tracks the health and role of a single Neo4g node.
type NodeState struct {
	ID           string
	Addr         string
	Role         string // gateway assignment: "primary" | "replica" | "unknown"
	ReportedRole string // last role the node reported for itself
	Alive        bool
	LSN          uint64 // contiguously applied primary LSN (primary: its tip)
	LSNKnown     bool
	Generation   uint64
	Failures     int
	LastOK       time.Time
}

// Cluster maintains the set of known nodes, which one is primary,
// and a round-robin counter for distributing reads.
//
// The gateway is the only authority for who is primary. A node's self-reported
// role is recorded and never adopted.
type Cluster struct {
	mu      sync.RWMutex
	nodes   map[string]*NodeState
	primary string        // node ID of current primary
	rrIndex atomic.Uint64 // round-robin counter for reads
}

// NewCluster builds a Cluster from the initial node list.
// Leadership is chosen by election, not by the first node that claims it.
func NewCluster(nodes []NodeConfig) *Cluster {
	c := &Cluster{
		nodes: make(map[string]*NodeState, len(nodes)),
	}
	for _, n := range nodes {
		c.nodes[n.ID] = &NodeState{
			ID:   n.ID,
			Addr: n.Addr,
			Role: "unknown",
		}
	}
	return c
}

// SetPrimary marks a node as the primary and updates all other nodes to replica.
// Call this only after the previous primary has been fenced or is unreachable.
func (c *Cluster) SetPrimary(nodeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.primary = nodeID
	for id, n := range c.nodes {
		if id == nodeID {
			n.Role = "primary"
		} else {
			n.Role = "replica"
		}
	}
}

// Primary returns the current primary node, or nil if none is known or it is down.
func (c *Cluster) Primary() *NodeState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.primary == "" {
		return nil
	}
	n := c.nodes[c.primary]
	if n == nil || !n.Alive {
		return nil
	}
	cpy := *n
	return &cpy
}

// PrimaryID returns the node ID of the current primary.
func (c *Cluster) PrimaryID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.primary
}

// UpdateHealth records a successful heartbeat. A self-reported primary that is
// not the gateway's current choice does not become primary.
func (c *Cluster) UpdateHealth(nodeID string, alive bool, role string, lsn uint64, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, ok := c.nodes[nodeID]
	if !ok {
		return
	}
	if !alive {
		n.Alive = false
		return
	}
	n.Alive = true
	n.Failures = 0
	n.LastOK = time.Now()
	n.ReportedRole = role
	n.LSN = lsn
	n.LSNKnown = true
	if generation > n.Generation {
		n.Generation = generation
	}
	if c.primary == nodeID {
		n.Role = "primary"
	} else if c.primary != "" {
		n.Role = "replica"
	} else {
		n.Role = "unknown"
	}
}

// NoteFailure counts a missed heartbeat. The node is marked dead only after
// threshold consecutive failures (derived from HeartbeatTimeout).
func (c *Cluster) NoteFailure(nodeID string, threshold int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, ok := c.nodes[nodeID]
	if !ok {
		return
	}
	if threshold < 2 {
		threshold = 2
	}
	n.Failures++
	if n.Failures >= threshold {
		n.Alive = false
	}
}

// NoteRole records a role the gateway successfully assigned and the generation
// used for that assignment.
func (c *Cluster) NoteRole(nodeID, reported string, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, ok := c.nodes[nodeID]
	if !ok {
		return
	}
	n.ReportedRole = reported
	if generation > n.Generation {
		n.Generation = generation
	}
}

// MarkDead marks a node as not alive.
func (c *Cluster) MarkDead(nodeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n, ok := c.nodes[nodeID]; ok {
		n.Alive = false
	}
}

// IsPrimaryDead reports whether the current primary is marked dead.
// No chosen primary counts as dead so startup runs an explicit election.
func (c *Cluster) IsPrimaryDead() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.primary == "" {
		return true
	}
	n := c.nodes[c.primary]
	return n == nil || !n.Alive
}

// ElectionCandidates returns alive nodes except the current primary.
// ok is false when any of those nodes has no applied-LSN cursor yet.
func (c *Cluster) ElectionCandidates() (nodes []NodeState, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []NodeState
	for _, n := range c.nodes {
		if !n.Alive || n.ID == c.primary {
			continue
		}
		if !n.LSNKnown {
			return nil, false
		}
		out = append(out, *n)
	}
	return out, true
}

// ReportedPrimaries returns alive nodes whose last self-report was primary.
func (c *Cluster) ReportedPrimaries() []NodeState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []NodeState
	for _, n := range c.nodes {
		if n.Alive && n.ReportedRole == "primary" {
			out = append(out, *n)
		}
	}
	return out
}

// AliveReplicas returns a copy of all alive replica NodeStates.
func (c *Cluster) AliveReplicas() []NodeState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []NodeState
	for _, n := range c.nodes {
		if n.Alive && n.Role == "replica" {
			out = append(out, *n)
		}
	}
	return out
}

// AllNodes returns a copy of all NodeStates.
func (c *Cluster) AllNodes() []NodeState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]NodeState, 0, len(c.nodes))
	for _, n := range c.nodes {
		out = append(out, *n)
	}
	return out
}

// HealthyNodes returns all alive nodes (primary + replicas).
func (c *Cluster) HealthyNodes() []NodeState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []NodeState
	for _, n := range c.nodes {
		if n.Alive {
			out = append(out, *n)
		}
	}
	return out
}

// ReadNode returns a replica whose applied LSN is at least minLSN.
// If none qualifies, the primary is returned (possibly nil).
func (c *Cluster) ReadNode(minLSN uint64) *NodeState {
	c.mu.RLock()
	var replicas []NodeState
	for _, n := range c.nodes {
		if n.Alive && n.ID != c.primary && n.LSNKnown && n.LSN >= minLSN {
			replicas = append(replicas, *n)
		}
	}
	c.mu.RUnlock()
	if len(replicas) == 0 {
		return c.Primary()
	}
	sort.Slice(replicas, func(i, j int) bool { return replicas[i].ID < replicas[j].ID })
	idx := c.rrIndex.Add(1) - 1
	pick := replicas[idx%uint64(len(replicas))]
	return &pick
}

// NextReadNode picks any healthy replica, or the primary when no replica is up.
func (c *Cluster) NextReadNode() *NodeState {
	return c.ReadNode(0)
}

// NodeByID returns a copy of the node with the given ID, or nil.
func (c *Cluster) NodeByID(id string) *NodeState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n, ok := c.nodes[id]
	if !ok {
		return nil
	}
	cpy := *n
	return &cpy
}
