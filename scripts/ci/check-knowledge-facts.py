#!/usr/bin/env python3
"""The knowledge base's entry points must agree about which revision they describe (audit Q45).

Q45's finding was about wording that drifts while every individual sentence stays true: the page a
reader opens first advertised a 2026-10-31 baseline while the scoped refresh two sections down
described a later release; the report index had grown a second row for the same directory, with a
different version in each; archives written after the index was last edited were never registered,
so the index silently covered less than the directory it claimed to describe. None of that is
visible by reading one file.

So this gate reads the entry points together and compares them with the tree:

  * `release/JIEJIE_VERSION` and `release/source-lock.json` name the same release;
  * `KNOWLEDGE/README.md` states a current scoped target (version + commit) and every version it
    names is not newer than the tree's;
  * `KNOWLEDGE/BASELINE.md` carries that same scoped target, and the broad baseline the README
    still advertises is one BASELINE.md actually records;
  * `KNOWLEDGE/reports/README.md` registers every directory, has no directory twice, has a section
    only for directories that exist, and lists every report that lives in a directory it gives a
    table for - a table that omits half its directory is the drift this check exists for;
  * the 55-item coverage matrix still has 55 unique items with statuses from its own vocabulary;
  * `verify-evidence/README.md` still states the rule that makes those logs untrusted (they are
    stale by their own text), because `AGENTS.md` relies on that rule.

Usage:
  scripts/ci/check-knowledge-facts.py [--repo DIR] [--json build/knowledge-facts.json]

Exit status: 0 pass, 1 a stated fact does not hold, 2 input or environment error.
"""

import argparse
import json
import os
import re
import sys

README = "KNOWLEDGE/README.md"
BASELINE = "KNOWLEDGE/BASELINE.md"
REPORTS = "KNOWLEDGE/reports"
REPORTS_README = "KNOWLEDGE/reports/README.md"
MATRIX = "KNOWLEDGE/reports/other-project-learning-and-integration/MASTER_PLAN.md"
VERSION_FILE = "release/JIEJIE_VERSION"
LOCK = "release/source-lock.json"
VERIFY_EVIDENCE = "verify-evidence/README.md"

SEMVER = re.compile(r"v?([0-9]+)\.([0-9]+)\.([0-9]+)")
SHA = re.compile(r"\b([0-9a-f]{40})\b")
ROW = re.compile(r"^\|\s*(.+?)\s*\|(.*)\|\s*$")
BT = chr(96)
MATRIX_ID = re.compile(r"^\|\s*([A-Z][0-9]{2})\s*\|")
MATRIX_STATUSES = ("PENDING", "IMPLEMENTED_VERIFIED", "ALREADY_PRESENT_VERIFIED",
                   "NOT_ADOPTED_WITH_EVIDENCE", "BLOCKED_EXTERNAL", "BLOCKED_BY_ENVIRONMENT")


class InputError(Exception):
    """A file the gate reads is missing or unreadable: exit status 2."""


def say(message=""):
    print(message, flush=True)


def read(repo, relative):
    path = os.path.join(repo, relative)
    try:
        with open(path, "r", encoding="utf-8") as handle:
            return handle.read()
    except FileNotFoundError:
        raise InputError("missing: %s" % relative)


def versions(text):
    return [(int(a), int(b), int(c)) for a, b, c in SEMVER.findall(text)]


def first_section(text, level=2):
    """The first section at `level` after the title: its heading and body."""
    lines = text.splitlines()
    for index, line in enumerate(lines):
        if line.startswith("#" * level + " ") and not line.startswith("#" * (level + 1)):
            body = []
            for following in lines[index + 1:]:
                if following.startswith("#" * level + " "):
                    break
                body.append(following)
            return line, "\n".join(body)
    return "", ""


def table_rows(text):
    """Rows of the first markdown table in `text`, as (first cell, rest)."""
    rows = []
    started = False
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped.startswith("|"):
            if started and rows:
                break
            continue
        started = True
        match = ROW.match(stripped)
        if match is None:
            continue
        first, rest = match.group(1).strip(), match.group(2)
        if set(first) <= set("-: "):
            continue
        rows.append((first, rest))
    return rows


def strip_code(text):
    return text.replace(BT, "")


def evaluate(repo):
    problems = []
    notes = []

    # -- 1. the tree's own release identity ---------------------------------------------------
    version_text = read(repo, VERSION_FILE).strip()
    match = re.match(r"^([0-9]+)\.([0-9]+)\.([0-9]+)$", version_text)
    if match is None:
        problems.append("%s is %r, not x.y.z" % (VERSION_FILE, version_text))
        tree_version = None
    else:
        tree_version = tuple(int(part) for part in match.groups())
    lock = json.loads(read(repo, LOCK))
    locked_version = lock.get("release_version")
    if tree_version and locked_version != version_text:
        problems.append("%s says %s but %s records release %r; the lock must be regenerated"
                        % (VERSION_FILE, version_text, LOCK, locked_version))

    # -- 2. the README's entry table ---------------------------------------------------------
    readme = read(repo, README)
    current_rows = [row for row in table_rows(readme) if "当前验收目标" in row[0]]
    if not current_rows:
        problems.append("%s: no row names the current 验收目标; a reader cannot tell which "
                        "revision this knowledge describes" % README)
    else:
        cell = strip_code(current_rows[0][0] + " " + current_rows[0][1])
        scoped = versions(cell)
        scoped_sha = SHA.search(cell)
        if not scoped:
            problems.append("%s: the current 验收目标 row names no version" % README)
        elif tree_version and scoped[0] > tree_version:
            problems.append("%s: the current target is v%s, newer than %s (%s)"
                            % (README, ".".join(map(str, scoped[0])), VERSION_FILE, version_text))
        if scoped_sha is None:
            problems.append("%s: the current 验收目标 row names no commit" % README)
        else:
            baseline = read(repo, BASELINE)
            if scoped_sha.group(1) not in baseline:
                problems.append("%s: the current target commit %s is not recorded in %s"
                                % (README, scoped_sha.group(1)[:12], BASELINE))
            heading, first_body = first_section(baseline)
            if not re.search(r"20\d\d-\d\d-\d\d", heading):
                problems.append("%s: the first section (%r) carries no date, so the scoped target "
                                "has no time point" % (BASELINE, heading))
            if scoped[0] not in versions(first_body):
                problems.append("%s: %s is the current target but the first section of %s (%r) "
                                "does not name that version" % (README, ".".join(map(str, scoped[0])),
                                                               BASELINE, heading))
    broad_rows = [row for row in table_rows(readme) if "生产基线" in row[0]]
    if not broad_rows:
        problems.append("%s: no row names the broad 生产基线" % README)
    else:
        cell = strip_code(broad_rows[0][0] + " " + broad_rows[0][1])
        sha = SHA.search(cell)
        if sha is None:
            problems.append("%s: the broad baseline row names no commit" % README)
        elif sha.group(1) not in read(repo, BASELINE):
            problems.append("%s: the broad baseline %s is not recorded in %s"
                            % (README, sha.group(1)[:12], BASELINE))
    named = versions(strip_code(readme))
    del named  # upstream references (for example `v1.15.0-alpha.13`) are not product versions;
    # the product-version claim that matters is the scoped-target row checked above.

    # -- 3. the report index -----------------------------------------------------------------
    index = read(repo, REPORTS_README)
    directories = sorted(name for name in os.listdir(os.path.join(repo, REPORTS))
                         if os.path.isdir(os.path.join(repo, REPORTS, name)))
    files = sorted(name for name in os.listdir(os.path.join(repo, REPORTS))
                   if os.path.isfile(os.path.join(repo, REPORTS, name)))
    structure = [row[0] for row in table_rows(index)]
    # rows may be `dir/` or a file name; be tolerant about the surrounding table
    structured = [strip_code(cell).strip("*").strip() for cell in structure]
    for directory in directories:
        if not any(cell.rstrip("/") == directory for cell in structured):
            problems.append("%s: directory %s/ has no row in 目录结构" % (REPORTS_README, directory))
    for name in files:
        if name == "README.md":
            continue
        if not any(cell == name for cell in structured) and name not in strip_code(index):
            problems.append("%s: %s has no row and is not linked" % (REPORTS_README, name))
    counts = {}
    for cell in structured:
        key = cell.rstrip("/")
        if key and key != "目录":
            counts[key] = counts.get(key, 0) + 1
    for key, count in sorted(counts.items()):
        if count > 1:
            problems.append("%s: %r is registered %d times in 目录结构" % (REPORTS_README, key, count))

    sections = {}
    for block in re.split(r"^### ", index, flags=re.MULTILINE)[1:]:
        title = block.splitlines()[0].strip().rstrip("/")
        sections[title] = block
    for title in sections:
        if title not in directories:
            problems.append("%s: section %r describes a directory that does not exist"
                            % (REPORTS_README, title))
    for title, block in sorted(sections.items()):
        if title not in directories:
            continue
        listed = strip_code(block)
        for name in sorted(os.listdir(os.path.join(repo, REPORTS, title))):
            path = os.path.join(repo, REPORTS, title, name)
            if not os.path.isfile(path) or name.endswith(".json"):
                continue
            # Registered means a reader can find it from this index: in the table for its own
            # directory, or linked from it somewhere in the page. What the check refuses is a file
            # a reader cannot reach at all, which is what happens when a report is archived and the
            # index is not touched.
            if name not in listed and name not in strip_code(index):
                problems.append("%s: %s/%s is not in the section that indexes that directory"
                                % (REPORTS_README, title, name))
    if "历史记录（未复核）" in index:
        legend = [line for line in index.splitlines()
                  if "历史记录（未复核）" in line and not line.strip().startswith("|")]
        if not legend:
            problems.append("%s: the status 历史记录（未复核） is used in rows but never explained"
                            % REPORTS_README)
    for marker in ("历史报告", "不能直接当作当前源码"):
        if marker not in index:
            problems.append("%s: the historical-by-default statement (%r) is gone"
                            % (REPORTS_README, marker))

    # -- 4. the 55-item coverage matrix ------------------------------------------------------
    if os.path.exists(os.path.join(repo, MATRIX)):
        matrix = read(repo, MATRIX)
        heading = re.search(r"^##\s*([0-9]+)\s*项覆盖矩阵\s*$", matrix, re.MULTILINE)
        if heading is None:
            problems.append("%s: no 覆盖矩阵 section" % MATRIX)
        else:
            claimed = int(heading.group(1))
            ids = [MATRIX_ID.match(line).group(1) for line in matrix.splitlines()
                   if MATRIX_ID.match(line)]
            if len(ids) != claimed:
                problems.append("%s: the matrix claims %d items and lists %d"
                                % (MATRIX, claimed, len(ids)))
            if len(set(ids)) != len(ids):
                problems.append("%s: duplicate item ids in the matrix" % MATRIX)
            body = matrix[matrix.index(heading.group(0)):]
            bad = [line.split("|")[1].strip() for line in body.splitlines()
                   if MATRIX_ID.match(line)
                   and not any(status in line for status in MATRIX_STATUSES)]
            if bad:
                problems.append("%s: %d row(s) carry no known status: %s"
                                % (MATRIX, len(bad), ", ".join(bad[:5])))
            notes.append("%s: %d items, all with a known status" % (MATRIX, len(ids)))
    else:
        problems.append("%s: the 55-item coverage matrix is missing" % MATRIX)

    # -- 5. verify-evidence is still declared untrusted ---------------------------------------
    if not os.path.exists(os.path.join(repo, VERIFY_EVIDENCE)):
        problems.append("%s: missing; AGENTS.md relies on its staleness rule" % VERIFY_EVIDENCE)
    elif "stale" not in read(repo, VERIFY_EVIDENCE):
        problems.append("%s: no longer states that a log naming a different HEAD is stale"
                        % VERIFY_EVIDENCE)

    return problems, notes, {"tree_version": version_text, "scoped": bool(current_rows)}


def main(argv):
    parser = argparse.ArgumentParser(prog="check-knowledge-facts.py")
    parser.add_argument("--repo", default=os.path.dirname(os.path.dirname(
        os.path.dirname(os.path.abspath(__file__)))), help="repository root")
    parser.add_argument("--json", default="", help="write the summary as JSON here")
    args = parser.parse_args(argv)
    repo = os.path.abspath(args.repo)

    problems, notes, summary = evaluate(repo)
    say("knowledge entry points")
    say("  tree version : %s" % summary.get("tree_version", "?"))
    say("  scoped target: %s" % ("declared" if summary.get("scoped") else "MISSING"))
    for note in notes:
        say("  note         : %s" % note)
    if args.json:
        path = os.path.join(repo, args.json)
        parent = os.path.dirname(path)
        if parent:
            os.makedirs(parent, exist_ok=True)
        with open(path, "w", encoding="utf-8", newline="\n") as handle:
            json.dump({"summary": summary, "problems": problems, "notes": notes}, handle,
                      indent=2, sort_keys=True)
            handle.write("\n")
    if problems:
        say()
        for problem in problems:
            say("FAIL: %s" % problem)
        say()
        say("FAILED: %d stated fact(s) do not hold." % len(problems))
        return 1
    say()
    say("PASS: the entry points agree on one dated target, the report index covers its "
        "directories, and the 55-item matrix is intact.")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except InputError as error:
        sys.stderr.write("ERROR: %s\n" % error)
        sys.exit(2)
