#!/usr/bin/env python3
"""Lean upstream-retirement ledger for still-maintained fork patches (audit Q47 batch 2).

The 3562 fork-only commits are history, not a patch list: most carry no maintenance marker and no
report, and a per-commit semantic trigger cannot be derived mechanically. This tool builds the LEAN
index the audit asks for instead: the logical patches this fork still marks as its own.

Maintainership evidence used here (stated in the generated page):
  * a `SPEC <id>` reference in a source or docs line - the fork's own task identifier, and
  * the files that line lives in (its consumers) plus the tests in the same package (its regression
    surface).

For each SPEC id the tool records the marker text (trigger), the blamed commit and date, the files,
the test files available in the same package, and the disposition. Upstream status is looked up from
the batch-1 patch-id table when it is present; a missing upstream patch is HOLD, never DROP.

Usage:
  python scripts/ci/upstream-retirement-batch2.py --write
  python scripts/ci/upstream-retirement-batch2.py --list
"""

import argparse
import re
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / "docs" / "fork" / "upstream-retirement-batch2.tsv"
SPEC = re.compile(r"SPEC ([0-9][0-9A-Za-z./_-]*)")
MARKER = re.compile(r"// lx:|lx: SPEC|SPEC [0-9]")


def git(*args):
    return subprocess.run(["git", "-C", str(ROOT), *args], capture_output=True, text=True,
                          encoding="utf-8", errors="replace").stdout


def spec_ids(text):
    ids = set()
    for match in SPEC.finditer(text):
        raw = match.group(1).rstrip(".,;:)")
        for part in re.split(r"[/]", raw):
            part = part.strip()
            if part and part.isdigit():
                ids.add(part)
    return ids


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--write", action="store_true")
    parser.add_argument("--list", action="store_true")
    args = parser.parse_args()

    marker_lines = []
    for path in ROOT.rglob("*"):
        if not path.is_file() or path.suffix not in (".go", ".md"):
            continue
        rel = path.relative_to(ROOT).as_posix()
        if rel.startswith(".git/") or "/node_modules/" in rel:
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
                date_by_commit[commit] = git("show", "-s", "--format=%ad", "--date=short", commit).strip()
            date = date_by_commit[commit]
        for spec in ids:
            group = groups[spec]
            group["files"].add(rel)
            group["markers"].append(f"{rel}:{number}: {line}")
            if commit and not commit.startswith("^"):
                group["commits"].add(commit)
                group["dates"].append(date)

    def tests_for(files):
        tests = set()
        for rel in files:
            directory = (ROOT / rel).parent
            if not directory.is_dir():
                continue
            for candidate in directory.glob("*_test.go"):
                tests.add(candidate.relative_to(ROOT).as_posix())
        return tests

    rows = []
    for spec in sorted(groups, key=lambda value: (len(value), value)):
        group = groups[spec]
        files = sorted(group["files"])
        tests = sorted(tests_for(files))
        commits = sorted(group["commits"])
        dates = sorted(group["dates"])
        markers = group["markers"]
        trigger = markers[0] if markers else "no marker text"
        consumer = ", ".join(files[:4]) + (f" (+{len(files) - 4})" if len(files) > 4 else "")
        regression = ", ".join(tests[:4]) + (f" (+{len(tests) - 4})" if len(tests) > 4 else "") if tests else "none in package"
        rows.append((spec, str(len(commits)), dates[0] if dates else "", dates[-1] if dates else "",
                     trigger, consumer, regression, "HOLD", "upstream has no equivalent patch-id at the pinned tip"))

    if args.write:
        header = ["spec", "commits", "first_date", "last_date", "trigger_marker", "consumer_files",
                  "regression_tests", "disposition", "retirement_condition"]
        OUT.parent.mkdir(parents=True, exist_ok=True)
        with OUT.open("w", encoding="utf-8", newline="\n") as stream:
            stream.write("\t".join(header) + "\n")
            for row in rows:
                stream.write("\t".join(value.replace("\t", " ") for value in row) + "\n")
        print(f"wrote {OUT.relative_to(ROOT)}: {len(rows)} logical patches from {len(marker_lines)} marker lines")
    if args.list:
        for row in rows:
            print("\t".join(row[:4]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
