<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# storage/record

## Purpose
Record manager — the primary CRUD interface for nodes and edges in the property graph. Implements both `storage.Store` (create/get/update/delete) and `storage.Scanner` (scan all nodes/edges, expand edge neighbors). Reads and writes serialized records on slotted pages via the buffer pool, maintains B+Tree indexes for O(log n) lookups by ID, and optionally wraps each mutation in a transaction when a `txn.Manager` is present.

## Key Files

| File | Description |
|------|-------------|
| `manager.go` | `Manager` — `NewManager`; `CreateNode`, `GetNode`, `UpdateNode`, `DeleteNode`; `CreateEdge`, `GetEdge`, `GetEdges`, `DeleteEdge`; `ScanNodes`, `ScanEdges`, `Expand`; `SetTxManager` |
| `manager_test.go` | Record manager integration tests |

## For AI Agents

### Working In This Directory
- `Manager` satisfies both `storage.Store` and `storage.Scanner` (verified by compile-time assertions)
- Pages are stored in two linked chains: one for nodes, one for edges (head/tail pointers in disk header)
- Mutations flow: encode → find/allocate slotted page → insert slot → update B+Tree index → optionally log to WAL via txn.Manager
- `SetTxManager(txm)` is called by `server.Engine.Open()` when WAL is enabled — `txm` is nil otherwise (no transactions)
- Thread safety: `sync.RWMutex` with read lock for scans/gets, write lock for mutations
- `ScanNodes(labels)` with empty labels returns all nodes; with labels, filters by label set — no dedicated label index, so it's a full scan

### Testing Requirements
- `go test ./storage/record/...`
- Integration tests should cover: create → get, update → re-get, delete, scan with labels, edge traversal

### Common Patterns
- After creating a record, the new `RID` is inserted into the appropriate B+Tree index (`nodeIndex` for nodes, `edgeIndex`/`outIndex`/`inIndex` for edges)
- `GetNode`/`GetEdge` use the B+Tree index for O(log n) lookup, then fetch and decode the page record
- `Expand(nodeID, dir, edgeType)` walks the out/in index, optionally filtering by edge type

## Dependencies

### Internal
- `github.com/neo4g/neo4g/graph` — `Node`, `Edge`, `NodeID`, `EdgeID`, `Value`
- `github.com/neo4g/neo4g/storage` — `Store`, `Scanner` interfaces, `RID`, `PageID`
- `github.com/neo4g/neo4g/storage/disk` — `disk.Manager`
- `github.com/neo4g/neo4g/storage/encoding` — encode/decode
- `github.com/neo4g/neo4g/storage/index` — `index.Manager`
- `github.com/neo4g/neo4g/storage/page` — `SlottedPage`
- `github.com/neo4g/neo4g/storage/pool` — `pool.Pool`
- `github.com/neo4g/neo4g/storage/txn` — `txn.Manager` (optional)

### External
- Standard library: `sync`

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
