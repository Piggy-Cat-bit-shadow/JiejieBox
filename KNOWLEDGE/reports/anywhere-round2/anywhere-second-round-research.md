# Anywhere 第二轮独立深度源码研究 与 sing-box/testing 跨项目性能审计

**审计类型：** 只读架构/性能审计（零生产代码改动、零提交、零推送）
**审计窗口：** 2026-10-19 12:15–13:05（Asia/Shanghai）
**审计主机：** Windows 10 19044 LTSC，AMD Ryzen 7 4800H（8C/16T），15.4 GiB，Go 1.25.5 windows/amd64，`CGO_ENABLED=0`
**目标仓库：** `C:\Deepseek\integration-release-next`（`github.com/Piggy-Cat-bit-shadow/sing-box`，分支 `testing`）
**审计起点 HEAD：** `ccc2812e45d8f5fbbca3bc7af14b91bc0ba92a36`（2026-10-19 12:14:40 +0800，`v0.1.20-96-gccc2812e45d`）
**外部参照：** `C:\Deepseek\_anywhere_src`（`github.com/NodePassProject/Anywhere`，`main` @ `89905e0e3641bcbd958a66fcb6f027a2bdecec90`）
**报告路径：** `docs/fork/anywhere-second-round-research.md`（本地新增，未 `git add`、未提交、未推送）

> 证据等级用词固定为：`VERIFIED_SOURCE`（逐行源码核实）、`MEASURED_LOCAL`（本机实测，附环境与命令）、`VERIFIED_CI`（核对到同一 SHA 的真实远端 CI 记录）、`REPORTED_PREVIOUSLY`（历史报告声称，本轮未复测）、`HYPOTHESIS`（有源码依据但未测量）、`BLOCKED_BY_ENVIRONMENT`、`NOT_RUN`。本报告没有把任何 `NOT_RUN` 写成 PASS。

---

## 1. 执行摘要

### 1.1 一句话结论

第二轮源码分析和本机测量支持第一轮的主要判断：采用流式规则集，不引入全局账本或 Swift Arena/TickScheduler，并保留 DNS Copy。但第一轮对规则集加载峰值的定位不够准确：在所测的十万至百万条规则集上，约 95% 的峰值来自 `srs.Read` 的解码瞬态，其中 `varbin.ReadSliceCount` 按元素增长；下载副本约占 4–5%。

本轮提出 **1 个 A 级候选**，位于依赖层，可测量且可回滚；其他候选为 B 级（继续实验）或 C/D 级（已有等价实现或暂不值得实施）。这些判断适用于报告所列审计版本和测试条件。

### 1.2 最值得期待的 5 个方向

| 排序 | 方向 | 证据 | 预期 |
|---|---|---|---|
| 1 | **A1：`varbin.ReadSliceCount` 按声明数量预分配**（替换层 `Piggy-Cat-bit-shadow/sing`） | `MEASURED_LOCAL` + `VERIFIED_SOURCE`：100k 规则集 load-peak 4.1 MiB / live 0.88 MiB；1M 规则集 load-peak 42.5 MiB / live 8.3 MiB；decode:live ≈ 4.6–5.1× | 去掉约 80% 解码瞬态与大部分 GC 周期；500k 规模的 6 次 GC / 520 µs 停顿可望降到 1–2 次 |
| 2 | **B1：CONNECT-IP 入口对每个包复制两次** | `VERIFIED_SOURCE`：`sing-tun/flow_dispatch.go:653` 复制进 stage，`protocol/masque/client.go:649-651` 再 `buf.NewSize`+`Write` 复制一次 | 每包少 1 次分配 + 1 次 memcpy；需给 `Port` 增加携带所有权的批量接口 |
| 3 | **B2：MASQUE 批量 >32 包必然退回逐包路径** | `VERIFIED_SOURCE`：`quic-go/datagram_queue.go:114-116` 直接拒绝 >32，`writeBatchOwned` 无分块 | 突发 >32 包时恢复批量路径，省 N 次队列锁与 N 次唤醒 |
| 4 | **B3：规则集解压没有输出上限** | `MEASURED_LOCAL`：254.8 KiB 输入 → 256 MiB 解压 / 143.6 ms；合法规则数据 20.1 MiB → 217.7 MiB 分配 | 给 `srs.Read` 加解压字节预算，把 CPU DoS 变成有界失败，旧代际保持 |
| 5 | **B4：QUIC 接收侧 128 B 帧使 1452 B 池缓冲无法释放** | `VERIFIED_SOURCE` + 算术：`wire/stream_frame.go:59-82`、`wire/pool.go:11-18`；窗口是信用不是预分配 | 一个 gap + 小帧可把接收内存放大到窗口的 ~11×；按 `cap/2` 降级复制即可 |

### 1.3 最重要的 5 个被否决（或降级）的猜想

1. **把内存 DNS 缓存改成 packed wire bytes** → **C**。实测：默认 1024 容量下只省约 0.31 MiB（含 freelru 开销 781→464 B/entry），而命中路径 CPU 变成 2.2–3.5×（`Copy` 238 ns/200 B/6 allocs vs `Unpack` 761 ns/328 B/12 allocs）。第一轮把它列为 B 是谨慎的；第二轮有数了，**默认容量下不值得**，只有 `cache_capacity` 上万时才重新评估。
2. **去掉 DNS 读侧 `Copy()`** → **D**。缓存对象在交出后会被 `normalizeTTL`、`response.Id`、`Extra` 过滤、以及 transport 的 `restoreOriginalQuestion` 改写；`client_family_hint_test.go:190` 明确验证“读方不得污染缓存条目”。
3. **引入 `FlowBufferLedger` 式全局字节账本** → **D**。本仓库数据面没有受它支配的无界队列：`route` 生产代码没有 channel 队列（只有测试里有），背压靠阻塞写 + 停拉 TUN；强行加账本只会多一套生命周期。
4. **自研调度器 / Arena / 第二复用池** → **D**。`common/trafficsched` 空闲时**不持有 timer**（`scheduler.go:849-861,869-892`），arena 的所有权契约与 `buf.Buffer` 重复，`route/reference.go` 是唯一复用/退休权威。
5. **优化"新旧 matcher 共存"** → **D**。实测 500k 规模旧代只多占一个 live 代（4.23 MiB，约峰值的 20%），窗口 28 ms，默认更新周期 24 h，且每次更新后强制 `runtime.GC()`。

### 1.4 下一步建议

**建议只立项 1 个 A（A1，替换层 `sing` fork 的 `ReadSliceCount`），并先做 1–2 个 B 的计数型实验（B1/B2），不建议做任何 C/D 项。** 理由：A1 有实测、有边界清晰的最小改法、有现成畸形输入测试兜底；B1/B2 需要先证明触发频率；其余候选的收益要么被实测否定，要么与"不引入第二套所有权"的价值取向冲突。

---

## 2. 审计对象与 Git/环境溯源

### 2.1 仓库真实状态

| 项 | 审计开始时 | 审计结束时 |
|---|---|---|
| 仓库 | `C:\Deepseek\integration-release-next` | 同 |
| 分支 | `testing`（与 `origin/testing` 同步） | 同 |
| HEAD | `ccc2812e45d8f5fbbca3bc7af14b91bc0ba92a36` | `45078a78b1d49c2536d71b2847310fd95a8fb8a1`（**外部提交**，见 2.3） |
| 工作树 | 有 1 个**既有**修改：`docs/fork/mieru-masque-final-acceptance.md`（非本审计产生） | 干净；唯一的未跟踪文件是本报告 |
| Anywhere | `_anywhere_src` @ `89905e0e...`，`main`，干净 | 同 |

`git status --short --branch`、`git branch --show-current`、`git rev-parse HEAD`、`git log -1 --format=fuller` 均按只读方式执行；没有 `checkout`/`switch`/`fetch`/`commit`/`push`/`stash`/`reset`/`rebase`/`clean`/`tag`。

### 2.2 依赖层：`go.mod` 的 require 不是实际编译版本

这是本轮第一个必须记录的溯源事实。三个关键依赖都被 `replace` 到 fork，`go list -m` 的 `.Dir` 指向的是替换后的模块：

| 逻辑模块 | `go.mod` require | 实际生效（replace 目标） | 依据 |
|---|---|---|---|
| `github.com/sagernet/sing` | `v0.9.7-0.20260929150544-6f21f2425a95` | `Piggy-Cat-bit-shadow/sing v0.9.6-0.20261011105649-bde7ddda55ef` | `go.mod` replace；`go list -m -f '{{.Dir}}'` |
| `github.com/sagernet/sing-tun` | `v0.9.7-0.20261013140216-9ffa5391a2ad` | `Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261015074151-d092c6459f4d` | 同上 |
| `github.com/sagernet/quic-go` | `v0.61.0-sing-box-mod.9` | `Piggy-Cat-bit-shadow/quic-go v0.61.1-0.20261011111900-4d740322a18a` | 同上 |
| `github.com/sagernet/sing-quic` | `v0.7.2-0.20260929152029-258509488380` | `Piggy-Cat-bit-shadow/sing-quic v0.7.2-0.20261011112547-a3462fc076c9` | 同上 |
| `github.com/sagernet/gvisor` | `v0.0.0-20260727.0-sing-box-mod.1` | 无 replace（但本 fork 的 TUN 默认走 `sing-tun` 的用户态栈，不走 gVisor） | `go list -m` |
| `github.com/miekg/dns` | `v1.1.72` | 无 replace | `go.mod` |

> 任何把 `v0.61.0-sing-box-mod.9` 当作 QUIC 行为的报告都会指错行号。本轮所有 quic-go 锚点都取自**替换后**的 `...!piggy-!cat-bit-shadow\quic-go@v0.61.1-0.20261011111900-...`，并已用 `git diff --no-index` 确认 ACK/重传/frame-sorter/pacer/空闲定时器代码与上游 `sagernet/quic-go@v0.61.0-sing-box-mod.9` 逐行一致，差异只在 DATAGRAM 所有权/批处理、MTU 增长上限与配置上限。

### 2.3 审计期间的外部改动（必须单独记录）

审计进行中（12:32–12:33），仓库出现两个**非本审计产生**的 docs-only 提交：

```
896915952ab docs: record the final CI for the Mieru/MASQUE acceptance
45078a78b1d docs: record the final CI for the Mieru/MASQUE acceptance
```

`git diff --name-only ccc2812e..45078a7` 只有 `docs/fork/mieru-masque-final-acceptance.md`，没有任何 `.go` 变化。因此本报告所有 `file:line` 锚点在 `ccc2812e` 与 `45078a7` 上等价（行号未移动）。这两个提交的 author/committer 是仓库所有者账号，不是本审计（本审计没有执行任何 `git commit`）。

另外一次**副作用及恢复**：本机 `GOFLAGS=-mod=mod`（环境预置），在执行 `go test` 时 Go 工具链向受跟踪的 `go.sum` 追加了 175 行 `/go.mod` 哈希。发现后已用 `git checkout -- go.sum` 恢复，之后 `git status --porcelain` 为空。这是工具副作用，不是源码修改；本报告如实记录。

### 2.4 证据边界

- 本机 **`-race` 不可用**（`CGO_ENABLED=0`，无 mingw-w64 gcc）：`BLOCKED_BY_ENVIRONMENT`。
- Apple 规范 tag（`tfogo_checklinkname0`/`badlinkname`）在 Windows 链接失败：`BLOCKED_BY_ENVIRONMENT`。
- 无 Apple/Android 真机：`phys_footprint`、能耗、设备 jetsam 全部 `NOT_RUN`。
- Linux `splice_*` 与 iOS mmap 行为只做源码核实，未在本机执行。

---

## 3. 历史结论复核矩阵（六份既有报告）

六份文件均位于 `docs/fork/`（不是 `KNOWLEDGE/`）；`KNOWLEDGE/` 未被刷新。下表"源码现状"列全部为 `VERIFIED_SOURCE`（除注明外）。

| # | 文件 | 当时结论 | 本轮源码/实测 | 判定 |
|---|---|---|---|---|
| 1 | `anywhere-deep-study.md` | `FlowBufferLedger` 关键是准入不对称；16 MiB 是登记字节不是 RSS；`InboundQueue` 靠停拉传播背压；Arena 有所有权前提；`TickScheduler` 惰性取消有代价；`buildBulk` 思想已由 srs v3+ 覆盖 | 全部与源码一致 | **成立** |
| 1b | 同上 §10 | "`bufio.CopyPacket` 每数据报一次 `buf.NewSize`（64 B）" | 机制需修正：`copyPacketWithPool` 走 `options.NewPacketBuffer()` → `network/direct.go:93` → `buf.NewSize(64 KiB 或 MTU)`；**payload 数组来自 `sync.Pool`（`buf/alloc.go`），每包新增的是 64 B 的 `*buf.Buffer` 头**；批处理分支（`copy.go:385-442`）会先尝试，TUN 两端都具备批量能力 | **结论成立，机制需更正** |
| 2 | `anywhere-audit-and-construction.md` | 无 cache_file 的 binary 规则集走流式 `srs.Read` + EOF drain；分配从 21,229,120 B 降到约 125 KB | `rule_set_remote.go:249-259,342-351` 与 `common/srs/binary.go:92-104` 均已核实；本轮复跑 `TestRemoteRuleSetStreamsBinaryWithoutCache` 得到 **120,584 B**（历史值 119,848–125,464 B 的抖动范围内） | **成立** |
| 2b | 同上 §4 | "这是瞬态/峰值结果，不是吞吐结论" | 正确，但峰值份额被 4 MiB 尾部夹具放大：真实 100k 规则集 srs 仅 218.1 KiB、1M 仅 1.6 MiB，只占 load-peak 的约 5%/4% | **成立但需重新定位**（见 §5 A1） |
| 3 | `anywhere-delivery-summary.md` | 结论摘要与 Git 边界 | 与源码一致 | **成立** |
| 3b | 同上 | "没有无界队列可以放进全局账本" | `route` 生产代码确实没有 channel 队列（grep 只命中 `_test.go`）；但依赖层有有界队列（sing-tun dispatch stage、quic-go 32/128、hysteria2 64） | **成立，需限定为"本仓库自己的代码"** |
| 4 | `anywhere-debug-audit.md` | BUG-02-1 截断体不得发布；BUG-02-2 zlib trailer 未校验；drain 在 publish 之前 | `loadBinaryStream` 顺序与错误返回已核实；`srs.Read` 的 `io.Copy(io.Discard, bReader)` 在 `binary.go:92-104`；`publishRules` 只有一个赋值点 `:281-285` | **成立** |
| 4b | 同上 §7 | "校验和验证会解压剩余部分，是畸形输入的 CPU 成本变化，被记录而非隐藏" | 方向正确，但当时没有测：本轮实测 254.8 KiB 输入 → 256 MiB 解压 / 143.6 ms，约 1700:1；**没有任何输出字节上限** | **成立但低估**（见 §5 B3 / §6.2） |
| 5 | `anywhere-debug-regression-list.md` | 回归项 NEG-02-1..15、ENV-02-1..5 | 本轮复跑 `./common/srs`（PASS）、`./route/rule` 的流式/截断用例（PASS）；本地 `-race` 与 Apple tag 仍 `BLOCKED_BY_ENVIRONMENT`；CI 记录未在本轮重新访问 | **成立**（CI 为 `REPORTED_PREVIOUSLY`） |
| 6 | `anywhere-debug-summary.md` | 修复与未验证项摘要 | 与源码一致 | **成立** |

### 3.1 第一轮没有解决、第二轮给出数据的三件事

1. **"规则 matcher 已很紧凑，没有剩余构建峰值"**：**表示**确实紧凑（100k live 882.4 KiB ≈ 8.6 B/域名；1M live 8.3 MiB ≈ 8.7 B/域名），但**构建瞬态**是 live 的 4.6–5.1×（100k 4.1 MiB、1M 42.5 MiB）。"没有表示层收益"成立；"没有构建峰值收益"**不成立**。
2. **"DNS packed 缓存是 B"**：已有数（§5 C1）→ 降为 **C**。
3. **"`minIdleKeep` 只是参数研究"**：`route/reference.go` 仍然只是 keep-idle 开关集合（`keepIdle map[any]bool`、`SetKeepIdleConnections`），没有池；第一轮的判断不变。

### 3.2 与 `KNOWLEDGE/` 的冲突

第一轮已记录 `KNOWLEDGE/modules/buffer-ownership.md` 的 splice `handed` 记录过期（锁定依赖已修复）。本轮**未重新执行** Linux 专项测试（Windows 主机，Linux-only 文件），维持第一轮的 `VERIFIED_SOURCE` + `BLOCKED_BY_ENVIRONMENT` 结论；`KNOWLEDGE/` 按维护规则**未刷新**。

---

## 4. 完整数据流与所有权图

两条纵向链路均按"层 → 函数/file:line → 输入 buffer 拥有者 → 输出 buffer 拥有者 → 复制/分配 → 批量接口 → 背压规则 → 失败释放"整理。标注 `[V]` 的行由本审计逐行核实；标注 `[S]` 的行来自本轮同一会话的定向源码走查（锚点已抽查，未逐行复核）。

### 4.1 路径 A：TUN → TCP → 出站 → socket

| 层 | 函数 / file:line | 输入 owner | 输出 owner | 复制/分配 | 批量 | 背压 | 失败释放 |
|---|---|---|---|---|---|---|---|
| A1 OS 读（Windows）`[S]` | `sing-tun/stack_go_io_windows.go:284` `readBurst`；`tun_windows.go:623` `copy` | wintun ring（驱动） | `goReadSlots` 槽池 | 1 次复制；稳态 0 分配 | ≤ `goReadBatch=64`，无批量 syscall | ring 满即丢（`tun_windows.go:590`） | 槽复用 `releaseReadBuffers` |
| A2 OS 读（Darwin）`[S]` | `sing-tun/stack_go_io_darwin.go:314` `readBurst`、`:374` `receiveMessageBatch`（`SYS_RECVMSG_X`） | 内核直接写入 iovec | 槽池 | 0 复制、0 分配 | 一次 `recvmsg_x` 收 ≤64 | kqueue 读兴趣移除 | 槽复用 |
| A3 TCP 重组 `[S]` | `stack_go_engine.go:624` `processBurst` → `stack_go_tcp_input.go` | 槽 buffer（借用） | `GoConn.receiveChain` slab | 1 次 payload 复制 | burst 预算 1 MiB | 接收窗口 / `writeBudget` | `consumedTail` 释放 |
| A4 交接 `[V]` | `route/conn.go:316` `NewConnection`；`route/splice.go:52-130`（`:107` `goConn.Splice`） | GoConn | 出站 conn | 无 | — | — | splice 被拒 → 普通复制路径 `:128` |
| A5a 直连（splice）`[V]` | `sing-tun/stack_go_splice.go:329` `spliceFlushUpload`、`:394` `spliceReadPeer` | receiveChain runs | socket / transmitStore | **0 用户态复制**（iovec `:337-338,410-411`） | 每 run 一次 `writev`/`readv` | EWOULDBLOCK → `splice.blocked`，停止读，可写时恢复 | `abortConn` |
| A5b 代理复制环 `[V]` | `route/conn.go:414` `uploadStreamGate` → `common/trafficsched/gate.go:74-86`；循环 `route/conn.go:901-948` → `sing/common/bufio/copy.go:27` → `copy_direct.go:25-68` → `stack_go_conn.go:501-541` | GoConn chain | `*buf.Buffer`（`network/direct.go:53-82`，≤32 KiB） | 1 次 chain→buffer 复制；稳态 0 分配（池） | 每个 buffer 一次 `WriteBuffer`；`GoConn` 没有 `CreateVectorisedReadWaiter`，writev 升级**不可达**（`copy_direct.go:56-58`） | gate `flow.wait` 阻塞复制 goroutine → TUN 窗口关闭 → socket 写阻塞 | 写入方 `defer buffer.Release()`（`sing/common/bufio/conn.go:158-161`）；错误时 `common.Close` 双向关闭 |
| A6 socket `[V]` | 出站 `net.Conn.Write` / `writeVector` | — | — | — | 代理路径每 MTU 一次写 syscall | socket send buffer | 取消 → `Scheduler.ReleaseAll`（`conn.go:153-155`） |

**当时的结论：** Apple TUN 入口使用零复制和批量 `recvmsg_x`；Windows 受 wintun API 限制，需要一次复制且没有批量 syscall；Linux 每包一次 read（GSO 聚合见下文）。审计认为这条路径没有值得进一步消除的重复复制，A5b 的一次复制用于转移所有权。此结论限定于本报告的代码快照。

### 4.2 路径 B：TUN → UDP → CONNECT-IP（MASQUE）→ HTTP/3 → QUIC → socket

| 层 | 函数 / file:line | 输入 owner | 输出 owner | 复制/分配 | 批量 | 背压 | 失败释放 |
|---|---|---|---|---|---|---|---|
| B1 OS 读 | 同 A1/A2 | ring/内核 | 槽池 | 1（Win）/0（Darwin） | ≤64 帧 burst | 无 | 槽复用 |
| B2 UDP 转发 `[V]` | `sing-tun/flow_dispatch.go:608` `forwardToPort` → `:653` `copyForStage`（`:658-662` `copy`）→ `:655` `stagePort` → `:724` `port.WritePackets` | 槽 buffer（借用） | staged 段（`segmentBuffers` 复用 `:731-736`） | **每包 1 次复制** | 每端口一个 `[][]byte`，每 `Flush` 一次 | 每 engine loop / tick flush | 段复用，所有权不出层 |
| B3 endpoint 边界 `[V]` | `protocol/masque/client.go:641-654`：`buf.NewSize(PacketHeadroom+len)` `:649-651` | staged `[]byte` | 新 `*buf.Buffer` | **每包 1 次分配 + 第 2 次复制** | 已是批量 `[][]byte`，但逐包再复制 | 无 | 返回错误，调用方记录并丢弃（`flow_dispatch.go:724-727`） |
| B4 session `[V]` | `transport/masque/session.go:470` `writePackets`；批量 `:472-478` → `:566` `writeBatchOwned`；逐包回退 `:480-524` | 调用方 buffer | transport（owned） | 批量 `prepared := make(...)` `:567`（每批 1 次） | n>1 走 `SendDatagramsOwned`；n=1 走原循环 | datagram 路径无锁；`writeAccess` 只护 capsule 回退 | 全有或全无：拒绝 → 全部 `Advance(1)`（`:590-592`）；错误 → `Release`+`ReleaseMulti`（`:518-520`）；成功 → transport 拥有 |
| B5 HTTP/3 `[V]` | `transport/http/client_h3.go:539-552` → `quic-go/http3/conn.go:363-406` | `OwnedDatagramPayload` | QUIC datagram 队列 | 前缀全部，失败全部回滚（`:401-404`）；不复制 payload | `SendDatagramsOwned` | — | 仅 nil 时转移；`Release` 只在序列化时执行一次 |
| B6 QUIC 队列 `[V]` | `quic-go/datagram_queue.go:108-150` `AddBatch`；`:14` `maxDatagramSendQueueLen=32` | payload owner | 队列 frame | 每批 `frames := make(...)` + 每帧 `&wire.DatagramFrame` | 一次 `sendMx` + 一次 `hasData()` | 全部装得下才入队（`:128`）；满时阻塞 `h.sent`（`:143-148`）；**>32 直接拒绝**（`:114-116`） | 永不入队前缀；close 时 drain 并释放 |
| B7 序列化 `[V]` | `quic-go/internal/wire/datagram_frame.go:99-120`：`:108` `append(b, f.Data...)`、`:120` `f.Release()` | frame | 发包 buffer | 1 次复制（帧头+payload 连续，GSO 要求） | packetizer 组包 | 拥塞/pace | 最后一个读者释放 |
| B8 socket `[V]` | `quic-go/send_queue.go:91` → `send_conn.go:73-98`（GSO `:111-112`） | packet buffer | 内核 | 0 | 每个聚合 ≤64 KiB 一次 syscall | socket send buffer | GSO 失败 → 关 GSO 重试单包 |

**当时的结论：** B3 的第二次复制和 B6 超过 32 个数据包时的批量退化，分别列为第二轮 B1、B2 候选；审计认为 B7 的复制因 QUIC 帧连续性及 GSO 要求不能省略。后续是否实施，应查看对应构建和收尾报告。

### 4.3 DNS 控制面所有权图（`VERIFIED_SOURCE`）

```
query
 └─ Router.exchange（dns/router.go，generation 捕获）
     └─ Client.Exchange / ExchangeAsync（dns/client.go:1044/1062）
         ├─ beginExchange（:826 LoadOrStore(exchangeKey, make(chan struct{}))——命中也会分配）
         ├─ loadResponse（:1358-1390）
         │    ├─ fresh  → response.Copy() → normalizeTTL(remaining) → caller owns
         │    ├─ stale  → response.Copy() → normalizeTTL(1) → message.Copy() 给后台刷新
         │    └─ miss   → NXDOMAIN 缓存（:859）→ 上游（transport 返回全新 Unpack 对象）
         ├─ finishExchange（:907+）
         │    ├─ applyResponseOptions（:1535-1541，原地 normalizeTTL）
         │    ├─ commitCacheState（generation/policy 守卫）
         │    │    └─ storeCache（:1269-1289）：内存 → message.Copy()；持久 → message.Pack()
         │    └─ cachedResponseForQuery（:1483-1493，仅 HTTPS 严格族策略时再 Copy 一次）
         └─ 返回给调用方（调用方会改 Id/Extra/Question）
```

- **缓存对象的唯一持有者**是 `freelru.Cache[dnsCacheKey, *dns.Msg]`（`client.go:102`，容量 `max(cache_capacity, 1024)`，`:185`）与 NXDOMAIN 缓存（`:105`）。
- **Copy 是所有权边界**：缓存条目绝不外借；每次命中产生一个调用方独占对象，调用方随后修改它（`:843,849,965,969-971`；`transport/exchange_strategy.go:198-204`）。
- **generation 不是 key 的一部分**（`:229-239`），而是 store/deliver 守卫（`:304-316,402-415`）；`ResetNetwork` 只清反向映射（`router.go:2286-2333`，`:1508`），不清 exact cache —— 这是**刻意设计**（`router.go:1502-1506` 注释），由 `environment` 字段做网络命名空间隔离。
- **键包含** question、`transportTag`、`clientSubnet`、`environment`（transport 固定的网络环境哈希，`:584-604`）；`dnsExchangeKey` 额外含 `timeout`，所以不同超时的查询不合并。

### 4.4 规则集控制面所有权图（`VERIFIED_SOURCE`）

```
HTTP body (net/http)
  └─ RemoteRuleSet.fetch（rule/rule_set_remote.go:303-373）
      ├─ cacheFile == nil && format == binary → loadBinaryStream(body)（:342-344）
      │    ├─ srs.Read(body)（:250）—— 流式解压+建 matcher
      │    ├─ io.Copy(io.Discard, body)（:254）—— 读 HTTP body 到 EOF，截断即失败
      │    └─ publishRules(ruleSet)（:258）
      └─ 其它 → io.ReadAll + loadBytes（:346-350）
           └─ publishRules
                ├─ mmapRuleSet(...).Upgrade()（:265；iOS 会写临时文件并 mmap）
                ├─ NewHeadlessRule × N（:269-275）
                ├─ buildRuleSetMetadata（:276）
                ├─ validateRuleSetMetadataUpdate（:277-280）
                └─ access.Lock(); metadata/rules 赋值; Unlock(); callbacks（:281-288）
```

- **发布点唯一**，失败路径全部在此之前返回，旧代际保持（`rule_set_remote.go:281-285`）。
- **旧代际释放**：没有 per-generation `Close`；`Cleanup`/`Close` 置 nil；iOS mmap 的 `Munmap` 由 `runtime.AddCleanup` 在 GC 时执行（`common/srs/mmap.go`），`rule_set_updater.go:88-90` 每次更新后强制 `runtime.GC()`。
- **iOS 的 mmap 在 parse 之后**：`mmapRuleSet` 先 `srs.WriteMmap` 再 `ReadMmap`（`rule_set_mmap_ios.go:24-43`），因此它降低**稳态**驻留，不降低**解析瞬态**；非 iOS 是 `rule_set_mmap_stub.go:12-14` 原样返回，全程 Go 堆。

### 4.5 背压与释放总表

| 资源 | 上限 | 满时行为 | 所有者 | 释放点 |
|---|---|---|---|---|
| TUN 读 burst | 64 帧 / 1 MiB（engine） | 不再读 ring（停拉） | sing-tun engine | 槽复用 |
| UDP dispatch stage | 每端口 `[][]byte`，按 burst | `Flush` 同步写出 | ForwardStage（复用段） | `:731-736` 截断复用 |
| MASQUE 批量 | quic 队列 32 | >32 拒绝 → 逐包回退 | session | 拒绝/错误时归还 |
| QUIC 发送 datagram | 32（阻塞） | 阻塞直到 drain 或关闭 | quic-go | close drain / 序列化后 |
| QUIC 接收 datagram | 128 | **静默丢弃** | quic-go | 出队交给应用 |
| QUIC 乱序 gap | 20000 | 连接被关 | frameSorter | 连接关闭 |
| QUIC sent history | 40k/50k **包数**，无字节上限 | 停止发送/只发 ACK | ackhandler | ACK/丢失/超时 |
| hysteria2/TUIC 入站 | 64 槽 channel，非阻塞丢 | 丢弃 | sing-quic | 消费者释放 |
| DNS exact cache | `max(cache_capacity,1024)` | LRU 淘汰 | Client | LRU/`ClearCache` |
| DNS 单飞 | map（按 exchangeKey） | 等待或 `exchangeWait` | Client | `operation.release()` |
| 规则集更新 | 每 set 一个串行 goroutine | 无队列 | RuleSetUpdater | 更新结束 |

**这张表能证明"本仓库没有无界数据面队列"，但不能证明"全系统 RSS 严格有界"**：依赖层缓冲、Go 堆碎片/GC 滞后、iOS mmap 的页驻留、以及下文的解压放大都不在这张表里。

## 5. 第二轮新审计发现

以下候选按任务要求的字段记录。A0 是本轮发现的缓冲区生命周期缺陷，与函数注释描述不一致；所有 A 级条目仅提出实现建议，本次审计没有修改代码。

### 5.1 A0（新 BUG，最高优先）：被劫持的 UDP DNS 被解析两次，第二次读的是已可能被回收的 TUN 读缓冲

```
Candidate ID: A0
问题与真实入口：
  TUN 收到一个发往 53 端口的 UDP 包 → sing-tun ForwardDispatcher 判定 ActionHijackDNS
  → protocol/tun/inbound.go NewDNSPacket → route.Router.HijackDNSPacket
Anywhere 源码：
  89905e0e:Anywhere Network Extension/Tunnel/PacketFlowBridge.swift:44-65 —— InboundBatch 只借用
  CFData 的指针，生命周期止于队列条目；需要跨异步边界的解码器自己复制。本缺陷违反的正是这条规则。
当前 testing / 依赖源码：
  ccc2812e45d8:route/dns.go:113-137（HijackDNSPacket）
    :115  err := message.Unpack(payload)      // 解析 #1，结果只用于校验，随后丢弃
    :136  r.dispatchDNSPacket(ctx, payload, writer, metadata)  // 传的是 payload，不是 message
  ccc2812e45d8:route/dns.go:56-74（dispatchDNSPacket）
    :58   err := message.Unpack(payload)      // 解析 #2，在另一个 goroutine 里、更晚发生
  ccc2812e45d8:protocol/tun/inbound.go:1134-1145（NewDNSPacket 原样转交 payload）
  sing-tun v0.0.0-20261015074151-d092c6459f4d:flow_dispatch.go:428-431（内联调用）
  sing-tun flow_dns.go:19（header.UDP(packet.transport).Payload() —— 一个视图）
  sing-tun flow_parse.go:120（p.transport = payload —— 仍是读槽视图）
  sing-tun stack_go_engine.go:647-670（processFrame 直接用 frame.buffer.Bytes()）
  sing-tun stack_go_io_windows.go:284-309（:291 buffer.Reset(); :294 receiveInto(buffer.FreeBytes())）
现有实现或重复点：
  解析 #1 已经完成全部校验，但没有被使用；解析 #2 重复做同样的工作。函数自己的注释（dns.go:102-106）
  写的是 "Parsing here and passing the copied message keeps the payload's lifetime confined to this
  call"，而代码把 payload 交给 goroutine。引入提交 994cc243a47 的提交说明也写的是 "parse the packet in
  the caller … then hand the exchange to a goroutine"。
瓶颈证据 / 反证：
  1) 重复解析成本：本机用锁定版本 miekg/dns v1.1.72 实测 dns.Msg.Unpack：A 查询 182–188 ns/40 B/2 allocs；
     EDNS0 AAAA 358 ns/152 B/5 allocs。每个被劫持的 DNS 包多付一次。
  2) 生命周期：payload 是 sing-tun 读槽的子切片；下一轮 readBurst 会 Reset 并复用同一个 *buf.Buffer。
     goroutine 可能读到半覆盖的数据：Unpack 失败（客户端无响应）或解析出错误的 name/ID（答非所问）。
     256 个在途上限（dns.go:83）意味着最多 256 个 goroutine 各持有一个读槽视图，最长 C.DNSTimeout（10 s）。
  3) 反证尝试全部失败：第一个解析结果没有被传给任何人；没有任何 bytes.Clone/append 复制；
     dns_hijack_isolation_test.go:62/104/226 都传入测试自有的稳定 []byte，覆盖不到重用；
     TestDNSPacketHijackConcurrencyIsBounded 用同一个 payload 调 512 次，即使修好也区分不出来。
  4) dispatchDNSPacket 只有一个调用点（dns.go:136），改签名没有外部影响。
若已测量：环境 = Windows 10 19044 / Ryzen 7 4800H / Go 1.25.5；命令 = 外部 %TEMP% 模块
  go test -run '^$' -bench 'BenchmarkUnpackQuery' -benchmem -count 2；原始值见上，两次重复一致。
可选最小施工方案（仅建议，不执行）：
  HijackDNSPacket 解析一次后把 &message 传给 dispatchDNSPacket，删除 dispatchDNSPacket 里的 Unpack；
  payload 参数从签名中移除。约 5 行，所有权不变，256 上限/context/writer/metadata 全部不动。
对生命周期、buffer 所有权、DNS/规则集代际、协议兼容和 UI 的影响：
  从"跨 goroutine 持有借用视图"变为"跨 goroutine 持有独占 dns.Msg"，严格更安全；不新建生命周期、
  不新建缓存、不改 generation 守卫、不改线协议。
将来必须加的回归测试：
  (a) 在 HijackDNSPacket 返回后立刻覆写 payload 的内容，等待 exchange 完成，断言问题名/ID 不被污染；
  (b) 用 sync.Pool 复用同一 []byte 连续投递两个不同查询，断言两个响应各自正确；
  (c) go test -race 覆盖该路径（本机不可跑，需 CI/带 cgo 的主机）；
  (d) 保留 malformed / 256 上限两个现有测试。
收益强度 / 复杂度 / 风险：高（正确性 + 每查询省一次解析）/ 低（5 行）/ 低（既有测试按构造仍通过）
判定理由：A —— 源码级矛盾 + 可测的重复成本 + 可证明的借用生命周期，修复边界清晰，且属于"只记录不施工"。
```

> 说明：这个缺陷是**统计口径上的"真实 BUG"**，不是性能优化。它之所以出现在一份性能审计里，是因为追"每包分配/复制"时顺着 packet loop 走到了 `dispatchDNSPacket`。按任务边界，本报告**只给复现步骤、最小修复思路和测试建议，没有改动任何代码**。

### 5.2 A1（性能，最高优先）：规则集解码瞬态约为最终 matcher 的 5 倍

```
Candidate ID: A1
问题与真实入口：
  远端/本地 binary 规则集加载：srs.Read → domain.ReadMatcher → varbin.ReadSliceCount。
  真实入口是 route/rule/rule_set_remote.go:250（流式）与 :214（read-all），最终都走
  common/srs/binary.go:200 domain.ReadMatcher(reader)。
Anywhere 源码：
  89905e0e:Shared/Networking/Routing/TieredRouteMatcher.swift:316-317（reserveIPv4/reserveIPv6）
  89905e0e:Shared/Networking/Routing/FlatLabelTrie.swift:326（buildBulk 按声明数量一次性构建）
当前 testing / 依赖源码：
  ccc2812e45d8:common/srs/binary.go:200 → Piggy-Cat-bit-shadow/sing@v0.9.6-0.20261011105649-bde7ddda55ef
  common/domain/matcher.go:53-59 ReadMatcher → common/domain/set.go:155-187 readSuccinctSet
  → common/varbin/slices.go:21-33 ReadSliceCount：
      for uint64(len(result)) < count {
          result = slices.Grow(result, 1)     // 每次只长 1 个元素，几何扩容+整段复制
          ...
      }
现有实现或重复点：
  count 在流里是已知的（uvarint 前缀），但实现选择逐元素增长；每次扩容把旧内容整段复制，
  总分配约为最终切片容量的 5–6 倍。矩阵构建本身（NewHeadlessRule）实测只有 1.5 KiB，
  不是峰值来源；下载副本也不是（见下）。
瓶颈证据 / 反证：
  (1) 本仓库自带的 scale census（MEASURED_LOCAL，见 §7）：
      10k 域名：srs 23.2 KiB，live 119.0 KiB，load-peak 537.0 KiB；stage breakdown：
        compressed-read 25.1 KiB / inflate 52.8 KiB / decode 518.1 KiB / rules 1.6 KiB / steady 23.9 KiB
      100k 域名：srs 218.1 KiB，live 882.4 KiB，load-peak 4.1 MiB（decode/live ≈ 4.67×）
      1M 域名：srs 1.6 MiB，live 8.3 MiB，load-peak 42.5 MiB，10 次 GC，停顿合计 691.1 µs（≈5.12×）
      100k CIDR：srs 501.6 KiB，live 1.0 MiB，load-peak 7.8 MiB
  (2) 第一轮的 120,584 B 流式测试仍然通过；但真实 100k 规则集的下载体只有 218.1 KiB，
      占 4.1 MiB 峰值的约 5%。第一轮的 21,229,120 B 对比是被 4 MiB 尾部夹具放大的。
  (3) 反证：如果 decode/live ≈1.1×，该候选即死。实测 4.6–5.1×，且 census 注释本身把
      load-garbage 定义为"parse transient：zlib 窗口、每个数组的增长余量、fixture 解码头"。
可选最小施工方案（仅建议，不执行）：
  在 ReadSliceCount 里做有界预分配：capHint = min(count, maxElems)（maxElems 取一个安全上限，
  例如 16Mi 元素或"剩余可读字节/元素大小"的估计），make([]T, 0, capHint)，再一次性 binary.Read
  到 result[:count]；count 超过上限时保留现有逐元素循环，保证恶意 count 仍然在 EOF 处廉价失败。
  同一处给 srs.Read 加 defer compressReader.Close()（当前 :73 从未 Close，32 KiB flate 窗口每次加载重建）。
对生命周期、buffer 所有权、DNS/规则集代际、协议兼容和 UI 的影响：
  解析字节完全相同，publishRules/校验/旧代际保持全部不变；只改峰值与 GC 压力。属于替换层
  Piggy-Cat-bit-shadow/sing 的改动，需要一次依赖发版，不涉及本仓库源码。
将来必须加的回归测试：
  common/srs/malformed_test.go 的 huge_* 用例必须仍然廉价失败；truncated_test.go 不变；
  census 增加断言 decode alloc ≤ 1.5× live；新增一个"恶意 count 不得预分配超过上限"的用例。
收益强度 / 复杂度 / 风险：高（1M 规模去掉约 80% 解码瞬态与多数 GC 周期）/ 中（依赖层 + 有界预分配）/
  中（count 由对端控制，必须保留有界回退；影响所有 varbin.ReadSlice 调用方）
判定理由：A —— 实测、根因单一、最小改法边界清晰、有现成畸形输入测试兜底。注意它落在依赖 fork，不在本仓库。
```

### 5.3 B1：CONNECT-IP 入口对每个包复制两次

```
Candidate ID: B1
问题与真实入口：TUN UDP 转发 → sing-tun ForwardStage → MASQUE ClientEndpoint.WritePackets。
Anywhere 锚点：PacketFlowBridge.swift:44-65（包以 CFData 指针贯穿，不在半路再复制）。
当前锚点：
  sing-tun flow_dispatch.go:653/658-662（copy #1：copy(staged[0], raw)）→ :724 port.WritePackets
  ccc2812e45d8:protocol/masque/client.go:641-654（copy #2：buf.NewSize(PacketHeadroom+len)+Write）
  下方 transport/masque/session.go:470-531/566-593 已经是 owned/批量路径，只差入口这一层。
现有实现或重复点：session 已支持 SendDatagramOwned/SendDatagramsOwned；endpoint 入口仍逐包复制。
瓶颈证据 / 反证：
  两次复制在 meta != nil 的转发路径上都是无条件的；第一层的 staged 段是复用的，所以不能直接把
  它的指针交出去（这正是第二层复制存在的原因）。反证：若 dispatcher 能直接分配带 headroom 的
  *buf.Buffer 并转移所有权，第二次复制即可去掉，分配次数不变、复制次数 −1。
若已测量：HYPOTHESIS（未跑吞吐）；证据是源码路径。
可选最小施工方案（仅建议，不执行）：给 tun.Port 增加可选接口 WritePacketBuffers([]*buf.Buffer)；
  dispatcher 对实现该接口的 endpoint 直接分配 buf.NewSize(PacketHeadroom+len)；endpoint 直接
  session.writePackets；拒绝路径已有全有或全无的回滚。
影响：所有权从"复用段"变为"每包一个 buffer"，分配点前移，次数不变；无新生命周期。
测试：transport/masque/batch_owned_test.go 的双重释放/拒绝回退；dispatcher 侧"成功/拒绝各释放一次"。
收益/复杂度/风险：中 / 中 / 中。
判定理由：B —— 复制真实存在，但需要一个跨包能力接口，且收益需真实吞吐数据支撑。
```

### 5.4 B2：MASQUE 批量 >32 包必然退回逐包路径

```
Candidate ID: B2
问题与真实入口：TUN 一个 read burst 最多 64 帧（sing-tun goReadBatch=64），同一条 CONNECT-IP
  flow 的多个包在 ForwardStage.Flush 时一次交给 WritePackets。
Anywhere 锚点：PacketFlowBridge 每次读都整批处理，没有 32 条消息的固定上限。
当前锚点：
  transport/masque/session.go:470-478（n>1 先试批量）→ :566-593 writeBatchOwned
  quic-go datagram_queue.go:14（maxDatagramSendQueueLen=32）、:114-116（len(frames)>32 直接返回错误）
  拒绝后 session.go:480-524 对整批逐包重发。
现有实现或重复点：没有分块；逐包路径本身正确。
瓶颈证据 / 反证：确定性——len>32 ⇒ 直接错误、不入队、全部回滚。是否经常发生取决于同一 flow
  的 burst 深度（未计数）。反证条件：若 dispatcher 从不让单个 port 收到 >32，候选即死。
若已测量：NOT_RUN；需要一个 fallback 计数器。
可选最小施工方案：writeBatchOwned 按 ≤32 分块，每块一个全有或全无单元；块 1 成功、块 2 被拒时
  只把块 2 的 buffer 交回逐包循环。
影响：每块独立转移；当前"整组回滚"不变量变成"每块回滚"，调用方已按剩余 buffer 重跑，语义成立。
测试：33/64 包必须走批量分块；块 2 拒绝不得双重释放块 1；batch_order_test.go 扩到 64。
收益/复杂度/风险：中 / 低 / 中。
判定理由：B —— 代码级确定，但触发频率未测；先加计数，再决定是否施工。
```

### 5.5 B3：规则集解压没有输出字节上限（CPU 放大 + 合法数据内存放大）

```
Candidate ID: B3
问题与真实入口：remote binary rule set 的 body 直接进 srs.Read，无任何解压上限。
Anywhere 锚点：AWCore.swift:632-633 —— 路由负载是有界磁盘文件（mappedIfSafe），不是无界网络流。
当前锚点：common/srs/binary.go:73（zlib.NewReader）、:100（io.Copy(io.Discard, bReader)）；
  rule_set_remote.go:250/254；zlib reader 从未 Close。
现有实现或重复点：只有完整性校验（checksum/truncation），没有大小/工作量预算。
瓶颈证据 / 反证（MEASURED_LOCAL，外部探针）：
  63.7 KiB 文件（1 条规则 + 64 MiB 零尾巴）→ 46.68 ms，分配 0.05 MiB（内存平稳，CPU 放大 ~1000:1）
  254.8 KiB 文件（+256 MiB 零尾巴）→ 143.63 ms，分配 0.05 MiB
  5.0 MiB 文件（65536×128B domain_keyword）→ 103.11 ms，分配 54.35 MiB（≈6.5× 解压后大小）
  20.1 MiB 文件（262144×128B）→ 407.79 ms，分配 217.65 MiB
  反证：若任何 io.LimitReader/预算存在，候选即死——grep 没有。
若已测量：上述四条，命令与夹具见 §7。
可选最小施工方案：给 srs.Read 一个解压字节预算（bufio.NewReader(io.LimitReader(compressReader, N))，
  N = max(64 MiB, 64×压缩大小) 或配置项），超限返回独立错误；drain 用同一个受限 reader。
  完整性校验原样保留。另加 defer compressReader.Close()。
影响：超限在 publish 之前失败 ⇒ 旧代际保持，与其它失败分支一致。
测试：truncated/malformed 全套不变；新增"500 KiB 输入解压超预算必须在有界时间/内存内失败"；
  TestRemoteRuleSetStreamDoesNotPublishATruncatedBody 仍通过。
收益/复杂度/风险：中（安全/健壮性）/ 低 / 中（合法超大列表需要配置项）。
判定理由：B —— 成本已实测，但"上限设多少"是产品决策，且不是性能收益。
```

### 5.6 B4：QUIC 接收侧 128 B STREAM 帧使 1452 B 池缓冲无法释放

```
Candidate ID: B4
问题与真实入口：对端发送乱序的小 STREAM 帧；接收侧 frameSorter 保存帧的 Data 直到 gap 填上。
Anywhere 锚点：AnywhereQUIC/Streams/ReceiveBuffer.swift:123-147 —— 按剩余长度精确分配。
当前锚点：quic-go（替换后）internal/wire/stream_frame.go:59-82：
  dataLen < 128 → 精确 make；否则 GetStreamFrame()（cap=MaxPacketBufferSize=1452，pool.go:11-18），
  frame.Data = frame.Data[:dataLen]。frame_sorter.go:22-26 保存 Data；:171-185 只在"被切小"时改精确复制。
  流控窗口是信用上限，不是预分配：params.go:24-34（初始 2 MiB/流、最大 6 MiB/流、连接 15 MiB）。
现有实现或重复点：只有 <128 B 的帧会被精确复制；128–1451 B 的帧保留池缓冲。
瓶颈证据 / 反证（算术，HYPOTHESIS/NOT_RUN）：
  2 MiB 初始窗口 ÷ 128 B = 16384 帧 × 1452 B ≈ 23.8 MiB；6 MiB 上限 ≈ 68 MiB；15 MiB 连接窗口 ≈ 170 MiB。
  反证：正常 MTU 大小的帧放大只有 ~1.1×；顺序到达时队列为空。需要"一个 gap + 小帧"才成立。
可选最小施工方案：frameSorter.push 时若 cap(Data) > 2*len(Data)，复制到精确切片并 PutBack。
影响：解析时归还池缓冲，每次小帧多一次小分配；DoneCb 必须只执行一次，不能双重释放。
测试：frame sorter/stream 单测；N×128 B 乱序的 AllocsPerRun 上界；重复帧 DoneCb 只跑一次；流控违规。
收益/复杂度/风险：中（恶意/异常对端）/ 低 / 低。
判定理由：B —— 算术上确实放大，但正常流量不触发，需要可重复的弱网/恶意对端测试。
```

### 5.7 B5：hysteria2/TUIC 出站数据报复制两次，owned API 已存在却未使用

```
Candidate ID: B5
问题与真实入口：hysteria2/TUIC 的 UDP 出站。
Anywhere 锚点：ConnectionCore+Send.swift:27-56（直接写进输出缓冲，无中间 pack 缓冲）。
当前锚点：sing-quic hysteria2/packet.go:71-82（pack() 分配并复制）→ :288-291 SendDatagram(...)；
  quic-go connection.go:3102-3109（SendDatagram 内部再复制一次）。
  同仓 transport/masque/session.go:484-489 已用 SendDatagramOwned 避免第二次复制。
现有实现或重复点：owned 契约已在 quic-go fork 与 masque 中实现，hysteria2/TUIC 未接。
瓶颈证据 / 反证：两处复制在源码中显式可见；第二个可去，线格式不变。
  反证：若 sing-quic 已调用 SendDatagramOwned——grep 显示只有 SendDatagram。
若已测量：HYPOTHESIS/NOT_RUN；省 1 次分配 + 1 次 memcpy/数据报。
可选最小施工方案：pack() 返回实现 DatagramOwner 的 buffer，调用 SendDatagramOwned(buf.Bytes(), owner)；
  成功不 Release，失败按 owned 契约归还。
影响：所有权转移必须精确一次；DiscardFront/close drain 已覆盖丢弃路径。
测试：owned_datagram_test.go + batch_owned_test.go 的所有权用例；错误回滚；重连泄漏。
收益/复杂度/风险：中 / 中 / 中（双重释放风险）。
判定理由：B —— 真实复制，但属协议实现细节，需要协议级回归。
```

### 5.8 B6：quic-go 接收队列 reslice 不置空，已交付的数据报仍可达

```
Candidate ID: B6
问题与真实入口：应用读走数据报之后。
当前锚点：quic-go datagram_queue.go:223-225（h.rcvQueue = h.rcvQueue[1:]，未置空 rcvQueue[0]）；
  http3/state_tracking_stream.go:191-193（同样）。
现有实现或重复点：仓库在 transport/masque/session.go:186-198 引用这段代码证明"所有权已交出"——
  对所有权是对的，对保留（retention）不完整。
瓶颈证据：被弹出的 slice header 仍留在底层数组里，Go GC 扫描整个数组对象；上限 128（+32）
  个独立 make 的 payload，≤128×~1.4 KiB ≈ 180 KiB/连接，只在队列非空时成立。
  反证：队列容量本身就限死了数组长度，不是泄漏；只是排水期 RSS 看起来像峰值。
若已测量：NOT_RUN；大小是上界算术。
可选最小施工方案：弹出后 h.rcvQueue[0] = nil（必要时 len==0 时 rcvQueue = nil）。
影响：无所有权变化；只是让 GC 能回收已交付的 payload。
测试：消费 N 条后强制 GC，断言 payload 不再可达（finalizer 或 HeapAlloc 上界）。
收益/复杂度/风险：低-中（内存核算保真）/ 低 / 低。
判定理由：B —— 真实保留但量小且局部；一行可修，值得顺手做。
```

### 5.9 B7：H3 入站一条连接可同时驻留 232 个数据报，最后一级静默丢弃且无计数

```
Candidate ID: B7
问题与真实入口：HTTP/3 DATAGRAM 入站、慢消费者。
当前锚点：transport/http/capsule.go:322/363-364（早期队列 8 个/64 KiB）、:424（chan 64，:532-553 阻塞发送）；
  quic-go http3/state_tracking_stream.go:12（32）；quic-go datagram_queue.go:15（128）、:205-216（静默丢弃）。
瓶颈证据：慢消费者时一条连接可在三处独立分配点驻留 8+64+32+128=232 个数据报；
  最后一级溢出只有 debug 日志，没有 drop 计数，丢包隧道与空闲隧道无法区分。
  反证：阻塞发送是刻意的背压，溢出丢弃对 UDP 是正确语义；问题在深度不可观测，不在语义。
可选最小施工方案：只加一个非阻塞原子 drop 计数 + 限频日志；不要改阻塞交接（那是背压）。
测试：1000 个数据报突发下 delivered+dropped 守恒；关闭时无泄漏；-race。
收益/复杂度/风险：中（可归因性）/ 低 / 低。
判定理由：B —— 不是性能改动，是把隐式丢弃变成可测量事实。
```

### 5.10 B8：UDP NAT 每会话 64 个数据报、最多 16384 会话，溢出静默且无全局字节预算

```
Candidate ID: B8
问题与真实入口：sing-tun 用户态 UDP NAT 的 session 队列。
当前锚点：sing-tun udp_nat.go:366（64/会话）、:425-446（default: 释放，无日志无计数）、:114-123（16384 会话）。
瓶颈证据：上界算术 16384×64×~1.4 KiB ≈ 1.4 GiB 的"允许缓冲"，没有全局字节预算；
  溢出完全静默。反证：每会话上界真实存在，close 会 drain，不存在泄漏；缺口是可观测性与全局预算。
可选最小施工方案：复用 goDropCounter 做一个限频计数；如需预算，只能加在已有 NAT 生命周期内，
  不新建账本。
测试：溢出计数守恒；close 后 packetChan 为空；无新 goroutine。
收益/复杂度/风险：中 / 低 / 低。
判定理由：B —— 与第一轮"没有无界队列"并不矛盾：这是有界但不可观测 + 无全局预算。
```

### 5.11 B9：mmap 只在 iOS 生效，Android 全程 Go 堆

```
Candidate ID: B9
问题与真实入口：规则集稳态驻留的平台差异。
当前锚点：route/rule/rule_set_mmap_ios.go:1（//go:build ios）；rule_set_mmap_stub.go:1（//go:build !ios）。
  common/srs/mmap_unix.go 本身已是 //go:build unix 且用 PROT_READ/MAP_PRIVATE，只差 route/rule 的 tag。
现有实现或重复点：iOS 路径完整（写临时文件 → mmap → ReadMmap → AddCleanup Munmap）。
瓶颈证据：500k 规模 live matcher 4.23 MiB 可移到文件映射；但页错误成本在 dial 路径上未测。
  反证：若 Android 的 temp 目录/冻结策略不允许，或缺页成本高于收益，则不做。
若已测量：NOT_RUN；无 Android 设备。
可选最小施工方案：把 build tag 改为 ios || android（文件重命名 _mobile），保留 stub；验证 Munmap 时机。
影响：旧代际的虚拟地址在 GC 时才释放，更新后已有 runtime.GC() 兜底。
测试：mmap_test.go 在新 GOOS；重载后 runtime.GC 断言旧映射释放；Android census。
收益/复杂度/风险：中 / 低 / 中（临时文件生命周期、冻结状态访问、缺页成本）。
判定理由：B —— 收益方向明确，但平台证据缺失。
```

### 5.12 C / D 级发现（不再展开为施工候选）

| ID | 发现 | 证据 | 判定 |
|---|---|---|---|
| C1 | 内存 DNS 缓存改 packed bytes | 默认 1024 容量：仅省约 0.31 MiB（freelru 入口 781→464 B）；命中 `Copy` 238 ns/200 B/6 allocs vs `Unpack` 761 ns/328 B/12 allocs；整个默认缓存 ≈1.3 MiB | **C**（只有 `cache_capacity` 上万才重开） |
| C2 | 持久缓存替换 LRU，无 L1 | `client.go:614-619,1273-1279,1359-1361`；每次命中 Unpack + 后端读 | **C**（持久性是目的） |
| C3 | generation 不进 key，ResetNetwork 不清 exact cache | `router.go:2286-2333`、`:1502-1506`；`environment` 字段做命名空间隔离 | **C**（刻意设计，有测试） |
| C4 | 每次查询都分配单飞 channel | `client.go:826` `make(chan struct{})` + sync.Map 存取；约 112 B/44 ns/查询 | **C**（绝对收益太小，不值得动热路径） |
| C5 | 乐观刷新在门控前复制请求 | `client.go:836,1349` 早于 `:1545/:1564/:1596` 的去重/省电/切换检查 | **C**（同上，量级 ~24–160 B/次） |
| C6 | `CopyPacket` 每包一个 `*buf.Buffer` 头 | `sing/common/bufio/copy.go:464-493` → `network/direct.go:84-101` → `buf.NewSize`（payload 来自池）；批处理分支优先 | **C**（所有权要求；批量路径已存在） |
| C7 | `writeBatchOwned` 每批一个 `prepared` slice | `session.go:567` | **C**（每批一次，量级 ≤几百 B） |
| C8 | gate 的 headroom 双计 | `gate.go:25-35,97-103` | **C**（有界、已文档化、测试钉住） |
| C9 | 旧/新规则集共存 | 500k reload-peak 21.45 MiB vs load 21.43 MiB；旧代 4.23 MiB（20%），窗口 28 ms，周期 24 h，每次更新强制 GC | **D** |
| C10 | `initial_path` 仍整份缓冲 | `rule_set_remote.go:139-149`；500k 时约 0.9 MiB live / 5.0 MiB alloc，仅启动一次 | **C**（可顺手改成流式，但不构成施工理由） |
| C11 | source/JSON 格式的结构性开销 | `loadBytes` JSON + `NewMatcher` 字符串树；生产远端集合实际发 `.srs` | **C**（运维上"发 .srs"即可） |
| C12 | QUIC sent history / 重传是否重复持有 payload | `OnLost` 只把同一个 `*StreamFrame` 指针移入 retransmissionQueue；ACK 时 `PutBack` | **C**（没有重复 payload；50k 包数上限比 Anywhere 更强） |
| C13 | masque server 的 `OutboundQueue` 是否双缓冲 | 同一 `*buf.Buffer` 只入队一次，再移交给 quic-go；满时释放丢弃 | **C**（队列深度，不是重复字节） |
| C14 | Anywhere 的 ACK/Pacer/Recovery 是否有 quic-go 没有的东西 | ACK range 64 上限、ACK delay、PTO/loss、token bucket pacer、单一定时器；一对一 | **C**（名字不同，机制等价） |
| C15 | 全局 `FlowBufferLedger` | `route` 生产代码无 channel 队列；trafficsched 只排 `*Flow` 不排字节 | **D** |
| C16 | Swift Arena / TickScheduler / 第二复用池 | 所有权契约重复；Go timer 会真正移除；`route/reference.go` 是唯一权威 | **D** |
| C17 | 去掉 DNS 读侧 Copy | 缓存条目在交出后被 TTL/Id/Extra/Question 改写；`client_family_hint_test.go:190` 钉住 | **D** |
| C18 | Windows 上模仿 Anywhere 的批量写（gather） | wintun `AllocateSendPacket` 返回单段，无法 gather | **D**（只能加一层队列掩盖，不做） |

> 关于"DNS 没有 singleflight"的说法：本审计在 `dns/client.go:826` 直接核实到 `cacheLock.LoadOrStore(exchangeKey, make(chan struct{}))` 的单飞机制，并在 `:1545` 核实后台刷新是绕开单飞、按 key 去重的独立路径。C4 的"每查询分配"正来自这条单飞路径，因此不会采纳"没有 singleflight"的描述。

## 6. 自主发现专题

### 6.1 主题一：HTTP/3 "响应未到"的第三态——一次正确的分类修正，把成本转移到了每次新建隧道

**发现过程。** 审计开始时 `git log` 显示 HEAD 前 3 个提交都发生在审计当天 12:09–12:14，其中 `6447d0af775 fix(masque): do not remember a slow CONNECT-IP response as H3 unavailable` 和 `ccc2812e45d test(masque): pin the ambiguous-outcome ordering` 是本轮之前刚落的代码。顺着 `transport/http/tunnel_client.go` 读下去，发现 H3 尝试的失败分类从两态变成了三态。

**源码事实（VERIFIED_SOURCE）。**

- `transport/http/tunnel_client.go:97-120` 定义 `http3TunnelEstablishTimeout = 3 * time.Second`，注释说明它是"一次 CONNECT-IP setup 的窗口"，并把它与 `http3EstablishTimeout` 刻意分开。
- `:128-183` 的分类：
  - `errHTTP3ResponsePending` → `noteHTTP3AmbiguousOutcome`（只 claim 序号，**不改 verdict**）；
  - `ErrHTTP3Unavailable` → `markHTTP3Broken`；
  - `!probeExpired` → 直接把应用错误返回，不记 verdict；
  - 窗口过期且是 `DeadlineExceeded/Canceled` → `markHTTP3Broken`；
  - 其它窗口过期 → 仍按 ambiguous 处理。
- `transport/http/http3_response_pending.go:15-31` 是新的错误类型：`Unwrap() []error` 返回 `{errHTTP3ResponsePending, deadline}`，保留 deadline 链。
- `transport/http/client.go:339-449` 是 verdict 状态机：`claimHTTP3Outcome` 用 CAS 保证"最后开始的那次尝试"才有资格改状态；`markHTTP3Broken` 的退避是 5 s 起、5 min 封顶（`:656-657`），并且一次事件只记一步（CAS 抢窗口）。
- 生产路径：`client_h3_request.go:324-372` 在 `OpenRequestStream` 失败、CONNECT 响应失败、setup 成功但调用方已放弃这三个点都返回 pending 错误；`client_h3.go:294-300` 保留该错误穿透 `ctx.Err()` 的包装。

**成本转移。** 修正是对的：把"服务器只是慢"误记成"H3 不可用"会让后续所有隧道在退避窗口内跳过一条可用的路径，这是可用性 bug。但代价是：**verdict 不被置位，下一次 `openTunnel` 仍会先试 H3，并再付一次最多 3 s 的窗口**。在一个 CONNECT 响应稳定慢于 3 s 的服务器上（高 RTT、过载、或中间盒限速），每一次新建隧道都会先等 3 s 再退回 H2。这个成本没有在提交里被测量，也没有计数器能回答"每会话发生几次 ambiguous"。

**为什么这不是"应该回退成老行为"。** 老的 `markHTTP3Broken` 会记住"不可用"，代价是错误地把可用路径封死 5 s–5 min；两者都不是免费的。真正缺的是**频率数据**：如果 ambiguous 只在冷启动/网络切换后偶发，3 s × 偶发 = 可接受；如果每次重连都发生，就需要一个"连续 ambiguous N 次后临时降低窗口"的有界策略（不是恢复 broken verdict）。这属于 B（继续实验），不是 A。

**可重复测量方案（本轮 NOT_RUN）。** 用现有 `h3_churn_stand_test.go` 的 stand 把 CONNECT 响应延迟设为 `window ± δ`，统计每个"重连周期"的 H3 尝试次数与 setup 延迟分布；或在 `noteHTTP3AmbiguousOutcome` 加一个仅测试可读的原子计数，跑 100 次重连。验收线：ambiguous 比例 <5% 则维持现状；>20% 再讨论窗口自适应。

**判定：B。** 证据是源码级的，收益/风险是双向的，但缺少频率。

### 6.2 主题二：完整性校验的代价——第一轮的 drain 把"提前返回"变成了"解压到 EOF"

**发现过程。** 复核 BUG-02-2 时读到 `common/srs/binary.go:92-104`：为了验证 Adler-32，`Read` 在读完规则后 `io.Copy(io.Discard, bReader)` 把整个解压流读到 EOF。第一轮报告已经预见了"畸形输入 CPU 成本变化"，但没有量。本轮用外部探针构造"合法规则 + 巨大可压缩尾巴"测了它。

**实测（MEASURED_LOCAL）。** 63.7 KiB 文件 → 64 MiB 解压，46.68 ms；254.8 KiB → 256 MiB，143.63 ms；两者分配都只有 0.05 MiB（`io.Discard` 固定缓冲）。也就是说：**内存被 `io.Copy` 保护住了，CPU 没有上限**。按 ~1.7 GB/s 的解压速度，一个 5 MiB 的恶意/损坏 `.srs` 可以让一次更新占满单核约 3 s；如果同时有多个规则集在更新，或者更新发生在移动网络唤醒窗口内，这个成本会叠加。合法数据方向则是另一回事：65536×128 B 的关键字规则（5.0 MiB 文件）解压后约 8.4 MiB，但分配 54.35 MiB（约 6.5×），来自 `readRuleItemString` 的 append 增长。

**结论。** 完整性校验不能退；需要的是**输出字节预算**（B3）。这是"正确性修复带来成本转移"的典型样本：第一轮把安全性提高了，第二轮要给它加一个工作量上界。

**判定：B（B3）。**

### 6.3 负结果：`route/` 没有 FIFO 保留、没有无界队列

按提示词 §8.2/§8.3 专门找了"逻辑出队 ≠ 对象解除引用"和"小 slice 持有大数组"：

- `route/` 的生产代码没有 `make(chan ...)` 队列（grep 只命中 `_test.go`）；`trafficsched` 的 lane 队列只排 `*Flow`，不排 payload。
- `route/reference.go:267-302` 的 `queue = queue[1:]` 是配置加载期的局部 BFS worklist，函数返回即死。
- 真正的两处 reslice 保留都在依赖层：`quic-go/datagram_queue.go:223-225` 与 `http3/state_tracking_stream.go:191-193`（B6），以及 `udp_nat` 的静默丢弃（B8）。
- 小 slice 持有大数组的一个确凿例子是 QUIC 的 1452 B 帧缓冲（B4）；`common/sniff/quic.go:184-209,307,362-365` 的 CRYPTO 分片会因引用关系保留整个 `newPacket`，但那是 `KNOWLEDGE/modules/buffer-ownership.md` 已登记的借用契约（HYPOTHESIS 量级，未测），本轮不判缺陷。

这条负结果本身有价值：它支持第一轮"不要造全局账本"的判断，并把"有界但不可观测"（B7/B8）与"真实保留"（B6）区分开。

## 7. 可重复实验与测量日志

### 7.1 环境

| 项 | 值 |
|---|---|
| OS | Windows 10 19044 LTSC（`go test` 报 `goos: windows`） |
| CPU / RAM | AMD Ryzen 7 4800H（8C/16T）/ 15.4 GiB |
| Go | `go1.25.5 windows/amd64`（`C:\Deepseek\_gotool\go\bin\go.exe`） |
| CGO | `CGO_ENABLED=0` |
| 模块缓存 | `C:\Users\Jie\go\pkg\mod`；`GOFLAGS=-mod=mod`（环境预置） |
| 目标 SHA | `ccc2812e45d8f5fbbca3bc7af14b91bc0ba92a36`（`testing`） |
| 外部探针 | `%TEMP%\anywhere2_dnsprobe`、`%TEMP%\anywhere2_srsprobe`（均在仓库外，`GOPROXY=file:///.../cache/download`，无外网） |

### 7.2 已运行的测量

| # | 命令 | 重复 | 原始结果 |
|---|---|---|---|
| M1 | `go test ./dns -run '^$' -bench 'BenchmarkDNSExactCacheHit\|BenchmarkDNSNXDomainNameCacheHit\|BenchmarkDNSCacheMiss' -benchmem -count 3 -timeout 300s` | 3 | ExactCacheHit 3243/3086/3188 ns，1616 B，31 allocs；NXDomainNameCacheHit 3975/4412/3596 ns，1784 B，33；CacheMiss 2907/2687/2667 ns，1290 B，28 |
| M2 | 外部探针 `go test -run TestSizes -v` + `-bench . -benchmem -count 3` | 3 | 报文：A 95 B、CNAME 118 B、HTTPS/SVCB 506 B、DNSSEC 723 B；`Copy`：2A 249/266/243 ns，200 B，6；HTTPS 1690/1667/1910 ns，1632 B，41；DNSSEC 431/424/492 ns，424 B，8；`Unpack`：2A 807/764/883 ns，328 B，12；HTTPS 4339/4152/4025 ns，2072 B，71；DNSSEC 2561/2683/3096 ns，2120 B，25 |
| M2b | 外部探针 `TestResident` | 1 | 1024 条 A：`*dns.Msg` 362,800 B（354 B/条）vs packed 135,704 B（133 B/条），2.67×；10000 条：3,481,944 vs 1,205,880（348 vs 121），2.89×；HTTPS/SVCB 1024：1,593,624 vs 556,744（1556 vs 544），2.86×；10000：15,402,200 vs 5,365,784 |
| M2c | 外部探针 `BenchmarkUnpackQueryA/EDNS0` | 2 | A 查询 187.7/181.7 ns，40 B，2 allocs；EDNS0 AAAA 358.0/358.0 ns，152 B，5 allocs |
| M3 | `go test ./route/rule -run 'TestRuleSetScaleCensus$\|TestRuleSetLoadStageBreakdown$' -v -count=1` | 1 | 1k domain：srs 3.6 KiB，live 32.7 KiB，load 125.3 KiB，reload 124.2 KiB；10k：23.2 KiB / 119.0 KiB / 537.0 KiB / 536.0 KiB；1k CIDR：5.3 KiB / 13.0 KiB / 127.3 KiB；10k CIDR：49.7 KiB / 123.6 KiB / 801.3 KiB；stages@10k：compressed-read 25.1 KiB、inflate 52.8 KiB、decode 518.1 KiB、rules 1.6 KiB、steady 23.9 KiB；hit/miss：480/363 ns（1k）、446/385 ns（10k） |
| M4 | `SING_BOX_SCALE_CENSUS=1 go test ./route/rule -run TestRuleSetScaleCensusLarge -v -count=1 -timeout 2400s` | 1 | 100k domain：srs 218.1 KiB，live 882.4 KiB，load 4.1 MiB，reload 4.1 MiB，gc 2；1M domain：srs 1.6 MiB，live 8.3 MiB，load 42.5 MiB，reload 42.5 MiB，gc 10，pause 691.1 µs；100k CIDR：srs 501.6 KiB，live 1.0 MiB，load 7.8 MiB，gc 1；总耗时 6.18 s |
| M5 | `go test ./common/srs -count=1` | 1 | PASS，0.030 s（含 truncated/malformed 全套） |
| M6 | `go test ./route/rule -run 'TestRemoteRuleSetStreamsBinaryWithoutCache\|TestRemoteRuleSetStreamDoesNotPublishATruncatedBody' -v -count=1` | 1 | PASS；日志 `binary load with a 4194304-byte tail allocated 120584 bytes` |
| M7 | `go test ./common/trafficsched -run '^$' -bench . -benchmem -count 1` | 1 | StreamWriteTax raw 2.055 ns / gate-nil 7.203 ns / gate-uncontended 14.61 ns（均 0 B/0 allocs）；StreamWriteBufferTax raw 74.68 → gate 87.40 ns（64 B/1 alloc）；CopyLoopTax raw 9552 → gate 15125 ns（32,785 → 33,377 B，3 → 9 allocs）；PacketGateTax single 75.82 → 83.46 ns（113 → 104 B，1 alloc），batch-64 5660 → 5702 ns（10,777 → 10,678 B，66 allocs）；paced-generous/65536 为 1,377,615 ns（含 shaper 自身等待，不是 gate 开销） |
| M8 | 外部探针 `go run .`（srsprobe） | 1 | 基线 1 规则 0.05 MiB；+64 MiB 零尾：文件 63.7 KiB，46.68 ms，0.05 MiB；+256 MiB 零尾：254.8 KiB，143.63 ms，0.05 MiB；65536×128 B 关键字：文件 5028.6 KiB，103.11 ms，54.35 MiB；262144×128 B：20113.8 KiB，407.79 ms，217.65 MiB |
| M9 | 并行探针（同一会话、同一仪器，未由报告作者重复） | 1 | 100k/300k/500k：decode alloc 4.06/13.32/21.43 MiB，live 0.873/2.68/4.23 MiB，decode:live 4.65/4.97/5.07×；500k GC-on resident 8.20 MiB、28 ms、6 GC、520 µs pause；500k buffered 26.43 MiB vs streamed 21.44 MiB |

### 7.3 未运行（必须如实列出）

| 项 | 原因 | 它能证明什么 |
|---|---|---|
| `go test -race ./route/...` / `./dns/...` | 本机 `CGO_ENABLED=0`，无 mingw-w64 gcc | A0 的跨 goroutine 读是否被 race detector 捕获；`NOT_RUN` |
| Apple 规范 tag 构建 | Windows 链接器拒绝 `tfo-go` 的 `tfogo_checklinkname0`/`badlinkname` | iOS 真实构建契约；`BLOCKED_BY_ENVIRONMENT` |
| iOS/Android 真机 `phys_footprint` / 能耗 | 无设备/SDK | 本报告的一切 MiB 都是进程内堆指标，不是设备驻留；`NOT_RUN` |
| QUIC 0/1/3/5% 丢包、乱序、PMTU 下降 | 无网络仿真环境 | B4 的真实内存高水位；`NOT_RUN` |
| Android mmap 路径 | 无 Android 设备/模拟器 | B9 的收益与缺页成本；`NOT_RUN` |
| 持久 DNS 缓存 I/O（bbolt） | 需要 `store_dns` 配置与磁盘 | C2 的命中延迟；`NOT_RUN` |
| 被劫持 DNS 的真实 TUN 负载复现 | 需要真实 TUN 流量；本机未跑 | A0 的现场触发率；源码已证明生命周期，`NOT_RUN` |
| 300k/500k 由本报告作者复跑 | 复用并行探针（M9） | 已标注来源；不是独立复测 |

---

## 8. A/B/C/D 候选矩阵

### 8.1 判定矩阵

| ID | 结论 | 最高证据 | 实际收益可能性 | 证据强度 | 维护成本 | 跨协议影响 | 回归危险 | 现在能测 | 依赖版本风险 | 改动面（仅建议） |
|---|---|---|---|---|---|---|---|---|---|---|
| **A0** | **A** | `VERIFIED_SOURCE` + `MEASURED_LOCAL` | 高（正确性 + 每查询一次解析） | 高 | 低 | 无（DNS 入站路径） | 低 | 能（单测可构造） | 无 | `route/dns.go` 5 行 |
| **A1** | **A** | `MEASURED_LOCAL` + `VERIFIED_SOURCE` | 高（1M 峰值去 ~80% 瞬态） | 高 | 中 | 所有 binary 规则集 | 中（有界预分配必须保留） | 能（census 已有） | 中（需 `sing` fork 发版） | `sing/common/varbin/slices.go`、`common/srs/binary.go` |
| B1 | B | `VERIFIED_SOURCE` | 中（每包 −1 复制） | 中 | 中 | MASQUE/CONNECT-IP | 中 | 需接口 | 低 | `sing-tun` + `protocol/masque` + `transport/masque` |
| B2 | B | `VERIFIED_SOURCE` | 中（>32 突发恢复批量） | 中 | 低 | MASQUE | 中 | 能（计数器） | 低 | `transport/masque/session.go` |
| B3 | B | `MEASURED_LOCAL` | 中（安全/健壮性） | 高 | 低 | 所有规则集输入 | 低 | 能 | 低 | `common/srs/binary.go` |
| B4 | B | `VERIFIED_SOURCE` + 算术 | 中（异常对端内存上界） | 中 | 低 | 所有 QUIC 接收 | 低 | 需仿真 | **高**（quic-go fork） | `quic-go` frame sorter |
| B5 | B | `VERIFIED_SOURCE` | 中（每数据报 −1 复制） | 中 | 中 | hysteria2/TUIC | 中（双重释放） | 能（单测） | 中（`sing-quic` fork） | `sing-quic` + quic-go owned API |
| B6 | B | `VERIFIED_SOURCE` | 低-中（≤180 KiB/连接） | 中 | 低 | 所有 QUIC DATAGRAM | 低 | 能（GC 断言） | 中（quic-go fork） | `quic-go` 两行 |
| B7 | B | `VERIFIED_SOURCE` | 中（可归因性） | 中 | 低 | HTTP/3 DATAGRAM | 低 | 能 | 低 | `transport/http/capsule.go` 计数 |
| B8 | B | `VERIFIED_SOURCE` | 中（可归因性/全局预算） | 中 | 低 | 所有 UDP NAT | 低 | 能 | 中（`sing-tun` fork） | `sing-tun/udp_nat.go` 计数 |
| B9 | B | `VERIFIED_SOURCE` | 中（Android 稳态驻留） | 中 | 低 | 规则集/Android | 中 | 需设备 | 低 | `route/rule` build tag |
| C1–C14 | C | 见 §5.12 | 低/已否决 | 高 | — | — | — | — | — | 不动 |
| D1–D4 | D | 见 §5.12 | 负/不适用 | 高 | — | — | — | — | — | 不做 |

### 8.2 优先级排序（由分项推出，不靠直觉）

1. **A0**：唯一一个"正确性 + 性能"双证据的项，且修复边界最小。
2. **A1**：唯一一个"实测收益足够 + 最小改法边界清晰"的性能项；虽然落在依赖 fork。
3. **B2 / B7 / B8**：先加计数（fallback 次数、丢包次数、溢出次数），用数据决定要不要动。
4. **B1 / B3 / B5**：有明确代码证据，但需要接口/产品决策/协议回归。
5. **B4 / B9**：需要弱网仿真或 Android 设备，当前环境无法闭环。
6. **B6**：一行、低风险，可作为任何一次触碰 quic-go fork 时的顺手项。
7. **C/D**：不投入。

### 8.3 若未来施工，安全回滚与验收策略

| ID | 回滚 | 验收 |
|---|---|---|
| A0 | 单 commit revert；行为回退到"双解析"，但**不要**回退到"跨 goroutine 借用" | 新增 (a)(b) 测试；`-race`；`TestDNSPacketHijack*` 全绿；真实 TUN 抓包 0 解析错误 |
| A1 | 依赖版本回退到当前 pseudo-version | census：100k load ≤2.5 MiB、1M load ≤15 MiB；malformed `huge_*` 仍廉价失败；`route/rule` 全绿 |
| B2 | 单 commit revert | 33/64 包走批量分块；块 2 拒绝不双重释放；顺序断言 |
| B3 | 单 commit revert（保留错误分支） | 500 KiB 炸弹在有界时间失败；旧代际保持；合法 100k 规则集不受影响 |
| B4 | quic-go fork 版本回退 | 乱序小帧的 AllocsPerRun 上界；DoneCb 一次；流控违规 |
| B5 | `sing-quic` 版本回退 | owned 契约测试；错误回滚；重连泄漏 |
| B7/B8 | 纯计数，revert 无行为变化 | 计数守恒；日志限频；无新 goroutine |

---

## 9. 跨组件风险与攻击性反证计划

本节列出"如果未来施工，必须增加的测试"以及本轮已经识别的隐藏失败成本。它们不是施工项，而是施工前置条件。

### 9.1 缓存与代际

| 风险 | 现状 | 反证/测试计划 |
|---|---|---|
| DNS 旧网络结果污染新网络 | key 含 `environment`（`client.go:584-604`）；generation 是 store/deliver 守卫（`:304-316,402-415`）；`ResetNetwork` 不清 exact cache（`router.go:2286-2333`） | 网络切换后同一 name 的 A 记录必须按 `environment` 分开；TTL 到期前不得跨环境命中；`cache_generation_guard_test.go` 已覆盖 store 侧 |
| 乐观刷新覆盖更新的结果 | 刷新绕过单飞（`:1606`），store 是最后写入者赢（`:1285/1287`） | 并发"普通交换 + 刷新"各写一次，断言最终条目来自更新的上游答案（需要 per-entry 序号；当前没有） |
| 规则集部分发布 | `publishRules` 单点赋值（`:281-285`），失败分支全部在赋值前返回 | 已有 `TestRemoteRuleSetStreamDoesNotPublishATruncatedBody`；新增"cancel 发生在 drain 之后、publish 之前"的用例（当前会发布，属语义边界而非损坏） |
| H3 verdict 乱序 | `claimHTTP3Outcome` CAS + `http3VerdictMu`（`client.go:339-449`）；`resetHTTP3Verdict` 同一临界区（`:379-385`，注释记录了 1758/200000 的实测复现率） | 并发 dial 与 `ResetConnections` 交错 20 万次，断言旧尝试不会重新启用 verdict |

### 9.2 资源释放与批量失败

| 风险 | 现状 | 测试计划 |
|---|---|---|
| 批量部分失败双重释放 | 每层都是全有或全无：`session.go:590-592`、`http3/conn.go:401-404`、`datagram_queue.go:128-136`、`sing/common/bufio/packet_batch_mmsg.go:225` 的 `defer ReleaseMulti` | 构造"第 k 个失败"的假 transport，断言每个 buffer 恰好释放一次（finalizer 计数） |
| 拒绝后仍持有前缀 | `writeBatchOwned` 在拒绝时 `Advance(1)` 回退 context ID（`:585-593`） | 拒绝后逐字节比较输入与回退结果 |
| 关闭时队列 drain | `datagram_queue.go:255-301`；`capsule.go` close 路径 | 关闭时排队数据报的 payload 全部释放；无 goroutine 泄漏 |
| mmap 生命周期 | iOS `runtime.AddCleanup`；`rule_set_updater.go:88-90` 每次更新后强制 GC | 重载后 GC，断言旧映射已 Munmap；冻结/后台状态下访问不崩溃 |

### 9.3 取消、EOF、半关闭

| 风险 | 现状 | 测试计划 |
|---|---|---|
| 规则集 body 截断 | drain + `srs.Read` 双层校验 | 已有；新增 HTTP/2 + chunked 截断组合 |
| 取消发生在 publish 前 | drain 之后、publish 之前没有 `ctx.Err()` 检查 | 明确语义：要么发布、要么不发布，不能部分发布；测试固定其中一种 |
| DNS 取消 | `ctx.Err()` 在 `beginExchange` 与 `finishExchange` 检查 | 取消后不得写缓存；`pending_transition_ownership_test.go` 已有部分覆盖 |
| 半关闭/重复取消 | `route/conn.go` 的 `common.Close` 双向关闭；`Scheduler.ReleaseAll` | 重复 Close 幂等；半关闭后另一方向仍可读 |

### 9.4 MTU / PTB

- quic-go 的 `send_queue.go:90-101` 对 `EMSGSIZE`（以及 `ENOBUFS`、`DatagramTooLargeError`）**只释放 buffer 并吞掉错误**；该分支之后的 `mtuFinder` 只在探测 ACK 时增长（`mtu_discoverer.go:198-233`），没有看到基于这个错误的回退路径。在 fork 中 grep 不到 `IP_RECVERR`/`MSG_ERRQUEUE` 的 PTB 读取路径（只有 `IP_MTU_DISCOVER`，`sys_conn_df_linux.go:13-19`），因此过大的包会留在 sent history 里，之后按 PTO 以同样大小重传。这是源码级观察（`VERIFIED_SOURCE`），真实链路下的后果需要弱网仿真才能确认。
- 该 fork 增加了 `Config.InitialPacketSizeCeiling`（`config.go:171-199`），hysteria2 已使用（`protocol/hysteria2/outbound.go:216-225`）——这是目前对"PMTUD 越过真实链路"的唯一硬约束。
- MASQUE 的 PTB 由应用层 `classifyDatagramError` 处理（`session.go:504-517`），与 QUIC 层是两条独立路径。
- **施工前置测试**：MTU 从 1500 降到 1280 时，CONNECT-IP 与 hysteria2 都必须在不重建连接的情况下恢复；注入 ICMP PTB 的仿真环境目前没有（`NOT_RUN`）。

### 9.5 隐藏失败成本（本轮新注意到的）

1. **`ExtendedWriterWrapper.WriteBuffer` 丢弃写入字节数**（`sing/common/bufio/conn.go:158-161`）：`defer buffer.Release(); return common.Error(w.Write(buffer.Bytes()))`。若底层 `Write` 返回 `(n<len, nil)`，这里静默当成成功。按 `io.Writer` 契约，短写必须返回非 nil 错误，所以这更像"防御缺口"而不是已证实的 bug；但 `conn_reclaim.go` 与 `route/conn.go` 的"写成功即推进"假设依赖它。**判定 C（记录，不修）**，除非未来出现非契约 writer。
2. **`srs.Read` 从未 `Close` zlib reader**（`common/srs/binary.go:73`）：每次加载重新分配 32 KiB flate 窗口，直到 GC。B3/A1 的顺手项。
3. **规则集更新后强制 `runtime.GC()`**（`rule_set_updater.go:88-90`）：为了释放 iOS 映射，代价是一次全堆 GC；每 24 h 一次，可接受，但要在文档里写明它是有意为之。
4. **`fetch` 每次非首次刷新后 `CloseIdleConnections()`**（`rule_set_remote.go:312-314`）：更新不能复用 keep-alive，每个规则集每次更新一次新握手。量级小，但"多个规则集同一时刻更新"会形成握手小高峰。
5. **`updateOnce` 在 `refs.Load()==0` 时清空 rules**（`rule_set_remote.go:292-301`）：这不是失败路径，但如果未来出现"先 DecRef 再重新引用"的用法，规则集会短暂为空。当前调用方没有这种模式，记录为语义边界。

### 9.6 弱网与内存高水位（未运行，施工前必须补）

- 0/1/3/5% 丢包、1% 重复、乱序、RTT 抖动、PMTU 从 1500 降到 1280：测 QUIC 接收 `frameSorter` 的实际高水位（B4 的算术需要真实数字）。
- 对端缓慢读取 + 应用层批量发送：测 `datagram_queue` 32 槽阻塞时的端到端尾延迟（B2 的收益需要用尾延迟而不是吞吐证明）。
- 后台/休眠/唤醒：测 `power.Governor` Trim 是否会与规则集更新、DNS 刷新、H3 verdict 重置交叠。

---

## 10. 不应该做什么

1. **不要引入第二套生命周期/资源管理器/缓存。** `adapter.Scope`、`runtimecoord`、`power.Governor`、`route/reference.go`、`dns.Client` 已经是各自维度的唯一权威。
2. **不要为了"像 Anywhere"而造全局字节账本。** 本仓库没有受它支配的无界队列；账本会变成没人执行的数字。
3. **不要移植 Swift Arena / SegmentArena / PlaintextArena。** `buf.Buffer` 的 acquire/release 契约已经覆盖短生命周期；第二套所有权只会带来双重释放。
4. **不要重写 Timer/TickScheduler。** Go runtime 的 timer wheel 会真正移除取消的 timer；`trafficsched` 空闲时已经不持有 timer。
5. **不要为了 0.3 MiB 改 DNS 缓存表示。** 默认容量下 packed 只省约 0.31 MiB，命中 CPU 却变成 2.2–3.5×。
6. **不要去掉 DNS 读侧 `Copy`。** TTL/Id/Extra/Question 都会在交出后被改写；这是所有权边界，不是冗余。
7. **不要用 `TotalAlloc` 冒充峰值 RSS，也不要用 flow-control window 冒充预分配。** 本报告所有数字都标了是 alloc、live 还是 sampled peak。
8. **不要为 Windows 的 wintun 造用户态 gather 队列。** API 不支持 gather，队列只会把复制换成延迟和内存。
9. **不要为 hysteria2/TUIC 另造一套 owned API。** quic-go fork 已经有 `SendDatagramOwned`，接上即可。
10. **不要合并"新/旧 matcher 共存"的优化。** 20% × 28 ms × 每 24 h，收益小于风险。
11. **不要在没有计数的情况下改 UDP NAT / H3 队列深度。** 先让丢弃可见（B7/B8），再谈预算。
12. **不要在 Apple tag 未验证的情况下宣称 iOS 行为。** 本机 `BLOCKED_BY_ENVIRONMENT`；CI 记录属 `REPORTED_PREVIOUSLY`。

---

## 11. 剩余不可验证项与决策建议

### 11.1 仍然不可验证（按重要性）

| 项 | 为什么 | 需要什么才能验证 |
|---|---|---|
| iOS/Android 真机驻留与能耗 | 无设备/SDK | 真机 `phys_footprint`、jetsam 日志、能耗仪 |
| A0 的现场触发率 | 需要真实 TUN 流量 + `-race` | 带 cgo 的主机跑 `-race`，或设备上抓 DNS 失败率 |
| B2 的触发频率 | 需要 fallback 计数 | 一行原子计数 + 现网/压测 |
| B4 的弱网高水位 | 无网络仿真 | `tc netem` / 自建 lossy relay |
| B9 的 Android 缺页成本 | 无设备 | Android 真机 + 规则集命中压测 |
| 持久 DNS 缓存 I/O | 无 `store_dns` 配置 | 临时配置 + bbolt 基准 |
| 解压预算的合理上限 | 产品决策 | 真实订阅规模分布 |

### 11.2 决策建议（不替用户改代码）

- **如果下一轮只做一件事：做 A0。** 它是正确性缺陷，修复 5 行，测试可构造，风险最低；不做它，任何"DNS 快/稳"的结论都建立在一个会读已回收缓冲区的路径上。
- **如果下一轮做两件事：A0 + A1。** A1 需要一次 `sing` fork 发版，收益在 100k 以上规模才显著；如果用户当前的主力配置是 10k 级规则集，A1 的收益只有约 0.3 MiB，可降为 B。
- **B2/B7/B8 先做"计数"，不要先做"修"。** 这三项的正确决策依赖频率数据，而频率数据只需要一行计数。
- **B1/B3/B5 属于"有证据但需要产品/协议决策"**：B1 需要跨包接口，B3 需要定上限，B5 需要协议级回归。三者都不应在没有 A0/A1 结论的情况下先动。
- **B4/B9 暂不投入**：当前环境无法闭环，投入会变成"只改代码不验证"。
- **C/D 全部不做。** 特别是 C1（packed DNS）、C15（全局账本）、C16（Arena/Timer）、C17（去 Copy）——这四项是本轮用数据或源码明确否决的回头路。

---

## 12. 审计自查

| 检查项 | 结果 |
|---|---|
| 是否修改任何生产源码/测试/配置/CI/文档（除本报告） | **否**。`go.sum` 的 `-mod=mod` 追加已恢复；`git status --porcelain` 在写报告前为空 |
| 是否创建分支 | **否** |
| 是否 `git commit` | **否** |
| 是否 `git push` / force / reset / rebase / clean / stash / tag | **否** |
| 是否覆盖任何已有文件 | **否**。`docs/fork/anywhere-second-round-research.md` 写前不存在；同名冲突未发生 |
| 报告之外的实验产物 | 全部在 `%TEMP%\anywhere2_dnsprobe`、`%TEMP%\anywhere2_srsprobe`（仓库外），未加入 Git |
| 审计期间仓库 HEAD 变化 | 由外部 docs-only 提交 `896915952ab`、`45078a78b1d` 引起；`git diff ccc2812e..HEAD -- '*.go'` 为空；本审计未产生提交 |
| 起点工作树既有修改 | `docs/fork/mieru-masque-final-acceptance.md`（非本审计产生，未触碰；后由外部提交） |
| 本报告精确路径 | `C:\Deepseek\integration-release-next\docs\fork\anywhere-second-round-research.md` |
| 报告状态 | 本地新增、未 `git add`、未提交、未推送 |

**审计结束。** 本报告只提供研究结论、源码锚点、实测数据与施工建议；是否进入第三轮施工由用户决定。

