#!/usr/bin/env python3
"""Behavioural tests for scripts/ci/release-freeze.py.

Every case builds a throwaway repository with real `git`, real `go.mod` files and real gitlinks,
then drives the tool with a RECORDED resolution (`resolve --from`), so the suite needs no network
and cannot pass because of what GitHub happened to answer today. The two things the tool shells
out to for network facts - `git ls-remote` and `gh api` - are replaced by fakes on PATH that read
their answers from a JSON file the case writes, and the fixture registers those answers from the
same table it builds the resolution from, so a case cannot forget one and read a spurious UNKNOWN.

Why the negatives are the point: a freeze that only proves "the pins I froze are the pins I
froze" would pass on a repository where a fork had diverged, where a branch had been deleted, or
where test/go.mod had quietly stopped replacing a fork. Each of those has its own case below, and
each asserts the tool FAILS with a message naming the target - a green run on a broken input is
the only outcome this suite treats as a defect in the tool.

Usage: scripts/ci/test-release-freeze.py
"""

import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, "release-freeze.py")
PYTHON = sys.executable or "python3"
LIB_PREFIX = "github.com/sagernet/cronet-go/lib/"

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


def sh(args, cwd, env=None, expect=0):
    result = subprocess.run(args, cwd=cwd, capture_output=True, text=True, env=env)
    if expect is not None and result.returncode != expect:
        raise RuntimeError("command failed (%d): %s\nstdout: %s\nstderr: %s"
                           % (result.returncode, " ".join(args),
                              result.stdout.strip(), result.stderr.strip()))
    return result


def force_rmtree(path):
    """Remove a tree even when Git has marked its object files read-only."""
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


def make_fake(directory, name, script_text):
    """Write a fake command that works on this host: a .cmd shim on Windows, a shebang elsewhere."""
    script = os.path.join(directory, name + ".py")
    with open(script, "w", encoding="utf-8", newline="\n") as handle:
        handle.write(script_text)
    if os.name == "nt":
        shim = os.path.join(directory, name + ".cmd")
        with open(shim, "w", encoding="utf-8", newline="\r\n") as handle:
            handle.write('@"%s" "%s" %%*\r\n' % (PYTHON, script))
        return shim
    shim = os.path.join(directory, name)
    with open(shim, "w", encoding="utf-8", newline="\n") as handle:
        handle.write("#!/usr/bin/env python3\n")
        handle.write(script_text)
    os.chmod(shim, os.stat(shim).st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    return shim


FAKE_GH = r'''
import json, os, sys

args = sys.argv[1:]
if not args or args[0] != "api":
    sys.stderr.write("fake gh: only `api` is implemented\n")
    sys.exit(1)
path = args[1]
answers = json.load(open(os.environ["FAKE_GH_ANSWERS"], encoding="utf-8"))


def emit(value):
    sys.stdout.write(str(value) + "\n")
    sys.exit(0)


if "/commits/" in path:
    short = path.rsplit("/commits/", 1)[1].split("?")[0]
    emit(answers["commits"].get(short, short.ljust(40, "0")))

if "/compare/" in path:
    comparison = path.rsplit("/compare/", 1)[1].split("?")[0]
    base, head = comparison.split("...")
    for row in answers.get("compare", []):
        if row["base"] == base and row["head"] == head:
            emit(row["status"])
    sys.stderr.write("fake gh: no answer for compare %s...%s\n" % (base, head))
    sys.exit(1)

sys.stderr.write("fake gh: unhandled path %s\n" % path)
sys.exit(1)
'''

FAKE_GO = r'''
import json, os, sys

args = sys.argv[1:]
if args[:3] != ["list", "-m", "-json"]:
    sys.stderr.write("fake go: only `list -m -json` is implemented\n")
    sys.exit(1)
module, sha = args[-1].rsplit("@", 1)
if len(sha) != 40:
    sys.stderr.write("fake go: %s is not a commit SHA\n" % sha)
    sys.exit(1)
answers = json.load(open(os.environ["FAKE_GO_ANSWERS"], encoding="utf-8"))
version = answers.get(module) or "v0.0.0-20261028000000-%s" % sha[:12]
print(json.dumps({"Path": module, "Version": version}))
'''


def full(short):
    return short if len(short) == 40 else short.ljust(40, "0")


class Fixture:
    """A throwaway repository shaped like the parts of this one the freeze reads."""

    MODULES = [
        ("github.com/sagernet/sing", "Piggy-Cat-bit-shadow/sing", "dev", "111111111111"),
        ("github.com/sagernet/sing-tun", "Piggy-Cat-bit-shadow/sing-tun", "dev", "222222222222"),
        ("github.com/metacubex/utls", "Piggy-Cat-bit-shadow/utls", "main", "333333333333"),
        ("github.com/sagernet/cronet-go", "Piggy-Cat-bit-shadow/cronet-go", "main",
         "444444444444"),
        ("github.com/sagernet/cronet-go/all", "Piggy-Cat-bit-shadow/cronet-go", "main",
         "444444444444"),
    ]
    LIB_MODULES = [
        "github.com/sagernet/cronet-go/lib/android_arm64",
        "github.com/sagernet/cronet-go/lib/linux_amd64",
        "github.com/sagernet/cronet-go/lib/windows_amd64",
    ]
    LIB_PIN = "aaaaaaaaaaaa"
    CLIENTS = [
        ("clients/apple", "Piggy-Cat-bit-shadow/sing-box-for-apple", "main", "555555555555"),
        ("clients/android", "Piggy-Cat-bit-shadow/sing-box-for-android", "main",
         "666666666666"),
        ("clients/desktop", "Piggy-Cat-bit-shadow/sing-box-for-desktop", "main",
         "777777777777"),
    ]
    NESTED = ("clients/desktop", "dashboard", "Piggy-Cat-bit-shadow/sing-box-dashboard", "main",
              "cccccccccccc")
    MIRROR = ("MetaCubeX/utls", "v1.9.0-mod-meta", "dddddddddddd")

    def __init__(self, skip_test_replace=(), with_nested_submodule=True, version="0.1.99"):
        self.dir = tempfile.mkdtemp(prefix="freeze-")
        self.bin = os.path.join(self.dir, "bin")
        os.makedirs(self.bin)
        self.repo = os.path.join(self.dir, "repo")
        os.makedirs(os.path.join(self.repo, "release"))
        with open(os.path.join(self.repo, "release", "JIEJIE_VERSION"), "w",
                  encoding="utf-8", newline="\n") as handle:
            handle.write(version + "\n")

        self.answers = {"commits": {}, "compare": []}
        self.go_answers = {}
        self.skip_test_replace = skip_test_replace
        self.with_nested_submodule = with_nested_submodule
        self.gh_answers_path = os.path.join(self.dir, "gh-answers.json")
        self.go_answers_path = os.path.join(self.dir, "go-answers.json")

        self._write_manifest()
        self._write_go_mods()
        self._write_clients()
        self._commit()
        self._install_fakes()

    # -- the pin table ----------------------------------------------------------

    def pins(self):
        table = {}
        for _module, slug, branch, pin in self.MODULES:
            table[(slug, branch)] = pin
        for _path, slug, branch, pin in self.CLIENTS:
            table[(slug, branch)] = pin
        parent, _nested, slug, branch, pin = self.NESTED
        table[(slug, branch)] = pin
        slug, branch, pin = self.MIRROR
        table[(slug, branch)] = pin
        return table

    def _name_for(self, key):
        for module, slug, branch, _pin in self.MODULES:
            if (slug, branch) == key:
                return module
        for path, slug, branch, _pin in self.CLIENTS:
            if (slug, branch) == key:
                return path
        _parent, _nested, slug, branch, _pin = self.NESTED
        if (slug, branch) == key:
            return "nested"
        return None

    # -- construction ------------------------------------------------------------

    def _write_manifest(self):
        parent, nested_path, nested_slug, nested_branch, _pin = self.NESTED
        data = {
            "schema": 1,
            "go_modules": [
                {"module": module, "slug": slug, "branch": branch, "policy": "update"}
                for module, slug, branch, _pin in self.MODULES
            ],
            "cronet_native": {
                "module_prefix": LIB_PREFIX,
                "slug": "Piggy-Cat-bit-shadow/cronet-go",
                "expected_modules": len(self.LIB_MODULES),
                "policy": "controlled",
            },
            "clients": [
                {"path": path, "slug": slug, "branch": branch, "policy": "update"}
                for path, slug, branch, _pin in self.CLIENTS
            ],
            "nested": [
                {"parent": parent, "path": nested_path, "slug": nested_slug,
                 "branch": nested_branch, "policy": "report"}
            ],
            "upstream_mirrors": [
                {"slug": self.MIRROR[0], "branch": self.MIRROR[1],
                 "mirror_branch": "mirror/meta-v1.9.0"}
            ],
        }
        with open(os.path.join(self.repo, "release", "dependencies.json"), "w",
                  encoding="utf-8", newline="\n") as handle:
            json.dump(data, handle, indent=2)
            handle.write("\n")

    def _replace_lines(self, skip=()):
        lines = []
        for module, slug, _branch, pin in self.MODULES:
            if module in skip:
                continue
            lines.append("replace %s => github.com/%s v0.0.0-20261028000000-%s"
                         % (module, slug, pin))
        for module in self.LIB_MODULES:
            lines.append("replace %s => github.com/Piggy-Cat-bit-shadow/cronet-go "
                         "v0.0.0-20260929202119-%s" % (module, self.LIB_PIN))
        return lines

    def _write_go_mods(self):
        root = ["module github.com/sagernet/sing-box", "", "go 1.25.5", ""]
        root += self._replace_lines()
        root.append("")
        with open(os.path.join(self.repo, "go.mod"), "w", encoding="utf-8",
                  newline="\n") as handle:
            handle.write("\n".join(root))

        os.makedirs(os.path.join(self.repo, "test"))
        nested = ["module github.com/sagernet/sing-box/test", "", "go 1.25.5", "",
                  "replace github.com/sagernet/sing-box => ../"]
        nested += self._replace_lines(self.skip_test_replace)
        nested.append("")
        with open(os.path.join(self.repo, "test", "go.mod"), "w", encoding="utf-8",
                  newline="\n") as handle:
            handle.write("\n".join(nested))

    def _write_clients(self):
        for path, _slug, _branch, _pin in self.CLIENTS:
            os.makedirs(os.path.join(self.repo, path), exist_ok=True)
            with open(os.path.join(self.repo, path, ".keep"), "w", encoding="utf-8",
                      newline="\n") as handle:
                handle.write("placeholder; the gitlink is what matters\n")
        if self.with_nested_submodule:
            parent, nested_path, _slug, _branch, pin = self.NESTED
            desktop = os.path.join(self.repo, parent)
            sh(["git", "init", "-q"], desktop)
            with open(os.path.join(desktop, ".keep"), "w", encoding="utf-8",
                      newline="\n") as handle:
                handle.write("desktop\n")
            self._commit_in(desktop, "desktop")
            sh(["git", "update-index", "--add", "--cacheinfo", "160000", full(pin),
                nested_path], desktop)
            self._commit_in(desktop, "desktop dashboard pin", stage=False)

    def _stage_gitlinks(self):
        # The superproject tracks clients/* as gitlinks and nothing else, so a client that holds
        # only placeholder files is removed before the gitlink is written. The Desktop client is
        # the exception on purpose: it is a real (embedded) repository here, because the freeze
        # reads the nested dashboard pin out of it. `git add` stages an embedded repository as a
        # gitlink on its own, and the cacheinfo write below then pins the SHA the case chose.
        for path, _slug, _branch, _pin in self.CLIENTS:
            target = os.path.join(self.repo, path)
            if os.path.exists(os.path.join(target, ".git")):
                continue
            force_rmtree(target)
        sh(["git", "add", "-A"], self.repo)
        for path, _slug, _branch, pin in self.CLIENTS:
            sh(["git", "update-index", "--add", "--cacheinfo", "160000", full(pin), path],
               self.repo)

    def _commit(self):
        sh(["git", "init", "-q", "-b", "testing"], self.repo)
        self._stage_gitlinks()
        # No `git add -A` here: the client paths exist in the index only, and `add -A` would
        # stage their deletion - which is how the first version of this fixture produced a
        # repository with no gitlinks at all and a suite that proved nothing.
        self._commit_in(self.repo, "fixture", stage=False)

    def _commit_in(self, directory, message, stage=True):
        if stage:
            sh(["git", "add", "-A"], directory)
        sh(["git", "-c", "user.email=fixture@example.invalid", "-c", "user.name=fixture",
            "-c", "commit.gpgsign=false", "commit", "-q", "-m", message], directory)

    def _install_fakes(self):
        self._flush_answers()
        gh = make_fake(self.bin, "gh", FAKE_GH)
        go = make_fake(self.bin, "go", FAKE_GO)
        self.env = dict(os.environ)
        # By absolute path rather than by shadowing PATH: Windows resolves a bare command name to
        # `.exe` only, so a `gh.cmd` earlier on PATH would be skipped and the REAL gh would answer
        # - which is how the first version of this suite silently talked to GitHub.
        self.env["RELEASE_FREEZE_GH"] = gh
        self.env["RELEASE_FREEZE_GO"] = go
        self.env["FAKE_GH_ANSWERS"] = self.gh_answers_path
        self.env["FAKE_GO_ANSWERS"] = self.go_answers_path

    def _flush_answers(self):
        with open(self.gh_answers_path, "w", encoding="utf-8", newline="\n") as handle:
            json.dump(self.answers, handle)
        with open(self.go_answers_path, "w", encoding="utf-8", newline="\n") as handle:
            json.dump(self.go_answers, handle)

    # -- resolution -------------------------------------------------------------

    def write_resolution(self, changes=None, statuses=None, name="resolve.tsv"):
        """Write a recorded resolution and register the ancestry answers it implies.

        `changes` maps a module path, a client path, "nested", or a (slug, branch) tuple to a
        SHORT SHA. `statuses` maps the same key to the compare status the fake API answers with;
        the default for a changed target is "ahead", meaning the tip contains the pin.
        """
        changes = dict(changes or {})
        statuses = dict(statuses or {})
        rows = []
        for key, pin in self.pins().items():
            slug, branch = key
            short, status = self._override(key, changes, statuses, pin)
            rows.append((slug, branch, full(short)))
            self._register_answer(pin, short, status)
        path = os.path.join(self.dir, name)
        with open(path, "w", encoding="utf-8", newline="\n") as handle:
            for slug, branch, sha in rows:
                handle.write("%s\t%s\t%s\n" % (slug, branch, sha or "-"))
        return path

    def _override(self, key, changes, statuses, pin):
        for selector in (key, key[0], self._name_for(key)):
            if selector is not None and selector in changes:
                status = statuses.get(selector, statuses.get(key, "ahead"))
                return changes[selector], status
        return pin, None

    def _register_answer(self, pin, short, status):
        if short is None or status is None:
            return
        base = full(pin)
        head = full(short)
        if base == head:
            return
        for row in self.answers["compare"]:
            if row["base"] == base and row["head"] == head:
                row["status"] = status
                self._flush_answers()
                return
        self.answers["compare"].append({"base": base, "head": head, "status": status})
        self._flush_answers()

    # -- invocation -------------------------------------------------------------

    def run(self, *args, expect=0):
        result = subprocess.run([PYTHON, TOOL, "--repo", self.repo] + list(args),
                                capture_output=True, text=True, env=self.env)
        if expect is not None and result.returncode != expect:
            print("      command: %s" % " ".join(args))
            print("      exit   : %d (expected %d)" % (result.returncode, expect))
            print("      stdout : %s" % result.stdout.strip()[:4000])
            print("      stderr : %s" % result.stderr.strip()[:2000])
        return result

    def read(self, relative):
        with open(os.path.join(self.repo, relative), "r", encoding="utf-8",
                  newline="") as handle:
            return handle.read()

    def write(self, relative, text):
        with open(os.path.join(self.repo, relative), "w", encoding="utf-8",
                  newline="\n") as handle:
            handle.write(text)

    def go_shas(self, path="go.mod"):
        out = []
        for line in self.read(path).splitlines():
            if line.startswith("replace ") and LIB_PREFIX not in line:
                out.append(line.rsplit("-", 1)[-1])
        return out

    def client_pin(self, path):
        # The index, because `apply` writes gitlinks with `git update-index` and the assertion is
        # about what the next commit will contain.
        fields = sh(["git", "ls-files", "-s", "--", path], self.repo).stdout.split()
        return fields[1] if len(fields) >= 3 else None

    def nested_pin(self):
        return sh(["git", "-C", os.path.join(self.repo, self.NESTED[0]), "ls-tree", "HEAD",
                   self.NESTED[1]], self.repo).stdout.split()[2]

    def cleanup(self):
        force_rmtree(self.dir)


# ------------------------------------------------------------------------------------------------
# Cases
# ------------------------------------------------------------------------------------------------

def case_no_updates_are_idempotent():
    fixture = Fixture()
    try:
        resolve = fixture.write_resolution()
        result = fixture.run("check", "--resolve", resolve, expect=0)
        check("no-updates: check reports nothing to move",
              "no pin needs to move" in result.stdout, result.stdout[-400:])

        before = fixture.read("go.mod")
        result = fixture.run("apply", "--resolve", resolve, expect=0)
        check("no-updates: apply writes nothing",
              "no pin needs to move" in result.stdout and fixture.read("go.mod") == before,
              result.stdout[-400:])

        first = os.path.join(fixture.dir, "lock-a.json")
        second = os.path.join(fixture.dir, "lock-b.json")
        fixture.run("snapshot", "--resolve", resolve, "--out", first, expect=0)
        fixture.run("snapshot", "--resolve", resolve, "--out", second, expect=0)
        with open(first, "rb") as one, open(second, "rb") as two:
            check("no-updates: the snapshot is byte-identical when nothing changed",
                  one.read() == two.read())
        result = fixture.run("verify", "--lock", first, expect=0)
        check("no-updates: verify accepts the snapshot it just wrote",
              "PASS: every pin in the tree" in result.stdout, result.stdout[-400:])
    finally:
        fixture.cleanup()


def case_one_client_behind():
    fixture = Fixture()
    try:
        new_apple = "5" * 11 + "f"
        resolve = fixture.write_resolution({"clients/apple": new_apple})
        result = fixture.run("check", "--resolve", resolve, expect=0)
        check("one-client: check names exactly one update",
              "1 pin(s) are behind" in result.stdout and "clients/apple" in result.stdout,
              result.stdout[-600:])

        fixture.run("apply", "--resolve", resolve, expect=0)
        check("one-client: apply moved the apple gitlink",
              fixture.client_pin("clients/apple") == full(new_apple),
              fixture.client_pin("clients/apple"))
        check("one-client: android and desktop gitlinks did not move",
              fixture.client_pin("clients/android") == full("666666666666")
              and fixture.client_pin("clients/desktop") == full("777777777777"))
        check("one-client: go modules were not touched",
              fixture.go_shas() == ["111111111111", "222222222222", "333333333333",
                                    "444444444444", "444444444444"],
              str(fixture.go_shas()))

        result = fixture.run("apply", "--resolve", resolve, expect=0)
        check("one-client: apply is idempotent", "no pin needs to move" in result.stdout,
              result.stdout[-300:])
    finally:
        fixture.cleanup()


def case_go_module_behind_moves_both_modules():
    fixture = Fixture()
    try:
        new_sha = "1" * 11 + "b"
        fixture.go_answers["github.com/sagernet/sing"] = \
            "v0.9.6-0.20261028120000-%s" % new_sha
        fixture._flush_answers()
        resolve = fixture.write_resolution({"github.com/sagernet/sing": new_sha})
        fixture.run("apply", "--resolve", resolve, expect=0)

        want = "v0.9.6-0.20261028120000-%s" % new_sha
        check("go-module: the root replace carries the canonical pseudo-version",
              want in fixture.read("go.mod"), want)
        check("go-module: the nested test module carries the same version",
              want in fixture.read("test/go.mod"), want)
        check("go-module: no other module moved",
              "222222222222" in fixture.read("go.mod") and "333333333333" in fixture.read("go.mod"))
    finally:
        fixture.cleanup()


def case_diverged_fails_closed():
    fixture = Fixture()
    try:
        tip = "9" * 11 + "e"
        resolve = fixture.write_resolution({"github.com/sagernet/sing": tip},
                                           statuses={"github.com/sagernet/sing": "diverged"})
        before_root = fixture.read("go.mod")
        before_test = fixture.read("test/go.mod")

        result = fixture.run("check", "--resolve", resolve, expect=1)
        check("diverged: check fails and names the divergent module",
              "DIVERGED" in result.stdout and "github.com/sagernet/sing" in result.stdout,
              result.stdout[-600:])

        result = fixture.run("apply", "--resolve", resolve, expect=1)
        check("diverged: apply refuses and changes nothing",
              fixture.read("go.mod") == before_root
              and fixture.read("test/go.mod") == before_test,
              result.stdout[-400:])

        lock = os.path.join(fixture.dir, "lock.json")
        fixture.run("snapshot", "--resolve", resolve, "--out", lock, expect=1)
        check("diverged: snapshot refuses to freeze and writes no lock",
              not os.path.exists(lock))
    finally:
        fixture.cleanup()


def case_missing_branch_fails():
    fixture = Fixture()
    try:
        resolve = fixture.write_resolution()
        rows = []
        with open(resolve, "r", encoding="utf-8") as handle:
            for line in handle.read().splitlines():
                fields = line.split("\t")
                if fields[0] == "Piggy-Cat-bit-shadow/sing-tun":
                    fields[2] = "-"
                rows.append("\t".join(fields))
        with open(resolve, "w", encoding="utf-8", newline="\n") as handle:
            handle.write("\n".join(rows) + "\n")
        result = fixture.run("check", "--resolve", resolve, expect=1)
        check("missing-branch: check fails and names the repository",
              "MISSING" in result.stdout and "sing-tun" in result.stdout,
              result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_nested_lag_is_reported_not_fatal():
    fixture = Fixture()
    try:
        dashboard_tip = "a" * 11 + "d"
        resolve = fixture.write_resolution({"nested": dashboard_tip})
        result = fixture.run("check", "--resolve", resolve, expect=0)
        check("nested-lag: check passes even though the nested pin is behind",
              "PASS: every target is comparable" in result.stdout, result.stdout[-500:])

        lock = os.path.join(fixture.dir, "lock.json")
        fixture.run("snapshot", "--resolve", resolve, "--out", lock, expect=0)
        with open(lock, "r", encoding="utf-8") as handle:
            data = json.load(handle)
        nested = data["nested"][0]
        check("nested-lag: the snapshot records the nested relation",
              nested["relation"] == "behind" and nested["resolved"] == full(dashboard_tip),
              json.dumps(nested))
        check("nested-lag: the nested pin in the desktop repository was not rewritten",
              fixture.nested_pin() == full("cccccccccccc"), fixture.nested_pin())
    finally:
        fixture.cleanup()


def case_verify_catches_drift():
    fixture = Fixture()
    try:
        resolve = fixture.write_resolution()
        lock = os.path.join(fixture.dir, "lock.json")
        fixture.run("snapshot", "--resolve", resolve, "--out", lock, expect=0)
        fixture.run("verify", "--lock", lock, expect=0)

        fixture.write("go.mod", fixture.read("go.mod").replace("111111111111", "111111111999"))
        result = fixture.run("verify", "--lock", lock, expect=1)
        check("drift: verify fails and names the module",
              "github.com/sagernet/sing" in result.stdout and "FAIL" in result.stdout,
              result.stdout[-500:])
    finally:
        fixture.cleanup()


def case_verify_catches_client_drift():
    fixture = Fixture()
    try:
        resolve = fixture.write_resolution()
        lock = os.path.join(fixture.dir, "lock.json")
        fixture.run("snapshot", "--resolve", resolve, "--out", lock, expect=0)
        sh(["git", "update-index", "--cacheinfo", "160000", full("7" * 11 + "e"),
            "clients/desktop"], fixture.repo)
        sh(["git", "-c", "user.email=fixture@example.invalid", "-c", "user.name=fixture",
            "-c", "commit.gpgsign=false", "commit", "-q", "-m", "move desktop"], fixture.repo)
        result = fixture.run("verify", "--lock", lock, expect=1)
        check("client-drift: verify fails when a gitlink stops matching the frozen source",
              "clients/desktop" in result.stdout, result.stdout[-500:])
    finally:
        fixture.cleanup()


def case_verify_catches_root_test_mismatch():
    fixture = Fixture(skip_test_replace=("github.com/sagernet/sing-tun",))
    try:
        resolve = fixture.write_resolution()
        lock = os.path.join(fixture.dir, "lock.json")
        fixture.run("snapshot", "--resolve", resolve, "--out", lock, expect=0)
        result = fixture.run("verify", "--lock", lock, expect=1)
        check("root/test: verify fails when the nested module stops replacing a fork",
              "test/go.mod does not replace it" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_verify_catches_cronet_native_split():
    fixture = Fixture()
    try:
        resolve = fixture.write_resolution()
        lock = os.path.join(fixture.dir, "lock.json")
        fixture.run("snapshot", "--resolve", resolve, "--out", lock, expect=0)
        fixture.write("go.mod", fixture.read("go.mod").replace(
            "v0.0.0-20260929202119-%s" % Fixture.LIB_PIN,
            "v0.0.0-20260929202119-%s" % ("b" * 12), 1))
        result = fixture.run("verify", "--lock", lock, expect=1)
        check("cronet: verify fails when one native module moves alone",
              "disagree on their SHA" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_verify_catches_missing_fork_pin():
    fixture = Fixture()
    try:
        resolve = fixture.write_resolution()
        lock = os.path.join(fixture.dir, "lock.json")
        fixture.run("snapshot", "--resolve", resolve, "--out", lock, expect=0)
        with open(lock, "r", encoding="utf-8") as handle:
            data = json.load(handle)
        data["go_modules"].append({"module": "github.com/sagernet/sing-quic",
                                   "pinned": "0" * 12, "relation": "identical"})
        with open(lock, "w", encoding="utf-8", newline="\n") as handle:
            json.dump(data, handle, indent=2, sort_keys=True)
        result = fixture.run("verify", "--lock", lock, expect=1)
        check("missing-pin: verify fails when the lock records a module the tree does not pin",
              "github.com/sagernet/sing-quic" in result.stdout, result.stdout[-600:])
    finally:
        fixture.cleanup()


def case_apply_never_moves_controlled_pins():
    fixture = Fixture()
    try:
        new_root = "4" * 11 + "a"
        fixture.go_answers["github.com/sagernet/cronet-go"] = \
            "v0.0.1-143.0.7499.109-2.0.20261028130000-%s" % new_root
        fixture.go_answers["github.com/sagernet/cronet-go/all"] = \
            "v0.0.0-20261028130000-%s" % new_root
        fixture._flush_answers()
        resolve = fixture.write_resolution({
            "github.com/sagernet/cronet-go": new_root,
            "github.com/sagernet/cronet-go/all": new_root,
        })
        fixture.run("apply", "--resolve", resolve, expect=0)
        native_lines = [line for line in fixture.read("go.mod").splitlines()
                        if LIB_PREFIX in line and line.startswith("replace ")]
        check("cronet: all native replaces keep the controlled pin",
              native_lines and all(Fixture.LIB_PIN in line for line in native_lines),
              "\n".join(native_lines))
        check("cronet: the root module did move",
              new_root in fixture.read("go.mod"), fixture.read("go.mod")[-300:])
    finally:
        fixture.cleanup()


def case_verify_skips_nested_without_submodule():
    fixture = Fixture(with_nested_submodule=False)
    try:
        resolve = fixture.write_resolution()
        lock = os.path.join(fixture.dir, "lock.json")
        fixture.run("snapshot", "--resolve", resolve, "--out", lock, expect=0)
        with open(lock, "r", encoding="utf-8") as handle:
            data = json.load(handle)
        nested = data["nested"][0]
        check("nested-absent: the lock records that the nested pin could not be read",
              nested["relation"] == "skipped" and "not checked out" in (nested["reason"] or ""),
              json.dumps(nested))
        result = fixture.run("verify", "--lock", lock, expect=0)
        check("nested-absent: verify reports SKIPPED rather than a pass",
              "SKIPPED" in result.stdout, result.stdout[-400:])
    finally:
        fixture.cleanup()


def case_resolve_from_recorded_file():
    fixture = Fixture()
    try:
        recorded = fixture.write_resolution()
        out = os.path.join(fixture.dir, "resolved.tsv")
        result = fixture.run("resolve", "--from", recorded, "--out", out, expect=0)
        check("resolve: a recorded resolution is replayed without the network",
              "no network" in result.stdout and os.path.exists(out), result.stdout[-300:])
        with open(out, "r", encoding="utf-8") as handle:
            rows = [line.split("\t") for line in handle.read().splitlines() if line]
        declared = {(slug, branch) for slug, branch, _sha in rows}
        expected = set(fixture.pins().keys())
        check("resolve: every declared (repository, branch) is in the output",
              declared == expected, "missing %s, unexpected %s"
              % (sorted(expected - declared), sorted(declared - expected)))
    finally:
        fixture.cleanup()


def case_lock_records_the_release_version():
    fixture = Fixture(version="0.1.42")
    try:
        resolve = fixture.write_resolution()
        lock = os.path.join(fixture.dir, "lock.json")
        fixture.run("snapshot", "--resolve", resolve, "--out", lock, expect=0)
        with open(lock, "r", encoding="utf-8") as handle:
            data = json.load(handle)
        check("version: the lock names the release it was frozen for",
              data["release_version"] == "0.1.42", str(data.get("release_version")))

        # A version bump without a new freeze is a NOTE, not a failure: ordinary development
        # must not have to re-freeze on every version edit. The release workflow is what
        # requires the two to agree.
        fixture.write("release/JIEJIE_VERSION", "0.1.43\n")
        result = fixture.run("verify", "--lock", lock, expect=0)
        check("version: a newer JIEJIE_VERSION is reported without failing verify",
              "regenerate it before a release" in result.stdout, result.stdout[-400:])
    finally:
        fixture.cleanup()


def case_parse_block_form():
    fixture = Fixture()
    try:
        script = os.path.join(fixture.dir, "blockcheck.py")
        with open(script, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(
                "import importlib.util\n"
                "spec = importlib.util.spec_from_file_location('rf', r'%s')\n"
                "m = importlib.util.module_from_spec(spec)\n"
                "spec.loader.exec_module(m)\n"
                "text = ('module x\\n\\nreplace (\\n\\tgithub.com/a/b => github.com/c/d '\n"
                "        'v1.2.3\\n)\\n\\nreplace github.com/e/f => github.com/g/h '\n"
                "        'v0.0.1-20260101000000-abcdefabcdef\\n')\n"
                "got = m.parse_replaces(text)\n"
                "ok = (got.get('github.com/a/b') == ('github.com/c/d', 'v1.2.3')\n"
                "      and got.get('github.com/e/f')[1].endswith('abcdefabcdef'))\n"
                "print('OK' if ok else 'BAD ' + repr(got))\n" % TOOL)
        result = sh([PYTHON, script], fixture.dir)
        check("parser: both the single-line and the block replace form are read",
              result.stdout.strip() == "OK", result.stdout.strip())
    finally:
        fixture.cleanup()


def main():
    cases = [
        case_no_updates_are_idempotent,
        case_one_client_behind,
        case_go_module_behind_moves_both_modules,
        case_diverged_fails_closed,
        case_missing_branch_fails,
        case_nested_lag_is_reported_not_fatal,
        case_verify_catches_drift,
        case_verify_catches_client_drift,
        case_verify_catches_root_test_mismatch,
        case_verify_catches_cronet_native_split,
        case_verify_catches_missing_fork_pin,
        case_apply_never_moves_controlled_pins,
        case_verify_skips_nested_without_submodule,
        case_resolve_from_recorded_file,
        case_lock_records_the_release_version,
        case_parse_block_form,
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
