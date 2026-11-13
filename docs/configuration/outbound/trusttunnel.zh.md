# TrustTunnel 出站

`type: "trusttunnel"` 原生客户端支持 TLS/TCP 上的 H2 与 QUIC/UDP 上的 H3，承载 TCP stream 和 UDP association。需要带 `with_trusttunnel` 的内核。`quic: true` 选择 H3；省略或设为 `false` 使用 H2。出站的 `network` 限制**隧道内业务** TCP/UDP，含义与入站不同。

```json
{
  "type": "trusttunnel",
  "tag": "tt-out",
  "server": "proxy.example.com",
  "server_port": 443,
  "username": "user",
  "password": "替换为密码",
  "quic": true,
  "quic_congestion_control": "bbr",
  "health_check": true,
  "idle_timeout": "5m",
  "network": ["tcp", "udp"],
  "tls": {"enabled": true, "server_name": "proxy.example.com"}
}
```

`username`、`password` 与 TLS 为必需的连接条件；服务器写为字面 IP 时应设置对应的 `tls.server_name`。`quic_congestion_control` 可选 `bbr`、`cubic`、`reno`；`health_check` 开启健康检查，`idle_timeout` 控制闲置会话保留时间。共享 session 可供多个 stream 使用，网络变化和关闭按会话归属处理。H3 不支持同时开启 uTLS；TLS ALPN 应与所选传输一致。[入站配置](../inbound/trusttunnel.zh.md) · [构建档位](../../BUILD-PROFILES.md)。
