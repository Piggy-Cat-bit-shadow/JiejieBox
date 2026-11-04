#!/usr/bin/env bash
# Write the source manifest for this build: every revision the artifact was made from.
#
# # Why one file
#
# A build that reports "0.1.37" says nothing about which core, which client, or which fork pins
# produced it. Two artifacts of the same version can be built from different sources, and the
# release path's job is to know the difference. This writes that answer once, at the start of a
# build, so the later stages (IPA, macOS app, DMG, TestFlight, Release) can read one file instead
# of each resolving something of their own.
#
# # What it does NOT do
#
# It does not change what the build uses. The dependency pins are the ones recorded in `go.mod` and
# the source lock; the Apple revision is what the caller selected (the parent gitlink by default).
# It records them. A build that tracks a moving branch resolves it to an exact SHA here and freezes
# that SHA for the rest of the run - which is the property that matters, not the frequency of
# resolution.
#
# # Usage
#
#   scripts/ci/source-manifest.sh OUT.json --apple-sha <sha> [--platform ios]
#
# Exit status is 0 only when every field could be determined. A manifest with a blank revision is
# worse than none: it looks like provenance.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

out=""
apple_sha=""
platform="ios"

while [ $# -gt 0 ]; do
  case "$1" in
    --apple-sha) apple_sha="$2"; shift 2 ;;
    --platform) platform="$2"; shift 2 ;;
    -*) echo "source-manifest: unknown option $1" >&2; exit 2 ;;
    *) out="$1"; shift ;;
  esac
done

if [ -z "$out" ]; then
  echo "source-manifest: an output path is required" >&2
  exit 2
fi
if [ -z "$apple_sha" ]; then
  # Same rule as the workflows: this commit's gitlink is the default client revision.
  apple_sha="$(git ls-tree HEAD clients/apple | awk '{print $3}')"
fi
case "$apple_sha" in
  *[!0-9a-f]*|'') echo "source-manifest: not a 40-hex Apple revision: '$apple_sha'" >&2; exit 1 ;;
esac
if [ "${#apple_sha}" != 40 ]; then
  echo "source-manifest: not a 40-hex Apple revision: '$apple_sha'" >&2
  exit 1
fi

core_sha="$(git rev-parse HEAD)"
version="$(tr -d '[:space:]' < release/JIEJIE_VERSION)"
go_version="$(go env GOVERSION)"

# The fork pins, as the build will actually use them: replacements in go.mod. Recorded in file
# order so two manifests of the same tree are byte-identical.
pins_json="$(
  grep -E '^replace ' go.mod | awk '{print $2, $4, $5}' \
    | python3 -c '
import json, sys
pins = []
for line in sys.stdin:
    parts = line.split()
    if len(parts) == 3:
        pins.append({"module": parts[0], "path": parts[1], "version": parts[2]})
print(json.dumps(pins, indent=2, sort_keys=True))
' 2>/dev/null || echo "[]"
)"

mkdir -p "$(dirname "$out")"
cat > "$out" <<JSON
{
  "schema": 1,
  "platform": "${platform}",
  "core": {
    "repository": "https://github.com/Piggy-Cat-bit-shadow/sing-box",
    "sha": "${core_sha}"
  },
  "apple_client": {
    "repository": "https://github.com/Piggy-Cat-bit-shadow/sing-box-for-apple",
    "sha": "${apple_sha}"
  },
  "version": "${version}",
  "go": "${go_version}",
  "pins": ${pins_json},
  "generated_by": "scripts/ci/source-manifest.sh"
}
JSON

# A manifest is only provenance if it names a revision for each source.
python3 - "$out" <<'PY'
import json
import sys

manifest = json.load(open(sys.argv[1], encoding="utf-8"))
for field, value in (
    ("core.sha", manifest["core"]["sha"]),
    ("apple_client.sha", manifest["apple_client"]["sha"]),
    ("version", manifest["version"]),
):
    if not value:
        raise SystemExit(f"source-manifest: {field} is empty")
PY

echo "source manifest: $out"
echo "  core:  $core_sha"
echo "  apple: $apple_sha"
echo "  version: $version"
