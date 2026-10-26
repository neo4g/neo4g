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
//
// The gateway alone decides the primary. Promotion carries a monotonic
// generation kept in this process; a restart runs election again and bumps
// past every generation the nodes still report. Async replication can lose
// the tail written during the last poll interval — candidates are ordered by
// the applied primary LSN they publish, and election refuses to guess when
// that cursor is missing.
type Election struct {
	mu         sync.Mutex
	cluster    *Cluster
	cfg        Config
	dialer     NodeDialer
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	closed     bool
}

// NewElection creates a new election handler.
func NewElection(cfg Config, cluster *Cluster, dialer NodeDialer) *Election {
	if dialer == nil {
		dialer = NewDialer()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Election{
		cluster: cluster,
		cfg:     cfg,
		dialer:  dialer,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Trigger runs an election unless one is already shutting down.
func (e *Election) Trigger() { e.Go(e.Run) }

// TriggerDemote fences a node that claims primary but is not the gateway's choice.
func (e *Election) TriggerDemote(nodeID string) {
	e.Go(func() { e.Demote(nodeID) })
}

// TriggerRedirect points a replica at the current primary.
func (e *Election) TriggerRedirect(replicaNodeID, primaryAddr string) {
	e.Go(func() { e.Redirect(replicaNodeID, primaryAddr) })
}

// Go runs fn on a goroutine that Shutdown waits for.
// Calls after Shutdown are ignored so a closing gateway does not SetRole.
func (e *Election) Go(fn func()) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.wg.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.wg.Done()
		fn()
	}()
}

// Shutdown cancels in-flight role RPCs and waits for election goroutines.
func (e *Election) Shutdown() {
	e.cancel()
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	e.wg.Wait()
}

// Run triggers an election: fences every other live self-reported primary,
// then promotes the replica with the highest applied primary LSN.
func (e *Election) Run() {
	if !e.waitDelay() {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.ctx.Err() != nil {
		return
	}
	if !e.cluster.IsPrimaryDead() {
		log.Println("gateway: election cancelled — primary recovered")
		return
	}

	// Rank by the primary LSN each node has contiguously applied, not by its
	// local WAL NextLSN. A checkpointed replica's local log can look older
	// than a lagging one. If any alive candidate has not published a cursor,
	// refuse — guessing would hide the last poll interval of async replication.
	replicas, ok := e.cluster.ElectionCandidates()
	if !ok {
		log.Println("gateway: election refused — applied LSN unknown for an alive node")
		return
	}
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
	gen := e.nextGenLocked()
	log.Printf("gateway: electing %s as new primary (applied_lsn=%d generation=%d)", winner.ID, winner.LSN, gen)

	// An unreachable primary cannot be fenced and is replaced so failover can
	// proceed; it is demoted if it returns. A live node that still reports
	// primary must accept the demotion before the winner is advertised.
	for _, n := range e.cluster.ReportedPrimaries() {
		if n.ID == winner.ID {
			continue
		}
		log.Printf("gateway: fencing %s before advertising %s", n.ID, winner.ID)
		if err := e.setRole(n.Addr, "replica", winner.Addr, gen); err != nil {
			log.Printf("gateway: cannot fence %s (%v); not replacing primary", n.ID, err)
			return
		}
		e.cluster.NoteRole(n.ID, "replica", gen)
	}

	if err := e.promote(winner.Addr, gen); err != nil {
		log.Printf("gateway: failed to promote %s: %v", winner.ID, err)
		return
	}
	e.cluster.NoteRole(winner.ID, "primary", gen)
	e.cluster.SetPrimary(winner.ID)
	log.Printf("gateway: %s is now primary", winner.ID)

	for _, r := range replicas {
		if r.ID == winner.ID {
			continue
		}
		if err := e.demote(r.Addr, winner.Addr, gen); err != nil {
			log.Printf("gateway: failed to set %s as replica of %s: %v", r.ID, winner.ID, err)
		}
	}
}

// Demote tells a node that is not the gateway's primary to follow it.
func (e *Election) Demote(nodeID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.ctx.Err() != nil {
		return
	}

	node := e.cluster.NodeByID(nodeID)
	primary := e.cluster.Primary()
	if node == nil || primary == nil || node.ID == primary.ID {
		return
	}

	gen := e.nextGenLocked()
	log.Printf("gateway: demoting stale node %s to replica of %s (generation=%d)", nodeID, primary.ID, gen)
	if err := e.setRole(node.Addr, "replica", primary.Addr, gen); err != nil {
		log.Printf("gateway: failed to demote %s: %v", nodeID, err)
		return
	}
	e.cluster.NoteRole(nodeID, "replica", gen)
	e.cluster.UpdateHealth(nodeID, true, "replica", node.LSN, gen)
}

// Redirect tells a replica to follow a different primary.
func (e *Election) Redirect(replicaNodeID, primaryAddr string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.ctx.Err() != nil {
		return
	}

	node := e.cluster.NodeByID(replicaNodeID)
	if node == nil {
		return
	}

	gen := e.nextGenLocked()
	log.Printf("gateway: redirecting replica %s to follow %s", replicaNodeID, primaryAddr)
	if err := e.setRole(node.Addr, "replica", primaryAddr, gen); err != nil {
		log.Printf("gateway: failed to redirect %s: %v", replicaNodeID, err)
		return
	}
	e.cluster.NoteRole(replicaNodeID, "replica", gen)
}

func (e *Election) waitDelay() bool {
	timer := time.NewTimer(e.cfg.ElectionDelay)
	defer timer.Stop()
	select {
	case <-e.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (e *Election) nextGenLocked() uint64 {
	max := e.generation
	for _, n := range e.cluster.AllNodes() {
		if n.Generation > max {
			max = n.Generation
		}
	}
	max++
	if max == 0 {
		max = 1
	}
	e.generation = max
	return max
}

func (e *Election) promote(addr string, generation uint64) error {
	return e.setRole(addr, "primary", "", generation)
}

func (e *Election) demote(addr, primaryAddr string, generation uint64) error {
	return e.setRole(addr, "replica", primaryAddr, generation)
}

func (e *Election) setRole(addr, role, primaryAddr string, generation uint64) error {
	if err := e.ctx.Err(); err != nil {
		return err
	}
	api, err := e.dialer.Client(addr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
	defer cancel()
	ctx = rpcpb.WithGeneration(ctx, generation)
	_, err = api.SetRole(ctx, &rpcpb.SetRoleRequest{
		Role:        role,
		PrimaryAddr: primaryAddr,
	})
	return err
}
