#!/usr/bin/env bash
# Self-test for require-executed-coverage.sh.
#
# The script it tests decides whether a CI step may claim that coverage was executed. Logic like
# that has to be executable on its own with fixtures that prove it fails when it should - the same
# reason check-reference-coverage.test.sh exists next to its subject. A guard nobody can falsify is
# a guard nobody can trust.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

subject="./scripts/ci/require-executed-coverage.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

passed=0
failed=0
report() {
  if [ "$2" -eq "$3" ]; then
    echo "PASS  $1"
    passed=$((passed + 1))
  else
    echo "FAIL  $1 (expected rc=$2, got rc=$3)"
    failed=$((failed + 1))
  fi
}

# exercise <name> <expected-rc> <log-content>
exercise() {
  local name="$1" expected="$2" content="$3"
  local log="$work/log.txt"
  printf '%s' "$content" > "$log"
  local rc=0
  "$subject" "$log" "selftest" > "$work/out.txt" 2>&1 || rc=$?
  report "$name" "$expected" "$rc"
}

# --- the case this script exists for -----------------------------------------
# Every matched test skipped. This is what the old `grep -c 'PASS|FAIL|SKIP'` guard accepted,
# and it is exactly the shape a run with no JIEJIE_CADDY_REFERENCE_BINARY produces.
exercise "all-SKIP is NOT_EXECUTED" 1 '=== RUN   TestA
--- SKIP: TestA (0.00s)
    caddy reference binary not configured
=== RUN   TestB
--- SKIP: TestB (0.00s)
    caddy reference binary not configured
'

# --- the honest cases --------------------------------------------------------
exercise "a passing suite is accepted" 0 '=== RUN   TestA
--- PASS: TestA (0.01s)
'
exercise "a mixed suite with one pass is accepted" 0 '=== RUN   TestA
--- PASS: TestA (0.01s)
=== RUN   TestB
--- SKIP: TestB (0.00s)
'
exercise "one pass among many skips is accepted" 0 '--- PASS: TestA (0.01s)
--- SKIP: TestB (0.00s)
--- SKIP: TestC (0.00s)
--- SKIP: TestD (0.00s)
'

# --- the failure cases -------------------------------------------------------
exercise "a failing test is refused even beside passes" 1 '--- PASS: TestA (0.01s)
--- FAIL: TestB (0.02s)
'
exercise "an empty log is refused" 1 ''
exercise "a log with no results is refused" 1 'ok  	github.com/example	0.01s
'
# A stale -run filter matches nothing, which is a different failure from an all-SKIP run and is
# refused for its own reason.
exercise "no tests matched is refused" 1 'testing: warning: no tests to run
PASS
ok  	github.com/example	0.01s
'

# --- subtests must not inflate the count -------------------------------------
# Only top-level results are counted: a subtest line is indented, and counting those would let a
# single passing parent with skipping children look like broad coverage.
exercise "indented subtest results are not counted" 1 '=== RUN   TestA
=== RUN   TestA/child
    --- SKIP: TestA/child (0.00s)
--- SKIP: TestA (0.00s)
'

# --- usage -------------------------------------------------------------------
rc=0
"$subject" > /dev/null 2>&1 || rc=$?
report "no arguments is a usage error" 2 "$rc"
rc=0
"$subject" "$work/does-not-exist" "label" > /dev/null 2>&1 || rc=$?
report "a missing log is a usage error" 2 "$rc"

echo
echo "require-executed-coverage self-test: $passed passed, $failed failed"
[ "$failed" -eq 0 ] || exit 1
