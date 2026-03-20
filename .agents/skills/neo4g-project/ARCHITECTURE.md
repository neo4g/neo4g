# Neo4g — Architecture

## Layers (Bottom → Top)

### L1: Filesystem — `storage/disk/` + `storage/wal/`

**Disk Manager** (`disk/manager.go`)
- Single data file, 8KB fixed-size pages
- Page 0 = header: magic (`0x4E454F34`), version, pageCount, freeHead, nextNodeID, nextEdgeID
- Free list: freed pages form a singly-linked list via first 4 bytes of each free page
- Allocate: reuse free list head, else extend file
- Thread-safe (mutex)

**WAL** (`wal/wal.go`)
- Append-only log file, sequential records
- Record types: Update, Commit, Abort, Checkpoint
- Update records store **full page images** (before + after), not deltas
- Record header: LSN(8) + TxID(8) + Type(1) + PageID(4) + DataLen(4) = 25 bytes
- `ReadAll` scans entire WAL for recovery; `Truncate` clears after checkpoint

### L2: Buffer Pool — `storage/pool/`

- Fixed-size in-memory page cache
- **Clock-sweep eviction**: clock pointer, ref bit, two passes max (2× pool size)
- Pin/unpin semantics: pinned pages (pinCount > 0) cannot be evicted
- Dirty tracking: dirty pages flushed to disk on eviction or explicit flush
- `FetchPage` → `NewPage` → `UnpinPage` → `FlushPage` → `FlushAll`

### L3: Slotted Page Layout — `storage/page/`

- Variable-size records within fixed 8KB pages
- Layout: `[header 20B][slot directory →  ← records]`
- Header: pageID(4) + pageType(2) + slotCount(2) + freeSpacePtr(2) + freeSpaceEnd(2) + nextPage(4) + reserved(4)
- Slot entry: offset(2) + length(2) = 4 bytes, grows downward from header
- Records grow upward from end of page
- Tombstone: slot offset = `0xFFFF`
- Compact repacks live records to reclaim fragmented space

### L3.5: Binary Encoding — `storage/encoding/`

- Little-endian binary serialization for Node and Edge
- Node: `NodeID(8) | labelCount(2) | labels(len+data)... | properties`
- Edge: `EdgeID(8) | typeLen(2) | type | SourceID(8) | TargetID(8) | properties`
- Properties: `count(2) | (keyLen(2) + key + type(1) + dataLen(2) + data)...`

### L4: Record Manager — `storage/record/`

- CRUD for nodes and edges
- Node pages and edge pages maintained as separate linked lists (via `nextPage` pointer)
- **Currently uses in-memory maps** for ID→RID lookup and adjacency lists (not B+Tree)
- Insert: try tail page → if full, allocate new page and link
- Update: in-place if new data fits, else delete + reinsert
- DeleteNode fails if edges still connected

### L5: Index Manager — `storage/index/`

- B+Tree with `uint64` keys → `RID` values
- Internal nodes: child pointers + separator keys; leaf nodes: key-RID pairs + next-leaf pointer
- Split: mid key promoted; leaf split preserves sibling links
- Delete: leaf-only removal, **no rebalancing/merge** (simplified)
- RangeScan follows next-leaf chain
- IndexManager creates 4 trees: NodeIndex, EdgeIndex, OutIndex (source adjacency), InIndex (target adjacency)
- Adjacency key packing: `(nodeID << 32) | edgeID` for range scan by node

### L6: Transaction Manager — `storage/txn/`

- `Begin` → `WritePage` → `Commit`/`Abort`
- WritePage: log before/after to WAL, acquire page-level lock
- Commit: WAL commit record, sync, release locks
- Abort: undo via before-images, WAL abort record
- Checkpoint: flush pool, sync disk, log checkpoint, truncate WAL
- **Page-level locking**: one transaction per page, no deadlock detection
- ARIES recovery (`recovery.go`): analysis → redo (committed) → undo (active), then sync + truncate

## Dependency Graph

```
graph/model.go  ←─── encoding/ ←─── record/
                                        ↑
storage/types.go ←── disk/ ←── pool/ ←──┤
                      ↑         ↑        │
                     wal/ ←── txn/       │
                                        │
                     pool/ ←── index/ ───┘  (not wired yet)
```

Sub-packages only import downward + `storage/` for shared types. No circular deps.

## On-Disk File Layout

### Data File (`data.db`)

```
Page 0 (Header):
  [0..3]   magic       = 0x4E454F34
  [4..7]   version     = 1
  [8..11]  pageCount
  [12..15] freeHead    (PageID of first free page, or 0)
  [16..23] nextNodeID
  [24..31] nextEdgeID

Pages 1..N:
  Each page starts with:
    [0..3]  pageID
    [4..5]  pageType (0=free, 1=node, 2=edge, 3=btree-int, 4=btree-leaf)
  Rest depends on page type (slotted page or B+Tree node)
```

### WAL File

```
Sequential records:
  [LSN 8B][TxID 8B][Type 1B][PageID 4B][DataLen 4B][BeforeData...][AfterData...]
  
  Type=Update:  DataLen = PageSize, Before = 8KB, After = 8KB
  Type=Commit:  DataLen = 0
  Type=Abort:   DataLen = 0
  Type=Checkpoint: DataLen = 0
```
