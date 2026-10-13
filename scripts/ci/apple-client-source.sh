#!/usr/bin/env bash
# Resolves, checks out and verifies the Apple client source for the two Apple products
# this fork ships.
#
# Usage:
#   apple-client-source.sh resolve          print the resolved selection as shell assignments
#                                           (this is what FREEZES the SHA for a build)
#   apple-client-source.sh sha    <ios|macos>
#   apple-client-source.sh branch <ios|macos>
#   apple-client-source.sh dir    <ios|macos>
#   apple-client-source.sh checkout <ios|macos> [dir]
#   apple-client-source.sh verify   <ios|macos> [dir]
#   apple-client-source.sh ls-remote                resolve the default revision now, print it
#   apple-client-source.sh assert-shared-source
#   apple-client-source.sh assert-on-branch [dir]
#
# # The topology this encodes
#
# Both Apple products take their Swift source from ONE commit of ONE Apple client
# repository, and link ONE Libbox framework built once from this repository:
#
#     parent HEAD  ->  one Libbox.xcframework
#                          |
#            +-------------+-------------+
#            v                           v
#     Apple source @ APPLE_SHA    Apple source @ APPLE_SHA
#     scheme SFI - iOS            scheme SFM - macOS
#
# There is no per-platform source selection. Which UI a device shows is decided by the
# Apple source at runtime, from the device idiom; the parent only decides which commit is
# built. An earlier revision of this script did the opposite - it resolved macOS to an
# independent `dev` pin and asserted the two sources were DISTINCT - which made the parent
# repository, not the Apple source, the thing that decided UI ownership.
#
# # Where the SHA comes from, and why that changed
#
# The authority used to be the `clients/apple` GITLINK. That made every client commit a
# two-repository operation: commit the UI repo, come back here, move the gitlink, commit,
# push, pull. The pin was manual by construction, and the script could only ever mirror a
# decision someone had already made by hand.
#
# The authority is now the APPLE CLIENT REPOSITORY:
#
#   DEFAULT   the tip of $APPLE_CLIENT_BRANCH, resolved to an exact SHA
#   OVERRIDE  APPLE_CLIENT_REF / APPLE_CLIENT_SHA, an exact SHA / tag / branch
#
# Either way the exact commit is materialised, so the build never depends on a branch tip.
#
# # Resolve ONCE, then frozen
#
# `resolve` is the single resolution point. It reads the frozen SHA if one exists, otherwise
# it resolves one and writes it to $APPLE_CLIENT_STATE (default build/apple-client-source.env,
# beside the macOS working tree, and gitignored). Every later subcommand - and every separate
# process in the same build - reads that file rather than asking the remote again.
#
# That is the whole point: without it, iOS could resolve A, someone could push B, and macOS
# could resolve B, so ONE release would contain two UI commits. Resolving twice is not a
# performance detail, it is a correctness boundary.
#
# Delete the state file (or set a different APPLE_CLIENT_STATE) to start a new resolution.
#
# # Two directories exist even though the SHA is one
#
# Each platform applies its own overlays (compatibility, branding, entitlements) to its
# working tree, so they must not share one. They share the SHA and nothing else:
#
#     clients/apple                the default checkout (iOS by default)
#     build/apple-client-macos     an ephemeral checkout of the SAME commit
#
# Overlays may leave either tree dirty; HEAD never moves. That is checked, not assumed:
# `verify` requires the tree's HEAD to equal the resolved SHA.
#
# # What the gitlink is now
#
# `clients/apple` and its gitlink still exist and still work. They are no longer the build
# authority, and `assert-shared-source` no longer requires the resolved SHA to equal the
# gitlink - that requirement is what made the pin manual. The difference is REPORTED rather
# than ignored, and APPLE_CLIENT_REQUIRE_GITLINK=1 restores the old hard equality for a build
# that must match the recorded submodule.
#
# # Fail closed
#
# An unresolvable revision, a repository that cannot be reached, an ephemeral tree whose
# object store is not the one this script wired, and a checkout whose HEAD is not the
# resolved SHA are all hard failures. Nothing here falls back to "whatever is checked out":
# publishing a client built from an unrecorded revision is the failure this script prevents.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

refs_file="release/apple-client-refs.env"

# The two working trees. iOS uses the submodule directly; macOS gets an ephemeral copy of
# the same commit so the two overlays cannot contaminate each other.
ios_dir_default="clients/apple"
macos_dir_default="build/apple-client-macos"

# Where the resolved SHA is frozen for the rest of the build.
state_file_default="build/apple-client-source.env"

fail() { echo "apple-client-source: $*" >&2; exit 1; }

state_file() { printf '%s\n' "${APPLE_CLIENT_STATE:-$state_file_default}"; }

# --- configuration -----------------------------------------------------------

# Does the REFS FILE ITSELF declare a per-platform source?
#
# Deliberately a text test on the file, NOT `[ -n "${MACOS_APPLE_SHA:-}..." ]` after
# sourcing it. That form cannot tell the file's own assignments from the caller's
# environment, and the caller legitimately carries BOTH names: `resolve` emits
# MACOS_APPLE_SHA / MACOS_APPLE_BRANCH as historical aliases - so a caller that evals its
# output does not trip `set -u` - and release-apple.sh exports them on to the builders.
#
# Testing the environment therefore made every subcommand that calls this fail against the
# values `resolve` had printed moments earlier, and the message blamed a refs file that
# contains no such line at all. `release-apple.sh` deadlocked on its own output: resolve ->
# export -> assert-shared-source, which refused. The question this guard exists to ask is
# about the FILE, which is what it now asks.
refs_declares_platform_source() {
  grep -qE '^[[:space:]]*(export[[:space:]]+)?(MACOS_APPLE_SHA|MACOS_APPLE_BRANCH)=' "$refs_file"
}

load_refs() {
  [ -f "$refs_file" ] || fail "no $refs_file; the Apple client repository is undeclared."
  # Asked before sourcing, so a file that reinstates the two-source model is refused rather
  # than half-applied - which keeps the original intent ("every subcommand must refuse it")
  # while making the answer depend on the file and not on who called us.
  if refs_declares_platform_source; then
    fail "$refs_file still declares a macOS-specific Apple source.
  Both platforms build one Apple source now; a per-platform source pin is the
  two-source model this topology removed. Delete MACOS_APPLE_SHA and MACOS_APPLE_BRANCH."
  fi
  # shellcheck disable=SC1090
  . "$refs_file"
  [ -n "${APPLE_CLIENT_REPOSITORY:-}" ] || fail "$refs_file sets no APPLE_CLIENT_REPOSITORY"
  [ -n "${APPLE_CLIENT_BRANCH:-}" ] || fail "$refs_file sets no APPLE_CLIENT_BRANCH"
}

# The requested revision, or empty for "the tip of the declared branch".
requested_ref() {
  printf '%s\n' "${APPLE_CLIENT_REF:-${APPLE_CLIENT_SHA:-}}"
}

# ls_remote_sha resolves a revision against the Apple repository WITHOUT a local checkout.
#
# The remote is asked once, for one ref, and the fully-qualified result is preferred: a
# `--refs` query hides the peeled `^{}` line, which is the only way to learn the commit a
# TAG names. Passing a 40-hex SHA through unchanged keeps the override path from needing the
# network at all.
#
# The candidate list is built so that a SHORT name works for all three override kinds - a branch,
# a tag and a SHA - without the caller having to say which one it is:
#
#     v1        -> refs/heads/v1, refs/tags/v1, refs/tags/v1^{}, refs/tags/v1 (peeled preferred)
#     main      -> refs/heads/main
#     refs/...  -> passed through as an already-qualified ref
#     40-hex    -> returned as-is
ls_remote_sha() {
  local repo="$1" want="$2" out sha candidates
  case "$want" in
    [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]*)
      if [ "${#want}" -eq 40 ]; then printf '%s\n' "$want"; return 0; fi
      ;;
  esac
  [ -n "$want" ] || fail "ls_remote_sha needs a revision to resolve"

  case "$want" in
    refs/*) candidates="$want" ;;
    *)      candidates="refs/heads/$want refs/tags/$want refs/tags/$want^{} refs/tags/$want" ;;
  esac

  # shellcheck disable=SC2086
  if ! out="$(git ls-remote "$repo" $candidates 2>/dev/null)"; then
    fail "could not reach $repo to resolve '$want'."
  fi
  [ -n "$out" ] || fail "$repo has no '$want' (tried as a SHA, a branch and a tag)."
  # Prefer a peeled tag, then a branch, then a plain ref: the peeled line is the commit.
  sha="$(printf '%s\n' "$out" | awk '$2 ~ /\^\{\}$/ {print $1; exit}')"
  [ -n "$sha" ] || sha="$(printf '%s\n' "$out" | awk '$2 ~ /^refs\/heads\// {print $1; exit}')"
  [ -n "$sha" ] || sha="$(printf '%s\n' "$out" | awk 'NR==1 {print $1}')"
  case "$sha" in
    [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]*) ;;
    *) fail "could not parse a commit out of the ls-remote answer for '$want'" ;;
  esac
  printf '%s\n' "$sha"
}

# resolve_sha is the ONE resolution point, and it is also the freeze.
#
# Order, and each step's reason:
#   1. an explicit APPLE_CLIENT_REF/SHA wins over everything - it is how an older build is
#      reproduced, so it must not be overridden by a stale state file;
#   2. otherwise a frozen state file wins - this is the same build answering the same
#      question a second time, and re-asking the remote is the two-commit hazard;
#   3. otherwise resolve the declared branch tip and freeze it.
resolve_sha() {
  load_refs
  local want state sha
  want="$(requested_ref)"
  state="$(state_file)"

  if [ -z "$want" ] && [ -f "$state" ]; then
    # shellcheck disable=SC1090
    sha="$(sed -n 's/^APPLE_CLIENT_SHA=//p' "$state" | tail -n 1)"
    [ -n "$sha" ] || fail "$state exists but records no APPLE_CLIENT_SHA.
  Delete it to resolve a fresh Apple source."
    printf '%s\n' "$sha"
    return 0
  fi

  if [ -z "$want" ]; then
    sha="$(ls_remote_sha "$APPLE_CLIENT_REPOSITORY" "refs/heads/$APPLE_CLIENT_BRANCH")"
  else
    sha="$(ls_remote_sha "$APPLE_CLIENT_REPOSITORY" "$want")"
  fi

  # Freeze it, so separate processes in this build cannot each resolve their own answer.
  mkdir -p "$(dirname "$state")"
  {
    printf '# Written by apple-client-source.sh. Delete to start a new resolution.\n'
    printf 'APPLE_CLIENT_SHA=%s\n' "$sha"
    printf 'APPLE_CLIENT_REPOSITORY=%s\n' "$APPLE_CLIENT_REPOSITORY"
    printf 'APPLE_CLIENT_BRANCH=%s\n' "$APPLE_CLIENT_BRANCH"
  } > "$state"
  printf '%s\n' "$sha"
}

# apple_sha_gitlink reads the parent gitlink. `git ls-tree HEAD` rather than a working-tree
# read: this is the value the COMMIT records, which is what a comparison against it means.
apple_sha_gitlink() {
  local sha
  sha="$(git ls-tree HEAD "$ios_dir_default" | awk '{print $3}')"
  [ -n "$sha" ] || fail "this commit records no gitlink at $ios_dir_default"
  case "$sha" in
    [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]*) ;;
    *) fail "the gitlink at $ios_dir_default ('$sha') is not a commit SHA" ;;
  esac
  printf '%s\n' "$sha"
}

platform_dir() {
  case "$1" in
    ios)   printf '%s\n' "${2:-$ios_dir_default}" ;;
    macos) printf '%s\n' "${2:-$macos_dir_default}" ;;
    *) fail "unknown platform '$1' (expected ios or macos)" ;;
  esac
}

# platform_sha answers "which Apple commit does this product build?" - and for both products
# the answer is the one resolved SHA. This is the single most important line in the file: if
# either arm ever read a different source, the two products would stop being one.
#
# The refs file is loaded through resolve_sha so that the same misconfiguration fails here as
# everywhere else: a refs file edited back to the two-source model must be refused by every
# subcommand, not only by the ones that happen to read the file.
platform_sha() {
  case "$1" in
    ios|macos) ;;
    *) fail "unknown platform '$1' (expected ios or macos)" ;;
  esac
  resolve_sha
}

# platform_branch names the branch a new resolution would follow. It never selects a commit:
# the SHA is already resolved by the time anything reads this, and it is reported for
# diagnostics and for the ancestor check.
#
# Both arms answer with the SAME branch, because there is only one source. They do not fall
# back to `IOS_APPLE_BRANCH`: that name records the branch the iOS product tracked before the
# two UI families were merged into one tree, so returning it would answer "which branch does
# the pinned commit live on?" with a stale historical value - and the answer would depend on
# whether an earlier call had already sourced the file. Loading the file here makes the answer
# a property of the file, not of the caller's environment.
platform_branch() {
  case "$1" in
    ios|macos) ;;
    *) fail "unknown platform '$1' (expected ios or macos)" ;;
  esac
  load_refs
  printf '%s\n' "$APPLE_CLIENT_BRANCH"
}

# --- checkout ----------------------------------------------------------------

# fetch_branch ensures a checkout knows the declared branch. Fetching the branch rather than
# the SHA is deliberate: fetching an arbitrary commit by SHA depends on a server-side
# setting, and the ancestor assertion below needs the branch ref anyway.
fetch_branch() {
  local dir="$1" branch="$2"
  git -C "$dir" fetch --prune origin \
    "+refs/heads/$branch:refs/remotes/origin/$branch" >/dev/null 2>&1 \
    || fail "could not fetch branch '$branch' into $dir"
}

require_commit() {
  local dir="$1" sha="$2"
  git -C "$dir" cat-file -e "$sha^{commit}" 2>/dev/null || fail \
    "$dir does not contain commit $sha.
  The resolved Apple source names a commit this checkout cannot reach."
}

# require_on_branch asserts the resolved commit is an ancestor of the declared branch, so a
# pin cannot be edited to a commit that the reviewed branch never contained.
require_on_branch() {
  local dir="$1" sha="$2" branch="$3"
  git -C "$dir" merge-base --is-ancestor "$sha" "refs/remotes/origin/$branch" 2>/dev/null || fail \
    "commit $sha is not an ancestor of origin/$branch.
  The pinned Apple source must be a commit of the branch this fork reviews; refusing to
  build a source that the declared branch never contained."
}

# fetch_one_commit brings the resolved commit into a checkout that may not have it, using the
# branch fetch when it can and a direct SHA fetch as the fallback. A bare fetch of an
# arbitrary SHA is not guaranteed by every server, which is why the branch is tried first -
# but for a DEFAULT resolution off the branch tip the commit IS the branch tip, and for an
# override naming an old commit the branch fetch usually carries it too.
fetch_one_commit() {
  local dir="$1" want="$2" branch="$3"
  git -C "$dir" cat-file -e "$want^{commit}" 2>/dev/null && return 0
  fetch_branch "$dir" "$branch" || true
  git -C "$dir" cat-file -e "$want^{commit}" 2>/dev/null && return 0
  git -C "$dir" fetch --no-tags origin "+$want:refs/apple-client/pinned" >/dev/null 2>&1 || true
  git -C "$dir" cat-file -e "$want^{commit}" 2>/dev/null && return 0
  fail "$want is not reachable from $APPLE_CLIENT_REPOSITORY.
  The resolved Apple source must be a commit of origin/$branch, or a commit the server will
  serve by SHA. Refusing to build a source this checkout cannot verify."
}

checkout_ios() {
  local dir; dir="$(platform_dir ios "$1")"
  local want; want="$(resolve_sha)"

  if [ ! -d "$dir" ]; then
    fail "$dir is missing. The Apple source is this repository's submodule; check it out with:
    git submodule update --init --recursive $ios_dir_default"
  fi
  [ -e "$dir/.git" ] || fail "$dir is not a git checkout."

  # The nested submodule (Frameworks/Runestone) is what the Xcode project resolves
  # packages from, so it is part of "the Apple source", not an optional extra. It is
  # initialised from the RECORDED revision, so a pinned client cannot pull a nested
  # dependency from wherever that dependency's default branch happens to be.
  git -C "$dir" submodule update --init --recursive >/dev/null 2>&1 || fail \
    "could not initialise the nested submodules of $dir"

  local actual; actual="$(git -C "$dir" rev-parse HEAD)"
  if [ "$actual" != "$want" ]; then
    # The submodule may simply be behind the resolved SHA - that is the normal case now that
    # the client repository, not the gitlink, is the authority. Fetch the commit and move.
    fetch_one_commit "$dir" "$want" "$(platform_branch ios)"
    git -C "$dir" checkout --detach "$want" >/dev/null 2>&1 || fail \
      "could not check out $want in $dir"
    git -C "$dir" submodule update --init --recursive >/dev/null 2>&1 || fail \
      "could not initialise the nested submodules of $dir"
    actual="$(git -C "$dir" rev-parse HEAD)"
    [ "$actual" = "$want" ] || fail "$dir is at $actual after checkout, expected $want"
  fi

  printf '%s\n' "$actual"
}

# checkout_macos produces a SECOND working tree of the SAME commit. It is a copy rather than
# a second submodule so that the parent keeps recording exactly one Apple gitlink, and it is
# a copy rather than the submodule itself so the macOS overlays cannot touch the iOS tree.
checkout_macos() {
  local dir; dir="$(platform_dir macos "$1")"
  # load_refs FIRST. `resolve_sha` runs inside a command substitution, so anything it sourced
  # happens in a subshell and does not survive into this function - and this function needs
  # APPLE_CLIENT_REPOSITORY for the no-submodule clone path. Relying on resolve_sha to have loaded
  # it produced "APPLE_CLIENT_REPOSITORY: parameter not set" under `set -u`, which the executable
  # check caught on the very path the CI macOS job takes (it checks out with submodules: false).
  load_refs
  local want; want="$(resolve_sha)"
  local branch; branch="$(platform_branch macos)"
  local repo="$APPLE_CLIENT_REPOSITORY"

  # The macOS tree is a second working copy of the SAME commit, and it is produced from the
  # submodule when that exists because a local copy is cheap and needs no network.
  #
  # It is NOT required to exist. The macOS job deliberately checks the parent out with
  # `submodules: false` - it has no use for the iOS tree, and the Libbox it links arrives as
  # a downloaded artifact - so this function has to work with only the resolved SHA to go on.
  local submodule="$ios_dir_default"
  local have_submodule=0
  [ -d "$submodule/.git" ] && have_submodule=1

  if [ "$have_submodule" = "1" ]; then
    fetch_one_commit "$submodule" "$want" "$branch"
    require_on_branch "$submodule" "$want" "$branch"
  fi

  # Where the copy draws its objects from. With a submodule that is the submodule's own store,
  # refreshed on every call; without one the clone's own store is used and the commit is
  # fetched into it below.
  local objects_source=""
  if [ "$have_submodule" = "1" ]; then
    objects_source="$(git -C "$submodule" rev-parse --absolute-git-dir)/objects"
    [ -d "$objects_source" ] || fail \
      "cannot locate the object store of $submodule; the macOS copy cannot be refreshed from it."
  fi

  # Which source tree the copy came from is answered by its object-store wiring, not by
  # `origin`: a `--shared` clone points `origin` at the source PATH rather than at the Apple
  # repository URL, and the URL can be re-pointed between runs, so comparing origin would be
  # both wrong on a healthy tree and unstable across runs.
  if [ -d "$dir/.git" ] && [ -n "$objects_source" ]; then
    local copy_git_dir; copy_git_dir="$(git -C "$dir" rev-parse --absolute-git-dir)"
    local alternate=""
    [ -f "$copy_git_dir/objects/info/alternates" ] && \
      alternate="$(head -n 1 "$copy_git_dir/objects/info/alternates")"
    if [ -n "$alternate" ] && [ "$alternate" != "$objects_source" ]; then
      fail "$dir draws its objects from '$alternate' instead of $submodule.
  Remove $dir and re-run so the copy is taken from the source this build resolved."
    fi
  fi

  if [ ! -d "$dir/.git" ]; then
    rm -rf "$dir"
    mkdir -p "$(dirname "$dir")"
    if [ "$have_submodule" = "1" ]; then
      # A local, shared-object clone: cheap, offline, and it carries every commit the
      # submodule has - which is the commit we are about to check out.
      git clone --shared --no-checkout "$submodule" "$dir" >/dev/null 2>&1 || fail \
        "could not copy $submodule into $dir"
    else
      # No submodule to copy: clone the declared repository instead. Blobless and
      # single-branch, because only one commit is ever needed here.
      git clone --filter=blob:none --no-checkout "$repo" "$dir" >/dev/null 2>&1 || fail \
        "could not clone $repo into $dir"
    fi
  fi

  if [ "$have_submodule" = "1" ]; then
    # Refresh the alias rather than fetch: a `--shared` clone aliases the source's object
    # database at clone time, and the submodule gains objects afterwards - every time the
    # resolved SHA moves - so the alias has to be re-pointed before the wanted commit
    # resolves.
    #
    # Fetching a commit that exists only locally is awkward by design (a bare fetch of an
    # arbitrary SHA is not guaranteed, and a refspec for a commit that is on no branch cannot
    # be written), while an alternate is exactly the mechanism for "this object store is also
    # mine".
    local copy_git_dir; copy_git_dir="$(git -C "$dir" rev-parse --absolute-git-dir)"
    mkdir -p "$copy_git_dir/objects/info"
    printf '%s\n' "$objects_source" > "$copy_git_dir/objects/info/alternates"
  else
    fetch_one_commit "$dir" "$want" "$branch"
  fi

  require_commit "$dir" "$want"
  git -C "$dir" checkout --detach "$want" >/dev/null 2>&1 || fail \
    "could not check out $want in $dir"
  git -C "$dir" submodule update --init --recursive >/dev/null 2>&1 || fail \
    "could not initialise the nested submodules of $dir"

  local actual; actual="$(git -C "$dir" rev-parse HEAD)"
  [ "$actual" = "$want" ] || fail "$dir is at $actual after checkout, expected $want"
  printf '%s\n' "$actual"
}

verify() {
  local platform="$1" dir; dir="$(platform_dir "$1" "$2")"
  local want; want="$(platform_sha "$platform")" || exit 1
  [ -e "$dir/.git" ] || fail "$dir is not a git checkout."
  local actual; actual="$(git -C "$dir" rev-parse HEAD)"
  [ "$actual" = "$want" ] || fail \
    "$dir is at $actual but the $platform Apple source must be $want."
  echo "PASS: $platform Apple source $dir @ $actual"
}

# assert-shared-source is the topology invariant stated as a check: both products must be
# built from the SAME Apple commit.
#
# It deliberately does NOT require that commit to equal the parent gitlink. Requiring it is
# what made every client release a two-repository pin: the build could only ever mirror a
# gitlink someone had already updated by hand, in a second commit, after the fact. The
# gitlink is now a CONVENIENCE - the local default checkout - not the authority.
#
# The difference is still reported, because "the submodule is 26 commits behind what this
# build is using" is something a reader needs to know, and silence about it would be its own
# kind of lie. APPLE_CLIENT_REQUIRE_GITLINK=1 restores the old hard equality for a build that
# must match the recorded submodule.
assert_shared_source() {
  load_refs
  local ios macos gitlink
  ios="$(resolve_sha)"
  macos="$(platform_sha macos)"
  [ "$ios" = "$macos" ] || fail \
    "the iOS and macOS Apple sources differ ($ios vs $macos).
  Both products must build one Apple source; a per-platform source is the
  two-source model this topology removed."

  if gitlink="$(apple_sha_gitlink 2>/dev/null)"; then
    if [ "$ios" != "$gitlink" ]; then
      if [ "${APPLE_CLIENT_REQUIRE_GITLINK:-0}" = "1" ]; then
        fail "the resolved Apple source $ios is not the gitlink $gitlink.
  APPLE_CLIENT_REQUIRE_GITLINK=1 requires the build to match the recorded submodule."
      fi
      echo "note: the resolved Apple source $ios differs from the clients/apple gitlink $gitlink." >&2
      echo "      The client repository is the build authority; the gitlink is the local default checkout." >&2
    fi
  fi
  echo "PASS: iOS and macOS share Apple source $ios"
}

# assert-on-branch proves the resolved commit is on the reviewed branch, which the SHA cannot
# show on its own. It is a diagnostic, not a source selector: it never changes which commit is
# built.
assert_on_branch() {
  local dir; dir="$(platform_dir ios "${1:-}")"
  local sha; sha="$(resolve_sha)"
  local branch; branch="$(platform_branch ios)"
  [ -d "$dir/.git" ] || fail "$dir is not a git checkout."
  fetch_one_commit "$dir" "$sha" "$branch"
  require_on_branch "$dir" "$sha" "$branch"
  echo "PASS: resolved Apple source $sha is on origin/$branch"
}

# show emits the resolved selection as shell assignments. Every name below keeps its
# historical spelling because callers `eval` this output and then read those names: the
# migration changed what the macOS names RESOLVE TO, not what they are called. Dropping
# `MACOS_APPLE_SHA` here would have made every consumer fail on an unbound variable under
# `set -u`, which is a silent interface break dressed up as a rename.
#
# The macOS names therefore answer "which Apple commit does the macOS product build?" - and
# the answer is the same resolved SHA. `MACOS_APPLE_DIR` stays distinct because the two
# platforms still need separate working trees for their overlays.
#
# resolve_sha is called ONCE here. Every value below is derived from that single answer, so
# this function cannot disagree with itself even across a concurrent push to the client
# repository.
show() {
  local dir="${1:-}"
  load_refs
  local sha; sha="$(resolve_sha)"
  local branch; branch="$(platform_branch ios)"
  echo "APPLE_CLIENT_REPOSITORY=$APPLE_CLIENT_REPOSITORY"
  echo "APPLE_CLIENT_BRANCH=$branch"
  echo "APPLE_CLIENT_SHA=$sha"
  echo "APPLE_CLIENT_REF=$(requested_ref)"
  echo "APPLE_CLIENT_STATE=$(state_file)"
  echo "IOS_APPLE_BRANCH=$branch"
  echo "IOS_APPLE_SHA=$sha"
  echo "IOS_APPLE_DIR=$ios_dir_default"
  echo "MACOS_APPLE_BRANCH=$branch"
  echo "MACOS_APPLE_SHA=$sha"
  echo "MACOS_APPLE_DIR=${dir:-$macos_dir_default}"
}

# --- dispatch ----------------------------------------------------------------

action="${1:-}"
case "$action" in
  resolve)
    show
    ;;
  ls-remote)
    # Resolve the requested revision - or the default branch tip - and print it WITHOUT freezing
    # anything. This is the diagnostic form: it answers "what would a new build pick up?" without
    # becoming the answer for this one.
    #
    # An explicit override is honoured here exactly as `resolve` honours it. It was NOT, and the
    # executable check caught it: `APPLE_CLIENT_REF=A` still answered the branch tip, so the one
    # command a reader would use to ask "what does this ref resolve to?" ignored the ref.
    load_refs
    want="$(requested_ref)"
    [ -n "$want" ] || want="refs/heads/$APPLE_CLIENT_BRANCH"
    ls_remote_sha "$APPLE_CLIENT_REPOSITORY" "$want"
    ;;
  sha)     shift; platform_sha "${1:?usage: sha <ios|macos>}" ;;
  branch)  shift; platform_branch "${1:?usage: branch <ios|macos>}" ;;
  dir)     shift; platform_dir "${1:?usage: dir <ios|macos>}" "${2:-}" ;;
  checkout)
    shift
    case "${1:?usage: checkout <ios|macos> [dir]}" in
      ios)   checkout_ios "${2:-}" ;;
      macos) checkout_macos "${2:-}" ;;
      *) fail "unknown platform '$1' (expected ios or macos)" ;;
    esac
    ;;
  verify)
    shift
    verify "${1:?usage: verify <ios|macos> [dir]}" "${2:-}"
    ;;
  assert-shared-source)
    assert_shared_source
    ;;
  assert-on-branch)
    shift
    assert_on_branch "${1:-}"
    ;;
  ""|-h|--help)
    sed -n '2,72p' "$0" | sed 's/^# \{0,1\}//'
    ;;
  *)
    fail "unknown action '$action'"
    ;;
esac
