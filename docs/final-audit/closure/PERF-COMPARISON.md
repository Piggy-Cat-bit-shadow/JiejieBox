# Performance comparison (closure scope)

Only the wireguard-go pin changed on a data path. The stage-1/2 matrices are
therefore not rerun: re-running unaffected protocols after a schema/report fix
would produce different host-state numbers without a code delta, and the
instruction explicitly warns against that.

## B7 cell before/after (same host, loopback, two processes)

| cell | old pin `a765d957fd81` | new pin `7ece3d66d79d` |
|---|---|---|
| wireguard upload, 2 streams, 0 ms spacing | fails 2/2 (sent=0) | 3/3 pass, 42.9–53.9 MiB/s |
| wireguard upload, 2 streams, 200 ms | fails 2/2 | 3/3 pass, 47.5–51.8 MiB/s |
| wireguard upload, 4 streams, 0 ms | fails 3/3 (stage 3) | 3/3 pass, 49.8–54.8 MiB/s |
| awg upload, 2 streams, 0 ms | fails (stage 1/3) | 3/3 pass, 38.6–53.7 MiB/s |
| awg upload, 4 streams, 0 ms | fails 3/3 (stage 3) | 3/3 pass, 41.5–53.8 MiB/s |

The full matrix (WG+AWG × 0/200 ms/1 s × 2/4 streams × 3 reps) is 36/36 pass;
see `WG-AWG-B7.md`. The 1 s AWG 4-stream cells are lower (15–24 MiB/s) but
complete; with no old-pin equivalent (it stalls) this is not called a regression.

## Everything else

No production path changed outside the wireguard-go fork and the lab's schema
validation. The 17 stage-3 runs and 25 stage-1 runs remain the performance
record; their numbers are unchanged and they now validate against schema v1.1.
Claims remain limited to WSL2/loopback and the userspace data plane; privileged
TUN, Cronet native, macOS/Android/Windows runtime cells stay NOT_RUN.
