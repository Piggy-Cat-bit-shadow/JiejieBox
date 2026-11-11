# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T191032`
- kind: e2e
- started: 2026-11-11T19:10:32+08:00
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
| goodput | 1 | udp | 3 | 0.27 | 0.04 | 0.23 | 0.31 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 320.97 | 18.33 | 302.64 | 414.74 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 167.66 | 28.85 | 114.20 | 196.51 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.31 | 0.04 | 2.27 | 3.02 | null | ratio |
| latency_p50 | 1 | udp | 3 | 2373.66 | 8.72 | 2364.94 | 3365.59 | null | ms |
| latency_p95 | 1 | udp | 3 | 4118.61 | 423.00 | 3695.61 | 4711.73 | null | ms |
| latency_p99 | 1 | udp | 3 | 4260.07 | 430.17 | 3829.89 | 4948.86 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.2 | 5.000 | 0.2 | 414.74 | 167.66 | 0.00 | 2.31 | 2373.66 | 4711.73 | 4948.86 |
| 1 | 1 | udp | 1.8 | 6.650 | 0.3 | 302.64 | 114.20 | 0.01 | 2.27 | 2364.94 | 3695.61 | 3829.89 |
| 2 | 1 | udp | 1.6 | 5.000 | 0.3 | 320.97 | 196.51 | 0.01 | 3.02 | 3365.59 | 4118.61 | 4260.07 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.470 | 84.7 | 13 | 13 | 0.190 | 85.8 | 12 | 15 |
| 1 | 0.530 | 85.1 | 13 | 13 | 0.200 | 86.9 | 12 | 14 |
| 2 | 0.490 | 85.6 | 13 | 13 | 0.300 | 88.3 | 13 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
