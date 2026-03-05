package storage

// recovery.go implements crash recovery using the write-ahead log.
//
// Recovery algorithm (simplified ARIES):
//
//  1. Analysis phase: scan the WAL forward to determine which transactions
//     were committed and which were in-progress at the time of the crash.
//
//  2. Redo phase: scan forward and replay all UPDATE records from committed
//     transactions by writing their after-images to the data file. This
//     ensures all committed changes are reflected on disk.
//
//  3. Undo phase: scan backward and restore before-images for any UPDATE
//     records belonging to uncommitted transactions. This ensures no
//     partial/uncommitted changes remain on disk.
//
// After recovery, the WAL is truncated since all data is consistent on disk.
//
// Simplifications vs. full ARIES:
//   - We use full page images instead of physiological logging (larger WAL but
//     simpler recovery — no need to track individual operations within a page).
//   - No log-based checkpointing with active transaction table and dirty page
//     table — we simply scan from the beginning.
//   - Sufficient for the target scale where WAL files are manageable.

import "fmt"

// Recover performs crash recovery by replaying the WAL.
// It should be called at database startup before any operations.
//
// Returns the number of transactions redone and undone.
func Recover(wal *WAL, disk *DiskManager) (redone, undone int, err error) {
	records, err := wal.ReadAll()
	if err != nil {
		return 0, 0, fmt.Errorf("read WAL: %w", err)
	}

	if len(records) == 0 {
		return 0, 0, nil // clean shutdown, nothing to recover
	}

	// --- Analysis phase ---
	// Determine which transactions committed.
	committed := make(map[TxID]bool)
	aborted := make(map[TxID]bool)
	for _, rec := range records {
		switch rec.Type {
		case WALRecordCommit:
			committed[rec.TxID] = true
		case WALRecordAbort:
			aborted[rec.TxID] = true
		}
	}

	// --- Redo phase ---
	// Replay committed transactions' updates.
	redoneSet := make(map[TxID]bool)
	for _, rec := range records {
		if rec.Type != WALRecordUpdate {
			continue
		}
		if !committed[rec.TxID] {
			continue // skip uncommitted
		}

		var afterPage Page
		copy(afterPage[:], rec.AfterData)
		if err := disk.WritePage(rec.PageID, &afterPage); err != nil {
			return redone, undone, fmt.Errorf("redo page %d (tx %d): %w", rec.PageID, rec.TxID, err)
		}
		redoneSet[rec.TxID] = true
	}
	redone = len(redoneSet)

	// --- Undo phase ---
	// Roll back uncommitted (and non-aborted) transactions in reverse order.
	undoneSet := make(map[TxID]bool)
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec.Type != WALRecordUpdate {
			continue
		}
		if committed[rec.TxID] || aborted[rec.TxID] {
			continue // already handled
		}

		var beforePage Page
		copy(beforePage[:], rec.BeforeData)
		if err := disk.WritePage(rec.PageID, &beforePage); err != nil {
			return redone, undone, fmt.Errorf("undo page %d (tx %d): %w", rec.PageID, rec.TxID, err)
		}
		undoneSet[rec.TxID] = true
	}
	undone = len(undoneSet)

	// Sync data file to ensure all recovery writes are durable.
	if err := disk.Sync(); err != nil {
		return redone, undone, fmt.Errorf("sync after recovery: %w", err)
	}

	// Truncate WAL — database is now consistent.
	if err := wal.Truncate(); err != nil {
		return redone, undone, fmt.Errorf("truncate WAL: %w", err)
	}

	return redone, undone, nil
}
