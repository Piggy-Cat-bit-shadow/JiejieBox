# awg-endpoint - awg

- run_id: `awg-endpoint-20261111T162958`
- kind: e2e
- started: 2026-11-11T16:29:58+08:00
- commit: `fa256e89afd8` (merge: start the closure round from the current testing tip)
- branch: `work/closure-2026-11-11`
- **dirty worktree**: 4 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 2 | upload | 3 | 49.37 | 0.82 | 48.28 | 50.19 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 59.97 | 0.49 | 59.47 | 62.19 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 63.27 | 0.01 | 63.26 | 65.25 | null | s/GiB |
| goodput | 4 | upload | 3 | 54.04 | 2.20 | 42.25 | 56.24 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 58.46 | 1.79 | 56.67 | 66.32 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 64.92 | 3.05 | 61.87 | 71.82 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 147.4 | 3.052 | 48.3 | 62.19 | 65.25 | null | null | null | null | null |
| 1 | 2 | upload | 152.0 | 3.028 | 50.2 | 59.97 | 63.27 | null | null | null | null | null |
| 2 | 2 | upload | 151.3 | 3.066 | 49.4 | 59.47 | 63.26 | null | null | null | null | null |
| 0 | 4 | upload | 136.0 | 3.219 | 42.3 | 66.32 | 71.82 | null | null | null | null | null |
| 1 | 4 | upload | 166.4 | 3.079 | 54.0 | 58.46 | 64.92 | null | null | null | null | null |
| 2 | 4 | upload | 173.3 | 3.081 | 56.2 | 56.67 | 61.87 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.950 | 99.3 | 19 | 17 | 9.390 | 96.1 | 17 | 16 |
| 1 | 8.900 | 102.7 | 19 | 17 | 9.390 | 128.5 | 17 | 16 |
| 2 | 8.790 | 106.0 | 19 | 18 | 9.350 | 138.2 | 17 | 17 |
| 0 | 8.810 | 116.8 | 19 | 19 | 9.540 | 136.7 | 17 | 18 |
| 1 | 9.500 | 121.6 | 19 | 20 | 10.550 | 136.7 | 17 | 19 |
| 2 | 9.590 | 121.9 | 19 | 19 | 10.470 | 136.7 | 17 | 18 |
