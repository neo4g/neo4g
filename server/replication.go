package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/seankohjs/neo4g/rpcpb"
	"github.com/seankohjs/neo4g/storage"
	"github.com/seankohjs/neo4g/storage/wal"
)

const driftCheckInterval = 10 // check drift every N pull cycles

// Replicator pulls WAL records from the primary and applies them locally.
type Replicator struct {
	engine       *Engine
	primaryAddr  string
	pollInterval time.Duration
	appliedLSN   wal.LSN
	pullCount    int // counts pull cycles for periodic drift check
	conn         *grpc.ClientConn
	api          rpcpb.NodeClient

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
	if r.conn != nil {
		r.conn.Close()
		r.conn = nil
		r.api = nil
	}
}

func (r *Replicator) client() (rpcpb.NodeClient, error) {
	if r.api != nil {
		return r.api, nil
	}
	conn, err := rpcpb.Dial(r.primaryAddr)
	if err != nil {
		return nil, err
	}
	r.conn = conn
	r.api = rpcpb.NewNodeClient(conn)
	return r.api, nil
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
			r.pullCount++
			if r.pullCount%driftCheckInterval == 0 {
				if drifted, err := r.checkDrift(); err != nil {
					log.Printf("neo4g: drift check error: %v", err)
				} else if drifted {
					log.Println("neo4g: drift detected — starting full re-sync from LSN 1")
					r.appliedLSN = 0
					if err := r.pull(); err != nil {
						log.Printf("neo4g: re-sync pull error: %v", err)
					} else {
						log.Printf("neo4g: re-sync complete, applied up to LSN %d", r.appliedLSN)
					}
				}
			}
		}
	}
}

func (r *Replicator) pull() error {
	api, err := r.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := api.PullWAL(ctx, &rpcpb.PullWALRequest{FromLsn: uint64(r.appliedLSN + 1)})
	if err != nil {
		return fmt.Errorf("PullWAL %s: %w", r.primaryAddr, err)
	}

	records := resp.GetRecords()
	if len(records) > 0 {
		log.Printf("neo4g: replication pulled %d WAL record(s) from %s (LSN %d..%d)",
			len(records), r.primaryAddr,
			records[0].GetLsn(), records[len(records)-1].GetLsn())
	}

	for _, rec := range records {
		if err := r.applyRecord(rec); err != nil {
			return fmt.Errorf("apply LSN %d: %w", rec.GetLsn(), err)
		}
		r.appliedLSN = wal.LSN(rec.GetLsn())
		log.Printf("neo4g: applied WAL record LSN=%d type=%s tx=%d page=%d",
			rec.GetLsn(), walTypeName(byte(rec.GetType())), rec.GetTxId(), rec.GetPageId())
	}

	return nil
}

func (r *Replicator) applyRecord(rec *rpcpb.WALRecord) error {
	pool := r.engine.Pool()
	w := r.engine.WAL()

	switch byte(rec.GetType()) {
	case wal.RecordUpdateDelta:
		delta := rec.GetData()
		pageID := storage.PageID(rec.GetPageId())

		page, err := pool.FetchPage(pageID)
		if err != nil {
			return fmt.Errorf("fetch page %d: %w", pageID, err)
		}

		applied := wal.ApplyDelta(page, delta)
		copy(page[:], applied[:])

		if pageID == storage.HeaderPageID {
			r.engine.DiskManager().RefreshHeaderFromPage(&applied)
			if idx := r.engine.IndexManager(); idx != nil {
				idx.RefreshRoots(r.engine.DiskManager())
			}
		}

		if err := pool.UnpinPage(pageID, true); err != nil {
			return fmt.Errorf("unpin page %d: %w", pageID, err)
		}

		if w != nil {
			if _, err := w.LogUpdate(storage.TxID(rec.GetTxId()), pageID, &storage.Page{}, &applied); err != nil {
				return fmt.Errorf("write local WAL: %w", err)
			}
		}

	case wal.RecordCommit:
		if w != nil {
			if _, err := w.LogCommit(storage.TxID(rec.GetTxId())); err != nil {
				return fmt.Errorf("write local WAL commit: %w", err)
			}
			if err := w.Sync(); err != nil {
				return fmt.Errorf("sync WAL: %w", err)
			}
		}

	case wal.RecordAbort:
		if w != nil {
			if _, err := w.LogAbort(storage.TxID(rec.GetTxId())); err != nil {
				return fmt.Errorf("write local WAL abort: %w", err)
			}
		}

	case wal.RecordCheckpoint:
		// skip checkpoints from primary

	case wal.RecordUpdate:
		data := rec.GetData()
		pageID := storage.PageID(rec.GetPageId())
		if len(data) >= 2*storage.PageSize {
			page, err := pool.FetchPage(pageID)
			if err != nil {
				return fmt.Errorf("fetch page %d: %w", pageID, err)
			}
			copy(page[:], data[storage.PageSize:2*storage.PageSize])
			if pageID == storage.HeaderPageID {
				r.engine.DiskManager().RefreshHeaderFromPage(page)
			}
			if err := pool.UnpinPage(pageID, true); err != nil {
				return fmt.Errorf("unpin page %d: %w", pageID, err)
			}
		}
	}

	return nil
}

// checkDrift compares the local header checksum against the primary's.
// Returns true if the replica has diverged and needs a full re-sync.
func (r *Replicator) checkDrift() (bool, error) {
	api, err := r.client()
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	primary, err := api.Checksum(ctx, &rpcpb.ChecksumRequest{})
	if err != nil {
		return false, fmt.Errorf("Checksum %s: %w", r.primaryAddr, err)
	}

	// If primary is ahead, that's not drift — we just haven't caught up yet.
	if primary.GetLsn() > uint64(r.appliedLSN) {
		return false, nil
	}

	// Compare header page checksums at the same LSN.
	localChecksum, err := r.localHeaderChecksum()
	if err != nil {
		return false, fmt.Errorf("local checksum: %w", err)
	}

	if localChecksum != primary.GetChecksum() {
		log.Printf("neo4g: drift detected — local checksum %s != primary %s (lsn local=%d primary=%d)",
			localChecksum, primary.GetChecksum(), r.appliedLSN, primary.GetLsn())
		return true, nil
	}
	return false, nil
}

func (r *Replicator) localHeaderChecksum() (string, error) {
	pool := r.engine.Pool()
	page, err := pool.FetchPage(storage.HeaderPageID)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write(page[:])
	pool.UnpinPage(storage.HeaderPageID, false)
	return hex.EncodeToString(h.Sum(nil)), nil
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
