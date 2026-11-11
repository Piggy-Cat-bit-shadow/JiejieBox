# mieru-tcp-underlay - mieru

- run_id: `mieru-tcp-underlay-20261111T140235`
- kind: e2e
- started: 2026-11-11T14:02:35+08:00
- commit: `53a9f5dc8e2d` (perf(baseline): re-read the Mieru cells with corrected metadata)
- branch: `work/coupling-audit-2026-11-11`
- **dirty worktree**: 9 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | upload | 3 | 121.89 | 0.01 | 121.88 | 127.04 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 8.17 | 0.01 | 8.09 | 8.19 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 5.91 | 0.06 | 5.85 | 6.16 | null | s/GiB |
| goodput | 4 | upload | 3 | 453.26 | 0.90 | 452.36 | 455.14 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 10.31 | 0.02 | 10.29 | 10.39 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 6.79 | 0.11 | 6.68 | 7.03 | null | s/GiB |
| goodput | 1 | download | 3 | 103.79 | 1.40 | 102.38 | 105.59 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 5.66 | 0.06 | 5.60 | 6.21 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 5.52 | 0.03 | 5.50 | 5.80 | null | s/GiB |
| goodput | 4 | download | 3 | 664.53 | 25.07 | 623.16 | 689.61 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 7.18 | 0.05 | 6.67 | 7.23 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 6.49 | 0.12 | 6.37 | 6.61 | null | s/GiB |
| latency_p50 | 1 | connect | 3 | 2.28 | 0.01 | 2.27 | 2.30 | null | ms |
| latency_p95 | 1 | connect | 3 | 2.89 | 0.05 | 2.84 | 3.07 | null | ms |
| latency_p99 | 1 | connect | 3 | 3.21 | 0.08 | 3.14 | 3.72 | null | ms |
| latency_p50 | 4 | connect | 3 | 7.58 | 0.63 | 3.43 | 8.21 | null | ms |
| latency_p95 | 4 | connect | 3 | 9.64 | 1.80 | 4.89 | 11.44 | null | ms |
| latency_p99 | 4 | connect | 3 | 10.74 | 2.83 | 7.91 | 19.04 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 610.0 | 5.005 | 121.9 | 8.17 | 6.16 | null | null | null | null | null |
| 1 | 1 | upload | 610.1 | 5.006 | 121.9 | 8.09 | 5.91 | null | null | null | null | null |
| 2 | 1 | upload | 635.4 | 5.002 | 127.0 | 8.19 | 5.85 | null | null | null | null | null |
| 0 | 4 | upload | 2279.1 | 5.008 | 455.1 | 10.29 | 7.03 | null | null | null | null | null |
| 1 | 4 | upload | 2269.8 | 5.008 | 453.3 | 10.31 | 6.79 | null | null | null | null | null |
| 2 | 4 | upload | 2265.6 | 5.008 | 452.4 | 10.39 | 6.68 | null | null | null | null | null |
| 0 | 1 | download | 528.0 | 5.000 | 105.6 | 6.21 | 5.80 | null | null | null | null | null |
| 1 | 1 | download | 519.0 | 5.000 | 103.8 | 5.66 | 5.52 | null | null | null | null | null |
| 2 | 1 | download | 512.0 | 5.001 | 102.4 | 5.60 | 5.50 | null | null | null | null | null |
| 0 | 4 | download | 3323.3 | 5.001 | 664.5 | 7.23 | 6.37 | null | null | null | null | null |
| 1 | 4 | download | 3116.1 | 5.001 | 623.2 | 6.67 | 6.49 | null | null | null | null | null |
| 2 | 4 | download | 3448.7 | 5.001 | 689.6 | 7.18 | 6.61 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.28 | 2.89 | 3.21 |
| 1 | 1 | connect | 0.0 | 5.001 | null | null | null | null | null | 2.27 | 2.84 | 3.14 |
| 2 | 1 | connect | 0.0 | 5.000 | null | null | null | null | null | 2.30 | 3.07 | 3.72 |
| 0 | 4 | connect | 0.0 | 5.004 | null | null | null | null | null | 3.43 | 4.89 | 7.91 |
| 1 | 4 | connect | 0.0 | 5.005 | null | null | null | null | null | 7.58 | 9.64 | 10.74 |
| 2 | 4 | connect | 0.0 | 5.012 | null | null | null | null | null | 8.21 | 11.44 | 19.04 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 4.870 | 62.3 | 16 | 12 | 3.670 | 66.2 | 16 | 12 |
| 1 | 4.820 | 62.7 | 17 | 13 | 3.520 | 69.9 | 17 | 13 |
| 2 | 5.080 | 63.0 | 17 | 14 | 3.630 | 77.1 | 20 | 14 |
| 0 | 22.900 | 71.4 | 19 | 20 | 15.640 | 96.9 | 21 | 20 |
| 1 | 22.850 | 72.1 | 20 | 21 | 15.060 | 124.0 | 22 | 21 |
| 2 | 22.980 | 72.1 | 20 | 23 | 14.790 | 126.2 | 22 | 23 |
| 0 | 3.200 | 72.1 | 20 | 20 | 2.990 | 124.6 | 22 | 20 |
| 1 | 2.870 | 72.9 | 20 | 20 | 2.800 | 124.6 | 22 | 20 |
| 2 | 2.800 | 79.1 | 20 | 21 | 2.750 | 124.6 | 22 | 21 |
| 0 | 23.470 | 105.8 | 20 | 24 | 20.670 | 128.3 | 22 | 24 |
| 1 | 20.300 | 139.9 | 21 | 28 | 19.740 | 128.9 | 22 | 28 |
| 2 | 24.190 | 148.8 | 21 | 30 | 22.260 | 132.2 | 22 | 30 |
| 0 | 4.900 | 147.3 | 21 | 91 | 4.380 | 130.8 | 22 | 90 |
| 1 | 4.930 | 150.9 | 21 | 122 | 4.210 | 131.2 | 22 | 121 |
| 2 | 4.970 | 164.9 | 21 | 144 | 4.220 | 133.6 | 22 | 143 |
| 0 | 13.780 | 206.4 | 21 | 198 | 10.400 | 159.1 | 22 | 196 |
| 1 | 6.310 | 218.8 | 21 | 212 | 16.350 | 164.7 | 22 | 211 |
| 2 | 6.100 | 219.1 | 21 | 224 | 16.490 | 164.7 | 22 | 223 |
