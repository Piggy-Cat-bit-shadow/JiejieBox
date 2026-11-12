#!/usr/bin/env python3
"""Behavioural tests for scripts/ci/check-upstream-retirement.py.

Each case builds a throwaway repository that contains the real generator and the real checker, a
handful of marked files, and a Git history, then mutates ONE thing and asserts the gate notices.
The mutations are the failure modes a maintenance ledger actually has: a consumer file that was
deleted, a marker line that moved on, a patch-id that belongs to another diff, a disposition that
contradicts its upstream status, a precise regression that does not name the patch, an unmarked
index that names commits outside the branch, and a page whose numbers no longer match its file.

Usage: scripts/ci/test-check-upstream-retirement.py
"""

import os
import shutil
import stat
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
PYTHON = sys.executable or "python3"
TOOLS = ("upstream-retirement-batch2.py", "check-upstream-retirement.py")

PASSED = 0
FAILED = 0


def check(name, condition, detail=""):
    global PASSED, FAILED
    if condition:
        PASSED += 1
        print("PASS  %s" % name)
    else:
        FAILED += 1
        print("FAIL  %s%s" % (name, (": " + detail) if detail else ""))
    return condition


def sh(args, cwd):
    result = subprocess.run(args, cwd=cwd, capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError("command failed (%d): %s\nstdout: %s\nstderr: %s"
                           % (result.returncode, " ".join(args),
                              result.stdout.strip(), result.stderr.strip()))
    return result


def force_rmtree(path):
    if not os.path.exists(path):
        return
    if os.name == "nt":
        for base, _dirs, files in os.walk(path):
            for name in files:
                try:
                    os.chmod(os.path.join(base, name), stat.S_IWRITE)
                except OSError:
                    pass
    shutil.rmtree(path, ignore_errors=True)


SOURCE = """package demo

// SPEC 007 the queue must hand its inventory to the next owner (lx: SPEC 007).
func Drain() int { return 0 }
"""

SOURCE_TEST = """package demo

// TestDrainIsOwned is the SPEC 007 regression: the inventory is handed over once.
func TestDrainIsOwned(t *testing.T) {}
"""

DOC = """# Residual sweep

## SPEC 009: why the sweep changed no public signature

Nothing to retire; this is the record.
"""

OTHER_SOURCE = """package demo2

// SPEC 011 the second patch, unrelated to the first.
func Other() int { return 1 }
"""

UNMARKED_TSV = (
    "group\tcommit\tdate\tsubject\ttrigger\tregression_tests\tconsumer_files\tdisposition\n"
    "{group}\t{commit}\t2026-11-01\tdemo: touch a file\tthe body of the change\t"
    "none-in-commit\tfiles.txt\tREVIEW\n"
)

UNMARKED_PAGE = """# Fork-only history: the full factual index (audit Q47 B2 remainder)

| fact | value |
| --- | --- |
| non-merge commits in `HEAD --not upstream/testing` (round-10 head) | **{commits}** |
| normalized-subject groups | **{groups}** |
| disposition | **REVIEW** for every row |

Columns: `group, commit, date, subject, trigger, regression_tests, consumer_files, disposition`.
"""


class Fixture:
    def __init__(self):
        self.dir = tempfile.mkdtemp(prefix="retire-")
        self.repo = os.path.join(self.dir, "repo")
        os.makedirs(os.path.join(self.repo, "scripts", "ci"))
        os.makedirs(os.path.join(self.repo, "docs", "fork"))
        os.makedirs(os.path.join(self.repo, "demo"))
        os.makedirs(os.path.join(self.repo, "demo2"))
        for name in TOOLS:
            shutil.copyfile(os.path.join(HERE, name),
                            os.path.join(self.repo, "scripts", "ci", name))
        self.write("demo/queue.go", SOURCE)
        self.write("demo/queue_test.go", SOURCE_TEST)
        self.write("docs/fork/lx-residual-bug-sweep.md", DOC)
        self.write("demo2/other.go", OTHER_SOURCE)
        self.write("files.txt", "one file\n")
        sh(["git", "init", "-q", "-b", "testing"], self.repo)
        self.commit("initial tree")
        head = sh(["git", "rev-parse", "HEAD"], self.repo).stdout.strip()
        self.write("docs/fork/upstream-retirement-unmarked.tsv",
                   UNMARKED_TSV.format(group="g1", commit=head))
        self.write("docs/fork/upstream-retirement-unmarked.md",
                   UNMARKED_PAGE.format(commits=1, groups=1))
        self.commit("history index")

    def write(self, relative, text):
        path = os.path.join(self.repo, relative)
        with open(path, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(text)

    def read(self, relative):
        with open(os.path.join(self.repo, relative), "r", encoding="utf-8", newline="") as handle:
            return handle.read()

    def commit(self, message):
        sh(["git", "add", "-A"], self.repo)
        sh(["git", "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid",
            "commit", "-q", "-m", message], self.repo)

    def generate(self, expect=0):
        result = subprocess.run([PYTHON, os.path.join("scripts", "ci", TOOLS[0]), "--write"],
                                cwd=self.repo, capture_output=True, text=True)
        if expect is not None and result.returncode != expect:
            print("      stdout: %s" % result.stdout.strip()[:2000])
            print("      stderr: %s" % result.stderr.strip()[:1000])
        return result

    def gate(self, expect=0):
        result = subprocess.run([PYTHON, os.path.join("scripts", "ci", TOOLS[1]),
                                 "--repo", self.repo], capture_output=True, text=True)
        if expect is not None and result.returncode != expect:
            print("      exit: %d (expected %d)" % (result.returncode, expect))
            print("      stdout: %s" % result.stdout.strip()[:3000])
            print("      stderr: %s" % result.stderr.strip()[:1000])
        return result

    def tsv_lines(self):
        return self.read("docs/fork/upstream-retirement-batch2.tsv").splitlines()

    def rewrite_tsv(self, mutate):
        lines = self.tsv_lines()
        header = lines[0].split("\t")
        rows = [line.split("\t") for line in lines[1:]]
        mutate(header, rows)
        text = "\n".join("\t".join(row) for row in [header] + rows) + "\n"
        self.write("docs/fork/upstream-retirement-batch2.tsv", text)

    def cleanup(self):
        force_rmtree(self.dir)


def case_generated_ledger_passes():
    fixture = Fixture()
    try:
        fixture.generate()
        result = fixture.gate(expect=0)
        check("generated: the generated ledger holds against the tree",
              "PASS" in result.stdout, result.stdout[-500:])
        rows = fixture.tsv_lines()
        check("generated: three rows for three marked groups", len(rows) - 1 == 3,
              "\n".join(rows)[:600])
        check("generated: the analysis row is ANALYSIS and the code rows are HOLD",
              "ANALYSIS" in "\n".join(rows) and "HOLD" in "\n".join(rows))
    finally:
        fixture.cleanup()


def case_marker_line_moved_fails():
    """A marker that shifted lines without a regeneration: the recorded line is now another line.

    This is the daily reality - someone adds a comment above the marker, the ledger still points at
    the old line number, and the row's "trigger" becomes unrelated prose. The gate has to see it
    before a reviewer relies on that row.
    """
    fixture = Fixture()
    try:
        fixture.generate()
        text = fixture.read("demo/queue.go")
        fixture.write("demo/queue.go", "// A new comment shifts every line below it.\n" + text)
        fixture.commit("insert a line above the marker")
        result = fixture.gate(expect=1)
        check("drift: a marker line that moved without regeneration fails",
              "no longer carries" in result.stdout and "spec 007" in result.stdout,
              result.stdout[-800:])
    finally:
        fixture.cleanup()


def case_deleted_consumer_fails():
    fixture = Fixture()
    try:
        fixture.generate()
        os.remove(os.path.join(fixture.repo, "demo", "queue_test.go"))
        fixture.commit("delete a consumer")
        result = fixture.gate(expect=1)
        check("consumer: a deleted consumer file fails",
              "is not in the tree" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_tampered_consumer_fails():
    fixture = Fixture()

    def mutate(header, rows):
        for row in rows:
            if row[header.index("spec")] == "007":
                row[header.index("consumer_files")] = "demo/gone.go"

    try:
        fixture.generate()
        fixture.rewrite_tsv(mutate)
        result = fixture.gate(expect=1)
        check("consumer: a consumer file that does not exist fails",
              "demo/gone.go" in result.stdout and "not in the tree" in result.stdout,
              result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_patch_id_mismatch_fails():
    fixture = Fixture()

    def mutate(header, rows):
        for row in rows:
            if row[header.index("spec")] == "007":
                row[header.index("patch_ids")] = "0" * 40

    try:
        fixture.generate()
        fixture.rewrite_tsv(mutate)
        result = fixture.gate(expect=1)
        check("patch-id: a patch-id that belongs to another diff fails",
              "patch-id for" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_retire_ready_without_absorbed_fails():
    fixture = Fixture()

    def mutate(header, rows):
        for row in rows:
            if row[header.index("spec")] == "007":
                row[header.index("disposition")] = "RETIRE_READY"

    try:
        fixture.generate()
        fixture.rewrite_tsv(mutate)
        result = fixture.gate(expect=1)
        check("disposition: RETIRE_READY over a not-comparable status fails",
              "RETIRE_READY without an absorbed" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_analysis_over_code_footprint_fails():
    fixture = Fixture()

    def mutate(header, rows):
        for row in rows:
            if row[header.index("spec")] == "007":
                row[header.index("disposition")] = "ANALYSIS"

    try:
        fixture.generate()
        fixture.rewrite_tsv(mutate)
        result = fixture.gate(expect=1)
        check("disposition: ANALYSIS over a code footprint fails",
              "ANALYSIS over a code footprint" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_precise_regression_without_the_marker_fails():
    fixture = Fixture()
    try:
        fixture.generate()
        # Point the precise regression at a test that does not carry SPEC 007.
        fixture.write("demo/other_test.go", "package demo\n\n// TestNothing names no patch.\n"
                                            "func TestNothing(t *testing.T) {}\n")
        fixture.commit("add a test that names no patch")

        def mutate(header, rows):
            for row in rows:
                if row[header.index("spec")] == "007":
                    row[header.index("regression_exact")] = "demo/other_test.go"

        fixture.rewrite_tsv(mutate)
        result = fixture.gate(expect=1)
        check("regression: a precise regression that does not name the patch fails",
              "does not carry" in result.stdout, result.stdout[-800:])
    finally:
        fixture.cleanup()


def case_unmarked_row_outside_the_branch_fails():
    fixture = Fixture()
    try:
        fixture.generate()
        text = fixture.read("docs/fork/upstream-retirement-unmarked.tsv")
        fixture.write("docs/fork/upstream-retirement-unmarked.tsv",
                      text.replace("REVIEW", "REVIEW").replace("\t", "\t", 1)[:0] + text)
        lines = text.splitlines()
        fields = lines[1].split("\t")
        fields[1] = "f" * 40
        lines[1] = "\t".join(fields)
        fixture.write("docs/fork/upstream-retirement-unmarked.tsv", "\n".join(lines) + "\n")
        result = fixture.gate(expect=1)
        check("history: a row outside the branch fails",
              "not on this branch" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_unmarked_page_number_drift_fails():
    fixture = Fixture()
    try:
        fixture.generate()
        lines = fixture.read("docs/fork/upstream-retirement-unmarked.md").replace(
            "**1**", "**99**")
        fixture.write("docs/fork/upstream-retirement-unmarked.md", lines)
        result = fixture.gate(expect=1)
        check("history: a page whose count disagrees with its file fails",
              "the page states" in result.stdout, result.stdout[-800:])
    finally:
        fixture.cleanup()


def case_missing_ledger_is_an_input_error():
    fixture = Fixture()
    try:
        fixture.generate()
        os.remove(os.path.join(fixture.repo, "docs", "fork",
                               "upstream-retirement-batch2.tsv"))
        result = fixture.gate(expect=2)
        check("input: a missing ledger is exit 2, not a failed claim",
              result.returncode == 2 and "ERROR" in result.stderr,
              "returncode=%d stderr=%s" % (result.returncode, result.stderr[-300:]))
    finally:
        fixture.cleanup()


def case_page_drift_fails():
    fixture = Fixture()
    try:
        fixture.generate()
        page = fixture.read("docs/fork/upstream-retirement-batch2.md")
        fixture.write("docs/fork/upstream-retirement-batch2.md",
                      page.replace("## Mechanical result", "## Mechanical result (edited)"))
        result = fixture.gate(expect=1)
        check("drift: a hand-edited page fails",
              "no longer match the tree" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def main():
    cases = [
        case_generated_ledger_passes,
        case_marker_line_moved_fails,
        case_deleted_consumer_fails,
        case_tampered_consumer_fails,
        case_patch_id_mismatch_fails,
        case_retire_ready_without_absorbed_fails,
        case_analysis_over_code_footprint_fails,
        case_precise_regression_without_the_marker_fails,
        case_unmarked_row_outside_the_branch_fails,
        case_unmarked_page_number_drift_fails,
        case_missing_ledger_is_an_input_error,
        case_page_drift_fails,
    ]
    for case in cases:
        print("--- %s" % case.__name__)
        try:
            case()
        except Exception as error:  # noqa: BLE001 - a case that raises is a failed case
            check(case.__name__ + " (raised)", False, "%s: %s" % (type(error).__name__, error))
    print()
    print("%d passed, %d failed" % (PASSED, FAILED))
    return 1 if FAILED else 0


if __name__ == "__main__":
    sys.exit(main())
