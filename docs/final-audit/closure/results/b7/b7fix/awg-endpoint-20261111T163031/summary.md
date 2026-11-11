# awg-endpoint - awg

- run_id: `awg-endpoint-20261111T163031`
- kind: e2e
- started: 2026-11-11T16:30:31+08:00
- commit: `fa256e89afd8` (merge: start the closure round from the current testing tip)
- branch: `work/closure-2026-11-11`
- **dirty worktree**: 4 file(s)
- go: go version go1.25.5 linux/amd64
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 2 | upload | 3 | 55.52 | 0.41 | 54.02 | 55.92 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 55.01 | 1.91 | 51.90 | 56.92 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 58.20 | 2.18 | 54.84 | 60.38 | null | s/GiB |
| goodput | 4 | upload | 3 | 17.79 | 2.41 | 15.38 | 24.61 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 115.45 | 17.44 | 94.05 | 132.89 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 112.70 | 10.79 | 89.16 | 123.49 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 169.8 | 3.036 | 55.9 | 55.01 | 58.20 | null | null | null | null | null |
| 1 | 2 | upload | 165.7 | 3.067 | 54.0 | 56.92 | 60.38 | null | null | null | null | null |
| 2 | 2 | upload | 166.9 | 3.007 | 55.5 | 51.90 | 54.84 | null | null | null | null | null |
| 0 | 4 | upload | 79.5 | 3.229 | 24.6 | 94.05 | 89.16 | null | null | null | null | null |
| 1 | 4 | upload | 59.6 | 3.351 | 17.8 | 115.45 | 112.70 | null | null | null | null | null |
| 2 | 4 | upload | 49.0 | 3.187 | 15.4 | 132.89 | 123.49 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 9.120 | 99.5 | 16 | 17 | 9.650 | 87.4 | 17 | 16 |
| 1 | 9.210 | 102.0 | 18 | 17 | 9.770 | 97.2 | 17 | 16 |
| 2 | 8.460 | 104.3 | 18 | 17 | 8.940 | 99.0 | 18 | 16 |
| 0 | 7.300 | 108.8 | 19 | 19 | 6.920 | 100.7 | 18 | 18 |
| 1 | 6.720 | 110.8 | 21 | 19 | 6.560 | 110.3 | 18 | 18 |
| 2 | 6.360 | 110.8 | 21 | 19 | 5.910 | 127.1 | 18 | 18 |
