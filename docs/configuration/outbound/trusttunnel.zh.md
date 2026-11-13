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

## H3 → H2 回退

从 **0.1.46** 起支持 `quic_fallback_to_h2`，默认 `false`。上面的示例已开启回退；保留 `quic: true` 并删除回退字段即可恢复严格 H3。

| 配置 | 会话建立方式 |
| --- | --- |
| `quic: false` 或省略，回退关闭 | 直接使用 H2 |
| `quic: true`，回退关闭 | 只使用 H3，失败直接返回 |
| `quic: true`、`quic_fallback_to_h2: true` | 优先 H3，符合条件时回退 H2 |
| `quic: false`、`quic_fallback_to_h2: true` | 配置错误，启动时拒绝 |

### 服务端与 TLS 配置

- 同一 `server` / `server_port` 须同时提供 UDP/H3 和 TCP/H2，使用相同节点身份与凭据。使用本内核入站时，配置 `network: ["tcp", "udp"]`，并放行对应端口的 TCP 和 UDP；完整示例见[入站配置](../inbound/trusttunnel.zh.md)。客户端开启回退不会替服务端开启 H2。
- `tls.alpn` 可省略，内核自动配置 H3/H2；显式配置时必须同时包含 `"h3"` 和 `"h2"`，例如 `"alpn": ["h3", "h2"]`。仅写 `h3` 会在启动时拒绝。
- 构建须带 `with_trusttunnel` 和 `with_quic`；不带 QUIC 的内核不会把此配置静默当作 H2。`quic: true` 仍不能与 uTLS 同时开启。参见[构建档位](../../BUILD-PROFILES.md)。

### 尝试、冷却与恢复

1. 新会话先尝试 H3，单次预算最多 **3 秒**。共享创建自身的剩余期限更短时以它为准；回退不会延长上层期限，也不保证剩余时间足够建立 H2。
2. UDP 拨号、QUIC 传输建立失败或上述尝试超时，且创建尚未取消时，改用同一节点的 H2。H2 也失败时保留两条路径的错误，便于定位原因。
3. 可回退的 H3 失败开启 **30 秒时间窗口**，期间需要新建会话时直接使用 H2；这不是请求次数或会话次数计数。3 秒与 30 秒是当前实现常量，没有对应的用户配置字段。
4. 到期后的真实新业务需求会让回退产生的 H2 会话停止接收新业务，并尝试建立 H3。旧会话上的 stream / UDP association 保留至自身结束，不迁移、不重放。没有新需求时不定时探测；到期也不会主动中断已有业务。
5. 网络变化清除旧判断。失败与成功结果都核对网络代次，旧网络的迟到结果不能覆盖新网络状态；判断保存在各客户端实例中，不是全局禁用 H3。

### 不会触发回退的情况

认证失败、证书不可信、主机名不匹配、证书失效和对端 TLS alert 不会作为“UDP 不可用”处理。业务 CONNECT 的目标拒绝发生在会话建立之后，也不会触发本开关；创建被取消或其整体期限耗尽时不会继续拨 H2。回退不能绕过认证或 TLS 校验，也不是对任意业务错误的自动重试。

排查时可开启 debug 日志，查找 `HTTP/3 attempt`、`HTTP/3 is cordoned` 和 `retired to retry HTTP/3`；先确认服务端 TCP/H2 可达、ALPN 与证书正确，再区分 QUIC 传输失败和明确拒绝。已有 H2 回退会话不代表配置永久变成 H2。
