# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T182721`
- kind: e2e
- started: 2026-11-11T18:27:21+08:00
- commit: `f03b1c728993` (perf(udp): make drain completeness explicit and record invocation parameters)
- branch: `work/closure-final-reconcile-2026-11-11`
- **dirty worktree**: 5 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.06 | 0.00 | 0.06 | 0.06 | null | MiB/s |
| wire_gbit_per_s | 1 | udp | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.24 | 0.01 | 2.22 | 2.76 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.53 | 0.04 | 81.48 | 81.68 | null | ms |
| latency_p95 | 1 | udp | 3 | 82.27 | 0.11 | 82.04 | 82.38 | null | ms |
| latency_p99 | 1 | udp | 3 | 82.75 | 0.36 | 82.39 | 97.64 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 0.3 | 5.000 | 0.1 | null | null | 0.00 | 2.76 | 81.53 | 82.27 | 97.64 |
| 1 | 1 | udp | 0.3 | 5.000 | 0.1 | null | null | 0.00 | 2.22 | 81.68 | 82.38 | 82.75 |
| 2 | 1 | udp | 0.3 | 5.001 | 0.1 | null | null | 0.00 | 2.24 | 81.48 | 82.04 | 82.39 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.300 | 66.4 | 13 | 14 | 0.290 | 69.2 | 14 | 14 |
| 1 | 0.290 | 66.5 | 14 | 13 | 0.270 | 69.3 | 14 | 15 |
| 2 | 0.260 | 66.5 | 14 | 13 | 0.270 | 69.6 | 14 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
