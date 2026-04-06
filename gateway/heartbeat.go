package gateway

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// healthResponse is the JSON returned by GET /api/health on a Neo4g node.
type healthResponse struct {
	Status      string `json:"status"`
	NodeID      string `json:"node_id"`
	Role        string `json:"role"`
	LSN         uint64 `json:"lsn"`
	PrimaryAddr string `json:"primary_addr"`
}

// Heartbeat periodically pings all nodes and updates cluster state.
type Heartbeat struct {
	cluster        *Cluster
	cfg            Config
	client         *http.Client
	onPrimaryDead  func()
	onStalePrimary func(staleNodeID string)                  // node claims primary but gateway disagrees
	onWrongPrimary func(replicaNodeID string, shouldFollow string) // replica following wrong primary

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewHeartbeat creates a new heartbeat monitor.
// onPrimaryDead is called when the primary is detected as dead.
func NewHeartbeat(cfg Config, cluster *Cluster, onPrimaryDead func()) *Heartbeat {
	return &Heartbeat{
		cluster:       cluster,
		cfg:           cfg,
		client:        &http.Client{Timeout: cfg.HeartbeatInterval},
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
	ticker := time.NewTicker(h.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			h.pingAll()
			if h.cluster.IsPrimaryDead() && h.onPrimaryDead != nil {
				h.onPrimaryDead()
			}
		}
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
	url := node.Addr + "/api/health"
	resp, err := h.client.Get(url)
	if err != nil {
		h.cluster.MarkDead(node.ID)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		h.cluster.MarkDead(node.ID)
		return
	}

	var hr healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		h.cluster.MarkDead(node.ID)
		return
	}

	h.cluster.UpdateHealth(node.ID, true, hr.Role, hr.LSN)
	log.Printf("gateway: heartbeat %s → alive (role=%s, lsn=%d)", node.ID, hr.Role, hr.LSN)

	if hr.Role == "primary" && h.cluster.PrimaryID() != node.ID && h.onStalePrimary != nil {
		log.Printf("gateway: node %s claims primary but %s is the actual primary — demoting", node.ID, h.cluster.PrimaryID())
		h.onStalePrimary(node.ID)
	}

	if hr.Role == "replica" && hr.PrimaryAddr != "" && h.onWrongPrimary != nil {
		primary := h.cluster.Primary()
		if primary != nil && hr.PrimaryAddr != primary.Addr {
			log.Printf("gateway: replica %s following %s but primary is %s (%s) — correcting",
				node.ID, hr.PrimaryAddr, primary.ID, primary.Addr)
			h.onWrongPrimary(node.ID, primary.Addr)
		}
	}
}
