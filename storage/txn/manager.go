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
	id          storage.TxID
	state       TxState
	lockedPages map[storage.PageID]bool
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
func NewManager(w *wal.WAL, p *pool.Pool, dm *disk.Manager) *Manager {
	return &Manager{
		wal:       w,
		pool:      p,
		disk:      dm,
		nextTxID:  1,
		active:    make(map[storage.TxID]*txEntry),
		pageLocks: make(map[storage.PageID]storage.TxID),
	}
}

// Begin starts a new transaction and returns its ID.
func (tm *Manager) Begin() storage.TxID {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	id := tm.nextTxID
	tm.nextTxID++

	tm.active[id] = &txEntry{
		id:          id,
		state:       TxStateActive,
		lockedPages: make(map[storage.PageID]bool),
	}
	return id
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

	if err := tm.disk.FlushHeader(); err != nil {
		return fmt.Errorf("flush header: %w", err)
	}

	if _, err := tm.wal.LogCommit(txID); err != nil {
		return fmt.Errorf("WAL log commit: %w", err)
	}
	if !tm.NoSync {
		if err := tm.wal.Sync(); err != nil {
			return fmt.Errorf("WAL sync: %w", err)
		}
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
		if err := undoPage(tm.disk, rec); err != nil {
			return fmt.Errorf("undo page %d: %w", rec.PageID, err)
		}
	}

	if _, err := tm.wal.LogAbort(txID); err != nil {
		return fmt.Errorf("WAL log abort: %w", err)
	}

	tx.state = TxStateAborted
	tm.releaseLocks(tx)
	delete(tm.active, txID)
	return nil
}

// Checkpoint flushes all dirty pages to disk and writes a checkpoint record.
func (tm *Manager) Checkpoint() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

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

	return tm.wal.Truncate()
}

func (tm *Manager) releaseLocks(tx *txEntry) {
	for pageID := range tx.lockedPages {
		delete(tm.pageLocks, pageID)
	}
}
