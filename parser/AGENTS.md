<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-03-19 | Updated: 2026-03-19 -->

# parser

## Purpose
Cypher-lite frontend: lexer, token definitions, AST node types, and a recursive-descent parser. Transforms a raw query string into a typed `Statement` AST that the `query` package can execute. Supports a deliberate subset of Cypher: MATCH, CREATE, RETURN, SET, DELETE, WHERE, patterns with nodes and directed/undirected edges, property maps, parameters, and common expressions.

## Key Files

| File | Description |
|------|-------------|
| `token.go` | `TokenType` enum (literals, keywords, symbols), `Token` struct, `LookupKeyword` (case-insensitive) |
| `lexer.go` | `Lexer` — tokenises a Cypher string into a `[]Token` stream |
| `ast.go` | All AST node types: `Statement`, clause types (`MatchClause`, `CreateClause`, etc.), `Pattern`/`PatternElement`, expression types (`LiteralExpr`, `PropExpr`, `BinaryExpr`, etc.) |
| `parser.go` | `Parser` — recursive-descent parser, entry point `Parse(input string) (*Statement, error)` |
| `lexer_test.go` | Lexer unit tests |
| `parser_test.go` | Parser unit tests |

## For AI Agents

### Working In This Directory
- The grammar is intentionally minimal — do not add features not needed by the query executor
- Keywords are case-insensitive (handled in `LookupKeyword`)
- `Direction` in `ast.go` (`DirRight`, `DirLeft`, `DirNone`) maps to `graph.Direction` via `astDirToGraph` in `query/executor.go`
- `TokenType` constants are also used as operators in `BinaryExpr` and `UnaryExpr` — the `Op` field holds a `TokenType` like `EQ`, `AND`, `NOT`

### Testing Requirements
- `go test ./parser/...`
- Add test cases to `lexer_test.go` for new tokens, `parser_test.go` for new grammar rules
- Parse errors must return descriptive messages prefixed with `"parse error:"`

### Common Patterns
- `PatternElement` is a sealed interface — only `*NodePattern` and `*EdgePattern` implement it
- `Expr` is a sealed interface — all expression types implement `expr()`
- Parser uses a single-token lookahead (`peek()`/`consume()`) pattern

## Dependencies

### Internal
None — parser is dependency-free within Neo4g.

### External
- `strings` — `strings.ToUpper` for keyword lookup

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
