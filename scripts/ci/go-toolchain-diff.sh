#!/usr/bin/env bash
# Runs one test selection under more than one Go toolchain and prints a comparison table.
#
# Usage:
#   scripts/ci/go-toolchain-diff.sh <test-pattern> <package> [toolchain-root]...
#
#   scripts/ci/go-toolchain-diff.sh \
#     'TestARecoveryPinsARemoteSubscriptionSnapshot' ./daemon \
#     /c/Deepseek/.tools/go1.25.5 /c/Deepseek/.tools/go1.27.4
#
# Each toolchain root is a GOROOT that contains bin/go(.exe). With none given, the toolchains this
# repository's history has used are tried: the language floor from go.mod's `go` directive and the
# pinned `toolchain`, looked up under /c/Deepseek/.tools/go<VERSION> (the layout this workspace uses).
#
# # Why this exists
#
# A test that passes under one toolchain and fails under another is the ONLY direct evidence that an
# upgrade changed behaviour. Everything else - release notes, source diffs, static gates - narrows the
# search; this decides it. It is how the 1.27 provider-regression below was found, and it is the first
# thing to run when a test starts failing after a `toolchain` bump:
#
#   docs/fork/go-1274-regressions.md
#
# # What it does not do
#
# It does not bisect. Two toolchains give you "changed between A and B"; adding a third narrows it.
# It does not build a separate module cache per toolchain either: GOMODCACHE is shared and safe to be,
# because module content is content-addressed and toolchain-independent.
#
# GOTOOLCHAIN=local is forced, because otherwise the newer go command would silently download and use
# the pinned toolchain and every row would test the same compiler.
set -uo pipefail

if [ "$#" -lt 2 ]; then
  echo "usage: $0 <test-pattern> <package> [toolchain-root]..." >&2
  exit 2
fi

pattern="$1"
package="$2"
shift 2

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

toolchains=("$@")
if [ "${#toolchains[@]}" -eq 0 ]; then
  floor="$(awk '$1 == "go" && NF >= 2 { print $2; exit }' go.mod)"
  pinned="$(awk '$1 == "toolchain" && NF >= 2 { sub(/^go/, "", $2); print $2; exit }' go.mod)"
  for version in "$floor" "$pinned"; do
    [ -n "$version" ] || continue
    toolchains+=("/c/Deepseek/.tools/go${version}")
  done
fi

# goTool resolves a toolchain root to its go binary. Two layouts are in use here: an unpacked tarball
# (<root>/bin/go) and an unzipped release archive (<root>/go/bin/go.exe).
goTool() {
  local root="$1"
  local candidate
  for candidate in "$root/bin/go" "$root/bin/go.exe" "$root/go/bin/go" "$root/go/bin/go.exe"; do
    if [ -x "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

if [ "${#toolchains[@]}" -eq 0 ]; then
  echo "no toolchains to compare; pass GOROOTs or place them under /c/Deepseek/.tools/go<VERSION>" >&2
  exit 2
fi

echo "package : $package"
echo "pattern : $pattern"
echo

verdicts=()
for toolchain in "${toolchains[@]}"; do
  if ! gobin="$(goTool "$toolchain")"; then
    echo "SKIP  $toolchain (no bin/go)" >&2
    continue
  fi
  version="$(GOTOOLCHAIN=local "$gobin" version 2>/dev/null | awk '{print $3}')"
  log="$(mktemp)"
  # -count=1: a cached PASS would compare toolchains it never ran on.
  if GOTOOLCHAIN=local "$gobin" test -count=1 -run "$pattern" "$package" >"$log" 2>&1; then
    verdicts+=("$version:PASS")
  else
    verdicts+=("$version:FAIL")
    echo "--- $version failed; last 20 lines:"
    tail -20 "$log" | sed 's/^/    /'
    echo
  fi
  rm -f "$log"
done

echo "== comparison =="
printf '%s\n' "${verdicts[@]}"

distinct="$(printf '%s\n' "${verdicts[@]}" | awk -F: '{print $2}' | sort -u | wc -l)"
if [ "$distinct" -gt 1 ]; then
  echo
  echo "RESULT: the toolchains DISAGREE. That is a behavioural change caused by the toolchain, not a"
  echo "        flaky test. Record it in docs/fork/go-1274-regressions.md with these exact commands."
  exit 1
fi

echo
echo "RESULT: every toolchain agrees, so this selection is not evidence of a toolchain-caused change."
