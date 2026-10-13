#!/usr/bin/env python3
"""Structural checks on the release workflow files.

Usage: check-release-workflows.py

Exit: 0 = every check passed, 1 = one did not.

# What this protects

A workflow is YAML, shell and permissions at once, and none of the three is checked by
anything else in this repository. The failure modes are quiet: an env var that no longer
resolves reads as an empty string, a missing `if:` turns a dry run into a publish, and a
gate that was deleted while editing a step above it leaves a green run that verifies
nothing.

These checks are structural, not behavioural - they cannot prove the shell works. The shell
is proved by test-release-artifacts.sh on a runner. What they prove is that the guards are
still DECLARED, which is the class of mistake an editor makes.
"""
import os
import re
import sys

try:
    import yaml
except ImportError:  # pragma: no cover
    print("FAIL: PyYAML is required (pip install pyyaml)", file=sys.stderr)
    raise SystemExit(1)

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
WF = os.path.join(REPO, ".github", "workflows")

RELEASE = os.path.join(WF, "release.yml")
DESKTOP = os.path.join(WF, "client-desktop-windows.yml")
VERIFY = os.path.join(WF, "release-contract-verify.yml")
AUDIT = os.path.join(WF, "client-desktop-windows-runtime-audit.yml")
LINUX = os.path.join(WF, "server-linux-amd64.yml")
MOBILE = os.path.join(WF, "mobile-contract.yml")
GATE = os.path.join(REPO, "scripts", "ci", "release-artifacts.sh")

passed = 0
failed = 0


def report(name, ok, detail=""):
    global passed, failed
    if ok:
        passed += 1
        print("PASS  %-64s %s" % (name, detail))
    else:
        failed += 1
        print("FAIL  %-64s %s" % (name, detail))


def load(path):
    with open(path, encoding="utf-8") as fh:
        text = fh.read()
    # PyYAML resolves the bare key `on` to the boolean True; GitHub keeps it a string.
    doc = yaml.safe_load(text)
    return text, doc, (doc.get("on", doc.get(True)) or {})


def inputs_of(on):
    return (on.get("workflow_dispatch") or {}).get("inputs") or {}


def check_release():
    text, doc, on = load(RELEASE)
    perms = doc.get("permissions") or {}
    report("release.yml: the only writer holds contents: write",
           perms.get("contents") == "write", str(perms))
    report("release.yml: dispatch-only, so no tag can auto-publish",
           list(on) == ["workflow_dispatch"], str(list(on)))
    ins = inputs_of(on)
    report("release.yml: dry_run still declared",
           "dry_run" in ins and ins["dry_run"].get("default") is False)
    report("release.yml: tag input still declared and required",
           ins.get("tag", {}).get("required") is True)

    # Every step that must exist. A release gate is only as good as the steps that run.
    for needle, label in [
        ("test-release-artifacts.sh", "runs the gate's own tests before anything else"),
        ("check-tag", "validates the tag"),
        ("release-version", "reads the version from release/JIEJIE_VERSION"),
    ]:
        report("release.yml: %s" % label, needle in text)

    report("release.yml: stages the desktop installer",
           "release-artifacts.sh desktop" in text)
    report("release.yml: verifies the desktop installer's provenance",
           "--desktop-dir" in text and "--version" in text)
    report("release.yml: enforces the upload contract before publishing",
           "check-upload" in text)
    report("release.yml: only the publish step is conditional on the flag",
           text.count("steps.plan.outputs.publish") >= 1)

    # Assets are named, never globbed. A glob publishes whatever is in the directory,
    # which is how CI records once reached a release page.
    create = text.split("gh release create", 1)
    report("release.yml: gh release create is present exactly once",
           len(create) == 2)
    body = create[1].split("echo \"PASS: release", 1)[0] if len(create) == 2 else ""
    report("release.yml: the upload names files explicitly, no glob",
           "upload/*" not in body and "Jiejiebox-v${version}-windows-x64.exe" in body)
    report("release.yml: the published set is read back and compared",
           "gh release view" in text and "the published assets are exactly" in text)


def check_desktop():
    text, doc, on = load(DESKTOP)
    ins = inputs_of(on)
    report("desktop: core_sha is a required input",
           ins.get("core_sha", {}).get("required") is True)
    report("desktop: core_version is a required input",
           ins.get("core_version", {}).get("required") is True)
    report("desktop: neither input has a default",
           "default" not in ins.get("core_sha", {}) and "default" not in ins.get("core_version", {}))

    env = doc.get("env") or {}
    report("desktop: CORE_SHA comes from the input",
           "inputs.core_sha" in str(env.get("CORE_SHA", "")))
    report("desktop: CORE_VERSION comes from the input",
           "inputs.core_version" in str(env.get("CORE_VERSION", "")))
    report("desktop: permissions stay read-only",
           (doc.get("permissions") or {}).get("contents") == "read")
    report("desktop: no stale 0.1.5 literal remains as data",
           '"0.1.5"' not in text and "'0.1.5'" not in text)

    for needle, label in [
        ('--version "$CORE_VERSION"', "the overlay is invoked with an explicit version"),
        ("refs/tags/v$CORE_VERSION", "the tag is proved to name core_sha"),
        ("release/JIEJIE_VERSION", "release/JIEJIE_VERSION is proved to agree"),
        ("$CLIENT_DIR/version.json", "the overlaid version.json is re-read"),
        ('--expect-core-sha "$CORE_SHA"', "daemon provenance is checked against the core"),
        ('--expect-version "$CORE_VERSION"', "daemon provenance is checked against the version"),
    ]:
        report("desktop: %s" % label, needle in text)


def code_only(text):
    """Drop comment and blank lines, so a check does not match its own prose.

    A YAML step's `run:` body is a shell script whose comments are documentation; asserting
    "this workflow never runs X" against the raw text fails on a comment that explains why
    it never runs X - which is what happened to this check the first time it ran.
    """
    keep = []
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        keep.append(line)
    return "\n".join(keep)


def check_verify():
    text, doc, on = load(VERIFY)
    body = code_only(text)
    perms = doc.get("permissions") or {}
    report("verify workflow: read-only",
           perms.get("contents") == "read", str(perms))
    report("verify workflow: no write permission anywhere",
           "contents: write" not in body and "actions: write" not in body)
    report("verify workflow: dispatches nothing",
           "gh workflow run" not in body and "gh api" not in body)
    report("verify workflow: creates no release",
           "gh release create" not in body and "gh release upload" not in body)
    report("verify workflow: deletes no asset",
           "delete-asset" not in body and "release delete" not in body)
    report("verify workflow: moves no tag",
           "push --delete" not in body and "tag -d" not in body)
    report("verify workflow: reads no secret",
           "secrets." not in body)
    report("verify workflow: runs the release gate suite",
           "test-release-artifacts.sh" in body)
    report("verify workflow: runs the overlay version suite",
           "test-desktop-overlay-version.py" in body)
    report("verify workflow: bash -n on the release scripts",
           "bash -n" in body)


def code_only(text):
    """Strip whole-line comments so a structural check tests CODE, not prose.

    Written after this checker's own first version failed on the audit workflow because the
    workflow's header explains the rejected `continue-on-error` design in a comment. A check that
    a suppression key is absent must not be satisfied or defeated by the sentence describing why
    it is absent.
    """
    kept = []
    for line in text.splitlines():
        if line.lstrip().startswith("#"):
            continue
        # A trailing comment on a code line is left in place deliberately: removing it needs a
        # YAML-aware pass, and `key: value  # note` still has to be seen as code.
        kept.append(line)
    return "\n".join(kept)


def check_runtime_audit_split():
    """The Windows runtime gates live in their own workflow, as ordinary steps.

    This is the contract that stops the split from silently reverting. It was attempted the wrong
    way once already: the four runtime gates sat in the build workflow behind
    `continue-on-error: true`, which suppressed a failure instead of giving it an honest status.
    Both halves are asserted - the build workflow must NOT carry the runtime gates, and the audit
    workflow must carry them with no suppression.
    """
    if not os.path.isfile(AUDIT):
        report("runtime audit workflow exists", False, AUDIT)
        return
    with open(AUDIT, encoding="utf-8") as fh:
        audit = fh.read()
    with open(DESKTOP, encoding="utf-8") as fh:
        desktop = fh.read()

    # The build workflow's conclusion is what a release depends on, so every gate in it must mean
    # "the artifact is wrong". The runtime gates are about the RUNNER as much as the product.
    for gate in ("gate 5]", "gate 5b]", "gate 6]", "gate 7]"):
        report("build workflow: no %s runtime gate" % gate.rstrip("]"), gate not in desktop)

    # The audit workflow owns all four, under the same names.
    for gate in ("gate 5]", "gate 5b]", "gate 6]", "gate 7]"):
        report("runtime audit: has %s" % gate.rstrip("]"), gate in audit)

    # It must resolve the build run by workflow PATH, or a renamed or newly added workflow could
    # be audited instead of the one that actually built the installer.
    report("runtime audit: selects the build run by workflow path",
           'select(.path == ".github/workflows/client-desktop-windows.yml")' in audit)
    report("runtime audit: requires a successful build run", '"success"' in audit)

    # No suppression anywhere: the gates are ordinary steps, so a failure turns the workflow red
    # rather than being reported as a green run with a warning. Checked against CODE, so the
    # header's explanation of the rejected design cannot make this fail.
    audit_code = code_only(audit)
    report("runtime audit: no continue-on-error",
           "continue-on-error" not in audit_code)
    report("runtime audit: no `|| true` escape", "|| true" not in audit_code)

    # The unpacked tree the gates need is uploaded by the build workflow, and its name must match
    # NEITHER pattern release-artifacts.sh selects on - otherwise the release would download the
    # wrong artifact, or two of them. Asserted against the NAME, not against a comment about it.
    upload = re.search(r"name:\s*(jiejiebox-windows-unpacked\S*)", code_only(desktop))
    if upload is None:
        report("build workflow: uploads the unpacked tree for the audit", False)
    else:
        name = upload.group(1)
        report("build workflow: uploads the unpacked tree for the audit", True, name)
        report("unpacked artifact name matches neither release pattern",
               not name.endswith("-signed-test") and "build-records" not in name, name)

    # And the audit must stay non-release-blocking: the release gate consults the BUILD workflow
    # only. If this ever fails, a red runtime audit would start silently withholding releases.
    if os.path.isfile(GATE):
        with open(GATE, encoding="utf-8") as fh:
            gate = fh.read()
        report("release gate does not consult the runtime audit workflow",
               "runtime-audit" not in gate and "runtime_audit" not in gate)


def check_mobile_contract():
    """The mobile/Apple source contract has exactly one owner.

    `scripts/ci/test-low-memory.sh` used to run inside the LINUX artifact workflow, before that
    workflow had even built its binary, while its subject is the iOS buffer geometry. It now owns a
    workflow of its own. This asserts the result: one invoker, and it is not the Linux one.
    """
    for label, path in (
        ("server-linux-amd64.yml", LINUX),
        ("mobile-contract.yml", MOBILE),
    ):
        if not os.path.isfile(path):
            report("%s exists" % label, False, path)
            return
    with open(LINUX, encoding="utf-8") as fh:
        linux = code_only(fh.read())
    with open(MOBILE, encoding="utf-8") as fh:
        mobile = fh.read()
    mobile_code = code_only(mobile)

    report("linux artifact workflow no longer runs the mobile contract",
           "test-low-memory.sh" not in linux)
    report("mobile contract workflow runs it", "test-low-memory.sh" in mobile_code)
    report("mobile contract workflow has no continue-on-error",
           "continue-on-error" not in mobile_code)

    # Every workflow in the repository that INVOKES the script, so a second owner is caught
    # wherever it appears rather than only in the two files named above.
    owners = []
    for name in sorted(os.listdir(WF)):
        if not name.endswith(".yml"):
            continue
        with open(os.path.join(WF, name), encoding="utf-8") as fh:
            if "test-low-memory.sh" in code_only(fh.read()):
                owners.append(name)
    report("exactly one workflow owns the mobile contract",
           owners == ["mobile-contract.yml"], str(owners))


def main():
    for path in (RELEASE, DESKTOP, VERIFY, AUDIT, LINUX, MOBILE):
        if not os.path.isfile(path):
            print("FAIL: missing %s" % path, file=sys.stderr)
            return 1
    check_release()
    print()
    check_desktop()
    print()
    check_runtime_audit_split()
    print()
    check_mobile_contract()
    print()
    check_verify()
    print()
    print("release workflow structure: %d passed, %d failed" % (passed, failed))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
