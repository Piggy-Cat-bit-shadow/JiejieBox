#!/usr/bin/env bash
# Selects the CI runs that built one commit, downloads their artifacts, and proves
# every platform in the release contract is present, unique, hashed and genuine.
#
# Usage:
#   release-artifacts.sh check-tag  --tag TAG
#   release-artifacts.sh plan       --repo R --sha SHA --out FILE [--interval N] [--timeout N]
#   release-artifacts.sh evidence   --repo R --sha SHA --out FILE [--interval N] [--timeout N]
#   release-artifacts.sh download   --repo R --plan FILE --dir DIR
#   release-artifacts.sh verify     --dir DIR --sha SHA
#   release-artifacts.sh flatten    --plan FILE --dir DIR --out DIR --tag TAG
#   release-artifacts.sh publish-flag --dry-run VALUE
#
# `plan` and `evidence` answer two different release questions and both must pass before
# anything is published: `plan` selects the run that produced each product artifact, and
# `evidence` proves the source and tooling gates succeeded at the same commit. See `cmd_evidence`.
#
# # Why this file exists
#
# The previous release workflow asked `gh run list --limit 60` for the runs on a
# commit and required the answer to be EXACTLY two rows, both `completed`, joined
# into the string "completed,completed". Every part of that is fragile:
#
#   * `--limit 60` is a window over the whole repository, so unrelated activity
#     pushes the target runs out of it and they read as "not built yet";
#   * selecting by workflow NAME breaks silently when a workflow is renamed;
#   * `count == 2` is unsatisfiable as soon as a workflow is re-run for the same
#     commit - which is routine here: 13 of the last 100 commits on each product
#     workflow carry two or three runs - and the loop then spins until its
#     90-minute deadline while both runs are green;
#   * a commit with NO runs at all took the same path, so the job burned the full
#     90 minutes before saying so.
#
# That is not hypothetical. Run 37811184607 (tag v1.15.0-alpha.7) printed
# "runs: 0" every 30 seconds from 16:44:51Z to 18:14:55Z and then failed with
# "did not complete within 90 minutes".
#
# This script replaces the whole thing:
#
#   * runs are queried BY WORKFLOW ID AND COMMIT SHA through the API, so an
#     unrelated run cannot displace them and a renamed file is caught by the
#     path assertion in resolve_workflow_id;
#   * the newest run per workflow decides. A completed non-success fails at once
#     - a failed or cancelled run is never papered over by an older green one;
#   * zero runs fails immediately with the exact dispatch command to run;
#   * only a run that is genuinely still executing is waited for, with an
#     explicit interval and deadline;
#   * downloads are not wrapped in `|| true`;
#   * each platform must independently have its binary, its .sha256, its
#     BUILD-INFO and a passing `sha256sum -c`, plus a byte-level check that the
#     binary is the architecture the name claims.
#
# Every branch below is exercised by scripts/ci/test-release-artifacts.sh against
# a mock `gh`, so the matrix can be verified without waiting for a real 90-minute
# release.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$script_dir/lib.sh"

die() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
note() { printf '%s\n' "$*"; }

# ---------------------------------------------------------------------------
# The release contract
# ---------------------------------------------------------------------------
#
# The products this fork ships from an automated build. There are FIVE deliverables:
#
#   from a target workflow   linux-amd64, macos-arm64, windows-core-amd64, android-arm64
#   staged separately        the Windows desktop installer (see `desktop`)
#
# `TARGET_WORKFLOWS` below holds two of the four workflow targets by DEFAULT, which is a
# fixture-focus decision and not the product contract: the unit fixtures build two products so
# they stay readable, and `release.yml` sets `RELEASE_EXTRA_PLATFORMS: true` at job level, which
# is what adds the Windows core and the Android libbox. So the default is two, the release is
# four, and a consumer must not read the default as "these are the platforms we ship".
#
# This comment used to say Windows, Android and the Apple IPA/DMG were "deliberately NOT here"
# and that adding them "is a product decision, not a bug fix". That stopped being true when
# `RELEASE_EXTRA_PLATFORMS` was introduced: the case statement directly below adds Windows and
# Android, and a release that shipped only Linux and macOS would now be the incomplete one. A
# comment that contradicts the code beneath it is how a reader concludes the wrong contract, so
# it is corrected rather than left as history.

TARGET_WORKFLOWS=(
  ".github/workflows/server-linux-amd64.yml"
  ".github/workflows/client-macos.yml"
)

# Adds the Windows core, the Android ARM64 libbox, and the Apple Libbox.
#
# Named `_EXTRA_` for the reason above - they are extra to the DEFAULT TARGET SET, not extra to
# the product.
#
# Apple is a release target even though it publishes no tarball of its own, because the release is
# the distribution point for the Apple core: `jiejiebox-libbox-<sha>` is what TestFlight consumes,
# and the whole reason it is promoted onto the release page is that a published TestFlight build
# can then be traced to the exact bytes named in `SHA256SUMS`. Leaving it out would have meant the
# Apple core had two possible sources and no single authoritative one.
case "${RELEASE_EXTRA_PLATFORMS:-false}" in
  false) ;;
  true)
    TARGET_WORKFLOWS+=(
      ".github/workflows/windows-core-amd64.yml"
      ".github/workflows/android-core-arm64.yml"
      ".github/workflows/client-apple.yml"
    )
    ;;
  *) die "RELEASE_EXTRA_PLATFORMS must be exactly true or false" ;;
esac

# The REQUIRED EVIDENCE gates: workflows that produce no release artifact but prove something the
# release depends on. They are a separate list from TARGET_WORKFLOWS on purpose - see `evidence`.
#
#   verify.yml                    the source gate: pinned-assumption tripwires, compileability,
#                                 build-tag policy, and the race coverage of the packages this
#                                 fork changed. Deliberately NOT under RELEASE_EXTRA_PLATFORMS:
#                                 source evidence is required for every release, and making it
#                                 part of the "extra platforms" switch would mean a Linux+macOS
#                                 release could skip it.
#   release-contract-verify.yml   proves the release tooling itself behaves, which is what makes
#                                 this script's own decisions trustworthy.
REQUIRED_EVIDENCE=(
  ".github/workflows/verify.yml"
  ".github/workflows/release-contract-verify.yml"
)

# target_field <workflow file> <field>
#
# One row per product, so a field cannot be added for one platform and forgotten
# for the other. An unknown workflow is an error rather than a default: a default
# here would let a third workflow slip into the release unnoticed.
target_field() {
  local wf="$1" field="$2"
  case "$wf:$field" in
    .github/workflows/server-linux-amd64.yml:binary)    echo "sing-box-linux-amd64" ;;
    .github/workflows/server-linux-amd64.yml:buildinfo) echo "BUILD-INFO-LINUX.txt" ;;
    .github/workflows/server-linux-amd64.yml:magic)     echo "elf-x86-64" ;;
    .github/workflows/server-linux-amd64.yml:platform)  echo "linux" ;;
    .github/workflows/server-linux-amd64.yml:arch)      echo "amd64" ;;
    .github/workflows/server-linux-amd64.yml:tarball)   echo "jiejie-sing-box-linux-amd64" ;;

    .github/workflows/client-macos.yml:binary)          echo "sing-box-darwin-arm64" ;;
    .github/workflows/client-macos.yml:buildinfo)       echo "BUILD-INFO-MACOS.txt" ;;
    .github/workflows/client-macos.yml:magic)           echo "macho-arm64" ;;
    .github/workflows/client-macos.yml:platform)        echo "darwin" ;;
    .github/workflows/client-macos.yml:arch)            echo "arm64" ;;
    .github/workflows/client-macos.yml:tarball)         echo "jiejie-sing-box-macos-arm64" ;;

    .github/workflows/windows-core-amd64.yml:binary)    echo "sing-box.exe" ;;
    .github/workflows/windows-core-amd64.yml:buildinfo) echo "BUILD-INFO-WINDOWS.txt" ;;
    .github/workflows/windows-core-amd64.yml:magic)     echo "pe-amd64" ;;
    .github/workflows/windows-core-amd64.yml:platform)  echo "windows" ;;
    .github/workflows/windows-core-amd64.yml:arch)      echo "amd64" ;;
    .github/workflows/windows-core-amd64.yml:tarball)   echo "jiejie-sing-box-windows-amd64" ;;

    .github/workflows/android-core-arm64.yml:binary)    echo "libbox.aar" ;;
    .github/workflows/android-core-arm64.yml:buildinfo) echo "BUILD-INFO-ANDROID.txt" ;;
    .github/workflows/android-core-arm64.yml:magic)     echo "aar-arm64" ;;
    .github/workflows/android-core-arm64.yml:platform)  echo "android" ;;
    .github/workflows/android-core-arm64.yml:arch)      echo "arm64" ;;
    .github/workflows/android-core-arm64.yml:tarball)   echo "jiejie-sing-box-android-arm64" ;;

    # `kind` is declared for EVERY target, including the four whose value is the default.
    #
    # The alternative - defaulting an absent `kind` to "binary" - reads as harmless and is not:
    # `target_field` fails closed on an unknown field by design, so a caller cannot ask a question
    # the contract does not answer. Teaching it that one particular field may be missing would put
    # a special case in the shared lookup, and the next target to forget a field would be silently
    # defaulted instead of refused. The failure this replaces was real and immediate: the first
    # runner run after Apple was added died on "no 'kind' for server-linux-amd64.yml", from a
    # lookup that was working exactly as intended.
    .github/workflows/server-linux-amd64.yml:kind)      echo "binary" ;;
    .github/workflows/client-macos.yml:kind)            echo "binary" ;;
    .github/workflows/windows-core-amd64.yml:kind)      echo "binary" ;;
    .github/workflows/android-core-arm64.yml:kind)      echo "binary" ;;

    # Apple is the one target whose verification is not "a binary plus a sidecar checksum", and the
    # `kind` field is what says so. It publishes `Libbox.xcframework` - a directory - and its
    # identity lives in a manifest the producer writes, not in a `.sha256` file beside a binary.
    # Generic code branches on `kind` rather than on the workflow name, so a second manifest-based
    # target would need no new special case.
    .github/workflows/client-apple.yml:kind)            echo "xcframework-manifest" ;;
    # `binary` names the DIRECTORY this target's payload artifact contains, and stays the key that
    # `download` names the staging directory after - the same "named for what it must contain" rule
    # the other four follow. It is not a file, and nothing looks for it as one.
    .github/workflows/client-apple.yml:binary)          echo "Libbox.xcframework" ;;
    # The manifest IS the build info: it carries parent_sha, jj_version, artifact_sha256 and slices,
    # and `verify` reads it through the producer's own validator rather than re-implementing it.
    .github/workflows/client-apple.yml:buildinfo)       echo "manifest.json" ;;
    .github/workflows/client-apple.yml:magic)           echo "zip" ;;
    .github/workflows/client-apple.yml:platform)        echo "apple" ;;
    .github/workflows/client-apple.yml:arch)            echo "arm64" ;;
    # NO tarball: the artifact already IS a tarball, repacking it would rebuild bytes the manifest
    # has already hashed, and the manifest's digest is what a consumer checks. `flatten` copies it.
    .github/workflows/client-apple.yml:tarball)         echo "" ;;

    # The exact name of the Actions artifact that carries this product's PUBLIC PAYLOAD.
    #
    # This is the field that turns artifact selection from a search into an identity. Before it,
    # `download` fetched every artifact of a run and required exactly one directory to appear,
    # and `download`/`verify` located the binary with `find -name`. Both are guesses about shape:
    # they happen to work while a producer uploads exactly one artifact of exactly one shape, and
    # they fail - or worse, silently pick the wrong one - the moment a producer changes what it
    # uploads. Which is precisely what the Android payload/evidence split does.
    #
    # SHA-derived rather than version-derived on purpose: `download` runs BEFORE the tag is
    # resolved and has no version, so a version-derived name would be unresolvable at the point
    # it is needed. Every producer therefore names its artifact with the commit it built.
    .github/workflows/server-linux-amd64.yml:payload_artifact)    echo "linux-amd64-$3" ;;
    .github/workflows/client-macos.yml:payload_artifact)          echo "macos-arm64-$3" ;;
    .github/workflows/windows-core-amd64.yml:payload_artifact)    echo "windows-core-amd64-$3" ;;
    .github/workflows/android-core-arm64.yml:payload_artifact)    echo "android-core-arm64-$3" ;;
    .github/workflows/client-apple.yml:payload_artifact)          echo "jiejiebox-libbox-$3" ;;

    # The evidence artifact, where a producer separates it from the payload. Empty when the
    # producer uploads one artifact, which is every target except Android today. `download`
    # treats empty as "nothing to fetch", so adding a split target is a one-line change here
    # rather than a second code path.
    .github/workflows/server-linux-amd64.yml:evidence_artifact)   echo "" ;;
    .github/workflows/client-macos.yml:evidence_artifact)         echo "" ;;
    .github/workflows/windows-core-amd64.yml:evidence_artifact)   echo "" ;;
    .github/workflows/android-core-arm64.yml:evidence_artifact)   echo "android-core-arm64-records-$3" ;;
    .github/workflows/client-apple.yml:evidence_artifact)         echo "" ;;

    *) die "the release contract has no '$field' for workflow '$wf'" ;;
  esac
}

# payload_artifact <workflow file> <sha>
#
# A convenience wrapper so callers do not have to remember the third argument. Fails closed if a
# target has no declared payload artifact, because a release whose artifact name is unknown is a
# release that cannot prove which bytes it is publishing.
payload_artifact() {
  local name
  name="$(target_field "$1" payload_artifact "$2")"
  [ -n "$name" ] || die "the release contract declares no payload_artifact for '$1'. Every target must name the artifact it publishes."
  printf '%s\n' "$name"
}

evidence_artifact() {
  target_field "$1" evidence_artifact "$2"
}

# Statuses GitHub may report. Anything that is not a completed run is waited for;
# anything completed that is not `success` fails the release.
is_in_flight() {
  case "$1" in
    queued | in_progress | pending | requested | waiting) return 0 ;;
    *) return 1 ;;
  esac
}

# ---------------------------------------------------------------------------
# GitHub access
# ---------------------------------------------------------------------------

# resolve_workflow_id <repo> <workflow file>
#
# Prints the numeric workflow id. The path returned by the API is asserted to be
# the file we named, so a workflow that was renamed or replaced by one with a
# different path fails here instead of silently resolving to a different product.
#
# The API accepts a workflow's file NAME or its numeric id but not a repository
# path, so only the basename is sent; the full path we expect is then asserted
# against what came back.
resolve_workflow_id() {
  local repo="$1" file="$2" row id path state
  if ! row="$(gh api "repos/$repo/actions/workflows/$(basename "$file")" --jq '[.id, .path, .state] | @tsv')"; then
    die "cannot resolve workflow '$file' in '$repo'. Check the repository name and that the workflow still exists."
  fi
  IFS=$'\t' read -r id path state <<<"$row"
  [ -n "${id:-}" ] || die "workflow '$file' resolved to an empty id"
  [ "$path" = "$file" ] || die \
    "workflow '$file' resolved to id $id at path '$path'. The release contract names workflows by file, so a moved or renamed workflow must be updated there deliberately."
  [ "$state" = "active" ] || die "workflow '$file' is '$state', not active"
  printf '%s\n' "$id"
}

# newest_run <repo> <workflow id> <sha>
#
# Prints one TSV row for the NEWEST run of that workflow on that exact commit,
# or nothing when the commit has no run of it. The query is filtered by
# head_sha server-side, so it does not matter how many other runs exist or how
# the API orders them.
newest_run() {
  local repo="$1" wfid="$2" sha="$3" rows
  # `.conclusion` is null while a run is in progress, and the API is documented to
  # use null rather than "" - but both are normalised to "-" here, because `//`
  # alone does NOT substitute for an empty string, and a tab-separated EMPTY field
  # is consumed by `read`: tab is IFS whitespace, so `read` collapses runs of it and
  # every later field shifts left by one, silently turning the workflow id into the
  # head SHA.
  local conclusion_field='(if (.conclusion // "") == "" then "-" else .conclusion end)'
  if ! rows="$(gh api --paginate \
      "repos/$repo/actions/workflows/$wfid/runs?head_sha=$sha&per_page=100" \
      --jq ".workflow_runs[] | [.id, .run_number, .status, $conclusion_field, .created_at, .html_url, .head_sha, .workflow_id] | @tsv")"; then
    die "the GitHub API query for workflow $wfid on $sha failed. Refusing to continue: an unreadable answer is not a green one."
  fi
  [ -n "$rows" ] || return 0

  local row
  while IFS= read -r row; do
    [ -n "$row" ] || continue
    # awk counts an empty field where bash `read` would silently consume it, so
    # this is the guard that notices a shift instead of trusting it.
    [ "$(printf '%s' "$row" | awk -F'\t' '{print NF}')" -eq 8 ] || die \
      "the API returned a run row with the wrong number of fields:
      $row"
    printf '%s\n' "$row"
  done <<<"$rows" | sort -t$'\t' -k2,2n -k1,1n | tail -n 1
}

# ---------------------------------------------------------------------------
# check-tag
# ---------------------------------------------------------------------------
#
# Validates the tag's shape, resolves it, and proves the commit it points at is
# the commit that was checked out - so the artifacts verified later cannot belong
# to a different commit than the tag being released.
#
# The shape check is also the injection guard: the tag reaches this script through
# an environment variable, and anything outside this alphabet is refused before it
# can be used in a path, a URL or an argument.
cmd_check_tag() {
  local tag=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --tag) tag="${2:-}"; shift 2 ;;
      *) die "check-tag: unknown argument '$1'" ;;
    esac
  done
  [ -n "$tag" ] || die "check-tag: --tag is required"

  case "$tag" in
    v[0-9]*) ;;
    *) die "tag '$tag' is not a release tag: it must start with 'v' followed by a digit" ;;
  esac
  # Only characters that are safe in a URL, a path and a git ref. This rejects
  # whitespace, ';', '$(', backticks, '/', '..' and a leading '-'.
  #
  # Matched with bash's own pattern matching rather than `printf | grep -q`: grep
  # exits as soon as it matches, which can kill the writer with SIGPIPE, and
  # `set -o pipefail` then reports the pipeline as FAILED - so the unsafe tag
  # would be the one that slipped through.
  if [[ "$tag" = *[!0-9A-Za-z._+-]* ]]; then
    die "tag '$tag' contains characters outside [0-9A-Za-z._+-]"
  fi
  case "$tag" in
    *..*) die "tag '$tag' contains '..'" ;;
  esac

  local tag_sha head_sha
  if ! tag_sha="$(git rev-parse --verify --quiet "refs/tags/$tag^{commit}")"; then
    die "tag '$tag' does not exist in this checkout, or does not point at a commit. Push the tag, or check out with fetch-depth 0 and fetch-tags."
  fi
  if ! head_sha="$(git rev-parse --verify --quiet HEAD)"; then
    die "cannot resolve HEAD in this checkout"
  fi
  [ "$tag_sha" = "$head_sha" ] || die \
    "tag '$tag' points at $tag_sha but the checked-out commit is $head_sha. Releasing would attach artifacts built from one commit to a tag naming another."

  printf '%s\n' "$tag_sha"
  note "tag $tag -> $tag_sha (matches the checked-out commit)"
}

# ---------------------------------------------------------------------------
# release-version
# ---------------------------------------------------------------------------
#
# Reads release/JIEJIE_VERSION from the checkout and proves it names the same release as
# the tag. This is the single authority for the fork's version, so it is read here once
# and passed down; nothing downstream keeps a copy that could go stale.
#
# The check is two-sided on purpose. A tag v0.1.6 whose checkout says 0.1.5 would otherwise
# publish artifacts named for 0.1.6 containing an app that believes it is 0.1.5 - the
# updater then refuses to upgrade to it, or offers it forever.

cmd_release_version() {
  local tag=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --tag) tag="${2:-}"; shift 2 ;;
      *) die "release-version: unknown argument '$1'" ;;
    esac
  done
  [ -n "$tag" ] || die "release-version: --tag is required"

  case "$tag" in
    v*) ;;
    *) die "release-version: tag '$tag' is not a release tag" ;;
  esac
  if [[ "$tag" = *[!0-9A-Za-z._+-]* ]]; then
    die "release-version: tag '$tag' contains characters outside [0-9A-Za-z._+-]"
  fi

  local version_file="release/JIEJIE_VERSION"
  [ -f "$version_file" ] || die \
    "no $version_file in this checkout. It is the single authority for the release version, so a release cannot proceed without it."
  local version
  version="$(tr -d '[:space:]' < "$version_file")"
  [ -n "$version" ] || die "$version_file is empty"
  if [[ "$version" = *[!0-9A-Za-z._+-]* ]]; then
    die "$version_file contains '$version', which has characters outside [0-9A-Za-z._+-]"
  fi

  # The tag names the version, so the two must be the same release. `v` plus the file.
  [ "$tag" = "v$version" ] || die \
    "tag '$tag' names version '${tag#v}', but $version_file says '$version'.
      The tag and the version source disagree, so any artifact named from one would
      misdescribe the other."

  printf '%s\n' "$version"
  note "$version_file = $version (matches tag $tag)"
}

# ---------------------------------------------------------------------------
# plan
# ---------------------------------------------------------------------------
#
# Waits for both product workflows on the commit and writes the selected runs to
# a plan file. The plan is the ONLY thing the download step reads, so the run that
# was judged is the run that is unpacked - the two steps cannot disagree.
#
# Plan columns: workflow_file, workflow_id, run_id, run_number, status,
#               conclusion, head_sha, html_url

cmd_plan() {
  local repo="" sha="" out="" interval=30 timeout=5400
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:-}"; shift 2 ;;
      --sha) sha="${2:-}"; shift 2 ;;
      --out) out="${2:-}"; shift 2 ;;
      --interval) interval="${2:-}"; shift 2 ;;
      --timeout) timeout="${2:-}"; shift 2 ;;
      *) die "plan: unknown argument '$1'" ;;
    esac
  done
  [ -n "$repo" ] || die "plan: --repo is required"
  [ -n "$sha" ] || die "plan: --sha is required"
  [ -n "$out" ] || die "plan: --out is required"
  case "$interval" in ''|*[!0-9]*) die "plan: --interval must be a whole number of seconds" ;; esac
  case "$timeout" in ''|*[!0-9]*) die "plan: --timeout must be a whole number of seconds" ;; esac

  mkdir -p "$(dirname "$out")"
  : > "$out"

  local deadline=$(( $(date +%s) + timeout ))
  local wf wfid newest pending
  while :; do
    : > "$out"
    pending=0
    for wf in "${TARGET_WORKFLOWS[@]}"; do
      wfid="$(resolve_workflow_id "$repo" "$wf")"
      newest="$(newest_run "$repo" "$wfid" "$sha")"

      if [ -z "$newest" ]; then
        # Fail fast with something to DO. The old workflow waited 90 minutes for
        # runs that were never going to appear: run 37811184607 sat on "runs: 0"
        # from 16:44:51Z until its deadline at 18:14:55Z.
        die "no run of $wf exists for commit $sha, so there is nothing to release.
      The release attaches artifacts that CI built; it does not build them.
      Request those builds for this commit, then dispatch the release again:
$(for f in "${TARGET_WORKFLOWS[@]}"; do printf '        gh workflow run %s --repo %s --ref %s\n' "$(basename "$f")" "$repo" "$sha"; done)"
      fi

      local rid rnum status conclusion created url rsha rwfid
      IFS=$'\t' read -r rid rnum status conclusion created url rsha rwfid <<<"$newest"

      # Defence in depth: the query filtered by SHA server-side, and every row is
      # checked again here, so a wrong artifact source cannot reach the release.
      # The shape checks catch a shifted parse (see newest_run) before the values
      # are compared, so the failure names the real problem.
      [[ "$rwfid" =~ ^[0-9]+$ ]] || die "could not read the workflow id of the newest run of $wf (got '$rwfid'); the API row was not in the expected shape:
      $newest"
      [[ "$rsha" =~ ^[0-9a-f]{40}$ ]] || die "could not read the commit of the newest run of $wf (got '$rsha'); the API row was not in the expected shape:
      $newest"
      [ "$rsha" = "$sha" ] || die "the newest run of $wf reports head_sha $rsha, not $sha"
      [ "$rwfid" = "$wfid" ] || die "the newest run of $wf reports workflow_id $rwfid, not $wfid"

      if is_in_flight "$status"; then
        note "  $wf: run $rid is $status - waiting up to ${timeout}s (every ${interval}s)"
        note "        $url"
        pending=$((pending + 1))
        continue
      fi
      if [ "$status" != "completed" ]; then
        die "the newest run of $wf for $sha is in unexpected status '$status' (run $rid). $url"
      fi
      case "$conclusion" in
        success) ;;
        skipped)
          die "the newest run of $wf for $sha was SKIPPED (run $rid). A skipped workflow did not verify anything, so it cannot gate a release. $url" ;;
        *)
          die "the newest run of $wf for $sha did not succeed: conclusion=$conclusion (run $rid). Re-run it and dispatch the release again. $url" ;;
      esac

      # The ninth column is the EXACT name of the artifact holding this product's public
      # payload. `download` reads it back so selection is an identity rather than a search.
      printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
        "$wf" "$wfid" "$rid" "$rnum" "$status" "$conclusion" "$sha" "$url" \
        "$(payload_artifact "$wf" "$sha")" >> "$out"
      note "  $wf: selected run $rid (#$rnum) $conclusion  payload artifact $(payload_artifact "$wf" "$sha")  $url"
    done

    [ "$pending" -eq 0 ] && break

    if [ "$(date +%s)" -ge "$deadline" ]; then
      die "timed out after ${timeout}s waiting for ${pending} of ${#TARGET_WORKFLOWS[@]} product workflow(s) on $sha to finish. The runs above are still going; check them and dispatch the release again."
    fi
    sleep "$interval"
  done

  local lines
  lines="$(grep -c . "$out" || true)"
  [ "$lines" -eq "${#TARGET_WORKFLOWS[@]}" ] || die \
    "the release plan has $lines row(s), expected ${#TARGET_WORKFLOWS[@]}. Refusing to release a partial product."

  note "PASS: every product workflow succeeded for $sha"
}

# ---------------------------------------------------------------------------
# evidence
# ---------------------------------------------------------------------------
#
# Resolves the REQUIRED EVIDENCE gates at the candidate SHA and proves each one succeeded.
#
# # Why this is a separate command from `plan`
#
# `plan` answers "which run produced each product artifact". This answers a different question -
# "was the source and the release tooling PROVED at this commit" - and the two have different
# members: evidence gates are workflows that produce no release artifact at all. Folding them into
# `plan`'s table would make one file answer two questions, and `plan`'s row count is a contract
# (it must equal TARGET_WORKFLOWS, and the 92-case suite asserts the exact run ids it contains).
# A separate output keeps both contracts intact.
#
# # Why the release needs this
#
# Before this, a candidate SHA could be released with Verify cancelled by a later push and
# Release Contract Verification never dispatched, because nothing in the release path named
# either of them. The product workflows were required; the gates that prove the source and the
# tooling were not. This closes that: the plan phase now fails if either gate is missing or not
# successful at exactly this commit.
#
# # Selection semantics
#
# Deliberately the same as `newest_run`'s, because two selectors that disagree are how a release
# ends up judged by rules it was not built under:
#
#   newest exact-SHA run wins;  newest not success -> FAIL;  never fall back to an older green.
#
# The retired second authority (`verify-release-acceptance.sh`) chose differently - it scanned the
# admissible runs and took the first that was `success`, so an older green could satisfy it while a
# newer run at the same commit had failed. That is the opposite of the rule here, and it is one
# reason it is the wrong script to be the release's authority.
#
# # Per-job verification
#
# A run can be `success` while one of its jobs was skipped, and a skipped job proves nothing. So
# every job of every evidence run must be `completed` and `success`. `neutral` is accepted for a
# job explicitly listed by the workflow as optional, because that is what `continue-on-error`-free
# optional steps report; everything else - skipped, cancelled, timed_out, failure - is a FAIL.

# job_conclusions <repo> <run id>
#
# Prints one TAB-separated "name<TAB>status<TAB>conclusion" row per job. Fails closed: an
# unreadable job list is not a green one.
job_conclusions() {
  local repo="$1" rid="$2" rows
  if ! rows="$(gh api "repos/$repo/actions/runs/$rid/jobs?per_page=100" \
      --jq '.jobs[] | [.name, .status, (.conclusion // "-")] | @tsv')"; then
    die "cannot read the jobs of run $rid. An unreadable answer is not a green one."
  fi
  printf '%s\n' "$rows"
}

cmd_evidence() {
  local repo="" sha="" out="" interval=30 timeout=5400
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:-}"; shift 2 ;;
      --sha) sha="${2:-}"; shift 2 ;;
      --out) out="${2:-}"; shift 2 ;;
      --interval) interval="${2:-}"; shift 2 ;;
      --timeout) timeout="${2:-}"; shift 2 ;;
      *) die "evidence: unknown argument '$1'" ;;
    esac
  done
  [ -n "$repo" ] || die "evidence: --repo is required"
  [ -n "$sha" ] || die "evidence: --sha is required"
  [ -n "$out" ] || die "evidence: --out is required"
  case "$interval" in ''|*[!0-9]*) die "evidence: --interval must be a whole number of seconds" ;; esac
  case "$timeout" in ''|*[!0-9]*) die "evidence: --timeout must be a whole number of seconds" ;; esac
  [ "${#REQUIRED_EVIDENCE[@]}" -gt 0 ] || die \
    "evidence: REQUIRED_EVIDENCE is empty, so this command would prove nothing. That is a defect in the release contract, not a release condition."

  mkdir -p "$(dirname "$out")"
  : > "$out"

  local deadline=$(( $(date +%s) + timeout ))
  local wf wfid newest pending
  while :; do
    : > "$out"
    pending=0
    for wf in "${REQUIRED_EVIDENCE[@]}"; do
      wfid="$(resolve_workflow_id "$repo" "$wf")"
      newest="$(newest_run "$repo" "$wfid" "$sha")"

      if [ -z "$newest" ]; then
        # Fail fast, exactly as `plan` does for a product workflow: a gate that was never run at
        # this commit is not going to appear while the release waits.
        die "no run of $wf exists for commit $sha, so the source/tooling evidence for this
      release is absent. The release publishes bytes CI already proved; it does not prove them.
      Dispatch the gate for this commit, then dispatch the release again:
        gh workflow run $(basename "$wf") --repo $repo --ref $sha"
      fi

      local rid rnum status conclusion url rsha rwfid
      IFS=$'\t' read -r rid rnum status conclusion _ url rsha rwfid <<<"$newest"

      [[ "$rwfid" =~ ^[0-9]+$ ]] || die "could not read the workflow id of the newest run of $wf (got '$rwfid'); the API row was not in the expected shape:
      $newest"
      [[ "$rsha" =~ ^[0-9a-f]{40}$ ]] || die "could not read the commit of the newest run of $wf (got '$rsha'); the API row was not in the expected shape:
      $newest"
      [ "$rsha" = "$sha" ] || die "the newest run of $wf reports head_sha $rsha, not $sha"
      [ "$rwfid" = "$wfid" ] || die "the newest run of $wf reports workflow_id $rwfid, not $wfid"

      if is_in_flight "$status"; then
        note "  $wf: run $rid is $status - waiting up to ${timeout}s (every ${interval}s)"
        note "        $url"
        pending=$((pending + 1))
        continue
      fi
      if [ "$status" != "completed" ]; then
        die "the newest run of $wf for $sha is in unexpected status '$status' (run $rid). $url"
      fi
      case "$conclusion" in
        success) ;;
        skipped)
          die "the newest run of $wf for $sha was SKIPPED (run $rid). A skipped gate did not prove anything, so it cannot gate a release. $url" ;;
        *)
          die "the newest run of $wf for $sha did not succeed: conclusion=$conclusion (run $rid). Re-run it and dispatch the release again. $url" ;;
      esac

      # Every job, not just the run. A run is `success` with a skipped job, and a skipped job
      # proves nothing about the fact this gate exists for.
      local job name jstatus jconclusion bad=""
      while IFS=$'\t' read -r name jstatus jconclusion; do
        [ -n "$name" ] || continue
        if [ "$jstatus" != "completed" ]; then
          bad="${bad}${name} (${jstatus})"$'\n'
        elif [ "$jconclusion" != "success" ] && [ "$jconclusion" != "neutral" ]; then
          bad="${bad}${name} (${jconclusion})"$'\n'
        fi
      done < <(job_conclusions "$repo" "$rid")
      if [ -n "$bad" ]; then
        die "run $rid of $wf succeeded, but not every job in it did:
$(printf '      %s\n' $bad)      A run whose job was skipped or failed did not prove the fact this gate exists for.
      $url"
      fi

      printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
        "$wf" "$wfid" "$rid" "$rnum" "$status" "$conclusion" "$sha" "$url" >> "$out"
      note "  $wf: evidence run $rid (#$rnum) $conclusion, every job successful  $url"
    done

    [ "$pending" -eq 0 ] && break

    if [ "$(date +%s)" -ge "$deadline" ]; then
      die "timed out after ${timeout}s waiting for ${pending} of ${#REQUIRED_EVIDENCE[@]} required evidence gate(s) on $sha to finish."
    fi
    sleep "$interval"
  done

  local lines
  lines="$(grep -c . "$out" || true)"
  [ "$lines" -eq "${#REQUIRED_EVIDENCE[@]}" ] || die \
    "the evidence plan has $lines row(s), expected ${#REQUIRED_EVIDENCE[@]}. Refusing to release on partial evidence."

  note "PASS: every required evidence gate succeeded, every job included, for $sha"
}

# ---------------------------------------------------------------------------
# download
# ---------------------------------------------------------------------------

cmd_download() {
  local repo="" plan="" dir="" evidence_dir=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:-}"; shift 2 ;;
      --plan) plan="${2:-}"; shift 2 ;;
      --dir) dir="${2:-}"; shift 2 ;;
      # Where a producer's separated EVIDENCE artifact goes. Deliberately a different tree from
      # --dir: the payload tree is what becomes the published tarballs, and the whole point of
      # the split is that evidence must never be able to reach it.
      --evidence-dir) evidence_dir="${2:-}"; shift 2 ;;
      *) die "download: unknown argument '$1'" ;;
    esac
  done
  [ -n "$repo" ] || die "download: --repo is required"
  [ -n "$plan" ] || die "download: --plan is required"
  [ -n "$dir" ] || die "download: --dir is required"
  [ -s "$plan" ] || die "download: the release plan '$plan' is empty; nothing was verified for this commit"
  if [ -n "$evidence_dir" ]; then
    rm -rf "$evidence_dir"
    mkdir -p "$evidence_dir"
  fi

  rm -rf "$dir"
  mkdir -p "$dir"

  local rows=0
  local wf wfid rid rnum status conclusion sha url artifact evidence dest subdirs files row
  while IFS= read -r row; do
    [ -n "$row" ] || continue
    # See newest_run: an empty field would shift the rest of the row, and here that
    # would mean downloading a run that was never verified. awk counts an empty
    # field where `read` would silently consume it.
    #
    # Nine fields: the first eight are the run identity, the ninth is the payload artifact name.
    # A plan written before the ninth column existed is refused rather than guessed at, because
    # a plan is an input like any other and "the field is missing so I will search instead" is
    # how a release ends up publishing bytes it never identified.
    [ "$(printf '%s' "$row" | awk -F'\t' '{print NF}')" -eq 9 ] || die \
      "the release plan has a malformed row (expected 9 fields: run identity plus the payload
      artifact name). Re-run 'plan' with this version of the gate:
      $row"
    IFS=$'\t' read -r wf wfid rid rnum status conclusion sha url artifact <<<"$row"
    [ -n "$artifact" ] || die "the release plan names no payload artifact for $wf. Refusing to guess which artifact to publish."
    binary="$(target_field "$wf" binary)"
    # One directory per product, named for the binary it must contain, so a
    # second run of the other workflow can never overwrite this one.
    dest="$dir/$binary"
    mkdir -p "$dest"
    note "downloading run $rid ($wf) artifact '$artifact' -> $dest"
    # EXACT NAME, no `--dir` sweep. `gh run download <id> --dir` fetches every artifact of the
    # run and the old code then required exactly one directory to appear - a shape assumption
    # that a producer adding a second artifact (which Android now does) turns into a hard
    # failure, and that a producer RENAMING its artifact turns into a silent mismatch.
    # NOT wrapped in `|| true`: a download that failed leaves the artifact unverified, and an
    # unverified artifact must not be publishable.
    # `gh run download --name X --dir D` extracts the artifact's CONTENTS into D. It does not
    # create a D/X directory: the zip GitHub stores an artifact as holds the payload files at its
    # top level, and gh unpacks it into the destination as given. Every consumer downstream -
    # `verify`, `flatten`, `check-upload` - addresses the payload as `$dir/$binary/$artifact`, so
    # `download` MATERIALISES that directory here rather than the gate being rewritten around a
    # layout it cannot produce.
    #
    # The previous assertion required exactly one subdirectory named after the artifact, directly
    # after the download. Against the real API that was unsatisfiable from the day it was written,
    # and it survived because the test suite's mock gh fabricated `D/X/` with shutil.copytree - so
    # the mock and the gate agreed with each other and neither agreed with GitHub. The release had
    # never been executed, so nothing noticed until the dry run:
    #
    #     FAIL: downloading artifact 'linux-amd64-<sha>' of run <id> produced 0 directories
    #           in release-assets/sing-box-linux-amd64, expected exactly 1.
    #
    # Unpacking into a scratch directory and renaming it is deliberate rather than downloading
    # straight into `$dest/$artifact`: a rename cannot leave the payload half-written under the name
    # the contract publishes, and it keeps the "did anything unexpected land here" check below
    # meaningful by failing BEFORE the rename when the download produced a different shape.
    scratch="$dest/.unpack-$$"
    rm -rf "$scratch"
    mkdir -p "$scratch"
    if ! gh run download "$rid" --repo "$repo" --name "$artifact" --dir "$scratch"; then
      rm -rf "$scratch"
      die "gh run download failed for run $rid ($wf), artifact '$artifact'. $binary is NOT verified; refusing to continue.
      If the run predates the artifact naming this contract expects, re-dispatch that workflow."
    fi
    files="$(find "$scratch" -type f | wc -l | tr -d ' ')"
    [ "$files" -gt 0 ] || {
      rm -rf "$scratch"
      die "run $rid artifact '$artifact' downloaded no files. The download reported success, so this is not the payload the artifact contract describes."
    }
    subdirs="$(find "$scratch" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
    [ "$subdirs" -eq 0 ] || {
      rm -rf "$scratch"
      die "run $rid artifact '$artifact' unpacked $subdirs director(y/ies) at its top level, expected the payload's files. One artifact, one payload."
    }
    rm -rf "$dest/$artifact"
    mv "$scratch" "$dest/$artifact"

    # The producer's separated evidence, when it declares one. It goes to its own tree and is
    # NOT part of the payload staging, so no later step can publish it by accident.
    evidence="$(evidence_artifact "$wf" "$sha")"
    if [ -n "$evidence" ]; then
      [ -n "$evidence_dir" ] || die \
        "$wf separates its payload from its evidence (artifact '$evidence'), but download was not given --evidence-dir. Refusing to silently drop the evidence."
      note "downloading run $rid ($wf) evidence '$evidence' -> $evidence_dir"
      # Same layout rule, and the same materialisation, as the payload above.
      scratch="$evidence_dir/.unpack-$$"
      rm -rf "$scratch"
      mkdir -p "$scratch"
      if ! gh run download "$rid" --repo "$repo" --name "$evidence" --dir "$scratch"; then
        rm -rf "$scratch"
        die "gh run download failed for run $rid ($wf), evidence artifact '$evidence'. The payload is verified but its build records are not; refusing to continue."
      fi
      rm -rf "$evidence_dir/$evidence"
      mv "$scratch" "$evidence_dir/$evidence"
    fi

    rows=$((rows + 1))
  done < "$plan"

  [ "$rows" -eq "${#TARGET_WORKFLOWS[@]}" ] || die \
    "downloaded $rows run(s), expected ${#TARGET_WORKFLOWS[@]}"
  note "PASS: downloaded the exact payload artifact of $rows verified run(s)"
}

# ---------------------------------------------------------------------------
# desktop
# ---------------------------------------------------------------------------
#
# Stages the Windows desktop installer for the release.
#
# Unlike the four tarball products, the desktop workflow is dispatch-only and produces a
# signed-test installer that is NOT a tarball, so it cannot go through the plan/download
# pair: there is no run to select before one has been dispatched. It is therefore required
# to already have a successful run on the commit being released, and this step finds it,
# waits for it if it is still going, downloads it and stages the artifact directory.
#
# Two things are deliberately NOT done here:
#
#   * the workflow is not dispatched. A release that starts a 90-minute Windows build as a
#     side effect of being published is a release whose inputs were not reviewed; the run
#     is required to exist, and the failure message says which command produces one.
#   * the artifact directory is not filtered. The whole thing is staged, because the build
#     record has to be read to prove the installer's core, and `check-upload` is what keeps
#     the record out of the published set.

DESKTOP_WORKFLOW=".github/workflows/client-desktop-windows.yml"

cmd_desktop() {
  local repo="" sha="" out="" interval=30 timeout=5400 v=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:-}"; shift 2 ;;
      --sha) sha="${2:-}"; shift 2 ;;
      --out) out="${2:-}"; shift 2 ;;
      --interval) interval="${2:-}"; shift 2 ;;
      --timeout) timeout="${2:-}"; shift 2 ;;
      *) die "desktop: unknown argument '$1'" ;;
    esac
  done
  [ -n "$repo" ] || die "desktop: --repo is required"
  [ -n "$sha" ] || die "desktop: --sha is required"
  [ -n "$out" ] || die "desktop: --out is required"

  local wfid
  wfid="$(resolve_workflow_id "$repo" "$DESKTOP_WORKFLOW")"

  rm -rf "$out"
  mkdir -p "$out"

  local started now elapsed row rid rnum status conclusion created url head_sha rwfid
  started="$(date +%s)"
  while :; do
    row="$(newest_run "$repo" "$wfid" "$sha")"
    if [ -z "$row" ]; then
      die "no run of $DESKTOP_WORKFLOW exists for $sha, so there is no verified Windows installer to publish.
      Dispatch it first, then re-run this release:
        gh workflow run client-desktop-windows.yml --repo $repo \\
          -f core_sha=$sha -f core_version=<version> -f tooling_sha=$sha
      tooling_sha must equal core_sha: the candidate carries the tooling that builds and
      judges it, and the workflow refuses a dispatch where the two differ.
      A release that silently omitted the installer would still publish, which is why this is fatal."
    fi
    [ "$(printf '%s' "$row" | awk -F'\t' '{print NF}')" -eq 8 ] || die \
      "the API returned a desktop run row with the wrong number of fields:
      $row"
    IFS=$'\t' read -r rid rnum status conclusion created url head_sha rwfid <<<"$row"

    if ! is_in_flight "$status"; then
      break
    fi
    now="$(date +%s)"
    elapsed=$((now - started))
    if [ "$elapsed" -ge "$timeout" ]; then
      die "timed out after ${timeout}s waiting for the desktop run on $sha.
      run $rid is still '$status': $url"
    fi
    note "waiting for desktop run $rid (status $status) on $sha; ${elapsed}s elapsed"
    sleep "$interval"
  done

  [ "$head_sha" = "$sha" ] || die \
    "desktop run $rid is for $head_sha, but this release is $sha."
  case "$conclusion" in
    success) ;;
    *) die "the desktop run for $sha concluded '$conclusion', not success.
      run $rid: $url
      An unverified installer must not be published." ;;
  esac

  # The run uploads TWO artifacts: the installer, and the build records alongside it. They
  # have to be separated here because only the installer is a deliverable - the record is
  # read to prove the installer's core and must NOT be published.
  #
  # The artifact carrying the installer is selected by SHAPE, not by version. On v0.1.6 the
  # artifact is named `jiejiebox-windows-x64-0.1.5-signed-test` while the installer inside it
  # is `Jiejiebox-v0.1.6-windows-x64.exe` and the release is v0.1.6: the artifact name is a
  # third carrier of the version and it was stale. Matching on the version would therefore
  # have found nothing at all on the release this code was written for, which is simply how
  # it was found - by running it against v0.1.6 rather than against a fixture.
  local listing row aname aexpired
  listing="$(gh api "repos/$repo/actions/runs/$rid/artifacts?per_page=100" \
    --jq '.artifacts[] | [.name, (.expired|tostring)] | @tsv')" || die \
    "cannot list the artifacts of desktop run $rid. An unreadable answer is not a green one."

  local installer_artifact="" records_artifact=""
  # Read line by line with the tab as the field separator. `for row in $listing` would split
  # a TSV row into its two words and mispair a name with the next row's expiry.
  while IFS=$'\t' read -r aname aexpired; do
    [ -n "$aname" ] || continue
    case "$aexpired" in
      true) die "desktop artifact '$aname' on run $rid has expired. Expired artifacts cannot be released." ;;
    esac
    case "$aname" in
      *-signed-test) installer_artifact="$aname" ;;
      *build-records*) records_artifact="$aname" ;;
    esac
  done < <(printf '%s\n' "$listing")

  [ -n "$installer_artifact" ] || die "desktop run $rid has no *-signed-test artifact. Artifacts found:
$(printf '      %s\n' $listing)"
  [ -n "$records_artifact" ] || die "desktop run $rid has no *build-records* artifact, so the installer's core cannot be proven. A name is not provenance. Artifacts found:
$(printf '      %s\n' $listing)"

  # The installer is staged in the layout the release publishes: inside a directory named
  # after the artifact, so the run's structure survives into the staging area and the
  # allowlist below has one directory to reason about. The records are kept APART, because
  # they are read and then discarded - putting them here would make them publishable.
  #
  # `$out.records` is a sibling of `$out`, so the two trees stay obviously paired and the
  # records never sit inside the directory the release is built from.
  local records_dir="${out}.records"
  # Removed, then both trees created: the download writes into them, so a leftover tree from
  # an earlier run must not be able to satisfy the checks below.
  rm -rf "$out" "$records_dir"
  # The `note` on the next line was a second command on the `mkdir` line, which is why it had
  # stopped printing; split, so the log says what is about to be downloaded.
  mkdir -p "$out" "$records_dir"
  note "downloading desktop artifact '$installer_artifact' from run $rid"
  if ! gh run download "$rid" --repo "$repo" --name "$installer_artifact" --dir "$out"; then
    die "gh run download failed for '$installer_artifact' on desktop run $rid. The installer is NOT verified; refusing to continue."
  fi
  note "downloading desktop artifact '$records_artifact' from run $rid (read, never published)"
  if ! gh run download "$rid" --repo "$repo" --name "$records_artifact" --dir "$records_dir"; then
    die "gh run download failed for '$records_artifact' on desktop run $rid. Without the build record the installer's core cannot be proven; refusing to continue."
  fi

  # Make the staging layout the one the next check and release.yml both declare: the installer
  # inside a directory named after the artifact it came in.
  #
  # Both shapes have to be accepted, and neither is hypothetical. This suite's mock copies the
  # artifact into a directory of its own name - "`gh run download --name` nests everything one
  # level down", in the words of the test that asserts it - while the v0.1.7 run left
  # `release-desktop` holding the installer directly, with no directory under it, which is what
  # the check below rejected. Written for the runner alone this step is a no-op in the fixture;
  # written for the fixture alone it is a no-op on the runner and the release dies unstaged.
  #
  # So the installer is LOCATED rather than assumed: searched for anywhere under $out, required
  # to number exactly one, and moved into the artifact-named directory only when it is not
  # already there. Exactly one copy is required because a second is precisely what the
  # allowlist below exists to reject, and it is better named here than three steps later.
  #
  # `find` is deliberately not piped into `head`: this script runs under `set -o pipefail`, and
  # a `head` that exits after one line can leave find writing into a closed pipe, which would
  # fail the assignment for a reason that has nothing to do with the artifact.
  local nested="$out/$installer_artifact" installer_path found_installers installer_count
  mkdir -p "$nested"
  found_installers="$(find "$out" -type f -name 'Jiejiebox-v*-windows-x64.exe')"
  installer_count="$(printf '%s\n' "$found_installers" | grep -c . || true)"
  [ "$installer_count" -eq 1 ] || die \
    "the desktop artifact '$installer_artifact' yielded $installer_count copies of
      Jiejiebox-v*-windows-x64.exe under $out, expected exactly 1. This step stages one
      installer, and a second copy is exactly what the public allowlist would reject later."
  installer_path="$found_installers"
  if [ "$installer_path" != "$nested/$(basename "$installer_path")" ]; then
    mv "$installer_path" "$nested/"
  fi
  # Whatever else the download left beside the artifact directory would reach the allowlist as
  # a stray file, so it is reported rather than silently dropped.
  local leftover
  leftover="$(find "$out" -mindepth 1 -maxdepth 1 -not -name "$installer_artifact")"
  [ -z "$leftover" ] || die \
    "the staging directory holds more than the installer's own artifact directory:
$(printf '      %s\n' $leftover)
      Only the installer is a public deliverable, so this is refused rather than pruned."

  local subdirs files
  subdirs="$(find "$out" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
  [ "$subdirs" -eq 1 ] || die \
    "desktop artifact '$installer_artifact' produced $subdirs directories in $out, expected exactly 1."
  files="$(find "$out" -type f | wc -l | tr -d ' ')"
  [ "$files" -gt 0 ] || die "desktop artifact '$installer_artifact' downloaded no files into $out"
  files="$(find "$records_dir" -type f -name 'BUILD-INFO-WINDOWS-DESKTOP-*.txt' | wc -l | tr -d ' ')"
  [ "$files" -ge 1 ] || die \
    "desktop artifact '$records_artifact' carries no BUILD-INFO-WINDOWS-DESKTOP-*.txt under $records_dir, so the installer cannot be attributed to a core. Refusing to continue."
  note "PASS: desktop run $rid staged ($(find "$out" -type f | wc -l | tr -d ' ') file(s)); records in $records_dir"
}

# ---------------------------------------------------------------------------
# verify
# ---------------------------------------------------------------------------

# check_sha256_file <dir> <sumfile basename>
#
# Actually verifies, in the directory the sumfile describes. `sha256sum -c` on the
# Linux runner, `shasum -a 256 -c` here, so the same check can be run locally.
check_sha256_file() {
  local dir="$1" sum="$2"
  if command -v sha256sum >/dev/null 2>&1; then
    ( cd "$dir" && sha256sum -c "$sum" )
  elif command -v shasum >/dev/null 2>&1; then
    ( cd "$dir" && shasum -a 256 -c "$sum" )
  else
    echo "FAIL: neither sha256sum nor shasum is available to verify $sum" >&2
    return 1
  fi
}

# check_magic <file> <kind>
#
# Reads the architecture out of the FILE, so a binary that is present, hashed and
# documented but built for the wrong platform cannot pass on the strength of its
# name. `od -N` is used rather than `head -c`: an early-exiting reader on the other
# end of a pipe raises SIGPIPE, which `set -o pipefail` reports as a failure.
check_magic() {
  local file="$1" kind="$2" hex
  hex="$(od -An -tx1 -N 20 "$file" | tr -d ' \n')"
  case "$kind" in
    elf-x86-64)
      [ "${hex:0:8}" = "7f454c46" ] || { echo "FAIL: $(basename "$file") is not an ELF file (magic '${hex:0:8}')" >&2; return 1; }
      [ "${hex:36:4}" = "3e00" ] || { echo "FAIL: $(basename "$file") is ELF but not x86-64 (e_machine '${hex:36:4}', want '3e00')" >&2; return 1; }
      ;;
    macho-arm64)
      [ "${hex:0:8}" = "cffaedfe" ] || { echo "FAIL: $(basename "$file") is not a 64-bit little-endian Mach-O (magic '${hex:0:8}')" >&2; return 1; }
      [ "${hex:8:8}" = "0c000001" ] || { echo "FAIL: $(basename "$file") is Mach-O but not arm64 (cputype '${hex:8:8}', want '0c000001')" >&2; return 1; }
      ;;
    pe-amd64)
      python3 - "$file" <<'PY'
import struct, sys
data = open(sys.argv[1], 'rb').read()
if data[:2] != b'MZ' or len(data) < 0x40:
    raise SystemExit(1)
offset = struct.unpack_from('<I', data, 0x3c)[0]
if data[offset:offset + 4] != b'PE\\0\\0' or struct.unpack_from('<H', data, offset + 4)[0] != 0x8664:
    raise SystemExit(1)
PY
      ;;
    aar-arm64)
      local extracted
      extracted="$(mktemp)"
      if ! unzip -p "$file" 'jni/arm64-v8a/libbox.so' >"$extracted" 2>/dev/null; then
        rm -f "$extracted"
        return 1
      fi
      hex="$(od -An -tx1 -N 20 "$extracted" | tr -d ' \n')"
      rm -f "$extracted"
      [ "${hex:0:8}" = "7f454c46" ] && [ "${hex:36:4}" = "b700" ] || return 1
      ;;
    *) echo "FAIL: unknown platform magic '$kind'" >&2; return 1 ;;
  esac
  return 0
}

# count_files <find args...> - prints the number of matches, 0 when there are none.
count_files() {
  local n
  n="$(find "$@" | wc -l | tr -d ' ')"
  printf '%s\n' "$n"
}

# verify_platform <dir> <sha> <workflow file>
#
# Every requirement is enforced PER PLATFORM. A hash for Linux does not stand in
# for a hash for macOS, which is what the old single loop allowed.
verify_platform() {
  local dir="$1" sha="$2" wf="$3"
  local binary buildinfo magic platform arch
  binary="$(target_field "$wf" binary)"
  buildinfo="$(target_field "$wf" buildinfo)"
  magic="$(target_field "$wf" magic)"
  platform="$(target_field "$wf" platform)"
  arch="$(target_field "$wf" arch)"

  note "--- $platform/$arch ($wf)"

  local n bin sum info
  n="$(count_files "$dir" -type f -name "$binary")"
  [ "$n" -eq 1 ] || die "$binary: found $n copies under $dir, expected exactly 1"
  bin="$(find "$dir" -type f -name "$binary")"

  n="$(count_files "$dir" -type f -name "$binary.sha256")"
  [ "$n" -eq 1 ] || die "$binary.sha256: found $n copies under $dir, expected exactly 1. Every platform must carry its own checksum."
  sum="$(find "$dir" -type f -name "$binary.sha256")"

  n="$(count_files "$dir" -type f -name "$buildinfo")"
  [ "$n" -eq 1 ] || die "$buildinfo: found $n copies under $dir, expected exactly 1"
  info="$(find "$dir" -type f -name "$buildinfo")"

  local bin_dir sum_dir info_dir
  bin_dir="$(dirname "$bin")"
  sum_dir="$(dirname "$sum")"
  info_dir="$(dirname "$info")"
  [ "$bin_dir" = "$sum_dir" ] && [ "$bin_dir" = "$info_dir" ] || die \
    "$binary, $binary.sha256 and $buildinfo are not in the same artifact directory. They must travel together."

  # Empty files are a real failure mode of an interrupted upload.
  local size
  size="$(wc -c < "$bin" | tr -d ' ')"
  [ "$size" -gt 0 ] || die "$binary is empty (0 bytes)"

  check_magic "$bin" "$magic" || die "$binary is not a $platform/$arch executable"

  check_sha256_file "$bin_dir" "$(basename "$sum")" || die \
    "$binary.sha256 does not match $binary. The published checksum would not verify the published binary."

  # The sidecar must name THIS commit. A binary from a different commit can share
  # the artifact name, so the recorded commit is what ties the bytes to the tag.
  local recorded
  recorded="$(sed -n 's/^git_commit:[[:space:]]*//p' "$info")"
  [ -n "$recorded" ] || die "$buildinfo has no git_commit line"
  [ "$recorded" = "$sha" ] || die \
    "$buildinfo records git_commit $recorded, but the release is verifying $sha. These bytes were built from a different commit."

  local recorded_platform recorded_arch
  recorded_platform="$(sed -n 's/^platform:[[:space:]]*//p' "$info")"
  recorded_arch="$(sed -n 's/^arch:[[:space:]]*//p' "$info")"
  [ "$recorded_platform" = "$platform" ] || die "$buildinfo records platform '$recorded_platform', expected '$platform'"
  [ "$recorded_arch" = "$arch" ] || die "$buildinfo records arch '$recorded_arch', expected '$arch'"

  # The artifact's own hash, as recorded at build time, must agree with the bytes
  # that were downloaded. This catches a truncated or substituted transfer.
  local recorded_sha actual_sha
  recorded_sha="$(sed -n 's/^artifact_sha256:[[:space:]]*//p' "$info")"
  [ -n "$recorded_sha" ] || die "$buildinfo has no artifact_sha256 line"
  actual_sha="$(sha256_of "$bin")"
  [ "$recorded_sha" = "$actual_sha" ] || die \
    "$buildinfo records artifact_sha256 $recorded_sha, but the downloaded $binary hashes to $actual_sha"

  note "    $binary  $size bytes  sha256=$actual_sha"
  note "    verified: binary, checksum, build info, platform magic, commit $sha"
}

# # Why the Apple file set has exactly four names
#
# `Libbox.xcframework.tar.gz` is the payload; `manifest.json` is the machine-readable identity;
# `SHA256SUMS-LIBBOX.txt` is its checksum list; `BUILD-INFO-LIBBOX.txt` is the same facts in prose.
#
# The prose record is redundant for a program and is what a person reads when a release is being
# diagnosed, so it stays. It was ALSO the one name missing from this list on the first runner run,
# and the assertion reported both sides - four found against three wanted - which is why the
# message prints the expected and actual sets rather than only refusing. Narrowing the list to
# match what was there would have green'd the run and quietly dropped a published record, which is
# the failure mode a two-sided assertion exists to make visible.
#
# # The unresolved PRESENT_INVALID proof
#
# `scripts/ci/test-apple-canonical-source.sh` is the harness for the rule below. It is NOT passing
# and is NOT wired into any workflow:
#
#   canonical asset PRESENT, any validation failure -> HARD FAIL, never fall back to Actions
#   canonical asset ABSENT                          -> auto MAY fall back to Actions
#
# Every case currently stops in publish-apple-beta.sh's own preflight with
# `fatal: not a git repository`, before reaching the logic it is meant to test. The status, and
# what was ruled out, is written at the top of that file. Named here so that a reader of the gate
# - which is where the rule lives - knows the rule is implemented but not yet integration-proven.
#
# verify_apple <dir> <sha> <workflow file> <version>
#
# The Apple target is verified by the PRODUCER'S OWN validator, invoked as a subprocess, rather than
# by a second implementation of the same checks living here.
#
# That is a deliberate choice about ownership. `apple-libbox-artifact.sh validate` already owns the
# question "is this Libbox bundle internally consistent and attributable": it owns the manifest
# schema, the identity fields, the digest, and the slice check. A release-side copy of those rules
# would be a second owner of one fact, and the two would drift - which is the failure this whole
# body of work exists to remove. The release contributes the two things only it knows: the commit
# being released, and the release version from the tag.
#
# What this function adds on top is the FILE SET: the artifact directory must hold exactly the
# bundle, its manifest and its checksum list, and nothing else.
verify_apple() {
  local dir="$1" sha="$2" wf="$3" version="$4"
  local kind
  kind="$(target_field "$wf" kind)"
  [ "$kind" = "xcframework-manifest" ] || die "verify_apple called for a '$kind' target; this is a wiring bug"
  [ -n "$version" ] || die \
    "verify: the Apple target needs the release version. It is read from the tag, because the
      artifact's own 'version' field is JJ_ARTIFACT_VERSION and is not the release version."

  note "--- apple/arm64 ($wf)"

  local tarball
  tarball="$(find "$dir" -maxdepth 1 -type f -name 'Libbox.xcframework.tar.gz' -print -quit)"
  [ -n "$tarball" ] || die "$dir holds no Libbox.xcframework.tar.gz"
  [ "$(count_files "$dir" -maxdepth 1 -type f -name 'Libbox.xcframework.tar.gz')" -eq 1 ] || die \
    "$dir holds more than one Libbox.xcframework.tar.gz. Exactly one bundle may be published."

  # The file set is pinned. `manifest.json`, the checksum list and the human-readable build record
  # are the bundle's own records and must travel with it; anything else means the producer uploaded
  # something the release has not been told about.
  #
  # The expected list was wrong on the first run and the failure said so precisely, naming the four
  # files it found against the three it wanted - which is the whole reason the check reports both
  # sides rather than just refusing. BUILD-INFO-LIBBOX.txt is redundant with the manifest for a
  # program, and is exactly what a person reads when a release is being diagnosed; dropping it
  # would have been the wrong way to make this assertion pass.
  local expected actual
  expected="$(printf '%s\n%s\n%s\n%s\n' \
    Libbox.xcframework.tar.gz SHA256SUMS-LIBBOX.txt manifest.json BUILD-INFO-LIBBOX.txt | sort)"
  actual="$(find "$dir" -maxdepth 1 -type f -exec basename {} \; | sort)"
  [ "$actual" = "$expected" ] || die \
    "the Apple artifact holds an unexpected file set.
      expected:
$(printf '        %s\n' $expected)
      actual:
$(printf '        %s\n' $actual)"

  # Delegated, with the release's two facts passed in. Fail-closed: a non-zero exit is a refusal,
  # and the validator's own message is surfaced unchanged so the cause is not paraphrased.
  if ! ./scripts/ci/apple-libbox-artifact.sh validate "$dir" \
        --parent-sha "$sha" --jj-version "$version"; then
    die "the Apple Libbox artifact failed the producer's own validation (see above).
      The release is refusing to publish it. Do not weaken this check to make a release pass;
      re-run the Apple producer for $sha."
  fi
  note "    Libbox.xcframework  $(wc -c < "$tarball" | tr -d ' ') bytes  verified by the producer's validator"
}

# verify_desktop <staged dir> <sha> <version>
#
# The desktop installer is verified from the workflow's own build record, not from its
# file name. A name containing the version proves nothing: the v0.1.6 installer was for a
# while produced from a tree whose version literals still said 0.1.5, so the two carriers
# have to be read and compared rather than assumed to agree.
#
# The record is the same BUILD-INFO the desktop workflow writes into its artifact, and it
# is deliberately NOT published - it is read here and left in the staging directory, which
# the public allowlist then refuses to copy.
# verify_desktop <installer tree> <sha> <version> [records dir]
#
# The installer is verified from the workflow's own build record, not from its file name. A
# name containing the version proves nothing: the v0.1.6 installer was produced from a tree
# whose version literals still said 0.1.5, so the two carriers have to be read and compared
# rather than assumed to agree. Its ARTIFACT is named 0.1.5-signed-test for the same reason.
#
# The record and the installer arrive in two different artifacts, so the record's directory
# is a separate argument. It defaults to the installer tree, which is the shape the unit
# fixtures build and the shape a single-artifact producer would have.
verify_desktop() {
  local staged="$1" sha="$2" version="$3" records="${4:-}"
  [ -n "$records" ] || records="$staged"
  local record name expected_name declared_sha declared_version declared_bytes declared_sha256

  # The record is whichever BUILD-INFO-the-desktop-workflow produced; its exact filename
  # carries the version, so it is found rather than guessed. Searched recursively, because a
  # downloaded artifact keeps the directory structure it was uploaded with.
  record="$(find "$records" -type f -name 'BUILD-INFO-WINDOWS-DESKTOP-*.txt' -print -quit)"
  [ -n "$record" ] || die \
    "no BUILD-INFO-WINDOWS-DESKTOP-*.txt under '$records'. Without the build record the installer's core cannot be proven, and a name is not provenance."

  field() { sed -n "s/^$1[[:space:]]\{1,\}//p" "$record" | head -1; }

  expected_name="Jiejiebox-v${version}-windows-x64.exe"
  name="$(field 'artifact')"
  declared_sha="$(field 'core SHA')"
  declared_version="$(field 'core version')"
  declared_bytes="$(field 'artifact bytes')"
  declared_sha256="$(field 'artifact sha256')"

  [ "$name" = "$expected_name" ] || die \
    "the desktop build record names '$name', but this release publishes '$expected_name'."
  [ "$declared_sha" = "$sha" ] || die \
    "the desktop installer was built from core $declared_sha, but this release is $sha.
      A name containing the version is not provenance; the recorded commit is."
  [ "$declared_version" = "$version" ] || die \
    "the desktop build record says core version '$declared_version', expected '$version'."

  # Searched rather than assumed to sit at the top level: `gh run download --name` keeps the
  # artifact's own directory, so the installer is one level down in a real release.
  local installer
  installer="$(find "$staged" -type f -name "$expected_name" -print -quit)"
  [ -n "$installer" ] || die "the desktop artifact has no $expected_name"

  local actual_bytes actual_sha
  actual_bytes="$(wc -c < "$installer" | tr -d ' ')"
  [ "$actual_bytes" = "$declared_bytes" ] || die \
    "$expected_name is $actual_bytes bytes but the build record says $declared_bytes."
  actual_sha="$(sha256_of "$installer")"
  [ "$actual_sha" = "$declared_sha256" ] || die \
    "$expected_name hashes to $actual_sha but the build record says $declared_sha256.
      The record and the binary are not from the same build."

  note "PASS: desktop $expected_name verified against $declared_sha (version $declared_version)"
}

cmd_verify() {
  local dir="" sha="" desktop_dir="" desktop_records="" version=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --dir) dir="${2:-}"; shift 2 ;;
      --sha) sha="${2:-}"; shift 2 ;;
      --desktop-dir) desktop_dir="${2:-}"; shift 2 ;;
      --desktop-records) desktop_records="${2:-}"; shift 2 ;;
      --version) version="${2:-}"; shift 2 ;;
      *) die "verify: unknown argument '$1'" ;;
    esac
  done
  [ -n "$dir" ] || die "verify: --dir is required"
  [ -n "$sha" ] || die "verify: --sha is required"
  [ -d "$dir" ] || die "verify: '$dir' does not exist"

  # Each product is verified INSIDE the exact artifact directory its producer declared, derived
  # from the name `download` staged. Walking all of $dir instead would make the check depend on
  # nothing else having been written there - which is how a payload/evidence split turns into a
  # false "unexpected file" failure the moment a producer's evidence lands one level up.
  #
  # The BRANCH is on `kind`, not on the workflow name. A target whose identity lives in a manifest
  # rather than in a sidecar checksum beside a binary is a different shape, and saying so in the
  # target table means a second such target needs no new code here.
  local wf binary artifact kind
  for wf in "${TARGET_WORKFLOWS[@]}"; do
    binary="$(target_field "$wf" binary)"
    artifact="$(payload_artifact "$wf" "$sha")"
    [ -d "$dir/$binary/$artifact" ] || die \
      "verify: $wf declares payload artifact '$artifact', but $dir/$binary/$artifact does not exist."
    kind="$(target_field "$wf" kind)"
    case "$kind" in
      xcframework-manifest)
        [ -n "$version" ] || die \
          "verify: $wf is a manifest-based target and needs --version, which comes from the release tag."
        verify_apple "$dir/$binary/$artifact" "$sha" "$wf" "$version" ;;
      *)
        verify_platform "$dir/$binary/$artifact" "$sha" "$wf" ;;
    esac
  done

  # The desktop installer is part of the public contract, so it is verified too - and
  # required, because a release that silently shipped without it would still pass every
  # check below.
  if [ -n "$desktop_dir" ]; then
    [ -n "$version" ] || die "verify: --version is required with --desktop-dir"
    [ -d "$desktop_dir" ] || die "verify: '$desktop_dir' does not exist"
    if [ -n "$desktop_records" ]; then
      [ -d "$desktop_records" ] || die "verify: '$desktop_records' does not exist"
    fi
    verify_desktop "$desktop_dir" "$sha" "$version" "$desktop_records"
  else
    die "verify: --desktop-dir is required. The Windows desktop installer is a public deliverable; a release without it is incomplete."
  fi

  # Nothing outside the contract may reach the release. The allowlist is derived
  # from the same table that produced the checks above, so a third product cannot
  # appear in the tarballs without being added to the contract deliberately.
  #
  # Scanned per PRODUCT DIRECTORY rather than over all of $dir. $dir also holds the producer's
  # separated evidence when a target declares one, and evidence is not payload: it is read for
  # provenance and must not be publishable, so requiring it to be on this allowlist would be the
  # wrong fix - it would license publishing a build record.
  #
  # A manifest-based target contributes its OWN file names rather than a binary/sidecar/buildinfo
  # triple, because it has no binary and no sidecar. `verify_apple` has already proved that set is
  # exactly right; this keeps the derived allowlist honest for the same reason the other four are.
  local allowed=() wf f unexpected=""
  local binary kind
  allowed+=( LICENSE )
  for wf in "${TARGET_WORKFLOWS[@]}"; do
    kind="$(target_field "$wf" kind)"
    if [ "$kind" = "xcframework-manifest" ]; then
      allowed+=( Libbox.xcframework.tar.gz SHA256SUMS-LIBBOX.txt manifest.json BUILD-INFO-LIBBOX.txt )
      continue
    fi
    allowed+=( "$(target_field "$wf" binary)" "$(target_field "$wf" binary).sha256" "$(target_field "$wf" buildinfo)" )
  done
  for wf in "${TARGET_WORKFLOWS[@]}"; do
    binary="$(target_field "$wf" binary)"
    artifact="$(payload_artifact "$wf" "$sha")"
    while IFS= read -r f; do
      [ -n "$f" ] || continue
      local ok=0 a
      for a in "${allowed[@]}"; do
        [ "$f" = "$a" ] && ok=1
      done
      [ "$ok" -eq 1 ] || unexpected="${unexpected}${wf}:${f}"$'\n'
    done < <(find "$dir/$binary/$artifact" -maxdepth 1 -type f -exec basename {} \;)
  done
  if [ -n "$unexpected" ]; then
    die "unexpected file(s) in a product payload directory:
$(printf '      %s\n' $unexpected)
      The shipped file set must be exactly the verified one."
  fi

  # Names the products actually verified rather than a count, because the count varies: two by
  # default, four under `RELEASE_EXTRA_PLATFORMS=true`, which is what a release uses. This line
  # said "both platforms" and kept saying it after the loop could run four times, which is the
  # same stale-contract problem as the header comment above - a green message describing a
  # contract the run did not check.
  local verified_platforms=() wfv
  for wfv in "${TARGET_WORKFLOWS[@]}"; do
    verified_platforms+=( "$(target_field "$wfv" platform)/$(target_field "$wfv" arch)" )
  done
  note "PASS: verified ${#verified_platforms[@]} platform(s) against commit $sha: ${verified_platforms[*]}"
}

# ---------------------------------------------------------------------------
# flatten
# ---------------------------------------------------------------------------

cmd_flatten() {
  local plan="" dir="" out="" tag="" desktop="" version=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --plan) plan="${2:-}"; shift 2 ;;
      --dir) dir="${2:-}"; shift 2 ;;
      --out) out="${2:-}"; shift 2 ;;
      --tag) tag="${2:-}"; shift 2 ;;
      --desktop) desktop="${2:-}"; shift 2 ;;
      --version) version="${2:-}"; shift 2 ;;
      *) die "flatten: unknown argument '$1'" ;;
    esac
  done
  [ -n "$plan" ] || die "flatten: --plan is required"
  [ -n "$dir" ] || die "flatten: --dir is required"
  [ -n "$out" ] || die "flatten: --out is required"
  [ -n "$tag" ] || die "flatten: --tag is required"
  [ -n "$desktop" ] || die "flatten: --desktop is required"
  [ -n "$version" ] || die "flatten: --version is required"
  case "$tag" in
    v[0-9]*) ;;
    *) die "flatten: '$tag' is not a release tag" ;;
  esac
  # Re-checked here because the tag is interpolated into an output FILENAME; see
  # the note in cmd_check_tag for why this is a bash glob and not `grep -q`.
  if [[ "$tag" = *[!0-9A-Za-z._+-]* ]]; then
    die "flatten: tag '$tag' contains unsafe characters"
  fi
  # The version is interpolated into a filename too, and it is the value the installer
  # name must carry, so it is checked here as well rather than trusted from the caller.
  if [[ "$version" = *[!0-9A-Za-z._+-]* ]]; then
    die "flatten: version '$version' contains unsafe characters"
  fi

  rm -rf "$out"
  mkdir -p "$out"

  local wf binary buildinfo prefix src expected actual artifact kind
  for wf in "${TARGET_WORKFLOWS[@]}"; do
    binary="$(target_field "$wf" binary)"
    buildinfo="$(target_field "$wf" buildinfo)"
    prefix="$(target_field "$wf" tarball)"
    kind="$(target_field "$wf" kind)"
    # The payload directory is derived from the plan's exact artifact name, not from a search for
    # the binary. `find -name <binary>` would also match a copy of the same file anywhere under
    # $dir - including inside a producer's EVIDENCE artifact, if one were ever written into the
    # payload tree - and it silently picks whichever the walk reaches first. The name is in the
    # plan because `plan` wrote it from the same target table that declares `binary`, so the two
    # cannot disagree.
    artifact="$(awk -F'\t' -v w="$wf" '$1 == w {print $9}' "$plan")"
    [ -n "$artifact" ] || die "flatten: the plan names no payload artifact for $wf"
    src="$dir/$binary/$artifact"
    [ -d "$src" ] || die \
      "flatten: $wf declares payload artifact '$artifact', but $src does not exist. The payload was not staged where the plan says it is."

    # The archive contents are pinned to an allowlist, so what a downloader gets
    # is a known, small set that has been verified - not "whatever was in the
    # artifact directory".
    #
    # A manifest-based target is COPIED, not repacked. Its bundle is already an archive whose
    # digest the manifest records and `verify` has already checked; unwrapping and rewrapping it
    # would produce different bytes under the same name, so the file the release page offers would
    # no longer be the file whose hash was verified - and `SHA256SUMS` would then describe an
    # artifact nobody validated. The bytes are promoted exactly as proved.
    if [ "$kind" = "xcframework-manifest" ]; then
      local apple_bundle="$src/Libbox.xcframework.tar.gz"
      [ -f "$apple_bundle" ] || die "flatten: no Libbox.xcframework.tar.gz in $src"
      cp "$apple_bundle" "$out/Libbox.xcframework.tar.gz"
      note "promoted Libbox.xcframework.tar.gz (byte-identical to the verified artifact)"
      continue
    fi

    expected="$(printf '%s\n%s\n%s\n%s\n' "$binary" "$binary.sha256" "$buildinfo" LICENSE | sort)"
    actual="$(find "$src" -maxdepth 1 -type f -exec basename {} \; | sort)"
    [ "$actual" = "$expected" ] || die \
      "artifact directory for $binary holds an unexpected file set.
    expected:
$(printf '      %s\n' $expected)
    actual:
$(printf '      %s\n' $actual)"

    tar -C "$src" -czf "$out/${prefix}-${tag}.tar.gz" .
    note "packaged $out/${prefix}-${tag}.tar.gz"
  done

  # The desktop installer is a public deliverable but NOT a tarball: it is copied in
  # under the exact name the release publishes, so its filename and the name inside
  # SHA256SUMS are the same string by construction rather than by agreement.
  [ -f "$desktop" ] || die "flatten: no desktop installer at '$desktop'"
  local installer="Jiejiebox-v${version}-windows-x64.exe"
  cp "$desktop" "$out/$installer"
  note "staged $installer"

  # ---- the public asset contract -------------------------------------------
  #
  # This is the allowlist, and it is enforced twice: nothing unexpected may be in the
  # staging directory, and every expected name must be there exactly once. A build that
  # added a file - the CI records the desktop workflow also uploads, a stray .sha256, a
  # log - fails here rather than shipping it to the download page.
  #
  # The expected set is derived from TARGET_WORKFLOWS plus the installer, so a product
  # cannot be added to one and forgotten in the other.
  #
  # Apple contributes a fixed file name rather than `<prefix>-<tag>.tar.gz`: it has no `prefix`,
  # because there is no repacked tarball to name. Deriving the name from the target table keeps the
  # "one product, one entry" property without pretending Apple has a prefix it does not have.
  local expected_assets=() wf2 kind2
  for wf2 in "${TARGET_WORKFLOWS[@]}"; do
    kind2="$(target_field "$wf2" kind)"
    if [ "$kind2" = "xcframework-manifest" ]; then
      expected_assets+=( Libbox.xcframework.tar.gz )
      continue
    fi
    expected_assets+=( "$(target_field "$wf2" tarball)-${tag}.tar.gz" )
  done
  expected_assets+=( "$installer" )
  expected_assets+=( SHA256SUMS )

  # ---- one manifest over every public binary --------------------------------
  #
  # Written BEFORE the listing check below, because it is itself part of the contract: a
  # check that runs first and looks for a file the code has not created yet can only ever
  # fail. That ordering mistake is exactly what the first runner run of this change found.
  #
  # SHA256SUMS describes the bytes that are about to be published, and it names them the
  # way the release page does - bare filenames, no './' prefix, because that is the string
  # a downloader has. Sorted by filename so the file is stable across runs.
  #
  # `expected_binaries` is computed here rather than inline at the comparison: `[ ... -eq
  # "${#arr[@]}" - 1 ]` is not valid - the arithmetic is not evaluated inside a test - and
  # bash reports it as a syntax error on the `-` rather than as a failed comparison. It was
  # found by the second runner run.
  local expected_binaries=$(( ${#expected_assets[@]} - 1 ))
  (
    cd "$out"
    : > SHA256SUMS
    for name in $(find . -mindepth 1 -maxdepth 1 -type f -exec basename {} \; | sort); do
      [ "$name" = SHA256SUMS ] && continue
      printf '%s  %s\n' "$(sha256_of "$name")" "$name" >> SHA256SUMS
    done
  )

  # Nothing unexpected, and nothing missing.
  local listing want_listing
  listing="$(find "$out" -mindepth 1 -maxdepth 1 -exec basename {} \; | sort)"
  want_listing="$(printf '%s\n' "${expected_assets[@]}" | sort)"
  [ "$listing" = "$want_listing" ] || die \
    "the staged release does not match the public asset contract.
    expected exactly:
$(printf '      %s\n' $want_listing)
    found:
$(printf '      %s\n' $listing)"

  # Every expected name exactly once, so a duplicate cannot publish two different files
  # under one name.
  local asset copies
  for asset in "${expected_assets[@]}"; do
    [ "$asset" = SHA256SUMS ] && continue
    copies="$(count_files "$out" -maxdepth 1 -type f -name "$asset")"
    [ "$copies" -eq 1 ] || die \
      "$asset: found $copies copies in the staged release, expected exactly 1. Every public deliverable must appear once."
  done

  # Prove the manifest before it is published. `sha256sum -c` is the exact command the
  # release notes tell a downloader to run, so a manifest that cannot satisfy it must not
  # reach the release.
  local entries
  entries="$(grep -c . "$out/SHA256SUMS" | tr -d ' ')"
  [ "$entries" -eq "$expected_binaries" ] || die \
    "SHA256SUMS has $entries entries, expected $expected_binaries - one per public binary."
  check_sha256_file "$out" SHA256SUMS >/dev/null ||
    die "SHA256SUMS does not verify against the files it names"

  # One archive per target, where an archive is what a target produces. A manifest-based target
  # publishes a `.tar.gz` too, so the count is still one per target - but it is stated in terms of
  # the target table rather than assumed to come from `flatten`'s own `tar` invocation, because
  # Apple's arrives by `cp` and a fall in this number would otherwise look like a packaging bug
  # rather than a missing product.
  local archives
  archives="$(count_files "$out" -maxdepth 1 -type f -name '*.tar.gz')"
  [ "$archives" -eq "${#TARGET_WORKFLOWS[@]}" ] || die \
    "flatten produced $archives tarball(s), expected ${#TARGET_WORKFLOWS[@]} - one per release target.
      Apple's bundle arrives by copy rather than by tar, and still counts."

  note "release assets:"
  ( cd "$out" && ls -la . && cat SHA256SUMS )
}

# ---------------------------------------------------------------------------
# publish-flag
# ---------------------------------------------------------------------------
#
# Turns the dry_run input into an explicit publish decision. Anything that is not
# exactly `true` or `false` is refused, so a malformed or missing input fails
# closed instead of publishing a release by accident.

cmd_publish_flag() {
  local dry=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --dry-run) dry="${2:-}"; shift 2 ;;
      *) die "publish-flag: unknown argument '$1'" ;;
    esac
  done
  case "$dry" in
    true) echo "false" ;;
    false) echo "true" ;;
    *) die "the dry_run input must be exactly 'true' or 'false', got '${dry:-<empty>}'. Refusing to guess whether this run may publish." ;;
  esac
}

# ---------------------------------------------------------------------------
# check-upload
# ---------------------------------------------------------------------------
#
# The public asset contract, checked against the staging DIRECTORY rather than against the
# command that filled it. It runs as its own step immediately before publishing, so the
# last thing between this repository and the download page is a statement about what is
# actually on disk:
#
#   exactly these files, no more and no fewer:
#     Jiejiebox-v<version>-windows-x64.exe
#     jiejie-sing-box-{linux-amd64,macos-arm64,windows-amd64,android-arm64}-<tag>.tar.gz
#     SHA256SUMS
#
# and SHA256SUMS names every one of them, with no entry for anything else, and verifies.

# public_asset_names <tag> <version>
#
# THE canonical public Release asset contract. Prints one asset name per line.
#
# # Why this function exists
#
# The v0.1.8 release published SIX assets where the contract requires SEVEN:
# `Libbox.xcframework.tar.gz` was absent from the public release page. The cause was not the Apple
# artifact and not `check-upload` - it was that the asset list existed in THREE places:
#
#   * `check-upload` DERIVED it from TARGET_WORKFLOWS        -> 7, and it passed
#   * `release.yml`'s `gh release create` spelled it out      -> 6, and it uploaded 6
#   * `release.yml`'s read-back spelled it out AGAIN          -> 6, so 6 == 6 and the workflow
#                                                               went green on a bad release
#
# A check and the thing it checks agreeing with each other is not evidence that either is right.
# The read-back was self-consistently wrong, which is why the release could be published with an
# asset missing and still report success.
#
# There is now ONE derivation. `check-upload`, `expected-assets`, `publish-args` and
# `verify-public-release` all call this, so adding a target makes the upload list, the staging
# check and the read-back move together, and a publisher that does not keep up fails its own
# read-back instead of passing.
#
# The `kind` branch is the one thing that is not mechanical: a manifest-based target publishes a
# bundle whose bytes were already verified under its own fixed name, rather than a repacked tarball
# named from a prefix and the tag. See `flatten` for why it is promoted and not rebuilt.
public_asset_names() {
  local tag="$1" version="$2" wf
  for wf in "${TARGET_WORKFLOWS[@]}"; do
    if [ "$(target_field "$wf" kind)" = "xcframework-manifest" ]; then
      printf '%s\n' Libbox.xcframework.tar.gz
      continue
    fi
    printf '%s\n' "$(target_field "$wf" tarball)-${tag}.tar.gz"
  done
  printf '%s\n' "Jiejiebox-v${version}-windows-x64.exe" SHA256SUMS
}

# expected-assets --tag <tag> --version <version>
#
# The contract as data, so a caller can consume it instead of restating it. `release.yml` uses this
# for both the upload list and the read-back expectation, which is what makes the two incapable of
# disagreeing.
cmd_expected_assets() {
  local tag="" version=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --tag) tag="${2:-}"; shift 2 ;;
      --version) version="${2:-}"; shift 2 ;;
      *) die "expected-assets: unknown argument '$1'" ;;
    esac
  done
  [ -n "$tag" ] || die "expected-assets: --tag is required"
  [ -n "$version" ] || die "expected-assets: --version is required"
  case "$tag" in
    v[0-9]*) ;;
    *) die "expected-assets: '$tag' is not a release tag" ;;
  esac
  if [[ "$tag" = *[!0-9A-Za-z._+-]* ]] || [[ "$version" = *[!0-9A-Za-z._+-]* ]]; then
    die "expected-assets: tag or version contains unsafe characters"
  fi
  public_asset_names "$tag" "$version"
}

# publish-args --dir <upload dir> --tag <tag> --version <version>
#
# The upload arguments for `gh release create`, derived from the same contract, one per line.
# Paths are validated to exist HERE rather than left to `gh`, because a missing path makes `gh`
# fail after it has already created the release - the one irreversible step - and the failure then
# reads as a CLI error rather than as "the contract and the staging directory disagree".
#
# The caller must word-split this on purpose: the output is one argument per line and paths contain
# no whitespace, because every name in the contract is derived from a tag and a version that this
# function has already refused to accept if they contain anything outside [0-9A-Za-z._+-].
cmd_publish_args() {
  local dir="" tag="" version="" name
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --dir) dir="${2:-}"; shift 2 ;;
      --tag) tag="${2:-}"; shift 2 ;;
      --version) version="${2:-}"; shift 2 ;;
      *) die "publish-args: unknown argument '$1'" ;;
    esac
  done
  [ -n "$dir" ] || die "publish-args: --dir is required"
  [ -d "$dir" ] || die "publish-args: '$dir' does not exist"
  [ -n "$tag" ] || die "publish-args: --tag is required"
  [ -n "$version" ] || die "publish-args: --version is required"

  local missing=()
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    if [ -f "$dir/$name" ]; then
      printf '%s\n' "$dir/$name"
    else
      missing+=( "$name" )
    fi
  done < <(public_asset_names "$tag" "$version")

  [ "${#missing[@]}" -eq 0 ] || die \
    "publish-args: the staging directory is missing asset(s) the contract requires:
$(printf '      %s\n' "${missing[@]}")
      Refusing to hand gh a list it cannot upload. A release created from this directory would be
      missing exactly these files, which is how v0.1.8 was published without its Apple bundle."
}

# verify-public-release --repo <owner/name> --tag <tag> --version <version>
#
# Reads the PUBLISHED release back from the API and compares its asset names against the contract,
# BY NAME. Compares by name rather than by count on purpose: v0.1.8 had the right count for the
# wrong list, and a count-only check cannot tell the difference.
cmd_verify_public_release() {
  local repo="" tag="" version=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:-}"; shift 2 ;;
      --tag) tag="${2:-}"; shift 2 ;;
      --version) version="${2:-}"; shift 2 ;;
      *) die "verify-public-release: unknown argument '$1'" ;;
    esac
  done
  [ -n "$repo" ] || die "verify-public-release: --repo is required"
  [ -n "$tag" ] || die "verify-public-release: --tag is required"
  [ -n "$version" ] || die "verify-public-release: --version is required"

  local want found
  want="$(public_asset_names "$tag" "$version" | sort)"
  found="$(gh release view "$tag" --repo "$repo" --json assets --jq '.assets[].name' | sort)" || die \
    "verify-public-release: could not read the published release $tag from $repo."

  if [ "$found" != "$want" ]; then
    local missing extra
    missing="$(comm -23 <(printf '%s\n' "$want") <(printf '%s\n' "$found"))"
    extra="$(comm -13 <(printf '%s\n' "$want") <(printf '%s\n' "$found"))"
    die "the PUBLISHED release $tag does not carry the public asset contract.
      missing:
$(printf '      %s\n' ${missing:-<none>})
      unexpected:
$(printf '      %s\n' ${extra:-<none>})"
  fi
  note "PASS: the published release carries exactly the $(printf '%s\n' "$want" | grep -c .) contract assets"
}

cmd_check_upload() {
  local out="" tag="" version=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --out) out="${2:-}"; shift 2 ;;
      --tag) tag="${2:-}"; shift 2 ;;
      --version) version="${2:-}"; shift 2 ;;
      *) die "check-upload: unknown argument '$1'" ;;
    esac
  done
  [ -n "$out" ] || die "check-upload: --out is required"
  [ -n "$tag" ] || die "check-upload: --tag is required"
  [ -n "$version" ] || die "check-upload: --version is required"
  [ -d "$out" ] || die "check-upload: '$out' does not exist"
  if [[ "$version" = *[!0-9A-Za-z._+-]* ]]; then
    die "check-upload: version '$version' contains unsafe characters"
  fi

  # Derived from the ONE contract function, not restated here. This is what makes the staging check
  # and the upload list incapable of disagreeing: see `public_asset_names` for the v0.1.8 release
  # that went public with six assets because they were derived in three places and two of them
  # agreed with each other.
  local expected=() wf
  while IFS= read -r wf; do
    [ -n "$wf" ] || continue
    expected+=( "$wf" )
  done < <(public_asset_names "$tag" "$version")

  local want found extra missing
  want="$(printf '%s\n' "${expected[@]}" | sort)"
  found="$(find "$out" -mindepth 1 -maxdepth 1 -exec basename {} \; | sort)"

  extra="$(comm -13 <(printf '%s\n' "$want") <(printf '%s\n' "$found"))"
  missing="$(comm -23 <(printf '%s\n' "$want") <(printf '%s\n' "$found"))"
  [ -z "$extra" ] || die \
    "the upload directory holds file(s) that are not public deliverables:
$(printf '      %s\n' $extra)
      Release assets are the user's download page. CI records - BUILD-INFO-*, *.sha256
      sidecars, runner environment and build logs - belong in Actions artifacts, which is
      where the workflows already put them."
  [ -z "$missing" ] || die \
    "the upload directory is missing public deliverable(s):
$(printf '      %s\n' $missing)"

  # Every expected name exactly once. `find -maxdepth 1 -type f` counts name collisions
  # that a sorted listing would show as one line only when they are truly the same file.
  local asset copies
  for asset in "${expected[@]}"; do
    copies="$(count_files "$out" -maxdepth 1 -type f -name "$asset")"
    [ "$copies" -eq 1 ] || die \
      "$asset: found $copies copies, expected exactly 1. A duplicate would publish two different files under one name."
  done

  # SHA256SUMS must name exactly the public binaries - no entry for a file that is not
  # published, and no published binary left out.
  local binaries=() e
  for e in "${expected[@]}"; do
    [ "$e" = SHA256SUMS ] && continue
    binaries+=( "$e" )
  done
  local named want_named
  # `sha256sum` output is "<64 hex>  <name>"; an older manifest used a './' prefix, so the
  # prefix is stripped rather than assumed absent. Either spelling names the same file.
  named="$(sed -n 's/^[0-9a-f]\{64\}  \.\{0,2\}\([^ ].*\)$/\1/p' "$out/SHA256SUMS" | sort)"
  want_named="$(printf '%s\n' "${binaries[@]}" | sort)"
  [ "$named" = "$want_named" ] || die \
    "SHA256SUMS does not name exactly the published binaries.
    expected:
$(printf '      %s\n' $want_named)
    found:
$(printf '      %s\n' $named)"

  local entries
  entries="$(grep -c . "$out/SHA256SUMS" | tr -d ' ')"
  [ "$entries" -eq "${#binaries[@]}" ] || die \
    "SHA256SUMS has $entries entries but there are ${#binaries[@]} public binaries."

  check_sha256_file "$out" SHA256SUMS >/dev/null ||
    die "sha256sum -c SHA256SUMS does not pass in the upload directory"

  note "PASS: the upload directory is exactly the public asset contract"
  note "      ${#binaries[@]} binaries + SHA256SUMS, each once, manifest verifies"
}

# ---------------------------------------------------------------------------

main() {
  [ "$#" -gt 0 ] || die "usage: release-artifacts.sh <check-tag|release-version|plan|evidence|download|desktop|verify|flatten|check-upload|publish-flag|expected-assets|publish-args|verify-public-release> [args]"
  local sub="$1"
  shift
  case "$sub" in
    check-tag) cmd_check_tag "$@" ;;
    release-version) cmd_release_version "$@" ;;
    plan) cmd_plan "$@" ;;
  evidence) cmd_evidence "$@" ;;
    download) cmd_download "$@" ;;
    desktop) cmd_desktop "$@" ;;
    verify) cmd_verify "$@" ;;
    flatten) cmd_flatten "$@" ;;
    check-upload) cmd_check_upload "$@" ;;
    publish-flag) cmd_publish_flag "$@" ;;
    expected-assets) cmd_expected_assets "$@" ;;
    publish-args) cmd_publish_args "$@" ;;
    verify-public-release) cmd_verify_public_release "$@" ;;
    *) die "unknown subcommand '$sub'" ;;
  esac
}

main "$@"
