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
APPLE = os.path.join(WF, "client-apple.yml")
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
    # P0-B: the tooling revision is an exact identity, and it is the SAME identity as the
    # core. A floating `release_branch` used to be accepted here with a `testing` default,
    # which let a dispatch build one commit's product with another commit's scripts.
    report("desktop: tooling_sha is a required input",
           ins.get("tooling_sha", {}).get("required") is True)
    report("desktop: no floating release_branch input survives",
           "release_branch" not in ins and "RELEASE_BRANCH" not in text)
    report("desktop: no required input carries a default",
           all("default" not in ins[name]
               for name in ("core_sha", "core_version", "tooling_sha")))

    env = doc.get("env") or {}
    report("desktop: CORE_SHA comes from the input",
           "inputs.core_sha" in str(env.get("CORE_SHA", "")))
    report("desktop: CORE_VERSION comes from the input",
           "inputs.core_version" in str(env.get("CORE_VERSION", "")))
    report("desktop: TOOLING_SHA comes from the input",
           "inputs.tooling_sha" in str(env.get("TOOLING_SHA", "")))
    report("desktop: permissions stay read-only",
           (doc.get("permissions") or {}).get("contents") == "read")
    report("desktop: no stale 0.1.5 literal remains as data",
           '"0.1.5"' not in text and "'0.1.5'" not in text)

    # The tooling checkout must be pinned to the exact revision, never to a branch. A
    # `ref: env.RELEASE_BRANCH`-style checkout is the defect this pair of checks exists for.
    report("desktop: the tooling checkout is pinned to TOOLING_SHA",
           "ref: ${{ env.TOOLING_SHA }}" in text)

    for needle, label in [
        ('--version "$CORE_VERSION"', "the version stamp is invoked with an explicit version"),
        ('--tooling-sha "$TOOLING_SHA"', "the identity checker is given the tooling revision"),
        ('check-desktop-source-identity.sh', "the identity checker is called, not re-implemented"),
        ("--remote origin", "the identity checker is allowed to fetch the release tag"),
        ("release/JIEJIE_VERSION", "release/JIEJIE_VERSION is proved to agree"),
        ("$CLIENT_DIR/version.json", "the stamped version.json is re-read"),
        ('--expect-core-sha "$CORE_SHA"', "daemon provenance is checked against the core"),
        ('--expect-version "$CORE_VERSION"', "daemon provenance is checked against the version"),
    ]:
        report("desktop: %s" % label, needle in text)

    # The tag-to-commit proof moved OUT of this file and into the checker, so asserting the
    # string here would assert that the workflow re-implements it - the opposite of what is
    # wanted. What is asserted instead is that the helper which owns the proof still contains
    # it, so the proof cannot be lost by deleting it from both places.
    identity = os.path.join(HERE, "check-desktop-source-identity.sh")
    if os.path.isfile(identity):
        with open(identity, encoding="utf-8") as fh:
            helper = fh.read()
        report("desktop: the identity checker proves the tag names core_sha",
               "refs/tags/v$version" in helper and "tag_sha" in helper
               and '[ "$tag_sha" = "$core_sha" ]' in helper)
        report("desktop: the identity checker enforces tooling == core",
               '[ "$tooling_sha" != "$core_sha" ]' in helper)
    else:
        report("desktop: the identity checker exists", False, "missing %s" % identity)


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
    report("verify workflow: runs the desktop release-version suite",
           "test-desktop-release-version.py" in body)
    report("verify workflow: runs the desktop source-identity suite",
           "test-desktop-source-identity.sh" in body)
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


def check_coverage_semantics():
    """A gate that claims coverage must require a PASS, not merely a reported result.

    `grep -cE '^--- (PASS|FAIL|SKIP):'` counts tests that REPORTED, so an all-SKIP run satisfies it
    - which is how the reference suite could print "PASS: reference suite (N differential tests)"
    where every one of those N was a SKIP. The rule this asserts: no workflow may treat that count
    as evidence of execution, and the Linux workflow must use the checking script.
    """
    with open(LINUX, encoding="utf-8") as fh:
        linux_code = code_only(fh.read())
    report("linux workflow no longer counts reported tests as coverage",
           "'^--- (PASS|FAIL|SKIP):'" not in linux_code)
    report("linux workflow requires executed coverage",
           "require-executed-coverage.sh" in linux_code)

    # The same pattern must not reappear anywhere else as an execution claim.
    offenders = []
    for name in sorted(os.listdir(WF)):
        if not name.endswith(".yml"):
            continue
        with open(os.path.join(WF, name), encoding="utf-8") as fh:
            text = code_only(fh.read())
        if "'^--- (PASS|FAIL|SKIP):'" not in text:
            continue
        # interop-xray.yml counts the same way but ALSO requires PASS > 0, which is the correct
        # shape: the count there is a report, not the gate.
        if "grep -cE '^--- PASS:'" in text:
            continue
        offenders.append(name)
    report("no workflow treats reported-test count as coverage", not offenders, str(offenders))


def check_audit_sha_pin():
    """The runtime audit's tooling must come from the audited build's own commit.

    AUDITED_ARTIFACT_SHA == AUDIT_TOOLING_SHA. The earlier revision checked the tooling out at a
    BRANCH and merely printed when that branch was ahead of the build, which allows artifacts from
    commit A to be tested by scripts from commit B. That is silent in the direction that matters: a
    script made more tolerant keeps passing artifacts it no longer describes.

    These assertions exist so the pin cannot be relaxed back to a branch by a later edit.
    """
    if not os.path.isfile(AUDIT):
        report("runtime audit workflow exists", False, AUDIT)
        return
    with open(AUDIT, encoding="utf-8") as fh:
        raw = fh.read()
    audit = code_only(raw)

    # The tooling checkout is pinned to the audited SHA, not to a branch.
    report("audit tooling is checked out at the audited SHA",
           "ref: ${{ steps.audited.outputs.tooling_ref }}" in audit)
    report("audit tooling is NOT checked out at a branch",
           "steps.build.outputs.release_branch" not in audit)

    # The audited SHA is derived from workflow_run.head_sha / the dispatch input, and both paths
    # produce one normalised value.
    report("audit normalises the audited SHA from both triggers",
           "github.event.workflow_run.head_sha" in audit and "inputs.core_sha" in audit)

    # The assertion is present and it throws rather than warning.
    report("audit asserts AUDITED_BUILD_SHA == AUDIT_TOOLING_SHA",
           "AUDITED_BUILD_SHA" in audit and "AUDIT_TOOLING_SHA" in audit)
    report("the SHA mismatch is a hard failure, not a warning",
           "AUDITED_BUILD_SHA ($auditedSha) != AUDIT_TOOLING_SHA" in audit)

    # Every value the closure report has to carry is emitted.
    for value in ("AUDITED_BUILD_RUN_ID", "AUDITED_BUILD_RUN_NUMBER", "AUDITED_BUILD_SHA",
                  "AUDIT_TOOLING_SHA", "AUDITED_VERSION", "INSTALLER_ARTIFACT_NAME",
                  "UNPACKED_ARTIFACT_NAME"):
        report("audit emits %s" % value, value in audit)

    # Version metadata comes from the audited SHA, and a dispatched core_version must AGREE with it
    # rather than override it.
    report("version is read from the pinned checkout",
           "tooling/release/JIEJIE_VERSION" in audit)
    report("a dispatched core_version is verified against the audited SHA",
           "disagrees with release/JIEJIE_VERSION" in audit)

    # The diagnostic override is opt-in, empty by default, and announced.
    report("the tooling override defaults to empty",
           re.search(r"tooling_ref:.*\n(?:.*\n)*?\s+default: ''", audit) is not None)
    report("the tooling override is marked NON-RELEASE DIAGNOSTIC",
           "NON-RELEASE DIAGNOSTIC" in audit)

    # The selected run's own head_sha is asserted, not assumed from the query parameter.
    report("the selected build run's SHA is re-asserted",
           "not the audited SHA" in audit)


def check_step_output_ids():
    """Every `steps.<id>.outputs.*` reference must name a step id that EXISTS in that file.

    The first flight found this the hard way. The download step wrote `installer_path` and
    `unpacked_dir` but declared no `id`, so `steps.build.outputs.unpacked_dir` resolved to the empty
    string and gate 5 looked for the daemon at "/resources/daemon/sing-box-daemon.exe". It reported
    "the daemon does not exist", which reads like a product failure and was purely plumbing.

    GitHub does not validate this at parse time - an unknown step id is simply empty - so it has to
    be asserted here.
    """
    for label, path in (("runtime audit", AUDIT), ("desktop installer", DESKTOP),
                        ("release", RELEASE), ("verify", VERIFY)):
        if not os.path.isfile(path):
            report("%s exists for step-id check" % label, False, path)
            continue
        with open(path, encoding="utf-8") as fh:
            text = fh.read()

        declared = set(re.findall(r"^\s+id:\s*([A-Za-z0-9_-]+)\s*$", text, re.M))
        referenced = set(re.findall(r"steps\.([A-Za-z0-9_-]+)\.outputs\.", text))
        unknown = sorted(referenced - declared)
        report("%s: every steps.<id>.outputs reference names a declared id" % label,
               not unknown, "unknown=%s declared=%s" % (unknown, sorted(declared)))

        # And the writer side: a step that emits to GITHUB_OUTPUT must be readable by id, or its
        # values are unreachable.
        for match in re.finditer(
                r"^      - name: (.*)\n((?:.*\n)*?)(?=^      - |\Z)", text, re.M):
            body = match.group(2)
            if "GITHUB_OUTPUT" not in body:
                continue
            if re.search(r"^\s+id:\s*[A-Za-z0-9_-]+\s*$", body, re.M):
                continue
            # A step that writes outputs for later steps needs an id. Steps that only write to
            # GITHUB_STEP_SUMMARY are not in scope.
            if "GITHUB_STEP_SUMMARY" in body and "GITHUB_OUTPUT" not in body:
                continue
            report("%s: output-writing step %r declares an id" % (label, match.group(1).strip()),
                   False, "writes GITHUB_OUTPUT with no id")


def check_apple_resolve_once():
    """One workflow run resolves ONE Apple client SHA, and every job consumes it.

    libbox, ios and macos run on SEPARATE runners with separate workspaces, so the frozen state
    file that keeps one shell consistent cannot keep one WORKFLOW consistent. Without a resolver
    job whose output the others read, this sequence is possible:

        libbox resolves main -> A
        someone pushes  main -> B
        ios resolves    main -> B

    and one release carries two Apple client commits. These assertions exist so the fix cannot be
    undone back to per-job resolution.
    """
    if not os.path.isfile(APPLE):
        report("apple workflow exists", False, APPLE)
        return
    with open(APPLE, encoding="utf-8") as fh:
        raw = fh.read()
    apple = code_only(raw)

    report("apple workflow has a resolve-apple-source job", "resolve-apple-source:" in apple)
    report("the resolver publishes apple_client_sha",
           "apple_client_sha: ${{ steps.resolve.outputs.apple_client_sha }}" in apple)

    # Consumers depend on it and READ it.
    for consumer in ("libbox", "ios", "macos"):
        report("%s needs the resolver" % consumer,
               re.search(r"^  %s:\n(?:.*\n)*?    needs:.*resolve-apple-source" % consumer,
                         apple, re.M) is not None)
    report("libbox reads the resolved SHA",
           "needs.resolve-apple-source.outputs.apple_client_sha" in apple)

    # And none of them may resolve the branch tip again. `checkout ios|macos` is allowed - it
    # consumes the seeded state file - but `resolve` re-asks the remote.
    offenders = []
    for match in re.finditer(r"^  ([a-z][a-z-]*):\n((?:.*\n)*?)(?=^  [a-z]|\Z)", apple, re.M):
        name, body = match.group(1), match.group(2)
        if name == "resolve-apple-source":
            continue
        if "apple-client-source.sh resolve" in body:
            offenders.append(name)
    report("no job re-resolves the Apple source", not offenders, str(offenders))

    # Only the resolver may ask the remote for a branch tip.
    ls_remote_jobs = []
    for match in re.finditer(r"^  ([a-z][a-z-]*):\n((?:.*\n)*?)(?=^  [a-z]|\Z)", apple, re.M):
        name, body = match.group(1), match.group(2)
        if "ls-remote" in body and name != "resolve-apple-source":
            ls_remote_jobs.append(name)
    report("only the resolver consults the remote", not ls_remote_jobs, str(ls_remote_jobs))

    # The gitlink is reported, not required, unless the operator asks for the old behaviour.
    report("the gitlink is no longer a hard equality by default",
           "APPLE_CLIENT_REQUIRE_GITLINK" in apple)


def main():
    for path in (RELEASE, DESKTOP, VERIFY, AUDIT, LINUX, MOBILE, APPLE):
        if not os.path.isfile(path):
            print("FAIL: missing %s" % path, file=sys.stderr)
            return 1
    check_release()
    print()
    check_desktop()
    print()
    check_runtime_audit_split()
    print()
    check_audit_sha_pin()
    print()
    check_step_output_ids()
    print()
    check_apple_resolve_once()
    print()
    check_mobile_contract()
    print()
    check_coverage_semantics()
    print()
    check_verify()
    print()
    print("release workflow structure: %d passed, %d failed" % (passed, failed))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
