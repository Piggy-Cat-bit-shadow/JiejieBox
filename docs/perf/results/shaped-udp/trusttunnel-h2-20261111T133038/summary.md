# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T133038`
- kind: e2e
- started: 2026-11-11T13:30:38+08:00
- commit: `49679c0e9273` (docs(perf): record that the remote advanced by docs-only commits during the baseline)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 40 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss0.1 delay=40.0ms loss=0.10% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.79 | 0.22 | 0.56 | 3.35 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 98.64 | 10.66 | 87.98 | 112.53 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 88.26 | 0.89 | 87.37 | 112.53 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.02 | 0.00 | 0.01 | 0.06 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.37 | 0.13 | 2.24 | 2.61 | null | ratio |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 16.8 | 5.000 | 3.4 | 87.98 | 87.37 | 0.06 | 2.24 | null | null | null |
| 1 | 1 | udp | 3.9 | 5.000 | 0.8 | 98.64 | 88.26 | 0.02 | 2.37 | null | null | null |
| 2 | 1 | udp | 2.8 | 5.000 | 0.6 | 112.53 | 112.53 | 0.01 | 2.61 | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 1.440 | 64.0 | 12 | 13 | 1.430 | 66.4 | 14 | 14 |
| 1 | 0.380 | 64.5 | 13 | 13 | 0.340 | 66.5 | 15 | 14 |
| 2 | 0.310 | 65.0 | 15 | 13 | 0.310 | 66.8 | 15 | 14 |
