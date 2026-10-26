package txn

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neo4g/neo4g/storage"
	"github.com/neo4g/neo4g/storage/disk"
	"github.com/neo4g/neo4g/storage/pool"
	"github.com/neo4g/neo4g/storage/wal"
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
	if len(records) == 0 {
		t.Fatal("checkpoint must keep WAL history")
	}
	var sawCP bool
	for _, rec := range records {
		if rec.Type == wal.RecordCheckpoint {
			sawCP = true
		}
	}
	if !sawCP {
		t.Fatal("expected a checkpoint record")
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

func TestCheckpointRejectsActiveTx(t *testing.T) {
	tm, _, _, _ := newTestTxEnv(t)
	tx := tm.Begin()
	if err := tm.Checkpoint(); err == nil {
		t.Fatal("expected checkpoint to fail while a transaction is active")
	}
	if err := tm.Commit(tx); err != nil {
		t.Fatal(err)
	}
	if err := tm.Checkpoint(); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointLSNMonotonicAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	dm, err := disk.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	walPath := filepath.Join(dir, "test.wal")
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	bp := pool.New(dm, 8)
	tm := NewManager(w, bp, dm)

	pageID, _, _ := dm.AllocatePage()
	tx := tm.Begin()
	var before, after storage.Page
	copy(after[:], "kept")
	if err := tm.WritePage(tx, pageID, &before, &after); err != nil {
		t.Fatal(err)
	}
	if err := tm.Commit(tx); err != nil {
		t.Fatal(err)
	}
	if err := tm.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	want := w.NextLSN()
	if want <= 1 {
		t.Fatalf("expected LSN to advance, got %d", want)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dm.Close(); err != nil {
		t.Fatal(err)
	}

	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if w2.NextLSN() != want {
		t.Fatalf("reopen next LSN = %d, want %d", w2.NextLSN(), want)
	}
}

func TestWALForcedBeforeDataWrite(t *testing.T) {
	tm, w, bp, _ := newTestTxEnv(t)
	id, page, err := bp.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	before := *page
	copy(page[:], "logged-before-flush")
	tx := tm.Begin()
	if err := tm.WritePage(tx, id, &before, page); err != nil {
		t.Fatal(err)
	}
	if w.Durable() {
		t.Fatal("LogUpdate must stay buffered until a data write")
	}
	if err := bp.UnpinPage(id, true); err != nil {
		t.Fatal(err)
	}
	if err := bp.FlushPage(id); err != nil {
		t.Fatal(err)
	}
	if !w.Durable() {
		t.Fatal("flushing a data page must force the WAL")
	}
	if err := tm.Commit(tx); err != nil {
		t.Fatal(err)
	}
}

func TestNoSyncSkipsWALForce(t *testing.T) {
	tm, w, _, dm := newTestTxEnv(t)
	tm.NoSync = true
	tx := tm.Begin()
	if err := dm.SetNodeIndexRoot(7); err != nil {
		t.Fatal(err)
	}
	if err := tm.Commit(tx); err != nil {
		t.Fatal(err)
	}
	if w.Durable() {
		t.Fatal("NoSync commit must not force the WAL")
	}
}

func TestAbortRestoresPoolAndHeader(t *testing.T) {
	tm, _, bp, dm := newTestTxEnv(t)
	id, page, err := bp.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	copy(page[:], "original")
	if err := bp.UnpinPage(id, true); err != nil {
		t.Fatal(err)
	}
	if err := bp.FlushPage(id); err != nil {
		t.Fatal(err)
	}

	page, err = bp.FetchPage(id)
	if err != nil {
		t.Fatal(err)
	}
	before := *page
	copy(page[:], "modified")
	tx := tm.Begin()
	if err := dm.SetNodeIndexRoot(99); err != nil {
		t.Fatal(err)
	}
	if err := tm.WritePage(tx, id, &before, page); err != nil {
		t.Fatal(err)
	}
	if err := bp.UnpinPage(id, true); err != nil {
		t.Fatal(err)
	}
	modified := *page
	if err := dm.WritePage(id, &modified); err != nil {
		t.Fatal(err)
	}

	if err := tm.Abort(tx); err != nil {
		t.Fatal(err)
	}
	if dm.NodeIndexRoot() != 0 {
		t.Fatalf("header root = %d, want 0 after abort", dm.NodeIndexRoot())
	}
	got, err := bp.FetchPage(id)
	if err != nil {
		t.Fatal(err)
	}
	defer bp.UnpinPage(id, false)
	if string(got[:8]) != "original" {
		t.Fatalf("pool page = %q, want original", got[:8])
	}
	if err := bp.FlushAll(); err != nil {
		t.Fatal(err)
	}
	diskPage, err := dm.ReadPage(id)
	if err != nil {
		t.Fatal(err)
	}
	if string(diskPage[:8]) != "original" {
		t.Fatalf("disk page = %q after flush, want original", diskPage[:8])
	}
}

func TestHeaderRedoAndExtendFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	walPath := filepath.Join(dir, "test.wal")

	dm, err := disk.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	bp := pool.New(dm, 8)
	tm := NewManager(w, bp, dm)

	tx := tm.Begin()
	if err := dm.SetNodeIndexRoot(42); err != nil {
		t.Fatal(err)
	}
	if _, err := dm.NextNodeID(); err != nil {
		t.Fatal(err)
	}
	if err := tm.Commit(tx); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dm.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(dbPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	var zero [4]byte
	if _, err := f.WriteAt(zero[:], 32); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dm2, err := disk.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dm2.Close()
	if dm2.NodeIndexRoot() != 0 {
		t.Fatalf("clobber failed, root = %d", dm2.NodeIndexRoot())
	}
	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if _, _, err := Recover(w2, dm2); err != nil {
		t.Fatal(err)
	}
	if dm2.NodeIndexRoot() != 42 {
		t.Fatalf("recovered root = %d, want 42", dm2.NodeIndexRoot())
	}
	if dm2.PageCount() < 1 {
		t.Fatal("page count missing")
	}

	// A committed record for a page past the catalog must extend the file.
	extDir := t.TempDir()
	extDB := filepath.Join(extDir, "ext.db")
	extWAL := filepath.Join(extDir, "ext.wal")
	dm3, err := disk.Open(extDB)
	if err != nil {
		t.Fatal(err)
	}
	w3, err := wal.Open(extWAL)
	if err != nil {
		t.Fatal(err)
	}
	var before, after storage.Page
	copy(after[:], "extended")
	if _, err := w3.LogUpdate(1, 5, &before, &after); err != nil {
		t.Fatal(err)
	}
	if _, err := w3.LogCommit(1); err != nil {
		t.Fatal(err)
	}
	if err := w3.Sync(); err != nil {
		t.Fatal(err)
	}
	w3.Close()
	dm3.Close()

	dm4, err := disk.Open(extDB)
	if err != nil {
		t.Fatal(err)
	}
	defer dm4.Close()
	w4, err := wal.Open(extWAL)
	if err != nil {
		t.Fatal(err)
	}
	defer w4.Close()
	if _, _, err := Recover(w4, dm4); err != nil {
		t.Fatal(err)
	}
	page, err := dm4.ReadPage(5)
	if err != nil {
		t.Fatal(err)
	}
	if string(page[:8]) != "extended" {
		t.Fatalf("page 5 = %q", page[:8])
	}
}

func TestTornWALTailTruncated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := wal.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.LogCommit(1); err != nil {
		t.Fatal(err)
	}
	if _, err := w.LogCommit(2); err != nil {
		t.Fatal(err)
	}
	if err := w.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xff}, info.Size()-1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{1, 2, 3}, info.Size()); err != nil {
		t.Fatal(err)
	}
	f.Close()

	w2, err := wal.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	records, err := w2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].TxID != 1 {
		t.Fatalf("torn tail kept bad records: %+v", records)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != info.Size()/2 {
		t.Fatalf("file size = %d, want %d", st.Size(), info.Size()/2)
	}
	if w2.NextLSN() != 2 {
		t.Fatalf("next LSN = %d, want 2", w2.NextLSN())
	}
}
