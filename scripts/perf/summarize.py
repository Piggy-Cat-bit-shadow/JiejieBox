#!/usr/bin/env python3
"""Summarize a baseline tree written by run-baseline.sh and perflab.

Reads every run.json under the results root and prints one Markdown table per
scenario, grouped by transport guarantee. Nothing is recomputed from raw
samples: the runner already reduced each run and this script only re-formats the
summary rows, so two documents cannot disagree.

Usage:
    summarize.py <results-root> [--out matrix.md] [--json matrix.json]
"""

import argparse
import json
import os
import pathlib
import sys

# Cells are grouped by transport guarantee, not by speed. A QUIC DATAGRAM cell
# and a reliable stream carrying datagrams are never placed in one ranking.
GROUPS = [
    ("TCP with TLS", ["vless-vision-tcp", "vless-reality-vision", "anytls-tcp"]),
    ("HTTP/2", ["trusttunnel-h2", "naive-h2", "xhttp-h2"]),
    ("HTTP/3 and QUIC", ["trusttunnel-h3", "naive-h3", "xhttp-h3", "masque-connect-ip"]),
    ("UDP underlay and userspace tunnels",
     ["mieru-tcp-underlay", "mieru-udp-underlay", "wireguard-endpoint", "awg-endpoint"]),
]

# A run can appear under several network profiles; they are separate records and
# are never merged into one median.
def load_runs(root):
    records = []
    for path in sorted(pathlib.Path(root).rglob("run.json")):
        try:
            with open(path, "r", encoding="utf-8") as handle:
                run = json.load(handle)
        except (OSError, json.JSONDecodeError) as error:
            print(f"warn: {path}: {error}", file=sys.stderr)
            continue
        network = run.get("network") or {}
        # The cell class is the network profile when there is one, and the
        # results subdirectory otherwise. The directory matters: loopback
        # throughput cells, loopback duplex/ping-pong cells and native-engine
        # re-reads share the loopback topology but are separate experiments.
        shape = network.get("profile")
        if not shape:
            shape = path.parent.parent.name or "unknown"
        records.append({
            "scenario": (run.get("scenario") or {}).get("id") or path.parent.name,
            "shape": shape,
            "run": run,
            "dir": str(path.parent),
        })
    records.sort(key=lambda item: (item["scenario"], item["shape"]))
    return records


def fmt(value, digits=1):
    if value is None:
        return "null"
    return f"{value:.{digits}f}"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("root")
    parser.add_argument("--out")
    parser.add_argument("--json")
    args = parser.parse_args()

    records = load_runs(args.root)
    if not records:
        print(f"no run.json under {args.root}", file=sys.stderr)
        return 1

    # The same cell can be run more than once (a retry, a fixed harness). The
    # newest run wins for that (scenario, network) pair; the rest are counted so
    # a stale directory cannot quietly contribute a row.
    newest = {}
    superseded = 0
    for record in records:
        key = (record["scenario"], record["shape"])
        previous = newest.get(key)
        if previous is None:
            newest[key] = record
            continue
        superseded += 1
        if os.path.getmtime(os.path.join(record["dir"], "run.json")) > \
                os.path.getmtime(os.path.join(previous["dir"], "run.json")):
            newest[key] = record
    records = sorted(newest.values(), key=lambda item: (item["scenario"], item["shape"]))
    if superseded:
        print(f"note: {superseded} superseded run(s) ignored", file=sys.stderr)

    lines = ["# Performance matrix", ""]
    lines.append("Every row below is a real server process and a real client process. "
                 "`null` means not measured.")
    lines.append("")
    lines.append("| scenario | network | commit | repetitions | run dir |")
    lines.append("|---|---|---|---|---|")
    for record in records:
        run = record["run"]
        source = run.get("source") or {}
        lines.append("| {} | {} | `{}` | {} | `{}` |".format(
            record["scenario"], record["shape"],
            (source.get("commit") or "")[:12], len(run.get("reps") or []),
            os.path.relpath(record["dir"], args.root)))
    lines.append("")

    for title, members in GROUPS:
        present = [record for record in records if record["scenario"] in members]
        if not present:
            continue
        lines.append(f"## {title}")
        lines.append("")
        for record in present:
            run = record["run"]
            scenario = run.get("scenario") or {}
            extra = scenario.get("extra") or {}
            lines.append(f"### {record['scenario']} — {record['shape']}")
            lines.append("")
            lines.append(f"- run dir: `{os.path.relpath(record['dir'], args.root)}`")
            lines.append(f"- protocol: {scenario.get('protocol')} / "
                         f"{scenario.get('transport', '')} / {scenario.get('security', '')}".rstrip())
            if extra.get("startup_client_ms") is not None:
                lines.append(f"- startup: client {extra['startup_client_ms']:.0f} ms, "
                             f"server {extra.get('startup_server_ms', 0):.0f} ms")
            network = run.get("network") or {}
            if network.get("delay_ms") is not None:
                detail = f"- network: {record['shape']}, one-way delay {network['delay_ms']:.1f} ms"
                if network.get("loss_percent") is not None:
                    detail += f", loss {network['loss_percent']:.2f}% per direction"
                if network.get("bandwidth_mbps") is not None:
                    detail += f", cap {network['bandwidth_mbps']:.0f} Mbit/s"
                lines.append(detail)
            lines.append("")

            summary = {(row["metric"], row.get("streams", 0), row.get("pattern", "")): row
                       for row in run.get("summary") or []}
            keys = sorted({(key[2], key[1]) for key in summary})
            lines.append("| pattern | streams | n | goodput MiB/s | CPU s/GiB client | "
                         "CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |")
            lines.append("|---|---|---|---|---|---|---|---|---|---|---|")
            for pattern, streams in keys:
                def med(metric, digits=1):
                    row = summary.get((metric, streams, pattern))
                    return fmt(None if row is None else row.get("median"), digits)

                def count(metric):
                    row = summary.get((metric, streams, pattern))
                    return 0 if row is None else row.get("n", 0)

                lines.append("| {} | {} | {} | {} | {} | {} | {} | {} | {} | {} | {} |".format(
                    pattern, streams, count("goodput"),
                    med("goodput"), med("cpu_seconds_per_gib_client", 2),
                    med("cpu_seconds_per_gib_server", 2), med("wire_gbit_per_s", 2),
                    med("wire_overhead_ratio", 2), med("latency_p50", 2),
                    med("latency_p95", 2), med("latency_p99", 2)))
            lines.append("")

    text = "\n".join(lines).rstrip() + "\n"
    if args.out:
        pathlib.Path(args.out).write_text(text, encoding="utf-8")
        print(f"wrote {args.out}")
    else:
        print(text)

    if args.json:
        payload = []
        for record in records:
            run = record["run"]
            payload.append({
                "scenario": record["scenario"],
                "network": record["shape"],
                "run_dir": os.path.relpath(record["dir"], args.root),
                "commit": (run.get("source") or {}).get("commit"),
                "summary": run.get("summary") or [],
            })
        pathlib.Path(args.json).write_text(json.dumps(payload, indent=2), encoding="utf-8")
        print(f"wrote {args.json}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
