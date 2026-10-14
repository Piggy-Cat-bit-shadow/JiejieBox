#!/usr/bin/env bash
# Packs and validates the Libbox.xcframework artifact that CI hands to the Apple GUI builds and to
# the local TestFlight publish.
#
# # Why this exists
#
# Compiling Libbox is the most expensive step in the Apple pipeline, and it is determined entirely by
# the source commit - it needs no Apple secrets. Both GUI jobs previously compiled it themselves, so
# the same commit was compiled twice per CI run and a third time on the publishing Mac.
#
# The artifact is the unit that lets all three share one build, which only works if the artifact can
# be proven to belong to the commit being built. A bare directory cannot carry that proof, and
# artifact NAMES are chosen by whoever uploads them, so the name is a label and not evidence. This
# script therefore writes a manifest and treats it as authoritative, and it validates against the
# manifest rather than against any string a caller supplies.
#
# Usage:
#   apple-libbox-artifact.sh pack    <xcframework-dir> <output-dir>
#   apple-libbox-artifact.sh validate <input-dir> --parent-sha <sha>
#   apple-libbox-artifact.sh install  <input-dir> <client-dir>
#
# # What the artifact is bound to, and what it is not
#
# The artifact is bound to ONE thing: the parent commit it was compiled from. Libbox is
# the sing-box core, and the core does not depend on which Apple UI branch a client was
# built from - so the artifact deliberately records no Apple client revision and none is
# validated against it.
#
# This matters because the two Apple products are built from two different Apple
# branches (iOS from hako-ui, macOS from the pinned dev commit), with two different
# SHAs. An earlier version of this script required the artifact's recorded Apple
# submodule to equal the building checkout's, which could only ever hold for one of the
# two platforms. Each platform's Apple revision is recorded in that platform's
# BUILD-INFO instead, where it is meaningful.
set -euo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

action="${1:-}"
case "$action" in
  pack)     ;;
  validate) ;;
  install)  ;;
  ""|-h|--help)
    sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
    ;;
  *)
    echo "apple-libbox-artifact.sh: unknown action '$action'" >&2
    exit 2
    ;;
esac

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

# --- pack --------------------------------------------------------------------
#
# The directory is archived with `ditto` on macOS and `tar` elsewhere: ditto is what preserves the
# symlinks and resource forks an xcframework relies on, and a plain tar round trip has silently
# dropped them before.
pack() {
  local xcframework="$1" output="$2"
  [ -d "$xcframework" ] || fail "no xcframework at $xcframework"
  mkdir -p "$output"

  local tarball="$output/Libbox.xcframework.tar.gz"
  rm -f "$tarball"
  if command -v ditto >/dev/null 2>&1; then
    # --sequesterRsrc keeps the bundle's resource forks inside the archive; --keepParent keeps the
    # top-level directory so extraction restores the name the Xcode project references.
    ditto -c -k --sequesterRsrc --keepParent "$xcframework" "$tarball"
  else
    tar -czf "$tarball" -C "$(dirname "$xcframework")" "$(basename "$xcframework")"
  fi

  local parent_sha version go_version xcode_version
  parent_sha="$(git rev-parse HEAD)"
  # shellcheck disable=SC1091
  eval "$(./scripts/ci/version.sh)"
  version="$JJ_ARTIFACT_VERSION"
  go_version="$JJ_GO_VERSION"
  # Collapse to a single printable line. `xcodebuild -version` on a runner can emit a control
  # character that is legal in a shell variable and illegal in JSON; sanitising here keeps the
  # value readable in BUILD-INFO-LIBBOX.txt as well.
  xcode_version="$(xcodebuild -version 2>/dev/null | head -1 | tr -d '\000-\037\177' || true)"
  [ -n "$xcode_version" ] || xcode_version="unknown"

  # Provenance only - recorded so a reader can find the run, never matched by a consumer.
  # Empty when packing outside CI, which is legitimate: the local publish path packs fixtures.
  local gomobile_version gomobile_source workflow_ref run_id run_attempt
  gomobile_version="$(gomobile version 2>/dev/null | tr -d '\000-\037\177' | head -1 || true)"
  [ -n "$gomobile_version" ] || gomobile_version="unknown"
  gomobile_source="$(go list -m -f '{{.Path}}@{{.Version}}' golang.org/x/mobile 2>/dev/null || true)"
  [ -n "$gomobile_source" ] || gomobile_source="unknown"
  workflow_ref="${GITHUB_WORKFLOW_REF:-}"
  run_id="${GITHUB_RUN_ID:-}"
  run_attempt="${GITHUB_RUN_ATTEMPT:-}"

  local artifact_sha
  artifact_sha="$(sha256_of "$tarball")"

  # The xcframework's own slices, DISCOVERED rather than assumed. `check-apple-shared-libbox.sh`
  # already inspects the installed framework, but that is after the fact and per-install: a
  # manifest that declares its slices turns "does this framework carry what the clients need"
  # from something rediscovered at every install into a property of the artifact, checked once
  # at pack time and re-checked by every consumer against the same declaration.
  #
  # Names come from the directory listing, never from a list written here - an xcframework's
  # slice directory names are chosen by Xcode and a hardcoded guess would be wrong the first time
  # a platform is added.
  local slices
  slices="$(find "$xcframework" -mindepth 1 -maxdepth 1 -type d -exec basename {} \; | sort | tr '\n' ' ')"
  slices="${slices% }"
  [ -n "$slices" ] || fail "the xcframework at $xcframework has no slice directories"
  # Requirement, not a guess: the two clients this artifact exists for. Named here because a
  # manifest that lists whatever it happens to find would happily declare a single-slice
  # framework as valid.
  case " $slices " in
    *" ios-arm64 "*) ;;
    *) fail "the xcframework has no ios-arm64 slice (found: $slices)" ;;
  esac
  case " $slices " in
    *" macos-arm64 "*|*" macos-arm64_x86_64 "*) ;;
    *) fail "the xcframework has no macOS slice (found: $slices)" ;;
  esac

  local jj_version
  jj_version="$JJ_VERSION"

  cat > "$output/BUILD-INFO-LIBBOX.txt" <<EOF
Jiejie Libbox build information
===============================
parent_sha:            $parent_sha
jj_version:            $jj_version
version:               $version
build_time_utc:        $(date -u +%Y-%m-%dT%H:%M:%SZ)
xcode_version:         $xcode_version
go_version:            $go_version
slices:                $slices
gomobile_version:      $gomobile_version
gomobile_source:       $gomobile_source
workflow:              $workflow_ref
run_id:                $run_id
run_attempt:           $run_attempt
artifact:              $(basename "$tarball")
artifact_sha256:       $artifact_sha
EOF

  printf '%s  %s\n' "$artifact_sha" "$(basename "$tarball")" > "$output/SHA256SUMS-LIBBOX.txt"

  # manifest.json is the authoritative record. The workflow job that uploads this also names the
  # artifact after the commit, but nothing downstream trusts the name.
  #
  # Written with a real JSON encoder rather than by interpolating into a heredoc. Hand-built JSON
  # cannot escape what it interpolates, and a single control character in the Xcode version string
  # was enough to make the manifest unparseable on a runner while it parsed fine on a workstation.
  #
  # parent_sha is the ONLY commit this records. Libbox comes from the core repository and is
  # independent of which Apple UI branch links it - see the header.
  #
  # # Identity and provenance are separate layers, and the split is load-bearing
  #
  #   IDENTITY            parent_sha, artifact_sha256, jj_version
  #                       what a consumer must match to be sure these are the right bytes
  #   ARTIFACT REVISION   version  (JJ_ARTIFACT_VERSION, e.g. 0.1.7+jiejie.98b0708)
  #                       a label the artifact carries; NOT the release version
  #   PROVENANCE          workflow, run_id, run_attempt, go_version, gomobile, xcode_version, slices
  #                       where and how it was built - recorded, never matched
  #
  # `jj_version` exists because `version` cannot answer the question a release asks. `version` is
  # `0.1.7+jiejie.<short_sha>`, so comparing it to release/JIEJIE_VERSION (0.1.7) is false on every
  # artifact ever built. An equality check against the composed string is not "close enough" - it
  # is a check that can only fail.
  #
  # `run_id` is provenance and deliberately NOT identity: a re-run of the same commit produces a
  # different run id for byte-identical output, so binding to it would make the artifact
  # irreproducible by design.
  python3 - "$output/manifest.json" "$parent_sha" "$version" \
    "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$xcode_version" "$go_version" \
    "$(basename "$tarball")" "$artifact_sha" "$jj_version" "$slices" \
    "$gomobile_version" "$gomobile_source" "$workflow_ref" "$run_id" "$run_attempt" <<'PY'
import json
import sys

(path, parent, version, built, xcode, goversion, artifact, digest,
 jj_version, slices, gomobile_version, gomobile_source,
 workflow, run_id, run_attempt) = sys.argv[1:16]

with open(path, "w", encoding="utf-8") as handle:
    json.dump(
        {
            "schema_version": 2,
            # --- identity: match these to prove the bytes are the right bytes ---
            "parent_sha": parent,
            "artifact_sha256": digest,
            "jj_version": jj_version,
            # --- the artifact's own revision label, not the release version ---
            "version": version,
            "artifact": artifact,
            # --- provenance: recorded for a reader, never matched by a consumer ---
            "build_time_utc": built,
            "xcode_version": xcode,
            "go_version": goversion,
            "slices": slices.split(),
            "gomobile_version": gomobile_version,
            "gomobile_source": gomobile_source,
            "workflow": workflow,
            "run_id": run_id,
            "run_attempt": run_attempt,
        },
        handle,
        indent=2,
    )
    handle.write("\n")
PY

  echo "packed:   $tarball"
  echo "sha256:   $artifact_sha"
  echo "jj:       $jj_version"
  echo "slices:   $slices"
  echo "manifest: $output/manifest.json"
}

# --- manifest field ----------------------------------------------------------
#
# Reads one string field without requiring jq, which is not present on every runner image. The
# manifest is written by pack() in a fixed shape, so this is a lookup rather than a parser.
manifest_field() {
  local file="$1" key="$2"
  python3 - "$file" "$key" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    value = json.load(handle).get(sys.argv[2])
if value is None:
    sys.exit(1)
print(value)
PY
}

# manifest_field_list <manifest> <key>
#
# A list field, printed space-separated and SORTED, so it can be compared with the sorted listing
# of a real directory. Exits non-zero when the key is absent or is not a list of strings, which is
# what lets validate distinguish "no slices declared" from "slices declared as empty".
manifest_field_list() {
  local file="$1" key="$2"
  python3 - "$file" "$key" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    document = json.load(handle)
value = document.get(sys.argv[2])
if not isinstance(value, list) or not all(isinstance(v, str) for v in value):
    sys.exit(1)
print(" ".join(sorted(value)))
PY
}

# --- validate ----------------------------------------------------------------
#
# Every gate is fail-closed. A wrong commit, a missing file or a checksum mismatch all stop the
# publish: the whole point of downloading a prebuilt xcframework is that the build it came from is
# known to be the build being shipped.
#
# The parent commit is the only binding. The Apple UI branch is deliberately not checked: the two
# platforms build from two different Apple SHAs and both link this one framework.
validate() {
  local input="$1" expect_parent="$2" expect_jj="${3:-}" require_schema="${4:-1}"

  [ -d "$input" ] || fail "no artifact directory at $input"
  local manifest="$input/manifest.json"
  [ -f "$manifest" ] || fail "the artifact has no manifest.json; it cannot be attributed to a commit"

  local parent_sha artifact_sha
  parent_sha="$(manifest_field "$manifest" parent_sha)" || fail "manifest.json has no parent_sha"
  artifact_sha="$(manifest_field "$manifest" artifact_sha256)" || fail "manifest.json has no artifact_sha256"

  [ "$parent_sha" = "$expect_parent" ] || fail \
    "the Libbox artifact was built from $parent_sha but this checkout is $expect_parent.
  Publishing it would ship a binary that does not match the commit you are releasing."

  # --- schema gate -----------------------------------------------------------
  #
  # The canonical path REQUIRES the v2 fields. A manifest without them is an artifact from before
  # this contract existed, and it is refused rather than accommodated: the alternative is to infer
  # `jj_version` by chopping `+jiejie.<sha>` off `version`, which would mean a release could not
  # tell "this artifact declares the release version" from "this artifact's label happens to parse
  # into something that looks like it". The whole point of an explicit field is that it is explicit.
  #
  # `--allow-legacy-manifest` exists for the test suite and for reading genuinely old artifacts; it
  # is NOT defaulted on, and a caller that wants the old behaviour has to say so.
  local schema
  schema="$(manifest_field "$manifest" schema_version || true)"
  if [ "$require_schema" = "1" ]; then
    [ "$schema" = "2" ] || fail \
      "manifest.json declares schema_version '${schema:-<absent>}', and this consumer requires 2.
  The schema-2 fields (jj_version, slices) are what make this artifact's identity checkable;
  an older artifact can be read with --allow-legacy-manifest, but it cannot be a canonical
  release source. Re-run the Apple producer for this commit."
  fi

  # --- identity: jj_version --------------------------------------------------
  if [ "$require_schema" = "1" ]; then
    local declared_jj
    declared_jj="$(manifest_field "$manifest" jj_version)" || fail \
      "manifest.json has no jj_version. The release version is an explicit field, never inferred from 'version'."
    [ -n "$expect_jj" ] || fail "validate requires --jj-version when the schema is required"
    [ "$declared_jj" = "$expect_jj" ] || fail \
      "the Libbox artifact declares jj_version '$declared_jj', but this release is '$expect_jj'.
  These bytes were produced for a different release."
  fi

  local tarball="$input/$(manifest_field "$manifest" artifact)"
  [ -f "$tarball" ] || fail "manifest names $(manifest_field "$manifest" artifact) but the artifact is missing"

  local actual_sha
  actual_sha="$(sha256_of "$tarball")"
  [ "$actual_sha" = "$artifact_sha" ] || fail \
    "checksum mismatch for $(basename "$tarball"):
  manifest: $artifact_sha
  actual:   $actual_sha"

  # --- slices: declared AND verified against the real archive -----------------
  #
  # A declaration that is never compared to the archive is decoration. This extracts the
  # framework and lists what is actually inside it, then requires the declared set to match
  # exactly - a missing slice means a client cannot link, and an UNDECLARED slice means the
  # manifest is not describing the artifact a consumer is about to install.
  if [ "$require_schema" = "1" ]; then
    local declared actual
    declared="$(manifest_field_list "$manifest" slices)" || fail "manifest.json has no slices array"
    [ -n "$declared" ] || fail "manifest.json declares an empty slices array"

    local staging
    staging="$(mktemp -d)"
    trap 'rm -rf "$staging"' RETURN
    tar -xzf "$tarball" -C "$staging" 2>/dev/null || fail \
      "could not extract $(basename "$tarball") to inspect its slices"
    [ -d "$staging/Libbox.xcframework" ] || fail \
      "the artifact did not contain Libbox.xcframework at its root"
    actual="$(find "$staging/Libbox.xcframework" -mindepth 1 -maxdepth 1 -type d -exec basename {} \; | sort | tr '\n' ' ')"
    actual="${actual% }"

    [ "$declared" = "$actual" ] || fail \
      "the manifest's declared slices do not match the archive:
  declared: $declared
  actual:   $actual
  A slice that is declared but absent cannot be linked; a slice present but undeclared means the
  manifest is not describing these bytes."
    echo "PASS: slices                 $declared"
  fi

  echo "PASS: manifest parent_sha    $parent_sha"
  echo "PASS: artifact sha256        $artifact_sha"
  [ -n "$expect_jj" ] && echo "PASS: jj_version             $expect_jj"
  return 0
}

# --- install -----------------------------------------------------------------
#
# Extracts into an Apple client checkout named by the caller, so the SAME artifact can be
# installed into the iOS checkout and the macOS checkout. The existing directory is replaced rather
# than merged: a stale slice left behind by a previous build would make the two platforms link
# different frameworks while both believed they installed the same artifact.
install() {
  local input="$1" client="$2"
  local manifest="$input/manifest.json"
  [ -f "$manifest" ] || fail "no manifest.json in $input; run validate first"

  [ -d "$client" ] || fail \
    "no Apple client checkout at $client. Check the platform's source out before installing."
  [ -f "$client/sing-box.xcodeproj/project.pbxproj" ] || fail \
    "$client does not look like an Apple client checkout (no sing-box.xcodeproj)."

  local tarball="$input/$(manifest_field "$manifest" artifact)"
  [ -f "$tarball" ] || fail "artifact $(manifest_field "$manifest" artifact) is missing"

  local destination="$client/Libbox.xcframework"
  local staging
  staging="$(mktemp -d)"
  trap 'rm -rf "$staging"' RETURN

  if tar -tzf "$tarball" >/dev/null 2>&1; then
    tar -xzf "$tarball" -C "$staging"
  else
    fail "could not read $tarball"
  fi

  [ -d "$staging/Libbox.xcframework" ] || fail \
    "the artifact did not contain Libbox.xcframework at its root"

  rm -rf "$destination"
  mkdir -p "$(dirname "$destination")"
  if command -v ditto >/dev/null 2>&1; then
    ditto "$staging/Libbox.xcframework" "$destination"
  else
    cp -R "$staging/Libbox.xcframework" "$destination"
  fi

  local slices
  slices="$(ls "$destination" | grep -v Info.plist | tr '\n' ' ')"
  [ -n "$slices" ] || fail "the installed xcframework has no slices"
  echo "installed: $destination"
  echo "slices:    $slices"
}

case "$action" in
  pack)
    pack "${2:?usage: pack <xcframework-dir> <output-dir>}" "${3:?usage: pack <xcframework-dir> <output-dir>}"
    ;;
  validate)
    shift
    input="${1:?usage: validate <input-dir> --parent-sha <sha> [--jj-version <v>] [--allow-legacy-manifest]}"
    shift
    expect_parent=""
    expect_jj=""
    require_schema=1
    while [ $# -gt 0 ]; do
      case "$1" in
        --parent-sha) expect_parent="${2:?}"; shift 2 ;;
        --jj-version) expect_jj="${2:?}"; shift 2 ;;
        # Opts OUT of the schema-2 requirement, for reading genuinely old artifacts and for the
        # test suite's legacy case. Never the default: a canonical release source must declare its
        # version and its slices.
        --allow-legacy-manifest) require_schema=0; shift ;;
        # Accepted and ignored so an older caller that still passes it is not silently
        # mis-parsed into a different gate. It binds nothing: see the header.
        --submodule-sha) shift 2 ;;
        *) fail "unknown argument '$1'" ;;
      esac
    done
    [ -n "$expect_parent" ] || fail "validate requires --parent-sha"
    validate "$input" "$expect_parent" "$expect_jj" "$require_schema"
    ;;
  install)
    install "${2:?usage: install <input-dir> <client-dir>}" "${3:?usage: install <input-dir> <client-dir>}"
    ;;
esac
