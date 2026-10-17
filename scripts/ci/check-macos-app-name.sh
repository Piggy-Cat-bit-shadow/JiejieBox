#!/usr/bin/env bash
# Fails unless an archived macOS app carries the name this fork ships it under.
#
# Usage: check-macos-app-name.sh <app-bundle> [expected-display-name]
#
# # Why this is its own script
#
# The macOS product reached TestFlight still called sing-box because nothing checked
# the NAME - every gate that existed looked at identifiers, entitlements and signing.
# The branding overlay rewrites the pinned project's source, so the archive is the
# first place the result becomes visible, and an app uploaded under the wrong name
# cannot be corrected without another build. This is the gate that exists for it.
#
# It is separate from build-macos-testflight.sh so that the check can be exercised
# against a fixture bundle without archiving anything: a gate that has never been run
# against a bundle named sing-box is not known to catch one.
#
# # What "the name" means
#
#   CFBundleDisplayName   the name in the menu bar, the Dock and About: JiejieBox.
#   the .app directory    the product name, and what Finder and /Applications show:
#                         JiejieBox.app. It follows PRODUCT_NAME, which the Apple client
#                         declares for the SFM target. The rename exists so the App
#                         Store app no longer shares the official client's name.
#   CFBundleName          also follows PRODUCT_NAME, and is the fallback when no
#                         display name is present; when the key is there it must be
#                         JiejieBox.
#
# An earlier revision of this gate required the bundle directory and CFBundleName to
# stay `sing-box` while only the display name was branded, because PRODUCT_NAME was
# treated as identity. That is no longer the contract: the client's own project declares
# JiejieBox for SFM, and a `sing-box.app` in the archive is now the defect - it is the
# collision with the official client that this rename removes.
#
# The identifier surface - bundle id, App Group, extension bundle ids - is asserted by
# the signing gates rather than here: this script judges the archive's name, and the
# overlay and prepared-tree checks judge the project it was built from.
#
# CFBundleName is only asserted when it is present: the project generates its Info.plist
# and a missing key is legitimate, so requiring it would fail on a correct build. A key
# that is present and wrong is a real disagreement and is treated as one.
#
# # Expected names
#
# Both are read from the branding overlay rather than passed in by every caller, so the
# display name the overlay writes, the product name it verifies and the names this gate
# requires cannot drift apart. A caller may still pass the display name explicitly as $2.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
  echo "usage: check-macos-app-name.sh <app-bundle> [expected-display-name]" >&2
  exit 2
fi

app="$1"
overlay="$root/scripts/ci/apply-apple-macos-branding-overlay.py"
expected="${2:-}"

# Read the two names from the overlay's own constants. The pattern is anchored at column
# 0, NOT `^\s*`: the overlay's docstring quotes the Apple repo's own
# `BRAND_DISPLAY_NAME = "Jiejiebox"` line as background, indented, and a leading-\s*
# match finds that quote first and makes this gate require the wrong spelling. The real
# assignment is the only one at the start of a line.
read_overlay() {
  python3 - "$overlay" "$1" <<'PY'
import ast, re, sys

path, which = sys.argv[1], sys.argv[2]
src = open(path, encoding="utf-8").read()

if which == "display":
    m = re.search(r'^BRAND_DISPLAY_NAME = "([^"]+)"', src, re.M)
    if not m:
        sys.exit("the macOS branding overlay declares no BRAND_DISPLAY_NAME")
    print(m.group(1))
else:
    # PROTECTED is a dict literal of name -> value; parse it rather than guessing.
    m = re.search(r'^PROTECTED = (\{.*?\n\})', src, re.M | re.S)
    if not m:
        sys.exit("the macOS branding overlay declares no PROTECTED mapping")
    protected = ast.literal_eval(m.group(1))
    if "PRODUCT_NAME" not in protected:
        sys.exit("the macOS branding overlay protects no PRODUCT_NAME")
    print(protected["PRODUCT_NAME"])
PY
}

if [ -z "$expected" ]; then
  expected="$(read_overlay display)"
  if [ -z "$expected" ]; then
    echo "check-macos-app-name: cannot resolve the expected display name from $overlay" >&2
    exit 2
  fi
fi

product="$(read_overlay identity)"
if [ -z "$product" ]; then
  echo "check-macos-app-name: cannot resolve the expected product name from $overlay" >&2
  exit 2
fi

fail() {
  echo "check-macos-app-name: $*" >&2
  exit 1
}

[ -d "$app" ] || fail "$app is not an application bundle"
plist="$app/Contents/Info.plist"
[ -f "$plist" ] || fail "$app has no Contents/Info.plist"

read_key() {
  /usr/libexec/PlistBuddy -c "Print :$1" "$plist" 2>/dev/null || true
}

display="$(read_key CFBundleDisplayName)"
name="$(read_key CFBundleName)"
bundle_name="$(basename "$app")"

# 1. The user-visible name. This is what the branding overlay exists to set.
if [ "$display" != "$expected" ]; then
  fail "CFBundleDisplayName is '${display:-<missing>}', expected '$expected'"
fi

# 2. The bundle directory follows PRODUCT_NAME, and the product name is JiejieBox.app.
#    Checked as an equality against the name the overlay declares rather than "not
#    sing-box", so a third name is caught too rather than only the collision we happened
#    to think of.
if [ "$bundle_name" != "$product.app" ]; then
  fail "the app bundle is named '$bundle_name', expected '$product.app'.
  The bundle directory name follows PRODUCT_NAME, which the SFM project declares as
  '$product'. A bundle named 'sing-box.app' means the rename did not reach the project -
  see the header of this script."
fi

# 3. CFBundleName follows PRODUCT_NAME too. Absent is fine; present and wrong is not.
if [ -n "$name" ] && [ "$name" != "$product" ]; then
  fail "CFBundleName is '$name', expected '$product'.
  CFBundleName follows PRODUCT_NAME, not CFBundleDisplayName (brand),
  so '$product' is the correct pairing."
fi

echo "check-macos-app-name: PASS ($bundle_name, CFBundleDisplayName=$display, CFBundleName=${name:-<absent>})"
