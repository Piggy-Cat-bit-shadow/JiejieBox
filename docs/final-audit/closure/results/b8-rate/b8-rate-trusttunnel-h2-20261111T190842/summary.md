# trusttunnel-h2 - trusttunnel

- run_id: `trusttunnel-h2-20261111T190842`
- kind: e2e
- started: 2026-11-11T19:08:42+08:00
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
| goodput | 1 | udp | 3 | 0.11 | 0.00 | 0.11 | 0.11 | null | MiB/s |
| wire_gbit_per_s | 1 | udp | 3 | 0.00 | 0.00 | 0.00 | 0.00 | null | Gbit/s |
| wire_overhead_ratio | 1 | udp | 3 | 2.33 | 0.00 | 2.31 | 2.33 | null | ratio |
| latency_p50 | 1 | udp | 3 | 90.94 | 9.33 | 81.61 | 180.52 | null | ms |
| latency_p95 | 1 | udp | 3 | 172.67 | 1.14 | 171.53 | 527.59 | null | ms |
| latency_p99 | 1 | udp | 3 | 194.71 | 3.36 | 191.36 | 545.50 | null | ms |

## Repetitions

| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | udp | 0.6 | 5.000 | 0.1 | null | null | 0.00 | 2.31 | 180.52 | 527.59 | 545.50 |
| 1 | 1 | udp | 0.6 | 5.001 | 0.1 | null | null | 0.00 | 2.33 | 81.61 | 171.53 | 191.36 |
| 2 | 1 | udp | 0.6 | 5.000 | 0.1 | null | null | 0.00 | 2.33 | 90.94 | 172.67 | 194.71 |

## Resources

| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |
|---|---|---|---|---|---|---|---|---|
| 0 | 0.260 | 85.1 | 14 | 13 | 0.270 | 85.1 | 12 | 14 |
| 1 | 0.270 | 85.3 | 14 | 13 | 0.300 | 85.7 | 13 | 14 |
| 2 | 0.270 | 85.4 | 14 | 13 | 0.280 | 85.9 | 13 | 14 |

## Notes

- source: vcs_unavailable: the binary carries no vcs.revision (linked worktree cannot be stamped by buildvcs)
