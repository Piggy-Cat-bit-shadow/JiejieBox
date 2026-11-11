# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T133100`
- kind: e2e
- started: 2026-11-11T13:31:00+08:00
- commit: `49679c0e9273` (docs(perf): record that the remote advanced by docs-only commits during the baseline)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 40 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss0 delay=40.0ms loss=0.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 6.64 | 1.97 | 4.67 | 11.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 233.74 | 76.73 | 156.35 | 310.47 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 175.77 | 57.52 | 117.75 | 233.29 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.12 | 0.04 | 0.09 | 0.21 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.19 | 0.00 | 2.19 | 2.19 | null | ratio |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 57.8 | 5.000 | 11.6 | 156.35 | 117.75 | 0.21 | 2.19 | null | null | null |
| 1 | 1 | udp | 33.2 | 5.000 | 6.6 | 233.74 | 175.77 | 0.12 | 2.19 | null | null | null |
| 2 | 1 | udp | 23.4 | 5.000 | 4.7 | 310.47 | 233.29 | 0.09 | 2.19 | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.830 | 72.0 | 20 | 14 | 6.650 | 72.2 | 18 | 13 |
| 1 | 7.580 | 72.0 | 20 | 14 | 5.700 | 72.2 | 18 | 15 |
| 2 | 7.080 | 72.0 | 20 | 14 | 5.320 | 72.6 | 18 | 17 |
