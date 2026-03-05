package storage

// rid.go defines the Record ID (RID) type — the stable physical address of a
// record within the database file.
//
// A RID is a (PageID, SlotIndex) pair. The page ID identifies which page holds
// the record, and the slot index identifies which slot within that page's slot
// directory points to the record data.
//
// RIDs are stable across in-page compaction: the slot directory can be
// reorganized (moving record data within the page) without changing the slot
// index, so existing RID references remain valid.
//
// RIDs are used internally by indexes (e.g., B+Tree leaf entries point to RIDs)
// and are not exposed to end users, who work with NodeID/EdgeID instead.

// RID uniquely identifies a record's physical location as (PageID, SlotIndex).
type RID struct {
	PageID PageID
	Slot   uint16
}

// InvalidRID is a sentinel value representing no record.
var InvalidRID = RID{PageID: InvalidPageID, Slot: 0}

// IsValid returns true if this RID points to a potentially valid location.
func (r RID) IsValid() bool {
	return r.PageID != InvalidPageID
}
