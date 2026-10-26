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

	"github.com/seankohjs/neo4g/parser"
	"github.com/seankohjs/neo4g/rpcpb"
)

const readAfterWriteWindow = 2 * time.Second

// Gateway is the reverse-proxy that routes queries to Neo4g nodes.
type Gateway struct {
	cluster    *Cluster
	cfg        Config
	dialer     *Dialer
	http       *http.Server
	mu         sync.Mutex
	lastWrites map[string]time.Time // per-client last write time, keyed by IP
}

// New creates a Gateway wired to the given cluster and config.
// dialer reaches database nodes over gRPC. If dialer is nil, a private one is created.
func New(cfg Config, cluster *Cluster, dialer *Dialer) *Gateway {
	if dialer == nil {
		dialer = NewDialer()
	}
	g := &Gateway{
		cluster:    cluster,
		cfg:        cfg,
		dialer:     dialer,
		lastWrites: make(map[string]time.Time),
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
		g.mu.Lock()
		lw, ok := g.lastWrites[clientIP]
		recentWrite := ok && time.Since(lw) < readAfterWriteWindow
		g.mu.Unlock()

		if recentWrite {
			target = g.cluster.Primary()
		} else {
			target = g.cluster.NextReadNode()
		}
		if target == nil {
			writeError(w, http.StatusServiceUnavailable, "no healthy nodes available")
			return
		}
	} else {
		target = g.cluster.Primary()
		if target == nil {
			writeError(w, http.StatusServiceUnavailable, "no primary available")
			return
		}
		g.mu.Lock()
		g.lastWrites[clientIP] = time.Now()
		g.mu.Unlock()
	}

	g.forward(w, r.Context(), target.Addr, req)
}

func (g *Gateway) forward(w http.ResponseWriter, ctx context.Context, addr string, req queryRequest) {
	var paramsJSON []byte
	if req.Params != nil {
		var err error
		paramsJSON, err = json.Marshal(req.Params)
		if err != nil {
			writeError(w, http.StatusBadRequest, "encode params: "+err.Error())
			return
		}
	}

	api, err := g.dialer.Client(addr)
	if err != nil {
		log.Printf("gateway: dial %s failed: %v", addr, err)
		writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return
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
		return
	}

	status := int(resp.GetHttpStatus())
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(resp.GetJsonBody())
}

func clientAddr(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

// CleanupStaleClients removes expired entries from the per-client write tracker.
// Call periodically (e.g. from heartbeat goroutine).
func (g *Gateway) CleanupStaleClients() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for ip, t := range g.lastWrites {
		if time.Since(t) > readAfterWriteWindow {
			delete(g.lastWrites, ip)
		}
	}
}

func (g *Gateway) handleHealth(w http.ResponseWriter, _ *http.Request) {
	nodes := g.cluster.AllNodes()
	primary := g.cluster.PrimaryID()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"primary": primary,
		"nodes":   nodes,
	})
}
