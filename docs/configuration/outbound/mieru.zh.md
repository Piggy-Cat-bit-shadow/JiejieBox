---
icon: material/new-box
---

### 结构

```json
{
  "type": "mieru",
  "tag": "mieru-out",

  "server": "127.0.0.1",
  "server_port": 8964,
  "server_ports": ["8965-8999"],
  "transport": "tcp",
  "username": "alice",
  "password": "password",
  "multiplexing": "low",
  "handshake_mode": "default",
  "mtu": 1400,
  "traffic_pattern": "",

  ... // 拨号字段
}
```

### 字段

#### server

==必填==

服务器地址。

#### server_port

主服务器端口。`server_port` 与 `server_ports` 至少设置一个。

#### server_ports

同一服务器的额外端口或范围，例如 `["8965-8999"]`。

#### transport

底层传输协议：`tcp`、`udp` 或 `both`。默认：`tcp`。

`both` 会在每个配置端口上同时注册两种协议，由 Mieru 调度器选择。`udp` 使用
Mieru 的分组 underlay；`tcp` 会把包括 UDP associate 在内的全部流量放进流中。

#### username

==必填==

用户名。

#### password

明文密码。`password` 与 `hashed_password` 必须且只能设置一个。

#### hashed_password

`mita`/`mieru` 保存的 64 位十六进制 SHA-256 形式。

#### multiplexing

Mieru 自带的多路复用级别：`default`、`off`、`low`、`middle` 或 `high`。
默认：SDK 默认值（等价于 `low`）。

这是 **Mieru 的**多路复用，不是 sing-box 的。不要在本 outbound 之上再启用
sing-box 多路复用：流量会被复用两次，并叠加两层独立的流控与缓冲。

#### handshake_mode

`default`、`standard` 或 `no_wait`。默认：`default`（标准）。

`no_wait` 会把代理请求与第一次应用写入一起发送。它不是真正的零 RTT：第一次
读取前仍然要等待对端响应。

#### mtu

Mieru UDP 传输使用的 L2 载荷大小。有效范围：`1280` 到 `1500`。默认：SDK
默认值（1280）。

#### traffic_pattern

由 `mieru export traffic-pattern` 生成的 base64 流量模式。设置后必须与服务端
一致。

### 拨号字段

见 [拨号字段](/configuration/shared/dial/)。

服务器域名由本 fork 的 dialer 解析，遵循 `domain_resolver`、
`route.default_domain_resolver`、网络策略与双栈策略；不会在 SDK 内再次解析。
