package txn

import (
	"path/filepath"
	"testing"

	"github.com/seankohjs/neo4g/storage"
	"github.com/seankohjs/neo4g/storage/disk"
	"github.com/seankohjs/neo4g/storage/pool"
	"github.com/seankohjs/neo4g/storage/wal"
)

func newTestTxEnv(t *testing.T) (*Manager, *wal.WAL, *pool.Pool, *disk.Manager) {
	t.Helper()
	dir := t.TempDir()

	dm, err := disk.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}

	w, err := wal.Open(filepath.Join(dir, "test.wal"))
	if err != nil {
		dm.Close()
		t.Fatal(err)
	}

	bp := pool.New(dm, 100)
	tm := NewManager(w, bp, dm)

	t.Cleanup(func() {
		w.Close()
		dm.Close()
	})
	return tm, w, bp, dm
}

func TestTxBeginCommit(t *testing.T) {
	tm, _, _, _ := newTestTxEnv(t)

	txID := tm.Begin()
	if txID == 0 {
		t.Fatal("expected non-zero TxID")
	}

	if err := tm.Commit(txID); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func TestTxBeginAbort(t *testing.T) {
	tm, _, _, dm := newTestTxEnv(t)

	pageID, page, _ := dm.AllocatePage()
	copy(page[:], "original")
	dm.WritePage(pageID, page)

	txID := tm.Begin()

	var before storage.Page
	copy(before[:], "original")
	var after storage.Page
	copy(after[:], "modified")
	tm.WritePage(txID, pageID, &before, &after)

	dm.WritePage(pageID, &after)

	if err := tm.Abort(txID); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	restored, _ := dm.ReadPage(pageID)
	if string(restored[:8]) != "original" {
		t.Errorf("expected 'original' after abort, got %q", string(restored[:8]))
	}
}

func TestTxWritePageLocking(t *testing.T) {
	tm, _, _, dm := newTestTxEnv(t)

	pageID, _, _ := dm.AllocatePage()

	tx1 := tm.Begin()
	tx2 := tm.Begin()

	var before, after storage.Page
	tm.WritePage(tx1, pageID, &before, &after)

	err := tm.WritePage(tx2, pageID, &before, &after)
	if err == nil {
		t.Fatal("expected lock conflict error")
	}

	tm.Commit(tx1)
	err = tm.WritePage(tx2, pageID, &before, &after)
	if err != nil {
		t.Fatalf("expected success after lock release: %v", err)
	}
	tm.Commit(tx2)
}

func TestTxCommitNotFound(t *testing.T) {
	tm, _, _, _ := newTestTxEnv(t)
	if err := tm.Commit(999); err == nil {
		t.Fatal("expected error committing non-existent tx")
	}
}

func TestTxCheckpoint(t *testing.T) {
	tm, w, _, dm := newTestTxEnv(t)

	pageID, _, _ := dm.AllocatePage()
	txID := tm.Begin()

	var before, after storage.Page
	copy(after[:], "checkpointed")
	tm.WritePage(txID, pageID, &before, &after)
	tm.Commit(txID)

	if err := tm.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	records, _ := w.ReadAll()
	if len(records) != 0 {
		t.Errorf("expected 0 WAL records after checkpoint, got %d", len(records))
	}
}

// --- WAL-specific tests ---

func TestWALWriteAndRead(t *testing.T) {
	dir := t.TempDir()
	w, err := wal.Open(filepath.Join(dir, "test.wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	var before, after storage.Page
	copy(before[:], "before")
	copy(after[:], "after")

	lsn, err := w.LogUpdate(1, storage.PageID(5), &before, &after)
	if err != nil {
		t.Fatalf("LogUpdate: %v", err)
	}
	if lsn == 0 {
		t.Error("expected non-zero LSN")
	}

	w.LogCommit(1)

	records, err := w.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].Type != wal.RecordUpdateDelta {
		t.Errorf("expected UPDATE_DELTA record, got type %d", records[0].Type)
	}
	if records[1].Type != wal.RecordCommit {
		t.Errorf("expected COMMIT record, got type %d", records[1].Type)
	}
	// Verify delta can reconstruct both directions
	reconstructed := wal.ApplyDelta(&before, records[0].DeltaData)
	if string(reconstructed[:5]) != "after" {
		t.Errorf("delta redo mismatch")
	}
	undone := wal.ApplyDeltaUndo(&after, records[0].DeltaData)
	if string(undone[:6]) != "before" {
		t.Errorf("delta undo mismatch")
	}
}

func TestWALTruncate(t *testing.T) {
	dir := t.TempDir()
	w, _ := wal.Open(filepath.Join(dir, "test.wal"))
	defer w.Close()

	w.LogCommit(1)
	w.Truncate()

	records, _ := w.ReadAll()
	if len(records) != 0 {
		t.Errorf("expected 0 records after truncate, got %d", len(records))
	}
}

// --- Recovery tests ---

func TestRecoveryRedoCommitted(t *testing.T) {
	dir := t.TempDir()
	dm, _ := disk.Open(filepath.Join(dir, "test.db"))
	w, _ := wal.Open(filepath.Join(dir, "test.wal"))

	pageID, _, _ := dm.AllocatePage()

	var before, after storage.Page
	copy(after[:], "committed data")
	w.LogUpdate(1, pageID, &before, &after)
	w.LogCommit(1)
	w.Sync()

	w.Close()
	dm.Close()

	dm2, _ := disk.Open(filepath.Join(dir, "test.db"))
	w2, _ := wal.Open(filepath.Join(dir, "test.wal"))
	defer dm2.Close()
	defer w2.Close()

	redone, undone, err := Recover(w2, dm2)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if redone != 1 {
		t.Errorf("expected 1 redone tx, got %d", redone)
	}
	if undone != 0 {
		t.Errorf("expected 0 undone tx, got %d", undone)
	}

	page, _ := dm2.ReadPage(pageID)
	if string(page[:14]) != "committed data" {
		t.Errorf("expected 'committed data', got %q", string(page[:14]))
	}
}

func TestRecoveryUndoUncommitted(t *testing.T) {
	dir := t.TempDir()
	dm, _ := disk.Open(filepath.Join(dir, "test.db"))
	w, _ := wal.Open(filepath.Join(dir, "test.wal"))

	pageID, page, _ := dm.AllocatePage()
	copy(page[:], "original")
	dm.WritePage(pageID, page)

	var before storage.Page
	copy(before[:], "original")
	var after storage.Page
	copy(after[:], "uncommitted")
	w.LogUpdate(1, pageID, &before, &after)

	dm.WritePage(pageID, &after)
	w.Sync()

	w.Close()
	dm.Close()

	dm2, _ := disk.Open(filepath.Join(dir, "test.db"))
	w2, _ := wal.Open(filepath.Join(dir, "test.wal"))
	defer dm2.Close()
	defer w2.Close()

	redone, undone, err := Recover(w2, dm2)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if redone != 0 {
		t.Errorf("expected 0 redone, got %d", redone)
	}
	if undone != 1 {
		t.Errorf("expected 1 undone, got %d", undone)
	}

	page2, _ := dm2.ReadPage(pageID)
	if string(page2[:8]) != "original" {
		t.Errorf("expected 'original' after undo, got %q", string(page2[:8]))
	}
}

func TestRecoveryCleanShutdown(t *testing.T) {
	dir := t.TempDir()
	dm, _ := disk.Open(filepath.Join(dir, "test.db"))
	w, _ := wal.Open(filepath.Join(dir, "test.wal"))
	defer dm.Close()
	defer w.Close()

	redone, undone, err := Recover(w, dm)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if redone != 0 || undone != 0 {
		t.Errorf("expected 0/0 for clean shutdown, got %d/%d", redone, undone)
	}
}
