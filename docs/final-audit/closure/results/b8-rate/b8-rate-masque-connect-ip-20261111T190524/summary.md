# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T190524`
- kind: e2e
- started: 2026-11-11T19:05:24+08:00
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
| goodput | 1 | udp | 3 | 0.11 | 0.00 | 0.08 | 0.11 | null | MiB/s |
| wire_gbit_per_s | 1 | udp | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.63 | 0.41 | 2.22 | 4.14 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.78 | 0.34 | 81.43 | 151.64 | null | ms |
| latency_p95 | 1 | udp | 3 | 212.01 | 130.16 | 81.85 | 501.65 | null | ms |
| latency_p99 | 1 | udp | 3 | 541.77 | 459.39 | 82.38 | 2237.01 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 0.6 | 5.000 | 0.1 | null | null | 0.00 | 4.14 | 151.64 | 501.65 | 541.77 |
| 1 | 1 | udp | 0.6 | 7.155 | 0.1 | null | null | 0.00 | 2.63 | 81.78 | 212.01 | 2237.01 |
| 2 | 1 | udp | 0.6 | 5.000 | 0.1 | null | null | 0.00 | 2.22 | 81.43 | 81.85 | 82.38 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.480 | 90.3 | 16 | 14 | 0.390 | 92.8 | 16 | 14 |
| 1 | 0.510 | 90.1 | 16 | 14 | 0.480 | 93.6 | 16 | 16 |
| 2 | 0.490 | 90.2 | 16 | 13 | 0.500 | 94.4 | 16 | 17 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
