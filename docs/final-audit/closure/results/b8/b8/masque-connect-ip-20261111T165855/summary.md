# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T165855`
- kind: e2e
- started: 2026-11-11T16:58:55+08:00
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
| goodput | 1 | udp | 3 | 0.57 | 0.00 | 0.57 | 0.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 766.55 | 18.73 | 702.64 | 785.28 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 813.12 | 6.52 | 806.60 | 882.54 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.22 | 0.00 | 2.21 | 2.22 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.63 | 0.18 | 81.37 | 81.80 | null | ms |
| latency_p95 | 1 | udp | 3 | 131.00 | 24.77 | 106.24 | 309.13 | null | ms |
| latency_p99 | 1 | udp | 3 | 142.17 | 29.49 | 112.68 | 318.46 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 2.9 | 5.001 | 0.6 | 766.55 | 813.12 | 0.01 | 2.22 | 81.37 | 106.24 | 112.68 |
| 1 | 1 | udp | 2.9 | 5.000 | 0.6 | 702.64 | 806.60 | 0.01 | 2.21 | 81.80 | 309.13 | 318.46 |
| 2 | 1 | udp | 2.8 | 5.001 | 0.6 | 785.28 | 882.54 | 0.01 | 2.22 | 81.63 | 131.00 | 142.17 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 2.140 | 92.5 | 15 | 13 | 2.270 | 94.7 | 17 | 13 |
| 1 | 1.960 | 91.4 | 15 | 13 | 2.250 | 94.3 | 17 | 15 |
| 2 | 2.180 | 91.4 | 16 | 13 | 2.450 | 94.0 | 17 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
