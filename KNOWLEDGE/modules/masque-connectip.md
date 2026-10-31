# MASQUE 与 CONNECT-IP（MASQUE / CONNECT-IP）

## 经由 TUN 栈的端点本地流量（Endpoint-local flows through the TUN stack, 2026-10-29）

`protocol/masque/endpoint.go` 判断目的地是否为端点本地地址。在
system 模式下它保留 `ActionAccept`。在非 system 模式下，它调用 `adapter.JudgeHostFlow`；发往
其他目的地的流量仍走 `adapter.JudgeFlow`。WireGuard（`protocol/wireguard/endpoint.go`）
与 Tailscale（`protocol/tailscale/port.go`）也区分 system 与非 system，各自实现自己的本地
地址检查。这是源码行为，并不构成端到端数据包投递的证明。

TUN 启动时，只有当栈实现了 `tun.RoutedPort`，`protocol/tun/inbound.go` 才注册它。
`route.NetworkManager` 在互斥锁保护下只保留第一个非 nil 的注册。`JudgeHostFlow` 选中
被路由端口的 IPv4 或 IPv6 地址，保留原来的目的地端口，对 TCP、UDP 或 ICMP 返回 `ActionFlow`。
管理器或端口不存在、匹配到的地址族无效，或者协议不受支持时，它返回先前的 `ActionAccept`。
没有实现被路由端口的 system 栈与 gVisor 栈，不会注册该端口。`adapter.FlowLogger` 由 host 流
与普通路由流共用；host tracker 没有出站。单元用例见 `adapter/host_test.go`。
除首次注册那段代码之外，代码并不隐含任何关于注册替换或拆除的保证。

## v0.1.9 之后的客户端生命周期（Client lifecycle after v0.1.9）

**VERIFIED_SOURCE**，测试目标 `011f3fdcd99`：`protocol/masque/client.go` 在启动期间把隧道设备
与客户端各自的关闭函数都注册到生命周期 scope。因此 scope 清理会同时覆盖
隧道客户端与设备；不要把它换成只处理
TUN 的关闭逻辑。`transport/masque/client.go` 拥有客户端关闭路径。这并不构成
关于设备、服务器互通或性能结果的结论。

**范围：** `transport/masque/*`（本 fork 的数据平面）、`protocol/masque/*`（端点）、
`protocol/http` 的 CONNECT 路径，以及 `option/masque.go` 这一层接口。

**溯源 —— 在归因任何东西之前先读这里。** MASQUE 由 **上游** 的 `b83089ec` 引入
（"Add MASQUE support"，世界，2026-09-21）。这个提交位于 fork 基线 *之下*，可以从
上游引用到达。在上游祖先 `e4b7e692` 处，`protocol/masque` 有 **3 个文件**，
`transport/masque` 有 **7 个**；到 `a267e99` 处两者分别是 **29** 和 **57**。所以：

```text
upstream inherited base   protocol/masque's endpoint skeleton, the capsule concepts, DATAGRAM
fork-added feature        the whole transport/masque dataplane, DNS assignment, inner resolver,
                          PREF64 plumbing, the MTU ceiling, ICMP error generation, the address pool
fork fixes / hardening    52 commits under protocol/masque, 70 under transport/masque
```

**不存在 `protocol/connectip` 包。** CONNECT-IP 位于 `transport/masque` 与
`protocol/masque` 之中。本 fork 的主模块 **不使用** `quic-go/connect-ip-go`，也 **不使用** `quic-go/masque-go`：
`go.mod:294,303-306` 中的 `masque|connect-ip|connectip` 注释描述的是被 pin 的
sing-quic 模块行，不是依赖项。`connect-ip-go` 只出现在独立的
`test/jiejie/reference/go.mod` 中，用作第三方的 **互通对端（interop peer）**
（`test/jiejie/reference/connect_ip_go_interop_test.go:14,23`）。**CONNECT-IP 由本 fork 自己实现。**

**在改动以下内容时加载本模块：** 任何 capsule、会话生命周期、MTU/PTB、DNS 分配、
inner/bootstrap 解析器划分，或 H3 客户端连接池。

---

## 1. 数据平面 —— `transport/masque`（The dataplane — `transport/masque`）

### Capsule 封装（Capsules）

| 机制 | 标识符 | 锚点 |
|---|---|---|
| 解析 / 序列化 | `parseAddresses`、`parseVersion`、`parseRoutes`、`appendAddress`、`newCapsule`、`newAddressCapsule`、`newRouteCapsule`、`RangesFromPrefixes` | `capsule.go:45,80,109,211,220,228,239,249` |
| 地址 / 路由类型 | `AssignedAddress`、`AddressRange`、`Contains`、`OverlapsProtocol`、`protocolConflicts` | `capsule.go:30,35,41,94,105` |
| DNS capsule | `DNSConfiguration.validate`、`validateDomainName` | `capsule_dns.go:179,148` |
| DoH 路径模板化 | `validateTemplateExpression` | `capsule_dohpath.go:290` |
| SVCB 参数 | `ValidateServiceParameters` | `capsule_svcb.go:150` |
| 请求路由模板 | `expression`、`Template`、`Scope`、`ParseTemplate`、`Expand`、`Match`、`parseTarget` | `template.go:15,20,28,34,121,143,230` |

**Capsule 加固与背后的测量数据：**

* **协议 `0` 代表所有协议**，因此跨协议重叠确实存在，必须检查
  （`protocolConflicts`）。重叠检查是 **线性** 的；*正确的* 二次方版本实测为
  **13.5 秒对 16 毫秒基线 = 800 倍 CPU DoS**（`b656b062f`，取代 `969909bc1`）。
* **8192 条目的 capsule 上限**（`a529b68c8`，一项带引述来源的 `EXTERNAL-ADAPTED` 选择；
  `quic-go/connect-ip-go` 使用同一常量）。在上限之前：149,796 条目 / **17.65 MiB 堆**。
* 在服务器自身地址上 `Contains() == lookup()`（`e4d2f6c1a`）。

**MTU / PTB capsule：** `handlePacketTooBig`（客户端 `transport/masque/client.go:704`，服务端
`server.go:579`）、`minimumLinkMTU`（`session.go:20`，在 `:520` 处比较）、`icmp.go` 中的 ICMP 生成
（`packetAddresses`、`isControlProtocol`、`decrementHopLimit`、`buildICMPError`、`buildICMPErrorTo` 位于
`:12,25,44,73,99`）。

### 会话（Session）

`session`、`newSession`、`run`、`loopDatagram`、`loopCapsule`、`readDatagramCapsule`、
`readControlCapsule`、`writeCapsule`（`session.go:80,103,133,162,245,277,302,350`）。

**CONNECT-UDP 以 RFC 9298/9297 的数据报承载** —— `writePackets` 前置 context
ID（`session.go:493`，`PrependContextID`）—— 而不是独立的、每包一个 capsule 的类型。

客户端：`Client`、`clientSession`、`NewClient`、`Start`、`Close`、`Start`/`Suspend`/`Resume`/
`RestartSession`、`WritePacketBuffers`、`handleAddressAssign`、`handleDNSAssign`、`handlePREF64`、
`handleRouteAdvertisement`、`handlePacket`、`handlePacketTooBig`、`loop`、`connect`、`activeSession`、
`Ready`、`WaitReady`（`client.go:24,72,81,139,207,242,247,272,329,391,397,405,429,444,455,467,518,554,585,607,662,686`）。

服务端：`ServerHandler`、`Server`、`serverSession`、`NewServer`、`NewTunnelRequest`、`releaseSession`、
`lookup`、`route`、`RouteOutbound`、`Contains`、`WritePacketBuffers`、`receivedPacketHeadroom`、
`assignedAddresses`（`server.go:24,30,53,74,85,161,298,321,339,347,372,386,428,449`）。

地址池：`addressPool`、`usable`、`allocate`、`release`（`pool.go:10,30,37,59`）。
路由匹配：`routeMatcher`、`compileRouteMatcher`、`contains`（`route_matcher.go:49,69,98`）。

### 所有权规则 —— 全树中最严格的一套（The ownership rules）

| # | 规则 | 锚点 | 破坏后果 |
|---|---|---|---|
| M1 | 单包成功会 **释放** 该数据报 —— **除非** 传输具备 owned-datagram 能力：此时 **所有权转移**，完全跳过释放 | `session.go:496-508`（owned 跳过 `:499-500`，释放 `:507`）；`transport/http/client_h3.go:511` | 重复释放 / use-after-free |
| M2 | 每条失败路径都 **恰好释放仍然属于自己的剩余部分**：`datagram.Release()`、`buf.ReleaseMulti(capsules)`、`buf.ReleaseMulti(buffers[i+1:])` | `session.go:530-532` | 池化缓冲区泄漏 |
| M3 | 每个分块在 **所有权** 上全有或全无；`writeBatchOwned` 返回已转移的前缀长度，调用方只回退剩余部分 | `session.go`、`writeBatchOwned` | 重放已转移的前缀会重复发送或重复释放；HTTP/3 拷贝回退中的部分入队见 `buffer-ownership.md` |
| M4 | `PrependContextID` **替换时会释放原件**，因此替换在发送前拒绝受影响的分块，并就地更新调用方的切片 | `session.go`、`writeOwnedBatchChunk` | 陈旧指针、重复释放，或回退时的数据包丢失 |
| M5 | 批次只有一个元素时，永远不走批量路径 | `session.go:472-477` | 一次经测量的性能退化（batch=1 更慢） |
| M6 | 数据包 headroom 由 **生产者** 预留，以容纳 1 字节的 context ID | `session.go:584-588`；`protocol/masque/client.go:594 FrontHeadroom`；`transport/masque/server.go:428 receivedPacketHeadroom` | 每个数据包都走替换路径（M4） |

**M4 是真实发生过的缺陷，代码注释记录了它（`session.go:562-573`）：** 一个批次被拒绝时仍保留了
context ID，回退路径又前置了第二个，`packetAddresses` 因此读到错误的版本半字节，
结果 **每个数据包都被静默丢弃 —— 没有报错，没有 PTB**。

### H3 客户端连接池（The H3 client pool）

`transport/http/client_h3.go:105` 构建 `&http3.Transport{EnableDatagrams: true, DisableCompression: true}`；
批量 API 是 `SendDatagramOwned(payload http3.OwnedDatagramPayload)`（`:511`）。

连接池行为（`b83695603` → `1511c1c6a`）：连接池现在 **在并列最小值的集合内** 轮转；
此前 **slot 0 服务了每一个隧道**。CONNECT 以 `HandshakeComplete` 为门禁（`0271b145a`），
以关闭 0-RTT 重放窗口。`http3_fallback` 会被读取，但默认值保持在 **5 秒 / 5 分**，而不是
httpclient 的 5 分 / 48 小时（`9491008a9` —— 一次死配置修复）。

在当前目标上，`option/http.go` 与 `transport/http/server_h3.go` 默认让 HTTP/3 服务端
路径管理器保持启用；只有禁用迁移才需要显式设置。H3 客户端测试还
检查 `DisablePathManager` 是否默认为 false。拥塞控制选项在客户端与服务端
分开设置，客户端未设置的选项会保留库默认值；不要从未设置的选项推断出 BBR。
推广之前，先核实具体的构造函数与被 pin 的 QUIC 版本。

---

## 2. 端点 —— `protocol/masque`（The endpoint — `protocol/masque`）

`ClientEndpoint`（`client.go:50`）、`NewClientEndpoint`（`:118`）、`Start`（`:322`）、
`UpdateConfiguration`（`:363`）、`installAssignedDNS`（`:421`）、`Pref64Prefixes`（`:473`）、
`resolverCapability`（`:491`）、`h3ConnectionState`（`:528`）、`UpdateTunnelTransport`（`:551`）、
`recompileAssignedDNS`（`:560`）、`InterfaceUpdated`（`:598`）、`OnDemand`（`:602`）、
`SetKeepIdleConnections`（`:606`）、`PreMatchFlow`（`:627`）、`PortAddresses`（`:631`）、
`PortMTU`（`:654`）、`UpstreamPort`（`:658`）、`AttachReturn`/`DetachReturn`（`:662,666`）、
`JudgeFlow`（`:670`）、`NewDNSPacket`（`:674`）、`WritePackets`（`:678`）、`DialContext`（`:727`）、
`DialsDestinationLocally`（`:762`）、`lookupInner`（`:801`）、`dohExecutor`（`:863`）、
`dialResolved`（`:893`）、`ListenPacketWithDestination`（`:938`）。

`PREF64` 位于 `protocol/masque/pref64.go`，通过 `ClientEndpoint.Pref64Prefixes()`
（`client.go:473`）以及 `clientSession.handlePREF64`（`transport/masque/client.go:585`）使用；服务端
在 `transport/masque/server.go:495` 处应答。

---

## 3. 两个解析器 —— 不可互换（The two resolvers）

| 角色 | 解析什么 | 配置字段 | 实现 |
|---|---|---|---|
| **Bootstrap（外层）** | **服务器主机名** | `option/masque.go:34-40` `DialerOptions.DomainResolver` | `protocol/masque/bootstrap_cache.go` —— `bootstrapResolution` `:65`、`bootstrapCache` `:89`、`newBootstrapCache` `:101`、`resolve` `:115`、`recordFresh` `:156`、`promote` `:187`、`hasRecovery` `:197`、`bootstrapDialer` `:213`、`resolveCandidates` `:233`、`DialContext` `:259`、`buildBootstrapResolution` `:311`、`bootstrapResolver` `:331`、`strategy` `:349`；外加 `bootstrap_race.go`（`handshakeRacer` `:52`、`dialWithWinner` `:123`、`awaitHandshake` `:334`、`splitByPreference` `:357`、`interleaveCandidates` `:398`、`masqueConnDialer` `:445`、`preferIPv6FromStrategy` `:481`） |
| **Inner（内层）** | 在隧道 **内部** 承载的流量 | `option/masque.go:33-44` `InnerDomainResolver`，注释明确说明了这一点；未设置时行为不变（普通 DNS 路由规则，查询选项为空） | `protocol/masque/dns_resolver.go` + `dns_assignment.go`（`dnsAssignmentSnapshot` `:41`、`dnsConfigurationSnapshot` `:54`、`dnsResolverSnapshot` `:73`、`decide` `:266`、`hasUsableResolver` `:298`、`resolversByPriority` `:311`、`compileDNSAssignment` `:390`、`compileResolver` `:449`、`compileCapabilities` `:519`、`resolverCapability.sameH3OriginAvailable` `:370`、`reachableForProtocol` `:668`） |

两者在结构上分离：bootstrap dialer 在 **任何隧道存在之前** 构建
（`bootstrap_cache.go:311`），inner 路径只由 `lookupInner` 查询
（`client.go:745`）。**调换两者，服务器自身主机名的解析就会走一条
尚不存在的隧道**，或者把隧道内的名字泄露给外层解析器。

---

## 4. MTU 与 PTB（MTU and PTB）

**三个彼此不同的量** —— 完整论述见 `mtu-and-geometry.md`。在 MASQUE 中：

* **外层链路 MTU** —— 平台路径承载的量；
* **外层 QUIC 初始包大小** —— detour 容量已知时会被钳制；构造函数并
  不为后续数据包设定硬上限（见 `mtu-and-geometry.md`）；
* **内层隧道 MTU** —— 隧道向载荷承诺的量。

PTB 路径：数据报发送以 `datagramErrorTooLarge` 失败 → `mtu := maxPayloadSize - 1`；如果
`mtu >= minimumLinkMTU`，则用 `datagram.Advance(1)` 撤销 context ID，并
调用 `handlePacketTooBig(datagram, mtu)`（`session.go:516-524`）。

测试：`packet_too_big_test.go`、`packet_too_big_session_test.go`、`packet_size_bound_test.go`、
`outer_quic_mtu_test.go`、`port_mtu_lifecycle_test.go`，再加上
`protocol/masque/outer_quic_mtu_test.go`。

---

## 5. 拆除（Teardown）

`Server.releaseSession`（`server.go:298`）、`sessionErrorIsExpected`（`:613`）。
契约测试：`transport/masque/shutdown_test.go`、`close_boundary_test.go`、
`close_boundary_h3_test.go`、`session_datagram_error_test.go`、`session_error_class_test.go`；
`protocol/masque/close_boundary_h3_test.go`、`readiness_boundary_test.go`、
`lifecycle_generation_test.go`、`scope_ownership_test.go`。

有两处已记录的拆除修复值得了解：

* `9235f90` —— 服务器端点的拆除交给 **关闭它的那个 `Scope`**。
* `c3b4f567` —— 释放 **`Scope.Close` 返回之后** 才获取的资源。
* `a367bbe3` —— 关闭就绪窗口，把 **每一次** DNS 失效串行化，并修复三处
  平台测试缺陷。
* `a00a2fe6` —— 把 HTTP/3 的获取纳入 **关闭决策**，因此 H3 环节正在拆除时，
  隧道不能被报告为就绪。

`protocol/masque/server.go` 还有那个“排空之后再获取”的修复：`StartStateInitialize`
在获取设备时注册 `s.Close`，但 `StartStateStart` 会 *稍后* 才绑定监听
socket。`common/listener` 没有“关闭之后再启动”的防护，因此 `ServerEndpoint` 在
`startAccess` 下公布 `closed`，随后释放，并在绑定之后再次检查（`acquiredStillOwned`）；
如果端点在此期间已经关闭，`Start` 会释放自己刚刚绑定的 socket。

---

## 6. MASQUE DNS 角色与 DNS 锁工作（MASQUE DNS roles and the DNS-lock work, commit family `8f1ba42a9`…`1d87f0cf7`）

`e93e297aa` 用 **不可变快照** 取代了早先可变的 DNS 模型。新模型复用原生 DNS
传输，删除了 `assigned_dns_model_test.go`、`assigned_dns_transport_test.go`、
`assigned_doh_test.go`、`dns_leak_cycle_test.go`、`resolver_precedence_test.go`，替换为
`dns_assignment_test.go`、`dns_capability_test.go`、`dns_endpoint_test.go`、
`dns_request_url_test.go`、`inner_resolver_test.go` —— 一次伴随重测的重构，不是静默删除。
任何引用这些已删文件的文档，描述的都是 `e93e297aa` 之前的模型。

---

## 7. 证据（Evidence）

| 保证 | 级别 | 复检 |
|---|---|---|
| Capsule 解析/序列化、地址与路由类型 | `VERIFIED_UNIT` | `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./transport/masque/...` |
| 每块原子性、已转移前缀与拒绝回退 | `VERIFIED_UNIT` | `batch_chunk_test.go`、`batch_order_test.go`、`session_datagram_error_test.go` |
| 拆除所有权与关闭边界 | `VERIFIED_UNIT` | `shutdown_test.go`、`close_boundary*.go`、`scope_ownership_test.go` |
| MTU 初始钳制 / PTB | 对所引用的测试用例为 `VERIFIED_UNIT`；MASQUE 的后续数据包没有被证明存在硬性外层上限 | MTU 测试文件；`mtu-and-geometry.md` |
| H3 连接池轮转、0-RTT 门禁、回退调度 | `VERIFIED_UNIT` | `transport/http` 的连接池测试 |
| 长时间运行的分配行为 | `VERIFIED_UNIT`（慢） | `transport/masque/longrun_alloc_test.go` |
| CONNECT-IP 与独立实现的互操作性 | **本次校准中未验证** —— `test/jiejie/reference/connect_ip_go_interop_test.go` 位于独立的测试模块；Verify 在那里只运行选定的测试，而不是整套参考套件 | `run-jiejie-suite.sh`（需要 bash） |
| macOS/H3 设备行为 | **`UNVERIFIED`** | 需要设备 |
| 无竞态 | `-race` **在本机上受阻**；在声称覆盖 MASQUE 之前，先检查 CI 运行中的确切包列表与 SHA | Linux runner |
| `protocol/masque` 套件 | 2026-10-28 刷新期间未加标签的测试在本地通过；但这次运行不会选中带 QUIC 标签的用例。历史上带标签的结果仅对其记录的基线成立。 | 使用相应的 profile，并检查实际运行了哪些测试 |

---

## 8. 溯源（Provenance）

| 提交 | 类型 | 做了什么 |
|---|---|---|
| `b83089ec` | — | **UPSTREAM-INHERITED。** "Add MASQUE support"，世界，2026-09-21。位于 `e4b7e692` 之下；可从上游引用到达。**不是 fork 特性。** |
| `d53c867b7` | FEATURE | 本 fork **第一个由 fork 编写的提交**：入站 masquerade handler —— 即 MASQUE/HTTP 入站中用于服务超限探测的那一个 |
| `7e57fe8c7` | HARDENING | 限流器真正形成约束 |
| `b83695603` → `1511c1c6a` | FEATURE/FIX | H3 连接池及其 tie-break |
| `0271b145a` | HARDENING | CONNECT 以 `HandshakeComplete` 为门禁（0-RTT） |
| `9491008a9` | FIX | `http3_fallback` 曾是死配置 |
| `e4f847ecf` | COMPAT | **EXTERNAL-ADAPTED** 的 QUIC 默认值，来自 `quic-go/masque-go` + `connect-ip-go` |
| `969909bc1` → `b656b062f` | HARDENING → PERF/SUPERSEDED | 跨协议重叠；二次方版本是 800 倍 CPU DoS |
| `a529b68c8` | HARDENING | 8192 条目的 capsule 上限 |
| `e4d2f6c1a` | FIX | 在服务器自身地址上 `Contains() == lookup()` |
| `66f0e9436`、`db94a76b2`、`c0475793f` | HARDENING | 进一步的 capsule 上限 |
| `6d94da584` → `b5a25edd8` | PERF/ZERO-COPY | 无每包分配；`errors.As(&tooLarge)` 即使 **`err == nil`** 也强制堆逃逸 |
| `864c0048b` | PERF | 路由匹配器中的二分查找 |
| `1a7b3ebe5` | ZERO-COPY | owned DATAGRAM 路径 |
| `8870623cf` | FEATURE | bootstrap 缓存 |
| `8f1ba42a9` → `1d87f0cf7` | FEATURE/REFACTOR | 显式化 DNS 角色；不可变快照（`e93e297aa`） |
| `05fa73391`、`b1961033d`、`a6164dead`、`b81244bdf`、`907dbfb99` | REFACTOR | 死代码清理，包括 PREF64 清除路径与一条死掉的选择规则 |
| `2118ee40` | FEATURE | 引入 detour 容量处理；见 §4 中关于当前初始大小的限定 |
| `0d28c26d` | FIX | 让 H3 回退可达；允许隧道环节分片 |
| `a367bbe3` | FIX/HARDENING | 就绪窗口、串行化的 DNS 失效 |
| `a00a2fe6` | FIX | HTTP/3 的获取是关闭决策的一部分 |
| `9235f90` | FIX | 服务器端点拆除交给关闭它的 `Scope` |
| `c3b4f567` | FIX | 释放 `Scope.Close` 返回之后获取的资源 |

除 `b83089ec` 之外，其余全部是 **OWN**；标注处为带引述来源的 `EXTERNAL-ADAPTED`。

---

## 9. AI 注意事项（AI cautions）

1. **不要把 MASQUE 称为 fork 特性。** 端点骨架与 DATAGRAM 概念是上游的；
   数据平面是本 fork 的。必须说明指的是哪一半。
2. **不要去找 `protocol/connectip`。** 它不存在。
3. **不要向主模块添加 `quic-go/masque-go` 或 `connect-ip-go` 依赖** —— 本 fork
   自己实现了这一点，并且只在 `test/` 中把 `connect-ip-go` 作为互通对端使用。
4. **不要让批量路径处理单个数据包。** batch=1 是一次经测量的性能退化。
5. **不要在 owned 路径上释放数据报。** 所有权已经转移。
6. **不要就地“修复” `PrependContextID` 的替换。** 它会释放原件，必须拒绝
   受影响的分块并更新调用方的切片；不要重放已转移的前缀。
7. **不要移除生产者侧的 headroom 预留。** 否则每个数据包都会走替换
   路径。
8. **不要把线性重叠检查“纠正”为显而易见的二次方版本。** 实测为
   13.5 秒对 16 毫秒 —— 800 倍 CPU DoS。
9. **不要在未重新测量的情况下提高 8192 条目的 capsule 上限**；149,796 条目曾占用 17.65 MiB。
10. **不要调换 bootstrap 与 inner 解析器**，也不要给 `InnerDomainResolver` 一个
    经由外层路径的默认值。
11. **不要用常数给外层 QUIC 数据包设上限。** 它由 *经证明的* detour 容量来限制。
12. **不要重新启用 `MaxIncomingStreams = 1<<60`**，也不要把 QUIC 调优变成非 opt-in。
13. **不要用任何弱于 `HandshakeComplete` 的条件来给 CONNECT 设门禁。**
14. **不要引用已删除的 `assigned_dns_*` 测试文件** —— 那个模型已被 `e93e297aa` 取代。
15. **不要在未加标签的情况下运行本包的测试并据此下结论。** 它们需要
    `DEFAULT_BUILD_TAGS_OTHERS`；未加标签会得到 19 个标签缺失失败。
