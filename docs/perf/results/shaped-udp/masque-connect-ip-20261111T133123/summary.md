# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T133123`
- kind: e2e
- started: 2026-11-11T13:31:23+08:00
- commit: `49679c0e9273` (docs(perf): record that the remote advanced by docs-only commits during the baseline)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 40 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss0.1 delay=40.0ms loss=0.10% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.85 | 0.26 | 0.59 | 1.90 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 1174.20 | 386.92 | 572.31 | 1561.11 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 515.76 | 9.04 | 302.86 | 524.79 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.02 | 0.01 | 0.01 | 0.04 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.68 | 0.07 | 2.41 | 2.75 | null | ratio |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 9.5 | 5.000 | 1.9 | 572.31 | 302.86 | 0.04 | 2.41 | null | null | null |
| 1 | 1 | udp | 4.3 | 5.000 | 0.9 | 1174.20 | 524.79 | 0.02 | 2.75 | null | null | null |
| 2 | 1 | udp | 3.0 | 5.000 | 0.6 | 1561.11 | 515.76 | 0.01 | 2.68 | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.310 | 70.4 | 17 | 14 | 2.810 | 72.1 | 17 | 13 |
| 1 | 4.900 | 70.3 | 17 | 14 | 2.190 | 71.8 | 17 | 15 |
| 2 | 4.510 | 70.3 | 18 | 14 | 1.490 | 72.0 | 17 | 17 |
