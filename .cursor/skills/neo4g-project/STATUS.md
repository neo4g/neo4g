# Neo4g — Project Status

> **Last updated:** 2026-03-19

## Component Status

| Component | Package | Status | Notes |
|-----------|---------|--------|-------|
| **Domain Model** | `graph/` | `done` | Node, Edge, Value types, constructors |
| **Shared Types** | `storage/` | `done` | Page, PageID, RID, TxID, consts |
| **Disk Manager** | `storage/disk/` | `done` | Raw I/O, free list, ID counters, header page |
| **Write-Ahead Log** | `storage/wal/` | `done` | Full-page-image WAL, commit/abort/checkpoint records |
| **Buffer Pool** | `storage/pool/` | `done` | Clock-sweep eviction, pin/unpin, dirty tracking |
| **Slotted Pages** | `storage/page/` | `done` | Variable-size records, slot directory, compaction |
| **Binary Encoding** | `storage/encoding/` | `done` | Node/Edge binary codec |
| **Record Manager** | `storage/record/` | `done` | Node/Edge CRUD, page chains (not yet wired to index/txn) |
| **B+Tree Index** | `storage/index/` | `done` | Search, insert, delete, range scan |
| **Index Manager** | `storage/index/` | `done` | Node/Edge index creation (not wired to RecordManager) |
| **Tx Manager** | `storage/txn/` | `done` | Begin/commit/abort, page-level locking |
| **ARIES Recovery** | `storage/txn/` | `done` | Analysis, redo, undo passes |
| **RecordManager ↔ Index wiring** | `storage/record/` + `storage/index/` | `not-started` | RecordManager should use B+Tree instead of in-memory maps |
| **RecordManager ↔ Txn wiring** | `storage/record/` + `storage/txn/` | `not-started` | Wrap writes in transactions |
| **Index root persistence** | `storage/disk/` + `storage/index/` | `not-started` | Save/load B+Tree roots in header page |
| **Formal interfaces** | `storage/` | `not-started` | Extract Store, Index, Tx, Scanner interfaces |
| **Query language** | `query/` | `not-started` | Cypher-lite parser, AST |
| **Query planner** | `query/` | `not-started` | Logical → physical plan |
| **Query executor** | `query/` | `not-started` | Iterator-based execution |
| **Server / protocol** | `server/` | `not-started` | Local or HTTP access |

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
