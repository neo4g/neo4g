package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/neo4g/neo4g/parser"
	"github.com/neo4g/neo4g/rpcpb"
	"github.com/neo4g/neo4g/storage"
	"github.com/neo4g/neo4g/storage/wal"
)

var (
	errNotPrimary        = errors.New("only primary serves WAL records")
	errWALDisabled       = errors.New("WAL is disabled")
	errStaleGeneration   = errors.New("stale role generation")
	errWALGap            = errors.New("wal gap")
	maxPullWALBytes      = 1 << 20
	asyncReplicationNote = "async: failover can lose commits from the last replica poll interval"
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
	if s.Role() == "primary" {
		s.notePrimaryTip()
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
	Generation  uint64 `json:"generation"`
	Replication string `json:"replication,omitempty"`
}

func (s *Server) healthView() healthView {
	status := "ok"
	role := s.Role()
	if role == "replica" {
		if s.getPrimaryAddr() == "" {
			status = "no_primary"
		}
		if err := s.replicationErr(); err != nil {
			status = "replication_stuck"
		}
	}
	return healthView{
		Status:      status,
		NodeID:      s.cfg.NodeID,
		Role:        role,
		LSN:         s.publishedLSN(),
		PrimaryAddr: s.getPrimaryAddr(),
		Generation:  s.fenceGeneration(),
		Replication: asyncReplicationNote,
	}
}

// publishedLSN is the cursor election and read-your-writes compare.
// Replicas report the persisted applied primary LSN. The primary reports the
// high-water tip, which survives a checkpoint that truncates the local file.
func (s *Server) publishedLSN() uint64 {
	if s.Role() == "replica" {
		if repl := s.currentRepl(); repl != nil {
			return uint64(repl.Applied())
		}
		return readAppliedLSN(s.cfg.DataDir)
	}
	return s.notePrimaryTip()
}

func (s *Server) notePrimaryTip() uint64 {
	if s.Role() != "primary" {
		return readAppliedLSN(s.cfg.DataDir)
	}
	s.tipMu.Lock()
	defer s.tipMu.Unlock()
	var walTip uint64
	if w := s.engine.WAL(); w != nil && w.NextLSN() > 0 {
		walTip = uint64(w.NextLSN() - 1)
	}
	stored := readAppliedLSN(s.cfg.DataDir)
	if walTip > stored {
		if err := writeAppliedLSN(s.cfg.DataDir, walTip); err != nil {
			log.Printf("neo4g: persist primary tip: %v", err)
		}
		return walTip
	}
	return stored
}

// continuePrimaryLSN makes the next local WAL record continue the published
// primary sequence. Truncate plus a restart would otherwise number from 1.
func (s *Server) continuePrimaryLSN() {
	w := s.engine.WAL()
	if w == nil {
		return
	}
	stored := readAppliedLSN(s.cfg.DataDir)
	if stored == 0 {
		return
	}
	w.SetNextLSN(wal.LSN(stored) + 1)
}

// applyRole promotes or demotes this node. primaryAddr is the primary's
// gRPC address and is required for a replica to start pulling WAL records.
// generation is the gateway's monotonic fence. A smaller generation is
// rejected. The same generation cannot flip primary and replica.
func (s *Server) applyRole(role, primaryAddr string, generation uint64) error {
	switch role {
	case "primary", "replica":
	default:
		return fmt.Errorf("role must be \"primary\" or \"replica\"")
	}

	s.mu.Lock()
	if generation < s.fenceGen {
		s.mu.Unlock()
		return errStaleGeneration
	}
	if generation == s.fenceGen && role != s.role && !(generation == 0 && s.fenceGen == 0) {
		s.mu.Unlock()
		return errStaleGeneration
	}
	if generation > s.fenceGen {
		s.fenceGen = generation
	}
	s.mu.Unlock()

	if role == "primary" {
		// Stop applying replicated pages before this node accepts local writes.
		s.stopReplicator()
		s.mu.Lock()
		if s.fenceGen > generation {
			s.mu.Unlock()
			return errStaleGeneration
		}
		s.role = "primary"
		s.primaryAddr = ""
		s.mu.Unlock()
		s.continuePrimaryLSN()
		return nil
	}

	if strings.TrimSpace(primaryAddr) == "" {
		return fmt.Errorf("replica requires primary address")
	}
	s.mu.Lock()
	if s.fenceGen > generation {
		s.mu.Unlock()
		return errStaleGeneration
	}
	addrChanged := primaryAddr != s.primaryAddr
	running := s.repl != nil
	s.role = "replica"
	s.primaryAddr = primaryAddr
	s.mu.Unlock()
	if addrChanged || !running {
		s.startReplicator()
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
	tip := s.primaryTip()
	// ReadFrom still scans the local file. The response is capped so one poll
	// cannot copy the whole log into a single 4 MiB gRPC message. A caught-up
	// replica learns the tip from health and does not call PullWAL.
	if uint64(from) > tip {
		return nil, nil
	}
	if len(records) == 0 || records[0].LSN != from {
		return nil, fmt.Errorf("%w: requested LSN %d is missing (tip %d); the log was truncated or LSNs were reused", errWALGap, from, tip)
	}

	out := make([]*rpcpb.WALRecord, 0, len(records))
	size := 0
	for _, rec := range records {
		var data []byte
		switch rec.Type {
		case wal.RecordUpdateDelta:
			data = append([]byte(nil), rec.DeltaData...)
		case wal.RecordUpdate:
			data = append(append([]byte(nil), rec.BeforeData...), rec.AfterData...)
		}
		n := len(data) + 64
		if len(out) > 0 && size+n > maxPullWALBytes {
			break
		}
		out = append(out, &rpcpb.WALRecord{
			Lsn:    uint64(rec.LSN),
			TxId:   uint64(rec.TxID),
			Type:   uint32(rec.Type),
			PageId: uint32(rec.PageID),
			Data:   data,
		})
		size += n
	}
	return out, nil
}

// primaryTip is the published primary LSN, including a cursor that outlives
// a truncated local WAL. Callers must already know this node is the primary.
func (s *Server) primaryTip() uint64 {
	return s.notePrimaryTip()
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
