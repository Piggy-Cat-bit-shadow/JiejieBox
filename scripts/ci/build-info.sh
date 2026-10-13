#!/usr/bin/env bash
# Writes BUILD-INFO.txt for one artifact.
#
# Deliberately records the TIMESTAMP only in this sidecar file, never in the
# binary. Embedding a timestamp in the binary would make the same commit produce
# a different hash on every build and destroy reproducibility; the sidecar gives
# operators the build date without that cost.
#
# Usage: build-info.sh <output-file> <platform> <arch> <flavor> [binary]
set -euo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

out="$1"; platform="$2"; arch="$3"; flavor="$4"; binary="${5:-}"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

eval "$(./scripts/ci/version.sh)"

# This fork ships exactly TWO products: the Server Edition (Linux amd64 VPS) and
# the macOS arm64 core. Each has exactly one flavor, listed here in one place. A
# flavor missing from this case would silently record "unknown" tags in the
# sidecar, which is worse than failing: the artifact would look documented while
# claiming nothing.
#
# The aliases exist so a caller can name the product rather than the tag file.
# Both products now use upstream's own feature profiles; the only difference
# between them is the profile upstream itself selects for that platform.
#
# `server-minimal` is accepted as a legacy alias only. The product is not minimal any
# more - it carries the full upstream registry - so the canonical name is `server`,
# and that is what the workflows emit.
tags_file=""
build_profile=""
case "$flavor" in
  server)                  tags_file="release/DEFAULT_BUILD_TAGS_OTHERS" ;;
  macos|client-macos)      tags_file="release/DEFAULT_BUILD_TAGS" ;;
  windows)                 tags_file="release/DEFAULT_BUILD_TAGS_WINDOWS" ;;
  android)                 tags_file="" ;;
  server-minimal)          tags_file="release/DEFAULT_BUILD_TAGS_OTHERS" ;;
esac
if [ "$flavor" = android ]; then
  # The ANDROID tags come from the builder that compiles the AAR, not from a list kept here.
  #
  # This line used to be `go run ./cmd/internal/appletags -mobile-only`, which resolves to
  # mobilebuildtags.LowMemoryTags() - `with_low_memory` and nothing else. That is the Apple
  # mobile-only overlay, NOT the set the Android AAR is built with, so the shipped
  # BUILD-INFO-ANDROID.txt claimed:
  #
  #     build_tags:            with_low_memory
  #
  # for an artifact compiled with the ~25-tag android-main profile - and then described itself as
  # reproducible because "its hash is determined by the source commit and tags alone". The record
  # was the one thing that could not be checked against the artifact it described.
  #
  # `-print-resolved-tags` is the SAME flag the artifact workflow already uses to assert its
  # product decisions, and it reaches ResolveBuildTags("android-main") - the function the builder
  # itself calls before handing the set to gomobile. One source, no second list, and no shell-side
  # reconstruction. It prints and exits without needing the Android SDK or Java.
  build_profile="android-main"
  tags="$(go run ./cmd/internal/build_libbox -print-resolved-tags "$build_profile")"
  # Recorded ALONGSIDE rather than instead of: it is the mobile overlay that is genuinely applied
  # on top of the profile, and a consumer asking "does this artifact carry the mobile geometry"
  # still gets an answer without diffing the full set.
  mobile_tags="$(go run ./cmd/internal/appletags -mobile-only)"
elif [ -z "$tags_file" ] || [ ! -f "$tags_file" ]; then
  echo "build-info.sh: unknown flavor '$flavor' (no tag file)" >&2
  exit 2
else
  tags="$(cat "$tags_file")"
fi

# A tag set is never legitimately empty. An empty one would make the record claim a build with no
# features at all, which is a worse lie than the one this function was fixed to stop telling.
if [ -z "$tags" ]; then
  echo "build-info.sh: resolved an EMPTY tag set for flavor '$flavor'" >&2
  exit 2
fi

# Dependency versions are read from the ACTUAL module graph, not hardcoded, so
# the file cannot claim a version the binary was not built against.
dep() { go list -m -f '{{.Version}}' "$1" 2>/dev/null || echo unknown; }

# effective_dep reports the module a dependency ACTUALLY resolves to, following
# any `replace`.
#
# This matters for github.com/sagernet/sing, which this fork replaces with
# github.com/Piggy-Cat-bit-shadow/sing. `go list -m -f '{{.Version}}'` reports
# the ORIGINAL upstream version from the require line and says nothing about the
# replacement, so a plain dep() call recorded a version the binary was NOT built
# against — the exact failure the comment above says this file must not have. It
# was invisible because the upstream version is a real, plausible string.
effective_dep() {
  go list -m -f '{{if .Replace}}{{.Replace.Path}}@{{.Replace.Version}} (replaces {{.Path}}@{{.Version}}){{else}}{{.Path}}@{{.Version}}{{end}}' "$1" 2>/dev/null || echo unknown
}

{
  echo "Jiejie sing-box build information"
  echo "================================="
  echo "flavor:                $flavor"
  echo "platform:              $platform"
  echo "arch:                  $arch"
  echo "version:               $JJ_VERSION"
  echo "artifact_version:      $JJ_ARTIFACT_VERSION"
  echo "git_commit:            $JJ_COMMIT"
  echo "git_short_sha:         $JJ_SHORT_SHA"
  echo "git_branch:            $JJ_BRANCH"
  echo "build_time_utc:        $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "go_version:            $JJ_GO_VERSION"
  # build_profile names WHICH resolved profile the artifact was compiled from; build_tags is that
  # profile's complete, resolved set. Both are emitted for the flavors that have a named profile, so
  # a reader can tell "this is android-main" without diffing 25 tags by hand.
  if [ -n "$build_profile" ]; then
    echo "build_profile:         $build_profile"
  fi
  echo "build_tags:            $tags"
  if [ -n "$mobile_tags" ]; then
    echo "mobile_tags:           $mobile_tags"
  fi
  echo "quic_go:               $(dep github.com/sagernet/quic-go)"
  echo "sing_quic:             $(dep github.com/sagernet/sing-quic)"
  echo "sing:                  $(effective_dep github.com/sagernet/sing)"
  echo ""
  echo "This build embeds NO timestamp: its hash is determined by the source"
  echo "commit and tags alone, so it is reproducible. The build_time_utc above is"
  echo "informational and lives only in this file."
} > "$out"

if [ -n "$binary" ] && [ -f "$binary" ]; then
  {
    echo ""
    echo "artifact:              $(basename "$binary")"
    echo "artifact_bytes:        $(wc -c < "$binary" | tr -d ' ')"
    echo "artifact_sha256:       $(sha256_of "$binary")"
  } >> "$out"
fi
