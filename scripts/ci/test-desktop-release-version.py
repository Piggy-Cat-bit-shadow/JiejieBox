#!/usr/bin/env python3
"""Behavioural tests for the desktop release-version injection contract.

Usage: test-desktop-release-version.py

Exit: 0 = every case behaved as required, 1 = a case did not.

# What this protects

The Jiejiebox release version used to have three carriers - release/JIEJIE_VERSION, a
literal in the branding overlay and CORE_VERSION in the Windows workflow - and no
authority. They drifted, and the v0.1.6 installer was built from a working tree that was
never committed. The version is now an argument, the branding overlay is retired, and this
suite is what keeps the remaining contract honest: one authority, one field written, one
field changed.

It replaces `test-desktop-overlay-version.py`, which tested the same version argument
through the 23-rule overlay. Those rules now live in the pinned fork, so a test that needed
them would be testing a file the build no longer runs.

# Why the fixture is synthetic

The real client is a submodule of a submodule. It is present in a developer checkout but
empty in a clean one, and the injector deliberately needs no repository context - so the
fixture below is a bare version.json and nothing else. That is the whole input surface, and
keeping it synthetic is what lets this run in CI without initialising two submodules.

# What it does NOT test, and where that is tested

The source-identity half of the contract - the client is the revision the parent commit
records, and the dashboard is the revision the client records - needs git and a
superproject, so it lives in prepare-desktop-client.sh and is exercised by the Windows
workflow against the real submodules. This suite covers the half that can be proved from a
fixture, and the two halves are named in the same place (`scripts/ci/README` style comments
in the script) so neither is mistaken for the other.
"""
import ast
import importlib.util
import json
import os
import re
import shutil
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
INJECTOR = os.path.join(HERE, "inject-desktop-release-version.py")

# The version the fork's own version.json carries. Deliberately NOT this fork's core
# version: the two identities are independent, and a suite that assumed they matched would
# fail the day the fork bumps its own client version - which is a legitimate change.
FORK_BASELINE_VERSION = "0.1.5"
GO_VERSION = "go1.26.8"

passed = 0
failed = 0


def report(name, ok, detail=""):
    global passed, failed
    if ok:
        passed += 1
        print("PASS  %-66s %s" % (name, detail))
    else:
        failed += 1
        print("FAIL  %-66s %s" % (name, detail))


def load_injector():
    spec = importlib.util.spec_from_file_location("desktop_version_injector", INJECTOR)
    module = importlib.util.module_from_spec(spec)
    sys.argv = ["inject-desktop-release-version.py"]
    spec.loader.exec_module(module)
    return module


def record_text(version=FORK_BASELINE_VERSION, go_version=GO_VERSION, extra=None):
    body = {'version': version}
    if go_version is not None:
        body['go_version'] = go_version
    for key, value in (extra or {}).items():
        body[key] = value
    return json.dumps(body, indent=2) + "\n"


def make_client(root, text):
    os.makedirs(root, exist_ok=True)
    with open(os.path.join(root, "version.json"), "w", encoding="utf-8", newline="\n") as fh:
        fh.write(text)
    return root


def run(client, *args):
    proc = subprocess.run([sys.executable, INJECTOR, client, *args],
                          capture_output=True, text=True)
    return proc.returncode, proc.stdout + proc.stderr


def read_record(client):
    path = os.path.join(client, "version.json")
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


def read_text(client):
    path = os.path.join(client, "version.json")
    with open(path, encoding="utf-8", newline="") as fh:
        return fh.read()


def split_docstring(source):
    """The module docstring and the rest of the source.

    Sliced by the AST rather than by a regex, because the docstring is full of the very
    tokens these checks search for - `1.14.2`, `apply-desktop-branding-overlay.py` - and a
    pattern that tried to find its end would stop at the first `\"\"\"` inside it.
    """
    doc = ast.get_docstring(ast.parse(source), clean=False) or ""
    return doc, source[source.index(doc) + len(doc):] if doc else source


def main():
    if not os.path.isfile(INJECTOR):
        print("FAIL: injector not found at %s" % INJECTOR, file=sys.stderr)
        return 1

    injector = load_injector()
    work = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_tmp-version-contract")
    shutil.rmtree(work, ignore_errors=True)
    try:
        def fresh(case, text=None):
            d = os.path.join(work, case)
            shutil.rmtree(d, ignore_errors=True)
            return make_client(d, record_text() if text is None else text)

        # --- the happy path -------------------------------------------------
        d = fresh("happy")
        rc, out = run(d, "--version", "0.1.7")
        report("valid CORE_VERSION -> version is exactly that value",
               rc == 0 and read_record(d)["version"] == "0.1.7",
               "rc=%d version=%s" % (rc, read_record(d).get("version")))

        report("  go_version is preserved", read_record(d)["go_version"] == GO_VERSION,
               str(read_record(d).get("go_version")))

        # The stamp must not acquire a second carrier: the installer name is derived from
        # the same value inside the fork, so there is no constant to keep in step here.
        rc2, _ = run(d, "--version", "0.1.7")
        report("re-running the same version is idempotent",
               rc2 == 0 and read_record(d)["version"] == "0.1.7", "rc=%d" % rc2)

        # --- the source record may carry any valid baseline ------------------
        # This is the regression that the retired overlay had backwards: it anchored on the
        # upstream literal 1.14.2, so the fork's own 0.1.5 was a hard failure. The baseline
        # is allowed to move; it is only required to be a version.
        for baseline in [FORK_BASELINE_VERSION, "0.1.7", "1.14.2", "0.2.0-rc1", "10.20.30"]:
            d = fresh("baseline")
            make_client(d, record_text(version=baseline))
            rc, _ = run(d, "--version", "0.1.7")
            report("baseline %-12r -> injected 0.1.7" % baseline,
                   rc == 0 and read_record(d)["version"] == "0.1.7", "rc=%d" % rc)

        # --- other fields survive -------------------------------------------
        d = fresh("extra")
        make_client(d, record_text(extra={"channel": "stable", "build": {"id": 7}}))
        before = read_record(d)
        rc, _ = run(d, "--version", "0.1.7")
        after = read_record(d)
        report("extra top-level fields survive verbatim",
               rc == 0 and {k: v for k, v in after.items() if k != "version"}
               == {k: v for k, v in before.items() if k != "version"},
               json.dumps(after, sort_keys=True))

        d = fresh("no-go-version")
        make_client(d, record_text(go_version=None))
        rc, _ = run(d, "--version", "0.1.7")
        report("a record without go_version is still stamped",
               rc == 0 and read_record(d)["version"] == "0.1.7"
               and "go_version" not in read_record(d), "rc=%d" % rc)

        # --- the file's own shape is preserved -------------------------------
        d = fresh("shape")
        make_client(d, '{\n    "version": "0.1.5",\n    "go_version": "%s"\n}\n' % GO_VERSION)
        rc, _ = run(d, "--version", "0.1.7")
        text = read_text(d)
        report("4-space indentation and trailing newline are preserved",
               rc == 0 and '\n    "go_version"' in text and text.endswith("}\n"),
               repr(text[:40]))

        d = fresh("no-final-newline")
        make_client(d, '{"version": "0.1.5", "go_version": "%s"}' % GO_VERSION)
        rc, _ = run(d, "--version", "0.1.7")
        report("an absent final newline is not added",
               rc == 0 and not read_text(d).endswith("\n"), repr(read_text(d)[-12:]))

        # --- malformed sources fail closed -----------------------------------
        # A directory that exists but holds no record: created directly rather than through
        # make_client, because make_client always writes one.
        d = os.path.join(work, "missing-file")
        os.makedirs(d, exist_ok=True)
        rc, _ = run(d, "--version", "0.1.7")
        report("missing version.json -> FAIL", rc != 0, "rc=%d" % rc)

        d = fresh("invalid-json")
        make_client(d, '{\n  "version": "0.1.5",\n')
        before = read_text(d)
        rc, _ = run(d, "--version", "0.1.7")
        report("invalid JSON -> FAIL", rc != 0, "rc=%d" % rc)
        report("  and it wrote nothing", read_text(d) == before)

        d = fresh("not-an-object")
        make_client(d, '["0.1.5"]\n')
        rc, _ = run(d, "--version", "0.1.7")
        report("a JSON array -> FAIL", rc != 0, "rc=%d" % rc)

        d = fresh("no-version-key")
        make_client(d, '{\n  "go_version": "%s"\n}\n' % GO_VERSION)
        rc, _ = run(d, "--version", "0.1.7")
        report("a record with no version key -> FAIL", rc != 0, "rc=%d" % rc)

        for bad_source in [1, True, None, "", "latest", "v0.1.5", "0.1"]:
            d = fresh("bad-source")
            make_client(d, json.dumps({"version": bad_source}, indent=2) + "\n")
            before = read_text(d)
            rc, _ = run(d, "--version", "0.1.7")
            report("source version %-10r -> FAIL" % bad_source, rc != 0, "rc=%d" % rc)
            report("  and it wrote nothing", read_text(d) == before)

        # --- the argument is required and validated --------------------------
        d = fresh("missing-arg")
        rc, _ = run(d)
        report("missing --version -> FAIL", rc != 0, "rc=%d" % rc)

        d = fresh("empty-arg")
        rc, _ = run(d, "--version", "")
        report("empty --version -> FAIL", rc != 0, "rc=%d" % rc)

        for bad in ["latest", "0.1", "v0.1.7", "release/JIEJIE_VERSION",
                    "0.1.7 extra", "../0.1.7", "0.1.7/../../etc", "1.2.3.4"]:
            d = fresh("bad-arg")
            rc, _ = run(d, "--version", bad)
            report("illegal --version %-22r -> FAIL" % bad, rc != 0, "rc=%d" % rc)
            report("  and it wrote nothing",
                   read_record(d)["version"] == FORK_BASELINE_VERSION,
                   "version=%s" % read_record(d)["version"])

        for good, want in [("0.2.0-rc1", "0.2.0-rc1"), (" 0.1.7 ", "0.1.7"),
                           ("1.0.0+build.5", "1.0.0+build.5")]:
            d = fresh("good-arg")
            rc, _ = run(d, "--version", good)
            report("--version %-14r -> %s" % (good, want),
                   rc == 0 and read_record(d)["version"] == want,
                   "rc=%d version=%s" % (rc, read_record(d)["version"]))

        # --- --check-only reports without writing ----------------------------
        d = fresh("check-only")
        before = read_text(d)
        rc, out = run(d, "--check-only")
        report("--check-only reports the source version and writes nothing",
               rc == 0 and FORK_BASELINE_VERSION in out and read_text(d) == before,
               "rc=%d" % rc)

        d = fresh("both-modes")
        rc, _ = run(d, "--check-only", "--version", "0.1.7")
        report("--check-only with --version -> FAIL", rc != 0, "rc=%d" % rc)

        # --- the retired literal must not come back as data ------------------
        # 1.14.2 was the UPSTREAM client's version, and anchoring on it is what made the
        # fork's own version.json a failure. Re-introducing it as a constant here is the
        # exact regression this check exists to catch.
        with open(INJECTOR, encoding="utf-8") as fh:
            script = fh.read()
        # The module docstring is prose that has to be able to NAME the values it retired -
        # otherwise the file could not explain why it does not use them. So it is excluded,
        # and the assertions apply to every other part of the file.
        with open(INJECTOR, encoding="utf-8") as fh:
            script = fh.read()
        docstring, code = split_docstring(script)
        report("the injector documents what it retired",
               "1.14.2" in docstring and "apply-desktop-branding-overlay" in docstring)
        report("no 1.14.2 upstream anchor survives as data in the injector",
               "1.14.2" not in code)
        report("no 0.1.x release literal remains as data in the injector",
               not re.search(r'["\']0\.1\.\d+["\']', code))
        report("the release-version grammar is a named constant",
               "RELEASE_VERSION_RE" in script)

        # --- the retired overlay must not be referenced as a dependency -------
        report("the injector does not reference the retired overlay by path",
               "apply-desktop-branding-overlay.py" not in code)
        report("the retired upstream anchor constant is gone",
               "UPSTREAM_DESKTOP_VERSION" not in script)
    finally:
        shutil.rmtree(work, ignore_errors=True)

    print()
    print("desktop release-version contract: %d passed, %d failed" % (passed, failed))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
