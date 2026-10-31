# MTU、数据包大小与缓冲区几何（MTU, packet size and buffer geometry）

**依赖路径范围：** 下文引用的 `common/buf/buffer_standard.go`、`common/buf/buffer_low_memory.go` 与 `common/low_memory*.go` 位于 pinned `github.com/sagernet/sing` 替换中，而不是本仓库根目录。在依赖旧几何或旧行号之前，请重新核对其 `go.mod` pin 与源码。下文的具体 MTU 数值是历史值，除非针对目标配置重新确认。

## v0.1.9 之后 TUIC 下层隧道的上限（TUIC lower-tunnel ceiling after v0.1.9）

**VERIFIED_SOURCE**，测试目标 `011f3fdcd99`：当下层隧道能证明一个上限时，`protocol/tuic/outbound.go` 从 `dialer.DetourPathCapacity` 推导出一个 QUIC payload 上限。它提供或钳制初始数据包大小，针对那个已证明的上限禁用路径 MTU 发现，并拒绝低于 QUIC Initial 最小值的上限。未知路径不会被断言任何上限。代码把结果交给 pinned sing-quic 客户端；这是路径容量约束，而不是对物理链路 MTU 的测量。在推广到其他协议之前，请先看 TUIC 测试与 pinned fork。

对 MASQUE 而言，`protocol/masque/client.go` 已经会从 detour 计算 `outerPayloadCeiling`，并在容量已知时钳制 `HTTP3Options.InitialPacketSize`。因此 2026-10-28 历史审计中“没有计算任何外层上限”的陈述过于宽泛。那个构造函数并没有从这个值赋值 `InitialPacketSizeCeiling`，也没有据此禁用路径 MTU 发现，因此初始钳制不得被呈现为对后续 QUIC 数据包存在硬性上界的证明。这一区分在 `011f3fdcd99` 处是 `VERIFIED_SOURCE`；它不隐含任何真实底层网络或对端测量。

**范围：** `common/bufgeom`（fork 创建）、MASQUE 的 MTU/PTB 路径、hysteria2 的硬上限、WireGuard 的隧道 MTU、`common/physicalpath/status.go` 的 MTU 块，以及低内存几何 profile。

**在改动以下内容时加载本模块：** 任何 MTU 计算、数据包大小上界、缓冲区大小、低内存 profile，或 PTB 处理路径。

---

## 1. 三个 MTU 量，而不是一个（Three MTU quantities, not one）

```text
outer link MTU          what the platform path carries        ← not proven by the MASQUE path model
        │
outer QUIC packet size  what the tunnel's own QUIC layer may put on that path
        │
inner tunnel MTU        what the tunnel promises the payload it carries
```

它们被**分别**计算、限制和证明，其中一个的正确答案并不是另一个的正确答案。把它们混为一谈，正是本节存在所要防止的失败模式。

| # | 量 | 在何处计算 | 在何处受限 | 在随附配置中 |
|---|---|---|---|---|
| 3 | **内层隧道 MTU** | `protocol/masque/client.go:105`（`c.mtu`），由 `PortMTU()` `:654-655` 发布，种子在 `:119-121` | `DefaultMTU = 1280`（`transport/masque/session.go:18`）；设备 `protocol/masque/endpoint.go:88`/`:91` | **1280**——而且它是**配置出来的，不是测量出来的**（`client.go:635-653`） |
| 2 | **外层 QUIC 初始数据包设置** | `client.go` → `min(mtu + 51, MaxUint16)`；服务端镜像 `protocol/masque/server.go` | detour 容量钳制初始设置；低于 1200 的值被拒绝。这不是对所有后续数据包的硬上限 | 在更低的 detour 上限之前是 **1331** |
| 1 | **外层链路 MTU** | 不由这套 MASQUE 计算确立 | 不由这套计算确立 | 就这些输入而言，该路径上**未知** |

**这个 51 是逐项列出的，不是魔法：** `QUICPacketOverhead = 51`（`session.go:19`）= 1 context ID + 8 quarter-stream-ID varint + 16 AEAD + 1 header + 1 packet number + 24 frame，逐项列在 `outer_quic_mtu_test.go:31-46`。

**是不等式断言，不是注释：** outer > inner（`outer_quic_mtu_test.go:73-77`、`:172-176`），以及 `1280 + 51 − 1232 = 99` 恰好成立（`:181-185`）。

**这套 MASQUE 计算为何不能确立外层链路 MTU：** `client.go:146-147` 自己这么说，`PathCapacity` **根本没有链路 MTU 字段**（`path_mtu.go:127-155`），而*确实*被携带的那个上限是一个 **UDP payload** 数字（`path_mtu.go:111-120` = 内层 − 20/40 − 8 − 封装），它比链路 MTU *低*一个层级。`outer_quic_mtu_test.go:222-237` 固定了该选项面没有第三个旋钮。其他组件可以读取接口 MTU，例如 `protocol/bridge/backend_linux.go`；但接口 MTU 本身并不构成端到端 MASQUE 路径 MTU 的证明。

**上限来自路径模型，而不是来自一个常量。** `feat(mtu): cap the MASQUE outer QUIC packet by a proven detour capacity`（`2118ee40`）恰好是两个文件：`client.go` +51 行与 `outer_quic_mtu_test.go` +237。WireGuard 的 `validateTunnelMTU`（`transport/wireguard/endpoint.go:523-569`）只拒绝 IPv6-on-sub-1280——一个 1210 字节的 detour 会产出 1130 的嵌套值，而该 detour 自身需要 1360（`:550-552`）。

**`sing-quic` 对这个上限是承重的。** `scripts/ci/check-go-module-integrity.sh:105-115` 记录了它为何是四个 `REQUIRED_FORK_REPLACES` 之一：

> *"It became load-bearing when the hard packet-size ceiling was given its own field: the ceiling reaches the wire only if this layer forwards it, so a nested module resolving upstream sing-quic would build a product whose proven ceiling silently disappears."*（中文：当硬性数据包大小上限有了自己的字段之后，它就变成承重的：只有这一层把它转发出去，该上限才能到达线路上；因此一个解析到上游 sing-quic 的嵌套模块会构建出一个“已证明的上限静默消失”的产品。）

---

## 2. PTB（数据包过大，packet too big）

确切的路径，来自 `transport/masque/session.go`：

1. 一次发送因过大而失败，而可用大小是 `MaxPayloadSize = MaxDatagramPayloadSize − VarintLen(StreamID/4)`——**此时 context ID 仍被计入**（`transport/http/client_h3.go:486-489`）；
2. 错误被分类（`session.go:401-411`）：**先做 nil 检查，然后做直接类型断言**，`errors.As` 只作为 `:417-426` 处的回退——因为 `errors.As` **会在函数入口把它的目标提升到堆上**，那会让每个*成功的*数据包多付一次分配（`session.go:358-403`，由 `session_datagram_error_test.go:151-171` 守护，其预算是单包写入**恰好 1 次**分配）；
3. `mtu := maxPayloadSize − 1`；
4. **如果 `mtu >= minimumLinkMTU`（1280）**，则用 `datagram.Advance(1)` 撤销 context ID，并运行 `handlePacketTooBig(datagram, mtu)`（`transport/masque/session.go:516-525`）；否则是一个**硬错误**——*"unable to carry 1280 byte packets"*（中文：无法承载 1280 字节的数据包）；
5. 客户端在 `transport/masque/client.go:704-715` 处理它；服务端在 `transport/masque/server.go:579-592`，定向到**所属对端**（`firstPeerAddress :596-605`）并在该会话上排队；ICMP 错误在 `icmp.go:73-85`、`:99-134` 中构建，而目的地址重写会**重新计算校验和**。

**这条不变量，精确表述：** 一个过大的数据报**永远不会被静默丢弃，也永远不会被提升为 capsule**。PTB 通告 `MaxDatagramPayloadSize − VarintLen(StreamID/4) − 1`，**且仅当该值 ≥ 1280**；否则发送方会得到上面那个显式错误。MTU 还会被 `sing-tun/icmp_error.go:113`、`:155` 进一步钳制。

测试：`packet_too_big_test.go`（`:111`、`:175`、`:234`、`:271`、`:294`、`:374`）、`packet_too_big_session_test.go`（`:150`、`:212`、`:270`、`:295`）、`packet_size_bound_test.go:90-186`（`maxPacketSize = 40 + 65535`，`session.go:56-61`）。

**服务端侧头空间：** `server.go:428-430` 报告 `max(PacketHeadroom, handler.FrontHeadroom())`——传输层自己的预留，或者处理器的预留（如果它需要更多）。

---

## 3. hysteria2 的“硬路径上限”（The hysteria2 "hard path ceiling"）

提交 `91ca1652d`——*"fix(hysteria2): a hard path ceiling is its own field, applied after ChromeParrot"*。

**它解决的问题：** ChromeParrot 指纹覆盖会强制 `initialPacketSize = 1250`（`config.go:105-125`），而在 MTU 发现未激活时，**没有别的配置字段能降低首个飞行包大小**。因此一个已证明的 1232 字节上限在指纹打开时毫无效果。

**修复：** 给上限自己的字段（pinned quic-go fork 中的 `Config.InitialPacketSizeCeiling`），并在 ChromeParrot 覆盖**之后**应用它。现在一个已证明的 1232 在指纹打开时也能到达线路上，而同一个字段也限制 MTU 发现可以把数据包大小提升到多大。

**测量与 pin：** `protocol/hysteria2/hard_ceiling_wire_test.go`；pin 在 `go.mod` 中；决策记录是 `docs/fork/v016-final-closure-ledger.md:4-13`。

> **陷阱：那份台账自身的正文与它的横幅相矛盾。** 第 `:465` 节仍然说*"HY2 ChromeParrot is BLOCKED on the pinned quic-go fork"*（中文：HY2 ChromeParrot 在 pinned quic-go fork 上被阻塞），而 `:4-13` 处的头部恰恰撤销了这一点。**横幅是当前的；正文是记录。** 先读横幅。

MASQUE 的构造函数没有设置 `InitialPacketSizeCeiling`：它会从已知的 detour 容量钳制初始偏好，而这并不确立对后续数据包的硬性上界。后来的覆盖也可能让那个初始偏好失效。`protocol/tuic/outbound.go` 现在传入它自己的硬上限，因此该字段并非 hysteria2 专属。改动这些路径时，要在确切的协议与 pinned QUIC 实现下同时测试初始数据包与后续增长。

hysteria2 的这一区分值得精确陈述，因为这三个字段含义不同：

| 字段 | 种类 | 应用方式 |
|---|---|---|
| `InitialPacketSize` | **偏好** | 最先，并被指纹覆盖 |
| `InitialPacketSizeCeiling` | **约束** | **最后**（`quic-go config.go:127-140`），因此它能挺过覆盖，并且还限制 PMTUD 的增长（实测 1232 → 1438，`config.go:184-198`） |
| `DisablePathMTUDiscovery` | **策略** | `hysteria2/outbound.go:148-156`, `:224`; `quicPathSizing :241-280` |

`config.go:109-125` 正是顺序重要的原因：ChromeParrot **用 1250 覆盖大小，且从不读取上限**，因此对默认配置而言早期设置的上限会被丢弃（Parrot 默认开启，`outbound.go:228`）。

---

## 4. `common/bufgeom`——几何契约（the geometry contract）

一个 fork 创建的包（**1,157 行**：75 行非测试 + 4 个测试文件中的 1,082 行测试）。它拥有这样一条规则：为数据包路径获取的缓冲区**恰好可入池**，并且经受 `Advance`/`Prepend` 而**不重新分配其 payload**。

**“几何”在替换后的 `sing` fork 中是一对编译期组合，不是一个运行时开关：**

| Profile | 来源 | 大小 |
|---|---|---|
| default | `common/buf/buffer_standard.go:5-8`（`//go:build !with_low_memory`） | **32 KiB / 16 KiB** |
| low memory | `common/buf/buffer_low_memory.go:5-8`（`with_low_memory`） | **16 KiB / 8 KiB** |

加上 `common/low_memory_stub.go:5` / `low_memory.go:5`（`LowMemory` 为 false/true）。

**历史测量，两个 profile**（已记录的运行，退出码 0；本次校准中没有重新做基准）：

| | BufferSize / UDPBufferSize | `LowMemory` | 每流活跃堆 | 每次获取分配（live/idle/bulk） |
|---|---|---|---|---|
| default | 32768 / 16384 | false | **64.1 KiB** | 1.75 / 0.93 / 1.75 B |
| `,with_low_memory` | 16384 / 8192 | true | **32.1 KiB** | 0.94 / 0.53 / 0.93 B |

对所报告的活跃堆数值而言大约是 2×。记录下来的批量分配值是 6.01 对 5.48 B/KiB；它们并不能证明对每个工作负载或目标都具备几何独立性。历史的解释指向 `copyExtended` 在约 512 KiB 之后的增长（`geometry_bench_test.go:656-724`）。

| 规则 | 锚点 | 坏在哪里 |
|---|---|---|
| 缓冲区恰好可入池 | `geometry_test.go:63 TestGeometryBuffersAreExactlyPoolable` | 一个非 2 的幂的大小**在首次释放时 panic**——`Buffer.Release()` 以 `common.Must(Put(…))` 结束（`buffer.go:307`, `cond.go:294-300`），而在 `MaxPooledBufferSize = 73728`（`alloc.go:11`, `:41-44`）之上，`Get` 返回 nil，失败形态是 nil slice panic |
| `Acquire` 在 `Advance`/`Prepend` 时从不重新分配 payload | `geometry_test.go:99 TestBufferAcquireNeverReallocatesThePayload` | 热路径上每个数据包多一次分配 |
| **分配上限**是被断言的，而不只是被基准测量 | `!race` 那一半 | 一次回归会以数字而不是失败的形式到达评审 |
| 该上限是 **`//go:build !race`**；在 `-race` 下只运行结构性的一半 | `race_detector_test.go:1`, `:25` vs `race_detector_stub_test.go:1`, `:7`; 消费方 `geometry_test.go:147-151` | 该上限在 `-race` 下没有意义：一次池化的 `Get`/`Put` 交接**在 `-race` 下实测 8,251 B/次获取，而没有它时是 1.7**，为容纳这个数字而放宽 1024 字节上限，会抹掉该断言存在所要建立的“池化 vs 重新分配”区分 |
| `buf.New`/`Release` 的 `AllocsPerRun` 是 **1.00，而不是 0.00**，除非调用被内联 | `geometry_test.go:84-98` | 断言 `== 0` 是一个错误期望（内联时 0.00，未内联时 1.00 = 64 B） |
| `UDPBufferSize == BufferSize/2` | `geometry_test.go:48-52` | 数据包路径与流路径对池的判断不一致 |

> **过期的交叉引用：** `geometry_bench_test.go:732` 提到 `TestAllocsPerRunBufferAcquire`，而它**并不存在**。不要去找它。

> **后果：按设计，`-race` 在任何地方都不覆盖字节上限。** 在 `-race` 可用的主机上，这些上限只在非 race 构建中运行。在本次审计主机上 `-race` 完全 `BLOCKED_BY_ENVIRONMENT`，因此 `race` 那一半在任何地方都没有被实际执行。

MASQUE 批次路径添加了它自己的几何规则：数据包**头空间由生产者预留**，以便容纳 1 字节的 context ID（`transport/masque/session.go:561-565`），因为否则每个数据包都走替换路径——也就是带有已记录静默丢弃 bug 的那条路径（`masque-connectip.md` §1）。

---

## 5. 低内存几何 profile（The low-memory geometry profile）

16 KiB 与 32 KiB 的选择是一个**仅移动端的构建标签**，从恰好一个来源读取：

```sh
go run ./cmd/internal/appletags -mobile-only
```

* `cmd/internal/appletags/main.go:33` 定义了它；其文档注释陈述了该包存在的原因：低内存 CI 门禁与内存基准过去读取 `DEFAULT_BUILD_TAGS_OTHERS`，而它*"does not match what libbox builds"*（中文：与 libbox 实际构建的不符），因此那些检查*"were testing a configuration no Apple binary ships"*（中文：在测试一个没有任何 Apple 二进制随附的配置）。
* 该标签通过 **`-tags-not-macos`** 而不是 `-tags` 到达 gomobile：`-tags` 会应用到每个目标，因此把仅移动端标签放在那里，会在混合构建中到达 macOS（`applebuildtags/tags.go:63`, `mobilebuildtags/tags.go:42`）。
* `verify.yml:385-391` 读取它，并在字符串为空时**失败而不是回退**：*"Refusing to fall back to a guess: this step would compile the default geometry while claiming to exercise the low-memory one."*（中文：拒绝回退到猜测：这一步会编译默认几何，却声称在演练低内存几何。）
* `client-macos.yml` 对标签集做**双向**断言——必需的标签存在，退役的标签不存在。

**几何标签是一个真实的行为开关，不是一次大小优化。** 一个声称低内存却编译默认几何的构建，比一个不作此声称的构建更糟。

---

## 6. 状态，以及消费方可以得出什么结论（Status and what a consumer may conclude）

`common/physicalpath/status.go` 按跳（hop）发布一个 MTU 块，每块带一个 `Generations` 字段。状态面是**只读的，并且是重新发布该模型**（`status.go:394-398`, `:638-641`）；其中的值是**拷贝，不是视图**。

**一个状态消费方可以得出结论：** 某一跳最后被证明的值，以及它是在哪一代下被证明的。它必须在把一个值当作当前值之前比较代次。

**代码必须在何处带原因报告 `UNKNOWN`：** 只要内核无法知道某个值——状态面就报告带具名原因的 `UNKNOWN`，而不是给一个默认值；并且只要任何一跳未知，`Path.Exit()` 就报告 `ok=false`，因此一条部分验证的路径不会被误当作已验证的路径（`path-selection-validation.md` §2）。

**`HopStatus` 的标量是拷贝。** `agentf_hop_scalar_ownership_attack_test.go`（`TestAgentFEveryHopScalarIsCopiedNotShared`）砸烂每一个 `PathStatus.Hops` 标量——包括嵌套的 MTU 与 `Generations` 字段——并截断调用方的 slice，从而证明状态值是拷贝。**这个测试与跨 kind 循环无关**；过去把它描述成那样是错误的。

---

## 7. 证据（Evidence）

| 保证 | 等级 | 复检 |
|---|---|---|
| 几何可入池性，以及 `Advance`/`Prepend` 上不重新分配 | `VERIFIED_UNIT` | `go test -count=1 ./common/bufgeom/...` |
| 分配上限 | 仅在 **`!race`** 构建中为 `VERIFIED_UNIT` | 同上 |
| MASQUE 的 PTB 纠正与初始大小钳制 | 对被测试的用例为 `VERIFIED_UNIT`；未确立对后续数据包的普遍硬上限 | `go test -count=1 -timeout 8m -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./transport/masque/... ./protocol/masque/...` |
| hysteria2 的硬上限在 ChromeParrot 打开时到达线路上 | `VERIFIED_UNIT` | `go test -count=1 -run HardCeiling ./protocol/hysteria2/` |
| 低内存几何只从一个来源读取 | `VERIFIED_SOURCE` | `go run ./cmd/internal/appletags -mobile-only` |
| 状态值是拷贝，不是视图 | `VERIFIED_UNIT` | `go test -count=1 -run TestAgentFEveryHopScalarIsCopiedNotShared ./common/physicalpath/` |
| 真实网卡的 MTU 行为 | **本次刷新中为 `UNVERIFIED`**——需要相关的真实 TUN 与环境 | `docs/fork/real-tun-validation.md` |
| 无竞态 | **`BLOCKED_BY_ENVIRONMENT`** | 需要 cgo |

**一个被撤回的结论，记录在此以免被重新推导：** `docs/fork/v016-path-mtu-audit.md` 的第一个 PTB 论证**用错了路径，已被撤回**，撤回见 `docs/fork/v016-path-mtu-audit-correction.md:11-32`——`flow_dispatch.go` 是 *ForwardDispatcher* 分支，而 hysteria2 的 QUIC 套接字走的是 `stack_go_udp_socket.go` 的 `GoUDPConn.transmit`。**读纠正文档，不要读那份审计。**

---

## 8. AI 告诫（AI cautions）

1. **不要把三个 MTU 量合并成一个。**
2. **不要用一个常量去封顶 MASQUE 外层 QUIC 数据包。** 它由一个*已证明的* detour 容量封顶。
3. **不要取消 `sing-quic` 的 pin。** 只有那一层转发，已证明的上限才能到达线路上。
4. **不要静默丢弃一个过大的数据报。** 那是带修正后 MTU 的 PTB。
5. **不要把 MTU 修正到 `minimumLinkMTU` 以下。**
6. **不要在 ChromeParrot 覆盖之前应用 hysteria2 的上限。** 它必须在之后，否则指纹会赢，上限就是惰性的。
7. **不要在缺少其纠正文档的情况下读 `docs/fork/v016-path-mtu-audit.md`。**
8. **不要假定 `-race` 覆盖了分配上限。** 它们按设计由 `!race` 门控。
9. **不要把缓冲区大小做成非 2 的幂。** 它无法入池。
10. **不要移除生产者侧的头空间预留。** 每个数据包都会走替换路径——就是带有历史静默丢弃 bug 的那一条。
11. **不要把低内存几何标签硬编码。** 它从 `appletags -mobile-only` 读取，而 CI 步骤会失败而不是猜测。
12. **不要把仅移动端标签放进共享的 Apple 标签集。**
13. **不要在没有代次的情况下把某个状态 MTU 值当作当前值。** `status.go:816-823` 在错误**之前**读取 `Generations`，并在 `:831` 给它打时间戳，因此来自更早代次的失败不会被显示为当前值。
14. **不要把 `agentf_hop_scalar_ownership_attack_test.go` 描述成跨 kind 的测试。** 它是状态视图的别名测试。
15. **不要在未检查 `d46717dbd` 的情况下更新 `docs/fork/physicalpath-controlpath-model.md` 的主张。** 它在 `ControlPath` 顺序上也已过期。
16. **不要在未先添加一个能捕获被作废上限的测试之前，给 MASQUE 客户端添加指纹或任何构造后的大小覆盖。** 见 §3。
17. **不要钳制一个低于 1200 的上限。** 它会被拒绝，而不是被钳制（`path_mtu.go:36-41`, `client.go:168-179`, `hysteria2/outbound.go:189-199`）。
18. **不要只凭 `PortMTU()` 决定上层协议的大小**——那会因 WireGuard 的 32 字节而高估预算（`path_mtu.go:169-193`; `mtu_budget_test.go:234-267`）。
19. **不要在任一方向上把内层 MTU 赋给 `initial_packet_size`。** 那个 99 字节的差值（`1280 + 51 − 1232`）在 `outer_quic_mtu_test.go:181-185` 处被断言。
20. **不要再次通过 `c.device` 读取 `PortMTU()`。** nil 设备 panic 窗口恰恰就是它被询问的时候（`client.go:635-653`; `port_mtu_lifecycle_test.go:26-40`, `:78-108`）。
21. **不要通告低于 1280 的 PTB MTU**，也不要把 `maxPacketSize` 降到 65575 以下（`session.go:56-61`; `packet_size_bound_test.go:80-150`）。
