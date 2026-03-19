# Neo4g — Project Status

> **Last updated:** 2026-03-19

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
| **Query language** | `query/` | `not-started` | Cypher-lite parser, AST |
| **Query planner** | `query/` | `not-started` | Logical → physical plan |
| **Query executor** | `query/` | `not-started` | Iterator-based execution |
| **Server engine** | `server/` | `done` | Engine with env-var config, lifecycle (open/close/recovery), exposes Store + Scanner |

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
