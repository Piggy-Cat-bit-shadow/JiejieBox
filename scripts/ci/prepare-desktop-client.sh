#!/usr/bin/env bash
# Prepares the desktop (Electron) client checkout for an UNSIGNED Windows x64 build.
#
# Usage: prepare-desktop-client.sh [--version <X.Y.Z>]
#
# Environment:
#   DESKTOP_CLIENT_DIR   the desktop checkout to prepare (default: clients/desktop)
#
# # Where the version comes from, and why it is not optional
#
# The Jiejiebox release version has exactly ONE authority: `release/JIEJIE_VERSION` in the
# checkout being released. This script owns the root context, so it reads that file - the
# overlay it calls cannot, because it runs against the client tree with no root context.
# The value is then passed down explicitly rather than duplicated.
#
# With no --version the file is read; with one, the two must agree. Both directions are
# refused rather than reconciled: a caller that thinks it is releasing a different version
# than the checkout says is a caller about to ship an installer named for one version and
# containing another, and the updater then either refuses to upgrade to it or offers it
# forever.
#
# # Why this is an overlay rather than a commit in the client
#
# clients/desktop is a submodule pinned to upstream SagerNet/sing-box-for-desktop. Branding
# cannot be committed into a pinned upstream checkout - doing so would either need a fork of
# that repository or an unpinned working tree, and either one breaks the property that the
# client source is exactly the revision the parent records.
#
# So this follows the same shape as prepare-apple-client.sh: the parent records the revision,
# and the parent owns the overlay. Re-running it is safe, and it fails closed rather than
# leaving a half-branded client behind.
#
# # What it deliberately does NOT touch
#
#   sing-box-daemon.exe   the daemon keeps its name; it is an internal component
#   appId                 io.nekohasekai.sfw / .sfl are install identity, not branding
#   IPC / service ids     internal identifiers
#   driver, WinDivert     driver names
#   config schema         user configuration compatibility
#   localStorage keys     renaming them would silently discard a user's theme and accent
#   attribution           SagerNet authorship, the licence and the fork lineage
#
# The rule is the one the brief states: the user sees Jiejiebox, and the internals keep the
# names they already have. There is no global string replacement here.
#
# # Why the rules live in a Python file
#
# The edit set is exact, counted, multi-line, and has to be planned before anything is
# written so that a partial overlay is impossible. Expressing that in shell heredocs is
# what broke an earlier attempt at this overlay, so it lives next to the other
# apply-*-overlay.py scripts instead.
set -euo pipefail

usage="usage: prepare-desktop-client.sh [--version <X.Y.Z>]"

version=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || { echo "FAIL: --version needs a value" >&2; exit 2; }
      version="$2"
      shift 2
      ;;
    --version=*)
      version="${1#--version=}"
      shift
      ;;
    -h|--help)
      echo "$usage" >&2
      exit 0
      ;;
    *)
      echo "FAIL: unknown argument '$1'. $usage" >&2
      exit 2
      ;;
  esac
done

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
client_dir="${DESKTOP_CLIENT_DIR:-clients/desktop}"
[ -d "$client_dir" ] || { echo "FAIL: $client_dir is missing" >&2; exit 1; }

# The single authority for the fork's release version.
version_file="$root/release/JIEJIE_VERSION"
[ -f "$version_file" ] || { echo "FAIL: no $version_file" >&2; exit 1; }
file_version="$(tr -d '[:space:]' < "$version_file")"
[ -n "$file_version" ] || { echo "FAIL: $version_file is empty" >&2; exit 1; }

if [ -n "$version" ]; then
  if [ "$version" != "$file_version" ]; then
    echo "FAIL: --version $version but release/JIEJIE_VERSION says $file_version." >&2
    echo "      The overlay stamps the version it is given into the app and the installer" >&2
    echo "      name, so the two must be the same release. Refusing rather than choosing." >&2
    exit 1
  fi
else
  version="$file_version"
fi

case "$version" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "FAIL: release version '$version' is not a dotted numeric version" >&2; exit 1 ;;
esac

builder="$client_dir/electron-builder.yml"
pkg="$client_dir/package.json"
for f in "$builder" "$pkg"; do
  [ -f "$f" ] || { echo "FAIL: $f is missing; is $client_dir a desktop checkout?" >&2; exit 1; }
done

overlay="$root/scripts/ci/apply-desktop-branding-overlay.py"
[ -f "$overlay" ] || { echo "FAIL: $overlay is missing" >&2; exit 1; }

# The overlay itself is Python. macOS and Linux expose it as python3; the Windows
# runners this overlay also has to run on usually expose it as python, so the name is
# resolved rather than assumed, and PYTHON overrides both.
python_bin="${PYTHON:-}"
if [ -z "$python_bin" ]; then
  for candidate in python3 python; do
    if command -v "$candidate" >/dev/null 2>&1; then
      python_bin="$candidate"
      break
    fi
  done
fi
[ -n "$python_bin" ] || { echo "FAIL: python3 is required to apply the overlay" >&2; exit 1; }

echo "== desktop overlay: $client_dir =="
echo "   release version: $version (release/JIEJIE_VERSION)"

# Every rule is resolved before the first byte is written, and every rule is re-read
# afterwards. A rule that cannot find its anchor - because the pinned client changed shape -
# aborts the whole overlay instead of leaving a client branded in some places and not others.
"$python_bin" "$overlay" "$client_dir" --version "$version"

echo "PASS: desktop client prepared"
