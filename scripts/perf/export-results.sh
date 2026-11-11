#!/usr/bin/env bash
# export-results.sh - copy raw run records into the repository tree.
#
# The raw records are part of the deliverable: a report whose numbers cannot be
# re-read is a claim, not evidence. This copies each run's JSON, summary and
# rendered configuration, and optionally its logs, keeping the run directory
# name so the report can point at a specific one.
#
# Usage: export-results.sh <results-root> <destination> [--with-logs]
set -euo pipefail

if [ $# -lt 2 ]; then
  sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi

source_root="$1"
destination="$2"
with_logs=0
[ "${3:-}" = "--with-logs" ] && with_logs=1

if [ ! -d "$source_root" ]; then
  echo "export-results: $source_root does not exist" >&2
  exit 1
fi
mkdir -p "$destination"

count=0
while IFS= read -r run_json; do
  run_dir="$(dirname "$run_json")"
  shape="$(basename "$(dirname "$run_dir")")"
  name="$(basename "$run_dir")"
  target="$destination/$shape/$name"
  mkdir -p "$target"
  cp "$run_json" "$target/run.json"
  [ -f "$run_dir/summary.md" ] && cp "$run_dir/summary.md" "$target/summary.md"
  if [ -d "$run_dir/configs" ]; then
    mkdir -p "$target/configs"
    cp "$run_dir/configs"/* "$target/configs/" 2>/dev/null || true
  fi
  if [ "$with_logs" = "1" ] && [ -d "$run_dir/logs" ]; then
    mkdir -p "$target/logs"
    for log in "$run_dir/logs"/*.log; do
      [ -f "$log" ] || continue
      # Logs are mostly one repeated INFO line per connection; gzip makes them
      # small enough to keep as evidence instead of discarding them.
      gzip -9 -c "$log" > "$target/logs/$(basename "$log").gz"
    done
  fi
  count=$((count + 1))
done < <(find "$source_root" -name run.json -not -path "$destination/*" | sort)

echo "exported $count run(s) to $destination"
du -sh "$destination"
