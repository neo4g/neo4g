#!/usr/bin/env bash
set -euo pipefail

# ── Config ───────────────────────────────────────────────────────────
TMPDIR_E2E=$(mktemp -d)
BINARY="$TMPDIR_E2E/neo4g"
DATA_DIR="$TMPDIR_E2E/data"
PORT=0  # kernel picks a free port
PASS=0
FAIL=0

# ── Cleanup on exit ─────────────────────────────────────────────────
cleanup() {
    if [[ -n "${SERVER_PID:-}" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    rm -rf "$TMPDIR_E2E"
    echo ""
    echo "════════════════════════════════════"
    echo "  PASSED: $PASS   FAILED: $FAIL"
    echo "════════════════════════════════════"
    if [[ $FAIL -gt 0 ]]; then
        exit 1
    fi
}
trap cleanup EXIT

# ── Helpers ──────────────────────────────────────────────────────────
query() {
    local q="$1"
    local params="${2:-}"
    local body
    if [[ -n "$params" ]]; then
        body=$(printf '{"query": %s, "params": %s}' "$(echo "$q" | jq -Rs .)" "$params")
    else
        body=$(printf '{"query": %s}' "$(echo "$q" | jq -Rs .)")
    fi
    curl -s -w "\n%{http_code}" -X POST "http://127.0.0.1:${ACTUAL_PORT}/api/query" \
        -H 'Content-Type: application/json' \
        -d "$body"
}

assert_ok() {
    local label="$1"
    local response="$2"
    local http_code
    http_code=$(echo "$response" | tail -1)
    local body
    body=$(echo "$response" | sed '$d')

    if [[ "$http_code" != "200" ]]; then
        echo "  FAIL  $label — HTTP $http_code"
        echo "        $body"
        FAIL=$((FAIL + 1))
        return 1
    fi
    echo "  PASS  $label"
    PASS=$((PASS + 1))
    echo "$body"
}

assert_contains() {
    local label="$1"
    local body="$2"
    local expected="$3"

    if echo "$body" | grep -q "$expected"; then
        echo "  PASS  $label (contains '$expected')"
        PASS=$((PASS + 1))
    else
        echo "  FAIL  $label — expected '$expected' in response"
        echo "        $body"
        FAIL=$((FAIL + 1))
    fi
}

assert_not_contains() {
    local label="$1"
    local body="$2"
    local unexpected="$3"

    if echo "$body" | grep -q "$unexpected"; then
        echo "  FAIL  $label — did NOT expect '$unexpected' in response"
        echo "        $body"
        FAIL=$((FAIL + 1))
    else
        echo "  PASS  $label (does not contain '$unexpected')"
        PASS=$((PASS + 1))
    fi
}

# ── Build ────────────────────────────────────────────────────────────
echo "Building neo4g..."
cd "$(dirname "$0")/.."
go build -o "$BINARY" ./cmd/neo4g
echo "Binary: $BINARY"

# ── Start server ─────────────────────────────────────────────────────
SERVER_LOG="$TMPDIR_E2E/server.log"
echo "Starting server..."
NEO4G_DATA_DIR="$DATA_DIR" NEO4G_LISTEN_ADDR="127.0.0.1:0" "$BINARY" > "$SERVER_LOG" 2>&1 &
SERVER_PID=$!

# Wait for server to log its listening address
ACTUAL_PORT=""
for i in $(seq 1 40); do
    if [[ -f "$SERVER_LOG" ]]; then
        ACTUAL_PORT=$(grep -oE 'listening on 127\.0\.0\.1:[0-9]+' "$SERVER_LOG" 2>/dev/null | grep -oE '[0-9]+$' || true)
        if [[ -n "$ACTUAL_PORT" ]]; then
            break
        fi
    fi
    sleep 0.25
done

if [[ -z "${ACTUAL_PORT:-}" ]]; then
    echo "ERROR: could not determine server port"
    cat "$SERVER_LOG" 2>/dev/null || true
    exit 1
fi
echo "Server running on port $ACTUAL_PORT (PID $SERVER_PID)"

# ── Tests ────────────────────────────────────────────────────────────
echo ""
echo "Running end-to-end tests..."
echo "────────────────────────────────────"

# 1. CREATE Alice
RESP=$(query 'CREATE (n:Person {name: "Alice", age: 30})')
BODY=$(assert_ok "CREATE Alice" "$RESP") || true
assert_contains "Alice in response" "$BODY" "Alice"

# 2. CREATE Bob
RESP=$(query 'CREATE (n:Person {name: "Bob", age: 25})')
BODY=$(assert_ok "CREATE Bob" "$RESP") || true
assert_contains "Bob in response" "$BODY" "Bob"

# 3. MATCH all Person nodes
RESP=$(query 'MATCH (n:Person) RETURN n')
BODY=$(assert_ok "MATCH Person" "$RESP") || true
assert_contains "Alice exists" "$BODY" "Alice"
assert_contains "Bob exists" "$BODY" "Bob"

# 4. MATCH with WHERE filter
RESP=$(query 'MATCH (n:Person) WHERE n.name = "Alice" RETURN n.name')
BODY=$(assert_ok "WHERE filter" "$RESP") || true
assert_contains "Filtered to Alice" "$BODY" "Alice"
assert_not_contains "Bob filtered out" "$BODY" "Bob"

# 5. MATCH with parameter
RESP=$(query 'MATCH (n:Person) WHERE n.name = $name RETURN n.name' '{"name": "Bob"}')
BODY=$(assert_ok "Param query" "$RESP") || true
assert_contains "Param found Bob" "$BODY" "Bob"

# 6. CREATE edge
RESP=$(query 'MATCH (a), (b) WHERE a.name = "Alice" AND b.name = "Bob" CREATE (a)-[:KNOWS {since: 2024}]->(b)')
BODY=$(assert_ok "CREATE edge" "$RESP") || true
assert_contains "Edge type KNOWS" "$BODY" "KNOWS"

# 7. Traverse edge
RESP=$(query 'MATCH (n:Person)-[:KNOWS]->(m:Person) RETURN n.name, m.name')
BODY=$(assert_ok "Traverse edge" "$RESP") || true
assert_contains "Source Alice" "$BODY" "Alice"
assert_contains "Target Bob" "$BODY" "Bob"

# 8. RETURN *
RESP=$(query 'MATCH (n:Person) WHERE n.name = "Alice" RETURN *')
BODY=$(assert_ok "RETURN *" "$RESP") || true
assert_contains "Star returns node" "$BODY" "Alice"

# 9. SET update
RESP=$(query 'MATCH (n:Person) WHERE n.name = "Alice" SET n.name = "Alicia", n.age = 31')
assert_ok "SET update" "$RESP" > /dev/null || true

RESP=$(query 'MATCH (n:Person) WHERE n.name = "Alicia" RETURN n.name, n.age')
BODY=$(assert_ok "Verify SET" "$RESP") || true
assert_contains "Name updated" "$BODY" "Alicia"

# 10. DELETE edge
RESP=$(query 'MATCH (a)-[e:KNOWS]->(b) WHERE a.name = "Alicia" DELETE e')
assert_ok "DELETE edge" "$RESP" > /dev/null || true

RESP=$(query 'MATCH (a:Person)-[:KNOWS]->(b:Person) RETURN a')
BODY=$(assert_ok "Verify edge gone" "$RESP") || true
assert_not_contains "No traversal results" "$BODY" "Alicia"

# 11. DELETE node
RESP=$(query 'MATCH (n:Person) WHERE n.name = "Alicia" DELETE n')
assert_ok "DELETE node" "$RESP" > /dev/null || true

RESP=$(query 'MATCH (n:Person) RETURN n.name')
BODY=$(assert_ok "Verify node gone" "$RESP") || true
assert_not_contains "Alicia deleted" "$BODY" "Alicia"
assert_contains "Bob survives" "$BODY" "Bob"

echo ""
echo "────────────────────────────────────"
echo "All e2e tests complete."
