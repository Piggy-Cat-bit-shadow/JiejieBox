# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T182800`
- kind: e2e
- started: 2026-11-11T18:28:00+08:00
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
| goodput | 1 | udp | 3 | 0.06 | 0.00 | 0.04 | 0.06 | null | MiB/s |
| wire_gbit_per_s | 1 | udp | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.37 | 0.00 | 2.37 | 2.60 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.43 | 0.02 | 81.40 | 81.55 | null | ms |
| latency_p95 | 1 | udp | 3 | 182.48 | 18.54 | 163.94 | 279.01 | null | ms |
| latency_p99 | 1 | udp | 3 | 458.52 | 237.08 | 221.44 | 2282.17 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 0.3 | 5.000 | 0.1 | null | null | 0.00 | 2.60 | 81.40 | 279.01 | 458.52 |
| 1 | 1 | udp | 0.3 | 7.141 | 0.0 | null | null | 0.00 | 2.37 | 81.55 | 182.48 | 2282.17 |
| 2 | 1 | udp | 0.3 | 5.000 | 0.1 | null | null | 0.00 | 2.37 | 81.43 | 163.94 | 221.44 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.140 | 60.4 | 11 | 13 | 0.170 | 61.2 | 12 | 15 |
| 1 | 0.160 | 61.0 | 12 | 13 | 0.200 | 61.8 | 13 | 14 |
| 2 | 0.150 | 61.1 | 12 | 13 | 0.160 | 62.0 | 13 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
