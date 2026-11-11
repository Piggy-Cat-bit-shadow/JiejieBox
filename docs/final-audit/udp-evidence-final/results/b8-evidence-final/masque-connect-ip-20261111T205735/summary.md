# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T205735`
- kind: e2e
- started: 2026-11-11T20:57:35+08:00
- commit: `9ab0d01eeedd` (fix(perf): initialize scenario extras before recording effective parameters)
- branch: `work/udp-evidence-final-2026-11-11`
- **dirty worktree**: 6 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.06 | 0.00 | 0.06 | 0.06 | null | MiB/s |
| wire_gbit_per_s | 1 | udp | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.23 | 0.01 | 2.23 | 2.30 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.51 | 0.11 | 81.38 | 81.63 | null | ms |
| latency_p95 | 1 | udp | 3 | 82.14 | 0.23 | 81.91 | 82.47 | null | ms |
| latency_p99 | 1 | udp | 3 | 82.43 | 0.23 | 82.20 | 112.07 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 0.3 | 5.000 | 0.1 | null | null | 0.00 | 2.30 | 81.51 | 82.47 | 112.07 |
| 1 | 1 | udp | 0.3 | 5.001 | 0.1 | null | null | 0.00 | 2.23 | 81.63 | 82.14 | 82.43 |
| 2 | 1 | udp | 0.3 | 5.001 | 0.1 | null | null | 0.00 | 2.23 | 81.38 | 81.91 | 82.20 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.260 | 89.2 | 15 | 14 | 0.260 | 93.1 | 14 | 14 |
| 1 | 0.300 | 89.5 | 16 | 13 | 0.300 | 93.3 | 14 | 15 |
| 2 | 0.260 | 89.7 | 16 | 13 | 0.260 | 93.3 | 14 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
