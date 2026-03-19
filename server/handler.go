package server

import (
	"encoding/json"
	"net/http"
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

// handleQuery accepts a query string and (eventually) feeds it to the
// query parser/executor. For now it returns the raw query string back
// as acknowledgement — the query engine is not wired yet.
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

	// TODO: parse → plan → execute via query engine
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"query":  req.Query,
		"params": req.Params,
		"status": "query engine not connected yet",
	})
}
