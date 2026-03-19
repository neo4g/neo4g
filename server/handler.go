package server

import (
	"encoding/json"
	"net/http"

	"github.com/seankohjs/neo4g/parser"
)

// queryRequest is the JSON body for POST /api/query.
type queryRequest struct {
	Query  string                 `json:"query"`
	Params map[string]interface{} `json:"params,omitempty"`
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

	result, err := s.executor.Execute(stmt, req.Params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "execute error: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}
