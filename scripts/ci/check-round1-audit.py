#!/usr/bin/env python3
"""Round-1 audit consistency gate (with counterexample self-test).

Normal mode validates the facts the closure report depends on:
- 108 conflict paths == the reviewed matrix, no duplicates, category total 108
- merge commit parents and pre-merge/final merge-base semantics
- canonical head == origin/testing, upstream ancestor, behind == 0
- CI-tested head -> final head is docs/audit only (or the audit gate itself)
- source-lock pins and client gitlinks agree with the tree

`--self-test` injects six classes of deliberate errors and requires each one to
be rejected. A gate that cannot fail is not a gate.
"""

import argparse
import csv
import os
import re
import subprocess
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
MERGE = "303fbb47dfdca4520dd201fea8bd48b3db1f1010"
PRE_MERGE = "8eb4395a391b775ad240433a7009f39357c1eba0"
UPSTREAM = "a13dbe1f785f66c86acbf3985c33fdafea0cbfd6"
CI_TESTED = "b473c21f4803c0196e3eb920d530602d5f92867f"
ALLOWED_RESOLUTIONS = {"FORK", "UPSTREAM", "SEMANTIC_FUSION", "GENERATED", "OBSOLETE", "SAME"}
ALLOWED_REVIEW = {"VERIFIED", "RECONSTRUCTED"}
CLIENT_PINS = {
    "clients/apple": "7fce7117b33a38ef4c73c81db67c49f0be7a7dad",
    "clients/android": "ba5c6b161827a830cb7343850bf547156fbfdfee",
    "clients/desktop": "e8b48beadd7d11a9bce46f62e12820f3e3773518",
}
CI_ALLOWED = re.compile(
    r"^(docs/fork/|release/dependency-final-shas\.json$|release/source-lock\.json$|"
    r"0[0-9]_.*\.md$|INTEGRATION-LOG\.md$|CLEANUP.*\.md$|"
    r"scripts/ci/check-round1-audit\.py$)"
)


def git(*args):
    p = subprocess.run(["git", "-C", ROOT, *args], capture_output=True, text=True, encoding="utf-8")
    if p.returncode != 0:
        raise RuntimeError("git %s failed: %s" % (" ".join(args), p.stderr.strip()))
    return p.stdout.strip()


def load_conflicts(list_path, csv_path):
    raw = open(list_path, "rb").read().decode("utf-8-sig")
    original = sorted({line.strip().replace("\\", "/") for line in raw.splitlines() if line.strip()})
    rows = list(csv.DictReader(open(csv_path, encoding="utf-8")))
    return original, rows


def check_conflict_matrix(list_path, csv_path):
    errors = []
    original, rows = load_conflicts(list_path, csv_path)
    paths = [r["path"] for r in rows]
    if len(original) != 108:
        errors.append("original conflict list is %d, want 108" % len(original))
    if len(rows) != 108:
        errors.append("matrix has %d rows, want 108" % len(rows))
    if len(set(paths)) != len(paths):
        errors.append("matrix has duplicate paths")
    if set(paths) != set(original):
        errors.append("matrix paths differ from the original conflict list")
    counts = {}
    for row in rows:
        res = row.get("resolution", "")
        if res not in ALLOWED_RESOLUTIONS:
            errors.append("unknown resolution %r at %s" % (res, row.get("path")))
        if row.get("review_status") not in ALLOWED_REVIEW:
            errors.append("unknown review status %r at %s" % (row.get("review_status"), row.get("path")))
        counts[res] = counts.get(res, 0) + 1
    if sum(counts.values()) != 108:
        errors.append("resolution category sum is %d, want 108" % sum(counts.values()))
    return errors, counts


def check_graph(two_parents, pre_base, final_base):
    errors = []
    if len(two_parents) != 2:
        errors.append("merge commit does not have two parents")
    else:
        fork_parent, upstream_parent = two_parents
        if upstream_parent != UPSTREAM:
            errors.append("merge second parent is %s, want %s" % (upstream_parent, UPSTREAM))
        if git("merge-base", fork_parent, upstream_parent) != PRE_MERGE:
            errors.append("pre-merge merge-base is not %s" % PRE_MERGE)
    if final_base != UPSTREAM:
        errors.append("final merge-base is not the merged upstream head")
    return errors


def check_refs(head, testing, origin_testing, upstream_ref, left, right):
    errors = []
    if testing != head or origin_testing != head:
        errors.append("testing/origin/testing do not point at HEAD")
    if upstream_ref != UPSTREAM:
        errors.append("upstream/testing moved; the observation is now historical")
    if int(right) != 0:
        errors.append("HEAD is %s commits behind upstream/testing" % right)
    if int(left) < 0:
        errors.append("invalid ahead count")
    return errors


def check_ci_set(changed):
    return ["non-docs changes after CI-tested head: %s" % ", ".join(p for p in changed if not CI_ALLOWED.match(p))] if any(not CI_ALLOWED.match(p) for p in changed) else []


def check_gitlinks(pins, lock_text):
    errors = []
    for path, sha in pins.items():
        tree = git("ls-tree", "HEAD", path).split()
        actual = tree[2] if len(tree) > 2 else "-"
        if actual != sha:
            errors.append("%s gitlink is %s, want %s" % (path, actual, sha))
        if sha not in lock_text:
            errors.append("source lock does not record %s" % path)
    return errors



def check_facts(facts_path):
    """Check the stable claims in the single facts file, not the moving HEAD."""
    import json
    errors = []
    if not os.path.exists(facts_path):
        return ["missing facts file: " + facts_path]
    d = json.load(open(facts_path, encoding="utf-8"))
    main = d.get("main", {})
    if main.get("upstream_head") != git("rev-parse", "upstream/testing"):
        errors.append("facts upstream_head does not match upstream/testing")
    if main.get("merge_commit") != MERGE:
        errors.append("facts merge_commit does not match")
    if main.get("merge_parents") != git("show", "-s", "--format=%P", MERGE).split():
        errors.append("facts merge_parents do not match")
    if main.get("final_merge_base_at_observation") != git("merge-base", "HEAD", UPSTREAM):
        errors.append("facts final_merge_base does not match")
    for client in d.get("clients", []):
        path = client.get("path", "")
        sha = client.get("sha", "")
        if path.startswith("clients/") and path.count("/") == 1 and sha:
            tree = git("ls-tree", "HEAD", path).split()
            actual = tree[2] if len(tree) > 2 else "-"
            if actual != sha:
                errors.append("facts %s pin %s != tree %s" % (path, sha, actual))
    return errors


def normal():
    errors = []
    matrix_errors, counts = check_conflict_matrix(
        os.path.join(ROOT, "docs", "fork", "round1-main-merge-conflicts.txt"),
        os.path.join(ROOT, "docs", "fork", "ROUND1-MERGE-CONFLICT-RESOLUTION.csv"),
    )
    errors += matrix_errors

    parents = git("show", "-s", "--format=%P", MERGE).split()
    final_base = git("merge-base", "HEAD", UPSTREAM)
    errors += check_graph(parents, git("merge-base", parents[0], parents[1]) if len(parents) == 2 else "", final_base)

    head = git("rev-parse", "HEAD")
    left, right = git("rev-list", "--left-right", "--count", "HEAD...upstream/testing").split()
    errors += check_refs(head, git("rev-parse", "testing"), git("rev-parse", "origin/testing"), git("rev-parse", "upstream/testing"), left, right)

    changed = git("diff", "--name-only", CI_TESTED + "..HEAD").splitlines()
    errors += check_ci_set(changed)

    lock_text = open(os.path.join(ROOT, "release", "source-lock.json"), encoding="utf-8").read()
    errors += check_gitlinks(CLIENT_PINS, lock_text)
    errors += check_facts(os.path.join(ROOT, "docs", "fork", "ROUND1-FINAL-FACTS.json"))

    print("conflict rows:", len(load_conflicts(
        os.path.join(ROOT, "docs", "fork", "round1-main-merge-conflicts.txt"),
        os.path.join(ROOT, "docs", "fork", "ROUND1-MERGE-CONFLICT-RESOLUTION.csv"))[1]), "categories:", counts)
    print("merge parents:", parents)
    print("head:", head, "ahead/behind:", left, right)
    print("CI change set:", changed)
    if errors:
        for e in errors:
            print("FAIL:", e)
        return 1
    print("PASS: round-1 audit facts are consistent")
    return 0


def self_test():
    list_path = os.path.join(ROOT, "docs", "fork", "round1-main-merge-conflicts.txt")
    csv_path = os.path.join(ROOT, "docs", "fork", "ROUND1-MERGE-CONFLICT-RESOLUTION.csv")
    original, rows = load_conflicts(list_path, csv_path)
    failures = 0

    def expect_error(name, errors):
        nonlocal failures
        if errors:
            print("SELFTEST PASS:", name, "->", errors[0])
        else:
            print("SELFTEST FAIL:", name, "was accepted")
            failures += 1

    # 1 duplicate path
    _, dup_rows = load_conflicts(list_path, csv_path)
    dup_rows = dup_rows + [dup_rows[0]]
    with tempfile.NamedTemporaryFile("w", suffix=".csv", delete=False, encoding="utf-8", newline="") as fh:
        w = csv.DictWriter(fh, fieldnames=rows[0].keys()); w.writeheader(); w.writerows(dup_rows); dup_path = fh.name
    e, _ = check_conflict_matrix(list_path, dup_path)
    expect_error("duplicate path", e)
    os.unlink(dup_path)

    # 2 missing path
    _, miss_rows = load_conflicts(list_path, csv_path)
    miss_rows = miss_rows[:-1]
    with tempfile.NamedTemporaryFile("w", suffix=".csv", delete=False, encoding="utf-8", newline="") as fh:
        w = csv.DictWriter(fh, fieldnames=rows[0].keys()); w.writeheader(); w.writerows(miss_rows); miss_path = fh.name
    e, _ = check_conflict_matrix(list_path, miss_path)
    expect_error("missing path", e)
    os.unlink(miss_path)

    # 3 bad category
    _, bad_rows = load_conflicts(list_path, csv_path)
    bad_rows[0]["resolution"] = "BOGUS"
    with tempfile.NamedTemporaryFile("w", suffix=".csv", delete=False, encoding="utf-8", newline="") as fh:
        w = csv.DictWriter(fh, fieldnames=rows[0].keys()); w.writeheader(); w.writerows(bad_rows); bad_path = fh.name
    e, _ = check_conflict_matrix(list_path, bad_path)
    expect_error("unknown category", e)
    os.unlink(bad_path)

    # 4 non-doc CI change
    expect_error("non-doc CI change", check_ci_set(["docs/fork/a.md", "protocol/direct/outbound.go"]))

    # 5 wrong gitlink
    lock_text = open(os.path.join(ROOT, "release", "source-lock.json"), encoding="utf-8").read()
    expect_error("wrong gitlink", check_gitlinks({"clients/android": "deadbeef"}, lock_text))

    # 6 wrong final merge-base
    real_parents = git("show", "-s", "--format=%P", MERGE).split()
    expect_error("wrong final merge-base", check_graph(real_parents, PRE_MERGE, "c" * 40))

    if failures:
        print("SELFTEST FAILED:", failures, "case(s) accepted")
        return 1
    print("SELFTEST PASS: all injected inconsistencies were rejected")
    return 0


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    sys.exit(self_test() if args.self_test else normal())
