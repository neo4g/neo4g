<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# storage/page

## Purpose
Slotted page layout for storing variable-length records within a fixed 8KB page. Manages an on-page slot directory and free-space pointer so records of different sizes can be packed efficiently. Used by the record manager to physically store serialized node and edge bytes.

## Key Files

| File | Description |
|------|-------------|
| `slotted.go` | `SlottedPage` — insert, get, delete, and iteration over slots; slot directory management; free-space tracking |
| `slotted_test.go` | Slotted page unit tests |

## For AI Agents

### Working In This Directory
- A `SlottedPage` wraps a `*storage.Page` (8KB byte array) — it does not own the page's memory
- Slot directory grows from the end of the page toward the start; record data grows from the start toward the end
- Deleted slots are marked as free but not compacted until explicitly requested — watch for fragmentation in long-running tests
- The record manager accesses pages through the buffer pool (`pool.FetchPage`), wraps them in `SlottedPage`, performs operations, then unpins with `dirty=true`

### Testing Requirements
- `go test ./storage/page/...`
- Tests should cover insert to full capacity, delete + re-insert, and slot iteration

### Common Patterns
- `Insert(data []byte) (uint16, error)` returns the slot index for the new record
- `Get(slot uint16) []byte` returns a slice into the page — do not retain it after unpinning
- Slot indices are stable (not compacted) so `RID.Slot` remains valid after other inserts/deletes

## Dependencies

### Internal
- `github.com/neo4g/neo4g/storage` — `Page`, `PageSize`

### External
- Standard library: `encoding/binary`

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
