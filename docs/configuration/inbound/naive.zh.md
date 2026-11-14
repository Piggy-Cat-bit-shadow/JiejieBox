!!! quote "sing-box 1.13.0 中的更改"

    :material-plus: [quic_congestion_control](#quic_congestion_control)

### 结构

```json
{
"type": "naive",
"tag": "naive-in",
"network": "udp",

... // 监听字段

"users": [
{
"username": "sekai",
"password": "password"
}
],
"quic_congestion_control": "",
"tls": {}
}
```

### 监听字段

参阅 [监听字段](/zh/configuration/shared/listen/)。

### 字段

#### network

监听的网络协议，`tcp` `udp` 之一。

默认所有。

`network` 决定是否提供 HTTP/3：包含 `udp` 时，QUIC 监听器总是提供 `h3`，与 `tls.alpn` 无关。`tls.alpn` 只影响各传输自己的列表：TCP 侧保留其中的非 `h3` 值，QUIC 侧固定为 `h3`。

#### users

==必填==

Naive 用户。

#### quic_congestion_control

!!! question "Since sing-box 1.13.0"

QUIC 拥塞控制算法。

| 算法             | 描述                 |
|----------------|--------------------|
| `bbr`          | BBR                |
| `cubic`        | CUBIC              |
| `reno`         | New Reno           |

默认使用 `cubic`；`bbr` 与 `reno` 为显式选项。未设置时**显式**选择 CUBIC，不依赖 quic-go 的库默认值（该 fork 的库默认是 Reno）。

#### tls

TLS 配置, 参阅 [TLS](/zh/configuration/shared/tls/#入站)。