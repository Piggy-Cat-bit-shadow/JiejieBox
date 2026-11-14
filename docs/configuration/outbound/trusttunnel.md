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

Credentials and TLS are required. Set `tls.server_name` when the server is a literal IP. `quic_congestion_control` accepts `bbr`, `cubic`, or `reno`; unset uses `cubic`. `health_check` enables health checks; `idle_timeout` controls idle session retention. Streams may share a session, which remains under its protocol owner's lifecycle. H3 cannot be combined with uTLS; keep TLS ALPN consistent with the selected transport.

## H3 → H2 fallback

Available since **0.1.46**, `quic_fallback_to_h2` defaults to `false` and requires `quic: true`. Without it, `quic: true` remains strict H3; omitting `quic` selects H2. Enabling fallback with `quic: false` is rejected.

### Server and TLS requirements

The same `server` and `server_port` must offer both UDP/H3 and TCP/H2 with the same identity and credentials. For a JiejieBox inbound, use `network: ["tcp", "udp"]` and allow both transports through the firewall. See the [inbound example](../inbound/trusttunnel.md). Omit `tls.alpn` to derive both protocols automatically, or explicitly set `"alpn": ["h3", "h2"]`; an H3-only list is rejected. The build needs `with_trusttunnel` and `with_quic`; H3 still cannot be combined with uTLS. See [build profiles](../../BUILD-PROFILES.md).

### Attempts, cooldown and recovery

1. A new session tries H3 for at most **3 seconds**, bounded by the shared creation's remaining deadline. H2 uses the remaining time; fallback never extends that deadline.
2. An eligible UDP/QUIC establishment failure or attempt timeout starts a **30-second clock-based cooldown**, and the same node is tried over H2. If H2 also fails, both errors are retained. New sessions during the cooldown go directly to H2. These durations are implementation constants, not configuration fields.
3. After expiry, new real demand retires a fallback H2 session from accepting new work and tries H3. Existing streams and UDP associations finish on the old session without migration or replay. There is no periodic probe and expiry alone does not interrupt active traffic.
4. A network change clears the verdict. Both late successes and failures check their network generation before changing it; the verdict belongs to each client instance, not a global H3 switch.

Authentication refusals, certificate verification failures, peer TLS alerts and business CONNECT refusals do not trigger fallback. Cancelled or expired creations do not continue to H2. This option cannot bypass TLS or authentication checks and does not retry arbitrary business operations.

For diagnosis, enable debug logging and look for `HTTP/3 attempt`, `HTTP/3 is cordoned` and `retired to retry HTTP/3`. Check the server's TCP/H2 listener, ALPN and certificate before treating every failure as blocked UDP.
