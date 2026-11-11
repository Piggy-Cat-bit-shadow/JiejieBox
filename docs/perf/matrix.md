# Performance matrix

Every row below is a real server process and a real client process. `null` means not measured.

| scenario | network | commit | repetitions | run dir |
|---|---|---|---|---|
| anytls-tcp | loopback | `bf0e0bec5600` | 30 | `loopback/anytls-tcp-20261111T114228` |
| awg-endpoint | loopback | `bf0e0bec5600` | 30 | `loopback/awg-endpoint-20261111T120712` |
| masque-connect-ip | loopback | `bf0e0bec5600` | 30 | `loopback/masque-connect-ip-20261111T115737` |
| masque-connect-ip | remote | `bf0e0bec5600` | 17 | `shaped-remote/masque-connect-ip-20261111T125139` |
| mieru-tcp-underlay | loopback | `bf0e0bec5600` | 30 | `loopback/mieru-tcp-underlay-20261111T115132` |
| mieru-udp-underlay | loopback | `bf0e0bec5600` | 30 | `loopback/mieru-udp-underlay-20261111T115433` |
| naive-h2 | native | `bf0e0bec5600` | 18 | `native/naive-h2-20261111T124028` |
| naive-h3 | native | `bf0e0bec5600` | 18 | `native/naive-h3-20261111T124233` |
| trusttunnel-h2 | loopback | `bf0e0bec5600` | 30 | `loopback/trusttunnel-h2-20261111T114529` |
| trusttunnel-h2 | loopback-extra | `bf0e0bec5600` | 6 | `loopback-extra/trusttunnel-h2-20261111T123648` |
| trusttunnel-h2 | normal | `bf0e0bec5600` | 9 | `shaped-100mbit/trusttunnel-h2-20261111T125505` |
| trusttunnel-h2 | remote | `bf0e0bec5600` | 15 | `shaped-remote/trusttunnel-h2-20261111T124834` |
| trusttunnel-h3 | loopback | `bf0e0bec5600` | 30 | `loopback/trusttunnel-h3-20261111T114830` |
| vless-reality-vision | loopback | `bf0e0bec5600` | 30 | `loopback/vless-reality-vision-20261111T113927` |
| vless-vision-tcp | loopback | `bf0e0bec5600` | 20 | `loopback/vless-vision-tcp-20261111T113727` |
| vless-vision-tcp | loopback-extra | `bf0e0bec5600` | 6 | `loopback-extra/vless-vision-tcp-20261111T123523` |
| vless-vision-tcp | normal | `bf0e0bec5600` | 6 | `shaped-100mbit/vless-vision-tcp-20261111T125408` |
| vless-vision-tcp | remote | `bf0e0bec5600` | 12 | `shaped-remote/vless-vision-tcp-20261111T124649` |
| wireguard-endpoint | loopback | `bf0e0bec5600` | 30 | `loopback/wireguard-endpoint-20261111T120039` |

## TCP with TLS

### anytls-tcp — loopback

- run dir: `loopback/anytls-tcp-20261111T114228`
- protocol: anytls / tcp / tls
- startup: client 102 ms, server 101 ms

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| download | 1 | 5 | 557.2 | 2.82 | 1.91 | null | null | null | null | null |
| download | 4 | 5 | 1198.2 | 4.20 | 3.43 | null | null | null | null | null |
| udp | 1 | 5 | 23.9 | 102.08 | 99.93 | null | null | null | null | null |
| udp | 4 | 5 | 22.3 | 108.55 | 106.34 | null | null | null | null | null |
| upload | 1 | 5 | 255.4 | 4.19 | 3.52 | null | null | null | null | null |
| upload | 4 | 5 | 750.3 | 5.60 | 3.80 | null | null | null | null | null |

### vless-reality-vision — loopback

- run dir: `loopback/vless-reality-vision-20261111T113927`
- protocol: vless / tcp / reality
- startup: client 102 ms, server 102 ms

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| connect | 1 | 0 | null | null | null | null | null | 4.09 | 4.88 | 5.27 |
| connect | 4 | 0 | null | null | null | null | null | 9.99 | 11.43 | 13.13 |
| download | 1 | 5 | 542.8 | 2.00 | 1.05 | null | null | null | null | null |
| download | 4 | 5 | 1365.1 | 3.09 | 2.07 | null | null | null | null | null |
| upload | 1 | 5 | 255.2 | 4.21 | 2.98 | null | null | null | null | null |
| upload | 4 | 5 | 782.8 | 5.38 | 3.19 | null | null | null | null | null |

### vless-vision-tcp — loopback

- run dir: `loopback/vless-vision-tcp-20261111T113727`
- protocol: vless / tcp / tls
- startup: client 102 ms, server 102 ms

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| download | 1 | 5 | 532.5 | 2.05 | 1.13 | null | null | null | null | null |
| download | 4 | 5 | 1372.8 | 3.10 | 2.13 | null | null | null | null | null |
| upload | 1 | 5 | 243.7 | 4.40 | 3.18 | null | null | null | null | null |
| upload | 4 | 5 | 608.6 | 6.97 | 4.35 | null | null | null | null | null |

### vless-vision-tcp — loopback-extra

- run dir: `loopback-extra/vless-vision-tcp-20261111T123523`
- protocol: vless / tcp / tls
- startup: client 101 ms, server 102 ms

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| duplex | 1 | 3 | 446.1 | 3.93 | 2.48 | null | null | null | null | null |
| pingpong | 1 | 3 | 2.1 | 220.59 | 223.37 | null | null | 0.43 | 0.67 | 0.86 |

### vless-vision-tcp — normal

- run dir: `shaped-100mbit/vless-vision-tcp-20261111T125408`
- protocol: vless / tcp / tls
- startup: client 102 ms, server 142 ms
- network: normal, one-way delay 10.0 ms, loss 0.00% per direction, cap 100 Mbit/s

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| download | 1 | 3 | 11.1 | 14.03 | 1.31 | 0.09 | 1.01 | null | null | null |
| upload | 1 | 3 | 11.8 | 3.80 | 14.40 | 0.09 | 0.95 | null | null | null |

### vless-vision-tcp — remote

- run dir: `shaped-remote/vless-vision-tcp-20261111T124649`
- protocol: vless / tcp / tls
- startup: client 102 ms, server 263 ms
- network: remote, one-way delay 40.0 ms, loss 1.00% per direction

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| download | 1 | 3 | 0.6 | 25.50 | 63.76 | 0.02 | 4.14 | null | null | null |
| download | 4 | 3 | 2.6 | 12.50 | 3.61 | 0.02 | 1.05 | null | null | null |
| upload | 1 | 3 | 1.5 | 5.00 | 14.50 | 0.01 | 0.99 | null | null | null |
| upload | 4 | 3 | 5.6 | 4.71 | 14.33 | 0.04 | 0.96 | null | null | null |

## HTTP/2

### naive-h2 — native

- run dir: `native/naive-h2-20261111T124028`
- protocol: naive / http2 / tls
- startup: client 102 ms, server 101 ms

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| connect | 1 | 0 | null | null | null | null | null | 4.06 | 5.00 | 5.33 |
| connect | 4 | 0 | null | null | null | null | null | 4.89 | 6.03 | 9.32 |
| download | 1 | 3 | 217.6 | 6.74 | 5.57 | null | null | null | null | null |
| download | 4 | 3 | 342.5 | 6.64 | 4.55 | null | null | null | null | null |
| upload | 1 | 3 | 119.4 | 10.79 | 10.93 | null | null | null | null | null |
| upload | 4 | 3 | 224.7 | 9.99 | 9.97 | null | null | null | null | null |

### trusttunnel-h2 — loopback

- run dir: `loopback/trusttunnel-h2-20261111T114529`
- protocol: trusttunnel / http2 / tls
- startup: client 102 ms, server 102 ms

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| download | 1 | 5 | 134.8 | 9.96 | 11.02 | null | null | null | null | null |
| download | 4 | 5 | 137.8 | 13.42 | 12.71 | null | null | null | null | null |
| udp | 1 | 5 | 9.6 | 217.19 | 244.53 | null | null | null | null | null |
| udp | 4 | 5 | 8.9 | 233.60 | 263.60 | null | null | null | null | null |
| upload | 1 | 5 | 176.1 | 9.85 | 9.13 | null | null | null | null | null |
| upload | 4 | 5 | 208.5 | 10.35 | 8.60 | null | null | null | null | null |

### trusttunnel-h2 — loopback-extra

- run dir: `loopback-extra/trusttunnel-h2-20261111T123648`
- protocol: trusttunnel / http2 / tls
- startup: client 102 ms, server 102 ms

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| duplex | 1 | 3 | 172.1 | 10.15 | 10.30 | null | null | null | null | null |
| pingpong | 1 | 3 | 1.3 | 467.62 | 565.72 | null | null | 0.70 | 1.02 | 1.22 |

### trusttunnel-h2 — normal

- run dir: `shaped-100mbit/trusttunnel-h2-20261111T125505`
- protocol: trusttunnel / http2 / tls
- startup: client 102 ms, server 142 ms
- network: normal, one-way delay 10.0 ms, loss 0.00% per direction, cap 100 Mbit/s

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| download | 1 | 3 | 11.2 | 17.87 | 16.23 | 0.10 | 1.02 | null | null | null |
| udp | 1 | 3 | 10.1 | 139.34 | 170.58 | 0.19 | 2.29 | null | null | null |
| upload | 1 | 3 | 11.8 | 13.71 | 21.17 | 0.10 | 0.97 | null | null | null |

### trusttunnel-h2 — remote

- run dir: `shaped-remote/trusttunnel-h2-20261111T124834`
- protocol: trusttunnel / http2 / tls
- startup: client 102 ms, server 262 ms
- network: remote, one-way delay 40.0 ms, loss 1.00% per direction

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| download | 1 | 1 | 0.0 | 3276.80 | 3932.16 | 0.00 | 178.42 | null | null | null |
| download | 4 | 2 | 0.0 | 462.61 | 665.00 | 0.01 | 35.29 | null | null | null |
| udp | 1 | 3 | 0.1 | 224.63 | 336.95 | 0.00 | 3.99 | null | null | null |
| udp | 4 | 3 | 0.1 | 181.22 | 282.27 | 0.00 | 3.55 | null | null | null |
| upload | 1 | 3 | 0.3 | 13.81 | 20.08 | 0.00 | 1.00 | null | null | null |
| upload | 4 | 3 | 0.3 | 14.54 | 29.09 | 0.00 | 0.95 | null | null | null |

## HTTP/3 and QUIC

### masque-connect-ip — loopback

- run dir: `loopback/masque-connect-ip-20261111T115737`
- protocol: masque / quic / tls
- startup: client 102 ms, server 802 ms

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| download | 1 | 5 | 64.0 | 41.92 | 36.12 | null | null | null | null | null |
| download | 4 | 5 | 70.1 | 47.62 | 37.97 | null | null | null | null | null |
| udp | 1 | 5 | 45.6 | 93.71 | 86.13 | null | null | null | null | null |
| udp | 4 | 5 | 46.9 | 88.31 | 80.76 | null | null | null | null | null |
| upload | 1 | 5 | 64.2 | 37.46 | 40.65 | null | null | null | null | null |
| upload | 4 | 5 | 72.4 | 38.75 | 45.84 | null | null | null | null | null |

### masque-connect-ip — remote

- run dir: `shaped-remote/masque-connect-ip-20261111T125139`
- protocol: masque / quic / tls
- startup: client 102 ms, server 802 ms
- network: remote, one-way delay 40.0 ms, loss 1.00% per direction

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| download | 1 | 3 | 0.1 | 1225.45 | 1354.45 | 0.00 | 6.67 | null | null | null |
| download | 4 | 3 | 0.1 | 1495.69 | 1866.38 | 0.00 | 9.79 | null | null | null |
| udp | 1 | 2 | 0.0 | 33644.41 | 4249.96 | 0.00 | 24.02 | null | null | null |
| udp | 4 | 3 | 0.0 | 80530.64 | 11591.53 | 0.00 | 64.97 | null | null | null |
| upload | 1 | 3 | 0.3 | 243.29 | 249.37 | 0.00 | 1.14 | null | null | null |
| upload | 4 | 3 | 0.2 | 254.53 | 263.96 | 0.00 | 1.28 | null | null | null |

### naive-h3 — native

- run dir: `native/naive-h3-20261111T124233`
- protocol: naive / http3 / tls
- startup: client 102 ms, server 102 ms

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| connect | 1 | 0 | null | null | null | null | null | 3.50 | 4.30 | 4.76 |
| connect | 4 | 0 | null | null | null | null | null | 4.85 | 9.93 | 11.71 |
| download | 1 | 3 | 79.4 | 17.28 | 19.83 | null | null | null | null | null |
| download | 4 | 3 | 66.8 | 27.34 | 22.10 | null | null | null | null | null |
| upload | 1 | 3 | 53.1 | 21.37 | 22.71 | null | null | null | null | null |
| upload | 4 | 3 | 67.3 | 20.45 | 24.01 | null | null | null | null | null |

### trusttunnel-h3 — loopback

- run dir: `loopback/trusttunnel-h3-20261111T114830`
- protocol: trusttunnel / http3 / tls
- startup: client 102 ms, server 802 ms

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| download | 1 | 5 | 195.0 | 11.04 | 11.11 | null | null | null | null | null |
| download | 4 | 5 | 202.2 | 17.65 | 12.02 | null | null | null | null | null |
| udp | 1 | 5 | 32.0 | 90.49 | 90.68 | null | null | null | null | null |
| udp | 4 | 5 | 28.4 | 100.54 | 101.12 | null | null | null | null | null |
| upload | 1 | 5 | 119.1 | 16.05 | 14.83 | null | null | null | null | null |
| upload | 4 | 5 | 196.5 | 14.83 | 17.46 | null | null | null | null | null |

## UDP underlay and userspace tunnels

### awg-endpoint — loopback

- run dir: `loopback/awg-endpoint-20261111T120712`
- protocol: awg / udp / noise
- startup: client 102 ms, server 802 ms

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| upload | 1 | 2 | 49.8 | 55.34 | 58.42 | null | null | null | null | null |

### mieru-tcp-underlay — loopback

- run dir: `loopback/mieru-tcp-underlay-20261111T115132`
- protocol: mieru / __TRANSPORT__ /
- startup: client 102 ms, server 102 ms

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| connect | 1 | 0 | null | null | null | null | null | 2.43 | 3.19 | 3.61 |
| connect | 4 | 0 | null | null | null | null | null | 7.99 | 10.79 | 13.08 |
| download | 1 | 5 | 124.5 | 7.07 | 6.64 | null | null | null | null | null |
| download | 4 | 5 | 570.9 | 7.55 | 7.25 | null | null | null | null | null |
| upload | 1 | 5 | 126.5 | 8.75 | 6.46 | null | null | null | null | null |
| upload | 4 | 5 | 438.5 | 10.79 | 7.15 | null | null | null | null | null |

### mieru-udp-underlay — loopback

- run dir: `loopback/mieru-udp-underlay-20261111T115433`
- protocol: mieru / udp /
- startup: client 101 ms, server 802 ms

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| connect | 1 | 0 | null | null | null | null | null | 2.80 | 3.67 | 4.19 |
| connect | 4 | 0 | null | null | null | null | null | 8.69 | 10.68 | 12.18 |
| download | 1 | 5 | 28.5 | 60.06 | 56.53 | null | null | null | null | null |
| download | 4 | 5 | 34.3 | 64.00 | 58.10 | null | null | null | null | null |
| upload | 1 | 5 | 29.0 | 53.62 | 58.33 | null | null | null | null | null |
| upload | 4 | 5 | 71.4 | 46.68 | 40.48 | null | null | null | null | null |

### wireguard-endpoint — loopback

- run dir: `loopback/wireguard-endpoint-20261111T120039`
- protocol: wireguard / udp / noise
- startup: client 102 ms, server 802 ms

| pattern | streams | n | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|---|---|---|---|---|
| upload | 1 | 5 | 44.8 | 61.69 | 65.20 | null | null | null | null | null |
