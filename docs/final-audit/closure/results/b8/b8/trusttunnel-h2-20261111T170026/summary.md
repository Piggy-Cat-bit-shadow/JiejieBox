# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T170026`
- kind: e2e
- started: 2026-11-11T17:00:26+08:00
- commit: `227777920b8e` (feat(perf): paced UDP with sequence and drain, and binary provenance)
- branch: `work/closure-2026-11-11`
- **dirty worktree**: 8 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss0.1 delay=40.0ms loss=0.10% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.57 | 0.00 | 0.42 | 0.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 293.49 | 9.72 | 283.77 | 418.76 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 186.79 | 90.15 | 96.64 | 468.87 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.26 | 0.01 | 2.24 | 2.33 | null | ratio |
| latency_p50 | 1 | udp | 3 | 192.04 | 17.19 | 81.05 | 209.23 | null | ms |
| latency_p95 | 1 | udp | 3 | 329.22 | 136.33 | 192.88 | 504.99 | null | ms |
| latency_p99 | 1 | udp | 3 | 529.90 | 144.44 | 385.46 | 1917.55 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 2.9 | 6.836 | 0.4 | 418.76 | 468.87 | 0.01 | 2.33 | 81.05 | 192.88 | 1917.55 |
| 1 | 1 | udp | 2.9 | 5.001 | 0.6 | 293.49 | 96.64 | 0.01 | 2.26 | 192.04 | 329.22 | 385.46 |
| 2 | 1 | udp | 2.9 | 5.000 | 0.6 | 283.77 | 186.79 | 0.01 | 2.24 | 209.23 | 504.99 | 529.90 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 1.170 | 85.5 | 13 | 13 | 1.310 | 87.3 | 13 | 14 |
| 1 | 0.820 | 86.3 | 15 | 13 | 0.270 | 87.9 | 15 | 14 |
| 2 | 0.790 | 87.0 | 15 | 13 | 0.520 | 88.2 | 16 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
