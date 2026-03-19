# Neo4g — Interfaces

Defined in `storage/ifaces.go`. Compile-time assertions in implementing packages.

## Store — Record Layer API

The top-level interface that the query engine (or a user) talks to.

```go
package storage

import "github.com/seankohjs/neo4g/graph"

type Store interface {
    CreateNode(labels []string, props map[string]graph.Value) (graph.NodeID, error)
    GetNode(id graph.NodeID) (*graph.Node, error)
    UpdateNode(id graph.NodeID, props map[string]graph.Value) error
    DeleteNode(id graph.NodeID) error

    CreateEdge(typ string, src, dst graph.NodeID, props map[string]graph.Value) (graph.EdgeID, error)
    GetEdge(id graph.EdgeID) (*graph.Edge, error)
    GetEdges(nodeID graph.NodeID, dir graph.Direction) ([]*graph.Edge, error)
    DeleteEdge(id graph.EdgeID) error
}
```

**Current state:** `record.Manager` already implements these methods. Just needs the interface extracted so `query/` can depend on the interface, not the concrete type.

## Scanner — Read-Path for Query Engine

Needed for label scans, type scans, and graph traversal.

```go
type Scanner interface {
    ScanNodes(labels []string) ([]*graph.Node, error)
    ScanEdges(typ string) ([]*graph.Edge, error)
    Expand(nodeID graph.NodeID, dir graph.Direction, edgeType string) ([]*graph.Edge, error)
}
```

**Current state:** Not implemented. `record.Manager` has no scan/filter methods yet. Implementation needs:
- `ScanNodes`: walk node page chain, decode each, filter by label
- `ScanEdges`: walk edge page chain, decode each, filter by type
- `Expand`: use adjacency index (OutIndex/InIndex) to find edges for a node, optionally filter by type

## Index — B+Tree API

```go
type Index interface {
    Search(key uint64) (RID, bool)
    Insert(key uint64, rid RID) error
    Delete(key uint64) bool
    RangeScan(startKey, endKey uint64) []RID
}
```

**Current state:** `index.BTree` already implements these methods. Interface extraction is straightforward.

## Tx — Transaction API

```go
type Tx interface {
    Begin() TxID
    WritePage(txID TxID, pageID PageID, before, after *Page) error
    Commit(txID TxID) error
    Abort(txID TxID) error
}
```

**Current state:** `txn.Manager` already implements these methods. Needs to be injected into `record.Manager` so writes become transactional.

## PageManager — Buffer Pool API

```go
type PageManager interface {
    FetchPage(id PageID) (*Page, error)
    UnpinPage(id PageID, dirty bool) error
    NewPage() (PageID, *Page, error)
    FlushPage(id PageID) error
    FlushAll() error
}
```

**Current state:** `pool.Pool` already implements these. Interface useful for testing with mock pools.

## Where to Put Them

| Interface | Suggested location | Rationale |
|-----------|-------------------|-----------|
| `Store` | `storage/store.go` | Top-level storage API, query/ imports it |
| `Scanner` | `storage/store.go` | Same level as Store |
| `Index` | `storage/index.go` or `storage/types.go` | Used by record layer |
| `Tx` | `storage/tx.go` or `storage/types.go` | Used by record layer |
| `PageManager` | `storage/types.go` | Low-level, used widely |

All interfaces in the parent `storage/` package so sub-packages can implement them without circular imports.
