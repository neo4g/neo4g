<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# graph

## Purpose
Core domain model for the Neo4g property graph database. Defines the fundamental data types — `Node`, `Edge`, and `Value` — that flow through every layer of the system, from storage serialization up to the HTTP response. This package has zero internal dependencies; all other packages import from it.

## Key Files

| File | Description |
|------|-------------|
| `model.go` | All domain types: `NodeID`, `Node`, `EdgeID`, `Edge`, `Direction`, `Value`, `ValueType`, plus value constructors and accessors |

## For AI Agents

### Working In This Directory
- This package must remain dependency-free (no imports from other Neo4g packages)
- `Value` stores raw bytes + a type tag; use the constructor helpers (`IntValue`, `StringValue`, etc.) — never construct `Value{}` directly with raw bytes
- `NodeID(0)` and `EdgeID(0)` are reserved sentinel values meaning "no node/edge" — IDs start at 1
- `Direction` (`Outgoing`, `Incoming`, `Both`) is used by the storage scanner and query executor for edge traversal

### Testing Requirements
- No dedicated test file currently; logic is exercised through storage encoding tests and query executor tests
- If adding new `Value` types, update `storage/encoding/encoding.go` and `query/result.go` (`ValueToGo`/`GoToValue`) in tandem

### Common Patterns
- `Value` accessors (`AsInt()`, `AsString()`, etc.) panic on type mismatch — always check `v.Type` before calling
- Properties are `map[string]Value` on both `Node` and `Edge`

## Dependencies

### Internal
None — this is the base package.

### External
- `encoding/binary` — little-endian byte encoding for `IntValue`/`FloatValue`
- `math` — `Float64bits`/`Float64frombits` for float encoding

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
