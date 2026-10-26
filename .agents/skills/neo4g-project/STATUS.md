# Neo4g — Project Status

> **Last updated:** 2026-10-26

## Component Status

| Component | Package | Status | Notes |
|-----------|---------|--------|-------|
| **Domain Model** | `graph/` | `done` | Node, Edge, Value types, constructors |
| **Shared Types** | `storage/` | `done` | Page, PageID, RID, TxID, consts + Store/Scanner/Index/PageManager interfaces |
| **Disk Manager** | `storage/disk/` | `done` | Raw I/O, free list, ID counters, header page, index root + page chain persistence |
| **Write-Ahead Log** | `storage/wal/` | `done` | Full-page-image WAL, commit/abort/checkpoint records |
| **Buffer Pool** | `storage/pool/` | `done` | Clock-sweep eviction, pin/unpin, dirty tracking; implements PageManager |
| **Slotted Pages** | `storage/page/` | `done` | Variable-size records, slot directory, compaction |
| **Binary Encoding** | `storage/encoding/` | `done` | Node/Edge binary codec |
| **Record Manager** | `storage/record/` | `done` | Node/Edge CRUD wired to B+Tree indexes and optional txn; implements Store + Scanner |
| **B+Tree Index** | `storage/index/` | `done` | Search, insert, delete, range scan; implements Index |
| **Index Manager** | `storage/index/` | `done` | 4 trees (Node, Edge, Out, In) + LoadManager for persistence |
| **Tx Manager** | `storage/txn/` | `done` | Begin/commit/abort, page-level locking |
| **ARIES Recovery** | `storage/txn/` | `done` | Analysis, redo, undo passes |
| **RecordManager ↔ Index wiring** | `storage/record/` + `storage/index/` | `done` | B+Tree replaces in-memory maps |
| **RecordManager ↔ Txn wiring** | `storage/record/` + `storage/txn/` | `done` | Optional auto-tx per CRUD call with before-image capture |
| **Index root persistence** | `storage/disk/` + `storage/index/` | `done` | 4 roots + 4 page chain pointers in header |
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
- 2026-10-26: Split MATCH expansion so the merged query executor stays within the lint complexity limit (`query/`)
