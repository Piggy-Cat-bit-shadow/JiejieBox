# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T165924`
- kind: e2e
- started: 2026-11-11T16:59:24+08:00
- commit: `227777920b8e` (feat(perf): paced UDP with sequence and drain, and binary provenance)
- branch: `work/closure-2026-11-11`
- **dirty worktree**: 8 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.24 | 0.02 | 0.20 | 0.26 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 2 | 681.93 | 9.99 | 671.94 | 691.93 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 2 | 493.37 | 25.57 | 467.81 | 518.94 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.50 | 0.04 | 2.45 | 2.70 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1717.14 | 13.16 | 1703.98 | 1849.89 | null | ms |
| latency_p95 | 1 | udp | 3 | 2655.45 | 3.64 | 2244.57 | 2659.09 | null | ms |
| latency_p99 | 1 | udp | 3 | 2691.39 | 103.62 | 2339.93 | 2795.01 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.2 | 5.000 | 0.2 | 671.94 | 467.81 | 0.01 | 2.50 | 1849.89 | 2655.45 | 2691.39 |
| 1 | 1 | udp | 1.3 | 5.000 | 0.3 | 691.93 | 518.94 | 0.01 | 2.45 | 1717.14 | 2244.57 | 2339.93 |
| 2 | 1 | udp | 1.0 | 5.000 | 0.2 | null | null | 0.00 | 2.70 | 1703.98 | 2659.09 | 2795.01 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.790 | 91.1 | 16 | 15 | 0.550 | 94.3 | 16 | 14 |
| 1 | 0.880 | 91.6 | 16 | 15 | 0.660 | 94.5 | 17 | 16 |
| 2 | 0.690 | 91.8 | 16 | 15 | 0.440 | 95.5 | 18 | 18 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
