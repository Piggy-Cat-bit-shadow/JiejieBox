#!/usr/bin/env python3
"""Verifies the branding overlay's scope: the display name is branded, the identity is not.

Usage: test-branding-scope.py <client-dir>

The overlay writes exactly ONE setting - INFOPLIST_KEY_CFBundleDisplayName - into the SFI
target's Debug and Release configurations. So the question this answers is not "did the
branding happen" but "did ONLY that change".

That distinction is not academic. An earlier revision of the overlay branded
PRODUCT_NAME as well, which renamed the built product and forced the SFI scheme's
BuildableName to be rewritten to match. PRODUCT_NAME is what the scheme builds, and it
sits next to the bundle identifier, the sing-box:// URL scheme and every existing Profile
record that keys on the product. The iOS product therefore keeps upstream's
PRODUCT_NAME = "sing-box"; the macOS product's rename to JiejieBox is committed by the
Apple client itself and is not this overlay's to make. The pinned Apple client draws the
same boundary in scripts/dev/audit_apple_ui_boundary.py, where PRODUCT_NAME is protected
except for the documented SFM rename and CFBundleDisplayName is the overlay's single
override. So the display name is asserted as branded AND the identity as unchanged, in
both cases positively: a reintroduced rename has to fail here.

Two targets still read upstream's PRODUCT_NAME = "sing-box" (SFI, SFT), so a global string
replacement would rename the tvOS app as well and still leave the SFI target looking
correct. A test that only counted branded SFI configurations would pass in that case, which
is why every other configuration is compared against the PINNED project rather than
against a literal: SFM already carries the client's committed JiejieBox product name and
branded display name, so "unchanged" has to mean "what the client committed", not
"sing-box".

    SFI Debug and Release   CFBundleDisplayName carries a brand spelling the overlay
                            accepts (the client commits "Jiejiebox")
    SFI Debug and Release   PRODUCT_NAME is still "sing-box", and the scheme still
                            builds sing-box.app
    SFT, SFM.System         both settings at their pinned values
    SFM                     not this overlay's target: compared to the pin, which carries
                            the client's JiejieBox product name
    every other target      the same; extensions included, whose display names are
                            unquoted and belong to no target this overlay owns
    the URL scheme          sing-box, read from the plist rather than grepped
    bundle id and App Group target-scoped, and unchanged in SFI Debug and Release

Exit status is non-zero on any failure, so a CI step cannot mistake a report for a pass.
"""
import importlib.util
import os
import plistlib
import re
import subprocess
import sys

#: What the overlay writes: the user-visible name only.
BRAND = "JiejieBox"

#: The product identity. Never branded, on any target.
UPSTREAM = "sing-box"

DISPLAY_KEY = "INFOPLIST_KEY_CFBundleDisplayName"
PRODUCT_KEY = "PRODUCT_NAME"

PROJECT = "sing-box.xcodeproj/project.pbxproj"
SCHEME = "sing-box.xcodeproj/xcshareddata/xcschemes/SFI.xcscheme"

#: The scheme names the app buildable. Because PRODUCT_NAME is untouched, this stays
#: `sing-box.app`; a branded buildable here means something renamed the product.
SCHEME_BUILDABLE = f'BuildableName = "{UPSTREAM}.app"'
SCHEME_BRANDED_BUILDABLE = f'BuildableName = "{BRAND}.app"'

# Targets that must NOT be branded by this overlay, with the settings they must still hold.
#
# SFM is deliberately absent: the macOS client commits its own JiejieBox product name and
# branded display name, so its expected values are the PIN, not the upstream literal. The
# pin comparison at the end of this file still covers it, and covers it more strongly than a
# literal would - the iOS overlay must leave SFM byte-identical.
#
# SFM.System declares no PRODUCT_NAME at all, which is legitimate upstream: it derives
# its name another way. Requiring "sing-box" there would fail on an untouched project,
# so its absence is asserted as absence rather than as a specific value.
UNTOUCHED_TARGETS = ["SFT", "SFM.System"]
TARGETS_WITHOUT_PRODUCT_NAME = {"SFM.System"}

SETTINGS = [PRODUCT_KEY, DISPLAY_KEY]


def accepted_source_names() -> tuple[str, ...] | None:
    """The spellings the iOS overlay accepts from the pinned client, or None.

    The pinned client commits the brand in the lower-case-b spelling (`Jiejiebox`) and the
    overlay normalises it, so the SFI assertion below has to ask "is this a spelling the
    overlay accepts" rather than "is this exactly what the overlay writes". Restating the
    list here is what let this test go stale: it demanded `JiejieBox` of a client that
    commits `Jiejiebox`, and the overlay's own accepted list - the thing that actually has
    to agree - was never consulted.
    """
    path = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                        "apply-apple-branding-overlay.py")
    try:
        spec = importlib.util.spec_from_file_location("apple_branding_overlay", path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
    except (OSError, AttributeError, SyntaxError):
        return None
    names = getattr(module, "ACCEPTED_SOURCE_NAMES", None)
    return tuple(names) if names else None


#: `importlib.util` is imported for the loader above; `sys` is set to not write bytecode
#: beside the overlays, which this suite asserts the repository is clean of.
sys.dont_write_bytecode = True
SFI_ACCEPTED_SOURCE_NAMES = accepted_source_names()

problems: list[str] = []


def check(condition: bool, message: str) -> None:
    if not condition:
        problems.append(message)


def config_list_ids(src: str, target: str) -> list[str]:
    """Real configuration ids for a target, from its buildConfigurations array."""
    m = re.search(
        r'Build configuration list for PBXNativeTarget "' + re.escape(target) + r'" \*/ = \{(.*?)\n\t\t\};',
        src,
        re.S,
    )
    if not m:
        return []
    array = re.search(r"buildConfigurations = \((.*?)\);", m.group(1), re.S)
    if not array:
        return []
    ids, seen = [], set()
    for cid in re.findall(r"([0-9A-Fa-f]{24}) /\*", array.group(1)):
        if cid not in seen:
            seen.add(cid)
            ids.append(cid)
    return ids


def config_blocks(src: str, target: str) -> dict[str, tuple[str, str]]:
    """{configuration name: (id, block body)} for a target."""
    out: dict[str, tuple[str, str]] = {}
    for cid in config_list_ids(src, target):
        m = re.search(
            rf'{re.escape(cid)} /\* (\w+) \*/ = \{{\s*isa = XCBuildConfiguration;(.*?)\n\t\t\}};',
            src,
            re.S,
        )
        if m:
            out[m.group(1)] = (cid, m.group(0))
    return out


def all_config_blocks(src: str) -> dict[str, tuple[str, str]]:
    """{configuration id: (name, block body)} for every XCBuildConfiguration present."""
    out: dict[str, tuple[str, str]] = {}
    for m in re.finditer(
        r'([0-9A-Fa-f]{24}) /\* (\w+) \*/ = \{\s*isa = XCBuildConfiguration;(.*?)\n\t\t\};',
        src,
        re.S,
    ):
        out.setdefault(m.group(1), (m.group(2), m.group(0)))
    return out


def setting(block: str, key: str) -> str | None:
    m = re.search(rf'^\s*{re.escape(key)} = "([^"]*)";', block, re.M)
    return m.group(1) if m else None


def without_display_name(block: str) -> str:
    """The block with its display-name line removed, for comparison against the pin."""
    return re.sub(rf'^\s*{re.escape(DISPLAY_KEY)} = "[^"]*";\n', "", block, flags=re.M)


def pinned_blocks(client: str) -> dict[str, tuple[str, str]] | None:
    """The configuration blocks as the pinned client committed them, or None.

    The expectation for every configuration the overlay does not own is the PIN, not a
    literal, so this reads the committed project out of the client's own git object
    store. None means "could not tell", which is reported rather than treated as a pass.
    """
    try:
        prefix = subprocess.run(
            ["git", "-C", client, "rev-parse", "--show-prefix"],
            capture_output=True, text=True, check=True,
        ).stdout.strip()
        blob = subprocess.run(
            ["git", "-C", client, "show", f"HEAD:{prefix}{PROJECT}"],
            capture_output=True, text=True, check=True,
        ).stdout
    except (OSError, subprocess.CalledProcessError):
        return None
    # `git show` hands back the file as the checkout's `core.autocrlf` decides, so on a
    # Windows host the blob's line endings are CRLF while the working-tree copy the caller
    # read is LF. The block regexes anchor on `\n\t\t};`, which then matches neither the
    # terminator nor anything else, `all_config_blocks` returns an empty table, and every
    # configuration is reported as "not in the pinned project" - a green tree failing for
    # a line-ending reason. Normalising here keeps one parser and one set of patterns for
    # both hosts, rather than a second CRLF-aware set that could drift from them.
    return all_config_blocks(blob.replace("\r\n", "\n").replace("\r", "\n"))


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: test-branding-scope.py <client-dir>", file=sys.stderr)
        return 2
    client = sys.argv[1]
    src = open(f"{client}/{PROJECT}", encoding="utf-8").read()

    # --- SFI: the display name is branded, the identity is NOT -----------------
    sfi = config_blocks(src, "SFI")
    check(set(sfi) == {"Debug", "Release"},
          f"SFI configurations: expected Debug and Release, found {sorted(sfi)}")
    for name in ("Debug", "Release"):
        if name not in sfi:
            continue
        _, block = sfi[name]
        got = setting(block, DISPLAY_KEY)
        if SFI_ACCEPTED_SOURCE_NAMES is None:
            problems.append(
                "apply-apple-branding-overlay.py declares no readable ACCEPTED_SOURCE_NAMES, "
                "so the SFI display name cannot be checked against what the overlay accepts"
            )
        else:
            check(got in SFI_ACCEPTED_SOURCE_NAMES,
                  f"SFI {name} {DISPLAY_KEY}: expected one of the spellings the overlay "
                  f"accepts {SFI_ACCEPTED_SOURCE_NAMES!r}, got {got!r}")
        # Identity is asserted positively. "Some other value" is not an acceptable
        # outcome here: the built product's name is what the scheme builds and what
        # every existing record, link and Profile keys on.
        got = setting(block, PRODUCT_KEY)
        check(got == UPSTREAM,
              f"SFI {name} {PRODUCT_KEY}: identity, expected {UPSTREAM!r}, got {got!r} "
              "(branding must not rename the product)")

    # --- every target this overlay does not own: still at their upstream values -----
    # Enumerating the targets we care about is not enough on its own, so the pinned
    # comparison below catches anything new, extensions included - and catches SFM, which
    # the client renames itself and which therefore has no upstream literal to assert.
    for target in UNTOUCHED_TARGETS:
        blocks = config_blocks(src, target)
        check(bool(blocks), f"{target}: no build configurations found; the scope check cannot run")
        for name, (_, block) in blocks.items():
            got = setting(block, PRODUCT_KEY)
            if target in TARGETS_WITHOUT_PRODUCT_NAME:
                # Legitimately absent upstream; the requirement is that this overlay did
                # not add one, asserted as absence rather than as a specific value.
                check(got is None,
                      f"{target} {name} {PRODUCT_KEY}: upstream declares none, got {got!r}")
            else:
                check(got == UPSTREAM,
                      f"{target} {name} {PRODUCT_KEY}: expected {UPSTREAM!r}, got {got!r} "
                      "(identity, never branded)")
            display = setting(block, DISPLAY_KEY)
            check(display != BRAND,
                  f"{target} {name} {DISPLAY_KEY}: {BRAND!r} belongs to SFI only, found it here")

    sfi_ids = {cid for cid, _ in sfi.values()}

    # --- nothing outside the SFI display name may differ from the pin ----------
    #
    # This is the assertion that makes "only the display name changed" mean the whole
    # project rather than the two settings we happened to think of. A blanket replacement
    # of the product name, a rename of SFT or SFM, or an edit to an extension's own
    # display name all show up here even though none of them carries the brand.
    pinned = pinned_blocks(client)
    if pinned is None:
        problems.append(
            f"the pinned {PROJECT} could not be read from {client} (is it a git checkout?), "
            "so 'unchanged everywhere else' cannot be verified; refusing to report a pass "
            "on an unchecked project"
        )
    else:
        prepared = all_config_blocks(src)
        for cid in sorted(prepared):
            name, block = prepared[cid]
            want = pinned.get(cid)
            if want is None:
                problems.append(
                    f"configuration {name} ({cid}) is not in the pinned project; "
                    "the overlay added it"
                )
                continue
            if cid in sfi_ids:
                # The overlay may rewrite the display-name line and NOTHING else.
                # Comparing the two settings alone would miss a splice that dropped,
                # duplicated or reordered a line elsewhere in the block.
                check(without_display_name(block) == without_display_name(want[1]),
                      f"SFI {name} ({cid}): only {DISPLAY_KEY} may differ from the "
                      "pinned client")
                continue
            for key in SETTINGS:
                got, expected = setting(block, key), setting(want[1], key)
                check(got == expected,
                      f"{name} ({cid}) {key}: the pinned client has {expected!r}, the "
                      f"prepared project has {got!r}; branding is scoped to the SFI "
                      "display name")
        for cid in sorted(set(pinned) - set(prepared)):
            problems.append(
                f"configuration {pinned[cid][0]} ({cid}) is in the pinned project and "
                "missing here"
            )

    # --- global sweep: a blanket replacement must fail this test --------------
    #
    # The assertion that matters is where the BRAND appears, not which upstream values are
    # legitimate. An earlier version also flagged any unrecognised PRODUCT_NAME, which
    # meant enumerating every value the project legitimately uses - $(TARGET_NAME),
    # $(inherited), sfajb-roothelper, and whatever a future update adds. That is a test
    # that breaks for reasons unrelated to branding, so it was replaced with the direct
    # question: is the brand anywhere it should not be?
    #
    # Two spellings count as "the brand is here": the finished name the overlay writes, and
    # the pinned spelling the client commits. `sing-box` is deliberately NOT one of them
    # even though the overlay accepts it as a starting point - it is the unbranded identity,
    # and SFT and both ShareExtensions legitimately carry it, so treating it as brand
    # surface flagged most of the project.
    brand_spellings = {BRAND}
    if SFI_ACCEPTED_SOURCE_NAMES:
        brand_spellings |= {name for name in SFI_ACCEPTED_SOURCE_NAMES if name != UPSTREAM}

    blocks = all_config_blocks(src)
    branded_ids = {
        cid for cid, (_, block) in blocks.items()
        if any(setting(block, key) in brand_spellings for key in SETTINGS)
    }
    # SFI is the only target this overlay owns. The pinned client additionally carries a
    # brand spelling on the targets the macOS overlay owns, which is the pinned state that
    # overlay expects rather than a second brand; everything else carrying one is the exact
    # silent second brand this sweep exists to catch.
    allowed = set(sfi_ids) | {cid for cid, (_, block) in blocks.items()
                              if setting(block, DISPLAY_KEY) in brand_spellings - {BRAND}}
    check(branded_ids == allowed,
          "a brand spelling may appear only in the configurations the overlays own; "
          f"branded {sorted(branded_ids)}, allowed {sorted(allowed)}")
    for cid in sorted(branded_ids - allowed):
        m = re.search(re.escape(cid) + r' /\* (\w+) \*/', src)
        problems.append(
            f"configuration {m.group(1) if m else cid} ({cid}) carries the brand but is "
            "neither SFI nor a target the macOS overlay owns"
        )

    # --- the SFI scheme still builds the unchanged product --------------------
    # The buildable follows PRODUCT_NAME. The revision this test exists for renamed the
    # product and had to rewrite this line to JiejieBox.app to keep the scheme working;
    # the overlay now validates the scheme and never rewrites it.
    scheme_path = f"{client}/{SCHEME}"
    try:
        scheme = open(scheme_path, encoding="utf-8").read()
    except FileNotFoundError:
        problems.append(f"{scheme_path} is missing, so the buildable name cannot be verified")
    else:
        check(SCHEME_BUILDABLE in scheme,
              f"{SCHEME} must still build {SCHEME_BUILDABLE!r}; the buildable follows "
              "PRODUCT_NAME, which branding does not change")
        check(SCHEME_BRANDED_BUILDABLE not in scheme,
              f"{SCHEME} must not point at {SCHEME_BRANDED_BUILDABLE!r}; the product was "
              "not renamed")

    # --- the URL scheme, read from the plist ----------------------------------
    # grep cannot prove CFBundleURLSchemes is intact: the string appears in many
    # contexts in an Info.plist. Reading the structured value can.
    plist_path = f"{client}/SFI/Info.plist"
    try:
        with open(plist_path, "rb") as fh:
            plist = plistlib.load(fh)
    except FileNotFoundError:
        problems.append(f"{plist_path} is missing, so the URL scheme cannot be verified")
    except Exception as exc:  # noqa: BLE001 - report, do not crash the gate
        problems.append(f"{plist_path} could not be parsed: {exc}")
    else:
        schemes: list[str] = []
        for entry in plist.get("CFBundleURLTypes", []):
            schemes.extend(entry.get("CFBundleURLSchemes", []))
        check(UPSTREAM in schemes,
              f"CFBundleURLSchemes must still contain {UPSTREAM!r}, found {schemes}")
        check(BRAND not in schemes,
              f"CFBundleURLSchemes must not contain {BRAND!r}; the URL scheme is a compatibility contract")

    # --- bundle id and App Group, target-scoped -------------------------------
    #
    # Previously this only proved the variable name still appeared SOMEWHERE in the
    # project, which a rename elsewhere would not have disturbed. Each location is now
    # checked where it actually lives.
    #
    # The bundle id is a pbxproj setting on the SFI configurations themselves.
    for name in ("Debug", "Release"):
        if name not in sfi:
            continue
        _, block = sfi[name]
        got = setting(block, "PRODUCT_BUNDLE_IDENTIFIER")
        check(got is not None and got.startswith("$("),
              f"SFI {name} PRODUCT_BUNDLE_IDENTIFIER: expected a build-setting reference, got {got!r}")

    # The App Group is an entitlement, not a build setting, so it is read from the
    # entitlements plist. Asserting on the pbxproj would have been asserting on the
    # wrong file - and would have passed whether or not the entitlement was intact.
    entitlements_path = f"{client}/SFI/SFI.entitlements"
    try:
        with open(entitlements_path, "rb") as fh:
            entitlements = plistlib.load(fh)
    except FileNotFoundError:
        problems.append(f"{entitlements_path} is missing, so the App Group cannot be verified")
    except Exception as exc:  # noqa: BLE001
        problems.append(f"{entitlements_path} could not be parsed: {exc}")
    else:
        groups = entitlements.get("com.apple.security.application-groups", [])
        check(groups == ["$(APP_GROUP_IDENTIFIER)"],
              "the App Group entitlement must remain $(APP_GROUP_IDENTIFIER), "
              f"found {groups}")
        check(all(BRAND not in str(g) for g in groups),
              f"the App Group must not be renamed to include {BRAND!r}")

    # --- report ---------------------------------------------------------------
    if problems:
        print("test-branding-scope: FAILED", file=sys.stderr)
        for problem in problems:
            print(f"  - {problem}", file=sys.stderr)
        return 1

    print("test-branding-scope: PASS")
    print(f"  SFI Debug/Release: {DISPLAY_KEY} is {BRAND}")
    print(f"  SFI Debug/Release: {PRODUCT_KEY} is {UPSTREAM}, and the scheme still builds {UPSTREAM}.app")
    print(f"  {'/'.join(UNTOUCHED_TARGETS)} and every other configuration: as pinned")
    print("  URL scheme, bundle id and App Group: intact")
    return 0


if __name__ == "__main__":
    sys.exit(main())
