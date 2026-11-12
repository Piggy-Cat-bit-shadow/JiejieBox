#!/usr/bin/env python3
"""Lean upstream-retirement ledger for still-maintained fork patches (audit Q47 batch 2).

The fork-only population is history, not a patch list: thousands of commits, most carrying no
maintenance marker and no report, and no mechanical way to derive a per-commit trigger. This tool
builds the LEAN index the audit asks for instead: the logical patches this fork still marks as its
own, with the columns the audit's table needs - fork commits, patch-id, behaviour, upstream status,
retirement condition, the command that verifies retirement, and the current disposition.

Maintainership evidence used here:
  * a `SPEC <id>` reference in a source or documentation line - the fork's own task identifier,
  * the files that line lives in (the consumer footprint),
  * the test files in those packages that carry the SAME `SPEC <id>` (the precise regression
    reference) and the count of test files in those packages (the available surface).

Three things this version deliberately does differently from the first one, each because the first
one produced a claim it had not measured:

1. The index is not its own source. Scanning `docs/**` made the generated page's own prose
   ("test name (for example `SPEC 016`)") a marker, blamed a documentation commit onto the patch,
   and produced a row whose trigger was a sentence about the index. The generated files are
   excluded by path; a row whose only evidence is the ledger is not evidence.
2. `upstream_status` is measured, not asserted. The first version wrote "upstream has no equivalent
   patch-id at the pinned tip" for every code row without ever asking - it never compared patch-ids
   and this clone has no `refs/remotes/upstream/*`. Each row now records one of: `absorbed` (the
   commit is reachable from a local upstream ref), `fork-only` (an upstream ref exists and the
   commit is not reachable from it), or `not-comparable` with the reason. "Could not compare" is a
   state, not a synonym for "upstream lacks it".
3. The patch-id is recorded per commit. Retirement is decided by patch-id equality with an upstream
   candidate; without the value in the row, that decision has to be re-derived by hand every time.

Usage:
  python scripts/ci/upstream-retirement-batch2.py --write    # regenerate TSV + page
  python scripts/ci/upstream-retirement-batch2.py --check    # fail if the files drifted
  python scripts/ci/upstream-retirement-batch2.py --list     # spec, commit count, dates
  python scripts/ci/upstream-retirement-batch2.py --print-ids # the patch-ids alone
"""

import argparse
import re
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / "docs" / "fork" / "upstream-retirement-batch2.tsv"
PAGE = ROOT / "docs" / "fork" / "upstream-retirement-batch2.md"
PATCHID_TABLE = ROOT / "docs" / "fork" / "fork-patchids.tsv"
SPEC = re.compile(r"SPEC ([0-9][0-9A-Za-z./_-]*)")
MARKER = re.compile(r"// lx:|lx: SPEC|SPEC [0-9]")
# The ledger cannot be its own evidence: these are the files this pair of tools writes.
SELF_PATHS = ("docs/fork/upstream-retirement-batch2.md",
              "docs/fork/upstream-retirement-batch2.tsv",
              "docs/fork/upstream-retirement-unmarked.md",
              "docs/fork/upstream-retirement-unmarked.tsv",
              "docs/fork/upstream-retirement-ledger.md",
              "docs/fork/upstream-retirement-b3.md")

HEADER = ["spec", "commit_shas", "patch_ids", "first_date", "last_date", "trigger_marker",
          "consumer_files", "regression_exact", "regression_surface", "upstream_status",
          "retirement_condition", "retirement_verify", "disposition"]


def git(*args, stdin=None):
    result = subprocess.run(["git", "-C", str(ROOT), *args], capture_output=True, text=True,
                            encoding="utf-8", errors="replace", input=stdin)
    return result.stdout


def spec_ids(text):
    ids = set()
    for match in SPEC.finditer(text):
        raw = match.group(1).rstrip(".,;:)")
        for part in re.split(r"[/]", raw):
            part = part.strip()
            if part and part.isdigit():
                ids.add(part)
    return ids


def upstream_refs():
    listing = git("for-each-ref", "--format=%(refname)", "refs/remotes/upstream")
    return [line for line in listing.splitlines() if line.strip()]


def patch_id(commit):
    """`git show <commit> | git patch-id --stable`, or "" for a diff with no patch-id."""
    diff = git("show", "--no-color", "--format=", commit)
    if not diff.strip():
        return ""
    out = git("patch-id", "--stable", stdin=diff).split()
    return out[0] if out else ""


def batch1_map():
    """The pre-rewrite fork -> upstream commit map batch 1 left behind, if the file is present."""
    mapping = {}
    if not PATCHID_TABLE.exists():
        return mapping
    with PATCHID_TABLE.open("r", encoding="utf-8") as stream:
        for line in stream:
            fields = line.rstrip("\n").split("\t")
            if len(fields) >= 2 and len(fields[0]) == 40 and len(fields[1]) == 40:
                mapping[fields[0]] = fields[1]
    return mapping


def render_tsv(rows):
    lines = ["\t".join(HEADER)]
    for row in rows:
        lines.append("\t".join(value.replace("\t", " ") for value in row))
    return "\n".join(lines) + "\n"


def cell(text, limit=120):
    """One table cell: no pipes, no newlines, bounded length."""
    text = " ".join(str(text).split()).replace("|", "/")
    return text if len(text) <= limit else text[:limit - 1] + "…"


def render_page(rows, marker_count):
    """The page is generated from the same rows as the TSV.

    Why generated: the first version's table was written by hand next to a machine-generated TSV,
    and the two drifted in exactly the way the audit complains about - the row for SPEC 016 named
    the index's own prose as its trigger while the TSV named a test, and the summary table still
    advertised a "0 upstream equivalents" figure nothing had measured. A page that cannot be
    regenerated is a second truth.
    """
    total = len(rows)
    analysis = sum(1 for row in rows if row[12] == "ANALYSIS")
    code = total - analysis
    exact = sum(1 for row in rows if not row[7].startswith("none"))
    absorbed = sum(1 for row in rows if row[9] == "absorbed")
    fork_only = sum(1 for row in rows if row[9] == "fork-only")
    not_comparable = sum(1 for row in rows if row[9] == "not-comparable")
    no_exact = total - exact

    out = []
    out.append("# Upstream retirement, batch 2: the lean maintained-patch index (audit Q47)")
    out.append("")
    out.append("Batch 1 answered \"which fork commits have an identical upstream patch\" mechanically. "
               "Batch 2's object is the other side: the commits upstream does **not** carry, "
               "\"grouped into logical patches, each with trigger/regression/consumer\". That "
               "population is history, not a patch list - months of merges, rebases and rewrites, "
               "most commits with no maintenance marker - so a per-commit trigger cannot be derived "
               "without adjudicating each one, and writing thousands of generated rows would look "
               "like completion while saying nothing.")
    out.append("")
    out.append("This batch therefore builds the **lean** index the audit's wording asks for: the "
               "logical patches the fork still marks as its own. A patch is in scope when a source "
               "or documentation line carries a `SPEC <id>` reference. The marker text is the "
               "trigger evidence, the files are the consumers, the tests that carry the same "
               "`SPEC <id>` are the precise regression reference, and the patch-id makes the "
               "retirement decision mechanical.")
    out.append("")
    out.append("This page and `upstream-retirement-batch2.tsv` are generated by "
               "`scripts/ci/upstream-retirement-batch2.py --write`; `--check` fails when either "
               "file no longer matches the tree.")
    out.append("")
    out.append("## Mechanical result")
    out.append("")
    out.append("| Fact | Value |")
    out.append("| --- | --- |")
    out.append("| marker lines (`lx:` / `SPEC <id>`) in the tree, index files excluded | **%d** |"
               % marker_count)
    out.append("| logical patches, grouped by SPEC id | **%d** |" % total)
    out.append("| code patches (HOLD) / analysis-only records (ANALYSIS) | **%d / %d** |"
               % (code, analysis))
    out.append("| patches with a test that names the patch | **%d** |" % exact)
    out.append("| patches with no test naming them (precise regression absent) | **%d** |"
               % no_exact)
    out.append("| upstream status: absorbed / fork-only / not-comparable | **%d / %d / %d** |"
               % (absorbed, fork_only, not_comparable))
    out.append("")
    if not_comparable == total:
        out.append("Every row is `not-comparable` because this clone has no "
                   "`refs/remotes/upstream/*`: the equivalence question cannot be answered from the "
                   "tree alone. Batch 1 answered it once, at its own upstream pin, and recorded the "
                   "result in `../evidence/q47_upstream_retirement_round09.txt`; that is a dated "
                   "measurement, not a property of these rows. Re-run batch 1's patch-id comparison "
                   "at the current pinned tip before retiring anything.")
        out.append("")
    out.append("## The %d patches" % total)
    out.append("")
    out.append("`trigger` is the marker line itself (`file:line: text`), preferring a source line "
               "over a documentation line. `regression` names the tests that carry the same "
               "`SPEC <id>`; `none names this patch` is a finding, not a placeholder - it says the "
               "available regression surface for that patch is package-level only.")
    out.append("")
    out.append("| spec | kind | commits | last touched | trigger | regression | upstream | disposition |")
    out.append("| --- | --- | --- | --- | --- | --- | --- | --- |")
    for row in rows:
        spec, commits, _ids, _first, last, trigger, _consumer, regression, _surface, status, \
            _condition, _verify, disposition = row
        kind = "analysis" if disposition == "ANALYSIS" else "code"
        out.append("| %s | %s | %d | %s | %s | %s | %s | %s |"
                   % (spec, kind, len(commits.split()), last or "-", cell(trigger),
                      cell(regression, 60), status, disposition))
    out.append("")
    out.append("## What each column means - and does not")
    out.append("")
    out.append("- **commit_shas / patch_ids** are the blamed commits for the marker lines and their "
               "`git patch-id --stable` values. Retirement is decided by patch-id equality with an "
               "upstream candidate, so the value has to be in the row; without it every retirement "
               "review re-derives it by hand.")
    out.append("- **trigger_marker** is the marker comment with `file:line`. It is the source of the "
               "trigger claim for review, NOT a rewritten \"user-visible symptom\" paragraph. The "
               "ledger's own files are excluded: a sentence about the index is not evidence about "
               "the patch, and the first version of this tool ingested exactly that.")
    out.append("- **consumer_files** is every file carrying that SPEC id - the patch's footprint, "
               "not a proven list of runtime call sites.")
    out.append("- **regression_exact** lists the test files in those packages that carry the same "
               "`SPEC <id>`. That is a precise reference, and still not a claim that the test "
               "exercises this patch. **regression_surface** is the count of test files available in "
               "those packages; a large number there is not evidence of coverage.")
    out.append("- **upstream_status** is measured here, never assumed: `absorbed` when the commit is "
               "reachable from a local upstream ref, `fork-only` when an upstream ref exists and the "
               "commit is not reachable from it, `not-comparable` when this clone has no upstream "
               "ref at all (with the reason in `retirement_condition`). The first version of this "
               "tool wrote \"upstream has no equivalent patch-id\" for every code row without ever "
               "asking; `not-comparable` is what the tree can actually support.")
    out.append("- **disposition** is HOLD for code rows (no measured upstream equivalent), ANALYSIS "
               "for historical investigation records with no code patch to retire, and "
               "RETIRE_READY only when the upstream status is `absorbed`.")
    out.append("- **retirement_condition** is what has to become true, and **retirement_verify** is "
               "the command that decides it.")
    out.append("")
    out.append("## Reproduction")
    out.append("")
    out.append("```sh")
    out.append("python scripts/ci/upstream-retirement-batch2.py --write   # regenerate TSV + this page")
    out.append("python scripts/ci/upstream-retirement-batch2.py --check   # fail on drift")
    out.append("python scripts/ci/check-upstream-retirement.py          # the semantic gate")
    out.append("```")
    out.append("")
    out.append("## Remaining batches (unchanged, and not claimed)")
    out.append("")
    out.append("| batch | object | state |")
    out.append("| --- | --- | --- |")
    out.append("| B2 remainder | the unmarked fork-only commits (history without a maintenance "
               "marker) | NOT individually adjudicated: `upstream-retirement-unmarked.tsv` indexes "
               "them with disposition REVIEW, per-topic adjudication is a review task. |")
    out.append("| B3 | the 218 retire-ready duplicate patches | still requires a rebase plan and "
               "approval: it rewrites published history. |")
    out.append("| B4 | dependency forks (sing, sing-tun, sing-quic, cronet-go) | Q42's pin/run "
               "binding: `release/pin-runs.json` plus `scripts/ci/check-pin-runs.py`. |")
    out.append("")
    out.append("Batch 2's contribution is that the maintained set is small, traced to commits, "
               "patch-ids and files, checked for the tests that name it, and explicit about what "
               "this clone cannot decide. The unmarked history is named as unindexed rather than "
               "silently counted as done.")
    out.append("")
    return "\n".join(out)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--write", action="store_true")
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--list", action="store_true")
    parser.add_argument("--print-ids", action="store_true")
    args = parser.parse_args()

    marker_lines = []
    for path in ROOT.rglob("*"):
        if not path.is_file() or path.suffix not in (".go", ".md"):
            continue
        rel = path.relative_to(ROOT).as_posix()
        if rel.startswith(".git/") or "/node_modules/" in rel or rel in SELF_PATHS:
            continue
        try:
            lines = path.read_text(encoding="utf-8").splitlines()
        except UnicodeDecodeError:
            continue
        for number, line in enumerate(lines, 1):
            ids = spec_ids(line)
            if ids and MARKER.search(line):
                marker_lines.append((rel, number, sorted(ids), line.strip()))

    groups = defaultdict(lambda: {"files": set(), "markers": [], "commits": set(), "dates": []})
    date_by_commit = {}
    for rel, number, ids, line in marker_lines:
        blame = git("blame", "-L", f"{number},{number}", "--porcelain", "--", rel)
        commit = blame.split("\n", 1)[0].split()[0] if blame else ""
        date = ""
        if commit and not commit.startswith("^"):
            if commit not in date_by_commit:
                date_by_commit[commit] = git("show", "-s", "--format=%ad", "--date=short",
                                             commit).strip()
            date = date_by_commit[commit]
        for spec in ids:
            group = groups[spec]
            group["files"].add(rel)
            group["markers"].append((rel, number, line.strip()))
            if commit and not commit.startswith("^"):
                group["commits"].add(commit)
                group["dates"].append(date)

    test_ids_cache = {}

    def test_spec_ids(relative):
        if relative not in test_ids_cache:
            try:
                test_ids_cache[relative] = spec_ids((ROOT / relative).read_text(encoding="utf-8"))
            except (UnicodeDecodeError, OSError):
                test_ids_cache[relative] = set()
        return test_ids_cache[relative]

    def tests_in_packages(files):
        tests = set()
        for rel in files:
            directory = (ROOT / rel).parent
            if not directory.is_dir():
                continue
            for candidate in directory.glob("*_test.go"):
                tests.add(candidate.relative_to(ROOT).as_posix())
        return sorted(tests)

    refs = upstream_refs()
    mapping = batch1_map()

    def upstream_status(commits):
        """One measured state, never a guess: absorbed / fork-only / not-comparable."""
        if not refs:
            return ("not-comparable",
                    "no refs/remotes/upstream/* in this clone; batch 1's comparison is recorded "
                    "separately")
        for ref in refs:
            for commit in commits:
                result = subprocess.run(["git", "-C", str(ROOT), "merge-base", "--is-ancestor",
                                         commit, ref], capture_output=True)
                if result.returncode == 0:
                    return "absorbed", ref
        if mapping and any(commit in mapping for commit in commits):
            return "fork-only", "batch-1 table maps it to an upstream commit; compare patch-ids"
        return "fork-only", "not reachable from any local upstream ref"

    rows = []
    for spec in sorted(groups, key=lambda value: (len(value), value)):
        group = groups[spec]
        files = sorted(group["files"])
        commits = sorted(group["commits"])
        dates = sorted(group["dates"])
        tests = tests_in_packages(files)
        exact = [rel for rel in tests if spec in test_spec_ids(rel)]
        # A source marker is the patch's own statement; a documentation line is the record about
        # it. Prefer the former so the trigger column points at the change, not at its write-up.
        markers = sorted(group["markers"], key=lambda item: (item[0].endswith(".md"), item[0],
                                                             item[1]))
        trigger = "%s:%d: %s" % markers[0] if markers else "no marker text"
        consumer = ", ".join(files[:4]) + (f" (+{len(files) - 4})" if len(files) > 4 else "")
        regression_exact = (", ".join(exact[:4]) + (f" (+{len(exact) - 4})" if len(exact) > 4 else "")
                            if exact else "none names this patch")
        status, status_reason = upstream_status(commits)
        patch_ids = [pid for pid in (patch_id(commit) for commit in commits) if pid]
        analysis_only = all(path.endswith(".md") for path in files)
        if analysis_only:
            disposition = "ANALYSIS"
            condition = "documentation/analysis record; no code patch to retire"
            verify = "n/a (analysis record)"
        elif status == "absorbed":
            disposition = "RETIRE_READY"
            condition = "the pinned upstream already carries the commit; drop the local copy"
            verify = "git merge-base --is-ancestor <commit> <upstream-ref>"
        else:
            disposition = "HOLD"
            if status == "not-comparable":
                condition = ("upstream equivalence is UNMEASURED here (%s); HOLD until the "
                             "batch-1 comparison is re-run at the pinned tip" % status_reason)
            else:
                condition = ("upstream ships an equivalent change (%s), or the product drops the "
                             "behaviour" % status_reason)
            verify = ("git show <commit> | git patch-id --stable  ==  same command on the "
                      "upstream candidate")
        rows.append((spec, " ".join(commits), " ".join(patch_ids), dates[0] if dates else "",
                     dates[-1] if dates else "", trigger, consumer, regression_exact,
                     str(len(tests)), status, condition, verify, disposition))

    if args.write:
        OUT.parent.mkdir(parents=True, exist_ok=True)
        with OUT.open("w", encoding="utf-8", newline="\n") as stream:
            stream.write(render_tsv(rows))
        with PAGE.open("w", encoding="utf-8", newline="\n") as stream:
            stream.write(render_page(rows, len(marker_lines)))
        print(f"wrote {OUT.relative_to(ROOT)} and {PAGE.relative_to(ROOT)}: {len(rows)} logical "
              f"patches from {len(marker_lines)} marker lines")
    if args.check:
        expected_tsv = render_tsv(rows)
        expected_page = render_page(rows, len(marker_lines))
        drifted = []
        for path, expected in ((OUT, expected_tsv), (PAGE, expected_page)):
            actual = path.read_text(encoding="utf-8") if path.exists() else None
            if actual != expected:
                drifted.append(path.relative_to(ROOT).as_posix())
        if drifted:
            print("FAIL: the index no longer matches the tree: %s" % ", ".join(drifted))
            print("      regenerate with `python scripts/ci/upstream-retirement-batch2.py --write`")
            return 1
        print("PASS: the index matches the tree (%d logical patches, %d marker lines)."
              % (len(rows), len(marker_lines)))
        return 0
    if args.list:
        for row in rows:
            print("\t".join((row[0], str(len(row[1].split())), row[3], row[4], row[12])))
    if args.print_ids:
        for row in rows:
            print("%s\t%s" % (row[0], row[2]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
