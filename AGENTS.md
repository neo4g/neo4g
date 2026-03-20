<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# Neo4g

## Purpose
A minimal Neo4j-equivalent graph database written from scratch in Go for CS464. Implements the property graph model — nodes with labels and properties, directed edges with types and properties — backed by a fully custom 6-layer storage engine with ACID transactions, WAL-based crash recovery, B+Tree indexes, and a Cypher-lite query parser/executor. Exposed via a single HTTP endpoint (`POST /api/query`).

## Key Files

| File | Description |
|------|-------------|
| `go.mod` | Module definition (`github.com/seankohjs/neo4g`); requires Go 1.25+, `golang.org/x/sync` |
| `go.sum` | Dependency checksums |
| `README.md` | Build/run instructions, full API reference, Cypher-lite syntax guide |
| `test.md` | Manual test notes / scratch pad |
| `LICENSE` | Project license |

## Subdirectories

| Directory | Purpose |
|-----------|---------|
| `cmd/neo4g/` | Binary entry point — wires config, server, signal handling (see `cmd/neo4g/AGENTS.md`) |
| `server/` | HTTP server, database engine lifecycle, configuration (see `server/AGENTS.md`) |
| `parser/` | Cypher-lite lexer, AST nodes, recursive-descent parser (see `parser/AGENTS.md`) |
| `query/` | Query executor — MATCH resolution, CREATE/RETURN/SET/DELETE dispatch (see `query/AGENTS.md`) |
| `graph/` | Core domain model — Node, Edge, Value types (see `graph/AGENTS.md`) |
| `storage/` | 6-layer storage engine — disk, WAL, pool, page, encoding, index, record, txn (see `storage/AGENTS.md`) |
| `scripts/` | Shell scripts for end-to-end tests and benchmarks (see `scripts/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- This is a CS464 academic project — intentionally minimal, no clustering, no query optimizer
- Module path is `github.com/seankohjs/neo4g`; all imports use this prefix
- Build with `go build -o neo4g ./cmd/neo4g`; test with `go test ./...`
- Do not add external dependencies without strong justification — the current dep surface is intentionally tiny (`golang.org/x/sync` only)

### Testing Requirements
- Unit tests: `go test ./...` (each package has `*_test.go` files)
- End-to-end test: `./scripts/e2e.sh` (builds binary, starts server, runs curl checks, cleans up)
- Benchmark: `./scripts/benchmark.sh`

### Common Patterns
- Interfaces are defined in `storage/ifaces.go`; implementations satisfy them via compile-time assertions (`var _ storage.Store = (*record.Manager)(nil)`)
- Each storage sub-package is a clean layer — import only from layers below
- All public structs are thread-safe via `sync.Mutex` or `sync.RWMutex`

## Dependencies

### Internal
All packages communicate through well-defined interfaces in `storage/ifaces.go`.

### External
- `golang.org/x/sync` — `errgroup` for parallel MATCH resolution

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
