package gateway

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/neo4g/neo4g/rpcpb"
)

// Election handles leader election when the primary goes down.
type Election struct {
	mu      sync.Mutex
	cluster *Cluster
	cfg     Config
	dialer  *Dialer
}

// NewElection creates a new election handler.
func NewElection(cfg Config, cluster *Cluster, dialer *Dialer) *Election {
	if dialer == nil {
		dialer = NewDialer()
	}
	return &Election{
		cluster: cluster,
		cfg:     cfg,
		dialer:  dialer,
	}
}

// Run triggers an election: picks the replica with the highest LSN,
// promotes it to primary, and tells other replicas to follow it.
func (e *Election) Run() {
	e.mu.Lock()
	defer e.mu.Unlock()

	time.Sleep(e.cfg.ElectionDelay)

	if !e.cluster.IsPrimaryDead() {
		log.Println("gateway: election cancelled — primary recovered")
		return
	}

	replicas := e.cluster.AliveReplicas()
	if len(replicas) == 0 {
		log.Println("gateway: election failed — no alive replicas")
		return
	}

	sort.Slice(replicas, func(i, j int) bool {
		if replicas[i].LSN != replicas[j].LSN {
			return replicas[i].LSN > replicas[j].LSN
		}
		return replicas[i].ID < replicas[j].ID
	})

	winner := replicas[0]
	log.Printf("gateway: electing %s as new primary (lsn=%d)", winner.ID, winner.LSN)

	if err := e.promote(winner.Addr); err != nil {
		log.Printf("gateway: failed to promote %s: %v", winner.ID, err)
		return
	}

	e.cluster.SetPrimary(winner.ID)
	log.Printf("gateway: %s is now primary", winner.ID)

	for _, r := range replicas[1:] {
		if err := e.demote(r.Addr, winner.Addr); err != nil {
			log.Printf("gateway: failed to set %s as replica of %s: %v", r.ID, winner.ID, err)
		}
	}
}

// Demote tells a specific node to become a replica of the current primary.
// Used when a stale primary rejoins the cluster.
func (e *Election) Demote(nodeID string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	node := e.cluster.NodeByID(nodeID)
	primary := e.cluster.Primary()
	if node == nil || primary == nil || node.ID == primary.ID {
		return
	}

	log.Printf("gateway: demoting stale node %s to replica of %s", nodeID, primary.ID)
	if err := e.setRole(node.Addr, "replica", primary.Addr); err != nil {
		log.Printf("gateway: failed to demote %s: %v", nodeID, err)
		return
	}
	e.cluster.UpdateHealth(nodeID, true, "replica", node.LSN)
}

// Redirect tells a replica to follow a different primary.
func (e *Election) Redirect(replicaNodeID, primaryAddr string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	node := e.cluster.NodeByID(replicaNodeID)
	if node == nil {
		return
	}

	log.Printf("gateway: redirecting replica %s to follow %s", replicaNodeID, primaryAddr)
	if err := e.setRole(node.Addr, "replica", primaryAddr); err != nil {
		log.Printf("gateway: failed to redirect %s: %v", replicaNodeID, err)
	}
}

func (e *Election) promote(addr string) error {
	return e.setRole(addr, "primary", "")
}

func (e *Election) demote(addr, primaryAddr string) error {
	return e.setRole(addr, "replica", primaryAddr)
}

func (e *Election) setRole(addr, role, primaryAddr string) error {
	api, err := e.dialer.Client(addr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = api.SetRole(ctx, &rpcpb.SetRoleRequest{
		Role:        role,
		PrimaryAddr: primaryAddr,
	})
	return err
}
