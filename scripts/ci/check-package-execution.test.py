#!/usr/bin/env python3
"""The self-test for check-package-execution.py.

A gate that cannot be shown to fail is not a gate. These cases run the real script against synthetic
workflows and package lists, so every branch (covered, new gap, baselined gap, compile-only line,
shell continuation) is exercised in the same process that CI uses.

    python3 scripts/ci/check-package-execution.test.py
"""

import os
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "check-package-execution.py")

PACKAGES = "./covered\n./new\n./joined\n"


def write(directory: str, name: str, content: str) -> str:
    path = os.path.join(directory, name)
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(content)
    return path


def run(workflows: str, packages: str, baseline: str | None = None) -> tuple[int, str]:
    command = [sys.executable, SCRIPT, "--workflows", workflows, "--packages", packages]
    if baseline is not None:
        command += ["--baseline", baseline]
    result = subprocess.run(command, capture_output=True, text=True)
    return result.returncode, result.stdout + result.stderr


def main() -> int:
    failures = []
    with tempfile.TemporaryDirectory() as scratch:
        workflows = os.path.join(scratch, "workflows")
        os.makedirs(workflows)
        packages = write(scratch, "packages.txt", PACKAGES)

        # A workflow that runs one package, plus a compile-only `./...` line and a continued command
        # whose package list is on the next line.
        write(workflows, "a.yml", """jobs:
  test:
    steps:
      - run: go test -count=1 ./covered
      - run: go test -run '^$' ./...
      - run: |
          go test -count=1 \\
            ./joined
""")

        code, output = run(workflows, packages)
        if code != 0 or "./covered" in output or "./joined" in output:
            failures.append("a covered package (or a continued one) was reported as uncovered:\n" + output)
        if "./new" not in output:
            failures.append("the uncovered package was not reported:\n" + output)

        # Compile-only must not count: `./new` must still be uncovered even though `./...` appears.
        if "packages with tests that no workflow runs: 1" not in output:
            failures.append("the compile-only `./...` line was counted as execution:\n" + output)

        baseline = write(scratch, "baseline.txt", "./new\n")
        code, output = run(workflows, packages, baseline)
        if code != 0:
            failures.append("a baselined gap must pass, got exit %d:\n%s" % (code, output))

        baseline = write(scratch, "baseline-missing.txt", "./somewhere-else\n")
        code, output = run(workflows, packages, baseline)
        if code != 1:
            failures.append("a NEW gap must fail with exit 1, got %d:\n%s" % (code, output))
        if "./new" not in output:
            failures.append("the failure must name the new gap:\n" + output)

        # A baseline entry that became covered is reported for pruning, but is not a failure.
        baseline = write(scratch, "baseline-stale.txt", "./new\n./covered\n")
        code, output = run(workflows, packages, baseline)
        if code != 0 or "prune" not in output:
            failures.append("a stale baseline entry must be reported and not fail:\n" + output)

    if failures:
        for failure in failures:
            print("FAIL: " + failure, file=sys.stderr)
        return 1
    print("PASS: the package-execution gate catches a new gap, ignores compile-only lines, joins "
          "continuations, and ratchets on its baseline")
    return 0


if __name__ == "__main__":
    sys.exit(main())
