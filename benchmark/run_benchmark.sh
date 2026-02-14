#!/bin/bash
# Benchmark script for gitslice import of top GitHub repos
# Usage: ./benchmark/run_benchmark.sh [num_repos] [max_commits] [parallelism] [per_repo_timeout_sec]

set -uo pipefail

NUM_REPOS=${1:-5}
MAX_COMMITS=${2:-1}
PARALLELISM=${3:-1}
PER_REPO_TIMEOUT=${4:-300}  # 5 min default per repo
GS_CLI="${GS_CLI:-/tmp/gs_cli}"
SERVER_ADDR="${SERVER_ADDR:-localhost:50051}"
USER="benchmark"
RESULTS_DIR="/tmp/benchmark_results"
REPO_LIST="/tmp/top_repos.txt"

mkdir -p "$RESULTS_DIR"

RUN_ID=$(date +%Y%m%d_%H%M%S)
RUN_DIR="$RESULTS_DIR/$RUN_ID"
mkdir -p "$RUN_DIR"

echo "============================================"
echo "Gitslice Import Benchmark"
echo "============================================"
echo "Repos:            $NUM_REPOS"
echo "Max commits:      $MAX_COMMITS"
echo "Parallelism:      $PARALLELISM"
echo "Per-repo timeout: ${PER_REPO_TIMEOUT}s"
echo "Run ID:           $RUN_ID"
echo "============================================"

# Read repos from list (format: full_name\tstars\tsize_kb)
declare -a REPO_NAMES
declare -a REPO_SIZES
while IFS=$'\t' read -r name stars size; do
    REPO_NAMES+=("$name")
    REPO_SIZES+=("$size")
    if [ ${#REPO_NAMES[@]} -ge "$NUM_REPOS" ]; then
        break
    fi
done < "$REPO_LIST"

echo "Selected ${#REPO_NAMES[@]} repos for benchmark"
echo ""

SUMMARY_FILE="$RUN_DIR/summary.csv"
echo "repo,size_kb,status,time_sec,commits_imported,warnings" > "$SUMMARY_FILE"

# Lock file for atomic CSV writes
LOCK_FILE="$RUN_DIR/.lock"
touch "$LOCK_FILE"

import_repo() {
    local idx=$1
    local repo=$2
    local size_kb=$3
    local repo_name
    repo_name=$(echo "$repo" | tr '/' '_')
    local mount_path="/o/genesis/projects/${repo_name}"
    local log_file="$RUN_DIR/${repo_name}.log"
    local url="https://github.com/${repo}.git"
    local size_mb
    size_mb=$(echo "scale=1; $size_kb / 1024" | bc)

    echo "[$(date +%H:%M:%S)] START  #$((idx+1))/$NUM_REPOS $repo (${size_mb}MB)"

    local start_time
    start_time=$(date +%s%N)

    local output
    if output=$(timeout "${PER_REPO_TIMEOUT}s" "$GS_CLI" --addr "$SERVER_ADDR" --user "$USER" import git \
        --repo "$url" \
        --ref HEAD \
        --slice root_slice \
        --mount "$mount_path" \
        --max-commits "$MAX_COMMITS" \
        --first-parent \
        --timeout "${PER_REPO_TIMEOUT}s" 2>&1); then

        local end_time
        end_time=$(date +%s%N)
        local elapsed_ms=$(( (end_time - start_time) / 1000000 ))
        local elapsed_sec
        elapsed_sec=$(echo "scale=2; $elapsed_ms / 1000" | bc)

        local commits
        commits=$(echo "$output" | grep -oP 'Imported \K[0-9]+' || echo "?")
        local warnings
        warnings=$(echo "$output" | grep -oP 'Warnings: \K[0-9]+' || echo "0")

        echo "[$(date +%H:%M:%S)] DONE   #$((idx+1)) $repo: ${elapsed_sec}s, $commits commits"
        echo "$output" > "$log_file"
        (
            flock -w 5 200
            echo "$repo,$size_kb,success,$elapsed_sec,$commits,$warnings" >> "$SUMMARY_FILE"
        ) 200>"$LOCK_FILE"
    else
        local exit_code=$?
        local end_time
        end_time=$(date +%s%N)
        local elapsed_ms=$(( (end_time - start_time) / 1000000 ))
        local elapsed_sec
        elapsed_sec=$(echo "scale=2; $elapsed_ms / 1000" | bc)

        local fail_status="failed"
        if [ $exit_code -eq 124 ]; then
            fail_status="timeout"
        fi

        echo "[$(date +%H:%M:%S)] FAIL   #$((idx+1)) $repo: ${elapsed_sec}s ($fail_status)"
        echo "$output" > "$log_file"
        (
            flock -w 5 200
            echo "$repo,$size_kb,$fail_status,$elapsed_sec,0,0" >> "$SUMMARY_FILE"
        ) 200>"$LOCK_FILE"
    fi
}

# Main execution
OVERALL_START=$(date +%s%N)

if [ "$PARALLELISM" -le 1 ]; then
    echo "--- Running sequentially ---"
    for idx in $(seq 0 $(( ${#REPO_NAMES[@]} - 1 ))); do
        import_repo "$idx" "${REPO_NAMES[$idx]}" "${REPO_SIZES[$idx]}"
    done
else
    echo "--- Running with parallelism=$PARALLELISM ---"
    active_pids=()
    for idx in $(seq 0 $(( ${#REPO_NAMES[@]} - 1 ))); do
        import_repo "$idx" "${REPO_NAMES[$idx]}" "${REPO_SIZES[$idx]}" &
        active_pids+=($!)

        if [ ${#active_pids[@]} -ge "$PARALLELISM" ]; then
            wait "${active_pids[0]}" 2>/dev/null || true
            active_pids=("${active_pids[@]:1}")
        fi
    done
    for pid in "${active_pids[@]}"; do
        wait "$pid" 2>/dev/null || true
    done
fi

OVERALL_END=$(date +%s%N)
OVERALL_MS=$(( (OVERALL_END - OVERALL_START) / 1000000 ))
OVERALL_SEC=$(echo "scale=2; $OVERALL_MS / 1000" | bc)

echo ""
echo "============================================"
echo "Benchmark Complete"
echo "============================================"
echo "Total wall time: ${OVERALL_SEC}s"
echo "Results: $RUN_DIR/summary.csv"
echo ""

# Print summary table sorted by time
echo "--- Results (sorted by time) ---"
echo ""
printf "%-45s %10s %10s %8s %8s\n" "REPO" "SIZE(MB)" "STATUS" "TIME(s)" "COMMITS"
printf "%-45s %10s %10s %8s %8s\n" "----" "--------" "------" "-------" "-------"
tail -n +2 "$SUMMARY_FILE" | sort -t',' -k4 -n -r | while IFS=',' read -r repo size_kb status time commits warnings; do
    size_mb=$(echo "scale=1; $size_kb / 1024" | bc)
    printf "%-45s %10s %10s %8s %8s\n" "$repo" "$size_mb" "$status" "$time" "$commits"
done

echo ""

# Statistics
total_success=$(tail -n +2 "$SUMMARY_FILE" | grep -c "success" || echo "0")
total_failed=$(tail -n +2 "$SUMMARY_FILE" | grep -c "failed" || echo "0")
total_timeout=$(tail -n +2 "$SUMMARY_FILE" | grep -c "timeout" || echo "0")
total_time=$(tail -n +2 "$SUMMARY_FILE" | grep "success" | awk -F',' '{sum += $4} END {printf "%.2f", sum}')

if [ "$total_success" -gt 0 ]; then
    avg_time=$(echo "scale=2; $total_time / $total_success" | bc 2>/dev/null || echo "N/A")
    min_time=$(tail -n +2 "$SUMMARY_FILE" | grep "success" | awk -F',' 'NR==1 || $4<min {min=$4} END{printf "%.2f", min}')
    max_time=$(tail -n +2 "$SUMMARY_FILE" | grep "success" | awk -F',' 'NR==1 || $4>max {max=$4} END{printf "%.2f", max}')
    median_time=$(tail -n +2 "$SUMMARY_FILE" | grep "success" | awk -F',' '{print $4}' | sort -n | awk 'NR==1{a[NR]=$1;next}{a[NR]=$1}END{if(NR%2==1)printf "%.2f",a[(NR+1)/2]; else printf "%.2f",(a[NR/2]+a[NR/2+1])/2}')
else
    avg_time="N/A"
    min_time="N/A"
    max_time="N/A"
    median_time="N/A"
fi

echo "--- Statistics ---"
echo "Successful:       $total_success / $NUM_REPOS"
echo "Failed:           $total_failed / $NUM_REPOS"
echo "Timed out:        $total_timeout / $NUM_REPOS"
echo ""
echo "Sum of times:     ${total_time}s"
echo "Wall clock time:  ${OVERALL_SEC}s"
echo "Min import time:  ${min_time}s"
echo "Max import time:  ${max_time}s"
echo "Avg import time:  ${avg_time}s"
echo "Median import:    ${median_time}s"
if [ "$PARALLELISM" -gt 1 ] && [ "$total_success" -gt 0 ]; then
    speedup=$(echo "scale=2; $total_time / $OVERALL_SEC" | bc 2>/dev/null || echo "N/A")
    echo "Parallelism:      ${PARALLELISM}x"
    echo "Effective speedup: ${speedup}x"
fi
echo ""
echo "Throughput:       $(echo "scale=2; $total_success / ($OVERALL_SEC + 0.001)" | bc 2>/dev/null || echo "N/A") repos/sec"

# Save final report
cp "$SUMMARY_FILE" "$RUN_DIR/summary_final.csv"
echo "$OVERALL_SEC" > "$RUN_DIR/wall_time.txt"
