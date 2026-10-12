#!/usr/bin/env python3
"""Behavioural tests for apply-desktop-branding-overlay.py's required --version.

Usage: test-desktop-overlay-version.py

Exit: 0 = every case behaved as required, 1 = a case did not.

# What this protects

The Jiejiebox release version used to have three carriers - release/JIEJIE_VERSION, a
literal in the overlay and CORE_VERSION in the Windows workflow - and no authority. They
drifted, and the v0.1.6 installer was built from a working tree that was never committed:
dispatching the desktop workflow against that tag fails gate 1 and gate 2 today. The
version is now an argument, and this suite is what keeps it an argument.

# Why the fixture is synthetic

The real client is a submodule of a submodule and is empty in a clean checkout, so a test
that needed it could not run in CI. The fixture below carries only the files the overlay
reads or writes, with the pristine upstream `version.json` (`1.14.2`) and the anchors the
rules match. That is enough to prove the version contract - which is what this file is for
- and the rule set as a whole is exercised against the real client by the Windows workflow
itself.

A fixture that drifts from the pinned client shows up as a rule reporting "expected 1 ...,
found 0", which is a failure here rather than a silent pass.
"""
import importlib.util
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
OVERLAY = os.path.join(HERE, "apply-desktop-branding-overlay.py")

passed = 0
failed = 0


def report(name, ok, detail=""):
    global passed, failed
    if ok:
        passed += 1
        print("PASS  %-62s %s" % (name, detail))
    else:
        failed += 1
        print("FAIL  %-62s %s" % (name, detail))


def load_overlay():
    """Import the overlay as a module so its rule table can be read, not copied."""
    spec = importlib.util.spec_from_file_location("desktop_overlay_under_test", OVERLAY)
    module = importlib.util.module_from_spec(spec)
    saved = sys.argv
    sys.argv = ["apply-desktop-branding-overlay.py"]
    try:
        spec.loader.exec_module(module)
    finally:
        sys.argv = saved
    return module


def fixture_files(overlay):
    """A client tree containing exactly the `old` text of every static rule.

    Derived from the rule table rather than hand-written, so a rule whose anchor changes
    cannot leave the fixture behind: a stale anchor shows up here as the rule reporting
    "expected 1 ..., found 0", which is a failure rather than a silent pass.

    Each anchor is repeated `rule.expected` times, because a rule with `expected=2` is
    asserting that the pinned client contains it twice; a fixture carrying it once would
    fail on the count rather than on the thing being tested.
    """
    files = {}
    for rule in overlay.RULES:
        files.setdefault(rule.path, [])
        if rule.kind == "block":
            for _ in range(rule.expected):
                files[rule.path].append(rule.old)
            continue
        # A line rule matches the STRIPPED text of one physical line, and `anchor`
        # additionally requires the nearest PRECEDING non-blank line to be exactly that
        # text. The anchor is therefore emitted before EACH repeat: emitting it once before
        # the group leaves every repeat but the first preceded by another `old` line, which
        # is not the anchor and does not match.
        #
        # A rule whose anchor IS its old text is already satisfied by consecutive repeats.
        for _ in range(rule.expected):
            if rule.anchor is not None and rule.anchor != rule.old:
                files[rule.path].append(rule.anchor + "\n")
            files[rule.path].append(rule.old + "\n")
    for path, needle, _why in overlay.PRESERVED:
        files.setdefault(path, [])
        files[path].append(needle + "\n")
    # The pristine upstream version.json, which the dynamic rule anchors on. Deliberately
    # NOT this fork's version: the two identities are separate. Any line rule that also
    # names version.json is dropped, because the dynamic rule is the only one that may
    # touch this file and its pristine content is what the anchor is matched against.
    files.pop("version.json", None)
    files["version.json"] = [
        '{\n  "version": "%s",\n  "go_version": "go1.26.8"\n}\n' % overlay.UPSTREAM_DESKTOP_VERSION
    ]
    return {path: "".join(chunks) for path, chunks in files.items()}


def make_client(root, overlay):
    for rel, text in fixture_files(overlay).items():
        path = os.path.join(root, rel)
        os.makedirs(os.path.dirname(path) or root, exist_ok=True)
        with open(path, "w", encoding="utf-8", newline="\n") as fh:
            fh.write(text)
    return root


def run(client, *args):
    proc = subprocess.run([sys.executable, OVERLAY, client, *args],
                          capture_output=True, text=True)
    return proc.returncode, proc.stdout + proc.stderr


def version_of(client):
    with open(os.path.join(client, "version.json"), encoding="utf-8") as fh:
        return json.load(fh)["version"]


def main():
    if not os.path.isfile(OVERLAY):
        print("FAIL: overlay not found at %s" % OVERLAY, file=sys.stderr)
        return 1

    overlay = load_overlay()
    upstream = overlay.UPSTREAM_DESKTOP_VERSION

    work = tempfile.mkdtemp(prefix="desktop-overlay-version-")
    try:
        def fresh(case):
            d = os.path.join(work, case)
            if os.path.exists(d):
                shutil.rmtree(d)
            return make_client(d, overlay)

        # --- the happy path --------------------------------------------------
        d = fresh("happy")
        rc, out = run(d, "--version", "0.1.6")
        report("--version 0.1.6 -> version.json is exactly 0.1.6",
               rc == 0 and version_of(d) == "0.1.6",
               "rc=%d version=%s" % (rc, version_of(d)))

        rc2, _ = run(d, "--version", "0.1.6")
        report("re-running the overlay is idempotent",
               rc2 == 0 and version_of(d) == "0.1.6", "rc=%d" % rc2)

        # The installer name is derived from the same value, so there is no second
        # constant to keep in step.
        with open(os.path.join(d, "scripts", "package.ts"), encoding="utf-8") as fh:
            pkg = fh.read()
        report("installer name derives from {version}, no second constant",
               "Jiejiebox-v\\${version}-windows-" in pkg, "")

        # --- the version argument is required and validated -------------------
        d = fresh("missing")
        rc, _ = run(d)
        report("missing --version -> FAIL", rc != 0, "rc=%d" % rc)

        d = fresh("empty")
        rc, _ = run(d, "--version", "")
        report("empty --version -> FAIL", rc != 0, "rc=%d" % rc)

        for bad in ["latest", "0.1", "v0.1.6", "release/JIEJIE_VERSION",
                    "0.1.6 extra", "../0.1.6", "0.1.6/../../etc", "1.2.3.4"]:
            d = fresh("bad")
            rc, _ = run(d, "--version", bad)
            report("illegal --version %-22r -> FAIL" % bad, rc != 0, "rc=%d" % rc)
            # A refused run must not have touched anything: a half-written version.json
            # would be the drift this whole change exists to remove.
            report("  and it wrote nothing", version_of(d) == upstream,
                   "version=%s" % version_of(d))

        # --- versions that ARE versions are written verbatim ------------------
        for good, want in [("0.2.0-rc1", "0.2.0-rc1"), (" 0.1.6 ", "0.1.6"),
                           ("1.0.0+build.5", "1.0.0+build.5")]:
            d = fresh("good")
            rc, _ = run(d, "--version", good)
            report("--version %-14r -> %s" % (good, want),
                   rc == 0 and version_of(d) == want,
                   "rc=%d version=%s" % (rc, version_of(d)))

        # --- the upstream anchor must still be found --------------------------
        # If the pinned client's version.json is not the pristine value, the overlay must
        # refuse rather than skip the rule: skipping is how a release ships an app that
        # believes it is another version.
        d = fresh("anchor")
        with open(os.path.join(d, "version.json"), "w", encoding="utf-8") as fh:
            fh.write('{\n  "version": "9.9.9",\n  "go_version": "go1.26.8"\n}\n')
        rc, _ = run(d, "--version", "0.1.6")
        report("an upstream version mismatch fails closed, writing nothing",
               rc != 0 and version_of(d) == "9.9.9", "rc=%d" % rc)

        # --- no release literal survives as data in the script ----------------
        with open(OVERLAY, encoding="utf-8") as fh:
            script = fh.read()
        code = "\n".join(line for line in script.splitlines()
                         if not line.lstrip().startswith("#"))
        report("no 0.1.x release literal remains as data in the overlay",
               not re.search(r'["\']0\.1\.\d+["\']', code))
        report("the upstream anchor is a named constant, not an inline literal",
               'UPSTREAM_DESKTOP_VERSION = "%s"' % upstream in script)
    finally:
        shutil.rmtree(work, ignore_errors=True)

    print()
    print("desktop overlay version: %d passed, %d failed" % (passed, failed))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
