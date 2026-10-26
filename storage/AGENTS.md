<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# storage

## Purpose
The complete custom storage engine for Neo4g. Organized as six strictly layered sub-packages — each layer imports only from the one below it. Provides ACID transactions, WAL-based crash recovery (ARIES-inspired), a clock-sweep buffer pool, slotted page layout, B+Tree indexes, binary encoding, and a full CRUD record manager. Shared types (`PageID`, `RID`, `TxID`, `Page`, page type constants) and the four public interfaces (`Store`, `Scanner`, `Index`, `PageManager`) are defined in the root of this package.

```
┌─────────────────────────────────┐
│  6. Transaction Manager (ACID)  │  txn/
├─────────────────────────────────┤
│  5. Index Manager (B+Tree)      │  index/
├─────────────────────────────────┤
│  4. Record Manager              │  record/
│     (Node/Edge CRUD)            │
├─────────────────────────────────┤
│  3. Slotted Page Layout         │  page/
├─────────────────────────────────┤
│  2. Buffer Pool + Page Cache    │  pool/
├─────────────────────────────────┤
│  1. Disk Manager + WAL          │  disk/ + wal/
└─────────────────────────────────┘
```

## Key Files

| File | Description |
|------|-------------|
| `types.go` | Shared constants and types: `PageSize` (8KB), `MagicNumber`, `PageID`, `RID`, `TxID`, `Page`, page type constants |
| `ifaces.go` | Public interfaces: `Store`, `Scanner`, `Index`, `PageManager` |

## Subdirectories

| Directory | Purpose |
|-----------|---------|
| `disk/` | Raw file I/O, page allocation/free-list, header page, ID counters (see `disk/AGENTS.md`) |
| `wal/` | Write-ahead log — delta-compressed records, LSN sequencing, crash recovery data (see `wal/AGENTS.md`) |
| `pool/` | Buffer pool with clock-sweep eviction, pin counting, dirty tracking (see `pool/AGENTS.md`) |
| `page/` | Slotted page format for variable-size records (see `page/AGENTS.md`) |
| `encoding/` | Binary serialization/deserialization of `graph.Node` and `graph.Edge` (see `encoding/AGENTS.md`) |
| `index/` | B+Tree index implementation and index manager (see `index/AGENTS.md`) |
| `record/` | Record manager — Node/Edge CRUD wired to indexes and transactions (see `record/AGENTS.md`) |
| `txn/` | Transaction manager — begin/commit/abort/checkpoint; ARIES recovery (see `txn/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- **Never break the layer ordering** — lower layers must not import from higher layers
- Page size is fixed at 8192 bytes (`PageSize` constant); changing it requires a full format migration
- `InvalidPageID = 0` and `HeaderPageID = 0` are both page 0 — the header page is never allocatable
- Compile-time interface checks (`var _ storage.Store = (*record.Manager)(nil)`) guard all implementations
- `RID` (Record ID) = `{PageID, SlotIndex}` — the canonical physical address of any record

### Testing Requirements
- `go test ./storage/...` runs all sub-package tests
- Each sub-package has its own `*_test.go` files
- Tests that touch disk use `t.TempDir()` for isolation

### Common Patterns
- All managers are thread-safe via `sync.Mutex` or `sync.RWMutex`
- Pages are always pinned (via `pool.FetchPage`) before access and unpinned (via `pool.UnpinPage`) after
- The `dirty` flag on unpin drives deferred write-back

## Dependencies

### Internal
- `github.com/neo4g/neo4g/graph` — `Node`, `Edge`, `Value` types (used in `ifaces.go`)

### External
- Standard library only

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
