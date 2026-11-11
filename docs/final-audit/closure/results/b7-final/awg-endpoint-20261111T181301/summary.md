# awg-endpoint - awg

- run_id: `awg-endpoint-20261111T181301`
- kind: e2e
- started: 2026-11-11T18:13:01+08:00
- commit: `897553e81221` (docs(closure): record the three self-check rounds and the final HEAD)
- branch: `work/closure-final-reconcile-2026-11-11`
- **dirty worktree**: 4 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 2 | upload | 3 | 40.56 | 8.51 | 24.53 | 49.07 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 60.22 | 8.99 | 49.22 | 69.21 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 63.94 | 8.64 | 52.97 | 72.58 | null | s/GiB |
| goodput | 4 | upload | 3 | 53.58 | 3.26 | 50.32 | 61.73 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 57.01 | 3.69 | 49.06 | 60.69 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 62.39 | 4.06 | 54.22 | 66.45 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 124.6 | 3.072 | 40.6 | 69.21 | 72.58 | null | null | null | null | null |
| 1 | 2 | upload | 148.8 | 3.032 | 49.1 | 60.22 | 63.94 | null | null | null | null | null |
| 2 | 2 | upload | 73.9 | 3.010 | 24.5 | 49.22 | 52.97 | null | null | null | null | null |
| 0 | 4 | upload | 186.8 | 3.026 | 61.7 | 49.06 | 54.22 | null | null | null | null | null |
| 1 | 4 | upload | 154.7 | 3.075 | 50.3 | 60.69 | 66.45 | null | null | null | null | null |
| 2 | 4 | upload | 169.4 | 3.162 | 53.6 | 57.01 | 62.39 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.420 | 96.3 | 18 | 17 | 8.830 | 91.8 | 16 | 16 |
| 1 | 8.750 | 103.1 | 18 | 16 | 9.290 | 101.6 | 17 | 15 |
| 2 | 3.550 | 104.7 | 18 | 17 | 3.820 | 103.1 | 17 | 15 |
| 0 | 8.950 | 113.4 | 19 | 19 | 9.890 | 104.3 | 17 | 18 |
| 1 | 9.170 | 119.4 | 19 | 18 | 10.040 | 106.2 | 17 | 17 |
| 2 | 9.430 | 124.8 | 19 | 19 | 10.320 | 130.1 | 17 | 18 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
