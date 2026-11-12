#!/usr/bin/env python3
"""Behavioural tests for scripts/ci/check-pin-runs.py.

Every case builds a throwaway repository with real `git`, a real source lock and a real run
record, then drives the gate through its CLI. The negatives are the point: a binding gate that
passes on a moved pin, on a cancelled run, on a run of a commit the clone does not have, on a
diverged fork, or on a compensating job that no longer checks the submodule out would be worse
than no gate at all, because it would turn prose about evidence into a green check.

Usage: scripts/ci/test-check-pin-runs.py
"""

import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-pin-runs.py")
PYTHON = sys.executable or "python3"

PASSED = 0
FAILED = 0

PIN_ONE = "1" * 12
PIN_NATIVE = "2" * 12
PIN_CLIENT = "3" * 12
PIN_NESTED = "4" * 12


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


def manifest():
    return {
        "schema": 1,
        "go_modules": [
            {"module": "example.com/one", "slug": "owner/one", "branch": "main",
             "policy": "update"},
        ],
        "cronet_native": {"module_prefix": "example.com/native/", "slug": "owner/native",
                          "expected_modules": 2, "policy": "controlled"},
        "clients": [
            {"path": "clients/app", "slug": "owner/app", "branch": "main", "policy": "update"},
        ],
        "nested": [
            {"parent": "clients/app", "path": "dashboard", "slug": "owner/dash",
             "branch": "main", "policy": "report"},
        ],
    }


def lock(client_pin=PIN_CLIENT):
    return {
        "schema": 1,
        "release_version": "9.9.9",
        "go_modules": [
            {"module": "example.com/one", "pinned": PIN_ONE, "target": "github.com/owner/one",
             "version": "v0.0.0-20260101000000-" + PIN_ONE},
        ],
        "cronet_native": {"modules": 2, "policy": "controlled", "shas": [PIN_NATIVE]},
        "clients": [{"path": "clients/app", "pinned": client_pin}],
        "nested": [{"parent": "clients/app", "path": "dashboard", "pinned": PIN_NESTED}],
    }


def observed_tips(dash_relation="behind"):
    return [
        {"repo": "owner/one", "branch": "main", "tip": PIN_ONE, "pin": PIN_ONE,
         "relation": "identical", "policy": "update", "observed_at": "2026-11-12"},
        {"repo": "owner/native", "branch": None, "tip": PIN_NATIVE, "pin": PIN_NATIVE,
         "relation": "identical", "policy": "controlled", "observed_at": "2026-11-12"},
        {"repo": "owner/app", "branch": "main", "tip": PIN_CLIENT, "pin": PIN_CLIENT,
         "relation": "identical", "policy": "update", "observed_at": "2026-11-12"},
        {"repo": "owner/dash", "branch": "main", "tip": "5" * 12, "pin": PIN_NESTED,
         "relation": dash_relation, "policy": "report", "observed_at": "2026-11-12"},
    ]


WORKFLOW_SUBMODULES = """name: Kernel release
on:
  workflow_dispatch:
jobs:
  freeze:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          submodules: recursive
  verify:
    runs-on: ubuntu-latest
    steps:
      - run: echo verify
"""

WORKFLOW_NO_SUBMODULES = """name: Kernel release
on:
  workflow_dispatch:
jobs:
  freeze:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
  verify:
    runs-on: ubuntu-latest
    steps:
      - run: echo verify
"""


class Fixture:
    def __init__(self, workflow=WORKFLOW_SUBMODULES):
        self.dir = tempfile.mkdtemp(prefix="pinruns-")
        self.repo = os.path.join(self.dir, "repo")
        os.makedirs(os.path.join(self.repo, "release"))
        os.makedirs(os.path.join(self.repo, ".github", "workflows"))
        self._write("release/dependencies.json", manifest())
        self._write("release/source-lock.json", lock())
        self._write(".github/workflows/kernel-release.yml", workflow, json_file=False)
        self._write("README.md", "fixture\n", json_file=False)
        sh(["git", "init", "-q", "-b", "testing"], self.repo)
        self.commit("initial")

    def _write(self, relative, data, json_file=True):
        path = os.path.join(self.repo, relative)
        os.makedirs(os.path.dirname(path) or ".", exist_ok=True)
        text = json.dumps(data, indent=2, sort_keys=True) + "\n" if json_file else data
        with open(path, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(text)

    def commit(self, message):
        sh(["git", "add", "-A"], self.repo)
        sh(["git", "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid",
            "commit", "-q", "-m", message], self.repo)
        return sh(["git", "rev-parse", "HEAD"], self.repo).stdout.strip()

    def write_runs(self, runs, tips=None, gaps=None):
        self._write("release/pin-runs.json", {
            "schema": 1,
            "recorded_at": "2026-11-12",
            "parent_runs": runs,
            "child_runs": [],
            "observed_tips": observed_tips() if tips is None else tips,
            "not_covered": gaps if gaps is not None else [
                {"pin": "nested:clients/app:dashboard",
                 "why": "the push gate checks out no submodules",
                 "checked_by": {"workflow": "kernel-release.yml", "job": "freeze"}},
            ],
        })

    def run(self, *args, expect=0):
        result = subprocess.run([PYTHON, TOOL, "--repo", self.repo] + list(args),
                                capture_output=True, text=True)
        if expect is not None and result.returncode != expect:
            print("      command: %s" % " ".join(args))
            print("      exit   : %d (expected %d)" % (result.returncode, expect))
            print("      stdout : %s" % result.stdout.strip()[:3000])
            print("      stderr : %s" % result.stderr.strip()[:1500])
        return result

    def head(self):
        return sh(["git", "rev-parse", "HEAD"], self.repo).stdout.strip()

    def compare_path(self):
        return os.path.join(self.repo, "build", "pin-runs.json")

    def cleanup(self):
        force_rmtree(self.dir)


def parent_run(sha, run_id=1, conclusion="success", workflow="Verify"):
    return {"repo": "owner/kernel", "workflow": workflow, "run_id": run_id, "head_sha": sha,
            "event": "push", "conclusion": conclusion,
            "url": "https://example.invalid/runs/%d" % run_id}


def case_covered_pin_set_passes():
    fixture = Fixture()
    try:
        fixture.write_runs([parent_run(fixture.head(), 11)])
        result = fixture.run("--json", "build/pin-runs.json", expect=0)
        check("covered: a run at a commit carrying these pins passes", "PASS" in result.stdout,
              result.stdout[-400:])
        with open(fixture.compare_path(), "r", encoding="utf-8") as handle:
            report = json.load(handle)
        check("covered: the report names every pin and its run",
              len(report["pins"]) == 4 and all(row["covered_by"] == [11] for row in report["pins"]),
              json.dumps(report["pins"]))
        check("covered: the declared gap is carried into the report",
              report["pins"][3].get("gap", {}).get("checked_by", {}).get("job") == "freeze",
              json.dumps(report["pins"][3]))
    finally:
        fixture.cleanup()


def case_moved_pin_without_a_run_fails():
    fixture = Fixture()
    try:
        fixture.write_runs([parent_run(fixture.head(), 12)])
        moved = lock(client_pin="6" * 12)
        fixture._write("release/source-lock.json", moved)
        fixture.commit("move a client pin without a run")
        result = fixture.run(expect=1)
        check("moved-pin: a pin moved with no new run fails closed",
              "no recorded run carries the current pin set" in result.stdout,
              result.stdout[-500:])
    finally:
        fixture.cleanup()


def case_cancelled_run_is_not_evidence():
    fixture = Fixture()
    try:
        fixture.write_runs([parent_run(fixture.head(), 13, conclusion="cancelled")])
        result = fixture.run(expect=1)
        check("cancelled: a cancelled run is not a pass",
              "cancelled" in result.stdout and "FAILED" in result.stdout, result.stdout[-500:])
    finally:
        fixture.cleanup()


def case_unreachable_run_commit_fails():
    fixture = Fixture()
    try:
        fixture.write_runs([parent_run("f" * 40, 14)])
        result = fixture.run(expect=1)
        check("unreachable: a run commit the clone does not contain fails",
              "does not contain" in result.stdout, result.stdout[-500:])
    finally:
        fixture.cleanup()


def case_duplicate_run_id_fails():
    fixture = Fixture()
    try:
        sha = fixture.head()
        fixture.write_runs([parent_run(sha, 15), parent_run(sha, 15, workflow="Mobile")])
        result = fixture.run(expect=1)
        check("duplicate: one run cannot be cited twice",
              "recorded twice" in result.stdout, result.stdout[-500:])
    finally:
        fixture.cleanup()


def case_diverged_fork_fails():
    fixture = Fixture()
    try:
        fixture.write_runs([parent_run(fixture.head(), 16)],
                           tips=observed_tips(dash_relation="diverged"))
        result = fixture.run(expect=1)
        check("diverged: a diverged fork stops the gate",
              "diverged" in result.stdout, result.stdout[-500:])
    finally:
        fixture.cleanup()


def case_missing_observation_fails():
    fixture = Fixture()
    try:
        tips = [row for row in observed_tips() if row["repo"] != "owner/one"]
        fixture.write_runs([parent_run(fixture.head(), 17)], tips=tips)
        result = fixture.run(expect=1)
        check("observation: a repository with no recorded tip fails",
              "owner/one" in result.stdout and "no branch tip is recorded" in result.stdout,
              result.stdout[-500:])
    finally:
        fixture.cleanup()


def case_identical_claim_that_disagrees_fails():
    fixture = Fixture()
    try:
        tips = observed_tips()
        tips[0] = dict(tips[0], tip="7" * 12, relation="identical")
        fixture.write_runs([parent_run(fixture.head(), 18)], tips=tips)
        result = fixture.run(expect=1)
        check("identical: pin != tip under a claimed identical fails",
              "identical" in result.stdout and "differ" in result.stdout, result.stdout[-500:])
    finally:
        fixture.cleanup()


def case_gap_points_at_a_job_that_cannot_read_it():
    fixture = Fixture(workflow=WORKFLOW_NO_SUBMODULES)
    try:
        fixture.write_runs([parent_run(fixture.head(), 19)])
        result = fixture.run(expect=1)
        check("gap: a compensating job without submodules fails",
              "does not check out" in result.stdout, result.stdout[-500:])
    finally:
        fixture.cleanup()


def case_gap_points_at_a_job_that_does_not_exist():
    fixture = Fixture()
    try:
        fixture.write_runs([parent_run(fixture.head(), 20)], gaps=[
            {"pin": "nested:clients/app:dashboard", "why": "not read here",
             "checked_by": {"workflow": "kernel-release.yml", "job": "no-such-job"}}])
        result = fixture.run(expect=1)
        check("gap: a compensating job that does not exist fails",
              "does not exist" in result.stdout, result.stdout[-500:])
    finally:
        fixture.cleanup()


def case_gap_must_name_a_real_pin_and_a_reason():
    fixture = Fixture()
    try:
        fixture.write_runs([parent_run(fixture.head(), 21)], gaps=[
            {"pin": "nested:clients/app:not-a-pin", "why": "x",
             "checked_by": {"workflow": "kernel-release.yml", "job": "freeze"}},
            {"pin": "module:example.com/one",
             "checked_by": {"workflow": "kernel-release.yml", "job": "freeze"}},
        ])
        result = fixture.run(expect=1)
        check("gap: an unknown pin is rejected",
              "is not a pin in the lock" in result.stdout, result.stdout[-600:])
        check("gap: a pin with no reason is rejected",
              "states no reason" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_undeclared_pin_fails():
    fixture = Fixture()
    try:
        data = lock()
        data["go_modules"].append({"module": "example.com/two", "pinned": "8" * 12,
                                   "target": "github.com/owner/two",
                                   "version": "v0.0.0-20260101000000-" + "8" * 12})
        fixture._write("release/source-lock.json", data)
        fixture.commit("add a pin the manifest does not describe")
        fixture.write_runs([parent_run(fixture.head(), 25)])
        result = fixture.run(expect=1)
        check("undeclared: a pin outside the manifest fails",
              "does not describe this pin" in result.stdout, result.stdout[-500:])
    finally:
        fixture.cleanup()


def case_moved_gitlink_and_stale_observation_fails():
    """A client gitlink that moved while the recorded observation still names the old pin.

    The run record is refreshed here on purpose, so the digest check is satisfied and the only
    thing left to catch the move is the observation: the branch tip this project measured was
    about a different revision than the one the lock now ships. That is a real failure mode -
    `clients/desktop` sat one commit behind exactly this way before v0.1.33 - and it must not
    pass just because a run exists.
    """
    fixture = Fixture()
    try:
        data = lock()
        data["clients"][0]["pinned"] = "9" * 12
        fixture._write("release/source-lock.json", data)
        fixture.commit("client gitlink moved without a fresh observation")
        fixture.write_runs([parent_run(fixture.head(), 26)])
        result = fixture.run(expect=1)
        check("client: a moved gitlink with a stale observation fails",
              "owner/app" in result.stdout and "the lock now pins" in result.stdout,
              result.stdout[-500:])
        tips = observed_tips()
        tips[2] = dict(tips[2], pin="9" * 12, tip="9" * 12)
        fixture.write_runs([parent_run(fixture.head(), 27)], tips=tips)
        result = fixture.run(expect=0)
        check("client: the refreshed observation makes the same lock pass",
              "PASS" in result.stdout, result.stdout[-400:])
    finally:
        fixture.cleanup()


def main():
    cases = [
        case_covered_pin_set_passes,
        case_moved_pin_without_a_run_fails,
        case_cancelled_run_is_not_evidence,
        case_unreachable_run_commit_fails,
        case_duplicate_run_id_fails,
        case_diverged_fork_fails,
        case_missing_observation_fails,
        case_identical_claim_that_disagrees_fails,
        case_gap_points_at_a_job_that_cannot_read_it,
        case_gap_points_at_a_job_that_does_not_exist,
        case_gap_must_name_a_real_pin_and_a_reason,
        case_undeclared_pin_fails,
        case_moved_gitlink_and_stale_observation_fails,
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
