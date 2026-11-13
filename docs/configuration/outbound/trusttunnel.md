# TrustTunnel outbound

The native `trusttunnel` client uses H2 over TLS/TCP or H3 over QUIC/UDP for TCP streams and UDP associations. Build with `with_trusttunnel`. Set `quic: true` for H3; omit it or use `false` for H2. With `quic: true`, opt into `quic_fallback_to_h2` to open an HTTP/2 session when the HTTP/3 attempt fails. On an **outbound**, `network` limits inner TCP/UDP traffic, unlike the inbound listener selector.

```json
{
  "type": "trusttunnel",
  "tag": "tt-out",
  "server": "proxy.example.com",
  "server_port": 443,
  "username": "user",
  "password": "replace-me",
  "quic": true,
  "quic_fallback_to_h2": true,
  "quic_congestion_control": "bbr",
  "health_check": true,
  "idle_timeout": "5m",
  "network": ["tcp", "udp"],
  "tls": {"enabled": true, "server_name": "proxy.example.com"}
}
```

Credentials and TLS are required. Set `tls.server_name` when the server is a literal IP. `quic_congestion_control` accepts `bbr`, `cubic`, or `reno`. `health_check` enables health checks; `idle_timeout` controls idle session retention. Streams may share a session, which remains under its protocol owner's lifecycle. H3 cannot be combined with uTLS; keep TLS ALPN consistent with the selected transport.

**`quic_fallback_to_h2`** (off by default; requires `quic: true`): a new session first attempts H3 under a 3-second budget (the caller's remaining time wins when it is shorter). If that attempt fails or runs out, the same node is reached over H2 for this session, and new sessions go straight to H2 for the following 30 seconds instead of paying the QUIC timeout again. The cooldown expires by the clock and the next real demand probes H3 once more; a network change clears the verdict. The fallback only happens while creating a session: established streams and UDP associations are neither migrated nor replayed, and authentication, certificate verification, target refusals and cancellation are never bypassed by it. It needs a server that offers both QUIC and TCP/H2 on the same endpoint; a build without `with_quic`, or a `tls.alpn` that names only `h3`, fails at startup. [Inbound configuration](../inbound/trusttunnel.md) · [build profiles](../../BUILD-PROFILES.md).
