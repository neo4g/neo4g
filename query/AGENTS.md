<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# query

## Purpose
Query execution engine. Takes a parsed `*parser.Statement` AST and executes it against the storage layer. Handles MATCH pattern resolution (with predicate pushdown and parallel pattern evaluation via `errgroup`), and dispatches to CREATE, RETURN, SET, or DELETE action handlers. Also provides the `graph.Value` ↔ Go primitive bridge used by both expression evaluation and HTTP serialization.

## Key Files

| File | Description |
|------|-------------|
| `executor.go` | `Executor` struct; `Execute()` entry point; MATCH resolution with predicate pushdown; CREATE/RETURN/SET/DELETE handlers; `crossProduct` for multi-pattern joins |
| `eval.go` | Expression evaluator `evalExpr()` — handles all `parser.Expr` types including property access, parameters, function calls (`id()`), binary/unary operators |
| `result.go` | `Row`, `Result`, `RowData` types; `ValueToGo`/`GoToValue` type bridge; `NodeToMap`/`EdgeToMap` JSON serializers |
| `executor_test.go` | Executor integration tests |
| `eval_test.go` | Expression evaluator unit tests |

## For AI Agents

### Working In This Directory
- `Executor` takes `storage.Store` and `storage.Scanner` interfaces — never the concrete `record.Manager` directly
- Predicate pushdown (`extractPredicates`) decomposes `WHERE` equality predicates into per-variable filters applied during `ScanNodes`, reducing the cross-product size
- Multiple MATCH patterns are resolved in parallel using `golang.org/x/sync/errgroup`; results are cross-producted
- `Row` is `map[string]interface{}` where values are `*graph.Node`, `*graph.Edge`, or Go primitives
- `serialize()` converts Row values to JSON-friendly maps before building `Result`

### Testing Requirements
- `go test ./query/...`
- Executor tests typically use an in-memory store mock or a real storage stack
- Expression evaluator tests in `eval_test.go` can use synthetic `Row` maps

### Common Patterns
- All action handlers (`execCreate`, `execReturn`, `execSet`, `execDelete`) receive the already-resolved `[]Row` from MATCH and a `params map[string]interface{}`
- `GoToValue` / `ValueToGo` are the canonical bridge between storage `graph.Value` and Go primitives — use them consistently
- `RETURN *` collects all bound variable names from the first row and sorts them for deterministic column ordering

## Dependencies

### Internal
- `github.com/neo4g/neo4g/graph` — `Node`, `Edge`, `Value` types
- `github.com/neo4g/neo4g/parser` — AST node types
- `github.com/neo4g/neo4g/storage` — `Store` and `Scanner` interfaces

### External
- `golang.org/x/sync/errgroup` — parallel pattern resolution
- `sort` — deterministic `RETURN *` column ordering

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
