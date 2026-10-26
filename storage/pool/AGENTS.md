<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# storage/pool

## Purpose
In-memory page cache (buffer pool) that sits between the disk manager and all higher storage layers. Implements the `storage.PageManager` interface. Uses clock-sweep (second-chance) eviction to select victim frames when all slots are full. Cache hits use a read lock for maximum concurrency; misses and evictions take a write lock. Pages are pinned while in use and must be explicitly unpinned.

## Key Files

| File | Description |
|------|-------------|
| `pool.go` | `Pool` — `New`, `FetchPage`, `UnpinPage`, `NewPage`, `FlushPage`, `FlushAll`; `frame` struct with atomic pin/dirty/ref fields; `findVictim` (clock-sweep), `evict` |
| `pool_test.go` | Buffer pool unit tests |

## For AI Agents

### Working In This Directory
- `Pool` satisfies `storage.PageManager` (verified by compile-time assertion)
- **Every** `FetchPage` call must be paired with an `UnpinPage` — missing unpins exhaust the pool and cause `"buffer pool exhausted"` errors
- `dirty=true` on unpin marks the frame for write-back before eviction; `dirty=false` means the page can be discarded without I/O
- `frame` fields `pinCount`, `dirty`, `refBit` use `sync/atomic` — accessed without holding the pool mutex for performance
- Default pool size is 1024 frames (`Config.PoolSize`); configurable via `NEO4G_POOL_SIZE`
- `findVictim` does two passes (2×n iterations) before giving up — a pool too small will cause errors under write-heavy loads

### Testing Requirements
- `go test ./storage/pool/...`
- Tests should cover: cache hit, cache miss, eviction of dirty page, pool exhaustion

### Common Patterns
- Double-checked locking in `FetchPage`: read lock for cache hit, upgrade to write lock only on miss
- `NewPage` calls `disk.AllocatePage` then loads the fresh page into a pool frame (pinned)

## Dependencies

### Internal
- `github.com/neo4g/neo4g/storage` — `PageID`, `Page`, `PageManager` interface
- `github.com/neo4g/neo4g/storage/disk` — `disk.Manager` for disk reads/writes

### External
- Standard library: `sync`, `sync/atomic`

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
