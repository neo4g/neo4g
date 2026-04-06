package gateway

import (
	"sync"
	"sync/atomic"
	"time"
)

// NodeState tracks the health and role of a single Neo4g node.
type NodeState struct {
	ID     string
	Addr   string
	Role   string // "primary" | "replica"
	Alive  bool
	LSN    uint64
	LastOK time.Time
}

// Cluster maintains the set of known nodes, which one is primary,
// and a round-robin counter for distributing reads.
type Cluster struct {
	mu      sync.RWMutex
	nodes   map[string]*NodeState
	primary string        // node ID of current primary
	rrIndex atomic.Uint64 // round-robin counter for reads
}

// NewCluster builds a Cluster from the initial node list.
// The first node that responds as primary (via heartbeat) will be elected.
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

// SetPrimary marks a node as the primary and updates all other alive nodes to replica.
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

// Primary returns the current primary node, or nil if none is known.
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

// UpdateHealth updates a node's health status from a heartbeat response.
func (c *Cluster) UpdateHealth(nodeID string, alive bool, role string, lsn uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, ok := c.nodes[nodeID]
	if !ok {
		return
	}
	n.Alive = alive
	n.LSN = lsn
	if alive {
		n.LastOK = time.Now()
		n.Role = role
		if role == "primary" && c.primary != nodeID {
			c.primary = nodeID
		}
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
func (c *Cluster) IsPrimaryDead() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.primary == "" {
		return true
	}
	n := c.nodes[c.primary]
	return n == nil || !n.Alive
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

// NextReadNode picks the next healthy node for a read query using round-robin.
func (c *Cluster) NextReadNode() *NodeState {
	healthy := c.HealthyNodes()
	if len(healthy) == 0 {
		return nil
	}
	idx := c.rrIndex.Add(1) - 1
	pick := healthy[idx%uint64(len(healthy))]
	return &pick
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
