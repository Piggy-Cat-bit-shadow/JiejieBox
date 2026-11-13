# TrustTunnel outbound

The native `trusttunnel` client uses H2 over TLS/TCP or H3 over QUIC/UDP for TCP streams and UDP associations. Build with `with_trusttunnel`. Set `quic: true` for H3; omit it or use `false` for H2. On an **outbound**, `network` limits inner TCP/UDP traffic, unlike the inbound listener selector.

```json
{
  "type": "trusttunnel",
  "tag": "tt-out",
  "server": "proxy.example.com",
  "server_port": 443,
  "username": "user",
  "password": "replace-me",
  "quic": true,
  "quic_congestion_control": "bbr",
  "health_check": true,
  "idle_timeout": "5m",
  "network": ["tcp", "udp"],
  "tls": {"enabled": true, "server_name": "proxy.example.com"}
}
```

Credentials and TLS are required. Set `tls.server_name` when the server is a literal IP. `quic_congestion_control` accepts `bbr`, `cubic`, or `reno`. `health_check` enables health checks; `idle_timeout` controls idle session retention. Streams may share a session, which remains under its protocol owner's lifecycle. H3 cannot be combined with uTLS; keep TLS ALPN consistent with the selected transport. [Inbound configuration](../inbound/trusttunnel.md) · [build profiles](../../BUILD-PROFILES.md).
