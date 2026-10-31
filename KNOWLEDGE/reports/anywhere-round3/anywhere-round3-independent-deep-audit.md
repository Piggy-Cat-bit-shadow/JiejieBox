# Anywhere 第三轮独立深度审计报告

**审计日期：** 2026-10-20（Asia/Shanghai）
**主仓库：** `github.com/Piggy-Cat-bit-shadow/sing-box`，分支 `testing`，**无新建分支**
**审计快照：** `sing-box@5064f20a97880d3d5cfe1624c7f79da8d3b03b5c`（下文简写 `sb@5064f20a`）
**学习对象快照：** `NodePassProject/Anywhere@89905e0e3641bcbd958a66fcb6f027a2bdecec90`（下文简写 `aw@89905e0e`）
**有效依赖（`go list` 口径，非默认分支）：**

| 模块 | 实际生效版本 | 本地对照源码 |
|---|---|---|
| `github.com/sagernet/sing` | `v0.9.6-0.20261019051552-baed8c3b4a34` | module cache |
| `github.com/sagernet/sing-tun` | `v0.0.0-20261019070836-a7bd7facc7cc` | module cache + `C:\Deepseek\sing-tun-fork@a7bd7fac` |
| `github.com/sagernet/quic-go` | `v0.61.1-0.20261019061042-7e08ecaba383` | module cache |
| `github.com/sagernet/sing-quic` | `v0.7.2-0.20261019060901-d0deb0359c06` | module cache |
| `github.com/sagernet/cronet-go` | `v0.0.1-143.0.7499.109-2.0.20260929202119-8c68ce89873c` | module cache |
| `github.com/metacubex/utls` | `v1.8.8-0.20261009084700-6c3e08ed4c4d` | module cache |

**证据等级标签**（沿用本仓库约定，不使用"已验证"而不注明层级）：
`MEASURED_LOCAL`＝本轮在本机跑出原始数据；`VERIFIED_SOURCE`＝当前 SHA 源码直接可读；
`VERIFIED_SOURCE_DEP`＝锁定依赖源码可读；`INFERRED`＝推理；`RECORDED_HISTORY`＝仓库内历史记录（不再复测）；
`NOT_RUN` / `BLOCKED_BY_ENVIRONMENT`＝未执行。

---

## 1. 执行摘要与最终判决

### 1.1 一句话结论

本轮重点考察的四个方向中，有三个未得到实测支持。值得继续研究的工作集中在两处返回路径和 L4 隧道边界；审计还发现 2 个确认缺陷（其中 1 个在审计版本上不可达）以及 1 个需要确定性复现的活性风险。

### 1.2 五条最有价值的结论

**结论一：在本次测试条件下，不建议为 TCP 引入 `PacketTemplate`。**
`sing-tun` 的分段发出路径每段重新编码 IPv4 头、重算 IPv4 首部校验和、重算伪首部校验和
（`sing-tun@a7bd7fac:stack_go_tcp_output.go:822-884`）。看上去这正是 Anywhere
`PacketTemplate`（`aw@89905e0e:Packages/AnywhereIP/Sources/AnywhereIP/TCP/PacketTemplate.swift:8-101`）
要消除的工作。本轮用同一台机器测出这条工作的真实量级：

| 载荷 | 现状（全量重编码） | 仅折叠部分和 | 完整字节模板（Anywhere 形状） | 现状总成本中可被模板消除的部分 |
|---|---:|---:|---:|---:|
| 1 400 B | 62.4 ns | 53.7 ns | 31.9 ns | ≤ 30.5 ns（乐观上界），现实 8.7 ns |
| 64 B | 46.8 ns | 38.4 ns | — | ≤ 8.4 ns |
| 0 B（纯 ACK） | 44.8 ns | 36.1 ns | 17.7 ns | ≤ 27.1 ns |

按 1 Gbps、满 MTU、单向发送折算：62.4 ns / 1 400 B ≈ 0.55 % 单核；换成完整字节模板后仍是
1 400 B / 31.9 ns ≈ 0.28 % 单核。**也就是说整条优化最多省下 0.27 个百分点的一个核，而在手机上
200 Mbps 量级只有 0.05 个百分点。** 这不是"值得下一轮施工"的量级，且它还引入 IP ID、选项、
GSO/offload、MTU 变更、地址变更五类失效条件（见 §9.6、§15.2）。
更关键的是：**Anywhere 自己也省不掉大头**——它的模板每段仍要重算 TCP 头部分和与
payload 部分和（`aw@89905e0e:.../PacketTemplate.swift:93-94`），而本轮测出 1 400 B 载荷的
校验和本身就要 19.8 ns（≈ 70 GB/s 的 SIMD 实现，`gtcpip/internal/tschecksum`），
这部分任何模板都动不了。

**结论二（强否定）：XHTTP `packetUplink.gather` 的 50 µs 轮询不是唤醒风暴，换 timer 没有收益。**
实测本机 `time.Sleep(50 * time.Microsecond)` 的真实睡眠是 **518.6 µs**（均值，n=2000，p99 589 µs，
max 1.50 ms）。`gather()`（`sb@5064f20a:transport/v2rayxhttp/conn.go:1066-1082`）的循环条件因此
**只迭代 1–2 次就退出**，而 `packetUpGatherCap = 2 ms` 在最坏的生产者速率下也只到 2.04 ms。
叠加 `sc_min_posts_interval_ms` 默认 30 ms 的节流（`conn.go:1269-1283`），每个 session 每秒最多约
33 次 POST ⇒ 每秒最多约 33–66 次 `Sleep` 唤醒。**用事件/timer 替换掉这几十次唤醒，
在 Windows/macOS 上省不到任何有意义的东西，还要多一套状态机。**
但实测同时暴露了一个真实的语义漂移：常量写的是 250 µs 静默窗，实际生效的是 ~520 µs（见 §9.5），
这才是需要写进文档或重新取值的点（不是性能优化）。

**结论三（强否定）：Hysteria2/TUIC "缺少批量" 是假前提。**
`sing-quic` 的 `writePackets`（`sing-quic@d0deb0359c06:hysteria2/packet.go:278-287`、
`tuic/packet.go:295-304`）确实逐条循环，但它的入参来自 `fragUDPMessage`
（`hysteria2/packet.go:88-92`）：**数据报放得下时返回单元素切片**。也就是说正常路径上这个循环
只跑一次，`writePackets` 是"分片辅助函数"而不是"批次接收入口"。上层 `protocol/hysteria2`、
`protocol/tuic` 也没有实现 `tun.Port`/`PacketBufferPort`（仓库内 grep 无 `WritePackets` 命中），
所以 MASQUE 那种"TUN 读突发 4–64 包"的批量来源在这里根本不存在。
唯一还有一点点空间的是**超大 UDP 载荷被分片成 2–4 片**的那条支路。把 MASQUE 的
`SendDatagramsOwned` 搬过来需要先在适配层造出一个批次来源，属于"为了复用而新增层"，
按 §11 的负面清单应否决（结论：D，附复核条件）。

**结论四（真阳性，建议施工）：返回路径的分组在每次调用上分配，而有一个热调用者是逐包调用它。**
`portReturn.ReturnPackets`（`sing-tun@a7bd7fac:flow_dispatch.go:941-971`）每次调用都新建
`[]returnBatch` 并为每批新建 packet 切片。**本轮用真代码夹具（不是复刻）测出：**

| 场景 | 时间 | 分配 | 说明 |
|---|---:|---|---|
| 1 包 / 1 个 writeback（`protocol/bridge/backend.go:259` 的真实形状） | 236–250 ns | **72 B / 2 allocs** | 两次分配全部来自分组本身 |
| 64 包 / 1 个 writeback | 11 042–11 152 ns | 3 480 B / 8 allocs | 分组已被摊薄到 ~0.13 alloc/包 |

注意两点：**① `slices.IndexFunc` 不是问题**——`writeback` 的数量（B）在生产中通常就是 1
（一个 stage 一个 writeback），索引在第一个元素上就命中；成本全在容器分配上。
**② 真代码的 1 包耗时（236 ns）远高于纯分组复刻（166 ns）**，因为真路径还包含
`parseForwardPacket` + `applyRewrite` + 校验和重算；所以这条候选的收益是**分配次数**，
不是"每次调用省 166 ns"。
而 `protocol/bridge` 的返回环是 `make([]byte, headroom+len(packet))` + 拷贝 +
`[][]byte{buffer}`（`sb@5064f20a:protocol/bridge/backend.go:251-263`）——每包一次，
所以在这条路径上这 2 次分配是**每包**发生的。
这是本轮唯一一个"改动局部、语义不变、有确定性数字"的 L1 候选（§6.1）。

**结论五（真阳性，建议施工）：MASQUE L4 CONNECT-UDP 仍然走逐包复制的 DATAGRAM 路径。**
CONNECT-IP 已经有完整的 owned/batch 契约（`sb@5064f20a:transport/masque/session.go:475-543`），
但同一套 `transport/http` 里的 L4 `http3PacketConn.WritePacket`
（`sb@5064f20a:transport/http/capsule.go:572-586`）是
`PrependContextID` → `SendDatagram(datagram.Bytes())` → `Release()`：**每包一次 payload 复制 +
每包一次 quic-go 队列锁与调度信号**，尽管它下面的 stream 同时实现了 `SendDatagramOwned` 与
`SendDatagramsOwned`（`transport/http/client_h3.go:511-552`）。
L4 **已经有**一个批处理 writer（`WriteConnectedPacketBatch`，`capsule.go:954-971`）把"每包一次锁"
降成"整批一次锁"，所以缺的**不是批量，而是所有权**：批路径仍然逐包 `SendDatagram` 复制。
它的 `FrontHeadroom()` 返回 6 而不是 9（`capsule.go:683-685`，常量 `capsule.go:26`）——作为推断，
这正是"只按 capsule 预留、没把 owned 需要的 9 字节算进去"的指纹：9 = 1 字节 Context ID +
8 字节 Quarter Stream ID，6 = 1 + 5（capsule 上限）。
这是一条 **与已交付 B1/B2/B5 同源、但被漏掉的路径**（§6.2）。

### 1.3 明确"不要做"的清单

| 不要做 | 依据 |
|---|---|
| 给 `ForwardStage` 的端口查找加 map 索引 | 真 `Flush` 实测：K=1 时 2.7 ns/包，K=64 时 25.7 ns/包，而 **K 的实际上界是 `min(已选端口数, 每批包数)`**（K=256 与 K=64 同值）；纯扫描复刻里 K=1 线性 6.35 ns 也**快于** map 7.9 ns（§7.1、§12.8） |
| 用 timer/事件替换 XHTTP gather 轮询 | §1.2 结论二 |
| 引入 TCP PacketTemplate / 第二套 TCP PacketGenerator | §1.2 结论一 |
| 把 MASQUE 的 owned-batch 复制到 Hysteria2/TUIC | §1.2 结论三 |
| 在 Fake-IP 映射里缓存"最终路由裁决" | 现有 4 个 per-connection 匹配缓存字段是**死代码**（`sb@5064f20a:adapter/inbound.go:163-188` 只写不读），说明这条路此前已被设计过又放弃；裁决依赖 forward-only 的 sniff 结果、resolve、NAT 重写与规则代际，见 §8.4 |
| 在上游继承的语义上做 fork-local 行为变更（如把 `domain_keyword` 改成大小写不敏感） | 该文件与 `upstream/testing` 逐字节相同；改了会让同一份配置在 fork/上游行为不同，之后每次同步都要重新解释（§9.2） |
| 为 TCP 收包合并另造一层 | Go stack 已有 direct-to-app-buffer、receive chain、25 ms delayed ACK、256 KiB ACK 合并（`sing-tun@a7bd7fac:stack_go_tcp_input.go:16-43,593-700`），比 Anywhere 的 200 ms tick 更细（§8.5） |

---

## 2. 环境 / Git / 版本与证据等级

### 2.1 Git 事实（记录，不预设基线）

```
$ git rev-parse HEAD              -> 5064f20a97880d3d5cfe1624c7f79da8d3b03b5c
$ git branch --show-current       -> testing
$ git ls-remote origin testing    -> 5064f20a97880d3d5cfe1624c7f79da8d3b03b5c
$ git status --porcelain=v1       -> " M clients/apple" / " M clients/desktop"（子模块指针，非本轮改动）
$ git rev-list --count 5fc4f936..HEAD -> 380
$ git diff --stat 5fc4f936..HEAD -- '*.go' -> 343 files changed, 56389 insertions(+), 898 deletions(-)
```

`KNOWLEDGE/BASELINE.md` 的生产基线仍是 `5fc4f9366`（v0.1.9）。**本轮没有移动它**，
本文所有结论只对 `sb@5064f20a` 成立；知识库模块与本文冲突时以源码为准。
审计开始时工作树位于 `feat/windows-coexistence-subscription-quota@32304df5`，
因该树完全干净（`git status` 无文件级差异）才切换到**已存在**的 `testing` 并快进到远端；
两个子模块的工作树没有做任何 `submodule update`，保持原状。

### 2.2 测量环境

| 项 | 值 |
|---|---|
| OS | Windows 10 Pro 19044，`GOOS=windows GOARCH=amd64` |
| CPU | AMD Ryzen 7 4800H（8C/16T），`GOMAXPROCS=16` |
| RAM | 15.4 GiB |
| Go | `go1.25.5 windows/amd64`（`C:\Deepseek\_gotool\go`），`CGO_ENABLED=0` |
| 构建模式 | `GOFLAGS=-mod=readonly` 跑仓库内命令；临时 harness 在 `C:\tmp\r3probe`（仓库外，独立模块，replace 指向锁定版本） |
| 未执行 | `-race`（无 cgo 工具链）、任何 iOS/Android/macOS 目标、任何真机、任何真实 WAN |

### 2.3 证据分层

- **`MEASURED_LOCAL`**：§12 的 7 组微基准 + 1 组仓库既有 benchmark 复跑。全部是可重跑命令，量级以中位数给出。
- **`VERIFIED_SOURCE` / `VERIFIED_SOURCE_DEP`**：所有 `path:line` 都来自本轮实际读取的当前快照或锁定依赖快照。
- **`RECORDED_HISTORY`**：仓库注释里记录的历史测量（如 XHTTP 26.1–27.8 vs 23.3 MB/s、
  MASQUE DATAGRAM "mean ~4 buffer"），本轮**没有**复测，正文按"仓库记录值"引用。
- **`NOT_RUN`**：真机内存/能耗、真实 WAN 吞吐、四协议互通矩阵、`-race`。

---

## 3. 前两轮结论复核矩阵

复核方法：对每条历史结论回到**当前锁定依赖的实际源码**确认"机制仍在"，而不是复述报告。

| 代号 | 历史结论 | 本轮复核（当前源码） | 判定 |
|---|---|---|---|
| A0 | 劫持 DNS 只解析一次，`*dns.Msg` 跨 goroutine 移交 | `route` 侧未在本轮重点路径上重读；机制未发现回退迹象 | 维持 |
| A1 | `varbin.ReadSliceCount` 几何增长 + 1024 首块上限 | `sing@baed8c3b:common/varbin/slices.go:20-50` 仍在：`maxInitialChunk = 1024`、`growth = cap(result)`、`min(growth, count-len)` | **仍生效** |
| B1 | `tun.PacketBufferPort` owned 路径 | `sing-tun@a7bd7fac:flow.go:79-87` 契约仍在；`flow_dispatch.go:711-733` 实现仍在；`protocol/masque/client.go:670-680` 消费仍在 | **仍生效** |
| B2 | MASQUE owned batch ≤32 分块 | `transport/masque/session.go:545-599`：`maxOwnedDatagramBatch = 32`，`writeBatchOwned` 分块，`writeOwnedBatchChunk` 全有全无 | **仍生效** |
| B3 | SRS 128 MiB 解压预算 + 1 字节探针 | `common/srs/binary.go:76` `decompressedSizeLimit = 128 << 20`；`:83-110` 预算 reader；`:85-86` 注释明确"探针是关键，否则预算耗尽 = 自然 EOF" | **仍生效** |
| B4 | QUIC 小帧精确复制，避免大池缓冲被钉住 | **`quic-go@7e08ecab:frame_sorter.go:198`**：`return len(data) > 0 && cap(data) > 2*len(data)` 仍在，即"超过 2 倍才精确复制" | **仍生效** |
| B5 | Hysteria2/TUIC 使用 `SendDatagramOwned` | `sing-quic:hysteria2/packet.go:288-291`、`tuic/packet.go:305-308` 单包路径确为 `sendOwnedDatagram` | **仍生效**，但见 §8.3 的批量前提修正 |
| B6 | QUIC/H3 收包队列出队清槽 | **`quic-go@7e08ecab:datagram_queue.go:240-243`** 仍在（注释明确"reslice 会把指针留在底层数组里，GC 扫描整个数组"），并有专门测试 `datagram_queue_retention_test.go:17`；HTTP/3 侧另有 `http3/state_tracking_stream_retention_test.go` | **仍生效** |
| B7 | `Conn.DroppedDatagrams()` | `quic-go@7e08ecab:connection.go:3227-3234` + `datagram_queue.go:227-230` | **仍生效** |
| B8 | `UDPNatDroppedPackets()` | `sing-tun@a7bd7fac:udp_nat.go:425-432` | **仍生效** |
| B9 | Android 大规则 mmap 未开启 | `route/rule/rule_set_mmap_ios.go:17-32` 仅 iOS；stub 在非 iOS 直接返回输入 | 维持（仍无设备证据） |

### 3.1 需要纠正的历史表述（不是新 Bug）

- **round2 README 的 "B5｜Hysteria2/TUIC use `SendDatagramOwned`｜one fewer copy" 是准确的**，
  但本轮要补一句边界：**它不是"批量"**，因为调用方每次只给一条消息。历史报告没有说错，
  是第三轮提示词对它的演绎（"仍 for 循环，可能没有用 owned batch"）需要修正为"没有批次来源"。
- **round2 的 "B1 交付后 TUN stage → 端点只复制一次"** 仍然成立，但本轮发现同一族路径上
  **L4 CONNECT-UDP 没有被覆盖**（§6.2）。这不是 B1 的回退，而是 B1 的范围边界。

---

## 4. Anywhere 核心机制学习摘要：为什么有效，哪些只是 Swift 特性

本轮读的是 `aw@89905e0e` 的实际实现（不是类名）。以下按"机制—为何有效—能否迁移到 Go"三段式。

### 4.1 有效的、可迁移的四个思想

**(1) 分层匹配器 + 冻结不可变快照。**
`TieredRouteMatcher` 在 `finalize()` 里一次性做 BFS 求 failure link 与 `dictSuffix`，
把 dict 树**丢弃**，只留下 5 列 per-node 数组 + CSR 边表（`Shared/Networking/Routing/TieredRouteMatcher.swift:100-223`）。
它的价值不在 Aho–Corasick 本身，而在三点：**构建与查询分离**、**构建产物不可变（`let`+`ContiguousArray`）**、
**suffix 与 keyword 是两个结构、同层内 suffix 先短路**（`:329-347`）。这三点与语言无关。
但注意它的一条副作用：`bypass` 层排在最后（tier 顺序 `adBlock→builtIn→user→neutral→bypass`，
`Shared/Models/RoutingRule.swift:102`），说明"层序即优先级"是显式设计而不是偶然。

**(2) Fake-IP 条目携带 `(verdict, verdictVersion)`，惰性失效。**
`Entry` 四个字段：`domain` / `shouldReject` / `verdict: DomainRouter.Match?` / `verdictVersion`
（`Anywhere Network Extension/Tunnel/FakeIPPool.swift:14-19`）。新连接按 **IP** 反查后，
只有 `entry.verdictVersion == domainRouter.currentRulesVersion` 才复用，否则重匹配并回写
（`Anywhere Network Extension/Routers/ConnectionRouter.swift:70-83`）。规则重装时
`version = old + 1` 且懒扫描、不清池（`DomainRouter.swift:102-109`）。
**思想可迁移：版本号 + 按需重算，优于 eager flush。**
**直接照搬不可迁移**：因为 sing-box 的裁决依赖 sniff 之后才可知的域名、
`process_name`、端口、inbound、clash 模式、rule-set 代际与网络环境（§8.4 会给反例）。

**(3) 预计算"不变量 + 可加部分和"，而不是缓存整帧。**
`PacketTemplate` 存的是 IPv4 首部固定部分和、12 字节伪首部和、打包后的端口
（`PacketTemplate.swift:19-59`）；`encode` 只把 `totalLength`/`seq`/`ack`/`flags`/`window`
加上去再折叠（`:61-96`）。这条思路本身正确，**但本轮用它自己的形状去测，收益被载荷校验和
与 TCP 头重算吃掉了**（§1.2 结论一）。所以"能迁移的"是**部分和的可加性**，不是"给 TCP 建模板"。

**(4) 收包侧的"未封尾 chunk"模型。**
`ReceiveBuffer` 只用 `[Data]` + 读游标；append 进未封尾尾块，封尾条件是
`push` **或** `tailSize > limit - MSS`（`:24-45`）；时间条件由 `Stream.expire()` 在**一个 tick** 后触发
（`TCP/Stream.swift:493-503`，`TickClock.interval = 200 ms`）。更细的一点是**任一非尾块在有更新块之后立即可读**
（`ReceiveBuffer.swift:52-63`），所以大包不会被尾块拖住。
可迁移的是**判据组合**（PUSH/尺寸/时间）；不可迁移的是 200 ms tick——对交互式尾包是五分之一的秒，
而 sing-box 的 Go stack 用的是 25 ms delayed ACK + 直接投递（§8.5）。

### 4.2 看起来漂亮但**不要学**的实现

| Anywhere 实现 | 问题 | 证据 |
|---|---|---|
| UDP 批分组 `batches.firstIndex(where:)` | 每数据报线性扫描已累计批次 ⇒ O(D·F)；同一仓库的 TCP 侧做对了（≥8 组时改用字典） | `Tunnel/UDPPlane.swift:79`；对照 `AnywhereIP/.../InboundBatch.swift:39-51` |
| `AsyncInbox` 满即丢 | `if buffer.count >= capacity { return nil }`，无日志无计数 | `Protocols/Core/AsyncInbox.swift:42` |
| `SerialSender` 无界队列 | `AsyncStream.makeStream` 未给 `bufferingPolicy`（默认 unbounded），`submit` 从不挂起 | `Protocols/Core/SerialSender.swift:30-44` |
| `removeFirst()` 出队 | O(n) 每元素；只有 `nextBatch` 是 O(1) | `AsyncInbox.swift:75` |
| `FlowBufferLedger` 全局锁 + `max(by:)` 线性淘汰 | 在唯一全局 Mutex 下遍历所有 flow，`while` 调用 ⇒ O(N·victims) | `FlowBufferLedger.swift:81-125` |
| H3 admission API 无调用者 + `isStreamBlocked` 永不清除 | 一次瞬时 `MAX_STREAMS` 命中会永久毒化该 session 的新建流 | `HTTP3Multiplexer.swift:131-202,168,419`；`QUICConnection+Streams.swift:16-19` |
| AnyTLS "multiplexer" 实际一次一条流 | reserve 要求 `state.streams.isEmpty`，并发靠 128 条会话池 | `AnyTLSMultiplexer.swift:86-100` |
| 无被动 QUIC 迁移 | 只认 active/pending/retired path，其余包直接丢弃计数 | `ConnectionCore+Receive.swift:40-43` |

**这一节的意义**：Anywhere 的价值是"哪些约束值得显式建模"（版本、所有权、批次、封尾判据），
不是"哪些代码值得抄"。它自己也有 O(D·F) 和静默丢弃。

### 4.3 Swift 运行时特性 vs 普适思想

必须区分开的三类：

1. **纯 Swift 运行时副产品**：`AsyncStream` continuation 作为唯一唤醒源、`Mutex` 包裹值类型、
   `Sendable` 检查、`deinit` 里做泄漏断言、200 ms `mach_continuous_time` tick。
   这些在 Go 里要么天生就有（有界 channel、`sync.Mutex`+atomic），要么应当用真实 deadline 的 `time.Timer`。
   **`AsyncInbox` 的静默丢失和 `SerialSender` 的无界队列是 Swift 默认值造成的，不是设计选择**，不能作为"Anywhere 的设计"引用。
2. **可在 Go 里一比一表达**：分层不可变匹配器、版本化惰性失效、部分和可加性、封尾判据组合、
   reserve-before-open、GOAWAY 按 lastStreamID 放弃、OneShot 认领语义。
3. **不能照搬**：`FlowSlot`/`TerminationLatch` 依赖 ARC + `deinit`；`PacketTemplate` 依赖
   "每连接一个 `let` 模板且地址永不变"这一 TUN 栈假设（`AnywhereIP` 里**没有任何**地址变更失效路径）；
   H3/AnyTLS 的池化语义。

---

## 5. 两项目端到端成本路径图（含本轮实测锚点）

### 5.1 路径 A：一次 TUN UDP 数据报（上行）

```
TUN fd read（goReadBatch = 64，iOS 32）
  └─ goEngine.processFrame → parseForwardPacket
      └─ ForwardStage.DispatchParsed                       [s.access 加锁做表查找]
          └─ judgeAndInstall → handler.JudgeFlow
              ├─ PreMatch（整表线性扫一遍）                 ← 见 §8.1
              └─ createFlow → natFor(port) → nat.reserve
          └─ forwardToPort → rewriteForward → stagePortBuffer
              └─ buf.NewSize(headroom+n) + 一次拷贝         ← B1 已交付
                  └─ 线性扫描 s.ports（累计 K 个）          ← 实测 6.4–85 ns
      └─ Flush()  （持有 s.access）
          └─ PacketBufferPort.WritePacketBuffers
              └─ MASQUE Client.WritePacketBuffers
                  └─ session.writePackets
                      ├─ len>1：writeBatchOwned → SendDatagramsOwned（≤32/块，全有全无）
                      └─ len==1：SendDatagramOwned → quic-go datagramQueue.Add
                                                                  ↑ 队满 32 帧时**阻塞**
```

本轮在该路径上测到的数字：`stagePort` 扫描（§12.3）、`ReturnPackets` 分配（§12.4）、
以及 quic-go `Add/AddBatch` 在 32 帧满时的阻塞契约（`quic-go@7e08ecab:datagram_queue.go:61-89,113-153`，
注释原文 "Once that limit is reached, Add blocks until the queue size has reduced"）。

### 5.2 路径 B：一次 TUN TCP 段发出（下行方向的对称面）

```
GoConn.transmitFrame
  ├─ 连续 payload：buildContiguousFrame
  │     └─ buffer.ExtendHeader(40) → encodeHeaders
  │           ├─ tcpHdr.Encode（含 timestamp/SACK 选项）
  │           └─ goEncodeNetworkHeader
  │                 ├─ ipHdr.Encode + CalculateChecksum   ← 21 ns（实测）
  │                 └─ PseudoHeaderChecksum               ←  5.1 ns（实测）
  │     └─ offload ? SetChecksum(pseudoSum) : checksum.Checksum(tcpHdr) + goChecksumRuns
  └─ 非连续：buildFrame（scratch + appendRuns）
```

**这条路径是本轮证伪 TCP 模板候选的位置**：可消除部分只有 IPv4 头重编码 + 两个部分和，
而 1 400 B 的 TCP 校验和本身（19.8 ns）与 `tcpHdr.Encode`（5.9 ns）都在"模板"之外。

### 5.3 路径 C：一次 XHTTP packet-up 上行

```
relay Write → packetConn.Write → uplink.write（mutex + append + Signal）
  └─ flusher goroutine：
      ├─ shouldGatherUpload(seq, pending)（seq≤1 或 ≥64 KiB 跳过）
      ├─ gather()：每 50 µs 睡一次的轮询，直到 250 µs 静默或 2 ms 上限
      ├─ maxEach = scMaxEachPostBytes.rand()（默认 1 MB）
      ├─ payload = pending[:size]（**不复制**，子切片移交）
      └─ sendPacket：time.NewTimer(节流) → context.WithTimeout(15 s) → newRequest（Header.Clone + url.URL + 100–1000 B padding）→ roundTrip → drain
```

实测：`Sleep(50µs)` = 518.6 µs ⇒ `gather()` 只迭代 1–2 次；`packetUpGatherCap` 实际最坏 2.04 ms。

### 5.4 路径 D：一次规则集下载与发布

```
HTTP GET → 分支：
  ├─ cacheFile == nil && format == binary：srs.Read(stream) + io.Copy(io.Discard)   （流式）
  └─ 其它：io.ReadAll                                                                 （整块）
  → 128 MiB 解压预算 reader（1 字节探针区分预算耗尽与自然 EOF）
  → Adler-32 校验 → publishRules（内存先换）→ SaveRuleSet（磁盘后写，失败只记日志）
```

本轮不重复 round2 的分配实验，只标注两件事：**"已流式"只覆盖一条分支**，
以及**内存先于磁盘发布**的窗口（§9.4）。

### 5.5 资源所有权速查（本轮复核过的部分）

| 资源 | 所有者 | 释放点 | 危险点 |
|---|---|---|---|
| TUN 读缓冲 `goFrame.buffer` | `goEngine` slab pool | 每帧处理完归还 | 被 stage 借用时**不得**跨 Flush 持有（B1 用 owned 拷贝规避） |
| `stagedPort.buffers` | `ForwardStage` | `Flush` 无条件移交 / `Close` 统一 `ReleaseMulti` | 移交后绝不回看（`flow_dispatch.go:776-787` 注释明确） |
| `datagram`（PrependContextID 后） | 调用者 → 传输层 | 成功即转移；失败 `Advance(1)` 回滚后仍归调用者 | 替换缓冲区（headroom 不足）会 `Release` 原件 ⇒ 必须整块拒绝（`session.go:574-617`） |
| `quic.Conn` + UDP socket | `transport/http.http3ClientImpl` | `masque.Client.Close` → `httpClient.Close()` | session cancel ≠ httpClient close |
| Mieru 的 TCP/UDP socket | **fork** 的 `serverDialer.packetConns`，不是 SDK | `Close`/`InterfaceUpdated` | SDK 的 `PacketUnderlay.Close` 不关 `PacketConn`（`protocol/mieru/inbound.go:480-490`） |

---

## 6. A 类确认候选详解

### 6.1 A3-1：返回路径 `ReturnPackets` 的每次调用分配（L1，建议施工）

**出处**　`sing-tun@a7bd7fac:flow_dispatch.go:941-971`（`portReturn.ReturnPackets`），
调用者之一 `sb@5064f20a:protocol/bridge/backend.go:251-263`。

**代码事实**

```go
// flow_dispatch.go:941-971（节选）
func (r *portReturn) ReturnPackets(packets [][]byte) [][]byte {
	...
	unconsumed := packets[:0]
	var batches []returnBatch            // ← 每次调用新建
	for _, raw := range packets {
		decision, writeback := r.classifyReturn(raw, headroom, now)
		switch decision {
		case returnWrite:
			index := slices.IndexFunc(batches, func(batch returnBatch) bool { return batch.writeback == writeback })
			if index < 0 {
				batches = append(batches, returnBatch{writeback: writeback})   // ← 分配
				index = len(batches) - 1
			}
			batches[index].packets = append(batches[index].packets, raw)       // ← 每批一次分配
```

**瓶颈到底是什么（本轮修正的一个预判）**　提示词怀疑 `slices.IndexFunc` 的线性查找。
实测与代码都表明**查找不是问题**：`batch.writeback` 在生产中几乎总是同一个
（`flow.owner.writeback`，一个 `ForwardStage` 一个 writeback），所以 `IndexFunc` 在第一个元素命中。
真正的成本是：**`batches` 切片本身** + **每批的 `packets` 切片**，每次调用都重新分配，
而 `protocol/bridge` 的返回环是**逐包**调用（`backend.go:255-259`：`make` 一个包缓冲 → 拷贝 →
`[][]byte{buffer}` → `ReturnPackets`）。

**实测（§12.4 与 §12.8，后者是真代码夹具）**

| 场景 | 真代码 ns/op | 真代码分配 | 纯分组复刻 ns/op |
|---|---:|---|---:|
| 1 包 / 1 writeback（bridge 的真实形状） | 236–250 | **2 allocs / 72 B** | 166 |
| 4 包 / 1 writeback | — | — | 165 |
| 64 包 / 1 writeback | 11 042–11 152 | 8 allocs / 3 480 B | 1 097 |
| 64 包 / 8 writeback | — | — | 2 265（复刻，未在真代码上构造） |

**触发频率**　凡是"从代理侧往 TUN 写回"的包都走这里。`transport/device`（CONNECT-IP）
按批调用；`protocol/bridge`、`transport/wireguard`、`protocol/tailscale` 按批或逐包调用
（`grep ReturnPackets(` 的 8 个调用点，其中 `protocol/bridge/backend.go:259` 明确逐包）。

**最小方案（不改契约、不改顺序）**
1. 在 `ReturnPackets` 内部用**栈上内联数组**承接批次：
   `var inline [2]returnBatch; batches := inline[:0]`，每批的 `packets` 用
   `var inlinePackets [2][8][]byte` 之类的小内联缓冲；超过 2 个 writeback 或单批超过 8 包时
   自动退化为当前 `append`（Go 切片超出 cap 自动堆分配，无需分支）。
2. 不要给 `portReturn` 加复用字段：`ReturnPackets` 可被多个读 goroutine 并发调用
   （`protocol/bridge` 的 `deliverReturn` 在 `readLoop` 里，`transport/device` 在回调里），
   共享 scratch 会引入新锁或数据竞争。
3. `unconsumed := packets[:0]` 的复用语义保持不变。

**预期收益**　常见路径 **2 allocs → 0、72 B → 0**（真代码夹具，§12.8）；按 10 万包/秒的返回速率折算，
约省 20 万次分配/秒、7.2 MB/s 的分配压力。**这是"降低 GC 压力"类收益，不是吞吐翻倍**，
所以必须以 `allocs/op` 与 GC 次数验收，不能以 MB/s 验收。

**危险与回滚**　唯一危险是内联缓冲越界时误用；用 `append` 到内联切片即可避免。
回滚＝还原该函数体，没有跨文件影响。

**为什么它是 A 而不是 B**　触发路径确定、语义边界清晰（纯局部容器）、成本已实测、
回滚单点。唯一的前提是"bridge/wireguard 这批调用者被真实使用"，这一点在 Windows 上
`protocol/bridge` 可配置（`type: bridge` inbound）即可成立。

### 6.2 A3-2：L4 CONNECT-UDP 缺少 owned DATAGRAM（L2，建议试验后施工）

**出处**　`sb@5064f20a:transport/http/capsule.go:572-586`（单包）、`:923-1010`（批处理 writer）。

**代码事实（本轮先修正了自己的第一印象）**　CONNECT-UDP **已经有**批处理 writer：

```go
// capsule.go:954-971
func (w *connectedBatchWriter) WriteConnectedPacketBatch(buffers []*buf.Buffer) error {
	c.writeAccess.Lock()          // ← 整批只拿一次锁（这就是"批量"）
	defer c.writeAccess.Unlock()
	...
	for _, buffer := range buffers {
		if err := c.writeOnePacketLocked(buffer); err != nil { return err }
	}
```

所以"L4 没有批量"是**错的**。真正缺的是**所有权**：

```go
// capsule.go:978-1005
func (c *http3PacketConn) writeOnePacketLocked(buffer *buf.Buffer) error {
	datagram := PrependContextID(buffer)
	if c.stream.DatagramsEnabled() {
		err := c.stream.SendDatagram(datagram.Bytes())   // ← 复制 API：一包一次 payload 拷贝
		switch {
		case err == nil:
			// SendDatagram copies the payload into the QUIC frame, so the buffer is
			// reusable immediately; the caller's deferred release handles this.
			return nil
```

而同一个 `DatagramStream` 已经实现了 `SendDatagramOwned` 与 `SendDatagramsOwned`
（`transport/http/client_h3.go:511-552`），CONNECT-IP 正是靠它们把"每包一次复制 + 一次队列锁 +
一次调度信号"降成"整批一次"（`transport/masque/session.go:475-543`）。
`http3PacketConn.FrontHeadroom()` 返回 `CapsuleHeadroom`（= 6 = 1 字节 Context ID + 5 字节 capsule 头，
`capsule.go:683-685`，常量在 `capsule.go:26`），而 owned 路径需要 9（1 + 8 字节 Quarter Stream ID，
`transport/masque/session.go:27-55` 里 `PacketHeadroom = max(6, 9) = 9`）。
由于 `SendDatagram(bytes)` 是复制 API、根本不读 headroom，**6 这个值说明这条路径只按 capsule 预留，
从未把 owned 的 9 字节算进去**——这是推断（源码里没有对应注释），不是文档原文。

**为什么这是真候选**　CONNECT-IP 的 owned 契约已经过 round2 的 B1/B2 与大量所有权测试
（拒绝时仍归调用者、成功后 exactly-once、替换缓冲必须整块拒绝）。L4 路径只是没接上，
不是没有能力。这是"同一仓库里已被验证的机制少接了一条路"，而不是新发明。

**最小方案**
1. `writeOnePacketLocked` 里把 `SendDatagram(bytes)` 换成
   `SendDatagramOwned(datagram)`，成功即转移、失败走原有的 too-large/unsupported 分类
   （注意分类必须在**失败后**做，`transport/masque/session.go:511-516` 记录了
   "先分类会让 typed pointer 无条件逃逸、每包多一次分配"这条教训）。
2. 批路径：`DatagramsEnabled()` 已能在发送前判定，`MaxDatagramPayloadSize` 也能预先比较，
   于是可以把一批拆成"可走 DATAGRAM"与"必须走 capsule"两组，前者一次
   `SendDatagramsOwned(≤32 分块)`，后者在 `writeAccess` 下顺序写 capsule。
   **顺序必须保持**（同一 tunnel 内的 datagram 与 capsule 对端按流顺序消费）。
3. `FrontHeadroom()` 6 → 9，并确认所有生产者的预留在 relay 侧被尊重
   （`bufio.CopyPacket` 会读这个值；`protocol/http` 的 `ListenPacket` 亦然）。

**预期收益**　每包少一次 `payload` 拷贝 + 每包少一次 `datagramQueue` 的锁与信号。
按仓库自己的 mock 基准（§12.7），MASQUE 发送路径在 mock sink 下约 100–130 ns/包；
真实收益必须靠 `payloadcopies/op` 与 QUIC 队列信号次数验收，**不能用 WAN 吞吐推断**。

**风险**　(a) 头部预算从 6 变 9 会抬高 relay 侧每包预留，需确认没有别处硬编码 6；
(b) 批内混排 datagram/capsule 的顺序必须与逐包路径等价，需要一条"混合批次顺序"测试；
(c) 失败时 `SendDatagramsOwned` 全有全无，拒绝的块必须原样回退到逐包路径
（`transport/masque/session.go:603-631` 已有可抄的 rollback 形状）。

---

## 7. B 类待实验详解

### 7.1 B3-1：`ForwardStage.ports` 的累计 K 与从不回收

**事实**　`stagedPort` 数组在 `stagePort`/`stagePortBuffer` 里只增不减
（`sing-tun@a7bd7fac:flow_dispatch.go:699-706,725-731`）；`Flush` 只清 `packets`/`buffers`
（`:786-797`），**从不删除条目**；只有 `Dispatcher.Close` 才释放。因此线性扫描的 K
是"这个 stage 一生中被选过的 `tun.Port` 个数"，而不是"本批的端口数"。
由于 `JudgeFlow` 返回的 `Port` 就是具体 outbound 对象（`sb@5064f20a:adapter/router.go:94-101`），
K ≤ 配置中的 outbound/endpoint 数量——**有界，但会随订阅规模线性增长**。

**实测（§12.3）**

| K | 线性扫描 ns/op | `map[*portNAT]int` ns/op | 结论 |
|---:|---:|---:|---|
| 1 | **6.35** | 7.90 | 线性更快（生产最常见） |
| 2 | 10.9 | — | 线性 |
| 4 | 13.6 | — | 线性 |
| 8 | 15.4 | 16.6 | 基本持平 |
| 16 | 17.7 | — | 线性略慢 |
| 64 | 31.5 | **13.6** | map 快 2.3× |
| 256 | 85.0 | **13.9** | map 快 6.1× |

**结论**　切换阈值大约在 **K ≥ 32**。所以：
- 若真实配置的活跃 outbound 数通常 ≤ 8（绝大多数客户端），**不要动**，线性更快且零分配；
- 若存在"几百节点 load-balance + 频繁切换"的配置，按 `len(s.ports) > 32` 启用批内
  `map[*portNAT]int`（每批清空复用）是安全的，且**不改变输出顺序**（顺序由最终 for 循环保证）。

**真代码夹具给出的同一结论（§12.8 第 3 项）**：直接跑真 `Flush`（64 包/次、0 分配）——
K=1 时 **2.7 ns/包**，K=8 时 5.1，K=64 时 25.7，**K=256 时仍是 25.7**。
最后一点是决定性的：一次 64 包的突发最多只触及 64 个不同端口，所以 K 的实际上界是
`min(已选端口数, 每批包数)`——配置里写 256 个节点也不会让单次 Flush 扫 256 项。
线性模型约为"每包每多一个条目 +0.36 ns"，与 §12.3 的纯扫描复刻（K=64 → 31.5 ns）吻合。

**现在缺的证据**　生产里"一次突发到底跨几个 outbound"的分布。
获取方式（不施工即可做）：在一次性工作副本里把每批实际触及的条目数打到 `Trace` 日志，跑一天真实流量。
**在拿到这个分布之前本项只能是 B；而上面的真机数据已经说明它的收益上限很小。**

**顺带一个更小、更确定的点**　`Flush` 结束后若某条目两个切片都为空，可以把它从 `s.ports` 删除
（保持别的条目顺序），使 K 回到"活跃端口数"。这属于 5 行改动，但它和 6.1 一样需要
"是否值得"的判断——在 K≤8 的世界里它只省一点 L1 访问，**不建议单独为它开一轮施工**。

### 7.2 B3-2：`domain_keyword` 的线性扫描（阈值 K≈128）

**事实**　`sb@5064f20a:route/rule/rule_item_domain_keyword.go:19-36`：
`strings.ToLower(host)` 后对每个关键词 `strings.Contains`。关键词在构造时**原样存**
（`:15-17`），没有小写化、没有去重、没有 AC/trie。

**实测（§12.6，host 30 B、全部未命中＝最坏情况）**

| 关键词数 | ns/op | 折算 |
|---:|---:|---|
| 1 | 9.9 | 其中 `ToLower` 占 31 ns 量级的固定成本 |
| 8 | 78 | |
| 32 | 320 | |
| 128 | 1 330 | |
| 1 024 | **11 300** | 每次规则评估 11.3 µs |

`strings.ToLower` 对一个已经小写的 30 B host 仍要 **31 ns**（§12.6 末行），
所以小 K 时它反而是主要成本；AC 也省不掉它（必须先归一化）。

**判断**　
- 语义上 AC **完全等价**：该 item 的语义是"任一关键词是 host 的子串"，
  这就是 AC 的 `any-match`，**不涉及规则顺序、不涉及最长匹配优先级**。
  这与"整个规则引擎"不同——这里没有"改顺序就改行为"的风险，只有"漏匹配"的风险。
- 收益门槛（**本轮用两种 AC 原型实测，不是估算**，§12.8 第 4 项）：
  **稠密转移表 AC 恒为 ~95 ns**（与 K 无关），线性扫描 K=8 → 77 ns、K=32 → 313、
  K=128 → 1 305、K=1 024 → 11 100。**交叉点在 K≈16–32**（K=8 时线性仍更快）。
- **布局决定一切**：`map[byte]int32` 边的 AC 在 K≤32 时**慢于** `strings.Contains`
  （826–968 ns），因为它每字节做一次 map 哈希。照搬"AC 一定更快"的直觉会在小 K 配置上倒退。
- **内存是真正的约束**：稠密表 1 KiB/节点，K=1 024 要 **2.19 MB 常驻**、构建期 10.6 MB 累计分配；
  Anywhere 用的 CSR（每边 4 B）+ 扁平列同规模约 17 KB。**要移植就移植 CSR 布局，不要稠密表。**
- 构建成本一次性：K=128 → 0.53–0.59 ms，K=1 024 → 3.5–3.8 ms（配置加载期，可接受）。
- **触发频率才是关键**：该 item 只在同一条规则的 `domain`/`domain_suffix` 匹配失败后才跑
  （`matchAnyItem` 短路，`route/rule/rule_abstract.go:126-131,266-270`）。手机端连接速率
  几十/秒时，K=1 024 也只有 0.5 ms/s；但**服务器端或"每连接都重扫"的场景**会放大到可观比例。

**现在缺的证据**　(a) 真实配置里 `domain_keyword` 的 K 分布；(b) 单位时间规则评估次数。
两者都可以用 trace 日志在不改语义的前提下采集。**没有这两个分布，本项是 B 而不是 A**——
而且如上，K≤8 时做 AC 是**净倒退**。

**另一个必须同时修的小问题（见 §9.2）**　关键词没有小写化，配置里写 `Google` 永远不匹配。

### 7.3 B3-3：`retiredFakeIPAddress` 的每连接重建

**事实**　配置了 Fake-IP 且目的地址**不在**保留段时，`PreMatch` 与 `matchRule` 都会走到
`retiredFakeIPAddress`（`sb@5064f20a:route/route.go:1164-1165`），它调用
`Store().RetiredAddresses()`：取锁 + 复制两个切片 + 归一化
（`dns/transport/fakeip/store.go:269-291`）；若配置了 `store_fakeip`，还要一次 bbolt 读事务 + 反序列化
（`experimental/cachefile/fakeip.go:98-108`）。而这段状态**只在 store 启动/范围变更/显式 flush 时改变**
（`store.go:119-152`、`experimental/clashapi/cache.go:21-33`）。

**判断**　这是一个"每次连接重建一份只随冷事件变化的状态"的典型形态，
与 Anywhere 的"版本化惰性失效"思想同源，但**方向相反**：Anywhere 是缓存判定，
这里是缓存**区间快照**。缓存一份 `atomic.Pointer[[]netip.Prefix]` 并只在上述三个事件上重建，
语义上完全等价。

**实测（§12.8 第 5 项，真 store + 真 bbolt 代理）**　这条候选的关键不是"重建"，而是**存储后端**：

| 后端 | 每次 out-of-range 目的地的守卫成本 | 每连接（PreMatch + matchRule 两次） |
|---|---|---|
| 内存（默认，未开 `store_fakeip`） | **25–28 ns，0 allocs** | ~55 ns，0 allocs |
| `cache_file`（`store_fakeip: true`） | **~590 ns，608 B，7 allocs**（一次 bbolt 读事务） | **~1.2 µs，14 allocs** |

也就是说：**默认配置下这条路径已经是噪声（28 ns、零分配）**，此前"每连接重建一份状态"
的描述在内存后端下过于悲观；真正的成本只在启用 `store_fakeip` 时出现，而且因为
`CacheFile.FakeIPRetired()` 每次调用都开一个 bbolt 读事务
（`experimental/cachefile/fakeip.go:98-108`），它对**每一个非 Fake-IP 段内的目的地**都要付一次，
即透明代理下的**每个连接**。

**结论修正**　从"B，需要 profile"改为：**内存后端 = C（已足够好）；`store_fakeip` 后端 = B+**
（有确定数字，收益取决于连接速率）。最小方案不是"再造缓存"，而是把
`RetiredAddresses()` 的结果按"仅在 store Start / 范围变更 / 显式 flush 时变化"缓存成
`atomic.Pointer`，并在上述三个事件上重建——这正是 Fake-IP store 已经拥有的失效点。
**仍缺**：启用 `store_fakeip` 的真实连接速率分布（决定它是 0.1 % 还是 5 %）。

### 7.4 B3-4：`PreMatch` 与 `matchRule` 的双整表扫描

**事实**　TUN / endpoint 这类"先判 flow、再进用户态"的路径上，同一个连接先走
`Router.PreMatch`（`route/route.go:490-617`，内部 `prepareMatchMetadata` + 完整 `for range r.rules`），
若结论是 accept，再走 `matchRule`（`route/route.go:1258-1322`，**又一次** `prepareMatchMetadata`
+ **又一次**完整 `for range r.rules`）。两次之间元数据可能被改动（sniff），但**未改动时应可复用**。

**为什么是 B 而不是 A**　去重需要回答"两次匹配之间哪些元数据可能变"：
`firstPacket` 参与 `sniff` 与 `PreMatch` 的 flow 判定、`Destination` 可能被 `resolve` 改写。
最保守的做法是只复用**逐条规则的"不可能匹配"结论**（例如纯目的地址规则在 destination 未变时），
而这恰好需要**当前已存在但从未被写入的 per-connection 缓存字段**
（`sb@5064f20a:adapter/inbound.go:163-188`：`SourceAddressMatch`/`SourcePortMatch`/
`DestinationAddressMatch`/`DestinationPortMatch` 只被 `ResetRuleMatchCache` 清零，
全仓库没有第二个写入点——本轮 grep 确认）。**先把这套脚手架接上或删掉，再谈去重**，
否则会变成"又加一层缓存"。

---

## 8. C / D 类：已有等价机制与否决项

### 8.1 XHTTP 聚合窗口的轮询 → **D（否决换 timer）**

见 §1.2 结论二与 §12.1/12.2 的实测。补充三条本轮的源码事实：
- 窗口只在 `pending < 64 KiB` 且 `seq > 1` 时生效（`conn.go:1096-1101`）；
- 深队列直接跳过（`gather()` 首轮即 `return`，`conn.go:1074-1076`）；
- 每次 POST 另有一个 `time.NewTimer(wait)`（`conn.go:1196-1204`）与
  `context.WithTimeout`（`:1210`）——**真正的 timer 分配在节流路径上，不在轮询上**。

如果要减少 timer 对象，正确的目标是 `sendPacket` 里的节流 timer（可复用 `time.Timer`），
**不是** `gather`。而节流最快 30 ms 一次，33 次/秒的 timer 分配同样无关紧要。
结论：整条方向 D。

### 8.2 TCP PacketTemplate → **D（否决）**

见 §1.2 结论一与 §12.5。危险清单（若未来有人重启此方向，必须逐条回答）：

| 不变字段 | 本仓库现状 | 若建模板的失效条件 |
|---|---|---|
| IPv4 ID | 每段自增（`stack_go_tcp_output.go:851` `c.ident.Add(1)`） | 不可预计算，模板必须留槽位 |
| TTL | 固定 `synthesizedTTL` | 若有 TTL 改写路径需失效 |
| IPv4 首部校验和 | 每段重算（`:872`） | 依赖 `TotalLength` + `ID`，只能增量 |
| 伪首部校验和 | 每段重算（`:873,883`） | 依赖 `TotalLength`，只能增量 |
| TCP 选项（timestamp/SACK） | **每段重建**（`:844-850`） | 本仓库选项在**动态**部分，不在固定头，模板无法覆盖 |
| 地址 | 连接级固定 | 但本仓库没有"地址变更"路径，若将来加迁移需失效 |
| GSO/offload | `offload==true` 时**不算** TCP 校验和（`:909-916`） | 模板若把 checksum 写死会破坏 offload 语义 |

**结论**：本仓库的可消除部分（IPv4 头编码 + 两个部分和）实测只有 8.7 ns/段；
而 offload 路径已经把最贵的一段（TCP 校验和）省了。**D，且不建议下一轮再评估。**

### 8.3 Hysteria2/TUIC owned batch → **D（假前提）**

见 §1.2 结论三。补充一条可复核的证据链：
`sing-quic@d0deb0359c06:hysteria2/packet.go:88-92` 的 `fragUDPMessage` 在载荷放得下时
`return []*udpMessage{message}`；`udpPacketConn.WritePacket`（`:181-215`）在每次调用里
只 `fragUDPMessage(...)` 一次并立即 `writePackets(...)`。**循环长度恒为 1（除非分片）。**
`protocol/hysteria2`、`protocol/tuic` 也没有 `WritePackets`/`PacketBufferPort` 的实现，
所以不存在被丢弃的批次。**复核条件**：若将来给这两个协议加了 `PacketBufferPort`
（例如为了与 MASQUE 一致的 TUN 归并），则本结论自动失效，应重新评估。

### 8.4 Fake-IP 里缓存"最终路由裁决" → **D（且现有字段是死代码）**

反例（任一条即可让复用出错）：
1. **sniff 的顺序性**：`matchRule` 里 `sniff` 动作**不是终止动作**，它设置 `metadata.Domain` 后
   **继续下一条规则**，且不会回头重跑前面的规则（`route/route.go:1286-1296`）。
   所以"同一域名"在不同连接上可能因为 sniff 是否触发、触发早晚而得到不同裁决。
2. **`process_name` / 端口 / inbound / clash 模式 / 网络环境**：同一 Fake-IP 反查出的域名
   在不同进程、不同端口、不同入站下可以匹配到不同规则。
3. **Fake-IP 映射的失效与规则无关**：映射只在 store 启动/范围变更/显式 flush 时失效
   （`dns/transport/fakeip/store.go:119-152`），而规则集可以随时热更新；两者代际不同源。
4. **`resolve` 动作会改写 `DestinationAddresses`**，进而影响 `ip_cidr` 规则（`route/route.go:1297-1301`）。

**可以安全复用的"中间信息"**（如果将来要做）：同一连接内的
`source address / source port / inbound tag / network` 这些**连接级常量**——正好就是
`adapter.InboundContext` 里那 4 个**从未被写入**的布尔字段。这不是巧合：
脚手架存在说明设计过，而从未接通说明当时的收益/风险比不成立。**本轮结论：先删或先用，不要再加第三套。**

### 8.5 TCP 收包合并与延迟投递 → **C（已有，且更细）**

`sing-tun@a7bd7fac:stack_go_tcp_input.go` 已有：
- `deliverInOrder` 直接把数据拷进应用已经挂上的读缓冲（`postedTarget.CompareAndSwap`，
  `:652-700`），零中间缓冲；
- `splice` 直通路径（`:660-662`）；
- 延迟 ACK 25 ms（`goDelayedAckInterval`，`:37`）与 ACK 合并阈值 256 KiB（`goAckCoalesceBytes`，`:43`）；
- 乱序区间集合 `oooRanges` 与接收链（`:630-650`）。

对比 Anywhere 的 200 ms tick 封尾（`aw@89905e0e:TCP/Stream.swift:493-503`），
**本仓库的时延契约明显更紧**。结论：C，不要再造"接收合并层"。

### 8.6 QUIC 主动路径迁移 → **C/暂缓（L3，且已有部分能力）**

锁定 `quic-go@7e08ecab` 已有 path manager：`connection.go:1312` 的
`c.pathManager.HandlePacket(remoteAddr, ..., pathChallenge, ...)`、`:1329` 之后的
`MigratedPath`，并且 `connection.go:1299` 有一条**本 fork 特有**的注释：
"for hysteria2 port hopping, direct change remote address without connection migration logic"。
也就是说：**peer 侧地址变化（含端口跳变）已被处理，主动迁移（客户端换本地路径）没有对外 API**。
Anywhere 的两阶段状态机（proactive probe → validate → commit）可以作为**未来设计参考**，
但本轮不改 `quic-go`、不新增本地 API，结论 C（能力有边界，不是缺口）。

### 8.7 `FlowSlot` / `TerminationLatch` → **C（只做测试脚手架）**

Anywhere 的 OneShot 是 `Atomic<Bool>.exchange` + `deinit` 断言，**日志而非陷阱**
（`aw@89905e0e:Transport/FlowSlot.swift:12-31`、`Protocols/Core/TerminationLatch.swift:11-70`）。
Go 里对应物是 `sync.Once` + `defer` + 测试期计数。**没有任何生产性能收益**，
不应在热路径加常驻追踪。结论：C，只在下一轮给 B1/B2/B5/QUIC stream 写**测试期**的
release 计数断言（不进生产构建）。

### 8.8 远端规则集下载 → **C（主体已完成），但发布顺序值得记录**

已确认（`sb@5064f20a`）：
- 无 cache_file 且 binary → 流式（`route/rule/rule_set_remote.go:249-259,341-351`）；
- 有 cache_file 或非 binary → `io.ReadAll`（`:346`）；
- 解压预算 128 MiB + 1 字节探针（`common/srs/binary.go:76,83-110`）；
- 缓存写入是单事务 bbolt（`experimental/cachefile/cache.go:475-487`）；
- **发布顺序是"内存先、磁盘后"**（`publishRules` 在 `rule_set_remote.go:281-285`，
  `SaveRuleSet` 在 `:360-370`，失败只 `logger.Warn`）。

最后一条**不是本轮要改的 Bug**（崩溃窗口内规则是新的、磁盘是旧的，下次启动回落到旧代际，
可接受），但它必须在"原子发布"的讨论里被明确写出来，否则会被误当成"已经严格原子"。
结论：C（另一条具体路径仍然是 `io.ReadAll`，可作为下一轮 B 级调研，但不属本轮 A）。

### 8.9 连接池 / 预留容量 → **C（差异在协议，不在实现）**

- XHTTP XMUX：`openUsage` + `closed` + 延迟关闭三者构成的不变式是正确的
  （`transport/v2rayxhttp/xmux.go:78-98`），单次 stream reset **不会**退役连接，
  连续 3 次失败才 `failing`（`:147,158`）；退役后**新流拒绝、旧流继续**（`:438-457`）。
  这与 Anywhere 的 reserve/draining 语义**已经等价**。
- AnyTLS：协议本身**没有 GOAWAY/draining**（`sing-anytls@580984e4:session.go` 只有
  `idle/starting/ready/closed`），所以"退役时新请求的流向"这件事在 AnyTLS 里由**会话池**
  而不是协议承担。**这是协议差异，不是实现缺口。**

结论：C，不要再建"统一连接池"。唯一值得做的是**差异测试**（下一轮 debug 矩阵 §14.3）。

### 8.10 TLS/ECH/QUIC recovery → **C（本轮只确认边界）**

`sing-box` 的 REALITY 走 `common/tls/reality_client.go`，首飞（`prepareFirstFlight`）
与 `uTLS` 包装只在握手期生效；`sing-quic` 的拥塞控制与 recovery 属依赖实现。
**本轮没有发现可安全复用的重复工作**，也**不建议**用 Anywhere 的
`DeliveryRateSampler`/`Pacer` 替换 `quic-go`（后者已按 RFC 9002 实现，且与拥塞控制器耦合）。
结论：C。

---

## 9. R 类：正确性风险与潜在缺陷

> 本轮**没有修改生产代码**。以下每条都给出"当前是否可达"，避免把死缺陷报成事故。

### 9.1 R3-1：`equalFoldASCII` 的折叠起点写成了 `'B'`（确认缺陷，当前不可达）

```go
// sb@5064f20a:protocol/masque/dns_assignment.go:746-763
for index := range len(first) {
	a, b := first[index], second[index]
	if 'A' <= a && a <= 'Z' { a += 'a' - 'A' }
	if 'B' <= b && b <= 'Z' { b += 'a' - 'A' }   // ← 应为 'A'
```

- **影响方向**：`second` 里的大写 `A` 不被折叠 ⇒ 两个本应相等的 host 被判为**不同** ⇒
  same-connection DoH 被**拒绝**（fail-closed，不会误放行）。
- **当前可达性：不可达。** 唯一调用者 `sameOrigin`（`:700-707`）对两侧都先做
  `normalizeHostName`（内含 `strings.ToLower`）。`transport/http` 里有一份**正确**的副本
  （`client_h3_request.go:237-254`），说明这是复制时的手误。
- **为什么仍列为 R**：这是一个 8 字符的差异、无测试覆盖、且一旦有人"优化掉多余的 ToLower"
  就会变成真实的路由/DoH 拒绝。修复是改一个字符 + 加一条大小写用例。
  **下一轮应作为 P3 顺手修复，不需要专门窗口。**

### 9.2 R3-2：`domain_keyword` 的关键词没有小写化（**上游继承的语义陷阱**，可达但不是 fork 缺陷）

`NewDomainKeywordItem` 原样保存 `keywords`（`route/rule/rule_item_domain_keyword.go:15-17`），
而 `Match` 只把 **host** 转小写（`:29`）。因此配置里写 `DomainKeyword: ["Google"]`
**永远不会匹配**，且没有任何告警。

**本轮把定级改了（见 §15 第 16 条）**：该文件与 `upstream/testing` **逐字节相同**
（`git show upstream/testing:route/rule/rule_item_domain_keyword.go` 输出同样代码，
且该路径在上游只有 1 次提交历史）。所以这不是 fork 的缺陷，而是**上游语义**：
关键词大小写敏感、host 大小写不敏感。文档（`docs/configuration/dns/rule.md:349`）
也只写 "Match domain using keyword"，没有承诺大小写不敏感。

- **影响方向**：规则静默失效 ⇒ 本该代理的流量走直连（或反之）。属于"配置语义与直觉不符"，
  不是崩溃；且与上游行为一致，用户的既有配置在官方 sing-box 上同样如此。
- **是否可达**：完全可达，只需要用户写一个大写字母（但同一配置在上游也一样不生效）。
- **为什么不建议 fork 单方面修**：修了会让本 fork 与上游在这一条规则上**行为分叉**——
  同一份配置在 fork 匹配、在上游不匹配，未来每次同步都要重新解释。更合适的动作是
  **在文档/知识库记一条注意事项**，或在配置校验期对含大写字母的 `domain_keyword`
  打一条 warning（不改变匹配语义）。
- **如果决定要修**：构造时 `strings.ToLower` 每个关键词并去重；加一条
  "大写关键词必须匹配小写 host" 的 Red→Green 测试；并且必须在 changelog 里写成
  **行为变更 + 与上游分叉**，不能当成纯内部重构。

### 9.3 R3-3：`ForwardStage.Flush` 在持有 stage 锁时可能阻塞（**已确定性复现**）

**事实链（全部为源码确认）**
1. `Flush` 在 `s.access` 保护下调用 `bufferPort.WritePacketBuffers(staged.buffers)`
   （`sing-tun@a7bd7fac:flow_dispatch.go:772-786`）；
2. 该调用进入 `protocol/masque/client.go:675-680` → `transport/masque/client.go:467-516`
   → `session.writePackets`（`:475-543`）→ `SendDatagramOwned` / `SendDatagramsOwned`；
3. `quic-go@7e08ecab:datagram_queue.go:61-89`（`Add`）与 `:113-153`（`AddBatch`）
   在**发送队列已满 32 帧时阻塞**，等待 `<-h.sent` 或 `<-h.closed`；
4. capsule fallback 更直接：`session.writePackets:540-542` 在 `s.writeAccess` 下
   `WriteDatagramCapsules(s.stream, ...)`，即**流控写**；而 `s.writeAccess` 同时被
   控制 capsule 的读侧使用（`:350-356`）。

**后果（不夸大）**　不是死锁：连接关闭会 `close(h.closed)` 让 `AddBatch` 返回。
但**同一 stage 的 `Close()`、`Dispatch()`、`sweep()` 都要拿 `s.access`**，
所以在拥塞/对端不读的情况下，`Box.Close()` 或网络切换可以等待一个 RTT 级甚至更久的时间。
在 gVisor 模式下 `Flush` 是**逐包**调用（`sing-tun@a7bd7fac:stack_gvisor_filter.go:74-75`），
锁的进出频率也更高。

**已构造确定性复现（§12.8，真代码夹具，非推理）**

用一个假 `PacketBufferPort`，它的 `WritePacketBuffers` 在 channel 上阻塞（模拟 quic-go 在
32 帧满时的 `Add`/`AddBatch`），然后：

1. goroutine A：`stage.Flush()`（进入阻塞的 `WritePacketBuffers`）；
2. goroutine B：`dispatcher.Close()`；
3. **实测结果：`Close` 在 300 ms 后仍未返回**（A 正在端口调用里阻塞）；
4. 一旦释放端口，`Flush` 立即返回，`Close` 在 **301.24 ms** 完成（即它等满了整个阻塞期）。

也就是说：**`ForwardStage.access` 确实被跨在 `PacketBufferPort.WritePacketBuffers` 上，
`Close`/`Dispatch`/`sweep` 会被下游阻塞原样拖住。** 三条源码事实（锁下调用、quic-go 队满阻塞、
capsule 回退的流控写）由此从"链路上可能"变成"已测到"。

**它仍然不是死锁**：连接关闭会 `close(h.closed)` 让 `AddBatch` 返回；夹具里释放端口后
`Close` 立刻完成。所以正确的定级是 **P0 风险（阻塞无上界）而非产品缺陷**：
阻塞时长由拥塞/对端读取决定，没有本地上界。

**为什么仍是 R 而不是 Bug**　复现证明的是"锁被持有到下游返回"，不是"生产里已经发生长阻塞"。
真实触发需要发送队列满（32 帧）或对端停止读取 capsule；本轮**没有**在真实 QUIC 连接上
把队列打满并测量过。所以结论是：**耦合已证实，发生频率未证实**。

**可能的修法（本轮只写条件，不实施）**　在锁下把批次摘出并把条目状态置为 `in-flight`，
锁外写入；`Close` 只释放仍归自己所有的未移交缓冲。**最危险的是 exactly-once 与有序写入**，
所以门槛是：先有一份真实拥塞下的阻塞时长分布（不只是夹具里的 300 ms），再谈改动。

### 9.4 R3-4：规则集"内存先发布、磁盘后写"的窗口（记录，不建议改）

见 §8.8。风险等级低（崩溃后回落旧代际，不会把坏规则持久化），
但**不要**在任何文档里声称"规则发布是原子的"。

### 9.5 R3-5：XHTTP 静默窗常量的实际语义与字面值不一致（文档级）

`packetUpGatherQuiet = 250 µs`（`conn.go:994`），但实测 `time.Sleep(50µs)` = 518 µs，
所以真实生效的静默阈值是 ~520 µs（在本机与任何 0.5 ms 时钟粒度的平台上）。
代码注释已经预见了这一点（`:989-993` "the monotonic clock ticks at roughly 500 us"），
**但常量名与文档仍在暗示 250 µs**。这不是性能问题，是**可维护性/可测性问题**：
下一轮若要给这个窗口写回归测试，必须按"≥1 tick"而不是 250 µs 断言。

### 9.6 本轮确认**不存在**的问题（避免下一轮重复排查）

| 曾经的怀疑 | 结论 | 依据 |
|---|---|---|
| MASQUE owned batch 会双释放/漏释放 | 不存在：拒绝时回滚前缀、替换缓冲整块拒绝、成功即转移 | `transport/masque/session.go:436-446,562-631` |
| XHTTP 单次 stream reset 会杀死共享会话 | 不存在：`release()` 只 `addOpenUsage(-1)`，阈值 3 | `transport/v2rayxhttp/xmux.go:78-98,147` |
| 规则集解压预算会把自然 EOF 当成超限 | 不存在：1 字节探针 + 独立错误 | `common/srs/binary.go:83-110` |
| Hysteria2/TUIC 单包路径还在复制 | 不存在：已是 `SendDatagramOwned` | `sing-quic:hysteria2/packet.go:288-291` |
| AnyTLS 每个 stream 各自建 TLS | 不存在：一条 TLS 连接一条 session，多 stream 复用 | `sing-anytls@580984e4:session.go:151-163`、`stream.go:18-44` |

---

## 10. 本轮自主发现：超出委托方向的发现

> 本章回答"你有没有看到名单以外的东西"。共 7 条，其中 2 条已升级为 A（§6），
> 2 条为确认缺陷（§9.1/9.2），其余 3 条按"证据 + 为什么还没升级"列出。

### 10.1 发现一（A 级）：返回路径的分组分配 + bridge 的逐包调用
见 §6.1。**这条完全不在提示词名单里**：提示词怀疑的是 `slices.IndexFunc` 的查找复杂度，
而实测证明查找是 O(1)，成本在**每次调用的容器分配**，且被一个逐包调用者放大成每包 3 次分配。

### 10.2 发现二（A 级）：L4 CONNECT-UDP 的 owned DATAGRAM 缺口
见 §6.2。提示词把 MASQUE 分成 L4 与 CONNECT-IP 两个 surface，但**没有**指出
"L4 已有批量锁、没有 owned 所有权"这个非对称。这条发现纠正了我自己最初"L4 完全没有批量"的误判，
也给出了一个比"新写批量"更小、更安全的方案。

### 10.3 发现三：`adapter.InboundContext` 的 per-connection 匹配缓存是死代码
`SourceAddressMatch` / `SourcePortMatch` / `DestinationAddressMatch` / `DestinationPortMatch`
在 `adapter/inbound.go:163-188` 只被清零；全仓库（排除 `clients/`）grep 不到第二个写入点。
这意味着：**历史上设计过"连接级匹配缓存"，但从未接通**。
它同时解释了为什么"再加一层缓存"是危险方向（§8.4），也给出了正确的最小切口
（"连接级常量"而非"域名裁决"）。**下一轮应做的是删掉或接通，不是再新增。**

### 10.4 发现四：`protocol/bridge` 的返回环每包一次 `make` + 拷贝
`backend.go:251-263`：`make([]byte, headroom+len(packet))` → `copy` → `[][]byte{buffer}` →
`ReturnPackets`，全部在 `readLoop` 的每包路径上。它与 §6.1 是同一个修复的两端
（一端减少容器分配，一端减少数据拷贝——第二项需要 `ReturnPackets` 接受"可借用的 buffer"，
属于接口级改动，**本轮只记录不建议**）。

### 10.5 发现五：Mieru 的 `dialSocketTimeout` 是"本地已判故障、但对外是 Unknown"
`protocol/mieru/fallback.go:389-421` 把 `dialSocketTimeout` 明确排除在 `FailureFacts` 之外
（`:417-421` return err 原样），但同一个错误在 `:527-539` 又会触发"切到 secondary + 冷却翻倍"。
于是出现一个不对称：**节点内部已经认为这是传输故障，外层 group 拿到的却是 Unknown，
既不能重试也不能惩罚**（`protocol/group/loadbalance_failover.go:160-173,287-308`）。
这不是 Bug（Unknown 的保守语义是有意的），但它是一个**策略可见性缺口**，
值得下一轮单独判断"超时是否应携带 Path/Dial 事实"。

### 10.6 发现六：本仓库既有的 MASQUE mock benchmark 显示 batch 路径"没有更快，还多一次分配"
`transport/masque/dataplane_bench_test.go` 的 `BenchmarkDataplaneOutboundBatchSize`
（本轮复跑，`-benchtime 300x -count 3`）：

| 批量 | per-packet（3 轮 ns/包） | batched（3 轮 ns/包） | 分配（per-packet → batched） |
|---|---|---|---|
| 4 | 124.6 / 128.4（B/op 303） | 130.2 / 139.5 / 142.3（B/op 335） | 5 → **6** |
| 8 | 105.8 / 121.8 / 126.9（B/op 611） | 108.3 / 109.4 / 120.4（B/op 648） | 9 → **10** |
| 16 | 117.2 / 117.3 / 126.6（B/op 1 222） | 101.4 / 115.5 / 128.3（B/op 1 296） | 17 → **18** |
| 32 | 99.96 / 101.3 / 107.0（B/op 2 431） | 97.14 / 101.3 / 132.2（B/op 2 571） | 33 → **34** |

**读法**：三轮回原始值全部列出，不做"取最好一轮"的修饰。批量路径在 4/16/32 三档都出现
**至少一次比 per-packet 更慢**的轮次；唯一稳定的是**每批多 1 次分配**。

**这不是说 batch 路径没用**——mock sink 不模拟 quic-go 的发送队列锁与唤醒，而那正是 batch 省掉的东西。
但它说明两件事：(a) 仓库里的 batch 收益**没有**被这条 benchmark 证明，任何"batch 提升 X%"的
说法都必须来自真实 quic-go 队列；(b) batch 路径确实多一次 `prepared` 切片分配
（`transport/masque/session.go:604`），这是可以顺手池化的地方，但优先级低于 §6。

### 10.7 发现七：`time.Sleep` 的实际粒度改变了多个"微秒级"设计的前提
实测 518 µs（§12.1）。受影响的常量不止 XHTTP：
`packetUpGatherTick = 50 µs`、`packetUpGatherQuiet = 250 µs`、
以及任何注释里写"wait N µs"的地方。**在 Windows 与任何 0.5 ms tick 的平台上，
小于 1 ms 的睡眠常量都不应被当作可实现的时间精度**。这条应写进知识库的
`mtu-and-geometry` 或 `xhttp-client-transport` 模块（本轮不改模块，只在报告里记录）。

### 10.8 没有找到的（诚实交代）
本轮沿"一次 TUN UDP 数据报"、"一次 TCP 段发出"、"一次 XHTTP packet-up"、
"一次规则集下载"、"一次域名裁决"五条路径各追了两层，**没有**发现：
新的大块复制（B1/B5 已覆盖主要路径）、新的全局锁热点（MASQUE/route 的热路径都是
快照或原子读）、新的重复握手（XHTTP/AnyTLS 的复用边界清晰）。
剩下的可优化点都落在"每包小分配"与"每连接重建只随冷事件变化的状态"这两类，
已在 §6/§7 列出。

---

## 11. 跨平台 / 跨依赖兼容成本

| 项 | 现状（本轮确认） | 对候选的影响 |
|---|---|---|
| `sing-tun` 是 fork 依赖（`replace` 到 `a7bd7fac`） | 本轮所有候选都涉及它 | §6.1、§7.1 需要再改一次 fork pin；`test/go.mod` 的四个 pin 必须同步（round2 的 CI 已因漏同步失败过一次） |
| `quic-go` 是 fork 依赖（`7e08ecab`） | §9.3 的阻塞语义来自它；§6.2 依赖 `SendDatagramOwned`/`SendDatagramsOwned` 已存在 | §6.2 **不需要**改 quic-go；§9.3 若真要解耦需要改 sing-tun 而非 quic-go |
| Windows / macOS / iOS 时钟粒度 | 实测 Windows ≈ 0.5 ms | §8.1 的否决在所有平台成立；§9.5 的文档修正需要覆盖 macOS/iOS |
| `with_low_memory` / `with_quic` | 未在无 tag 构建下测试本轮候选 | §6.2 必须在 `with_quic` 下单独验证；`with_low_memory` 会改 slab 行为，需重跑 §12.7 |
| Android / iOS 真机 | 无设备 | 所有"内存/能耗"结论 `NOT_RUN` |
| `-race` | 无 cgo（`CGO_ENABLED=0`） | §14 的所有并发用例**只能**在 CI 跑；本地只能跑非 race 形态 |
| Apple `cronet-go` native archive | 本轮未触及 | 不适用 |

**依赖 pin 的维护成本必须写进下一轮的施工单**：任何 `sing-tun` 改动都要同时更新
`go.mod`、`test/go.mod` 与 `KNOWLEDGE/modules/*` 的 provenance 行；
round2 的 `5c88bad` 提交就是为了修这个同步问题，不能重犯。

---

## 12. 性能实测与实验日志

所有实验都在**仓库外**的独立模块 `C:\tmp\r3probe` 中完成（`go.mod` 用 `replace` 指向
`Piggy-Cat-bit-shadow/sing-tun@a7bd7facc7cc` 与 `sing@baed8c3b4a34`，即锁定版本），
**没有向主仓库写入任何文件**。仓库内命令一律 `GOFLAGS=-mod=readonly`，
实验后 `git status` 不含 `go.mod`/`go.sum` 变更。

### 12.1 M1 — `time.Sleep` 的分辨率

```
go test -run TestSleepResolution -v -count 2 .
```
| 轮次 | mean | p50 | p90 | p99 | max |
|---|---:|---:|---:|---:|---:|
| 1 | 518.6 µs | 516.8 µs | 541.7 µs | 589.3 µs | 1.499 ms |
| 2 | 524.1 µs | 517.9 µs | 543.9 µs | 591.9 µs | 1.441 ms |

n = 2000/轮，目标睡眠 50 µs。**结论：请求 50 µs，实际 ~518 µs，约 10×。**

### 12.2 M2 — XHTTP `gather()` 循环的复刻

复刻 `conn.go:1066-1082` 的循环（mutex + 两次时钟读 + 50 µs 睡眠），
生产者以固定间隔追加数据：

| 生产者间隔 | 实测退出耗时 | 迭代次数 | 相对 2 ms 上限 |
|---|---:|---:|---:|
| 无（只睡一次就满足 250 µs 静默） | 508 / 516 µs | 2 | −1.49 ms |
| 100 µs | 1.05 / 2.04 ms | 3–5 | 最多 +42 µs |
| 300 µs | 1.02 / 1.05 ms | 3 | −0.95 ms |
| 1 ms | 509 / 518 µs | 2 | −1.48 ms |

**解读**：所谓"每 50 µs 轮询一次、最多 40 次"在真实平台上不会发生；
最坏也只有 5 次。**timer 化改造的收益上限就是"省掉 1–4 次 sleep"，不可能更多。**

### 12.3 M3 — `stagePort` 线性扫描 vs map

> 这是**按源码语义复刻**的微基准；真代码的同题测量在 §12.8 第 3 项（结论一致、量级吻合）。

复刻 `flow_dispatch.go:692-706` 的指针比较循环（`[]stagedPort`，每项 56 B）：

| K | 线性 ns/op | map ns/op | 分配 |
|---:|---:|---:|---|
| 1 | 6.35 / 6.38 / 6.35 | 7.91 / 7.95 / 7.72 | 0 |
| 2 | 10.93 / 11.15 / 10.97 | — | 0 |
| 4 | 13.57 / 13.82 / 13.80 | — | 0 |
| 8 | 15.34 / 15.54 / 15.49 | 16.61 / 16.38 / 16.76 | 0 |
| 16 | 17.80 / 17.58 / 17.68 | — | 0 |
| 64 | 31.31 / 31.70 / 31.88 | 13.51 / 13.74 / 13.58 | 0 |
| 256 | 85.94 / 84.73 / 84.72 | 13.59 / 13.71 / 13.95 | 0 |

（3 轮，逐轮列出以体现稳定性；`-benchtime` 默认，`-count 3`。）

### 12.4 M4 — `ReturnPackets` 的分组分配

> 这是**纯分组复刻**（不含 parse/rewrite）；真代码的同题测量在 §12.8 第 2 项：
> 1 包 = **2 allocs / 72 B / 236 ns**，64 包 = 8 allocs / 3 480 B / 11.0 µs。
> 复刻只用来把"分组"这一件事单独隔离出来。

复刻 `flow_dispatch.go:941-971` 的 `slices.IndexFunc` + `append` 结构：

| 包数 × 不同 writeback | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| 4 × 1 | 161–167 | 168 | 3 |
| 64 × 1 | 1 095–1 103 | 3 432 | 7 |
| 64 × 2 | 1 317–1 385 | 3 344 | 13 |
| 64 × 8 | 2 248–2 277 | 3 136 | 33 |

### 12.5 M5 — TCP 段头编码与校验和

四条路径，同一 1 400 B payload、同一 CPU：

| 变体 | 1 400 B | 64 B | 0 B |
|---|---:|---:|---:|
| A. 现状（`encodeHeaders` + `goEncodeNetworkHeader` + 全量校验和） | 62.4 ns | 46.8 ns | 44.8 ns |
| B. 只把两个部分和改成可加折叠（仍调 `Encode`） | 53.7 ns | 38.4 ns | 36.1 ns |
| C. 完整字节模板（Anywhere `PacketTemplate` 形状：memcpy 40 B + 只写动态字段） | **31.9 ns** | — | **17.7 ns** |
| 分解：IPv4 头 `Encode` + `CalculateChecksum` | 21.0 ns | | |
| 分解：`PseudoHeaderChecksum` | 5.1 ns | | |
| 分解：`header.TCP.Encode` | 5.9 ns | | |
| 分解：1 400 B payload 校验和（SIMD） | 19.8 ns（≈ 70 GB/s） | | |

**可消除量**：A→B = 8.7 ns（14 %）；A→C = 30.5 ns（49 %）。
按 1 Gbps / 1 400 B / 单向折算：A = 0.55 % 单核，C = 0.28 % 单核。

### 12.6 M6 — `domain_keyword` 线性扫描

host 30 B 全小写、关键词全部未命中（最坏情况）：

| K | ns/op（3 轮） |
|---:|---|
| 1 | 9.89 / 9.88 / 9.93 |
| 8 | 77.7 / 78.1 / 78.1 |
| 32 | 318 / 330 / 320 |
| 128 | 1 335 / 1 324 / 1 335 |
| 1 024 | 11 287 / 11 357 / 11 345 |
| 1 024（最后一条命中） | 11 345 / 11 389 / 11 362 |
| `strings.ToLower` 单次（已是小写） | 31.1 / 30.8 / 30.6 |

### 12.7 M7 — 仓库既有 benchmark 复跑（非本轮编写）

```
GOFLAGS=-mod=readonly go test -run '^$' -bench \
  'BenchmarkDataplaneOutboundBatchSize$|BenchmarkDataplaneOutboundSendPath$|BenchmarkDataplaneCapsuleFallback$' \
  -benchmem -benchtime 300x -count 3 ./transport/masque/
```

结果见 §10.6 的表。**限定**：sink 是进程内 mock，不含 quic-go 队列、TLS、内核与真实 RTT，
因此**不能**用它证明或否定 batch 的端到端收益；它只证明"mock 下无收益 + 多 1 次分配"。

### 12.8 真代码夹具（收尾实测：R3-3 / 返回路径 / K 扫描 / 关键字候选）

此前的 §12.3/§12.4 是"按源码语义复刻"的微基准。收尾阶段补做了**直接跑真实实现**的夹具：
模块 `C:\tmp\r3real`（仓库外，`go.mod` 直接复制本仓库的完整 require/replace 图，
因此依赖版本与生产完全一致），实例化真实的
`tun.NewForwardDispatcher` + `ForwardStage`，配合假的 `Handler`/`Port`/`PacketBufferPort`/
`ForwardWriteback`，用 `gtcpip/header` 构造合法的 IPv4/UDP 包。**主仓库未改动一个字节。**

**(1) R3-3：`Flush` 持锁跨越阻塞端口调用（确定性复现）**

```
=== RUN   TestFlushBlocksUnderLock
    MEASURED: Close still blocked after 300ms while WritePacketBuffers blocks; Stage.access IS held across the port call
    MEASURED: Close completed 301.2366ms after the port was released
--- PASS
```

即：端口阻塞期间 `Close()` 不返回；端口释放后它立刻返回（301 ms ≈ 整个阻塞期）。
**这是本轮从"源码推理"升级为"实测"的唯一一条**，也是 §9.3 定级为 P0 风险的依据。

**(2) 真 `ReturnPackets` 的分配**

| 形状 | 时间 | 分配 |
|---|---:|---|
| 1 包 / 1 writeback | 236.3 / 239.8 / 249.5 ns | **72 B / 2 allocs**（三轮一致） |
| 64 包 / 1 writeback | 11 042 / 11 071 / 11 152 ns | 3 480 B / 8 allocs |

两次分配就是 `[]returnBatch` 与那个 batch 的 `packets` 切片；64 包时摊薄到 0.125 alloc/包。
**注意**：1 包的真实耗时（236 ns）远高于纯分组复刻（166 ns），差额是
`parseForwardPacket` + `applyRewrite` + 校验和；所以这条候选的收益是**分配**，不是时间。

**(3) 真 `Flush` 的 K 扫描成本**（每次 Flush 前预置 64 个包，只计 Flush；0 allocs）

| 该 stage 内的端口条目 K | Flush ns/op | 折算每包 | 相对 K=1 |
|---:|---:|---:|---:|
| 1 | 171.8 / 173.6 / 174.2 | 2.7 ns | 1× |
| 8 | 321.5 / 322.9 / 331.3 | 5.1 ns | 1.9× |
| 64 | 1 625 / 1 654 / 1 661 | 25.7 ns | 9.5× |
| 256 | 1 636 / 1 646 / 1 648 | 25.7 ns | 9.5× |

**读法**：K=256 与 K=64 相同，因为一次 64 包的突发最多只触及 64 个不同端口，
这直接印证了 §7.1 的结论——**K 的上界是 `min(已选端口数, 每批包数)`**。
线性模型约为"每包每多一个端口条目 ≈ +0.36 ns"，即 K=64 时每包多 ~23 ns。
对照 §12.3 的纯扫描复刻（K=64 时 31.5 ns），两者吻合，说明**夹具没有引入额外开销**。

**(4) 关键字匹配：Aho–Corasick 原型（两种布局）**

同一 host（37 B、全部未命中），三种实现：

| K | 线性 `strings.Contains` | AC（`map` 边） | AC（稠密 `[]int32` 表） | 稠密表内存 |
|---:|---:|---:|---:|---:|
| 8 | **77 ns** | 826 ns | 87 ns | 63 KB / 62 节点 |
| 32 | 313 ns | 968 ns | **94 ns（3.3×）** | 160 KB / 156 节点 |
| 128 | 1 305 ns | 952 ns（1.4×） | **95 ns（13.7×）** | 356 KB / 348 节点 |
| 1 024 | 11 100 ns | 968 ns（11×） | **95 ns（117×）** | 2.19 MB / 2 134 节点 |

- **交叉点在 K≈16–32**（K=8 时线性仍更快）。
- **布局决定一切**：`map[byte]int32` 边的 AC 在 K≤32 时**慢于** `strings.Contains`
  （每字节一次 map 哈希），这正是"Go 里照搬 Swift 的 CSR 之前必须测"的证据。
- **内存是真正的约束**：稠密表 1 KiB/节点，K=1024 要 2.19 MB；构建设施实测
  K=128 → 0.53–0.59 ms / 1.97 MB 累计分配，K=1024 → 3.5–3.8 ms / 10.6 MB 累计分配。
  Anywhere 用的是 **CSR（每边 4 B）+ 扁平列**，同规模约 17 KB——**若要移植，必须移植它的布局，
  而不是稠密表**。
- 两种实现都通过了等价性测试（`TestACEquivalence` / `TestDenseACEquivalence`：
  同一批 host 上 AC 与线性结果一致，包括"最后一条关键词命中"的用例）。

**(5) Fake-IP 守卫的真实成本（真 store + bbolt 代理）**

用真实的 `dns/transport/fakeip.NewStore`（内存后端）测 `Contains + RetiredAddresses`：
**25–28 ns/次、0 allocs**；`Lookup`（in-range）同样是 25 ns。
另用 bbolt 读事务做代理（即 `store_fakeip` 打开时 `CacheFile.FakeIPRetired()`
在 `experimental/cachefile/fakeip.go:98-108` 每次调用要做的事）：
**590–595 ns/次、608 B、7 allocs**。

结论见 §7.3：内存后端下这条守卫是噪声；只有 `store_fakeip` 打开时它才变成
"每连接两次 × 7 allocs"的真实成本。

**(6) 夹具顺带暴露的两个契约尖角（不是 Bug，但会影响未来的测试与复用）**

1. `Flush` 在 `WritePackets` 返回后立刻 `clear(staged.packets)`（`flow_dispatch.go:796-797`），
   所以**普通 `Port` 拿到的是借用视图**：调用返回后再读那些字节会看到 nil。
   这与 B1 的 owned 契约相反，写测试时必须深拷贝（本轮夹具第一次就踩到了，返回空包）。
2. `ReturnPackets` **就地重写**包头，而且这个重写**不幂等**：首次调用把目的地址改成
   *原始客户端*，同一缓冲区再喂一次就匹配不到 flow、被当作 unconsumed 返回。
   真实流量里每个线包都是新的，所以**不是 Bug**；但它意味着"部分失败后用同一缓冲重试"
   这条路走不通，任何重试逻辑都必须重新构造包。

### 12.9 未执行（NOT_RUN）

| 想测但没测 | 原因 |
|---|---|
| 真实 TUN 上的 K 分布与每批包数 | 需要真实流量 + instrumented 构建；本轮禁止改生产源 |
| 真实 QUIC 拥塞下 `WritePacketBuffers` 的阻塞**时长分布** | 夹具只能证明"耦合"（§12.8 第 1 项，301 ms）；打满真实 32 帧队列需要可注入的拥塞对端 |
| L4 CONNECT-UDP owned 前后的 `payloadcopies/op` | 需要改代码才能 A/B；本轮不施工 |
| 真机内存/能耗/`phys_footprint` | 无设备 |
| 真实 WAN 吞吐/p99 | 无受控对端与网络 |
| `-race` | 无 cgo 工具链 |
| `with_quic` / `with_low_memory` 下的完整套件 | 本轮候选尚未施工，无需跑 |
| AnyTLS 单流慢读者的 HOL **影响** | 已用 `net.Pipe` + 真 `sing-anytls` 搭两流 stand，但**对照组**（持续 drain 第一条流）本身无法让第二条流回显，说明夹具没有建立可用基线；按规则记为 `NOT_TESTED`，不把红灯当结论。机制本身由 `sing-anytls:stream.go:59-84` 确认 |

---

## 13. 有待正式施工的设计蓝图

> 本轮**不修改任何代码**。以下只是"如果下一轮批准，改哪里、改什么、怎么验"。

### 13.1 P1 — `ReturnPackets` 去分配（对应 §6.1）

- **拟改文件/函数**：`sing-tun:flow_dispatch.go` 的 `portReturn.ReturnPackets`（仅此一个函数）。
- **契约**：入参 `packets` 的所有权、`unconsumed` 的返回语义、写回顺序、`classifyReturn` 的判定全部不变。
- **最小实现**：栈上内联 `[2]returnBatch` + 每批内联小缓冲；超出即 `append` 退化。
- **测试**：现有 `flow_dispatch_buffer_port_test.go` 与返回路径测试全部保留；
  新增 (a) 1/2/3 个 writeback 的等价性，(b) `allocs/op == 0` 的基准断言，
  (c) 64 包批次的顺序断言（用计数器比对 `WriteReturnPackets` 收到的序列）。
- **回滚**：单函数还原。
- **优先级**：P2（收益是 GC 压力，不是吞吐）。**但它是本轮唯一"改动面最小、证据最硬"的一条。**

### 13.2 P1 — L4 CONNECT-UDP 接入 owned DATAGRAM（对应 §6.2）

- **拟改文件**：`transport/http/capsule.go`（`writeOnePacketLocked`、`WriteConnectedPacketBatch`、
  `FrontHeadroom`），必要时 `transport/http/client_h3.go` 的调用点。
- **契约**：wire format 不变；capsule fallback 不变；`DatagramTooLarge` → capsule 的决策不变；
  错误**必须**在失败之后分类（避免 typed pointer 逃逸）。
- **最小实现**：单包走 `SendDatagramOwned`；批路径按能力预分组，datagram 组
  `SendDatagramsOwned`（≤32/块，全有全无，拒绝则整块回退逐包），capsule 组保持顺序写。
- **测试**：现有 `connect_udp_batch_test.go` / `early_datagram_ownership_test.go` /
  `http3_ingress_zerocopy_test.go` 的形状可直接扩展：
  (a) 成功转移后不释放，(b) 拒绝后原样回退且不重复发送，(c) 混合批次顺序，
  (d) headroom 不足时的替换-拒绝路径（`session.go:574-617` 的教训）。
- **回滚**：`FrontHeadroom` 改回 6 + 恢复 `SendDatagram`。
- **优先级**：P2；若 profile 显示 CONNECT-UDP 是热点则升 P1。
- **依赖成本**：**不需要**改 quic-go（owned API 已在）；**不需要**改 sing-tun。

### 13.3 P3 — `domain_keyword` 大写关键词（**默认不改**，只记账）

- **结论变更**：本轮原先把它列为"顺手修的正确性缺陷"，核对后改为
  **上游继承语义**（与 `upstream/testing` 逐字节相同），**默认不建议 fork 单方面改**
  （理由与分叉成本见 §9.2）。
- **推荐动作**：在知识库/文档记一条"`domain_keyword` 关键词大小写敏感、host 不敏感"；
  可选地在配置校验期对含大写字母的关键词打 warning（**不改变匹配语义**，因此不构成分叉）。
- **若决定要改**：`route/rule/rule_item_domain_keyword.go` 构造函数（3 行）+
  `["Google"]`/`["GOOGLE"]` 的 Red→Green 测试 + changelog 里写明"行为变更且与上游分叉"。

### 13.4 暂不设计（只有条件，没有方案）

- **`s.ports` 回收 / map 索引**（§7.1）：先拿到 K 分布；K≤8 则永久不做。
- **`retiredFakeIPAddress` 快照**（§7.3）：先拿到 profile 占比。
- **`PreMatch`/`matchRule` 去重**（§7.4）：先决定 `adapter.InboundContext` 那 4 个死字段的去留。
- **`Flush` 锁外写入**（§9.3）：先做出确定性复现（Close 超时红灯）。
- **`domain_keyword` AC**（§7.2）：先拿到 K 分布与规则评估频率。

---

## 14. 下一轮专项 Debug 矩阵

> 每行给出"最可能失败的方式"与"如何让它必然失败"，而不是泛泛的"注意并发"。

### 14.1 `ReturnPackets` 去分配

| 编号 | 用例 | 期望 | 失败即意味着 |
|---|---|---|---|
| R-1 | 1 / 2 / 3 / 8 个不同 writeback | 每个 writeback 收到完整、有序的包序列 | 内联缓冲越界或批次串味 |
| R-2 | 全部 `returnPass`（无法解析的包） | 原样返回给调用者，顺序不变 | `unconsumed` 与批次共用底层数组导致覆盖 |
| R-3 | 0 个包 | 不写回、不分配 | 空批次触发多余调用 |
| R-4 | 并发 8 个 goroutine 各自调用 | 无数据竞争、无串批 | 使用了共享 scratch |
| R-5 | `allocs/op` 断言 | 常见路径 0 alloc | 内联数组被逃逸分析搬到堆 |

### 14.2 L4 CONNECT-UDP owned

| 编号 | 用例 | 期望 | 失败即意味着 |
|---|---|---|---|
| U-1 | DATAGRAM 可用、headroom ≥ 9 | 成功转移、调用方不再释放 | 违反 exactly-once |
| U-2 | DATAGRAM 不可用（peer 未协商） | 全部走 capsule，顺序不变 | 能力判定用了"尝试后失败"而非 `DatagramsEnabled()` |
| U-3 | 批中一半超大 | 超大的走 capsule，其余走 datagram，**顺序保持** | 分组打乱了隧道内顺序 |
| U-4 | 批被拒绝（队满/关闭） | 整块回退逐包，且不重复发送 | rollback 未撤销 Context ID |
| U-5 | headroom 只有 6 | 走复制路径，结果正确但慢 | 替换缓冲被释放后仍被使用 |
| U-6 | `FrontHeadroom()` = 9 后 relay 侧分配 | 所有生产者按 9 预留 | 有硬编码 6 的地方 |

### 14.3 连接池/退役差异测试（不施工，只测）

| 编号 | 用例 | 期望 |
|---|---|---|
| P-1 | XHTTP 单流 reset | 会话存活，`openUsage` 正确归零 |
| P-2 | 连续 3 次失败 | 会话退役，新流被拒，旧流继续 |
| P-3 | 退役时 Close 交错 | `sync.Once` 只关闭一次 |
| P-4 | AnyTLS 底层 conn 死掉 | 所有 stream `closeWithError(net.ErrClosed)`，无泄漏 |
| P-5 | Mieru preference 翻转 | 现有 session 不被 Stop，新 session 走新 transport |

### 14.4 规则/缓存

| 编号 | 用例 | 期望 |
|---|---|---|
| C-1 | 规则集热更新后 Fake-IP 旧映射 | 反查域名仍正确；裁决重新计算（不得复用旧裁决） |
| C-2 | `domain_keyword` 大写 | 命中（修复后）；修复前记录"不命中"作为现状 |
| C-3 | 规则集下载中途取消 | 旧规则继续服务，内存与磁盘均不半写 |
| C-4 | 解压炸弹刚好 128 MiB + 1 B | 专用错误，不是 EOF，不是 panic |

### 14.5 平台/CI

| 项 | 要求 |
|---|---|
| `-race` | 必须在 CI 跑（本地 `BLOCKED_BY_ENVIRONMENT`），覆盖 U-4/R-4 |
| `with_quic` | §13.2 的所有用例 |
| `with_low_memory` | §13.1 的 alloc 断言（slab 行为不同） |
| macOS/iOS | 时钟粒度断言必须 ≥ 1 tick，不得用 250 µs |
| 真机 | 内存/能耗结论 **禁止** 在无设备时写下 |

---

## 15. 审计中证伪的错误假设与认识修正

> 本章是本轮的"自我反对"记录。每一条都是**我（或提示词）先有判断、再被源码/实测推翻**。

| # | 原来的判断 | 实际 | 推翻它的证据 |
|---|---|---|---|
| 1 | `ReturnPackets` 的 `slices.IndexFunc` 线性查找是瓶颈 | 查找在 B=1 时是 O(1)；瓶颈是每次调用的容器分配 | §12.4 + `writeback` 生产中唯一 |
| 2 | `stagePort` 的 K 是"每批不同端口数" | K 是"该 stage 一生中被选过的 Port 数"，因为 `s.ports` 从不回收 | `flow_dispatch.go:786-797` 只清内容不删条目 |
| 3 | 换掉 50 µs 轮询能省电 | 实际睡眠 518 µs，循环只跑 1–2 次；timer 化收益上限 1–4 次唤醒 | §12.1/12.2 |
| 4 | TCP 模板能显著降低每段 CPU | 现实可消除 8.7 ns（14 %），乐观上界 30.5 ns；折合 0.27–0.55 % 单核 | §12.5 |
| 5 | Hysteria2/TUIC 缺 owned batch | 缺的是"批次来源"，不是 batch；单包路径已 owned | `sing-quic:fragUDPMessage` 返回单元素 |
| 6 | L4 CONNECT-UDP 完全没有批量 | 已有 `WriteConnectedPacketBatch`（整批一次锁）；缺的是 **owned 所有权** | `capsule.go:954-971` |
| 7 | TCP 收包合并是空白 | Go stack 已有 direct-to-app-buffer + 25 ms delayed ACK + 256 KiB 合并 | `stack_go_tcp_input.go:16-43,652-700` |
| 8 | AnyTLS 与 XHTTP 的池化都需要新机制 | XHTTP 的 `openUsage`/`closed`/延迟关闭已等价于 reserve+draining；AnyTLS 的"退役"是协议差异 | `xmux.go:78-98,438-457`；`sing-anytls:session.go` |
| 9 | 规则集发布是原子的 | 内存先、磁盘后，失败只记日志；崩溃窗口存在 | `rule_set_remote.go:281-285,360-370` |
| 10 | `route/rule/rule.go` 存在（提示词与常识默认） | 不存在；引擎在 `rule_default.go`/`rule_abstract.go`/`route/route.go` | 目录实况 |
| 11 | `reserveNewRequest` 是 XHTTP 的机制 | XHTTP 没有这个名字；`transport/http` 的 H2 里有同名概念，与 XHTTP 无关 | 全仓库 grep 只命中 `transport/http/client_h2*.go` |
| 12 | MASQUE 每个 session 独立发送队列 | 同一 H3 连接上的所有 session **共享**一个 32 帧 DATAGRAM 发送队列；一个 session 打满会阻塞另一个的 `Add` | `quic-go:datagram_queue.go:15,61-89`；`session` 只持有 `quic.Conn` |
| 13 | 复刻微基准足以代表真代码 | 复刻只隔离"分组"：166 ns/3 allocs；真代码 236 ns/**2 allocs**（含 parse+rewrite+校验和）。结论方向对，绝对数字必须用真代码 | §12.4 vs §12.8 第 2 项 |
| 14 | Aho–Corasick 一定比 `strings.Contains` 快 | `map` 边的 AC 在 K≤32 时**更慢**（826–968 ns vs 77–313 ns）；只有稠密表/CSR 才在 K≥16–32 胜出，且稠密表每节点 1 KiB | §12.8 第 4 项 |
| 15 | `ReturnPackets` 可以在部分失败后用同一缓冲重试 | 它**就地重写**包头且**不幂等**：首次调用把目的地址改成原始客户端，再喂同一缓冲会变成 unconsumed | §12.8 第 5 项 |
| 16 | `domain_keyword` 大小写不敏感是 fork 的缺陷 | 该文件与 `upstream/testing` **逐字节相同**，是上游语义；fork 单方面修会造成行为分叉 | `git show upstream/testing:route/rule/rule_item_domain_keyword.go` |

**第 12 条值得单独说**：这是一个**没有被任何测试覆盖**的跨 session 耦合
（`transport/masque` 的隔离测试用的是假 stream）。它不是本轮候选，但应列入下一轮
"对抗测试"清单：一个 session 打满队列时，另一个 session 的 `WritePacketBuffers` 延迟。

---

## 16. 最终决策清单

排序依据：**收益潜力 × 证据强度 × 正确性影响 ÷ 复杂度/长期维护成本**。

| 排名 | 编号 | 候选 | 收益潜力 | 证据强度 | 正确性影响 | 复杂度 | 判决 |
|---:|---|---|---|---|---|---|---|
| 1 | A3-1 | `ReturnPackets` 去分配 | 中（GC/分配） | **高（实测）** | 无（纯容器） | 低 | **A / 施工** |
| 2 | A3-2 | L4 CONNECT-UDP owned | 中高（每包一拷贝+一锁） | 中高（源码对称性 + 已验证的 owned 契约） | 中（所有权） | 中 | **A / 试验后施工** |
| 3 | R3-1 | `equalFoldASCII` `'B'` | 无（当前不可达，fork 自有代码） | 高（源码） | 低 | 极低 | **R / 顺手修** |
| 4 | R3-2 | `domain_keyword` 大写关键词 | 低（可用性） | 高（源码，**与上游逐字节相同**） | 中（行为变更 + 与上游分叉） | 极低 | **记账 / 不建议 fork 单方面修** |
| 5 | B3-1 | `s.ports` 累计 K | 低–中 | 中（实测曲线，缺 K 分布） | 无 | 低 | **B / 先测 K** |
| 6 | B3-2 | `domain_keyword` AC | 中（K≥128 时） | 中（实测曲线，缺频率） | 中（漏匹配风险） | 中 | **B / 先测分布** |
| 7 | B3-3 | retired Fake-IP 快照 | 低–中 | **中（实测：内存 28 ns/0 allocs；`store_fakeip` ~1.2 µs + 14 allocs/连接）** | 中（错拒绝） | 低 | **内存后端 C；`store_fakeip` B+** |
| 8 | B3-4 | PreMatch/matchRule 去重 | 中 | 低（未 profile） | **高** | 高 | **B / 最谨慎** |
| 9 | R3-3 | Flush 锁跨阻塞 | 未知 | **高（已确定性复现：Close 被拖住 301 ms）** | **高（活性）** | 高 | **R / 需真实拥塞分布再改** |
| 10 | — | XHTTP gather timer 化 | ~0 | **高（实测否决）** | — | 中 | **D** |
| 11 | — | TCP PacketTemplate | ~0.3–0.5 % 单核 | **高（实测否决）** | 高 | 高 | **D** |
| 12 | — | HY2/TUIC batch | ~0 | **高（假前提）** | — | 中 | **D** |
| 13 | — | Fake-IP 裁决缓存 | 未知 | 高（反例充分） | **极高** | 高 | **D** |
| 14 | — | TCP 收包合并 | 0（已有） | 高 | — | — | **C** |
| 15 | — | QUIC 主动迁移 | 低（手机切换才有感） | 中 | 高 | **极高** | **C / 暂缓** |
| 16 | — | FlowSlot 生产化 | 0 | 高 | — | 低 | **C（仅测试）** |

**如果下一轮只能做一件事**：做 #1（`ReturnPackets`），因为它是唯一"证据已闭合、改动面最小、
失败可局部回滚"的项。**如果只能做一件"有产品感知"的事**：做 #2（L4 owned），
因为它是同一仓库已被验证的机制漏接了一条路径。

---

## 17. 剩余限制 / NOT_RUN / BLOCKED_BY_ENVIRONMENT

| 类别 | 具体项 | 状态 |
|---|---|---|
| 设备 | iOS/Android `phys_footprint`、能耗、后台唤醒 | `NOT_RUN`（无设备） |
| 设备 | Android 规则集 mmap（round2 B9） | `NOT_RUN`（无设备，结论不变） |
| 网络 | 真实 WAN 吞吐、p95/p99、丢包/重排/PMTU | `NOT_RUN`（无受控对端） |
| 并发 | `-race` 全量 | `BLOCKED_BY_ENVIRONMENT`（`CGO_ENABLED=0`，无 cgo 工具链） |
| 构建 | `with_quic`/`with_low_memory`/iOS tags 的完整套件 | `NOT_RUN`（候选未施工，无需跑） |
| 测量 | 生产 K 分布、规则评估频率 | `NOT_RUN`（需 instrumented 构建或 profile） |
| 测量 | 启用 `store_fakeip` 时的真实连接速率 | `NOT_RUN`（守卫成本已实测：~1.2 µs + 14 allocs/连接，只缺速率） |
| 测量 | 真实 QUIC 拥塞下的阻塞时长分布 | `NOT_RUN`（耦合已用夹具复现，时长分布未测） |
| 交叉 | Xray/官方 AnyTLS/Naive/Mieru 参考实现的互通矩阵 | `NOT_RUN`（本轮不涉及行为变更） |
| 数据 | 所有 `RECORDED_HISTORY` 数字（XHTTP 26.1–27.8 MB/s、MASQUE "mean ~4"） | 未复测，按"仓库记录值"引用 |

**特别注意**：本报告中的 `MEASURED_LOCAL` 全部是**单机微基准或进程内 mock**。
按本仓库的既有纪律：**微基准不得外推为整机/移动端收益**，
`TotalAlloc` 不得写成 RSS，`allocs/op` 下降不得写成"内存下降"。

---

## 18. Git 与知识库归档说明

- **新增文件**：本目录 `README.md` + 本文件；
  `KNOWLEDGE/reports/README.md` 增加一行第三轮索引（只加索引，不改既有条目语义）。
- **未改动**：`KNOWLEDGE/BASELINE.md`、`KNOWLEDGE/README.md`、`KNOWLEDGE/INDEX.md`、
  `KNOWLEDGE/modules/**`、`KNOWLEDGE/INVARIANTS.md`、`KNOWLEDGE/REFRESH.md`、
  `KNOWLEDGE/ACCEPTANCE.md`、全部历史报告、`go.mod`/`go.sum`/`test/go.mod`、任何 `.go` 文件、
  CI 配置、依赖仓库。
- **分支**：`testing`（既有）。**未新建任何本地/远程分支，未打 tag，未创建 Release。**
- **工作树**：`git status --porcelain=v1` 只有本轮新增的文档；`clients/apple`、`clients/desktop`
  两个子模块指针是审计开始前就存在的状态，本轮**没有**执行 `submodule update`、
  `reset --hard`、`clean`、`stash`。
- **提交与推送（实测记录）**：

| 项 | 值 |
|---|---|
| 审计时的生产代码 SHA | `5064f20a97880d3d5cfe1624c7f79da8d3b03b5c` |
| 归档内容提交 | `2b5e6951dbe370949417de6a316aed69572d0601`（`docs(knowledge): archive Anywhere round-three audit and six-protocol efficiency audit`，父提交 `5064f20a9`） |
| 本记录提交 | 只改这两份报告的"归档记录"段落，不改任何结论；它使 HEAD 前移，因此远端 HEAD 等于本记录提交（`git log -1 --pretty=%H`） |
| 推送方式 | 普通 `git push origin testing`（`5064f20a9..2b5e6951d`），**无** `--force`、**无** rebase、**无** reset |
| 推送后远端 | `git ls-remote origin refs/heads/testing` = `2b5e6951dbe370949417de6a316aed69572d0601`，与本地 HEAD 一致 |
| 并行窗口 | 提交前 `git fetch origin testing`；远端未前移，无需再整合 |
| 变动范围 | `git diff --cached --name-only` = 本目录两个文件 + `KNOWLEDGE/reports/README.md` 的索引行；无 `.go`、无 `go.mod`/`go.sum`、无 CI、无子模块 |

### 18.1 为什么这份报告不移动 Baseline

`KNOWLEDGE/README.md` 明确规定 `reports/` 是时间点记录，`BASELINE.md`/`INDEX.md`/`modules/`
才是"当前主张"。本轮的结论全部针对 `sb@5064f20a`；如果之后 `testing` 前进，
本报告的**每一条**都必须重新对照源码，尤其是 §9 的三条 R（一个字符的修复就可能让它们消失）。


