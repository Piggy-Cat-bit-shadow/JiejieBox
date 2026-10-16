#!/usr/bin/env bash
# Publishes an Apple beta to TestFlight from a CI-verified Libbox artifact.
#
# # Why this exists
#
# The Apple pipeline splits cleanly along the line of what needs secrets. Compiling Libbox and
# proving the client builds needs none, and CI already does both. Archiving, signing and uploading
# need the certificate, the provisioning profile and an Apple ID session, and those must never leave
# this Mac.
#
# Before this script, a release meant running the heavy non-secret work locally as well, and
# compiling Libbox a third time for a commit CI had already compiled. This script instead requires
# that CI has passed for the exact commit being released, takes the Libbox it produced, proves the
# artifact belongs to that commit, and then hands off to the existing release pipeline. Signing,
# export and upload are not reimplemented here.
#
# Usage:
#   ./scripts/publish-apple-beta.sh [--run-id <id>] [--no-dispatch]
#
#   --run-id       publish from a specific run instead of looking one up for this commit
#   --no-dispatch  fail when no run exists for this commit instead of dispatching one
#
# Environment:
#   APPLE_BUILD_NUMBER   respected if set; otherwise derived once for the whole run
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

workflow="client-apple.yml"
input_dir="build/apple-publish/input"
run_id=""
no_dispatch=0
# Where the Libbox for this release comes from. `auto` is the default and prefers the canonical
# Release asset, falling back to the exact-SHA Actions artifact ONLY when the asset is absent.
libbox_source="auto"

while [ $# -gt 0 ]; do
  case "$1" in
    --run-id) run_id="${2:?--run-id needs a value}"; shift 2 ;;
    --no-dispatch) no_dispatch=1; shift ;;
    --libbox-source) libbox_source="${2:?--libbox-source needs a value}"; shift 2 ;;
    -h|--help) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "publish-apple-beta: unknown argument '$1'" >&2; exit 2 ;;
  esac
done

case "$libbox_source" in
  auto|release|actions) ;;
  *) echo "publish-apple-beta: --libbox-source must be auto, release or actions (got '$libbox_source')" >&2; exit 2 ;;
esac

fail() { echo; echo "FAIL: $*" >&2; exit 1; }
step() { echo; echo "== $* =="; }

# ---------------------------------------------------------------------------
# 1. repository preflight
# ---------------------------------------------------------------------------
step "repository"
# ---------------------------------------------------------------------------
# Identity, deliberately FOUR distinct values and never one overloaded `branch`.
# ---------------------------------------------------------------------------
#
# A single `branch` variable used to carry three unrelated meanings at once: the local checkout
# state, the ref handed to GitHub for dispatch, and the identity a run is checked against. They
# coincide only while the checkout is on a normal branch, so the conflation was invisible until
# someone published from a release tag:
#
#     git checkout --detach v0.1.10
#     ./scripts/publish-apple-beta.sh
#
# `git rev-parse --abbrev-ref HEAD` answers the literal string `HEAD` when detached - not an error,
# not empty, just a name no remote has. That string then reached the GitHub API as `--branch HEAD`
# and `--ref HEAD`, producing `HTTP 422: No ref found for: HEAD`, and the obvious workaround
# (publishing from a branch) defeated the point of releasing a tag at all.
#
# The four values, and why each is separate:
#
#   local_sha     the commit being published. THE authority for identity; `git rev-parse HEAD` is
#                 correct in every checkout state, detached or not.
#   local_branch  the local checkout's branch name, or empty when detached. Informational only -
#                 it is never sent to GitHub, because `HEAD` is not a ref.
#   release_tag   the tag the repository's own version contract implies, e.g. v0.1.10. Derived
#                 later, from release/JIEJIE_VERSION, so there is one source of truth for it.
#   dispatch_ref  a ref that PROVABLY resolves to local_sha on the remote. The only value ever
#                 passed to `--ref` or `--branch`.
local_sha="$(git rev-parse HEAD)"
local_branch="$(git rev-parse --abbrev-ref HEAD)"
if [ "$local_branch" = "HEAD" ]; then
  # Detached: at a tag, at a raw commit, or mid-rebase. Deliberately not fatal - publishing a
  # release tag is a supported workflow, and this is the case the old code broke.
  local_branch=""
fi
# Kept as `branch` for the published interface. The contract test greps this script for `branch:`,
# and the log line is genuinely useful, so the name survives with a corrected meaning: the local
# checkout state, empty when detached.
branch="$local_branch"

# A TestFlight build has to correspond to a commit that exists. Publishing a dirty tree produces a
# binary nobody can rebuild or attribute, and App Store Connect would accept it without complaint,
# so the check has to be here rather than left to the operator's judgement.
if [ -n "$(git status --porcelain)" ]; then
  echo "The working tree has uncommitted changes:" >&2
  git status --short | sed 's/^/  /' >&2
  fail "TestFlight release must correspond to a reproducible Git commit.
  Commit or discard these changes, then re-run.
  There is deliberately no --allow-dirty: an unreproducible build is not attributable to CI."
fi

# ---------------------------------------------------------------------------
# 1b. select the Apple source
# ---------------------------------------------------------------------------
#
# ONE Apple repository, ONE source, TWO products. Both products are built from the commit
# the parent gitlink records, so there is a single shared SHA and `assert-shared-source`
# gates exactly that. An earlier revision had two per-platform pins and asserted they
# differed; that model is gone, and `assert-distinct` no longer exists as an action.
step "Apple sources"

# Start this release from a FRESH resolution.
#
# The resolver freezes the SHA it picks into APPLE_CLIENT_STATE so that one build cannot resolve
# iOS and macOS to two different commits. That guarantee is WITHIN a build; the file itself lives
# under build/, which nothing cleans, so it outlives the release that wrote it.
#
# Nothing invalidated it, and that shipped a stale UI. v0.1.10 froze 088196af on 2026-10-15; the
# v0.1.16 run on 2026-10-17 read that file, never asked the remote, and built the OLDER Apple
# commit - leaving out 6ce67b1 "fix(hako): make the iPhone outbound-mode control work against a
# real configuration". Every check still passed, which is what makes this failure mode expensive:
# a frozen commit is a real commit of the right repository, so the SHA agreement, the Libbox
# manifest and all pre-upload gates are equally satisfied by the wrong answer.
#
# So each publish run resolves the tip as it stands NOW and freezes it for its own duration - the
# documented model, "resolved to an exact SHA at the start of a build". To reproduce an exact
# revision instead, set APPLE_CLIENT_REF: it wins over everything and is deliberately left alone.
apple_state="${APPLE_CLIENT_STATE:-build/apple-client-source.env}"
if [ -f "$apple_state" ]; then
  echo "  discarding the previous resolution in $apple_state; a new release resolves the tip again"
  rm -f "$apple_state"
fi

eval "$(./scripts/ci/apple-client-source.sh resolve)"

ios_client_dir="$IOS_APPLE_DIR"
macos_client_dir="$MACOS_APPLE_DIR"
export APPLE_CLIENT_REPOSITORY IOS_APPLE_SHA IOS_APPLE_BRANCH MACOS_APPLE_SHA MACOS_APPLE_BRANCH

# The gitlink is no longer the authority, so it is no longer an equality gate.
#
# It is still READ, because a submodule that has drifted behind the resolved source is something
# a reader of this log needs to know, and because `gitlink_sha` is part of this script's
# published interface (the publish contract test greps for it). What changed is the consequence:
# a difference is reported, not fatal. Making it fatal is precisely what required a second
# commit here before any client change could be built.
gitlink_sha="$(git ls-tree HEAD "$ios_client_dir" | awk '{print $3}')"
[ -n "$gitlink_sha" ] || fail "the parent records no gitlink at $ios_client_dir."
if [ "$gitlink_sha" != "$IOS_APPLE_SHA" ]; then
  if [ "${APPLE_CLIENT_REQUIRE_GITLINK:-0}" = "1" ]; then
    fail "the parent records $ios_client_dir at $gitlink_sha but the source selection resolved
  to $IOS_APPLE_SHA. APPLE_CLIENT_REQUIRE_GITLINK=1 requires the build to match the gitlink."
  fi
  echo "  note: clients/apple gitlink is $gitlink_sha; this build uses $IOS_APPLE_SHA."
  echo "        The Apple client repository is the authority; the gitlink is the local default"
  echo "        checkout. Set APPLE_CLIENT_REQUIRE_GITLINK=1 to require equality."
fi

./scripts/ci/apple-client-source.sh assert-shared-source
echo "  iOS:   $IOS_APPLE_BRANCH @ $IOS_APPLE_SHA  (Apple client repository)"
echo "  macOS: $MACOS_APPLE_BRANCH @ $MACOS_APPLE_SHA  (same shared source)"

# ---------------------------------------------------------------------------
# Restore what this run changes
# ---------------------------------------------------------------------------
#
# prepare-apple-client.sh applies overlays that MODIFY each Apple checkout in place. That is
# by design, but it left the iOS submodule dirty, so the next run of this script failed its
# own clean-tree gate and the operator had to clean up by hand after every release.
#
# The gate above has already established that the tree was clean, so anything dirty under
# either checkout when this script exits was produced by this script. Restoring cannot
# discard user work - and restoring only the Apple checkouts, rather than running
# `git reset --hard` on the parent, keeps the blast radius to the directories the overlays
# touch.
#
# `checkout -- .` rather than `reset --hard`: every overlay write targets a file it read
# first, so the changes are tracked modifications and there is nothing untracked to sweep.
# Both were verified against a real overlay run.
#
# The macOS checkout is deliberately NOT removed: it is a pinned checkout under build/,
# reused across runs, and leaving it in place means a re-run does not re-clone it. It is
# restored to its own revision, not deleted.
#
# Installed with `trap ... EXIT` so success, failure and Ctrl-C all take the same path.
restore_clients() {
  local status
  status=$?
  local dir
  for dir in "$ios_client_dir" "$macos_client_dir"; do
    [ -e "$dir/.git" ] || continue
    if [ -n "$(git -C "$dir" status --porcelain 2>/dev/null)" ]; then
      git -C "$dir" checkout -- . 2>/dev/null || true
      echo
      echo "restored $dir to its pinned revision (overlay changes from this run discarded)"
    fi
  done
  return $status
}
trap restore_clients EXIT
# shellcheck disable=SC1091
eval "$(./scripts/ci/version.sh)"
echo "  commit:    $local_sha"
# Name the checkout state honestly. The old line printed the literal `branch: HEAD` when detached,
# which read as a branch named HEAD and is what led the API calls to be handed an unusable ref.
echo "  branch:    $(if [ -n "$branch" ]; then echo "$branch"; else echo '(detached HEAD)'; fi)"
echo "  version:   $JJ_VERSION"

# ---------------------------------------------------------------------------
# 2. GitHub CLI preflight
# ---------------------------------------------------------------------------
step "GitHub"
command -v gh >/dev/null 2>&1 || fail "the GitHub CLI (gh) is required but was not found."
gh auth status >/dev/null 2>&1 || fail "gh is not authenticated. Run: gh auth login"

# Resolved rather than hardcoded, so a fork or a rename does not silently query the wrong repository.
repo="$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null)" \
  || fail "could not determine the repository. Run this from inside a checkout with a GitHub remote."
echo "  repository: $repo"

# ---------------------------------------------------------------------------
# 2b. resolve the release tag, and decide the Libbox source
# ---------------------------------------------------------------------------
#
# The tag is derived from release/JIEJIE_VERSION and then RESOLVED against the remote, peeled if it
# is annotated, and required to point at the commit being published. Three weaker things are
# deliberately not used:
#
#   * the local tag, which can be stale or absent;
#   * the version string alone, which says what the tag should be named, not where it points;
#   * `gh release view` without a tag, i.e. "the latest release", which is a different question.
step "release tag"
jj_version="$(eval "$(./scripts/ci/version.sh)"; printf '%s' "$JJ_VERSION")"
[ -n "$jj_version" ] || fail "could not resolve JJ_VERSION from release/JIEJIE_VERSION"
release_tag="v${jj_version}"
echo "  release/JIEJIE_VERSION: $jj_version"
echo "  expected tag:           $release_tag"

# `git ls-remote` rather than the local ref: the remote is the authority on where a tag points.
tag_line="$(git ls-remote --tags origin "refs/tags/$release_tag^{}" "refs/tags/$release_tag" 2>/dev/null || true)"
tag_sha="$(printf '%s\n' "$tag_line" | awk '$2 ~ /\^\{\}$/ {print $1}' | head -1)"
if [ -z "$tag_sha" ]; then
  # Not annotated, so the plain ref is the commit.
  tag_sha="$(printf '%s\n' "$tag_line" | awk '{print $1}' | head -1)"
fi
if [ -z "$tag_sha" ]; then
  if [ "$libbox_source" = "actions" ]; then
    echo "  tag:                    $release_tag not found on origin (--libbox-source actions does not need it)"
  else
    fail "tag $release_tag does not exist on origin, so there is no release to take a canonical Libbox from.
  Push the tag first, or pass --libbox-source actions to use the exact-SHA CI artifact instead."
  fi
elif [ "$tag_sha" != "$local_sha" ]; then
  fail "tag $release_tag points at $tag_sha, but this checkout is $local_sha.
  Publishing would attach this commit's Libbox to a release for a different commit."
else
  echo "  tag:                    $tag_sha (== HEAD)"
fi

# The canonical Libbox asset name inside the release. Exact, never globbed.
libbox_asset="jiejie-libbox-apple-${jj_version}.tar.gz"

# ---------------------------------------------------------------------------
# 2b. resolve a dispatch ref that PROVABLY points at local_sha
# ---------------------------------------------------------------------------
#
# Only this step may produce a value for `gh workflow run --ref`. `local_branch` is not enough on
# its own - a local branch name says nothing about whether the remote has it, or whether the
# remote's copy is the commit being published - and a detached checkout has no name at all.
#
# The order is deliberate, and each candidate is VERIFIED against the remote rather than assumed:
#
#   1. the current branch, when the remote has it at exactly local_sha (the common case, and the
#      only one where the local name is meaningful);
#   2. the release tag, when the remote has it at exactly local_sha - which is the detached-at-tag
#      case this change exists for;
#   3. any remote branch at exactly local_sha, so a checkout that is behind or ahead of its own
#      branch name can still be published from the ref GitHub does have;
#   4. otherwise FAIL CLOSED.
#
# Nothing here writes to the remote. No push, no temporary branch, no tag creation or movement, and
# no switching the operator's checkout - a publisher that mutates the repository to make its own
# dispatch legal would be making the release attributable to a ref the operator never chose.
dispatch_ref=""
dispatch_reason=""

remote_branch_sha() {
  # The remote's commit for a branch, or empty. `--heads` keeps a same-named tag from answering.
  # An empty result and an empty local_sha must never be treated as a match, so the caller compares
  # only against a local_sha that is known non-empty.
  [ -n "${local_sha:-}" ] || return 0
  git ls-remote --heads origin "refs/heads/$1" 2>/dev/null | awk '{print $1}' | head -1
}

remote_tag_sha() {
  # Annotated tags need the peeled value; the plain ref is the tag OBJECT for those, which is not a
  # commit and must never be compared against local_sha.
  [ -n "${local_sha:-}" ] || return 0
  local peeled plain
  peeled="$(git ls-remote --tags origin "refs/tags/$1^{}" 2>/dev/null | awk '{print $1}' | head -1)"
  if [ -n "$peeled" ]; then
    printf '%s' "$peeled"
    return
  fi
  plain="$(git ls-remote --tags origin "refs/tags/$1" 2>/dev/null | awk '{print $1}' | head -1)"
  printf '%s' "$plain"
}

# A ref whose remote commit is EMPTY did not match. Writing the comparison as `= "$local_sha"` alone
# would let an absent remote ref match an absent local_sha; local_sha is always set by the time this
# runs, but the guard is what makes that a property of the code rather than of the call order.
ref_matches() {
  [ -n "$1" ] && [ "$1" = "$local_sha" ]
}

if [ -n "$local_branch" ] && ref_matches "$(remote_branch_sha "$local_branch")"; then
  dispatch_ref="$local_branch"
  dispatch_reason="the local branch, which origin has at exactly this commit"
elif ref_matches "$(remote_tag_sha "$release_tag")"; then
  dispatch_ref="$release_tag"
  dispatch_reason="the release tag $release_tag, which origin has at exactly this commit"
else
  # Last resort: any remote branch that is exactly this commit. This covers the detached case when
  # the tag is absent, and the case where the local branch name is stale or unrelated. Sorted for
  # determinism so the same repository state always picks the same ref.
  while IFS= read -r candidate; do
    [ -n "$candidate" ] || continue
    if ref_matches "$(remote_branch_sha "$candidate")"; then
      dispatch_ref="$candidate"
      dispatch_reason="remote branch $candidate, which origin has at exactly this commit"
      break
    fi
  done <<EOF
$(git ls-remote --heads origin 2>/dev/null | awk '{print $2}' | sed 's#^refs/heads/##' | sort)
EOF
fi

if [ -z "$dispatch_ref" ]; then
  fail "no ref on origin points at $local_sha, so there is nothing to dispatch against.
  Checked: branch '$local_branch' (when on one), tag '$release_tag', and every remote branch.
  Push the commit (or the tag) first. This script will not create a branch or move a tag to make
  its own dispatch legal, because that would attribute the release to a ref you never chose.
  Pass --no-dispatch to validate without dispatching, or --libbox-source release|actions to skip CI."
fi

echo "  checkout:               $(if [ -n "$local_branch" ]; then echo "branch $local_branch"; else echo 'detached HEAD (no branch)'; fi)"
echo "  dispatch ref:           $dispatch_ref  ($dispatch_reason)"
# --- end of the dispatch-ref resolution block ---------------------------------
#
# The pair of section markers around this block is load-bearing beyond readability:
# `scripts/ci/test-apple-dispatch-ref.sh` slices the block out by them and EXECUTES it against real
# repositories in each checkout state. A grep assertion can only prove the code is still present; the
# defect being fixed is a runtime one, so it has to be run. Moving these markers without updating that
# test makes the slice fail loudly rather than silently test an empty script.

# The three states, decided ONLY by listing the release's assets by exact name.
#
#   ABSENT           the release exists (or does not) but has no asset with this exact name
#   PRESENT_VALID    the asset exists and passes every identity check
#   PRESENT_INVALID  the asset exists and fails one - which is FATAL, never a fallback
#
# The distinction between ABSENT and PRESENT_INVALID is the whole point: an asset that is present
# but corrupt, mislabelled or built from another commit means the canonical source is broken, and
# silently using the Actions artifact instead would publish something the release says it is not.
# A validation failure must never be reported as absence.
canonical_state="ABSENT"
if [ "$libbox_source" != "actions" ] && [ -n "$tag_sha" ]; then
  step "canonical Libbox asset"
  if ! asset_names="$(gh release view "$release_tag" --repo "$repo" --json assets -q '.assets[].name' 2>/dev/null)"; then
    # The release may simply not exist yet. Under `release` that is fatal; under `auto` it is
    # genuinely ABSENT, because a release with no such asset and no release at all are the same
    # answer to the only question being asked here - "is there a canonical asset to consume".
    if [ "$libbox_source" = "release" ]; then
      fail "could not read release $release_tag in $repo, and --libbox-source release requires it."
    fi
    echo "  no readable release $release_tag; treating the canonical asset as ABSENT"
  else
    if printf '%s\n' "$asset_names" | grep -qxF "$libbox_asset"; then
      canonical_state="PRESENT"
      echo "  asset: $libbox_asset (present, exact name)"
    else
      echo "  asset: $libbox_asset not among the release's assets; treating it as ABSENT"
    fi
  fi
fi

use_canonical=0
case "$libbox_source:$canonical_state" in
  release:ABSENT)
    fail "--libbox-source release was requested but $release_tag has no asset named $libbox_asset.
  Publishing from Actions instead would mean this run's Libbox is not the released one." ;;
  actions:*)
    : ;;
  *:PRESENT)
    use_canonical=1 ;;
  *:ABSENT)
    : ;;
esac

if [ "$use_canonical" = "1" ]; then
  # PRESENT is not VALID until it is proved. Every failure below is FATAL and, because
  # `canonical_state` is already PRESENT, control never reaches the Actions path afterwards.
  step "download the canonical Libbox"
  rm -rf "$input_dir"
  mkdir -p "$input_dir"
  gh release download "$release_tag" --repo "$repo" --pattern "$libbox_asset" --dir "$input_dir" \
    || fail "could not download $libbox_asset from release $release_tag. The canonical source is PRESENT but unreadable; refusing to fall back to Actions, because a release asset that cannot be fetched is a broken canonical source and not an absent one."

  bundle="$input_dir/$libbox_asset"
  [ -f "$bundle" ] || fail "$release_tag supplied no $libbox_asset after download"

  # Unpack the outer bundle and validate the INNER artifact exactly as the Actions path does, with
  # the same tool and the same expectations. The outer bundle is promotion packaging; the identity
  # being checked is the producer's.
  unpack="$input_dir/unpacked"
  rm -rf "$unpack"; mkdir -p "$unpack"
  tar -xzf "$bundle" -C "$unpack" || fail "$libbox_asset is not a readable tar.gz"
  ./scripts/ci/apple-libbox-artifact.sh validate "$unpack" \
    --parent-sha "$local_sha" \
    --jj-version "$jj_version" \
    || fail "the canonical Libbox asset in release $release_tag failed validation. This is PRESENT_INVALID: the release's own Apple core does not match this commit or this release. Nothing was published, and no fallback was attempted."

  echo "  canonical Libbox validated: parent_sha, jj_version, digest and slices all match"
  libbox_from="release:$release_tag"
fi

# --- Actions path ------------------------------------------------------------
#
# Everything below is the pre-existing exact-SHA path, unchanged and NOT relaxed by the new source:
# exact SHA run, exact run headSha, conclusion success, required jobs, exact artifact name,
# manifest parent validation, shared install, APPLE_USE_PREBUILT_LIBBOX=1.
#
# It runs in two situations and only two: the canonical asset was ABSENT under `auto` or under
# `actions`, or the caller asked for `actions` explicitly. When the canonical path ran, it already
# put a validated artifact in `$input_dir` and the validate step at the end of this section
# re-checks it with the same tool - which is deliberate: the canonical bytes are held to exactly
# the same standard as the Actions ones, by the same code.

# ---------------------------------------------------------------------------
# 3. find the Apple CI run for THIS commit
# ---------------------------------------------------------------------------
#
# Sections 3 and 4 exist to obtain the Libbox from Actions. When the canonical Release asset was
# used they are SKIPPED entirely - not merely tolerated. The download in section 4 starts with
# `rm -rf "$input_dir"`, so running it after the canonical path would delete the validated artifact
# and replace it with a different one, which is precisely the substitution this whole change exists
# to prevent.
if [ "$use_canonical" = "1" ]; then
  step "Apple CI: not consulted"
  echo "  the canonical Release asset was used; no Actions run was selected or downloaded"
else
step "Apple CI for $local_sha"

if [ -z "$run_id" ]; then
  # The SHA is matched exactly. Taking the most recent successful run would happily publish the
  # previous commit's Libbox, which is the failure this whole check exists to prevent.
  run_id="$(gh run list --repo "$repo" --workflow "$workflow" --branch "$dispatch_ref" \
    --limit 100 --json databaseId,headSha,conclusion \
    -q "[.[] | select(.headSha == \"$local_sha\" and .conclusion == \"success\")][0].databaseId" \
    2>/dev/null || true)"
fi

if [ -z "$run_id" ] || [ "$run_id" = "null" ]; then
  if [ "$no_dispatch" = "1" ]; then
    fail "no $workflow run found for $local_sha, and --no-dispatch was given.
  Dispatch it and wait, then re-run:
      gh workflow run $workflow --ref $dispatch_ref
      gh run watch"
  fi

  # Dispatching is safe only because the run it produces is verified against the local commit before
  # anything is published from it. A dispatch runs the BRANCH, so if the branch has moved it builds
  # someone else's commit - and the headSha check below refuses exactly that, which is why the check
  # is what makes this convenience permissible rather than a hole in it.
  #
  # The branch tip is compared first anyway, so the common mistake - publishing a commit that was
  # never pushed - is reported as itself instead of as a 40-minute wait for a run that cannot match.
  remote_sha="$(gh api "repos/$repo/commits/$dispatch_ref" --jq .sha 2>/dev/null || true)"
  if [ -z "$remote_sha" ]; then
    fail "could not read $dispatch_ref from $repo to check that $local_sha is pushed."
  fi
  [ "$remote_sha" = "$local_sha" ] || fail \
    "$dispatch_ref is at $remote_sha on $repo, but $local_sha is checked out.
  A dispatched run would build the branch tip, not this commit. Push first, then re-run."

  step "dispatching $workflow for $local_sha"
  gh workflow run "$workflow" --ref "$dispatch_ref" --repo "$repo" \
    || fail "could not dispatch $workflow."

  # Wait for the run that belongs to THIS commit. A run created for another commit never satisfies
  # this, so the wait ends in a refusal rather than in publishing the wrong one.
  deadline=$((SECONDS + 5400))
  while [ "$SECONDS" -lt "$deadline" ]; do
    read -r candidate_id candidate_status candidate_conclusion <<<"$(
      gh run list --repo "$repo" --workflow "$workflow" --branch "$dispatch_ref" --limit 20 \
        --json databaseId,headSha,status,conclusion \
        -q "[.[] | select(.headSha == \"$local_sha\")][0] | \"\(.databaseId) \(.status) \(.conclusion)\"" \
        2>/dev/null || true
    )"
    if [ -n "${candidate_id:-}" ] && [ "$candidate_id" != "null" ] && [ "$candidate_status" = "completed" ]; then
      run_id="$candidate_id"
      echo "  run:        $run_id ($candidate_status)"
      break
    fi
    echo "  waiting for a run of $local_sha... (${candidate_status:-not created yet})"
    sleep 30
  done
  [ -n "$run_id" ] || fail \
    "no completed $workflow run for $local_sha appeared within 90 minutes.
  Check: gh run list --workflow $workflow --branch $dispatch_ref"
fi

# Re-verify the run even when the id was supplied: --run-id is an escape hatch for re-publishing a
# known run, not a way to publish an arbitrary one.
run_sha="$(gh run view "$run_id" --repo "$repo" --json headSha -q .headSha)"
run_conclusion="$(gh run view "$run_id" --repo "$repo" --json conclusion -q .conclusion)"
[ "$run_sha" = "$local_sha" ] || fail \
  "run $run_id is for $run_sha, not the checked-out $local_sha."
[ "$run_conclusion" = "success" ] || fail "run $run_id concluded '$run_conclusion', not success."

echo "  run:        $run_id"
echo "  conclusion: $run_conclusion"
echo "  head SHA:   $run_sha  (matches)"

# A run-level success does not prove both clients were built: the workflow's build_ios and
# build_macos inputs skip their job without failing the run, and a skipped job contributes nothing to
# the conclusion. The pipeline below signs and uploads BOTH clients, so the run has to have proved
# both. This applies to --run-id as well - it is a way to re-publish a known run, not to accept one.
jobs_json="$(gh run view "$run_id" --repo "$repo" --json jobs)" \
  || fail "could not read the jobs of run $run_id."
if ! job_results="$(printf '%s' "$jobs_json" | python3 scripts/ci/apple-ci-job-gate.py)"; then
  fail "run $run_id may not be published from (see above).
  Re-dispatch with both platforms enabled:
      gh workflow run $workflow --ref $dispatch_ref -f build_ios=true -f build_macos=true
      gh run watch"
fi
job_libbox="$(printf '%s\n' "$job_results" | sed -n 's/^libbox=//p')"
job_ios="$(printf '%s\n' "$job_results" | sed -n 's/^ios=//p')"
job_macos="$(printf '%s\n' "$job_results" | sed -n 's/^macos=//p')"
echo "  jobs:       libbox=$job_libbox ios=$job_ios macos=$job_macos"

# ---------------------------------------------------------------------------
# 4. download the Libbox artifact from that exact run
# ---------------------------------------------------------------------------
step "download the verified Libbox"
rm -rf "$input_dir"
mkdir -p "$input_dir"

# Named after the commit by the workflow, and downloaded by that name rather than by pattern, so a
# stale artifact from an earlier run cannot be picked up.
gh run download "$run_id" --repo "$repo" --name "jiejiebox-libbox-$local_sha" --dir "$input_dir" \
  || fail "run $run_id has no Libbox artifact for $local_sha.
  The run may predate the shared-libbox pipeline; re-dispatch the workflow."
fi

# ---------------------------------------------------------------------------
# 5. validate and install
# ---------------------------------------------------------------------------
step "validate the Libbox artifact"
# Everything that makes the artifact attributable lives in the manifest, so the manifest is what is
# checked - not the artifact name. TWO independent identities are matched:
#
#   parent_sha   ties the bytes to a COMMIT (the core does not depend on which Apple UI branch
#                links it, and the two platforms deliberately use two different ones)
#   jj_version   ties the bytes to a RELEASE, read from release/JIEJIE_VERSION rather than from the
#                manifest, so one side of the comparison is an independent authority
#
# `version` is deliberately NOT compared: it is JJ_ARTIFACT_VERSION (`0.1.7+jiejie.<sha>`), so an
# equality check against the release version could only ever fail.
local_jj="$(eval "$(./scripts/ci/version.sh)"; printf '%s' "$JJ_VERSION")"
[ -n "$local_jj" ] || fail "could not resolve JJ_VERSION from release/JIEJIE_VERSION"

# The artifact lives in a different place depending on where it came from: the Actions download
# puts files straight into $input_dir, while the canonical Release asset is a promoting tarball
# whose payload was unpacked into $unpack. `$validated` names whichever this run used, so the
# second validation below re-checks the SAME bytes rather than looking in the wrong directory and
# passing because nothing was there.
if [ "$use_canonical" = "1" ]; then
  validated="$unpack"
else
  validated="$input_dir"
fi
./scripts/ci/apple-libbox-artifact.sh validate "$validated" \
  --parent-sha "$local_sha" \
  --jj-version "$local_jj"

artifact_sha="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["artifact_sha256"])' \
  "$validated/manifest.json")"

# Materialise both Apple sources at their own pinned revisions, then install the ONE verified
# framework into each. A failure here is fatal: publishing with only one client's source present
# would either build the wrong UI for a platform or ship a half-configured release.
step "check out both Apple sources"
./scripts/ci/apple-client-source.sh checkout ios >/dev/null
./scripts/ci/apple-client-source.sh verify ios
./scripts/ci/apple-client-source.sh checkout macos >/dev/null
./scripts/ci/apple-client-source.sh verify macos

# `$validated`, not `$input_dir`: both installs must put THE SAME bytes into both clients, and the
# bytes proved above are the ones in `$validated`. Reading the manifest and installing from
# different directories would be exactly the divergence check-apple-shared-libbox.sh exists to
# catch - and it would catch it only after both clients had been built.
step "install the verified Libbox into both clients"
./scripts/ci/apple-libbox-artifact.sh install "$validated" "$ios_client_dir"
./scripts/ci/apple-libbox-artifact.sh install "$validated" "$macos_client_dir"
./scripts/ci/check-apple-shared-libbox.sh "$ios_client_dir" "$macos_client_dir"

# ---------------------------------------------------------------------------
# 6. hand off to the existing release pipeline
# ---------------------------------------------------------------------------
step "sign and upload"
echo "  Libbox is installed and verified; the pipeline below will not rebuild it."
echo

# The build number is derived HERE, once, for two reasons.
#
# It is what keeps iOS and macOS on the same number: release-apple.sh only derives one when the
# caller left it unset, and both builders then read the same exported value.
#
# It is also what lets the summary below report the real number. When release-apple.sh generated it
# in its own process, the value never reached this shell, so the summary printed a placeholder for
# every default run. An explicit APPLE_BUILD_NUMBER from the caller still wins.
if [ -z "${APPLE_BUILD_NUMBER:-}" ]; then
  export APPLE_BUILD_NUMBER="$(date -u +%Y%m%d%H%M)"
fi

# APPLE_USE_PREBUILT_LIBBOX is what keeps the local run from compiling a fourth copy. release-apple.sh
# owns signing, the overlays, archiving, export and upload; nothing about that path is duplicated here.
APPLE_USE_PREBUILT_LIBBOX=1 ./scripts/release-apple.sh testflight

step "APPLE BETA RELEASE"
# The summary states the ACTUAL source. It is computed from `use_canonical` - the same variable
# that selected the path - and never typed into the heredoc, so a run cannot report a source it
# did not use. An operator reading a published build's log has to be able to tell whether these
# bytes came off the release page or out of an Actions run, and "Libbox: PASS" answers neither.
if [ "$use_canonical" = "1" ]; then
  libbox_source_line="canonical Release asset (release $release_tag, asset $libbox_asset)"
  libbox_ci_line="not consulted - the canonical asset was used, so no run was selected or downloaded"
else
  libbox_source_line="Actions run $run_id (exact head SHA $local_sha)"
  libbox_ci_line="run:                 $run_id
  libbox:              ${job_libbox:-PASS}
  ios GUI:             ${job_ios:-PASS}
  macOS GUI:           ${job_macos:-PASS}
  run head SHA:        $local_sha (exact match)
  Libbox artifact:     PASS"
fi

# What the gitlink did, said once and truthfully.
#
# This line used to be the literal text `gitlink match:     PASS`, printed unconditionally. In the
# v0.1.16 release the gitlink was 40dd487 and the build used 6ce67b19 - the summary asserted a
# match that did not exist, which is worse than saying nothing, because the operator reads this
# block to answer "did the right UI go out?".
if [ "$gitlink_sha" = "$IOS_APPLE_SHA" ]; then
  gitlink_line="matches the resolved source"
else
  gitlink_line="does NOT match - the resolved source is ahead. Not a failure: the client"
  gitlink_line="$gitlink_line
                        repository is the authority, the gitlink is the local default checkout"
fi

cat <<EOF
Source:
  commit:              $local_sha
  branch:              $branch
  version:             $JJ_VERSION

Apple sources (one repository, one source, two products):
  repository:          $APPLE_CLIENT_REPOSITORY

  iOS / SFI:
    branch:            $IOS_APPLE_BRANCH
    commit:            $IOS_APPLE_SHA
    working tree:      $IOS_APPLE_DIR

  macOS / SFM:
    branch:            $MACOS_APPLE_BRANCH
    commit:            $MACOS_APPLE_SHA
    working tree:      $MACOS_APPLE_DIR

  source of truth:     the Apple client repository, resolved when this run started
  shared source:       PASS (both products build the one commit, as they must)
  parent gitlink:      $gitlink_sha
  gitlink:             $gitlink_line

  The two working trees differ; the source commit does not. Which UI a device shows is
  decided by the Apple source at runtime, from the device idiom.

Libbox source:
  $libbox_source_line
  parent SHA match:    PASS (== $local_sha)
  release version:     PASS (== $jj_version)
  artifact SHA256:     $artifact_sha
  shared by both:      PASS (identical framework in both Apple checkouts)

GitHub Apple CI:
  $libbox_ci_line


Local:
  Libbox build:        SKIPPED (reused the verified artifact)
  signing style:       ${APPLE_SIGNING_STYLE:-automatic}
  build number:        ${APPLE_BUILD_NUMBER}

RESULT:
  TESTFLIGHT UPLOAD:   PASS (iOS + macOS, one App Store Connect record)

The archive, export and upload all completed successfully. App Store Connect
processes the build afterwards, on Apple's side; "uploaded" is not the same as
"available for testing". Check TestFlight for processing status.
EOF
