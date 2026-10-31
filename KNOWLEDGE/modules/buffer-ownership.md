# 缓冲区所有权与内存（Buffer ownership and memory）

**依赖路径范围：** 本模块引用的 `common/buf/*`、`common/bufio/*` 与 `common/cond.go` 是 pinned `github.com/sagernet/sing` 替换中的路径，而不是本仓库根目录。请从 `go.mod` 解析出当前生效的 fork 与版本；旧行号与池几何在使用前需要针对那个确切的 pinned 源码重新核对。

## v0.1.9 之后 kTLS 读等待的尾部所有权（kTLS read-wait tail ownership after v0.1.9）

**VERIFIED_SOURCE**，测试目标 `011f3fdcd99`：受 Linux/Go 1.25/`badlinkname` 门控的 `common/ktls/ktls_read_wait.go` 会在返回数据之前排空握手后消息。如果一个 alert 或 record 错误跟随在已经读取的数据之后，`deferReadErrorAfterData` 会先归还缓冲区，并把该错误排队给下一次读取；在该分支上释放它就会丢失真实的尾部字节。见 `common/ktls/ktls_read_wait_test.go`。这些仅限 Linux 的路径没有在 Windows 校准中执行；Windows 上的 Go 测试无法验证内核 TLS 行为。

在 splice 钩子路径上，当前的 `common/ktls/ktls.go` 会排空握手后消息，而确切的 pinned sing 修订版 `1729220c46c4` 会转发钩子返回的任何字节并继续 splice，而不是把一个 nil 错误传给 `E.Cause`。这取代了历史上完整审计中报告为 BUG-002/010 的控制流。这是源码证据，而不是一次已执行的 Linux kTLS splice 或 KeyUpdate 互操作测试。

**范围：** 横切。规则分布在 `common/sniff`、`common/bufgeom`、`transport/masque`、`route/conn*.go`、`route/splice.go`、`route/packet_destination_guard.go`、`common/trafficsched/gate.go`，以及 `route/reference.go` 中的退役路径。

**来源：** `common/bufgeom` 是 **fork 创建**的。所有权文档以及普查/有界拷贝测试都是 fork 的工作。其中许多单条规则是上游的结构，由 fork 新增的测试来防守。

**在任何零拷贝、分配、缓冲池、批次所有权或 splice 改动之前加载本模块。** 本模块的全部目的就是拦住一次好意的优化。

---

## 1. 规则，以及各自被违反时坏在哪里（The rules, each with what breaks）

### 1.1 缓冲区获取与几何（Buffer acquisition and geometry）

| # | 规则 | 锚点 | 坏在哪里 |
|---|---|---|---|
| B1 | `buf.NewPacket()` = 从 **12 个 `sync.Pool`** 之一 `Get(UDPBufferSize)`；`MaxPooledBufferSize = 73728` | `common/buf/alloc.go:11,19-36`; `buffer_standard.go:7` | 见 B1a/B1b——失败形态是 **panic**，不是泄漏 |
| B1a | `Put` 只接受 `cap == MaxPooledBufferSize` **或 [64, 65536] 内的 2 的幂** | `common/buf/alloc.go:89-97` | 一个受管理、未被引用但容量不可入池的缓冲区，在释放时会 panic：`Buffer.Release()` 调用 `common.Must(Put(…))`；nil、非受管理或仍被引用的缓冲区会更早返回 |
| B1b | 超过 `MaxPooledBufferSize` 时，`Get` 返回 **nil** | `common/buf/alloc.go:41-44` | `buffer.go:343-369` 内部的 nil slice panic |
| B2 | `Buffer.Bytes()` 是数组上的一个**视图**；符合条件时 `Release()` 把它归还池中，并把 **Buffer 结构体**清零，而不是把底层字节清零 | `common/buf/buffer.go` `Bytes`, `Release` | 被保留的 slice 可能观察到已被复用的数据；通过已清零的结构体再做一次释放是空操作，因此不要依赖它来发现每一次所有权错误 |
| B3 | `ReadOnceFrom` 在缓冲区满时返回 `io.ErrShortBuffer` | `common/buf/buffer.go:137-144` | 这是 `PeekStream` 的字节上限——把它当作错误而不是一个上界来处理，会改变嗅探行为 |
| B4 | 池化的缓冲区必须经受 `Advance`/`Prepend` 而**不重新分配其 payload** | `common/bufgeom/geometry_test.go:99 TestBufferAcquireNeverReallocatesThePayload`, `:63 TestGeometryBuffersAreExactlyPoolable` | 热路径上每个数据包多一次分配 |
| B5 | 分配**上限**断言是 `//go:build !race`；在 `-race` 下只运行结构性的一半 | `common/bufgeom/race_detector_test.go` (`//go:build race`) vs `race_detector_stub_test.go` (`//go:build !race`) | 在 `-race` 下，一次池化的 `buf.Get`/`Put` 循环**实测每次获取 8,251 字节，而没有它时是 1.7**；为容纳这个数字而放宽 1024 字节上限，会抹掉该断言存在所要建立的“池化 vs 重新分配”区分 |

关于几何的更多细节——包括两个 profile 之间实测的 2× 差异——见 `mtu-and-geometry.md`。

### 1.2 借用视图与保留的内存（Borrowed views and retained memory）

| # | 规则 | 锚点 | 坏在哪里 |
|---|---|---|---|
| B6 | `payloadReader` 游标借用分段，**不得比创建它的 `PeekStream` 调用存活更久** | `common/sniff/sniff.go:88-91` | 保留该游标的嗅探器会观察到已被复用的池缓冲区 |
| B7 | `takePacketBytes` 返回**调用方数据报内的一个视图**，只在该数据报存活期间有效 | `common/sniff/quic.go:23-35` | 被保留的 DCID/PN 采样会读到已被复用的内存 |
| B8 | QUIC 的工作副本是一次**新的、数据包大小的 `make`，刻意不入池**；解密后的缓冲区与它互为别名 | `common/sniff/quic.go:184`, `:227` | 把它池化会让一个分段比其所有者存活更久 |
| B9 | 被保留的 `qCryptoFragment.payload` 与**工作副本**互为别名，从不与调用方的数据报；重组缓冲区由 `defer` 释放 | `common/sniff/quic.go:306-307`, `:342-343`, `:477-481` | 被保留的分段会活过数据报的 `Release` |
| B10 | 头部保护会**原地写入 4 个字节**，因此无法到达它们的 `Length` 会被拒绝 | `common/sniff/quic.go:125-131`, `:186-189` | 向数据报尾部越界写入 |

B6–B10 是被证明的，而不是被断言的：一个**地址范围**测试（`quic_ownership_test.go:314-350`）、**数据报投毒**等价性测试（`:206-281`、`quic_boundary_test.go:427-456`），以及一个不会随合并尾部增长的 `cap` 上界（`sniff_bounded_copy_internal_test.go:120-156`）。见 `sniff.md` §2。

### 1.3 所有权转移与批次（Ownership transfer and batch）

| # | 规则 | 锚点 | 坏在哪里 |
|---|---|---|---|
| B11 | 每个批次**块（chunk）**都是全有或全无；被接受的前缀已经转移，只有被拒绝的剩余部分留在调用方 | `transport/masque/session.go`, `writeBatchOwned` | 重放一个已被接受的前缀会导致重复发送，或释放已转移的缓冲区 |

| B12 | 在**自有数据报（owned-datagram）**路径上，所有权**在成功时转移**，因此释放被完全跳过 | `transport/masque/session.go:496-500`; `transport/http/client_h3.go:507` | **重复释放 / use-after-free** |
| B13 | 一次 `PrependContextID` **替换会释放原件**，因此它必须在尝试替换之前拒绝受影响的块，并原地更新调用方的 slice | `transport/masque/session.go`, `writeOwnedBatchChunk` | 悬垂指针、重复释放，或回退时丢包 |
| B14 | `writePackets` 中每一条失败路径都**恰好释放仍然拥有的剩余部分** | `transport/masque/session.go:530-533` | 池化缓冲区泄漏 |
| B15 | **单个**的批次永远不走批次路径 | `transport/masque/session.go:472-477` | 一次实测到的回归 |
| B16 | 数据包头空间由**生产者**预留，以便容纳 1 字节的 context ID | `transport/masque/session.go:584-588`; `protocol/masque/client.go:594 FrontHeadroom`; `transport/masque/server.go:428 receivedPacketHeadroom` | 每个数据包都走 B13 的替换路径 |
| B17 | `matchRule` 的数据包缓冲区属于调用方，必须在**全部五条**错误路径上释放 | `route/route.go:394-400`, `:412`, `:422`, `:426`, `:440`; 注释 `:396-399` 记录了测量结果：**释放 1,817 字节 vs 未释放 18,255 字节** | 一次 16 KiB 的池化分配永远不会被回收 |
| B18 | 释放点是显式的，不是推断出来的：`buf.ReleaseMulti` (`route/route.go:146,156,160,184`)、`N.ReleaseMultiPacketBuffer` (`route/route.go:400,412,422,426,440`; `route/dns.go:44`)、`route/fakeip_conn.go:205`、`protocol/masque/{client,server}.go`、`transport/masque/client.go`、`common/listener/listener_udp.go`、`common/iponly/conn.go`、`common/trafficsched/gate.go:135,353,376`、`transport/device/*`、`transport/wireguard/device_peer_router.go` | 如所引 | 泄漏 |
| B19 | 重复释放防护存在，必须保留：`route/conn_reclaim.go` (`managedConnState`, `markKernelOwned:114`, `reclaimState:118`, `touch:125`, `countBytes:136`) 与 `route/splice.go` (`spliceSource.takeCached:145`) | 如所引 | 一条 splice 连接的缓存数据包被释放两次 |

关于块的陈述关乎**所有权**，而不是恰好一次投递。在 pinned 的 quic-go HTTP/3 拷贝回退中，一个批次可能在后一次发送失败之前就已经把更早的数据报入队；非 nil 的结果会把全部 payload 所有权留给调用方，即使其中一些拷贝可能已经入队。无头空间的拷贝路径现在会在成功时释放原始 payload，但在错误时不释放。见 pinned `http3/conn.go` 的 `sendDatagramOwned` 与 `sendDatagramsOwned`；不要从所有权契约推断线路级原子性。

### 1.4 生命周期、池与退役（Lifetime, pools and retirement）

| # | 规则 | 锚点 | 坏在哪里 |
|---|---|---|---|
| B20 | `IdleConnectionReleaser` 只能关闭**没有活跃用户流量**的资源，并且该遍历**永远不能拨号** | `adapter/outbound.go:214-219`; `route/reference.go:420-440` | 一次裁剪变成重连触发器——这正是内存路径绝不能有的形态（`route/reference.go:394-411`） |
| B21 | `ReuseSuspect.RetireSuspect()` 在 `CloseIdleConnections` **之前**运行；一个只会丢弃空闲连接的池会得到旧行为 | `route/reference.go:496-511`, `:547-556` | 新流附着到一段没人信任的会话上 |
| B22 | 退役遍历断言的是 `IdleConnectionReleaser`，**而不是**捆绑的 `IdleConnectionKeeper` | `route/reference.go:479-484` | 断言那个捆绑接口在运行时于**每一次**遍历上都静默失败，导致 HTTP 客户端池在四轮评审中都不可达（提交 `93823c3d`, `8735b1d`） |
| B23 | `route/reusable_owner_inventory_test.go` 检查具名可复用所有者的能力，包括 XHTTP | 如所引 | 削弱或移除某个具名所有者的退役能力，可能静默绕过该遍历 |
| B24 | 作用域清理从不在持有作用域锁时运行 | `adapter/lifecycle.go:124-125`, `:133-134` | 重入死锁 |
| B25 | `RebindLease.Complete()` **总是取消**，即使对一个已被取代的租约也是如此 | `common/runtimecoord/coordinator.go:307-309` | 一个 cancel func 被留在活动父级上注册着 |
| B26 | 池内省是 pinned 工具链 `sync.Pool` 布局的 **badlinkname** 镜像，并带有一个在布局变化时大声失败的大小断言 | `experimental/libbox/internal/runtimeinfo/pool_badlinkname.go:14,44-47` | 对池内部结构的静默误读 |

---

## 2. splice 路径——已修正的建立期交接（The splice path — corrected setup handover）

`docs/internal/fork-splice-silent-stall.md` 不可替代：它是这个缺陷及其修复的唯一记录。

* 历史缺陷在 `Pipe2` 成功之前就报告交接，因此一次建立失败会阻止用户态回退，并可能在不搬运任何数据的情况下停掉拷贝。
* 在 pinned `sing` 修订版 `1729220c46c4` 中，`common/bufio/splice_linux.go` 在 `splicePipe2` 失败时返回 `handed=false`，并且只在管道存在之后才置 `handed=true`。`splice_actual_linux_test.go` 包含一个强制失败的回归测试。这是在该 pin 上源码验证的；本次 Windows 刷新中没有执行 Linux 运行。
* splice 计数器衡量的是**接受**，而不是一次内核 splice（`c02d7cc8e` 把它们重命名为 `spliceReasonHandoverAccepted` / `Accepted` / `AcceptanceRatio()`），并且有一个源码级测试禁止旧的 `Successes` / `successes=` / `"splice success"` / `"splice ratio"` 名称。

两个决定内核 splice 究竟能否介入的事实，每一条都是检查过的而不是假定的：

* **`copyDirect` 在 `copyExtended` 之前尝试。** `CopyWithCounters` 先调用 `copyDirect`，只有当它返回 `handed == false` 时才回退，因此任一端上的读等待者都不会抢占系统调用路径。
* **`ktls.Conn` 是 `SyscallReader`/`SyscallWriter` 的唯一实现者**，并且该接口把 `SyscallConnForRead()` 与 `HandleSyscallReadError(err)` 耦合在**同一份**契约里——因此一个类型无法只交出裸 fd 而不说明如何处理在它上面到达的非应用数据记录。不存在部分实现的表达方式。
* 一条没有 kTLS 的裸连接会**显式回退**：`splice` 会走到 `else if !notFirstTime && E.IsMulti(err, unix.EINVAL, unix.ENOSYS) { handed = false; return }`（`splice_linux.go:68-70`），因此一个只有 `syscall.Conn`、无法 splice 的源，会在**第一次**尝试时让位给用户态拷贝。

`common/ktls` 由 `linux && go1.25 && badlinkname` 门控。它现在有回归测试，包括 `common/ktls/ktls_read_wait_test.go`；这些测试需要 Linux/带标签的环境，本次 Windows 刷新中没有执行。

**因为在这里无法运行，两个 kTLS 读路径缺陷是通过把快路径与其慢路径做比较发现的，并通过镜像而不是通过执行来修复：**

| 位置 | 缺陷 | 后果 |
|---|---|---|
| `ktls_read_wait.go:35` | `Input.Len() == 0 && Input.Len() > 0` —— 不可满足 | close-notify 清理从未运行，因此一次读取返回 `(n, nil)` 而不是 `(n, EOF)` |
| `ktls_read_wait.go:22-27` | `readRecord` 之后没有 `Hand` 排空 | 一次 TLS 1.3 KeyUpdate 从未被应用，因此内核保留了旧的 RX 密钥，而对端已经前进 |

**根本原因是缺少这次比较，而不是两个笔误中的任何一个。** `Conn.Read` 与 `Conn.WaitReadBuffer` 是同一份契约的两个实现。历史审计建议做一次共享的记录序列比较；该包现在包含 `ktls_read_wait_test.go`，因此它过去“零测试”的说法已经过期。仅凭测试存在并不能为每一种记录序列确立相同行为；Linux/带标签的运行时比较没有在本次 Windows 刷新中执行。

---

## 3. 零拷贝目标——什么是可观测的，什么不是（The zero-copy objective — what is observable and what is not）

`docs/internal/zerocopy-closure-ledger.md`。

| 级别 | 可观测自 | 状态 |
|---|---|---|
| `splice_eligible` | sing-box | 已交付——用户态包装器的遍历 |
| `handover_accepted` | sing-box | 已交付（`c02d7cc8e`） |
| `copy_direct_selected` | **仅 fork**——`copyDirect` 的 `handled`，在 fork 内部消费 | 无法从本仓库观测 |
| `actual_kernel_splice_observed` | **仅 fork**——`splice()` 的 `n > 0`，从不离开该包 | 无法从本仓库观测 |

> **零拷贝的第 3–5 级在结构上无法从 sing-box 观测**，而用 `handed` 构建的普查“会以美化这项改动的方向出错”。不要构建它。

**“数据包克隆”是空谈。** 仓库中不存在数据包克隆路径；每一个 `Clone()` 都是配置/TLS/缓冲区克隆。

**Shadowsocks 稳态拷贝是一次经过测量的 KEEP，而不是疏忽。** 读侧拷贝是真实存在的、位于某个依赖中、无分配，其成本在噪声范围内。关掉它需要一个非 2 的幂的容量，而 `buf.Put` 会拒绝这样的容量（`alloc.go` 的 switch 只池化 2 的幂；原地分支需要 `length + Overhead`）。这笔交易的实质是：用每次读取约 32 KiB 的未入池垃圾，去换一次良性的 memcpy。

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
| `unix.Splice()` 真正在运行 | **此处为 `UNVERIFIED`**——sing fork 声明了 `linux-splice-proof`，但本次校准中没有检查到精确 pin 的运行 | 检查一个匹配的工作流运行 |
| splice 静默停滞 | **在 sing pin 中已修复**——当前根替换解析到 `1729220c46c4`，其 `splice_linux.go` 在 `Pipe2` 失败时返回未交接。`docs/internal/fork-splice-silent-stall.md` 是历史记录。 | 检查确切的 pinned `common/bufio/splice_linux.go` 并运行其 Linux 回归测试 |

---

## 5. AI 告诫（AI cautions）

1. **借用的游标不得比 `PeekStream` 存活更久。** 不要缓存一个 `payloadReader`。
2. **不要把 QUIC 工作副本入池。** 它是每次调用新建的 `make`，这是有原因的。
3. **不要在调用方的数据报被释放之后仍保留对它的视图。**
4. **不要丢掉 `Length < hdrLen+4` 防护。**
5. **不要在调用方的数据报内部解掩码。**
6. **在移除任何拷贝之前，先找到要求它存在的那条规则。** MASQUE 的替换与 `takePacketBytes` 的视图，就是拷贝/别名边界本身就是正确性条件、而不是优化目标的两个例子。
7. **不要改自有数据报的释放语义。** 所有权已经转移；在那里做一次释放就是双重释放。
8. **不要在原地“修复”一次 `PrependContextID` 替换。**
9. **不要移除生产者侧的头空间预留。**
10. **不要为了“避免” `io.ErrShortBuffer` 而提高嗅探缓冲区大小。** 那个错误就是那个上界。
11. **不要在只实现了 `IdleConnectionReleaser` 的地方断言 `IdleConnectionKeeper`。**
12. **对任何新增的 `ReuseSuspect` 所有者，都要对照退役遍历及其测试进行审查。**
13. **不要让一次裁剪拨号、唤醒被挂起的工作、重置网络或终止活跃的流。**
14. **不要用 `handed` 构建普查。** 它衡量的是“系统调用路径被选中了”。
15. **不要在不解决非 2 的幂池约束的情况下关掉 Shadowsocks 稳态拷贝**；它是一次刻意的、经过测量的保留。
16. **不要假定 `-race` 覆盖了分配上限。** 它们按设计由 `!race` 门控。
17. **不要把受平台门控的包当作已被无标签运行测试过。** `common/ktls` 是一条数据面路径，它的 Linux/带标签测试需要正确的环境。
18. **保持 splice 交接契约：** 在消费 payload 之前的失败必须允许回退；已经交接的流不得被重放。
