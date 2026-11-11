# wireguard-endpoint - wireguard

- run_id: `wireguard-endpoint-20261111T145234`
- kind: e2e
- started: 2026-11-11T14:52:34+08:00
- commit: `f30e36a4dea1` (merge: bring the coupling-audit branch onto the current testing tip)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 3 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 1 | 51.01 | 0.00 | 51.01 | 51.01 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 1 | 53.16 | 0.00 | 53.16 | 53.16 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 1 | 55.44 | 0.00 | 55.44 | 55.44 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 255.1 | 5.000 | 51.0 | 53.16 | 55.44 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 13.240 | 98.5 | 19 | 15 | 13.810 | 91.2 | 15 | 14 |

## Failures

- **measure** (no-payload): no payload arrived for upload at 1 stream(s): sent=0 target_received=0 client_received=0
- **measure** (no-payload): no payload arrived for upload at 1 stream(s): sent=0 target_received=0 client_received=0
- **measure** (no-payload): no payload arrived for upload at 4 stream(s): sent=0 target_received=0 client_received=0
- **measure** (no-payload): no payload arrived for upload at 4 stream(s): sent=0 target_received=0 client_received=0
- **measure** (no-payload): no payload arrived for upload at 4 stream(s): sent=0 target_received=0 client_received=0
