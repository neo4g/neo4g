package gateway

import (
	"context"
	"log"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/neo4g/neo4g/rpcpb"
)

// Heartbeat periodically pings all nodes over gRPC and updates cluster state.
type Heartbeat struct {
	cluster        *Cluster
	cfg            Config
	dialer         NodeDialer
	onPrimaryDead  func()
	onStalePrimary func(staleNodeID string)                        // node claims primary but gateway disagrees
	onWrongPrimary func(replicaNodeID string, shouldFollow string) // replica following wrong primary

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewHeartbeat creates a new heartbeat monitor.
// onPrimaryDead is called when the primary is detected as dead.
func NewHeartbeat(cfg Config, cluster *Cluster, dialer NodeDialer, onPrimaryDead func()) *Heartbeat {
	if dialer == nil {
		dialer = NewDialer()
	}
	return &Heartbeat{
		cluster:       cluster,
		cfg:           cfg,
		dialer:        dialer,
		onPrimaryDead: onPrimaryDead,
		stopCh:        make(chan struct{}),
	}
}

// OnStalePrimary sets a callback for when a node reports as primary
// but the gateway knows a different node is the actual primary.
func (h *Heartbeat) OnStalePrimary(fn func(staleNodeID string)) {
	h.onStalePrimary = fn
}

// OnWrongPrimary sets a callback for when a replica is pulling WAL from the
// wrong primary address.
func (h *Heartbeat) OnWrongPrimary(fn func(replicaNodeID string, shouldFollow string)) {
	h.onWrongPrimary = fn
}

// Start begins the heartbeat loop.
func (h *Heartbeat) Start() {
	h.wg.Add(1)
	go h.loop()
}

// Stop signals the heartbeat loop to stop and waits.
func (h *Heartbeat) Stop() {
	close(h.stopCh)
	h.wg.Wait()
}

func (h *Heartbeat) loop() {
	defer h.wg.Done()
	h.beat()
	ticker := time.NewTicker(h.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			h.beat()
		}
	}
}

func (h *Heartbeat) beat() {
	h.pingAll()
	if h.cluster.IsPrimaryDead() && h.onPrimaryDead != nil {
		h.onPrimaryDead()
	}
}

func (h *Heartbeat) pingAll() {
	nodes := h.cluster.AllNodes()
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		go func(node NodeState) {
			defer wg.Done()
			h.ping(node)
		}(n)
	}
	wg.Wait()
}

func (h *Heartbeat) ping(node NodeState) {
	api, err := h.dialer.Client(node.Addr)
	if err != nil {
		h.cluster.NoteFailure(node.ID, h.cfg.consecutiveFailures())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), h.cfg.HeartbeatInterval)
	defer cancel()
	var hdr metadata.MD
	hr, err := api.Health(ctx, &rpcpb.HealthRequest{}, grpc.Header(&hdr))
	if err != nil {
		h.cluster.NoteFailure(node.ID, h.cfg.consecutiveFailures())
		return
	}

	gen := rpcpb.GenerationFromHeader(hdr)
	h.cluster.UpdateHealth(node.ID, true, hr.GetRole(), hr.GetLsn(), gen)
	log.Printf("gateway: heartbeat %s → alive (role=%s, applied_lsn=%d, generation=%d)", node.ID, hr.GetRole(), hr.GetLsn(), gen)

	// UpdateHealth does not adopt a self-reported primary, so this fires when
	// the node disagrees with the gateway's choice.
	chosen := h.cluster.PrimaryID()
	if hr.GetRole() == "primary" && chosen != "" && chosen != node.ID && h.onStalePrimary != nil {
		log.Printf("gateway: node %s claims primary but %s is the actual primary — demoting", node.ID, chosen)
		h.onStalePrimary(node.ID)
	}

	if hr.GetRole() == "replica" && hr.GetPrimaryAddr() != "" && h.onWrongPrimary != nil {
		primary := h.cluster.Primary()
		if primary != nil && hr.GetPrimaryAddr() != primary.Addr {
			log.Printf("gateway: replica %s following %s but primary is %s (%s) — correcting",
				node.ID, hr.GetPrimaryAddr(), primary.ID, primary.Addr)
			h.onWrongPrimary(node.ID, primary.Addr)
		}
	}
}
