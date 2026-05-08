#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Run the Postgres benchmark matrix and write raw logs plus a CSV summary.

Required:
  BENCHMARK_POSTGRES_DSN or TEST_POSTGRES_DSN

Common overrides:
  BENCHMARK_MATRIX_USERS="5000"
  BENCHMARK_MATRIX_WORKERS="128 256 512"
  BENCHMARK_MATRIX_MAX_CONNS="64 96 128"
  BENCHMARK_MATRIX_HOME_SHARDS="64 256"
  BENCHMARK_MATRIX_TIMEOUT="600s"
  BENCHMARK_MATRIX_OUTPUT_DIR="benchmark_suite/results/postgres-matrix-..."

Example:
  BENCHMARK_POSTGRES_DSN=postgres://... ./benchmark_suite/run_postgres_matrix.sh
USAGE
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

dsn="${BENCHMARK_POSTGRES_DSN:-${TEST_POSTGRES_DSN:-}}"
if [[ -z "$dsn" ]]; then
  echo "BENCHMARK_POSTGRES_DSN or TEST_POSTGRES_DSN is required" >&2
  exit 2
fi

users="${BENCHMARK_MATRIX_USERS:-${BENCHMARK_USERS:-100000}}"
workers_list="${BENCHMARK_MATRIX_WORKERS:-128 256 512}"
max_conns_list="${BENCHMARK_MATRIX_MAX_CONNS:-64 96 128}"
home_shards_list="${BENCHMARK_MATRIX_HOME_SHARDS:-64 256}"
timeout="${BENCHMARK_MATRIX_TIMEOUT:-600s}"
output_dir="${BENCHMARK_MATRIX_OUTPUT_DIR:-benchmark_suite/results/postgres-matrix-$(date -u +%Y%m%dT%H%M%SZ)}"
csv_path="$output_dir/results.csv"

mkdir -p "$output_dir"

csv_escape() {
  local value="${1:-}"
  value="${value//\"/\"\"}"
  printf '"%s"' "$value"
}

write_csv_row() {
  local first=1
  for value in "$@"; do
    if [[ "$first" -eq 0 ]]; then
      printf ',' >>"$csv_path"
    fi
    csv_escape "$value" >>"$csv_path"
    first=0
  done
  printf '\n' >>"$csv_path"
}

extract_sed() {
  local pattern="$1"
  local log_path="$2"
  sed -nE "$pattern" "$log_path" | tail -n 1
}

extract_section_percentile() {
  local section="$1"
  local percentile="$2"
  local log_path="$3"
  awk -v section="$section" -v percentile="$percentile" '
    index($0, section) {
      in_section = 1
      next
    }
    in_section && index($0, "latency") && index($0, section) == 0 {
      in_section = 0
    }
    in_section && index($0, percentile ":") {
      print $NF
      exit
    }
  ' "$log_path"
}

extract_pool_key() {
  local label="$1"
  local key="$2"
  local log_path="$3"
  awk -v label="$label" -v key="$key" '
    index($0, label) {
      for (i = 1; i <= NF; i++) {
        if ($i ~ ("^" key "=")) {
          split($i, parts, "=")
          print parts[2]
          exit
        }
      }
    }
  ' "$log_path"
}

write_csv_row \
  "started_at" \
  "users" \
  "workers" \
  "postgres_max_conns" \
  "home_shards" \
  "status" \
  "exit_code" \
  "elapsed_sec" \
  "throughput_users_sec" \
  "successful_merges" \
  "conflicts" \
  "errors" \
  "total_p50_ms" \
  "total_p95_ms" \
  "total_p99_ms" \
  "create_slice_p50_ms" \
  "create_slice_p95_ms" \
  "create_slice_p99_ms" \
  "create_changeset_p50_ms" \
  "create_changeset_p95_ms" \
  "create_changeset_p99_ms" \
  "merge_p50_ms" \
  "merge_p95_ms" \
  "merge_p99_ms" \
  "promotion_drain_sec" \
  "fg_pool_max_acquired" \
  "fg_pool_acquire_count" \
  "fg_pool_empty_acquire_count" \
  "fg_pool_empty_acquire_wait" \
  "fg_pool_acquire_duration" \
  "fg_pool_canceled_acquire_count" \
  "promotion_pool_max_acquired" \
  "promotion_pool_acquire_count" \
  "promotion_pool_empty_acquire_count" \
  "promotion_pool_empty_acquire_wait" \
  "log_path"

echo "Writing benchmark matrix output to $output_dir"

for home_shards in $home_shards_list; do
  for workers in $workers_list; do
    for max_conns in $max_conns_list; do
      started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
      log_path="$output_dir/users-${users}_workers-${workers}_conns-${max_conns}_shards-${home_shards}.log"

      echo "Running users=$users workers=$workers max_conns=$max_conns home_shards=$home_shards"

      set +e
      BENCHMARK_STORAGE=postgres \
        BENCHMARK_POSTGRES_DSN="$dsn" \
        BENCHMARK_USERS="$users" \
        BENCHMARK_WORKERS="$workers" \
        BENCHMARK_POSTGRES_MAX_CONNS="$max_conns" \
        BENCHMARK_POSTGRES_PROMOTION_MAX_CONNS="${BENCHMARK_POSTGRES_PROMOTION_MAX_CONNS:-}" \
        BENCHMARK_HOME_SHARDS="$home_shards" \
        go test -v -timeout "$timeout" ./benchmark_suite -run TestSimulate100kUsers -count=1 2>&1 | tee "$log_path"
      exit_code=${PIPESTATUS[0]}
      set -e

      status="pass"
      if [[ "$exit_code" -ne 0 ]]; then
        status="fail"
      fi

      elapsed_sec="$(extract_sed 's/.*Elapsed:[[:space:]]*([0-9.]+) s.*/\1/p' "$log_path")"
      throughput="$(extract_sed 's/.*Throughput:[[:space:]]*([0-9.]+) users\/sec.*/\1/p' "$log_path")"
      successful_merges="$(extract_sed 's/.*Successful merges:[[:space:]]*([0-9]+).*/\1/p' "$log_path")"
      conflicts="$(extract_sed 's/.*Conflicts:[[:space:]]*([0-9]+).*/\1/p' "$log_path")"
      errors="$(extract_sed 's/.*Errors:[[:space:]]*([0-9]+).*/\1/p' "$log_path")"

      total_p50="$(extract_section_percentile "End-to-end latency per user" "P50" "$log_path")"
      total_p95="$(extract_section_percentile "End-to-end latency per user" "P95" "$log_path")"
      total_p99="$(extract_section_percentile "End-to-end latency per user" "P99" "$log_path")"
      create_slice_p50="$(extract_section_percentile "CreateSliceFromFolder latency" "P50" "$log_path")"
      create_slice_p95="$(extract_section_percentile "CreateSliceFromFolder latency" "P95" "$log_path")"
      create_slice_p99="$(extract_section_percentile "CreateSliceFromFolder latency" "P99" "$log_path")"
      create_changeset_p50="$(extract_section_percentile "CreateChangeset latency" "P50" "$log_path")"
      create_changeset_p95="$(extract_section_percentile "CreateChangeset latency" "P95" "$log_path")"
      create_changeset_p99="$(extract_section_percentile "CreateChangeset latency" "P99" "$log_path")"
      merge_p50="$(extract_section_percentile "MergeChangeset latency" "P50" "$log_path")"
      merge_p95="$(extract_section_percentile "MergeChangeset latency" "P95" "$log_path")"
      merge_p99="$(extract_section_percentile "MergeChangeset latency" "P99" "$log_path")"

      promotion_drain_sec="$(extract_sed 's/.*Promotion drain elapsed:[[:space:]]*([0-9.]+) s.*/\1/p' "$log_path")"
      fg_pool_max_acquired="$(extract_pool_key "Foreground workload Postgres pool observed max" "acquired" "$log_path")"
      fg_pool_acquire_count="$(extract_pool_key "Foreground workload Postgres pool cumulative delta" "acquire_count" "$log_path")"
      fg_pool_empty_acquire_count="$(extract_pool_key "Foreground workload Postgres pool cumulative delta" "empty_acquire_count" "$log_path")"
      fg_pool_empty_acquire_wait="$(extract_pool_key "Foreground workload Postgres pool cumulative delta" "empty_acquire_wait" "$log_path")"
      fg_pool_acquire_duration="$(extract_pool_key "Foreground workload Postgres pool cumulative delta" "acquire_duration" "$log_path")"
      fg_pool_canceled_acquire_count="$(extract_pool_key "Foreground workload Postgres pool cumulative delta" "canceled_acquire_count" "$log_path")"
      promotion_pool_max_acquired="$(extract_pool_key "Promotion drain Postgres pool observed max" "acquired" "$log_path")"
      promotion_pool_acquire_count="$(extract_pool_key "Promotion drain Postgres pool cumulative delta" "acquire_count" "$log_path")"
      promotion_pool_empty_acquire_count="$(extract_pool_key "Promotion drain Postgres pool cumulative delta" "empty_acquire_count" "$log_path")"
      promotion_pool_empty_acquire_wait="$(extract_pool_key "Promotion drain Postgres pool cumulative delta" "empty_acquire_wait" "$log_path")"

      write_csv_row \
        "$started_at" \
        "$users" \
        "$workers" \
        "$max_conns" \
        "$home_shards" \
        "$status" \
        "$exit_code" \
        "$elapsed_sec" \
        "$throughput" \
        "$successful_merges" \
        "$conflicts" \
        "$errors" \
        "$total_p50" \
        "$total_p95" \
        "$total_p99" \
        "$create_slice_p50" \
        "$create_slice_p95" \
        "$create_slice_p99" \
        "$create_changeset_p50" \
        "$create_changeset_p95" \
        "$create_changeset_p99" \
        "$merge_p50" \
        "$merge_p95" \
        "$merge_p99" \
        "$promotion_drain_sec" \
        "$fg_pool_max_acquired" \
        "$fg_pool_acquire_count" \
        "$fg_pool_empty_acquire_count" \
        "$fg_pool_empty_acquire_wait" \
        "$fg_pool_acquire_duration" \
        "$fg_pool_canceled_acquire_count" \
        "$promotion_pool_max_acquired" \
        "$promotion_pool_acquire_count" \
        "$promotion_pool_empty_acquire_count" \
        "$promotion_pool_empty_acquire_wait" \
        "$log_path"
    done
  done
done

echo "Benchmark matrix complete: $csv_path"
