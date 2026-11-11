# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T190937`
- kind: e2e
- started: 2026-11-11T19:09:37+08:00
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
| goodput | 1 | udp | 3 | 0.24 | 0.04 | 0.01 | 0.28 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 2 | 397.12 | 49.84 | 347.27 | 446.96 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 2 | 179.52 | 9.58 | 169.94 | 189.10 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.00 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.45 | 0.15 | 2.30 | 34.64 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1318.81 | 723.82 | 594.99 | 8262.64 | null | ms |
| latency_p95 | 1 | udp | 3 | 2666.02 | 520.50 | 2145.52 | 8354.29 | null | ms |
| latency_p99 | 1 | udp | 2 | 2499.79 | 314.34 | 2185.45 | 2814.13 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 0.1 | 6.691 | 0.0 | null | null | 0.00 | 34.64 | 8262.64 | 8354.29 | null |
| 1 | 1 | udp | 1.2 | 5.000 | 0.2 | 446.96 | 189.10 | 0.00 | 2.45 | 1318.81 | 2666.02 | 2814.13 |
| 2 | 1 | udp | 1.4 | 5.000 | 0.3 | 347.27 | 169.94 | 0.01 | 2.30 | 594.99 | 2145.52 | 2185.45 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.070 | 84.4 | 12 | 14 | 0.050 | 85.0 | 11 | 15 |
| 1 | 0.520 | 84.9 | 12 | 13 | 0.220 | 86.0 | 12 | 14 |
| 2 | 0.470 | 85.5 | 12 | 13 | 0.230 | 86.4 | 12 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
