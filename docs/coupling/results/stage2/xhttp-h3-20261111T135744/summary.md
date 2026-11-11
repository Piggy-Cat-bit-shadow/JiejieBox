# xhttp-h3 - vless+xhttp

- run_id: `xhttp-h3-20261111T135744`
- kind: e2e
- started: 2026-11-11T13:57:44+08:00
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
| goodput | 1 | upload | 3 | 32.52 | 0.03 | 32.50 | 32.77 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 25.46 | 0.19 | 25.27 | 26.43 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 16.42 | 0.58 | 15.80 | 17.00 | null | s/GiB |
| goodput | 4 | upload | 3 | 127.16 | 0.05 | 127.07 | 127.21 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 26.88 | 0.43 | 26.45 | 28.18 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 19.51 | 0.12 | 18.87 | 19.64 | null | s/GiB |
| goodput | 1 | download | 3 | 73.15 | 13.30 | 59.84 | 108.60 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 25.39 | 2.67 | 18.39 | 28.06 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 27.13 | 2.78 | 19.59 | 29.91 | null | s/GiB |
| goodput | 4 | download | 3 | 181.36 | 2.50 | 178.85 | 190.64 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 19.26 | 0.12 | 18.58 | 19.38 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 17.79 | 0.66 | 17.13 | 18.80 | null | s/GiB |
| latency_p50 | 1 | connect | 3 | 3.24 | 0.09 | 3.15 | 3.47 | null | ms |
| latency_p95 | 1 | connect | 3 | 4.49 | 0.07 | 4.43 | 4.83 | null | ms |
| latency_p99 | 1 | connect | 3 | 6.14 | 0.12 | 6.02 | 7.31 | null | ms |
| latency_p50 | 4 | connect | 3 | 4.77 | 1.25 | 3.52 | 24.35 | null | ms |
| latency_p95 | 4 | connect | 3 | 11.44 | 6.02 | 5.42 | 59.49 | null | ms |
| latency_p99 | 4 | connect | 3 | 14.94 | 6.53 | 8.41 | 73.47 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 164.7 | 5.025 | 32.8 | 26.43 | 16.42 | null | null | null | null | null |
| 1 | 1 | upload | 163.3 | 5.025 | 32.5 | 25.27 | 15.80 | null | null | null | null | null |
| 2 | 1 | upload | 163.3 | 5.020 | 32.5 | 25.46 | 17.00 | null | null | null | null | null |
| 0 | 4 | upload | 639.2 | 5.025 | 127.2 | 26.88 | 19.51 | null | null | null | null | null |
| 1 | 4 | upload | 638.8 | 5.027 | 127.1 | 26.45 | 18.87 | null | null | null | null | null |
| 2 | 4 | upload | 639.9 | 5.032 | 127.2 | 28.18 | 19.64 | null | null | null | null | null |
| 0 | 1 | download | 543.0 | 5.000 | 108.6 | 18.39 | 19.59 | null | null | null | null | null |
| 1 | 1 | download | 299.3 | 5.001 | 59.8 | 28.06 | 29.91 | null | null | null | null | null |
| 2 | 1 | download | 365.8 | 5.001 | 73.1 | 25.39 | 27.13 | null | null | null | null | null |
| 0 | 4 | download | 906.9 | 5.001 | 181.4 | 19.26 | 17.79 | null | null | null | null | null |
| 1 | 4 | download | 894.3 | 5.000 | 178.9 | 19.38 | 18.80 | null | null | null | null | null |
| 2 | 4 | download | 953.2 | 5.000 | 190.6 | 18.58 | 17.13 | null | null | null | null | null |
| 0 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 3.24 | 4.43 | 6.14 |
| 1 | 1 | connect | 0.0 | 5.003 | null | null | null | null | null | 3.15 | 4.49 | 6.02 |
| 2 | 1 | connect | 0.0 | 5.002 | null | null | null | null | null | 3.47 | 4.83 | 7.31 |
| 0 | 4 | connect | 0.0 | 5.003 | null | null | null | null | null | 3.52 | 5.42 | 8.41 |
| 1 | 4 | connect | 0.0 | 5.002 | null | null | null | null | null | 4.77 | 11.44 | 14.94 |
| 2 | 4 | connect | 0.0 | 5.013 | null | null | null | null | null | 24.35 | 59.49 | 73.47 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 4.250 | 88.7 | 19 | 15 | 2.640 | 50.1 | 17 | 11 |
| 1 | 4.030 | 90.3 | 19 | 15 | 2.520 | 50.1 | 17 | 11 |
| 2 | 4.060 | 90.3 | 19 | 15 | 2.710 | 50.1 | 17 | 11 |
| 0 | 16.780 | 106.7 | 21 | 21 | 12.180 | 66.3 | 18 | 17 |
| 1 | 16.500 | 108.0 | 21 | 18 | 11.770 | 70.3 | 18 | 19 |
| 2 | 17.610 | 111.4 | 21 | 19 | 12.270 | 70.1 | 19 | 17 |
| 0 | 9.750 | 111.4 | 21 | 14 | 10.390 | 70.1 | 19 | 12 |
| 1 | 8.200 | 111.4 | 21 | 14 | 8.740 | 70.1 | 19 | 9 |
| 2 | 9.070 | 111.4 | 21 | 14 | 9.690 | 70.1 | 19 | 9 |
| 0 | 17.060 | 111.4 | 21 | 17 | 15.760 | 70.1 | 19 | 12 |
| 1 | 16.930 | 111.4 | 21 | 17 | 16.420 | 70.1 | 19 | 12 |
| 2 | 17.300 | 111.4 | 21 | 17 | 15.950 | 70.1 | 19 | 12 |
| 0 | 6.810 | 111.4 | 21 | 14 | 6.820 | 70.4 | 20 | 323 |
| 1 | 6.740 | 111.4 | 21 | 14 | 6.860 | 77.6 | 20 | 354 |
| 2 | 6.710 | 111.4 | 21 | 14 | 6.860 | 80.5 | 20 | 307 |
| 0 | 16.340 | 111.4 | 21 | 18 | 14.890 | 152.7 | 21 | 1087 |
| 1 | 10.820 | 111.4 | 21 | 18 | 18.370 | 167.6 | 21 | 1135 |
| 2 | 3.480 | 111.4 | 21 | 18 | 8.860 | 166.4 | 23 | 406 |
