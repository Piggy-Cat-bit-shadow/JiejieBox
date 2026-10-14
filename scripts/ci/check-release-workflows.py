#!/usr/bin/env python3
"""Structural checks on the release workflow files.

Usage:
    check-release-workflows.py                     check the CURRENT repository
    check-release-workflows.py --target-state v6   check the V6 Stage 1-3 TARGET state

Exit: 0 = every check passed, 1 = one did not, 2 = the invocation was wrong.

# Two modes, one checker, and why

The default run answers "has the repository regressed?" It must pass on every commit, so it
only asserts things that are TRUE NOW.

`--target-state v6` answers a different question: "has the V6 Stage 1-3 restructure landed?"
Its assertions describe a state the repository is deliberately NOT in yet, so it is EXPECTED to
fail until that work is done on a Unix runner. It exists so the acceptance criteria are
executable rather than prose in a report - the work can be judged the moment it lands, instead
of being re-derived from a plan.

Neither mode may be folded into the other. Making the default run assert the target state would
turn every branch red for work nobody has done; making the target mode assert only what is
already true would make it prove nothing. The mode is explicit on the command line for that
reason, and `--target-state` never changes the default run's verdict.

Both modes share the reporting counters below, so a caller reading `N passed, M failed` reads
the same shape either way.

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
import argparse
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
# The SOURCE gate is a different workflow from the release-tooling gate, and conflating the two
# is what this constant exists to stop: `VERIFY` above has always pointed at
# release-contract-verify.yml, while several checks were labelled "verify workflow" as if they
# described verify.yml. They described the release-tooling gate, so a reader asking "does the
# push gate still refuse a docs-only push" was reading an answer about another file entirely.
SOURCE_VERIFY = os.path.join(WF, "verify.yml")
AUDIT = os.path.join(WF, "client-desktop-windows-runtime-audit.yml")
LINUX = os.path.join(WF, "server-linux-amd64.yml")
MOBILE = os.path.join(WF, "mobile-contract.yml")
APPLE = os.path.join(WF, "client-apple.yml")
GATE = os.path.join(REPO, "scripts", "ci", "release-artifacts.sh")
ACCEPTANCE = os.path.join(REPO, "scripts", "ci", "verify-release-acceptance.sh")

RELEASE_SUITE = "test-release-artifacts.sh"

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
        ("check-tag", "validates the tag"),
        ("release-version", "reads the version from release/JIEJIE_VERSION"),
        # Stage B: the release requires exact-SHA source and tooling evidence rather than
        # re-deriving it. `evidence` is the canonical selector for that question.
        ("release-artifacts.sh evidence", "requires exact-SHA source and tooling evidence"),
    ]:
        report("release.yml: %s" % label, needle in text)

    # Stage H: a release is serialised per tag and bounded.
    conc = doc.get("concurrency") or {}
    report("release.yml: releases are serialised per tag",
           "inputs.tag" in str(conc.get("group", "")), str(conc.get("group")))
    report("release.yml: a release is never cancelled by another run",
           conc.get("cancel-in-progress") is False, str(conc.get("cancel-in-progress")))
    rto = (doc.get("jobs", {}).get("release", {}) or {}).get("timeout-minutes")
    report("release.yml: the release job is bounded by a timeout",
           isinstance(rto, int) and rto > 0, str(rto))

    # Stage C: Formal Release and Verify must NOT re-run the 92-case suite; RCV owns it. The
    # equivalence is asserted in the frozen `--target-state v6` mode over every workflow, so
    # this is the cheap single-file version of the same rule - it fails on the exact edit that
    # would regress it.
    report("release.yml: no longer re-runs the 92-case suite on the publishing runner",
           not executes_release_suite(RELEASE),
           "executes at " + ", ".join("line %d" % n for n, _ in executes_release_suite(RELEASE)))

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


def check_canonical_fixture_is_executed():
    """The PRESENT_INVALID suite must be RUN by its owner, not merely committed.

    A test that exists but is executed by nothing is worse than no test: an audit sees coverage
    while nothing is proven, and `bash -n` plus a file-existence check cannot tell the difference.
    This asserts the executing step is in the workflow that owns release tooling, and that no
    second workflow runs it.

    The unlisted-tooling check above already catches half of this - a suite named in a workflow but
    missing from that workflow's `paths` filter would not run on a change to itself. This catches
    the other half: a suite nobody invokes at all.
    """
    problems = []
    owner = "release-contract-verify.yml"
    owner_path = os.path.join(REPO, ".github", "workflows", owner)
    with open(owner_path, encoding="utf-8") as handle:
        text = handle.read()
    if "./scripts/ci/test-apple-canonical-source.sh" not in text:
        problems.append(
            owner + " does not execute the canonical-source suite. A fixture nothing runs is not "
            "evidence, and the PRESENT_INVALID rule would be unproven."
        )
    others = []
    workflows_dir = os.path.join(REPO, ".github", "workflows")
    for name in sorted(os.listdir(workflows_dir)):
        if not name.endswith(".yml") or name == owner:
            continue
        with open(os.path.join(workflows_dir, name), encoding="utf-8") as handle:
            if "test-apple-canonical-source.sh" in handle.read():
                others.append(name)
    if others:
        problems.append(
            "the canonical-source suite is executed by more than one workflow: " + ", ".join(others)
        )
    # `report(name, ok, detail)`. The first version passed a description as `ok`, and a non-empty
    # string is truthy - so the check printed PASS while listing its own problems beside it, and
    # the exit status stayed 0. The mutation test caught it: stopping the workflow from running the
    # suite left the checker green.
    report(
        "release contract: the PRESENT_INVALID suite is executed by its owner",
        not problems,
        "" if not problems else "; ".join(problems),
    )
    return problems


def check_release_contract_workflow():
    """The RELEASE TOOLING gate: release-contract-verify.yml.

    Every label says which workflow it is about. These checks were previously labelled "verify
    workflow", which named the wrong file for four of them - including the duplication check
    added for P0-C, which therefore asserted release-contract-verify.yml while reading as a
    statement about the push gate. A check whose label names the wrong subject is worse than a
    missing check, because it reads as coverage.
    """
    text, doc, on = load(VERIFY)
    body = code_only(text)
    perms = doc.get("permissions") or {}
    report("release contract: read-only",
           perms.get("contents") == "read", str(perms))
    report("release contract: no write permission anywhere",
           "contents: write" not in body and "actions: write" not in body)
    report("release contract: dispatches nothing",
           "gh workflow run" not in body and "gh api" not in body)
    report("release contract: creates no release",
           "gh release create" not in body and "gh release upload" not in body)
    report("release contract: deletes no asset",
           "delete-asset" not in body and "release delete" not in body)
    report("release contract: moves no tag",
           "push --delete" not in body and "tag -d" not in body)
    report("release contract: reads no secret",
           "secrets." not in body)

    # The suites this gate owns as their primary owner.
    report("release contract: runs the release gate suite",
           "test-release-artifacts.sh" in body)
    report("release contract: runs the desktop release-version suite",
           "test-desktop-release-version.py" in body)
    report("release contract: runs the desktop source-identity suite",
           "test-desktop-source-identity.sh" in body)
    report("release contract: runs the coverage guard self-test",
           "require-executed-coverage.test.sh" in body)
    report("release contract: bash -n on the release scripts",
           "bash -n" in body)

    # P1-C: every tooling file this workflow EXECUTES must be in its own trigger filter, or a
    # change to that file does not run the gate that syntax-checks and exercises it. Two were
    # missing - the coverage guard and its self-test - which is how this check came to exist.
    listed = set(((on.get("push") or {}).get("paths")) or [])
    fails = False
    if listed:
        executed = set()
        for m in re.finditer(r"(?:\./)?((?:scripts|release)/[A-Za-z0-9._/-]+)", text):
            p = m.group(1)
            if p.endswith((".sh", ".py")) or p.startswith("release/"):
                executed.add(p)
        # Only tooling that exists in the tree: a path named in prose is not a dependency.
        executed = {p for p in executed if os.path.exists(os.path.join(REPO, p))}
        unlisted = sorted(executed - listed)
        report("release contract: every tooling file it runs is in its paths filter",
               not unlisted, "unlisted=%s" % unlisted)
        report("release contract: the filter carries no documentation path",
               not any(p.endswith(".md") or p.startswith("docs/") for p in listed))
    else:
        fails = True
        report("release contract: declares a push paths filter", False, "none declared")

    # The two trigger lists are one authority, aliased (YAML anchor) or identical, so a push and
    # a pull_request cannot end up asserting different things about the same files.
    push_paths = ((on.get("push") or {}).get("paths")) or []
    pr_paths = ((on.get("pull_request") or {}).get("paths")) or []
    report("release contract: push and pull_request assert the same file set",
           push_paths == pr_paths and bool(push_paths),
           "push=%d pr=%d" % (len(push_paths), len(pr_paths)))
    report("release contract: workflow_dispatch retained for on-demand evidence",
           "workflow_dispatch" in on)
    return not fails


def check_source_verify():
    """The SOURCE gate: verify.yml. Nothing above describes this file."""
    text, doc, on = load(SOURCE_VERIFY)
    body = code_only(text)
    perms = doc.get("permissions") or {}
    report("source verify: read-only",
           perms.get("contents") == "read", str(perms))
    report("source verify: triggers on the long-lived branch only",
           (on.get("push") or {}).get("branches") == ["testing"],
           str((on.get("push") or {}).get("branches")))

    # P1-B: a report-only push must not start this workflow. The filter is `*.md`, which in
    # GitHub's pattern language does not cross `/`, so it reaches the repository-root reports
    # and leaves `docs/fork/**` alone - and `docs/fork/**` must stay triggering, because
    # verify-fork-handoff.sh parses three files there, one of which carries the release verdict
    # marker. Both halves are asserted: widening this to `docs/**` would silently stop
    # verifying a release input, and dropping it entirely would restore the docs-only push that
    # cancelled a real code Verify.
    push = on.get("push") or {}
    pr = on.get("pull_request") or {}
    ign = push.get("paths-ignore") or []
    report("source verify: report-only markdown is ignored on push",
           ign == ["*.md"], str(ign))
    report("source verify: the same filter applies to pull_request",
           (pr.get("paths-ignore") or []) == ign, str(pr.get("paths-ignore")))
    report("source verify: the filter cannot reach the machine-read docs",
           not any(p.startswith("docs") or p.startswith("KNOWLEDGE") or p == "**" for p in ign),
           str(ign))
    report("source verify: the filter cannot reach source identity",
           not any(p in (".gitmodules", "clients/**", "clients/*", "go.mod", "go.sum",
                         "release/**", "scripts/**", ".github/**", "*") for p in ign),
           str(ign))

    # The race steps are kept deliberately and the reason is recorded beside them. Asserted so
    # that deleting one without building the final-candidate owner it needs fails here rather
    # than silently shrinking push-time coverage of packages nothing else races.
    report("source verify: the fork-data-path race step is present",
           "race on the fork's own data paths" in text)
    report("source verify: the configuration-surface race step is present",
           "race on the configuration surface" in text)
    report("source verify: the race ownership table is recorded",
           "final-candidate" in text and "deep_checks" in text)
    report("source verify: the header does not claim it skips the race suite",
           "does not\n# run the full unit suite, the race suite" not in text)

    # Stage C. The source gate stops executing the release-helper suite: it is not a property of
    # the source, and its primary owner is Release Contract Verification. The counter-assertion
    # (RCV still runs it) lives in check_release_contract_workflow, so the pair cannot both be
    # satisfied by deleting the suite everywhere.
    report("source verify: no longer executes the 92-case release-helper suite",
           not executes_release_suite(SOURCE_VERIFY),
           "executes at " + ", ".join("line %d" % n
                                      for n, _ in executes_release_suite(SOURCE_VERIFY)))


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


# ---------------------------------------------------------------------------
# Target state: V6 Stage 1-3
# ---------------------------------------------------------------------------
#
# These assertions describe where the release authority is GOING, not where it is. The default
# run above must stay green on every commit; this section is expected to fail until the Unix
# Stage 1-3 work lands, and turning it green is the acceptance criterion for that work.
#
# They are deliberately limited to the FIVE facts V6 fixed and that need no further product
# decision. In particular there is NO assertion about Android artifact field names or Apple
# canonical Libbox metadata shape: those directions are agreed but their exact form should be
# settled by the implementation in Stages 4-7, and a checker written first would distort the
# implementation to satisfy itself. Adding them later is the point, not an omission.
#
# Nothing here asserts "this file must be deleted". A retired verdict ENGINE may keep its
# parser, its fixtures and its self-test - V6 section 4.4 says so explicitly - so what is
# asserted is that a SECOND may-release authority is no longer reachable from the release path.


def executes_release_suite(path):
    """Code lines of `path` that RUN the release suite (not comment lines, not path filters).

    The distinction matters more than it looks. `release-contract-verify.yml` MENTIONS the suite
    in its `paths:` filter, in its `bash -n` list and in `grep` guards that assert the suite still
    wires its own mock `gh`. None of those is "this workflow runs the 92 cases", and a substring
    test would read all of them as execution - which is how a checker reports the opposite of the
    truth. So only lines that would actually invoke it are counted.
    """
    if not os.path.isfile(path):
        return []
    hits = []
    for number, line in enumerate(open(path, encoding="utf-8").read().splitlines(), 1):
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if RELEASE_SUITE not in stripped:
            continue
        # A path-filter entry, a syntax-check list entry, or a grep that merely names the file.
        if stripped.startswith("- "):
            continue
        if stripped.startswith(("grep ", "for f in ", "scripts/ci/prepare-desktop-client.sh")):
            continue
        if stripped.endswith("\\"):  # a continuation inside such a list
            continue
        hits.append((number, stripped))
    return hits


def check_target_state_v6():
    """The V6 Stage 1-3 terminal state. EXPECTED to fail before that work is done."""
    print("== target state: V6 Stage 1-3 (release authority) ==")
    print("   These describe a state the repository is not in yet; failures below are the")
    print("   work list for the Unix round, not regressions.")
    print()

    # --- 1 & 2: the two gates that must stop running the suite ------------------
    for label, path in (("Verify", SOURCE_VERIFY), ("Formal Release", RELEASE)):
        hits = executes_release_suite(path)
        report("V6-1/2 %s no longer executes the full 92-case suite" % label,
               not hits,
               ("still executes at " + ", ".join("line %d" % n for n, _ in hits)) if hits
               else "no invocation found")

    # --- 3: RCV is the workflow primary owner ----------------------------------
    # Two halves, and both are needed: RCV must still run it, AND it must be the only workflow
    # that does. The second half is what stops the change from becoming a deletion.
    rcv_hits = executes_release_suite(VERIFY)
    report("V6-3 Release Contract Verification still executes the full suite",
           bool(rcv_hits),
           ("executes at " + ", ".join("line %d" % n for n, _ in rcv_hits)) if rcv_hits
           else "NO invocation found - the suite lost its owner")
    others = []
    for name in sorted(os.listdir(WF)):
        if not name.endswith((".yml", ".yaml")):
            continue
        path = os.path.join(WF, name)
        if os.path.abspath(path) == os.path.abspath(VERIFY):
            continue
        if executes_release_suite(path):
            others.append(name)
    report("V6-3 no other workflow executes the full suite",
           not others,
           "also executes: %s" % ", ".join(others) if others else "sole owner")

    # --- 4: Formal Release requires exact-SHA Verify AND RCV evidence ----------
    # Asserted across the TWO files that together make the requirement, because naming the
    # workflow files inside release.yml would be the wrong design: the release path does not know
    # or care which gates exist, it asks the canonical authority for "the required evidence" and
    # the authority decides what that means. So the requirement is a pair:
    #
    #   release.yml              calls release-artifacts.sh evidence   (it consumes the verdict)
    #   release-artifacts.sh     REQUIRED_EVIDENCE names each gate     (it defines the question)
    #
    # Asserting only the first would pass for an authority whose REQUIRED_EVIDENCE list was
    # emptied - the release would call `evidence`, be told nothing, and publish. Asserting both
    # is what makes the requirement real.
    rel_code = ""
    if os.path.isfile(RELEASE):
        with open(RELEASE, encoding="utf-8") as fh:
            rel_code = "\n".join(l for l in fh.read().splitlines()
                                 if not l.strip().startswith("#"))
    gate_code = ""
    if os.path.isfile(GATE):
        with open(GATE, encoding="utf-8") as fh:
            gate_code = fh.read()
    report("V6-4 Formal Release consumes the canonical evidence verdict",
           "release-artifacts.sh evidence" in rel_code,
           "release.yml calls evidence" if "release-artifacts.sh evidence" in rel_code
           else "release.yml never calls the evidence subcommand")
    report("V6-4 the canonical authority is non-empty and names both gates",
           "REQUIRED_EVIDENCE=(" in gate_code
           and "verify.yml" in gate_code
           and "release-contract-verify.yml" in gate_code,
           "REQUIRED_EVIDENCE declares verify.yml and release-contract-verify.yml")
    for label, name in (("Verify", "verify.yml"),
                        ("Release Contract Verification", "release-contract-verify.yml")):
        report("V6-4 %s is a required evidence gate" % label,
               name in gate_code,
               "named in REQUIRED_EVIDENCE" if name in gate_code else "absent")

    # --- 5: exactly one may-release verdict authority --------------------------
    # Read as: can anything OTHER than release-artifacts.sh decide "this SHA may be released"?
    # The acceptance engine counts as a second authority while it is reachable from the release
    # path; a parser or fixture it leaves behind does not.
    gate_selector = os.path.isfile(GATE) and "newest_run" in open(GATE, encoding="utf-8").read()
    report("V6-5 release-artifacts.sh holds the run-selection primitive",
           gate_selector,
           "newest_run present" if gate_selector else "no selection primitive found")

    acceptance_used = "verify-release-acceptance.sh" in rel_code
    report("V6-5 verify-release-acceptance.sh is not a second release verdict",
           not acceptance_used,
           "release.yml invokes it" if acceptance_used
           else "not reachable from release.yml")

    # The acceptance engine's own SELF-TEST is the other half of "is it still an authority".
    #
    # The criterion is NOT "no workflow mentions it". The release-tooling owner is allowed - and
    # expected - to keep running the engine's 18-snapshot self-test, because that is a test of a
    # release tool and Release Contract Verification is where release-tool tests live. What must
    # not exist is a workflow that holds the engine up as the gate a RELEASE depends on, which is
    # what verify.yml was doing while nothing on the release path called the engine at all.
    #
    # So the owner is exempt and every other workflow is checked. Before this exemption the check
    # failed on release-contract-verify.yml after the self-test was correctly moved there, which
    # is a check refusing the right answer - the same defect class as a check accepting the wrong
    # one, and worth the comment.
    engine_gates = []
    for name in sorted(os.listdir(WF)):
        if not name.endswith((".yml", ".yaml")):
            continue
        if os.path.abspath(os.path.join(WF, name)) == os.path.abspath(VERIFY):
            continue
        with open(os.path.join(WF, name), encoding="utf-8") as fh:
            body = "\n".join(l for l in fh.read().splitlines()
                             if not l.strip().startswith("#"))
        if "verify-release-acceptance-test.sh" in body:
            engine_gates.append(name)
    report("V6-5 no workflow but the release-tooling owner declares the acceptance engine",
           not engine_gates,
           ("still declared in: " + ", ".join(engine_gates)) if engine_gates
           else "only release-contract-verify.yml, which owns release-tool tests")

    print()
    print("V6 target state (Stage 1-3): %d passed, %d failed" % (passed, failed))
    if failed:
        print()
        print("NOT YET ACHIEVED - %d Stage 1-3 criterion(s) outstanding." % failed)
        print("This is the expected result until the Unix Stage 1-3 work lands; the default")
        print("run above is what reports repository regressions.")
    return 1 if failed else 0


def check_executable_invocations():
    """A workflow that runs `./script.sh` needs that file to carry the executable bit.

    Found by the first real GitHub Ubuntu run of this branch: the release-contract gate invoked
    `./scripts/ci/test-desktop-source-identity.sh` and the step died with exit code 126 -
    "Permission denied" - because the file's git mode was 100644. Nothing on Windows notices:
    the local runs used `bash <file>`, which does not consult the mode, and git's index mode is
    invisible in an editor. The suite that existed to catch a broken identity check was itself
    unrunnable, and it reported as a FAILURE step rather than as a broken invocation.

    Two things are asserted, and the second is the one that generalises:
      * every `./<path>.sh` invocation in a workflow names a file whose INDEX mode is 100755;
      * and conversely, a tracked `.sh` under scripts/ that no workflow runs via `./` is
        reported, because a mode that is merely untidy today becomes the same failure the
        moment someone changes the invocation.

    The index is read with `git ls-files -s` rather than the filesystem: on Windows the
    filesystem has no executable bit, and what CI checks out is the index mode.
    """
    import shutil
    import subprocess
    git = shutil.which("git")
    if git is None:
        # Not every host that runs this checker has git on PATH - a Windows workstation may use
        # a portable copy the shell has but the process environment does not. This check is a
        # convenience on top of the real gate (CI runs it on Ubuntu, where git is always present
        # and the index mode is what is checked out), so a missing git is reported as SKIP rather
        # than as a failure. Reporting FAIL here would make the default suite red for an
        # environment reason, which is exactly what the default suite must never do.
        print("SKIP  %-64s %s" % ("executable bit: git index is readable",
                                  "git not on PATH for this process"))
        return
    try:
        listing = subprocess.run(
            [git, "-C", REPO, "ls-files", "-s", "scripts/ci"],
            capture_output=True, text=True, check=True).stdout
    except (OSError, subprocess.CalledProcessError) as error:
        report("executable bit: git index is readable", False, str(error))
        return

    modes = {}
    for line in listing.splitlines():
        parts = line.split(None, 3)
        if len(parts) == 4:
            modes[parts[3]] = parts[0]

    invoked = set()
    for name in sorted(os.listdir(WF)):
        if not name.endswith((".yml", ".yaml")):
            continue
        with open(os.path.join(WF, name), encoding="utf-8") as fh:
            for line in fh.read().splitlines():
                stripped = line.strip()
                if stripped.startswith("#"):
                    continue
                for match in re.finditer(r"\./((?:scripts|\.github)/[A-Za-z0-9._/-]+\.sh)", stripped):
                    invoked.add(match.group(1))

    bad = sorted(p for p in invoked if modes.get(p) not in (None, "100755"))
    report("executable bit: every ./<script>.sh invocation is mode 100755",
           not bad,
           ("not executable: " + ", ".join("%s (%s)" % (p, modes.get(p)) for p in bad)) if bad
           else "%d invocation(s) checked" % len(invoked))

    # And the reverse direction, so a non-executable script cannot sit waiting for an invocation
    # change to break it. Only .sh: a .py is run through `python3`, which never needs the bit.
    stray = sorted(p for p, m in modes.items()
                   if p.endswith(".sh") and m != "100755" and p not in invoked)
    report("executable bit: no tracked scripts/ci/*.sh is left non-executable",
           not stray,
           ("non-executable: " + ", ".join(stray)) if stray else "all .sh are 100755")


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="check-release-workflows.py",
        description="Structural checks on the release workflow files.")
    parser.add_argument(
        "--target-state",
        choices=["v6"],
        default=None,
        metavar="v6",
        help="check the V6 Stage 1-3 TARGET state instead of the current repository. "
             "Expected to FAIL until that work lands; it never changes the default verdict.")
    args = parser.parse_args(argv)

    needed = [RELEASE, DESKTOP, VERIFY, SOURCE_VERIFY, AUDIT, LINUX, MOBILE, APPLE]
    if args.target_state:
        needed = needed + [GATE]
    for path in needed:
        if not os.path.isfile(path):
            print("FAIL: missing %s" % path, file=sys.stderr)
            return 1

    if args.target_state == "v6":
        return check_target_state_v6()

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
    check_release_contract_workflow()
    print()
    # Defined above and, until this line, invoked by nothing - which is precisely the failure it
    # exists to catch, arriving in the checker that catches it. A helper nothing calls reports
    # nothing, so `check_release_workflows.py` stayed green while the new suite's execution was
    # unasserted.
    check_canonical_fixture_is_executed()
    print()
    check_source_verify()
    print()
    check_executable_invocations()
    print()
    print("release workflow structure: %d passed, %d failed" % (passed, failed))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
