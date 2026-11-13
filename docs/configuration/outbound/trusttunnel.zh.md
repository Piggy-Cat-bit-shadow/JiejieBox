# TrustTunnel 出站

`type: "trusttunnel"` 原生客户端支持 TLS/TCP 上的 H2 与 QUIC/UDP 上的 H3，承载 TCP stream 和 UDP association。需要带 `with_trusttunnel` 的内核。`quic: true` 选择 H3；省略或设为 `false` 使用 H2；`quic: true` 时可选 `quic_fallback_to_h2` 让 H3 建立失败后自动改用 H2。出站的 `network` 限制**隧道内业务** TCP/UDP，含义与入站不同。

```json
{
  "type": "trusttunnel",
  "tag": "tt-out",
  "server": "proxy.example.com",
  "server_port": 443,
  "username": "user",
  "password": "替换为密码",
  "quic": true,
  "quic_fallback_to_h2": true,
  "quic_congestion_control": "bbr",
  "health_check": true,
  "idle_timeout": "5m",
  "network": ["tcp", "udp"],
  "tls": {"enabled": true, "server_name": "proxy.example.com"}
}
```

`username`、`password` 与 TLS 为必需的连接条件；服务器写为字面 IP 时应设置对应的 `tls.server_name`。`quic_congestion_control` 可选 `bbr`、`cubic`、`reno`；`health_check` 开启健康检查，`idle_timeout` 控制闲置会话保留时间。共享 session 可供多个 stream 使用，网络变化和关闭按会话归属处理。H3 不支持同时开启 uTLS；TLS ALPN 应与所选传输一致。

**`quic_fallback_to_h2`**（默认关闭，需同时 `quic: true`）：在新会话建立阶段，先尝试 H3，最多用 3 秒（父级剩余时间更短时以父级为准）；若这次尝试失败或超时，则在同一节点上改用 H2 建立这个会话，并在随后 30 秒内让新会话直接走 H2，不再重复支付 QUIC 超时。冷却按会话创建计数、到期后由真实新需求触发一次新的 H3 尝试（不会定时探测）；网络变化会清除该判断。回退只发生在新会话：已建立的 stream / UDP association 不迁移、不重放业务字节。认证、证书校验、目标拒绝与取消都不被回退绕过。该功能要求服务端在同一节点上同时提供 QUIC 与 TCP/H2 入口；未开 `with_quic` 的构建、或 `tls.alpn` 只写了 `h3` 时，启动即报错。[入站配置](../inbound/trusttunnel.zh.md) · [构建档位](../../BUILD-PROFILES.md)。
