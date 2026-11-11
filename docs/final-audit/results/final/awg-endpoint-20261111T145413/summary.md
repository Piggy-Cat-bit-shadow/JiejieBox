# awg-endpoint - awg

- run_id: `awg-endpoint-20261111T145413`
- kind: e2e
- started: 2026-11-11T14:54:13+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 3 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 54.33 | 0.00 | 54.32 | 54.94 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 50.43 | 0.07 | 49.91 | 50.50 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 52.15 | 0.08 | 52.03 | 52.23 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 272.3 | 5.013 | 54.3 | 50.50 | 52.15 | null | null | null | null | null |
| 1 | 1 | upload | 272.5 | 5.016 | 54.3 | 50.43 | 52.23 | null | null | null | null | null |
| 2 | 1 | upload | 274.7 | 5.000 | 54.9 | 49.91 | 52.03 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 13.430 | 99.6 | 18 | 15 | 13.870 | 92.5 | 17 | 14 |
| 1 | 13.420 | 100.5 | 19 | 15 | 13.900 | 97.6 | 17 | 14 |
| 2 | 13.390 | 103.1 | 19 | 15 | 13.960 | 100.0 | 17 | 14 |

## Failures

- **measure** (no-payload): no payload arrived for upload at 4 stream(s): sent=0 target_received=0 client_received=0
- **measure** (no-payload): no payload arrived for upload at 4 stream(s): sent=0 target_received=0 client_received=0
- **measure** (no-payload): no payload arrived for upload at 4 stream(s): sent=0 target_received=0 client_received=0
