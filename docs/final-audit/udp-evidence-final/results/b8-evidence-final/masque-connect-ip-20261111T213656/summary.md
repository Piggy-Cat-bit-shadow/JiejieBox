# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T213656`
- kind: e2e
- started: 2026-11-11T21:36:56+08:00
- commit: `aa32f0c47bb0` (test(perf): pin quiesce timeout, confirmation and unavailable states)
- branch: `work/udp-evidence-final-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.06 | 0.00 | 0.04 | 0.06 | null | MiB/s |
| wire_gbit_per_s | 1 | udp | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.24 | 0.00 | 2.24 | 2.24 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.42 | 0.03 | 81.39 | 81.48 | null | ms |
| latency_p95 | 1 | udp | 3 | 81.87 | 0.03 | 81.84 | 81.99 | null | ms |
| latency_p99 | 1 | udp | 3 | 82.27 | 0.23 | 82.04 | 2468.69 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 0.3 | 5.001 | 0.1 | null | null | 0.00 | 2.24 | 81.42 | 81.87 | 82.04 |
| 1 | 1 | udp | 0.3 | 5.000 | 0.1 | null | null | 0.00 | 2.24 | 81.39 | 81.84 | 82.27 |
| 2 | 1 | udp | 0.3 | 7.388 | 0.0 | null | null | 0.00 | 2.24 | 81.48 | 81.99 | 2468.69 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.260 | 89.4 | 14 | 12 | 0.250 | 93.1 | 16 | 12 |
| 1 | 0.250 | 89.4 | 14 | 12 | 0.250 | 93.2 | 16 | 14 |
| 2 | 0.260 | 89.4 | 14 | 12 | 0.240 | 93.2 | 16 | 16 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
