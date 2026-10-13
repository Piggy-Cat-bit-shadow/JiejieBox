#!/usr/bin/env bash
# Asserts that a `go test -v` run actually EXECUTED something, by counting results rather than
# treating every non-failure line as coverage.
#
# Usage: require-executed-coverage.sh <log-file> <label>
#
# Exit: 0 = at least one test PASSED, 1 = none did (or the log names a failure), 2 = usage.
#
# # Why this exists
#
# The anti-vacuity guards in server-linux-amd64.yml counted this:
#
#     grep -cE '^--- (PASS|FAIL|SKIP):'
#
# and treated a non-zero count as "the suite ran". It does not follow. `PASS|FAIL|SKIP` counts
# REPORTED tests, and a suite whose every test SKIPs reports exactly as many as one that passed -
# so a run in which nothing executed could still claim coverage. That is the failure this script
# exists to make impossible.
#
# # What is being distinguished
#
#   discovered  how many tests the filter matched, i.e. how many results were reported
#   executed    how many actually ran        (PASS + FAIL)
#   passed      how many succeeded
#   skipped     how many declined to run
#   failed      how many failed
#
# A suite may legitimately skip some tests - the reference suites skip against a binary that cannot
# serve them, and `check-reference-coverage.sh` explains why a SKIP still satisfies its
# ACCOUNTING check. Acceptance is a different question and is answered here: for a gate that claims
# differential or reference coverage, `passed > 0` is the minimum. A gate whose whole population
# skipped has coverage NOT_EXECUTED, and saying so is the point.
#
# The concrete case: the Caddy reference tests skip when JIEJIE_CADDY_REFERENCE_BINARY is unset,
# and no workflow sets it. Before this check the step printed "PASS: reference suite (N
# differential tests)" where every one of those N was a SKIP.
set -euo pipefail

log_file="${1:-}"
label="${2:-}"

if [ -z "$log_file" ] || [ -z "$label" ]; then
  echo "usage: $0 <log-file> <label>" >&2
  exit 2
fi
if [ ! -f "$log_file" ]; then
  echo "::error::$label: log file '$log_file' does not exist" >&2
  exit 2
fi

# Each `--- PASS:` / `--- FAIL:` / `--- SKIP:` line is one reported result. Subtests are indented,
# so the pattern is anchored at the start of the line and counts top-level results only - which is
# what a -run filter selects and what a coverage claim is about.
passed="$(grep -cE '^--- PASS:' "$log_file" || true)"
failed="$(grep -cE '^--- FAIL:' "$log_file" || true)"
skipped="$(grep -cE '^--- SKIP:' "$log_file" || true)"
discovered=$((passed + failed + skipped))
executed=$((passed + failed))

echo "$label: discovered=$discovered executed=$executed passed=$passed failed=$failed skipped=$skipped"

if [ "$failed" -gt 0 ]; then
  echo "::error::$label: $failed test(s) FAILED" >&2
  grep -E '^--- FAIL:' "$log_file" | sed 's/^/      /' >&2
  exit 1
fi

if [ "$discovered" -eq 0 ]; then
  echo "::error::$label: the filter matched NO tests, so this step proved nothing. The -run filter is stale." >&2
  exit 1
fi

if [ "$passed" -eq 0 ]; then
  echo "::error::$label: NOT_EXECUTED - every one of the $discovered matched test(s) skipped." >&2
  echo "      A gate that claims this coverage cannot claim it from a run in which nothing ran." >&2
  if [ "$label" = "reference suite" ]; then
    echo "      The usual cause is a missing JIEJIE_CADDY_REFERENCE_BINARY: the Caddy differential" >&2
    echo "      tests skip without it, and no workflow sets it. Either provision the reference" >&2
    echo "      binary or stop claiming differential coverage in this step." >&2
  fi
  grep -E '^--- SKIP:' "$log_file" | head -10 | sed 's/^/      /' >&2
  exit 1
fi

echo "$label: PASS ($passed passed, $skipped skipped)"
