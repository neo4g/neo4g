package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/seankohjs/neo4g/parser"
	"github.com/seankohjs/neo4g/storage"
	"github.com/seankohjs/neo4g/storage/wal"
)

// queryRequest is the JSON body for POST /api/query.
type queryRequest struct {
	Query  string                 `json:"query"`
	Params map[string]interface{} `json:"params,omitempty"`
}

// roleRequest is the JSON body for POST /api/role.
type roleRequest struct {
	Role        string `json:"role"`
	PrimaryAddr string `json:"primary_addr,omitempty"`
}

// walRecordJSON is the JSON representation of a WAL record for replication.
type walRecordJSON struct {
	LSN    uint64 `json:"lsn"`
	TxID   uint64 `json:"tx_id"`
	Type   byte   `json:"type"`
	PageID uint32 `json:"page_id"`
	Data   string `json:"data"`
}

// --- HTTP helpers ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{"data": v})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// --- Route registration ---

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/query", s.handleQuery)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/wal/pull", s.handleWALPull)
	mux.HandleFunc("POST /api/role", s.handleRole)
	mux.HandleFunc("GET /api/checksum", s.handleChecksum)
	return mux
}

// handleQuery parses and executes a Cypher-lite query.
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	var req queryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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

	if !stmt.IsReadOnly() && s.Role() == "replica" {
		writeError(w, http.StatusForbidden, "node is read-only replica")
		return
	}

	result, err := s.executor.Execute(stmt, req.Params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "execute error: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// handleHealth returns node health info for heartbeat checks.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	var lsn uint64
	if wl := s.engine.WAL(); wl != nil {
		lsn = uint64(wl.NextLSN()) - 1
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "ok",
		"node_id":      s.cfg.NodeID,
		"role":         s.Role(),
		"lsn":          lsn,
		"primary_addr": s.getPrimaryAddr(),
	})
}

// handleWALPull serves WAL records from a given LSN for replica replication.
func (s *Server) handleWALPull(w http.ResponseWriter, r *http.Request) {
	if s.Role() != "primary" {
		writeError(w, http.StatusForbidden, "only primary serves WAL records")
		return
	}

	wl := s.engine.WAL()
	if wl == nil {
		writeError(w, http.StatusServiceUnavailable, "WAL is disabled")
		return
	}

	fromLSN := wal.LSN(1)
	if v := r.URL.Query().Get("from_lsn"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid from_lsn: "+err.Error())
			return
		}
		fromLSN = wal.LSN(n)
	}

	records, err := wl.ReadFrom(fromLSN)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read WAL: "+err.Error())
		return
	}

	jsonRecs := make([]walRecordJSON, 0, len(records))
	for _, rec := range records {
		var data []byte
		switch rec.Type {
		case wal.RecordUpdateDelta:
			data = rec.DeltaData
		case wal.RecordUpdate:
			data = append(rec.BeforeData, rec.AfterData...)
		}
		jsonRecs = append(jsonRecs, walRecordJSON{
			LSN:    uint64(rec.LSN),
			TxID:   uint64(rec.TxID),
			Type:   rec.Type,
			PageID: uint32(rec.PageID),
			Data:   base64.StdEncoding.EncodeToString(data),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"records": jsonRecs})
}

// handleRole allows the gateway to promote or demote this node.
func (s *Server) handleRole(w http.ResponseWriter, r *http.Request) {
	var req roleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	switch req.Role {
	case "primary", "replica":
	default:
		writeError(w, http.StatusBadRequest, "role must be \"primary\" or \"replica\"")
		return
	}

	oldRole := s.Role()
	oldPrimary := s.getPrimaryAddr()
	s.SetRole(req.Role)

	if req.Role == "replica" && req.PrimaryAddr != "" {
		s.SetPrimaryAddr(req.PrimaryAddr)
		addrChanged := req.PrimaryAddr != oldPrimary
		if oldRole == "primary" || addrChanged || !s.replicatorRunning() {
			s.startReplicator()
		}
	}

	if req.Role == "primary" && oldRole == "replica" {
		s.stopReplicator()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"node_id": s.cfg.NodeID,
		"role":    req.Role,
	})
}

// handleChecksum returns a SHA-256 over the header page + page count.
// Replicas compare this against their own to detect drift.
func (s *Server) handleChecksum(w http.ResponseWriter, _ *http.Request) {
	pool := s.engine.Pool()
	dm := s.engine.DiskManager()
	pageCount := dm.PageCount()

	h := sha256.New()
	page, err := pool.FetchPage(storage.HeaderPageID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fetch header: "+err.Error())
		return
	}
	h.Write(page[:])
	pool.UnpinPage(storage.HeaderPageID, false)

	var lsn uint64
	if wl := s.engine.WAL(); wl != nil {
		lsn = uint64(wl.NextLSN()) - 1
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"checksum":   hex.EncodeToString(h.Sum(nil)),
		"page_count": pageCount,
		"lsn":        lsn,
	})
}
