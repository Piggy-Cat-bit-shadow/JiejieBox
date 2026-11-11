# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T182600`
- kind: e2e
- started: 2026-11-11T18:26:00+08:00
- commit: `f03b1c728993` (perf(udp): make drain completeness explicit and record invocation parameters)
- branch: `work/closure-final-reconcile-2026-11-11`
- **dirty worktree**: 5 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss0.1 delay=40.0ms loss=0.10% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.57 | 0.00 | 0.57 | 0.57 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 362.07 | 74.58 | 286.33 | 436.66 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 415.18 | 2.92 | 412.26 | 486.76 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.31 | 0.02 | 2.28 | 2.34 | null | ratio |
| latency_p50 | 1 | udp | 3 | 82.86 | 1.85 | 81.01 | 202.22 | null | ms |
| latency_p95 | 1 | udp | 3 | 189.17 | 2.18 | 187.00 | 279.87 | null | ms |
| latency_p99 | 1 | udp | 3 | 258.10 | 1.23 | 256.86 | 293.61 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 2.9 | 5.001 | 0.6 | 436.66 | 486.76 | 0.01 | 2.34 | 81.01 | 187.00 | 256.86 |
| 1 | 1 | udp | 2.9 | 5.000 | 0.6 | 362.07 | 412.26 | 0.01 | 2.31 | 82.86 | 189.17 | 258.10 |
| 2 | 1 | udp | 2.9 | 5.000 | 0.6 | 286.33 | 415.18 | 0.01 | 2.28 | 202.22 | 279.87 | 293.61 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 1.220 | 60.5 | 13 | 13 | 1.360 | 62.5 | 13 | 14 |
| 1 | 1.010 | 61.5 | 13 | 13 | 1.150 | 63.5 | 14 | 14 |
| 2 | 0.800 | 62.6 | 13 | 13 | 1.160 | 63.7 | 14 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
