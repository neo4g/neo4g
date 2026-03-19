package txn

import (
	"fmt"

	"github.com/seankohjs/neo4g/storage"
	"github.com/seankohjs/neo4g/storage/disk"
	"github.com/seankohjs/neo4g/storage/wal"
)

// Recover performs crash recovery by replaying the WAL.
// It should be called at database startup before any operations.
//
// Returns the number of transactions redone and undone.
func Recover(w *wal.WAL, dm *disk.Manager) (redone, undone int, err error) {
	records, err := w.ReadAll()
	if err != nil {
		return 0, 0, fmt.Errorf("read WAL: %w", err)
	}

	if len(records) == 0 {
		return 0, 0, nil
	}

	// --- Analysis phase ---
	committed := make(map[storage.TxID]bool)
	aborted := make(map[storage.TxID]bool)
	for _, rec := range records {
		switch rec.Type {
		case wal.RecordCommit:
			committed[rec.TxID] = true
		case wal.RecordAbort:
			aborted[rec.TxID] = true
		}
	}

	// --- Redo phase ---
	redoneSet := make(map[storage.TxID]bool)
	for _, rec := range records {
		if rec.Type != wal.RecordUpdate {
			continue
		}
		if !committed[rec.TxID] {
			continue
		}

		var afterPage storage.Page
		copy(afterPage[:], rec.AfterData)
		if err := dm.WritePage(rec.PageID, &afterPage); err != nil {
			return redone, undone, fmt.Errorf("redo page %d (tx %d): %w", rec.PageID, rec.TxID, err)
		}
		redoneSet[rec.TxID] = true
	}
	redone = len(redoneSet)

	// --- Undo phase ---
	undoneSet := make(map[storage.TxID]bool)
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec.Type != wal.RecordUpdate {
			continue
		}
		if committed[rec.TxID] || aborted[rec.TxID] {
			continue
		}

		var beforePage storage.Page
		copy(beforePage[:], rec.BeforeData)
		if err := dm.WritePage(rec.PageID, &beforePage); err != nil {
			return redone, undone, fmt.Errorf("undo page %d (tx %d): %w", rec.PageID, rec.TxID, err)
		}
		undoneSet[rec.TxID] = true
	}
	undone = len(undoneSet)

	if err := dm.Sync(); err != nil {
		return redone, undone, fmt.Errorf("sync after recovery: %w", err)
	}

	if err := w.Truncate(); err != nil {
		return redone, undone, fmt.Errorf("truncate WAL: %w", err)
	}

	return redone, undone, nil
}
