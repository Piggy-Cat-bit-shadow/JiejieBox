#!/usr/bin/env python3
"""Assert the Apple client's external links are the ones this fork intends to ship.

Usage: check-apple-links.py <client-dir>

Run after apply-apple-link-overlay.py, and separately by any build that wants to verify
the prepared source without applying anything.

# What it checks, and why the documentation links are checked too

It is not enough to confirm the fork links are present. A build that had silently lost
the documentation links - or had them rewritten to something else - would still pass a
check that only looked for the fork URLs. Every assertion below is therefore two-sided:
the fork links must be present, and the upstream documentation links must still be there.

# Why "present", not "present exactly once"

There is ONE Apple source, so this is one file with one About section - there is no
"the iOS source" and "the macOS source" to compare. At the pinned revision
`SettingView.swift` carries one source row and one releases row, already pointing at the
fork; a revision that still ships upstream attribution has both rewritten by the link
overlay, and the overlaid tree then holds one of each as well.

The check is still written as "present" rather than "present exactly once" on purpose: the
About section is a UI surface, and a row may legitimately be added or the section split per
platform in a future client revision without attribution becoming wrong. Demanding a fixed
count would fail a correct client for the shape of its settings screen, which is not what
this file is for. The invariant that actually matters is direction of attribution - every
sing-box repository link points at the fork and both link kinds exist - and that is what is
asserted below, in both directions.

# What it cannot check

This reads source. It cannot prove that tapping a row opens Safari on a device. That
distinction is stated rather than blurred: a static check passes for a build whose links
are correct in source, which is the strongest claim this file can make.
"""

import sys

SOURCE_FILE = "ApplicationLibrary/Views/Setting/SettingView.swift"

# Matched as quoted literals, so the surrounding Swift expression does not matter. See
# apply-apple-link-overlay.py for why the quoted form is what makes the source URL safe to
# look for even though it is a prefix of the releases URL.
#
# The fork publishes under two repository names - `JiejieBox` since the 2026-10-31 rename and
# `sing-box` before it - so either spelling satisfies the requirement. Both are the same
# repository (GitHub redirects the old name); only SagerNet is upstream. Requiring one
# spelling is what failed the Apple build when the pinned client moved to the new name.
FORK_REPOSITORIES = (
    "https://github.com/Piggy-Cat-bit-shadow/JiejieBox",
    "https://github.com/Piggy-Cat-bit-shadow/sing-box",
)

REQUIRED: dict[str, tuple[str, ...]] = {
    "source code": tuple(f'"{url}"' for url in FORK_REPOSITORIES),
    "releases": tuple(f'"{url}/releases"' for url in FORK_REPOSITORIES),
}

DOCUMENTATION = {
    "documentation": 'URL(string: String(localized: "https://sing-box.sagernet.org/"))!',
    "changelog": 'URL(string: String(localized: "https://sing-box.sagernet.org/changelog/"))!',
    "configuration": 'URL(string: String(localized: "https://sing-box.sagernet.org/configuration/"))!',
}

# Attribution that must NOT appear anywhere in the About section's product links.
FORBIDDEN = {
    "upstream source code": '"https://github.com/SagerNet/sing-box"',
    "upstream releases": '"https://github.com/SagerNet/sing-box/releases"',
}


def fail(message: str, details: list[str] | None = None) -> None:
    print(f"check-apple-links: {message}", file=sys.stderr)
    for detail in details or []:
        print(f"    - {detail}", file=sys.stderr)
    raise SystemExit(1)


def main() -> int:
    if len(sys.argv) != 2:
        fail("usage: check-apple-links.py <client-dir>")

    path = f"{sys.argv[1]}/{SOURCE_FILE}"
    try:
        src = open(path, encoding="utf-8").read()
    except FileNotFoundError:
        fail(f"{path} is missing")

    problems: list[str] = []

    for label, needles in REQUIRED.items():
        found = sum(src.count(needle) for needle in needles)
        if found < 1:
            problems.append(f"{label}: expected at least 1, found {found} -> any of {list(needles)}")

    for label, needle in DOCUMENTATION.items():
        found = src.count(needle)
        if found < 1:
            problems.append(f"{label}: expected at least 1, found {found} -> {needle}")

    for label, needle in FORBIDDEN.items():
        found = src.count(needle)
        if found != 0:
            problems.append(f"{label}: still present x{found} -> {needle}")

    if problems:
        fail("the prepared client does not carry the links this fork ships", problems)

    print("check-apple-links: OK")
    present = [
        url
        for url in FORK_REPOSITORIES
        if src.count(f'"{url}"') or src.count(f'"{url}/releases"')
    ]
    print(f"    fork repo     = {present[0] if len(present) == 1 else ' / '.join(present)}")
    print("    documentation = https://sing-box.sagernet.org/ (upstream technical docs, unchanged)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
