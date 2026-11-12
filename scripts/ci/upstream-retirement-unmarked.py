#!/usr/bin/env python3
"""Full factual index of the fork-only history (audit Q47 B2 remainder).

The lean ledger (upstream-retirement-batch2.tsv) covers the 28 patches this fork explicitly marks.
This tool covers the rest: every non-merge commit in HEAD that is not in the pinned upstream tip,
with the facts a reviewer needs and one explicit disposition.

It does NOT claim semantic adjudication. Each row carries:
  * the commit and its date/subject,
  * the first paragraph of the commit body (the author's own trigger statement, or the subject when
    the body is empty),
  * the test files and non-test files the commit touched (the regression surface and consumers),
  * a group id derived from the normalized subject, so related commits can be reviewed together,
  * disposition REVIEW: the row still needs the per-topic decision the audit asks for.

Usage:
  python scripts/ci/upstream-retirement-unmarked.py --write
  python scripts/ci/upstream-retirement-unmarked.py --summary
"""

import argparse
import hashlib
import re
import subprocess
import sys
from collections import Counter, defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / "docs" / "fork" / "upstream-retirement-unmarked.tsv"
BASE = "upstream/testing"


def git(*args):
    return subprocess.run(["git", "-C", str(ROOT), *args], capture_output=True, text=True,
                          encoding="utf-8", errors="replace").stdout


def normalized_subject(subject):
    subject = re.sub(r"\s*\(#\d+\)\s*$", "", subject)
    subject = re.sub(r"^[a-z]+(\([^)]*\))?!?:\s*", "", subject)
    return re.sub(r"\s+", " ", subject.strip().lower())


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--write", action="store_true")
    parser.add_argument("--summary", action="store_true")
    args = parser.parse_args()

    metadata = git("log", "--no-merges", "--date=short",
                   "--format=%x1e%H%x1f%ad%x1f%s%x1f%b", f"HEAD", "--not", BASE)
    commits = []
    for record in metadata.split("\x1e"):
        if not record.strip():
            continue
        fields = record.split("\x1f")
        if len(fields) < 4:
            continue
        sha, date, subject, body = fields[0], fields[1], fields[2], "\x1f".join(fields[3:])
        trigger = ""
        for line in body.splitlines():
            line = line.strip()
            if line:
                trigger = line
                break
        if not trigger:
            trigger = subject
        commits.append({"sha": sha.strip(), "date": date.strip(), "subject": subject.strip(),
                        "trigger": trigger[:300]})

    files = defaultdict(lambda: {"tests": [], "consumers": []})
    listing = git("log", "--no-merges", "--name-only", "--format=%x1e%H", f"HEAD", "--not", BASE)
    for record in listing.split("\x1e"):
        if not record.strip():
            continue
        lines = [line for line in record.splitlines() if line.strip()]
        if not lines:
            continue
        sha = lines[0].strip()
        for path in lines[1:]:
            path = path.strip()
            if path.endswith("_test.go"):
                files[sha]["tests"].append(path)
            else:
                files[sha]["consumers"].append(path)

    groups = Counter()
    rows = []
    for commit in commits:
        key = normalized_subject(commit["subject"])
        group = hashlib.sha1(key.encode("utf-8")).hexdigest()[:8]
        groups[group] += 1
        touched = files[commit["sha"]]
        tests = sorted(set(touched["tests"]))
        consumers = sorted(set(touched["consumers"]))
        rows.append((
            group,
            commit["sha"],
            commit["date"],
            commit["subject"],
            commit["trigger"],
            "; ".join(tests[:4]) + (f" (+{len(tests) - 4})" if len(tests) > 4 else "") if tests else "none-in-commit",
            "; ".join(consumers[:6]) + (f" (+{len(consumers) - 6})" if len(consumers) > 6 else "") if consumers else "none-in-commit",
            "REVIEW",
        ))

    if args.write:
        header = ["group", "commit", "date", "subject", "trigger", "regression_tests",
                  "consumer_files", "disposition"]
        OUT.parent.mkdir(parents=True, exist_ok=True)
        with OUT.open("w", encoding="utf-8", newline="\n") as stream:
            stream.write("\t".join(header) + "\n")
            for row in rows:
                stream.write("\t".join(value.replace("\t", " ") for value in row) + "\n")
        print(f"wrote {OUT.relative_to(ROOT)}: {len(rows)} commits, {len(groups)} subject groups")
    if args.summary:
        print(f"commits={len(rows)} groups={len(groups)}")
        print("largest groups:", groups.most_common(5))
    return 0


if __name__ == "__main__":
    sys.exit(main())
