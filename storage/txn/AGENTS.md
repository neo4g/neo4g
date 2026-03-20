<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# storage/txn

## Purpose
Transaction manager providing ACID guarantees and ARIES-inspired crash recovery. Manages transaction begin/commit/abort lifecycle, page-level locking, WAL-coordinated write ordering (WAL record before page write), and checkpointing. The `Recover` function replays the WAL on startup: redo all committed records, undo uncommitted ones.

## Key Files

| File | Description |
|------|-------------|
| `manager.go` | `Manager` — `NewManager`; `Begin`, `Commit`, `Abort`, `Checkpoint`; `LogPageUpdate` (write WAL record then dirty the pool page); page locking; `NoSync` flag |
| `recovery.go` | `Recover(wal, disk)` — reads all WAL records, builds committed/aborted sets, applies redo then undo passes |
| `manager_test.go` | Transaction manager tests |

## For AI Agents

### Working In This Directory
- Write ordering: WAL record must be written **before** the page is marked dirty in the buffer pool (write-ahead logging invariant)
- `LogPageUpdate(txID, pageID, before, after)` encodes the delta and logs it, then marks the page dirty via `pool.UnpinPage(..., dirty=true)`
- Page locking is at the page level (coarse-grained): a transaction acquires a lock on a page via `pageLocks` map; another transaction writing the same page will see a conflict
- `NoSync=true` skips `wal.Sync()` after each commit — used in benchmarks; set via `Config.WALNoSync`
- `Checkpoint()` flushes all dirty pool pages, writes a checkpoint WAL record, flushes the WAL, flushes the disk header, then truncates the WAL
- `Recover` is called once at startup by `server.Engine.Open()` before any transactions begin

### Testing Requirements
- `go test ./storage/txn/...`
- Tests should cover: commit persists, abort undoes, recovery after simulated crash (write WAL without flushing pool, then recover)

### Common Patterns
- `TxState`: `TxStateActive` → `TxStateCommitted` or `TxStateAborted`
- `active map[TxID]*txEntry` tracks in-flight transactions; entries are removed after commit/abort
- Recovery redo pass: apply after-delta for all committed update records; undo pass: apply before-delta for all uncommitted update records

## Dependencies

### Internal
- `github.com/seankohjs/neo4g/storage` — `TxID`, `PageID`
- `github.com/seankohjs/neo4g/storage/disk` — `disk.Manager` (header flush, page writes)
- `github.com/seankohjs/neo4g/storage/pool` — `pool.Pool` (page dirty marking)
- `github.com/seankohjs/neo4g/storage/wal` — `wal.WAL` (log writes, `ReadAll`, `Truncate`)

### External
- Standard library: `sync`

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
