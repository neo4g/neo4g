# Neo4g — Package Reference

## `graph/` — Domain Model

Zero dependencies on storage. Pure data types.

| Export | Kind | Signature / Fields |
|--------|------|--------------------|
| `NodeID` | type | `uint64` (0 = sentinel) |
| `Node` | struct | `ID NodeID`, `Labels []string`, `Properties map[string]Value` |
| `EdgeID` | type | `uint64` (0 = sentinel) |
| `Edge` | struct | `ID EdgeID`, `Type string`, `SourceID NodeID`, `TargetID NodeID`, `Properties map[string]Value` |
| `Direction` | type | `int` — `Outgoing`, `Incoming`, `Both` |
| `ValueType` | type | `byte` — `Null`, `Int`, `Float`, `String`, `Bool`, `Bytes` |
| `Value` | struct | `Type ValueType`, `Data []byte` |
| `IntValue` | func | `(int64) Value` |
| `FloatValue` | func | `(float64) Value` |
| `StringValue` | func | `(string) Value` |
| `BoolValue` | func | `(bool) Value` |
| `BytesValue` | func | `([]byte) Value` — copies input |
| `NullValue` | func | `() Value` |
| `AsInt/AsFloat/AsString/AsBool/AsBytes` | methods | On `Value` — panic on type mismatch |

---

## `storage/` — Shared Types

| Export | Kind | Value |
|--------|------|-------|
| `PageSize` | const | `8192` |
| `MagicNumber` | const | `0x4E454F34` |
| `FormatVersion` | const | `1` |
| `PageID` | type | `uint32` |
| `InvalidPageID` | const | `0` |
| `HeaderPageID` | const | `0` |
| `Page` | type | `[PageSize]byte` |
| `PageTypeFree/Node/Edge/BTreeInt/BTreeLf` | const | `0/1/2/3/4` (uint16) |
| `OffPageID` | const | `0` |
| `OffPageType` | const | `4` |
| `RID` | struct | `PageID PageID`, `Slot uint16` |
| `InvalidRID` | var | `RID{InvalidPageID, 0}` |
| `RID.IsValid()` | method | `bool` |
| `TxID` | type | `uint64` |

---

## `storage/disk/` — Disk Manager

**Imports:** `storage`

| Export | Kind | Signature |
|--------|------|-----------|
| `Manager` | struct | `(unexported fields: file, pageCount, freeHead, nextNodeID, nextEdgeID, mutex)` |
| `Open` | func | `(path string) (*Manager, error)` — creates or opens data file |
| `Close` | method | `() error` |
| `ReadPage` | method | `(id PageID) (*Page, error)` |
| `WritePage` | method | `(id PageID, p *Page) error` |
| `AllocatePage` | method | `() (PageID, *Page, error)` — free list or extend |
| `FreePage` | method | `(id PageID) error` — push onto free list |
| `NextNodeID` | method | `() (uint64, error)` — auto-increment |
| `NextEdgeID` | method | `() (uint64, error)` — auto-increment |
| `Sync` | method | `() error` — fsync |
| `PageCount` | method | `() uint32` |

---

## `storage/wal/` — Write-Ahead Log

**Imports:** `storage`

| Export | Kind | Signature / Value |
|--------|------|-------------------|
| `LSN` | type | `uint64` |
| `Record` | struct | `LSN`, `TxID`, `Type byte`, `PageID`, `BeforeData []byte`, `AfterData []byte` |
| `RecordUpdate` | const | `byte = 1` |
| `RecordCommit` | const | `byte = 2` |
| `RecordAbort` | const | `byte = 3` |
| `RecordCheckpoint` | const | `byte = 4` |
| `WAL` | struct | `(unexported: file, nextLSN, mutex)` |
| `Open` | func | `(path string) (*WAL, error)` |
| `Close` | method | `() error` |
| `LogUpdate` | method | `(txID TxID, pageID PageID, before, after *Page) (LSN, error)` |
| `LogCommit` | method | `(txID TxID) (LSN, error)` |
| `LogAbort` | method | `(txID TxID) (LSN, error)` |
| `LogCheckpoint` | method | `() (LSN, error)` |
| `Sync` | method | `() error` |
| `ReadAll` | method | `() ([]Record, error)` |
| `Truncate` | method | `() error` |

---

## `storage/pool/` — Buffer Pool

**Imports:** `storage`, `storage/disk`

| Export | Kind | Signature |
|--------|------|-----------|
| `Pool` | struct | `(unexported: disk, frames, index map, clock pointer, mutex)` |
| `New` | func | `(dm *disk.Manager, poolSize int) *Pool` |
| `FetchPage` | method | `(id PageID) (*Page, error)` — pin + load |
| `UnpinPage` | method | `(id PageID, dirty bool) error` |
| `NewPage` | method | `() (PageID, *Page, error)` — allocate + pin |
| `FlushPage` | method | `(id PageID) error` |
| `FlushAll` | method | `() error` |

Eviction: clock-sweep, ref bit, max 2× pool size scans.

---

## `storage/page/` — Slotted Page Layout

**Imports:** `storage`

| Export | Kind | Signature |
|--------|------|-----------|
| `HeaderSize` | const | `20` |
| `SlotEntrySize` | const | `4` |
| `SlottedPage` | struct | wraps `*storage.Page` |
| `New` | func | `(p *Page) *SlottedPage` — wrap existing |
| `Init` | func | `(p *Page, id PageID, pageType uint16) *SlottedPage` — initialize fresh page |
| `GetPageID` | method | `() PageID` |
| `GetPageType` | method | `() uint16` |
| `SlotCount` | method | `() uint16` |
| `FreeSpace` | method | `() uint16` |
| `GetNextPage` | method | `() PageID` |
| `SetNextPage` | method | `(id PageID)` |
| `InsertRecord` | method | `(data []byte) (uint16, error)` — returns slot index |
| `GetRecord` | method | `(slot uint16) ([]byte, error)` |
| `UpdateRecord` | method | `(slot uint16, data []byte) error` — in-place or realloc |
| `DeleteRecord` | method | `(slot uint16) error` — tombstone |
| `IsDeleted` | method | `(slot uint16) bool` |
| `Compact` | method | `()` — repack live records |

---

## `storage/encoding/` — Binary Codec

**Imports:** `graph`

| Export | Kind | Signature |
|--------|------|-----------|
| `EncodeNode` | func | `(n *graph.Node) []byte` |
| `DecodeNode` | func | `(data []byte) (*graph.Node, error)` |
| `EncodeEdge` | func | `(e *graph.Edge) []byte` |
| `DecodeEdge` | func | `(data []byte) (*graph.Edge, error)` |

---

## `storage/record/` — Record Manager

**Imports:** `graph`, `storage`, `storage/disk`, `storage/encoding`, `storage/page`, `storage/pool`

| Export | Kind | Signature |
|--------|------|-----------|
| `Manager` | struct | `(unexported: pool, disk, page heads/tails, nodeIndex, edgeIndex, outEdges, inEdges, mutex)` |
| `NewManager` | func | `(p *pool.Pool, dm *disk.Manager) (*Manager, error)` |
| `CreateNode` | method | `(labels []string, props map[string]Value) (NodeID, error)` |
| `GetNode` | method | `(id NodeID) (*Node, error)` |
| `UpdateNode` | method | `(id NodeID, props map[string]Value) error` |
| `DeleteNode` | method | `(id NodeID) error` — fails if edges exist |
| `CreateEdge` | method | `(edgeType string, src, dst NodeID, props map[string]Value) (EdgeID, error)` |
| `GetEdge` | method | `(id EdgeID) (*Edge, error)` |
| `GetEdges` | method | `(nodeID NodeID, dir Direction) ([]*Edge, error)` |
| `DeleteEdge` | method | `(id EdgeID) error` |

**Note:** Currently uses `map[NodeID]RID` / `map[EdgeID]RID` internally. Should be replaced with B+Tree indexes.

---

## `storage/index/` — B+Tree & Index Manager

**Imports:** `graph`, `storage`, `storage/disk`, `storage/pool`

| Export | Kind | Signature |
|--------|------|-----------|
| `BTree` | struct | `(unexported: pool, disk, rootID)` |
| `NewBTree` | func | `(p *pool.Pool, dm *disk.Manager) (*BTree, error)` — allocates root leaf |
| `LoadBTree` | func | `(p *pool.Pool, dm *disk.Manager, rootID PageID) *BTree` |
| `RootPageID` | method | `() PageID` |
| `Search` | method | `(key uint64) (RID, bool)` |
| `Insert` | method | `(key uint64, rid RID) error` |
| `Delete` | method | `(key uint64) bool` — leaf only, no merge |
| `RangeScan` | method | `(startKey, endKey uint64) []RID` |
| `Manager` | struct | `NodeIndex, EdgeIndex, OutIndex, InIndex *BTree` |
| `NewManager` | func | `(p *pool.Pool, dm *disk.Manager) (*Manager, error)` |
| `AdjacencyKey` | func | `(nodeID uint64, edgeID uint64) uint64` — `(nodeID<<32)\|edgeID` |
| `AdjacencyRangeStart` | func | `(nodeID uint64) uint64` |
| `AdjacencyRangeEnd` | func | `(nodeID uint64) uint64` |

---

## `storage/txn/` — Transaction Manager + Recovery

**Imports:** `storage`, `storage/disk`, `storage/pool`, `storage/wal`

| Export | Kind | Signature |
|--------|------|-----------|
| `TxState` | type | `int` — `Active`, `Committed`, `Aborted` |
| `Manager` | struct | `(unexported: wal, pool, disk, nextTxID, active map, pageLocks map, mutex)` |
| `NewManager` | func | `(w *WAL, p *Pool, dm *disk.Manager) *Manager` |
| `Begin` | method | `() TxID` |
| `WritePage` | method | `(txID TxID, pageID PageID, before, after *Page) error` |
| `Commit` | method | `(txID TxID) error` |
| `Abort` | method | `(txID TxID) error` |
| `Checkpoint` | method | `() error` |
| `Recover` | func | `(w *WAL, dm *disk.Manager) (redone, undone int, err error)` — ARIES |
