# xhttp-h2 - vless+xhttp

- run_id: `xhttp-h2-20261111T135555`
- kind: e2e
- started: 2026-11-11T13:55:55+08:00
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
| goodput | 1 | upload | 3 | 32.59 | 0.05 | 32.45 | 32.64 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 17.23 | 0.88 | 12.24 | 18.11 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 10.92 | 0.10 | 8.14 | 11.01 | null | s/GiB |
| goodput | 4 | upload | 3 | 127.20 | 0.33 | 126.73 | 127.53 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 15.58 | 0.42 | 15.16 | 16.89 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 12.04 | 0.40 | 11.64 | 13.33 | null | s/GiB |
| goodput | 1 | download | 3 | 83.38 | 0.24 | 83.14 | 83.87 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 15.65 | 0.07 | 15.58 | 15.79 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 18.79 | 0.10 | 18.68 | 18.89 | null | s/GiB |
| goodput | 4 | download | 3 | 224.61 | 8.44 | 216.17 | 319.99 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 14.12 | 0.53 | 11.21 | 14.65 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 15.32 | 0.53 | 13.25 | 15.86 | null | s/GiB |
| latency_p50 | 1 | connect | 3 | 3.62 | 0.50 | 2.42 | 4.11 | null | ms |
| latency_p95 | 1 | connect | 3 | 9.77 | 0.95 | 3.41 | 10.72 | null | ms |
| latency_p99 | 1 | connect | 3 | 14.19 | 1.73 | 4.37 | 15.92 | null | ms |
| latency_p50 | 4 | connect | 3 | 6.87 | 0.12 | 2.81 | 6.99 | null | ms |
| latency_p95 | 4 | connect | 3 | 7.75 | 0.44 | 3.97 | 8.20 | null | ms |
| latency_p99 | 4 | connect | 3 | 8.92 | 0.70 | 5.49 | 9.62 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 164.8 | 5.050 | 32.6 | 12.24 | 8.14 | null | null | null | null | null |
| 1 | 1 | upload | 162.3 | 5.001 | 32.4 | 17.23 | 10.92 | null | null | null | null | null |
| 2 | 1 | upload | 164.6 | 5.050 | 32.6 | 18.11 | 11.01 | null | null | null | null | null |
| 0 | 4 | upload | 644.9 | 5.057 | 127.5 | 16.89 | 12.04 | null | null | null | null | null |
| 1 | 4 | upload | 640.7 | 5.056 | 126.7 | 15.58 | 13.33 | null | null | null | null | null |
| 2 | 4 | upload | 642.4 | 5.051 | 127.2 | 15.16 | 11.64 | null | null | null | null | null |
| 0 | 1 | download | 419.4 | 5.001 | 83.9 | 15.58 | 18.68 | null | null | null | null | null |
| 1 | 1 | download | 415.7 | 5.000 | 83.1 | 15.79 | 18.89 | null | null | null | null | null |
| 2 | 1 | download | 416.9 | 5.000 | 83.4 | 15.65 | 18.79 | null | null | null | null | null |
| 0 | 4 | download | 1081.0 | 5.001 | 216.2 | 14.65 | 15.86 | null | null | null | null | null |
| 1 | 4 | download | 1123.2 | 5.001 | 224.6 | 14.12 | 15.32 | null | null | null | null | null |
| 2 | 4 | download | 1600.3 | 5.001 | 320.0 | 11.21 | 13.25 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 4.11 | 9.77 | 14.19 |
| 1 | 1 | connect | 0.0 | 5.000 | null | null | null | null | null | 3.62 | 10.72 | 15.92 |
| 2 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.42 | 3.41 | 4.37 |
| 0 | 4 | connect | 0.0 | 5.002 | null | null | null | null | null | 2.81 | 3.97 | 5.49 |
| 1 | 4 | connect | 0.0 | 5.001 | null | null | null | null | null | 6.87 | 7.75 | 8.92 |
| 2 | 4 | connect | 0.0 | 5.004 | null | null | null | null | null | 6.99 | 8.20 | 9.62 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 1.970 | 90.6 | 18 | 15 | 1.310 | 50.5 | 17 | 14 |
| 1 | 2.730 | 91.4 | 18 | 15 | 1.730 | 52.2 | 17 | 14 |
| 2 | 2.910 | 92.2 | 18 | 15 | 1.770 | 52.1 | 17 | 14 |
| 0 | 10.640 | 114.8 | 20 | 21 | 7.580 | 65.4 | 18 | 20 |
| 1 | 9.750 | 114.8 | 21 | 21 | 8.340 | 72.4 | 19 | 20 |
| 2 | 9.510 | 118.8 | 21 | 21 | 7.300 | 74.7 | 19 | 23 |
| 0 | 6.380 | 118.8 | 21 | 14 | 7.650 | 74.7 | 19 | 15 |
| 1 | 6.410 | 118.8 | 21 | 14 | 7.670 | 74.7 | 19 | 12 |
| 2 | 6.370 | 118.8 | 21 | 14 | 7.650 | 74.7 | 19 | 12 |
| 0 | 15.470 | 118.8 | 21 | 17 | 16.740 | 74.7 | 19 | 15 |
| 1 | 15.490 | 118.8 | 21 | 17 | 16.810 | 74.7 | 19 | 15 |
| 2 | 17.520 | 118.8 | 21 | 17 | 20.710 | 74.7 | 19 | 15 |
| 0 | 4.340 | 118.8 | 21 | 15 | 4.700 | 74.7 | 20 | 285 |
| 1 | 4.110 | 118.8 | 21 | 15 | 4.480 | 74.7 | 21 | 308 |
| 2 | 5.140 | 118.8 | 21 | 14 | 5.990 | 85.8 | 21 | 428 |
| 0 | 13.600 | 118.8 | 21 | 18 | 14.890 | 170.9 | 21 | 1430 |
| 1 | 5.590 | 118.8 | 21 | 18 | 18.270 | 178.5 | 24 | 1306 |
| 2 | 5.310 | 118.8 | 21 | 18 | 18.430 | 177.4 | 24 | 591 |
