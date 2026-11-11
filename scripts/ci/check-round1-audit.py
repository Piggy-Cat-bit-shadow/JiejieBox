#!/usr/bin/env python3
"""Round-1 audit consistency gate.

Verifies the facts the closure report depends on, so they cannot silently drift:
- 108 conflict paths == the reviewed matrix, no duplicates, category total 108
- merge commit parents and pre-merge/final merge-base semantics
- canonical head == origin/testing, upstream ancestor, behind == 0
- CI-tested head -> final head is docs/audit only (or a documented CI-script change)
- source-lock pins and client gitlinks agree with the tree
"""

import csv
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
MERGE = "303fbb47dfdca4520dd201fea8bd48b3db1f1010"
PRE_MERGE = "8eb4395a391b775ad240433a7009f39357c1eba0"
UPSTREAM = "a13dbe1f785f66c86acbf3985c33fdafea0cbfd6"
CI_TESTED = "b473c21f4803c0196e3eb920d530602d5f92867f"


def git(*args):
    p = subprocess.run(["git", "-C", ROOT, *args], capture_output=True, text=True, encoding="utf-8")
    if p.returncode != 0:
        raise SystemExit("git %s failed: %s" % (" ".join(args), p.stderr.strip()))
    return p.stdout.strip()


def fail(message):
    print("FAIL:", message)
    global failed
    failed = True


failed = False

# 1. conflict path set
raw = open(os.path.join(ROOT, "docs", "fork", "round1-main-merge-conflicts.txt"), "rb").read().decode("utf-8-sig")
original = sorted({line.strip().replace("\\", "/") for line in raw.splitlines() if line.strip()})
matrix = list(csv.DictReader(open(os.path.join(ROOT, "docs", "fork", "ROUND1-MERGE-CONFLICT-RESOLUTION.csv"), encoding="utf-8")))
paths = [row["path"] for row in matrix]
if len(original) != 108:
    fail("original conflict list is %d, want 108" % len(original))
if len(paths) != 108:
    fail("matrix has %d rows, want 108" % len(paths))
if len(set(paths)) != len(paths):
    fail("matrix has duplicate paths")
if set(paths) != set(original):
    fail("matrix paths differ from the original conflict list")
counts = {}
for row in matrix:
    counts[row["resolution"]] = counts.get(row["resolution"], 0) + 1
if sum(counts.values()) != 108:
    fail("resolution category sum is %d, want 108" % sum(counts.values()))

# 2. merge graph
parents = git("show", "-s", "--format=%P", MERGE).split()
if len(parents) != 2:
    fail("merge commit does not have two parents")
else:
    fork_parent, upstream_parent = parents
    if upstream_parent != UPSTREAM:
        fail("merge second parent is %s, want %s" % (upstream_parent, UPSTREAM))
    if git("merge-base", fork_parent, upstream_parent) != PRE_MERGE:
        fail("pre-merge merge-base is not %s" % PRE_MERGE)
head = git("rev-parse", "HEAD")
if git("merge-base", head, UPSTREAM) != UPSTREAM:
    fail("final merge-base is not the merged upstream head")
if git("rev-parse", "upstream/testing") != UPSTREAM:
    fail("upstream/testing moved; the observation is now historical")

# 3. canonical refs and zero behind
if git("rev-parse", "testing") != head or git("rev-parse", "origin/testing") != head:
    fail("testing/origin/testing do not point at HEAD")
left, right = git("rev-list", "--left-right", "--count", "HEAD...upstream/testing").split()
if int(right) != 0:
    fail("HEAD is %s commits behind upstream/testing" % right)

# 4. CI boundary: b473 -> HEAD must be docs/audit only
changed = git("diff", "--name-only", CI_TESTED + "..HEAD").splitlines()
allowed = re.compile(r"^(docs/fork/|release/dependency-final-shas\.json$|release/source-lock\.json$|"
                     r"0[0-9]_.*\.md$|INTEGRATION-LOG\.md$|CLEANUP.*\.md$|"
                     r"scripts/ci/check-round1-audit\.py$)")
unexpected = [p for p in changed if not allowed.match(p)]
if unexpected:
    fail("non-docs changes after CI-tested head: %s" % ", ".join(unexpected))

# 5. source lock and gitlinks
lock = open(os.path.join(ROOT, "release", "source-lock.json"), encoding="utf-8").read()
for path, sha in (
    ("clients/apple", "7fce7117b33a38ef4c73c81db67c49f0be7a7dad"),
    ("clients/android", "ba5c6b161827a830cb7343850bf547156fbfdfee"),
    ("clients/desktop", "e8b48beadd7d11a9bce46f62e12820f3e3773518"),
):
    tree = git("ls-tree", "HEAD", path).split()
    actual = tree[2] if len(tree) > 2 else "-"
    if actual != sha:
        fail("%s gitlink is %s, want %s" % (path, actual, sha))
    if sha not in lock:
        fail("source lock does not record %s" % path)

print("conflict rows:", len(paths), "categories:", counts)
print("merge parents:", parents)
print("head:", head, "ahead/behind:", left, right)
print("CI change set:", changed)
if failed:
    sys.exit(1)
print("PASS: round-1 audit facts are consistent")
