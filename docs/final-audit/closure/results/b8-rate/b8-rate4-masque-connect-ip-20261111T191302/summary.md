# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T191302`
- kind: e2e
- started: 2026-11-11T19:13:02+08:00
- commit: `c26a4ddce4bf` (docs(closure): state the archive and sidecar manifest contract)
- branch: `work/closure-final-reconcile-2026-11-11`
- **dirty worktree**: 1 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.32 | 0.02 | 0.30 | 0.34 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 527.94 | 13.39 | 489.80 | 541.33 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 351.96 | 11.05 | 340.47 | 363.01 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.24 | 0.05 | 2.20 | 2.33 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1948.61 | 115.93 | 1293.47 | 2064.54 | null | ms |
| latency_p95 | 1 | udp | 3 | 2234.78 | 4.16 | 2230.62 | 3478.07 | null | ms |
| latency_p99 | 1 | udp | 3 | 2440.12 | 59.62 | 2380.50 | 3566.28 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.5 | 5.000 | 0.3 | 527.94 | 351.96 | 0.01 | 2.33 | 1948.61 | 2230.62 | 2380.50 |
| 1 | 1 | udp | 1.6 | 5.000 | 0.3 | 541.33 | 363.01 | 0.01 | 2.24 | 2064.54 | 3478.07 | 3566.28 |
| 2 | 1 | udp | 1.7 | 5.000 | 0.3 | 489.80 | 340.47 | 0.01 | 2.20 | 1293.47 | 2234.78 | 2440.12 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.780 | 91.0 | 15 | 14 | 0.520 | 94.5 | 16 | 14 |
| 1 | 0.850 | 90.6 | 16 | 13 | 0.570 | 95.0 | 16 | 15 |
| 2 | 0.820 | 90.5 | 16 | 13 | 0.570 | 94.5 | 16 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
