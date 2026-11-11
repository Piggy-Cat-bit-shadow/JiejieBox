# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T213833`
- kind: e2e
- started: 2026-11-11T21:38:33+08:00
- commit: `aa32f0c47bb0` (test(perf): pin quiesce timeout, confirmation and unavailable states)
- branch: `work/udp-evidence-final-2026-11-11`
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss0 delay=40.0ms loss=0.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.29 | 0.00 | 0.20 | 0.29 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 536.87 | 7.16 | 522.55 | 544.03 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 637.09 | 7.16 | 601.30 | 644.25 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.37 | 0.00 | 2.37 | 2.37 | null | ratio |
| latency_p50 | 1 | udp | 3 | 81.14 | 0.00 | 81.13 | 81.14 | null | ms |
| latency_p95 | 1 | udp | 3 | 146.88 | 3.16 | 111.68 | 150.04 | null | ms |
| latency_p99 | 1 | udp | 3 | 213.96 | 16.00 | 197.96 | 2149.55 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.4 | 5.000 | 0.3 | 544.03 | 644.25 | 0.01 | 2.37 | 81.13 | 111.68 | 197.96 |
| 1 | 1 | udp | 1.4 | 7.068 | 0.2 | 522.55 | 601.30 | 0.00 | 2.37 | 81.14 | 150.04 | 2149.55 |
| 2 | 1 | udp | 1.4 | 5.001 | 0.3 | 536.87 | 637.09 | 0.01 | 2.37 | 81.14 | 146.88 | 213.96 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.760 | 85.3 | 13 | 12 | 0.900 | 86.7 | 13 | 13 |
| 1 | 0.730 | 85.7 | 13 | 12 | 0.840 | 87.4 | 13 | 13 |
| 2 | 0.750 | 86.1 | 13 | 12 | 0.890 | 88.4 | 14 | 13 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
