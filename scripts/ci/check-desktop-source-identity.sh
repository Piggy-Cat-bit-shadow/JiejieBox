#!/usr/bin/env bash
# Proves the desktop release build's SOURCE IDENTITY before anything is built.
#
# Usage:
#   check-desktop-source-identity.sh \
#     --core-dir <dir> --tooling-dir <dir> \
#     --version <X.Y.Z> --core-sha <40hex> --tooling-sha <40hex> \
#     [--remote origin] [--no-fetch] [--shallow] [--audit-only]
#
# Exit: 0 = every identity holds, 1 = one does not, 2 = the caller used it wrongly.
#
# # Two modes, and the difference is exactly one requirement
#
# RELEASE (default): `refs/tags/v<version>` must exist and name `core_sha`. A commit is a
# release only when it is labelled as one.
#
# AUDIT (`--audit-only`): the tag requirement is DROPPED; everything else - exact core_sha,
# exact tooling_sha, tooling == core, release/JIEJIE_VERSION agreement - is still enforced.
# This is the diagnostic path, for auditing a commit that is not the release. It is what
# stops the audit from depending on the release decision it exists to inform; see the long
# note at the tag block. A caller that selects this mode must announce it in its own record,
# and no release may consume its artifacts.
#
# # Why this is a script and not an inline `run:` block
#
# It was an inline block in client-desktop-windows.yml, which made the strongest checks in
# the release path the only ones that could not be exercised except by dispatching a
# 120-minute Windows build. Every negative case below - a floating tooling ref, a tooling
# revision that is not the core's, a tag that names another commit, a version file that
# disagrees with the input - is now proved locally in seconds by
# `test-desktop-source-identity.sh`. The workflow calls this; it does not re-implement it.
#
# # One identity, not two
#
# A release candidate carries the tooling that builds and judges it, so the tooling revision
# IS the core revision: `TOOLING_SHA == CORE_SHA` is required, not merely allowed. The old
# model checked the tooling out at a floating branch (`testing`), which meant a dispatch
# could build one commit's product with another commit's scripts and record only the first.
# The equality is asserted on the VALUES, before either directory is read, so a caller cannot
# satisfy this by checking out the same commit under two names while the inputs disagree.
#
# `--tooling-dir` is still a separate directory rather than being dropped, because the
# workflow needs a checkout that can fetch `refs/tags/v<version>` without disturbing the
# core's own worktree. With the equality enforced, the two directories are the same commit,
# so the separation costs one checkout and buys no second identity.
#
# # Why the tag is fetched here
#
# The tag has to exist AND name this commit. Without that, a caller could name a version whose
# tag points somewhere else, and every later gate would still pass because they all compare
# against the inputs this script just accepted. The fetch is the only step that needs the
# network; `--no-fetch` exists so the fixtures in the test suite can assert the rest.
set -euo pipefail

usage="usage: check-desktop-source-identity.sh --core-dir <dir> --tooling-dir <dir> --version <X.Y.Z> --core-sha <40hex> --tooling-sha <40hex> [--remote origin] [--no-fetch] [--shallow] [--audit-only]"

core_dir=""
tooling_dir=""
version=""
core_sha=""
tooling_sha=""
remote="origin"
fetch=1
shallow=""
audit_only=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    --core-dir)     [ "$#" -ge 2 ] || { echo "FAIL: --core-dir needs a value. $usage" >&2; exit 2; }; core_dir="$2"; shift 2 ;;
    --tooling-dir)  [ "$#" -ge 2 ] || { echo "FAIL: --tooling-dir needs a value. $usage" >&2; exit 2; }; tooling_dir="$2"; shift 2 ;;
    --version)      [ "$#" -ge 2 ] || { echo "FAIL: --version needs a value. $usage" >&2; exit 2; }; version="$2"; shift 2 ;;
    --core-sha)     [ "$#" -ge 2 ] || { echo "FAIL: --core-sha needs a value. $usage" >&2; exit 2; }; core_sha="$2"; shift 2 ;;
    --tooling-sha)  [ "$#" -ge 2 ] || { echo "FAIL: --tooling-sha needs a value. $usage" >&2; exit 2; }; tooling_sha="$2"; shift 2 ;;
    --remote)       [ "$#" -ge 2 ] || { echo "FAIL: --remote needs a value. $usage" >&2; exit 2; }; remote="$2"; shift 2 ;;
    --no-fetch)     fetch=0; shift ;;
    --shallow)      shallow="--depth 1"; shift ;;
    --audit-only)   audit_only=1; shift ;;
    -h|--help)      echo "$usage" >&2; exit 0 ;;
    *)              echo "FAIL: unknown argument '$1'. $usage" >&2; exit 2 ;;
  esac
done

for pair in "core-dir:$core_dir" "tooling-dir:$tooling_dir" "version:$version" \
            "core-sha:$core_sha" "tooling-sha:$tooling_sha"; do
  [ -n "${pair#*:}" ] || { echo "FAIL: --${pair%%:*} is required. $usage" >&2; exit 2; }
done

is_sha() {
  case "$1" in
    *[!0-9a-f]*|"") return 1 ;;
  esac
  [ "${#1}" -eq 40 ]
}

# --- the inputs are the release identity, so they are validated first --------
is_sha "$core_sha" || {
  echo "FAIL: core_sha must be 40 lowercase hex characters, got '$core_sha'" >&2; exit 1; }
is_sha "$tooling_sha" || {
  echo "FAIL: tooling_sha must be 40 lowercase hex characters, got '$tooling_sha'." >&2
  echo "      A branch name, a short SHA or a tag is not an identity: it resolves to" >&2
  echo "      whatever it points at when the build runs, and the build record then names" >&2
  echo "      a revision nobody can reconstruct." >&2
  exit 1; }

# The one-identity rule, asserted on the values rather than on the checkouts.
if [ "$tooling_sha" != "$core_sha" ]; then
  echo "FAIL: tooling_sha ($tooling_sha) is not core_sha ($core_sha)." >&2
  echo "      A release candidate carries the tooling that builds and judges it, so the" >&2
  echo "      two are the same revision. Building one commit's product with another" >&2
  echo "      commit's scripts is how a release is judged by rules it was not built under." >&2
  exit 1
fi

case "$version" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "FAIL: version '$version' is not a dotted numeric version" >&2; exit 1 ;;
esac

for d in "$core_dir" "$tooling_dir"; do
  git -C "$d" rev-parse --git-dir >/dev/null 2>&1 || {
    echo "FAIL: $d is not a git checkout" >&2; exit 1; }
done

# --- the core is the source of record ---------------------------------------
head_sha="$(git -C "$core_dir" rev-parse HEAD)"
echo "core HEAD:       $head_sha"
echo "core describe:   $(git -C "$core_dir" describe --tags 2>/dev/null || echo '<no tag>')"
[ "$head_sha" = "$core_sha" ] || {
  echo "FAIL: core is at $head_sha, expected $core_sha" >&2; exit 1; }

describe="$(git -C "$core_dir" describe --tags 2>/dev/null || true)"
# `git describe --tags` is what stamps the daemon, so on the RELEASE path it must name exactly
# `v$version`: a candidate is built at the commit the tag names.
#
# In AUDIT mode the commit being audited is expected to sit PAST the release tag - that is the
# whole point, since the fix under audit is newer than the last candidate. So the requirement
# becomes "describes the same release", which is exactly the rule
# scripts/ci/verify-desktop-daemon-provenance.py already applies to this same value:
#
#   describe == v<version>                  -> at the tag
#   describe == v<version>-<n>-g<sha>       -> a descendant of the tag
#   describe == <no tag> / v<other>         -> refused in BOTH modes
#
# The version the daemon actually reports is set by SING_BOX_BUILD_VERSION on the audit path (see
# build_shared.ReadTag, which consults it first), and it is asserted downstream by the runtime
# audit's `--expect-version`. What this check still refuses is a checkout that belongs to a
# DIFFERENT release, in either mode.
describe_ok=0
if [ "$describe" = "v$version" ]; then
  describe_ok=1
elif [ "$audit_only" -eq 1 ]; then
  case "$describe" in
    "v$version"-[0-9]*-g*) describe_ok=1 ;;
  esac
fi
[ "$describe_ok" -eq 1 ] || {
  echo "FAIL: the daemon version stamp derives from \`git describe --tags\`," >&2
  echo "      which must be v$version in this checkout, but is '${describe:-<none>}'" >&2
  if [ "$audit_only" -eq 1 ]; then
    echo "      (audit mode accepts 'v$version-<n>-g<sha>' as a descendant of the release tag," >&2
    echo "      but the checkout must still belong to the v$version release.)" >&2
  fi
  exit 1; }

# --- the tooling is the SAME revision, and it is a revision ------------------
tooling_head="$(git -C "$tooling_dir" rev-parse HEAD)"
echo "tooling HEAD:    $tooling_head"
[ "$tooling_head" = "$tooling_sha" ] || {
  echo "FAIL: tooling is at $tooling_head, expected $tooling_sha" >&2
  echo "      The tooling checkout did not land on the revision the inputs name." >&2
  exit 1; }
[ "$tooling_head" = "$core_sha" ] || {
  echo "FAIL: the tooling checkout is not the core revision" >&2; exit 1; }

# --- the tag must exist AND name this commit --------------------------------
#
# # Why the tag is a RELEASE requirement and not an AUDIT precondition
#
# Requiring `refs/tags/v<version>` to point at `core_sha` made the audit depend on the release
# decision it is supposed to inform. The consequence was a real inversion:
#
#     wanted:  fix a SHA -> audit it -> the audit passes -> THEN choose the candidate/tag
#     actual:  move the candidate tag first -> only now may the SHA be audited
#
# That is backwards. An audit is evidence about a commit, and a commit does not become auditable
# by being labelled. It also blocked this audit in practice: the lifecycle fix under test sat one
# commit past `v0.1.15`, whose tag belongs to a HELD candidate that must not be moved, so the only
# way to run the lifecycle was to move a published tag - which the release rules forbid.
#
# `--audit-only` is therefore the DIAGNOSTIC path: it drops the tag requirement and keeps every
# other identity check EXACT. `git describe` is what stamps the daemon, so in audit mode the
# version is injected explicitly (SING_BOX_BUILD_VERSION, read first by build_shared.ReadTag) and
# the record says so; see the AUDIT_ONLY line this prints at the end.
#
# The release path is unchanged and still requires the tag: without `--audit-only`, a tag that is
# missing, or that names another commit, fails exactly as before.
tag_ref="refs/tags/v$version"
if [ "$audit_only" -eq 1 ]; then
  echo "AUDIT_ONLY:       yes - this run is a DIAGNOSTIC AUDIT of $core_sha."
  echo "                  The v$version tag is NOT required to name this commit, and this"
  echo "                  result must never be cited as a release acceptance result."
  if tag_sha="$(git -C "$tooling_dir" rev-parse "$tag_ref^{commit}" 2>/dev/null)"; then
    echo "tag v$version:     $tag_sha (present; audit mode does not require it to be core_sha)"
  else
    echo "tag v$version:     absent (audit mode does not require it)"
  fi
else
  if [ "$fetch" -eq 1 ]; then
    if ! git -C "$tooling_dir" fetch $shallow "$remote" "+$tag_ref:$tag_ref" 2>/dev/null; then
      echo "FAIL: $tag_ref does not exist on $remote." >&2
      echo "      The release version names a tag that is not published, so this build" >&2
      echo "      would be the only record of it. Pass --audit-only to audit a commit that" >&2
      echo "      is not the release." >&2
      exit 1
    fi
  fi
  if ! tag_sha="$(git -C "$tooling_dir" rev-parse "$tag_ref^{commit}" 2>/dev/null)"; then
    echo "FAIL: $tag_ref does not resolve in the tooling checkout." >&2
    echo "      The tag must exist and name this commit; a version whose tag points" >&2
    echo "      elsewhere names a different release." >&2
    exit 1
  fi
  echo "tag v$version:     $tag_sha"
  [ "$tag_sha" = "$core_sha" ] || {
    echo "FAIL: $tag_ref is $tag_sha, but core_sha is $core_sha." >&2
    echo "      The version and the commit name different releases." >&2
    echo "      Pass --audit-only to audit a commit that is not the release." >&2; exit 1; }
fi

# --- and the checkout's own single version source must agree -----------------
core_version_file="$core_dir/release/JIEJIE_VERSION"
[ -f "$core_version_file" ] || { echo "FAIL: no $core_version_file" >&2; exit 1; }
declared="$(tr -d '[:space:]' < "$core_version_file")"
echo "JIEJIE_VERSION:  $declared"
[ "$declared" = "$version" ] || {
  echo "FAIL: $core_version_file says '$declared', but version is '$version'." >&2
  echo "      release/JIEJIE_VERSION is the single authority for the fork's version;" >&2
  echo "      a caller that disagrees with it is releasing something else." >&2; exit 1; }

if [ "$audit_only" -eq 1 ]; then
  echo "PASS: source identity holds at $core_sha (tooling = core, version agrees; AUDIT-ONLY, tag not required)"
else
  echo "PASS: source identity holds at $core_sha (tooling = core, tag names the commit, version agrees)"
fi
