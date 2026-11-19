#!/usr/bin/env python3
"""Report packages that have tests but are executed by no workflow.

Why this exists: this repository has a lot of test files and a lot of workflows, and the two sets are
not the same. Three defects in the 2026-11 audit were found by *running* a suite that no job ran
(`e2e`, `protocol/*`, `service/oomkiller`'s guard test); in each case the test existed, was green when
run by hand, and would have caught the bug before a release. A gate that names the gap is cheaper than
remembering to look.

Granularity: "executed" means some workflow line runs `go test` with a pattern that matches the
package. `go test -run '^$' ./...` is COMPILE ONLY and deliberately does not count, because that is the
exact line that made the gap invisible in the first place.

Usage:
    check-package-execution.py --list                    # print the uncovered packages
    check-package-execution.py --baseline FILE           # fail if a NEW uncovered package appears
    check-package-execution.py --workflows DIR --packages FILE   # for the self-test

Exit codes: 0 ok, 1 a new uncovered package (or a bad baseline), 2 usage error.
"""

import argparse
import glob
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
MODULE = "github.com/sagernet/sing-box"

# `go test` with this run expression builds the test binary and runs nothing. It must not count as
# execution; see the module docstring.
COMPILE_ONLY = re.compile(r"-run[= ]['\"]?\^?\$['\"]?")


def packages_with_tests() -> list[str]:
    """Every package under the module that has tests, as `./path` (or `./`)."""
    try:
        result = subprocess.run(
            ["go", "list", "-f", "{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}", "./..."],
            cwd=ROOT,
            capture_output=True,
            text=True,
        )
    except FileNotFoundError:
        print("FAIL: `go` is not on PATH; this gate needs the toolchain to enumerate packages", file=sys.stderr)
        sys.exit(2)
    if result.returncode != 0:
        print("FAIL: `go list` exited %d:\n%s" % (result.returncode, result.stderr), file=sys.stderr)
        sys.exit(2)
    out = result.stdout
    packages = []
    for line in out.splitlines():
        line = line.strip()
        if not line:
            continue
        if not line.startswith(MODULE):
            continue
        relative = line[len(MODULE):]
        packages.append("./" if relative == "" else "." + relative)
    return sorted(set(packages))


def workflow_commands(workflows_dir: str) -> list[str]:
    """Every logical line of every workflow that runs `go test` and is not compile-only.

    Shell continuations are joined first: a `go test ... \\` followed by its package list on the next
    line is one command, and a line-based reader misses every package it runs. That false negative was
    visible in the first run of this script (`./transport/v2rayhttp` reported as uncovered although a
    race step runs it).
    """
    commands = []
    for path in sorted(glob.glob(os.path.join(workflows_dir, "*.yml")) + glob.glob(os.path.join(workflows_dir, "*.yaml"))):
        with open(path, "r", encoding="utf-8") as handle:
            lines = handle.read().splitlines()
        logical = []
        pending = ""
        for raw in lines:
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            if line.endswith("\\"):
                pending += line[:-1].strip() + " "
                continue
            logical.append((pending + line).strip())
            pending = ""
        if pending:
            logical.append(pending.strip())
        for line in logical:
            if "go test" not in line:
                continue
            if COMPILE_ONLY.search(line):
                continue
            commands.append(line)
    return commands


def patterns_in(command: str) -> list[str]:
    """The package arguments of one `go test` line.

    The arguments are the tokens after `go test` that look like package patterns (start with `./`, a
    module path, or `.`). Flags and their values are skipped; a value that looks like a path after a
    flag is only kept when it is a package pattern the caller meant (the heuristics are deliberately
    narrow, and an over-narrow list only means a package is reported as uncovered, which is the safe
    direction for a gate).
    """
    tokens = command.split()
    try:
        index = tokens.index("test")
    except ValueError:
        return []
    patterns = []
    for token in tokens[index + 1:]:
        token = token.strip("'\"")
        if token.startswith("-"):
            continue
        if token in ("&&", "||", "|", ";"):
            break
        if token.startswith("./") or token == "." or token.startswith(MODULE):
            patterns.append(token)
        elif "/" in token and not token.startswith("/") and re.match(r"^[A-Za-z0-9_.\-/]+$", token) is not None:
            # A relative package pattern such as `dns/...` (no leading `./`).
            patterns.append("./" + token)
    return patterns


def covers(pattern: str, package: str) -> bool:
    if pattern.startswith(MODULE):
        pattern = "." + pattern[len(MODULE):]
    # A bare `.` is the ROOT package, not the whole tree; `./...` is the whole tree. Conflating the two
    # made the first version of this gate report zero gaps, which is exactly the silent-green failure
    # mode it exists to prevent.
    if pattern == ".":
        pattern = "./"
    if pattern == "./...":
        return True
    if pattern.endswith("/..."):
        prefix = pattern[: -len("/...")]
        if prefix == ".":
            return True
        return package == prefix or package.startswith(prefix + "/")
    if pattern.endswith("/") and pattern != "./":
        pattern = pattern[:-1]
    return package == pattern


def uncovered(packages: list[str], commands: list[str]) -> list[str]:
    patterns = []
    for command in commands:
        patterns.extend(patterns_in(command))
    result = []
    for package in packages:
        if not any(covers(pattern, package) for pattern in patterns):
            result.append(package)
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--baseline", help="file of the uncovered packages that are known and accepted; "
                                           "the gate fails only when one appears that is NOT in it")
    parser.add_argument("--list", action="store_true", help="print the uncovered packages and exit 0")
    parser.add_argument("--workflows", default=os.path.join(ROOT, ".github", "workflows"))
    parser.add_argument("--packages", help="file with one package per line (for the self-test)")
    args = parser.parse_args()

    if args.packages:
        with open(args.packages, "r", encoding="utf-8") as handle:
            packages = sorted({line.strip() for line in handle if line.strip()})
    else:
        packages = packages_with_tests()

    missing = uncovered(packages, workflow_commands(args.workflows))

    if args.list or not args.baseline:
        print("packages with tests that no workflow runs: %d of %d" % (len(missing), len(packages)))
        for package in missing:
            print("  " + package)
        return 0

    with open(args.baseline, "r", encoding="utf-8") as handle:
        baseline = {line.strip() for line in handle if line.strip() and not line.startswith("#")}

    new_gaps = [package for package in missing if package not in baseline]
    recovered = sorted(baseline - set(missing))

    print("packages with tests: %d" % len(packages))
    print("packages no workflow runs: %d (baseline: %d)" % (len(missing), len(baseline)))
    if recovered:
        print("no longer a gap (prune these from the baseline):")
        for package in recovered:
            print("  " + package)
    if new_gaps:
        print("FAIL: these packages gained tests and no workflow runs them:", file=sys.stderr)
        for package in new_gaps:
            print("  " + package, file=sys.stderr)
        print("Add a job that runs them (or, for work that needs a device/root, an explicit line in the "
              "baseline with the reason next to it).", file=sys.stderr)
        return 1
    print("PASS: no package gained tests without a workflow that runs them")
    return 0


if __name__ == "__main__":
    sys.exit(main())
