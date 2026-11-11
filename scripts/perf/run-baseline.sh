#!/usr/bin/env bash
# run-baseline.sh - run the stage-1 performance matrix.
#
# One cell is one scenario: a real server process and a real client process,
# driven by the load generator in perflab. The matrix is a list of scenarios,
# not a product of every knob, because a cell that no protocol can honour (UDP
# over a TCP-only protocol, say) is a false result rather than a thorough one.
#
# Usage:
#   scripts/perf/run-baseline.sh --bin <kernel-binary> [options]
#
# Options:
#   --bin PATH             kernel binary built with the lab tag set (required)
#   --xray PATH            reference Xray binary; without it the XHTTP cells are
#                          recorded as skipped, not run against no server
#   --out DIR              output root (default ./perf-results)
#   --profile NAME         loopback (default) or a netlab profile: normal|remote|weak
#   --reps N               repetitions per (pattern, streams) cell (default 3)
#   --duration DURATION    measured window (default 5s)
#   --warmup DURATION      warmup window (default 1s)
#   --streams LIST         concurrency levels (default 1,4)
#   --only LIST            comma-separated scenario ids to run
#   --smoke                1 rep, 1 stream, 2s: a wiring check, never a result
#
# Output:
#   <out>/<profile>/<scenario>-<timestamp>/run.json, summary.md, logs, configs
#   <out>/matrix-<profile>-<timestamp>.json   cell index with status per cell
#   <out>/matrix-<profile>-<timestamp>.log    the runner's own log
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
perflab="${PERFLAB:-$HOME/perf/bin/perflab}"
netlab="$here/netlab/netlab.sh"

bin=""
xray=""
out="./perf-results"
profile="loopback"
reps=3
duration="5s"
warmup="1s"
streams="1,4"
only=""
smoke=0

while [ $# -gt 0 ]; do
  case "$1" in
    --bin) bin="$2"; shift 2 ;;
    --xray) xray="$2"; shift 2 ;;
    --out) out="$2"; shift 2 ;;
    --profile) profile="$2"; shift 2 ;;
    --reps) reps="$2"; shift 2 ;;
    --duration) duration="$2"; shift 2 ;;
    --warmup) warmup="$2"; shift 2 ;;
    --streams) streams="$2"; shift 2 ;;
    --only) only="$2"; shift 2 ;;
    --smoke) smoke=1; shift ;;
    -h|--help) sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
    *) echo "run-baseline: unknown argument $1" >&2; exit 2 ;;
  esac
done

if [ -z "$bin" ]; then
  echo "run-baseline: --bin is required" >&2
  exit 2
fi
if [ ! -x "$perflab" ]; then
  echo "run-baseline: perflab not found at $perflab (set PERFLAB)" >&2
  exit 2
fi

if [ "$smoke" = "1" ]; then
  reps=1
  streams=1
  duration="2s"
  warmup="300ms"
fi

cd "$repo" || exit 1
stamp="$(date +%Y%m%dT%H%M%S)"
root="$out/$profile"
mkdir -p "$root"
matrix="$out/matrix-$profile-$stamp.json"
log="$out/matrix-$profile-$stamp.log"
: > "$log"

# Resolve the source record once for the whole matrix. Each git read of this
# repository costs seconds on a WSL2 drvfs mount, and every cell would otherwise
# repeat the same four commands.
if [ -z "${PERF_SOURCE_JSON:-}" ]; then
  PERF_SOURCE_JSON="$("$perflab" env -binary "$bin" -json 2>/dev/null \
    | python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)["source"]))' 2>/dev/null \
    || true)"
  if [ -n "${PERF_SOURCE_JSON:-}" ]; then
    export PERF_SOURCE_JSON
    echo "source: $(printf '%s' "$PERF_SOURCE_JSON" | head -c 160)" | tee -a "$log"
  fi
fi

# Every scenario is a pair of config templates plus a declared load model. A
# scenario that cannot serve the load it declares fails loudly instead of
# silently producing a smaller number.
scenarios=(
  vless-vision-tcp
  vless-reality-vision
  anytls-tcp
  xhttp-h2
  xhttp-h3
  trusttunnel-h2
  trusttunnel-h3
  mieru-tcp
  mieru-udp
  masque-connect-ip
  wireguard-endpoint
  awg-endpoint
  naive-h2
  naive-h3
)

selected() {
  [ -z "$only" ] && return 0
  case ",$only," in
    *",$1,"*) return 0 ;;
    *) return 1 ;;
  esac
}

echo "[" > "$matrix"
first=1
for scenario in "${scenarios[@]}"; do
  selected "$scenario" || continue
  dir="scripts/perf/scenarios/$scenario"
  if [ ! -d "$dir" ]; then
    printf '%s\n' "run-baseline: $scenario: scenario directory missing" | tee -a "$log" >&2
    continue
  fi
  echo "=== $scenario ($profile) $(date -Is)" | tee -a "$log"
  # The XHTTP scenarios use the fork's client against a reference Xray server.
  # Without the reference binary the cell cannot be honest about what it
  # measured, so it is recorded as skipped rather than run against nothing.
  if [ "${scenario#xhttp}" != "$scenario" ] && [ -z "$xray" ]; then
    echo "=== $scenario: skipped (no --xray reference server)" | tee -a "$log"
    if [ "$first" = "1" ]; then first=0; else echo "," >> "$matrix"; fi
    printf '  {"scenario": "%s", "profile": "%s", "status": 3, "seconds": 0, "run_dir": "", "note": "no --xray reference server"}' \
      "$scenario" "$profile" >> "$matrix"
    continue
  fi
  started=$(date +%s)
  extra_args=()
  [ -n "$xray" ] && extra_args+=(-bin-xray "$xray")
  # Shaped profiles run the whole cell inside netlab; unshaped ones do not pay
  # for a namespace they would not use.
  if [ "$profile" = "loopback" ]; then
    "$perflab" run -scenario "$dir" -out "$root" -bin-lab "$bin" "${extra_args[@]}" \
      -reps "$reps" -duration "$duration" -warmup "$warmup" -streams "$streams" \
      >>"$log" 2>&1
    status=$?
  else
    "$netlab" --profile "$profile" -- "$perflab" run -scenario "$dir" -out "$root" -bin-lab "$bin" "${extra_args[@]}" \
      -reps "$reps" -duration "$duration" -warmup "$warmup" -streams "$streams" \
      >>"$log" 2>&1
    status=$?
  fi
  elapsed=$(( $(date +%s) - started ))
  latest="$(ls -1dt "$root"/"$scenario"-* 2>/dev/null | head -1)"
  rel="${latest#$repo/}"
  if [ "$first" = "1" ]; then
    first=0
  else
    echo "," >> "$matrix"
  fi
  printf '  {"scenario": "%s", "profile": "%s", "status": %d, "seconds": %d, "run_dir": "%s"}' \
    "$scenario" "$profile" "$status" "$elapsed" "$rel" >> "$matrix"
  echo "=== $scenario: status=$status elapsed=${elapsed}s dir=${rel:-none}" | tee -a "$log"
done
echo "" >> "$matrix"
echo "]" >> "$matrix"

# Every run this matrix produced must satisfy the run schema before the matrix is
# handed to a report. A missing validator is reported as unavailable, not as a
# pass: the data is not claimed valid without a validation result.
if command -v python3 >/dev/null 2>&1 && python3 -c 'import jsonschema' >/dev/null 2>&1; then
  if ! python3 "$here/validate_runs.py" "$root"; then
    echo "schema validation failed for run(s) under $root" >&2
    exit 1
  fi
else
  echo "warning: python3-jsonschema unavailable; run.json schema validation NOT performed" >&2
fi

echo "matrix: $matrix"
echo "log:    $log"
