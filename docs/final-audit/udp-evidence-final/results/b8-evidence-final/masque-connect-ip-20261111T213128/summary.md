# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T213128`
- kind: e2e
- started: 2026-11-11T21:31:28+08:00
- commit: `aa32f0c47bb0` (test(perf): pin quiesce timeout, confirmation and unavailable states)
- branch: `work/udp-evidence-final-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss0 delay=40.0ms loss=0.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.29 | 0.00 | 0.29 | 0.29 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 808.89 | 7.16 | 751.62 | 816.04 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 816.04 | 42.95 | 744.46 | 858.99 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.22 | 0.00 | 2.22 | 2.22 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.41 | 0.00 | 81.22 | 81.41 | null | ms |
| latency_p95 | 1 | udp | 3 | 81.87 | 0.01 | 81.59 | 81.88 | null | ms |
| latency_p99 | 1 | udp | 3 | 82.11 | 0.03 | 81.80 | 82.14 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.4 | 5.000 | 0.3 | 751.62 | 744.46 | 0.01 | 2.22 | 81.22 | 81.59 | 81.80 |
| 1 | 1 | udp | 1.4 | 5.000 | 0.3 | 816.04 | 858.99 | 0.01 | 2.22 | 81.41 | 81.88 | 82.11 |
| 2 | 1 | udp | 1.4 | 5.001 | 0.3 | 808.89 | 816.04 | 0.01 | 2.22 | 81.41 | 81.87 | 82.14 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 1.050 | 91.6 | 14 | 12 | 1.040 | 94.2 | 15 | 12 |
| 1 | 1.140 | 91.6 | 15 | 12 | 1.200 | 93.8 | 15 | 14 |
| 2 | 1.130 | 90.7 | 15 | 12 | 1.140 | 94.5 | 15 | 16 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
