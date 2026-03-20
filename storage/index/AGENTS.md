<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# storage/index

## Purpose
Disk-resident B+Tree index implementation and an index manager that owns four indexes used by the record manager: node-by-ID, edge-by-ID, outgoing-edge-by-source-node, and incoming-edge-by-target-node. Keys are `uint64`; values are `RID` (physical record location). B+Tree root page IDs are persisted in the disk manager header so indexes survive restarts.

## Key Files

| File | Description |
|------|-------------|
| `btree.go` | `BTree` — `NewBTree`, `LoadBTree`; `Search`, `Insert`, `Delete`, `RangeScan`; internal/leaf node navigation, splits |
| `manager.go` | `Manager` — owns the four `BTree` instances; `NewManager`, `LoadManager`; lookup helpers used by `record.Manager` |
| `btree_test.go` | B+Tree unit tests (insert, search, delete, range scan, splits) |

## For AI Agents

### Working In This Directory
- `BTree` satisfies `storage.Index` (verified by compile-time assertion)
- B+Tree pages are either internal (`PageTypeBTreeInt`) or leaf (`PageTypeBTreeLf`) — checked via the page type field at offset `OffPageType`
- Leaf pages form a linked list via `btreeOffNextLeaf` pointer for efficient range scans
- Page capacity: `btreeMaxInternalKeys` and `btreeMaxLeafKeys` are computed from `PageSize` — not hardcoded
- Splits propagate upward; root splits create a new root page and update `BTree.rootID` + persist to disk header via `disk.Manager.SetNodeIndexRoot` etc.
- `Manager.LoadManager` returns `nil` if no indexes exist yet (fresh database) — caller creates with `NewManager`

### Testing Requirements
- `go test ./storage/index/...`
- B+Tree tests must cover: single insert+search, delete, range scan, enough inserts to trigger leaf and internal splits

### Common Patterns
- All B+Tree operations fetch pages via `pool.FetchPage` and unpin when done
- Key=0 is not used (NodeID/EdgeID start at 1) — safe to use as a "not found" sentinel in tests

## Dependencies

### Internal
- `github.com/seankohjs/neo4g/storage` — `PageID`, `RID`, `Page`, page type constants, `Index` interface
- `github.com/seankohjs/neo4g/storage/disk` — `disk.Manager` (for page allocation and root persistence)
- `github.com/seankohjs/neo4g/storage/pool` — `pool.Pool` (page access)

### External
- Standard library: `encoding/binary`

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
