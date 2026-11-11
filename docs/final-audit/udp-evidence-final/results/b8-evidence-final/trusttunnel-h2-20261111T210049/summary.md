# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T210049`
- kind: e2e
- started: 2026-11-11T21:00:49+08:00
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
| goodput | 1 | udp | 3 | 0.29 | 0.00 | 0.20 | 0.29 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 379.39 | 14.32 | 365.07 | 451.69 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 164.64 | 28.42 | 136.23 | 207.59 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.24 | 0.01 | 2.23 | 2.25 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1574.23 | 140.84 | 1433.39 | 1880.35 | null | ms |
| latency_p95 | 1 | udp | 3 | 3437.98 | 258.68 | 1856.85 | 3696.67 | null | ms |
| latency_p99 | 1 | udp | 3 | 3635.16 | 81.26 | 1880.09 | 3716.42 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.4 | 5.000 | 0.3 | 365.07 | 164.64 | 0.01 | 2.25 | 1574.23 | 3437.98 | 3635.16 |
| 1 | 1 | udp | 1.4 | 6.999 | 0.2 | 379.39 | 207.59 | 0.00 | 2.24 | 1880.35 | 3696.67 | 3716.42 |
| 2 | 1 | udp | 1.4 | 5.000 | 0.3 | 451.69 | 136.23 | 0.01 | 2.23 | 1433.39 | 1856.85 | 1880.09 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.510 | 84.8 | 14 | 13 | 0.230 | 85.4 | 12 | 14 |
| 1 | 0.530 | 85.2 | 14 | 13 | 0.290 | 85.7 | 12 | 14 |
| 2 | 0.630 | 85.7 | 14 | 13 | 0.190 | 86.4 | 12 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
