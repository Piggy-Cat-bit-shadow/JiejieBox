#!/usr/bin/env bash
#
# ABI compatibility gate for the libbox StringBox migration.
#
# # The failure this exists to prevent
#
# libbox's exported methods used to return a bare `string`. gomobile writes such a result into
# the packed `_cgo_argtype` frame, which puts a Go pointer at alignment 1, and the Go runtime
# then throws `fatal error: bulkBarrierPreWrite: unaligned arguments` - a THROW, so the
# process dies and nothing can recover it. The fix is to return a bound `*StringBox` and read
# it through its `Value` accessor.
#
# That fix spans three layers, versioned in three different repositories:
#
#   1. the Go declaration in experimental/libbox     (this repository)
#   2. the generated Java/ObjC binding               (produced here, consumed there)
#   3. the Kotlin and Swift call sites               (clients/android, clients/apple)
#
# Layer 3 is pinned by a gitlink, so "the AAR built" and "the client compiled" are both
# compatible with a client checked out at a revision that still calls the OLD ABI. That is the
# concrete historical failure this gate exists for: the Kotlin `.value` edits sat uncommitted
# in clients/android, GitHub built the AAR green, and a clean checkout of the client called
# `session.name()` expecting a String.
#
# # What it checks
#
#   1. GO SHAPE      - every method named in docs/fork/libbox-abi-contract.tsv is still
#                      attributed by the surface scanner (experimental/libbox/
#                      gomobile_surface_test.go) and still has a bound-object result rather
#                      than a pointer-bearing string/[]byte one. Catches a revert.
#   2. GO DECL       - the declaration's result really is *StringBox.
#   3. BINDING SHAPE - the generated Java binding declares the method returning StringBox and
#                      does not declare a bare java.lang.String result for it. Uses gobind
#                      when available; says so and falls back to layers 1+2 otherwise.
#   4. CALL SITES    - every call site in the client source whose receiver matches the
#                      contract reads the result through a migrated accessor (Kotlin `.value`
#                      or the `StringBox?.unwrap` extension; Swift/ObjC `.value`). A call that
#                      reads it any other way is an old-ABI call site.
#   4b. IMPLEMENTERS - every PLATFORM type that conforms to a bound Go interface named in the
#                      contract re-declares the migrated method with the boxed result. A method
#                      on a bound Go interface is implemented by the platform as well as by Go,
#                      and an implementation is a DECLARATION, so the call-site scan above cannot
#                      see it. Both clients shipped this defect: Android's
#                      RootBridgeSessionWrapper and Apple's BridgeServiceSession each declared
#                      `name(): String` against a Go `BridgeSession.Name() *StringBox`. The
#                      compiler catches it only in a configuration the release build does not
#                      always compile (Apple's is `#if os(macOS) || JAILBREAK`), which is why it
#                      is scanned here rather than left to a build that never sees the file.
#   5. CLIENT PIN    - the client gitlinks are the revisions the contract was verified against.
#
# # Usage
#
#   check-libbox-abi.sh
#   check-libbox-abi.sh --client-root <dir>    also scan a client checkout at <dir>
#   check-libbox-abi.sh --skip-client          layers 1-3 and the client pin only
#   check-libbox-abi.sh --pin-only             the client pin only, no Go toolchain needed
#
# Client source is found automatically in clients/android and clients/apple when the submodules
# are initialised. Point ANDROID_CLIENT_ROOT / APPLE_CLIENT_ROOT or --client-root at a checkout
# elsewhere to run the gate against a scratch clone of the fork. A missing client checkout is
# reported as SKIP with the reason and never as PASS: "I could not look" must not read the same
# as "I looked and it was fine".
#
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"

CONTRACT="$repo_root/docs/fork/libbox-abi-contract.tsv"
GO_PKG_DIR="$repo_root/experimental/libbox"
PROJECT_GOMOBILE_VERSION="$(cd "$repo_root" && go list -m -f '{{.Version}}' github.com/sagernet/gomobile 2>/dev/null || true)"

# The client revisions this contract was verified against; the superproject gitlinks must
# resolve to these. A checkout of either client at any other revision has not been shown to
# carry the migrated call sites.
#
# Android: Piggy-Cat-bit-shadow/sing-box-for-android, branch fix/libbox-stringbox-callsites.
#          Upstream SagerNet/sing-box-for-android does NOT carry them.
# Apple:   Piggy-Cat-bit-shadow/sing-box-for-apple. The migration is carried as
#          docs/fork/apple-stringbox-callsites.patch; see docs/fork/apple-client-fork.md.
#
# THE APPLE VALUE TRACKS THE SUPERPROJECT GITLINK. It is the revision the gitlink pins
# (`git ls-tree HEAD clients/apple`) after the Apple repository was consolidated to the single
# product branch `main`. The Apple repository now carries only `main` and `dev`; every
# construction branch, including the `jiejiebox/integrated` tip this constant used to name, was
# retired and its content preserved as `archive/*` tags, so the old value named a branch that no
# longer exists. The revision named here carries the call-site migration, the
# BridgeServiceSession implementer and the screen-state observer - and it is the revision whose
# caller sweep layer 4 performs and whose SFI (iOS) and SFM (macOS) builds were observed to
# succeed, which is what makes it the verified revision rather than merely a recorded one.
#
# Naming a revision here is a statement about it, not a verification of it, and the report above
# is not a substitute for layer 4. Layer 4 is the verification and it needs a client checkout to
# sweep. A host that cannot check one out - the submodule is unpopulated and github.com is
# unreachable, which is how the v0.1.6 Apple artifact run was performed - can establish layer 5
# (this constant and the gitlink agree) and nothing more, and must not report layer 4 as passed
# on that basis. See docs/fork/apple-artifact-verification.md §9.1.
# Moved to c7837107dc01 on 2026-10-28, when the Android fork converged its five branches into
# one product main. The move is not a rename: the revision merges that fork's dev (which carried
# the 2026-10-28 subscription User-Agent fix), the three StringBox commits layer 4 exists for,
# and SagerNet's dev (nine genuinely unabsorbed commits, three of them the service-lifecycle
# chain that has to be taken together).
#
# What verified it: scripts/ci/probe-accessors.py - the layer-4 scanner - against that checkout,
# 309 Kotlin files, 3 interface methods, zero violations. That covers both shapes the previous
# revision got wrong: the extension receiver in Wrappers.kt:49 (RoutePrefix.toIpPrefix) and the
# implementer in PlatformInterfaceWrapper.kt:445 (RootBridgeSessionWrapper.name).
#
# What did NOT verify it, written down so this constant is not read as more than it is: the
# Kotlin compile. `./gradlew assembleOtherDebug` needs JDK 17 and a build against a freshly
# regenerated libbox.aar, and the host that performed this merge has neither, so it is NOT_RUN
# there. The compiler errors recorded in docs/fork/android-abi-migration-closure.md are fixed by
# the same commits; the accessor sweep is what can be re-run without a toolchain, and it is.
VERIFIED_ANDROID_SHA="c7837107dc0126fb704fdf08cf203e10b30c8a22"
# Moved from 90a0e9aef when the Apple gitlink advanced to d05249121, because the two must
# be the same commit or this gate fails - which is exactly what it did.
#
# Moved again to 5cab4ec5, when the gitlink advanced one commit further with
# "build(apple): update Apple client pin". The second move is NOT the same argument as the
# first, so it is stated separately and re-derived rather than inherited.
#
# 90a0e9ae -> d0524912 (the first move; no source file changed at all):
#
#   sing-box.xcodeproj/project.pbxproj   IPHONEOS_DEPLOYMENT_TARGET 15.0 -> 16.0  (x2)
#
# d0524912 -> 5cab4ec5 DOES change one Swift file, so "no Swift file changed" would be
# false here and is deliberately not claimed. What changes is a pure presentation modifier:
#
#   ApplicationLibrary/Views/HakoStyle/HakoToolsView.swift  +2 `.buttonStyle(...)` lines
#
# on two navigation rows. No method this contract names is called, declared or altered by
# it, and the remainder of the range is KNOWLEDGE/ plus README/AGENTS documentation:
#
#   git -C clients/apple diff --name-status d0524912 5cab4ec5
#
# The property this constant asserts was therefore re-established rather than assumed:
# layer 4 was run against a clean clients/apple checked out at 5cab4ec5 and reported
# `scanned: 670, violations: []` across the whole tree, which is stronger evidence than
# the name-only comparison above. Re-derive rather than assume if this ever moves again,
# and prefer running the probe to reading this comment.
#
# Moved again to 40dd4879, when the gitlink advanced five commits to brand the macOS app
# that actually ships ("fix(apple): brand the macOS app SFM.System actually ships" and the
# four commits below it). Derived a third time from the range rather than inherited:
#
#   git -C clients/apple diff --name-status 5cab4ec5 40dd4879
#
# is 26 files: KNOWLEDGE/ (20), AGENTS.md, scripts/dev/audit_apple_ui_boundary.py,
# sing-box.xcodeproj/project.pbxproj, and exactly ONE Swift source file -
# ApplicationLibrary/Views/Dashboard/Cards/HTTPProxyCard.swift, which gained a
# `SurfaceStyle` enum and moved its body onto `HakoCardSurface`. That file is not a call
# site of anything this contract names: the contract's Swift entries are
# Library/Network/ExtensionPlatformInterface.swift and
# ApplicationLibrary/Views/Abstract/GlobalChecksModifier.swift, neither of which the range
# touches. The rest is documentation plus one `INFOPLIST_KEY_CFBundleDisplayName` value
# (`sing-box` -> `Jiejiebox` on the SFM.System target, Debug and Release), which no ABI
# layer reads.
#
# Layer 4 was then RUN again rather than reasoned about, against clients/apple at 40dd4879:
#
#   python3 scripts/ci/probe-accessors.py \
#     --contract docs/fork/libbox-abi-contract.tsv --lang swift --root clients/apple
#   {"scanned": 670, "violations": [], "interface_methods": 3, "error": ""}
#
# the same `scanned: 670, violations: []` the 5cab4ec5 move reported, on a tree that now
# contains the branded target.
#
# What this move does NOT rest on, stated because the comment above sets the precedent of
# saying so: layers 1-3 need a Go toolchain and a gobind, and the host this was derived on
# has neither, so this is a layer-4-and-5 derivation and not a whole-gate run. That is
# equally true of the two moves above - it is a property of the host, not of this range.
#
# Moved again to c001d59c, when the gitlink advanced to ship the App Store app as
# JiejieBox.app: the client's own five commits since 40dd487 ("separate quota freshness and
# make metadata probe header-only" through "make the iPhone outbound-mode card one compact
# row") plus "fix(macos): ship the App Store app as JiejieBox.app". The range is larger than
# the rename, so it was re-derived rather than inherited:
#
#   git -C clients/apple diff --name-status 40dd4879 c001d59c
#
# is 34 files: the client's subscription/UI work (Swift pages, Database, Network, the new
# CommandSubscriptionRecovery plus its harnesses), scripts/dev audits, and the rename
# itself - two `PRODUCT_NAME` values (`sing-box` -> `JiejieBox`, SFM Debug and Release) and
# the SFM scheme's three `BuildableName` entries (`sing-box.app` -> `JiejieBox.app`). It
# touches neither Swift file this contract names,
# Library/Network/ExtensionPlatformInterface.swift and
# ApplicationLibrary/Views/Abstract/GlobalChecksModifier.swift, and no method the contract
# lists is declared or called differently.
#
# Layer 4 was RUN against clients/apple at c001d59c rather than reasoned about:
#
#   python3 scripts/ci/probe-accessors.py \
#     --contract docs/fork/libbox-abi-contract.tsv --lang swift --root clients/apple
#   {"scanned": 418, "violations": [], "interface_methods": 3, "error": ""}
#
# `violations: []` on the moved tree, which is the property this constant asserts. The
# scanned count is recorded as measured here rather than carried over from the earlier move:
# it is a property of the probe and the tree, not a value this contract fixes.
#
# Layers 1-3 still need a Go toolchain and a gobind this host does not have, exactly as for
# the two moves above.
#
# Moved again to 6e559a4, which makes the tunnel settings omit IPv6 when the core sends none. The
# range is ONE file, Library/Network/ExtensionPlatformInterface.swift, and `git -C clients/apple
# diff -w c001d59 6e559a4` is exactly six added lines: four comment lines, `if !ipv6Address.isEmpty {`
# and its closing brace. Everything else in the ordinary diff is re-indentation of the block that is
# now inside the guard.
#
# That is the property layer 4 asserts, and it is derivable from the diff without the probe: no call
# site of a migrated method is added, removed or rewritten - the receiver text is identical modulo
# leading whitespace - and no declaration the contract lists changes. Layer 4 was NOT re-run on this
# host, unlike the c001d59 move: the interpreter resolves to a Store alias here and exits 9009. CI
# runs it against the moved tree with the unchanged contract.
#
# Moved again to 3aedd741, the v0.1.28 Apple pin. The range from 6e559a4 is seven commits and
# touches the Hako profile title, the macOS subscription-quota caption and its test package, the
# macOS double-click caption, and the network path monitor. `git diff --name-only 6e559a4
# 3aedd741` contains no file the ABI contract names: the migrated BridgeSession / RoutePrefix /
# TunOptions call sites are unchanged. Layer 4 is run in client-apple.yml against this exact
# gitlink, and the pin check is wired into verify.yml so a gitlink move cannot land alone again.
#
# Moved to aeccc754, the v0.1.32 Apple pin. The range 3aedd741..aeccc754 is one file,
# `Library/Network/ExtensionPlatformInterface.swift`, and it only changes how the utun MTU is
# derived (clamp to the hotspot bridge under include_all_networks) and adds one log line after
# `setTunnelNetworkSettings`. The one libbox call in that hunk, `options.getMTU()`, is unchanged;
# no contract-named method is added, removed or reshaped, so the same nine migrated methods are
# still the ones this gate covers.
#
# Moved to 16cf87b4 on 2026-10-28, the Apple fork's own `main` tip - the subscription
# User-Agent fix, and the revision the v0.1.33 Apple CI resolved and built while this gitlink
# still said aeccc754. That gap is the defect this round exists to remove: the superproject
# recorded one Apple source and the build used another.
#
# What verified it: layer 4 re-run against the exact revision,
#
#   python3 scripts/ci/probe-accessors.py --contract docs/fork/libbox-abi-contract.tsv \
#     --lang swift --root <sing-box-for-apple @ 16cf87b4>
#   {"scanned": 423, "violations": [], "interface_methods": 3, "error": ""}
#
# and the range aeccc754..16cf87b4 is the UA fix alone - `HTTPClient.swift`,
# `RemoteProfileFetcher.swift`, `NewProfileViewModel.swift` and the Hako subscription test with its
# stubs (`git diff --name-only aeccc754..16cf87b4`) - which touches no file this contract names.
# Layer 3 (the generated binding) is unchanged by it for the same reason. The compile half is the
# Apple client workflow, which is what the release path consumes.
VERIFIED_APPLE_SHA="16cf87b4bea0dd4e81d51b2b1b8c2e317ae88423"

client_root=""
skip_client=0
pin_only=0
while [ $# -gt 0 ]; do
  case "$1" in
    --client-root) client_root="${2:?--client-root needs a directory}"; shift 2 ;;
    --skip-client) skip_client=1; shift ;;
    --pin-only) pin_only=1; shift ;;
    -h|--help) sed -n '2,60p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) printf 'FAIL: unknown argument %s\n' "$1" >&2; exit 2 ;;
  esac
done

fails=0
skipped_client_layers=0
fail() { printf 'FAIL: %s\n' "$*" >&2; fails=$((fails + 1)); }
ok()   { printf 'PASS: %s\n' "$*"; }
skip() { printf 'SKIP: %s\n' "$*"; }
info() { printf '     %s\n' "$*"; }

# check_gitlink compares one client gitlink with the revision this contract was verified
# against. It is defined here so --pin-only can use it without running the Go surface scanner,
# which needs cgo and the Darwin profile and therefore cannot run on every push runner.
check_gitlink() {
  local path="$1" expected="$2" label="$3" upper actual
  upper="$(printf '%s' "$label" | tr '[:lower:]' '[:upper:]')"
  actual="$(cd "$repo_root" && git ls-tree HEAD "clients/$path" 2>/dev/null | awk '{print $3}')"
  if [ -z "$actual" ]; then
    # A missing gitlink is NOT a "client checkout not initialised" skip. The gitlink lives in the
    # superproject tree; a missing one means the tree cannot answer which client revision it
    # contains. Treating it as SKIP made --pin-only print PASS with nothing compared, so a project
    # that dropped the gitlink would turn the ABI gate green by removing its own input.
    fail "$label: no gitlink at clients/$path. The superproject must track it; a missing gitlink is not a skipped client."
    return
  fi
  if [ "$actual" = "$expected" ]; then
    ok "$label gitlink is the verified revision (${actual:0:12})"
  else
    fail "$label gitlink is ${actual:0:12}, but this contract was verified against
      ${expected:0:12}. A client at a different revision may still call the old ABI, and the
      AAR regenerating successfully says nothing about it. Verify the call sites at $actual,
      then update VERIFIED_${upper}_SHA in this script and the gitlink."
  fi
}

[ -f "$CONTRACT" ] || { printf 'FAIL: no ABI contract at %s\n' "$CONTRACT" >&2; exit 2; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

grep -v '^[[:space:]]*#' "$CONTRACT" | grep -v '^[[:space:]]*$' > "$tmp/records"
record_count="$(wc -l < "$tmp/records" | tr -d ' ')"
[ "$record_count" -gt 0 ] || { printf 'FAIL: the ABI contract has no records\n' >&2; exit 2; }

printf '== libbox ABI gate ==\n'
info "contract: docs/fork/libbox-abi-contract.tsv ($record_count migrated methods)"
info "gomobile pin: ${PROJECT_GOMOBILE_VERSION:-unknown}"
printf '\n'

# --pin-only is the cheap half for a push gate: it compares the client gitlinks with the
# revisions the contract was verified against, without building the Go surface scanner. The
# scanner needs cgo and the Darwin profile, so it cannot run on every push runner; the full gate
# runs in client-apple.yml. A gitlink move that lands alone is exactly how the Apple ABI drift
# this script exists for would reappear, so this half belongs in the push gate.
if [ "$pin_only" = 1 ]; then
  printf -- '-- layer 5: client gitlink --\n'
  check_gitlink android "$VERIFIED_ANDROID_SHA" android
  check_gitlink apple "$VERIFIED_APPLE_SHA" apple
  printf '\n'
  if [ "$fails" -gt 0 ]; then
    printf 'FAILED: %d client pin violation(s).\n' "$fails" >&2
    exit 1
  fi
  printf 'PASS: client gitlinks match the verified revisions.\n'
  exit 0
fi

# ---------------------------------------------------------------------------
# The gomobile surface inventory
# ---------------------------------------------------------------------------
#
# The scanner is the checked-in tripwire in gomobile_surface_test.go. It parses every non-test
# .go file in the package - including files excluded by the current build tags, so an
# Android-only declaration is still seen from a macOS checkout - and mirrors cmd/gobind's
# reachability rules. Reusing it keeps one definition of "the bound surface" instead of a
# second one here that would drift.
#
# -checklinkname=0 is passed because the default tag set contains tfogo_checklinkname0, which
# selects signal_handler_darwin.go and its runtime.getsig/setsig/fwdSig linknames; the linker
# rejects those under the default -checklinkname policy, so without the flag the test binary
# does not link on darwin and this gate could not run at all. The flag is exactly what the
# release recipe passes (see the comment at the top of signal_handler_darwin.go).
printf -- '-- surface inventory --\n'
inventory_ok=0
if (cd "$repo_root" && GOTOOLCHAIN="${GOTOOLCHAIN:-go1.25.5}" go test -v \
      -ldflags=-checklinkname=0 -tags "$(cat release/DEFAULT_BUILD_TAGS)" \
      -run TestGomobileMethodResultSurface ./experimental/libbox/) > "$tmp/surface.log" 2>&1; then
  inventory_ok=1
  grep -E 'gomobile surface:' "$tmp/surface.log" | tail -1 | sed 's/^ *//' | while IFS= read -r l; do info "$l"; done
  # The table is logged with a leading indent; dedent it, then key it by "owner<TAB>method".
  sed -n '/type | method | result type | class/,$p' "$tmp/surface.log" \
    | sed 's/^[[:space:]]*//' \
    | awk -F' \\| ' 'NF==4 { o=$1; if (o=="(package function)") o="-"; print o "\t" $2 "\t" $3 "\t" $4 }' \
    > "$tmp/surface.tsv"
  info "inventory rows: $(wc -l < "$tmp/surface.tsv" | tr -d ' ')"
else
  fail "the gomobile surface scanner did not run, so the Go layer cannot be verified:
$(sed 's/^/      /' "$tmp/surface.log" | tail -20)"
fi
printf '\n'

# ---------------------------------------------------------------------------
# Layers 1+2: the Go declaration
# ---------------------------------------------------------------------------
printf -- '-- layers 1-2: Go declaration --\n'
while IFS=$'\t' read -r go_owner go_method javamethod check_kind kotlin_re swift_re evidence; do
  key="$go_owner.$go_method"
  [ "$go_owner" = "-" ] && key="$go_method"

  if [ "$inventory_ok" = 1 ]; then
    # "-" is the scanner's marker for a package-level function; its key is the bare name,
    # not "-.Name". Kept in one place so the two spellings cannot drift.
    # `|| true`: awk exits 0 with no output when nothing matches, but an empty assignment
    # still must not be allowed to trip `set -e` on a future awk that exits non-zero.
    row="$(awk -F'\t' -v k="$key" '{ rk = ($1=="-") ? $2 : ($1"."$2); if (rk==k) { print; exit } }' "$tmp/surface.tsv" || true)"
    if [ -z "$row" ]; then
      fail "$key is not in the gomobile surface inventory at all. Either the declaration was
      removed or renamed (a silently missing API), or gobind now rejects its signature. The
      contract in docs/fork/libbox-abi-contract.tsv names an API no client can reach."
      continue
    fi
    klass="$(printf '%s' "$row" | cut -f4)"
    case "$klass" in
      *"BARE STRING/BYTES"*)
        fail "$key has a pointer-bearing result again: $klass
      The migration was reverted. A bare string/[]byte result puts a Go pointer in the packed
      gomobile result frame and can kill the process with
      'fatal error: bulkBarrierPreWrite: unaligned arguments'."
        continue
        ;;
    esac
    ok "$key is bound and its result is not pointer-bearing ($klass)"
  fi

  # The scanner classifies `*StringBox` as the generic "bound object" - it cannot know the box
  # type - so the specific box is asserted against the declaration itself. The receiver is
  # matched as `[^)]*` and the middle as `[^{]*` so a multi-line signature or a complex
  # parameter list still matches; only the result shape is being asserted.
  if [ "$go_owner" = "-" ]; then
    decl_re="^func ${go_method}\([^{]*\*StringBox"
  else
    decl_re="^func \([^)]*\) ${go_method}\([^{]*\*StringBox"
  fi
  if grep -rqE "$decl_re" "$GO_PKG_DIR"/*.go 2>/dev/null; then
    ok "$key returns *StringBox"
  else
    fail "$key does not return *StringBox in experimental/libbox. Expected a declaration
      matching: $decl_re"
  fi
done < "$tmp/records"

# ---------------------------------------------------------------------------
# Layer 3: the generated binding
# ---------------------------------------------------------------------------
printf '\n-- layer 3: generated Java binding --\n'
gobind_version=""
if command -v gobind >/dev/null 2>&1; then
  gobind_version="$(go version -m "$(command -v gobind)" 2>/dev/null \
    | sed -n 's/.*mod[[:space:]]*github.com\/sagernet\/gomobile[[:space:]]*\(v[^[:space:]]*\).*/\1/p' | head -1)"
fi

if [ -n "$gobind_version" ]; then
  info "gobind from github.com/sagernet/gomobile@$gobind_version"
  if [ "$gobind_version" != "$PROJECT_GOMOBILE_VERSION" ]; then
    info "NOTE: root go.mod pins $PROJECT_GOMOBILE_VERSION. scripts/ci/gomobile-toolchain.sh"
    info "      documents why the Apple build uses a newer patch level; for -lang=java the two"
    info "      versions are known to emit identical bindings (verified by diffing both)."
  fi
  if (cd "$repo_root" && gobind -lang=java -javapkg=io.nekohasekai -libname=box \
        -tags "$(cat release/DEFAULT_BUILD_TAGS)" -outdir "$tmp/bind" ./experimental/libbox) >/dev/null 2>&1; then
    while IFS=$'\t' read -r go_owner go_method javamethod check_kind kotlin_re swift_re evidence; do
      key="$go_owner.$go_method"; [ "$go_owner" = "-" ] && key="$go_method"
      if grep -rqE "^[[:space:]]*(public|protected)?[[:space:]]*(static[[:space:]]+)?(final[[:space:]]+)?(native[[:space:]]+)?StringBox ${javamethod}\(" "$tmp/bind" --include=*.java 2>/dev/null; then
        ok "$key -> generated Java 'StringBox ${javamethod}('"
      else
        fail "$key does not appear in the generated Java binding as returning StringBox.
      Either the method is no longer bound (a silently missing API) or its result went back to
      java.lang.String. Searched $tmp/bind for 'StringBox ${javamethod}('."
      fi
      if grep -rqE "^[[:space:]]*(public|protected)?[[:space:]]*(static[[:space:]]+)?(final[[:space:]]+)?(native[[:space:]]+)?java\.lang\.String ${javamethod}\(" "$tmp/bind" --include=*.java 2>/dev/null; then
        fail "$key still has a bare java.lang.String result in the generated Java binding
      ('java.lang.String ${javamethod}('). That is the old ABI shape."
      fi
    done < "$tmp/records"
  else
    fail "gobind failed to generate Java bindings for experimental/libbox, so the binding
      layer cannot be verified. Install github.com/sagernet/gomobile/cmd/gobind and re-run."
  fi
else
  skip "no gobind on PATH, so the generated binding was not inspected directly.
      Layers 1-2 above still assert the Go result is *StringBox, and cmd/gobind derives the
      Java result type purely from that. Install gomobile to have it checked end to end."
fi

# ---------------------------------------------------------------------------
# Layer 4: client call sites
# ---------------------------------------------------------------------------
printf '\n-- layer 4: client call sites --\n'

kotlin_roots=()
[ -n "${ANDROID_CLIENT_ROOT:-}" ] && kotlin_roots+=("$ANDROID_CLIENT_ROOT")
[ -n "$client_root" ] && kotlin_roots+=("$client_root")
kotlin_roots+=("$repo_root/clients/android")
swift_roots=()
[ -n "${APPLE_CLIENT_ROOT:-}" ] && swift_roots+=("$APPLE_CLIENT_ROOT")
[ -n "$client_root" ] && swift_roots+=("$client_root")
swift_roots+=("$repo_root/clients/apple")

find_root() {
  local want="$1"; shift
  local c
  for c in "$@"; do
    [ -d "$c" ] || continue
    # A real checkout has the marker; an empty submodule directory does not, and scanning an
    # empty directory would report a clean scan for a client that was never read.
    if [ "$want" = kotlin ] && [ -d "$c/app/src/main" ]; then printf '%s\n' "$c"; return 0; fi
    if [ "$want" = swift ] && { [ -d "$c/ApplicationLibrary" ] || [ -d "$c/Library" ]; }; then printf '%s\n' "$c"; return 0; fi
  done
  return 1
}

# The call-site scan itself lives in scripts/ci/probe-accessors.py. It is Python rather than
# shell on purpose, and the reason is written at the top of that file: the shell version of this
# check reported PASS on the one call site it was written to catch. This function only decides
# WHICH clients to scan and how to report the result.
# The call-site scan itself lives in scripts/ci/probe-accessors.py. It is Python rather than
# shell on purpose, and the reason is written at the top of that file: the shell version of this
# check reported PASS on the one call site it was written to catch. This function only decides
# which clients to scan and how to report the result.
# The call-site scan itself lives in scripts/ci/probe-accessors.py. It is Python rather than
# shell on purpose, and the reason is written at the top of that file: the shell version of this
# check reported PASS on the one call site it was written to catch. This function only decides
# which clients to scan and how to report the result.
scan_client() {
  local lang="$1" root="$2" result
  if ! result="$(python3 "$script_dir/probe-accessors.py" \
        --contract "$CONTRACT" --lang "$lang" --root "$root" 2>&1)"; then
    skip "the $lang accessor probe could not run against $root:
$(printf '%s' "$result" | sed 's/^/      /')"
    skipped_client_layers=$((skipped_client_layers + 1))
    return 0
  fi
  # The JSON is passed as an argument rather than on stdin so that the renderer can be a
  # here-document. An earlier revision piped it in and the here-document consumed it instead.
  local rendered
  rendered="$(python3 - "$result" <<'PYEOF'
import json
import sys

data = json.loads(sys.argv[1])
for v in data.get("violations") or []:
    if v.get("kind") == "implementer":
        # A platform type that conforms to a bound Go interface and re-declares the migrated
        # method with the old result type. The call-site scan cannot see this: an implementation
        # is a declaration, not a call.
        print(f"FAIL: {v['file'].rsplit('/', 1)[-1]} implements a bound libbox interface but does not")
        print("      return the migrated StringBox:")
        print(f"      {v['file']}:{v['line']}  (conformance at line {v['conformance_line']})")
        print(f"        {v['text']}")
        print(f"      {v['owner']}.{v['method']} is declared on a bound Go INTERFACE, so the platform")
        print("      implements it as well as Go, and its override must return the box too - it")
        print(f"      currently returns {v['return_type']!r}.")
        print("      The fix is to return the box: Kotlin `StringBox().apply { value = ... }`, Swift")
        print("      `LibboxStringBox()` with its `value` set. This is a COMPILE ERROR, not a silent")
        print("      one: the Kotlin compiler reports")
        print("        Return type of 'fun name(): String' is not a subtype of the return type of")
        print("        the overridden member 'fun name(): StringBox!'")
        print("      and the Swift compiler reports")
        print("        type '...' does not conform to protocol 'Libbox...Protocol'")
        print("        note: protocol requires function 'name()' with type '() -> LibboxStringBox?'")
        print("        note: candidate has non-matching type '() -> String'")
        continue
    print(f"FAIL: {v['file'].rsplit('/', 1)[-1]} call site does not read the migrated StringBox:")
    print(f"      {v['file']}:{v['line']}")
    print(f"        {v['text']}")
    print(f"      {v['owner']}.{v['method']} returns *StringBox (binding: {v['javamethod']}), so the")
    print("      result must be read through an accessor: Kotlin '.value' or the")
    print("      StringBox?.unwrap extension in")
    print("      app/src/main/java/io/nekohasekai/sfa/ktx/Wrappers.kt; Swift '.value'.")
    print(f"      The text after the call was {v['tail']!r}.")
    print("      Reading it any other way is a call against the OLD ABI: it compiles against the")
    print("      old libbox, and against a regenerated one the Kotlin compiler reports")
    print("        Argument type mismatch: actual type is 'StringBox!', but 'String!' was expected.")
PYEOF
)"
  if [ -n "$rendered" ]; then
    printf '%s\n' "$rendered" >&2
    fails=$((fails + $(printf '%s\n' "$rendered" | grep -c '^FAIL:')))
  fi
  local scanned iface
  scanned="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1]).get("scanned", 0))' "$result" 2>/dev/null || echo 0)"
  iface="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1]).get("interface_methods", 0))' "$result" 2>/dev/null || echo 0)"
  info "$lang sources scanned: $scanned files"
  [ "$scanned" -gt 0 ] || {
    skip "no $lang sources were read under $root"
    skipped_client_layers=$((skipped_client_layers + 1))
  }
  if [ "${iface:-0}" -gt 0 ]; then
    # A method on a bound Go interface is also implemented BY the platform, and that override
    # must return StringBox too. Both halves of that are now checked: the call-site scan above
    # for the calls, and the implementer scan inside probe-accessors.py for the declarations.
    # The compiler agreement is still recorded per method in the contract's check column, and it
    # is still the thing that makes the defect impossible to ship silently - Apple's
    # BridgeServiceSession lives under `#if os(macOS) || JAILBREAK`, so an iOS-only build never
    # compiles it and only this scan or a macOS/JAILBREAK build will say so.
    info "$iface of the contract's methods are declared on a bound Go interface, so the"
    info "platform implements them as well; those declarations are scanned for the boxed result."
  fi
}

if [ "$skip_client" = 1 ]; then
  skip "client scan disabled with --skip-client"
  skipped_client_layers=2
else
  if android_root="$(find_root kotlin "${kotlin_roots[@]}")"; then
    info "Android client: $android_root"
    scan_client kotlin "$android_root"
  else
    skip "no Android client checkout found (looked in: ${kotlin_roots[*]}).
      An uninitialised submodule is NOT evidence that the call sites are migrated.
      Run: git submodule update --init clients/android"
    skipped_client_layers=$((skipped_client_layers + 1))
  fi
  if apple_root="$(find_root swift "${swift_roots[@]}")"; then
    info "Apple client: $apple_root"
    scan_client swift "$apple_root"
  else
    skip "no Apple client checkout found (looked in: ${swift_roots[*]}).
      An uninitialised submodule is NOT evidence that the Apple call sites are migrated."
    skipped_client_layers=$((skipped_client_layers + 1))
  fi
fi

# ---------------------------------------------------------------------------
# Layer 5: the client pin
# ---------------------------------------------------------------------------
printf '\n-- layer 5: client gitlink --\n'
check_gitlink android "$VERIFIED_ANDROID_SHA" android
check_gitlink apple "$VERIFIED_APPLE_SHA" apple

printf '\n'
if [ "$fails" -gt 0 ]; then
  printf 'FAILED: %d ABI compatibility violation(s).\n' "$fails" >&2
  exit 1
fi
if [ "$skipped_client_layers" -gt 0 ]; then
  # Deliberately not a bare PASS. A run that never read a client checkout has verified the Go
  # side and nothing about the call sites, and "PASS ... verified across the client call sites"
  # would be a false statement about layers that were never opened.
  printf 'PARTIAL: the Go/binding layers passed, but %d client layer(s) were SKIPPED, so the\n' "$skipped_client_layers"
  printf 'call sites were NOT verified by this run. See the SKIP lines above for what was missing.\n'
  exit 0
fi
printf 'PASS: %d migrated methods verified across the Go declaration, the generated binding, the client call sites and the client pin.\n' "$record_count"
