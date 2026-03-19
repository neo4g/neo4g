# Neo4g

A minimal Neo4j-equivalent graph database written from scratch in Go. Property graph model — nodes with labels and properties, directed edges with types and properties — backed by a custom storage engine with ACID transactions.

Built for CS464. Not a production database — no clustering, no query optimizer, intentionally minimal.

## Build

Requires Go 1.22+.

```bash
go build -o neo4g ./cmd/neo4g
```

## Run

```bash
./neo4g
```

The server starts on `:7474` by default. All configuration is via environment variables:

```bash
NEO4G_LISTEN_ADDR=:8080 NEO4G_DATA_DIR=/tmp/mydb ./neo4g
```

Stop with `Ctrl-C` — the server drains connections, checkpoints the WAL, and flushes all pages.

## Configuration

| Environment Variable | Default | Description |
|---|---|---|
| `NEO4G_DATA_DIR` | `./data` | Directory for database and WAL files |
| `NEO4G_DB_FILE` | `neo4g.db` | Data file name (relative to data dir) |
| `NEO4G_WAL_ENABLED` | `true` | Enable write-ahead log for crash recovery |
| `NEO4G_WAL_FILE` | `neo4g.wal` | WAL file name (relative to data dir) |
| `NEO4G_POOL_SIZE` | `1024` | Buffer pool frame count |
| `NEO4G_LISTEN_ADDR` | `:7474` | HTTP listen address |
| `NEO4G_READ_TIMEOUT` | `30s` | Max duration for reading a request |
| `NEO4G_WRITE_TIMEOUT` | `30s` | Max duration for writing a response |
| `NEO4G_SHUTDOWN_TIMEOUT` | `10s` | Max duration for graceful shutdown |

## API

Single endpoint:

```
POST /api/query
Content-Type: application/json

{
  "query": "<cypher-lite query>",
  "params": { ... }          // optional
}
```

**Success response:**

```json
{
  "data": {
    "columns": ["n"],
    "rows": [{ "n": { "id": 1, "labels": ["Person"], "properties": {"name": "Alice"} } }]
  }
}
```

**Error response:**

```json
{ "error": "parse error: ..." }
```

## Cypher-lite Query Guide

### Create a node

```bash
curl -s -X POST http://localhost:7474/api/query \
  -H 'Content-Type: application/json' \
  -d '{"query": "CREATE (n:Person {name: \"Alice\", age: 30})"}'
```

### Create another node

```bash
curl -s -X POST http://localhost:7474/api/query \
  -H 'Content-Type: application/json' \
  -d '{"query": "CREATE (n:Person {name: \"Bob\", age: 25})"}'
```

### Create an edge (MATCH + CREATE)

```bash
curl -s -X POST http://localhost:7474/api/query \
  -H 'Content-Type: application/json' \
  -d '{"query": "MATCH (a), (b) WHERE a.name = \"Alice\" AND b.name = \"Bob\" CREATE (a)-[:KNOWS {since: 2024}]->(b)"}'
```

### Match nodes by label

```bash
curl -s -X POST http://localhost:7474/api/query \
  -H 'Content-Type: application/json' \
  -d '{"query": "MATCH (n:Person) RETURN n"}'
```

### Match with WHERE filter

```bash
curl -s -X POST http://localhost:7474/api/query \
  -H 'Content-Type: application/json' \
  -d '{"query": "MATCH (n:Person) WHERE n.name = \"Alice\" RETURN n"}'
```

### Match by ID

```bash
curl -s -X POST http://localhost:7474/api/query \
  -H 'Content-Type: application/json' \
  -d '{"query": "MATCH (n) WHERE id(n) = 1 RETURN n"}'
```

### Match with parameters

```bash
curl -s -X POST http://localhost:7474/api/query \
  -H 'Content-Type: application/json' \
  -d '{"query": "MATCH (n:Person) WHERE n.name = $name RETURN n", "params": {"name": "Alice"}}'
```

### Traverse edges

```bash
curl -s -X POST http://localhost:7474/api/query \
  -H 'Content-Type: application/json' \
  -d '{"query": "MATCH (n:Person)-[:KNOWS]->(m:Person) RETURN n, m"}'
```

### Return specific properties

```bash
curl -s -X POST http://localhost:7474/api/query \
  -H 'Content-Type: application/json' \
  -d '{"query": "MATCH (n:Person) RETURN n.name, n.age"}'
```

### Return all bound variables

```bash
curl -s -X POST http://localhost:7474/api/query \
  -H 'Content-Type: application/json' \
  -d '{"query": "MATCH (n:Person) RETURN *"}'
```

### Return with alias

```bash
curl -s -X POST http://localhost:7474/api/query \
  -H 'Content-Type: application/json' \
  -d '{"query": "MATCH (n:Person) RETURN n.name AS personName"}'
```

### Update properties (SET)

```bash
curl -s -X POST http://localhost:7474/api/query \
  -H 'Content-Type: application/json' \
  -d '{"query": "MATCH (n:Person) WHERE n.name = \"Alice\" SET n.name = \"Alicia\", n.age = 31"}'
```

### Delete an edge

```bash
curl -s -X POST http://localhost:7474/api/query \
  -H 'Content-Type: application/json' \
  -d '{"query": "MATCH (a)-[e:KNOWS]->(b) WHERE a.name = \"Alicia\" DELETE e"}'
```

### Delete a node

```bash
curl -s -X POST http://localhost:7474/api/query \
  -H 'Content-Type: application/json' \
  -d '{"query": "MATCH (n:Person) WHERE n.name = \"Alicia\" DELETE n"}'
```

## Supported Cypher-lite Syntax

```
-- Clauses
CREATE (n:Label {key: value, ...})
MATCH (n:Label) WHERE <expr> RETURN <items>
MATCH (n)-[:TYPE]->(m) RETURN n, m
SET n.key = value
DELETE n

-- Patterns
(variable:Label:Label {props})          Node
-[variable:TYPE {props}]->              Edge (right)
<-[variable:TYPE {props}]-              Edge (left)
-[variable:TYPE]-                       Edge (undirected)

-- Expressions
n.property                              Property access
id(n)                                   Built-in ID function
$param                                  Query parameter
=  <>  <  >  <=  >=                     Comparison
AND  OR  NOT                            Logical
true  false  null                       Literals
42  3.14  "string"                      Literals
```

## Run Tests

```bash
go test ./...
```

End-to-end test (builds, starts server, runs curl tests, cleans up):

```bash
./scripts/e2e.sh
```

## Project Structure

```
cmd/neo4g/       Entry point (main.go)
server/          HTTP server, engine lifecycle, config
parser/          Cypher-lite lexer, AST, recursive-descent parser
query/           Query executor (eval, MATCH resolution, action dispatch)
graph/           Domain model (Node, Edge, Value)
storage/
  disk/          Raw file I/O, free list, header page
  wal/           Write-ahead log
  pool/          Buffer pool with clock-sweep eviction
  page/          Slotted page format
  encoding/      Binary serialization for Node/Edge
  index/         B+Tree index + index manager
  record/        Record manager (CRUD, wired to indexes + transactions)
  txn/           Transaction manager, ARIES recovery
scripts/         End-to-end test script
```
