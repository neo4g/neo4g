package txn

import (
	"fmt"

	"github.com/neo4g/neo4g/storage"
	"github.com/neo4g/neo4g/storage/disk"
	"github.com/neo4g/neo4g/storage/wal"
)

func isUpdateRecord(t byte) bool {
	return t == wal.RecordUpdate || t == wal.RecordUpdateDelta
}

func redoPage(dm *disk.Manager, rec wal.Record) error {
	if err := dm.EnsurePage(rec.PageID); err != nil {
		return err
	}
	switch rec.Type {
	case wal.RecordUpdate:
		var afterPage storage.Page
		copy(afterPage[:], rec.AfterData)
		return dm.WritePage(rec.PageID, &afterPage)
	case wal.RecordUpdateDelta:
		current, err := dm.ReadPage(rec.PageID)
		if err != nil {
			return err
		}
		after := wal.ApplyDelta(current, rec.DeltaData)
		return dm.WritePage(rec.PageID, &after)
	}
	return nil
}

func undoPage(dm *disk.Manager, rec wal.Record) error {
	// Pages allocated by an uncommitted transaction sit past the committed
	// catalog. Leaving them unread is enough; the next allocation overwrites them.
	if uint32(rec.PageID) >= dm.PageCount() {
		return nil
	}
	switch rec.Type {
	case wal.RecordUpdate:
		var beforePage storage.Page
		copy(beforePage[:], rec.BeforeData)
		return dm.WritePage(rec.PageID, &beforePage)
	case wal.RecordUpdateDelta:
		current, err := dm.ReadPage(rec.PageID)
		if err != nil {
			return err
		}
		before := wal.ApplyDeltaUndo(current, rec.DeltaData)
		return dm.WritePage(rec.PageID, &before)
	}
	return nil
}

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

	// Replay only records after the last checkpoint. A checkpoint is taken
	// with no active transactions and after dirty pages are flushed, so the
	// log before that marker is history, not work left to redo.
	cp := -1
	for i, rec := range records {
		if rec.Type == wal.RecordCheckpoint {
			cp = i
		}
	}
	replay := records
	if cp >= 0 {
		replay = records[cp+1:]
	}
	if len(replay) == 0 {
		return 0, 0, nil
	}

	// --- Analysis phase ---
	committed := make(map[storage.TxID]bool)
	aborted := make(map[storage.TxID]bool)
	for _, rec := range replay {
		switch rec.Type {
		case wal.RecordCommit:
			committed[rec.TxID] = true
		case wal.RecordAbort:
			aborted[rec.TxID] = true
		}
	}

	// --- Redo phase ---
	redoneSet := make(map[storage.TxID]bool)
	for _, rec := range replay {
		if !isUpdateRecord(rec.Type) {
			continue
		}
		if !committed[rec.TxID] {
			continue
		}
		if err := redoPage(dm, rec); err != nil {
			return redone, undone, fmt.Errorf("redo page %d (tx %d): %w", rec.PageID, rec.TxID, err)
		}
		redoneSet[rec.TxID] = true
	}
	redone = len(redoneSet)

	// --- Undo phase ---
	undoneSet := make(map[storage.TxID]bool)
	for i := len(replay) - 1; i >= 0; i-- {
		rec := replay[i]
		if !isUpdateRecord(rec.Type) {
			continue
		}
		if committed[rec.TxID] || aborted[rec.TxID] {
			continue
		}
		if err := undoPage(dm, rec); err != nil {
			return redone, undone, fmt.Errorf("undo page %d (tx %d): %w", rec.PageID, rec.TxID, err)
		}
		undoneSet[rec.TxID] = true
	}
	undone = len(undoneSet)

	if err := dm.Sync(); err != nil {
		return redone, undone, fmt.Errorf("sync after recovery: %w", err)
	}

	// Append a checkpoint so the next open does not replay these records,
	// without discarding the log or rewinding LSNs.
	if _, err := w.LogCheckpoint(); err != nil {
		return redone, undone, fmt.Errorf("checkpoint after recovery: %w", err)
	}
	if err := w.Sync(); err != nil {
		return redone, undone, fmt.Errorf("sync checkpoint after recovery: %w", err)
	}

	return redone, undone, nil
}
