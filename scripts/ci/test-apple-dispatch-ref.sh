#!/usr/bin/env bash
# Functional test for the detached-HEAD dispatch-ref resolution in publish-apple-beta.sh.
#
# The grep assertions in test-apple-beta-publish.sh prove the code is still THERE. They cannot prove
# it WORKS, and the defect being fixed is a runtime one: `git rev-parse --abbrev-ref HEAD` answers
# the literal string `HEAD` in a detached checkout, which used to be handed to `gh` as a ref and
# rejected with `HTTP 422: No ref found for: HEAD`.
#
# So this extracts the resolution block from the real script and RUNS it against real repositories in
# each checkout state. It is not a reimplementation: the block is sliced out of the script by its own
# section markers, so the code under test is the shipped code.
#
# Usage: test-apple-dispatch-ref.sh
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

publish="./scripts/publish-apple-beta.sh"
pass=0
fail=0

ok()   { echo "  PASS: $1"; pass=$((pass + 1)); }
bad()  { echo "  FAIL: $1" >&2; shift; [ $# -gt 0 ] && printf '        %s\n' "$@" >&2; fail=$((fail + 1)); }

[ -f "$publish" ] || { echo "FAIL: $publish not found" >&2; exit 1; }

# ---------------------------------------------------------------------------
# Slice the resolution block out of the real script.
#
# The block is delimited by its own section comment and ends just before the canonical-asset step.
# A missing marker is a hard failure: silently testing an empty script would report success.
# ---------------------------------------------------------------------------
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

begin_marker='# 2b. resolve a dispatch ref that PROVABLY points at local_sha'
end_marker='# --- end of the dispatch-ref resolution block'
begin_line="$(grep -nF "$begin_marker" "$publish" | head -1 | cut -d: -f1)"
# Searched for AFTER the begin marker: the end marker must close this block, and searching from the top
# would accept an earlier occurrence. The guard below turns a bad slice into a loud failure.
end_line="$(awk -v b="$begin_line" 'NR > b && index($0, "# --- end of the dispatch-ref resolution block") { print NR; exit }' "$publish")"

if [ -z "$begin_line" ] || [ -z "$end_line" ] || [ "$begin_line" -ge "$end_line" ]; then
  echo "FAIL: could not slice the dispatch-ref block (begin=$begin_line end=$end_line)" >&2
  echo "      the markers moved; update this test rather than deleting it" >&2
  exit 1
fi

# The block reads $release_tag and fails through `fail()`, and it reads $local_sha / $local_branch
# which the real script sets before reaching it. Both are provided here from the actual checkout
# state, so the block is exercised exactly as the script exercises it.
{
  echo 'fail() { echo "FAIL: $*" >&2; exit 1; }'
  # Mirror the real script's own derivation rather than passing values in, so a change to how the
  # script derives them cannot drift from what this test feeds the block.
  echo 'local_sha="$(git rev-parse HEAD)"'
  echo 'local_branch="$(git rev-parse --abbrev-ref HEAD)"'
  echo '[ "$local_branch" = "HEAD" ] && local_branch=""'
  sed -n "${begin_line},$((end_line - 1))p" "$publish"
  echo 'echo "RESOLVED dispatch_ref=[$dispatch_ref]"'
} > "$work/block.sh"

echo "== sliced $(wc -l < "$work/block.sh") lines of the real resolution block =="

# ---------------------------------------------------------------------------
# Fixture: a bare "origin" plus a clone, so git ls-remote works for real.
# ---------------------------------------------------------------------------
remote="$work/origin.git"
git init --bare -q "$remote"

seed="$work/seed"
git init -q "$seed"
git -C "$seed" config user.email t@example.invalid
git -C "$seed" config user.name t
echo one > "$seed/file"
git -C "$seed" add file
git -C "$seed" -c commit.gpgsign=false commit -qm one
first_sha="$(git -C "$seed" rev-parse HEAD)"
git -C "$seed" branch -M testing
git -C "$seed" remote add origin "$remote"
git -C "$seed" push -q origin testing

echo two > "$seed/file"
git -C "$seed" add file
git -C "$seed" -c commit.gpgsign=false commit -qm two
second_sha="$(git -C "$seed" rev-parse HEAD)"
git -C "$seed" push -q origin testing

# An annotated tag at the FIRST commit, and one at the SECOND, so both resolutions can be checked.
git -C "$seed" tag -a v0.1.10 -m t "$first_sha"
git -C "$seed" tag -a v0.1.11 -m t "$second_sha"
git -C "$seed" push -q origin v0.1.10 v0.1.11

# Run the real block in a given checkout state. $1 dir, $2 release_tag, $3 expected ref (or '' for
# expected failure).
run_case() {
  local name="$1" dir="$2" tag="$3" want="$4"
  local out rc
  set +e
  out="$(cd "$dir" && release_tag="$tag" bash "$work/block.sh" 2>&1)"
  rc=$?
  set -e
  if [ -z "$want" ]; then
    # Expected to fail closed.
    if [ "$rc" -eq 0 ]; then
      bad "$name" "the block resolved a ref instead of failing closed" "$out"
    else
      ok "$name"
    fi
    return
  fi
  if [ "$rc" -ne 0 ]; then
    bad "$name" "exited $rc" "$out"
    return
  fi
  if printf '%s' "$out" | grep -qF "RESOLVED dispatch_ref=[$want]"; then
    ok "$name (resolved $want)"
  else
    bad "$name" "wanted dispatch_ref=$want" "$out"
  fi
}

echo
echo "== checkout states =="

# 1. On a branch that origin has at exactly HEAD -> the branch name is used.
run_case "on a branch at this commit resolves to the branch" "$seed" v0.1.11 testing

# 2. DETACHED at a commit that a tag points at -> the tag is used. THIS IS THE DEFECT.
detached_tag="$work/detached_tag"
git clone -q "$remote" "$detached_tag"
git -C "$detached_tag" checkout -q --detach v0.1.10
st="$(git -C "$detached_tag" rev-parse --abbrev-ref HEAD)"
[ "$st" = "HEAD" ] || bad "fixture precondition: clone is detached" "abbrev-ref returned '$st'"
run_case "DETACHED at a tag resolves to the tag" "$detached_tag" v0.1.10 v0.1.10

# 3. DETACHED at a commit NO tag points at, but a remote branch does -> the branch is used.
detached_bare="$work/detached_bare"
git clone -q "$remote" "$detached_bare"
git -C "$detached_bare" checkout -q --detach "$second_sha"
run_case "DETACHED with no matching tag falls back to a remote branch at this commit" \
  "$detached_bare" v0.9.9 testing

# 4. DETACHED at a commit no remote ref points at -> FAIL CLOSED.
detached_orphan="$work/detached_orphan"
git clone -q "$remote" "$detached_orphan"
git -C "$detached_orphan" config user.email t@example.invalid
git -C "$detached_orphan" config user.name t
echo orphan > "$detached_orphan/file"
git -C "$detached_orphan" add file
git -C "$detached_orphan" -c commit.gpgsign=false commit -qm orphan
run_case "DETACHED at an unpushed commit FAILS CLOSED rather than inventing a ref" \
  "$detached_orphan" v0.1.10 ""

# 5. On a branch whose REMOTE copy is behind -> the stale local name must NOT be trusted.
stale="$work/stale"
git clone -q "$remote" "$stale"
git -C "$stale" config user.email t@example.invalid
git -C "$stale" config user.name t
echo ahead > "$stale/file"
git -C "$stale" add file
git -C "$stale" -c commit.gpgsign=false commit -qm ahead
# Local `testing` is now ahead of origin/testing, so origin/testing != local_sha. The tag v0.1.11
# still points at the previous commit, not this one, so nothing resolves -> fail closed.
run_case "a branch ahead of its remote is not trusted as a dispatch ref" "$stale" v0.1.11 ""

echo
if [ "$fail" -gt 0 ]; then
  echo "FAILED: $fail of $((pass + fail)) dispatch-ref cases" >&2
  exit 1
fi
echo "PASS: all $pass dispatch-ref cases behaved as required"
