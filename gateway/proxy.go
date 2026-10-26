package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/neo4g/neo4g/parser"
	"github.com/neo4g/neo4g/rpcpb"
)

// asyncReplicationNote is the loss window of pull-based replication.
// A replica can trail the primary by one poll; failover can drop that tail.
const asyncReplicationNote = "async: failover can lose commits from the last replica poll interval"

const maxQueryBytes = 1 << 20

// forcedPrimaryLSN pins later reads to the primary when a write succeeded
// but its commit LSN could not be read. A failed write does not pin.
const forcedPrimaryLSN = ^uint64(0)

// Gateway is the reverse-proxy that routes queries to Neo4g nodes.
type Gateway struct {
	cluster    *Cluster
	cfg        Config
	dialer     NodeDialer
	http       *http.Server
	mu         sync.Mutex
	lastWrites map[string]uint64 // per-client commit LSN, keyed by the TCP peer
}

// New creates a Gateway wired to the given cluster and config.
// dialer reaches database nodes over gRPC. If dialer is nil, a private one is created.
func New(cfg Config, cluster *Cluster, dialer NodeDialer) *Gateway {
	if dialer == nil {
		dialer = NewDialer()
	}
	g := &Gateway{
		cluster:    cluster,
		cfg:        cfg,
		dialer:     dialer,
		lastWrites: make(map[string]uint64),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/query", g.handleQuery)
	mux.HandleFunc("GET /api/health", g.handleHealth)

	g.http = &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	return g
}

// Server returns the underlying http.Server for Shutdown.
func (g *Gateway) Server() *http.Server { return g.http }

// ListenAndServe binds and serves, logging the actual address.
func (g *Gateway) ListenAndServe() error {
	ln, err := net.Listen("tcp", g.cfg.ListenAddr)
	if err != nil {
		return err
	}
	log.Printf("neo4g-gateway: listening on %s", ln.Addr())
	err = g.http.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type queryRequest struct {
	Query  string                 `json:"query"`
	Params map[string]interface{} `json:"params,omitempty"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (g *Gateway) handleQuery(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxQueryBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	var req queryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "missing \"query\" field")
		return
	}

	stmt, err := parser.Parse(req.Query)
	if err != nil {
		writeError(w, http.StatusBadRequest, "parse error: "+err.Error())
		return
	}

	clientIP := clientAddr(r)

	var target *NodeState
	if stmt.IsReadOnly() {
		target = g.cluster.ReadNode(g.requiredLSN(clientIP))
		if target == nil {
			writeError(w, http.StatusServiceUnavailable, "no caught-up replica or primary available")
			return
		}
	} else {
		target = g.cluster.Primary()
		if target == nil {
			writeError(w, http.StatusServiceUnavailable, "no primary available")
			return
		}
	}

	status, err := g.forward(w, r.Context(), target.Addr, req)
	if err != nil || stmt.IsReadOnly() || status < 200 || status >= 300 {
		return
	}
	lsn, herr := g.committedLSN(target.Addr)
	if herr != nil {
		log.Printf("gateway: write committed on %s but tip is unknown: %v", target.Addr, herr)
		lsn = forcedPrimaryLSN
	}
	g.remember(clientIP, lsn)
}

func (g *Gateway) requiredLSN(client string) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastWrites[client]
}

func (g *Gateway) remember(client string, lsn uint64) {
	g.mu.Lock()
	g.lastWrites[client] = lsn
	g.mu.Unlock()
}

func (g *Gateway) committedLSN(addr string) (uint64, error) {
	api, err := g.dialer.Client(addr)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hr, err := api.Health(ctx, &rpcpb.HealthRequest{})
	if err != nil {
		return 0, err
	}
	return hr.GetLsn(), nil
}

func (g *Gateway) forward(w http.ResponseWriter, ctx context.Context, addr string, req queryRequest) (int, error) {
	var paramsJSON []byte
	if req.Params != nil {
		var err error
		paramsJSON, err = json.Marshal(req.Params)
		if err != nil {
			writeError(w, http.StatusBadRequest, "encode params: "+err.Error())
			return 0, err
		}
	}

	api, err := g.dialer.Client(addr)
	if err != nil {
		log.Printf("gateway: dial %s failed: %v", addr, err)
		writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return 0, err
	}

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := api.ExecuteQuery(callCtx, &rpcpb.ExecuteQueryRequest{
		Query:      req.Query,
		ParamsJson: paramsJSON,
	})
	if err != nil {
		log.Printf("gateway: forward to %s failed: %v", addr, err)
		writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return 0, err
	}

	status := int(resp.GetHttpStatus())
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(resp.GetJsonBody())
	return status, nil
}

// clientAddr is the TCP peer. X-Forwarded-For is ignored.
func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (g *Gateway) handleHealth(w http.ResponseWriter, _ *http.Request) {
	nodes := g.cluster.AllNodes()
	primary := g.cluster.PrimaryID()
	status := "ok"
	if g.cluster.Primary() == nil {
		status = "no_leader"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      status,
		"primary":     primary,
		"nodes":       nodes,
		"replication": asyncReplicationNote,
	})
}
