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
	Status string `json:"status"`
	NodeID string `json:"node_id"`
	Role   string `json:"role"`
	LSN    uint64 `json:"lsn"`
}

// Heartbeat periodically pings all nodes and updates cluster state.
type Heartbeat struct {
	cluster  *Cluster
	cfg      Config
	client   *http.Client
	onPrimaryDead func()

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
}
