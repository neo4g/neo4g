#!/usr/bin/env bash
set -euo pipefail

# ── Defaults ─────────────────────────────────────────────────────────
NODES=500
WORKERS=10
READS_PER_WORKER=100
WAL_NOSYNC="${NEO4G_WAL_NOSYNC:-false}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --nodes)       NODES="$2"; shift 2 ;;
        --workers)     WORKERS="$2"; shift 2 ;;
        --reads-per-worker) READS_PER_WORKER="$2"; shift 2 ;;
        --nosync)      WAL_NOSYNC="true"; shift ;;
        *) echo "Unknown flag: $1"; exit 1 ;;
    esac
done

# ── Temp dir & cleanup ──────────────────────────────────────────────
TMPDIR_BENCH=$(mktemp -d)
BINARY="$TMPDIR_BENCH/neo4g"
DATA_DIR="$TMPDIR_BENCH/data"
SERVER_LOG="$TMPDIR_BENCH/server.log"
TIMINGS="$TMPDIR_BENCH/timings.txt"
PEAK_RSS=0

cleanup() {
    if [[ -n "${SERVER_PID:-}" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    rm -rf "$TMPDIR_BENCH"
}
trap cleanup EXIT

# ── Helpers ──────────────────────────────────────────────────────────
json_escape() {
    local s="$1"
    s="${s//\\/\\\\}"
    s="${s//\"/\\\"}"
    printf '"%s"' "$s"
}

query() {
    local q="$1"
    local params="${2:-}"
    local escaped
    escaped=$(json_escape "$q")
    local body
    if [[ -n "$params" ]]; then
        body="{\"query\": ${escaped}, \"params\": ${params}}"
    else
        body="{\"query\": ${escaped}}"
    fi
    curl -s -o /dev/null -w '%{time_total}\n' -X POST "http://127.0.0.1:${ACTUAL_PORT}/api/query" \
        -H 'Content-Type: application/json' \
        -d "$body"
}

sample_rss() {
    local rss
    rss=$(ps -o rss= -p "$SERVER_PID" 2>/dev/null | tr -d ' ' || echo 0)
    if [[ "$rss" -gt "$PEAK_RSS" ]]; then
        PEAK_RSS="$rss"
    fi
    echo "$rss"
}

percentile() {
    local file="$1"
    local pct="$2"
    local count
    count=$(wc -l < "$file" | tr -d ' ')
    if [[ "$count" -eq 0 ]]; then
        echo "0"
        return
    fi
    local idx=$(( (count * pct + 99) / 100 ))
    if [[ "$idx" -lt 1 ]]; then idx=1; fi
    sort -n "$file" | sed -n "${idx}p"
}

ms_fmt() {
    awk "BEGIN { printf \"%.1fms\", $1 * 1000 }"
}

# ── Build ────────────────────────────────────────────────────────────
echo "Building neo4g..."
cd "$(dirname "$0")/.."
go build -o "$BINARY" ./cmd/neo4g

# ── Start server ─────────────────────────────────────────────────────
echo "Starting server..."
NEO4G_DATA_DIR="$DATA_DIR" NEO4G_LISTEN_ADDR="127.0.0.1:0" NEO4G_WAL_NOSYNC="$WAL_NOSYNC" "$BINARY" > "$SERVER_LOG" 2>&1 &
SERVER_PID=$!

ACTUAL_PORT=""
for _ in $(seq 1 40); do
    if [[ -f "$SERVER_LOG" ]]; then
        ACTUAL_PORT=$(grep -oE 'listening on 127\.0\.0\.1:[0-9]+' "$SERVER_LOG" 2>/dev/null | grep -oE '[0-9]+$' || true)
        if [[ -n "$ACTUAL_PORT" ]]; then break; fi
    fi
    sleep 0.25
done
if [[ -z "${ACTUAL_PORT:-}" ]]; then
    echo "ERROR: could not determine server port"
    cat "$SERVER_LOG" 2>/dev/null || true
    exit 1
fi
echo "Server on port $ACTUAL_PORT (PID $SERVER_PID)"
echo ""

# ── Phase results storage ───────────────────────────────────────────
declare -a PHASE_NAMES=()
declare -a PHASE_REQS=()
declare -a PHASE_ELAPSED=()
declare -a PHASE_RPS=()
declare -a PHASE_P50=()
declare -a PHASE_P95=()
declare -a PHASE_P99=()
PHASE_IDX=0
LAST_ELAPSED=""

run_phase() {
    local name="$1"
    local reqs="$2"
    local timings_file="$3"
    local elapsed="$4"

    local rps
    rps=$(awk "BEGIN { printf \"%.1f\", $reqs / $elapsed }")
    local p50 p95 p99
    p50=$(percentile "$timings_file" 50)
    p95=$(percentile "$timings_file" 95)
    p99=$(percentile "$timings_file" 99)

    local elapsed_fmt
    elapsed_fmt=$(awk "BEGIN { printf \"%.2fs\", $elapsed }")

    PHASE_NAMES[$PHASE_IDX]="$name"
    PHASE_REQS[$PHASE_IDX]="$reqs"
    PHASE_ELAPSED[$PHASE_IDX]="$elapsed_fmt"
    PHASE_RPS[$PHASE_IDX]="$rps"
    PHASE_P50[$PHASE_IDX]="$(ms_fmt "$p50")"
    PHASE_P95[$PHASE_IDX]="$(ms_fmt "$p95")"
    PHASE_P99[$PHASE_IDX]="$(ms_fmt "$p99")"
    LAST_ELAPSED="$elapsed_fmt"
    PHASE_IDX=$((PHASE_IDX + 1))

    sample_rss > /dev/null
}

now_seconds() {
    python3 -c 'import time; print(f"{time.time():.6f}")'
}

# ══════════════════════════════════════════════════════════════════════
# Phase 1: Sequential writes
# ══════════════════════════════════════════════════════════════════════
echo "Phase 1: Write throughput ($NODES sequential CREATEs)..."
> "$TIMINGS"
START=$(now_seconds)
for i in $(seq 1 "$NODES"); do
    query "CREATE (n:Bench {idx: $i, payload: 'node-$i'})" >> "$TIMINGS"
done
END=$(now_seconds)
ELAPSED=$(awk "BEGIN { print $END - $START }")
run_phase "Write (seq)" "$NODES" "$TIMINGS" "$ELAPSED"
echo "  Done: $NODES writes in $LAST_ELAPSED"

# ══════════════════════════════════════════════════════════════════════
# Phase 2: Sequential reads
# ══════════════════════════════════════════════════════════════════════
echo "Phase 2: Read throughput ($NODES sequential MATCHes)..."
> "$TIMINGS"
START=$(now_seconds)
for i in $(seq 1 "$NODES"); do
    query "MATCH (n:Bench) WHERE n.idx = $i RETURN n.idx" >> "$TIMINGS"
done
END=$(now_seconds)
ELAPSED=$(awk "BEGIN { print $END - $START }")
run_phase "Read (seq)" "$NODES" "$TIMINGS" "$ELAPSED"
echo "  Done: $NODES reads in $LAST_ELAPSED"

# ══════════════════════════════════════════════════════════════════════
# Phase 3: Concurrent reads
# ══════════════════════════════════════════════════════════════════════
TOTAL_CONCURRENT_READS=$((WORKERS * READS_PER_WORKER))
echo "Phase 3: Concurrent reads ($WORKERS workers x $READS_PER_WORKER reads = $TOTAL_CONCURRENT_READS)..."
> "$TIMINGS"
START=$(now_seconds)

PIDS=""
for w in $(seq 1 "$WORKERS"); do
    (
        for _ in $(seq 1 "$READS_PER_WORKER"); do
            query "MATCH (n:Bench) RETURN n.idx"
        done
    ) >> "$TMPDIR_BENCH/worker_${w}.txt" &
    PIDS="$PIDS $!"
done
for pid in $PIDS; do wait "$pid"; done

cat "$TMPDIR_BENCH"/worker_*.txt > "$TIMINGS"
rm -f "$TMPDIR_BENCH"/worker_*.txt

END=$(now_seconds)
ELAPSED=$(awk "BEGIN { print $END - $START }")
run_phase "Read (${WORKERS}w)" "$TOTAL_CONCURRENT_READS" "$TIMINGS" "$ELAPSED"
echo "  Done: $TOTAL_CONCURRENT_READS reads in $LAST_ELAPSED"

# ══════════════════════════════════════════════════════════════════════
# Phase 4: Mixed read/write (80/20)
# ══════════════════════════════════════════════════════════════════════
MIXED_PER_WORKER=$READS_PER_WORKER
TOTAL_MIXED=$((WORKERS * MIXED_PER_WORKER))
echo "Phase 4: Mixed workload ($WORKERS workers x $MIXED_PER_WORKER ops, 80% read / 20% write)..."
> "$TIMINGS"
START=$(now_seconds)

WRITE_COUNTER_FILE="$TMPDIR_BENCH/write_counter"
echo "$((NODES + 1))" > "$WRITE_COUNTER_FILE"

PIDS=""
for w in $(seq 1 "$WORKERS"); do
    (
        for j in $(seq 1 "$MIXED_PER_WORKER"); do
            if (( j % 5 == 0 )); then
                # 20% writes — use a unique counter
                wc_val=$(cat "$WRITE_COUNTER_FILE" 2>/dev/null || echo 99999)
                echo "$((wc_val + w * 10000 + j))" > "$WRITE_COUNTER_FILE" 2>/dev/null || true
                query "CREATE (n:Mixed {idx: $((wc_val + w * 10000 + j))})"
            else
                # 80% reads
                query "MATCH (n:Bench) WHERE n.idx = $((j % NODES + 1)) RETURN n.idx"
            fi
        done
    ) >> "$TMPDIR_BENCH/mixed_${w}.txt" &
    PIDS="$PIDS $!"
done
for pid in $PIDS; do wait "$pid"; done

cat "$TMPDIR_BENCH"/mixed_*.txt > "$TIMINGS"
rm -f "$TMPDIR_BENCH"/mixed_*.txt

END=$(now_seconds)
ELAPSED=$(awk "BEGIN { print $END - $START }")
run_phase "Mixed (${WORKERS}w)" "$TOTAL_MIXED" "$TIMINGS" "$ELAPSED"
echo "  Done: $TOTAL_MIXED ops in $LAST_ELAPSED"

# ══════════════════════════════════════════════════════════════════════
# Phase 5: Edge creation + traversal
# ══════════════════════════════════════════════════════════════════════
EDGE_COUNT=$((NODES < 50 ? NODES : 50))
echo "Phase 5: Edge create + traverse ($EDGE_COUNT edges)..."
> "$TIMINGS"
START=$(now_seconds)

for i in $(seq 1 "$EDGE_COUNT"); do
    j=$(( i % NODES + 1 ))
    if [[ "$i" -eq "$j" ]]; then j=$(( (j % NODES) + 1 )); fi
    query "MATCH (a), (b) WHERE a.idx = $i AND b.idx = $j CREATE (a)-[:LINKED {weight: $i}]->(b)" >> "$TIMINGS"
done

for i in $(seq 1 "$EDGE_COUNT"); do
    query "MATCH (a:Bench)-[:LINKED]->(b:Bench) WHERE a.idx = $i RETURN a.idx, b.idx" >> "$TIMINGS"
done

END=$(now_seconds)
ELAPSED=$(awk "BEGIN { print $END - $START }")
TOTAL_EDGE_OPS=$((EDGE_COUNT * 2))
run_phase "Edge ops" "$TOTAL_EDGE_OPS" "$TIMINGS" "$ELAPSED"
echo "  Done: $TOTAL_EDGE_OPS ops in $LAST_ELAPSED"

# ── Resource metrics ─────────────────────────────────────────────────
FINAL_RSS=$(sample_rss)
DB_SIZE=$(du -sh "$DATA_DIR" 2>/dev/null | awk '{print $1}' || echo "N/A")
CPU_TIME=$(ps -o time= -p "$SERVER_PID" 2>/dev/null | tr -d ' ' || echo "N/A")

# ══════════════════════════════════════════════════════════════════════
# Report
# ══════════════════════════════════════════════════════════════════════
echo ""
echo ""
echo "Neo4g Benchmark Report"
echo "══════════════════════════════════════════════════════════════════════"
printf "%-22s %8s %9s %9s %9s %9s %9s\n" "Phase" "Requests" "Elapsed" "Req/s" "p50" "p95" "p99"
echo "──────────────────────────────────────────────────────────────────────"

for i in "${!PHASE_NAMES[@]}"; do
    printf "%-22s %8s %9s %9s %9s %9s %9s\n" \
        "${PHASE_NAMES[$i]}" \
        "${PHASE_REQS[$i]}" \
        "${PHASE_ELAPSED[$i]}" \
        "${PHASE_RPS[$i]}" \
        "${PHASE_P50[$i]}" \
        "${PHASE_P95[$i]}" \
        "${PHASE_P99[$i]}"
done

echo ""
echo "Resource Utilization"
echo "──────────────────────────────────────────────────────────────────────"
PEAK_MB=$(awk "BEGIN { printf \"%.1f\", $PEAK_RSS / 1024 }")
FINAL_MB=$(awk "BEGIN { printf \"%.1f\", $FINAL_RSS / 1024 }")
printf "  Server RSS (peak):    %s MB\n" "$PEAK_MB"
printf "  Server RSS (final):   %s MB\n" "$FINAL_MB"
printf "  DB file size:         %s\n" "$DB_SIZE"
printf "  Server CPU time:      %s\n" "$CPU_TIME"
echo ""
echo "Config: nodes=$NODES workers=$WORKERS reads_per_worker=$READS_PER_WORKER wal_nosync=$WAL_NOSYNC"
echo "══════════════════════════════════════════════════════════════════════"
