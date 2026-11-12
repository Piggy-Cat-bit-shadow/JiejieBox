#!/usr/bin/env python3
"""Behavioural tests for scripts/ci/check-upstream-retirement.py.

Each case builds a throwaway repository with the real generator and gate, a few marked commits, an
upstream history and batch 1's recorded pair file, then mutates ONE thing and asserts the gate
notices. The mutations are the failure modes of a maintenance ledger: a consumer file that was
deleted, a marker line that shifted, a patch-id from another diff, a `duplicate-of-upstream` the
recorded pairs do not support, a pair measured outside the recorded upstream tip, a disposition
that contradicts its upstream status, a precise regression that does not name the patch, an
unmarked index naming commits outside the branch, and a page whose numbers drifted.

Usage: scripts/ci/test-check-upstream-retirement.py
"""

import json
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
    result = subprocess.run(args, cwd=cwd, capture_output=True, text=True, encoding="utf-8",
                            errors="replace")
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


ALPHA = """package alpha

// SPEC 013 a fork-only patch on the same line upstream also has a file for.
func Alpha() int { return 13 }
"""

BETA = """package beta

// SPEC 007 the fork-only patch: upstream carries neither the commit nor the diff.
func Beta() int { return 7 }
"""

BETA_TEST = """package beta

// TestBetaHandsOver is the SPEC 007 regression.
func TestBetaHandsOver(t *testing.T) {}
"""

GAMMA = """package gamma

// SPEC 011 the duplicated patch: upstream ships the same diff under another SHA.
func Gamma() int { return 11 }
"""

GAMMA_TEST = """package gamma

// TestGammaIsOwned is the SPEC 011 regression.
func TestGammaIsOwned(t *testing.T) {}
"""

DOC = """# Residual sweep

## SPEC 009: why the sweep changed no public signature

Nothing to retire; this is the record.
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
"""


class Fixture:
    """A miniature fork, its upstream history, and batch 1's recorded measurement."""

    def __init__(self):
        self.dir = tempfile.mkdtemp(prefix="retire-")
        self.repo = os.path.join(self.dir, "repo")
        os.makedirs(os.path.join(self.repo, "scripts", "ci"))
        os.makedirs(os.path.join(self.repo, "docs", "fork"))
        for name in TOOLS:
            shutil.copyfile(os.path.join(HERE, name),
                            os.path.join(self.repo, "scripts", "ci", name))
        sh(["git", "init", "-q", "-b", "testing"], self.repo)
        self.write("alpha/alpha.go", ALPHA)
        self.commit("alpha: a fork-only patch")
        self.write("beta/beta.go", BETA)
        self.write("beta/beta_test.go", BETA_TEST)
        self.commit("beta: another fork-only patch")
        self.write("gamma/gamma.go", GAMMA)
        self.write("gamma/gamma_test.go", GAMMA_TEST)
        self.gamma_commit = self.commit("gamma: the duplicated patch")
        self.write("docs/fork/lx-residual-bug-sweep.md", DOC)
        self.write("files.txt", "one file\n")
        self.commit("docs: the analysis record")
        self.write("docs/fork/upstream-retirement-unmarked.tsv",
                   UNMARKED_TSV.format(group="g1", commit=self.head))
        self.write("docs/fork/upstream-retirement-unmarked.md",
                   UNMARKED_PAGE.format(commits=1, groups=1))
        self.commit("history index")
        # Batch 1's population: every commit above.
        self.population = self.head

        # The upstream history: a different line whose gamma commit carries the gamma DIFF.
        sh(["git", "checkout", "-q", "--orphan", "upstream-work"], self.repo)
        self.write("alpha/alpha.go", ALPHA)
        self.commit("upstream: alpha arrives from upstream")
        self.write("gamma/gamma.go", GAMMA)
        self.write("gamma/gamma_test.go", GAMMA_TEST)
        self.upstream_duplicate = self.commit("upstream: gamma under another SHA")
        self.write("upstream-only.txt", "upstream work\n")
        self.upstream_tip = self.commit("upstream: unrelated tail")
        sh(["git", "checkout", "-q", "testing"], self.repo)
        self.write_pairs()

    # -- plumbing ---------------------------------------------------------------

    def write(self, relative, text):
        path = os.path.join(self.repo, relative)
        os.makedirs(os.path.dirname(path) or ".", exist_ok=True)
        with open(path, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(text)

    def read(self, relative):
        with open(os.path.join(self.repo, relative), "r", encoding="utf-8") as handle:
            return handle.read()

    def commit(self, message):
        sh(["git", "add", "-A"], self.repo)
        sh(["git", "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid",
            "commit", "-q", "--allow-empty", "-m", message], self.repo)
        return self.head

    @property
    def head(self):
        return sh(["git", "rev-parse", "HEAD"], self.repo).stdout.strip()

    def patch_id(self, commit):
        diff = sh(["git", "show", "--no-color", "--format=", commit], self.repo).stdout
        out = subprocess.run(["git", "patch-id", "--stable"], cwd=self.repo, input=diff,
                             capture_output=True, text=True, encoding="utf-8").stdout.split()
        return out[0] if out else ""

    def write_pairs(self, tip=None, population=None, pairs=None):
        tip = tip or self.upstream_tip
        population = population or self.population
        pairs = pairs if pairs is not None else [(self.gamma_commit, self.upstream_duplicate)]
        with open(os.path.join(self.repo, "docs", "fork", "upstream-retirement-batch1.tsv"),
                  "w", encoding="utf-8", newline="\n") as handle:
            handle.write("fork_commit\tupstream_commit\n")
            for fork, upstream in pairs:
                handle.write("%s\t%s\n" % (fork, upstream))
        with open(os.path.join(self.repo, "docs", "fork",
                               "upstream-retirement-batch1.meta.json"),
                  "w", encoding="utf-8", newline="\n") as handle:
            json.dump({"schema": 1, "upstream_tip": tip, "population_commit": population,
                       "population": "every non-merge commit reachable from population_commit",
                       "method": "git patch-id --stable equality",
                       "recorded_by": "fixture batch 1", "pairs": len(pairs),
                       "generated_at": "2026-11-13",
                       "note": "absent fork commits were measured at upstream_tip"}, handle,
                      indent=2)
            handle.write("\n")

    def generate(self, expect=0):
        result = subprocess.run([PYTHON, os.path.join("scripts", "ci", TOOLS[0]), "--write"],
                                cwd=self.repo, capture_output=True, text=True, encoding="utf-8",
                                errors="replace")
        if expect is not None and result.returncode != expect:
            print("      stdout: %s" % result.stdout.strip()[:2000])
            print("      stderr: %s" % result.stderr.strip()[:1000])
        return result

    def gate(self, expect=0):
        result = subprocess.run([PYTHON, os.path.join("scripts", "ci", TOOLS[1]),
                                 "--repo", self.repo], capture_output=True, text=True,
                                encoding="utf-8", errors="replace")
        if expect is not None and result.returncode != expect:
            print("      exit: %d (expected %d)" % (result.returncode, expect))
            print("      stdout: %s" % result.stdout.strip()[:3000])
            print("      stderr: %s" % result.stderr.strip()[:1000])
        return result

    def rows(self):
        lines = self.read("docs/fork/upstream-retirement-batch2.tsv").splitlines()
        header = lines[0].split("\t")
        return header, [dict(zip(header, line.split("\t"))) for line in lines[1:]]

    def rewrite_tsv(self, mutate):
        header, rows = self.rows()
        mutate(header, rows)
        text = "\t".join(header) + "\n"
        for row in rows:
            text += "\t".join(row[name] for name in header) + "\n"
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
        _, rows = fixture.rows()
        statuses = {row["spec"]: row["upstream_status"] for row in rows}
        check("generated: the recorded pair becomes duplicate-of-upstream, everything else is "
              "measured fork-only",
              statuses.get("011") == "duplicate-of-upstream"
              and statuses.get("007") == "fork-only"
              and statuses.get("013") == "fork-only"
              and statuses.get("009") == "fork-only", json.dumps(statuses))
        dispositions = {row["spec"]: row["disposition"] for row in rows}
        check("generated: only the duplicated patch is retire-ready",
              dispositions.get("011") == "RETIRE_READY"
              and dispositions.get("007") == "HOLD"
              and dispositions.get("009") == "ANALYSIS", json.dumps(dispositions))
        duplicate = [row for row in rows if row["spec"] == "011"][0]
        check("generated: the duplicate names the upstream commit that carries it",
              duplicate["upstream_commit"] == fixture.upstream_duplicate,
              duplicate["upstream_commit"])
    finally:
        fixture.cleanup()


def case_duplicate_without_recorded_pair_fails():
    fixture = Fixture()

    def mutate(header, rows):
        for row in rows:
            if row["spec"] == "011":
                row["upstream_commit"] = fixture.upstream_tip

    try:
        fixture.generate()
        fixture.rewrite_tsv(mutate)
        result = fixture.gate(expect=1)
        check("duplicate: an upstream commit batch 1 did not record fails",
              "did not record" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_pair_outside_the_recorded_tip_fails():
    fixture = Fixture()
    try:
        fixture.generate()
        fixture.write_pairs(pairs=[(fixture.gamma_commit, fixture.population)])
        result = fixture.gate(expect=1)
        check("pairs: an upstream commit outside the recorded tip fails",
              "not reachable from the recorded tip" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_pairs_meta_without_criterion_fails():
    fixture = Fixture()
    try:
        fixture.generate()
        self_meta = json.loads(fixture.read("docs/fork/upstream-retirement-batch1.meta.json"))
        self_meta["method"] = "somebody looked at it"
        fixture.write("docs/fork/upstream-retirement-batch1.meta.json",
                      json.dumps(self_meta, indent=2) + "\n")
        result = fixture.gate(expect=1)
        check("pairs: metadata without the patch-id criterion fails",
              "does not name the patch-id criterion" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_fork_only_outside_the_population_fails():
    fixture = Fixture()
    try:
        fixture.generate()
        # A commit made AFTER the recorded population: the measurement cannot cover it, so a
        # `fork-only` claim about it is unmeasured rather than false.
        fixture.write("late/late.go", "package late\n\nfunc Late() int { return 1 }\n")
        late = fixture.commit("late: after the recorded population")

        def mutate(header, rows):
            for row in rows:
                if row["spec"] == "007":
                    row["commit_shas"] = late
                    row["patch_ids"] = fixture.patch_id(late)

        fixture.rewrite_tsv(mutate)
        result = fixture.gate(expect=1)
        check("population: fork-only for a commit outside the measured population fails",
              "outside the recorded measurement population" in result.stdout,
              result.stdout[-800:])
    finally:
        fixture.cleanup()


def case_not_comparable_while_covered_fails():
    fixture = Fixture()

    def mutate(header, rows):
        for row in rows:
            if row["spec"] == "007":
                row["upstream_status"] = "not-comparable"
                row["retirement_condition"] = "upstream equivalence is UNMEASURED here"

    try:
        fixture.generate()
        fixture.rewrite_tsv(mutate)
        result = fixture.gate(expect=1)
        check("status: not-comparable although the measurement covers it fails",
              "regenerate the index" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_moved_marker_line_fails():
    fixture = Fixture()
    try:
        fixture.generate()
        text = fixture.read("beta/beta.go")
        fixture.write("beta/beta.go", "// A new comment shifts every line below it.\n" + text)
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
        os.remove(os.path.join(fixture.repo, "beta", "beta_test.go"))
        fixture.commit("delete a consumer")
        result = fixture.gate(expect=1)
        check("consumer: a deleted consumer file fails",
              "is not in the tree" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_patch_id_mismatch_fails():
    fixture = Fixture()

    def mutate(header, rows):
        for row in rows:
            if row["spec"] == "007":
                row["patch_ids"] = "0" * 40

    try:
        fixture.generate()
        fixture.rewrite_tsv(mutate)
        result = fixture.gate(expect=1)
        check("patch-id: a patch-id that belongs to another diff fails",
              "patch-id for" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_retire_ready_on_fork_only_fails():
    fixture = Fixture()

    def mutate(header, rows):
        for row in rows:
            if row["spec"] == "007":
                row["disposition"] = "RETIRE_READY"

    try:
        fixture.generate()
        fixture.rewrite_tsv(mutate)
        result = fixture.gate(expect=1)
        check("disposition: RETIRE_READY over a fork-only patch fails",
              "RETIRE_READY without an upstream" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_hold_on_duplicate_fails():
    fixture = Fixture()

    def mutate(header, rows):
        for row in rows:
            if row["spec"] == "011":
                row["disposition"] = "HOLD"

    try:
        fixture.generate()
        fixture.rewrite_tsv(mutate)
        result = fixture.gate(expect=1)
        check("disposition: HOLD although upstream ships the diff fails",
              "HOLD although upstream" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_analysis_over_code_footprint_fails():
    fixture = Fixture()

    def mutate(header, rows):
        for row in rows:
            if row["spec"] == "007":
                row["disposition"] = "ANALYSIS"

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
        fixture.write("beta/other_test.go", "package beta\n\n// TestNothing names no patch.\n"
                                            "func TestNothing(t *testing.T) {}\n")
        fixture.commit("add a test that names no patch")

        def mutate(header, rows):
            for row in rows:
                if row["spec"] == "007":
                    row["regression_exact"] = "beta/other_test.go"

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
        lines = fixture.read("docs/fork/upstream-retirement-unmarked.tsv").splitlines()
        fields = lines[1].split("\t")
        fields[1] = "f" * 40
        lines[1] = "\t".join(fields)
        fixture.write("docs/fork/upstream-retirement-unmarked.tsv", "\n".join(lines) + "\n")
        result = fixture.gate(expect=1)
        check("history: a row outside the branch fails",
              "not on this branch" in result.stdout, result.stdout[-700:])
    finally:
        fixture.cleanup()


def case_page_number_drift_fails():
    fixture = Fixture()
    try:
        fixture.generate()
        page = fixture.read("docs/fork/upstream-retirement-unmarked.md").replace("**1**", "**99**")
        fixture.write("docs/fork/upstream-retirement-unmarked.md", page)
        result = fixture.gate(expect=1)
        check("history: a page whose count disagrees with its file fails",
              "the page states" in result.stdout, result.stdout[-800:])
    finally:
        fixture.cleanup()


def case_hand_edited_page_fails():
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


def main():
    cases = [
        case_generated_ledger_passes,
        case_duplicate_without_recorded_pair_fails,
        case_pair_outside_the_recorded_tip_fails,
        case_pairs_meta_without_criterion_fails,
        case_fork_only_outside_the_population_fails,
        case_not_comparable_while_covered_fails,
        case_moved_marker_line_fails,
        case_deleted_consumer_fails,
        case_patch_id_mismatch_fails,
        case_retire_ready_on_fork_only_fails,
        case_hold_on_duplicate_fails,
        case_analysis_over_code_footprint_fails,
        case_precise_regression_without_the_marker_fails,
        case_unmarked_row_outside_the_branch_fails,
        case_page_number_drift_fails,
        case_hand_edited_page_fails,
        case_missing_ledger_is_an_input_error,
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
