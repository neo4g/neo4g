package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/neo4g/neo4g/rpcpb"
	"github.com/neo4g/neo4g/storage"
	"github.com/neo4g/neo4g/storage/wal"
)

const driftCheckInterval = 10 // check drift every N pull cycles

// queuedRecord is one primary WAL record received but not yet part of the
// durable applied prefix. Page images stay here until their commit or abort.
type queuedRecord struct {
	rec      *rpcpb.WALRecord
	resolved bool
}

// Replicator pulls WAL records from the primary and applies them locally.
//
// Async replication can lose commits from the last poll interval: the applied
// cursor trails the primary tip until the next successful pull, and a failover
// in that window promotes a replica that does not have those commits. Health
// publishes the cursor so the loss is the LSN gap, not a silent success.
type Replicator struct {
	engine       *Engine
	primaryAddr  string
	dataDir      string
	pollInterval time.Duration
	applied      atomic.Uint64
	pullCount    int
	pending      []queuedRecord

	mu    sync.Mutex
	conn  interface{ Close() error }
	api   rpcpb.NodeClient
	errMu sync.Mutex
	stuck error

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewReplicator creates a WAL pull replicator. The cursor is the persisted
// primary LSN, not this process's local WAL NextLSN.
func NewReplicator(engine *Engine, primaryAddr string, pollInterval time.Duration) *Replicator {
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}
	dataDir := ""
	if engine != nil {
		dataDir = engine.cfg.DataDir
	}
	r := &Replicator{
		engine:       engine,
		primaryAddr:  primaryAddr,
		dataDir:      dataDir,
		pollInterval: pollInterval,
		stopCh:       make(chan struct{}),
	}
	r.applied.Store(readAppliedLSN(dataDir))
	return r
}

// Applied returns the last contiguously applied primary LSN.
func (r *Replicator) Applied() wal.LSN {
	return wal.LSN(r.applied.Load())
}

// Err returns a sticky replication failure (log gap or checksum divergence).
// Transient dial errors are not sticky.
func (r *Replicator) Err() error {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	return r.stuck
}

func (r *Replicator) setStuck(err error) {
	r.errMu.Lock()
	r.stuck = err
	r.errMu.Unlock()
}

func (r *Replicator) clearStuck() {
	r.setStuck(nil)
}

// Start begins the replication loop in a background goroutine.
func (r *Replicator) Start() {
	r.wg.Add(1)
	go r.loop()
}

// Stop signals the replication loop to stop and waits for it to finish.
// It is safe to call more than once.
func (r *Replicator) Stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
	r.wg.Wait()
	r.mu.Lock()
	conn := r.conn
	r.conn = nil
	r.api = nil
	r.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

func (r *Replicator) client() (rpcpb.NodeClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	r.tick()
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.tick()
		}
	}
}

func (r *Replicator) tick() {
	if err := r.pull(); err != nil {
		log.Printf("neo4g: replication pull error: %v", err)
	}
	r.pullCount++
	if r.pullCount%driftCheckInterval != 0 {
		return
	}
	drifted, err := r.checkDrift()
	if err != nil {
		log.Printf("neo4g: drift check error: %v", err)
		return
	}
	if drifted {
		r.setStuck(fmt.Errorf("header checksum diverged from primary at applied LSN %d; not replaying historical deltas onto live pages", r.Applied()))
		log.Printf("neo4g: replication stuck: %v", r.Err())
	}
}

func (r *Replicator) pull() error {
	api, err := r.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	health, err := api.Health(ctx, &rpcpb.HealthRequest{})
	if err != nil {
		return fmt.Errorf("health %s: %w", r.primaryAddr, err)
	}
	tip := health.GetLsn()
	applied := r.applied.Load()
	// Caught-up replicas learn the tip from health and do not download the log.
	if applied >= tip {
		r.clearStuck()
		return nil
	}

	resp, err := api.PullWAL(ctx, &rpcpb.PullWALRequest{FromLsn: r.nextFetch()})
	if err != nil {
		if isWALGap(err) {
			r.setStuck(err)
		}
		return fmt.Errorf("PullWAL %s: %w", r.primaryAddr, err)
	}
	records := resp.GetRecords()
	if len(records) == 0 {
		err := fmt.Errorf("wal gap: applied LSN %d is behind primary tip %d and PullWAL returned no records (truncated log or reused LSN)", applied, tip)
		r.setStuck(err)
		return err
	}
	if err := r.ingest(records); err != nil {
		r.setStuck(err)
		return err
	}
	r.clearStuck()
	return nil
}

func (r *Replicator) nextFetch() uint64 {
	if n := len(r.pending); n > 0 {
		return r.pending[n-1].rec.GetLsn() + 1
	}
	return r.applied.Load() + 1
}

func (r *Replicator) ingest(records []*rpcpb.WALRecord) error {
	expect := r.nextFetch()
	for _, rec := range records {
		if rec.GetLsn() != expect {
			return fmt.Errorf("wal gap: expected primary LSN %d, got %d", expect, rec.GetLsn())
		}
		r.pending = append(r.pending, queuedRecord{rec: rec})
		expect++
	}
	if len(records) > 0 {
		log.Printf("neo4g: replication pulled %d WAL record(s) from %s (LSN %d..%d)",
			len(records), r.primaryAddr, records[0].GetLsn(), records[len(records)-1].GetLsn())
	}
	return r.resolvePending()
}

// resolvePending applies a transaction's page images only when its commit
// record arrives. Abort drops the buffered images; the delta already carries
// before-bytes, but those bytes are not installed, so no undo pass is required.
func (r *Replicator) resolvePending() error {
	for {
		progressed := false
		for i := range r.pending {
			if r.pending[i].resolved {
				continue
			}
			rec := r.pending[i].rec
			switch byte(rec.GetType()) {
			case wal.RecordUpdateDelta, wal.RecordUpdate:
				// Wait for the commit or abort that covers this image.
			case wal.RecordCommit:
				if err := r.applyTx(rec.GetTxId()); err != nil {
					return err
				}
				if err := r.logControl(rec, true); err != nil {
					return err
				}
				r.pending[i].resolved = true
				progressed = true
			case wal.RecordAbort:
				r.discardTx(rec.GetTxId())
				if err := r.logControl(rec, false); err != nil {
					return err
				}
				r.pending[i].resolved = true
				progressed = true
			case wal.RecordCheckpoint:
				if err := r.logControl(rec, false); err != nil {
					return err
				}
				r.pending[i].resolved = true
				progressed = true
			default:
				log.Printf("neo4g: skipping unknown WAL record type %d at LSN %d", rec.GetType(), rec.GetLsn())
				r.pending[i].resolved = true
				progressed = true
			}
		}
		if err := r.advanceApplied(); err != nil {
			return err
		}
		if !progressed {
			return nil
		}
	}
}

func (r *Replicator) applyTx(txID uint64) error {
	// The catalog record is logged at commit, after the page images. Install
	// it first so pageCount covers those pages. Extending the file first would
	// rewrite the catalog the header delta still has to match.
	if err := r.applyTxPages(txID, true); err != nil {
		return err
	}
	return r.applyTxPages(txID, false)
}

func (r *Replicator) applyTxPages(txID uint64, headerOnly bool) error {
	for i := range r.pending {
		rec := r.pending[i].rec
		if r.pending[i].resolved || rec.GetTxId() != txID || !isPageRecord(byte(rec.GetType())) {
			continue
		}
		isHeader := storage.PageID(rec.GetPageId()) == storage.HeaderPageID
		if isHeader != headerOnly {
			continue
		}
		if err := r.applyPageRecord(rec); err != nil {
			return fmt.Errorf("apply LSN %d: %w", rec.GetLsn(), err)
		}
		r.pending[i].resolved = true
	}
	return nil
}

func (r *Replicator) discardTx(txID uint64) {
	for i := range r.pending {
		rec := r.pending[i].rec
		if rec.GetTxId() == txID && isPageRecord(byte(rec.GetType())) {
			r.pending[i].resolved = true
		}
	}
}

func (r *Replicator) advanceApplied() error {
	n := 0
	var newest uint64
	for n < len(r.pending) && r.pending[n].resolved {
		newest = r.pending[n].rec.GetLsn()
		n++
	}
	if n == 0 || newest == r.applied.Load() {
		return nil
	}
	if err := writeAppliedLSN(r.dataDir, newest); err != nil {
		return fmt.Errorf("persist applied LSN %d: %w", newest, err)
	}
	r.pending = append([]queuedRecord(nil), r.pending[n:]...)
	r.applied.Store(newest)
	log.Printf("neo4g: applied primary LSN through %d", newest)
	return nil
}

func (r *Replicator) applyPageRecord(rec *rpcpb.WALRecord) error {
	pool := r.engine.Pool()
	pageID := storage.PageID(rec.GetPageId())
	if pageID != storage.HeaderPageID {
		// Redo extends the file for the same reason: the page image can
		// precede the catalog record that publishes the new page count.
		if err := r.engine.DiskManager().EnsurePage(pageID); err != nil {
			return fmt.Errorf("ensure page %d: %w", pageID, err)
		}
	}
	page, err := pool.FetchPage(pageID)
	if err != nil {
		return fmt.Errorf("fetch page %d: %w", pageID, err)
	}
	changed, applyErr := r.installPage(page, rec)
	if changed && pageID == storage.HeaderPageID {
		r.engine.DiskManager().RefreshHeaderFromPage(page)
		if idx := r.engine.IndexManager(); idx != nil {
			idx.RefreshRoots(r.engine.DiskManager())
		}
	}
	unpinErr := pool.UnpinPage(pageID, changed)
	if applyErr != nil {
		return applyErr
	}
	return unpinErr
}

func (r *Replicator) installPage(page *storage.Page, rec *rpcpb.WALRecord) (bool, error) {
	switch byte(rec.GetType()) {
	case wal.RecordUpdateDelta:
		return r.installDelta(page, rec)
	case wal.RecordUpdate:
		return r.installFull(page, rec)
	default:
		return false, nil
	}
}

func (r *Replicator) installDelta(page *storage.Page, rec *rpcpb.WALRecord) (bool, error) {
	delta := rec.GetData()
	matchesBefore, matchesAfter := deltaState(page, delta)
	if !matchesBefore && !matchesAfter && storage.PageID(rec.GetPageId()) == storage.HeaderPageID {
		// Commit logs the catalog from the last flushed header. Bootstrap
		// advances the in-memory image and leaves that flushed page behind,
		// so the live frame is not the delta's before-image.
		diskHeader, err := r.engine.DiskManager().ReadPage(storage.HeaderPageID)
		if err != nil {
			return false, err
		}
		if before, after := deltaState(diskHeader, delta); before {
			copy(page[:], diskHeader[:])
			matchesBefore, matchesAfter = before, after
		}
	}
	if matchesBefore && matchesAfter {
		return false, nil
	}
	if matchesAfter {
		return false, nil
	}
	if !matchesBefore {
		return false, fmt.Errorf("page %d does not match delta before-image at LSN %d", rec.GetPageId(), rec.GetLsn())
	}
	before := *page
	after := wal.ApplyDelta(&before, delta)
	if err := r.logLocal(wal.LSN(rec.GetLsn()), func() (wal.LSN, error) {
		w := r.engine.WAL()
		return w.LogUpdate(storage.TxID(rec.GetTxId()), storage.PageID(rec.GetPageId()), &before, &after)
	}); err != nil {
		return false, fmt.Errorf("write local WAL: %w", err)
	}
	copy(page[:], after[:])
	return true, nil
}

func (r *Replicator) installFull(page *storage.Page, rec *rpcpb.WALRecord) (bool, error) {
	data := rec.GetData()
	if len(data) < 2*storage.PageSize {
		return false, nil
	}
	before := data[:storage.PageSize]
	after := data[storage.PageSize : 2*storage.PageSize]
	if bytes.Equal(page[:], after) {
		return false, nil
	}
	if !bytes.Equal(page[:], before) {
		return false, fmt.Errorf("page %d does not match full before-image at LSN %d", rec.GetPageId(), rec.GetLsn())
	}
	var beforePage, afterPage storage.Page
	copy(beforePage[:], before)
	copy(afterPage[:], after)
	if err := r.logLocal(wal.LSN(rec.GetLsn()), func() (wal.LSN, error) {
		w := r.engine.WAL()
		return w.LogUpdate(storage.TxID(rec.GetTxId()), storage.PageID(rec.GetPageId()), &beforePage, &afterPage)
	}); err != nil {
		return false, fmt.Errorf("write local WAL: %w", err)
	}
	copy(page[:], afterPage[:])
	return true, nil
}

func (r *Replicator) logControl(rec *rpcpb.WALRecord, forceSync bool) error {
	primaryLSN := wal.LSN(rec.GetLsn())
	err := r.logLocal(primaryLSN, func() (wal.LSN, error) {
		w := r.engine.WAL()
		if w == nil {
			return 0, nil
		}
		txID := storage.TxID(rec.GetTxId())
		switch byte(rec.GetType()) {
		case wal.RecordCommit:
			return w.LogCommit(txID)
		case wal.RecordAbort:
			return w.LogAbort(txID)
		case wal.RecordCheckpoint:
			return w.LogCheckpoint()
		default:
			return 0, nil
		}
	})
	if err != nil {
		return fmt.Errorf("write local WAL: %w", err)
	}
	if forceSync && r.engine.WAL() != nil {
		if err := r.engine.WAL().Sync(); err != nil {
			return fmt.Errorf("sync WAL: %w", err)
		}
	}
	return nil
}

// logLocal writes one local WAL record under the primary's LSN. A retry after
// the record was already appended sees NextLSN past that LSN and skips it, so
// page images are not paired with a second local LSN.
func (r *Replicator) logLocal(primary wal.LSN, write func() (wal.LSN, error)) error {
	w := r.engine.WAL()
	if w == nil || primary == 0 {
		return nil
	}
	next := w.NextLSN()
	if next > primary {
		return nil
	}
	if next < primary {
		w.SetNextLSN(primary)
	}
	_, err := write()
	return err
}

// checkDrift compares the local header checksum against the primary's.
// A mismatch is reported and left alone. Replaying the log from LSN 0 onto
// live pages is not a snapshot restore.
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

	// A primary that is ahead is simply not caught up yet. Two nodes that have
	// not applied or committed anything are not replicas of each other; their
	// header pages are initialized independently and must not count as drift.
	if primary.GetLsn() != uint64(r.Applied()) || r.Applied() == 0 {
		return false, nil
	}

	localChecksum, err := r.localHeaderChecksum()
	if err != nil {
		return false, fmt.Errorf("local checksum: %w", err)
	}
	if localChecksum != primary.GetChecksum() {
		log.Printf("neo4g: drift detected — local checksum %s != primary %s (lsn local=%d primary=%d)",
			localChecksum, primary.GetChecksum(), r.Applied(), primary.GetLsn())
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

func isPageRecord(t byte) bool {
	return t == wal.RecordUpdateDelta || t == wal.RecordUpdate
}

func isWALGap(err error) bool {
	if err == nil {
		return false
	}
	if status.Code(err) == codes.FailedPrecondition {
		return true
	}
	return bytes.Contains([]byte(err.Error()), []byte("wal gap"))
}

// deltaState reports whether the live page matches the before-image, the
// after-image, or both (an empty or no-op delta).
func deltaState(page *storage.Page, delta []byte) (matchesBefore, matchesAfter bool) {
	if len(delta) == 0 {
		return true, true
	}
	matchesBefore, matchesAfter = true, true
	pos := 0
	off := 0
	for off+4 <= len(delta) {
		skip := int(binary.LittleEndian.Uint16(delta[off:]))
		length := int(binary.LittleEndian.Uint16(delta[off+2:]))
		off += 4
		pos += skip
		if length == 0 {
			continue
		}
		if pos+length > len(page) || off+2*length > len(delta) {
			return false, false
		}
		before := delta[off : off+length]
		after := delta[off+length : off+2*length]
		off += 2 * length
		if !bytes.Equal(page[pos:pos+length], before) {
			matchesBefore = false
		}
		if !bytes.Equal(page[pos:pos+length], after) {
			matchesAfter = false
		}
		pos += length
	}
	return matchesBefore, matchesAfter
}
