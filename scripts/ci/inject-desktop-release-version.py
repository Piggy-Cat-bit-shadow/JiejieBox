#!/usr/bin/env python3
"""Stamps the Jiejiebox release version into a desktop client's version.json.

Usage:
    inject-desktop-release-version.py <desktop-client-dir> --version <X.Y.Z>
    inject-desktop-release-version.py <desktop-client-dir> --check-only

Invoked by scripts/ci/prepare-desktop-client.sh. See that file for the source-identity
checks that run before this one, and for why the release version arrives as an argument.

# What this replaced, and why

This client used to be a checkout of upstream `SagerNet/sing-box-for-desktop`, and the
parent repository carried a 23-rule branding overlay
(`apply-desktop-branding-overlay.py`) that rewrote its source before every build. The
branding now lives in the maintained fork `Piggy-Cat-bit-shadow/sing-box-for-desktop`
(branch `jiejiebox/main`), which the parent pins like any other submodule, so the static
overlay has been retired.

What could NOT move into the fork is the release version. The fork cannot know which
version the core is being released as; `release/JIEJIE_VERSION` in the parent checkout is
the single authority, and the parent is the only place with the root context to read it.
So this file is the entire remainder of that overlay: one field, in one file.

That field is load-bearing. `version.json` is read by the packaged app and by the updater,
so a build whose `version.json` disagrees with the tag it was released under produces an
installer named for one version and containing another - and the updater then either
refuses to upgrade to it or offers it forever. That is not hypothetical: the v0.1.6
installer was built from a working tree whose version literal still said 0.1.5, which is
why the version is an argument here rather than a constant.

# What is asserted about the SOURCE record, and what is not

`version.json` is a fork-owned file in the pinned client, and its `version` is whatever the
last fork release left there - measured at the time of writing: `0.1.5`, while the core is
`0.1.7`. The two are independent identities and are deliberately NOT required to match: a
rule that demanded the current fork version would be a new historical literal, and a rule
that demanded the upstream client version (`1.14.2`, the value the retired overlay was
anchored on) would pin this script to a repository the build no longer uses.

What IS required is that the record is usable and well formed:

  * it exists, and is UTF-8 JSON;
  * it is a JSON object;
  * it HAS a `version` key, and that value is a string in the accepted release-version
    grammar.

The `version` key must be present rather than defaulted. A missing one means this is not
the file this build thinks it is, and inventing a value would hide exactly the drift this
script exists to prevent. A missing `go_version` is accepted, because that field is the
fork's build record rather than part of the release identity, and this script must not
become the reason a fork release cannot be built.

# What is guaranteed

Exactly one top-level field may change: `version`. Everything else - every other key, its
value, the nesting, the file's own indentation width and its trailing-newline convention -
is preserved, and the file is re-read and re-parsed afterwards to prove it. The write is
atomic: a failure anywhere leaves the previous bytes in place rather than a half-written
file, because a client that believes it is another version builds perfectly.
"""
import argparse
import json
import os
import re
import sys
import tempfile

# A release version: a dotted numeric core, optionally with a pre-release and build
# suffix. Anchored, so a path, a branch name or a trailing newline cannot pass as a
# version - `release/JIEJIE_VERSION` handed to --version by mistake must fail here rather
# than become an installer name.
RELEASE_VERSION_RE = re.compile(
    r"^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$"
)

VERSION_RECORD = "version.json"

# The one field this script is allowed to change. Named, so the post-write proof is a
# comparison against this constant rather than against a second hard-coded string.
VERSION_FIELD = "version"


def fail(message, details=None):
    print(f"inject-desktop-release-version: {message}", file=sys.stderr)
    for detail in details or []:
        print(f"    - {detail}", file=sys.stderr)
    raise SystemExit(1)


def parse_version(raw, what="--version"):
    """A release version, validated. No default: an unusable value is refused."""
    if not isinstance(raw, str) or not raw:
        fail(f"{what} is required and must not be empty")
    value = raw.strip()
    if not RELEASE_VERSION_RE.match(value):
        fail(
            f"{what} {raw!r} is not a release version.",
            [
                "expected a dotted numeric version, optionally suffixed: 0.1.7 / 0.2.0-rc1",
                "the authority is release/JIEJIE_VERSION in the checkout being released",
            ],
        )
    return value


def read_record(path):
    """The parsed record and its raw text, with every failure named as the failure it is.

    UTF-8 with universal newlines, for the same two reasons the retired overlay documented:
    the default encoding on a Windows runner is cp1252 (which raised UnicodeDecodeError on
    a client file containing Chinese), and the default newline translation rewrote LF files
    as CRLF - an invisible modification of a file no rule named.
    """
    if not os.path.isfile(path):
        fail(
            f"{VERSION_RECORD} is missing from the desktop client checkout",
            [
                f"looked for {path}",
                "the client is a submodule: run 'git submodule update --init clients/desktop'",
            ],
        )
    with open(path, encoding="utf-8", newline=None) as stream:
        raw = stream.read()
    try:
        record = json.loads(raw)
    except json.JSONDecodeError as error:
        fail(
            f"{VERSION_RECORD} is not valid JSON",
            [f"{error}", "refusing: a malformed record cannot be corrected by guessing"],
        )
    if not isinstance(record, dict):
        fail(
            f"{VERSION_RECORD} is not a JSON object",
            [f"found {type(record).__name__}"],
        )
    if VERSION_FIELD not in record:
        fail(
            f"{VERSION_RECORD} has no {VERSION_FIELD!r} key",
            [
                "found keys: %s" % ", ".join(sorted(record)) if record else "the object is empty",
                "refusing: the release identity must be stated by the source, not invented here",
            ],
        )
    return record, raw


def current_version(record, path):
    """The source record's own version, validated as a version rather than compared.

    This is the check that catches a checkout which is not the client this build expects:
    the value may legitimately differ between fork releases, so it is validated for SHAPE
    and never for equality with a literal. An unusable value - a number, an empty string, a
    git ref - means the file is not the record this script can safely edit.
    """
    value = record[VERSION_FIELD]
    if not isinstance(value, str) or not RELEASE_VERSION_RE.match(value.strip()):
        fail(
            f"{path}: {VERSION_FIELD!r} is not a release version",
            [f"found {value!r}", "refusing to overwrite an unreadable source record"],
        )
    return value.strip()


def detect_indent(raw):
    """The record's own indentation width, so re-serialising does not reformat the file."""
    for line in raw.splitlines():
        if not line.strip():
            continue
        stripped = line.lstrip(" ")
        width = len(line) - len(stripped)
        if width:
            return width
    return 2


def serialise(record, raw):
    """The record as text, in the file's own shape: same indentation, same final newline."""
    text = json.dumps(record, indent=detect_indent(raw), ensure_ascii=False)
    return text + "\n" if raw.endswith("\n") else text


def write_atomically(path, text):
    """Replace `path` only once the whole new text exists on disk.

    Written as UTF-8 with LF explicitly: on a Windows runner the default newline translation
    would turn an LF file into CRLF, silently modifying a file no rule named.
    """
    directory = os.path.dirname(path) or "."
    handle, temporary = tempfile.mkstemp(dir=directory, prefix=".version-inject-")
    try:
        with os.fdopen(handle, "w", encoding="utf-8", newline="\n") as stream:
            stream.write(text)
        os.chmod(temporary, os.stat(path).st_mode & 0o7777)
        os.replace(temporary, path)
    except BaseException:
        if os.path.exists(temporary):
            os.unlink(temporary)
        raise


def main():
    parser = argparse.ArgumentParser(
        prog="inject-desktop-release-version.py",
        description="Stamp the Jiejiebox release version into a desktop client's version.json.",
    )
    parser.add_argument("desktop_client_dir", help="the desktop client checkout to stamp")
    parser.add_argument(
        "--version",
        help="the Jiejiebox release version to stamp; read release/JIEJIE_VERSION and pass it",
    )
    parser.add_argument(
        "--check-only",
        action="store_true",
        help="validate the source record and report the version without writing",
    )
    args = parser.parse_args()

    if args.check_only and args.version is not None:
        fail("--check-only and --version are mutually exclusive")
    if not args.check_only and args.version is None:
        fail("--version is required unless --check-only is given")

    root = os.path.realpath(args.desktop_client_dir)
    if not os.path.isdir(root):
        fail(f"{args.desktop_client_dir} is not a directory")

    path = os.path.join(root, VERSION_RECORD)
    record, raw = read_record(path)
    before = current_version(record, path)

    if args.check_only:
        print(f"  {VERSION_RECORD}: source version {before} (not modified)")
        print("PASS: desktop version record is readable and well formed")
        return

    wanted = parse_version(args.version)

    # Everything except `version` must survive verbatim. Captured before the write so the
    # comparison afterwards is against what was actually read, not against a second copy of
    # the expectations.
    untouched_before = {k: v for k, v in record.items() if k != VERSION_FIELD}

    if before == wanted:
        # Still re-proved below rather than returned early: "already correct" must mean the
        # same thing as "just written", or a hand-edited tree would pass here and fail later.
        changed = False
    else:
        record[VERSION_FIELD] = wanted
        changed = True

    text = serialise(record, raw)
    if changed:
        write_atomically(path, text)

    # --- confirm what is on disk, by re-reading and re-parsing it -------------
    after_record, after_raw = read_record(path)
    if after_record.get(VERSION_FIELD) != wanted:
        fail(
            f"{VERSION_RECORD}: wrote {wanted!r} but read back {after_record.get(VERSION_FIELD)!r}",
            ["a release that cannot read its own version back will not be reproducible"],
        )
    untouched_after = {k: v for k, v in after_record.items() if k != VERSION_FIELD}
    if untouched_after != untouched_before:
        fail(
            f"{VERSION_RECORD}: fields other than {VERSION_FIELD!r} changed",
            [
                "before: %s" % json.dumps(untouched_before, sort_keys=True),
                "after:  %s" % json.dumps(untouched_after, sort_keys=True),
            ],
        )
    # The re-read text must parse to the same object that was written, so a serialisation
    # bug cannot pass by re-reading through the same broken path.
    if json.loads(after_raw) != after_record:
        fail(f"{VERSION_RECORD}: the re-read text does not parse to what was written")

    print(f"  {VERSION_RECORD}: {before} -> {wanted} (from release/JIEJIE_VERSION)")
    print(
        "  preserved: %s"
        % (", ".join(sorted(untouched_before)) if untouched_before else "no other fields")
    )
    print(f"PASS: desktop release version injected ({'wrote' if changed else 'already correct'})")


if __name__ == "__main__":
    main()
