#!/usr/bin/env bash
set -euo pipefail

# ── Config ───────────────────────────────────────────────────────────
TMPDIR_E2E=$(mktemp -d)
NEO4G_BIN="$TMPDIR_E2E/neo4g"
GW_BIN="$TMPDIR_E2E/neo4g-gateway"
PASS=0
FAIL=0

PIDS=()

# ── Cleanup on exit ─────────────────────────────────────────────────
cleanup() {
    for pid in "${PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
    done
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
query_via() {
    local port="$1"
    local q="$2"
    local body
    body=$(printf '{"query": %s}' "$(echo "$q" | jq -Rs .)")
    curl -s -w "\n%{http_code}" -X POST "http://127.0.0.1:${port}/api/query" \
        -H 'Content-Type: application/json' \
        -d "$body"
}

health() {
    local port="$1"
    curl -s "http://127.0.0.1:${port}/api/health" 2>/dev/null || echo '{"status":"unreachable"}'
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

assert_error() {
    local label="$1"
    local response="$2"
    local http_code
    http_code=$(echo "$response" | tail -1)

    if [[ "$http_code" != "200" ]]; then
        echo "  PASS  $label (got HTTP $http_code as expected)"
        PASS=$((PASS + 1))
    else
        echo "  FAIL  $label — expected error, got 200"
        FAIL=$((FAIL + 1))
    fi
}

wait_for_port() {
    local logfile="$1"
    local pattern="$2"
    local port=""
    for _ in $(seq 1 40); do
        if [[ -f "$logfile" ]]; then
            port=$(grep -oE "$pattern" "$logfile" 2>/dev/null | grep -oE '[0-9]+$' || true)
            if [[ -n "$port" ]]; then
                echo "$port"
                return 0
            fi
        fi
        sleep 0.25
    done
    echo ""
    return 1
}

# ── Build ────────────────────────────────────────────────────────────
echo "Building neo4g + neo4g-gateway..."
cd "$(dirname "$0")/.."
go build -o "$NEO4G_BIN" ./cmd/neo4g
go build -o "$GW_BIN" ./cmd/neo4g-gateway
echo "Binaries built"

# ── Start 3 nodes ───────────────────────────────────────────────────
echo ""
echo "Starting 3-node cluster..."

# Node 1 (primary)
N1_LOG="$TMPDIR_E2E/node1.log"
NEO4G_NODE_ID=node1 NEO4G_ROLE=primary NEO4G_LISTEN_ADDR="127.0.0.1:0" \
    NEO4G_DATA_DIR="$TMPDIR_E2E/data1" "$NEO4G_BIN" > "$N1_LOG" 2>&1 &
PIDS+=($!)

N1_PORT=$(wait_for_port "$N1_LOG" 'listening on 127\.0\.0\.1:[0-9]+')
if [[ -z "$N1_PORT" ]]; then
    echo "ERROR: node1 failed to start"; cat "$N1_LOG" 2>/dev/null; exit 1
fi
echo "  node1 (primary) on port $N1_PORT"

# Node 2 (replica)
N2_LOG="$TMPDIR_E2E/node2.log"
NEO4G_NODE_ID=node2 NEO4G_ROLE=replica NEO4G_PRIMARY_ADDR="http://127.0.0.1:${N1_PORT}" \
    NEO4G_LISTEN_ADDR="127.0.0.1:0" NEO4G_DATA_DIR="$TMPDIR_E2E/data2" \
    NEO4G_REPL_POLL_INTERVAL="200ms" "$NEO4G_BIN" > "$N2_LOG" 2>&1 &
PIDS+=($!)

N2_PORT=$(wait_for_port "$N2_LOG" 'listening on 127\.0\.0\.1:[0-9]+')
if [[ -z "$N2_PORT" ]]; then
    echo "ERROR: node2 failed to start"; cat "$N2_LOG" 2>/dev/null; exit 1
fi
echo "  node2 (replica) on port $N2_PORT"

# Node 3 (replica)
N3_LOG="$TMPDIR_E2E/node3.log"
NEO4G_NODE_ID=node3 NEO4G_ROLE=replica NEO4G_PRIMARY_ADDR="http://127.0.0.1:${N1_PORT}" \
    NEO4G_LISTEN_ADDR="127.0.0.1:0" NEO4G_DATA_DIR="$TMPDIR_E2E/data3" \
    NEO4G_REPL_POLL_INTERVAL="200ms" "$NEO4G_BIN" > "$N3_LOG" 2>&1 &
PIDS+=($!)

N3_PORT=$(wait_for_port "$N3_LOG" 'listening on 127\.0\.0\.1:[0-9]+')
if [[ -z "$N3_PORT" ]]; then
    echo "ERROR: node3 failed to start"; cat "$N3_LOG" 2>/dev/null; exit 1
fi
echo "  node3 (replica) on port $N3_PORT"

# ── Start gateway ───────────────────────────────────────────────────
echo ""
echo "Starting gateway..."
GW_LOG="$TMPDIR_E2E/gateway.log"
NEO4G_GW_LISTEN_ADDR="127.0.0.1:0" \
    NEO4G_GW_NODES="node1=http://127.0.0.1:${N1_PORT},node2=http://127.0.0.1:${N2_PORT},node3=http://127.0.0.1:${N3_PORT}" \
    NEO4G_GW_HEARTBEAT_INTERVAL="1s" NEO4G_GW_HEARTBEAT_TIMEOUT="3s" \
    NEO4G_GW_ELECTION_DELAY="500ms" \
    "$GW_BIN" > "$GW_LOG" 2>&1 &
PIDS+=($!)

GW_PORT=$(wait_for_port "$GW_LOG" 'listening on 127\.0\.0\.1:[0-9]+')
if [[ -z "$GW_PORT" ]]; then
    echo "ERROR: gateway failed to start"; cat "$GW_LOG" 2>/dev/null; exit 1
fi
echo "  gateway on port $GW_PORT"

# Wait for heartbeat to discover nodes
sleep 3

# ── Tests ────────────────────────────────────────────────────────────
echo ""
echo "Running replication e2e tests..."
echo "────────────────────────────────────"

# 1. Health check — all nodes alive
echo ""
echo "== Health checks =="
H1=$(health "$N1_PORT")
assert_contains "node1 healthy" "$H1" '"status":"ok"'
H2=$(health "$N2_PORT")
assert_contains "node2 healthy" "$H2" '"status":"ok"'
H3=$(health "$N3_PORT")
assert_contains "node3 healthy" "$H3" '"status":"ok"'

# 2. Write through gateway
echo ""
echo "== Write through gateway =="
RESP=$(query_via "$GW_PORT" 'CREATE (n:Person {name: "Alice", age: 30})')
BODY=$(assert_ok "CREATE Alice via gateway" "$RESP") || true
assert_contains "Alice created" "$BODY" "Alice"

RESP=$(query_via "$GW_PORT" 'CREATE (n:Person {name: "Bob", age: 25})')
BODY=$(assert_ok "CREATE Bob via gateway" "$RESP") || true
assert_contains "Bob created" "$BODY" "Bob"

# 3. Read from primary directly
echo ""
echo "== Read from primary =="
RESP=$(query_via "$N1_PORT" 'MATCH (n:Person) RETURN n.name')
BODY=$(assert_ok "Read from primary" "$RESP") || true
assert_contains "Alice on primary" "$BODY" "Alice"
assert_contains "Bob on primary" "$BODY" "Bob"

# 4. Wait for replication to propagate, then read from replicas
echo ""
echo "== Replication check =="
sleep 2

RESP=$(query_via "$N2_PORT" 'MATCH (n:Person) RETURN n.name')
BODY=$(assert_ok "Read from replica node2" "$RESP") || true
assert_contains "Alice replicated to node2" "$BODY" "Alice"
assert_contains "Bob replicated to node2" "$BODY" "Bob"

RESP=$(query_via "$N3_PORT" 'MATCH (n:Person) RETURN n.name')
BODY=$(assert_ok "Read from replica node3" "$RESP") || true
assert_contains "Alice replicated to node3" "$BODY" "Alice"
assert_contains "Bob replicated to node3" "$BODY" "Bob"

# 5. Read through gateway (round-robin)
echo ""
echo "== Read through gateway (round-robin) =="
RESP=$(query_via "$GW_PORT" 'MATCH (n:Person) RETURN n.name')
BODY=$(assert_ok "Read via gateway #1" "$RESP") || true
assert_contains "Data via gateway" "$BODY" "Alice"

RESP=$(query_via "$GW_PORT" 'MATCH (n:Person) RETURN n.name')
BODY=$(assert_ok "Read via gateway #2" "$RESP") || true
assert_contains "Data via gateway round-robin" "$BODY" "Bob"

# 6. Replica rejects writes directly
echo ""
echo "== Replica rejects writes =="
RESP=$(query_via "$N2_PORT" 'CREATE (n:Person {name: "Charlie"})')
assert_error "Replica rejects CREATE" "$RESP"

# 7. Kill primary and test election
echo ""
echo "== Failover test =="
echo "Killing node1 (primary)..."
kill "${PIDS[0]}" 2>/dev/null || true
wait "${PIDS[0]}" 2>/dev/null || true

# Wait for heartbeat to detect failure + election
sleep 5

# Gateway should have elected a new primary
GW_HEALTH=$(health "$GW_PORT")
echo "  Gateway health: $GW_HEALTH"

# Write through gateway should still work (new primary)
RESP=$(query_via "$GW_PORT" 'CREATE (n:Person {name: "Charlie", age: 20})')
BODY=$(assert_ok "CREATE Charlie after failover" "$RESP") || true
assert_contains "Charlie created on new primary" "$BODY" "Charlie"

echo ""
echo "────────────────────────────────────"
echo "All replication e2e tests complete."
