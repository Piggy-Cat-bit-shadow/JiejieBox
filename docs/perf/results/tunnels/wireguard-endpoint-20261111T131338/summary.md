# wireguard-endpoint - wireguard

- run_id: `wireguard-endpoint-20261111T131338`
- kind: e2e
- started: 2026-11-11T13:13:38+08:00
- commit: `49679c0e9273` (docs(perf): record that the remote advanced by docs-only commits during the baseline)
- branch: `work/perf-lab-2026-11-11`
- **dirty worktree**: 11 file(s)
- go: go1.25.5
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 49.21 | 1.00 | 48.21 | 50.42 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 54.82 | 1.18 | 53.63 | 56.45 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 57.60 | 1.35 | 55.94 | 58.95 | null | s/GiB |
| goodput | 1 | download | 1 | 0.22 | 0.00 | 0.22 | 0.22 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 1 | 293.67 | 0.00 | 293.67 | 293.67 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 1 | 344.01 | 0.00 | 344.01 | 344.01 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 252.8 | 5.014 | 50.4 | 53.63 | 55.94 | null | null | null | null | null |
| 1 | 1 | upload | 246.6 | 5.011 | 49.2 | 54.82 | 57.60 | null | null | null | null | null |
| 2 | 1 | upload | 242.1 | 5.023 | 48.2 | 56.45 | 58.95 | null | null | null | null | null |
| 0 | 1 | download | 1.2 | 5.501 | 0.2 | 293.67 | 344.01 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 13.240 | 98.9 | 18 | 16 | 13.810 | 95.0 | 16 | 15 |
| 1 | 13.200 | 105.5 | 18 | 16 | 13.870 | 103.9 | 17 | 15 |
| 2 | 13.350 | 106.7 | 19 | 16 | 13.940 | 111.6 | 17 | 15 |
| 0 | 0.350 | 82.8 | 16 | 16 | 0.410 | 83.6 | 18 | 16 |

## Failures

- **measure** (no-payload): no payload arrived for download at 1 stream(s): sent=0 target_received=0 client_received=0
- **measure** (no-payload): no payload arrived for download at 1 stream(s): sent=0 target_received=0 client_received=0
- **measure** (no-payload): no datagrams returned at 1 stream(s): sent=282171
- **measure** (no-payload): no datagrams returned at 1 stream(s): sent=284994
- **measure** (no-payload): no datagrams returned at 1 stream(s): sent=282532
