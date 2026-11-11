# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T210359`
- kind: e2e
- started: 2026-11-11T21:03:59+08:00
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
| wire_overhead_ratio | 1 | udp | 3 | 2.37 | 0.00 | 2.37 | 2.38 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.34 | 0.02 | 81.24 | 81.36 | null | ms |
| latency_p95 | 1 | udp | 3 | 161.44 | 2.59 | 148.97 | 164.03 | null | ms |
| latency_p99 | 1 | udp | 3 | 196.92 | 4.22 | 181.96 | 201.14 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 0.3 | 5.001 | 0.1 | null | null | 0.00 | 2.37 | 81.24 | 161.44 | 201.14 |
| 1 | 1 | udp | 0.3 | 5.001 | 0.1 | null | null | 0.00 | 2.38 | 81.34 | 164.03 | 196.92 |
| 2 | 1 | udp | 0.3 | 5.000 | 0.1 | null | null | 0.00 | 2.37 | 81.36 | 148.97 | 181.96 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.150 | 84.0 | 13 | 13 | 0.160 | 85.4 | 12 | 14 |
| 1 | 0.160 | 84.1 | 13 | 13 | 0.180 | 85.8 | 13 | 14 |
| 2 | 0.150 | 84.2 | 13 | 13 | 0.150 | 85.9 | 13 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
