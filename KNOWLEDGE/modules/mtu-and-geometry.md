# MTU、数据包大小与缓冲区几何（MTU, packet size and buffer geometry）

**依赖路径范围：** `common/buf/buffer_standard.go`、`common/buf/buffer_low_memory.go` 和
`common/low_memory*.go` 属于固定版本的 `github.com/sagernet/sing`，不在本仓库根目录。
引用历史缓冲区参数或行号前，请先根据 `go.mod` 确认依赖版本并核对源码。本文列出的 MTU
数值也有版本和配置范围，不能直接当作当前配置的测量结果。

## v0.1.9 之后 TUIC 下层隧道的上限（TUIC lower-tunnel ceiling after v0.1.9）

**VERIFIED_SOURCE，审阅版本 `011f3fdcd99`。** 当下层隧道能提供可证明的容量上限时，
`protocol/tuic/outbound.go` 会通过 `dialer.DetourPathCapacity` 推导 QUIC payload 上限，
用于设置或限制初始包大小；对于这个已知上限，还会禁用路径 MTU 发现，并拒绝低于 QUIC
Initial 最小值的结果。路径容量未知时，代码不会声称存在上限。

该结果交给固定版本的 sing-quic 客户端使用；它是路径容量约束，不是对物理链路 MTU 的测量。
将结论推广到其他协议前，应检查 TUIC 测试和对应的 fork 版本。

对于 MASQUE，`protocol/masque/client.go` 会根据 detour 计算 `outerPayloadCeiling`，并在容量已知时限制 `HTTP3Options.InitialPacketSize`。因此，2026-10-28 历史审计中“没有计算任何外层上限”的表述过于宽泛。

但该构造函数没有用这个值设置 `InitialPacketSizeCeiling`，也没有据此禁用路径 MTU 发现；初始大小受到限制，不代表后续 QUIC 数据包也有同样的硬上限。以上是对 `011f3fdcd99` 的 `VERIFIED_SOURCE`，并非对真实网络或对端的测量。

**范围：** `common/bufgeom`（fork 创建）、MASQUE 的 MTU/PTB 路径、hysteria2 的硬上限、WireGuard 的隧道 MTU、`common/physicalpath/status.go` 的 MTU 块，以及低内存几何 profile。

**改动以下内容之前，先加载本模块：** 任何 MTU 计算、数据包大小上界、缓冲区大小、低内存 profile，或 PTB 处理路径。

---

## 1. 三个 MTU 量，而不是一个（Three MTU quantities, not one）

```text
outer link MTU          what the platform path carries        ← not proven by the MASQUE path model
        │
outer QUIC packet size  what the tunnel's own QUIC layer may put on that path
        │
inner tunnel MTU        what the tunnel promises the payload it carries
```

这三个量处于不同的协议层，需要分别计算和验证。已知其中一个值，并不能直接推导出另外两个值。

| # | 量 | 在何处计算 | 在何处受限 | 在随附配置中 |
|---|---|---|---|---|
| 3 | **内层隧道 MTU** | `protocol/masque/client.go:105`（`c.mtu`），由 `PortMTU()` `:654-655` 发布，种子在 `:119-121` | `DefaultMTU = 1280`（`transport/masque/session.go:18`）；设备 `protocol/masque/endpoint.go:88`/`:91` | **1280**——而且它是**配置出来的，不是测量出来的**（`client.go:635-653`） |
| 2 | **外层 QUIC 初始数据包设置** | `client.go` → `min(mtu + 51, MaxUint16)`；服务端镜像 `protocol/masque/server.go` | detour 容量钳制初始设置；低于 1200 的值一律拒绝。这不是对所有后续数据包的硬上限 | 在遇到更低的 detour 上限之前是 **1331** |
| 1 | **外层链路 MTU** | 这套 MASQUE 计算无法确立它 | 这套计算同样无法确立 | 就这些输入而言，该路径上**未知** |

**51 字节的来源：** `QUICPacketOverhead = 51`（`session.go:19`）= 1 context ID + 8 quarter-stream-ID varint + 16 AEAD + 1 header + 1 packet number + 24 frame，逐项列在 `outer_quic_mtu_test.go:31-46`。

**相关测试断言：** outer > inner（`outer_quic_mtu_test.go:73-77`、`:172-176`），并且 `1280 + 51 − 1232 = 99` 恰好成立（`:181-185`）。

**为什么不能由此确定外层链路 MTU：** `PathCapacity` 没有链路 MTU 字段（`path_mtu.go:127-155`）。它携带的是 **UDP payload** 上限，即内层大小减去 20/40 字节 IP 头、8 字节 UDP 头及封装开销（`path_mtu.go:111-120`）；这与链路 MTU 处于不同层次。`outer_quic_mtu_test.go:222-237` 检查相关选项没有第三个配置项。

其他组件可以读取接口 MTU，例如 `protocol/bridge/backend_linux.go`，但接口 MTU 本身不能证明端到端 MASQUE 路径的 MTU。

**上限由路径模型确定。** 提交 `feat(mtu): cap the MASQUE outer QUIC packet by a proven detour capacity`（`2118ee40`）恰好只动两个文件：`client.go` +51 行，`outer_quic_mtu_test.go` +237。WireGuard 的 `validateTunnelMTU`（`transport/wireguard/endpoint.go:523-569`）只拒绝 IPv6-on-sub-1280：一个 1210 字节的 detour 会得出 1130 的嵌套值，而该 detour 自身需要 1360（`:550-552`）。

**`sing-quic` 是这个上限的关键环节。** `scripts/ci/check-go-module-integrity.sh:105-115` 记录了它为什么是四个 `REQUIRED_FORK_REPLACES` 之一：

> *"It became load-bearing when the hard packet-size ceiling was given its own field: the ceiling reaches the wire only if this layer forwards it, so a nested module resolving upstream sing-quic would build a product whose proven ceiling silently disappears."*（中文：硬性数据包大小上限有了自己的字段之后，它就成了关键环节：只有这一层继续传递上限，实际发送时才能遵守它。因此，如果嵌套模块解析到上游 sing-quic，构建出的产品可能悄然丢失已确认的上限。）

---

## 2. PTB（数据包过大，packet too big）

确切路径如下，源码在 `transport/masque/session.go`：

1. 一次发送因数据包过大而失败，此时可用大小是 `MaxPayloadSize = MaxDatagramPayloadSize − VarintLen(StreamID/4)`——**context ID 仍计入**（`transport/http/client_h3.go:486-489`）；
2. 错误在这里分类（`session.go:401-411`）：**先做 nil 检查，再做直接类型断言**，`errors.As` 只在 `:417-426` 处作为回退。原因是 `errors.As` **会在函数入口把目标提升到堆上**，让每个*成功的*数据包多一次分配（`session.go:358-403`，由 `session_datagram_error_test.go:151-171` 守护，其预算是单包写入**恰好 1 次**分配）；
3. `mtu := maxPayloadSize − 1`；
4. **如果 `mtu >= minimumLinkMTU`（1280）**，代码用 `datagram.Advance(1)` 撤销 context ID，然后运行 `handlePacketTooBig(datagram, mtu)`（`transport/masque/session.go:516-525`）；否则就是**硬错误**——*"unable to carry 1280 byte packets"*（中文：无法承载 1280 字节的数据包）；
5. 客户端在 `transport/masque/client.go:704-715` 处理它；服务端在 `transport/masque/server.go:579-592`，把错误定向到**所属对端**（`firstPeerAddress :596-605`）并在该会话上排队；ICMP 错误在 `icmp.go:73-85`、`:99-134` 中构建，目的地址重写会**重新计算校验和**。

**数据报过大时的约束：** 不得静默丢弃，也不得转换为 capsule。只有 `MaxDatagramPayloadSize − VarintLen(StreamID/4) − 1` 不小于 1280 时才发送包含该值的 PTB；否则向发送方返回上文所述的显式错误。`sing-tun/icmp_error.go:113`、`:155` 还会进一步限制 MTU。

测试：`packet_too_big_test.go`（`:111`、`:175`、`:234`、`:271`、`:294`、`:374`）、`packet_too_big_session_test.go`（`:150`、`:212`、`:270`、`:295`）、`packet_size_bound_test.go:90-186`（`maxPacketSize = 40 + 65535`，`session.go:56-61`）。

**服务端预留空间：** `server.go:428-430` 报告 `max(PacketHeadroom, handler.FrontHeadroom())`——即传输层自己的预留，或处理器的预留（如果处理器需要更多）。

---

## 3. hysteria2 的“硬路径上限”（The hysteria2 "hard path ceiling"）

提交 `91ca1652d`——*"fix(hysteria2): a hard path ceiling is its own field, applied after ChromeParrot"*。

**问题：** ChromeParrot 指纹会将 `initialPacketSize` 覆盖为 1250（`config.go:105-125`）。关闭 MTU 发现时，其他配置字段无法降低首批数据包的大小，因此启用指纹后，已确认的 1232 字节上限无法生效。

**修复：** 给上限单独一个字段（pinned quic-go fork 中的 `Config.InitialPacketSizeCeiling`），并在 ChromeParrot 覆盖**之后**应用。这样，即使启用了指纹，也能把已确认的 1232 字节上限传递到底层；同一个字段还限制 MTU 发现能把数据包大小提升到多大。

**测量与 pin：** `protocol/hysteria2/hard_ceiling_wire_test.go`；pin 在 `go.mod` 中；决策记录是 `docs/fork/v016-final-closure-ledger.md:4-13`。

> **陷阱：这份台账的正文与它自己的横幅相矛盾。** 第 `:465` 节仍然写着*"HY2 ChromeParrot is BLOCKED on the pinned quic-go fork"*（中文：HY2 ChromeParrot 在 pinned quic-go fork 上被阻塞），而 `:4-13` 处的头部恰恰推翻了这一点。**横幅是当前的；正文是记录。** 先读横幅。

MASQUE 的构造函数不设置 `InitialPacketSizeCeiling`：它只按已知的 detour 容量钳制初始偏好，而这不构成对后续数据包的硬性上界。后来的覆盖也可能让那个初始偏好失效。`protocol/tuic/outbound.go` 现在传入自己的硬上限，因此该字段并非 hysteria2 专属。改动这些路径时，要在确切的协议与 pinned QUIC 实现下，同时测试初始数据包与后续增长。

hysteria2 的这个区分值得说清楚，因为三个字段含义各不相同：

| 字段 | 种类 | 应用方式 |
|---|---|---|
| `InitialPacketSize` | **偏好** | 最先，并被指纹覆盖 |
| `InitialPacketSizeCeiling` | **约束** | **最后**（`quic-go config.go:127-140`），因此不会被指纹覆盖，并且还限制 PMTUD 的增长（实测 1232 → 1438，`config.go:184-198`） |
| `DisablePathMTUDiscovery` | **策略** | `hysteria2/outbound.go:148-156`, `:224`; `quicPathSizing :241-280` |

`config.go:109-125` 说明了顺序为什么重要：ChromeParrot **用 1250 覆盖大小，且从不读取上限**，因此在默认配置下，早先设置的上限会被丢弃（Parrot 默认开启，`outbound.go:228`）。

---

## 4. `common/bufgeom`——几何契约（the geometry contract）

这是一个 fork 新建的包（**1,157 行**：75 行非测试代码 + 4 个测试文件中的 1,082 行测试）。它规定：数据包路径获取的缓冲区**必须恰好可入池**，并且经过 `Advance`/`Prepend` 时**不重新分配 payload**。

**在替换后的 `sing` fork 中，“几何”是一对编译期组合，不是运行时开关：**

| Profile | 来源 | 大小 |
|---|---|---|
| default | `common/buf/buffer_standard.go:5-8`（`//go:build !with_low_memory`） | **32 KiB / 16 KiB** |
| low memory | `common/buf/buffer_low_memory.go:5-8`（`with_low_memory`） | **16 KiB / 8 KiB** |

另有 `common/low_memory_stub.go:5` / `low_memory.go:5`（`LowMemory` 分别为 false/true）。

**历史测量，覆盖两个 profile**（已记录的运行，退出码 0；本次校准没有重做基准）：

| | BufferSize / UDPBufferSize | `LowMemory` | 每流活跃堆 | 每次获取分配（live/idle/bulk） |
|---|---|---|---|---|
| default | 32768 / 16384 | false | **64.1 KiB** | 1.75 / 0.93 / 1.75 B |
| `,with_low_memory` | 16384 / 8192 | true | **32.1 KiB** | 0.94 / 0.53 / 0.93 B |

就所报告的活跃堆数值而言，差距约为 2×。记录的批量分配值是 6.01 对 5.48 B/KiB；这两个数字无法证明几何对每个工作负载或目标都独立。历史上的解释指向 `copyExtended` 在约 512 KiB 之后的增长（`geometry_bench_test.go:656-724`）。

| 规则 | 锚点 | 违反后的风险 |
|---|---|---|
| 缓冲区恰好可入池 | `geometry_test.go:63 TestGeometryBuffersAreExactlyPoolable` | 受管理且没有引用的缓冲区，若容量既非允许范围内的 2 的幂，也不是 `MaxPooledBufferSize = 73728`，释放时会 panic：`Buffer.Release()` 会调用 `common.Must(Put(…))`（`buffer.go:307`, `cond.go:294-300`）。请求超过该最大值时，`Get` 返回 nil，后续使用可能触发 nil slice panic（`alloc.go:11`, `:41-44`） |
| `Acquire` 在 `Advance`/`Prepend` 时从不重新分配 payload | `geometry_test.go:99 TestBufferAcquireNeverReallocatesThePayload` | 热路径上每个数据包多一次分配 |
| **分配上限**由断言保证，不只靠基准测量 | `!race` 那一半 | 一次回归会以数字、而不是失败的形式到达评审 |
| 该上限是 **`//go:build !race`**；在 `-race` 下只运行结构性的一半 | `race_detector_test.go:1`, `:25` vs `race_detector_stub_test.go:1`, `:7`; 消费方 `geometry_test.go:147-151` | 在 `-race` 下该上限没有意义：一次池化的 `Get`/`Put` 交接**在 `-race` 下实测 8,251 B/次获取，没有它时是 1.7**；为容纳这个数字而把 1024 字节上限放宽，会抹掉该断言原本要建立的“池化 vs 重新分配”区分 |
| `buf.New`/`Release` 的 `AllocsPerRun` 是 **1.00，而不是 0.00**，除非调用被内联 | `geometry_test.go:84-98` | 断言 `== 0` 是错误期望（内联时 0.00，未内联时 1.00 = 64 B） |
| `UDPBufferSize == BufferSize/2` | `geometry_test.go:48-52` | 数据包路径与流路径对池的判断不一致 |

> **过期的交叉引用：** `geometry_bench_test.go:732` 提到 `TestAllocsPerRunBufferAcquire`，但它**并不存在**。不要去找。

> **验证范围：** 按测试设计，字节分配上限只在非 race 构建中断言；race 构建运行结构性检查。本次审计主机无法运行 `-race`，因此该主机上的 race 检查为 `BLOCKED_BY_ENVIRONMENT`，不能据此推断其他环境的运行结果。

MASQUE 批次路径另有自己的几何规则：数据包的**头空间由生产者预留**，以容纳 1 字节的 context ID（`transport/masque/session.go:561-565`）；否则每个数据包都会走替换路径——也就是带有已记录静默丢弃 bug 的那条路径（`masque-connectip.md` §1）。

---

## 5. 低内存几何 profile（The low-memory geometry profile）

16 KiB 与 32 KiB 的选择由一个**仅移动端的构建标签**决定，且只从一处来源读取：

```sh
go run ./cmd/internal/appletags -mobile-only
```

* `cmd/internal/appletags/main.go:33` 定义这个标签；它的文档注释说明了该包存在的原因：低内存 CI 门禁与内存基准过去读取 `DEFAULT_BUILD_TAGS_OTHERS`，而它*"does not match what libbox builds"*（中文：与 libbox 实际构建的内容不符），所以那些检查*"were testing a configuration no Apple binary ships"*（中文：一直在测试一个没有任何 Apple 二进制随附的配置）。
* 这个标签经由 **`-tags-not-macos`**、而不是 `-tags` 传给 gomobile：`-tags` 会应用到每个目标，把仅移动端标签放在那里，就会在混合构建中传到 macOS（`applebuildtags/tags.go:63`, `mobilebuildtags/tags.go:42`）。
* `verify.yml:385-391` 读取它；字符串为空时**直接失败，不回退**：*"Refusing to fall back to a guess: this step would compile the default geometry while claiming to exercise the low-memory one."*（中文：拒绝回退到猜测：这一步会编译默认几何，却声称在演练低内存几何。）
* `client-macos.yml` 对标签集做**双向**断言：必需的标签存在，退役的标签不存在。

**几何标签是真实的行为开关，不是一次大小优化。** 一个声称低内存、却编译默认几何的构建，比不作此声称的构建更糟。

---

## 6. 状态，以及消费方可以得出什么结论（Status and what a consumer may conclude）

`common/physicalpath/status.go` 按跳（hop）发布一个 MTU 块，每块带一个 `Generations` 字段。状态面**只读，并且重新发布该模型**（`status.go:394-398`, `:638-641`）；里面的值是**拷贝，不是视图**。

**状态消费方能够得出的结论是：** 某一跳最后被证明的值，以及它是在哪一代下被证明的。把某个值当作当前值之前，必须先比较代次。

**代码必须在哪些地方带上原因报告 `UNKNOWN`：** 只要内核无法知道某个值，状态面就报告带具名原因的 `UNKNOWN`，而不是给一个默认值；只要任何一跳未知，`Path.Exit()` 就报告 `ok=false`，因此部分验证的路径不会被误当作已验证的路径（`path-selection-validation.md` §2）。

**`HopStatus` 的标量是拷贝。** `agentf_hop_scalar_ownership_attack_test.go`（`TestAgentFEveryHopScalarIsCopiedNotShared`）逐一破坏每一个 `PathStatus.Hops` 标量——包括嵌套的 MTU 与 `Generations` 字段——再截断调用方的 slice，从而证明状态值是拷贝。**这个测试与跨 kind 循环无关**；过去把它描述成那样是错误的。

---

## 7. 证据（Evidence）

| 保证 | 等级 | 复检 |
|---|---|---|
| 几何可入池性，以及 `Advance`/`Prepend` 上不重新分配 | `VERIFIED_UNIT` | `go test -count=1 ./common/bufgeom/...` |
| 分配上限 | 仅在 **`!race`** 构建中为 `VERIFIED_UNIT` | 同上 |
| MASQUE 的 PTB 纠正与初始大小钳制 | 对已测试的用例为 `VERIFIED_UNIT`；未确立对后续数据包的普遍硬上限 | `go test -count=1 -timeout 8m -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./transport/masque/... ./protocol/masque/...` |
| 启用 ChromeParrot 时，hysteria2 发送的数据包仍遵守硬上限 | `VERIFIED_UNIT` | `go test -count=1 -run HardCeiling ./protocol/hysteria2/` |
| 低内存几何只从一个来源读取 | `VERIFIED_SOURCE` | `go run ./cmd/internal/appletags -mobile-only` |
| 状态值是拷贝，不是视图 | `VERIFIED_UNIT` | `go test -count=1 -run TestAgentFEveryHopScalarIsCopiedNotShared ./common/physicalpath/` |
| 真实网卡的 MTU 行为 | **本次刷新中为 `UNVERIFIED`**——需要相关的真实 TUN 与环境 | `docs/fork/real-tun-validation.md` |
| 无竞态 | **`BLOCKED_BY_ENVIRONMENT`** | 需要 cgo |

**记录一个已撤回的结论，避免它被重新推导：** `docs/fork/v016-path-mtu-audit.md` 的第一个 PTB 论证**用错了路径，已经撤回**；撤回说明见 `docs/fork/v016-path-mtu-audit-correction.md:11-32`：`flow_dispatch.go` 属于 *ForwardDispatcher* 分支，而 hysteria2 的 QUIC 套接字走的是 `stack_go_udp_socket.go` 的 `GoUDPConn.transmit`。**应当读纠正文档，不要读那份审计。**

---

## 8. AI 告诫（AI cautions）

1. **不要把三个 MTU 量合并成一个。**
2. **不要用一个常量给 MASQUE 外层 QUIC 数据包封顶。** 封顶它的是*已证明的* detour 容量。
3. **不要取消 `sing-quic` 的 pin。** 只有这一层继续传递上限，实际发送时才能遵守它。
4. **不要静默丢弃一个过大的数据报。** 那是带修正后 MTU 的 PTB。
5. **不要把 MTU 修正到 `minimumLinkMTU` 以下。**
6. **不要在 ChromeParrot 覆盖之前应用 hysteria2 的上限。** 它必须在覆盖之后应用，否则指纹会赢，上限就是惰性的。
7. **不要在缺少其纠正文档的情况下读 `docs/fork/v016-path-mtu-audit.md`。**
8. **不要假定 `-race` 覆盖了分配上限。** 它们按设计由 `!race` 门控。
9. **不要把缓冲区大小做成非 2 的幂。** 它无法入池。
10. **不要移除生产者侧的头空间预留。** 每个数据包都会走替换路径——就是带有历史静默丢弃 bug 的那一条。
11. **不要把低内存几何标签硬编码。** 它从 `appletags -mobile-only` 读取，而 CI 步骤会失败而不是猜测。
12. **不要把仅移动端标签放进共享的 Apple 标签集。**
13. **不要在没有代次的情况下把某个状态 MTU 值当作当前值。** `status.go:816-823` 在错误**之前**读取 `Generations`，并在 `:831` 给它打时间戳，因此更早代次的失败不会被显示为当前值。
14. **不要把 `agentf_hop_scalar_ownership_attack_test.go` 描述成跨 kind 的测试。** 它是状态视图的别名测试。
15. **不要在未检查 `d46717dbd` 的情况下更新 `docs/fork/physicalpath-controlpath-model.md` 的主张。** 它在 `ControlPath` 顺序上也已过期。
16. **给 MASQUE 客户端添加指纹或任何构造后的大小覆盖之前，必须先有一个能捕获被作废上限的测试。** 见 §3。
17. **不要钳制低于 1200 的上限。** 这种值会被拒绝，而不是被钳制（`path_mtu.go:36-41`, `client.go:168-179`, `hysteria2/outbound.go:189-199`）。
18. **不要只凭 `PortMTU()` 决定上层协议的大小**——那会因 WireGuard 的 32 字节而高估预算（`path_mtu.go:169-193`; `mtu_budget_test.go:234-267`）。
19. **不要在任一方向上把内层 MTU 赋给 `initial_packet_size`。** 那个 99 字节的差值（`1280 + 51 − 1232`）由 `outer_quic_mtu_test.go:181-185` 断言。
20. **不要再通过 `c.device` 读取 `PortMTU()`。** nil 设备的 panic 窗口，恰恰就出现在查询这个值的时候（`client.go:635-653`; `port_mtu_lifecycle_test.go:26-40`, `:78-108`）。
21. **不要通告低于 1280 的 PTB MTU**，也不要把 `maxPacketSize` 降到 65575 以下（`session.go:56-61`; `packet_size_bound_test.go:80-150`）。
