#!/usr/bin/env python3
"""Rebuild the B8 tables from raw run.json files.

The report is derived, never typed: every number comes from one rep record and
the invariants are checked before a row is printed. Legacy runs that predate the
split observation fields are labelled legacy and their lost/loss_percent are not
presented as current-semantics loss.

Usage:
  report-b8.py [RUN_DIR ...] [--out FILE] [--check]
Exit codes: 0 ok, 1 invariant/schema failure, 2 usage, 3 jsonschema missing.
"""

import argparse
import glob
import json
import os
import sys

try:
    from jsonschema import Draft202012Validator
except ImportError:
    print("report-b8: python3-jsonschema is required", file=sys.stderr)
    sys.exit(3)

HERE = os.path.dirname(os.path.abspath(__file__))
SCHEMA = os.path.join(HERE, "schema", "perf-run.v1.2.schema.json")
DEFAULTS = [
    os.path.join(HERE, "..", "..", "docs", "final-audit", "closure", "results", "b8-rate"),
]


def load_schema():
    with open(SCHEMA, encoding="utf-8") as handle:
        return json.load(handle)


def reps(paths):
    schema = load_schema()
    validator = Draft202012Validator(schema)
    rows = []
    failures = []
    for root in paths:
        for path in sorted(glob.glob(os.path.join(root, "**", "run.json"), recursive=True)):
            with open(path, encoding="utf-8") as handle:
                document = json.load(handle)
            errors = list(validator.iter_errors(document))
            if errors:
                failures.append(f"{path}: schema: {errors[0].message}")
                continue
            params = (document.get("scenario", {}).get("extra") or {}).get("load_parameters") or {}
            for rep in document.get("reps", []):
                udp = rep.get("udp")
                if not udp:
                    continue
                writes = udp.get("writes")
                unique = udp.get("unique")
                scheduled = udp.get("scheduled")
                attempts = udp.get("attempts")
                skipped = udp.get("skipped") or 0
                returned = udp.get("returned")
                duplicate = udp.get("duplicate") or 0
                if None not in (scheduled, attempts) and scheduled != attempts + skipped:
                    failures.append(f"{path}: scheduled {scheduled} != attempts {attempts} + skipped {skipped}")
                if None not in (writes, attempts) and writes > attempts:
                    failures.append(f"{path}: writes {writes} > attempts {attempts}")
                if None not in (unique, returned) and unique > returned:
                    failures.append(f"{path}: unique {unique} > returned {returned}")
                if None not in (returned, unique, duplicate) and returned != unique + duplicate:
                    failures.append(f"{path}: returned {returned} != unique {unique} + duplicate {duplicate}")
                legacy = udp.get("drain_status") is None or udp.get("loss_estimable") is None
                status = udp.get("drain_status") or ("legacy_complete" if udp.get("drain_complete") else "legacy_incomplete")
                estimable = udp.get("loss_estimable")
                loss = udp.get("loss_percent") if estimable else None
                target = udp.get("target_received")
                rows.append({
                    "file": os.path.relpath(path, os.path.commonpath(paths)) if len(paths) > 1 else os.path.basename(os.path.dirname(path)),
                    "scenario": document.get("scenario", {}).get("id", "?"),
                    "rate": params.get("udp_rate", 0),
                    "drain_ms": udp.get("drain_ms"),
                    "writes": writes,
                    "unique": unique,
                    "target": target,
                    "not_returned": (writes - unique) if None not in (writes, unique) else None,
                    "returned": returned,
                    "loss": loss,
                    "status": status,
                    "estimable": estimable,
                    "legacy": legacy,
                })
    return rows, failures


def render(rows):
    if not rows:
        return "no B8 reps found\n"
    lines = ["# B8 table regenerated from raw JSON", ""]
    lines.append("Loss is shown only when the record says loss_estimable=true; `not_returned` is")
    lines.append("writes-unique and is a deadline fact, not network-confirmed loss. Legacy runs")
    lines.append("predate the split observation fields and are labelled `legacy_*`.")
    lines.append("")
    lines.append("| scenario | rate pps | drain ms | writes | unique | target | not_returned | loss % | drain status |")
    lines.append("|---|---:|---:|---:|---:|---:|---:|---:|---|")
    for row in sorted(rows, key=lambda r: (r["scenario"], r["rate"], str(r["drain_ms"]), r["file"])):
        loss = "" if row["loss"] is None else f"{row['loss']:.2f}"
        lines.append("| {scenario} | {rate} | {drain} | {writes} | {unique} | {target} | {nr} | {loss} | {status} |".format(
            scenario=row["scenario"], rate=row["rate"], drain=fmt(row["drain_ms"]), writes=row["writes"],
            unique=row["unique"], target=fmt(row["target"]), nr=fmt(row["not_returned"]), loss=loss, status=row["status"]))
    return "\n".join(lines) + "\n"


def fmt(value):
    if value is None:
        return "null"
    if isinstance(value, float):
        return f"{value:.1f}"
    return str(value)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("paths", nargs="*", default=DEFAULTS)
    parser.add_argument("--out")
    parser.add_argument("--check", help="compare rendered text with this file")
    args = parser.parse_args()
    rows, failures = reps(args.paths)
    text = render(rows)
    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    if args.check:
        with open(args.check, encoding="utf-8") as handle:
            existing = handle.read()
        if existing != text:
            print(f"report-b8: {args.check} is not the deterministic output", file=sys.stderr)
            return 1
        print(f"{args.check}: deterministic output matches")
        return 0
    if args.out:
        with open(args.out, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(text)
        print(f"wrote {args.out} ({len(rows)} reps)")
    else:
        sys.stdout.write(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
