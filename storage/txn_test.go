package storage

import (
	"path/filepath"
	"testing"
)

func newTestTxEnv(t *testing.T) (*TxManager, *WAL, *BufferPool, *DiskManager) {
	t.Helper()
	dir := t.TempDir()

	dm, err := OpenDiskManager(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}

	wal, err := OpenWAL(filepath.Join(dir, "test.wal"))
	if err != nil {
		dm.Close()
		t.Fatal(err)
	}

	bp := NewBufferPool(dm, 100)
	tm := NewTxManager(wal, bp, dm)

	t.Cleanup(func() {
		wal.Close()
		dm.Close()
	})
	return tm, wal, bp, dm
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

	// Allocate a page and write something.
	pageID, page, _ := dm.AllocatePage()
	copy(page[:], "original")
	dm.WritePage(pageID, page)

	txID := tm.Begin()

	// Record a modification.
	var before Page
	copy(before[:], "original")
	var after Page
	copy(after[:], "modified")
	tm.WritePage(txID, pageID, &before, &after)

	// Write the after-image to disk (simulating buffer pool flush).
	dm.WritePage(pageID, &after)

	// Abort should restore the before-image.
	if err := tm.Abort(txID); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	// Verify page is restored.
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

	var before, after Page
	// tx1 locks the page.
	tm.WritePage(tx1, pageID, &before, &after)

	// tx2 should be blocked from writing to the same page.
	err := tm.WritePage(tx2, pageID, &before, &after)
	if err == nil {
		t.Fatal("expected lock conflict error")
	}

	// After tx1 commits, tx2 should succeed.
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
	tm, wal, _, dm := newTestTxEnv(t)

	pageID, _, _ := dm.AllocatePage()
	txID := tm.Begin()

	var before, after Page
	copy(after[:], "checkpointed")
	tm.WritePage(txID, pageID, &before, &after)
	tm.Commit(txID)

	// Checkpoint should flush and truncate WAL.
	if err := tm.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	// WAL should be empty after checkpoint.
	records, _ := wal.ReadAll()
	if len(records) != 0 {
		t.Errorf("expected 0 WAL records after checkpoint, got %d", len(records))
	}
}

// --- WAL-specific tests ---

func TestWALWriteAndRead(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenWAL(filepath.Join(dir, "test.wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer wal.Close()

	var before, after Page
	copy(before[:], "before")
	copy(after[:], "after")

	lsn, err := wal.LogUpdate(1, PageID(5), &before, &after)
	if err != nil {
		t.Fatalf("LogUpdate: %v", err)
	}
	if lsn == 0 {
		t.Error("expected non-zero LSN")
	}

	wal.LogCommit(1)

	records, err := wal.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].Type != WALRecordUpdate {
		t.Errorf("expected UPDATE record, got type %d", records[0].Type)
	}
	if records[1].Type != WALRecordCommit {
		t.Errorf("expected COMMIT record, got type %d", records[1].Type)
	}
	if string(records[0].BeforeData[:6]) != "before" {
		t.Errorf("before-image mismatch")
	}
	if string(records[0].AfterData[:5]) != "after" {
		t.Errorf("after-image mismatch")
	}
}

func TestWALTruncate(t *testing.T) {
	dir := t.TempDir()
	wal, _ := OpenWAL(filepath.Join(dir, "test.wal"))
	defer wal.Close()

	wal.LogCommit(1)
	wal.Truncate()

	records, _ := wal.ReadAll()
	if len(records) != 0 {
		t.Errorf("expected 0 records after truncate, got %d", len(records))
	}
}

// --- Recovery tests ---

func TestRecoveryRedoCommitted(t *testing.T) {
	dir := t.TempDir()
	dm, _ := OpenDiskManager(filepath.Join(dir, "test.db"))
	wal, _ := OpenWAL(filepath.Join(dir, "test.wal"))

	// Allocate a page.
	pageID, _, _ := dm.AllocatePage()

	// Simulate: tx1 modified page and committed, but page wasn't flushed to disk.
	var before, after Page
	copy(after[:], "committed data")
	wal.LogUpdate(1, pageID, &before, &after)
	wal.LogCommit(1)
	wal.Sync()

	// Close and reopen (simulating crash — page was never written to disk).
	wal.Close()
	dm.Close()

	dm2, _ := OpenDiskManager(filepath.Join(dir, "test.db"))
	wal2, _ := OpenWAL(filepath.Join(dir, "test.wal"))
	defer dm2.Close()
	defer wal2.Close()

	redone, undone, err := Recover(wal2, dm2)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if redone != 1 {
		t.Errorf("expected 1 redone tx, got %d", redone)
	}
	if undone != 0 {
		t.Errorf("expected 0 undone tx, got %d", undone)
	}

	// Page should now have committed data.
	page, _ := dm2.ReadPage(pageID)
	if string(page[:14]) != "committed data" {
		t.Errorf("expected 'committed data', got %q", string(page[:14]))
	}
}

func TestRecoveryUndoUncommitted(t *testing.T) {
	dir := t.TempDir()
	dm, _ := OpenDiskManager(filepath.Join(dir, "test.db"))
	wal, _ := OpenWAL(filepath.Join(dir, "test.wal"))

	pageID, page, _ := dm.AllocatePage()
	copy(page[:], "original")
	dm.WritePage(pageID, page)

	// Simulate: tx1 modified page but did NOT commit (crash before commit).
	var before Page
	copy(before[:], "original")
	var after Page
	copy(after[:], "uncommitted")
	wal.LogUpdate(1, pageID, &before, &after)

	// Simulate the dirty page being flushed to disk before commit.
	dm.WritePage(pageID, &after)
	wal.Sync()

	wal.Close()
	dm.Close()

	dm2, _ := OpenDiskManager(filepath.Join(dir, "test.db"))
	wal2, _ := OpenWAL(filepath.Join(dir, "test.wal"))
	defer dm2.Close()
	defer wal2.Close()

	redone, undone, err := Recover(wal2, dm2)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if redone != 0 {
		t.Errorf("expected 0 redone, got %d", redone)
	}
	if undone != 1 {
		t.Errorf("expected 1 undone, got %d", undone)
	}

	// Page should be restored to original.
	page2, _ := dm2.ReadPage(pageID)
	if string(page2[:8]) != "original" {
		t.Errorf("expected 'original' after undo, got %q", string(page2[:8]))
	}
}

func TestRecoveryCleanShutdown(t *testing.T) {
	dir := t.TempDir()
	dm, _ := OpenDiskManager(filepath.Join(dir, "test.db"))
	wal, _ := OpenWAL(filepath.Join(dir, "test.wal"))
	defer dm.Close()
	defer wal.Close()

	// Empty WAL — clean shutdown.
	redone, undone, err := Recover(wal, dm)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if redone != 0 || undone != 0 {
		t.Errorf("expected 0/0 for clean shutdown, got %d/%d", redone, undone)
	}
}
