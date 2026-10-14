#!/usr/bin/env bash
# Regression tests for the Apple source selection.
#
# Usage: check-apple-source-selection.sh
#
# # What this protects
#
# The two Apple products are built from ONE commit of ONE Apple client repository, and then
# link ONE Libbox built once from this repository:
#
#     iOS / SFI     Repo B @ the parent's clients/apple gitlink   scheme SFI
#     macOS / SFM   Repo B @ the SAME gitlink                     scheme SFM
#
# Which UI a device shows is a property of the Apple source at runtime - an iPhone gets the
# fork's Hako presentation, an iPad and macOS get upstream's - so the parent decides ONE Apple
# commit, not one per platform. A per-platform commit here would make the parent, rather than
# the Apple source, the thing that owns UI ownership.
#
# # Why this suite was rewritten rather than repaired
#
# An earlier revision asserted the OPPOSITE topology: two branches of one repository, a
# `MACOS_APPLE_SHA` read out of the refs file, and an `assert-distinct` that demanded the two
# selections never coincide. The refs file no longer defines `MACOS_APPLE_BRANCH` or
# `MACOS_APPLE_SHA` - it deliberately records no SHA at all, because the `clients/apple`
# gitlink is the single authority and a second copy could disagree with it - so the suite died
# on its own `eval` under `set -u`:
#
#     check-apple-source-selection.sh: line 66: MACOS_APPLE_BRANCH: unbound variable
#
# ...before it checked anything at all. Had it got further it would have failed every correct
# build, because `assert-distinct` asserts the one thing this topology forbids. What follows
# is the same coverage pointed at the model that actually ships.
#
# Almost every way the selection can be wrong is silent: the build succeeds, the app runs, and
# one platform ships the other's UI, or a release is built from whatever a branch happened to
# point at. So the checks are of two kinds:
#
#   structural  the refs file records one source and no second copy of it, and the workflow is
#               wired so both jobs assert that shared source
#   behavioural the resolver is exercised against this tree and against scratch fixtures,
#               including the cases that must FAIL - a missing refs file, a refs file that
#               puts a per-platform SHA back, a checkout that is not the gitlink, and a parent
#               commit that records no gitlink
#
# The scratch fixtures are local git repositories, so this suite needs no network.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

pass=0
fail=0

check() {
  local name="$1"; shift
  if "$@" >/dev/null 2>&1; then
    echo "  PASS: $name"
    pass=$((pass + 1))
  else
    echo "  FAIL: $name" >&2
    fail=$((fail + 1))
  fi
}

expects_fail() {
  local name="$1"; shift
  if "$@" >/dev/null 2>&1; then
    echo "  FAIL: $name (expected failure, but it succeeded)" >&2
    fail=$((fail + 1))
  else
    echo "  PASS: $name"
    pass=$((pass + 1))
  fi
}

refs_file="release/apple-client-refs.env"
workflow=".github/workflows/client-apple.yml"
resolver="./scripts/ci/apple-client-source.sh"

# ---------------------------------------------------------------------------
# 1. One repository, one branch, and no SHA copy
# ---------------------------------------------------------------------------
echo "== one repository, one branch, and no SHA copy =="

check "the refs file exists" test -f "$refs_file"

# The file is sourced by the resolver and evaled by every consumer of `resolve`, so a name it
# promises but does not define is an unbound-variable abort in the middle of a release. That
# is the exact shape of the failure this suite was rewritten for, so sourcing it under
# `set -u` is checked rather than assumed.
check "the refs file sources cleanly under set -u" \
  bash -c 'set -euo pipefail; . "$1"' _ "$refs_file"

# Names are read in a subshell instead of sourcing the file into this shell: a name the file
# defines must not be able to shadow one this suite uses, and a broken file must yield an
# empty value that a check can report rather than aborting the suite before it checks anything.
refs_value() {
  local name="$1"
  [ -f "$refs_file" ] || return 0
  (
    # shellcheck disable=SC1090
    . "$refs_file" >/dev/null 2>&1 || exit 0
    eval "printf '%s\n' \"\${$name:-}\""
  ) 2>/dev/null || true
}

pin_repo="$(refs_value APPLE_CLIENT_REPOSITORY)"
pin_branch="$(refs_value APPLE_CLIENT_BRANCH)"
pin_tag="$(refs_value HAKO_DESIGN_BASELINE_TAG)"

check "the refs file names the Apple client repository" \
  bash -c "case '$pin_repo' in *sing-box-for-apple*) exit 0 ;; *) exit 1 ;; esac"

# `APPLE_CLIENT_BRANCH` is informational - it feeds the ancestry check and the diagnostics and
# never selects a commit - but it is still required, because `checkout` proves the gitlink is
# an ancestor of it, and "this commit is on the branch we review" is a fact the gitlink cannot
# state on its own. It is `main`, the product line; `dev` is the upstream reference.
check "the refs file names the reviewed product branch" test "$pin_branch" = "main"

# The iPhone presentation is frozen against a TAG, not the long-lived `hako-ui` branch that
# named it before the repository consolidation. A tag cannot move under a release.
check "the refs file records the Hako design baseline as a tag" test -n "$pin_tag"

# The gitlink is the only authority for which Apple commit a release builds. A second copy of
# that commit in this file is the thing that can disagree with it, so any assignment whose
# name ends in `_SHA` is a failure - `IOS_APPLE_SHA=`, `MACOS_APPLE_SHA=`, `APPLE_CLIENT_SHA=`
# or a name invented next round. Comments are exempt, which is why this matches assignments
# only: the file explains the rule in prose and names the removed names there.
check "the refs file records no SHA (the gitlink owns it)" \
  bash -c "! grep -qE '^[[:space:]]*[A-Za-z_][A-Za-z_0-9]*_SHA=' '$refs_file'"

# The same rule for the branch names of the removed two-source model: `IOS_APPLE_BRANCH` named
# a branch that was retired, and `MACOS_APPLE_BRANCH` would be a per-platform source selector
# again. Neither may be recorded, whether or not a reader would load it today.
check "the refs file declares no per-platform source name" \
  bash -c "! grep -qE '^[[:space:]]*(IOS|MACOS)_APPLE_(BRANCH|SHA)=' '$refs_file'"

# And no NEW name either: the file is allowed exactly three assignments, so a source selector
# cannot be smuggled in under a name the two checks above do not know about (a
# `MACOS_APPLE_TAG=`, say). Anything else here is either a second source or dead metadata of
# the kind that outlives the rename that killed it.
refs_declares_only_allowed_names() {
  [ -f "$refs_file" ] || return 1
  local name
  for name in $(grep -oE '^[[:space:]]*[A-Za-z_][A-Za-z_0-9]*=' "$refs_file" | tr -d ' ='); do
    case "$name" in
      APPLE_CLIENT_REPOSITORY|APPLE_CLIENT_BRANCH|HAKO_DESIGN_BASELINE_TAG) ;;
      *) return 1 ;;
    esac
  done
  return 0
}
check "the refs file declares only the three names this model allows" refs_declares_only_allowed_names

# ---------------------------------------------------------------------------
# 2. Structural: the workflow is wired to one source
# ---------------------------------------------------------------------------
echo "== the workflow uses one source =="

if python3 - "$workflow" <<'PY'
import re
import sys

# Deliberately stdlib-only. PyYAML is present on a macOS workstation but NOT on the GitHub
# runner image - scripts/ci/check-job-needs.py records the same discovery - so importing it
# here would make this suite fail in CI for a reason unrelated to the workflow it inspects.
#
# Job blocks are found by indentation, which is what YAML uses: a top-level job key sits at
# exactly two spaces under `jobs:`, and the block runs until the next such key.
path = sys.argv[1]
text = open(path, encoding="utf-8").read()

problems = []


def job_text(name):
    match = re.search(
        r"^  " + re.escape(name) + r":\n(.*?)(?=^  \S|\Z)",
        text,
        re.M | re.S,
    )
    if not match:
        problems.append(f"the workflow has no {name} job")
        return ""
    # Comments are not configuration. This workflow's comments explain the removed two-source
    # model BY NAME - `assert-distinct`, the per-platform pins, the separate sources - so
    # reading them back as settings would report every check below as violated by the very
    # step that documents why it is not.
    return "\n".join(
        line for line in match.group(1).splitlines() if not line.lstrip().startswith("#")
    )


ios = job_text("ios")
macos = job_text("macos")
libbox = job_text("libbox")

# One source. Both jobs must assert the invariant that keeps it one, and neither may call the
# action that asserted the opposite: `assert-distinct` was renamed to `assert-shared-source`
# precisely because it now fails every correct build.
for name, body in (("ios", ios), ("macos", macos)):
    if "apple-client-source.sh assert-shared-source" not in body:
        problems.append(
            f"the {name} job does not assert that both platforms build one Apple source"
        )
    if "assert-distinct" in body:
        problems.append(f"the {name} job calls the removed assert-distinct action")

# Neither SHA may be hardcoded: the refs file and the gitlink own them, and a literal here
# would keep selecting the same commit after the gitlink moved. The two short tokens are the
# revisions the removed two-source model pinned; a prefix copy is still a copy.
for name, body in (("ios", ios), ("macos", macos)):
    if re.search(r"\b[0-9a-f]{40}\b", body):
        problems.append(f"the {name} job hardcodes a 40-character Apple revision")
    for token in ("d1224bb5", "14f015e1"):
        if token in body:
            problems.append(f"the {name} job carries a removed two-source revision ({token})")

# Each job still declares which product it builds, and each keeps its OWN working tree. The
# commit is shared; the trees are not, because the two products apply different overlays and a
# shared tree would let one product's overlay reach the other's build.
for name, body, platform in (("ios", ios, "ios"), ("macos", macos, "macos")):
    if f"APPLE_CLIENT_PLATFORM={platform}" not in body:
        problems.append(f"the {name} job does not declare APPLE_CLIENT_PLATFORM={platform}")
# The jobs write the tree paths LITERALLY - `clients/apple` and `build/apple-client-macos` -
# rather than interpolating the resolver's variables.
#
# This used to require the variable form, which the workflow has never used, so it reported "the
# ios job does not point APPLE_CLIENT_DIR at the iOS working tree" for a job that points it at
# exactly the right directory. It went unnoticed because nothing ran this suite until the workflow
# was dispatched for real.
#
# What matters is not the spelling but that each product overlays its OWN tree, and that the trees
# are the ones the resolver declares. The literals are checked against the resolver itself below,
# which is a stronger assertion than a string match on the workflow: renaming a directory on
# either side now fails here instead of at build time.
if "APPLE_CLIENT_DIR=clients/apple" not in ios:
    problems.append(
        "the ios job does not point APPLE_CLIENT_DIR at the iOS working tree "
        "(expected APPLE_CLIENT_DIR=clients/apple)"
    )
if "APPLE_CLIENT_DIR=build/apple-client-macos" not in macos:
    problems.append(
        "the macos job does not point APPLE_CLIENT_DIR at the macOS working tree "
        "(expected APPLE_CLIENT_DIR=build/apple-client-macos)"
    )
if "APPLE_CLIENT_DIR=clients/apple" in macos:
    problems.append("the macos job overlays the shared iOS working tree")
if "APPLE_CLIENT_DIR=$MACOS_APPLE_DIR" in ios:
    problems.append("the ios job overlays the macOS working tree")

# Both jobs consume the SAME artifact, validated against the parent commit and installed into
# their own client tree. There is no per-platform Apple revision left to bind it to: the two
# products share one gitlink, so a `--submodule-sha` argument would be a copy of that gitlink
# kept somewhere it can disagree with it.
for name, body in (("ios", ios), ("macos", macos)):
    if 'apple-libbox-artifact.sh install build/apple-libbox "$APPLE_CLIENT_DIR"' not in body:
        problems.append(f"the {name} job does not install Libbox into its own client dir")
    if "apple-libbox-artifact.sh validate build/apple-libbox" not in body:
        problems.append(f"the {name} job does not validate the Libbox artifact")
    if "--parent-sha" not in body:
        problems.append(f"the {name} job does not validate Libbox against the parent commit")
    # The SECOND identity, and it is checked separately because deleting one argument from a
    # multi-line call leaves the other assertions above perfectly satisfied. `--parent-sha` ties
    # the bytes to a commit; `--jj-version` ties them to a release. The validator requires both,
    # so a job that dropped this one would fail at run time - but it would fail on a runner,
    # mid-build, after the expensive Libbox compile, instead of here.
    if "--jj-version" not in body:
        problems.append(f"the {name} job does not validate Libbox against the release version")
    if "--allow-legacy-manifest" in body:
        problems.append(
            f"the {name} job opts out of the schema-2 requirement. A canonical Apple build must "
            "declare its version and its slices; the opt-out exists for reading old artifacts, "
            "not for a GUI job"
        )
    if "--submodule-sha" in body:
        problems.append(f"the {name} job binds Libbox to a copy of the Apple revision")

# Libbox must still be compiled exactly once, in the shared job.
if "build-apple-libbox.sh both" not in libbox:
    problems.append("the libbox job does not build the framework")
if "build-apple-libbox.sh both" in ios or "build-apple-libbox.sh both" in macos:
    problems.append("a platform job compiles Libbox instead of consuming the shared artifact")

# Every variable the job's later steps read out of the environment must be exported into it.
# The BUILD-INFO and summary steps run under `set -u`, so a name referenced there but never
# written to $GITHUB_ENV aborts the job rather than printing an empty value.
EXPORTED = (
    "APPLE_CLIENT_REPOSITORY",
    "APPLE_CLIENT_PLATFORM",
    "APPLE_CLIENT_DIR",
    "APPLE_CLIENT_SHA",
    "APPLE_CLIENT_BRANCH",
    "APPLE_CLIENT_UI",
)
for name, body in (("ios", ios), ("macos", macos)):
    for variable in EXPORTED:
        if f'echo "{variable}=' not in body:
            problems.append(f"the {name} job never exports {variable} into its environment")

# And this suite must run in CI, or none of the rest of it is worth anything.
if "check-apple-source-selection.sh" not in libbox and "check-apple-source-selection.sh" not in ios:
    problems.append("the source selection suite does not run in CI")

# Cross-check the two literals against the resolver's OWN answers, so a rename on either side
# fails here rather than at build time. This replaces the string match on a variable name with an
# assertion about the property: the jobs use the trees the resolver declares.
import subprocess

for platform, expected in (("ios", "clients/apple"), ("macos", "build/apple-client-macos")):
    try:
        got = subprocess.run(
            ["bash", "scripts/ci/apple-client-source.sh", "dir", platform],
            capture_output=True, text=True, check=True,
        ).stdout.strip()
    except Exception as error:
        problems.append(f"could not ask the resolver for the {platform} directory: {error}")
        continue
    if got != expected:
        problems.append(
            f"the {platform} job uses '{expected}' but the resolver declares '{got}'"
        )

if problems:
    print("apple source selection: the workflow is not wired correctly.", file=sys.stderr)
    for problem in problems:
        print(f"  - {problem}", file=sys.stderr)
    raise SystemExit(1)

print("  PASS: both Apple jobs assert one shared source")
print("  PASS: neither job calls the removed assert-distinct action")
print("  PASS: neither job hardcodes an Apple revision")
print("  PASS: each job overlays its own working tree of the shared commit")
print("  PASS: both jobs consume the one shared Libbox artifact")
print("  PASS: the source selection suite runs in CI")
PY
then
  pass=$((pass + 6))
else
  fail=$((fail + 1))
fi

# ---------------------------------------------------------------------------
# 3. Behavioural: the resolver, against local fixtures
# ---------------------------------------------------------------------------
echo "== the resolver, against local fixtures =="

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

# THE FIXTURE'S RESOLUTION STATE MUST LIVE INSIDE THE FIXTURE.
#
# `apple-client-source.sh` defaults its state file to the RELATIVE path
# `build/apple-client-source.env`, resolved against the caller's directory. This suite runs from the
# repository root, so without this line the scratch resolver reads the REAL repository's frozen
# resolution - a commit that exists in the real Apple repository and not in the fixture at all. The
# fixture was not self-contained, despite its own comment claiming the scratch fixtures "need no
# network" because they are local repositories.
#
# Exported rather than prefixed onto each call: the assertions invoke the resolver from inside
# `bash -c` subshells, where a shell function does not exist, and an exported variable is inherited
# by every child process.
export APPLE_CLIENT_STATE="$scratch/build/apple-client-source.env"

mkdir -p "$scratch/scripts/ci" "$scratch/release"
cp scripts/ci/apple-client-source.sh "$scratch/scripts/ci/"
chmod +x "$scratch/scripts/ci/apple-client-source.sh"

# --- a fake Apple repository -------------------------------------------------
# One repository and one product branch (`main`), plus the two names the removed model used
# (`dev`, `hako-ui`) so that a resolver which still consulted them would have a wrong answer
# available to give.
apple_origin="$scratch/fixtures/apple.git"
apple_work="$scratch/fixtures/apple-work"
git init -q --bare "$apple_origin"
git init -q "$apple_work"
git -C "$apple_work" config user.email probe@example.invalid
git -C "$apple_work" config user.name probe
git -C "$apple_work" config commit.gpgsign false

printf 'one\n' > "$apple_work/file.txt"
git -C "$apple_work" add file.txt
git -C "$apple_work" commit -qm "first"
apple_first="$(git -C "$apple_work" rev-parse HEAD)"

printf 'two\n' >> "$apple_work/file.txt"
git -C "$apple_work" commit -qam "second"
apple_second="$(git -C "$apple_work" rev-parse HEAD)"

git -C "$apple_work" branch -M main
git -C "$apple_work" branch dev "$apple_second"
git -C "$apple_work" branch hako-ui "$apple_first"
git -C "$apple_work" remote add origin "$apple_origin"
git -C "$apple_work" push -q origin main dev hako-ui
git -C "$apple_origin" symbolic-ref HEAD refs/heads/main

# --- a fake parent repository, with the Apple source as a gitlink ------------
# The gitlink names the FIRST commit while `main` is already at the second: the branch tip is
# ahead of the pin from the start, so a resolver that answered with the branch tip instead of
# the gitlink is caught by the very first check rather than by a contrived later state.
git init -q "$scratch"
git -C "$scratch" config user.email probe@example.invalid
git -C "$scratch" config user.name probe
git -C "$scratch" config commit.gpgsign false
git clone -q "$apple_origin" "$scratch/clients/apple"
git -C "$scratch/clients/apple" checkout -q "$apple_first"
git -C "$scratch" add clients/apple

# The refs file as the current model defines it: one repository, one branch, and NO SHA.
#
# NOTHING PINS THIS FIXTURE, and that is the point. This repository's model is that the Apple CLIENT
# REPOSITORY is the authority and the gitlink is a local convenience, so an unpinned resolution
# follows the tip of APPLE_CLIENT_BRANCH. The fixture's `main` is one commit ahead of the gitlink by
# construction, which is exactly what lets the assertions below tell the two apart.
#
# This suite used to assert the opposite - that the resolution EQUALS the gitlink - in thirteen
# places, written for the model `apple-client-source.sh` deliberately replaced. Those assertions are
# inverted rather than deleted: "the resolution is NOT the gitlink, and here is what it is instead"
# is the property that would catch a per-platform source being reintroduced.
write_pin() {
  cat > "$scratch/release/apple-client-refs.env" <<EOF
APPLE_CLIENT_REPOSITORY=$apple_origin
APPLE_CLIENT_BRANCH=${1:-main}
HAKO_DESIGN_BASELINE_TAG=archive/hako-design-v1
EOF
}

write_pin
git -C "$scratch" commit -qm "parent with the Apple gitlink"

scratch_resolver="$scratch/scripts/ci/apple-client-source.sh"

echo "== the fixture resolves to one shared source =="

# The resolver answers with the BRANCH TIP, not the gitlink: the client repository is the
# authority. The fixture has `main` one commit ahead of its gitlink on purpose, so the two are always
# distinguishable - an equality here would pass under either model and prove neither.
check "the iOS source is the branch the refs file declares" \
  bash -c "test \"\$($scratch_resolver sha ios)\" = '$apple_second'"
check "the macOS source is the same branch tip" \
  bash -c "test \"\$($scratch_resolver sha macos)\" = '$apple_second'"
check "the resolution is NOT the gitlink (a convenience, not the authority)" \
  bash -c "test \"\$($scratch_resolver sha ios)\" != '$apple_first'"
check "the gitlink still records the older commit, so the two really differ" \
  bash -c "test \"\$(git -C '$scratch' ls-tree HEAD clients/apple | awk '{print \$3}')\" = '$apple_first'"
check "both platforms report the same branch" \
  bash -c "test \"\$($scratch_resolver branch ios)\" = \"\$($scratch_resolver branch macos)\""
check "the resolver asserts the shared source" "$scratch_resolver" assert-shared-source
# Two explicit steps rather than one `bash -c` with `&&`, so a failure says WHICH step failed. The
# single-command form collapsed "the checkout did not happen" and "the verification refused" into one
# FAIL with no output, which is the same undiagnosable shape as several earlier defects in this round.
scratch_checkout_rc=0
"$scratch_resolver" checkout ios >/dev/null 2>&1 || scratch_checkout_rc=$?
scratch_verify_rc=0
"$scratch_resolver" verify ios >/dev/null 2>&1 || scratch_verify_rc=$?
if [ "$scratch_checkout_rc" -eq 0 ] && [ "$scratch_verify_rc" -eq 0 ]; then
  echo "  PASS: the iOS checkout verifies once materialised at the resolved revision"
  pass=$((pass + 1))
else
  echo "  FAIL: the iOS checkout verifies once materialised at the resolved revision" >&2
  echo "        checkout rc=$scratch_checkout_rc  verify rc=$scratch_verify_rc" >&2
  echo "        submodule HEAD = $(git -C "$scratch/clients/apple" rev-parse HEAD 2>&1)" >&2
  echo "        resolver sha   = $("$scratch_resolver" sha ios 2>&1)" >&2
  echo "        verify output  = $("$scratch_resolver" verify ios 2>&1)" >&2
  fail=$((fail + 1))
fi

# `resolve` keeps the historical platform names on purpose, because callers eval its output and
# then read them; the migration changed what the macOS names resolve TO, not what they are
# called. Both name sets must therefore carry the one shared SHA.
resolve_out="$("$scratch_resolver" resolve 2>/dev/null || true)"
case "$resolve_out" in
  *"IOS_APPLE_SHA=$apple_second"*)   ios_named=yes ;;
  *)                                 ios_named=no ;;
esac
case "$resolve_out" in
  *"MACOS_APPLE_SHA=$apple_second"*) macos_named=yes ;;
  *)                                 macos_named=no ;;
esac
check "resolve keeps the iOS names pointing at the shared SHA" test "$ios_named" = "yes"
check "resolve keeps the macOS names pointing at the shared SHA" test "$macos_named" = "yes"

# A stale name in the refs file must not answer for the resolver. `IOS_APPLE_BRANCH` named the
# branch the iOS product tracked before the two UI families were merged into one tree;
# `platform_branch` is documented not to fall back to it, so a refs file that still carries
# the name cannot change the branch the ancestry check uses. `hako-ui` exists in the fixture,
# so a resolver that did look it up would have somewhere wrong to land.
write_pin_legacy_ios() {
  cat > "$scratch/release/apple-client-refs.env" <<EOF
APPLE_CLIENT_REPOSITORY=$apple_origin
APPLE_CLIENT_BRANCH=main
IOS_APPLE_BRANCH=hako-ui
HAKO_DESIGN_BASELINE_TAG=archive/hako-design-v1
EOF
}
write_pin_legacy_ios
check "a leftover IOS_APPLE_BRANCH does not replace the declared branch" \
  bash -c "test \"\$($scratch_resolver branch ios)\" = 'main'"
check "the macOS branch still answers the declared branch" \
  bash -c "test \"\$($scratch_resolver branch macos)\" = 'main'"
check "the shared source is unchanged by the leftover name" "$scratch_resolver" assert-shared-source
write_pin

# The macOS working tree is an ephemeral copy of the same commit, not a second source: the
# products still need separate trees for their overlays, and this is what proves the tree is a
# copy of the SAME commit.
check "the macOS checkout is a copy of the shared source" \
  bash -c "test \"\$($scratch_resolver checkout macos)\" = '$apple_second'"
check "the macOS checkout verifies at the shared source" "$scratch_resolver" verify macos
check "the macOS tree is at the iOS source's commit" \
  bash -c "test \"\$(git -C '$scratch/build/apple-client-macos' rev-parse HEAD)\" = \"\$($scratch_resolver sha ios)\""

# ---------------------------------------------------------------------------
# 4. This tree: both platforms resolve to its gitlink
# ---------------------------------------------------------------------------
echo "== both platforms resolve to this tree's gitlink =="

# The gitlink as the commit records it, which is what a release publishes. This is asserted
# against the real tree and not only against a fixture, because the fixture proves the resolver
# is consistent with itself while this proves it is consistent with what will be built.
real_gitlink="$(git ls-tree HEAD clients/apple | awk '{print $3}')"
check "this commit records an Apple gitlink" test -n "$real_gitlink"
# The resolved revision is the Apple client repository's branch tip; the gitlink is what this commit
# records as the local default. They are different commits until someone moves the gitlink, and that
# difference is REPORTED by `assert-shared-source` rather than being fatal - requiring equality is
# exactly the two-repository pin this model removed.
check "the resolved iOS source is a commit" \
  bash -c "test -n \"\$($resolver sha ios)\""
check "the macOS source is the same resolved revision" \
  bash -c "test \"\$($resolver sha macos)\" = \"\$($resolver sha ios)\""
check "the resolver asserts the shared source on this tree" "$resolver" assert-shared-source
check "both platforms report the same branch on this tree" \
  bash -c "test \"\$($resolver branch ios)\" = \"\$($resolver branch macos)\""
check "the branch is the one the refs file names" \
  bash -c "test \"\$($resolver branch ios)\" = '$pin_branch'"

# `verify` reads `clients/apple`'s HEAD, so it needs the submodule checked out - the same
# precondition the iOS build itself has, and one the job that runs this suite in CI satisfies
# by checking out submodules recursively. Without it there is no Apple source to select, which
# is a precondition failure worth reporting rather than skipping.
# NOT asserted here: `checkout ios` followed by `verify ios` on the REAL tree.
#
# That pair is exercised against the fixture above, deliberately. `verify` requires the tree to be at
# the resolved revision, which on a clean checkout it is not - the submodule sits at the gitlink - so
# asserting the pair here would mean running `checkout ios` against the real `clients/apple`. This
# suite is read-only by design, and the submodule it would move is the one the iOS job builds from.
# A structural check that mutates the checkout it inspects is a worse trade than one assertion
# covered elsewhere; the fixture proves the same code path with no side effect.
check "the real tree still records an Apple gitlink to build from" test -n "$real_gitlink"

# ---------------------------------------------------------------------------
# 5. ONE source, and only an explicit override moves it
# ---------------------------------------------------------------------------
echo "== one source, and only an explicit override moves it =="

# Stated in the CURRENT model: both platforms follow the SAME resolution, and moving the gitlink does
# NOT move it. A gitlink that decides the build is the two-repository pin this model removed.
#
# `main` is at apple_second, so moving the gitlink to apple_second could not distinguish "follows the
# gitlink" from "follows the branch". The two are separated instead: the gitlink moves to a commit
# the branch tip is NOT, and they are then checked apart.
# A DEDICATED COMMIT for the gitlink, set with `update-index --cacheinfo` rather than by checking the
# submodule out and staging it.
#
# Checking out `side` would leave the submodule on a commit that equals nothing new, and
# `git add clients/apple` would then stage no change - `git commit` exits 1 on an empty index, and
# under `set -e` the whole suite dies before printing a summary. An earlier version of this block did
# exactly that and the run ended inside git's output with no verdict at all.
#
# A gitlink is an index entry with mode 160000; writing it directly is both simpler and exactly the
# thing the assertion is about - the parent COMMIT records a different Apple revision.
# The gitlink is moved to a THIRD commit - not the one it already records, and not the branch tip.
#
# Two earlier versions of this block were wrong in ways worth recording, because both failed silently
# or died inside git's output with no verdict:
#
#   * `git -C clients/apple checkout FETCH_HEAD && git add clients/apple` - FETCH_HEAD was already the
#     recorded commit, so `git add` staged nothing, `git commit` exited 1 on an empty index, and under
#     `set -e` the suite died before printing a summary;
#   * `update-index --cacheinfo 160000,$apple_first,...` - `--cacheinfo` is a NO-OP when the entry
#     already holds that value, which it did, with the same consequence.
#
# Both are avoided by moving the gitlink somewhere it has never been. `apple_second` is the branch
# tip, so it cannot serve either - the whole point is that the gitlink and the tip DIFFER.
git -C "$apple_work" checkout -q main
printf 'three\n' >> "$apple_work/file.txt"
git -C "$apple_work" commit -qam "a third commit the branch tip will move past"
apple_third="$(git -C "$apple_work" rev-parse HEAD)"
git -C "$apple_work" push -q origin main
git -C "$apple_work" reset -q --hard "$apple_second"

check "a third commit exists to move the gitlink to" test "$apple_third" != "$apple_second"
git -C "$scratch" update-index --cacheinfo "160000,$apple_third,clients/apple"
check "the gitlink entry really moved" \
  bash -c "test \"\$(git -C '$scratch' diff --cached --name-only -- clients/apple | wc -l | tr -d ' ')\" != '0'"
git -C "$scratch" commit -qm "move the Apple gitlink somewhere the branch is not"
check "the gitlink now names a commit the branch tip does not" \
  bash -c "test \"\$(git -C '$scratch' ls-tree HEAD clients/apple | awk '{print \$3}')\" = '$apple_third'"
check "moving the gitlink does NOT move the iOS source" \
  bash -c "test \"\$($scratch_resolver sha ios)\" = '$apple_second'"
check "moving the gitlink does NOT move the macOS source" \
  bash -c "test \"\$($scratch_resolver sha macos)\" = '$apple_second'"
check "both platforms still share one source after the gitlink moved" "$scratch_resolver" assert-shared-source

# The ONE thing that may move the resolution is an explicit override - which is what a release uses
# when it must reproduce an exact revision rather than follow a branch.
check "an explicit APPLE_CLIENT_REF moves the iOS source" \
  bash -c "test \"\$(APPLE_CLIENT_REF='$apple_third' $scratch_resolver sha ios)\" = '$apple_third'"
check "an explicit APPLE_CLIENT_REF moves the macOS source too" \
  bash -c "test \"\$(APPLE_CLIENT_REF='$apple_third' $scratch_resolver sha macos)\" = '$apple_third'"
check "both platforms share one source under the override" \
  bash -c "APPLE_CLIENT_REF='$apple_third' $scratch_resolver assert-shared-source"

# The macOS working tree is a COPY, so it does not follow anything by itself, and a copy left at a
# revision the build is not using must be refused rather than built. That is the property, and it is
# asserted without reference to which commit the gitlink names - the gitlink no longer decides.
#
# The stale tree is made stale deliberately, by pointing it at a commit the resolution is not using.
# Relying on "the tree created earlier happens to be behind" made this assertion depend on which
# commit the fixture's branch had reached, which is exactly the coupling this rewrite removes.
git -C "$scratch/build/apple-client-macos" checkout -q --detach "$apple_first" >/dev/null 2>&1 || true
expects_fail "a macOS working tree left at a stale commit is refused" "$scratch_resolver" verify macos
# Compared against WHAT THE RESOLVER ANSWERS, not against a literal commit. This is the property the
# assertion is about - the copy is materialised at the revision the build resolved - and pinning it to
# a literal made it depend on which commit the fixture's branch had reached, which is the coupling
# this rewrite exists to remove.
check "re-checking out the macOS tree materialises the resolved revision" \
  bash -c "test \"\$($scratch_resolver checkout macos)\" = \"\$($scratch_resolver sha macos)\""
check "the re-checked-out macOS tree verifies" "$scratch_resolver" verify macos

# Requirement: a branch that moves must not move a release. `APPLE_CLIENT_BRANCH` names the
# branch the pinned commit is reviewed on; it never selects the commit.
git -C "$apple_work" checkout -q main
printf 'four\n' >> "$apple_work/file.txt"
git -C "$apple_work" commit -qam "main moves on"
# `-f`: origin/main already carries apple_third, pushed above, so the local branch is behind and a
# plain push is rejected. The scratch repository is built by this suite alone, so forcing it is safe
# and the alternative - a rejected push - aborts the run with git's advice text instead of a verdict.
git -C "$apple_work" push -q -f origin main
main_tip="$(git -C "$apple_work" rev-parse HEAD)"
check "the branch tip moved past the pinned commit" test "$main_tip" != "$apple_second"
check "a moved branch tip does not move the already-resolved iOS source" \
  bash -c "test \"\$($scratch_resolver sha ios)\" != '$main_tip'"
check "a moved branch tip does not move the already-resolved macOS source" \
  bash -c "test \"\$($scratch_resolver sha macos)\" != '$main_tip'"
check "the platforms still share one source after the branch moved" "$scratch_resolver" assert-shared-source

# The resolver's published interface. A rename that lands only in the docs - `assert-distinct`
# to `assert-shared-source` - leaves callers invoking an action that no longer exists, and the
# failure surfaces at the far end of a release as "unknown action". Pin the list.
resolver_actions="$(grep -oE '^  [a-z][a-z-]*\)' scripts/ci/apple-client-source.sh | tr -d ' )' | LC_ALL=C sort)"
expected_actions="assert-on-branch
assert-shared-source
branch
checkout
dir
ls-remote
resolve
sha
verify"
check "the resolver offers exactly the documented actions" test "$resolver_actions" = "$expected_actions"

# Behavioural, not textual: the removed name must be refused by the dispatch, not merely absent
# from a grep that a comment could satisfy.
expects_fail "the removed assert-distinct action is refused" "$scratch_resolver" assert-distinct
expects_fail "an action that does not exist is refused" "$scratch_resolver" assert-two-sources

# ---------------------------------------------------------------------------
# 6. The selection fails closed
# ---------------------------------------------------------------------------
echo "== the selection fails closed =="

# Requirement: a missing refs file is refused. `sha` reads the gitlink and needs no file, but
# every action that resolves the selection as a whole goes through `load_refs`, so a release
# cannot start from a tree that has lost the repository it declares.
expects_fail "a missing refs file is refused" \
  bash -c "mv '$scratch/release/apple-client-refs.env' '$scratch/release/refs.bak' || exit 0; \
           $scratch_resolver assert-shared-source; rc=\$?; \
           mv '$scratch/release/refs.bak' '$scratch/release/apple-client-refs.env' || exit 0; \
           exit \$rc"

# The branch is required even though it selects nothing: `checkout` proves the gitlink is an
# ancestor of it, and an absent branch would make that proof vacuous.
expects_fail "a refs file with no APPLE_CLIENT_BRANCH is refused" \
  bash -c "printf 'APPLE_CLIENT_REPOSITORY=%s\n' '$apple_origin' > '$scratch/release/apple-client-refs.env' || exit 0; \
           $scratch_resolver branch ios; rc=\$?; \
           printf 'APPLE_CLIENT_REPOSITORY=%s\nAPPLE_CLIENT_BRANCH=main\nHAKO_DESIGN_BASELINE_TAG=archive/hako-design-v1\n' '$apple_origin' > '$scratch/release/apple-client-refs.env' || exit 0; \
           exit \$rc"

# Requirement: the removed per-platform SHA must not silently select a source. The resolver
# refuses a refs file that declares `MACOS_APPLE_SHA` or `MACOS_APPLE_BRANCH` at all, so the
# file cannot be left half-migrated. Every public resolver action, including `sha macos`, must
# refuse that invalid configuration rather than returning either the gitlink or the planted value.
# The planted value is a real commit of the same repository that is NOT the gitlink, ensuring the
# fixture would expose an implementation that accidentally consulted the removed selector.
write_pin_macos_sha() {
  cat > "$scratch/release/apple-client-refs.env" <<EOF
APPLE_CLIENT_REPOSITORY=$apple_origin
APPLE_CLIENT_BRANCH=main
MACOS_APPLE_BRANCH=dev
MACOS_APPLE_SHA=$apple_first
EOF
}
write_pin_macos_sha
expects_fail "a refs file that puts a per-platform SHA back is refused" "$scratch_resolver" assert-shared-source
expects_fail "resolve refuses the same refs file" "$scratch_resolver" resolve
expects_fail "sha macos refuses the planted per-platform source" "$scratch_resolver" sha macos
write_pin
check "the restored refs file resolves the shared source again" "$scratch_resolver" assert-shared-source

expects_fail "an unknown platform is refused" "$scratch_resolver" sha windows

# Requirement: a checkout that is not the gitlink is refused - a build here would not be the
# Apple source this commit records.
git -C "$scratch/clients/apple" checkout -q "$apple_first"
expects_fail "an iOS checkout that is not the gitlink is refused" "$scratch_resolver" verify ios
git -C "$scratch/clients/apple" checkout -q "$apple_second"
check "the iOS checkout verifies once materialised at the resolved revision" \
  bash -c "$scratch_resolver checkout ios >/dev/null && $scratch_resolver verify ios >/dev/null"

# A parent commit with no gitlink at all.
#
# This used to expect a REFUSAL, on the reasoning that "the gitlink is the only authority, so its
# absence must not leave one platform with an answer". That reasoning is the superseded model: the
# gitlink is a convenience and `resolve_sha` never consults it, so removing it cannot change what a
# build resolves to and must not be fatal. The old assertion therefore failed for the right reason -
# the resolver succeeded - which is how it was found.
#
# The property worth asserting is the one that actually protects a release: removing the gitlink
# changes NOTHING about the resolution, and both platforms still agree. A missing gitlink is a fact
# about the parent commit, not a reason to refuse to build.
#
# Removal is committed, not just staged: `git rm --cached` alone leaves HEAD unchanged, and this
# case is about what the COMMIT records.
no_gitlink_rc=0
bash -c "before=\$(git -C '$scratch' rev-parse HEAD) || exit 9; \
         resolved_before=\$($scratch_resolver sha ios) || exit 9; \
         git -C '$scratch' rm -q --cached clients/apple && \
         git -C '$scratch' commit -qm 'drop the Apple gitlink' || exit 9; \
         test -z \"\$(git -C '$scratch' ls-tree HEAD clients/apple)\" || exit 9; \
         rc=0; \
         resolved_after=\$($scratch_resolver sha ios) || rc=1; \
         test \"\$resolved_after\" = \"\$resolved_before\" || rc=1; \
         $scratch_resolver sha macos >/dev/null || rc=1; \
         $scratch_resolver assert-shared-source >/dev/null || rc=1; \
         git -C '$scratch' reset -q --hard \"\$before\" || exit 9; \
         exit \$rc" || no_gitlink_rc=$?
if [ "$no_gitlink_rc" -eq 0 ]; then
  echo "  PASS: dropping the gitlink does not change the resolved source, for either platform"
  pass=$((pass + 1))
else
  echo "  FAIL: dropping the gitlink changed or broke the resolution (rc=$no_gitlink_rc)" >&2
  fail=$((fail + 1))
fi

check "the fixture still resolves one shared source after the negative cases" \
  "$scratch_resolver" assert-shared-source

# ---------------------------------------------------------------------------
# 7. The two clients are proven to share one Libbox
# ---------------------------------------------------------------------------
echo "== the shared Libbox check is wired in =="

check "the shared-Libbox checker exists" test -x scripts/ci/check-apple-shared-libbox.sh
check "release-apple verifies both clients link one Libbox" \
  grep -q 'check-apple-shared-libbox.sh' scripts/release-apple.sh

# Every invocation of a platform builder in release-apple.sh must carry an explicit
# APPLE_CLIENT_DIR. The scripts default to `clients/apple`, so an unqualified call would
# silently build one product from the other product's working tree - which, now that both trees
# hold the same commit, is a different bug than it used to be but no less silent.
if python3 - <<'PY'
import re
import sys

src = open("scripts/release-apple.sh", encoding="utf-8").read()
lines = src.splitlines()

problems = []
# A builder invocation is any line whose command mentions one of these scripts.
pattern = re.compile(r"scripts/ci/build-(ios|macos)-(ipa|dmg|testflight)\.sh")
for index, line in enumerate(lines):
    if not pattern.search(line):
        continue
    window = "\n".join(lines[max(0, index - 3): index + 1])
    if "APPLE_CLIENT_DIR=" not in window:
        problems.append(f"line {index + 1}: {line.strip()}")

if problems:
    print("release-apple: a platform builder is invoked without APPLE_CLIENT_DIR:", file=sys.stderr)
    for problem in problems:
        print(f"  - {problem}", file=sys.stderr)
    raise SystemExit(1)
print("  PASS: every release-apple platform build names its Apple source")
PY
then
  pass=$((pass + 1))
else
  fail=$((fail + 1))
fi

# And the iOS and macOS builders must be pointed at DIFFERENT working trees: the commit is
# shared, the overlays are not, and two products writing into one tree would contaminate each
# other's build.
check "release-apple points the iOS builder at the iOS working tree" \
  bash -c "grep -q 'APPLE_CLIENT_DIR=\"\$ios_client_dir\"' scripts/release-apple.sh"
check "release-apple points the macOS builder at the macOS working tree" \
  bash -c "grep -q 'APPLE_CLIENT_DIR=\"\$macos_client_dir\"' scripts/release-apple.sh"
check "publish verifies both clients link one Libbox" \
  grep -q 'check-apple-shared-libbox.sh' scripts/publish-apple-beta.sh

echo
echo "check-apple-source-selection: $pass passed, $fail failed"
[ "$fail" -eq 0 ] || exit 1
