# TrustTunnel 入站

JiejieBox 的 `trusttunnel` 入站支持 HTTP/2（TLS/TCP）和 HTTP/3（QUIC/UDP）监听，以及隧道内 TCP 连接和 UDP association。需要带 `with_trusttunnel` 的内核。`network` 在**入站**选择外层监听：`tcp` 对应 H2，`udp` 对应 H3；它不是隧道内业务 TCP/UDP 的开关。

```json
{
  "type": "trusttunnel",
  "tag": "tt-in",
  "listen": "::",
  "listen_port": 443,
  "users": [{"username": "user", "password": "替换为密码"}],
  "network": ["tcp", "udp"],
  "quic_congestion_control": "bbr",
  "tls": {"enabled": true, "certificate_path": "/path/to/cert.pem", "key_path": "/path/to/key.pem"}
}
```

`users` 是服务端允许的身份列表。`quic_congestion_control` 适用于 H3，可选 `bbr`、`cubic`、`reno`，未设置时默认使用 `cubic`。同时启用 TCP 与 UDP 监听时，ALPN 按传输隔离：QUIC 侧只提供 `h3`，TCP 侧提供 `h2`（以及 `tls.alpn` 中除 `h3` 外的值），两侧不会协商出对方无法使用的协议。TLS 证书和密钥按[入站 TLS](../shared/tls.zh.md)配置。平台监听、防火墙和证书路径由实际部署环境决定；验证 H2/H3 时分别检查 TCP/UDP 监听。协议的认证、CONNECT、UDP association 及关闭由该入站处理。[出站配置](../outbound/trusttunnel.zh.md)。
