# wireguard-endpoint - wireguard

- run_id: `wireguard-endpoint-20261111T184719`
- kind: e2e
- started: 2026-11-11T18:47:19+08:00
- commit: `067cceb562e9` (docs(closure): finalize B7 evidence chain and index)
- branch: `work/closure-final-reconcile-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: loopback

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 2 | upload | 3 | 53.48 | 0.33 | 50.15 | 53.81 | null | MiB/s |
| cpu_seconds_per_gib_client | 2 | upload | 3 | 55.73 | 2.32 | 53.41 | 58.20 | null | s/GiB |
| cpu_seconds_per_gib_server | 2 | upload | 3 | 58.76 | 1.54 | 56.35 | 60.29 | null | s/GiB |
| goodput | 4 | upload | 3 | 54.09 | 0.81 | 53.05 | 54.91 | null | MiB/s |
| cpu_seconds_per_gib_client | 4 | upload | 3 | 54.19 | 0.12 | 53.79 | 54.31 | null | s/GiB |
| cpu_seconds_per_gib_server | 4 | upload | 3 | 56.99 | 0.06 | 56.79 | 57.05 | null | s/GiB |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 2 | upload | 162.6 | 3.040 | 53.5 | 55.73 | 58.76 | null | null | null | null | null |
| 1 | 2 | upload | 163.7 | 3.043 | 53.8 | 53.41 | 56.35 | null | null | null | null | null |
| 2 | 2 | upload | 152.0 | 3.031 | 50.1 | 58.20 | 60.29 | null | null | null | null | null |
| 0 | 4 | upload | 168.0 | 3.106 | 54.1 | 54.19 | 57.05 | null | null | null | null | null |
| 1 | 4 | upload | 160.5 | 3.025 | 53.1 | 54.31 | 56.99 | null | null | null | null | null |
| 2 | 4 | upload | 167.3 | 3.048 | 54.9 | 53.79 | 56.79 | null | null | null | null | null |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 8.850 | 121.5 | 17 | 16 | 9.330 | 117.6 | 16 | 15 |
| 1 | 8.540 | 124.6 | 20 | 16 | 9.010 | 125.4 | 16 | 15 |
| 2 | 8.640 | 132.8 | 20 | 16 | 8.950 | 126.7 | 18 | 15 |
| 0 | 8.890 | 138.4 | 20 | 17 | 9.360 | 130.4 | 18 | 16 |
| 1 | 8.510 | 139.6 | 20 | 17 | 8.930 | 141.6 | 18 | 16 |
| 2 | 8.790 | 141.0 | 20 | 17 | 9.280 | 140.6 | 18 | 16 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
