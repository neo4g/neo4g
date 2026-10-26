package wal

import (
	"testing"

	"github.com/neo4g/neo4g/storage"
)

func TestDeltaRoundtrip(t *testing.T) {
	var before, after storage.Page

	delta := EncodeDelta(&before, &after)
	if len(delta) != 0 {
		t.Fatalf("expected empty delta for identical pages, got %d bytes", len(delta))
	}

	after[100] = 0xFF
	delta = EncodeDelta(&before, &after)
	if len(delta) == 0 {
		t.Fatal("expected non-empty delta")
	}
	// 4 header + 1 before + 1 after = 6 bytes
	if len(delta) != 6 {
		t.Errorf("expected 6 bytes for 1-byte change, got %d", len(delta))
	}

	reconstructed := ApplyDelta(&before, delta)
	if reconstructed != after {
		t.Error("ApplyDelta failed to reconstruct after-image")
	}

	undone := ApplyDeltaUndo(&after, delta)
	if undone != before {
		t.Error("ApplyDeltaUndo failed to reconstruct before-image")
	}
}

func TestDeltaMultipleChanges(t *testing.T) {
	var before, after storage.Page
	for i := range before {
		before[i] = byte(i % 256)
	}
	copy(after[:], before[:])

	for i := 0; i < 50; i++ {
		after[i*100] = 0xAA
		after[i*100+1] = 0xBB
	}

	delta := EncodeDelta(&before, &after)
	if len(delta) > 2000 {
		t.Errorf("delta too large for scattered 2-byte changes: %d bytes", len(delta))
	}

	reconstructed := ApplyDelta(&before, delta)
	if reconstructed != after {
		t.Error("roundtrip failed for multi-region changes")
	}

	undone := ApplyDeltaUndo(&after, delta)
	if undone != before {
		t.Error("undo failed for multi-region changes")
	}
}

func TestDeltaLargeChange(t *testing.T) {
	var before, after storage.Page
	for i := storage.PageSize / 2; i < storage.PageSize; i++ {
		after[i] = 0xFF
	}

	delta := EncodeDelta(&before, &after)
	reconstructed := ApplyDelta(&before, delta)
	if reconstructed != after {
		t.Error("roundtrip failed for large contiguous change")
	}

	undone := ApplyDeltaUndo(&after, delta)
	if undone != before {
		t.Error("undo failed for large contiguous change")
	}
}

func TestDeltaNonZeroBefore(t *testing.T) {
	var before, after storage.Page
	for i := range before {
		before[i] = 0x42
	}
	copy(after[:], before[:])
	after[0] = 0x01
	after[1] = 0x02
	after[4095] = 0x03

	delta := EncodeDelta(&before, &after)

	reconstructed := ApplyDelta(&before, delta)
	if reconstructed != after {
		t.Error("forward apply failed with non-zero before page")
	}

	undone := ApplyDeltaUndo(&after, delta)
	if undone != before {
		t.Error("undo failed with non-zero before page")
	}
}
