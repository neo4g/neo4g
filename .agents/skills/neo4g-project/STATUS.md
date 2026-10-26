# Neo4g — Project Status

> **Last updated:** 2026-10-26

## Component Status

| Component | Package | Status | Notes |
|-----------|---------|--------|-------|
| **Domain Model** | `graph/` | `done` | Node, Edge, Value types, constructors |
| **Shared Types** | `storage/` | `done` | Page, PageID, RID, TxID, consts + Store/Scanner/Index/PageManager interfaces |
| **Disk Manager** | `storage/disk/` | `done` | Page I/O, free list, ID counters; catalog image is the only writer of page 0; redo can extend the file |
| **Write-Ahead Log** | `storage/wal/` | `done` | Delta records with CRC, group-commit Force, torn-tail truncation; checkpoints do not reset LSNs |
| **Buffer Pool** | `storage/pool/` | `done` | Clock-sweep eviction; data writes go through the disk manager so the WAL is forced first; stale page 0 is not flushed |
| **Slotted Pages** | `storage/page/` | `done` | Variable-size records; oversized inserts rejected before mutation; compaction uses a scratch buffer |
| **Binary Encoding** | `storage/encoding/` | `done` | Node/Edge codec; property keys written in sorted order |
| **Record Manager** | `storage/record/` | `done` | Node/Edge CRUD wired to B+Tree indexes and optional txn; implements Store + Scanner |
| **B+Tree Index** | `storage/index/` | `done` | Search, insert, delete, range scan; leaf lookup is binary search; root splits update the header |
| **Index Manager** | `storage/index/` | `done` | 4 trees (Node, Edge, Out, In); roots persisted on create and on root split; abort restores the committed root |
| **Tx Manager** | `storage/txn/` | `done` | Begin/commit/abort, page-level locking; commit syncs the WAL (including the catalog) before flushing the header; NoSync is the benchmark opt-out |
| **ARIES Recovery** | `storage/txn/` | `done` | Redo/undo after the last checkpoint; runtime undo installs before-images into the buffer pool; log history is kept |
| **RecordManager ↔ Index wiring** | `storage/record/` + `storage/index/` | `done` | B+Tree replaces in-memory maps |
| **RecordManager ↔ Txn wiring** | `storage/record/` + `storage/txn/` | `done` | Optional auto-tx per CRUD call with before-image capture |
| **Index root persistence** | `storage/disk/` + `storage/index/` | `done` | 4 roots + 4 page chain pointers in the header, logged with the committing transaction |
| **Formal interfaces** | `storage/` | `done` | Store, Scanner, Index, PageManager in storage/ifaces.go |
| **Scanner methods** | `storage/record/` | `done` | ScanNodes, ScanEdges, Expand |
| **Query parser** | `parser/` | `done` | Cypher-lite lexer + recursive-descent parser; tokens, AST, full test suite (renamed from query/) |
| **Query executor** | `query/` | `done` | Expression evaluator, parallel MATCH resolution (errgroup), WHERE filtering, CREATE/RETURN/SET/DELETE dispatch; join and comparison semantics |
| **Query planner** | — | `not-started` | Logical → physical plan (future) |
| **Server engine** | `server/` | `done` | Engine with env-var config, lifecycle (open/close/recovery), exposes Store + Scanner |
| **HTTP API** | `server/` | `done` | Client `POST /api/query` and `GET /api/health`; graceful shutdown |
| **Internal gRPC** | `rpcpb/` + `server/` + `gateway/` | `done` | Gateway and replica traffic: Health, SetRole, ExecuteQuery, PullWAL, Checksum |
| **Concurrent reads** | `storage/record/` | `done` | RWMutex upgrade — readers run in parallel, writers serialize |

## Status Legend

| Status | Meaning |
|--------|---------|
| `done` | Implemented and tested |
| `in-progress` | Currently being worked on |
| `not-started` | Planned but no code yet |
| `blocked` | Waiting on another component |

## Changelog

- 2026-03-19: Initial project reorganization into layered sub-packages (`graph/`, `storage/*`)
- 2026-03-19: Created project skill with status tracker
- 2026-03-19: Wired storage layers — index root persistence, RecordManager↔Index, RecordManager↔Txn, scanners, formal interfaces (`storage/disk`, `storage/index`, `storage/record`, `storage/pool`, `storage/ifaces.go`)
- 2026-03-19: Server engine layer — Config from env vars, Engine open/close/recovery lifecycle, reopen persistence (`server/`)
- 2026-03-19: HTTP + concurrency layer — RWMutex for concurrent reads, Server struct with graceful shutdown (`server/handler.go`, `server/http.go`, `storage/record/manager.go`)
- 2026-03-19: Simplified HTTP API — replaced 11 CRUD routes with single `POST /api/query` endpoint; query engine stub for future parser/executor wiring
- 2026-03-19: Cypher-lite query parser — hand-written lexer, recursive-descent parser, full AST (CREATE/MATCH/WHERE/RETURN/SET/DELETE, node/edge patterns, expressions) (`query/`)
- 2026-03-19: Package rename — `query/` → `parser/` for lexer/AST/parser; new `query/` for executor engine
- 2026-03-19: Query executor engine — expression evaluator, parallel MATCH via errgroup, WHERE filtering, action dispatch (CREATE/RETURN/SET/DELETE), wired into HTTP handler (`query/`, `server/handler.go`, `server/http.go`)
- 2026-10-26: Internal cluster traffic moved to gRPC (`proto/node.proto`, `rpcpb/`, `server/`, `gateway/`); client API stays HTTP
- 2026-10-26: Module path renamed to `github.com/neo4g/neo4g` (`go.mod`, imports, `proto/node.proto`, docs)
- 2026-10-26: Fix MATCH correctness for empty joins, shared names, endpoints, property maps, labels, and comparisons; align write result columns (`query/`)
- 2026-10-26: Pull requests run `go test ./...` only; Docker image publish is skipped on `pull_request` (`.github/workflows/docker-publish.yml`)
- 2026-10-26: Gateway is the only primary (generation fence); failover ranks the persisted applied primary LSN; replicas reject truncated-log gaps and serve reads only when caught up to the client's last successful write (`gateway/`, `server/`, `storage/wal/`)
- 2026-10-26: Split MATCH expansion so the merged query executor stays within the lint complexity limit (`query/`)
- 2026-10-26: Storage durability — persist B+Tree roots on split, force the WAL before data pages, log the header catalog, undo into the buffer pool, keep checkpoint history and monotonic LSNs, CRC torn tails (`storage/`)
- 2026-10-26: Merged main's lint and test workflows; record-manager page parameter no longer shadows the page import (`storage/record`, `.github/workflows`)
- 2026-10-26: Replication safety sits on durable WAL history: checkpoints keep the log and monotonic LSNs, while gateway fencing, the applied primary LSN, and hard-fail empty pulls stay (`gateway/`, `server/`, `storage/`)
- 2026-10-26: Lint and E2E no longer run on feature-branch pushes, so pull requests schedule each check once; newer pushes cancel the in-progress run (`.github/workflows`)
