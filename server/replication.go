package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/seankohjs/neo4g/storage"
	"github.com/seankohjs/neo4g/storage/wal"
)

// Replicator pulls WAL records from the primary and applies them locally.
type Replicator struct {
	engine       *Engine
	primaryAddr  string
	pollInterval time.Duration
	appliedLSN   wal.LSN
	client       *http.Client

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewReplicator creates a new WAL pull replicator.
func NewReplicator(engine *Engine, primaryAddr string, pollInterval time.Duration) *Replicator {
	var startLSN wal.LSN = 1
	if w := engine.WAL(); w != nil {
		startLSN = w.NextLSN()
	}
	return &Replicator{
		engine:       engine,
		primaryAddr:  primaryAddr,
		pollInterval: pollInterval,
		appliedLSN:   startLSN - 1,
		client:       &http.Client{Timeout: 5 * time.Second},
		stopCh:       make(chan struct{}),
	}
}

// Start begins the replication loop in a background goroutine.
func (r *Replicator) Start() {
	r.wg.Add(1)
	go r.loop()
}

// Stop signals the replication loop to stop and waits for it to finish.
func (r *Replicator) Stop() {
	close(r.stopCh)
	r.wg.Wait()
}

func (r *Replicator) loop() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			if err := r.pull(); err != nil {
				log.Printf("neo4g: replication pull error: %v", err)
			}
		}
	}
}

type walPullResponse struct {
	Records []walRecordJSON `json:"records"`
}

func (r *Replicator) pull() error {
	url := fmt.Sprintf("%s/api/wal/pull?from_lsn=%d", r.primaryAddr, r.appliedLSN+1)
	resp, err := r.client.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}

	var pullResp walPullResponse
	if err := json.NewDecoder(resp.Body).Decode(&pullResp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(pullResp.Records) > 0 {
		log.Printf("neo4g: replication pulled %d WAL record(s) from %s (LSN %d..%d)",
			len(pullResp.Records), r.primaryAddr,
			pullResp.Records[0].LSN, pullResp.Records[len(pullResp.Records)-1].LSN)
	}

	for _, rec := range pullResp.Records {
		if err := r.applyRecord(rec); err != nil {
			return fmt.Errorf("apply LSN %d: %w", rec.LSN, err)
		}
		r.appliedLSN = wal.LSN(rec.LSN)
		log.Printf("neo4g: applied WAL record LSN=%d type=%s tx=%d page=%d",
			rec.LSN, walTypeName(rec.Type), rec.TxID, rec.PageID)
	}

	return nil
}

func (r *Replicator) applyRecord(rec walRecordJSON) error {
	pool := r.engine.Pool()
	w := r.engine.WAL()

	switch rec.Type {
	case wal.RecordUpdateDelta:
		delta, err := base64.StdEncoding.DecodeString(rec.Data)
		if err != nil {
			return fmt.Errorf("decode delta: %w", err)
		}
		pageID := storage.PageID(rec.PageID)

		page, err := pool.FetchPage(pageID)
		if err != nil {
			return fmt.Errorf("fetch page %d: %w", pageID, err)
		}

		applied := wal.ApplyDelta(page, delta)
		copy(page[:], applied[:])

		if err := pool.UnpinPage(pageID, true); err != nil {
			return fmt.Errorf("unpin page %d: %w", pageID, err)
		}

		if w != nil {
			if _, err := w.LogUpdate(storage.TxID(rec.TxID), pageID, &storage.Page{}, &applied); err != nil {
				return fmt.Errorf("write local WAL: %w", err)
			}
		}

	case wal.RecordCommit:
		if w != nil {
			if _, err := w.LogCommit(storage.TxID(rec.TxID)); err != nil {
				return fmt.Errorf("write local WAL commit: %w", err)
			}
			if err := w.Sync(); err != nil {
				return fmt.Errorf("sync WAL: %w", err)
			}
		}

	case wal.RecordAbort:
		if w != nil {
			if _, err := w.LogAbort(storage.TxID(rec.TxID)); err != nil {
				return fmt.Errorf("write local WAL abort: %w", err)
			}
		}

	case wal.RecordCheckpoint:
		// skip checkpoints from primary

	case wal.RecordUpdate:
		data, err := base64.StdEncoding.DecodeString(rec.Data)
		if err != nil {
			return fmt.Errorf("decode data: %w", err)
		}
		pageID := storage.PageID(rec.PageID)
		if len(data) >= 2*storage.PageSize {
			page, err := pool.FetchPage(pageID)
			if err != nil {
				return fmt.Errorf("fetch page %d: %w", pageID, err)
			}
			copy(page[:], data[storage.PageSize:2*storage.PageSize])
			if err := pool.UnpinPage(pageID, true); err != nil {
				return fmt.Errorf("unpin page %d: %w", pageID, err)
			}
		}
	}

	return nil
}

func walTypeName(t byte) string {
	switch t {
	case wal.RecordUpdate:
		return "UPDATE"
	case wal.RecordUpdateDelta:
		return "DELTA"
	case wal.RecordCommit:
		return "COMMIT"
	case wal.RecordAbort:
		return "ABORT"
	case wal.RecordCheckpoint:
		return "CHECKPOINT"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", t)
	}
}
