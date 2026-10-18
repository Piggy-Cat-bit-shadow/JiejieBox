---
icon: material/new-box
---

### 结构

```json
{
  "type": "mieru",
  "tag": "mieru-in",

  "listen": "::",
  "listen_port": 8964,
  "listen_port_ranges": ["8965-8999"],
  "transport": "both",
  "users": [
    {
      "name": "alice",
      "password": "password"
    }
  ],
  "mtu": 1400,
  "traffic_pattern": "",
  "user_hint_is_mandatory": false
}
```

### 字段

#### listen

监听地址。默认：`127.0.0.1`。

#### listen_port

主监听端口。`listen_port` 与 `listen_port_ranges` 至少设置一个。

#### listen_port_ranges

额外监听的端口或范围，例如 `["8965-8999"]`。

每一项由 Mieru SDK 展开；每个端口按所选传输协议各创建一个 socket。任一端口
绑定失败会使整个 inbound 启动失败，不会留下半监听的服务器。

#### transport

==必填==

底层传输协议：`tcp`、`udp` 或 `both`。默认：`tcp`。

`both` 会在每个配置的端口上同时提供 TCP 与 UDP，等价于用两个相同端口的
inbound（一个 `tcp`、一个 `udp`）；两种写法均受支持。

#### users

==必填==

用户列表。每项包含 `name`，以及 `password` 与 `hashed_password` 中恰好一个。

`hashed_password` 是 `mita` 保存的 64 位十六进制 SHA-256 形式，便于迁移既有
服务端配置时不再落盘明文。

重复用户名和空凭据会在启动时被拒绝。

#### mtu

Mieru UDP 传输使用的 L2 载荷大小。有效范围：`1280` 到 `1500`。默认：SDK
默认值（1280）。

#### traffic_pattern

由 `mieru export traffic-pattern` 或 `mita describe config` 生成的 base64
流量模式。设置后必须与客户端使用的模式一致。

#### user_hint_is_mandatory

要求客户端在认证前提供匹配已配置用户的用户名提示。默认：`false`。

### 说明

Mieru 没有 TLS 层：协议自身的加密与流量模式就是传输层。不要在前面再套 TLS
inbound 并期望官方客户端互通。

本 inbound 会完整应用监听选项（`bind_interface`、`routing_mark`、
`reuse_addr`、`netns`、TCP keepalive / fast open / multipath、UDP fragment）：
Mieru 的 socket 通过 sing-box 的 listener 工厂创建，而不是 SDK 默认的
`net.ListenConfig`。
