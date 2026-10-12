#!/usr/bin/env bash
# Selects the CI runs that built one commit, downloads their artifacts, and proves
# every platform in the release contract is present, unique, hashed and genuine.
#
# Usage:
#   release-artifacts.sh check-tag  --tag TAG
#   release-artifacts.sh plan       --repo R --sha SHA --out FILE [--interval N] [--timeout N]
#   release-artifacts.sh download   --repo R --plan FILE --dir DIR
#   release-artifacts.sh verify     --dir DIR --sha SHA
#   release-artifacts.sh flatten    --plan FILE --dir DIR --out DIR --tag TAG
#   release-artifacts.sh publish-flag --dry-run VALUE
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
# Exactly the products this fork ships from an automated build: the Linux amd64
# Server Edition and the macOS arm64 core. Windows, Android and the Apple IPA/DMG
# are deliberately NOT here - pulling them into the release is a product decision,
# not a bug fix, and a third artifact that nobody verified would be worse than a
# missing one.

TARGET_WORKFLOWS=(
  ".github/workflows/server-linux-amd64.yml"
  ".github/workflows/client-macos.yml"
)

# The first release contract shipped Linux and macOS only. v0.1.6 also publishes
# the already-verified Windows core and Android ARM64 libbox, enabled explicitly
# by release.yml so the existing two-product unit fixtures remain focused.
case "${RELEASE_EXTRA_PLATFORMS:-false}" in
  false) ;;
  true)
    TARGET_WORKFLOWS+=(
      ".github/workflows/windows-core-amd64.yml"
      ".github/workflows/android-core-arm64.yml"
    )
    ;;
  *) die "RELEASE_EXTRA_PLATFORMS must be exactly true or false" ;;
esac

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

    *) die "the release contract has no '$field' for workflow '$wf'" ;;
  esac
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

      printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
        "$wf" "$wfid" "$rid" "$rnum" "$status" "$conclusion" "$sha" "$url" >> "$out"
      note "  $wf: selected run $rid (#$rnum) $conclusion  $url"
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
# download
# ---------------------------------------------------------------------------

cmd_download() {
  local repo="" plan="" dir=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:-}"; shift 2 ;;
      --plan) plan="${2:-}"; shift 2 ;;
      --dir) dir="${2:-}"; shift 2 ;;
      *) die "download: unknown argument '$1'" ;;
    esac
  done
  [ -n "$repo" ] || die "download: --repo is required"
  [ -n "$plan" ] || die "download: --plan is required"
  [ -n "$dir" ] || die "download: --dir is required"
  [ -s "$plan" ] || die "download: the release plan '$plan' is empty; nothing was verified for this commit"

  rm -rf "$dir"
  mkdir -p "$dir"

  local rows=0
  local wf wfid rid rnum status conclusion sha url binary dest subdirs files row
  while IFS= read -r row; do
    [ -n "$row" ] || continue
    # See newest_run: an empty field would shift the rest of the row, and here that
    # would mean downloading a run that was never verified. awk counts an empty
    # field where `read` would silently consume it.
    [ "$(printf '%s' "$row" | awk -F'\t' '{print NF}')" -eq 8 ] || die \
      "the release plan has a malformed row:
      $row"
    IFS=$'\t' read -r wf wfid rid rnum status conclusion sha url <<<"$row"
    binary="$(target_field "$wf" binary)"
    # One directory per product, named for the binary it must contain, so a
    # second run of the other workflow can never overwrite this one.
    dest="$dir/$binary"
    mkdir -p "$dest"
    note "downloading run $rid ($wf) -> $dest"
    # NOT wrapped in `|| true`: a download that failed leaves the artifact
    # unverified, and an unverified artifact must not be publishable.
    if ! gh run download "$rid" --repo "$repo" --dir "$dest"; then
      die "gh run download failed for run $rid ($wf). $binary is NOT verified; refusing to continue."
    fi
    subdirs="$(find "$dest" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
    [ "$subdirs" -eq 1 ] || die \
      "run $rid produced $subdirs artifact directories in $dest, expected exactly 1. The shipped file set must be deterministic."
    files="$(find "$dest" -type f | wc -l | tr -d ' ')"
    [ "$files" -gt 0 ] || die "run $rid downloaded no files into $dest"
    rows=$((rows + 1))
  done < "$plan"

  [ "$rows" -eq "${#TARGET_WORKFLOWS[@]}" ] || die \
    "downloaded $rows run(s), expected ${#TARGET_WORKFLOWS[@]}"
  note "PASS: downloaded artifacts from $rows verified run(s)"
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
          -f core_sha=$sha -f core_version=<version>
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

  note "downloading desktop run $rid -> $out"
  if ! gh run download "$rid" --repo "$repo" --dir "$out"; then
    die "gh run download failed for desktop run $rid. The installer is NOT verified; refusing to continue."
  fi

  local subdirs files
  subdirs="$(find "$out" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
  [ "$subdirs" -eq 1 ] || die \
    "desktop run $rid produced $subdirs artifact directories in $out, expected exactly 1. The shipped file set must be deterministic."
  files="$(find "$out" -type f | wc -l | tr -d ' ')"
  [ "$files" -gt 0 ] || die "desktop run $rid downloaded no files into $out"
  note "PASS: desktop run $rid downloaded ($files files)"
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
verify_desktop() {
  local staged="$1" sha="$2" version="$3"
  local record name expected_name declared_sha declared_version declared_bytes declared_sha256

  # The record is whichever BUILD-INFO-the-desktop-workflow produced; its exact filename
  # carries the version, so it is found rather than guessed.
  record="$(find "$staged" -maxdepth 1 -type f -name 'BUILD-INFO-WINDOWS-DESKTOP-*.txt' -print -quit)"
  [ -n "$record" ] || die \
    "the desktop artifact carries no BUILD-INFO-WINDOWS-DESKTOP-*.txt. Without the build record the installer's core cannot be proven, and a name is not provenance."

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

  local installer="$staged/$expected_name"
  [ -f "$installer" ] || die "the desktop artifact has no $expected_name"

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
  local dir="" sha="" desktop_dir="" version=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --dir) dir="${2:-}"; shift 2 ;;
      --sha) sha="${2:-}"; shift 2 ;;
      --desktop-dir) desktop_dir="${2:-}"; shift 2 ;;
      --version) version="${2:-}"; shift 2 ;;
      *) die "verify: unknown argument '$1'" ;;
    esac
  done
  [ -n "$dir" ] || die "verify: --dir is required"
  [ -n "$sha" ] || die "verify: --sha is required"
  [ -d "$dir" ] || die "verify: '$dir' does not exist"

  local wf
  for wf in "${TARGET_WORKFLOWS[@]}"; do
    verify_platform "$dir" "$sha" "$wf"
  done

  # The desktop installer is part of the public contract, so it is verified too - and
  # required, because a release that silently shipped without it would still pass every
  # check below.
  if [ -n "$desktop_dir" ]; then
    [ -n "$version" ] || die "verify: --version is required with --desktop-dir"
    [ -d "$desktop_dir" ] || die "verify: '$desktop_dir' does not exist"
    verify_desktop "$desktop_dir" "$sha" "$version"
  else
    die "verify: --desktop-dir is required. The Windows desktop installer is a public deliverable; a release without it is incomplete."
  fi

  # Nothing outside the contract may reach the release. The allowlist is derived
  # from the same table that produced the checks above, so a third product cannot
  # appear in the tarballs without being added to the contract deliberately.
  local allowed=() wf f unexpected=""
  allowed+=( LICENSE )
  for wf in "${TARGET_WORKFLOWS[@]}"; do
    allowed+=( "$(target_field "$wf" binary)" "$(target_field "$wf" binary).sha256" "$(target_field "$wf" buildinfo)" )
  done
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    local ok=0 a
    for a in "${allowed[@]}"; do
      [ "$f" = "$a" ] && ok=1
    done
    [ "$ok" -eq 1 ] || unexpected="${unexpected}${f}"$'\n'
  done < <(find "$dir" -type f -exec basename {} \;)
  if [ -n "$unexpected" ]; then
    die "unexpected file(s) in the release tree:
$(printf '      %s\n' $unexpected)
      The shipped file set must be exactly the verified one."
  fi

  note "PASS: both platforms verified against commit $sha"
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

  local wf binary buildinfo prefix src expected actual
  for wf in "${TARGET_WORKFLOWS[@]}"; do
    binary="$(target_field "$wf" binary)"
    buildinfo="$(target_field "$wf" buildinfo)"
    prefix="$(target_field "$wf" tarball)"
    src="$(dirname "$(find "$dir" -type f -name "$binary")")"

    # The archive contents are pinned to an allowlist, so what a downloader gets
    # is a known, small set that has been verified - not "whatever was in the
    # artifact directory".
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
  local expected_assets=() wf2
  for wf2 in "${TARGET_WORKFLOWS[@]}"; do
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

  local archives
  archives="$(count_files "$out" -maxdepth 1 -type f -name '*.tar.gz')"
  [ "$archives" -eq "${#TARGET_WORKFLOWS[@]}" ] || die \
    "flatten produced $archives tarball(s), expected ${#TARGET_WORKFLOWS[@]}"

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

  local expected=() wf
  for wf in "${TARGET_WORKFLOWS[@]}"; do
    expected+=( "$(target_field "$wf" tarball)-${tag}.tar.gz" )
  done
  expected+=( "Jiejiebox-v${version}-windows-x64.exe" SHA256SUMS )

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
  [ "$#" -gt 0 ] || die "usage: release-artifacts.sh <check-tag|release-version|plan|download|desktop|verify|flatten|check-upload|publish-flag> [args]"
  local sub="$1"
  shift
  case "$sub" in
    check-tag) cmd_check_tag "$@" ;;
    release-version) cmd_release_version "$@" ;;
    plan) cmd_plan "$@" ;;
    download) cmd_download "$@" ;;
    desktop) cmd_desktop "$@" ;;
    verify) cmd_verify "$@" ;;
    flatten) cmd_flatten "$@" ;;
    check-upload) cmd_check_upload "$@" ;;
    publish-flag) cmd_publish_flag "$@" ;;
    *) die "unknown subcommand '$sub'" ;;
  esac
}

main "$@"
