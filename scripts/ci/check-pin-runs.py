#!/usr/bin/env python3
"""Pin <-> executed-run binding for the sources a release consumes (audit Q42).

# The gap this closes

`release-freeze.py verify` proves that the pins in the tree are the pins `release/source-lock.json`
records. That is a statement about CONSISTENCY, and a consistent pin can still be one that nothing
ever built. The audit's finding had exactly that shape: the lock names a parent revision, three
client gitlinks, eight Go forks and one nested gitlink, and nothing tied any of them to a run - a
workflow DECLARATION is not a run, and a run of a different commit proves nothing about these pins.

So there are two records, and this gate compares them:

  release/source-lock.json   what the tree will build (derived by `release-freeze.py snapshot`)
  release/pin-runs.json      what was executed against that same pin set (run ids, commits, results)

A run counts as evidence only when `release/source-lock.json` AT THE RUN'S COMMIT has the same pin
projection as the lock now. Reading that commit - instead of trusting a field that asserts "these
are the pins" - is what makes the claim falsifiable: move a pin without a new run and every
recorded run stops covering it, on the next push, offline.

# What else it checks, and why each one is a real failure mode

  * every repository whose branch tip the lock is measured against has a dated observation
    (`observed_tips`), so "the pin is identical / lags by two / is controlled" is a recorded fact
    rather than an assumption, and `diverged`/`missing` fail closed;
  * a pin this gate cannot read - the nested Desktop dashboard gitlink, because `verify.yml`
    checks out no submodules - must be declared in `not_covered` together with the workflow+job
    that does read it, and that job must actually check the submodule out. A compensating gate
    that was deleted fails here instead of leaving an empty section nobody notices.

# What it deliberately does not do

  * It does not fetch by default. A recorded run at a commit this clone does not contain is a
    failure, not a silent pass: the point is that the pins are re-readable where the check runs.
    `--fetch-missing` exists for the CI case - `verify.yml` checks out one commit, so the recorded
    run commits are absent and fetching exactly those commits (depth 1) is cheaper than cloning
    history. It fetches only the missing ids and says how many it fetched.
  * It does not claim a client built its own source: a parent run covers the gitlink the parent
    builds, which is the parent-repo consumption gate. A client's own CI run can be recorded in
    `child_runs` and is validated when present, but its absence is not invented as a pass.

Usage:
  scripts/ci/check-pin-runs.py                  # offline: a missing run commit fails
  scripts/ci/check-pin-runs.py --fetch-missing  # CI: fetch the recorded commits, then judge
  scripts/ci/check-pin-runs.py --json build/pin-runs.json

Exit status: 0 pass, 1 a recorded claim is contradicted, 2 input or environment error.
"""

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys

LOCK_PATH = "release/source-lock.json"
RUNS_PATH = "release/pin-runs.json"
MANIFEST_PATH = "release/dependencies.json"

REL_IDENTICAL = "identical"
REL_BEHIND = "behind"
REL_AHEAD = "ahead"
REL_DIVERGED = "diverged"
REL_MISSING = "missing"
REL_NOT_COMPARED = "not-compared"
RELATIONS = (REL_IDENTICAL, REL_BEHIND, REL_AHEAD, REL_DIVERGED, REL_MISSING, REL_NOT_COMPARED)

POLICIES = ("update", "controlled", "report")

# Only a completed success is evidence. `cancelled` is the interesting one: the parent repository
# cancels superseded runs on the same ref, so a cancelled run is not a failure of the pins - it is
# not evidence at all, and the record has to name a run that completed.
COVERING_CONCLUSION = "success"


class InputError(Exception):
    """The record or the tree cannot be read: exit status 2."""


def say(message=""):
    print(message, flush=True)


def git(repo, *args):
    return subprocess.run(["git", "-C", repo] + list(args), capture_output=True, text=True)


def load_json(path, what):
    try:
        with open(path, "r", encoding="utf-8") as handle:
            return json.load(handle)
    except FileNotFoundError:
        raise InputError("%s is missing: %s" % (what, path))
    except ValueError as error:
        raise InputError("%s is not valid JSON (%s): %s" % (what, error, path))


# ------------------------------------------------------------------------------------------------
# The pin projection: the part of the lock that names a source
# ------------------------------------------------------------------------------------------------

def projection(lock):
    """Return the pin-bearing part of a lock, canonically ordered.

    Deliberately excludes `release_version` and `schema`: bumping the version does not change which
    sources are built, and a run that exercised these pins still covers them. Everything that names
    a source IS included, including the cronet-native aggregate.
    """
    go = sorted(({"module": row.get("module"), "pinned": row.get("pinned"),
                  "version": row.get("version")} for row in lock.get("go_modules", [])),
                key=lambda row: row["module"] or "")
    native = lock.get("cronet_native") or {}
    clients = sorted(({"path": row.get("path"), "pinned": row.get("pinned")}
                      for row in lock.get("clients", [])), key=lambda row: row["path"] or "")
    nested = sorted(({"parent": row.get("parent"), "path": row.get("path"),
                      "pinned": row.get("pinned")} for row in lock.get("nested", [])),
                    key=lambda row: (row["parent"] or "", row["path"] or ""))
    return {
        "go_modules": go,
        "cronet_native": {"modules": native.get("modules"),
                          "shas": sorted(native.get("shas") or [])},
        "clients": clients,
        "nested": nested,
    }


def digest(proj):
    text = json.dumps(proj, sort_keys=True, separators=(",", ":"), ensure_ascii=True)
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def same_revision(left, right):
    """Are these the same revision? The lock records 12-character SHAs, observations 40.

    Comparing the strings directly would report "4abfaed41727 != 4abfaed41727e43d..." and turn
    every correct record into a failure; comparing only the shorter one's length is the honest
    reading of "one names the other". A prefix of seven characters is the shortest Git allows, and
    a mismatch of length is still a mismatch.
    """
    if not left or not right:
        return False
    left, right = str(left).lower(), str(right).lower()
    short, long_ = (left, right) if len(left) <= len(right) else (right, left)
    return len(short) >= 7 and long_.startswith(short)


def lock_at_commit(repo, sha):
    """Read release/source-lock.json out of a commit, not out of the working tree."""
    result = git(repo, "show", "%s:%s" % (sha, LOCK_PATH))
    if result.returncode != 0:
        return None, result.stderr.strip() or "no %s at %s" % (LOCK_PATH, sha[:12])
    try:
        return json.loads(result.stdout), ""
    except ValueError as error:
        return None, "the lock at %s is not valid JSON: %s" % (sha[:12], error)


# ------------------------------------------------------------------------------------------------
# Pin identities, so a gap or an observation can name one thing
# ------------------------------------------------------------------------------------------------

def native_id(manifest):
    prefix = (manifest.get("cronet_native") or {}).get("module_prefix")
    return "native:%s*" % (prefix or "cronet-go/lib/")


def pin_ids(lock, manifest):
    ids = ["module:%s" % row.get("module") for row in lock.get("go_modules", [])]
    if (lock.get("cronet_native") or {}).get("modules"):
        ids.append(native_id(manifest))
    ids += ["client:%s" % row.get("path") for row in lock.get("clients", [])]
    ids += ["nested:%s:%s" % (row.get("parent"), row.get("path"))
            for row in lock.get("nested", [])]
    return ids


def pin_sha(lock, pin, manifest):
    """The pinned SHA a pin identity names (the cronet-native aggregate is one SHA for 29 modules)."""
    for row in lock.get("go_modules", []):
        if "module:%s" % row.get("module") == pin:
            return row.get("pinned")
    if pin == native_id(manifest):
        shas = (lock.get("cronet_native") or {}).get("shas") or []
        return shas[0] if shas else None
    for row in lock.get("clients", []):
        if "client:%s" % row.get("path") == pin:
            return row.get("pinned")
    for row in lock.get("nested", []):
        if "nested:%s:%s" % (row.get("parent"), row.get("path")) == pin:
            return row.get("pinned")
    return None


def required_repositories(lock, manifest):
    """Every repository this lock consumes, derived from the ONE statement of intent.

    The lock says where a pin lands (a module path, a gitlink), not which repository it came from;
    `release/dependencies.json` is the single mapping, so the repository set comes from there and a
    pin with no manifest entry is itself a defect.
    """
    repos = {}

    def add(slug, branch, policy, pin):
        entry = repos.setdefault(slug, {"branch": branch, "policy": policy, "pins": []})
        entry["pins"].append(pin)

    go_by_module = {row["module"]: row for row in manifest.get("go_modules", [])}
    for row in lock.get("go_modules", []):
        declared = go_by_module.get(row.get("module"))
        if declared is not None:
            add(declared["slug"], declared["branch"], declared["policy"],
                "module:%s" % row.get("module"))
    native = manifest.get("cronet_native") or {}
    if native.get("slug") and (lock.get("cronet_native") or {}).get("modules"):
        add(native["slug"], None, native.get("policy"), native_id(manifest))
    clients = {row["path"]: row for row in manifest.get("clients", [])}
    for row in lock.get("clients", []):
        declared = clients.get(row.get("path"))
        if declared is not None:
            add(declared["slug"], declared["branch"], declared["policy"],
                "client:%s" % row.get("path"))
    nested = {(row.get("parent"), row.get("path")): row for row in manifest.get("nested", [])}
    for row in lock.get("nested", []):
        declared = nested.get((row.get("parent"), row.get("path")))
        if declared is not None:
            add(declared["slug"], declared["branch"], declared["policy"],
                "nested:%s:%s" % (row.get("parent"), row.get("path")))
    return repos


def undeclared_pins(lock, manifest):
    """Pins the manifest does not describe; they cannot be observed or policy-checked."""
    declared = set()
    for slug, info in required_repositories(lock, manifest).items():
        del slug
        declared.update(info["pins"])
    return [pin for pin in pin_ids(lock, manifest) if pin not in declared]


# ------------------------------------------------------------------------------------------------
# Workflow facts: does the compensating job exist, and does it check the submodule out?
# ------------------------------------------------------------------------------------------------

def workflow_job(repo, workflow, job):
    """Return one job's text from a workflow file, or None if it is not there.

    Job ids sit at two spaces of indentation under `jobs:`; that is enough to isolate a job without
    a YAML parser, and this script keeps its zero third-party imports.
    """
    path = os.path.join(repo, ".github", "workflows", workflow)
    if not os.path.exists(path):
        return None
    with open(path, "r", encoding="utf-8") as handle:
        text = handle.read()
    match = re.search(r"^  %s:\s*$" % re.escape(job), text, re.MULTILINE)
    if match is None:
        return None
    rest = text[match.end():]
    following = re.search(r"^  [A-Za-z0-9_-]+:\s*$", rest, re.MULTILINE)
    return rest[:following.start()] if following else rest


def checks_out_submodules(job_text):
    return bool(re.search(r"^\s+submodules:\s*recursive\s*$", job_text, re.MULTILINE))


# ------------------------------------------------------------------------------------------------
# Evaluation
# ------------------------------------------------------------------------------------------------

def evaluate(repo, lock, record, manifest, fetch_missing=False):
    problems = []
    notes = []
    covering = []
    fetched = []

    proj = projection(lock)
    current = digest(proj)

    # -- 1. executed runs that carry this pin set -----------------------------------------------
    runs = record.get("parent_runs") or []
    if not runs:
        problems.append("no parent run is recorded: this lock names pins that nothing built")
    seen = set()
    for entry in runs:
        where = "parent run %s" % entry.get("run_id", "?")
        missing = [key for key in ("repo", "workflow", "run_id", "head_sha", "conclusion")
                   if entry.get(key) in (None, "")]
        if missing:
            problems.append("%s: missing %s" % (where, ", ".join(missing)))
            continue
        if not re.match(r"^[0-9a-f]{40}$", str(entry["head_sha"])):
            problems.append("%s: head_sha is not a full commit id (%r)" % (where, entry["head_sha"]))
            continue
        if entry["run_id"] in seen:
            problems.append("%s: recorded twice; one run is one piece of evidence" % where)
        seen.add(entry["run_id"])
        sha = entry["head_sha"]
        if git(repo, "cat-file", "-e", "%s^{commit}" % sha).returncode != 0:
            if fetch_missing:
                # A CI checkout is one commit deep. Fetching exactly the recorded commit (depth 1)
                # brings its tree, which is all this gate reads; a full-history clone would pay for
                # 6,000 commits to answer the same question.
                run = git(repo, "fetch", "--no-tags", "--depth=1", "origin", sha)
                if run.returncode == 0:
                    fetched.append(sha)
                else:
                    problems.append("%s: the clone does not contain %s and fetching it failed: %s"
                                    % (where, sha[:12], (run.stderr or "").strip()[:200]))
                    continue
            else:
                problems.append("%s: this clone does not contain %s, so the pins it ran against "
                                "cannot be re-read here; cite a run at a reachable commit (or run "
                                "with --fetch-missing in a one-commit CI checkout)"
                                % (where, sha[:12]))
                continue
        theirs, why = lock_at_commit(repo, sha)
        if theirs is None:
            problems.append("%s: %s" % (where, why))
            continue
        their_digest = digest(projection(theirs))
        if their_digest != current:
            notes.append("%s (%s at %s) ran against a different pin set (lock %s, now %s)"
                         % (where, entry["workflow"], sha[:12], their_digest[:12], current[:12]))
            continue
        if entry["conclusion"] != COVERING_CONCLUSION:
            problems.append("%s: conclusion is %r; only a completed success is evidence"
                            % (where, entry["conclusion"]))
            continue
        covering.append(entry)

    if not covering:
        # The important failure, and the one that would otherwise read as a pass: the record may
        # be full of successful runs, but none of them ran against THESE pins. Moving a pin
        # without recording a run at the new commitment must not leave the gate green.
        problems.append("no recorded run carries the current pin set: a pin moved and no run was "
                        "recorded against the new lock, so the sources the lock names have not "
                        "been built")

    if fetched:
        notes.append("fetched %d recorded commit(s) at depth 1 to read their locks: %s"
                     % (len(fetched), ", ".join(sha[:12] for sha in fetched)))

    # -- 1b. optional per-client runs ----------------------------------------------------------
    child_runs = record.get("child_runs") or []
    for entry in child_runs:
        where = "child run %s" % entry.get("run_id", "?")
        missing = [key for key in ("repo", "workflow", "run_id", "head_sha", "conclusion", "pin")
                   if entry.get(key) in (None, "")]
        if missing:
            problems.append("%s: missing %s" % (where, ", ".join(missing)))
            continue
        expected = pin_sha(lock, entry["pin"], manifest)
        if expected is None:
            problems.append("%s: pin %r is not in the lock" % (where, entry["pin"]))
        elif entry["head_sha"] != expected:
            problems.append("%s: ran at %s but the lock pins %s at %s"
                            % (where, entry["head_sha"][:12], entry["pin"], expected[:12]))
        elif entry["conclusion"] != COVERING_CONCLUSION:
            problems.append("%s: conclusion is %r" % (where, entry["conclusion"]))

    # -- 2. branch-tip observations -------------------------------------------------------------
    tips = {}
    for row in record.get("observed_tips") or []:
        if row.get("repo") in tips:
            problems.append("observed_tips: %s is observed twice" % row.get("repo"))
        tips[row.get("repo")] = row
    for slug, info in sorted(required_repositories(lock, manifest).items()):
        row = tips.get(slug)
        if row is None:
            problems.append("%s: no branch tip is recorded, so the pin cannot be placed against "
                            "the branch this project tracks" % slug)
            continue
        relation = row.get("relation")
        if relation not in RELATIONS:
            problems.append("%s: relation %r is not one of %s"
                            % (slug, relation, ", ".join(RELATIONS)))
            continue
        expected_pin = pin_sha(lock, info["pins"][0], manifest)
        if row.get("pin") and expected_pin and not same_revision(row["pin"], expected_pin):
            problems.append("%s: the observation is about pin %s but the lock now pins %s"
                            % (slug, row["pin"][:12], expected_pin[:12]))
        if relation == REL_IDENTICAL and row.get("pin") and row.get("tip"):
            if not same_revision(row["pin"], row["tip"]):
                problems.append("%s: recorded as identical but pin %s and tip %s differ"
                                % (slug, row["pin"][:12], row["tip"][:12]))
        if relation in (REL_DIVERGED, REL_MISSING):
            problems.append("%s: the fork is %s; a release must not proceed until a human has "
                            "resolved it in that repository" % (slug, relation))
        if relation == REL_BEHIND and info["policy"] == "update":
            notes.append("%s: the pin is behind its tracked branch; release preparation lifts it "
                         "with `release-freeze.py apply`, and the lift still has to pass the "
                         "build, ABI and module gates" % slug)
        if relation == REL_NOT_COMPARED and not row.get("reason"):
            problems.append("%s: recorded as not-compared without a reason" % slug)
    for pin in undeclared_pins(lock, manifest):
        problems.append("%s: the dependency manifest does not describe this pin, so no branch "
                        "observation and no policy can apply to it" % pin)

    # -- 3. pins this gate cannot read ---------------------------------------------------------
    gaps = {}
    for entry in record.get("not_covered") or []:
        pin = entry.get("pin")
        if not pin:
            problems.append("not_covered: an entry names no pin")
            continue
        if pin in gaps:
            problems.append("not_covered: %s is listed twice" % pin)
        gaps[pin] = entry
        if pin not in pin_ids(lock, manifest):
            problems.append("not_covered: %s is not a pin in the lock" % pin)
        if not entry.get("why"):
            problems.append("not_covered: %s states no reason" % pin)
        checked_by = entry.get("checked_by") or {}
        workflow, job = checked_by.get("workflow"), checked_by.get("job")
        if not workflow or not job:
            problems.append("not_covered: %s names no compensating workflow+job" % pin)
            continue
        job_text = workflow_job(repo, workflow, job)
        if job_text is None:
            problems.append("not_covered: %s points at %s:%s, which does not exist"
                            % (pin, workflow, job))
            continue
        if pin.startswith("nested:") and not checks_out_submodules(job_text):
            problems.append("not_covered: %s points at %s:%s, but that job does not check out "
                            "submodules, so it cannot read the nested gitlink either"
                            % (pin, workflow, job))

    pins = []
    for pin in pin_ids(lock, manifest):
        entry = {"pin": pin, "pin_sha": pin_sha(lock, pin, manifest),
                 "covered_by": [row["run_id"] for row in covering]}
        if pin in gaps:
            entry["gap"] = {"why": gaps[pin].get("why"),
                            "checked_by": gaps[pin].get("checked_by")}
        pins.append(entry)

    return {
        "schema": 1,
        "lock": LOCK_PATH,
        "lock_digest": current,
        "covering_runs": [{"workflow": row["workflow"], "run_id": row["run_id"],
                           "head_sha": row["head_sha"], "repo": row["repo"]} for row in covering],
        "pins": pins,
        "observations": [tips[slug] for slug in sorted(tips)],
        "problems": problems,
        "notes": notes,
    }


def report_rows(result, lock, record, manifest):
    covering = result["covering_runs"]
    covered_by = ("run(s) %s" % ", ".join(str(row["run_id"]) for row in covering)
                  if covering else "NONE")
    gaps = {entry.get("pin"): entry for entry in record.get("not_covered") or []}
    rows = []
    for pin in pin_ids(lock, manifest):
        sha = pin_sha(lock, pin, manifest)
        if pin in gaps:
            entry = gaps[pin]
            checked_by = entry.get("checked_by") or {}
            coverage = "not read by this gate (%s); checked by %s:%s" % (
                entry.get("why", "?"), checked_by.get("workflow", "?"), checked_by.get("job", "?"))
        else:
            coverage = covered_by
        rows.append((pin, (sha or "?")[:12], coverage))
    return rows


def main(argv):
    parser = argparse.ArgumentParser(prog="check-pin-runs.py",
                                     description="Bind the release pins to executed runs.")
    parser.add_argument("--repo", default=os.path.dirname(os.path.dirname(
        os.path.dirname(os.path.abspath(__file__)))), help="repository root")
    parser.add_argument("--lock", default=LOCK_PATH, help="the frozen source lock")
    parser.add_argument("--runs", default=RUNS_PATH, help="the recorded run evidence")
    parser.add_argument("--json", default="", help="write the report as JSON here")
    parser.add_argument("--fetch-missing", action="store_true",
                        help="fetch recorded commits this checkout lacks (depth 1) before judging")
    args = parser.parse_args(argv)

    repo = os.path.abspath(args.repo)
    if not os.path.exists(os.path.join(repo, ".git")):
        raise InputError("not a git worktree: %s" % repo)

    lock = load_json(os.path.join(repo, args.lock), "the source lock")
    record = load_json(os.path.join(repo, args.runs), "the run record")
    manifest = load_json(os.path.join(repo, MANIFEST_PATH), "the dependency manifest")

    result = evaluate(repo, lock, record, manifest, fetch_missing=args.fetch_missing)

    say("pin -> executed-run binding")
    say("  lock    : %s (release %s)" % (args.lock, lock.get("release_version") or "-"))
    say("  record  : %s (%d parent run(s), %d observed tip(s), %d declared gap(s))"
        % (args.runs, len(record.get("parent_runs") or []),
           len(record.get("observed_tips") or []), len(record.get("not_covered") or [])))
    say("  pins    : %d" % len(pin_ids(lock, manifest)))
    say()
    width = max([34] + [len(pin) for pin, _sha, _cov in report_rows(result, lock, record, manifest)])
    for pin, sha, coverage in report_rows(result, lock, record, manifest):
        say("  %-*s  %-12s  %s" % (width, pin, sha, coverage))
    if result["notes"]:
        say()
        for note in result["notes"]:
            say("NOTE: %s" % note)
    if args.json:
        path = os.path.join(repo, args.json)
        parent = os.path.dirname(path)
        if parent:
            os.makedirs(parent, exist_ok=True)
        with open(path, "w", encoding="utf-8", newline="\n") as handle:
            json.dump(result, handle, indent=2, sort_keys=True)
            handle.write("\n")
        say()
        say("report: %s" % args.json)
    if result["problems"]:
        say()
        for problem in result["problems"]:
            say("FAIL: %s" % problem)
        say()
        say("FAILED: %d recorded claim(s) do not hold." % len(result["problems"]))
        return 1
    say()
    say("PASS: every pin in the lock is carried by an executed run at the same pin set (%s)."
        % ", ".join(str(row["run_id"]) for row in result["covering_runs"]))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except InputError as error:
        sys.stderr.write("ERROR: %s\n" % error)
        sys.exit(2)
