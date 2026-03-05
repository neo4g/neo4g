// Package storage implements the I/O layer for Neo4g, a graph database engine.
//
// # Architecture
//
// The storage layer is organized in 6 layers, each depending only on the one below it:
//
//	┌─────────────────────────────────┐
//	│  6. Transaction Manager (ACID)  │
//	├─────────────────────────────────┤
//	│  5. Index Manager (B+Tree)      │
//	├─────────────────────────────────┤
//	│  4. Record Manager              │
//	│     (Node/Edge CRUD)            │
//	├─────────────────────────────────┤
//	│  3. Slotted Page Layout         │
//	│     (variable-size records)     │
//	├─────────────────────────────────┤
//	│  2. Buffer Pool + Page Cache    │
//	├─────────────────────────────────┤
//	│  1. Disk Manager + WAL          │
//	│     (pages, free list, log)     │
//	└─────────────────────────────────┘
//
// # Layer 1: Disk Manager
//
// Manages raw page I/O using fixed 8KB pages. Pages are read/written at explicit
// file offsets using pread/pwrite semantics (via os.File.ReadAt/WriteAt). Page 0
// is reserved as a metadata header containing the magic number, version, page count,
// free list head, and index root pointers.
//
// The free list is an in-file linked list of deallocated pages. Allocating a page
// grabs from the free list head; if empty, the file is extended. Freeing a page
// prepends it to the list.
//
// See: disk.go, freelist.go
//
// # Layer 2: Buffer Pool
//
// An in-memory cache of fixed-size page frames with clock-sweep eviction. Pages are
// "pinned" while in use (preventing eviction) and "unpinned" when released. Dirty
// pages are tracked and flushed according to WAL protocol (log before data).
//
// The buffer pool provides predictable memory usage and control over flush ordering,
// which is critical for crash recovery correctness.
//
// See: buffer_pool.go
//
// # Layer 3: Slotted Page Layout
//
// Organizes variable-size records within a fixed 8KB page. Uses a slot directory
// that grows from the page header downward, while record data grows upward from
// the page end. Each record is addressed by a Record ID (RID) = (PageID, SlotIndex).
//
// Deletion uses tombstones; compaction reclaims space without invalidating RIDs.
//
// See: page_layout.go, rid.go
//
// # Layer 4: Record Manager
//
// Provides CRUD operations for nodes and edges using a property graph data model.
// Nodes have labels and key-value properties; edges have a type, source/target node
// references, and properties. Records are serialized using a custom binary encoding
// optimized for compact storage and fast deserialization.
//
// This is the primary API surface for the query layer built by other team members.
//
// See: record.go, node.go, edge.go, encoding.go
//
// # Layer 5: Index Manager (B+Tree)
//
// Disk-resident B+Tree indexes stored in buffer pool pages. Primary indexes
// (NodeID→RID, EdgeID→RID, adjacency) are always maintained. Secondary indexes
// on (Label, Property) combinations can be created for fast lookups.
//
// B+Trees are chosen for predictable I/O and efficient range scans, which map
// directly to adjacency list traversals in graph workloads.
//
// See: btree.go, index.go
//
// # Layer 6: Transaction Manager
//
// ACID transactions using write-ahead logging (WAL) with a simplified ARIES protocol.
// Uses page-level locking and a steal/no-force buffer management policy. On recovery,
// committed transactions are redone and uncommitted transactions are undone.
//
// See: wal.go, txn.go, recovery.go
package storage
