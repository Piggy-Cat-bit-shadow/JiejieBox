#!/usr/bin/env bash
# EXPERIMENTAL (phase 1): builds a self-sign-friendly UNSIGNED iOS IPA.
#
# Usage:
#   scripts/experiment/build-ios-selfsign-ipa.sh [--diagnostic] <output-ipa>
#
# # Why this is a separate script
#
# The official unsigned builder (`scripts/ci/build-ios-ipa.sh`) is what the release line runs,
# and phase 1 must not change it. But it leaves three things at the Xcode project's defaults,
# and all three matter for a build that will be re-signed by a third-party tool:
#
#   version     MARKETING_VERSION/CURRENT_PROJECT_VERSION were never passed, so the artifact
#               claimed the upstream client's 1.14.8 (10) while its file name and the release
#               file said 0.1.37. The version a user sees in Settings and the version the
#               artifact is named after must be the same number.
#   identity    BASE_PACKAGE_IDENTIFIER was only passed in development mode, so an unsigned
#               build carried io.nekohasekai.* - upstream's identifiers, not this fork's, and
#               not what a signer will map the package to.
#   layout      Every extension in the project ships, which is the maximum number of nested
#               bundles a re-signing tool has to get right. `--diagnostic` builds the same
#               app with ONLY the Packet Tunnel extension, so a launch failure can be
#               attributed to signing surface rather than guessed at.
#
# Everything else is deliberately the same as the proven builder: the same settings that make
# the build unsigned, the same package resolution first, the same ditto-based staging, and the
# same removal of signature residue.
#
# This script signs nothing. It cannot grant an entitlement the provisioning profile does not
# have, and it does not try.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

diagnostic=false
if [ "${1:-}" = "--diagnostic" ]; then
  diagnostic=true
  shift
fi

out="${1:?usage: build-ios-selfsign-ipa.sh [--diagnostic] <output-ipa>}"
scheme="SFI"
client="${APPLE_CLIENT_DIR:-clients/apple}"

# The identity this experiment builds with. Both values are overridable so a build can be
# reproduced against whatever base the tester's signing profile is built around, but the
# defaults are the fork's own rather than upstream's.
experiment_base="${APPLE_EXPERIMENT_BASE_BUNDLE_ID:-io.jiejiebox.app}"
experiment_group="${APPLE_EXPERIMENT_APP_GROUP_ID:-group.$experiment_base}"

version="$(tr -d '[:space:]' <release/JIEJIE_VERSION)"
[ -n "$version" ] || {
  echo "FAIL: release/JIEJIE_VERSION is empty" >&2
  exit 1
}
# CFBundleVersion must be a monotonic integer; derive it from the marketing version so the two
# cannot drift (0.1.37 -> 137) instead of inventing a second number to keep in step.
build_number="$(printf '%s' "$version" | tr -cd '0-9' | sed -E 's/^0+//')"
[ -n "$build_number" ] || build_number="1"

if [ ! -d "$client/Libbox.xcframework" ]; then
  echo "FAIL: $client/Libbox.xcframework is missing; build Libbox from this repository first." >&2
  exit 1
fi

work="build/apple-ios-selfsign"
mkdir -p "$(dirname "$out")"
rm -rf "$work"
mkdir -p "$work"

cat <<EOF
experimental iOS self-sign build
  scheme:        $scheme
  version:       $version ($build_number)
  base id:       $experiment_base
  app group:     $experiment_group
  layout:        $([ "$diagnostic" = true ] && echo "DIAGNOSTIC - packet tunnel only" || echo "full")
  client:        $client
EOF

echo "resolving packages"
xcodebuild -resolvePackageDependencies \
  -project "$client/sing-box.xcodeproj" \
  -scheme "$scheme" \
  -derivedDataPath "$work/dd" >/dev/null 2>&1 || true

build_settings=(
  -project "$client/sing-box.xcodeproj"
  -scheme "$scheme"
  -configuration Release
  -destination 'generic/platform=iOS'
  -derivedDataPath "$work/dd"
  ARCHS=arm64
  ONLY_ACTIVE_ARCH=NO
  -skipPackagePluginValidation
  # One value drives every target's identifier, because the project defines all of them as
  # $(BASE_PACKAGE_IDENTIFIER)[.suffix]. That is the property a re-signing tool needs: the
  # family stays consistent no matter what the base is.
  BASE_PACKAGE_IDENTIFIER="$experiment_base"
  APP_GROUP_IDENTIFIER="$experiment_group"
  MARKETING_VERSION="$version"
  CURRENT_PROJECT_VERSION="$build_number"
  # Unsigned, exactly as the official builder does it. Entitlements are still processed - the
  # app must declare what it will be signed FOR - only the signature is absent.
  CODE_SIGNING_ALLOWED=NO
  CODE_SIGNING_REQUIRED=NO
  CODE_SIGN_IDENTITY=""
  CODE_SIGN_ENTITLEMENTS=""
  EXPANDED_CODE_SIGN_IDENTITY=""
  PROVISIONING_PROFILE_SPECIFIER=""
  DEVELOPMENT_TEAM=""
)

# The build's diagnostics go to a file and only the tail is printed on success. On failure the
# compiler errors are printed too: `| tail -40` alone kept the "BUILD FAILED" summary and threw
# away every `error:` line, which is exactly the output a failing build needs.
if ! xcodebuild build "${build_settings[@]}" > "$work/xcodebuild.log" 2>&1; then
  echo "xcodebuild failed; errors:" >&2
  grep -E "error:|BUILD FAILED" "$work/xcodebuild.log" | head -40 >&2 || true
  echo "last lines:" >&2
  tail -20 "$work/xcodebuild.log" >&2
  exit 1
fi
tail -20 "$work/xcodebuild.log"

product_name="$(grep -oE 'BuildableName = "[^"]*\.app"' "$client/sing-box.xcodeproj/xcshareddata/xcschemes/$scheme.xcscheme" | head -1 | sed -E 's/.*"([^"]*)".*/\1/')"
[ -n "$product_name" ] || {
  echo "FAIL: could not determine the product name for scheme $scheme" >&2
  exit 1
}

app="$(find "$work/dd/Build/Products" -maxdepth 2 -name "$product_name" -type d | head -1)"
[ -n "$app" ] || {
  echo "FAIL: $product_name was not produced under $work/dd/Build/Products" >&2
  exit 1
}
echo "built app: $app"

stage="$work/ipa"
rm -rf "$stage"
mkdir -p "$stage/Payload"
ditto "$app" "$stage/Payload/$product_name"

# --- the diagnostic layout ---------------------------------------------------------------
# Keeping only the Packet Tunnel extension is a build-configuration choice, not a post-hoc
# edit of a shipped product: the app does not load its extensions itself, the system does, so
# an app bundle with one extension is a complete app that offers one extension.
if [ "$diagnostic" = true ]; then
  echo "diagnostic layout: keeping only the packet-tunnel extension"
  kept=0
  while IFS= read -r plugin; do
    point="$(/usr/libexec/PlistBuddy -c 'Print :NSExtension:NSExtensionPointIdentifier' "$plugin/Info.plist" 2>/dev/null || true)"
    if [ "$point" = "com.apple.networkextension.packet-tunnel" ]; then
      kept=$((kept + 1))
      echo "  keeping $(basename "$plugin")"
    else
      echo "  dropping $(basename "$plugin")"
      rm -rf "$plugin"
    fi
  done < <(find "$stage/Payload/$product_name/PlugIns" -maxdepth 1 -name '*.appex' -type d | sort)
  [ "$kept" -eq 1 ] || {
    echo "FAIL: expected exactly one packet-tunnel extension, kept $kept" >&2
    exit 1
  }
fi

# --- signature residue --------------------------------------------------------------
# The build is unsigned; Xcode can still leave a _CodeSignature directory or a profile behind.
# Both would suggest a signature that does not exist, which is the confusion this artifact
# must not create.
while IFS= read -r residue; do
  echo "  removing $residue"
  rm -rf "$residue"
done < <(find "$stage/Payload" \( -name "_CodeSignature" -o -name "embedded.mobileprovision" \) -print)

rm -f "$out"
(cd "$stage" && zip -qry "$root/$out" Payload)

echo "ipa: $out ($(du -h "$out" | cut -f1))"
