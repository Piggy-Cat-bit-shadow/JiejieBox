#!/usr/bin/env python3
"""Maintenance-loop census (audit Q46).

The census is a checked artifact, not a one-off table: every non-test ticker in the product tree
must appear in docs/fork/maintenance-loop-census.md with an owner, wake condition, cadence and exit.
`--check` fails when a ticker exists in the tree but not in the document, so a new periodic loop
cannot be added without stating who owns it and how it stops.

Test harnesses, benchmark tools and the interop drivers are listed with scope=tool: they are not
product maintenance loops and are excluded from the retention decision, but they ARE in the census so
the inventory is complete and a tool ticker is not mistaken for a product one.

Usage:
  python scripts/ci/maintenance-loop-census.py --check
  python scripts/ci/maintenance-loop-census.py --list
"""

import argparse
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DOC = ROOT / "docs" / "fork" / "maintenance-loop-census.md"

TICKER = re.compile(r"time\.(?:NewTicker|Tick)\(")
AFTER_FUNC = re.compile(r"time\.AfterFunc\(")

EXCLUDED_DIRS = (
    ".git",
    "vendor",
    "node_modules",
)
# Paths outside the product surface. They are still counted by --list (scope=tool) so the document
# can name them; the check only requires them when they are in the product tree list.
TOOL_PREFIXES = (
    "test/",
    "scripts/",
    "common/trafficsched/harness/",
    "test/xhttpbench/",
)


def iter_go_files():
    for path in ROOT.rglob("*.go"):
        rel = path.relative_to(ROOT).as_posix()
        if path.name.endswith("_test.go"):
            continue
        if any(part in rel.split("/") for part in EXCLUDED_DIRS):
            continue
        yield path, rel


def ticker_sites():
    sites = []
    for path, rel in iter_go_files():
        try:
            lines = path.read_text(encoding="utf-8").splitlines()
        except UnicodeDecodeError:
            continue
        for number, line in enumerate(lines, 1):
            if TICKER.search(line):
                scope = "tool" if rel.startswith(TOOL_PREFIXES) else "product"
                sites.append((rel, number, scope, line.strip()))
    return sorted(sites)


def after_func_sites():
    """Recurring AfterFunc loops are maintenance loops too; the document lists the four known
    self-rescheduling ones. One-shot timeouts are deliberately not part of this census."""
    sites = []
    for path, rel in iter_go_files():
        try:
            lines = path.read_text(encoding="utf-8").splitlines()
        except UnicodeDecodeError:
            continue
        for number, line in enumerate(lines, 1):
            if AFTER_FUNC.search(line):
                sites.append((rel, number, line.strip()))
    return sorted(sites)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--list", action="store_true")
    parser.add_argument("--after-func", action="store_true")
    args = parser.parse_args()

    sites = ticker_sites()
    if args.list:
        for rel, number, scope, line in sites:
            print(f"{rel}:{number}\t{scope}\t{line}")
    if args.after_func:
        for rel, number, line in after_func_sites():
            print(f"{rel}:{number}\tafterfunc\t{line}")
    if args.check:
        text = DOC.read_text(encoding="utf-8")
        missing = [f"{rel}:{number}" for rel, number, _scope, _line in sites if f"{rel}:{number}" not in text]
        if missing:
            print("maintenance-loop census is missing ticker sites:", file=sys.stderr)
            for site in missing:
                print(f"  {site}", file=sys.stderr)
            return 1
        print(f"maintenance-loop census: all {len(sites)} ticker sites are documented")
    return 0


if __name__ == "__main__":
    sys.exit(main())
