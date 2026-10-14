#!/usr/bin/env bash
# PRESENT_INVALID integration fixture for the canonical Libbox source selection.
#
# # What this suite exists to prove, and why "exit != 0" is not enough
#
# The rule under test is:
#
#   canonical Release asset PRESENT, any validation failure  ->  HARD FAIL, never fall back
#   canonical Release asset ABSENT                           ->  auto MAY fall back to Actions
#
# The cheap way to test the first half is to assert a non-zero exit. That test is worthless here.
# It also passes when the code falls back to Actions and the Actions path fails for its own
# reason - two different behaviours, one exit status. Worse, it passes when the fallback works and
# a LATER step fails, which is the masked failure the rule exists to prevent.
#
# So this suite proves a NEGATIVE: that a specific set of commands is never run.
#
# # The FALLBACK SENTINEL
#
# The mock `gh` treats every Actions command - `run list`, `run view`, `run download`,
# `workflow run` - as a SENTINEL: it records the call, creates the sentinel file, and exits
# non-zero. Each PRESENT+INVALID case then asserts three things:
#
#   1. the publish script failed, AND
#   2. it failed naming a CANONICAL reason (not an Actions reason), AND
#   3. the sentinel file DOES NOT EXIST - no fallback command was ever attempted
#
# (3) is the load-bearing one. (1) and (2) alone would still pass if the code reached the fallback
# after printing the canonical error, which is exactly the shape of a masked failure.
#
# The ABSENT cases assert the mirror image: the sentinel DOES exist, proving absence really does
# fall back. Without that control a mock that never recorded anything would make every "no
# fallback" assertion pass vacuously - the classic way a negative test proves nothing.
set -uo pipefail

# ============================================================================
# STATUS: INCOMPLETE - the cases do not yet reach the code under test.
#
# This harness runs, builds a real fixture repository, packs a real bundle with the producer's own
# packer, and installs a mock `gh` that treats Actions access as a sentinel. Every case then runs
# `publish-apple-beta.sh` - and the script stops in its own preflight with
#
#     == repository ==
#     fatal: not a git repository (or any of the parent directories): .git
#
# from `git rev-parse HEAD` at the top of the publish script, before any canonical-source logic is
# reached. So no case in this file currently proves anything about PRESENT_INVALID.
#
# What was RULED OUT, with the probe that ruled it out (the probe is removed; this is the record):
#
#   * the fixture is not a repository          - `rev-parse` succeeded in it
#   * the mock PATH breaks `git`               - `rev-parse` succeeded WITH `PATH=$BIN:$PATH`
#   * the working directory is wrong           - it is the fixture root, which has `.git`
#   * the shim is missing or not executable    - present, executable, in `$BIN`
#
# The remaining difference between the probe's working invocation and the script's failing one is
# the `env` prefix used to pass the case's environment. That is where the next session starts.
#
# WHY THIS FILE IS COMMITTED ANYWAY. It is a working scaffold with one unresolved bug, not a
# passing test, and it is labelled as such rather than deleted: the sentinel design, the fixture
# repository, the golden bundle and the assertion set are the parts that took the thinking, and
# they are correct. Deleting it would mean rediscovering all of that. Presenting it as green would
# be worse - that is the failure this whole body of work exists to remove.
#
# DO NOT add this file to a workflow until it passes. A suite that fails for an unrelated reason
# on every run trains its readers to ignore it.
# ============================================================================

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

pass=0
failed=0
ok()  { pass=$((pass + 1)); printf 'PASS  %s\n' "$1"; }
bad() {
  failed=$((failed + 1))
  printf 'FAIL  %s\n' "$1"
  [ -n "${2:-}" ] && printf '      %s\n' "$2"
  return 0
}

# ---------------------------------------------------------------------------
# the fixture repository
# ---------------------------------------------------------------------------
#
# A real git repository: the publish script refuses a dirty tree, reads its HEAD, and resolves the
# tag through `git ls-remote` - which the fixture redirects to itself with a `git` shim.
REPO="$work/repo"
mkdir -p "$REPO"
(
  cd "$REPO" || exit 1
  git init -q .
  git config user.email fixture@example.invalid
  git config user.name fixture
  git config commit.gpgsign false
  mkdir -p scripts/ci release
  printf '0.1.7\n' > release/JIEJIE_VERSION
  printf 'placeholder\n' > README
  git add -A >/dev/null 2>&1
  git commit -q -m fixture >/dev/null 2>&1
) || { echo "FATAL: could not create the fixture repository" >&2; exit 1; }

cd "$REPO" || exit 1
# The publish script shells out to ./scripts/ci/... relative to the repository root, so this has to
# look like a checkout of this repository.
for f in lib.sh version.sh apple-libbox-artifact.sh check-apple-shared-libbox.sh apple-client-source.sh; do
  [ -f "$repo_root/scripts/ci/$f" ] && cp "$repo_root/scripts/ci/$f" "scripts/ci/$f"
done
cp "$repo_root/scripts/publish-apple-beta.sh" publish-apple-beta.sh

LIBBOX="$REPO/scripts/ci/apple-libbox-artifact.sh"

git add -A >/dev/null 2>&1
git -c commit.gpgsign=false commit -q -m fixture2 >/dev/null 2>&1

# FAIL LOUDLY HERE IF THE FIXTURE IS NOT A WORKING REPOSITORY.
#
# The first version of this harness continued past a broken setup, and the symptom was every case
# reporting "refused, but not for a canonical reason" with a git error inside it - which reads as a
# policy bug in the code under test rather than as a broken fixture. A harness that cannot build
# its own subject must say so and stop; every assertion it makes afterwards is about the wrong
# thing. This costs four lines and would have saved most of a debugging session.
REAL_SHA="$(git rev-parse HEAD 2>/dev/null)"
if [ -z "$REAL_SHA" ]; then
  echo "FATAL: the fixture directory $REPO is not a usable git repository." >&2
  echo "       git rev-parse HEAD failed there. Nothing below this line would test the code." >&2
  exit 1
fi
if [ ! -d "$REPO/.git" ]; then
  echo "FATAL: $REPO/.git does not exist, so the publish script's preflight cannot pass." >&2
  exit 1
fi
if [ -n "$(git status --porcelain)" ]; then
  echo "FATAL: the fixture repository is dirty, and the publish script refuses a dirty tree." >&2
  git status --short >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# a genuine bundle, packed by the PRODUCER'S OWN packer
# ---------------------------------------------------------------------------
#
# Packed once and copied per case, so each case mutates exactly one thing. Packing per case would
# make any failure ambiguous between the mutation and a packing difference.
SLICES="$work/slices"
mkdir -p "$SLICES/Libbox.xcframework/ios-arm64" "$SLICES/Libbox.xcframework/macos-arm64_x86_64"
printf 'ios\n'   > "$SLICES/Libbox.xcframework/ios-arm64/Info.plist"
printf 'macos\n' > "$SLICES/Libbox.xcframework/macos-arm64_x86_64/Info.plist"

GOLDEN="$work/golden"
mkdir -p "$GOLDEN"
if ! "$LIBBOX" pack "$SLICES/Libbox.xcframework" "$GOLDEN" >/dev/null 2>&1; then
  echo "FATAL: could not pack the golden fixture - the producer's packer is not working." >&2
  exit 1
fi
[ -f "$GOLDEN/manifest.json" ] || { echo "FATAL: the packer produced no manifest.json" >&2; exit 1; }

ASSET="jiejie-libbox-apple-0.1.7.tar.gz"
( cd "$GOLDEN" && tar -czf "$work/$ASSET" . ) || exit 1

# ---------------------------------------------------------------------------
# the mock gh and git
# ---------------------------------------------------------------------------
BIN="$work/bin"
mkdir -p "$BIN"

cat > "$BIN/gh" <<'GH'
#!/usr/bin/env bash
# Mock GitHub CLI. Every invocation is logged; Actions access is the sentinel.
printf '%s\n' "$*" >> "${MOCK_GH_LOG:?}"

if [ "$1" = "release" ] && [ "$2" = "view" ]; then
  [ "${MOCK_RELEASE_EXISTS:-1}" = "1" ] || { echo "release not found" >&2; exit 1; }
  printf '%s\n' "${MOCK_ASSET_NAME:?}"
  exit 0
fi

if [ "$1" = "release" ] && [ "$2" = "download" ]; then
  [ "${MOCK_ASSET_FETCHABLE:-1}" = "1" ] || { echo "HTTP 500" >&2; exit 1; }
  dir=""; pattern=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --dir) dir="$2"; shift 2 ;;
      --pattern) pattern="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  [ -n "$dir" ] || exit 1
  mkdir -p "$dir" || exit 1
  cp "${MOCK_ASSET_FILE:?}" "$dir/$pattern" || exit 1
  exit 0
fi

case "$1 $2" in
  "run list"|"run view"|"run download"|"workflow run")
    # THE SENTINEL. Reaching any of these means the canonical path fell back to Actions.
    printf '%s\n' "$*" >> "${MOCK_FALLBACK_SENTINEL:?}"
    echo "mock-gh: refusing Actions access" >&2
    exit 1
    ;;
esac
exit 0
GH

cat > "$BIN/git" <<'GIT'
#!/usr/bin/env bash
# Intercepts only `ls-remote`, so tag resolution is decided by the fixture. Everything else is the
# real git, found by removing this directory from PATH.
#
# # Why the PATH filter is written this way
#
# `for entry in $PATH` with `IFS=:` does NOT split - bash word-splits unquoted EXPANSIONS, not a
# bare variable reference - so that loop saw one element, the comparison never matched, this
# directory stayed on PATH, and `exec git` re-entered this same script. Every caller then reported
# `fatal: not a git repository`, which points nowhere near the cause.
#
# `tr` + `grep` + `while read` splits reliably and needs no external joiner. `paste` is not on
# every host, and requiring it would make this shim fail on the very machines it exists to make
# testable.
if [ "$1" = "ls-remote" ]; then
  [ "${MOCK_TAG_EXISTS:-1}" = "1" ] || exit 0
  printf '%s\trefs/tags/%s\n' "${MOCK_TAG_SHA:?}" "${MOCK_TAG:?}"
  exit 0
fi

real_path=""
while IFS= read -r entry; do
  [ -z "$entry" ] && continue
  [ "$entry" = "$MOCK_BIN" ] && continue
  real_path="${real_path:+$real_path:}$entry"
done < <(printf '%s' "$PATH" | tr ':' '\n' | grep -vxF "$MOCK_BIN")
PATH="$real_path"
export PATH
exec git "$@"
GIT

chmod +x "$BIN/gh" "$BIN/git" 2>/dev/null || true

# ---------------------------------------------------------------------------
# run_case <name> <expect> [--] <mutator> [-- <script args...>] [--env K=V ...]
# ---------------------------------------------------------------------------
#
#   expect = invalid   canonical asset PRESENT, must be refused with no fallback
#   expect = absent    canonical asset ABSENT, auto MUST reach the fallback (the control)
#   expect = release   --libbox-source release with no asset, must fail with no fallback
run_case() {
  local name="$1" expect="$2"; shift 2
  [ "${1:-}" = "--" ] && shift
  local mutator="$1"; shift
  [ "${1:-}" = "--" ] && shift

  local -a script_args=() envs=()
  while [ $# -gt 0 ]; do
    # The FIRST `--` ends the script arguments and starts the environment assignments. A `--` is
    # never a legitimate publish argument, so this is unambiguous.
    if [ "$1" = "--" ]; then shift; envs=("$@"); break; fi
    script_args+=("$1"); shift
  done

  local case_dir="$work/case-$name"
  rm -rf "$case_dir"; mkdir -p "$case_dir"

  local asset="$case_dir/$ASSET"
  cp "$work/$ASSET" "$asset" || { bad "$name" "could not stage the asset"; return 0; }
  mkdir -p "$case_dir/unpack"
  tar -xzf "$asset" -C "$case_dir/unpack" >/dev/null 2>&1 || true
  if [ -n "$mutator" ]; then
    ( cd "$case_dir/unpack" && eval "$mutator" ) >/dev/null 2>&1 || true
    ( cd "$case_dir/unpack" && tar -czf "$asset" . ) >/dev/null 2>&1 || true
  fi

  local log="$case_dir/gh.log" sentinel="$case_dir/FALLBACK"
  : > "$log"

  local -a base=(
    "PATH=$BIN:$PATH"
    "MOCK_BIN=$BIN"
    "MOCK_GH_LOG=$log"
    "MOCK_FALLBACK_SENTINEL=$sentinel"
    "MOCK_ASSET_FILE=$asset"
    "MOCK_ASSET_NAME=$ASSET"
    "MOCK_TAG=v0.1.7"
    "MOCK_TAG_SHA=$REAL_SHA"
    "GH_TOKEN=fixture"
  )

  # ONE argv array. Concatenating two array expansions into a single command works in bash but
  # reads as if it might not, and the failure mode when it does not is that an assignment is
  # passed to the script as an argument - which is exactly what the first run of this suite
  # reported, as "unknown argument 'MOCK_ASSET_FETCHABLE=0'".
  local -a argv=(env)
  argv+=("${base[@]}")
  if [ "${#envs[@]}" -gt 0 ]; then argv+=("${envs[@]}"); fi
  argv+=(./publish-apple-beta.sh --no-dispatch)
  if [ "${#script_args[@]}" -gt 0 ]; then argv+=("${script_args[@]}"); fi

  local out rc=0
  out="$("${argv[@]}" 2>&1)" || rc=$?

  local fell_back=0
  [ -f "$sentinel" ] && fell_back=1
  local why
  # The HEAD of the output, not the tail: the reason a case failed is the FIRST thing the
  # script says, and the tail is whatever unrelated complaint it reached afterwards.
  why="$(printf '%s' "$out" | tr '\n' ' ' | head -c 500)"


  case "$expect" in
    invalid)
      if [ "$rc" -eq 0 ]; then
        bad "$name" "expected a refusal, but the script SUCCEEDED. output: $why"
        return 0
      fi
      if [ "$fell_back" -ne 0 ]; then
        bad "$name" "ACTIONS FALLBACK WAS REACHED despite a PRESENT canonical asset - sentinel: $(tr '\n' ' ' < "$sentinel")"
        return 0
      fi
      case "$out" in
        *PRESENT_INVALID*|*canonical*|*Libbox*|*manifest*|*jj_version*|*checksum*|*slices*|*slice*|*"readable tar"*)
          ok "$name" ;;
        *)
          bad "$name" "refused, but not for a canonical reason. output: $why" ;;
      esac
      ;;
    absent)
      if [ "$fell_back" -ne 1 ]; then
        bad "$name" "absence did NOT reach the Actions fallback. The auto path is broken, which makes every 'no fallback' assertion in this suite vacuous. output: $why"
        return 0
      fi
      if ! grep -q 'run list' "$log"; then
        bad "$name" "a fallback ran but no run was looked up: $(tr '\n' ' ' < "$log")"
        return 0
      fi
      case "$(cat "$log")" in
        *client-apple.yml*) ok "$name" ;;
        *) bad "$name" "the fallback did not target client-apple.yml: $(tr '\n' ' ' < "$log")" ;;
      esac
      ;;
    release)
      if [ "$rc" -eq 0 ]; then
        bad "$name" "expected a refusal: no canonical asset, and release-only was requested"
        return 0
      fi
      if [ "$fell_back" -ne 0 ]; then
        bad "$name" "release-only mode fell back to Actions: $(tr '\n' ' ' < "$sentinel")"
        return 0
      fi
      ok "$name"
      ;;
  esac
}

echo
echo "== canonical PRESENT + INVALID: every failure must be fatal, none may fall back =="

# A manifest that is internally consistent but describes another commit.
run_case wrong_parent_sha invalid '
python3 -c "
import json
p=\"manifest.json\"
d=json.load(open(p))
d[\"parent_sha\"]=\"2\"*40
json.dump(d,open(p,\"w\"),indent=2)
"
'

# The artifact declares a different release.
run_case wrong_jj_version invalid '
python3 -c "
import json
p=\"manifest.json\"
d=json.load(open(p))
d[\"jj_version\"]=\"9.9.9\"
json.dump(d,open(p,\"w\"),indent=2)
"
'

# artifact_sha256 no longer describes the bundle. The substituted-transfer case, and the one a
# download-success check would miss entirely.
run_case digest_mismatch invalid '
python3 -c "
import json
p=\"manifest.json\"
d=json.load(open(p))
d[\"artifact_sha256\"]=\"0\"*64
json.dump(d,open(p,\"w\"),indent=2)
"
'

# The bundle is not a tarball at all, and the manifest honestly hashes what is there - so this
# exercises the extraction path rather than the digest path.
run_case corrupt_archive invalid '
rm -f Libbox.xcframework.tar.gz
printf "this is not a gzip stream\n" > Libbox.xcframework.tar.gz
python3 -c "
import hashlib,json
p=\"manifest.json\"
d=json.load(open(p))
d[\"artifact_sha256\"]=hashlib.sha256(open(\"Libbox.xcframework.tar.gz\",\"rb\").read()).hexdigest()
json.dump(d,open(p,\"w\"),indent=2)
"
'

# The archive carries one slice; the manifest declares two. A declaration never compared to the
# bytes would pass this.
run_case invalid_slices invalid '
rm -rf Libbox.xcframework/macos-arm64_x86_64
tar -czf Libbox.xcframework.tar.gz Libbox.xcframework
python3 -c "
import hashlib,json
p=\"manifest.json\"
d=json.load(open(p))
d[\"artifact_sha256\"]=hashlib.sha256(open(\"Libbox.xcframework.tar.gz\",\"rb\").read()).hexdigest()
json.dump(d,open(p,\"w\"),indent=2)
"
'

# The mirror: the archive carries a slice the manifest never declared, so the manifest is not
# describing these bytes.
run_case undeclared_slice invalid '
mkdir -p Libbox.xcframework/tvos-arm64
printf "tv\n" > Libbox.xcframework/tvos-arm64/Info.plist
tar -czf Libbox.xcframework.tar.gz Libbox.xcframework
python3 -c "
import hashlib,json
p=\"manifest.json\"
d=json.load(open(p))
d[\"artifact_sha256\"]=hashlib.sha256(open(\"Libbox.xcframework.tar.gz\",\"rb\").read()).hexdigest()
json.dump(d,open(p,\"w\"),indent=2)
"
'

# A schema-2 manifest with the slices field removed. The consumer must REQUIRE the field rather
# than read its absence as "nothing to check".
run_case missing_slices_field invalid '
python3 -c "
import json
p=\"manifest.json\"
d=json.load(open(p))
d.pop(\"slices\",None)
json.dump(d,open(p,\"w\"),indent=2)
"
'

# A legacy manifest: no schema_version, no jj_version. The canonical path requires schema 2 and
# must not infer the version from the composed `version` string.
run_case legacy_manifest invalid '
python3 -c "
import json
p=\"manifest.json\"
d=json.load(open(p))
d.pop(\"schema_version\",None)
d.pop(\"jj_version\",None)
json.dump(d,open(p,\"w\"),indent=2)
"
'

# Listed, but the download fails. PRESENT and unfetchable is INVALID, never ABSENT - this is the
# case the V8 report recorded as untested.
run_case present_unfetchable invalid '' -- MOCK_ASSET_FETCHABLE=0

echo
echo "== canonical ABSENT: the control, which proves the sentinel can fire at all =="

run_case absent_auto_falls_back absent '' -- MOCK_ASSET_NAME=some-other-asset.tar.gz
run_case absent_no_release absent '' -- MOCK_RELEASE_EXISTS=0
run_case absent_no_tag absent '' -- MOCK_TAG_EXISTS=0

echo
echo "== --libbox-source release: no asset means no publication, and no fallback =="

run_case release_only_no_asset release -- --libbox-source release -- MOCK_ASSET_NAME=some-other-asset.tar.gz
run_case release_mode_no_asset release -- --libbox-source release -- MOCK_ASSET_NAME=some-other-asset.tar.gz MOCK_RELEASE_EXISTS=0

echo
if [ "$failed" -eq 0 ]; then
  printf 'PASS: all %s canonical-source cases behaved as required\n' "$pass"
  exit 0
fi
printf 'FAIL: %s of %s cases did not behave as required\n' "$failed" "$((pass + failed))" >&2
exit 1
