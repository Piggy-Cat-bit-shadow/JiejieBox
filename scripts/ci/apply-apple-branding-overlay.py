#!/usr/bin/env python3
"""Sets the iOS app product's USER-VISIBLE name, without touching its identity.

Invoked by scripts/ci/prepare-apple-client.sh. See that file for why this is an
overlay on the working tree rather than a change to the pinned submodule.

# Brand display and product identity are separate things

This overlay writes exactly ONE setting:

    INFOPLIST_KEY_CFBundleDisplayName   "sing-box" -> "JiejieBox"

and it writes it into the `SFI` target's Debug and Release configurations only.

It does NOT touch `PRODUCT_NAME`, and that is the whole point of this revision. A
previous version branded BOTH settings, which renamed the built product and required
patching the `SFI` scheme's `BuildableName` from `sing-box.app` to `JiejieBox.app`.
That made the parent repository the thing that decided the client's product identity:

    PRODUCT_NAME              the built .app name, and CFBundleName with it
    the SFI scheme buildable  what Xcode builds and archives
    BASE_PACKAGE_IDENTIFIER   the bundle id, the sing-box:// URL scheme, Profile types

Those are identity, protocol and signing surface. Another process, a server, an
existing Profile file or a `sing-box://` link keys on them, so renaming them to change
what SpringBoard shows is a trade the parent does not get to make on the client's
behalf. The Apple repository states the same boundary itself and enforces it in
scripts/dev/audit_apple_ui_boundary.py:

    BRAND_DISPLAY_NAME = "Jijiebox"        # the one override this fork may make
    PROTECTED_NAME_SETTINGS = ("PRODUCT_NAME", "PRODUCT_BUNDLE_IDENTIFIER",
                               "BASE_PACKAGE_IDENTIFIER")

`CFBundleDisplayName` is the smallest lever that satisfies both: it is what
SpringBoard, the Dock, Spotlight and the app switcher show, and it is the only one of
the four that nothing else reads.

# The name this overlay writes

`JiejieBox`, with a capital B. The Apple client currently commits `Jijiebox` (lower-case
b) for both app targets; the macOS overlay in this repository normalises its own target
to `JiejieBox` as well, so the two products present the same name to the user. Any other
spelling is refused rather than rewritten, because a brand name is not something to
guess at.

# Accepted starting states

The pinned client is not required to be pristine. Each SFI configuration may legitimately
start from any of the values upstream or the fork has carried:

    "sing-box"     upstream, unbranded
    "Jijiebox"     the fork's committed display name (lower-case b)

Both are normalised to "JiejieBox". A configuration already reading "JiejieBox" is left
alone, so a second run changes nothing. Any OTHER value fails closed: a display name
this script does not recognise means the pinned client moved in a way nobody reviewed.
"I could not tell" must not read the same as "I looked and it was fine".

# Fail closed

If the pinned client is updated and these settings change or move, this script fails
rather than silently skipping - a branding overlay that quietly stops working would ship
an app still called sing-box, which is the bug it exists to fix.
"""
import re
import sys

#: The single setting this overlay owns: the user-visible name.
DISPLAY_KEY = "INFOPLIST_KEY_CFBundleDisplayName"

#: What the user must see.
BRAND_DISPLAY_NAME = "JiejieBox"

#: Values the pinned client may legitimately start from, all normalised to the brand.
#: The spelling is JiejieBox - J-i-e-j-i-e-B-o-x. The client commits Jiejiebox
#: (lower-case b); "Jijiebox"/"JijieBox" (one "e" short) are NOT spellings of this brand
#: and are deliberately absent, so that a typo is refused rather than propagated.
ACCEPTED_SOURCE_NAMES = ("sing-box", "Jiejiebox", BRAND_DISPLAY_NAME)

#: Identity surface. Never written by this overlay; asserted so that a reintroduced
#: rename is caught here rather than discovered in a shipped bundle.
PROTECTED = {
    "PRODUCT_NAME": "sing-box",
    "PRODUCT_BUNDLE_IDENTIFIER": "$(BASE_PACKAGE_IDENTIFIER)",
}

TARGET = "SFI"

# The configurations this overlay is scoped to. Named explicitly so that a new
# configuration appearing upstream is a failure rather than something that silently gets
# branded: "Profile" or "Staging" could legitimately need a different product name, and
# guessing would ship a build nobody reviewed.
EXPECTED_CONFIG_NAMES = {"Debug", "Release"}

# The scheme names the app buildable. Because PRODUCT_NAME is untouched, this stays
# `sing-box.app`; the scheme is validated, never rewritten.
SCHEME = "sing-box.xcodeproj/xcshareddata/xcschemes/SFI.xcscheme"
SCHEME_BUILDABLE = 'BuildableName = "sing-box.app"'


def fail(message: str, details: list[str] | None = None) -> None:
    print(f"apply-apple-branding-overlay: {message}", file=sys.stderr)
    for detail in details or []:
        print(f"    - {detail}", file=sys.stderr)
    raise SystemExit(1)


def config_ids_for_target(src: str, target: str) -> list[str]:
    """Build-configuration ids belonging to `target`, in file order.

    Only the buildConfigurations array is read. The surrounding XCConfigurationList also
    contains defaultConfigurationIsVisible and defaultConfigurationName, and a naive scan
    for 24-character words picks up fragments of those names as if they were ids - which
    the earlier version did, and which then resolved to no block at all.
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
    # ids are collected from the `/* name */` comments so the order is the file's order,
    # and de-duplicated because the array names each id once per list it appears in.
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

    Returns (start, end, name, body). The span covers exactly this configuration's
    text, so a splice at these offsets cannot touch another block however similar its
    contents are.
    """
    header = re.compile(
        rf'{re.escape(cid)} /\* (\w+) \*/ = \{{\s*isa = XCBuildConfiguration;',
    )
    m = header.search(src)
    if not m:
        return None
    # The block ends at the first line that is exactly two tabs and a closing brace,
    # which is the indentation every XCBuildConfiguration in this file uses.
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
    PRODUCT_NAME, which this overlay deliberately does not change.
    """
    path = f"{client}/{SCHEME}"
    try:
        src = open(path, encoding="utf-8").read()
    except FileNotFoundError:
        fail(f"{path} is missing")

    if src.count(SCHEME_BUILDABLE) > 0:
        return "pinned"
    fail(
        "the SFI scheme does not reference the app buildable this overlay expects",
        [
            f"looked for {SCHEME_BUILDABLE!r}",
            "the scheme follows PRODUCT_NAME, which branding does not change",
            "refusing to guess at the intended rename",
        ],
    )


def main() -> int:
    if len(sys.argv) != 2:
        fail("usage: apply-apple-branding-overlay.py <client-dir>")
    client = sys.argv[1]
    path = f"{client}/sing-box.xcodeproj/project.pbxproj"
    try:
        src = open(path, encoding="utf-8").read()
    except FileNotFoundError:
        fail(f"{path} is missing")

    # --- resolve the SFI configurations by id ---------------------------------
    ids = config_ids_for_target(src, TARGET)

    # Resolve the listed ids to their names first, so an unrecognised configuration is
    # reported as a scope change rather than as a missing block.
    listed = []
    for cid in ids:
        found = block_span(src, cid)
        if found is None:
            fail(
                "the SFI configuration list references an id with no matching block",
                [f"id {cid}"],
            )
        listed.append((cid,) + found)

    names = {name for _, _, _, name, _ in listed}

    # Check the SCOPE before anything else. A new configuration upstream is the case this
    # overlay must not guess about.
    if names != EXPECTED_CONFIG_NAMES:
        fail(
            "the SFI target's configuration set is not the expected one",
            [
                f"found {sorted(names)}",
                f"expected {sorted(EXPECTED_CONFIG_NAMES)}",
                "a new configuration may need different branding; review required",
            ],
        )

    spans = {cid: (start, end, name, block) for cid, start, end, name, block in listed}
    if len(spans) != len(EXPECTED_CONFIG_NAMES):
        fail(
            "the SFI target resolves to more configurations than expected",
            [f"resolved {len(spans)} ids for {sorted(names)}"],
        )

    # --- identity surface must be intact --------------------------------------
    # Checked before any write, so a client whose PRODUCT_NAME moved is reported rather
    # than silently half-branded by a later overlay run.
    for cid, (_, _, name, block) in sorted(spans.items()):
        for key, want in PROTECTED.items():
            got = settable(block, key)
            if got != want:
                fail(
                    f"SFI {name} has {key} = {got!r}, expected {want!r}",
                    [
                        "PRODUCT_NAME and the bundle identifier are identity surface,",
                        "not branding; this overlay never changes them",
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
            "some SFI configurations are in neither the upstream nor the branded state",
            [
                f"SFI {name}: {DISPLAY_KEY} = {value!r}"
                for name, value in unknown
            ]
            + [
                f"accepted: {', '.join(repr(v) for v in ACCEPTED_SOURCE_NAMES)}",
                "refusing to edit a partially branded project",
            ],
        )

    already = [c for c, (_, _, _, b) in spans.items() if settable(b, DISPLAY_KEY) == BRAND_DISPLAY_NAME]
    if len(already) == len(spans):
        print("  [branding] display name already applied")
    else:
        changed_ids = 0
        # Splice by descending offset so earlier spans stay valid.
        for cid in sorted(spans, key=lambda c: spans[c][0], reverse=True):
            start, end, name, block = spans[cid]
            branded_block = set_display_name(block, BRAND_DISPLAY_NAME)
            if branded_block == block:
                continue
            src = src[:start] + branded_block + src[end:]
            changed_ids += 1
        if changed_ids == 0:
            fail(
                "no SFI configuration needed the display name, yet not all read it",
                ["refusing to report success without having changed anything"],
            )
        open(path, "w", encoding="utf-8").write(src)
        print(
            f"  [branding] SFI CFBundleDisplayName set to {BRAND_DISPLAY_NAME} "
            f"in {changed_ids} configuration(s)"
        )

    # --- the scheme must still name the unchanged product ---------------------
    scheme_state(client)
    print("  [branding] SFI scheme BuildableName unchanged (PRODUCT_NAME is identity)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
