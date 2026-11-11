# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T165825`
- kind: e2e
- started: 2026-11-11T16:58:25+08:00
- commit: `227777920b8e` (feat(perf): paced UDP with sequence and drain, and binary provenance)
- branch: `work/closure-2026-11-11`
- **dirty worktree**: 8 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss0 delay=40.0ms loss=0.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.57 | 0.00 | 0.40 | 0.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 744.46 | 7.16 | 737.30 | 894.78 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 765.94 | 7.16 | 758.78 | 923.42 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.22 | 0.00 | 2.22 | 2.22 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.25 | 0.01 | 81.24 | 81.49 | null | ms |
| latency_p95 | 1 | udp | 3 | 81.61 | 0.06 | 81.55 | 81.94 | null | ms |
| latency_p99 | 1 | udp | 3 | 81.84 | 0.09 | 81.75 | 2268.48 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 2.9 | 5.000 | 0.6 | 744.46 | 758.78 | 0.01 | 2.22 | 81.24 | 81.55 | 81.75 |
| 1 | 1 | udp | 2.9 | 5.001 | 0.6 | 737.30 | 765.94 | 0.01 | 2.22 | 81.25 | 81.61 | 81.84 |
| 2 | 1 | udp | 2.9 | 7.187 | 0.4 | 894.78 | 923.42 | 0.01 | 2.22 | 81.49 | 81.94 | 2268.48 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 2.080 | 91.0 | 15 | 13 | 2.120 | 93.7 | 16 | 13 |
| 1 | 2.060 | 89.6 | 15 | 13 | 2.140 | 93.5 | 16 | 15 |
| 2 | 2.500 | 89.6 | 17 | 13 | 2.580 | 93.3 | 16 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
