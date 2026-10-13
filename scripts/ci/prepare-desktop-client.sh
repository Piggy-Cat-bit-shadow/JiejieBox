#!/usr/bin/env bash
# Prepares the desktop (Electron) client checkout for an UNSIGNED Windows x64 build.
#
# Usage: prepare-desktop-client.sh [--version <X.Y.Z>]
#
# Environment:
#   DESKTOP_CLIENT_DIR   the desktop checkout to prepare (default: clients/desktop)
#
# # What this script owns now
#
# Two things, and nothing else:
#
#   source identity   the checkout is this repository's pinned client at the revision the
#                     parent records, and it is otherwise clean
#   release identity  release/JIEJIE_VERSION is stamped into the client's version.json
#
# # Why the static branding overlay is gone
#
# The client used to be a checkout of upstream SagerNet/sing-box-for-desktop, and the parent
# carried a 23-rule overlay (apply-desktop-branding-overlay.py) that rewrote the client's
# source before every build. The maintained fork
# Piggy-Cat-bit-shadow/sing-box-for-desktop @ jiejiebox/main now carries all of it - the
# updater's release feed and asset matcher, the installer name, the unsigned-build changes,
# the visible branding and the installer diagnostics - and the parent pins that fork like
# any other submodule.
#
# That makes the overlay not merely redundant but wrong: re-applying it would assert a shape
# the fork already satisfies, and any rule that stopped matching would fail a build whose
# source is, in fact, exactly the revision the parent records. The overlay was retired after
# being run against the pinned revision and reporting "nothing to write".
#
# # Where the version comes from, and why it is not optional
#
# The Jiejiebox release version has exactly ONE authority: `release/JIEJIE_VERSION` in the
# checkout being released. This script owns the root context, so it reads that file - the
# injector it calls cannot, because it runs against the client tree with no root context.
# The value is then passed down explicitly rather than duplicated.
#
# With no --version the file is read; with one, the two must agree. Both directions are
# refused rather than reconciled: a caller that thinks it is releasing a different version
# than the checkout says is a caller about to ship an installer named for one version and
# containing another, and the updater then either refuses to upgrade to it or offers it
# forever.
#
# # Why the identity checks are here and not in the injector
#
# The injector edits one file and needs no repository context, which is what makes it
# testable against a fixture in CI. The identity checks need git, the parent checkout and a
# superproject, so they live in the script that has all three. Splitting them this way is
# also what keeps the stronger check affordable: a submodule move that did not take effect
# is invisible to `git add` (which succeeds either way) and to a build (which reads whatever
# is checked out), so the two hashes are compared and printed on every run.
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
cd "$root"
client_dir="${DESKTOP_CLIENT_DIR:-clients/desktop}"
[ -d "$client_dir" ] || {
  echo "FAIL: $client_dir is missing" >&2
  echo "      the client is a submodule: git submodule update --init $client_dir" >&2
  exit 1
}

# The single authority for the fork's release version.
version_file="$root/release/JIEJIE_VERSION"
[ -f "$version_file" ] || { echo "FAIL: no $version_file" >&2; exit 1; }
file_version="$(tr -d '[:space:]' < "$version_file")"
[ -n "$file_version" ] || { echo "FAIL: $version_file is empty" >&2; exit 1; }

if [ -n "$version" ]; then
  if [ "$version" != "$file_version" ]; then
    echo "FAIL: --version $version but release/JIEJIE_VERSION says $file_version." >&2
    echo "      The stamp reaches the app and the installer name, so the two must be the" >&2
    echo "      same release. Refusing rather than choosing." >&2
    exit 1
  fi
else
  version="$file_version"
fi

case "$version" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "FAIL: release version '$version' is not a dotted numeric version" >&2; exit 1 ;;
esac

record="$client_dir/version.json"
builder="$client_dir/electron-builder.yml"
pkg="$client_dir/package.json"
for f in "$record" "$builder" "$pkg"; do
  [ -f "$f" ] || {
    echo "FAIL: $f is missing; is $client_dir a desktop client checkout?" >&2
    exit 1
  }
done

injector="$root/scripts/ci/inject-desktop-release-version.py"
[ -f "$injector" ] || { echo "FAIL: $injector is missing" >&2; exit 1; }

# The injector itself is Python. macOS and Linux expose it as python3; the Windows runners
# this also has to run on usually expose it as python, so the name is resolved rather than
# assumed, and PYTHON overrides both.
python_bin="${PYTHON:-}"
if [ -z "$python_bin" ]; then
  for candidate in python3 python; do
    if command -v "$candidate" >/dev/null 2>&1; then
      python_bin="$candidate"
      break
    fi
  done
fi
[ -n "$python_bin" ] || { echo "FAIL: python3 is required to stamp the version" >&2; exit 1; }

# --- source identity ---------------------------------------------------------
#
# Proved against the PARENT's committed record (`HEAD:clients/desktop`), not against the
# staging area: a workflow runs against a commit, and a gitlink that was staged but never
# committed is the exact silent failure this section exists to catch. `git add submodule`
# exits 0 whether or not the submodule branch ref actually moved to the new revision.
echo "== desktop source identity: $client_dir =="

if ! git -C "$client_dir" rev-parse --git-dir >/dev/null 2>&1; then
  echo "FAIL: $client_dir is not a git checkout." >&2
  echo "      the client is a submodule: git submodule update --init $client_dir" >&2
  exit 1
fi

client_head="$(git -C "$client_dir" rev-parse HEAD)"
recorded="$(git rev-parse "HEAD:$client_dir" 2>/dev/null || true)"
[ -n "$recorded" ] || {
  echo "FAIL: HEAD records no gitlink for $client_dir" >&2
  exit 1
}

printf '   client HEAD        = %s\n' "$client_head"
printf '   parent records     = %s\n' "$recorded"
if [ "$client_head" != "$recorded" ]; then
  echo "FAIL: the checked-out client revision is not the one this commit records." >&2
  echo "      The build would package a revision that is not part of the release." >&2
  echo "      Fix with: git submodule update --init $client_dir" >&2
  exit 1
fi
echo "   MATCH: the client is the revision this commit records"

# The dashboard is a submodule of the client, so it has a second, independent chance to be
# silently stale. Its recorded revision is read the same way, one level down.
dashboard_rel="$client_dir/dashboard"
if git -C "$client_dir" ls-files -s dashboard >/dev/null 2>&1 &&
   [ -n "$(git -C "$client_dir" ls-files -s dashboard)" ]; then
  dashboard_recorded="$(git -C "$client_dir" rev-parse "HEAD:dashboard")"
  if git -C "$dashboard_rel" rev-parse --git-dir >/dev/null 2>&1; then
    dashboard_head="$(git -C "$dashboard_rel" rev-parse HEAD)"
    printf '   dashboard HEAD     = %s\n' "$dashboard_head"
    printf '   client records     = %s\n' "$dashboard_recorded"
    if [ "$dashboard_head" != "$dashboard_recorded" ]; then
      echo "FAIL: the checked-out dashboard revision is not the one the client records." >&2
      echo "      Fix with: git -C $client_dir submodule update --init dashboard" >&2
      exit 1
    fi
    echo "   MATCH: the dashboard is the revision the client records"
  else
    echo "FAIL: $dashboard_rel is not checked out." >&2
    echo "      Fix with: git -C $client_dir submodule update --init dashboard" >&2
    exit 1
  fi
fi

# The source must be otherwise clean BEFORE the stamp, because after it version.json is
# expected to differ. A dirty tree here means the artifact would contain edits that are in
# no commit, which is how the v0.1.6 working tree came to disagree with its own tag.
dirty_before="$(git -C "$client_dir" status --porcelain)"
if [ -n "$dirty_before" ]; then
  echo "FAIL: the client checkout is not clean before the version stamp:" >&2
  printf '%s\n' "$dirty_before" >&2
  echo "      Refusing: uncommitted client source would ship in the artifact." >&2
  exit 1
fi
echo "   clean: the client checkout has no uncommitted changes"

# --- dynamic release version -------------------------------------------------
echo "== desktop release version: $version (release/JIEJIE_VERSION) =="
"$python_bin" "$injector" "$client_dir" --version "$version"

# --- confirm the stamp is the ONLY mutation ----------------------------------
#
# The injector proves version.json is well formed and that no other FIELD changed. This
# proves the stronger, repository-level claim: of every file in the client tree, exactly
# version.json differs from the recorded revision afterwards. A stamp that also rewrote a
# second file would otherwise be invisible here and shipped.
dirty_after="$(git -C "$client_dir" status --porcelain)"
if [ "$dirty_after" != " M version.json" ] && [ "$dirty_after" != "M  version.json" ]; then
  echo "FAIL: after the version stamp the only modified file must be version.json." >&2
  printf '      found:\n%s\n' "${dirty_after:-<nothing>}" >&2
  exit 1
fi

stamped="$("$python_bin" "$injector" "$client_dir" --check-only | awk '/source version/ {print $4}')"
if [ "$stamped" != "$version" ]; then
  echo "FAIL: version.json reads $stamped after the stamp, expected $version" >&2
  exit 1
fi
echo "   version.json is exactly $version, and nothing else in the client moved"

echo "PASS: desktop client prepared"
