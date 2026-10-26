// Package txn implements the Transaction Manager for ACID transactions
// and crash recovery.
package txn

import (
	"fmt"
	"sync"

	"github.com/neo4g/neo4g/storage"
	"github.com/neo4g/neo4g/storage/disk"
	"github.com/neo4g/neo4g/storage/pool"
	"github.com/neo4g/neo4g/storage/wal"
)

// TxState tracks the current state of a transaction.
type TxState int

const (
	TxStateActive TxState = iota
	TxStateCommitted
	TxStateAborted
)

type txEntry struct {
	id           storage.TxID
	state        TxState
	lockedPages  map[storage.PageID]bool
	headerBefore storage.Page
}

// Manager manages active transactions, coordinating with the WAL and
// buffer pool for ACID guarantees.
//
// Thread safety: Manager is safe for concurrent use.
type Manager struct {
	mu       sync.Mutex
	wal      *wal.WAL
	pool     *pool.Pool
	disk     *disk.Manager
	nextTxID storage.TxID
	active   map[storage.TxID]*txEntry

	pageLocks map[storage.PageID]storage.TxID

	// NoSync skips fsync on every commit for higher throughput.
	// Data is still written to the WAL file, but not forced to durable
	// storage until the next Checkpoint or Sync call. A crash may lose
	// the last few committed transactions.
	NoSync bool
}

// NewManager creates a transaction manager.
// Data-page writes force the WAL first unless NoSync is set.
func NewManager(w *wal.WAL, p *pool.Pool, dm *disk.Manager) *Manager {
	tm := &Manager{
		wal:       w,
		pool:      p,
		disk:      dm,
		nextTxID:  1,
		active:    make(map[storage.TxID]*txEntry),
		pageLocks: make(map[storage.PageID]storage.TxID),
	}
	dm.SetBeforeDataWrite(func() error {
		if tm.NoSync {
			return nil
		}
		return w.Force()
	})
	return tm
}

// Begin starts a new transaction and returns its ID.
func (tm *Manager) Begin() storage.TxID {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	id := tm.nextTxID
	tm.nextTxID++

	tm.active[id] = &txEntry{
		id:           id,
		state:        TxStateActive,
		lockedPages:  make(map[storage.PageID]bool),
		headerBefore: dmHeader(tm.disk),
	}
	return id
}

func dmHeader(dm *disk.Manager) storage.Page {
	if dm == nil {
		return storage.Page{}
	}
	return dm.HeaderImage()
}

// WritePage records a page modification within a transaction.
func (tm *Manager) WritePage(txID storage.TxID, pageID storage.PageID, before, after *storage.Page) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tx, ok := tm.active[txID]
	if !ok {
		return fmt.Errorf("transaction %d not found", txID)
	}
	if tx.state != TxStateActive {
		return fmt.Errorf("transaction %d is not active (state=%d)", txID, tx.state)
	}

	if holder, locked := tm.pageLocks[pageID]; locked && holder != txID {
		return fmt.Errorf("page %d is locked by transaction %d", pageID, holder)
	}

	_, err := tm.wal.LogUpdate(txID, pageID, before, after)
	if err != nil {
		return fmt.Errorf("WAL log update: %w", err)
	}

	tm.pageLocks[pageID] = txID
	tx.lockedPages[pageID] = true

	return nil
}

// Commit makes a transaction's changes durable.
func (tm *Manager) Commit(txID storage.TxID) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tx, ok := tm.active[txID]
	if !ok {
		return fmt.Errorf("transaction %d not found", txID)
	}
	if tx.state != TxStateActive {
		return fmt.Errorf("transaction %d is not active", txID)
	}

	// Log the catalog (page 0) before the commit record, then sync both
	// before the header image is allowed to reach the data file.
	if tm.disk != nil {
		diskHeader, err := tm.disk.ReadPage(storage.HeaderPageID)
		if err != nil {
			return fmt.Errorf("read header: %w", err)
		}
		mem := tm.disk.HeaderImage()
		if *diskHeader != mem {
			if _, err := tm.wal.LogUpdate(txID, storage.HeaderPageID, diskHeader, &mem); err != nil {
				return fmt.Errorf("WAL log header: %w", err)
			}
		}
	}

	if _, err := tm.wal.LogCommit(txID); err != nil {
		return fmt.Errorf("WAL log commit: %w", err)
	}
	if !tm.NoSync {
		if err := tm.wal.Sync(); err != nil {
			return fmt.Errorf("WAL sync: %w", err)
		}
	}

	if err := tm.disk.FlushHeader(); err != nil {
		return fmt.Errorf("flush header: %w", err)
	}

	tx.state = TxStateCommitted
	tm.releaseLocks(tx)
	delete(tm.active, txID)
	return nil
}

// Abort rolls back a transaction's changes by restoring before-images.
func (tm *Manager) Abort(txID storage.TxID) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tx, ok := tm.active[txID]
	if !ok {
		return fmt.Errorf("transaction %d not found", txID)
	}
	if tx.state != TxStateActive {
		return fmt.Errorf("transaction %d is not active", txID)
	}

	records, err := tm.wal.ReadAll()
	if err != nil {
		return fmt.Errorf("read WAL for abort: %w", err)
	}

	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec.TxID != txID || !isUpdateRecord(rec.Type) {
			continue
		}
		if rec.PageID == storage.HeaderPageID {
			continue
		}
		if err := tm.undoRecord(rec); err != nil {
			return fmt.Errorf("undo page %d: %w", rec.PageID, err)
		}
	}

	// Catalog edits from this transaction never reached disk (they are logged
	// only at commit). Put the in-memory header back to the begin snapshot
	// so index roots, page chains, and ID counters roll back with the pages.
	if tm.disk != nil {
		tm.disk.RestoreHeader(&tx.headerBefore)
	}

	if _, err := tm.wal.LogAbort(txID); err != nil {
		return fmt.Errorf("WAL log abort: %w", err)
	}

	tx.state = TxStateAborted
	tm.releaseLocks(tx)
	delete(tm.active, txID)
	return nil
}

// Checkpoint flushes all dirty pages and appends a checkpoint marker.
// It refuses to run while a transaction is active, and it does not truncate
// the log or reuse LSNs. Callers that need the log forced still Sync it.
func (tm *Manager) Checkpoint() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if len(tm.active) > 0 {
		return fmt.Errorf("checkpoint refused: %d active transaction(s)", len(tm.active))
	}

	if err := tm.pool.FlushAll(); err != nil {
		return fmt.Errorf("flush all pages: %w", err)
	}

	if err := tm.disk.Sync(); err != nil {
		return fmt.Errorf("sync data file: %w", err)
	}

	if _, err := tm.wal.LogCheckpoint(); err != nil {
		return fmt.Errorf("WAL log checkpoint: %w", err)
	}
	if err := tm.wal.Sync(); err != nil {
		return fmt.Errorf("WAL sync checkpoint: %w", err)
	}
	return nil
}

// undoRecord installs the before-image into the buffer pool and onto disk
// so a later flush cannot write the after-image back.
func (tm *Manager) undoRecord(rec wal.Record) error {
	before, err := tm.beforeImage(rec)
	if err != nil {
		return err
	}
	if tm.pool != nil {
		tm.pool.Install(rec.PageID, &before)
	}
	return tm.disk.WritePage(rec.PageID, &before)
}

func (tm *Manager) beforeImage(rec wal.Record) (storage.Page, error) {
	switch rec.Type {
	case wal.RecordUpdate:
		var before storage.Page
		if len(rec.BeforeData) < storage.PageSize {
			return storage.Page{}, fmt.Errorf("short before-image")
		}
		copy(before[:], rec.BeforeData)
		return before, nil
	case wal.RecordUpdateDelta:
		current, err := tm.currentPage(rec.PageID)
		if err != nil {
			return storage.Page{}, err
		}
		return wal.ApplyDeltaUndo(&current, rec.DeltaData), nil
	default:
		return storage.Page{}, fmt.Errorf("record type %d is not an update", rec.Type)
	}
}

func (tm *Manager) currentPage(id storage.PageID) (storage.Page, error) {
	if tm.pool != nil {
		if pg, ok := tm.pool.Cached(id); ok {
			return pg, nil
		}
	}
	p, err := tm.disk.ReadPage(id)
	if err != nil {
		return storage.Page{}, err
	}
	return *p, nil
}

func (tm *Manager) releaseLocks(tx *txEntry) {
	for pageID := range tx.lockedPages {
		delete(tm.pageLocks, pageID)
	}
}
