<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# storage/wal

## Purpose
Write-ahead log for crash recovery and durability. Every page modification is logged (as a delta-compressed record) before being written to the data file. On startup, the transaction manager replays committed records (redo) and rolls back uncommitted ones (undo). The WAL uses LSNs (Log Sequence Numbers) to order records and supports checkpointing (flush + truncate) to bound recovery time.

## Key Files

| File | Description |
|------|-------------|
| `wal.go` | `WAL` struct; `Open`, `Close`; `LogUpdate` (delta-compressed), `LogCommit`, `LogAbort`, `LogCheckpoint`; `ReadAll`, `Truncate`, `Sync`; record serialization/deserialization |
| `delta.go` | `EncodeDelta` / `ApplyDelta` — byte-level diff/patch for before/after page images; reduces WAL size significantly |
| `delta_test.go` | Delta encoding unit tests |

## For AI Agents

### Working In This Directory
- WAL writes use a `bufio.Writer` for throughput; `Sync()` flushes the buffer and calls `file.Sync()` (fsync)
- `LogUpdate` stores a **delta** (changed byte ranges only), not full before/after page images — see `delta.go`
- Record format: `LSN(8) + TxID(8) + Type(1) + PageID(4) + DataLen(4)` = 25-byte header, followed by payload
- Record types: `RecordUpdate` (full images, legacy), `RecordUpdateDelta` (delta, current), `RecordCommit`, `RecordAbort`, `RecordCheckpoint`
- `WAL` is thread-safe via `sync.Mutex`
- `NoSync` mode (controlled by `txn.Manager`) skips per-commit fsync for benchmarks — data still reaches the OS buffer

### Testing Requirements
- `go test ./storage/wal/...`
- Delta encoding is separately tested in `delta_test.go`

### Common Patterns
- `ReadAll()` is only called during recovery (`txn.Recover`) — not on the hot path
- After `ReadAll()`, the file position is restored to end-of-file so appends continue correctly
- `Truncate()` resets the WAL after a successful checkpoint

## Dependencies

### Internal
- `github.com/seankohjs/neo4g/storage` — `TxID`, `PageID`, `Page`, `PageSize`

### External
- Standard library: `bufio`, `encoding/binary`, `io`, `os`, `sync`

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
