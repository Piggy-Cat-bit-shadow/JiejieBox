# 原生 Naive 与 HTTP 传输栈（Native Naive and the HTTP transport stack）

## v0.1.9 之后的 Naive 出站 H3 回退（Naive outbound H3 fallback after v0.1.9）

**VERIFIED_SOURCE**，测试目标 `011f3fdcd99`：`protocol/naive/outbound.go` 由
`with_naive_outbound` 作为门禁。其 `quic_fallback_to_h2` 选项是 opt-in 的，并要求 `quic: true`；
否则构造时会拒绝该组合。`protocol/naive/h3_fallback.go` 会把符合条件的
H3 传输失败在 H2 上重试，并保留一个按出站计、可被网络重置的 30 秒失败窗口；
尝试次序可防止较旧的失败覆盖较新的结果。HTTP 状态响应与
调用方取消都不构成 H3 传输失败。重试包含 CONNECT
握手，并预留重试预算。一个内部有界、但已超时的探测是含义不明的：那种
流可以在 H2 上重试，但它不会武装失败窗口。Cronet 出站会拒绝
证书 pin 选项与 uTLS，而不是静默地把它们当作受支持。见
`option/naive.go` 以及回退相关测试。这
并不表示与每一个服务器都能互通。

在 pin `cb71cf80d72` 处，本地干净的 Cronet fork 源码 `c9c0e6ec8845` 声明了
`NaiveClient.Start() (err error)`，并在其清理路径上把恢复到的启动期 panic 赋给该返回值。
这取代了历史审计中关于未命名返回值的发现；本次并未执行原生启动期 panic 或
目标设备上的运行，而且清理期间的 panic 是另一种情形。

**范围：** `protocol/naive/*`（入站 + 受 tag 门禁的出站）、`transport/http/*`（本 fork 最大的一处
单点新增）、`common/httpclient/*`、`route/packet_destination_guard.go`。

**溯源 —— 早先的知识库在这一点上错了。** 在上游祖先 `e4b7e692` 处，`protocol/naive` 有 **4 个文件**；
而在 `a267e99` 处它有 **31 个**，跨 **64 个 fork 提交**。本 fork 自己的
`docs/FORK-DIFF.md` 第 15 行说得很直白：*“**Native Naive** —— 一个 NaiveProxy 服务端入站，带有
UoT、padding、masquerade 与预认证阶段的资源控制。”* 这是一个 **fork 子系统**，而不是
一次依赖升级。

`go.mod` 为证书管理引入了 `caddyserver/certmagic` 与 `caddyserver/zerossl`；
这并不意味着 Naive 服务器是用 Caddy 实现的。HTTP 引擎是
`golang.org/x/net/http2` + `.../http2/h2c`
（`protocol/naive/inbound.go:32-33`），以及本 fork 的 `github.com/sagernet/quic-go`（在
`go.mod` 的 replace 中替换）。客户端集成位于 `protocol/naive/outbound.go`，而其
Cronet 引擎位于独立的 `cronet-go` 依赖中。服务端 codec 测试通过本身并不能
说明客户端的情况。fork 特有的引擎控制属于客户端契约的一部分：
`insecure_concurrency` / `insecure_concurrency_single_engine`（`option/naive.go:100-118,134`；
`protocol/naive/outbound.go:239,256`）选择共享的还是按网络的 Cronet 引擎，并限制
并行的不安全握手数；而 `quic_congestion_control` / `quic_session_receive_window`
（`option/naive.go:152-153`）会被传给 QUIC 引擎。这些是客户端引擎的旋钮，而不是服务端
语义。

**在改动以下内容时加载本模块：** Naive 入站/出站、HTTP/2 或 HTTP/3 的流控上限、
`transport/http`、`http_clients`、UoT 数据路径，或 packet-destination guard。

---

## 1. 两套实现，同一个协议（Two implementations, one protocol）

| 侧 | 位置 | 备注 |
|---|---|---|
| **服务端入站** | 本仓库的 `protocol/naive/` | 作为生产协议注册在 **最小服务器注册表** 中（`include/registry.go:71 naive.RegisterInbound`）—— 入站 **总是** 被注册 |
| **客户端出站** | 本 fork 的 `cronet-go` | 由 `//go:build with_naive_outbound`（`include/naive_outbound.go:1`）选择，与之相对的是在 `include/registry.go:103` 处接线的 stub（`include/naive_outbound_stub.go`） |

> **`DEFAULT_BUILD_TAGS_OTHERS` 故意省略了 `with_naive_outbound`。** 因此 Linux 服务端
> profile 包含 Naive **入站，但不含 Naive 出站**。在当前 Cronet fork pin 处，
> `cronet-go/all` 既有 Apple 文件，也有 Linux 与 Windows 的平台文件；Windows 文件
> 需要 `with_purego`，而 Windows profile 会添加它。一次使用精确 pin 的 Cronet
> 源码与 purego 的本地测试通过了，但这并不证明原生库加载或构建产物支持。

参考实现的边界：两个自定义 codec 是相互独立的，而两者彼此一致
并不能替代与参考实现的对比。两个仓库共享的机器可读 padding 向量
位于 `test/jiejie/reference/naive_padding_vectors.json`。

---

## 2. 服务端入站行为（Server inbound behaviour）

### 认证与 CONNECT（Authentication and CONNECT）

用户使用配置的凭据进行认证。通过认证的 CONNECT 会建立隧道；
认证失败与未认证的流量在配置了 **masquerade** 时由该路径处理。
顺序是 **解析 → 认证 → 隧道**。

原版 NaiveProxy 路径是 TCP；UoT 是单独的扩展。与原版
客户端的实际互通性并未由本次校准中的本地 codec 测试建立；见证据表。

### UoT v1/v2 —— 关键的那个区别（UoT v1/v2 — the distinction that matters）

| 模式 | 目的地如何承载 |
|---|---|
| **v2 Connect 模式** | 会话头携带一个 **固定目的地**，在会话建立时校验 |
| **v1 与 v2 非 Connect** | 会话头中的地址只是一个 **会话标识符**；**后续每个数据报各自携带自己的目的地** |

对 magic 地址发起并通过认证的 CONNECT 会选中 UoT 解码器 ——
v2 用 `sp.v2.udp-over-tcp.arpa`，v1 用 `sp.udp-over-tcp.arpa`。**magic 地址永远不是真实的
目的地。**

**两种地址编码并存，而且绝不能混用：** v2 请求头使用 SOCKS5
序列化器，而按数据报的地址使用 UoT 地址解析器。把两者混淆会得到
`unknown address family: 0`。v2 请求头是在隧道 **内部** 传输的，因此当 padding 已被
协商启用时，它本身必须被包在 padding 帧之内。

解包后的数据报以一个数据包连接的形式交给 router，并保留已认证的用户名，
因此常规的路由、DNS、出站选择与访问控制都适用。

> 早先“v2 非 Connect 是 P0 产品缺陷”的结论是一个 **测试夹具错误**，而不是
> 产品缺陷。传输矩阵由
> `test/jiejie/jiejie_naive_uot_transport_matrix_test.go` 固定。

### Padding 与分帧（Padding and framing）

```text
padding FIRST, then the payload budget:
    payload_budget = 65536 - 3 - padding
    3 + payload + padding <= 65536
    writerMTU = 65278, front headroom = 3, rear headroom = 255
```

帧布局：`[2-byte big-endian data size][1-byte padding size][data][zero padding]`。**两个尺寸
都有固定宽度，因此恶意帧无法驱动大额分配。**

不得放宽的规则：

* **分帧仅由请求的 `Padding` 头启用。** 没有该头的 CONNECT 是一个有效的普通
  HTTP 代理请求，并获得逐字节的 I/O。（*响应* 的 `Padding` 头对每个
  已认证 CONNECT 都会发送，与此无关 —— 这是另一件独立的事。）
* Padding 作用于每个方向的 **前 8 帧**，之后被完全丢弃。
* 超过 `maxFrameSize - 3 - 255` 时就跳过就地分帧。**padding 范围 0..255
  被有意地永不钳制到缓冲区。**
* **短写就是失败**：`Write` 返回的字节数少于请求量且错误为 nil 时，
  会被报告为 `io.ErrShortWrite`，绝不会被静默截断。帧计数器 **仅在
  整个帧写完之后** 才前进，因此服务端与对端的分帧不会出现分歧。
* 一个只带 padding 而不带数据的帧会被消费，读取继续，因此错误为 nil 的零字节
  读取绝不会被交给调用方。
* 数据路径失败会返回错误，而不是在正常失败模式下 panic。

**HTTP/1 CONNECT 的载荷是原始字节：即使存在 `Padding` 请求头，它也不分帧。**
该头只在 HTTP/2 与 HTTP/3 上被查询。在 HTTP/2 上，隧道写入方的 flush
经由 response controller 进行，失败的 flush 会作为错误传播，并拆除该隧道，
而不是被静默丢弃。

由 `protocol/naive/audit_write_contract_test.go` 固定。

> **一次历史上的“panic 修复”本身就是一次协议变更**：一个测试误用了 `buf.Resize(start, end)`，
> 而第二个参数是 **长度**，不是结束偏移。该修复被回退
> （`3c6e0aad3` → `fe5789539`），并恢复了完整的 0..255 padding 范围。

### Masquerade 伪装（Masquerade）

`masquerade` 服务那些 **不是** 已认证 Naive 代理请求的请求 —— 普通浏览器
流量、探测，以及无凭据或凭据错误的代理尝试。它使用与 Hysteria2 和 HTTP 入站 masquerade
相同的 schema 与语义，因此一个正常网站可以共享该端口。

* **只是一个 Web 响应。** 它永远不能建立代理隧道：隧道路径首先要求
  认证成功，而 masquerade handler 无法访问代理或 UoT 数据路径。
* 未设置意味着上游行为：代理认证挑战，或一个 bad-request 状态码。
* 它并不会让服务变得不可探测。

当前的连接数限流器在 `limitedListener.Accept` 中拒绝超限的对端，做法是在
HTTP 请求处理之前关闭已接受的 socket。它在那里不生成 HTTP 429 响应；
旧的 429 诱饵说法已经过时。无论是 masquerade，还是某种特定的拒绝形态，都不能证明
该服务无法被指纹识别。见 `server_limited_listener.go` 与 `server_limiter_test.go`。

### 预认证资源控制（`server_limits`）（Pre-authentication resource controls）

`server_limits` 可以在认证之前约束资源；连接计数也包含
已认证的连接。每个字段都是可选的，省略整个块会让这些显式
限制保持关闭。其他传输层、运行时或操作系统层面的限制仍可能生效。

| 字段 | 约束 |
|---|---|
| `max_connections` | 该入站上并发连接总数，无论是否认证 —— 唯一覆盖整个 listener 的约束 |
| `max_connections_per_ip` | 来自单个源地址的并发连接数。**键是真实的传输对端，绝不是转发头** —— 一个能自报源地址的客户端可以轻易绕过按 IP 的限制 |
| `header_timeout` | 握手完成之后，一个连接交付 **完整** 请求所允许花费的时间；仅限请求阶段 |
| `max_tracked_ips` | 被跟踪源地址的上限 |

有意的省略，每一项都有理由：

* **这里没有握手超时。** `tls.handshake_timeout` 已经存在，覆盖在握手 *期间*
  停滞的对端；一旦握手成功，该截止时间就被取消，因此一个
  完成握手、随后沉默的对端改由 `header_timeout` 覆盖。为同一阶段再设
  第二个控制，会让运维面对两个可能互相矛盾的重叠字段。
* **这里没有空闲超时。** `HTTP2Options.IdleTimeout` 已经提供了该功能，并接到
  `http2.Server.IdleTimeout`。
* **已建立的隧道绝不受 `header_timeout` 约束。** 一条合法空闲超过
  头部超时的隧道 —— 长连接经常如此 —— 不得被拆除。
* `server_limits.idle_timeout` 会被解析，但 **从未被读取**；它已被移除（`724035c76`），并把
  理由记录下来，以免再次引入。

计数发生在 **accept** 处，包裹最外层的 listener，因此它既覆盖一个从未
完成握手的对端，也覆盖一个从不发送请求的对端。**无论连接以何种方式
结束（包括 TLS 握手失败），槽位都恰好释放一次。**限流器的被跟踪 IP 映射是
有界的，按摊还节奏清扫，并且 **在上限处失败即拒绝（fail-closed）**，而不是驱逐某个被跟踪的
源地址。

`HTTP2Options` 约束 HTTP/2 服务端：`max_concurrent_streams`、`idle_timeout`、
`stream_receive_window`、`connection_receive_window`。所有字段都不设置时，保持上游
`net/http` 与 `x/net/http2` 的默认值。**接收窗口是服务端的准入窗口，而不是
客户端的** —— 它们限制的是一个对端 *朝向本服务器* 可以有多少在途数据。那些在收窄时
会发生回绕的取值会被拒绝。`http2InitialWindowSize = 65535`（`inbound.go:458`），并附有一张
最小可接受值的表（`:409-418`）；`:350-364` 处的注释记录了该入站先前
使用裸的 `&http2.Server{}`，因此每一项约束都是无界的。

### HTTP/3 可用性 —— 致命与非致命（HTTP/3 availability — fatal vs non-fatal）

HTTP/3 由一个 **注入的构造函数** `ConfigureHTTP3ListenerFunc`（`inbound.go:37`）提供服务，
QUIC 标签开启时它由 `protocol/naive/quic/inbound_init.go:177` 安装，关闭时则由
`include/quic_stub.go:48` 处一个 **失败的 stub** 安装。

这种极性正是关键所在：它让 `decideHTTP3Availability(constructorPresent bool, network []string)`
（`inbound.go:316-324`）与 `decideHTTP3AvailabilityForOptions`（`:334`）能够区分三种情况：

| 条件 | 结果 |
|---|---|
| 存在构造函数 | `http3Available` |
| 无构造函数，**且入站同时还有 TCP** | `http3UnavailableNonFatal` —— *"A missing optional transport must not take down TCP CONNECT"*（中文：缺失的可选传输不得让 TCP CONNECT 一起失效）（`:220-223`） |
| 无构造函数，**且 H3 是唯一传输** | `http3UnavailableFatal`（`:212-219`） |

**stub 安装的是一个失败的构造函数，而不是把该字段留为 nil**，这样“不存在”与
“存在但已损坏”就是两种不同的状态。把 stub 变成 nil 会把它们合并。

### 保留头与 ALPN（Reserved headers and ALPN）

在 HTTP/2 与 HTTP/3 的 CONNECT 上，`:scheme` 与 `:path` 会被拒绝（`inbound.go:507-521`）。保留的额外
头在 `protocol/naive/outbound.go:418` 处被拒绝。ALPN 由 `http2.NextProtoTLS` 加上
可选的 `"h3"` 构建（`inbound.go:256-294`，`:245` 处 `http3ALPN = "h3"`）。

---

## 3. 目标 ACL / SSRF 边界 —— 风险最高的区域（Target ACL / SSRF boundary）

客户端选择代理目的地，因此目的地必须受到约束。**须遵守的
规则：先解析目标，再检查解析出的地址，然后恰好拨号该地址。**

**解析必须发生在地址检查之前。** `actionResolve` 把结果写入
`metadata.DestinationAddresses`，连接管理器把这些候选地址传给
`DialSerialNetwork`；其非并行的回退会拨号一个已解析的 IP。一个支持并行的
出站会收到原始目的地与候选列表，因此在声称普遍的地址固定之前，先核实该实现的
契约以及任何恢复路径。没有 `resolve` 动作时，
一个域名目标可以绕过基于 IP 的拒绝规则；预期的会话级策略要求必须使用它。

一个 DNS 重绑定测试确认这不是理论问题：一个先解析到允许地址、随后解析到 loopback 的域名
会在第二次解析时被拒绝，查询计数证明确实
发生了第二次真实查询（`TestJiejieTargetACLRebindingCannotBypass`）。

### UoT 的按数据报目的地 guard（Per-datagram destination guard for UoT）

对于非 Connect 的 UoT 会话，**每个数据报各自携带目的地**，因此一个通过了
会话级检查的会话，原本可以在后续数据报里把数据发到完全不同的地方。
`route/packet_destination_guard.go` 检查从数据报中解码出的目的地，并丢弃被拒绝的
那些。与既定会话目的地完全相同的会复用会话决策；其他目的地则在 guard 生命周期内
被记忆化，因此规则与 DNS 不会针对发往该键的每一个后续数据包重新评估。

| 属性 | 取值 |
|---|---|
| 生效于 | 被标记 `metadata.UoTDatagramDestinations` 的流，用于非 Connect 的 UoT 会话；控制该 guard 的是这个标记，而不是某个入站协议名 |
| 拦截位置 | **读** 侧 —— 写侧有一条批量快车道，无法保证看到每一个数据包，因此读侧是唯一无法被绕过的位置 |
| 开销 | 决策按目的地记忆化，因此一个被允许的数据报在首次之后只花一次 map 查找 |
| 规范化 | IPv4 映射的 IPv6 会先被规范化为 IPv4，因此 `::ffff:127.0.0.1` 无法把受限目的地偷带过规则 |
| 拒绝 | 被拒绝的数据报 **被静默丢弃且不产生错误**，因此同一会话中的其他数据报不受影响 |
| 无规则匹配 | guard **放行** —— `common/uot/router.go` 被十二个协议入站共享，因此一刀切的拒绝会是灾难性的 |

**该 guard 不会改路由到另一个出站。** 它的裁决只有许可/拒绝；一条会根据每个数据报的目的地
来选择不同出站的规则不会生效，而该
数据报仍然使用会话建立时选定的出站。
如果有人加入这样的规则，`TestPacketDestinationGuardOutboundSelectionPrecondition` 会失败。

对于由数据报承载的域名，`checkPacketDestination` 会为其规则决策解析地址，
但 `ReadPacket` 返回的是原始目的地，而 guard 按域名与端口缓存该决策。
它本身并不会把一个已解析的地址固定进被转发的数据报。因此上文所述的会话级
解析/检查/拨号保证不得推广到这条按数据报的路径：下游的
解析与 DNS 变更需要单独分析，然后才能声称具备重绑定抗性。

允许与拒绝两个 memo 映射都会在会话生命周期内保留不同的目的地键，
且没有驱逐上限（`packet_destination_guard.go`）。一个提供许多不同
目的地的对端因此可以增大保留状态。`TestPacketDestinationGuardMemoGrowsWithDistinctDestinations`
通过直接插入键来演示 map 的增长；它不是端到端的攻击测量。

**规则顺序本身就是安全属性：** 解析 → 例外 → 拒绝（`d4ead1498` → `7029722bf` →
`1dc734f75`/`3bbf17907`/`d1f3798a6`）。

### 为什么拒绝列表必须包含主机自身的地址（Why the deny list must include the host's own address）

`<VPS_SELF_IP>:443` 必须永久保持被拒绝。生产环境的 TCP/443 由 Nginx 占用，它按 SNI
把流量转发进本入站，因此允许客户端在 443 上重新 CONNECT 自身地址，就等于重新进入同一个
前门，形成自代理递归：

```text
Naive -> <VPS_SELF_IP>:443 -> Nginx -> Naive -> <VPS_SELF_IP>:443 -> ...
```

**CONNECT 的主机名与隧道内的 TLS SNI 并不相互绑定**，因此“为这一个域名放行自身
443”并不是可靠的递归防御：客户端可以先 CONNECT 一个被允许的域名，
然后在隧道内切换 SNI。拨号前重写到受限的内部 ingress 是
所选拓扑文档中给出的缓解措施；其安全性取决于实际的 ingress 与路由配置。

恰好只有 **一个合取的、仅限 TCP 的 self-SSH:2222 例外**（`7029722bf`）。

### 自托管 Web：重写目标，不要开放公网端口（Self-hosted web: rewrite the target, do not open the public port）

一个自托管 Web 区域会解析到主机自己的公网地址，从而被自身地址规则
命中；症状是客户端无法访问自己的站点。修复方式 **不是** 开放
`<VPS_SELF_IP>:443`，而是把目标重写到一个仅 loopback、仅 Web 的内部 ingress，做法是在一条限定
`network: tcp` 与 `port: [443]` 的 `domain_suffix` 规则上使用 `override_address`/`override_port`。

`override_address` 会清空 `metadata.DestinationAddresses`，因此拨号目标是 loopback ingress，
而绝不是解析出的公网地址。**即使一个客户端 CONNECT 了自托管域名，然后
切换到一个无关的 TLS SNI，它仍然终止于 loopback ingress**，因此递归路径被
*切断*，而不只是被劝阻。

`domain_suffix` 是原始后缀匹配：写裸后缀，而不要写 `*.` 前缀。在单条规则的
目的地组内，`domain`/`domain_suffix` 与 `ip_cidr` 是 **OR 而非 AND**，因此“仅当
该域名解析到自身地址时”无法通过组合它们来表达。

### 服务器内部连接不在本 ACL 的范围内（Server-internal connections are out of scope for this ACL）

目标 ACL 约束的是 **客户端选定的代理目标**；它不得破坏服务器
自身发起的连接。Naive 与 MASQUE 的 Web masquerade 后端、AnyTLS 回退后端、本地
DNS 解析器，以及 ShadowTLS 握手目标，都是由不经过 sing-box Router 的服务器组件
建立的，因此按入站作用域限定的规则不会匹配到它们。
`TestJiejieTargetACLDoesNotBreakMasqueradeBackend` 固定了这一点：在同一个实例中，规则确实
拒绝 loopback，而 masquerade 仍能到达其 loopback 后端，同时客户端对
loopback 的 CONNECT 仍被拒绝。

### CONNECT 状态码不是拒绝信号（The CONNECT status code is not a rejection signal）

入站在接受一个 CONNECT 时返回 `200 OK`，而 router 级的 `reject` 会在 **之后** 关闭该
连接。CONNECT 到 `127.0.0.1:443`、到主机自身地址，或到一个代理入口
名的，全都返回 `200 OK`，然后不传输任何数据。**测试必须向隧道内写入，并观察
源站是否有应答 —— 不得用状态码来判断某个目标是否被拒绝。**

### 已知限制（已记录，未解决）（Known limits）

* **主机自身的公网地址不在拒绝集合中。** 仓库里没有关于它的可信记录，
  因此有意不作猜测。在称其完成之前，请在主机上确认。
* 建议的 CIDR 拒绝列表并未在生产环境中验证过。
* 按 IP 限流只在单一 loopback 地址上演练过，包括其 IPv4 映射
  形式；在真正分布式来源下的行为未经验证。
* 生产拓扑有意地 **不配置** 任何 `server_limits`。
* 限流器的 fail-closed 路径与摊还清扫只在单元测试层面被证明，而非在负载之下。
* 自托管 Web 的运行时测试需要第二个 loopback 地址，而 macOS 无法做到，因此它们
  **在 macOS 上跳过**，只在 Linux 上执行。
* `test/jiejie/jiejie_naive_ipv6_udp_test.go:141,169` 以 *"no usable IPv6 address; **NOT a
  pass**"* 跳过（中文：没有可用的 IPv6 地址；**不算通过**）。本 fork 的跳过措辞处处都是刻意的 —— 请把 `SKIP` 视为 `UNVERIFIED`。

---

## 4. `transport/http` 与 `common/httpclient`（`transport/http` and `common/httpclient`）

`transport/http` 是本 fork **最大的一次单项新增**：在 `a267e99` 处有 90 个文件，而在
`e4b7e692` 处是 20 个，被 **137 个提交** 触及。注意 `protocol/http/*`（入站）与
`transport/http/*`（客户端传输）是两个不同的世界。

**HTTP 客户端传输需求。** `common/httpclient/transport_requirement.go`
（`:23-36` 处的 `TransportRequirement{AnyOf, AllOf, Conditional}`）计算一个已配置的
HTTP 客户端需要哪些传输：

| 配置 | 需求 |
|---|---|
| 版本 1 或 2 | `AllOf{tcp}` |
| 版本 3 且 **带** 回退 | **`AnyOf{udp, tcp}`**，其中 `udp` 为 **`Conditional`**（`http3FallbackRequirement()` `:54-64`） |
| 版本 3，无回退 | `AllOf{udp}` |

> **带回退的 HTTP/3 需要 `AnyOf{udp, tcp}`，而不是两者都要。** 要求两者都要会拒绝一份本来
> 可用的配置。这与 DNS 的 `udp ⇒ AllOf{udp} + Conditional{tcp}` 是同一形状
> （`dns/transport/requirement.go`），那里的 TCP 重试只在响应被截断时触发。

在 `common/httpclient/transport_capability.go:30` 处强制执行，由
`common/httpclient/manager.go:122` 调用。

在包装/能力层发现了五个真实缺陷，并以 red-first 的方式修复：

| 位置 | 缺陷 | 后果 |
|---|---|---|
| `route/conn.go` | 批量创建未被转发 | 一个支持批量的 UDP 被按每次系统调用一个数据报写入 |
| `route/packet_destination_guard.go` | 写侧未被固定 | 新增一个方法就能直接把批量数据写入原始 socket，跳过检查 |
| `transport/http/stream_error.go` | 对非 `ExtendedConn` 的 `WriteBuffer`/`ReadBuffer` 返回了 `nil` | **为一个从未写入的缓冲区报告了成功** |
| `transport/v2rayhttp/conn.go` | 在 `responseCache` 持有字节时 `ReaderReplaceable` 为 true | **响应头被静默截断** |
| `transport/trojan/protocol.go` | 一个承重的握手不变量；该包有 `protocol_replaceable_test.go`，但仅凭它存在并不能证明覆盖了每个标志位 | 一个被反转的标志位可以在没有握手的情况下发送载荷 |

三条审计线索被 **证伪而非修复**：`v2raygrpc` 的 `syscall.Conn`（与
`grpc@v1.79.1` 逐字节相同）、`normalizingConn` 宣称的能力（缺陷是那个静默的 `nil`，而不是
其宣称）、以及 `trackedConn` 的活动跟踪。

---

## 5. 证据（Evidence）

| 保证 | 级别 | 复检 |
|---|---|---|
| padding/写入契约、短写、帧计数器 | `VERIFIED_UNIT` | `protocol/naive/audit_write_contract_test.go`、`padding_test.go`、`padding_segmentation_test.go`、`padding_zero_frame_test.go`、`audit_padding_test.go`、`fuzz_padding_test.go` |
| UoT v1/v2 传输矩阵 | `VERIFIED_UNIT` | `test/jiejie/jiejie_naive_uot_transport_matrix_test.go`（独立模块） |
| 目标 ACL 重绑定示例 | 针对 `TestJiejieTargetACLRebindingCannotBypass` 的历史测试证据；并非普遍保证，也不是按数据报的保证 | 独立的 `test/jiejie` 模块 |
| 按数据报的 UoT guard | `VERIFIED_UNIT` | `TestJiejieTargetACLUoTV1MultiTargetChecksEachDatagram`、`TestJiejieTargetACLUoTV2NonConnectMultiTarget`、`TestPacketDestinationGuardPermitsWhenNoRuleMatches`、`TestPacketDestinationGuardExposesNoBypassReadPath`、`TestPacketDestinationGuardOutboundSelectionPrecondition` |
| 该 guard 不会破坏服务器内部后端 | `VERIFIED_UNIT` | `TestJiejieTargetACLDoesNotBreakMasqueradeBackend` |
| HTTP/2 资源约束、H3 可用性判定 | `VERIFIED_UNIT`（需要 `with_quic`） | `protocol/naive`、`option/http_resource_bounds_test.go` |
| 批量能力的丢失 | 带真实 red 对照的 `VERIFIED_UNIT` | `route/udp_batch_capability_test.go`（3 个测试） |
| 与官方 NaiveProxy 客户端和 Caddy 的互通 | **此处为 `UNVERIFIED`** —— 本次校准未运行参考套件；Verify 在独立的 `test/` 模块中运行另一个选定的测试 | `run-jiejie-suite.sh`（约 20 分钟，需要 bash + 参考二进制） |
| 真实网络 STUN、IPv6 UDP 源站、Caddy 性能对比 | **`UNVERIFIED`** —— 明确记录为未做 | — |
| 设备行为 | **`UNVERIFIED`** | 需要设备 |

---

## 6. 溯源（Provenance）

| 提交 | 类型 | 做了什么 |
|---|---|---|
| `d53c867b7` | FEATURE | **本 fork 第一个由 fork 编写的提交** —— HTTP 入站 masquerade handler |
| `45f6686a6` | FEATURE | **EXTERNAL-ADAPTED** 的原生 Naive 入站（参考：`klzgrad/forwardproxy@naive d62c80d3dd2c706b6b87579844d2397bddd18317`，在第 320/352 行以及 bufReader 注释中引用） |
| `77607290f`、`ec1e7ab29` | FEATURE | 该入站的生产注册 |
| `7454a8add` | FEATURE | padding 协商与 解析→认证→隧道 的顺序 |
| `23af897bf` | HARDENING | 移除 `-connect-authority` |
| `593f634a1` | FIX | 劫持已缓冲的字节 |
| `fb90272a5` | FIX | 一次 `io.Writer` 短写导致了 **帧失步** |
| `b16660ed1` | HARDENING | 移除了一处时序信号 |
| `3c6e0aad3` → `fe5789539` | FIX → FIX | 钳制被加入、随后被 **撤销**；完整的 0..255 padding 范围才是正确的 |
| `e9d329380` | COMPAT | `Padding` 头在响应上变为无条件发送 |
| `d4ead1498` | HARDENING | 引入 `packetDestinationGuard` |
| `7029722bf` | HARDENING | 唯一的、仅限 TCP 的 self-SSH:2222 例外 |
| `1dc734f75`、`3bbf17907`、`10e1f2a95`、`d1f3798a6`、`2ec06e451` | HARDENING | guard 的顺序与规范化 |
| `58985a375` … `f64638a98` | INTEROP | Naive 互通系列（Caddy、官方客户端、H3 对 Caddy） |
| `724035c76` | REFACTOR | 移除死配置（`server_limits.idle_timeout`） |
| `bbfc31582` → `968395304` | REFACTOR | Clash API 被移除后又恢复 |
| `4ebc00414`、`7f7c7ae11a38`、`8f1ba42a9` | FEATURE | TLS 终止之后的 AnyTLS 回退 |
| `b5a25edd8` | ZERO-COPY + HYGIENE | `errors.As` 的堆逃逸，以及一个误入的 21 MB `masque.test` 二进制（已移除 `9e7c85955`，已忽略 `bcad6b4d6`） |

`EXTERNAL-ADAPTED` 标签只在引述了来源之处使用 —— 这里的 `klzgrad/forwardproxy`
与 Caddy 引用来自本 fork 自己的提交与文档。

---

## 7. AI 注意事项（AI cautions）

1. **不要把原生 Naive 描述为“一次依赖升级”。** 它是一个有 64 个提交的 fork 子系统。
2. **不要添加 Caddy 依赖。** 本 fork 使用 `x/net/http2` + `h2c` + 它自己的 quic-go fork。
3. **不要测完服务端 codec 就断定客户端正确。** 客户端出站在
   另一个仓库中。
4. **不要对 HTTP/1 CONNECT 分帧。** `Padding` 头只在 HTTP/2 与 HTTP/3 上被查询。
5. **不要把 0..255 的 padding 范围钳制到缓冲区。** 那曾试过并被回退。
6. **不要静默截断或“修复”短写。** 按设计它就是 `io.ErrShortWrite`，并且
   帧计数器不得在整个帧写完之前前进。
7. **不要让 `include/quic_stub.go` 安装 nil 构造函数。** 正是这个失败的 stub 让
   `decideHTTP3Availability` 能区分“不存在”与“已损坏”。
8. **不要在入站同时还有 TCP 时，把缺失的 HTTP/3 传输当作致命。**
9. **不要添加 `server_limits` 的握手或空闲超时。** `tls.handshake_timeout` 与
   `HTTP2Options.IdleTimeout` 已经拥有那些阶段，两个重叠字段可能互相矛盾。
10. **不要把 `header_timeout` 应用于已建立的隧道。**
11. **不要用转发头作为 `max_connections_per_ip` 的键。** 必须是真实的传输对端。
12. **不要在检查之后才解析目标。** `resolve` 必须先行；被检查的地址必须
    是实际拨号的地址。
13. **不要把按数据报的 guard 移到写侧。** 批量快车道无法保证
    看到每一个数据包。
14. **不要在无规则匹配时让按数据报的 guard 拒绝**，也不要让它改路由到
    另一个出站。
15. **不要为了修好自托管站点而开放 `<VPS_SELF_IP>:443`。** 把目标重写到仅 loopback 的
    ingress。
16. **不要用 CONNECT 状态码判断目标是否被拒绝。** 两种情况都是 `200 OK`；向
    隧道内写入并观察源站。
17. **不要把 `domain_suffix` 与 `ip_cidr` 组合成“仅当它解析到自身
    地址”的意思。** 在同一个目的地组内它们是 OR，而不是 AND。
18. **不要为带回退的 HTTP/3 同时要求 UDP 与 TCP。** 它是 `AnyOf`，并把 UDP 设为
    `Conditional`。
19. **选择目标对应的 build profile 并核实其依赖。** OTHERS 省略了 Naive 出站；
    Windows profile 包含 `with_purego`。仅有平台源文件存在
    并不能证明某个原生库或发布产物可用。
20. **不要引用本工作树中 `test/jiejie/**` 的结果。** `test` 模块是独立的，需要
    参考二进制与网络，而 push CI 故意不运行它。
