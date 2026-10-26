package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/neo4g/neo4g/parser"
	"github.com/neo4g/neo4g/rpcpb"
	"github.com/neo4g/neo4g/storage"
	"github.com/neo4g/neo4g/storage/wal"
)

var (
	errNotPrimary  = errors.New("only primary serves WAL records")
	errWALDisabled = errors.New("WAL is disabled")
)

func errorJSON(msg string) []byte {
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(map[string]string{"error": msg})
	return buf.Bytes()
}

// executeQuery runs a Cypher-lite statement and returns an HTTP status plus
// the JSON body a client would see from POST /api/query.
func (s *Server) executeQuery(query string, params map[string]interface{}) (int, []byte) {
	if query == "" {
		return http.StatusBadRequest, errorJSON(`missing "query" field`)
	}

	stmt, err := parser.Parse(query)
	if err != nil {
		return http.StatusBadRequest, errorJSON("parse error: " + err.Error())
	}

	if !stmt.IsReadOnly() && s.Role() == "replica" {
		return http.StatusForbidden, errorJSON("node is read-only replica")
	}

	result, err := s.executor.Execute(stmt, params)
	if err != nil {
		return http.StatusInternalServerError, errorJSON("execute error: " + err.Error())
	}

	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(map[string]interface{}{"data": result})
	return http.StatusOK, buf.Bytes()
}

type healthView struct {
	Status      string `json:"status"`
	NodeID      string `json:"node_id"`
	Role        string `json:"role"`
	LSN         uint64 `json:"lsn"`
	PrimaryAddr string `json:"primary_addr"`
}

func (s *Server) healthView() healthView {
	var lsn uint64
	if wl := s.engine.WAL(); wl != nil {
		lsn = uint64(wl.NextLSN()) - 1
	}
	return healthView{
		Status:      "ok",
		NodeID:      s.cfg.NodeID,
		Role:        s.Role(),
		LSN:         lsn,
		PrimaryAddr: s.getPrimaryAddr(),
	}
}

// applyRole promotes or demotes this node. primaryAddr is the primary's
// gRPC address and is required for a replica to start pulling WAL records.
func (s *Server) applyRole(role, primaryAddr string) error {
	switch role {
	case "primary", "replica":
	default:
		return fmt.Errorf("role must be \"primary\" or \"replica\"")
	}

	oldRole := s.Role()
	oldPrimary := s.getPrimaryAddr()
	s.SetRole(role)

	if role == "replica" && primaryAddr != "" {
		s.SetPrimaryAddr(primaryAddr)
		addrChanged := primaryAddr != oldPrimary
		if oldRole == "primary" || addrChanged || !s.replicatorRunning() {
			s.startReplicator()
		}
	}

	if role == "primary" && oldRole == "replica" {
		s.stopReplicator()
	}
	return nil
}

func (s *Server) pullWAL(from wal.LSN) ([]*rpcpb.WALRecord, error) {
	if s.Role() != "primary" {
		return nil, errNotPrimary
	}
	wl := s.engine.WAL()
	if wl == nil {
		return nil, errWALDisabled
	}
	if from == 0 {
		from = 1
	}

	records, err := wl.ReadFrom(from)
	if err != nil {
		return nil, fmt.Errorf("read WAL: %w", err)
	}

	out := make([]*rpcpb.WALRecord, 0, len(records))
	for _, rec := range records {
		var data []byte
		switch rec.Type {
		case wal.RecordUpdateDelta:
			data = append([]byte(nil), rec.DeltaData...)
		case wal.RecordUpdate:
			data = append(append([]byte(nil), rec.BeforeData...), rec.AfterData...)
		}
		out = append(out, &rpcpb.WALRecord{
			Lsn:    uint64(rec.LSN),
			TxId:   uint64(rec.TxID),
			Type:   uint32(rec.Type),
			PageId: uint32(rec.PageID),
			Data:   data,
		})
	}
	return out, nil
}

type checksumView struct {
	Checksum  string
	PageCount uint32
	LSN       uint64
}

func (s *Server) checksumView() (checksumView, error) {
	pool := s.engine.Pool()
	h := sha256.New()
	page, err := pool.FetchPage(storage.HeaderPageID)
	if err != nil {
		return checksumView{}, fmt.Errorf("fetch header: %w", err)
	}
	h.Write(page[:])
	pool.UnpinPage(storage.HeaderPageID, false)

	var lsn uint64
	if wl := s.engine.WAL(); wl != nil {
		lsn = uint64(wl.NextLSN()) - 1
	}
	return checksumView{
		Checksum:  hex.EncodeToString(h.Sum(nil)),
		PageCount: s.engine.DiskManager().PageCount(),
		LSN:       lsn,
	}, nil
}
