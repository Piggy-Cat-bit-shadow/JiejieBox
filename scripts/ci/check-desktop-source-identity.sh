#!/usr/bin/env bash
# Proves the desktop release build's SOURCE IDENTITY before anything is built.
#
# Usage:
#   check-desktop-source-identity.sh \
#     --core-dir <dir> --tooling-dir <dir> \
#     --version <X.Y.Z> --core-sha <40hex> --tooling-sha <40hex> \
#     [--remote origin] [--no-fetch] [--shallow]
#
# Exit: 0 = every identity holds, 1 = one does not, 2 = the caller used it wrongly.
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

usage="usage: check-desktop-source-identity.sh --core-dir <dir> --tooling-dir <dir> --version <X.Y.Z> --core-sha <40hex> --tooling-sha <40hex> [--remote origin] [--no-fetch] [--shallow]"

core_dir=""
tooling_dir=""
version=""
core_sha=""
tooling_sha=""
remote="origin"
fetch=1
shallow=""

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
[ "$describe" = "v$version" ] || {
  echo "FAIL: the daemon version stamp derives from \`git describe --tags\`," >&2
  echo "      which must be v$version in this checkout, but is '${describe:-<none>}'" >&2
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
tag_ref="refs/tags/v$version"
if [ "$fetch" -eq 1 ]; then
  if ! git -C "$tooling_dir" fetch $shallow "$remote" "+$tag_ref:$tag_ref" 2>/dev/null; then
    echo "FAIL: $tag_ref does not exist on $remote." >&2
    echo "      The release version names a tag that is not published, so this build" >&2
    echo "      would be the only record of it." >&2
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
  echo "      The version and the commit name different releases." >&2; exit 1; }

# --- and the checkout's own single version source must agree -----------------
core_version_file="$core_dir/release/JIEJIE_VERSION"
[ -f "$core_version_file" ] || { echo "FAIL: no $core_version_file" >&2; exit 1; }
declared="$(tr -d '[:space:]' < "$core_version_file")"
echo "JIEJIE_VERSION:  $declared"
[ "$declared" = "$version" ] || {
  echo "FAIL: $core_version_file says '$declared', but version is '$version'." >&2
  echo "      release/JIEJIE_VERSION is the single authority for the fork's version;" >&2
  echo "      a caller that disagrees with it is releasing something else." >&2; exit 1; }

echo "PASS: source identity holds at $core_sha (tooling = core, tag names the commit, version agrees)"
