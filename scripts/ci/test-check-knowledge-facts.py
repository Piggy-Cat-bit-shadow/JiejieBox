#!/usr/bin/env python3
"""Behavioural tests for scripts/ci/check-knowledge-facts.py.

The fixture is a miniature knowledge base with one scoped target, one broad baseline, a report
index and a small coverage matrix. Each case changes exactly one fact and asserts the gate fails:
a duplicated directory row, an unregistered archive, a lock that names a different release, a
current target newer than the tree, a target commit the baseline never records, an undated first
section, a matrix whose count disagrees with its rows, a status outside the vocabulary, and a
`verify-evidence` page that stopped declaring its own staleness.

Usage: scripts/ci/test-check-knowledge-facts.py
"""

import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "check-knowledge-facts.py")
PYTHON = sys.executable or "python3"

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

SCOPED = "a" * 40
BROAD = "b" * 40

README = """# 分支知识库（Fork knowledge base）

## 基线

| | |
|---|---|
| 仓库 | `owner/repo` |
| **当前验收目标（专题）** | 内核 [v{s_version}](https://example.invalid) 剥离后的提交 `{scoped}` |
| **生产基线（广泛快照）** | `{broad}` —— 2026-10-31 校准 |
| 发布基线 | **`v0.1.34`** |

正文引用上游 `v1.15.0-alpha.13` 作为配置语法参考，它不是本仓的发布版本。
"""

BASELINE = """# 基线（Baseline）

## 2026-11-08 发布后专题增量（Scoped refresh）

| 范围 | 固定目标 |
| --- | --- |
| 最近回读的公开正式版 | [v{s_version}](https://example.invalid)，附注标签剥离后的提交 `{scoped}` |
| 全局广泛知识基线 | 仍保留 `{broad}` 的快照 |
"""

REPORTS_README = """# 历史报告

## 目录结构

| 目录 | 内容 |
|---|---|
| `a/` | A 组报告 |
| `b/` | B 组报告 |
| `other-project-learning-and-integration/` | 55 项覆盖账 |

## 报告索引

报告原文不因索引更新而变为当前全量认读；报告与源码不一致时以源码为准，不能直接当作当前源码的结论。

“历史记录（未复核）”是本轮补登记的状态：只声明它在某个时点写成并已归档，没有重新复核。

### a/

| 报告 | 日期 | 内容 | 当时状态 |
|---|---|---|---|
| `one.md` | 2026-01-01 | 第一份 | 历史记录（未复核） |
| `two.md` | 2026-01-02 | 第二份 | 历史记录（未复核） |
"""

MATRIX = """# MASTER_PLAN —— 覆盖矩阵与本轮状态

## {count} 项覆盖矩阵

| ID | 范围 | 主责 | 类型 | 状态 | 证据 / 备注 |
|---|---|---:|---|---|---|
{rows}
"""

VERIFY = """# verify-evidence

Everything here was produced by the code at the commit named in each log's own header. A log whose
header names a different HEAD is stale and must be regenerated rather than trusted.
"""


def matrix_rows(count, status="IMPLEMENTED_VERIFIED"):
    rows = []
    for index in range(count):
        rows.append("| S%02d | 范围 %d | 01 | I | `%s` | 证据 |" % (index + 1, index + 1, status))
    return "\n".join(rows)


class Fixture:
    def __init__(self, s_version="0.1.44", matrix_count=4, matrix_status="IMPLEMENTED_VERIFIED"):
        self.dir = tempfile.mkdtemp(prefix="facts-")
        self.repo = self.dir
        self.write("release/JIEJIE_VERSION", "0.1.45\n")
        self.write("release/source-lock.json",
                   json.dumps({"schema": 1, "release_version": "0.1.45"}) + "\n")
        self.write("KNOWLEDGE/README.md", README.format(s_version=s_version, scoped=SCOPED,
                                                        broad=BROAD))
        self.write("KNOWLEDGE/BASELINE.md", BASELINE.format(s_version=s_version, scoped=SCOPED,
                                                           broad=BROAD))
        self.write("KNOWLEDGE/reports/README.md", REPORTS_README)
        self.write("KNOWLEDGE/reports/a/one.md", "# 一\n")
        self.write("KNOWLEDGE/reports/a/two.md", "# 二\n")
        self.write("KNOWLEDGE/reports/b/three.md", "# 三\n")
        self.write("KNOWLEDGE/reports/other-project-learning-and-integration/MASTER_PLAN.md",
                   MATRIX.format(count=matrix_count,
                                 rows=matrix_rows(matrix_count, matrix_status)))
        self.write("verify-evidence/README.md", VERIFY)

    def write(self, relative, text):
        path = os.path.join(self.repo, relative)
        os.makedirs(os.path.dirname(path) or ".", exist_ok=True)
        with open(path, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(text)

    def read(self, relative):
        with open(os.path.join(self.repo, relative), "r", encoding="utf-8") as handle:
            return handle.read()

    def run(self, expect=0):
        # The documents this gate reads are Chinese, so the child's output is UTF-8. Windows would
        # otherwise decode it with the console code page and fail before any assertion runs.
        env = dict(os.environ, PYTHONIOENCODING="utf-8")
        result = subprocess.run([PYTHON, TOOL, "--repo", self.repo],
                                capture_output=True, text=True, encoding="utf-8",
                                errors="replace", env=env)
        if expect is not None and result.returncode != expect:
            print("      exit: %d (expected %d)" % (result.returncode, expect))
            print("      stdout: %s" % result.stdout.strip()[:3000])
            print("      stderr: %s" % result.stderr.strip()[:1000])
        return result

    def cleanup(self):
        if os.name == "nt":
            for base, _dirs, names in os.walk(self.repo):
                for name in names:
                    try:
                        os.chmod(os.path.join(base, name), stat.S_IWRITE)
                    except OSError:
                        pass
        shutil.rmtree(self.repo, ignore_errors=True)


def case_consistent_entry_points_pass():
    fixture = Fixture()
    try:
        result = fixture.run(expect=0)
        check("baseline: a consistent knowledge base passes",
              "PASS" in result.stdout, result.stdout[-500:])
    finally:
        fixture.cleanup()


def case_duplicate_directory_row_fails():
    fixture = Fixture()
    try:
        fixture.write("KNOWLEDGE/reports/README.md",
                      REPORTS_README.replace("| `b/` | B 组报告 |",
                                             "| `a/` | 又一次 A 组 |\n| `b/` | B 组报告 |"))
        result = fixture.run(expect=1)
        check("duplicate: a directory registered twice fails",
              "registered 2 times" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_unregistered_archive_fails():
    fixture = Fixture()
    try:
        fixture.write("KNOWLEDGE/reports/a/three.md", "# 三\n")
        result = fixture.run(expect=1)
        check("archive: a report missing from its section fails",
              "a/three.md" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_directory_without_a_row_fails():
    fixture = Fixture()
    try:
        fixture.write("KNOWLEDGE/reports/c/four.md", "# 四\n")
        result = fixture.run(expect=1)
        check("directory: a directory with no row fails",
              "c/ has no row" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_section_for_a_missing_directory_fails():
    fixture = Fixture()
    try:
        fixture.write("KNOWLEDGE/reports/README.md",
                      REPORTS_README + "\n### gone/\n\n| 报告 | 日期 | 内容 | 当时状态 |\n"
                                       "|---|---|---|---|\n| `x.md` | 2026-01-01 | x | 历史记录（未复核） |\n")
        result = fixture.run(expect=1)
        check("section: a section for a deleted directory fails",
              "describes a directory that does not exist" in result.stdout,
              result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_lock_version_disagrees_fails():
    fixture = Fixture()
    try:
        fixture.write("release/source-lock.json",
                      json.dumps({"schema": 1, "release_version": "0.1.44"}) + "\n")
        result = fixture.run(expect=1)
        check("lock: a lock that names another release fails",
              "must be regenerated" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_target_newer_than_tree_fails():
    fixture = Fixture(s_version="0.1.99")
    try:
        result = fixture.run(expect=1)
        check("version: a target newer than the tree fails",
              "newer than" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_target_commit_not_in_baseline_fails():
    fixture = Fixture()
    try:
        fixture.write("KNOWLEDGE/BASELINE.md",
                      BASELINE.format(s_version="0.1.44", scoped="c" * 40, broad=BROAD))
        result = fixture.run(expect=1)
        check("target: a target commit the baseline never records fails",
              "is not recorded in" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_undated_first_section_fails():
    fixture = Fixture()
    try:
        fixture.write("KNOWLEDGE/BASELINE.md",
                      BASELINE.replace("## 2026-11-08 发布后专题增量（Scoped refresh）",
                                       "## 发布后专题增量（Scoped refresh）").format(
                          s_version="0.1.44", scoped=SCOPED, broad=BROAD))
        result = fixture.run(expect=1)
        check("baseline: an undated first section fails",
              "has no time point" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_matrix_count_mismatch_fails():
    fixture = Fixture(matrix_count=4)
    try:
        fixture.write("KNOWLEDGE/reports/other-project-learning-and-integration/MASTER_PLAN.md",
                      MATRIX.format(count=5, rows=matrix_rows(4)))
        result = fixture.run(expect=1)
        check("matrix: a count that disagrees with its rows fails",
              "claims 5 items and lists 4" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_matrix_unknown_status_fails():
    fixture = Fixture(matrix_count=3, matrix_status="DONE")
    try:
        result = fixture.run(expect=1)
        check("matrix: a status outside the vocabulary fails",
              "no known status" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_verify_evidence_staleness_rule_missing_fails():
    fixture = Fixture()
    try:
        fixture.write("verify-evidence/README.md",
                      "# verify-evidence\n\nThese logs are curated and always current.\n")
        result = fixture.run(expect=1)
        check("evidence: a page that dropped the staleness rule fails",
              "no longer states" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_missing_release_file_is_an_input_error():
    fixture = Fixture()
    try:
        os.remove(os.path.join(fixture.repo, "release", "JIEJIE_VERSION"))
        result = fixture.run(expect=2)
        check("input: a missing release file is exit 2",
              result.returncode == 2 and "ERROR" in result.stderr,
              "returncode=%d %s" % (result.returncode, result.stderr[-300:]))
    finally:
        fixture.cleanup()


def case_undocumented_status_fails():
    fixture = Fixture()
    try:
        fixture.write("KNOWLEDGE/reports/README.md", REPORTS_README.replace(
            "“历史记录（未复核）”是本轮补登记的状态：只声明它在某个时点写成并已归档，没有重新复核。\n", ""))
        result = fixture.run(expect=1)
        check("status: a status used but never explained fails",
              "never explained" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def main():
    cases = [
        case_consistent_entry_points_pass,
        case_duplicate_directory_row_fails,
        case_unregistered_archive_fails,
        case_directory_without_a_row_fails,
        case_section_for_a_missing_directory_fails,
        case_lock_version_disagrees_fails,
        case_target_newer_than_tree_fails,
        case_target_commit_not_in_baseline_fails,
        case_undated_first_section_fails,
        case_matrix_count_mismatch_fails,
        case_matrix_unknown_status_fails,
        case_verify_evidence_staleness_rule_missing_fails,
        case_missing_release_file_is_an_input_error,
        case_undocumented_status_fails,
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
