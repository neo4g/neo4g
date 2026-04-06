#!/usr/bin/env bash
set -euo pipefail

GW_PORT="${1:-7480}"
RUNS=100
OK=0
EMPTY_EDGES=0
ERRORS=0

echo "Hitting GET /api/graph via gateway (port $GW_PORT) $RUNS times..."
echo ""

for i in $(seq 1 $RUNS); do
    RESP=$(curl -s "http://localhost:${GW_PORT}/api/query" \
        -X POST -H 'Content-Type: application/json' \
        -d '{"query": "MATCH (a:Note)-[:LINKS_TO]->(b:Note) RETURN a, b"}')

    ROWS=$(echo "$RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('data',{}).get('rows',[])))" 2>/dev/null || echo "ERR")

    if [[ "$ROWS" == "ERR" ]]; then
        ERRORS=$((ERRORS + 1))
        echo "  [$i] ERROR: $RESP"
    elif [[ "$ROWS" == "0" ]]; then
        EMPTY_EDGES=$((EMPTY_EDGES + 1))
        echo "  [$i] EMPTY (0 edges)"
    else
        OK=$((OK + 1))
    fi
done

echo ""
echo "════════════════════════════════════"
echo "  Total:       $RUNS"
echo "  With edges:  $OK"
echo "  Empty edges: $EMPTY_EDGES"
echo "  Errors:      $ERRORS"
echo "════════════════════════════════════"

if [[ $EMPTY_EDGES -gt 0 ]]; then
    echo ""
    echo "Drift detected: $EMPTY_EDGES/$RUNS requests returned empty edges."
    echo "Checking each node directly..."
    echo ""

    # Try hitting each node from docker-compose defaults
    for PORT in 7474 7475 7476; do
        RESP=$(curl -s "http://localhost:${PORT}/api/query" \
            -X POST -H 'Content-Type: application/json' \
            -d '{"query": "MATCH (a:Note)-[:LINKS_TO]->(b:Note) RETURN a, b"}' 2>/dev/null || echo '{"error":"unreachable"}')
        ROWS=$(echo "$RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('data',{}).get('rows',[])))" 2>/dev/null || echo "ERR")
        HEALTH=$(curl -s "http://localhost:${PORT}/api/health" 2>/dev/null || echo '{"status":"unreachable"}')
        ROLE=$(echo "$HEALTH" | python3 -c "import sys,json; print(json.load(sys.stdin).get('role','?'))" 2>/dev/null || echo "?")
        LSN=$(echo "$HEALTH" | python3 -c "import sys,json; print(json.load(sys.stdin).get('lsn','?'))" 2>/dev/null || echo "?")
        echo "  :$PORT  role=$ROLE  lsn=$LSN  edges=$ROWS"
    done
fi
