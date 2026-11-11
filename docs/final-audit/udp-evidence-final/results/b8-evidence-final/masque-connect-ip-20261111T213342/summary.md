# masque-connect-ip - masque

- run_id: `masque-connect-ip-20261111T213342`
- kind: e2e
- started: 2026-11-11T21:33:42+08:00
- commit: `aa32f0c47bb0` (test(perf): pin quiesce timeout, confirmation and unavailable states)
- branch: `work/udp-evidence-final-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.27 | 0.02 | 0.20 | 0.28 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 538.33 | 52.85 | 455.50 | 591.17 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 465.58 | 81.80 | 331.97 | 547.38 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.00 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.21 | 0.01 | 2.20 | 2.59 | null | ratio |
| latency_p50 | 1 | udp | 3 | 859.95 | 393.81 | 454.74 | 1253.76 | null | ms |
| latency_p95 | 1 | udp | 3 | 1617.95 | 581.05 | 1036.90 | 2886.20 | null | ms |
| latency_p99 | 1 | udp | 3 | 1659.24 | 560.17 | 1099.06 | 3012.40 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.4 | 7.015 | 0.2 | 538.33 | 465.58 | 0.00 | 2.59 | 454.74 | 2886.20 | 3012.40 |
| 1 | 1 | udp | 1.4 | 5.000 | 0.3 | 591.17 | 547.38 | 0.01 | 2.21 | 859.95 | 1036.90 | 1099.06 |
| 2 | 1 | udp | 1.3 | 5.001 | 0.3 | 455.50 | 331.97 | 0.00 | 2.20 | 1253.76 | 1617.95 | 1659.24 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.740 | 90.7 | 15 | 13 | 0.640 | 94.0 | 15 | 13 |
| 1 | 0.810 | 90.4 | 16 | 12 | 0.750 | 94.7 | 15 | 14 |
| 2 | 0.590 | 91.0 | 16 | 12 | 0.430 | 95.2 | 15 | 16 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
