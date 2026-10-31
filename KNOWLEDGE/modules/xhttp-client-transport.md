# XHTTP 客户端传输（XHTTP client transport）

## v0.1.9 之后的 VLESS 加密边界（VLESS encryption boundary after v0.1.9）

**VERIFIED_SOURCE，审阅版本 `011f3fdcd99`。** `option/vless.go` 提供可选的 VLESS
`encryption` 配置；空值或 `none` 表示关闭。`protocol/vless/outbound.go` 负责解析并初始化
客户端加密层，再用它包装已经建立的传输连接，供 VLESS 使用。

`protocol/vless/encryption` 实现了 `mlkem768x25519plus` 客户端握手，与 TLS 是独立的。
`client.go` 记录了该实现经 Leadaxe 从 starifly 适配而来，并说明服务端解密部分没有被引入。
`protocol/vless/encryption.go` 为阻塞式握手增加了 context 取消保护。
测试包括 `protocol/vless/encryption_test.go`、
`encryption_guard_test.go`，以及 `encryption` 包的测试。不要仅凭客户端解析或单元测试就推断服务端支持或
线上互通性；部分 ABI 测试在 `-race` 下被排除。

在 `cb71cf80d72`，`dialMode` 和 `normalizeXmux` 仍按下文描述选择模式及成组默认值。
`CloseIdleConnections` 移除空闲池条目；`RetireSuspect` 和 `Close` 移除全部条目，但会延迟
关闭仍在使用的连接。复用边界触发的重拨仍受已启用的 XMUX 熔断退避约束。

校准时，部分默认值、活跃流关闭以及 packet-up 批处理和尾部测试在 Windows、Go 1.26.10
下通过。这些结果不等于与真实对端的互操作测试或性能测量。

> 状态：`VERIFIED_SOURCE`
>
> 对照仓库 `Piggy-Cat-bit-shadow/sing-box`、分支 `testing`、
> HEAD `db8814ced6edf7a266fd2a73b03711332473dad9` 于 2026-10-13 复核。
>
> 本模块记录本 fork 的 **客户端 XHTTP / SplitHTTP 传输配置契约**。
> 它的目的是在无需审计源码的情况下回答“怎么配置一个 XHTTP 节点？”。
> 如果本文档与当前源码或测试冲突，**以源码和测试为准**。

---

## 1. 范围（Scope）

本模块覆盖：

- XHTTP 在 VLESS 出站中的配置位置；
- 当前暴露的全部 `V2RayXHTTPOptions` 字段；
- 四种传输模式及其默认值；
- 会话 / 序号元数据的放置位置；
- packet-up 上行调优；
- X-Padding 行为；
- XMUX 连接复用及其不显然的默认规则；
- HTTP/1.1 / HTTP/2 / HTTP/3 的选择；
- 与 TLS、Reality、Host、path 以及 VLESS XUDP 的相互作用；
- 被接受但被忽略的字段，以及仅服务端的字段；
- 推荐的配置模式与常见故障形态；
- XHTTP 连接复用的 fork 特有生命周期集成。

主要源码锚点：

```text
option/v2ray_xhttp.go
option/v2ray_transport.go
option/vless.go
transport/v2rayxhttp/client.go
transport/v2rayxhttp/meta.go
transport/v2rayxhttp/xmux.go
option/v2ray_transport_xhttp_test.go
protocol/vless/outbound.go
```

`option/v2ray_xhttp.go` 中的注释仍然引用旧的 `SPECS/TASKS/...` 路径。
配置 XHTTP 并不需要这些路径，而且它们在本仓库中可能并不存在。
本模块就是那份缺失的配置指南在知识库中的替代品。

---

## 2. 配置模型：XHTTP 是一种 V2Ray 传输（Configuration model: XHTTP is a V2Ray transport）

XHTTP **不是** 一种独立出站类型。

对于 VLESS，通过出站的 `transport` 对象选择 XHTTP：

```json
{
  "type": "vless",
  "tag": "example-xhttp",
  "server": "example.com",
  "server_port": 443,
  "uuid": "YOUR-UUID",
  "tls": {
    "enabled": true,
    "server_name": "example.com"
  },
  "transport": {
    "type": "xhttp"
  }
}
```

关键区别在于：

```text
outbound.type       = vless
transport.type      = xhttp
```

**不要** 写：

```json
{
  "type": "xhttp"
}
```

也 **不要** 写：

```json
{
  "network": "xhttp"
}
```

`network` 是 sing-box 常规的 TCP/UDP 网络选择器。XHTTP 属于 V2Ray 传输层。

---

## 3. 最小可用配置（Smallest practical configuration）

一个实用的 TLS XHTTP 出站可以写成：

```json
{
  "type": "vless",
  "tag": "XHTTP",
  "server": "example.com",
  "server_port": 443,
  "uuid": "YOUR-UUID",
  "tls": {
    "enabled": true,
    "server_name": "example.com",
    "alpn": [
      "h2"
    ]
  },
  "transport": {
    "type": "xhttp",
    "mode": "auto",
    "path": "/xhttp",
    "x_padding_bytes": "100-1000"
  },
  "packet_encoding": "xudp"
}
```

对于订阅/配置转换，可以遵循这样一条默认原则：

```text
preserve explicitly configured source fields;
do not invent advanced XHTTP tuning;
let omitted fields use the implementation defaults.
```

---

## 4. 模式（Modes）

受支持的 `mode` 值：

| 取值 | 行为 |
|---|---|
| `auto` | Reality → `stream-one`；否则 → `packet-up` |
| `packet-up` | 一条 GET 下载流 + 顺序的 POST 上传数据包 |
| `stream-up` | 一条流式 POST 上传 + 一条独立的 GET 下载流 |
| `stream-one` | 一条双向 HTTP 流；请求体上传，响应体下载 |

如果省略 `mode`，默认值为：

```text
auto
```

未知的模式值会被拒绝。

### 4.1 `auto`（自动模式）

当前实现：

```text
Reality enabled     -> stream-one
Reality not enabled -> packet-up
```

客户端目前没有实现 Xray 那种非对称的 `downloadSettings` 传输选择，
因此这里的决策有意比任何可能的 Xray 拓扑都简单。

### 4.2 `packet-up`（按包上传）

特征：

- 下载是一条长生命周期的 GET 响应；
- 上传拆分为按顺序的请求；
- 每个上传数据包都会获得一个序号；
- 数据包大小与 POST 间延迟可以调优；
- header/cookie 的上行载荷放置只在这里有效；
- `uplink_http_method: "GET"` 只在这里有意义。

### 4.3 `stream-up`（流式上传）

特征：

- 上传使用一个流式请求体；
- 下载使用一条独立的 GET；
- 流式请求体的请求通常携带 `Content-Type: application/grpc`。

### 4.4 `stream-one`（单流双向）

特征：

- 一个请求同时承载两个方向；
- 在传输形态上最接近 HTTP-upgrade 式双向流；
- 流式请求体的请求通常携带 `Content-Type: application/grpc`；
- Reality + `mode: auto` 会解析为此模式。

---

## 5. 完整字段参考（Full field reference）

下面所有 JSON 字段名都是当前 option 层实际使用的键。

### 5.1 基础字段（Basic fields）

| 字段 | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `host` | string | TLS SNI，否则为服务器地址 | HTTP Host 覆盖 |
| `path` | string | 规范化后实际上是 `/` | XHTTP 请求的基础路径 |
| `mode` | string | `auto` | `auto`、`packet-up`、`stream-up`、`stream-one` |
| `headers` | object | 无 | 额外的 HTTP 请求头 |
| `x_padding_bytes` | range string | `100-1000` | 闭区间的随机 X-Padding 字节长度范围 |
| `xmux` | object | 见 XMUX 一节 | HTTP 连接复用策略 |
| `no_grpc_header` | bool | `false` | 抑制流式模式下的 `Content-Type: application/grpc` |

范围字符串接受两种形式：

```text
"100-1000"
```

或单个值：

```text
"4000"
```

它的含义是：

```text
4000-4000
```

如果范围的两端顺序颠倒，解析器会把它们规范化。

---

## 6. Host、SNI 与 path（Host, SNI and path）

### 6.1 `host`（主机字段）

Host 的解析顺序是：

```text
transport.host
    -> tls.server_name
        -> server address
```

因此两者相关，但 **不是同一个设置**：

```text
tls.server_name = TLS SNI / certificate-facing server name
transport.host  = HTTP Host
```

转换节点时，即使两者取值相同，显式保留两者仍然有效。

示例：

```json
"tls": {
  "enabled": true,
  "server_name": "cdn.example.com"
},
"transport": {
  "type": "xhttp",
  "host": "cdn.example.com"
}
```

### 6.2 `path`（路径字段）

客户端保证有一个前导斜杠。

例如：

```text
abc
```

会被规范化为：

```text
/abc
```

除此之外，配置中的尾部斜杠会保留下来。

这在反向代理场景中很重要。例如下面这个 nginx location：

```nginx
location /upload/ {
    ...
}
```

可能会重定向：

```text
/upload -> /upload/
```

XHTTP 下载的 `RoundTrip` 不依赖浏览器式的自动重定向跟随。
因此端点前面出现一个 301/302，就可能表现为拨号失败。

如果服务端路径是 `/upload/`，就精确配置 `/upload/`。

---

## 7. HTTP 版本选择（HTTP version selection）

XHTTP **没有** 这样的 JSON 字段：

```json
"http_version": 2
```

客户端根据 TLS/ALPN/Reality 决定 HTTP 传输。

当前行为：

| 情形 | HTTP 传输 |
|---|---|
| 无 TLS | HTTP/1.1 |
| TLS 且 `alpn: ["http/1.1"]` | HTTP/1.1 |
| TLS 且 `alpn: ["h3"]` | HTTP/3 |
| 普通 TLS，没有显式指定特殊 ALPN | HTTP/2 |
| Reality 要求 H2 路径 | HTTP/2，并按需强制 H2 ALPN |

典型的 H2 配置：

```json
"tls": {
  "enabled": true,
  "server_name": "cdn.example.com",
  "alpn": [
    "h2"
  ]
}
```

典型的 H3 选择：

```json
"tls": {
  "enabled": true,
  "server_name": "cdn.example.com",
  "alpn": [
    "h3"
  ]
}
```

不要发明 `transport.http_version` 键。

---

## 8. 会话与序号放置（Session and sequence placement）

XHTTP 客户端会为每次拨号创建会话元数据。
`packet-up` 还额外带有一个按上传的序号。

### 8.1 会话字段（Session fields）

| 字段 | 接受值 | 默认值 |
|---|---|---|
| `session_placement` | `path`、`query`、`header`、`cookie` | `path` |
| `session_key` | 任意名称 | 取决于放置位置 |
| `session_table` | 预定义表名，或字面 ASCII 字母表 | 省略该对时使用带连字符的 UUID |
| `session_length` | range string | 省略该对时使用带连字符的 UUID |

当 `session_placement` 为以下值时，默认的 key 是：

```text
path   -> no key
header -> X-Session
query  -> x_session
cookie -> x_session
```

### 8.2 序号字段（Sequence fields）

| 字段 | 接受值 | 默认值 |
|---|---|---|
| `seq_placement` | `path`、`query`、`header`、`cookie` | `path` |
| `seq_key` | 任意名称 | 取决于放置位置 |

默认 key：

```text
path   -> no key
header -> X-Seq
query  -> x_seq
cookie -> x_seq
```

序号与 `packet-up` 相关。

### 8.3 自定义会话 ID（Custom session IDs）

`session_table` 与 `session_length` 必须 **一起** 配置。

只设置其中一个会被拒绝。

受支持的预定义 `session_table` 名称是大小写敏感的：

```text
hex
HEX
number
alphabet
Alphabet
ALPHABET
base36
BASE36
Base62
```

也可以提供字面的 ASCII 字母表。

示例：

```json
"session_table": "Base62",
"session_length": "12-16"
```

实现会校验生成的 ID 空间。字母表在最短长度下的空间必须足够大，
以避免不安全的跨客户端会话碰撞风险。

除非兼容性或混淆需求要求，否则两个字段都省略是最简单、
也最安全的配置。

---

## 9. 上行数据字段（Uplink data fields）

这些选项主要影响 `packet-up`。

| 字段 | 取值 / 格式 | 默认值 |
|---|---|---|
| `uplink_data_placement` | `body`、`auto`、`header`、`cookie` | `auto` |
| `uplink_data_key` | string | 取决于放置位置 |
| `uplink_chunk_size` | range string | 取决于放置位置 |
| `uplink_http_method` | HTTP method | `POST` |

### 9.1 `uplink_data_placement`（上行数据放置）

`header` 与 `cookie` 只在以下模式中有效：

```text
packet-up
```

流式模式不接受这两个取值。

默认 key：

```text
header -> X-Data
auto   -> X-Data
cookie -> x_data
body   -> no key
```

### 9.2 `uplink_chunk_size`（上行分块大小）

默认值：

```text
cookie -> 2048-3072
header -> 3000-4000
other  -> sc_max_each_post_bytes
```

实现会强制一个规范化后的最小分块下限：64 个 base64 字符。

### 9.3 `uplink_http_method`（上行 HTTP 方法）

默认值：

```text
POST
```

`GET` 只对 `packet-up` 有效。

如果在非 `packet-up` 模式上配置了 `GET`，当前代码会告警并回退到：

```text
POST
```

而不是拒绝整个配置。

---

## 10. X-Padding 填充（X-Padding）

### 10.1 传统/默认模式（Legacy/default mode）

当：

```json
"x_padding_obfs_mode": false
```

或者省略该字段时，客户端保持与 Xray 兼容的传统 X-Padding 行为。

默认的 padding 大小：

```json
"x_padding_bytes": "100-1000"
```

### 10.2 可配置的混淆模式（Configurable obfuscation mode）

字段：

| 字段 | 接受值 | 默认值 |
|---|---|---|
| `x_padding_obfs_mode` | bool | `false` |
| `x_padding_key` | string | `x_padding` |
| `x_padding_header` | string | `X-Padding` |
| `x_padding_placement` | `cookie`、`header`、`query`、`queryInHeader` | `queryInHeader` |
| `x_padding_method` | `repeat-x`、`tokenish` | `repeat-x` |

示例：

```json
{
  "x_padding_obfs_mode": true,
  "x_padding_placement": "header",
  "x_padding_header": "X-Padding",
  "x_padding_method": "tokenish",
  "x_padding_bytes": "100-1000"
}
```

除非对端/服务端 profile 明确要求这些取值，订阅转换一般应
保留来源设置，而不是发明它们。

---

## 11. `no_grpc_header`（禁用 gRPC 头）

对于流式请求体模式：

```text
stream-one
stream-up
```

客户端通常会设置：

```http
Content-Type: application/grpc
```

在依靠内容类型决定流式/不缓冲行为的 CDN/反向代理上，这个头在运维上
可能很重要。

因此：

```json
"no_grpc_header": true
```

**不是** 一项通用优化。

不要默认启用它。

抑制这个头，会让某些反向代理/CDN 路径上本来正常工作的流式 XHTTP 会话变成
一条持续缓冲或停滞、直至超时的连接。

---

## 12. packet-up 调优（Packet-up tuning）

| 字段 | 格式 | 默认值 |
|---|---|---|
| `sc_max_each_post_bytes` | range string | `1000000-1000000` |
| `sc_min_posts_interval_ms` | range string | `30-30` |

### 12.1 `sc_max_each_post_bytes`（每 POST 字节上限）

约束单个 packet-up 上传 POST 在拆分之前的载荷大小。

示例：

```json
"sc_max_each_post_bytes": "1000000-1000000"
```

### 12.2 `sc_min_posts_interval_ms`（POST 最小间隔）

连续 packet-up 上传 POST 之间的最小延迟。

示例：

```json
"sc_min_posts_interval_ms": "30-30"
```

不要仅仅因为字段被暴露就去调优。它们会改变 packet-up 的请求行为，并可能影响
延迟、突发性与对端兼容性。

### 12.3 POST 节奏，以及为什么响应完成没有被流水线化（Post cadence, and why response completion is not pipelined）

packet-up 上行保持 **同一时刻只有一个 POST 在途**：上一个 POST 的响应完全排空之后，
下一个 POST 才开始；因此在队列永不排空的情况下，节奏是

```text
one POST per max(round trip, sc_min_posts_interval_ms)
```

在 `011f3fdcd99` 处，`packetUplink.sendPacket` 会报告来自
`drainPacketUpResponse` 的读取或关闭错误；响应体停滞或失败的 HTTP 200 不会被计为成功的数据
流，也绝不能清除这条连接的连续失败计数。这个 POST 有自己受限的上下文。
见 `transport/v2rayxhttp/conn.go`；这个源码契约并不构成实时对端结果。

`shouldGatherUpload` 有意跳过前两次写入的聚合，以便完成开场交换（`conn.go:1115-1135`）。在所记录的测试中，从第三个 POST 开始，0 至 200 ms 各个往返时间下的 POST 都聚合到了 `sc_max_each_post_bytes`，即 1,000,000 字节。这是该测试条件下的结果，前两次写入不属于这一聚合结果。

参考实现采用不同的发送方式：它在
`httptrace.WroteRequest` 上启动下一个 POST，并在一个 goroutine 中排空响应，也就是把响应
完成流水线化（`Xray v26.9.30`、`transport/internet/splithttp/dialer.go:495-534`）。因此可以直接
对比两者（packet-up、HTTP/2、单流、同一参考服务器、每个点三次重复、
30 次运行、0 次载荷不匹配）：

| 往返时间 | 0 ms | 30 ms | 50 ms | 100 ms | 200 ms |
|---|---|---|---|---|---|
| fork 上传 | 26.93 MB/s | 24.25 MB/s | 15.57 MB/s | 7.88 MB/s | 3.92 MB/s |
| 参考上传 | 22.84 MB/s | 19.67 MB/s | 14.53 MB/s | 7.80 MB/s | 4.04 MB/s |
| 比值 | 1.18x | 1.23x | 1.07x | 1.01x | 0.97x |
| fork ping-pong p50，512 B | 30.0 ms | 32.2 ms | 52.0 ms | 102.0 ms | 202.0 ms |
| 参考 ping-pong p50 | 30.5 ms | 31.8 ms | 51.8 ms | 101.7 ms | 201.8 ms |

两种实现都落在这一特定扫描区间内（约 27 → 4 MB/s）；在 200 ms 处，所列 fork
结果略低于参考（3.92 对 4.04 MB/s）。这些测量不能证明
两者等价，也不能证明其他路径或工作负载上不存在响应等待代价。把响应
完成流水线化，需要有界的在途完成数，并会改变失败语义；如果要重新考虑这一权衡，请用
新的、有代表性的对比来评估。

证据：不变量（同一时刻至多一个 POST 在途、序号连续递增、
节奏不快于往返时间的一半 —— `TestPacketUpPostCadenceTracksTheRoundTrip`，位于
`transport/v2rayxhttp/packetup_pipeline_probe_test.go`）为 `VERIFIED_UNIT`；往返
扫描是 **历史记录**，来自单台主机的手动测试：通过 loopback 和延迟注入装置增加延迟，
不消耗带宽。该测试无法从 CI 重跑，也没有超过 200 ms 的数据。

溯源：探针提交 `58e3222558c` 紧随 `b5bc2a8de42`。两者都是所考察的
`011f3fdcd99` 测试目标的祖先；这层 Git 关系不会把那次历史手动测量变成
新的测试运行，也不确立当前性能。调优之前，先测量目标与部署。

---

## 13. XMUX 连接复用（XMUX）

XMUX 是 **HTTP 连接复用**，不是一种独立的线上协议。

服务器按会话元数据识别 XHTTP 会话，而不是按具体的池化 HTTP 连接。
因此池化改变的是客户端侧的连接复用/轮转，而不是逻辑上的 XHTTP 会话格式。

它避免为每条流都付出一次全新的 TCP+TLS(+Reality) 建立成本。

### 13.1 字段（Fields）

```json
"xmux": {
  "max_concurrency": "16-32",
  "max_connections": "1-2",
  "c_max_reuse_times": "100",
  "h_max_request_times": "600-900",
  "h_max_reusable_secs": "1800-3000",
  "h_keep_alive_period": 0
}
```

| 字段 | 含义 |
|---|---|
| `max_concurrency` | 共享一条 HTTP 连接的最大活跃流数 |
| `max_connections` | 连接池的连接数上限 |
| `c_max_reuse_times` | 一条连接被退役之前可被发放的次数 |
| `h_max_request_times` | 退役之前允许的 HTTP 请求数 |
| `h_max_reusable_secs` | 可复用的生命周期，单位为秒 |
| `h_keep_alive_period` | HTTP/2 keep-alive/读空闲 ping 周期 |

`h_max_request_times` 统计的是 **HTTP 请求**，而不是逻辑 XHTTP 流。

这一区别在 `packet-up` 中很重要，因为一条逻辑流可以产生许多上传 POST。

### 13.2 范围语法（Range syntax）

XMUX 的范围字段接受本 fork 常规的 XHTTP 范围形式：

```json
"max_concurrency": "16-32"
```

单个整数字符串也是有效的：

```json
"max_concurrency": "16"
```

它表示：

```text
16-16
```

在范围类型支持的地方，option 层也接受兼容的双元素数组形式。

### 13.3 `max_concurrency` 与 `max_connections`（并发与连接数）

它们代表两种可以互相替代的连接池限界策略；文档按互斥处理。

对于普通的手写配置，选择其中一种策略，而不要两者都设置。

解码器/往返测试可能故意同时包含两者，只是为了证明字段能通过解析；
不应把这当成推荐做法照抄。

### 13.4 重要的全有或全无默认规则（The important all-or-nothing default rule）

这条规则很容易被忽略。

如果整个 `xmux` 段是：

```text
absent
```

或者实际上是彻底空的，就会选中与 Xray 兼容的默认值：

```text
max_connections     = 3-3
h_max_request_times = 600-900
h_max_reusable_secs = 1800-3000
```

但只要 **任何一个 XMUX 字段被显式设置**，这一段就算显式编写。

其他未指定的范围字段保持为零，也就是采用它们正常的“显式段”语义
（例如无限制/没有相应的退役上限），而不是静默继承整套默认值。

因此：

```json
"xmux": {
  "max_concurrency": "16-32"
}
```

在行为上与省略 `xmux` 不同。

这是有意为之。

### 13.5 keep-alive 取值（Keep-alive value）

`h_keep_alive_period` 是一个普通整数，而不是范围。

它的 option 契约声明：

```text
0        -> implementation/default behaviour
negative -> disable keep-alive pings
positive -> configured HTTP/2 keep-alive period in seconds
```

---

## 14. fork 特有的 XMUX 生命周期行为（Fork-specific XMUX lifecycle behaviour）

本 fork 把 XHTTP 连接复用与运行时生命周期集成在一起。

XHTTP 客户端实现了：

```text
adapter.IdleConnectionKeeper
adapter.ReuseSuspect
```

重要后果：

### 内存裁剪（Memory trim）

`CloseIdleConnections()` 只退役不承载活跃流的池化连接。

代码有意避免让内存裁剪变成对活跃工作的不必要重连触发。

### 复用边界 / 可疑连接（Reuse boundary / suspect connections）

`RetireSuspect()` 阻止旧的或可疑的池化连接接受新流。

已经在承载实时流量的连接会被排空，而不是立即销毁。

### 完全关闭 / 网络变更（Full close / network change）

`Close()` 退役整个 XHTTP 池。

既有的使用中拆除按连接池所有权规则推迟，而不是在不安全的时刻
盲目终止一条共享传输。

### 失败熔断（Failure breaker）

复用边界后的重拨仍受 XMUX 失败熔断约束：连续三次失败触发熔断（`xmuxBreakerThreshold = 3`），退避时间从 100 ms 增长到 3 s（`xmux.go:56-69`、`:140-156`）。`noteBreakerTrip`（`:312`）记录熔断，一次成功复用会重置退避（`:327`）。熔断与连接池的裁剪、退役和关闭生命周期独立；`RetireSuspect` 之后的重拨本身不会重置熔断。

### 没有定时器驱动的预热（No timer-driven prewarm）

`SetKeepIdleConnections(true)` 不会主动拨号/预热连接。

这是有意为之：生命周期策略不应仅仅为了让连接池保持温暖就发起网络拨号。

这些行为是 fork 特有的运维上下文，在改动
`transport/v2rayxhttp` 时应予以保留。

---

## 15. 被接受但被忽略 / 与客户端无关的字段（Accepted but ignored / client-irrelevant fields）

option 层为了兼容而接受若干字段，尽管客户端并不使用它们。

| 字段 | 当前客户端行为 |
|---|---|
| `sc_max_concurrent_posts` | 历史兼容字段；被接受但被忽略 |
| `server_max_header_bytes` | 仅服务端；客户端忽略 |
| `no_sse_header` | 仅服务端；客户端忽略 |
| `sc_max_buffered_posts` | 仅服务端；客户端忽略 |
| `sc_stream_up_server_secs` | 服务端行为；客户端不配置它 |

不能把字段的存在解读为客户端真的实现了对应
服务端特性的证据。

---

## 16. VLESS UDP / XUDP 交互（VLESS UDP / XUDP interaction）

XHTTP 是 VLESS 的传输。VLESS 的数据包处理是单独配置的。

对于本 fork，一个显式的订阅风格设置：

```json
"packet_encoding": "xudp"
```

会清晰映射到 VLESS 的 XUDP 路径。

转换来源订阅时，最好保留显式的 `xudp`：它记录了
来源节点预期的数据包行为，即使本 fork 可能有兼容的默认值。

没有 Mihomo 那样的：

```json
"udp": true
```

这类布尔值在 sing-box 出站中并不需要。

同样，不要转换：

```yaml
network: xhttp
```

为 sing-box 的 `network`。

如果希望获得常规的 TCP+UDP 可用性，通常应让 sing-box 出站的网络选择器
保持未设置。

---

## 17. Mihomo / Clash 风格的 XHTTP 映射（Mihomo / Clash-style XHTTP mapping）

一个常见的来源节点：

```yaml
- name: 🇺🇸 美国｜Vless XHTTP TLS
  type: vless
  server: example.com
  port: 443
  uuid: YOUR-UUID
  network: xhttp
  tls: true
  udp: true
  servername: cdn.example.com
  client-fingerprint: chrome
  alpn:
    - h2
  encryption: none
  xhttp-opts:
    path: /example
    host: cdn.example.com
    mode: stream-one
    reuse-settings:
      max-concurrency: 16-32
  packet-encoding: xudp
```

在概念上映射为：

```json
{
  "type": "vless",
  "tag": "🇺🇸 美国｜Vless XHTTP TLS",
  "server": "example.com",
  "server_port": 443,
  "uuid": "YOUR-UUID",
  "encryption": "none",
  "tls": {
    "enabled": true,
    "server_name": "cdn.example.com",
    "alpn": [
      "h2"
    ],
    "utls": {
      "enabled": true,
      "fingerprint": "chrome"
    }
  },
  "transport": {
    "type": "xhttp",
    "path": "/example",
    "host": "cdn.example.com",
    "mode": "stream-one",
    "xmux": {
      "max_concurrency": "16-32"
    }
  },
  "packet_encoding": "xudp"
}
```

映射关系：

| 来源概念 | sing-box fork |
|---|---|
| `name` | `tag` |
| `type: vless` | `type: vless` |
| `server` | `server` |
| `port` | `server_port` |
| `uuid` | `uuid` |
| `network: xhttp` | `transport.type: xhttp` |
| `tls: true` | `tls.enabled: true` |
| `servername` | `tls.server_name` |
| `client-fingerprint` | `tls.utls.fingerprint` + `tls.utls.enabled` |
| `alpn` | `tls.alpn` |
| `encryption` | `encryption` |
| `xhttp-opts.path` | `transport.path` |
| `xhttp-opts.host` | `transport.host` |
| `xhttp-opts.mode` | `transport.mode` |
| `reuse-settings.max-concurrency` | `transport.xmux.max_concurrency` |
| `packet-encoding: xudp` | `packet_encoding: xudp` |
| `udp: true` | 不需要等价的布尔值 |
| `ip-version: dual` | 不要发明 `ip_version`；解析器/网络族策略是分开的 |

---

## 18. 推荐 profile（Recommended profiles）

这些是 **配置模板**，不是硬性要求。

### 18.1 普通 TLS、显式 H2、stream-one（Normal TLS, explicit H2, stream-one）

```json
{
  "type": "vless",
  "tag": "VLESS XHTTP TLS",
  "server": "example.com",
  "server_port": 443,
  "uuid": "YOUR-UUID",
  "encryption": "none",
  "tls": {
    "enabled": true,
    "server_name": "cdn.example.com",
    "alpn": [
      "h2"
    ],
    "utls": {
      "enabled": true,
      "fingerprint": "chrome"
    }
  },
  "transport": {
    "type": "xhttp",
    "host": "cdn.example.com",
    "path": "/xhttp",
    "mode": "stream-one",
    "x_padding_bytes": "100-1000"
  },
  "packet_encoding": "xudp"
}
```

### 18.2 使用 `auto` 的 Reality（Reality with `auto`）

```json
{
  "type": "vless",
  "tag": "VLESS Reality XHTTP",
  "server": "SERVER_IP_OR_DOMAIN",
  "server_port": 443,
  "uuid": "YOUR-UUID",
  "tls": {
    "enabled": true,
    "server_name": "REALITY_SNI",
    "utls": {
      "enabled": true,
      "fingerprint": "chrome"
    },
    "reality": {
      "enabled": true,
      "public_key": "YOUR_PUBLIC_KEY",
      "short_id": "YOUR_SHORT_ID"
    }
  },
  "transport": {
    "type": "xhttp",
    "path": "/xhttp",
    "mode": "auto",
    "x_padding_bytes": "100-1000"
  },
  "packet_encoding": "xudp"
}
```

在当前模式选择下：

```text
Reality + auto -> stream-one
```

### 18.3 保留来源中显式的 XMUX 并发设置（Preserve an explicit source XMUX concurrency setting）

```json
"transport": {
  "type": "xhttp",
  "path": "/xhttp",
  "mode": "stream-one",
  "xmux": {
    "max_concurrency": "16-32"
  }
}
```

除非来源 profile 或有意的调优需要，否则不要静默添加其他 XMUX 限制。

---

## 19. 常见错误（Common mistakes）

### 错误 1 —— 把 XHTTP 当作一种出站（Mistake 1 — treating XHTTP as an outbound）

错误：

```json
"type": "xhttp"
```

正确：

```json
"type": "vless",
"transport": {
  "type": "xhttp"
}
```

### 错误 2 —— 写 `network: "xhttp"`（Mistake 2）

错误：

```json
"network": "xhttp"
```

正确：

```json
"transport": {
  "type": "xhttp"
}
```

### 错误 3 —— 发明 `http_version`（Mistake 3）

错误：

```json
"transport": {
  "type": "xhttp",
  "http_version": 2
}
```

正确：通过 TLS/ALPN 选择想要的 HTTP 路径。

### 错误 4 —— 假定 Host 与 SNI 是别名（Mistake 4 — assuming Host and SNI are aliases）

它们服务于不同的层。

```text
transport.host  -> HTTP
tls.server_name -> TLS
```

### 错误 5 —— 去掉必需的尾部斜杠（Mistake 5 — stripping a required trailing slash）

如果反向代理是为 `/upload/` 配置的，就保留 `/upload/`。

### 错误 6 —— 把 `no_grpc_header: true` 当作优化来设置（Mistake 6）

在缓冲式反向代理/CDN 前面，这会破坏流式行为。

### 错误 7 —— 设置一个 XMUX 字段，并假定所有被省略的 XMUX 默认值仍然生效（Mistake 7）

它们并不生效。显式的 XMUX 遵循上文所述的全有或全无规则。

### 错误 8 —— 调优每一个被暴露的字段（Mistake 8 — tuning every exposed field）

大多数 XHTTP 字段为兼容性与高级传输塑形而存在。
普通节点不需要指定每一个字段。

---

## 20. 配置解码器覆盖（Configuration decoder coverage）

XHTTP 必须仍然能从真实的 JSON 配置文件到达，而不只是从直接的 Go 构造。

`option/v2ray_transport_xhttp_test.go` 作为这一契约的回归防线而存在。

V2Ray 传输 option 分发器必须保留：

```text
type = xhttp
```

并通过 JSON 解码/编码把它映射到 `V2RayXHTTPOptions`。

改动 option 分发、schema 生成、别名或构建标签时，请保留这个回归类别：
一个能编译、却无法从配置文件被选中的实现，并不是可用的 XHTTP
支持。

---

## 21. 维护规则（Maintenance rules）

当 XHTTP 代码变化时，至少复检：

```text
option/v2ray_xhttp.go
option/v2ray_transport.go
transport/v2rayxhttp/client.go
transport/v2rayxhttp/meta.go
transport/v2rayxhttp/xmux.go
protocol/vless/outbound.go
option/v2ray_transport_xhttp_test.go
```

当以下任何一项变化时，更新本模块：

- 增加、删除或重命名了一个 JSON 字段；
- 某个默认值改变；
- 某个接受值改变；
- 模式解析改变；
- HTTP 版本选择改变；
- 元数据放置语义改变；
- XMUX 默认值/轮转语义改变；
- 某个兼容字段不再被忽略；
- VLESS/XUDP 集成改变；
- 生命周期复用/退役语义改变。

不要把过时的 Xray 或 sing-box-lx 文档凌驾于当前源码行为之上。

事实来源的优先顺序：

```text
current source and tests
    > git history/diffs
        > this module
            > external/reference documentation
```

---

## 22. 快速参考（Quick reference）

对于一个普通转换后的 XHTTP 节点，通常按以下顺序考虑：

```text
1. VLESS server / UUID
2. TLS / Reality
3. transport.type = xhttp
4. path
5. host if HTTP Host must be explicit
6. mode
7. ALPN if an HTTP version must be forced
8. packet_encoding = xudp if the source explicitly uses XUDP
9. XMUX only when the source or an intentional tuning profile specifies it
10. leave advanced placement/padding/tuning fields alone unless required
```

紧凑的常规形式：

```json
{
  "type": "vless",
  "server": "example.com",
  "server_port": 443,
  "uuid": "YOUR-UUID",
  "tls": {
    "enabled": true,
    "server_name": "example.com",
    "alpn": [
      "h2"
    ]
  },
  "transport": {
    "type": "xhttp",
    "path": "/xhttp",
    "mode": "stream-one"
  },
  "packet_encoding": "xudp"
}
```

这就是起点。高级 XHTTP 选项应当在某条对端、代理路径或实测行为
需要时才添加 —— 而不是仅仅因为字段存在。
