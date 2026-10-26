<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# storage/encoding

## Purpose
Binary serialization and deserialization of `graph.Node` and `graph.Edge` structs into byte slices suitable for storage in slotted pages. Handles encoding of all `graph.Value` types (null, int64, float64, string, bool, bytes), labels/types, and properties maps. Used exclusively by the record manager when reading and writing records to pages.

## Key Files

| File | Description |
|------|-------------|
| `encoding.go` | `EncodeNode`, `DecodeNode`, `EncodeEdge`, `DecodeEdge` — binary encode/decode for graph entities |
| `encoding_test.go` | Round-trip encode/decode tests for nodes and edges |

## For AI Agents

### Working In This Directory
- Encoding format is custom binary (little-endian) — not JSON, protobuf, or gob
- If you add a new `graph.ValueType`, you **must** update both `EncodeNode`/`EncodeEdge` (write path) and `DecodeNode`/`DecodeEdge` (read path) here, and also update `ValueToGo`/`GoToValue` in `query/result.go`
- Changing the encoding format without a format version bump will corrupt existing database files
- Tests should always include round-trip (encode → decode → compare) assertions

### Testing Requirements
- `go test ./storage/encoding/...`
- Cover all `ValueType` variants and edge cases (empty labels, empty properties, nil properties)

### Common Patterns
- Length-prefixed strings for labels, type, and property keys
- Properties are encoded as a count followed by key-value pairs
- `graph.Value.Data` bytes are written directly after a 1-byte type tag and 4-byte length

## Dependencies

### Internal
- `github.com/neo4g/neo4g/graph` — `Node`, `Edge`, `Value`, `ValueType` constants

### External
- Standard library: `encoding/binary`, `bytes`

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
