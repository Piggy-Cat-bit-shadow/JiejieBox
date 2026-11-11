# anytls-tcp - anytls

- run_id: `anytls-tcp-20261111T140047`
- kind: e2e
- started: 2026-11-11T14:00:47+08:00
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
| goodput | 1 | upload | 3 | 249.59 | 3.91 | 239.18 | 253.50 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 4.28 | 0.06 | 4.23 | 4.52 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 3.56 | 0.06 | 3.50 | 4.00 | null | s/GiB |
| goodput | 4 | upload | 3 | 751.57 | 5.24 | 742.97 | 756.81 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 5.61 | 0.04 | 5.57 | 5.68 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 3.71 | 0.03 | 3.68 | 3.74 | null | s/GiB |
| goodput | 1 | download | 3 | 549.78 | 16.44 | 533.34 | 640.50 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 2.88 | 0.01 | 2.58 | 2.89 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 1.93 | 0.11 | 1.53 | 2.05 | null | s/GiB |
| goodput | 4 | download | 3 | 1182.06 | 21.18 | 1143.43 | 1203.24 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 4.32 | 0.01 | 4.21 | 4.32 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 3.37 | 0.02 | 3.20 | 3.39 | null | s/GiB |
| goodput | 1 | udp | 3 | 24.21 | 0.07 | 24.10 | 24.28 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 100.32 | 0.21 | 99.61 | 100.53 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 98.29 | 0.11 | 97.75 | 98.40 | null | s/GiB |
| goodput | 4 | udp | 3 | 22.68 | 0.11 | 22.57 | 22.87 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | udp | 3 | 106.28 | 0.26 | 106.02 | 106.61 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | udp | 3 | 104.41 | 0.02 | 104.38 | 104.71 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 1248.2 | 5.001 | 249.6 | 4.28 | 3.56 | null | null | null | null | null |
| 1 | 1 | upload | 1267.5 | 5.000 | 253.5 | 4.23 | 3.50 | null | null | null | null | null |
| 2 | 1 | upload | 1196.1 | 5.001 | 239.2 | 4.52 | 4.00 | null | null | null | null | null |
| 0 | 4 | upload | 3789.0 | 5.006 | 756.8 | 5.57 | 3.68 | null | null | null | null | null |
| 1 | 4 | upload | 3763.2 | 5.007 | 751.6 | 5.61 | 3.71 | null | null | null | null | null |
| 2 | 4 | upload | 3719.0 | 5.006 | 743.0 | 5.68 | 3.74 | null | null | null | null | null |
| 0 | 1 | download | 2749.1 | 5.000 | 549.8 | 2.88 | 1.93 | null | null | null | null | null |
| 1 | 1 | download | 3202.6 | 5.000 | 640.5 | 2.58 | 1.53 | null | null | null | null | null |
| 2 | 1 | download | 2667.0 | 5.001 | 533.3 | 2.89 | 2.05 | null | null | null | null | null |
| 0 | 4 | download | 6017.2 | 5.001 | 1203.2 | 4.32 | 3.20 | null | null | null | null | null |
| 1 | 4 | download | 5717.5 | 5.000 | 1143.4 | 4.32 | 3.39 | null | null | null | null | null |
| 2 | 4 | download | 5910.8 | 5.000 | 1182.1 | 4.21 | 3.37 | null | null | null | null | null |
| 0 | 1 | udp | 121.1 | 5.000 | 24.2 | 100.32 | 98.29 | null | null | null | null | null |
| 1 | 1 | udp | 120.5 | 5.000 | 24.1 | 100.53 | 98.40 | null | null | null | null | null |
| 2 | 1 | udp | 121.4 | 5.000 | 24.3 | 99.61 | 97.75 | null | null | null | null | null |
| 0 | 4 | udp | 112.9 | 5.000 | 22.6 | 106.61 | 104.71 | null | null | null | null | null |
| 1 | 4 | udp | 114.4 | 5.000 | 22.9 | 106.02 | 104.41 | null | null | null | null | null |
| 2 | 4 | udp | 113.4 | 5.000 | 22.7 | 106.28 | 104.38 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.220 | 59.9 | 13 | 13 | 4.340 | 61.7 | 13 | 14 |
| 1 | 5.230 | 61.0 | 13 | 13 | 4.330 | 63.2 | 14 | 14 |
| 2 | 5.280 | 61.5 | 14 | 13 | 4.670 | 63.1 | 15 | 14 |
| 0 | 20.600 | 63.8 | 17 | 22 | 13.600 | 66.7 | 20 | 23 |
| 1 | 20.610 | 64.8 | 17 | 22 | 13.640 | 67.4 | 20 | 23 |
| 2 | 20.620 | 64.3 | 17 | 22 | 13.590 | 66.9 | 20 | 23 |
| 0 | 7.730 | 65.0 | 17 | 19 | 5.190 | 66.4 | 20 | 20 |
| 1 | 8.060 | 65.2 | 17 | 19 | 4.800 | 66.6 | 20 | 20 |
| 2 | 7.530 | 64.2 | 17 | 19 | 5.330 | 66.6 | 20 | 20 |
| 0 | 25.360 | 66.9 | 17 | 22 | 18.810 | 66.0 | 20 | 23 |
| 1 | 24.140 | 67.3 | 17 | 22 | 18.930 | 66.0 | 20 | 23 |
| 2 | 24.310 | 67.7 | 17 | 22 | 19.480 | 66.0 | 20 | 23 |
| 0 | 11.860 | 66.4 | 17 | 20 | 11.620 | 66.3 | 20 | 20 |
| 1 | 11.830 | 66.4 | 17 | 20 | 11.580 | 67.1 | 20 | 20 |
| 2 | 11.810 | 66.4 | 17 | 20 | 11.590 | 66.2 | 20 | 20 |
| 0 | 11.750 | 66.4 | 17 | 16 | 11.540 | 66.0 | 20 | 16 |
| 1 | 11.840 | 66.4 | 17 | 16 | 11.660 | 66.3 | 20 | 16 |
| 2 | 11.770 | 66.4 | 17 | 16 | 11.560 | 66.0 | 20 | 16 |
