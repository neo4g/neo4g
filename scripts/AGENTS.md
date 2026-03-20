<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# scripts

## Purpose
Shell scripts for automated end-to-end testing and performance benchmarking of the Neo4g server. These scripts build the binary, spin up a live server, execute queries via `curl`, verify responses, and clean up. They complement the Go unit tests by testing the full request-response pipeline.

## Key Files

| File | Description |
|------|-------------|
| `e2e.sh` | End-to-end test: builds `neo4g`, starts server, runs curl-based CRUD + traversal checks, tears down |
| `benchmark.sh` | Performance benchmark: sequential and concurrent read/write phases with configurable parameters; logs results |

## For AI Agents

### Working In This Directory
- Scripts assume they are run from the project root (`./scripts/e2e.sh`)
- `e2e.sh` will fail if port 7474 is already in use — kill any running `neo4g` process first
- Both scripts use a temporary data directory and clean up after themselves
- `benchmark.sh` uses configurable parameters (node count, concurrency) — check script header for env vars

### Testing Requirements
- Run `./scripts/e2e.sh` after any change to server, parser, query, or storage layers
- No Go test framework involvement — failures are detected via exit codes and `grep` on curl output

### Common Patterns
- Scripts use `set -e` (fail fast) and trap-based cleanup
- Server startup uses a brief `sleep` + health check loop before issuing queries

## Dependencies

### Internal
- Depends on the built `./neo4g` binary (built inside the script via `go build`)

### External
- `curl`, `jq` (optional), `bash`

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
