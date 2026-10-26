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

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(errorJSON(msg))
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Route registration ---

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/query", s.handleQuery)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	return mux
}

// handleQuery parses and executes a Cypher-lite query.
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req queryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	status, body := s.executeQuery(req.Query, req.Params)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

// handleHealth returns node health info for operators and local checks.
// The gateway uses the gRPC Health RPC instead.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.healthView())
}
