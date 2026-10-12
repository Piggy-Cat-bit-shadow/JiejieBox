#!/usr/bin/env bash
# Regression tests for the macOS TestFlight app name.
#
# Usage: test-apple-macos-name.sh [--source-dir <dir>]
#
# # The failure this exists for
#
# The macOS TestFlight app installed as `sing-box`. The iOS branding overlay brands the
# SFI target only, and the macOS App Store product is a different target (SFM), so
# nothing renamed it. Every pre-upload gate looked at identifiers, entitlements and
# signing, so the wrong name reached Apple unchallenged.
#
# # Display and identity are separate
#
# The macOS overlay writes exactly ONE setting, INFOPLIST_KEY_CFBundleDisplayName, into the
# SFM target's Debug and Release configurations. It never writes PRODUCT_NAME and never
# rewrites the scheme: the built .app name, the SFM scheme's BuildableName, the bundle id
# and the sing-box:// URL scheme are identity and protocol surface that other software keys
# on, while the brand is what the menu bar, the Dock and About show. An earlier revision
# branded both settings and rewrote the scheme's buildable, which is the failure these
# tests exist to keep from coming back.
#
# # What is asserted
#
#   1. the macOS overlay brands the SFM display name in Debug and Release, leaves
#      PRODUCT_NAME = "sing-box" in both, and does not rewrite the SFM scheme - which still
#      names sing-box.app - or anything else
#   2. running it twice changes nothing (idempotent)
#   3. it FAILS CLOSED: a display name outside the accepted spellings, an SFM PRODUCT_NAME
#      renamed away from the identity, a configuration set it does not recognise, a scheme
#      that no longer names the app buildable, a missing project
#   4. the iOS overlay is unaffected: it still brands SFI and still leaves SFM alone, and
#      the macOS overlay does not touch SFI; neither overlay writes PRODUCT_NAME
#   5. prepare-apple-client.sh routes each platform to its own overlay
#   6. the pre-upload name gate accepts sing-box.app showing the brand and FAILS a bundle
#      named after the brand, which is the product renamed instead of its presentation
#   7. the pinned macOS source really is the structure the overlay was written for, and
#      the overlay really does apply to it
#
# # Fixtures
#
# Checks 1-4 run against a small synthetic Xcode project written by this script: three
# targets share PRODUCT_NAME = "sing-box" and near-identical configuration bodies, which
# is exactly the shape an implementation that located configurations by body text rather
# than by id would get wrong. Synthetic means no network and no Apple checkout, and makes
# the fail-closed cases expressible at all - they are corruptions of a known-good fixture.
#
# Check 7 then reads the REAL pinned project, because a fixture can only prove the overlay
# is self-consistent; it cannot prove it matches the source it will be applied to. That is
# the failure this task exists for - an overlay that was right about a target nobody built.
# Where no macOS checkout is present, check 7 is reported as SKIPPED, never as a pass.
#
# # When it must run
#
# Check 7 asserts the PINNED state - the identity anchors are present, the display name is a
# spelling the overlay accepts - and applies the overlay to a COPY rather than in place. It
# therefore has to run BEFORE prepare-apple-client.sh brands the real checkout: a run
# afterwards would find a checkout the build overlays had already rewritten, and the
# "unmodified" assertion would fail on a perfectly correct build. The workflow runs it at that
# point. --source-dir overrides which checkout is read, so the ordering can be demonstrated
# locally against a checkout whose phase this script does not otherwise know.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

source_dir=""
while [ $# -gt 0 ]; do
  case "$1" in
    --source-dir) source_dir="${2:?--source-dir needs a directory}"; shift 2 ;;
    *) echo "test-apple-macos-name: unknown argument: $1" >&2; exit 2 ;;
  esac
done

overlay="$root/scripts/ci/apply-apple-macos-branding-overlay.py"
ios_overlay="$root/scripts/ci/apply-apple-branding-overlay.py"
name_gate="$root/scripts/ci/check-macos-app-name.sh"

pass=0
fail=0
skip=0

check() {
  local name="$1"; shift
  if "$@" >/dev/null 2>&1; then
    echo "  PASS: $name"
    pass=$((pass + 1))
  else
    echo "  FAIL: $name" >&2
    fail=$((fail + 1))
  fi
}

expects_fail() {
  local name="$1"; shift
  if "$@" >/dev/null 2>&1; then
    echo "  FAIL: $name (expected failure, but it succeeded)" >&2
    fail=$((fail + 1))
  else
    echo "  PASS: $name"
    pass=$((pass + 1))
  fi
}

skipped() {
  echo "  SKIP: $1"
  skip=$((skip + 1))
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

if [ ! -f "$overlay" ] || [ ! -f "$name_gate" ]; then
  echo "FAIL: the macOS branding overlay or the name gate is missing" >&2
  exit 1
fi

# The two names under test are READ from the overlay rather than restated here: if the
# overlay's brand and this suite's notion of it could drift apart, every check below would
# pass while the app shipped under a name nothing had agreed on.
#
# The brand pattern is anchored at column 0, NOT `^\s*`: the overlay's docstring quotes the
# Apple repo's own `BRAND_DISPLAY_NAME = "Jiejiebox"` line as background, indented, and a
# leading-\s* match finds that quote first and makes these tests assert the wrong spelling.
# The real assignment is the only one at the start of a line.
brand="$(sed -nE 's/^BRAND_DISPLAY_NAME = "([^"]+)".*/\1/p' "$overlay" | head -1)"
if [ -z "$brand" ]; then
  echo "FAIL: the macOS overlay declares no BRAND_DISPLAY_NAME" >&2
  exit 1
fi

# The identity is the PRODUCT_NAME the overlay protects. PROTECTED is a dict literal, so it
# is parsed rather than pattern-matched: a renamed product has to be reported here loudly
# rather than slip past a regex that had quietly stopped matching.
identity="$(python3 - "$overlay" <<'PY'
import ast, re, sys
src = open(sys.argv[1], encoding="utf-8").read()
m = re.search(r'^PROTECTED = (\{.*?\n\})', src, re.M | re.S)
if not m:
    sys.exit("the macOS overlay declares no PROTECTED mapping")
protected = ast.literal_eval(m.group(1))
if "PRODUCT_NAME" not in protected:
    sys.exit("the macOS overlay protects no PRODUCT_NAME")
print(protected["PRODUCT_NAME"])
PY
)" || {
  echo "FAIL: the macOS overlay protects no PRODUCT_NAME identity" >&2
  exit 1
}
if [ -z "$identity" ]; then
  echo "FAIL: the macOS overlay protects an empty PRODUCT_NAME" >&2
  exit 1
fi

echo "macOS product under test: display name '$brand', identity '$identity'"

# ---------------------------------------------------------------------------
# The fixture
# ---------------------------------------------------------------------------
write_fixture() {
  local dir="$1"
  rm -rf "$dir"
  mkdir -p "$dir/sing-box.xcodeproj/xcshareddata/xcschemes"

  cat > "$dir/sing-box.xcodeproj/project.pbxproj" <<'PBX'
// !$*UTF8*$!
{
	archiveVersion = 1;
	objects = {

/* Begin PBXNativeTarget section */
		AAAAAAAAAAAAAAAAAAAAAAAA /* SFM */ = {
			isa = PBXNativeTarget;
			buildConfigurationList = AAAAAAAAAAAAAAAA000001 /* Build configuration list for PBXNativeTarget "SFM" */;
			name = SFM;
		};
		BBBBBBBBBBBBBBBBBBBBBBBB /* SFM.System */ = {
			isa = PBXNativeTarget;
			buildConfigurationList = BBBBBBBBBBBBBBBB000001 /* Build configuration list for PBXNativeTarget "SFM.System" */;
			name = "SFM.System";
		};
		CCCCCCCCCCCCCCCCCCCCCCCC /* SFI */ = {
			isa = PBXNativeTarget;
			buildConfigurationList = CCCCCCCCCCCCCCCC000001 /* Build configuration list for PBXNativeTarget "SFI" */;
			name = SFI;
		};
		DDDDDDDDDDDDDDDDDDDDDDDD /* SFT */ = {
			isa = PBXNativeTarget;
			buildConfigurationList = DDDDDDDDDDDDDDDD000001 /* Build configuration list for PBXNativeTarget "SFT" */;
			name = SFT;
		};
/* End PBXNativeTarget section */

/* Begin XCConfigurationList section */
		AAAAAAAAAAAAAAAA000001 /* Build configuration list for PBXNativeTarget "SFM" */ = {
			isa = XCConfigurationList;
			buildConfigurations = (
				3AEC21162A459B1A00A63465 /* Debug */,
				3AEC21172A459B1A00A63465 /* Release */,
			);
			defaultConfigurationIsVisible = 0;
			defaultConfigurationName = Release;
		};
		BBBBBBBBBBBBBBBB000001 /* Build configuration list for PBXNativeTarget "SFM.System" */ = {
			isa = XCConfigurationList;
			buildConfigurations = (
				3AEECC112A6DF9CA006A0E0C /* Debug */,
				3AEECC122A6DF9CA006A0E0C /* Release */,
			);
			defaultConfigurationIsVisible = 0;
			defaultConfigurationName = Release;
		};
		CCCCCCCCCCCCCCCC000001 /* Build configuration list for PBXNativeTarget "SFI" */ = {
			isa = XCConfigurationList;
			buildConfigurations = (
				3AEC20FF2A459AB500A63465 /* Debug */,
				3AEC21002A459AB500A63465 /* Release */,
			);
			defaultConfigurationIsVisible = 0;
			defaultConfigurationName = Release;
		};
		DDDDDDDDDDDDDDDD000001 /* Build configuration list for PBXNativeTarget "SFT" */ = {
			isa = XCConfigurationList;
			buildConfigurations = (
				3AC03BA12A72BF3500B7946F /* Debug */,
				3AC03BA22A72BF3500B7946F /* Release */,
			);
			defaultConfigurationIsVisible = 0;
			defaultConfigurationName = Release;
		};
/* End XCConfigurationList section */

/* Begin XCBuildConfiguration section */
		3AEC21162A459B1A00A63465 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				CODE_SIGN_ENTITLEMENTS = SFM/SFM.entitlements;
				INFOPLIST_KEY_CFBundleDisplayName = "sing-box";
				PRODUCT_BUNDLE_IDENTIFIER = "$(BASE_PACKAGE_IDENTIFIER)";
				PRODUCT_NAME = "sing-box";
			};
			name = Debug;
		};
		3AEC21172A459B1A00A63465 /* Release */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				CODE_SIGN_ENTITLEMENTS = SFM/SFM.entitlements;
				INFOPLIST_KEY_CFBundleDisplayName = "sing-box";
				PRODUCT_BUNDLE_IDENTIFIER = "$(BASE_PACKAGE_IDENTIFIER)";
				PRODUCT_NAME = "sing-box";
			};
			name = Release;
		};
		3AEECC112A6DF9CA006A0E0C /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				INFOPLIST_KEY_CFBundleDisplayName = "sing-box";
				PRODUCT_BUNDLE_IDENTIFIER = "$(BASE_PACKAGE_IDENTIFIER).standalone";
				PRODUCT_NAME = SFM;
			};
			name = Debug;
		};
		3AEECC122A6DF9CA006A0E0C /* Release */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				INFOPLIST_KEY_CFBundleDisplayName = "sing-box";
				PRODUCT_BUNDLE_IDENTIFIER = "$(BASE_PACKAGE_IDENTIFIER).standalone";
				PRODUCT_NAME = SFM;
			};
			name = Release;
		};
		3AEC20FF2A459AB500A63465 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				INFOPLIST_KEY_CFBundleDisplayName = "sing-box";
				PRODUCT_BUNDLE_IDENTIFIER = "$(BASE_PACKAGE_IDENTIFIER)";
				PRODUCT_NAME = "sing-box";
			};
			name = Debug;
		};
		3AEC21002A459AB500A63465 /* Release */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				INFOPLIST_KEY_CFBundleDisplayName = "sing-box";
				PRODUCT_BUNDLE_IDENTIFIER = "$(BASE_PACKAGE_IDENTIFIER)";
				PRODUCT_NAME = "sing-box";
			};
			name = Release;
		};
		3AC03BA12A72BF3500B7946F /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				INFOPLIST_KEY_CFBundleDisplayName = "sing-box";
				PRODUCT_BUNDLE_IDENTIFIER = "$(BASE_PACKAGE_IDENTIFIER)";
				PRODUCT_NAME = "sing-box";
			};
			name = Debug;
		};
		3AC03BA22A72BF3500B7946F /* Release */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				INFOPLIST_KEY_CFBundleDisplayName = "sing-box";
				PRODUCT_BUNDLE_IDENTIFIER = "$(BASE_PACKAGE_IDENTIFIER)";
				PRODUCT_NAME = "sing-box";
			};
			name = Release;
		};
/* End XCBuildConfiguration section */
	};
	rootObject = FFFFFFFFFFFFFFFFFFFFFFFF /* Project object */;
}
PBX

  cat > "$dir/sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme" <<'SCHEME'
<?xml version="1.0" encoding="UTF-8"?>
<Scheme LastUpgradeVersion = "2620" version = "1.7">
   <BuildAction parallelizeBuildables = "YES">
      <BuildActionEntries>
         <BuildActionEntry buildForTesting = "YES" buildForRunning = "YES">
            <BuildableReference
               BuildableIdentifier = "primary"
               BlueprintIdentifier = "3AEC21082A459B1900A63465"
               BuildableName = "sing-box.app"
               BlueprintName = "SFM"
               ReferencedContainer = "container:sing-box.xcodeproj">
            </BuildableReference>
         </BuildActionEntry>
      </BuildActionEntries>
   </BuildAction>
   <TestAction buildConfiguration = "Debug">
      <Testables>
         <TestableReference skipped = "NO">
            <BuildableReference
               BuildableIdentifier = "primary"
               BlueprintIdentifier = "3A6313A62F0CEFDD0060A550"
               BuildableName = "SFMUITests.xctest"
               BlueprintName = "SFMUITests"
               ReferencedContainer = "container:sing-box.xcodeproj">
            </BuildableReference>
         </TestableReference>
      </Testables>
   </TestAction>
   <LaunchAction buildConfiguration = "Debug">
      <BuildableProductRunnable runnableDebuggingMode = "0">
         <BuildableReference
            BuildableIdentifier = "primary"
            BlueprintIdentifier = "3AEC21082A459B1900A63465"
            BuildableName = "sing-box.app"
            BlueprintName = "SFM"
            ReferencedContainer = "container:sing-box.xcodeproj">
         </BuildableReference>
      </BuildableProductRunnable>
   </LaunchAction>
   <ProfileAction buildConfiguration = "Release">
      <BuildableProductRunnable runnableDebuggingMode = "0">
         <BuildableReference
            BuildableIdentifier = "primary"
            BlueprintIdentifier = "3AEC21082A459B1900A63465"
            BuildableName = "sing-box.app"
            BlueprintName = "SFM"
            ReferencedContainer = "container:sing-box.xcodeproj">
         </BuildableReference>
      </BuildableProductRunnable>
   </ProfileAction>
</Scheme>
SCHEME
}

# Reads one setting out of one target configuration by id, so an assertion is about the
# configuration the overlay was told to edit rather than whichever similar line appears
# first in the file.
setting() {
  python3 - "$1" "$2" "$3" "$4" <<'PY'
import re, sys
path, target, config, key = sys.argv[1:5]
src = open(path, encoding="utf-8").read()
m = re.search(r'Build configuration list for PBXNativeTarget "' + re.escape(target) +
              r'" \*/ = \{(.*?)\n\t\t\};', src, re.S)
if not m:
    sys.exit(1)
arr = re.search(r'buildConfigurations = \((.*?)\);', m.group(1), re.S)
for cid, name in re.findall(r'([0-9A-Fa-f]{24}) /\* (\w+) \*/', arr.group(1)):
    if name != config:
        continue
    b = re.search(rf'{cid} /\* {name} \*/ = \{{\s*isa = XCBuildConfiguration;(.*?)\n\t\t\}};',
                  src, re.S)
    if not b:
        sys.exit(1)
    v = re.search(rf'^\s*{re.escape(key)} = "?([^";]*)"?;', b.group(1), re.M)
    print(v.group(1) if v else "")
    sys.exit(0)
sys.exit(1)
PY
}

echo "== the macOS overlay brands SFM Debug and Release =="
# Both settings live in the same configuration block, so both are asserted: an overlay that
# branded the display name by renaming the product would satisfy a display-name-only check
# while shipping exactly the regression this contract exists to forbid.
fixture="$work/apply"
write_fixture "$fixture"
scheme_sha_before="$(shasum "$fixture/sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme" | awk '{print $1}')"

check "the overlay applies cleanly to the upstream fixture" python3 "$overlay" "$fixture"

for config in Debug Release; do
  check "SFM $config CFBundleDisplayName is $brand" \
    test "$(setting "$fixture/sing-box.xcodeproj/project.pbxproj" SFM "$config" INFOPLIST_KEY_CFBundleDisplayName)" = "$brand"
  check "SFM $config PRODUCT_NAME is still $identity" \
    test "$(setting "$fixture/sing-box.xcodeproj/project.pbxproj" SFM "$config" PRODUCT_NAME)" = "$identity"
done

# The scheme follows PRODUCT_NAME, so it must be byte-identical after the overlay runs: the
# overlay validates the buildable it expects and never rewrites it.
check "all three SFM scheme application buildables are still $identity.app" \
  bash -c "test \"\$(grep -c 'BuildableName = \"$identity.app\"' '$fixture/sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme')\" = 3"
check "the scheme was not rewritten to a branded $brand.app" \
  bash -c "! grep -q 'BuildableName = \"$brand.app\"' '$fixture/sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme'"
check "the overlay did not rewrite the scheme at all" \
  test "$scheme_sha_before" = "$(shasum "$fixture/sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme" | awk '{print $1}')"

# The pinned client commits "Jiejiebox" (lower-case b) for SFM, which is an accepted starting
# state that must be normalised rather than refused. It is exercised here because the main
# fixture starts upstream and check 7 - the only other place that real spelling appears - is
# SKIPPED when no macOS checkout is present. The spelling is asserted to be neither the brand
# nor upstream, so a mistyped fixture cannot quietly degrade into the branded case.
committed="$work/committed-spelling"
write_fixture "$committed"
python3 - "$committed/sing-box.xcodeproj/project.pbxproj" <<'PY'
import sys
p = sys.argv[1]
src = open(p, encoding="utf-8").read()
# Located by configuration id, so only the two SFM blocks change and the shape stays that
# of the pinned project: SFI already reads a brand spelling there, SFM.System stays upstream.
for header in ("3AEC21162A459B1A00A63465 /* Debug */ = {",
               "3AEC21172A459B1A00A63465 /* Release */ = {"):
    i = src.index(header)
    j = src.index("name = ", i)
    src = src[:i] + src[i:j].replace('INFOPLIST_KEY_CFBundleDisplayName = "sing-box";',
                                     'INFOPLIST_KEY_CFBundleDisplayName = "Jiejiebox";') + src[j:]
open(p, "w", encoding="utf-8").write(src)
PY
committed_value="$(setting "$committed/sing-box.xcodeproj/project.pbxproj" SFM Debug INFOPLIST_KEY_CFBundleDisplayName)"
check "the fixture starts from a real source spelling, not the brand or upstream" \
  bash -c "test -n '$committed_value' && test '$committed_value' != '$brand' && test '$committed_value' != '$identity'"
check "the overlay accepts and normalises the committed spelling" python3 "$overlay" "$committed"
check "SFM Debug is branded from the committed spelling" \
  test "$(setting "$committed/sing-box.xcodeproj/project.pbxproj" SFM Debug INFOPLIST_KEY_CFBundleDisplayName)" = "$brand"
check "SFM Release is branded from the committed spelling too" \
  test "$(setting "$committed/sing-box.xcodeproj/project.pbxproj" SFM Release INFOPLIST_KEY_CFBundleDisplayName)" = "$brand"

echo "== nothing else moved =="
# The fault lines: three targets share the product name and two share the display name,
# so a replacement not addressed by configuration id shows up here.
for target in SFI SFT; do
  for config in Debug Release; do
    check "$target $config PRODUCT_NAME is still sing-box" \
      test "$(setting "$fixture/sing-box.xcodeproj/project.pbxproj" "$target" "$config" PRODUCT_NAME)" = "sing-box"
    check "$target $config CFBundleDisplayName is still sing-box" \
      test "$(setting "$fixture/sing-box.xcodeproj/project.pbxproj" "$target" "$config" INFOPLIST_KEY_CFBundleDisplayName)" = "sing-box"
  done
done
for config in Debug Release; do
  check "SFM.System $config keeps PRODUCT_NAME = SFM" \
    test "$(setting "$fixture/sing-box.xcodeproj/project.pbxproj" SFM.System "$config" PRODUCT_NAME)" = "SFM"
  check "SFM.System $config keeps CFBundleDisplayName = sing-box" \
    test "$(setting "$fixture/sing-box.xcodeproj/project.pbxproj" SFM.System "$config" INFOPLIST_KEY_CFBundleDisplayName)" = "sing-box"
done
check "the SFM.System bundle id is unchanged" \
  grep -q 'PRODUCT_BUNDLE_IDENTIFIER = "\$(BASE_PACKAGE_IDENTIFIER).standalone";' \
    "$fixture/sing-box.xcodeproj/project.pbxproj"
check "the app bundle id is unchanged" \
  grep -q 'PRODUCT_BUNDLE_IDENTIFIER = "\$(BASE_PACKAGE_IDENTIFIER)";' \
    "$fixture/sing-box.xcodeproj/project.pbxproj"
check "the extension buildable is untouched" \
  grep -q 'BuildableName = "SFMUITests.xctest"' \
    "$fixture/sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme"
check "the SFM target is still called SFM" \
  grep -q 'Build configuration list for PBXNativeTarget "SFM"' \
    "$fixture/sing-box.xcodeproj/project.pbxproj"

echo "== a second apply changes nothing =="
before="$(cat "$fixture/sing-box.xcodeproj/project.pbxproj" "$fixture/sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme" | shasum | awk '{print $1}')"
check "the overlay is idempotent" python3 "$overlay" "$fixture"
after="$(cat "$fixture/sing-box.xcodeproj/project.pbxproj" "$fixture/sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme" | shasum | awk '{print $1}')"
check "re-applying produced byte-identical files" test "$before" = "$after"

echo "== the macOS overlay fails closed =="
# Each corruption below is one that a naive implementation would accept: a display name
# close to the brand but not one of the accepted spellings, a product renamed instead of its
# presentation, a configuration set the overlay was never reviewed for, and a scheme that no
# longer names the app buildable.
#
# There is no "half branded" state in the old sense any more: the overlay writes ONE setting,
# so a configuration still reading the upstream name is simply upstream and gets normalised.
# What must be refused is a display name outside ACCEPTED_SOURCE_NAMES, because that means
# the pinned client moved in a way nobody reviewed.
# The near-miss most likely to ship: the brand with its second "e" dropped ("JijieBox").
# It is not the upstream name, not the client's committed "Jiejiebox", and not the brand, so
# it is outside ACCEPTED_SOURCE_NAMES and the overlay must refuse it rather than normalise it.
# (Reverting the capital B would NOT work as a near-miss here: that produces "Jiejiebox",
# which is exactly the spelling the pinned client commits, and is therefore accepted.)
wrong_display="$(printf '%s' "$brand" | sed 's/^Jiejie/Jijie/')"
typo="$work/unaccepted-display"
write_fixture "$typo"
python3 - "$typo/sing-box.xcodeproj/project.pbxproj" "$wrong_display" <<'PY'
import sys
p, wrong = sys.argv[1], sys.argv[2]
src = open(p, encoding="utf-8").read()
old = '3AEC21162A459B1A00A63465 /* Debug */ = {'
i = src.index(old)
j = src.index("name = Debug;", i)
src = src[:i] + src[i:j].replace('INFOPLIST_KEY_CFBundleDisplayName = "sing-box";',
                                 f'INFOPLIST_KEY_CFBundleDisplayName = "{wrong}";') + src[j:]
open(p, "w", encoding="utf-8").write(src)
PY
check "the near-miss display name really differs from the brand" \
  test "$wrong_display" != "$brand"
check "the fixture really carries a display name outside the accepted spellings" \
  test "$(setting "$typo/sing-box.xcodeproj/project.pbxproj" SFM Debug INFOPLIST_KEY_CFBundleDisplayName)" = "$wrong_display"
check "that fixture's other configuration is still upstream" \
  test "$(setting "$typo/sing-box.xcodeproj/project.pbxproj" SFM Release INFOPLIST_KEY_CFBundleDisplayName)" = "sing-box"
expects_fail "a display name outside the accepted spellings is refused" python3 "$overlay" "$typo"

# The rename this contract forbids, in the state a previous revision of the overlay produced:
# the PRODUCT_NAME itself moved to the brand. It must be refused rather than normalised
# around, because the scheme, the bundle directory and the sing-box:// URL scheme all follow
# PRODUCT_NAME, and the overlay is not allowed to decide that trade.
renamed="$work/renamed-product"
write_fixture "$renamed"
python3 - "$renamed/sing-box.xcodeproj/project.pbxproj" "$brand" <<'PY'
import sys
p, brand = sys.argv[1], sys.argv[2]
src = open(p, encoding="utf-8").read()
old = '3AEC21162A459B1A00A63465 /* Debug */ = {'
i = src.index(old)
j = src.index("name = Debug;", i)
src = src[:i] + src[i:j].replace('PRODUCT_NAME = "sing-box";', f'PRODUCT_NAME = "{brand}";') + src[j:]
open(p, "w", encoding="utf-8").write(src)
PY
check "the fixture really has an SFM product renamed to the brand" \
  test "$(setting "$renamed/sing-box.xcodeproj/project.pbxproj" SFM Debug PRODUCT_NAME)" = "$brand"
check "that fixture's display name is untouched, so only the identity moved" \
  test "$(setting "$renamed/sing-box.xcodeproj/project.pbxproj" SFM Debug INFOPLIST_KEY_CFBundleDisplayName)" = "sing-box"
expects_fail "an SFM PRODUCT_NAME renamed away from $identity is refused" python3 "$overlay" "$renamed"

unexpected="$work/unexpected"
write_fixture "$unexpected"
python3 - "$unexpected/sing-box.xcodeproj/project.pbxproj" <<'PY'
import sys
p = sys.argv[1]
src = open(p, encoding="utf-8").read()
# A third configuration on SFM: new scope the overlay was never reviewed for.
src = src.replace("""				3AEC21172A459B1A00A63465 /* Release */,
			);
			defaultConfigurationIsVisible = 0;
			defaultConfigurationName = Release;
		};
		BBBBBBBBBBBBBBBB000001""", """				3AEC21172A459B1A00A63465 /* Release */,
				3AEC21182A459B1A00A63465 /* Profile */,
			);
			defaultConfigurationIsVisible = 0;
			defaultConfigurationName = Release;
		};
		BBBBBBBBBBBBBBBB000001""", 1)
src = src.replace("""		3AEECC112A6DF9CA006A0E0C /* Debug */ = {""", """		3AEC21182A459B1A00A63465 /* Profile */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				INFOPLIST_KEY_CFBundleDisplayName = "sing-box";
				PRODUCT_BUNDLE_IDENTIFIER = "$(BASE_PACKAGE_IDENTIFIER)";
				PRODUCT_NAME = "sing-box";
			};
			name = Profile;
		};
		3AEECC112A6DF9CA006A0E0C /* Debug */ = {""", 1)
open(p, "w", encoding="utf-8").write(src)
PY
check "the fixture really has an unexpected configuration" \
  grep -q '3AEC21182A459B1A00A63465 /\* Profile \*/' "$unexpected/sing-box.xcodeproj/project.pbxproj"
expects_fail "an unrecognised configuration set is refused" python3 "$overlay" "$unexpected"

# A scheme already rewritten to the brand's bundle name: what the previous revision of the
# overlay produced, and what re-running over such a checkout would find. The overlay
# validates the buildable and never rewrites the scheme, so it must refuse rather than brand
# a project whose product identity has already moved.
renamed_scheme="$work/renamed-scheme"
write_fixture "$renamed_scheme"
python3 - "$renamed_scheme/sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme" "$brand" <<'PY'
import sys
p, brand = sys.argv[1], sys.argv[2]
src = open(p, encoding="utf-8").read()
# All three application buildables, so the scheme no longer names sing-box.app at all.
src = src.replace('BuildableName = "sing-box.app"', f'BuildableName = "{brand}.app"')
open(p, "w", encoding="utf-8").write(src)
PY
check "the fixture really has a scheme renamed to the branded bundle" \
  bash -c "test \"\$(grep -c 'BuildableName = \"$brand.app\"' '$renamed_scheme/sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme')\" = 3"
expects_fail "a scheme already renamed to $brand.app is refused" python3 "$overlay" "$renamed_scheme"

# A buildable this overlay has never heard of. The scheme follows PRODUCT_NAME, so a name
# that is neither the identity nor the brand means the pinned client moved.
unknown="$work/unknown"
write_fixture "$unknown"
python3 - "$unknown/sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme" <<'PY'
import sys
p = sys.argv[1]
src = open(p, encoding="utf-8").read()
src = src.replace('BuildableName = "sing-box.app"', 'BuildableName = "SomeOtherName.app"')
open(p, "w", encoding="utf-8").write(src)
PY
check "the fixture really has no sing-box.app buildable left" \
  bash -c "! grep -q 'BuildableName = \"sing-box.app\"' '$unknown/sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme'"
expects_fail "an unknown application buildable is refused" python3 "$overlay" "$unknown"

missing="$work/missing"
mkdir -p "$missing"
expects_fail "a project directory with no pbxproj is refused" python3 "$overlay" "$missing"
expects_fail "no client directory at all is refused" python3 "$overlay" "$work/does-not-exist"

echo "== the two overlays keep their own scopes =="
# iOS brands SFI from its own overlay and macOS brands SFM from this one. The scopes are
# kept apart structurally rather than by running both against one fixture: each overlay
# addresses a different target and a different scheme FILE, which is what makes it
# possible to change one platform's branding without touching the other platform's build.
#
# The iOS overlay's own end-to-end behaviour is covered where it can be covered for real,
# in test-apple-signing.sh against the pinned iOS client. Duplicating a stand-in iOS
# project here would assert a fixture rather than the client.
check "the iOS overlay still targets the SFI target" \
  grep -q '^TARGET = "SFI"$' "$ios_overlay"
check "the macOS overlay targets the SFM target" \
  grep -q '^TARGET = "SFM"$' "$overlay"
check "the two overlays address different schemes" \
  bash -c "! diff <(grep '^SCHEME = ' '$ios_overlay') <(grep '^SCHEME = ' '$overlay') >/dev/null"
check "the macOS overlay left SFI untouched" \
  test "$(setting "$fixture/sing-box.xcodeproj/project.pbxproj" SFI Debug PRODUCT_NAME)" = "sing-box"
check "the macOS overlay left SFM.System's product name untouched" \
  test "$(grep -c 'PRODUCT_NAME = SFM;' "$fixture/sing-box.xcodeproj/project.pbxproj")" = "2"
# The two overlays must address different things, and what separates them is the SCOPE: a
# different target and a different scheme FILE. A previous revision expressed the separation
# as a spelling difference - the macOS product branded "Jiejiebox", the iOS one "JiejieBox" -
# and this fix deliberately unified the brand. Describing the macOS overlay as "the one that
# brands differently" is how it came to own settings it should never have touched, so the
# separation is asserted on scope, and the brand is asserted to agree.
check "both overlays write the same brand" \
  bash -c "test \"\$(grep '^BRAND_DISPLAY_NAME = ' '$ios_overlay')\" = \"\$(grep '^BRAND_DISPLAY_NAME = ' '$overlay')\""
check "the iOS overlay writes the brand under test too" \
  grep -qx "BRAND_DISPLAY_NAME = \"$brand\"" "$ios_overlay"
check "the two overlays address different TARGETs" \
  bash -c "! diff <(grep '^TARGET = ' '$ios_overlay') <(grep '^TARGET = ' '$overlay') >/dev/null"
# Neither may write PRODUCT_NAME. The only setting either overlay owns is its DISPLAY_KEY,
# and a literal `PRODUCT_NAME = ` in the source is the shape a reintroduced rename has to
# take, whether as a rewrite pattern or as a replacement string.
check "each overlay owns exactly the display setting" \
  bash -c "grep -qx 'DISPLAY_KEY = \"INFOPLIST_KEY_CFBundleDisplayName\"' '$ios_overlay' && grep -qx 'DISPLAY_KEY = \"INFOPLIST_KEY_CFBundleDisplayName\"' '$overlay'"
check "neither overlay rewrites PRODUCT_NAME" \
  bash -c "! grep -q 'PRODUCT_NAME = ' '$ios_overlay' '$overlay'"

echo "== prepare routes each platform to its own branding =="
prepare="$root/scripts/ci/prepare-apple-client.sh"
check "prepare selects the macOS overlay for APPLE_CLIENT_PLATFORM=macos" \
  grep -q 'apply-apple-macos-branding-overlay.py' "$prepare"
check "prepare still selects the iOS overlay otherwise" \
  grep -q 'apply-apple-branding-overlay.py' "$prepare"
check "prepare guards the macOS overlay on the platform" \
  grep -q 'if \[ "\$APPLE_CLIENT_PLATFORM" = "macos" \]; then' "$prepare"

echo "== the pre-upload name gate =="
# A gate that has never rejected a sing-box bundle is not known to catch one, so it is
# exercised in both directions against real bundle layouts.
make_bundle() {
  local path="$1" display="$2" cfbundlename="${3:-}"
  rm -rf "$path"
  mkdir -p "$path/Contents"
  {
    echo '<?xml version="1.0" encoding="UTF-8"?>'
    echo '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">'
    echo '<plist version="1.0"><dict>'
    echo '  <key>CFBundleIdentifier</key><string>com.example.jiejiebox</string>'
    echo "  <key>CFBundleDisplayName</key><string>$display</string>"
    if [ -n "$cfbundlename" ]; then
      echo "  <key>CFBundleName</key><string>$cfbundlename</string>"
    fi
    echo '</dict></plist>'
  } > "$path/Contents/Info.plist"
}

bundles="$work/bundles"
# The correct pairing under this contract: the bundle directory and CFBundleName are the
# product identity, and only the display name is the brand.
good="$bundles/$identity.app"
make_bundle "$good" "$brand" "$identity"
check "the gate accepts $identity.app with CFBundleDisplayName=$brand" "$name_gate" "$good" "$brand"
check "the gate resolves the expected names from the overlay when not told" "$name_gate" "$good"

# The bug the branding overlay exists to fix: the app is still called sing-box.
bad="$bundles/unbranded/$identity.app"
make_bundle "$bad" sing-box "$identity"
expects_fail "the gate rejects an unbranded $identity.app, the bug this fix is for" "$name_gate" "$bad"

# Misspellings the contract forbids. A close-but-wrong name installs under a name nobody
# asked for, so each is asserted to fail rather than trusted to be unlikely. The bundle
# directory keeps the correct identity, so a rejection here is attributable to the DISPLAY
# name rather than to the identity check firing on the same fixture.
for wrong in Jijiebox JijieBox Jijiebox2 jiejiebox; do
  make_bundle "$bundles/wrong/$identity.app" "$wrong" "$identity"
  expects_fail "the gate rejects '$wrong' as the display name" "$name_gate" "$bundles/wrong/$identity.app"
done

# The regression the new gate exists for: something renamed the PRODUCT instead of its
# presentation. A bundle named after the brand is a FAILURE even when it shows the right
# display name, and this is the assertion that catches the rename the old gate required.
make_bundle "$bundles/product-renamed/$brand.app" "$brand" "$brand"
expects_fail "the gate rejects a bundle named $brand.app, the rename this contract forbids" \
  "$name_gate" "$bundles/product-renamed/$brand.app"

# CFBundleName is identity, so it must be $identity even when the display name is right. It
# is the fallback when no display name is present, which is what makes it identity surface
# rather than presentation.
make_bundle "$bundles/disagree/$identity.app" "$brand" "$brand"
expects_fail "the gate rejects a bundle whose CFBundleName is the brand rather than $identity" \
  "$name_gate" "$bundles/disagree/$identity.app"

# And the same bundle with no CFBundleName key at all must PASS: the project generates its
# Info.plist, a missing key is legitimate, and the gate only judges the key when it is there.
# The old gate treated this exact bundle as a failure, which is the expectation this replaces.
make_bundle "$bundles/no-name-key/$identity.app" "$brand"
check "the gate accepts $identity.app showing the brand with no CFBundleName key" \
  "$name_gate" "$bundles/no-name-key/$identity.app"

# A bundle with no display name at all is not a branded bundle.
make_bundle "$bundles/nodisplay/$identity.app" "$brand" "$identity"
python3 - "$bundles/nodisplay/$identity.app/Contents/Info.plist" <<'PY'
import re, sys
p = sys.argv[1]
src = open(p, encoding="utf-8").read()
src = re.sub(r"\s*<key>CFBundleDisplayName</key><string>[^<]*</string>", "", src)
open(p, "w", encoding="utf-8").write(src)
PY
expects_fail "the gate rejects a bundle with no CFBundleDisplayName" \
  "$name_gate" "$bundles/nodisplay/$identity.app"

expects_fail "the gate refuses a path that is not a bundle" "$name_gate" "$bundles"
check "the TestFlight builder runs the gate before uploading" \
  grep -q 'check-macos-app-name.sh' "$root/scripts/ci/build-macos-testflight.sh"
check "the TestFlight builder no longer repeats the name itself" \
  bash -c "! grep -qE '^built_name=' '$root/scripts/ci/build-macos-testflight.sh'"

echo "== the pinned macOS source matches what the overlay expects =="
# The fixture proves the overlay is self-consistent. This proves it matches the source it
# is applied to, and that applying it there produces the branded name rather than failing
# on a structure it no longer recognises.
if [ -n "$source_dir" ]; then
  # An explicit checkout overrides the resolver, so the phase-dependent half can be run
  # against a specific tree - a pristine one, or a deliberately branded one to show that
  # this half is not phase-agnostic.
  macos_dir="$source_dir"
else
  macos_dir="$(./scripts/ci/apple-client-source.sh dir macos 2>/dev/null || true)"
fi
# The expected revision comes from the PARENT repository's gitlink either way, never from
# the checkout under test, so a --source-dir checkout cannot nominate itself as its own
# authority. `sha macos` reads `git ls-tree HEAD clients/apple`, which no working tree can
# move. An earlier revision read MACOS_APPLE_SHA out of release/apple-client-refs.env; that
# file no longer carries a per-platform SHA, because the gitlink is the single authority.
macos_sha="$(./scripts/ci/apple-client-source.sh sha macos 2>/dev/null || true)"
if [ -z "$macos_sha" ]; then
  echo "FAIL: the parent repository records no macOS Apple gitlink to compare against" >&2
  exit 1
fi
pinned_pbx="$macos_dir/sing-box.xcodeproj/project.pbxproj"
pinned_scheme="$macos_dir/sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme"

if [ -f "$pinned_pbx" ] && [ -f "$pinned_scheme" ]; then
  check "the macOS checkout is at the pinned revision" \
    test "$(git -C "$macos_dir" rev-parse HEAD)" = "$macos_sha"

  check "the pinned SFM target has exactly the Debug and Release configurations" python3 - "$pinned_pbx" <<'PY'
import re, sys
src = open(sys.argv[1], encoding="utf-8").read()
problems = []
for target in ("SFM", "SFM.System"):
    m = re.search(r'Build configuration list for PBXNativeTarget "' + re.escape(target) +
                  r'" \*/ = \{(.*?)\n\t\t\};', src, re.S)
    if not m:
        problems.append(f"no configuration list for {target}")
        continue
    arr = re.search(r"buildConfigurations = \((.*?)\);", m.group(1), re.S)
    names = [n for _, n in re.findall(r"([0-9A-Fa-f]{24}) /\* (\w+) \*/", arr.group(1))]
    if sorted(names) != ["Debug", "Release"]:
        problems.append(f"{target} configurations are {names}, expected Debug and Release")
if problems:
    print("\n".join("    - " + p for p in problems), file=sys.stderr)
    sys.exit(1)
PY

  check "the pinned SFM configurations are in a state the overlay accepts" python3 - "$pinned_pbx" "$overlay" <<'PY'
import importlib.util, re, sys
# Importing the overlay must not leave a bytecode cache behind in the repository: this suite
# is run against a tree whose cleanliness is asserted, and an untracked __pycache__ next to
# the overlays would be litter no other check knows about.
sys.dont_write_bytecode = True
src = open(sys.argv[1], encoding="utf-8").read()

# The overlay is the authority on what the pinned source has to look like, so its constants
# are read rather than restated here: a name the overlay no longer accepts must not be able
# to pass. The module is imported rather than pattern-matched because its accepted list
# refers to BRAND_DISPLAY_NAME by name, which a regex over the literal would not resolve.
spec = importlib.util.spec_from_file_location("macos_branding_overlay", sys.argv[2])
overlay = importlib.util.module_from_spec(spec)
spec.loader.exec_module(overlay)

m = re.search(r'Build configuration list for PBXNativeTarget "SFM" \*/ = \{(.*?)\n\t\t\};', src, re.S)
ids = re.findall(r"([0-9A-Fa-f]{24}) /\*",
                 re.search(r"buildConfigurations = \((.*?)\);", m.group(1), re.S).group(1))
problems = []
for cid in ids:
    b = re.search(rf'{cid} /\* (\w+) \*/ = \{{\s*isa = XCBuildConfiguration;(.*?)\n\t\t\}};',
                  src, re.S)
    if not b:
        problems.append(f"{cid} has no XCBuildConfiguration block")
        continue
    name, body = b.group(1), b.group(2)

    def setting(key):
        v = re.search(rf'^\s*{re.escape(key)} = "([^"]*)";', body, re.M)
        return v.group(1) if v else None

    # Identity: exact equality, because the overlay refuses to proceed on anything else.
    for key, wanted in overlay.PROTECTED.items():
        got = setting(key)
        if got is None:
            problems.append(f"SFM {name} declares no {key}")
        elif got != wanted:
            problems.append(f"SFM {name} {key} is {got!r}, expected {wanted!r}")
    # Presentation: one of the accepted starting spellings, which the overlay normalises.
    got = setting(overlay.DISPLAY_KEY)
    if got is None:
        problems.append(f"SFM {name} declares no {overlay.DISPLAY_KEY}")
    elif got not in overlay.ACCEPTED_SOURCE_NAMES:
        problems.append(
            f"SFM {name} {overlay.DISPLAY_KEY} is {got!r}, which the overlay does not accept "
            f"(accepted: {', '.join(map(repr, overlay.ACCEPTED_SOURCE_NAMES))})")
if problems:
    print("\n".join("    - " + p for p in problems), file=sys.stderr)
    sys.exit(1)
PY

  check "the pinned SFM scheme has exactly three SFM application buildables" \
    test "$(grep -c 'BlueprintIdentifier = "3AEC21082A459B1900A63465"' "$pinned_scheme")" = "3"
  check "the pinned SFM scheme names all of them $identity.app" \
    bash -c "test \"\$(grep -c 'BuildableName = \"$identity.app\"' '$pinned_scheme')\" = 3"
  check "the pinned SFM scheme names no branded $brand.app" \
    bash -c "! grep -q 'BuildableName = \"$brand.app\"' '$pinned_scheme'"
  check "the pinned scheme keeps the UI test buildable" \
    grep -q 'BuildableName = "SFMUITests.xctest"' "$pinned_scheme"

  # And the overlay really applies to that source, on a copy - the pinned checkout stays
  # exactly as it was, which is asserted again afterwards.
  #
  # The pinned SFI values are read BEFORE the apply, because the client commits a brand
  # spelling for SFI itself: "the macOS overlay did not brand SFI" cannot be asserted as a
  # spelling there - the value is already a brand spelling - only as "unchanged by this run".
  sfi_display_before="$(setting "$pinned_pbx" SFI Release INFOPLIST_KEY_CFBundleDisplayName || true)"
  sfi_identity_before="$(setting "$pinned_pbx" SFI Release PRODUCT_NAME || true)"
  real_copy="$work/real-copy"
  cp -R "$macos_dir" "$real_copy"
  check "the overlay applies to a copy of the pinned macOS source" python3 "$overlay" "$real_copy"
  check "the pinned SFM Release display name is branded" \
    test "$(setting "$real_copy/sing-box.xcodeproj/project.pbxproj" SFM Release INFOPLIST_KEY_CFBundleDisplayName)" = "$brand"
  check "the pinned SFM Release PRODUCT_NAME is still $identity" \
    test "$(setting "$real_copy/sing-box.xcodeproj/project.pbxproj" SFM Release PRODUCT_NAME)" = "$identity"
  check "the pinned SFM scheme still names $identity.app after the apply" \
    bash -c "test \"\$(grep -c 'BuildableName = \"$identity.app\"' '$real_copy/sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme')\" = 3"
  check "the pinned SFM.System display name is not branded" \
    test "$(setting "$real_copy/sing-box.xcodeproj/project.pbxproj" SFM.System Release INFOPLIST_KEY_CFBundleDisplayName)" = "sing-box"
  check "the pinned SFM.System target keeps PRODUCT_NAME = SFM" \
    test "$(setting "$real_copy/sing-box.xcodeproj/project.pbxproj" SFM.System Release PRODUCT_NAME)" = "SFM"
  check "the macOS overlay left the pinned SFI display name alone" \
    test "$(setting "$real_copy/sing-box.xcodeproj/project.pbxproj" SFI Release INFOPLIST_KEY_CFBundleDisplayName)" = "$sfi_display_before"
  check "the macOS overlay left the pinned SFI identity alone" \
    test "$(setting "$real_copy/sing-box.xcodeproj/project.pbxproj" SFI Release PRODUCT_NAME)" = "$sfi_identity_before"
  check "the overlay is idempotent on the pinned source" python3 "$overlay" "$real_copy"
  check "the pinned checkout itself was not modified" \
    test -z "$(git -C "$macos_dir" status --porcelain)"
  check "applying the overlay moved no repository revision" \
    test "$(git -C "$macos_dir" rev-parse HEAD)" = "$macos_sha"
else
  skipped "no macOS Apple checkout at $macos_dir; run ./scripts/ci/apple-client-source.sh checkout macos"
fi

echo
echo "test-apple-macos-name: $pass passed, $fail failed, $skip skipped"
[ "$fail" -eq 0 ] || exit 1
