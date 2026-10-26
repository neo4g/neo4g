package wal

import (
	"path/filepath"
	"testing"

	"github.com/neo4g/neo4g/storage"
)

func TestSetNextLSNNeverMovesBackward(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "t.wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	w.SetNextLSN(10)
	lsn, err := w.LogCommit(storage.TxID(1))
	if err != nil {
		t.Fatal(err)
	}
	if lsn != 10 {
		t.Fatalf("assigned LSN %d, want 10", lsn)
	}
	w.SetNextLSN(3)
	if got := w.NextLSN(); got != 11 {
		t.Fatalf("NextLSN %d, want 11", got)
	}
}
