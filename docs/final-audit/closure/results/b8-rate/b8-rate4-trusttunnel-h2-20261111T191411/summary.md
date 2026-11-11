# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T191411`
- kind: e2e
- started: 2026-11-11T19:14:11+08:00
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
| goodput | 1 | udp | 3 | 0.29 | 0.00 | 0.29 | 0.29 | null | MiB/s |
| cpu_seconds_per_gib_client | 1 | udp | 3 | 343.60 | 21.47 | 322.12 | 372.23 | null | s/GiB |
| cpu_seconds_per_gib_server | 1 | udp | 3 | 193.27 | 28.63 | 164.64 | 329.28 | null | s/GiB |
| wire_gbit_per_s | 1 | udp | 3 | 0.01 | 0.00 | 0.01 | 0.01 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.25 | 0.01 | 2.24 | 2.28 | null | ratio |
| latency_p50 | 1 | udp | 3 | 678.55 | 163.57 | 514.98 | 2357.52 | null | ms |
| latency_p95 | 1 | udp | 3 | 921.93 | 28.33 | 893.60 | 2526.66 | null | ms |
| latency_p99 | 1 | udp | 3 | 951.55 | 37.44 | 914.11 | 2545.23 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 1.4 | 5.000 | 0.3 | 322.12 | 329.28 | 0.01 | 2.28 | 678.55 | 893.60 | 914.11 |
| 1 | 1 | udp | 1.4 | 5.000 | 0.3 | 343.60 | 193.27 | 0.01 | 2.24 | 514.98 | 921.93 | 951.55 |
| 2 | 1 | udp | 1.4 | 5.000 | 0.3 | 372.23 | 164.64 | 0.01 | 2.25 | 2357.52 | 2526.66 | 2545.23 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.450 | 85.2 | 14 | 13 | 0.460 | 85.8 | 13 | 14 |
| 1 | 0.480 | 85.6 | 14 | 13 | 0.270 | 86.4 | 13 | 14 |
| 2 | 0.520 | 86.0 | 14 | 13 | 0.230 | 87.2 | 13 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
