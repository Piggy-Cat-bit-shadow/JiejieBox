# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T133015`
- kind: e2e
- started: 2026-11-11T13:30:15+08:00
- commit: `49679c0e9273` (docs(perf): record that the remote advanced by docs-only commits during the baseline)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 40 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss0 delay=40.0ms loss=0.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 10.56 | 0.18 | 10.37 | 11.01 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 128.55 | 12.74 | 115.81 | 165.27 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 151.25 | 25.16 | 126.09 | 195.67 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.20 | 0.00 | 0.20 | 0.21 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.29 | 0.02 | 2.27 | 2.32 | null | ratio |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 51.9 | 5.000 | 10.4 | 165.27 | 195.67 | 0.20 | 2.32 | null | null | null |
| 1 | 1 | udp | 55.0 | 5.000 | 11.0 | 128.55 | 151.25 | 0.21 | 2.29 | null | null | null |
| 2 | 1 | udp | 52.8 | 5.000 | 10.6 | 115.81 | 126.09 | 0.20 | 2.27 | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.370 | 64.7 | 15 | 13 | 9.910 | 67.6 | 15 | 14 |
| 1 | 6.910 | 64.9 | 17 | 13 | 8.130 | 68.0 | 16 | 14 |
| 2 | 5.970 | 65.0 | 17 | 13 | 6.500 | 68.5 | 17 | 14 |
