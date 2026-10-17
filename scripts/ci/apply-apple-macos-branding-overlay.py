#!/usr/bin/env python3
"""Sets the macOS app's USER-VISIBLE name, and verifies the product name it ships under.

Invoked by scripts/ci/prepare-apple-client.sh. See that file for why this is an
overlay on the working tree rather than a change to the pinned submodule.

# What this overlay owns, and what it only verifies

This overlay writes exactly ONE setting:

    INFOPLIST_KEY_CFBundleDisplayName   "sing-box" -> "JiejieBox"

into the `SFM` target's Debug and Release configurations only.

`PRODUCT_NAME` is owned by the Apple client itself, not by this overlay: the pinned
project declares `JiejieBox` for the SFM target, so the archive contains
`JiejieBox.app` and does not collide with the official `sing-box.app` in /Applications.
This script VERIFIES that value, and the matching `SFM` scheme buildable, rather than
rewriting either. An earlier version of this file branded both settings and rewrote
the scheme from `sing-box.app` to a branded name, which made the parent repository the
thing that decided the client's product identity; the client's own project is now the
authority for the bundle name.

The boundary that remains protected is the identifier surface:

    PRODUCT_BUNDLE_IDENTIFIER   the bundle id, the sing-box:// URL scheme, Profile types
    BASE_PACKAGE_IDENTIFIER     the prefix every target's bundle id derives from

Those are identity, protocol and signing surface: another process, a server, an
existing Profile file or a `sing-box://` link keys on them. The Apple repository
states the same boundary itself and enforces it in
scripts/dev/audit_apple_ui_boundary.py:

    BRAND_DISPLAY_NAME        the display override this fork may make: "Jiejiebox"
    BRANDED_PRODUCT_NAME      the product rename, scoped to SFM only: "JiejieBox"
    PROTECTED_NAME_SETTINGS   ("PRODUCT_NAME", "PRODUCT_BUNDLE_IDENTIFIER",
                               "BASE_PACKAGE_IDENTIFIER")

`CFBundleDisplayName` is the name in the menu bar, the Dock and About, and it is the
only one of these nothing else reads.

Note that `SFM.System` - the unsandboxed Developer ID product - is deliberately not
branded by this overlay, and neither is any extension target. Only the App Store /
TestFlight app is.

# The name this overlay writes

`JiejieBox`, with a capital B, matching the iOS product. The two products present the
same name to the user; the earlier split spelling (macOS `Jiejiebox`, lower-case b) is
gone. The Apple client commits `Jiejiebox` as the SFM display name, which is an
accepted starting state and is normalised here.

Any spelling other than the accepted ones is refused rather than rewritten: a brand name
is not something to guess at, and a half-remembered variant is exactly the kind of typo
that ships.

# Accepted starting states

Each SFM configuration may legitimately start from either value upstream or the fork has
carried:

    "sing-box"     upstream, unbranded
    "Jijiebox"     the fork's committed display name (lower-case b)

Both are normalised to "JiejieBox". A configuration already reading "JiejieBox" is left
alone, so a second run changes nothing. Any OTHER value fails closed.

# Fail closed

If the pinned client is updated and these settings change or move, this script fails
rather than silently skipping - a branding overlay that quietly stops working would ship
an app still presenting itself as sing-box, which is the bug it exists to fix.
"""
import re
import sys

#: The single setting this overlay owns: the user-visible name.
DISPLAY_KEY = "INFOPLIST_KEY_CFBundleDisplayName"

#: What the user must see. Unified across both Apple products.
BRAND_DISPLAY_NAME = "JiejieBox"

#: Values the pinned client may legitimately start from, all normalised to the brand.
#: The spelling is JiejieBox - J-i-e-j-i-e-B-o-x. The client commits Jiejiebox
#: (lower-case b); "Jijiebox"/"JijieBox" (one "e" short) are NOT spellings of this brand
#: and are deliberately absent, so that a typo is refused rather than propagated.
ACCEPTED_SOURCE_NAMES = ("sing-box", "Jiejiebox", BRAND_DISPLAY_NAME)

#: Product and identifier surface. Never written by this overlay; asserted so that the
#: archive, the project and this script cannot disagree about what ships.
PROTECTED = {
    "PRODUCT_NAME": "JiejieBox",
    "PRODUCT_BUNDLE_IDENTIFIER": "$(BASE_PACKAGE_IDENTIFIER)",
}

TARGET = "SFM"

# The configurations this overlay is scoped to. Named explicitly so that a new
# configuration appearing upstream is a failure rather than something that silently gets
# branded: "Profile" or "Staging" could legitimately need a different product name, and
# guessing would ship a build nobody reviewed.
EXPECTED_CONFIG_NAMES = {"Debug", "Release"}

# The scheme names the app buildable. PRODUCT_NAME is the client's, so this must read
# `JiejieBox.app`; the scheme is validated, never rewritten.
SCHEME = "sing-box.xcodeproj/xcshareddata/xcschemes/SFM.xcscheme"
SCHEME_BUILDABLE = 'BuildableName = "JiejieBox.app"'


def fail(message: str, details: list[str] | None = None) -> None:
    print(f"apply-apple-macos-branding-overlay: {message}", file=sys.stderr)
    for detail in details or []:
        print(f"    - {detail}", file=sys.stderr)
    raise SystemExit(1)


def config_ids_for_target(src: str, target: str) -> list[str]:
    """Build-configuration ids belonging to `target`, in file order.

    Only the buildConfigurations array is read. The surrounding XCConfigurationList also
    contains defaultConfigurationIsVisible and defaultConfigurationName, and a naive scan
    for 24-character words picks up fragments of those names as if they were ids - which
    then resolved to no block at all.
    """
    m = re.search(
        r'Build configuration list for PBXNativeTarget "' + re.escape(target) + r'" \*/ = \{(.*?)\n\t\t\};',
        src,
        re.S,
    )
    if not m:
        fail(f"no build configuration list for the {target} target")
    array = re.search(r"buildConfigurations = \((.*?)\);", m.group(1), re.S)
    if not array:
        fail(f"the {target} configuration list has no buildConfigurations array")
    ids, seen = [], set()
    for cid in re.findall(r"([0-9A-Fa-f]{24}) /\*", array.group(1)):
        if cid not in seen:
            seen.add(cid)
            ids.append(cid)
    if not ids:
        fail(f"the configuration list for {target} yielded no configuration ids")
    return ids


def block_span(src: str, cid: str) -> tuple[int, int, str, str] | None:
    """Locate one XCBuildConfiguration block by id.

    Returns (start, end, name, body). The span covers exactly this configuration's text,
    so a splice at these offsets cannot touch another block however similar its contents
    are.
    """
    header = re.compile(
        rf'{re.escape(cid)} /\* (\w+) \*/ = \{{\s*isa = XCBuildConfiguration;',
    )
    m = header.search(src)
    if not m:
        return None
    end_marker = src.find("\n\t\t};", m.end())
    if end_marker < 0:
        return None
    end = end_marker + len("\n\t\t};")
    return m.start(), end, m.group(1), src[m.start():end]


def settable(block: str, key: str) -> str | None:
    m = re.search(rf'^\s*{re.escape(key)} = "([^"]*)";', block, re.M)
    return m.group(1) if m else None


def set_display_name(block: str, value: str) -> str:
    """Rewrite only the display-name setting, whatever it currently reads."""
    new, n = re.subn(
        rf'^(\s*{re.escape(DISPLAY_KEY)} = )"[^"]*";',
        rf'\g<1>"{value}";',
        block,
        flags=re.M,
    )
    if n != 1:
        fail(
            f"expected exactly one {DISPLAY_KEY} in a configuration block, found {n}",
            ["refusing to write a partial or ambiguous result"],
        )
    return new


def scheme_state(client: str) -> str:
    """"pinned" when the scheme names the app buildable; fail on anything else.

    An ambiguous scheme is reported and never rewritten: the buildable name follows
    PRODUCT_NAME, which the client owns and this overlay only verifies.
    """
    path = f"{client}/{SCHEME}"
    try:
        src = open(path, encoding="utf-8").read()
    except FileNotFoundError:
        fail(f"{path} is missing")

    if src.count(SCHEME_BUILDABLE) > 0:
        return "pinned"
    fail(
        "the SFM scheme does not reference the app buildable this overlay expects",
        [
            f"looked for {SCHEME_BUILDABLE!r}",
            "the scheme follows PRODUCT_NAME, which this overlay verifies rather than writes",
            "refusing to guess at the intended rename",
        ],
    )


def main() -> int:
    if len(sys.argv) != 2:
        fail("usage: apply-apple-macos-branding-overlay.py <client-dir>")
    client = sys.argv[1]
    path = f"{client}/sing-box.xcodeproj/project.pbxproj"
    try:
        src = open(path, encoding="utf-8").read()
    except FileNotFoundError:
        fail(f"{path} is missing")

    # --- resolve the SFM configurations by id ---------------------------------
    ids = config_ids_for_target(src, TARGET)

    listed = []
    for cid in ids:
        found = block_span(src, cid)
        if found is None:
            fail(
                "the SFM configuration list references an id with no matching block",
                [f"id {cid}"],
            )
        listed.append((cid,) + found)

    names = {name for _, _, _, name, _ in listed}

    if names != EXPECTED_CONFIG_NAMES:
        fail(
            "the SFM target's configuration set is not the expected one",
            [
                f"found {sorted(names)}",
                f"expected {sorted(EXPECTED_CONFIG_NAMES)}",
                "a new configuration may need different branding; review required",
            ],
        )

    spans = {cid: (start, end, name, block) for cid, start, end, name, block in listed}
    if len(spans) != len(EXPECTED_CONFIG_NAMES):
        fail(
            "the SFM target resolves to more configurations than expected",
            [f"resolved {len(spans)} ids for {sorted(names)}"],
        )

    # --- product and identifier surface must be intact ------------------------
    for cid, (_, _, name, block) in sorted(spans.items()):
        for key, want in PROTECTED.items():
            got = settable(block, key)
            if got != want:
                fail(
                    f"SFM {name} has {key} = {got!r}, expected {want!r}",
                    [
                        "PRODUCT_NAME and the bundle identifier are not display settings:",
                        "this overlay verifies them and never writes them",
                    ],
                )

    # --- normalise the display name -------------------------------------------
    unknown = []
    for cid, (_, _, name, block) in sorted(spans.items()):
        current = settable(block, DISPLAY_KEY)
        if current not in ACCEPTED_SOURCE_NAMES:
            unknown.append((name, current))
    if unknown:
        fail(
            "some SFM configurations are in neither the upstream nor the branded state",
            [f"SFM {name}: {DISPLAY_KEY} = {value!r}" for name, value in unknown]
            + [
                f"accepted: {', '.join(repr(v) for v in ACCEPTED_SOURCE_NAMES)}",
                "refusing to edit a partially branded project",
            ],
        )

    already = [c for c, (_, _, _, b) in spans.items() if settable(b, DISPLAY_KEY) == BRAND_DISPLAY_NAME]
    if len(already) == len(spans):
        print("  [branding] macOS display name already applied")
    else:
        changed_ids = 0
        for cid in sorted(spans, key=lambda c: spans[c][0], reverse=True):
            start, end, name, block = spans[cid]
            branded_block = set_display_name(block, BRAND_DISPLAY_NAME)
            if branded_block == block:
                continue
            src = src[:start] + branded_block + src[end:]
            changed_ids += 1
        if changed_ids == 0:
            fail(
                "no SFM configuration needed the display name, yet not all read it",
                ["refusing to report success without having changed anything"],
            )
        open(path, "w", encoding="utf-8").write(src)
        print(
            f"  [branding] macOS SFM CFBundleDisplayName set to {BRAND_DISPLAY_NAME} "
            f"in {changed_ids} configuration(s)"
        )

    # --- the scheme must name the product the project builds ------------------
    scheme_state(client)
    print(
        "  [branding] macOS SFM scheme BuildableName is JiejieBox.app "
        "(matches the SFM PRODUCT_NAME)"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
