<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# server

## Purpose
Top-level database engine lifecycle and HTTP server. Wires all six storage layers into a single `Engine` handle, manages startup (including crash recovery via WAL replay) and graceful shutdown (flush + checkpoint + drain). Exposes a single HTTP endpoint `POST /api/query` that parses Cypher-lite queries and returns JSON results. Configuration is entirely environment-variable driven.

## Key Files

| File | Description |
|------|-------------|
| `config.go` | `Config` struct with all tunables; `DefaultConfig()` and `ConfigFromEnv()` for env-variable override |
| `engine.go` | `Engine` — opens/closes all storage layers in dependency order; exposes `Store()` and `Scanner()` |
| `http.go` | `Server` (wraps `net/http.Server`); `NewServer()`, `ListenAndServe()`, `Shutdown()` |
| `handler.go` | HTTP handler for `POST /api/query` — parses body, invokes parser+executor, writes JSON response |
| `engine_test.go` | Engine integration tests |
| `handler_test.go` | HTTP handler tests |

## For AI Agents

### Working In This Directory
- `Engine.Open()` initialises layers in strict order: disk → WAL → recovery → pool → index → record → txn
- WAL recovery runs automatically on `Open()` if a non-empty WAL file exists (calls `txn.Recover`)
- `Engine.Close()` must always be called — it flushes the pool, checkpoints the WAL, and closes files
- `Config.WALNoSync = true` skips `fsync` per commit for benchmarks; never set this in correctness-sensitive tests
- The HTTP server uses `net/http` standard library only — no external HTTP framework. Internal cluster RPCs use gRPC (`server/grpc_server.go`).

### Testing Requirements
- `go test ./server/...`
- Engine tests require a temp directory (use `t.TempDir()`)
- Handler tests can use `httptest.NewRecorder()` and `httptest.NewServer()`

### Common Patterns
- Error responses: `{"error": "<message>"}` with appropriate HTTP status
- Success responses: `{"data": {"columns": [...], "rows": [...]}}`
- Graceful shutdown: `Shutdown(ctx)` drains in-flight requests then calls `engine.Close()`
- All env variables are prefixed `NEO4G_` (see `config.go` for the full list)

## Dependencies

### Internal
- `github.com/neo4g/neo4g/storage` — `Store`, `Scanner` interfaces
- `github.com/neo4g/neo4g/storage/disk` — `disk.Manager`
- `github.com/neo4g/neo4g/storage/wal` — `wal.WAL`
- `github.com/neo4g/neo4g/storage/pool` — `pool.Pool`
- `github.com/neo4g/neo4g/storage/index` — `index.Manager`
- `github.com/neo4g/neo4g/storage/record` — `record.Manager`
- `github.com/neo4g/neo4g/storage/txn` — `txn.Manager`, `txn.Recover`
- `github.com/neo4g/neo4g/parser` — query parsing
- `github.com/neo4g/neo4g/query` — query execution

### External
- `google.golang.org/grpc` — internal node API (health, role, query forwarding, WAL pull, checksum). Client traffic stays on `net/http`.
- Standard library (`net/http`, `os`, `time`, `strconv`, etc.)

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->

Internal cluster calls use gRPC (`NEO4G_GRPC_ADDR`, default `:9474`). `POST /api/query` and `GET /api/health` remain HTTP. `NEO4G_PRIMARY_ADDR` is a gRPC `host:port`.
