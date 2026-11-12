#!/usr/bin/env python3
"""The upstream-retirement ledger has to be true about the tree (audit Q47).

`upstream-retirement-batch2.tsv` is generated from the tree, so the obvious gate (`--check`) only
proves the files were regenerated. That is necessary and not sufficient: a regenerated row can still
name a consumer file that was deleted, a test that does not contain the SPEC id it is cited for, a
commit this clone never had, a patch-id that belongs to a different diff, or an `absorbed` status
the repository cannot support. Those are the ways a maintenance ledger stops being usable - each row
looks authoritative and none of them is checkable by hand across 28 rows and 3789 history rows.

This gate re-derives every one of those claims from Git and the files:

  * commits exist, carry a patch-id, and the recorded patch-id is the one the commit produces;
  * the trigger line still exists, is inside the file, and still carries `SPEC <id>` - and it is
    not one of the ledger's own files, because a sentence about the index is not evidence;
  * consumer files exist; precise-regression files exist, are `_test.go`, and contain the SPEC id;
  * `regression_surface` is at least the number of precise references (a surface smaller than its
    own subset is arithmetically impossible);
  * `upstream_status` is one of the three measured states, `not-comparable` explains itself, and
    the disposition agrees with it (`RETIRE_READY` only for `absorbed`, `ANALYSIS` only for
    documentation-only footprints);
  * the unmarked index still matches the numbers its page states, and every path it names exists.

Usage:
  scripts/ci/check-upstream-retirement.py [--repo DIR]

Exit status: 0 pass, 1 a claim does not hold, 2 input or environment error.
"""

import argparse
import csv
import json
import os
import re
import subprocess
import sys

TSV = "docs/fork/upstream-retirement-batch2.tsv"
PAGE = "docs/fork/upstream-retirement-batch2.md"
UNMARKED_TSV = "docs/fork/upstream-retirement-unmarked.tsv"
UNMARKED_PAGE = "docs/fork/upstream-retirement-unmarked.md"
GENERATOR = "scripts/ci/upstream-retirement-batch2.py"
PAIRS = "docs/fork/upstream-retirement-batch1.tsv"
PAIRS_META = "docs/fork/upstream-retirement-batch1.meta.json"
UPSTREAM_REF = "refs/remotes/upstream/testing"

HEADER = ["spec", "commit_shas", "patch_ids", "first_date", "last_date", "trigger_marker",
          "consumer_files", "regression_exact", "regression_surface", "upstream_status",
          "upstream_commit", "retirement_condition", "retirement_verify", "disposition"]
UNMARKED_HEADER = ["group", "commit", "date", "subject", "trigger", "regression_tests",
                   "consumer_files", "disposition"]

STATUSES = ("absorbed", "duplicate-of-upstream", "fork-only", "not-comparable")
DISPOSITIONS = ("HOLD", "ANALYSIS", "RETIRE_READY")
SELF_FILES = (TSV, PAGE, UNMARKED_TSV, UNMARKED_PAGE, "docs/fork/upstream-retirement-ledger.md",
              "docs/fork/upstream-retirement-b3.md", PAIRS, PAIRS_META)
NO_EXACT = "none names this patch"
FULL_SHA = re.compile(r"^[0-9a-f]{40}$")
TRIGGER = re.compile(r"^(?P<path>[^:]+):(?P<line>[0-9]+): (?P<text>.*)$")
SUFFIX = re.compile(r" \(\+[0-9]+\)$")
# `SPEC 097/106` names two patches on one line; a literal `"SPEC 106" in line` test would call that
# line stale. The id grammar has to be the one the generator uses, or the gate rejects its own data.
SPEC = re.compile(r"SPEC ([0-9][0-9A-Za-z./_-]*)")


def spec_ids(text):
    ids = set()
    for match in SPEC.finditer(text):
        for part in re.split(r"[/]", match.group(1).rstrip(".,;:)")):
            part = part.strip()
            if part and part.isdigit():
                ids.add(part)
    return ids


class InputError(Exception):
    """The tree or the ledger cannot be read: exit status 2."""


def say(message=""):
    print(message, flush=True)


def git(repo, *args, stdin=None):
    return subprocess.run(["git", "-C", repo, *args], capture_output=True, text=True,
                          encoding="utf-8", errors="replace", input=stdin)


def read_lines(repo, relative):
    path = os.path.join(repo, relative)
    try:
        with open(path, "r", encoding="utf-8") as handle:
            return handle.read().splitlines()
    except FileNotFoundError:
        raise InputError("missing: %s" % relative)


def split_paths(text):
    """`a, b, c (+3)` -> [a, b, c]; the `(+N)` suffix counts files the row did not print."""
    text = SUFFIX.sub("", text.strip())
    return [part.strip() for part in text.split(",") if part.strip()]


def patch_id(repo, commit):
    diff = git(repo, "show", "--no-color", "--format=", commit).stdout
    if not diff.strip():
        return ""
    out = git(repo, "patch-id", "--stable", stdin=diff).stdout.split()
    return out[0] if out else ""


def tree_paths(repo):
    listing = git(repo, "ls-files", "-z").stdout
    return set(path for path in listing.split("\0") if path)


def load_pairs(repo):
    """Read batch 1's recorded measurement and validate its own claims.

    The file is only evidence if it says WHAT it measured: which upstream tip, over which
    population of fork commits, and by which criterion. Without that, "this commit is not in the
    list" would be reading as "upstream lacks the patch", which is exactly the unmeasured claim
    this audit removed. So the metadata is required, its commits must exist here, every recorded
    upstream commit must be reachable from the recorded tip, and the recorded pair count must match
    the rows.
    """
    problems = []
    mapping, meta = {}, {}
    pairs_path = os.path.join(repo, PAIRS)
    meta_path = os.path.join(repo, PAIRS_META)
    if not os.path.exists(pairs_path):
        return mapping, meta, problems
    with open(pairs_path, "r", encoding="utf-8") as handle:
        lines = handle.read().splitlines()
    if not lines or lines[0].split("\t") != ["fork_commit", "upstream_commit"]:
        problems.append("%s: header is %r" % (PAIRS, lines[0] if lines else ""))
        return mapping, meta, problems
    for number, line in enumerate(lines[1:], 2):
        fields = line.split("\t")
        if len(fields) != 2 or not FULL_SHA.match(fields[0]) or not FULL_SHA.match(fields[1]):
            problems.append("%s:%d: not a full fork/upstream pair" % (PAIRS, number))
            continue
        if fields[0] in mapping:
            problems.append("%s:%d: fork commit %s recorded twice"
                            % (PAIRS, number, fields[0][:12]))
        mapping[fields[0]] = fields[1]
    if not os.path.exists(meta_path):
        problems.append("%s: missing; the pairs cannot be read as a measurement without the tip "
                        "and population they were taken at" % PAIRS_META)
        return mapping, meta, problems
    try:
        with open(meta_path, "r", encoding="utf-8") as handle:
            meta = json.load(handle)
    except ValueError as error:
        problems.append("%s: not valid JSON (%s)" % (PAIRS_META, error))
        return mapping, meta, problems
    for field in ("upstream_tip", "population_commit"):
        value = meta.get(field) or ""
        if not FULL_SHA.match(value):
            problems.append("%s: %s %r is not a full commit id" % (PAIRS_META, field, value))
        elif git(repo, "cat-file", "-e", "%s^{commit}" % value).returncode != 0:
            problems.append("%s: %s %s is not in this clone"
                            % (PAIRS_META, field, value[:12]))
    if meta.get("pairs") not in (None, len(mapping)):
        problems.append("%s: records %r pairs, the file has %d"
                        % (PAIRS_META, meta.get("pairs"), len(mapping)))
    if "patch-id" not in (meta.get("method") or ""):
        problems.append("%s: method %r does not name the patch-id criterion"
                        % (PAIRS_META, meta.get("method")))
    tip = meta.get("upstream_tip") or ""
    if FULL_SHA.match(tip):
        reachable = set(git(repo, "rev-list", tip).stdout.split())
        outside = [fork for fork, upstream in mapping.items() if upstream not in reachable]
        if outside:
            problems.append("%s: %d recorded upstream commit(s) are not reachable from the "
                            "recorded tip %s; the pairs describe another upstream"
                            % (PAIRS, len(outside), tip[:12]))
        present = upstream_tip(repo)
        if present and present != tip:
            problems.append("%s: measured at %s but this clone's %s is %s"
                            % (PAIRS_META, tip[:12], UPSTREAM_REF, present[:12]))
    return mapping, meta, problems


def upstream_tip(repo):
    """The tip this clone currently has for the pinned upstream ref, or ""."""
    result = git(repo, "rev-parse", "--verify", "--quiet", UPSTREAM_REF + "^{commit}")
    return result.stdout.strip() if result.returncode == 0 else ""


def evaluate(repo):
    problems = []
    notes = []

    if not os.path.exists(os.path.join(repo, TSV)):
        raise InputError("%s is missing; the ledger does not exist to check" % TSV)

    with open(os.path.join(repo, TSV), "r", encoding="utf-8") as handle:
        reader = csv.DictReader(handle, delimiter="\t")
        if reader.fieldnames != HEADER:
            problems.append("%s: header is %r, expected %r"
                            % (TSV, reader.fieldnames, HEADER))
            return problems, notes, {}
        rows = list(reader)

    tracked = tree_paths(repo)
    pairs, pairs_meta, pair_problems = load_pairs(repo)
    problems.extend(pair_problems)
    population = pairs_meta.get("population_commit") or ""
    ref_tip = upstream_tip(repo)
    specs = set()
    for row in rows:
        spec = row["spec"]
        where = "spec %s" % spec
        if spec in specs:
            problems.append("%s: duplicate row" % where)
        specs.add(spec)

        # -- commits and patch-ids -----------------------------------------------------------
        commits = [field for field in row["commit_shas"].split() if field]
        ids = [field for field in row["patch_ids"].split() if field]
        if not commits:
            problems.append("%s: no blamed commit" % where)
        if len(commits) != len(ids) and commits:
            problems.append("%s: %d commits but %d patch-ids; a row whose ids cannot be matched "
                            "to its commits cannot decide retirement" % (where, len(commits), len(ids)))
        for commit in commits:
            if not FULL_SHA.match(commit):
                problems.append("%s: %r is not a full commit id" % (where, commit))
                continue
            present = git(repo, "cat-file", "-e", "%s^{commit}" % commit).returncode == 0
            if not present:
                problems.append("%s: commit %s is not in this clone (shallow checkout?)"
                                % (where, commit[:12]))
                continue
        for commit, recorded in zip(commits, ids):
            if not FULL_SHA.match(recorded):
                problems.append("%s: patch-id %r is not a full id" % (where, recorded))
                continue
            actual = patch_id(repo, commit)
            if actual and actual != recorded:
                problems.append("%s: patch-id for %s is %s, the row says %s"
                                % (where, commit[:12], actual[:12], recorded[:12]))
            elif not actual:
                notes.append("%s: commit %s has an empty patch-id (merge or empty diff)"
                             % (where, commit[:12]))

        # -- trigger line --------------------------------------------------------------------
        match = TRIGGER.match(row["trigger_marker"])
        if match is None:
            problems.append("%s: trigger_marker is not `path:line: text`" % where)
        else:
            path, number, text = match.group("path"), int(match.group("line")), match.group("text")
            del text
            if path in SELF_FILES:
                problems.append("%s: the trigger points at the ledger itself (%s); the index cannot "
                                "be its own evidence" % (where, path))
            elif path not in tracked:
                problems.append("%s: trigger file %s is not in the tree" % (where, path))
            elif not os.path.exists(os.path.join(repo, path)):
                problems.append("%s: trigger file %s is in the index but not checked out"
                                % (where, path))
            else:
                lines = read_lines(repo, path)
                if number < 1 or number > len(lines):
                    problems.append("%s: trigger line %s:%d is outside the file (%d lines)"
                                    % (where, path, number, len(lines)))
                elif spec not in spec_ids(lines[number - 1]):
                    problems.append("%s: %s:%d no longer carries `SPEC %s` - the marker moved on "
                                    "and the row was not regenerated" % (where, path, number, spec))

        # -- consumers and regression --------------------------------------------------------
        consumers = split_paths(row["consumer_files"])
        for path in consumers:
            if path not in tracked:
                problems.append("%s: consumer file %s is not in the tree" % (where, path))
            elif not os.path.exists(os.path.join(repo, path)):
                problems.append("%s: consumer file %s is in the index but not checked out"
                                % (where, path))
        surface = row["regression_surface"]
        if not surface.isdigit():
            problems.append("%s: regression_surface %r is not a count" % (where, surface))
        exact = [] if row["regression_exact"].strip() == NO_EXACT else split_paths(
            row["regression_exact"])
        for path in exact:
            if path not in tracked:
                problems.append("%s: precise regression %s is not in the tree" % (where, path))
                continue
            if not path.endswith("_test.go"):
                problems.append("%s: precise regression %s is not a test file" % (where, path))
                continue
            if not os.path.exists(os.path.join(repo, path)):
                problems.append("%s: precise regression %s is in the index but not checked out"
                                % (where, path))
                continue
            if spec not in spec_ids("\n".join(read_lines(repo, path))):
                problems.append("%s: precise regression %s does not carry `SPEC %s`, so it is not "
                                "a reference to this patch" % (where, path, spec))
        if surface.isdigit() and int(surface) < len(exact):
            problems.append("%s: regression_surface %s is smaller than its own %d precise "
                            "reference(s)" % (where, surface, len(exact)))

        # -- upstream status and disposition -------------------------------------------------
        status = row["upstream_status"]
        disposition = row["disposition"]
        upstream_commit = (row["upstream_commit"] or "").strip()

        def covered(commit):
            return bool(population) and git(repo, "merge-base", "--is-ancestor", commit,
                                            population).returncode == 0

        if status not in STATUSES:
            problems.append("%s: upstream_status %r is not one of %s"
                            % (where, status, ", ".join(STATUSES)))
        if status == "not-comparable" and disposition != "ANALYSIS" \
                and "UNMEASURED" not in row["retirement_condition"]:
            problems.append("%s: not-comparable without saying the equivalence is unmeasured"
                            % where)
        if status == "not-comparable" and pairs and all(covered(commit) for commit in commits):
            problems.append("%s: not-comparable although the recorded measurement covers every "
                            "one of its commits; regenerate the index" % where)
        if status == "duplicate-of-upstream":
            if not FULL_SHA.match(upstream_commit):
                problems.append("%s: duplicate-of-upstream without the upstream commit" % where)
            else:
                recorded = [pairs.get(commit) for commit in commits]
                if pairs and upstream_commit not in recorded:
                    problems.append("%s: batch 1 did not record %s as the upstream commit for any "
                                    "of this row's commits" % (where, upstream_commit[:12]))
                if ref_tip and git(repo, "cat-file", "-e",
                                   "%s^{commit}" % upstream_commit).returncode == 0:
                    if git(repo, "merge-base", "--is-ancestor", upstream_commit,
                           UPSTREAM_REF).returncode != 0:
                        problems.append("%s: the cited upstream commit %s is not in %s"
                                        % (where, upstream_commit[:12], UPSTREAM_REF))
        elif status in ("absorbed", "fork-only") and upstream_commit:
            problems.append("%s: %s should not name an upstream commit (%s)"
                            % (where, status, upstream_commit[:12]))
        if status == "absorbed":
            if ref_tip:
                if not any(git(repo, "merge-base", "--is-ancestor", commit,
                               UPSTREAM_REF).returncode == 0 for commit in commits):
                    problems.append("%s: absorbed, but none of its commits is in %s"
                                    % (where, UPSTREAM_REF))
            else:
                problems.append("%s: absorbed although this clone has no %s to check it against"
                                % (where, UPSTREAM_REF))
        if status == "fork-only" and pairs:
            if not all(covered(commit) for commit in commits):
                problems.append("%s: fork-only although part of the row is outside the recorded "
                                "measurement population" % where)
        if disposition not in DISPOSITIONS:
            problems.append("%s: disposition %r is not one of %s"
                            % (where, disposition, ", ".join(DISPOSITIONS)))
        documentation_only = bool(consumers) and all(
            path.endswith((".md", ".txt", ".json")) for path in consumers)
        if disposition == "ANALYSIS" and not documentation_only:
            problems.append("%s: ANALYSIS over a code footprint (%s)"
                            % (where, ", ".join(consumers[:3])))
        if disposition != "ANALYSIS" and documentation_only:
            problems.append("%s: %s over a documentation-only footprint"
                            % (where, disposition))
        if disposition == "RETIRE_READY" and status not in ("absorbed", "duplicate-of-upstream"):
            problems.append("%s: RETIRE_READY without an upstream that already carries it"
                            % where)
        if disposition == "HOLD" and status in ("absorbed", "duplicate-of-upstream"):
            problems.append("%s: HOLD although upstream already ships the change (%s)"
                            % (where, status))
        if not row["retirement_verify"].strip():
            problems.append("%s: no retirement verification command" % where)

    summary = {"patches": len(rows), "problems": len(problems)}

    # -- the unmarked index is still the one its page describes -------------------------------
    unmarked_rows = []
    if os.path.exists(os.path.join(repo, UNMARKED_TSV)):
        with open(os.path.join(repo, UNMARKED_TSV), "r", encoding="utf-8") as handle:
            reader = csv.DictReader(handle, delimiter="\t")
            if reader.fieldnames != UNMARKED_HEADER:
                problems.append("%s: header is %r" % (UNMARKED_TSV, reader.fieldnames))
            else:
                unmarked_rows = list(reader)
        summary["unmarked"] = len(unmarked_rows)
        bad_disposition = sorted({row["disposition"] for row in unmarked_rows
                                  if row["disposition"] != "REVIEW"})
        if bad_disposition:
            problems.append("%s: dispositions %s are not REVIEW; the page promises REVIEW for "
                            "every row" % (UNMARKED_TSV, ", ".join(bad_disposition)))
        # The paths in this index are the files each commit TOUCHED, at that commit. Most of them
        # were renamed or deleted since, so resolving them against the current tree would fail on
        # a perfectly accurate historical row; what has to hold is that each row names a commit
        # this branch really contains.
        reachable = set(git(repo, "rev-list", "HEAD").stdout.split())
        stale = sum(1 for row in unmarked_rows if row["commit"] not in reachable)
        if stale:
            problems.append("%s: %d row(s) name a commit that is not on this branch"
                            % (UNMARKED_TSV, stale))
        page = "\n".join(read_lines(repo, UNMARKED_PAGE)) if os.path.exists(
            os.path.join(repo, UNMARKED_PAGE)) else ""
        stated = re.search(r"non-merge commits in[^|]*\|\s*\*\*([0-9]+)\*\*", page)
        if stated and int(stated.group(1)) != len(unmarked_rows):
            problems.append("%s: the page states %s commits, the file has %d"
                            % (UNMARKED_PAGE, stated.group(1), len(unmarked_rows)))
        groups = len({row["group"] for row in unmarked_rows})
        stated_groups = re.search(r"normalized-subject groups\s*\|\s*\*\*([0-9]+)\*\*", page)
        if stated_groups and int(stated_groups.group(1)) != groups:
            problems.append("%s: the page states %s groups, the file has %d"
                            % (UNMARKED_PAGE, stated_groups.group(1), groups))

    # -- regenerated, not hand-edited --------------------------------------------------------
    generator = os.path.join(repo, GENERATOR)
    if os.path.exists(generator):
        result = subprocess.run([sys.executable, generator, "--check"], cwd=repo,
                                capture_output=True, text=True)
        if result.returncode != 0:
            problems.append("%s: the generated files no longer match the tree (run --write): %s"
                            % (GENERATOR, (result.stdout or result.stderr).strip()[:400]))
    else:
        notes.append("no generator found, so drift could not be checked")

    return problems, notes, summary


def main(argv):
    parser = argparse.ArgumentParser(prog="check-upstream-retirement.py")
    parser.add_argument("--repo", default=os.path.dirname(os.path.dirname(
        os.path.dirname(os.path.abspath(__file__)))), help="repository root")
    parser.add_argument("--json", default="", help="write the summary as JSON here")
    args = parser.parse_args(argv)
    repo = os.path.abspath(args.repo)
    if not os.path.exists(os.path.join(repo, ".git")):
        raise InputError("not a git worktree: %s" % repo)

    problems, notes, summary = evaluate(repo)
    say("upstream retirement ledger")
    say("  ledger : %s" % TSV)
    if "patches" in summary:
        say("  patches: %d" % summary["patches"])
    if "unmarked" in summary:
        say("  history: %d unmarked rows" % summary["unmarked"])
    if args.json:
        path = os.path.join(repo, args.json)
        parent = os.path.dirname(path)
        if parent:
            os.makedirs(parent, exist_ok=True)
        with open(path, "w", encoding="utf-8", newline="\n") as handle:
            json.dump({"summary": summary, "problems": problems, "notes": notes}, handle,
                      indent=2, sort_keys=True)
            handle.write("\n")
    for note in notes:
        say("NOTE: %s" % note)
    if problems:
        say()
        for problem in problems:
            say("FAIL: %s" % problem)
        say()
        say("FAILED: %d claim(s) about the ledger do not hold." % len(problems))
        return 1
    say()
    say("PASS: every row's commits, patch-ids, trigger lines, consumers, precise regressions, "
        "upstream status and disposition hold against the tree.")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except InputError as error:
        sys.stderr.write("ERROR: %s\n" % error)
        sys.exit(2)
