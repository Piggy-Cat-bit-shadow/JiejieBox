#!/usr/bin/env python3
"""Release dependency freeze: resolve, compare, pin and prove every source a release consumes.

# The problem this exists for

A release of this fork is built from ELEVEN repositories, and the SHA of each one lived in a
different place:

  * six Go forks + cronet-go's root and `all` modules  - replace directives in go.mod
  * the same module graph for the nested test module   - a second copy in test/go.mod
  * the three clients                                  - gitlinks in the superproject
  * the Desktop client's web UI                        - a gitlink inside ANOTHER repository
  * the Apple source a formal release builds           - release/apple-client-refs.env, which
                                                         resolved `main` at build time

Nothing compared those against the forks' own product branches, and nothing compared them with
each other. The consequence was measured, not hypothetical: v0.1.33 was tagged while
`clients/desktop` still pointed one commit behind the Desktop fix it was supposed to ship, so the
installer carried the old subscription User-Agent - and every build was green, because a green
build only proves the pin is CONSISTENT, never that it is CURRENT.

# What it does about it

`resolve`  reads release/dependencies.json and asks each repository for its tracked branch tip.
`check`    compares that tip with the pin the tree actually builds: identical / behind / ahead /
           diverged / missing. Diverged fails: a fork that is not a fast-forward needs a human
           merge in that fork, and this tool must never paper over it.
`apply`    moves the pins that are behind, for entries whose policy allows it. It computes the
           canonical Go pseudo-version with the toolchain instead of hand-writing one, and it
           refuses to touch `controlled` and `report` entries.
`snapshot` writes release/source-lock.json from the tree AFTER the pins were applied - so the
           lock is derived from what will actually be built, not from what was intended.
`verify`   recomputes those pins from the tree and compares them with the lock. It needs no
           network, so it can run on every push; the release workflow runs it again on the
           candidate commit, after which the tag may only be created at that exact commit.

# What it deliberately does not do

  * It does not merge anything upstream. Integrating a fork is a semantic job with tests; the
    freeze consumes a fork branch that a human already integrated, and fails loudly when one
    has not been.
  * It does not move the cronet-go native platform pins. Those archives come from a Chromium
    toolchain this repository does not have; see release/cronet-provenance.txt.
  * It does not write the Desktop client's nested dashboard gitlink, which lives in another
    repository's tree. It reports the lag so the order stays Dashboard -> Desktop -> kernel.
  * It does not create the release. A tag is created by the workflow only after the candidate
    commit has exact-SHA evidence, and this tool's job is the pin freeze that precedes it.

Usage:
  scripts/ci/release-freeze.py resolve  --out build/resolve.tsv
  scripts/ci/release-freeze.py resolve  --from build/recorded.tsv --out build/resolve.tsv
  scripts/ci/release-freeze.py check    --resolve build/resolve.tsv
  scripts/ci/release-freeze.py apply    --resolve build/resolve.tsv
  scripts/ci/release-freeze.py snapshot --resolve build/resolve.tsv --out release/source-lock.json \
                                        --md build/source-lock.md
  scripts/ci/release-freeze.py verify   --lock release/source-lock.json

Exit status: 0 pass, 1 a gate failed, 2 usage or environment error.
"""

import argparse
import json
import os
import re
import subprocess
import sys

# The three commands this tool drives. The defaults are the plain names, which is what CI and a
# developer shell have on PATH. The environment overrides exist for two real cases: the behaviour
# suite installs fakes so it can run without network (and Windows resolves a bare name to `.exe`
# only, so a fake has to be named explicitly), and a host whose toolchain is outside PATH can
# point at it without changing this file.
GIT = os.environ.get("RELEASE_FREEZE_GIT", "git")
GO = os.environ.get("RELEASE_FREEZE_GO", "go")
GH = os.environ.get("RELEASE_FREEZE_GH", "gh")

SCHEMA = 1
LOCK_PATH = "release/source-lock.json"
MANIFEST_PATH = "release/dependencies.json"
ROOT_MODULE = "go.mod"
TEST_MODULE = "test/go.mod"
VERSION_FILE = "release/JIEJIE_VERSION"

REL_IDENTICAL = "identical"
REL_BEHIND = "behind"          # the pin is an ancestor of the branch tip: an update is available
REL_AHEAD = "ahead"            # the tip is an ancestor of the pin: the fork already integrated it
REL_DIVERGED = "diverged"      # neither: a human must merge, and a release must not proceed
REL_MISSING = "missing"        # the branch does not exist
REL_UNKNOWN = "unknown"        # could not be decided (no network, API failure)
REL_SKIPPED = "skipped"        # not checked, with the reason stated


class FreezeError(Exception):
    """An expected failure: printed as a FAIL line, exit status 1."""


class EnvironmentError_(Exception):
    """A missing tool or unreadable input: exit status 2, so a broken host is not a FAIL."""


def run(args, cwd=None, check=True):
    result = subprocess.run(args, cwd=cwd, capture_output=True, text=True)
    if check and result.returncode != 0:
        raise EnvironmentError_(
            "command failed (%d): %s\n%s" % (result.returncode, " ".join(args), result.stderr.strip()))
    return result


def git(repo, *args, check=True):
    return run([GIT, "-C", repo] + list(args), check=check)


def say(message=""):
    print(message, flush=True)


def load_json(path):
    try:
        with open(path, "r", encoding="utf-8") as handle:
            return json.load(handle)
    except OSError as error:
        raise EnvironmentError_("cannot read %s: %s" % (path, error))


def dump_json(path, data):
    directory = os.path.dirname(path)
    if directory:
        os.makedirs(directory, exist_ok=True)
    with open(path, "w", encoding="utf-8", newline="\n") as handle:
        json.dump(data, handle, indent=2, sort_keys=True)
        handle.write("\n")


# --------------------------------------------------------------------------------------------
# go.mod parsing
# --------------------------------------------------------------------------------------------
# Every replace in this repository is a single-line `replace A => B vX`, but the block form is
# part of the language and a parser that silently ignores it would report a pin as absent - which
# reads as "verifies vacuously". Both forms are parsed for that reason.

REPLACE_LINE = re.compile(r"^replace\s+(?P<module>\S+)\s+=>\s+(?P<target>\S+)(?:\s+(?P<version>\S+))?\s*$")


REPLACE_BODY = re.compile(r"^(?P<module>\S+)\s+=>\s+(?P<target>\S+)(?:\s+(?P<version>\S+))?\s*$")


def parse_replaces(text):
    """Return {module: (target, version)} for every replace directive in a go.mod file."""
    replaces = {}
    in_block = False
    for raw in text.splitlines():
        line = raw.strip()
        if not line or line.startswith("//"):
            continue
        if not in_block:
            if line == "replace (":
                in_block = True
                continue
            if not line.startswith("replace "):
                continue
            body = line[len("replace "):]
        else:
            if line == ")":
                in_block = False
                continue
            body = line
        match = REPLACE_BODY.match(body)
        if match:
            replaces[match.group("module")] = (match.group("target"), match.group("version"))
    return replaces


def sha_of_version(version):
    """The commit a Go pseudo-version names, or None for a tagged/semver version."""
    if not version:
        return None
    match = re.search(r"-([0-9a-f]{12})$", version)
    return match.group(1) if match else None


def read_go_pins(repo):
    """Return {module: {"version","sha","target"}} for the root module and the nested test module."""
    root_pins = parse_replaces(open(os.path.join(repo, ROOT_MODULE), "r", encoding="utf-8").read())
    test_pins = {}
    test_path = os.path.join(repo, TEST_MODULE)
    if os.path.exists(test_path):
        test_pins = parse_replaces(open(test_path, "r", encoding="utf-8").read())

    pins = {}
    for module, (target, version) in root_pins.items():
        pins[module] = {
            "target": target,
            "version": version,
            "sha": sha_of_version(version),
            "test_version": test_pins.get(module, (None, None))[1],
            "test_sha": sha_of_version(test_pins.get(module, (None, None))[1]),
            "in_test": module in test_pins,
        }
    return pins


def gitlink(repo, path):
    """The commit a gitlink names, read from the INDEX first and from HEAD second.

    The index is what `apply` writes (`git update-index`) and what the next commit will contain, so
    reading it first is what makes `apply` idempotent within one checkout: a second run sees the
    pin it just moved instead of the one HEAD still recorded. After a commit the two agree, which
    is the state `verify` runs in. Reading only HEAD would report the same update twice and, worse,
    would let `snapshot` before the commit describe a tree that is not the one being built.
    """
    staged = git(repo, "ls-files", "-s", "--", path, check=False)
    if staged.returncode == 0 and staged.stdout.strip():
        fields = staged.stdout.split()
        if len(fields) >= 3 and fields[0] == "160000":
            return fields[1]
    result = git(repo, "ls-tree", "HEAD", path, check=False)
    if result.returncode != 0:
        return None
    fields = result.stdout.split()
    if len(fields) < 3 or fields[0] != "160000":
        return None
    return fields[2]


# --------------------------------------------------------------------------------------------
# Resolution
# --------------------------------------------------------------------------------------------

def manifest(repo):
    data = load_json(os.path.join(repo, MANIFEST_PATH))
    if data.get("schema") != SCHEMA:
        raise FreezeError("%s declares schema %r, this tool reads schema %d"
                          % (MANIFEST_PATH, data.get("schema"), SCHEMA))
    return data


def targets(data):
    """Every (slug, branch, kind, name) the freeze resolves, in a stable order."""
    out = []
    for entry in data.get("go_modules", []):
        out.append({"kind": "go", "name": entry["module"], "slug": entry["slug"],
                    "branch": entry["branch"], "policy": entry.get("policy", "update")})
    for entry in data.get("clients", []):
        out.append({"kind": "client", "name": entry["path"], "slug": entry["slug"],
                    "branch": entry["branch"], "policy": entry.get("policy", "update")})
    for entry in data.get("nested", []):
        out.append({"kind": "nested", "name": "%s:%s" % (entry["parent"], entry["path"]),
                    "slug": entry["slug"], "branch": entry["branch"],
                    "policy": entry.get("policy", "report")})
    for entry in data.get("upstream_mirrors", []):
        out.append({"kind": "mirror", "name": entry["slug"], "slug": entry["slug"],
                    "branch": entry["branch"], "policy": "report"})
    return out


def resolve_remote(slug, branch):
    """The branch tip, via `git ls-remote`. None means the branch does not exist."""
    result = run([GIT, "ls-remote", "https://github.com/%s.git" % slug, "refs/heads/%s" % branch],
                 check=False)
    if result.returncode != 0:
        raise EnvironmentError_("ls-remote %s %s failed: %s"
                                % (slug, branch, result.stderr.strip()))
    for line in result.stdout.splitlines():
        fields = line.split()
        if len(fields) == 2 and fields[1] == "refs/heads/%s" % branch:
            return fields[0]
    return None


def write_resolve(path, rows):
    directory = os.path.dirname(path)
    if directory:
        os.makedirs(directory, exist_ok=True)
    with open(path, "w", encoding="utf-8", newline="\n") as handle:
        for row in rows:
            handle.write("%s\t%s\t%s\n" % (row["slug"], row["branch"], row["sha"] or "-"))


def read_resolve(path):
    rows = {}
    try:
        with open(path, "r", encoding="utf-8") as handle:
            for line in handle:
                line = line.rstrip("\n")
                if not line or line.startswith("#"):
                    continue
                fields = line.split("\t")
                if len(fields) != 3:
                    raise FreezeError("%s: expected slug<TAB>branch<TAB>sha, got %r"
                                      % (path, line))
                rows[(fields[0], fields[1])] = None if fields[2] == "-" else fields[2]
    except OSError as error:
        raise EnvironmentError_("cannot read %s: %s" % (path, error))
    return rows


def cmd_resolve(repo, args):
    data = manifest(repo)
    rows = []
    if args.recorded:
        recorded = read_resolve(args.recorded)
        for target in targets(data):
            key = (target["slug"], target["branch"])
            if key not in recorded:
                raise FreezeError("the recorded resolution has no row for %s@%s"
                                  % (target["slug"], target["branch"]))
            task = dict(target)
            task["sha"] = recorded[key]
            rows.append(task)
        say("resolved %d target(s) from %s (no network)" % (len(rows), args.recorded))
    else:
        for target in targets(data):
            sha = resolve_remote(target["slug"], target["branch"])
            task = dict(target)
            task["sha"] = sha
            rows.append(task)
            state = "missing branch" if sha is None else sha
            say("resolved %-26s %-22s %s" % (target["slug"], target["branch"], state))
    write_resolve(args.out, rows)
    missing = [row for row in rows if row["sha"] is None]
    if missing:
        for row in missing:
            say("FAIL: %s has no branch %s" % (row["slug"], row["branch"]))
        return 1
    return 0


# --------------------------------------------------------------------------------------------
# Ancestry, via the GitHub compare API
# --------------------------------------------------------------------------------------------
# The pin is a commit the tree already names; asking GitHub to compare it with the branch tip is
# the only way to decide ancestry here without cloning every fork. A fork's default branch is
# irrelevant to this call - both endpoints are explicit commits.

def full_sha(slug, short):
    if short is None:
        return None
    if len(short) == 40:
        return short
    result = run([GH, "api", "repos/%s/commits/%s" % (slug, short), "--jq", ".sha"], check=False)
    if result.returncode != 0:
        # `--jq` with a leading dot is fine; a failure here is a missing commit or a bad slug.
        return None
    return result.stdout.strip() or None


def compare(slug, base, head, cache):
    key = (slug, base, head)
    if key in cache:
        return cache[key]
    result = run([GH, "api", "repos/%s/compare/%s...%s" % (slug, base, head),
                  "--jq", ".status"], check=False)
    if result.returncode != 0:
        cache[key] = REL_UNKNOWN
        return REL_UNKNOWN
    status = result.stdout.strip()
    mapping = {
        "identical": REL_IDENTICAL,
        "ahead": REL_BEHIND,
        "behind": REL_AHEAD,
        "diverged": REL_DIVERGED,
    }
    cache[key] = mapping.get(status, REL_UNKNOWN)
    return cache[key]


# --------------------------------------------------------------------------------------------
# Effective pins and classification
# --------------------------------------------------------------------------------------------

def effective_state(repo, data):
    pins = read_go_pins(repo)
    state = {"go": {}, "cronet_native": {}, "clients": {}, "nested": {}}

    for entry in data.get("go_modules", []):
        module = entry["module"]
        pin = pins.get(module)
        if pin is None or not pin.get("sha"):
            state["go"][module] = {"present": False}
            continue
        state["go"][module] = {
            "present": True,
            "sha": pin["sha"],
            "version": pin["version"],
            "target": pin["target"],
            "test_version": pin["test_version"],
            "test_sha": pin["test_sha"],
            "in_test": pin["in_test"],
        }

    native = data.get("cronet_native") or {}
    prefix = native.get("module_prefix")
    if prefix:
        lib_pins = {}
        for module, pin in pins.items():
            if module.startswith(prefix):
                lib_pins[module] = pin
        shas = sorted({pin["sha"] for pin in lib_pins.values()})
        versions = sorted({pin["version"] for pin in lib_pins.values()})
        test_shas = sorted({pin["test_sha"] for pin in lib_pins.values()})
        state["cronet_native"] = {
            "modules": len(lib_pins),
            "shas": shas,
            "versions": versions,
            "test_shas": test_shas,
            "expected_modules": native.get("expected_modules"),
        }

    for entry in data.get("clients", []):
        state["clients"][entry["path"]] = {"sha": gitlink(repo, entry["path"])}

    for entry in data.get("nested", []):
        key = "%s:%s" % (entry["parent"], entry["path"])
        parent = os.path.join(repo, entry["parent"])
        sha, reason = None, None
        if not os.path.exists(os.path.join(parent, ".git")):
            reason = "%s is not checked out (submodule not initialised)" % entry["parent"]
        else:
            sha = gitlink(parent, entry["path"])
            if sha is None:
                reason = "%s has no gitlink at %s" % (entry["parent"], entry["path"])
        state["nested"][key] = {"sha": sha, "reason": reason}
    return state


def classify(target, pin_sha, resolved_sha, cache):
    if resolved_sha is None:
        return REL_MISSING, "the remote branch does not exist"
    if pin_sha is None:
        return REL_UNKNOWN, "the tree carries no pin for this target"
    if pin_sha == resolved_sha[:len(pin_sha)] or resolved_sha.startswith(pin_sha):
        return REL_IDENTICAL, "the pin is the branch tip"
    base = full_sha(target["slug"], pin_sha)
    if base is None:
        return REL_UNKNOWN, "the pinned commit %s is not in %s" % (pin_sha, target["slug"])
    relation = compare(target["slug"], base, resolved_sha, cache)
    reasons = {
        REL_IDENTICAL: "the pin is the branch tip",
        REL_BEHIND: "the pin is an ancestor of the branch tip: an update is available",
        REL_AHEAD: "the branch tip is an ancestor of the pin: the fork already integrated it",
        REL_DIVERGED: "neither commit contains the other: the fork needs a human merge",
        REL_UNKNOWN: "ancestry could not be decided",
    }
    return relation, reasons.get(relation, "")


def build_report(repo, data, resolution):
    cache = {}
    state = effective_state(repo, data)
    report = {
        "schema": SCHEMA,
        "release_version": read_version(repo),
        # The commit the pins were READ from. It is the parent of the commit that carries this
        # file (a file cannot name its own commit), so it is not the release candidate: the
        # candidate is the commit the tag points at, which the workflow names explicitly.
        "derived_at": git(repo, "rev-parse", "HEAD").stdout.strip(),
        "branch": git(repo, "rev-parse", "--abbrev-ref", "HEAD").stdout.strip(),
        "go_modules": [],
        "cronet_native": state["cronet_native"],
        "clients": [],
        "nested": [],
        "mirrors": [],
    }
    for entry in data.get("go_modules", []):
        pin = state["go"].get(entry["module"], {})
        resolved = resolution.get((entry["slug"], entry["branch"]))
        relation, reason = classify(entry, pin.get("sha"), resolved, cache)
        report["go_modules"].append({
            "module": entry["module"],
            "slug": entry["slug"],
            "branch": entry["branch"],
            "policy": entry.get("policy", "update"),
            "version": pin.get("version"),
            "pinned": pin.get("sha"),
            "test_version": pin.get("test_version"),
            "test_pinned": pin.get("test_sha"),
            "resolved": resolved,
            "relation": relation,
            "reason": reason,
        })
    for entry in data.get("clients", []):
        pin = state["clients"].get(entry["path"], {})
        resolved = resolution.get((entry["slug"], entry["branch"]))
        relation, reason = classify(entry, pin.get("sha"), resolved, cache)
        report["clients"].append({
            "path": entry["path"],
            "slug": entry["slug"],
            "branch": entry["branch"],
            "policy": entry.get("policy", "update"),
            "pinned": pin.get("sha"),
            "resolved": resolved,
            "relation": relation,
            "reason": reason,
        })
    for entry in data.get("nested", []):
        key = "%s:%s" % (entry["parent"], entry["path"])
        pin = state["nested"].get(key, {})
        resolved = resolution.get((entry["slug"], entry["branch"]))
        if pin.get("sha") is None:
            relation, reason = REL_SKIPPED, pin.get("reason") or "not resolvable in this checkout"
        else:
            relation, reason = classify(entry, pin.get("sha"), resolved, cache)
        report["nested"].append({
            "parent": entry["parent"],
            "path": entry["path"],
            "slug": entry["slug"],
            "branch": entry["branch"],
            "policy": entry.get("policy", "report"),
            "pinned": pin.get("sha"),
            "resolved": resolved,
            "relation": relation,
            "reason": reason,
        })
    for entry in data.get("upstream_mirrors", []):
        resolved = resolution.get((entry["slug"], entry["branch"]))
        report["mirrors"].append({
            "slug": entry["slug"],
            "branch": entry["branch"],
            "resolved": resolved,
            "mirror_branch": entry.get("mirror_branch"),
        })
    return report


def read_version(repo):
    path = os.path.join(repo, VERSION_FILE)
    try:
        with open(path, "r", encoding="utf-8") as handle:
            return handle.read().strip()
    except OSError:
        return None


def print_report(report):
    say()
    say("%-42s %-14s %-14s %-10s %s" % ("go module", "pinned", "branch tip", "relation", "policy"))
    for row in report["go_modules"]:
        say("%-42s %-14s %-14s %-10s %s" % (
            row["module"].replace("github.com/", ""),
            (row["pinned"] or "-"),
            (row["resolved"] or "-")[:14],
            row["relation"],
            row["policy"]))
    say()
    say("%-22s %-14s %-14s %-10s %s" % ("client", "pinned", "branch tip", "relation", "policy"))
    for row in report["clients"]:
        say("%-22s %-14s %-14s %-10s %s" % (
            row["path"], (row["pinned"] or "-"), (row["resolved"] or "-")[:14],
            row["relation"], row["policy"]))
    native = report["cronet_native"] or {}
    if native.get("modules"):
        say()
        say("%-22s %d module(s), pin %s, policy controlled" % (
            "cronet-go native", native["modules"],
            ",".join(sha or "-" for sha in native.get("shas") or [])))
    for row in report["nested"]:
        say("%-22s %-14s %-14s %-10s %s" % (
            row["parent"] + ":" + row["path"], (row["pinned"] or "-"),
            (row["resolved"] or "-")[:14], row["relation"], row["policy"]))
    for row in report["mirrors"]:
        say("%-22s %-30s %s" % ("upstream", row["slug"] + "@" + row["branch"],
                                (row["resolved"] or "-")[:14]))


def failures(report, allowed_policies=("update",)):
    """Rows that must stop a release. A `report` row is recorded, never fatal."""
    problems = []
    for row in report["go_modules"] + report["clients"]:
        if row["relation"] in (REL_DIVERGED, REL_MISSING, REL_UNKNOWN, REL_SKIPPED):
            if row["relation"] == REL_SKIPPED and row["policy"] != "update":
                continue
            problems.append(row)
    for row in report["nested"]:
        if row["relation"] in (REL_DIVERGED, REL_UNKNOWN):
            problems.append(row)
    native = report["cronet_native"] or {}
    expected = native.get("expected_modules")
    if expected and native.get("modules") != expected:
        problems.append({"module": "cronet-go native", "relation": "module-count-mismatch",
                         "reason": "%d modules pinned, %d expected"
                                   % (native.get("modules"), expected)})
    if expected and len(native.get("shas") or []) > 1:
        problems.append({"module": "cronet-go native", "relation": "split-pin",
                         "reason": "the platform modules disagree on their SHA: %s"
                                   % ", ".join(native.get("shas") or [])})
    return problems


def cmd_check(repo, args):
    data = manifest(repo)
    resolution = read_resolve(args.resolve)
    report = build_report(repo, data, resolution)
    print_report(report)
    if args.json:
        dump_json(args.json, report)
    problems = failures(report)
    updates = [row for row in report["go_modules"] + report["clients"]
               if row["relation"] == REL_BEHIND and row["policy"] == "update"]
    say()
    for row in report["go_modules"] + report["clients"] + report["nested"]:
        if row["relation"] in (REL_AHEAD, REL_IDENTICAL):
            continue
        say("%-8s %-42s %s" % (row["relation"].upper(), row.get("module") or row.get("path"),
                               row.get("reason", "")))
    if problems:
        say()
        say("FAIL: %d target(s) block a release; nothing was changed." % len(problems))
        return 1
    say("PASS: every target is comparable and none is diverged.")
    if updates:
        say("%d pin(s) are behind and may be moved by `apply`: %s"
            % (len(updates), ", ".join(row.get("module") or row.get("path") for row in updates)))
    else:
        say("no pin needs to move")
    return 0


# --------------------------------------------------------------------------------------------
# apply
# --------------------------------------------------------------------------------------------

def go_pseudo_version(module, sha):
    result = run([GO, "list", "-m", "-json", "%s@%s" % (module, sha)], check=False)
    if result.returncode != 0:
        raise FreezeError("cannot resolve %s@%s to a canonical version:\n%s"
                          % (module, sha, result.stderr.strip()))
    data = json.loads(result.stdout)
    version = data.get("Version")
    if not version:
        raise FreezeError("%s@%s resolved without a version" % (module, sha))
    return version


def rewrite_replace(path, module, version):
    """Rewrite one replace target version in place. Returns True when the file changed."""
    with open(path, "r", encoding="utf-8", newline="") as handle:
        text = handle.read()
    pattern = re.compile(r"(^replace\s+%s\s+=>\s+\S+\s+)\S+(\s*$)" % re.escape(module), re.MULTILINE)
    new_text, count = pattern.subn(lambda match: match.group(1) + version + match.group(2), text)
    if count == 0:
        return False
    if new_text == text:
        return False
    with open(path, "w", encoding="utf-8", newline="\n") as handle:
        handle.write(new_text)
    return True


def cmd_apply(repo, args):
    data = manifest(repo)
    resolution = read_resolve(args.resolve)
    report = build_report(repo, data, resolution)
    problems = failures(report)
    if problems:
        for row in problems:
            say("FAIL: %-40s %s" % (row.get("module") or row.get("path"), row.get("reason", "")))
        say("refusing to move any pin while a target is diverged or unresolvable")
        return 1

    changed = []
    for row in report["go_modules"]:
        if row["relation"] != REL_BEHIND or row["policy"] != "update":
            continue
        if not row["resolved"]:
            continue
        version = go_pseudo_version(row["module"], row["resolved"])
        touched_root = rewrite_replace(os.path.join(repo, ROOT_MODULE), row["module"], version)
        touched_test = rewrite_replace(os.path.join(repo, TEST_MODULE), row["module"], version)
        if touched_root or touched_test:
            changed.append("%s -> %s (%s)" % (row["module"], version, row["resolved"][:12]))
            say("moved %-42s to %s" % (row["module"], version))

    for row in report["clients"]:
        if row["relation"] != REL_BEHIND or row["policy"] != "update":
            continue
        git(repo, "update-index", "--cacheinfo", "160000", row["resolved"], row["path"])
        changed.append("%s -> %s" % (row["path"], row["resolved"][:12]))
        say("moved %-42s to %s" % (row["path"], row["resolved"]))

    for row in report["nested"]:
        if row["relation"] == REL_BEHIND:
            say("note: %s:%s is %s behind; that pin lives in another repository and is not "
                "moved here (order: Dashboard -> Desktop -> kernel)"
                % (row["parent"], row["path"], row["pinned"][:12]))

    if not changed:
        say("no pin needs to move: every `update` target is at its branch tip")
        return 0
    say()
    say("moved %d pin(s). Next: `go mod tidy` in both modules, then `snapshot`." % len(changed))
    return 0


# --------------------------------------------------------------------------------------------
# snapshot
# --------------------------------------------------------------------------------------------

def cmd_snapshot(repo, args):
    data = manifest(repo)
    resolution = read_resolve(args.resolve)
    report = build_report(repo, data, resolution)
    problems = failures(report)
    if problems:
        for row in problems:
            say("FAIL: %-40s %s" % (row.get("module") or row.get("path"), row.get("reason", "")))
        say("refusing to freeze a candidate whose sources are not comparable")
        return 1
    dump_json(args.out, report)
    say("wrote %s" % args.out)
    if args.md:
        with open(args.md, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(render_markdown(report))
        say("wrote %s" % args.md)
    return 0


def render_markdown(report):
    lines = []
    lines.append("# Release source lock")
    lines.append("")
    lines.append("Derived from the pins the tree actually builds - `go.mod`, `test/go.mod` and the")
    lines.append("client gitlinks - by `scripts/ci/release-freeze.py snapshot`. Not a wish list: a")
    lines.append("value here that disagrees with the tree fails `release-freeze.py verify`.")
    lines.append("")
    lines.append("`derived at commit` below is the parent the file was generated from, not the release")
    lines.append("candidate. The candidate is the commit that contains this file, which is the commit a")
    lines.append("release tag names.")
    lines.append("")
    lines.append("| | |")
    lines.append("|---|---|")
    lines.append("| release version | `%s` |" % report.get("release_version"))
    lines.append("| derived at commit | `%s` |" % report.get("derived_at"))
    lines.append("| branch | `%s` |" % report.get("branch"))
    lines.append("")
    lines.append("## Go modules")
    lines.append("")
    lines.append("| module | pinned | branch tip | relation | policy |")
    lines.append("|---|---|---|---|---|")
    for row in report["go_modules"]:
        lines.append("| `%s` | `%s` | `%s` | %s | %s |" % (
            row["module"], row["version"] or "-", (row["resolved"] or "-")[:12],
            row["relation"], row["policy"]))
    native = report.get("cronet_native") or {}
    if native.get("modules"):
        lines.append("")
        lines.append("cronet-go native modules: %d, pinned at `%s` (policy `controlled`; the"
                     % (native["modules"], ",".join(native.get("shas") or [])))
        lines.append("archives come from a Chromium toolchain this repository does not have).")
    lines.append("")
    lines.append("## Clients")
    lines.append("")
    lines.append("| path | pinned | branch tip | relation | policy |")
    lines.append("|---|---|---|---|---|")
    for row in report["clients"]:
        lines.append("| `%s` | `%s` | `%s` | %s | %s |" % (
            row["path"], row["pinned"] or "-", (row["resolved"] or "-")[:12],
            row["relation"], row["policy"]))
    for row in report["nested"]:
        lines.append("| `%s:%s` | `%s` | `%s` | %s | %s |" % (
            row["parent"], row["path"], row["pinned"] or "-", (row["resolved"] or "-")[:12],
            row["relation"], row["policy"]))
    lines.append("")
    lines.append("## Upstream references")
    lines.append("")
    lines.append("| upstream | branch | tip |")
    lines.append("|---|---|---|")
    for row in report["mirrors"]:
        lines.append("| `%s` | `%s` | `%s` |" % (row["slug"], row["branch"],
                                                  (row["resolved"] or "-")[:12]))
    lines.append("")
    return "\n".join(lines)


# --------------------------------------------------------------------------------------------
# verify
# --------------------------------------------------------------------------------------------

def cmd_verify(repo, args):
    lock = load_json(args.lock)
    data = manifest(repo)
    problems = []
    notes = []

    version = read_version(repo)
    if lock.get("release_version") and version and lock["release_version"] != version:
        notes.append("the lock records release %s but %s says %s: regenerate it before a release"
                     % (lock["release_version"], VERSION_FILE, version))

    def locked(rows, key):
        return {row[key]: row for row in rows}

    go_locked = locked(lock.get("go_modules", []), "module")
    pins = read_go_pins(repo)
    for module, row in go_locked.items():
        pin = pins.get(module)
        if pin is None or not pin.get("sha"):
            problems.append("%s: the lock records %s but the tree carries no replace for it"
                            % (module, row.get("pinned")))
            continue
        if row.get("pinned") != pin["sha"]:
            problems.append("%s: lock says %s, go.mod says %s"
                            % (module, row.get("pinned"), pin["sha"]))
        if pin.get("in_test") and pin.get("test_sha") != pin["sha"]:
            problems.append("%s: the root module pins %s but test/go.mod pins %s"
                            % (module, pin["sha"], pin["test_sha"]))
        if not pin.get("in_test"):
            problems.append("%s: test/go.mod does not replace it at all; the nested module would "
                            "build upstream" % module)
    for module in pins:
        if module.startswith((data.get("cronet_native") or {}).get("module_prefix") or ""):
            continue
        if module not in go_locked and module.split("/")[0].startswith("github.com"):
            # Only fork modules are in the lock; a replace to a local path or another module is
            # outside the freeze, but a fork pin that is in the tree and not in the lock is the
            # drift this gate exists for.
            target = pins[module].get("target") or ""
            if "Piggy-Cat-bit-shadow" in target:
                problems.append("%s: the tree pins %s but the lock does not record it"
                                % (module, pins[module].get("sha")))

    native_lock = lock.get("cronet_native") or {}
    native_prefix = (data.get("cronet_native") or {}).get("module_prefix")
    if native_prefix:
        lib_pins = {module: pin for module, pin in pins.items() if module.startswith(native_prefix)}
        shas = sorted({pin["sha"] for pin in lib_pins.values()})
        expected_count = native_lock.get("modules")
        if expected_count and len(lib_pins) != expected_count:
            problems.append("cronet-go native: %d modules pinned, the lock records %d"
                            % (len(lib_pins), expected_count))
        if len(shas) > 1:
            problems.append("cronet-go native: the platform modules disagree on their SHA: %s"
                            % ", ".join(shas))
        if native_lock.get("shas") and shas and shas != native_lock["shas"]:
            problems.append("cronet-go native: lock says %s, the tree pins %s"
                            % (",".join(native_lock["shas"]), ",".join(shas)))

    for row in lock.get("clients", []):
        actual = gitlink(repo, row["path"])
        if actual is None:
            problems.append("%s: no gitlink in the tree (the lock records %s)"
                            % (row["path"], row.get("pinned")))
        elif actual != row.get("pinned"):
            problems.append("%s: lock says %s, the gitlink is %s"
                            % (row["path"], row.get("pinned"), actual))

    for row in lock.get("nested", []):
        parent = os.path.join(repo, row["parent"])
        if not os.path.exists(os.path.join(parent, ".git")):
            notes.append("%s:%s: SKIPPED, %s is not checked out in this workspace"
                         % (row["parent"], row["path"], row["parent"]))
            continue
        actual = gitlink(parent, row["path"])
        if actual != row.get("pinned"):
            problems.append("%s:%s: lock says %s, the gitlink in %s is %s"
                            % (row["parent"], row["path"], row.get("pinned"),
                               row["parent"], actual))

    say("release freeze verification")
    say("  lock       : %s" % args.lock)
    say("  version    : %s" % (lock.get("release_version") or "-"))
    say("  go modules : %d recorded" % len(go_locked))
    say("  clients    : %d recorded" % len(lock.get("clients", [])))
    for note in notes:
        say("  note       : %s" % note)
    if problems:
        say()
        for problem in problems:
            say("FAIL: %s" % problem)
        say()
        say("FAILED: %d pin(s) disagree with the frozen lock." % len(problems))
        return 1
    say()
    say("PASS: every pin in the tree is the pin the lock records.")
    return 0


# --------------------------------------------------------------------------------------------
# main
# --------------------------------------------------------------------------------------------

def main(argv):
    parser = argparse.ArgumentParser(
        prog="release-freeze.py",
        description="Resolve, compare, pin and verify every source a release consumes.")
    parser.add_argument("--repo", default=os.path.dirname(os.path.dirname(
        os.path.dirname(os.path.abspath(__file__)))), help="repository root")
    sub = parser.add_subparsers(dest="command", required=True)

    resolve = sub.add_parser("resolve", help="read the branch tip of every declared target")
    resolve.add_argument("--out", required=True)
    resolve.add_argument("--from", dest="recorded",
                         help="use a recorded resolution instead of the network")
    resolve.set_defaults(func=cmd_resolve)

    check = sub.add_parser("check", help="compare the branch tips with the pins in the tree")
    check.add_argument("--resolve", required=True)
    check.add_argument("--json")
    check.set_defaults(func=cmd_check)

    apply_cmd = sub.add_parser("apply", help="move the pins that are behind")
    apply_cmd.add_argument("--resolve", required=True)
    apply_cmd.set_defaults(func=cmd_apply)

    snapshot = sub.add_parser("snapshot", help="write the source lock from the tree")
    snapshot.add_argument("--resolve", required=True)
    snapshot.add_argument("--out", required=True)
    snapshot.add_argument("--md")
    snapshot.set_defaults(func=cmd_snapshot)

    verify = sub.add_parser("verify", help="compare the tree with the frozen lock")
    verify.add_argument("--lock", required=True)
    verify.set_defaults(func=cmd_verify)

    args = parser.parse_args(argv)
    repo = os.path.abspath(args.repo)
    try:
        return args.func(repo, args)
    except FreezeError as error:
        say("FAIL: %s" % error)
        return 1
    except EnvironmentError_ as error:
        say("ERROR: %s" % error)
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
