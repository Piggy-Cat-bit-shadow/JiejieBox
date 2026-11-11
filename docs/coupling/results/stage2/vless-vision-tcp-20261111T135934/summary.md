# vless-vision-tcp - vless

- run_id: `vless-vision-tcp-20261111T135934`
- kind: e2e
- started: 2026-11-11T13:59:34+08:00
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
| goodput | 1 | upload | 3 | 224.59 | 33.40 | 190.71 | 257.99 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | upload | 3 | 4.72 | 0.61 | 4.11 | 5.50 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | upload | 3 | 3.40 | 0.49 | 2.91 | 4.02 | null | s/GiB |
| goodput | 4 | upload | 3 | 766.42 | 4.02 | 762.40 | 793.76 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 5.49 | 0.02 | 5.29 | 5.51 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 3.25 | 0.03 | 3.13 | 3.28 | null | s/GiB |
| goodput | 1 | download | 3 | 520.00 | 0.06 | 519.93 | 541.82 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | download | 3 | 2.10 | 0.00 | 2.01 | 2.10 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | download | 3 | 1.12 | 0.00 | 1.11 | 1.13 | null | s/GiB |
| goodput | 4 | download | 3 | 1362.65 | 7.11 | 1293.15 | 1369.76 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | download | 3 | 3.09 | 0.00 | 3.09 | 3.27 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | download | 3 | 2.12 | 0.02 | 2.10 | 2.21 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | upload | 954.3 | 5.004 | 190.7 | 5.50 | 4.02 | null | null | null | null | null |
| 1 | 1 | upload | 1124.3 | 5.006 | 224.6 | 4.72 | 3.40 | null | null | null | null | null |
| 2 | 1 | upload | 1290.7 | 5.003 | 258.0 | 4.11 | 2.91 | null | null | null | null | null |
| 0 | 4 | upload | 3816.2 | 5.005 | 762.4 | 5.51 | 3.28 | null | null | null | null | null |
| 1 | 4 | upload | 3834.9 | 5.004 | 766.4 | 5.49 | 3.25 | null | null | null | null | null |
| 2 | 4 | upload | 3971.0 | 5.003 | 793.8 | 5.29 | 3.13 | null | null | null | null | null |
| 0 | 1 | download | 2600.2 | 5.000 | 520.0 | 2.10 | 1.13 | null | null | null | null | null |
| 1 | 1 | download | 2599.7 | 5.000 | 519.9 | 2.10 | 1.11 | null | null | null | null | null |
| 2 | 1 | download | 2709.2 | 5.000 | 541.8 | 2.01 | 1.12 | null | null | null | null | null |
| 0 | 4 | download | 6849.5 | 5.001 | 1369.8 | 3.09 | 2.10 | null | null | null | null | null |
| 1 | 4 | download | 6466.5 | 5.001 | 1293.2 | 3.27 | 2.21 | null | null | null | null | null |
| 2 | 4 | download | 6813.7 | 5.000 | 1362.7 | 3.09 | 2.12 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 5.130 | 60.6 | 11 | 12 | 3.750 | 61.1 | 14 | 13 |
| 1 | 5.180 | 62.0 | 12 | 12 | 3.730 | 61.5 | 14 | 13 |
| 2 | 5.180 | 62.8 | 12 | 12 | 3.670 | 62.3 | 16 | 13 |
| 0 | 20.550 | 65.7 | 16 | 18 | 12.210 | 64.1 | 16 | 19 |
| 1 | 20.570 | 66.5 | 17 | 18 | 12.160 | 65.2 | 16 | 19 |
| 2 | 20.510 | 66.5 | 17 | 18 | 12.140 | 65.1 | 16 | 19 |
| 0 | 5.330 | 64.9 | 17 | 12 | 2.880 | 64.2 | 16 | 13 |
| 1 | 5.320 | 64.9 | 17 | 12 | 2.830 | 64.0 | 16 | 13 |
| 2 | 5.310 | 64.9 | 17 | 12 | 2.960 | 63.9 | 16 | 13 |
| 0 | 20.660 | 66.3 | 17 | 18 | 14.020 | 65.7 | 16 | 19 |
| 1 | 20.620 | 66.4 | 17 | 18 | 13.960 | 65.2 | 16 | 19 |
| 2 | 20.580 | 66.7 | 17 | 18 | 14.090 | 66.0 | 16 | 19 |
