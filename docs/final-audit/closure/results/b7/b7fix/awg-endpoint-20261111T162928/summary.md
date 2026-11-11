# awg-endpoint - awg

- run_id: `awg-endpoint-20261111T162928`
- kind: e2e
- started: 2026-11-11T16:29:28+08:00
- commit: `fa256e89afd8` (merge: start the closure round from the current testing tip)
- branch: `work/closure-2026-11-11`
- **dirty worktree**: 4 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 2 | upload | 3 | 48.16 | 5.57 | 38.57 | 53.73 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 61.84 | 4.52 | 57.32 | 73.67 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 65.85 | 4.88 | 60.97 | 78.36 | null | s/GiB |
| goodput | 4 | upload | 3 | 48.46 | 5.29 | 41.53 | 53.75 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 60.08 | 1.47 | 58.61 | 69.11 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 68.10 | 1.18 | 66.92 | 77.18 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 162.9 | 3.032 | 53.7 | 57.32 | 60.97 | null | null | null | null | null |
| 1 | 2 | upload | 145.6 | 3.022 | 48.2 | 61.84 | 65.85 | null | null | null | null | null |
| 2 | 2 | upload | 117.7 | 3.052 | 38.6 | 73.67 | 78.36 | null | null | null | null | null |
| 0 | 4 | upload | 126.8 | 3.054 | 41.5 | 69.11 | 77.18 | null | null | null | null | null |
| 1 | 4 | upload | 157.6 | 3.252 | 48.5 | 58.61 | 68.10 | null | null | null | null | null |
| 2 | 4 | upload | 163.3 | 3.038 | 53.8 | 60.08 | 66.92 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 9.120 | 105.0 | 17 | 19 | 9.700 | 102.2 | 17 | 18 |
| 1 | 8.790 | 111.3 | 19 | 19 | 9.360 | 105.2 | 17 | 18 |
| 2 | 8.470 | 112.8 | 20 | 19 | 9.010 | 114.4 | 17 | 18 |
| 0 | 8.560 | 115.9 | 20 | 23 | 9.560 | 123.9 | 18 | 22 |
| 1 | 9.020 | 121.5 | 20 | 23 | 10.480 | 127.6 | 18 | 22 |
| 2 | 9.580 | 124.4 | 20 | 22 | 10.670 | 138.5 | 18 | 21 |
