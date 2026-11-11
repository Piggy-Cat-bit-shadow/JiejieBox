# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T182640`
- kind: e2e
- started: 2026-11-11T18:26:40+08:00
- commit: `f03b1c728993` (perf(udp): make drain completeness explicit and record invocation parameters)
- branch: `work/closure-final-reconcile-2026-11-11`
- **dirty worktree**: 5 file(s)
- go: go1.25.5
- tags: `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname,with_trusttunnel,with_awg,with_naive_outbound,with_purego`
- host: linux/amd64 Ubuntu 24.04, 16 cores, AMD Ryzen 7 4800H with Radeon Graphics
- network: netns-veth profile=custom-rtt80-loss1 delay=40.0ms loss=1.00% mtu=1500

## Summary (median +/- MAD over repetitions)

| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |
|---|---|---|---|---|---|---|---|---|---|
| goodput | 1 | udp | 3 | 0.11 | 0.00 | 0.10 | 0.14 | null | MiB/s |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.00 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 5.15 | 0.47 | 4.68 | 6.38 | null | ratio |
| latency_p50 | 1 | udp | 3 | 2807.55 | 1220.46 | 1568.34 | 4028.01 | null | ms |
| latency_p95 | 1 | udp | 3 | 5414.24 | 52.88 | 5057.14 | 5467.12 | null | ms |
| latency_p99 | 1 | udp | 3 | 5450.22 | 62.89 | 5106.32 | 5513.11 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 0.5 | 5.001 | 0.1 | null | null | 0.01 | 6.38 | 4028.01 | 5414.24 | 5450.22 |
| 1 | 1 | udp | 0.7 | 5.000 | 0.1 | null | null | 0.01 | 5.15 | 1568.34 | 5057.14 | 5106.32 |
| 2 | 1 | udp | 0.7 | 6.835 | 0.1 | null | null | 0.00 | 4.68 | 2807.55 | 5467.12 | 5513.11 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.170 | 60.4 | 13 | 14 | 0.200 | 62.1 | 12 | 14 |
| 1 | 0.400 | 62.0 | 13 | 13 | 0.200 | 63.3 | 12 | 14 |
| 2 | 0.330 | 63.1 | 13 | 13 | 0.240 | 64.0 | 13 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
