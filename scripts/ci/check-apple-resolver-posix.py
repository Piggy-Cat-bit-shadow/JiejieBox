"""Executable verification of the Apple source resolver, on a host with no bash.

WHAT THIS IS

The real regression gate is scripts/ci/check-apple-source-selection.sh, which is bash and cannot run
on this host. This harness does not replace it. It exercises the RESOLVER - the script under test -
against local git fixtures, so the §6 contract properties are demonstrated by execution rather than
asserted by reading code.

It works because apple-client-source.sh avoids bash-only syntax, so `dash` can execute it. Two
constructs are stripped from a COPY in a scratch tree, and both are recorded rather than quietly
worked around:

    set -o pipefail        dash rejects the option
    ${BASH_SOURCE[0]}      a bash array; $0 is the POSIX equivalent

Neither is part of the source-selection logic under test - one is error propagation, the other is
"where am I". The repository file is never modified.

Every assertion reads its expected value from the bare repo AT THAT MOMENT, so a stale in-memory SHA
cannot produce a false failure. An earlier revision of this harness did exactly that and reported
two phantom bugs.
"""

import io
import os
import re
import shutil
import subprocess
import sys

REPO = os.environ.get("APPLE_RESOLVER_CHECK_REPO") or r"C:\src\sing-box"
SCRATCH = os.environ.get("APPLE_RESOLVER_CHECK_SCRATCH") or os.path.join(
    os.environ.get("TEMP", r"C:\tmp"), "apple-resolver-check")
GIT = (os.environ.get("APPLE_RESOLVER_CHECK_GIT")
       or shutil.which("git")
       or r"C:\Deepseek\IOS客户端\_tools\mingit\cmd\git.exe")
DASH = (os.environ.get("APPLE_RESOLVER_CHECK_DASH")
        or shutil.which("dash")
        or shutil.which("sh")
        or r"C:\Deepseek\IOS客户端\_tools\mingit\usr\bin\dash.exe")
BASHENV = os.environ.get("APPLE_RESOLVER_CHECK_BASHENV") or os.path.join(
    os.path.dirname(GIT), "..", "usr", "bin")

passed = 0
failed = 0


def run(args, cwd=None, env=None):
    e = dict(os.environ)
    e["PATH"] = os.path.abspath(BASHENV) + os.pathsep + e.get("PATH", "")
    if env:
        e.update(env)
    return subprocess.run(args, cwd=cwd, capture_output=True, text=True, env=e)


def check(name, ok, detail=""):
    global passed, failed
    if ok:
        passed += 1
        print("  PASS  %s" % name)
    else:
        failed += 1
        print("  FAIL  %s%s" % (name, ("  -- " + detail) if detail else ""))


# ---------------------------------------------------------------------------
# Fixture
# ---------------------------------------------------------------------------
if os.path.isdir(SCRATCH):
    shutil.rmtree(SCRATCH, ignore_errors=True)
os.makedirs(SCRATCH)

apple_bare = os.path.join(SCRATCH, "fixtures", "apple.git")
work = os.path.join(SCRATCH, "apple-work")
parent = os.path.join(SCRATCH, "parent")
for d in (apple_bare, work, parent, os.path.join(parent, "scripts", "ci"),
          os.path.join(parent, "release")):
    os.makedirs(d, exist_ok=True)

run([GIT, "init", "--bare", "-q", "-b", "main", apple_bare])
run([GIT, "init", "-q", "-b", "main", work])
run([GIT, "-C", work, "config", "user.email", "t@example.invalid"])
run([GIT, "-C", work, "config", "user.name", "t"])
run([GIT, "config", "advice.detachedHead", "false"])
run([GIT, "config", "protocol.file.allow", "always"])


def work_commit(text, message):
    io.open(os.path.join(work, "file.txt"), "w", encoding="utf-8").write(text)
    run([GIT, "-C", work, "add", "file.txt"])
    run([GIT, "-C", work, "commit", "-qm", message])
    return run([GIT, "-C", work, "rev-parse", "HEAD"]).stdout.strip()


commit_a = work_commit("A\n", "A")
run([GIT, "-C", work, "tag", "-a", "v1", "-m", "release one"])   # ANNOTATED: must be peeled
commit_b = work_commit("B\n", "B")

run([GIT, "-C", work, "remote", "add", "origin", apple_bare])
run([GIT, "-C", work, "push", "-q", "origin", "main", "--tags"])
# A mirror keeps the bare repo's copies of A and B reachable after main moves on.
run([GIT, "-C", work, "push", "-q", "origin", "refs/tags/v1:refs/tags/v1"])

# The parent records a GITLINK at A, deliberately != main's tip B.
run([GIT, "init", "-q", "-b", "main", parent])
run([GIT, "-C", parent, "config", "user.email", "t@example.invalid"])
run([GIT, "-C", parent, "config", "user.name", "t"])
run([GIT, "clone", "-q", apple_bare, os.path.join(parent, "clients", "apple")])
run([GIT, "-C", os.path.join(parent, "clients", "apple"), "checkout", "-q", "--detach", commit_a])
run([GIT, "-C", parent, "add", "clients/apple"])
run([GIT, "-C", parent, "commit", "-qm", "parent with gitlink A"])

# The scratch resolver copy.
src = io.open(os.path.join(REPO, "scripts", "ci", "apple-client-source.sh"), encoding="utf-8").read()
stripped = src.replace("set -euo pipefail", "set -eu").replace("${BASH_SOURCE[0]}", "$0")
assert "BASH_SOURCE" not in stripped and "pipefail" not in stripped
io.open(os.path.join(parent, "scripts", "ci", "apple-client-source.sh"), "w",
        encoding="utf-8", newline="\n").write(stripped)

# The refs file points at the LOCAL bare repo: no network anywhere in this harness.
io.open(os.path.join(parent, "release", "apple-client-refs.env"), "w",
        encoding="utf-8", newline="\n").write(
    "APPLE_CLIENT_REPOSITORY=%s\nAPPLE_CLIENT_BRANCH=main\n"
    "HAKO_DESIGN_BASELINE_TAG=archive/hako-design-v1\n" % apple_bare.replace("\\", "/"))


def resolver(*args, env=None):
    return run([DASH, "scripts/ci/apple-client-source.sh"] + list(args), cwd=parent, env=env)


def bare_rev(rev):
    return run([GIT, "--git-dir=" + apple_bare, "rev-parse", rev]).stdout.strip()


def out_of(r):
    return r.stdout.strip().splitlines()[-1] if r.stdout.strip() else ""


print("fixture: A=%s  B=%s  (main -> B, tag v1 -> A, gitlink -> A)" % (commit_a[:9], commit_b[:9]))
print()

print("== 6.1 default latest ==")
tip = bare_rev("refs/heads/main")
got = out_of(resolver("ls-remote"))
check("default resolves main's tip", got == tip, "got %s tip %s" % (got[:12], tip[:12]))
check("default does NOT resolve the gitlink", got != commit_a)

print("== 6.2 explicit SHA ==")
got = out_of(resolver("ls-remote", env={"APPLE_CLIENT_REF": commit_a}))
check("APPLE_CLIENT_REF=<A> resolves A while main is at the tip", got == commit_a,
      "got %s want %s" % (got[:12], commit_a[:12]))

print("== 6.3 explicit annotated tag must peel ==")
tag_commit = bare_rev("refs/tags/v1^{commit}")
tag_object = bare_rev("refs/tags/v1")
got = out_of(resolver("ls-remote", env={"APPLE_CLIENT_REF": "v1"}))
check("tag v1 peels to its COMMIT, not the tag object",
      got == tag_commit and got != tag_object,
      "got %s commit %s object %s" % (got[:12], tag_commit[:12], tag_object[:12]))

print("== 6.4 resolve once ==")
state = os.path.join(parent, "build", "apple-client-source.env")
frozen_tip = bare_rev("refs/heads/main")
frozen = resolver("sha", "ios").stdout.strip()
check("sha ios freezes the tip in force", frozen == frozen_tip,
      "froze %s tip %s" % (frozen[:12], frozen_tip[:12]))
check("a frozen state file was written", os.path.isfile(state))
recorded = ""
if os.path.isfile(state):
    recorded = (re.search(r"^APPLE_CLIENT_SHA=(\S+)",
                          io.open(state, encoding="utf-8").read(), re.M) or [None, ""])[1]
check("the state file records the resolved SHA", recorded == frozen_tip, "records %s" % recorded[:12])

commit_c = work_commit("C\n", "C")
run([GIT, "-C", work, "push", "-q", "origin", "main"])
check("the fixture really moved main", bare_rev("refs/heads/main") == commit_c)
check("after main moves, the frozen build still answers the frozen SHA",
      resolver("sha", "ios").stdout.strip() == frozen_tip)
fresh = out_of(resolver("ls-remote", env={"APPLE_CLIENT_STATE": os.path.join(SCRATCH, "unused.env")}))
check("a fresh resolution sees the moved tip", fresh == commit_c,
      "got %s want %s" % (fresh[:12], commit_c[:12]))

print("== 6.5 shared source ==")
ios = resolver("sha", "ios").stdout.strip()
macos = resolver("sha", "macos").stdout.strip()
check("IOS_APPLE_SHA == MACOS_APPLE_SHA == APPLE_CLIENT_SHA", ios == macos == frozen_tip,
      "ios=%s macos=%s" % (ios[:12], macos[:12]))
r = resolver("assert-shared-source")
check("assert-shared-source passes", r.returncode == 0, r.stderr.strip()[:140])

print("== 6.6 gitlink mismatch ==")
check("mismatch is non-fatal by default", r.returncode == 0)
check("mismatch is reported as a note", "differs from the clients/apple gitlink" in r.stderr,
      "stderr=%r" % r.stderr.strip()[:140])
r2 = resolver("assert-shared-source", env={"APPLE_CLIENT_REQUIRE_GITLINK": "1"})
check("APPLE_CLIENT_REQUIRE_GITLINK=1 restores the hard failure", r2.returncode != 0,
      "rc=%s out=%r" % (r2.returncode, r2.stdout.strip()[:80]))
check("the opt-in failure names the reason", "REQUIRE_GITLINK=1" in r2.stderr,
      "stderr=%r" % r2.stderr.strip()[:140])

print("== 6.7 checkout correctness ==")
r = resolver("checkout", "ios")
ios_head = run([GIT, "-C", os.path.join(parent, "clients", "apple"), "rev-parse", "HEAD"]).stdout.strip()
check("checkout ios ends at the resolved SHA, moving off the gitlink", ios_head == frozen_tip,
      "HEAD=%s rc=%s err=%s" % (ios_head[:12], r.returncode, r.stderr.strip()[:140]))
check("verify ios passes after checkout", resolver("verify", "ios").returncode == 0)
r = resolver("checkout", "macos")
mpath = os.path.join(parent, "build", "apple-client-macos")
macos_head = run([GIT, "-C", mpath, "rev-parse", "HEAD"]).stdout.strip()
check("checkout macos ends at the same resolved SHA", macos_head == frozen_tip,
      "HEAD=%s rc=%s err=%s" % (macos_head[:12], r.returncode, r.stderr.strip()[:140]))
check("the two platforms are separate working trees",
      os.path.abspath(mpath) != os.path.abspath(os.path.join(parent, "clients", "apple")))

print("== guards ==")
refs = os.path.join(parent, "release", "apple-client-refs.env")
good = io.open(refs, encoding="utf-8").read()
io.open(refs, "w", encoding="utf-8", newline="\n").write(good + "MACOS_APPLE_SHA=%s\n" % commit_a)
check("a refs file re-declaring a per-platform source is refused",
      resolver("sha", "macos").returncode != 0)
io.open(refs, "w", encoding="utf-8", newline="\n").write(good)
check("an unknown platform is refused", resolver("sha", "windows").returncode != 0)
actions = sorted(re.findall(r"^  ([a-z][a-z-]*)\)", stripped, re.M))
check("the resolver offers the documented actions",
      actions == ["assert-on-branch", "assert-shared-source", "branch", "checkout", "dir",
                  "ls-remote", "resolve", "sha", "verify"], str(actions))

print()
print("apple resolver executable check: %d passed, %d failed" % (passed, failed))
sys.exit(1 if failed else 0)
