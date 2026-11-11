#!/usr/bin/env python3
"""Summarize a baseline matrix written by run-baseline.sh.

Reads every run.json under the given results root and prints one Markdown table
per protocol family, with the median, MAD and sample count that the run itself
recorded. Nothing is recomputed from raw samples here: the runner already did
that, and recomputing it in a second place is how two documents end up
disagreeing.

Usage:
    summarize.py <results-root> [--out matrix.md] [--json matrix.json]
"""

import argparse
import json
import os
import pathlib
import sys

# Protocols are grouped by transport guarantee, not by speed. Two cells whose
# transport promises differ (QUIC DATAGRAM vs a reliable stream carrying
# datagrams) are never placed in the same ranking.
GROUPS = [
    ("TCP with TLS", ["vless-vision-tcp", "vless-reality-vision", "anytls-tcp"]),
    ("HTTP/2", ["trusttunnel-h2", "naive-h2"]),
    ("HTTP/3 and QUIC", ["trusttunnel-h3", "naive-h3", "masque-connect-ip"]),
    ("UDP underlay / userspace tunnels", ["mieru-tcp-underlay", "mieru-udp-underlay",
                                          "wireguard-endpoint", "awg-endpoint"]),
]


def load_runs(root):
    runs = {}
    for path in sorted(pathlib.Path(root).rglob("run.json")):
        try:
            with open(path, "r", encoding="utf-8") as handle:
                run = json.load(handle)
        except (OSError, json.JSONDecodeError) as error:
            print(f"warn: {path}: {error}", file=sys.stderr)
            continue
        scenario = run.get("scenario", {}).get("id", path.parent.name)
        runs[scenario] = (run, str(path.parent))
    return runs


def fmt(value, digits=1, suffix=""):
    if value is None:
        return "null"
    return f"{value:.{digits}f}{suffix}"


def summarize(run):
    """Return {(metric, streams, pattern): summary_row}."""
    rows = {}
    for row in run.get("summary", []):
        rows[(row["metric"], row.get("streams", 0), row.get("pattern", ""))] = row
    return rows


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("root")
    parser.add_argument("--out")
    parser.add_argument("--json")
    args = parser.parse_args()

    runs = load_runs(args.root)
    if not runs:
        print(f"no run.json under {args.root}", file=sys.stderr)
        return 1

    lines = []
    lines.append("# Performance matrix")
    lines.append("")
    lines.append("| scenario | commit | network | cells |")
    lines.append("|---|---|---|---|")

    for scenario in sorted(runs):
        run, directory = runs[scenario]
        source = run.get("source", {})
        network = run.get("network", {})
        shape = network.get("profile") or network.get("topology", "")
        lines.append(f"| {scenario} | `{source.get('commit', '')[:12]}` | {shape} | "
                     f"{len(run.get('reps', []))} |")
    lines.append("")

    for title, scenarios in GROUPS:
        present = [s for s in scenarios if s in runs]
        if not present:
            continue
        lines.append(f"## {title}")
        lines.append("")
        for scenario in present:
            run, directory = runs[scenario]
            extra = run.get("scenario", {}).get("extra") or {}
            lines.append(f"### {scenario}")
            lines.append("")
            lines.append(f"- run dir: `{os.path.relpath(directory, args.root)}`")
            lines.append(f"- protocol: {run['scenario'].get('protocol')} / "
                         f"{run['scenario'].get('transport', '')} / {run['scenario'].get('security', '')}")
            if extra.get("startup_client_ms") is not None:
                lines.append(f"- startup: client {extra['startup_client_ms']:.0f} ms, "
                             f"server {extra.get('startup_server_ms', 0):.0f} ms")
            lines.append("")
            lines.append("| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |")
            lines.append("|---|---|---|---|---|---|---|---|---|---|---|")
            summary = summarize(run)
            keys = sorted({(k[2], k[1]) for k in summary})
            for pattern, streams in keys:
                goodput = summary.get(("goodput", streams, pattern))
                cpu_client = summary.get(("cpu_seconds_per_gib_client", streams, pattern))
                cpu_server = summary.get(("cpu_seconds_per_gib_server", streams, pattern))
                wire = summary.get(("wire_gbit_per_s", streams, pattern))
                overhead = summary.get(("wire_overhead_ratio", streams, pattern))
                p50 = summary.get(("latency_p50", streams, pattern))
                p95 = summary.get(("latency_p95", streams, pattern))
                p99 = summary.get(("latency_p99", streams, pattern))

                def med(row, digits=1):
                    return fmt(None if row is None else row.get("median"), digits)

                lines.append("| {} | {} | {} | {} | {} | {} | {} | {} | {} | {} | {} |".format(
                    pattern, streams,
                    goodput.get("n") if goodput else 0,
                    med(goodput), med(cpu_client, 2), med(cpu_server, 2),
                    med(wire, 2), med(overhead, 2),
                    med(p50, 2), med(p95, 2), med(p99, 2)))
            lines.append("")

    text = "\n".join(lines) + "\n"
    if args.out:
        pathlib.Path(args.out).write_text(text, encoding="utf-8")
        print(f"wrote {args.out}")
    else:
        print(text)

    if args.json:
        payload = {}
        for scenario, (run, directory) in runs.items():
            payload[scenario] = {
                "run_dir": os.path.relpath(directory, args.root),
                "commit": run.get("source", {}).get("commit"),
                "network": run.get("network", {}),
                "summary": run.get("summary", []),
            }
        pathlib.Path(args.json).write_text(json.dumps(payload, indent=2), encoding="utf-8")
        print(f"wrote {args.json}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
