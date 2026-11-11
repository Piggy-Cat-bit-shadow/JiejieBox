# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T213519`
- kind: e2e
- started: 2026-11-11T21:35:19+08:00
- commit: `aa32f0c47bb0` (test(perf): pin quiesce timeout, confirmation and unavailable states)
- branch: `work/udp-evidence-final-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.31 | 0.03 | 0.28 | 0.36 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 574.33 | 28.07 | 497.10 | 602.40 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 398.12 | 50.01 | 344.58 | 448.13 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.20 | 0.00 | 2.20 | 2.47 | null | ratio |
| latency_p50 | 1 | udp | 3 | 1739.00 | 329.08 | 1409.92 | 2093.81 | null | ms |
| latency_p95 | 1 | udp | 3 | 2391.08 | 317.36 | 1687.39 | 2708.44 | null | ms |
| latency_p99 | 1 | udp | 3 | 2516.93 | 238.09 | 1772.83 | 2755.03 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.4 | 5.000 | 0.3 | 602.40 | 448.13 | 0.01 | 2.47 | 1739.00 | 2391.08 | 2516.93 |
| 1 | 1 | udp | 1.8 | 5.000 | 0.4 | 497.10 | 344.58 | 0.01 | 2.20 | 2093.81 | 2708.44 | 2755.03 |
| 2 | 1 | udp | 1.6 | 5.001 | 0.3 | 574.33 | 398.12 | 0.01 | 2.20 | 1409.92 | 1687.39 | 1772.83 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.820 | 92.0 | 16 | 13 | 0.610 | 94.1 | 15 | 13 |
| 1 | 0.880 | 91.6 | 16 | 12 | 0.610 | 94.6 | 16 | 14 |
| 2 | 0.880 | 91.6 | 16 | 12 | 0.610 | 95.4 | 16 | 16 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
