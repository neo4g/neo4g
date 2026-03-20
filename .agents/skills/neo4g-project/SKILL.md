---
name: neo4g-project
description: Neo4g project context, architecture, and status tracker. Use when working on any Neo4g code, planning features, reviewing changes, or when the user asks about project status. Agents MUST read and update STATUS.md after completing work.
---

# Neo4g — Minimal Graph Database in Go

A minimal Neo4j-equivalent graph database engine in Go (CS464). Property graph model — nodes with labels/properties, directed edges with types/properties — backed by a custom storage engine with ACID transactions.

**Not** a full database — no clustering, no query optimizer, intentionally minimal.

## Quick Architecture

```
┌─────────────────────────────────────┐
│  Protocol (HTTP)                    │  server/
├─────────────────────────────────────┤
│  Cypher-lite Parser                 │  parser/
├─────────────────────────────────────┤
│  Query Executor                     │  query/
├─────────────────────────────────────┤
│  Storage Engine  │  Transactions    │  record/ + index/ + txn/
├─────────────────────────────────────┤
│  I/O (R/W) + codec/formatting       │  pool/ + page/ + encoding/
├─────────────────────────────────────┤
│  Filesystem                         │  disk/ + wal/
└─────────────────────────────────────┘
```

## Detailed References

| File | What's in it |
|------|-------------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Layer breakdown, dependency graph, on-disk format, data flow |
| [PACKAGES.md](PACKAGES.md) | Every package: exported types, functions, methods, constants, implementation notes |
| [INTERFACES.md](INTERFACES.md) | Interfaces for layer boundaries (Store, Scanner, Index, PageManager) |
| [STATUS.md](STATUS.md) | Live component status tracker + changelog |

## Status Tracking

Current status is tracked in [STATUS.md](STATUS.md). **Every agent MUST update STATUS.md after completing work.**

### Agent Hook: Post-Work Status Update

After finishing any task that changes code in this project:

1. Read `STATUS.md`
2. Update the relevant component's status (`not-started` → `in-progress` → `done`)
3. Add a one-line entry to the changelog at the bottom
4. Write the updated file

Format for changelog entries:
```
- YYYY-MM-DD: <what changed> (<packages affected>)
```
