# 缓冲区所有权与内存（Buffer ownership and memory）

**依赖路径范围：** 本模块提到的 `common/buf/*`、`common/bufio/*` 和 `common/cond.go`
属于 `github.com/sagernet/sing` 的固定替代版本，不在本仓库根目录。使用历史行号或缓冲池
参数前，应通过 `go.mod` 确定实际使用的 fork 和版本，再核对对应源码。

## v0.1.9 之后 kTLS 读等待的尾部所有权（kTLS read-wait tail ownership after v0.1.9）

**VERIFIED_SOURCE，审阅版本 `011f3fdcd99`。** `common/ktls/ktls_read_wait.go` 属于受
Linux、Go 1.25 和 `badlinkname` 构建条件限制的路径。它在返回应用数据前处理握手后消息；
如果读到数据后又遇到 alert 或 record 错误，`deferReadErrorAfterData` 会先返回数据缓冲区，
把错误留到下一次读取时报告。若在此分支提前释放缓冲区，会丢失尾部数据。参见
`common/ktls/ktls_read_wait_test.go`。Windows 校准没有执行 Linux kTLS 路径，不能据此
验证内核 TLS 行为。

在 splice 钩子路径上，当前的 `common/ktls/ktls.go` 会排空握手后消息；确切的 pinned sing 修订版 `1729220c46c4` 则转发钩子返回的任何字节并继续 splice，而不是把 nil 错误传给 `E.Cause`。这取代了历史上完整审计中报告为 BUG-002/010 的控制流。这是源码证据，不是一次已执行的 Linux kTLS splice 或 KeyUpdate 互操作测试。

**范围：** 这些规则横跨多个模块，包括 `common/sniff`、`common/bufgeom`、`transport/masque`、`route/conn*.go`、`route/splice.go`、`route/packet_destination_guard.go`、`common/trafficsched/gate.go`，以及 `route/reference.go` 中的退役路径。

**来源：** `common/bufgeom` 由 **fork 创建**。所有权文档以及普查/有界拷贝测试都是 fork 的工作。许多具体规则沿用上游设计，由 fork 新增的测试验证。

**修改零拷贝、内存分配、缓冲池、批次所有权或 splice 路径前，请先阅读本模块。** 这些优化可能改变缓冲区的生命周期，必须同时检查释放责任和失败路径。

---

## 1. 所有权规则与违反后的风险（The rules, each with what breaks）

### 1.1 缓冲区获取与几何（Buffer acquisition and geometry）

| # | 规则 | 锚点 | 违反后的风险 |
|---|---|---|---|
| B1 | `buf.NewPacket()` = 从 **12 个 `sync.Pool`** 之一 `Get(UDPBufferSize)`；`MaxPooledBufferSize = 73728` | `common/buf/alloc.go:11,19-36`; `buffer_standard.go:7` | 见 B1a/B1b——失败形态是 **panic**，不是泄漏 |
| B1a | `Put` 只接受 `cap == MaxPooledBufferSize` **或 [64, 65536] 内的 2 的幂** | `common/buf/alloc.go:89-97` | 受管理且没有引用的缓冲区如果容量不符合入池要求，释放时会 panic：`Buffer.Release()` 调用 `common.Must(Put(…))`；nil、非受管理或仍有引用的缓冲区会提前返回 |
| B1b | 超过 `MaxPooledBufferSize` 时，`Get` 返回 **nil** | `common/buf/alloc.go:41-44` | `buffer.go:343-369` 内部的 nil slice panic |
| B2 | `Buffer.Bytes()` 是底层数组上的一个**视图**；符合条件时 `Release()` 把它归还池中，并清零 **Buffer 结构体**，而不是清零底层字节 | `common/buf/buffer.go` `Bytes`, `Release` | 保留的 slice 可能读到复用后的数据；再次释放已清零的结构体是空操作，因此不能依赖它发现所有权错误 |
| B3 | `ReadOnceFrom` 在缓冲区满时返回 `io.ErrShortBuffer` | `common/buf/buffer.go:137-144` | 这是 `PeekStream` 的字节上限——把它当成错误、而不是上界来处理，会改变嗅探行为 |
| B4 | 池化的缓冲区必须经过 `Advance`/`Prepend` 而**不重新分配 payload** | `common/bufgeom/geometry_test.go:99 TestBufferAcquireNeverReallocatesThePayload`, `:63 TestGeometryBuffersAreExactlyPoolable` | 热路径上每个数据包多一次分配 |
| B5 | 分配**上限**断言是 `//go:build !race`；在 `-race` 下只运行结构性的一半 | `common/bufgeom/race_detector_test.go` (`//go:build race`) vs `race_detector_stub_test.go` (`//go:build !race`) | 在 `-race` 下，一次池化的 `buf.Get`/`Put` 循环**实测每次获取 8,251 字节，没有它时是 1.7**；为容纳这个数字而把 1024 字节上限放宽，会抹掉该断言要建立的“池化 vs 重新分配”区分 |

关于几何的更多细节——包括两个 profile 之间实测的 2× 差异——见 `mtu-and-geometry.md`。

### 1.2 借用视图与保留的内存（Borrowed views and retained memory）

| # | 规则 | 锚点 | 违反后的风险 |
|---|---|---|---|
| B6 | `payloadReader` 游标借用分段，**其生命周期不得超过创建它的 `PeekStream` 调用** | `common/sniff/sniff.go:88-91` | 保留该游标的嗅探器会观察到已被复用的池缓冲区 |
| B7 | `takePacketBytes` 返回**调用方数据报内的一个视图**，只在该数据报存活期间有效 | `common/sniff/quic.go:23-35` | 被保留下来的 DCID/PN 采样会读到已被复用的内存 |
| B8 | QUIC 的工作副本是一次**新的、数据包大小的 `make`，刻意不入池**；解密后的缓冲区与它互为别名 | `common/sniff/quic.go:184`, `:227` | 如果将工作副本放入池，保留的分段可能超过其所有者的生命周期 |
| B9 | 被保留的 `qCryptoFragment.payload` 与**工作副本**互为别名，从不与调用方的数据报；重组缓冲区由 `defer` 释放 | `common/sniff/quic.go:306-307`, `:342-343`, `:477-481` | 保留的分段会在数据报 `Release` 之后仍然存活 |
| B10 | 头部保护会**原地写入 4 个字节**；`Length` 覆盖不到这些字节的头部会被拒绝 | `common/sniff/quic.go:125-131`, `:186-189` | 向数据报尾部越界写入 |

B6–B10 的依据包括**地址范围测试**（`quic_ownership_test.go:314-350`）、**数据报投毒后的等价性测试**（`:206-281`、`quic_boundary_test.go:427-456`），以及验证 `cap` 上界不随合并尾部增长的测试（`sniff_bounded_copy_internal_test.go:120-156`）。另见 `sniff.md` §2。

### 1.3 所有权转移与批次（Ownership transfer and batch）

| # | 规则 | 锚点 | 违反后的风险 |
|---|---|---|---|
| B11 | 每个批次**块（chunk）**都是全有或全无：被接受的前缀已经转移，只有被拒绝的剩余部分留在调用方 | `transport/masque/session.go`, `writeBatchOwned` | 重放一个已被接受的前缀会导致重复发送，或释放已转移的缓冲区 |

| B12 | 在**自有数据报（owned-datagram）**路径上，所有权**在成功时转移**，因此完全跳过释放 | `transport/masque/session.go:496-500`; `transport/http/client_h3.go:507` | **重复释放 / use-after-free** |
| B13 | 一次 `PrependContextID` **替换会释放原件**，因此代码必须在尝试替换之前拒绝受影响的块，并原地更新调用方的 slice | `transport/masque/session.go`, `writeOwnedBatchChunk` | 悬垂指针、重复释放，或回退时丢包 |
| B14 | `writePackets` 中每一条失败路径都**恰好释放仍然拥有的剩余部分** | `transport/masque/session.go:530-533` | 池化缓冲区泄漏 |
| B15 | 批次中**只有一个数据包**时，不走批量发送路径 | `transport/masque/session.go:472-477` | 一次实测到的回归 |
| B16 | 数据包头空间由**生产者**预留，以便容纳 1 字节的 context ID | `transport/masque/session.go:584-588`; `protocol/masque/client.go:594 FrontHeadroom`; `transport/masque/server.go:428 receivedPacketHeadroom` | 每个数据包都走 B13 的替换路径 |
| B17 | `matchRule` 的数据包缓冲区属于调用方，必须在**全部五条**错误路径上释放 | `route/route.go:394-400`, `:412`, `:422`, `:426`, `:440`; 注释 `:396-399` 记录了测量结果：**释放 1,817 字节 vs 未释放 18,255 字节** | 可能导致 16 KiB 的池化缓冲区未及时归还 |
| B18 | 释放点是显式的，不靠推断：`buf.ReleaseMulti` (`route/route.go:146,156,160,184`)、`N.ReleaseMultiPacketBuffer` (`route/route.go:400,412,422,426,440`; `route/dns.go:44`)、`route/fakeip_conn.go:205`、`protocol/masque/{client,server}.go`、`transport/masque/client.go`、`common/listener/listener_udp.go`、`common/iponly/conn.go`、`common/trafficsched/gate.go:135,353,376`、`transport/device/*`、`transport/wireguard/device_peer_router.go` | 如所引 | 泄漏 |
| B19 | 重复释放防护存在，必须保留：`route/conn_reclaim.go` (`managedConnState`, `markKernelOwned:114`, `reclaimState:118`, `touch:125`, `countBytes:136`) 与 `route/splice.go` (`spliceSource.takeCached:145`) | 如所引 | 一条 splice 连接的缓存数据包被释放两次 |

关于块的陈述讲的是**所有权**，而不是恰好一次投递。在 pinned 的 quic-go HTTP/3 拷贝回退中，一个批次可能在后一次发送失败之前，就已经把更早的数据报入队；即使其中一些拷贝可能已经入队，非 nil 的结果仍会把全部 payload 所有权留给调用方。无头空间的拷贝路径现在会在成功时释放原始 payload，出错时不释放。参见 pinned `http3/conn.go` 的 `sendDatagramOwned` 与 `sendDatagramsOwned`；不要从所有权契约推断线路级原子性。

### 1.4 生命周期、池与退役（Lifetime, pools and retirement）

| # | 规则 | 锚点 | 违反后的风险 |
|---|---|---|---|
| B20 | `IdleConnectionReleaser` 只能关闭**没有活跃用户流量**的资源，而且这次遍历**永远不能拨号** | `adapter/outbound.go:214-219`; `route/reference.go:420-440` | 裁剪可能触发重连，违反内存回收路径不得拨号的约束（`route/reference.go:394-411`） |
| B21 | `ReuseSuspect.RetireSuspect()` 在 `CloseIdleConnections` **之前**运行；一个只丢弃空闲连接的池会退回旧行为 | `route/reference.go:496-511`, `:547-556` | 新流可能被分配到状态可疑的会话 |
| B22 | 退役遍历断言的是 `IdleConnectionReleaser`，**而不是**捆绑的 `IdleConnectionKeeper` | `route/reference.go:479-484` | 若断言组合接口，遍历会静默跳过 HTTP 客户端池；该问题曾持续四轮评审（提交 `93823c3d`, `8735b1d`） |
| B23 | `route/reusable_owner_inventory_test.go` 检查具名可复用所有者的能力，包括 XHTTP | 如所引 | 削弱或移除某个具名所有者的退役能力，可能静默绕过该遍历 |
| B24 | 作用域清理从不在持有作用域锁时运行 | `adapter/lifecycle.go:124-125`, `:133-134` | 重入死锁 |
| B25 | `RebindLease.Complete()` **总是取消**，即使对一个已被取代的租约也是如此 | `common/runtimecoord/coordinator.go:307-309` | 取消函数可能一直注册在仍然活动的父级上 |
| B26 | 池内省通过 **badlinkname** 镜像固定工具链的 `sync.Pool` 布局；大小断言会在布局变化时明确报错 | `experimental/libbox/internal/runtimeinfo/pool_badlinkname.go:14,44-47` | 会静默误读池内部结构 |

---

## 2. splice 路径——已修正的建立期交接（The splice path — corrected setup handover）

`docs/internal/fork-splice-silent-stall.md` 不可替代：它是这个缺陷及其修复的唯一记录。

* 历史缺陷在 `Pipe2` 成功之前就报告交接，于是一次建立失败会阻止用户态回退，并可能在不搬运任何数据的情况下停掉拷贝。
* 在 pinned `sing` 修订版 `1729220c46c4` 中，`common/bufio/splice_linux.go` 在 `splicePipe2` 失败时返回 `handed=false`，只在管道存在之后才置 `handed=true`。`splice_actual_linux_test.go` 包含一个强制失败的回归测试。这一点已在该 pin 上通过源码验证；本次 Windows 刷新没有执行 Linux 运行。
* splice 计数器衡量的是**接受**，而不是一次内核 splice（`c02d7cc8e` 把它们重命名为 `spliceReasonHandoverAccepted` / `Accepted` / `AcceptanceRatio()`）；另有一个源码级测试禁止旧的 `Successes` / `successes=` / `"splice success"` / `"splice ratio"` 名称。

决定内核 splice 能否介入的条件包括调用顺序、接口契约和失败后的回退行为：

* **先尝试 `copyDirect`，再尝试 `copyExtended`。** `CopyWithCounters` 先调用 `copyDirect`，只有它返回 `handed == false` 时才回退，因此任一端的读等待者都不会抢占系统调用路径。
* **`ktls.Conn` 是 `SyscallReader`/`SyscallWriter` 的唯一实现者。** 接口将 `SyscallConnForRead()` 和 `HandleSyscallReadError(err)` 作为同一契约，要求实现者在提供 fd 的同时处理读取时遇到的非应用数据记录，不能只实现其中一部分。
* 一条没有 kTLS 的裸连接会**显式回退**：`splice` 会走到 `else if !notFirstTime && E.IsMulti(err, unix.EINVAL, unix.ENOSYS) { handed = false; return }`（`splice_linux.go:68-70`），因此一个只有 `syscall.Conn`、无法 splice 的源，会在**第一次**尝试时让位给用户态拷贝。

`common/ktls` 由 `linux && go1.25 && badlinkname` 门控。这个包现在有回归测试，包括 `common/ktls/ktls_read_wait_test.go`；这些测试需要 Linux/带标签的环境，本次 Windows 刷新没有执行。

**本次环境无法运行这些测试。** 以下两个 kTLS 读取路径的历史缺陷通过对照快、慢路径发现，修复参考了另一条路径的实现；本次未通过运行验证：

| 位置 | 缺陷 | 后果 |
|---|---|---|
| `ktls_read_wait.go:35` | `Input.Len() == 0 && Input.Len() > 0` —— 不可满足 | close-notify 清理从未运行，于是一次读取返回 `(n, nil)`，而不是 `(n, EOF)` |
| `ktls_read_wait.go:22-27` | `readRecord` 之后没有做 `Hand` 排空 | 一次 TLS 1.3 KeyUpdate 从未被应用，于是内核保留旧的 RX 密钥，而对端已经前进 |

`Conn.Read` 与 `Conn.WaitReadBuffer` 实现同一份契约，需要对照检查两条路径处理记录序列的行为。历史审计指出了缺少这种比较的问题；该包现在已有 `ktls_read_wait_test.go`，过去“零测试”的说法已经过期。但测试文件的存在不等于所有记录序列都已验证，本次 Windows 刷新也没有执行需要 Linux 和构建标签的运行时比较。

---

## 3. 零拷贝目标——什么是可观测的，什么不是（The zero-copy objective — what is observable and what is not）

`docs/internal/zerocopy-closure-ledger.md`。

| 级别 | 可观测自 | 状态 |
|---|---|---|
| `splice_eligible` | sing-box | 已交付——用户态包装器的遍历 |
| `handover_accepted` | sing-box | 已交付（`c02d7cc8e`） |
| `copy_direct_selected` | **仅 fork**——`copyDirect` 的 `handled`，在 fork 内部消费 | 无法从本仓库观测 |
| `actual_kernel_splice_observed` | **仅 fork**——`splice()` 的 `n > 0`，从不离开该包 | 无法从本仓库观测 |

> **零拷贝的第 3–5 级在结构上无法从 sing-box 观测**；而用 `handed` 构建的普查“会以美化这项改动的方向出错”。不要构建它。

在所审阅的代码树中没有找到数据包克隆路径；找到的 `Clone()` 用于配置、TLS 或缓冲区。

**Shadowsocks 的稳态读侧拷贝根据历史测量予以保留。** 这次拷贝位于依赖内部，不产生分配，测量成本处于噪声范围。移除它需要使用不符合池容量要求的 `length + Overhead`，从而产生约 32 KiB/次读取的未池化分配。修改前应核对固定依赖的 `buf.Put` 规则和目标环境的测量。

---

## 4. 证据（Evidence）

| 保证 | 等级 | 复检 |
|---|---|---|
| 几何可入池性，以及 Advance 之后不重新分配 | `VERIFIED_UNIT` | `go test -count=1 ./common/bufgeom/...` |
| 分配上限 | **仅在 `!race` 构建中**为 `VERIFIED_UNIT` | 同上；`race` 那一半需要支持 cgo 的主机 |
| 嗅探所有权（借用游标、保留的 cap、原地解掩码） | `VERIFIED_UNIT` + fuzz | `go test -count=1 ./common/sniff/...` |
| MASQUE 原子块、已转移前缀与替换拒绝 | `VERIFIED_UNIT` | `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./transport/masque/...` |
| 经由计数器包装器丢失批次能力 | 带真实红色对照的 `VERIFIED_UNIT` | `route/udp_batch_capability_test.go`（3 个测试） |
| 退役遍历到达全部四个池 | `VERIFIED_UNIT` | `route/reference_coverage_stress_test.go`, `route/reusable_owner_inventory_test.go` |
| 无竞态 | **`BLOCKED_BY_ENVIRONMENT`**——`-race` 需要 cgo，而没有 C 编译器 | Linux/macOS 或 Windows+gcc |
| kTLS 读路径修复 | **`VERIFIED_SOURCE`**——与参照文本一致，且该包在此无法编译 | 需要 Linux |
| `unix.Splice()` 真正在运行 | **此处为 `UNVERIFIED`**——sing fork 声明了 `linux-splice-proof`，但本次校准没有检查到精确 pin 的运行 | 检查一个匹配的工作流运行 |
| splice 静默停滞 | **在 sing pin 中已修复**——当前根替换解析到 `1729220c46c4`，其 `splice_linux.go` 在 `Pipe2` 失败时返回未交接。`docs/internal/fork-splice-silent-stall.md` 是历史记录。 | 检查确切的 pinned `common/bufio/splice_linux.go` 并运行其 Linux 回归测试 |

---

## 5. AI 告诫（AI cautions）

1. **借用的游标不能在 `PeekStream` 调用结束后继续使用。** 不要缓存一个 `payloadReader`。
2. **不要把 QUIC 工作副本入池。** 它是每次调用新建的 `make`，这是有原因的。
3. **不要在调用方的数据报被释放之后仍保留对它的视图。**
4. **不要丢掉 `Length < hdrLen+4` 防护。**
5. **不要在调用方的数据报内部解掩码。**
6. **移除任何拷贝之前，先找到要求它存在的那条规则。** 有两个例子说明拷贝/别名边界本身就是正确性条件，而不是优化目标：MASQUE 的替换，以及 `takePacketBytes` 的视图。
7. **不要改自有数据报的释放语义。** 所有权已经转移；在那里再释放一次就是双重释放。
8. **不要在原地“修复”一次 `PrependContextID` 替换。**
9. **不要移除生产者侧的头空间预留。**
10. **不要为了消除 `io.ErrShortBuffer` 而扩大嗅探缓冲区。** 该错误用于表示已达到读取上限。
11. **不要在只实现了 `IdleConnectionReleaser` 的地方断言 `IdleConnectionKeeper`。**
12. **对任何新增的 `ReuseSuspect` 所有者，都要对照退役遍历及其测试做审查。**
13. **不要让一次裁剪拨号、唤醒被挂起的工作、重置网络或终止活跃的流。**
14. **不要用 `handed` 构建普查。** 它衡量的是“系统调用路径被选中了”。
15. **非 2 的幂池约束没有解决之前，不要关掉 Shadowsocks 稳态拷贝**；它是一次刻意的、经过测量的保留。
16. **不要假定 `-race` 覆盖了分配上限。** 它们按设计由 `!race` 门控。
17. **不要把受平台门控的包当作已被无标签运行测试过。** `common/ktls` 是一条数据面路径，它的 Linux/带标签测试需要正确的环境。
18. **保持 splice 交接契约：** 在消费 payload 之前的失败必须允许回退；已经交接的流不得被重放。
