#!/usr/bin/env python3
"""Validate perflab run.json files against the run schema.

The schema is a contract, not documentation: a run that does not validate is a
run whose numbers cannot be compared across cells. Two revisions are kept:

  perf-run.v1.schema.json    frozen, as published with the first 48 runs
  perf-run.v1.2.schema.json  the same payload, with wire/latency/udp nullable
                             when the runner did not measure them

New data is validated against v1.1. Historical files are validated against the
revision they claim (`schema_version` stays `perf-run/v1`; only a breaking shape
change would become v2).

Usage:
  validate_runs.py [PATH...]                 validate files/dirs (default: repo docs)
  validate_runs.py --schema PATH [PATH...]   override the schema
  validate_runs.py --self-test               validate the fixture corpus

Exit codes: 0 all valid, 1 validation failures, 2 usage/IO error, 3 validator
dependency missing (python jsonschema), 4 a fixture behaved contrary to its name.
"""

import argparse
import glob
import json
import os
import sys

try:
    from jsonschema import Draft202012Validator, FormatChecker
except ImportError:  # pragma: no cover - environment guard
    print("validate_runs: python3-jsonschema is required (pip install jsonschema)", file=sys.stderr)
    sys.exit(3)

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_SCHEMA = os.path.join(HERE, "schema", "perf-run.v1.2.schema.json")
FIXTURES = os.path.join(HERE, "internal", "schema", "testdata")
REPO = os.path.dirname(os.path.dirname(HERE))
DEFAULT_ROOTS = [
    os.path.join(REPO, "docs", "perf", "results"),
    os.path.join(REPO, "docs", "coupling", "results"),
    os.path.join(REPO, "docs", "final-audit", "results"),
]


def load_schema(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def expand(paths, pattern="run.json"):
    files = []
    for path in paths:
        if os.path.isdir(path):
            files.extend(sorted(glob.glob(os.path.join(path, "**", pattern), recursive=True)))
        elif os.path.isfile(path):
            files.append(path)
        else:
            print(f"validate_runs: no such path: {path}", file=sys.stderr)
            sys.exit(2)
    return files


def validate(files, schema_path, verbose=False):
    schema = load_schema(schema_path)
    validator = Draft202012Validator(schema, format_checker=FormatChecker())
    failures = 0
    for path in files:
        try:
            with open(path, encoding="utf-8") as handle:
                document = json.load(handle)
        except (OSError, json.JSONDecodeError) as exc:
            failures += 1
            print(f"FAIL {path}: unreadable: {exc}")
            continue
        errors = sorted(validator.iter_errors(document), key=lambda err: list(err.absolute_path))
        if not errors:
            if verbose:
                print(f"ok   {path}")
            continue
        failures += 1
        print(f"FAIL {path}: {len(errors)} error(s)")
        for error in errors[:20]:
            pointer = "/" + "/".join(str(part) for part in error.absolute_path)
            print(f"  {pointer or '/'}: {error.message}")
        if len(errors) > 20:
            print(f"  ... {len(errors) - 20} more")
    print(f"validated {len(files)} file(s), {failures} failed, schema={os.path.basename(schema_path)}")
    return 1 if failures else 0


def self_test(schema_path):
    valid_files = expand([os.path.join(FIXTURES, "valid")], "*.json")
    invalid_files = expand([os.path.join(FIXTURES, "invalid")], "*.json")
    if not valid_files or not invalid_files:
        print("validate_runs: fixture corpus is empty", file=sys.stderr)
        return 2
    schema = load_schema(schema_path)
    validator = Draft202012Validator(schema, format_checker=FormatChecker())
    wrong = 0

    def check(path, expected_valid):
        nonlocal wrong
        with open(path, encoding="utf-8") as handle:
            document = json.load(handle)
        errors = list(validator.iter_errors(document))
        is_valid = not errors
        status = "ok  " if is_valid == expected_valid else "WRONG"
        print(f"{status} {os.path.relpath(path, FIXTURES)} expected={'valid' if expected_valid else 'invalid'} got={'valid' if is_valid else 'invalid'}")
        if is_valid != expected_valid:
            wrong += 1
            for error in errors[:5]:
                pointer = "/" + "/".join(str(part) for part in error.absolute_path)
                print(f"  {pointer or '/'}: {error.message}")

    for path in valid_files:
        check(path, True)
    for path in invalid_files:
        check(path, False)
    print(f"self-test: {len(valid_files)} valid + {len(invalid_files)} invalid fixtures, {wrong} wrong")
    return 4 if wrong else 0


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("paths", nargs="*", help="run.json files or directories (default: the three result trees)")
    parser.add_argument("--schema", default=DEFAULT_SCHEMA)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--verbose", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        return self_test(args.schema)
    paths = args.paths or [root for root in DEFAULT_ROOTS if os.path.isdir(root)]
    files = expand(paths)
    if not files:
        print("validate_runs: no run.json found", file=sys.stderr)
        return 2
    return validate(files, args.schema, args.verbose)


if __name__ == "__main__":
    sys.exit(main())
