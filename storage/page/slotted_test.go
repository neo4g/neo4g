package page

import (
	"bytes"
	"testing"

	"github.com/seankohjs/neo4g/storage"
)

func newTestSlottedPage() *SlottedPage {
	var p storage.Page
	return Init(&p, 1, storage.PageTypeNode)
}

func TestInitSlottedPage(t *testing.T) {
	sp := newTestSlottedPage()

	if sp.GetPageID() != 1 {
		t.Errorf("expected page ID 1, got %d", sp.GetPageID())
	}
	if sp.GetPageType() != storage.PageTypeNode {
		t.Errorf("expected page type %d, got %d", storage.PageTypeNode, sp.GetPageType())
	}
	if sp.SlotCount() != 0 {
		t.Errorf("expected 0 slots, got %d", sp.SlotCount())
	}
	if sp.GetNextPage() != storage.InvalidPageID {
		t.Errorf("expected next page %d, got %d", storage.InvalidPageID, sp.GetNextPage())
	}
	expected := uint16(storage.PageSize - HeaderSize)
	if sp.FreeSpace() != expected {
		t.Errorf("expected free space %d, got %d", expected, sp.FreeSpace())
	}
}

func TestInsertAndGetRecord(t *testing.T) {
	sp := newTestSlottedPage()

	data := []byte("hello world")
	slot, err := sp.InsertRecord(data)
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}
	if slot != 0 {
		t.Errorf("expected slot 0, got %d", slot)
	}
	if sp.SlotCount() != 1 {
		t.Errorf("expected 1 slot, got %d", sp.SlotCount())
	}

	got, err := sp.GetRecord(0)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("expected %q, got %q", data, got)
	}
}

func TestInsertMultipleRecords(t *testing.T) {
	sp := newTestSlottedPage()

	records := []string{"record-0", "record-1", "record-2"}
	for i, r := range records {
		slot, err := sp.InsertRecord([]byte(r))
		if err != nil {
			t.Fatalf("InsertRecord %d: %v", i, err)
		}
		if slot != uint16(i) {
			t.Errorf("expected slot %d, got %d", i, slot)
		}
	}

	for i, r := range records {
		got, err := sp.GetRecord(uint16(i))
		if err != nil {
			t.Fatalf("GetRecord %d: %v", i, err)
		}
		if string(got) != r {
			t.Errorf("slot %d: expected %q, got %q", i, r, string(got))
		}
	}
}

func TestDeleteRecord(t *testing.T) {
	sp := newTestSlottedPage()
	sp.InsertRecord([]byte("keep"))
	sp.InsertRecord([]byte("delete me"))
	sp.InsertRecord([]byte("keep too"))

	if err := sp.DeleteRecord(1); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}

	got, err := sp.GetRecord(1)
	if err != nil {
		t.Fatalf("GetRecord deleted: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for deleted record, got %q", got)
	}

	if !sp.IsDeleted(1) {
		t.Error("expected slot 1 to be deleted")
	}

	r0, _ := sp.GetRecord(0)
	r2, _ := sp.GetRecord(2)
	if string(r0) != "keep" || string(r2) != "keep too" {
		t.Errorf("adjacent records corrupted: %q, %q", r0, r2)
	}
}

func TestDeleteAlreadyDeleted(t *testing.T) {
	sp := newTestSlottedPage()
	sp.InsertRecord([]byte("x"))
	sp.DeleteRecord(0)

	if err := sp.DeleteRecord(0); err == nil {
		t.Fatal("expected error deleting already-deleted slot")
	}
}

func TestUpdateRecordSameSize(t *testing.T) {
	sp := newTestSlottedPage()
	sp.InsertRecord([]byte("AAAA"))

	if err := sp.UpdateRecord(0, []byte("BBBB")); err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}

	got, _ := sp.GetRecord(0)
	if string(got) != "BBBB" {
		t.Errorf("expected 'BBBB', got %q", got)
	}
}

func TestUpdateRecordSmaller(t *testing.T) {
	sp := newTestSlottedPage()
	sp.InsertRecord([]byte("longrecord"))

	if err := sp.UpdateRecord(0, []byte("short")); err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}

	got, _ := sp.GetRecord(0)
	if string(got) != "short" {
		t.Errorf("expected 'short', got %q", got)
	}
}

func TestUpdateRecordLarger(t *testing.T) {
	sp := newTestSlottedPage()
	sp.InsertRecord([]byte("small"))

	if err := sp.UpdateRecord(0, []byte("much larger record data")); err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}

	got, _ := sp.GetRecord(0)
	if string(got) != "much larger record data" {
		t.Errorf("expected 'much larger record data', got %q", got)
	}
}

func TestCompact(t *testing.T) {
	sp := newTestSlottedPage()

	sp.InsertRecord([]byte("keep-0"))
	sp.InsertRecord([]byte("delete-1"))
	sp.InsertRecord([]byte("keep-2"))

	freeBeforeDelete := sp.FreeSpace()
	sp.DeleteRecord(1)

	if sp.FreeSpace() != freeBeforeDelete {
		t.Error("free space should not change on delete")
	}

	sp.Compact()

	if sp.FreeSpace() <= freeBeforeDelete {
		t.Error("free space should increase after compaction")
	}

	r0, _ := sp.GetRecord(0)
	r2, _ := sp.GetRecord(2)
	if string(r0) != "keep-0" {
		t.Errorf("slot 0: expected 'keep-0', got %q", r0)
	}
	if string(r2) != "keep-2" {
		t.Errorf("slot 2: expected 'keep-2', got %q", r2)
	}

	r1, _ := sp.GetRecord(1)
	if r1 != nil {
		t.Errorf("slot 1: expected nil after compact, got %q", r1)
	}
}

func TestGetRecordOutOfRange(t *testing.T) {
	sp := newTestSlottedPage()
	_, err := sp.GetRecord(0)
	if err == nil {
		t.Fatal("expected error for out-of-range slot")
	}
}

func TestPageFull(t *testing.T) {
	sp := newTestSlottedPage()

	record := make([]byte, 100)
	count := 0
	for {
		_, err := sp.InsertRecord(record)
		if err != nil {
			break
		}
		count++
	}

	if count == 0 {
		t.Fatal("should be able to insert at least one record")
	}

	for i := 0; i < count; i++ {
		got, err := sp.GetRecord(uint16(i))
		if err != nil {
			t.Fatalf("GetRecord %d: %v", i, err)
		}
		if len(got) != 100 {
			t.Errorf("slot %d: expected 100 bytes, got %d", i, len(got))
		}
	}
}

func TestSetNextPage(t *testing.T) {
	sp := newTestSlottedPage()
	sp.SetNextPage(42)
	if sp.GetNextPage() != 42 {
		t.Errorf("expected next page 42, got %d", sp.GetNextPage())
	}
}
