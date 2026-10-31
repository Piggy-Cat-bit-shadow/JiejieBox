#!/usr/bin/env python3
"""Point the Apple client's Source Code and Releases links at this fork.

Usage: apply-apple-link-overlay.py <client-dir>

# What this changes, and what it deliberately does not

The Settings > About section carries product attribution: the row that opens the source
repository, and the Releases item in its context menu. Both must point at this fork, not
at upstream SagerNet/sing-box.

The Documentation, Changelog and Configuration links are NOT touched. They point at
sing-box.sagernet.org, which is the upstream project's technical documentation and
stays correct for a downstream client: the fork changes who publishes the app, not
where the protocol is documented. Rewriting them to a GitHub README would replace
maintained documentation with a file that is not a substitute for it.

The Sponsors links are not touched either. They credit the upstream author, and this
fork has no sponsor configuration of its own to substitute.

# Why an overlay rather than editing the client

The Apple checkouts are pinned revisions. Committing changes inside them would either be
lost on the next checkout or become an unmaintainable private fork. The edit is applied
to the WORKING TREE at build time, and the parent repository never records a modified
gitlink.

# Two clients, two shapes, one invariant

This overlay runs against BOTH Apple sources - the custom iOS client and the original
macOS client - and they are in different states:

  * the original macOS client ships upstream attribution, and this overlay rewrites it;
  * the custom iOS client already points at the fork, and is split into a per-platform
    About section (`#if os(iOS)` carries the source row, `#if os(macOS) || os(tvOS)`
    carries the source row and the Releases item).

An earlier version matched whole Swift expressions and demanded exactly one of each. That
only ever described one of those shapes: it reported the already-correct iOS client as
"patched, but not to the expected shape" and refused to prepare it, because its two
platform sections legitimately contain the same URL twice in two different spellings.

So the overlay is written against the invariant that actually matters, and it is
spelling-independent:

    every sing-box GitHub URL in the About section points at the fork
    there is at least one source link and at least one releases link

The URL is matched AS A QUOTED LITERAL, including its closing quote. That is what makes
the source URL safe to rewrite even though it is a strict prefix of the releases URL:
after the repository name the source literal requires a closing quote, and the releases
URL has a `/` there. The prefix relationship is therefore broken by the pattern itself
rather than by the order the replacements happen to run in.

# Two repository names for one fork

The kernel repository was renamed `Piggy-Cat-bit-shadow/sing-box` ->
`Piggy-Cat-bit-shadow/JiejieBox` on 2026-10-31, and the custom client's About page was
repointed to the new name in the same round (sing-box-for-apple 72136e5). The old name is
a GitHub redirect to the same repository, not a different project, so a client pinned
before or after the rename legitimately carries either spelling. Both are accepted as
"the fork"; only SagerNet is upstream. Rewriting an upstream link targets the CANONICAL
(JiejieBox) name, because that is where the project lives now.

Accepting one spelling only is what broke the Apple build on 2026-10-31: the overlay
demanded `sing-box`, the pinned client had already moved to `JiejieBox`, and it refused a
client that was already correct.

# Fail-closed

Every way this can go wrong is an error: no sing-box link at all (the About section was
restructured), an upstream link that survives the edit, a fork source or releases link
that is missing, or a documentation anchor that the edit disturbed. A half-attributed
build is not accepted.
"""

import sys

SOURCE_FILE = "ApplicationLibrary/Views/Setting/SettingView.swift"

# Matched as quoted string literals, so the surrounding Swift expression is irrelevant:
# `URL(string: "...")!` and `URL(string: String("..."))!` both contain the same literal.
UPSTREAM_SOURCE = '"https://github.com/SagerNet/sing-box"'
UPSTREAM_RELEASES = '"https://github.com/SagerNet/sing-box/releases"'

# The fork's own repository, under both of the names it has published as. Element 0 is the
# canonical name: it is what an upstream link is rewritten TO. See the docstring's
# "Two repository names for one fork".
FORK_SOURCE_URLS = (
    "https://github.com/Piggy-Cat-bit-shadow/JiejieBox",
    "https://github.com/Piggy-Cat-bit-shadow/sing-box",
)
FORK_RELEASES_URLS = tuple(f"{url}/releases" for url in FORK_SOURCE_URLS)


def quoted(url: str) -> str:
    """The Swift string literal carrying `url`, including its closing quote."""
    return f'"{url}"'

# Anchors that must survive untouched, asserted so that a repinning which rewrites the
# About section is noticed rather than silently accepted.
DOCUMENTATION_ANCHORS = [
    'URL(string: String(localized: "https://sing-box.sagernet.org/"))!',
    'URL(string: String(localized: "https://sing-box.sagernet.org/changelog/"))!',
    'URL(string: String(localized: "https://sing-box.sagernet.org/configuration/"))!',
]


def fail(message: str, details: list[str] | None = None) -> None:
    print(f"apply-apple-link-overlay: {message}", file=sys.stderr)
    for detail in details or []:
        print(f"    - {detail}", file=sys.stderr)
    raise SystemExit(1)


def counts(src: str) -> dict[str, int]:
    return {
        "upstream_source": src.count(UPSTREAM_SOURCE),
        "upstream_releases": src.count(UPSTREAM_RELEASES),
        "fork_source": sum(src.count(quoted(url)) for url in FORK_SOURCE_URLS),
        "fork_releases": sum(src.count(quoted(url)) for url in FORK_RELEASES_URLS),
    }


def describe(c: dict[str, int]) -> list[str]:
    return [
        f"upstream source x{c['upstream_source']}, fork source x{c['fork_source']}",
        f"upstream releases x{c['upstream_releases']}, fork releases x{c['fork_releases']}",
        "the fork is published as " + " and ".join(FORK_SOURCE_URLS),
    ]


def assert_final_state(src: str, label: str) -> None:
    """The invariant every prepared client must satisfy, whatever its starting shape."""
    c = counts(src)
    if c["upstream_source"] or c["upstream_releases"]:
        fail(
            f"{label}: an upstream SagerNet link is still present",
            describe(c) + ["this fork must not attribute itself to the upstream project"],
        )
    if c["fork_source"] < 1:
        fail(
            f"{label}: no source-code link points at this fork",
            describe(c) + ["the About section may have been restructured"],
        )
    if c["fork_releases"] < 1:
        fail(
            f"{label}: no releases link points at this fork",
            describe(c) + ["the About section may have been restructured"],
        )
    for anchor in DOCUMENTATION_ANCHORS:
        if anchor not in src:
            fail(
                f"{label}: a documentation link this overlay must preserve is missing",
                [f"looked for {anchor!r}", "the About section may have been restructured"],
            )


def main() -> int:
    if len(sys.argv) != 2:
        fail("usage: apply-apple-link-overlay.py <client-dir>")

    path = f"{sys.argv[1]}/{SOURCE_FILE}"
    try:
        src = open(path, encoding="utf-8").read()
    except FileNotFoundError:
        fail(f"{path} is missing")

    before = counts(src)

    # Releases BEFORE source. The quoted-literal patterns already make this safe - the source
    # literal cannot match inside the releases URL - but releasing the longer URL first keeps
    # the two edits independent of each other rather than relying on that.
    releases_rewritten = before["upstream_releases"]
    source_rewritten = before["upstream_source"]

    if releases_rewritten or source_rewritten:
        # A file holding BOTH the upstream and the fork URL is a partially applied state that
        # cannot be resolved safely: it is not obvious which of the two the product intends.
        if before["fork_source"] or before["fork_releases"]:
            fail(
                "the About section references BOTH the upstream and the fork links",
                describe(before) + ["this is a partially applied state that cannot be resolved safely"],
            )
        src = src.replace(UPSTREAM_RELEASES, quoted(FORK_RELEASES_URLS[0]))
        src = src.replace(UPSTREAM_SOURCE, quoted(FORK_SOURCE_URLS[0]))
        state = "upstream-rewritten"
    elif before["fork_source"] or before["fork_releases"]:
        # Already correct under either of the fork's repository names. Nothing is written, which
        # is also what makes a second run a no-op rather than an error.
        state = "already-fork"
    else:
        fail(
            "the About section contains no sing-box repository link at all",
            [
                f"looked for {UPSTREAM_SOURCE!r}",
                "and any of " + ", ".join(repr(quoted(url)) for url in FORK_SOURCE_URLS),
                "the About section may have been restructured",
            ],
        )

    if state == "upstream-rewritten":
        open(path, "w", encoding="utf-8").write(src)
        # Re-read rather than trusting the in-memory string.
        src = open(path, encoding="utf-8").read()

    after = counts(src)
    assert_final_state(src, path)

    print(
        "apply-apple-link-overlay: "
        f"{state}; rewrote {source_rewritten} source and {releases_rewritten} releases link(s); "
        f"fork source x{after['fork_source']}, fork releases x{after['fork_releases']}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
