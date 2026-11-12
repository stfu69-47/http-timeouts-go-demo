#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'USAGE'
Usage: ./run.sh [output-parent-directory]

Runs baseline, server-egress delay 20 ms, and delay 20 ms + random loss 1%.
Creates a new run-* subdirectory; never overwrites an earlier run.
Requires Linux Docker Engine, Docker Compose v2, Bash and GNU timeout.

Optional environment:
  LAB_N=1000                   Requests in each case.
  LAB_C=8                      Concurrent workers / max connections.
  LAB_CASE_TIMEOUT_SECONDS=900  Wall-clock limit for one case.
  LAB_GO_VERSION=1.27.4         Go build-image tag component.
USAGE
}

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

if [[ ${1:-} == -h || ${1:-} == --help ]]; then
  usage
  exit 0
fi
(( $# <= 1 )) || { usage >&2; exit 2; }

for dependency in docker timeout awk grep mktemp id date; do
  command -v "$dependency" >/dev/null 2>&1 || die "Missing command: $dependency"
done
docker compose version >/dev/null
timeout --kill-after=5s 15s docker info >/dev/null

LAB_N=${LAB_N:-1000}
LAB_C=${LAB_C:-8}
LAB_CASE_TIMEOUT_SECONDS=${LAB_CASE_TIMEOUT_SECONDS:-900}
for value in "$LAB_N" "$LAB_C" "$LAB_CASE_TIMEOUT_SECONDS"; do
  [[ $value =~ ^[1-9][0-9]*$ ]] || die "Counts and case timeout must be positive integers"
  (( value <= 10000000 )) || die "Numeric parameter is too large"
done

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
[[ -f $script_dir/../go.mod ]] || die "Place network/ inside the timeoutlab Go module first"
output_parent=${1:-"$script_dir/output"}
mkdir -p -- "$output_parent"
output_parent=$(cd -- "$output_parent" && pwd)
LAB_OUTPUT_DIR=$(mktemp -d "$output_parent/run-$(date -u +%Y%m%dT%H%M%SZ)-XXXXXX")
LAB_PROJECT="timeoutlab-$(date -u +%Y%m%d%H%M%S)-$$-$RANDOM"
LAB_GO_VERSION=${LAB_GO_VERSION:-1.27.4}
export LAB_OUTPUT_DIR LAB_PROJECT LAB_GO_VERSION
host_identity="$(id -u):$(id -g)"
compose=(docker compose --project-name "$LAB_PROJECT" --project-directory "$script_dir" -f "$script_dir/compose.yaml")
capture_pids=()
capture_services=()
cleanup_needed=0
measurements_complete=false

dc() { "${compose[@]}" "$@"; }

stop_captures() {
  local service pid attempt status failed=0
  # pkill runs only inside this run's isolated, uniquely named containers.
  for service in "${capture_services[@]}"; do
    timeout --kill-after=2s 10s "${compose[@]}" exec -T "$service" pkill -INT -x tcpdump \
      >>"$LAB_OUTPUT_DIR/capture-stop.log" 2>&1 || true
  done
  for pid in "${capture_pids[@]}"; do
    for ((attempt = 0; attempt < 100; attempt++)); do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.1
    done
    if kill -0 "$pid" 2>/dev/null; then
      # The container watchdog and subsequent Compose teardown also bound its child.
      kill -TERM "$pid" 2>/dev/null || true
      for ((attempt = 0; attempt < 50; attempt++)); do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.1
      done
      if kill -0 "$pid" 2>/dev/null; then
        # GNU timeout creates its own process group unless --foreground is used.
        kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
      fi
      failed=1
    fi
    if wait "$pid"; then
      :
    else
      status=$?
      printf 'Capture attachment exited with status %s\n' "$status" >>"$LAB_OUTPUT_DIR/capture-stop.log"
      failed=1
    fi
  done
  capture_pids=()
  capture_services=()
  return "$failed"
}

cleanup() {
  local status=$? teardown_status=0
  trap - EXIT INT TERM
  set +e
  stop_captures
  if (( cleanup_needed )); then
    timeout --kill-after=2s 10s "${compose[@]}" logs --no-color \
      >"$LAB_OUTPUT_DIR/containers.log" 2>&1
    timeout --kill-after=5s 30s "${compose[@]}" down --timeout 5 --remove-orphans \
      >"$LAB_OUTPUT_DIR/teardown.log" 2>&1
    teardown_status=$?
    if (( teardown_status != 0 )); then
      printf 'ERROR: Compose teardown failed; see %s/teardown.log and metadata.txt\n' "$LAB_OUTPUT_DIR" >&2
      (( status != 0 )) || status=1
    fi
  fi
  {
    printf 'measurements_complete=%s\n' "$measurements_complete"
    printf 'exit_status=%s\ncleanup_exit_status=%s\n' "$status" "$teardown_status"
    printf 'finished_utc=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  } >"$LAB_OUTPUT_DIR/status.txt"
  printf 'Artifacts: %s\n' "$LAB_OUTPUT_DIR"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

{
  printf 'started_utc=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'compose_project=%s\noutput_dir=%s\n' "$LAB_PROJECT" "$LAB_OUTPUT_DIR"
  printf 'go_image_version=%s\nrequests_per_case=%s\nconcurrency=%s\n' "$LAB_GO_VERSION" "$LAB_N" "$LAB_C"
  printf 'client_timeout=5s\nheader_timeout=0\ndial_timeout=5s\ntls_timeout=5s\n'
  printf 'case_wall_timeout_seconds=%s\nload_model=closed_loop\n' "$LAB_CASE_TIMEOUT_SECONDS"
  printf 'server_header_delay=5ms\nprotocol=HTTP/1.1\n'
} >"$LAB_OUTPUT_DIR/metadata.txt"
dc config >"$LAB_OUTPUT_DIR/compose.resolved.yaml"
docker version >"$LAB_OUTPUT_DIR/docker-version.txt"
docker compose version >"$LAB_OUTPUT_DIR/compose-version.txt"

cleanup_needed=1
dc build server 2>&1 | tee "$LAB_OUTPUT_DIR/build.log"
dc up -d --no-build --wait --wait-timeout 60 2>&1 | tee "$LAB_OUTPUT_DIR/startup.log"

dc exec -T client sh -c 'cat /usr/local/share/timeoutlab-build-go-version.txt; uname -a; tcpdump --version; tshark --version; tc -V' \
  >"$LAB_OUTPUT_DIR/runtime-versions.txt" 2>&1
for service in server client; do
  dc exec -T "$service" ip -details link show dev eth0 >"$LAB_OUTPUT_DIR/$service.interface.txt"
done

start_captures() {
  local case_name=$1 service attempt ready capture_seconds pid
  capture_seconds=$((LAB_CASE_TIMEOUT_SECONDS + 30))
  for service in server client; do
    capture_services+=("$service")
    # stdout is the raw pcap stream, stderr retains capture/drop counters.
    # -p avoids promiscuous-mode changes on the client (no NET_ADMIN there).
    timeout --signal=TERM --kill-after=5s "$((capture_seconds + 10))s" \
      "${compose[@]}" exec -T "$service" \
      timeout --signal=INT --kill-after=5s "${capture_seconds}s" \
      tcpdump -p -i eth0 -nn -s 0 -B 4096 -U -Z root -w - 'tcp port 8080' \
      >"$LAB_OUTPUT_DIR/$case_name.$service.pcap" \
      2>"$LAB_OUTPUT_DIR/$case_name.$service.capture.stderr" &
    capture_pids+=("$!")
  done
  for ((attempt = 0; attempt < 100; attempt++)); do
    ready=1
    for service in server client; do
      if ! grep -q 'listening on ' "$LAB_OUTPUT_DIR/$case_name.$service.capture.stderr"; then
        ready=0
      fi
    done
    (( ready )) && return 0
    for pid in "${capture_pids[@]}"; do
      kill -0 "$pid" 2>/dev/null || die "Capture failed to start; inspect $case_name.*.capture.stderr"
    done
    sleep 0.1
  done
  die "Capture readiness timed out; inspect $case_name.*.capture.stderr"
}

snapshot() {
  local case_name=$1 point=$2 service
  for service in server client; do
    dc exec -T "$service" nstat -asz >"$LAB_OUTPUT_DIR/$case_name.$service.nstat.$point.txt"
    dc exec -T "$service" tc -s -d qdisc show dev eth0 >"$LAB_OUTPUT_DIR/$case_name.$service.tc.$point.txt"
  done
}

derive_tables() {
  local case_name=$1 service
  for service in server client; do
    awk '
      BEGIN { print "counter,before,after,delta" }
      NR == FNR { if ($1 !~ /^#/ && NF >= 2) before[$1] = $2; next }
      $1 !~ /^#/ && NF >= 2 && ($1 in before) {
        printf "%s,%.0f,%.0f,%.0f\n", $1, before[$1], $2, $2 - before[$1]
      }
    ' "$LAB_OUTPUT_DIR/$case_name.$service.nstat.before.txt" \
      "$LAB_OUTPUT_DIR/$case_name.$service.nstat.after.txt" \
      >"$LAB_OUTPUT_DIR/$case_name.$service.nstat.delta.csv"
    dc exec -T --user "$host_identity" client \
      tshark -n -r "/data/$case_name.$service.pcap" \
      -Y 'tcp.analysis.retransmission || tcp.analysis.fast_retransmission || tcp.analysis.spurious_retransmission' \
      -T fields -E header=y -E separator=, -E quote=d -E occurrence=f \
      -e frame.number -e frame.time_epoch -e ip.src -e tcp.srcport \
      -e ip.dst -e tcp.dstport -e tcp.stream -e tcp.seq_raw -e tcp.ack_raw -e tcp.len \
      -e tcp.analysis.retransmission -e tcp.analysis.fast_retransmission -e tcp.analysis.spurious_retransmission \
      >"$LAB_OUTPUT_DIR/$case_name.$service.retransmissions.csv" \
      2>"$LAB_OUTPUT_DIR/$case_name.$service.tshark.stderr"
  done
}

run_case() {
  local case_name=$1 service
  shift
  printf 'Case: %s\n' "$case_name"
  if (( $# )); then
    dc exec -T server tc qdisc replace dev eth0 root netem limit 10000 "$@"
  fi
  snapshot "$case_name" before
  start_captures "$case_name"
  timeout --signal=TERM --kill-after=5s "$((LAB_CASE_TIMEOUT_SECONDS + 10))s" \
    "${compose[@]}" exec -T --user "$host_identity" client \
    timeout --signal=TERM --kill-after=5s "${LAB_CASE_TIMEOUT_SECONDS}s" \
    timeoutlab probe -url http://server:8080/ -n "$LAB_N" -c "$LAB_C" \
    -max-conns "$LAB_C" -timeout 5s -header-timeout 0 -dial-timeout 5s -tls-timeout 5s \
    -out "/data/$case_name.jsonl" \
    >"$LAB_OUTPUT_DIR/$case_name.probe.stdout" 2>"$LAB_OUTPUT_DIR/$case_name.probe.stderr"
  snapshot "$case_name" after
  stop_captures || die "Capture did not close cleanly; see capture-stop.log"
  for service in server client; do
    [[ $(wc -c <"$LAB_OUTPUT_DIR/$case_name.$service.pcap") -gt 24 ]] \
      || die "No captured packets in $case_name.$service.pcap"
  done
  derive_tables "$case_name"
}

run_case baseline
run_case delay20ms delay 20ms
run_case delay20ms-loss1pct delay 20ms loss random 1%
measurements_complete=true
