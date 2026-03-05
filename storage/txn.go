package storage

// txn.go implements the Transaction Manager for ACID transactions.
//
// Design:
//   - Each transaction gets a unique TxID.
//   - Page-level locking: transactions hold locks on pages they modify.
//     This is coarser than row-level locking but simpler and sufficient for
//     the target scale.
//   - Steal/No-Force policy:
//     * Steal: dirty pages can be flushed to disk before the transaction commits
//       (the WAL before-image enables undo if the transaction aborts).
//     * No-Force: pages don't need to be flushed at commit time — only the
//       COMMIT WAL record needs to be durable. This improves commit latency.
//   - Recovery uses the WAL: redo committed transactions, undo uncommitted ones.
//
// Transaction lifecycle:
//   1. Begin()  → allocates a TxID
//   2. Read/write operations record WAL entries
//   3. Commit() → writes COMMIT to WAL, syncs, releases locks
//   4. Abort()  → restores before-images from WAL, writes ABORT, releases locks

import (
	"fmt"
	"sync"
)

// TxID uniquely identifies a transaction.
type TxID uint64

// TxState tracks the current state of a transaction.
type TxState int

const (
	TxStateActive    TxState = iota
	TxStateCommitted
	TxStateAborted
)

// txEntry tracks a single active transaction's state and modified pages.
type txEntry struct {
	id         TxID
	state      TxState
	lockedPages map[PageID]bool // pages locked by this transaction
}

// TxManager manages active transactions, coordinating with the WAL and
// buffer pool for ACID guarantees.
//
// Thread safety: TxManager is safe for concurrent use.
type TxManager struct {
	mu       sync.Mutex
	wal      *WAL
	pool     *BufferPool
	disk     *DiskManager
	nextTxID TxID
	active   map[TxID]*txEntry

	// Page-level lock table: tracks which transaction holds each page.
	// A page can only be held by one writer at a time.
	pageLocks map[PageID]TxID
}

// NewTxManager creates a transaction manager.
func NewTxManager(wal *WAL, pool *BufferPool, disk *DiskManager) *TxManager {
	return &TxManager{
		wal:       wal,
		pool:      pool,
		disk:      disk,
		nextTxID:  1,
		active:    make(map[TxID]*txEntry),
		pageLocks: make(map[PageID]TxID),
	}
}

// Begin starts a new transaction and returns its ID.
func (tm *TxManager) Begin() TxID {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	id := tm.nextTxID
	tm.nextTxID++

	tm.active[id] = &txEntry{
		id:          id,
		state:       TxStateActive,
		lockedPages: make(map[PageID]bool),
	}
	return id
}

// WritePage records a page modification within a transaction.
// The before-image and after-image are logged to the WAL.
// The page is locked by the transaction (exclusive page-level lock).
func (tm *TxManager) WritePage(txID TxID, pageID PageID, before, after *Page) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tx, ok := tm.active[txID]
	if !ok {
		return fmt.Errorf("transaction %d not found", txID)
	}
	if tx.state != TxStateActive {
		return fmt.Errorf("transaction %d is not active (state=%d)", txID, tx.state)
	}

	// Check page lock.
	if holder, locked := tm.pageLocks[pageID]; locked && holder != txID {
		return fmt.Errorf("page %d is locked by transaction %d", pageID, holder)
	}

	// Log to WAL.
	_, err := tm.wal.LogUpdate(txID, pageID, before, after)
	if err != nil {
		return fmt.Errorf("WAL log update: %w", err)
	}

	// Acquire lock.
	tm.pageLocks[pageID] = txID
	tx.lockedPages[pageID] = true

	return nil
}

// Commit makes a transaction's changes durable.
// The COMMIT record is written to WAL and synced to disk before returning.
func (tm *TxManager) Commit(txID TxID) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tx, ok := tm.active[txID]
	if !ok {
		return fmt.Errorf("transaction %d not found", txID)
	}
	if tx.state != TxStateActive {
		return fmt.Errorf("transaction %d is not active", txID)
	}

	// Write COMMIT record and sync WAL for durability.
	if _, err := tm.wal.LogCommit(txID); err != nil {
		return fmt.Errorf("WAL log commit: %w", err)
	}
	if err := tm.wal.Sync(); err != nil {
		return fmt.Errorf("WAL sync: %w", err)
	}

	tx.state = TxStateCommitted
	tm.releaseLocks(tx)
	delete(tm.active, txID)
	return nil
}

// Abort rolls back a transaction's changes by restoring before-images.
func (tm *TxManager) Abort(txID TxID) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tx, ok := tm.active[txID]
	if !ok {
		return fmt.Errorf("transaction %d not found", txID)
	}
	if tx.state != TxStateActive {
		return fmt.Errorf("transaction %d is not active", txID)
	}

	// Read WAL to find this transaction's updates and undo them.
	records, err := tm.wal.ReadAll()
	if err != nil {
		return fmt.Errorf("read WAL for abort: %w", err)
	}

	// Undo in reverse order.
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec.TxID != txID || rec.Type != WALRecordUpdate {
			continue
		}
		// Restore before-image.
		var beforePage Page
		copy(beforePage[:], rec.BeforeData)
		if err := tm.disk.WritePage(rec.PageID, &beforePage); err != nil {
			return fmt.Errorf("undo page %d: %w", rec.PageID, err)
		}
	}

	// Write ABORT record.
	if _, err := tm.wal.LogAbort(txID); err != nil {
		return fmt.Errorf("WAL log abort: %w", err)
	}

	tx.state = TxStateAborted
	tm.releaseLocks(tx)
	delete(tm.active, txID)
	return nil
}

// Checkpoint flushes all dirty pages to disk and writes a checkpoint record
// to the WAL. After a checkpoint, the WAL can be truncated since all data
// is safely on disk.
func (tm *TxManager) Checkpoint() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Flush all dirty pages.
	if err := tm.pool.FlushAll(); err != nil {
		return fmt.Errorf("flush all pages: %w", err)
	}

	// Sync data file.
	if err := tm.disk.Sync(); err != nil {
		return fmt.Errorf("sync data file: %w", err)
	}

	// Write checkpoint record.
	if _, err := tm.wal.LogCheckpoint(); err != nil {
		return fmt.Errorf("WAL log checkpoint: %w", err)
	}
	if err := tm.wal.Sync(); err != nil {
		return fmt.Errorf("WAL sync checkpoint: %w", err)
	}

	// Truncate WAL — all data is safely on disk.
	return tm.wal.Truncate()
}

// releaseLocks releases all page locks held by a transaction.
func (tm *TxManager) releaseLocks(tx *txEntry) {
	for pageID := range tx.lockedPages {
		delete(tm.pageLocks, pageID)
	}
}
