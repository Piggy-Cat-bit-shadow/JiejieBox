# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T192138`
- kind: e2e
- started: 2026-11-11T19:21:38+08:00
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
| goodput | 1 | udp | 3 | 0.34 | 0.02 | 0.30 | 0.36 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 502.00 | 50.61 | 451.39 | 561.13 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 373.11 | 24.57 | 348.54 | 464.59 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.29 | 0.10 | 2.20 | 2.77 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1553.61 | 81.52 | 1272.90 | 1635.13 | null | ms |
| latency_p95 | 1 | udp | 3 | 1903.68 | 299.87 | 1572.37 | 2203.56 | null | ms |
| latency_p99 | 1 | udp | 3 | 1966.08 | 281.47 | 1602.30 | 2247.55 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.7 | 5.001 | 0.3 | 561.13 | 464.59 | 0.01 | 2.77 | 1272.90 | 1572.37 | 1602.30 |
| 1 | 1 | udp | 1.8 | 5.000 | 0.4 | 451.39 | 348.54 | 0.01 | 2.29 | 1635.13 | 2203.56 | 2247.55 |
| 2 | 1 | udp | 1.5 | 5.000 | 0.3 | 502.00 | 373.11 | 0.01 | 2.20 | 1553.61 | 1903.68 | 1966.08 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.930 | 90.9 | 16 | 14 | 0.770 | 92.4 | 15 | 14 |
| 1 | 0.790 | 90.2 | 16 | 13 | 0.610 | 93.1 | 16 | 15 |
| 2 | 0.740 | 90.9 | 16 | 13 | 0.550 | 92.6 | 16 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
