# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T191816`
- kind: e2e
- started: 2026-11-11T19:18:16+08:00
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
| goodput | 1 | udp | 3 | 0.49 | 0.08 | 0.13 | 0.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 2 | 191.95 | 9.41 | 182.54 | 201.36 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 2 | 132.72 | 6.87 | 125.85 | 139.59 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.31 | 0.08 | 2.23 | 7.98 | null | ratio |
| latency_p50 | 1 | udp | 3 | 9458.19 | 1706.42 | 5053.62 | 11164.62 | null | ms |
| latency_p95 | 1 | udp | 3 | 11524.95 | 1376.41 | 5914.41 | 12901.36 | null | ms |
| latency_p99 | 1 | udp | 3 | 11569.91 | 1499.44 | 6111.25 | 13069.35 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 0.7 | 5.000 | 0.1 | null | null | 0.01 | 7.98 | 9458.19 | 11524.95 | 11569.91 |
| 1 | 1 | udp | 2.4 | 5.000 | 0.5 | 201.36 | 125.85 | 0.01 | 2.31 | 11164.62 | 12901.36 | 13069.35 |
| 2 | 1 | udp | 2.9 | 5.000 | 0.6 | 182.54 | 139.59 | 0.01 | 2.23 | 5053.62 | 5914.41 | 6111.25 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.200 | 85.7 | 12 | 14 | 0.330 | 85.8 | 13 | 15 |
| 1 | 0.480 | 87.7 | 13 | 13 | 0.300 | 86.9 | 13 | 14 |
| 2 | 0.510 | 88.1 | 13 | 13 | 0.390 | 88.0 | 14 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
