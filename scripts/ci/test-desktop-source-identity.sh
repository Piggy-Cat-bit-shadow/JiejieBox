#!/usr/bin/env bash
# Behavioural tests for check-desktop-source-identity.sh.
#
# Usage: test-desktop-source-identity.sh
#
# Exit: 0 = every case behaved as required, 1 = a case did not.
#
# # What this protects
#
# The release path used to check out its publishing tooling at a floating BRANCH
# (`release_branch`, default `testing`) while the product came from an exact `core_sha`. A
# dispatch could therefore build one commit's installer with another commit's scripts and
# record only the first - and nothing in the run would say so, because every gate compares
# against the inputs it was handed.
#
# The identity is now exact and single: `TOOLING_SHA == CORE_SHA`, and the tag must exist and
# name that commit. These are the negative cases that keep it that way. They are cheap
# because the checks were extracted out of the workflow into a script; as an inline `run:`
# block the only way to exercise them was to dispatch a 120-minute Windows build.
#
# # How the fixtures work
#
# Three real git repositories are built in a scratch directory - a bare "origin", a core
# clone and a tooling clone - with real commits and real tags. That is deliberately not a
# mock: `git rev-parse`, `git describe --tags` and `git fetch` are exactly what the script
# runs in production, and a stub would only prove the stub. `--no-fetch` is used for almost
# every case so the suite needs no network; two cases exercise the fetch path against the
# local origin.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$HERE/check-desktop-source-identity.sh"

if [ ! -f "$CHECK" ]; then
  echo "FAIL: checker not found at $CHECK" >&2
  exit 1
fi

passed=0
failed=0
step=0

report() {
  local name="$1" ok="$2" detail="${3:-}"
  if [ "$ok" = "0" ]; then
    passed=$((passed + 1))
    printf 'PASS  %-62s %s\n' "$name" "$detail"
  else
    failed=$((failed + 1))
    printf 'FAIL  %-62s %s\n' "$name" "$detail"
  fi
}

# The common shapes are "this input must be refused", "this input must be accepted" and
# "this is a usage error". Each is a function taking the whole command, so `$?` is read
# immediately after it rather than after a `[ ]` test that would overwrite it.
report_expect_fail() {
  step=$((step + 1))
  local label="$1"; shift
  "$@" >"$WORK/out" 2>&1
  local rc=$?
  if [ "$rc" -ne 0 ]; then
    report "$(printf '%2d. %s' "$step" "$label") -> refused (rc=$rc)" 0
  else
    report "$(printf '%2d. %s' "$step" "$label") -> must be refused" 1 "accepted, rc=0"
  fi
}

report_expect_pass() {
  step=$((step + 1))
  local label="$1"; shift
  "$@" >"$WORK/out" 2>&1
  local rc=$?
  if [ "$rc" -eq 0 ]; then
    report "$(printf '%2d. %s' "$step" "$label") -> accepted" 0
  else
    report "$(printf '%2d. %s' "$step" "$label") -> must be accepted" 1 "rc=$rc"
  fi
}

report_expect_rc() {
  step=$((step + 1))
  local want="$1" label="$2"; shift 2
  "$@" >"$WORK/out" 2>&1
  local rc=$?
  if [ "$rc" = "$want" ]; then
    report "$(printf '%2d. %s' "$step" "$label") -> exit $want" 0
  else
    report "$(printf '%2d. %s' "$step" "$label") -> must exit $want" 1 "rc=$rc"
  fi
}

WORK="$(mktemp -d "${TMPDIR:-/tmp}/desktop-identity-XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

export GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME="identity test"
export GIT_AUTHOR_EMAIL="identity@test.invalid"
export GIT_COMMITTER_NAME="identity test"
export GIT_COMMITTER_EMAIL="identity@test.invalid"

g() { git -C "$1" "${@:2}" >/dev/null 2>&1; }

# --- fixtures ---------------------------------------------------------------
ORIGIN="$WORK/origin.git"
CORE="$WORK/core"
TOOLING="$WORK/tooling"
git init -q --bare "$ORIGIN"

git init -q "$CORE"
mkdir -p "$CORE/release"
printf '0.1.7\n' > "$CORE/release/JIEJIE_VERSION"
printf 'core\n' > "$CORE/data.txt"
g "$CORE" add -A
g "$CORE" commit -m 'core: release candidate'
g "$CORE" tag "v0.1.7"
CORE_SHA="$(git -C "$CORE" rev-parse HEAD)"

# A second commit, so "a commit that is not the release" exists.
printf 'later\n' >> "$CORE/data.txt"
g "$CORE" add -A
g "$CORE" commit -m 'core: later work'
OTHER_SHA="$(git -C "$CORE" rev-parse HEAD)"

# The release candidate is the one under test, so it goes back to HEAD.
g "$CORE" checkout -q --detach "$CORE_SHA"

g "$CORE" remote add origin "$ORIGIN"
g "$CORE" push -q origin "$CORE_SHA:refs/heads/testing" "refs/tags/v0.1.7"
g "$CORE" push -q origin "$OTHER_SHA:refs/heads/later"

# The tooling clone: the same commit, but reached through a BRANCH - which is exactly what
# the old model checked out, and exactly what must no longer be accepted as an identity.
git clone -q "$ORIGIN" "$TOOLING"
g "$TOOLING" checkout -q testing
g "$TOOLING" branch -f floating "$CORE_SHA"
g "$TOOLING" tag floating-tag "$CORE_SHA"
g "$TOOLING" checkout -q --detach "$CORE_SHA"

SHORT7="$(printf '%s' "$CORE_SHA" | cut -c1-7)"

# Convenience: the checker with the fixture paths filled in. `--no-fetch` unless a case says
# otherwise, so no case depends on the network by accident.
chk() {
  "$CHECK" --core-dir "$CORE" --tooling-dir "$TOOLING" --no-fetch \
    --version 0.1.7 --core-sha "$CORE_SHA" --tooling-sha "$CORE_SHA" "$@"
}

echo "fixtures: core=$CORE_SHA"
echo "          other=$OTHER_SHA"
echo

# --- the exact case ---------------------------------------------------------
report_expect_pass "exact tooling SHA, tag names the commit, version agrees" chk

# --- a floating reference is not an identity --------------------------------
for floating in testing floating floating-tag HEAD origin/testing \
                refs/heads/floating refs/tags/v0.1.7 refs/remotes/origin/testing; do
  report_expect_fail "floating tooling ref '$floating'" \
    chk --tooling-sha "$floating"
done

# --- a malformed or short SHA is not an identity ----------------------------
report_expect_rc 2 "an empty --tooling-sha is a usage error" \
  chk --tooling-sha ""
report_expect_fail "a 7-character short SHA" chk --tooling-sha "$SHORT7"
report_expect_fail "a 41-character SHA" chk --tooling-sha "${CORE_SHA}0"
report_expect_fail "an uppercase SHA" \
  chk --tooling-sha "ABCDEF0123456789ABCDEF0123456789ABCDEF01"
report_expect_fail "a non-hex string" chk --tooling-sha "not-a-sha"

# --- a different, valid commit is not the same identity ---------------------
report_expect_fail "a different valid commit as tooling_sha" \
  chk --tooling-sha "$OTHER_SHA"

# --- the tag must exist AND name the commit ---------------------------------
g "$CORE" tag -f "v0.1.7" "$OTHER_SHA"
report_expect_fail "the tag names another commit" chk
g "$CORE" tag -f "v0.1.7" "$CORE_SHA"

report_expect_fail "a version whose tag does not exist" \
  chk --version 0.1.8

# --- release/JIEJIE_VERSION is the version authority ------------------------
printf '0.1.6\n' > "$CORE/release/JIEJIE_VERSION"
report_expect_fail "release/JIEJIE_VERSION disagrees with --version" chk
printf '0.1.7\n' > "$CORE/release/JIEJIE_VERSION"

# `git describe --tags` is what stamps the daemon, so it must name the version.
g "$CORE" tag -d "v0.1.7"
report_expect_fail "no tag at HEAD, so describe cannot name the version" chk
g "$CORE" tag "v0.1.7" "$CORE_SHA"

# --- the core checkout must be the named commit -----------------------------
report_expect_fail "core HEAD is not core_sha" \
  chk --core-sha "$OTHER_SHA" --tooling-sha "$OTHER_SHA"

# --- the tooling checkout must be the named revision ------------------------
g "$TOOLING" checkout -q --detach "$OTHER_SHA"
report_expect_fail "the tooling worktree is not the named revision" chk
g "$TOOLING" checkout -q --detach "$CORE_SHA"

# --- the fetch path, against the local origin -------------------------------
report_expect_pass "the fetch path succeeds against a reachable origin" \
  chk --remote origin

# An unreachable remote must fail - and the local tag is deleted first, or `git fetch` would
# find it already present, never touch the network, and report success for a remote that
# does not exist. That vacuous pass is what this case exists to prevent.
g "$TOOLING" tag -d "v0.1.7"
report_expect_fail "an unreachable remote" \
  chk --remote "$WORK/does-not-exist.git"
g "$TOOLING" fetch -q origin "+refs/tags/v0.1.7:refs/tags/v0.1.7"

# --- argument handling ------------------------------------------------------
report_expect_rc 2 "missing required arguments" \
  "$CHECK" --core-dir "$CORE" --tooling-dir "$TOOLING" --no-fetch
report_expect_rc 2 "no arguments at all" "$CHECK"
report_expect_rc 2 "an unknown argument" \
  chk --bogus
report_expect_rc 2 "--version with no value" \
  "$CHECK" --core-dir "$CORE" --tooling-dir "$TOOLING" --no-fetch \
    --version --core-sha "$CORE_SHA" --tooling-sha "$CORE_SHA"

# --- the workflow must not reintroduce a floating ref ------------------------
WF="$HERE/../../.github/workflows/client-desktop-windows.yml"
if [ -f "$WF" ]; then
  if grep -q 'RELEASE_BRANCH\|release_branch' "$WF"; then
    report "the workflow carries no floating branch ref" 1 "release_branch is back"
  else
    report "the workflow carries no floating branch ref" 0
  fi
  if grep -q 'check-desktop-source-identity.sh' "$WF"; then
    report "the workflow calls this checker instead of re-implementing it" 0
  else
    report "the workflow calls this checker instead of re-implementing it" 1 "not called"
  fi
  if grep -q 'tooling_sha' "$WF"; then
    report "the workflow declares a tooling_sha input" 0
  else
    report "the workflow declares a tooling_sha input" 1 "absent"
  fi
else
  report "the desktop workflow is present" 1 "not found at $WF"
fi

echo
echo "desktop source identity: $passed passed, $failed failed"
[ "$failed" -eq 0 ]
