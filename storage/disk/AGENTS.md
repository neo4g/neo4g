<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# storage/disk

## Purpose
Lowest layer of the storage engine. Manages raw page I/O against a single `.db` file. Handles page allocation (free-list + file extension), page deallocation, atomic monotonically-increasing ID counters for nodes and edges, and the header page (page 0) which persists all metadata: magic number, format version, page count, free-list head, node/edge ID counters, B+Tree root page IDs, and record page chain head/tail pointers.

## Key Files

| File | Description |
|------|-------------|
| `manager.go` | `Manager` — `Open`, `Close`, `ReadPage`, `WritePage`, `AllocatePage`, `FreePage`, `NextNodeID`, `NextEdgeID`, header persistence, index root getters/setters, page chain head/tail getters/setters |
| `manager_test.go` | Manager unit tests |

## For AI Agents

### Working In This Directory
- `Manager` is the only type in this package — do not add more types without strong justification
- All public methods are thread-safe (protected by `sync.Mutex`)
- Header page (page 0 / `HeaderPageID`) is **never** allocatable or freeable — guarded by `FreePage`
- `headerDirty` flag batches header writes; `FlushHeader()` is called by the transaction manager at commit time
- Page offsets in the file: `page_id * PageSize` — no indirection, direct random I/O via `ReadAt`/`WriteAt`
- Free list is a singly-linked list stored in the first 4 bytes of each freed page

### Testing Requirements
- `go test ./storage/disk/...`
- Use `t.TempDir()` for test database files

### Common Patterns
- `AllocatePage` tries the free list first, then extends the file
- `NextNodeID` / `NextEdgeID` return a pre-incremented counter and mark the header dirty

## Dependencies

### Internal
- `github.com/neo4g/neo4g/storage` — `PageID`, `Page`, `PageSize`, constants

### External
- Standard library: `encoding/binary`, `os`, `sync`

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
