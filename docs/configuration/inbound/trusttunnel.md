# TrustTunnel inbound

JiejieBox's `trusttunnel` inbound listens over HTTP/2 (TLS/TCP) or HTTP/3 (QUIC/UDP) and carries TCP connections and UDP associations. Build with `with_trusttunnel`. For an **inbound**, `network` selects the outer listeners: `tcp` for H2 and `udp` for H3. It does not limit the inner TCP/UDP traffic.

```json
{
  "type": "trusttunnel",
  "tag": "tt-in",
  "listen": "::",
  "listen_port": 443,
  "users": [{"username": "user", "password": "replace-me"}],
  "network": ["tcp", "udp"],
  "quic_congestion_control": "bbr",
  "tls": {"enabled": true, "certificate_path": "/path/to/cert.pem", "key_path": "/path/to/key.pem"}
}
```

`users` lists accepted credentials. `quic_congestion_control` applies to H3 and accepts `bbr`, `cubic`, or `reno`. When both listeners are enabled, ALPN is scoped per transport: the QUIC listener offers only `h3`, and the TCP listener offers `h2` (plus any non-`h3` values from `tls.alpn`), so neither can negotiate a protocol the other cannot serve. Configure the certificate and key using [inbound TLS](../shared/tls.md). Check TCP and UDP listeners separately when deploying both H2 and H3. The inbound owns authentication, CONNECT, UDP associations, and cleanup. [Outbound configuration](../outbound/trusttunnel.md).
