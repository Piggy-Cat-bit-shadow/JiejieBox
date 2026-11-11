# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T140424`
- kind: e2e
- started: 2026-11-11T14:04:24+08:00
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
| goodput | 1 | upload | 3 | 177.57 | 0.37 | 177.21 | 180.74 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 9.61 | 0.01 | 9.60 | 9.79 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 8.93 | 0.07 | 8.85 | 9.05 | null | s/GiB |
| goodput | 4 | upload | 3 | 217.86 | 1.83 | 204.46 | 219.69 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 9.90 | 0.00 | 9.89 | 10.71 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 8.19 | 0.08 | 8.11 | 8.86 | null | s/GiB |
| goodput | 1 | download | 3 | 140.60 | 1.06 | 138.75 | 141.66 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 9.36 | 0.07 | 9.19 | 9.43 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 10.49 | 0.05 | 10.44 | 10.60 | null | s/GiB |
| goodput | 4 | download | 3 | 140.05 | 0.76 | 139.30 | 142.71 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 12.91 | 0.00 | 12.91 | 12.91 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 12.46 | 0.02 | 12.20 | 12.48 | null | s/GiB |
| goodput | 1 | udp | 3 | 12.15 | 0.08 | 9.64 | 12.23 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 173.33 | 1.48 | 171.85 | 215.49 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 203.68 | 2.52 | 201.16 | 242.06 | null | s/GiB |
| goodput | 4 | udp | 3 | 9.79 | 0.08 | 9.55 | 9.87 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | udp | 3 | 210.68 | 1.47 | 209.21 | 216.33 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | udp | 3 | 238.29 | 2.52 | 235.78 | 244.85 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 904.5 | 5.005 | 180.7 | 9.60 | 8.85 | null | null | null | null | null |
| 1 | 1 | upload | 886.9 | 5.005 | 177.2 | 9.61 | 8.93 | null | null | null | null | null |
| 2 | 1 | upload | 888.0 | 5.001 | 177.6 | 9.79 | 9.05 | null | null | null | null | null |
| 0 | 4 | upload | 1028.2 | 5.029 | 204.5 | 10.71 | 8.86 | null | null | null | null | null |
| 1 | 4 | upload | 1092.7 | 5.016 | 217.9 | 9.90 | 8.19 | null | null | null | null | null |
| 2 | 4 | upload | 1101.2 | 5.012 | 219.7 | 9.89 | 8.11 | null | null | null | null | null |
| 0 | 1 | download | 703.0 | 5.000 | 140.6 | 9.19 | 10.49 | null | null | null | null | null |
| 1 | 1 | download | 708.4 | 5.000 | 141.7 | 9.43 | 10.44 | null | null | null | null | null |
| 2 | 1 | download | 693.8 | 5.000 | 138.8 | 9.36 | 10.60 | null | null | null | null | null |
| 0 | 4 | download | 713.6 | 5.001 | 142.7 | 12.91 | 12.20 | null | null | null | null | null |
| 1 | 4 | download | 696.5 | 5.000 | 139.3 | 12.91 | 12.48 | null | null | null | null | null |
| 2 | 4 | download | 700.4 | 5.001 | 140.1 | 12.91 | 12.46 | null | null | null | null | null |
| 0 | 1 | udp | 60.7 | 5.000 | 12.1 | 173.33 | 203.68 | null | null | null | null | null |
| 1 | 1 | udp | 61.1 | 5.000 | 12.2 | 171.85 | 201.16 | null | null | null | null | null |
| 2 | 1 | udp | 48.2 | 5.000 | 9.6 | 215.49 | 242.06 | null | null | null | null | null |
| 0 | 4 | udp | 49.3 | 5.000 | 9.9 | 209.21 | 235.78 | null | null | null | null | null |
| 1 | 4 | udp | 47.8 | 5.000 | 9.6 | 216.33 | 244.85 | null | null | null | null | null |
| 2 | 4 | udp | 48.9 | 5.000 | 9.8 | 210.68 | 238.29 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.480 | 62.1 | 13 | 12 | 7.820 | 64.1 | 16 | 14 |
| 1 | 8.320 | 63.9 | 13 | 12 | 7.730 | 65.0 | 16 | 14 |
| 2 | 8.490 | 64.7 | 14 | 12 | 7.850 | 66.0 | 16 | 14 |
| 0 | 10.750 | 66.8 | 15 | 15 | 8.900 | 66.5 | 16 | 17 |
| 1 | 10.560 | 68.4 | 15 | 15 | 8.740 | 67.7 | 18 | 17 |
| 2 | 10.640 | 70.8 | 15 | 15 | 8.720 | 67.3 | 18 | 17 |
| 0 | 6.310 | 73.6 | 16 | 12 | 7.200 | 67.8 | 18 | 14 |
| 1 | 6.520 | 73.8 | 16 | 12 | 7.220 | 66.7 | 18 | 14 |
| 2 | 6.340 | 73.0 | 17 | 12 | 7.180 | 66.8 | 18 | 14 |
| 0 | 9.000 | 73.0 | 17 | 15 | 8.500 | 67.6 | 18 | 17 |
| 1 | 8.780 | 73.0 | 17 | 15 | 8.490 | 67.5 | 18 | 17 |
| 2 | 8.830 | 73.0 | 17 | 15 | 8.520 | 67.7 | 18 | 17 |
| 0 | 10.280 | 73.0 | 17 | 13 | 12.080 | 68.9 | 18 | 14 |
| 1 | 10.260 | 73.0 | 17 | 13 | 12.010 | 67.8 | 18 | 14 |
| 2 | 10.140 | 73.0 | 17 | 13 | 11.390 | 68.1 | 18 | 14 |
| 0 | 10.080 | 73.0 | 17 | 13 | 11.360 | 67.8 | 18 | 14 |
| 1 | 10.090 | 73.0 | 17 | 13 | 11.420 | 68.0 | 18 | 14 |
| 2 | 10.070 | 73.0 | 17 | 13 | 11.390 | 67.7 | 18 | 14 |
