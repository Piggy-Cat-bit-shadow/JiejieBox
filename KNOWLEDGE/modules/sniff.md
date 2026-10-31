# Sniff（嗅探）

## v0.1.9 之后的 FakeIP 与分片 QUIC（FakeIP and fragmented QUIC after v0.1.9）

**VERIFIED_SOURCE，审阅版本 `011f3fdcd99`。** 如果 FakeIP 反向映射已经提供域名，
`route/route.go` 的 `packetSniffUsesKnownFakeIPDomain` 会在满足条件时停止等待剩余的分片
QUIC ClientHello。这不意味着所有分片数据包都可以跳过嗅探。具体条件与测试见
`route/fragmented_quic_known_domain_test.go`；源码检查也不能证明与所有 QUIC 客户端互通。

TLS 与 QUIC 的 SNI 也先经过 `ParseSocksaddrHostPort(...).Fqdn`，之后才写入
`metadata.Domain`（`common/sniff/tls.go`、`common/sniff/quic.go`）。
这样 IP 字面量 SNI 不会被当作域名参与规则匹配；见 `common/sniff/sni_domain_normalization_test.go`。
本次刷新中，未加过滤的 `common/sniff` 包测试在本地通过。

**范围（Scope）：** `common/sniff/*`（13 个非测试文件）及其消费者 `route/route.go actionSniff`。

**来源（Provenance）：** fork 修改了 `sniff.go`、`quic.go`、`http.go`、`tls.go`，并新增 `wireguard.go`。
`dns.go`、`bittorrent.go`、`dtls.go`、`ntp.go`、`rdp.go`、`ssh.go`、`stun.go` 与 `internal/qtls/qtls.go`
在 `e4b7e692` 与 `a267e99` 上**逐字节相同**；无论 fork 的提交列表暗示了什么，
这些内容都属于上游。共有 25 个 fork 提交触及 `common/sniff`，
**新增 22 个测试文件**。

**在改动以下内容时加载本模块（Load this module when changing）：** 任何嗅探器、
peek/read 行为、嗅探超时、分片 QUIC，或 sniff→routing 的元数据契约。

---

## 1. 清单（Inventory）

| 种类 | 嗅探器 | 产出 | 锚点 |
|---|---|---|---|
| 流 | `TLSClientHello` | `Protocol=TLS`，`Domain` 来自 SNI，基于 JA3 的 `Client` | `tls.go:30-99` |
| 流 | `HTTPHost` | `Protocol=HTTP`，`Domain=Host` | `http.go:28-241` |
| 流 | `StreamDomainNameQuery` | `Protocol=DNS` | `dns.go` |
| 流 | `BitTorrent` | `Protocol=BitTorrent` | `bittorrent.go` |
| 流 | `SSH` | `Protocol=SSH`，`Client` = banner[8:] | `ssh.go` |
| 流 | `RDP` | `Protocol=RDP` | `rdp.go` |
| 包 | `DomainNameQuery` | `Protocol=DNS` | `dns.go:41-74` |
| 包 | `QUICClientHello` | `Protocol=QUIC`，`Domain`，`Client`，`SniffContext` | `quic.go:56-476` |
| 包 | `STUNMessage` | — | `stun.go` |
| 包 | `WireGuard` | — | `wireguard.go`（fork 新增） |
| 包 | `UTP` | `Protocol=BitTorrent` | `bittorrent.go:56-94` |
| 包 | `UDPTracker` | `Protocol=BitTorrent` | `bittorrent.go:95-96` |
| 包 | `DTLSRecord` | — | `dtls.go` |
| 包 | `NTP` | — | `ntp.go` |

嗅探计划按顺序调用各解析器，一旦某个解析器识别出协议，本轮就会停止。因此，不同协议的
识别条件重叠时，调整顺序可能改变结果。一个已记录的例子是 WireGuard 必须排在 UTP 之前：
当时较宽松的 UTP 检查可能误认 WireGuard 握手（`sniff.go`，提交 `912778dd6`）。这不代表
其他解析器之间不会发生重叠。

`sniff.Skip(metadata)` 会跳过 SMTP 25/465/587、IMAP 143/993 和 POP3 110/995 端口的
嗅探（`sniff.go:23-37`）。

### 嗅探器可以写入什么（What sniffers may write）

恰好四个元数据字段：`Client`、`Domain`、`Protocol`、`SniffContext`。

---

## 2. 所有权 —— “零拷贝改进”会破坏的规则（Ownership — the rules that a "zero-copy improvement" breaks）

| # | 规则 | 锚点 | 违反后会破坏什么 |
|---|---|---|---|
| S1 | `payloadReader` 借用 segments，**不能在创建它的 `PeekStream` 调用结束后继续使用** | `sniff.go:79-91` | 保留该游标的嗅探器会读到**被回收的池化缓冲区** |
| S2 | `segments` 中的缓存数据在本次调用开始时捕获一次；每次 `ReadOnceFrom` 后更新最新缓冲区的视图 | `sniff.go:132-137`、`:162` | 按“每次读取一个 segment”的错误假设会读到陈旧的 segment |
| S3 | 每轮之间会**重置游标位置（rewind）**，而不是重新创建游标 | `sniff.go:186` | 每轮一次分配，且嗅探器之间产生分歧 |
| S4 | 每次读取前设置截止时间，读取后清除 | `sniff.go:146-151` | 未清除的截止时间会影响该连接的后续读取 |
| S5 | 缓冲区来自**调用方**（`buf.NewPacket()`，16 KiB），写满时 `ReadOnceFrom` 返回 `io.ErrShortBuffer` | `route/route.go:1401`（stream）/ `:1525`（packet） | 16 KiB 上限就是 PeekStream 的字节上限 —— 不要随意“提高”它 |
| S6 | `takePacketBytes` 返回**调用方数据报内部的视图**，仅在数据报存活期间有效 | `quic.go:36-54` | 保留的 DCID / PN 样本会读到被回收的内存 |
| S7 | QUIC 的工作副本 `newPacket` 是一次**全新的、按包大小的 `make`，有意不做池化**；解密后的缓冲区与 `newPacket` 重叠引用 | `quic.go:184`、`:227` | 将工作副本放入池可能使保留的分片超过其所有者的生命周期；见 S8 |
| S8 | 保留的 `qCryptoFragment.payload` **与 `newPacket` 重叠引用，绝不引用调用方的数据报**；重组缓冲区由 `defer` 释放 | `quic.go:306-307`、`:342-343`、`:477-481` | 保留的分片会在数据报 `Release` 之后仍然存活 |
| S9 | 头部保护在包内**原地写入 4 个字节**，因此无法容纳这些字节的 `Length` 会被拒绝 | `quic.go:125-131`（返回 `os.ErrInvalid`；`:186-189`） | 越界写入数据报尾部 |
| S10 | `PeekPacket` 的成功路径零分配（两个局部错误槽位，仅在失败时才分配一个切片） | `sniff.go:205-236` | 数据包热路径上每个数据报一次分配 |

S6–S8 有以下测试依据：`quic_ownership_test.go:314-350` 检查地址范围；同文件的 `:206-281` 和 `quic_boundary_test.go:427-456` 在修改调用方数据报后确认 `newPacket` 不受影响；`sniff_bounded_copy_internal_test.go:120-156` 则检查保留的 `cap` 不会随合并尾部（coalesced tail）增长。

---

## 3. 两个循环（The two loops）

### 流（Stream，`sniff.go:125-202`）

* 每轮一个 `sniffBuffer`，由**调用方**分配；
* 只要**任一**嗅探器返回 `ErrNeedMoreData` 就继续循环（`:182-199`）；
* 同时受每次读取的截止时间（默认约 300 ms，可由 `action.Timeout` 覆盖）**以及** 16 KiB 缓冲区上的
  `io.ErrShortBuffer` 约束；
* 聚合结果只在**终止**的那一轮构建（`:154-163`、`:191`）。

### 数据包与分片 QUIC（Packet, and fragmented QUIC，`route/route.go:1372-1650`）

1. 第一轮重放已经捕获的 `inputPacketBuffers`（`:1488-1522`）。
2. 是否继续由 `errors.Is(err, ErrNeedMoreData)` 判定（`:1309`、`:1394`）。
3. **一旦“需要更多”，后续轮次只运行 `sniff.QUICClientHello`**
   （`quicMoreData()` `:1477-1479`；分支 `:1489-1502`、`:1579-1592`）。
   因此分片的 QUIC ClientHello 不会在每个续传数据报上重跑整个 packet plan。
4. 每一轮都在一个自带截止时间的 goroutine 中读取一个新的 16 KiB `buf.NewPacket()`
   （`:1523-1564`）。
5. **上限：`maxSniffRounds = 64`**（`route/route.go`，`actionSniff`）。
   它只约束新读取的续传轮次，不约束之前对 `inputPacketBuffers` 的重放。
   达到上限时，代码会**释放**刚读取的缓冲区，并视该流为不可嗅探。
6. 其他退出路径：每轮截止时间（`:1349-1355`）、上下文取消（`:1358-1364`）、
   非超时的读取错误（`:1365-1370`）、任何非“需要更多”的判定（`:1394-1400`）。

`:1529-1546` 处的注释记录了**实测的**修复前增长：1,000 个构造的数据报保留了 1,000 个缓冲区
（+16.7 MB 常驻），每轮 QUIC 缓冲区从 33 KB 增长到 1.06 MB；
为 1,083,915 字节的输入分配了 541,710,584 字节。

> **这次测量无法从工作树复现。** 在 `route/*_test.go` 中 grep
> `maxSniffRounds|sniffRounds|"abandoned after"` 得不到任何结果。上限与
> 放弃时释放（release-on-abandon）**仅**为 `VERIFIED_SOURCE`；不要把它们当作已测试。

### “不可嗅探”意味着什么（What "unsniffable" means）

嗅探是**可选的，也可能失败**。解析器给出非“需要更多”的判定时，只会记录为 `metadata.SniffError`，
不会因此中止该流。数据包读取器有独立的致命路径：
上下文取消与非超时读取错误会返回 `fatalErr`（来自 `actionSniff`）。
不要把解析器失败的处理推广到所有 I/O 失败（`route/route.go`、`actionSniff`）。

---

## 4. sniff → routing 契约（The sniff → routing contract）

* 被嗅探出的域名经由 `resolveLookupName` 到达路由（`route/route.go:1725-1762`），
  途中排除 UoT 哨兵（`:1757-1760`），并由 `validSniffedDomain` 做严格校验（`:1764-1802`）。
* `metadata.DestinationAddresses`（`:1720`）作为**拨号候选**使用
  （`route/conn.go:284-285`、`:523-531`、`:562-563`；`common/dialer/default_parallel_network.go:15-35`）。
* 仅靠嗅探不会重写 `Destination`；与嗅探相关的例外是显式且已废弃的按动作
  `OverrideDestination`，它会保留端口。
* 另外，`route/route.go` 中的 FakeIP 反向映射会保存 `OriginDestination`，
  把 `Destination.Fqdn` 设为映射到的名字并标记 `FakeIP`。这次转换不是嗅探覆写。
* 被嗅探出的域名**会使 Direct Offload 快速路径失效**：
  `BypassRefusedSniffedDomain`（`route/route.go:832-833`、`route/bypass_verdict.go:23`、`:49`）。
* `metadata.Domain` 并不是已知域名的唯一表示：当前 FakeIP 路径可以在留空它的同时，
  设置 `FakeIP` 与 `Destination.Fqdn`。哪些情形会被接受，见
  `packetSniffUsesKnownFakeIPDomain`。

---

## 5. 性能、行为与加固的区分（Perf vs behaviour vs hardening — the classification）

这个区分至关重要：把性能改动误说成协议语义改动（或反过来），
正是本节要防止的失败模式。
**在 sniff 的历史中没有发现这种错误标注。**

| 类别 | 改动 |
|---|---|
| **PERF**（减少分配/拷贝，语义不变） | 用显式 plan 取代 planner 间接层（`sniff.go:45-77`）；用 `payloadReader` 取代 `io.MultiReader`（`:92-123`）；惰性聚合（`:204-248`）；TLS 头部预检查 + 哨兵；`takePacketBytes` 视图；`frameSummary`；**以数据包为界的 QUIC 拷贝**；HTTP 门禁 + 重放读取器 |
| **FIX**（行为缺陷） | WireGuard 嗅探器本身；**WireGuard 排在 UTP 之前的次序**；`matchRule` 的错误路径缓冲区释放（`route/route.go:390-403`） |
| **HARDENING**（恶意输入） | `Length < hdrLen+4` 守卫；`maxSniffRounds` + 放弃时释放 |
| **REFACTOR** | `defaultPacketSniffers` 间接层（`route/route.go:41`） |
| **TEST** | 新增 22 个测试文件；wireguard.go 的文档块仅为注释 |

两点需要如实说明的提醒：

1. **`bfe721454` 在一个提交中融合了 PERF 与 HARDENING。** 它把 QUIC 工作副本收窄到当前数据包，
   *并且*需要 `Length` 下界守卫，因为否则那 4 字节的头部保护写入会 panic。
   两个标签对它都成立。（无论哪种方式，结论不变：
   小于 4 字节的数据包在 GCM 下永远无法通过认证。）
2. **`9167595c7` 的 `perf` 标签依赖一个等价性，而这个等价性本身是正确性断言** ——
   `frameSummary` 必须保留帧序列所表达的内容。

fork 曾修复一处整数据报拷贝造成的内存放大问题。适配器评审发现，即使回退该修复，
整个包的测试仍然通过（`sniff_bounded_copy_internal_test.go:22-28`）：行为等价测试无法单独发现
这类资源开销问题，因此还需要专门检查拷贝范围和容量的测试。

---

## 6. 检测细节，包括两个 fork 并未修复的已记录弱点（Detection specifics, including two documented weaknesses the fork did NOT fix）

* **TLS** —— 5 字节头部预检查，type ∈ {20, 22}，version `< 0x1000`，且它从不在
  部分头部上作决定（`tls.go:29-30`、`:51-62`、`:83-87`）。
  `errClientHelloSniffed` 是一个不得逃出 `TLSClientHello` 的哨兵（`tls.go:97-101`），
  因此非 TLS 载荷不会导致重复读取。已针对真实的 `crypto/tls` 输出做穷举检查（`sniff_gate_internal_test.go:56-80`）。
* **HTTP** —— 前置检查只复现 `net/http` 自身的请求行规则，借助
  `common/badhttp` 的 `//go:linkname net/http.readRequest`（`badhttp.go:13-14`）。契约是
  单向且可测试的：**`GateRejects ⇒ ParserRejects`**；前置检查绝不能在
  解析器只会要求更多数据时拒绝 —— 已在 500 多个前缀上验证（`http.go:118-133`、`:152-155`）。
  `httpReplayReader`（`:62-83`）重放前置检查已经读取的内容。
  前置检查正确，并不意味着在每一个读取边界上都能识别：测试记录了
  请求行被切断的情形 —— 此时底层解析器报告版本格式错误，
  而嗅探尝试在下一次读取之前已经停止。某些头部切断也会失败。见
  `TestPeekStreamReadBoundaryInsideTheRequestLine` 与
  `TestStreamSniffingAcrossReadsMatchesPreOptimizationBaseline`。
* **SSH** —— `ssh.go` 忽略了 `bufio.ReadLine` 的 `isPrefix`；
  因此未完成的 banner 可能被归类为 SSH，并带有被截断的客户端字符串。
  分片基线测试记录了这条限制；不要假定记录下来的 `Client` 必然完整。
* **QUIC** —— 密码学层面：ClientHello 必须**通过认证**（`quic.go:159-229`）。
  **已知且未修复的误报：** `frames.count == 1 ⇒ ClientFirefox`（`quic.go:373-375`），
  因此三片段（three-fragment）的 Chrome ClientHello 会被报告为 Firefox。
  `quic_ownership_test.go:271-280` 将其记录为 MEASURED，并说明修复超出范围。
  **不要把它报告为 fork 回归，也不要在没有互操作方案的情况下“修复”它。**
* **QUIC v1 0-RTT** 通过类型检查但无法通过认证 —— 这是一个**先前已存在的**漏报。
* **WireGuard** —— 仅结构性检查：保留字节 `[1:4] == 0` 加上精确长度（`wireguard.go:76-113`）。
  `1/2^16` 的 uTP 残留误判**已有记录，且有意不收窄**（`wireguard.go:44-72`、
  `TestSniffWireGuardUTPAmbiguityIsStructural`）。不支持 AmneziaWG。

---

## 7. 证据（Evidence）

| 保证 | 级别 | 复检 |
|---|---|---|
| QUIC 数据包/数据报边界、合并的数据包、9 种头部变体、畸形头部、`Length < 4` | `VERIFIED_UNIT` + fuzz | `go test -count=1 ./common/sniff/...`；`FuzzQUICPacketBoundary`、`FuzzQUICPacketLengthBoundary` |
| 分片非重叠引用 | `VERIFIED_UNIT`（地址 + 行为） | `quic_ownership_test.go` |
| TLS/HTTP 前置检查正确性 | `VERIFIED_UNIT` | `sniff_gate_internal_test.go` |
| WireGuard 矩阵 + uTP 歧义 | `VERIFIED_UNIT` | `TestSniffWireGuardUTPAmbiguityIsStructural` |
| PeekStream 重试/聚合，包括伪装错误 | `VERIFIED_UNIT` | `sniff_aggregate_test.go` |
| 16 KiB 上限 / 有界拷贝 | `VERIFIED_UNIT` | `sniff_bounded_copy_internal_test.go` |
| `maxSniffRounds` 上限、放弃时释放、`matchRule` 错误路径释放 | **仅 `VERIFIED_SOURCE`** —— 无测试、无可复现测量 | — |
| 无竞态 | **`BLOCKED_BY_ENVIRONMENT`** | 需要 cgo |
| sniff goroutine 普查 | `VERIFIED_UNIT`，但**对负载敏感**：失败的测试名每次运行不同（`TestAgentFFrameShape*`、`TestTheSniffCensusMovesForExactlyOneLeakedGoroutine`）；已记录单独运行时 5/5 通过 | 在负载下重跑 |

本次审计中测得：`go test ./common/sniff/... -count=1 -v` → **PASS, ok 6.557s, exit 0**；
`-count=2` → ok 8.848s；`go vet` 干净。已记录的偶发失败（flake）在单次运行中**未复现**。

历史审计基准（2026-10-28 校准未重新测量）：`PeekPacketQUIC` 7,339 B/op / 73 allocs；
`PeekStreamTLSFragmented` 70,754 B/op / 212 allocs；HTTP 门禁对未加门禁的 DNS-over-stream 是 205 B/3 allocs 对 4,889 B/9。

---

## 8. 来源（Provenance）

| 提交 | 类型 | 它做了什么 |
|---|---|---|
| `e053cc733` | PERF + REFACTOR | 显式 plan 列表（`sniff.go:45-77`）；`payloadReader`（`:92-123`）；TLS 头部预检查 + `errClientHelloSniffed`；`defaultPacketSniffers` 间接层；`route/route.go:1239-1241` |
| `300b167b4` | PERF | 重试由上一轮的失败驱动（`sniff.go:132-199`）。有一处已记录的分歧：一个模仿报文、不触发回绕的错误会让它读取**更多**（`:176-181`），并有专门的测试。 |
| `912778dd6` | FIX | 新增 WireGuard 嗅探器；把 WireGuard 重排到 UTP 之前（`sniff.go:59-67`、`route/rule/rule_action.go:537-540`、`:547`） |
| `91577eaa2` | PERF | HTTP 门禁 + 重放读取器（`http.go:28-241`） |
| `4dff25c90` | PERF | `takePacketBytes` 返回视图（`quic.go:36-54`） |
| `9167595c7` | PERF | `frameSummary` 取代帧类型切片（`quic.go:422-475`） |
| `bfe721454` | PERF + HARDENING | 以数据包为界的拷贝（`quic.go:118-124`、`:183-188`）**以及** `Length` 下界守卫（`:125-131`）；是 `1b0cedf40` 的移植 |
| `578663e0` | HARDENING + FIX | `maxSniffRounds = 64` 与放弃时释放（`route/route.go:1347-1353`、`:1547-1553`）；在 `matchRule` 的错误路径上释放池化的 16 KiB 缓冲区（`:390-403`） |
| `1292a85a5` | TEST | `wireguard.go` 的注释块 —— **新增 0 行非注释行**，已核实 |
| `a05a6954f` | UPSTREAM-ABSORBED | “Fix DNS sniffer matching non-DNS packets” —— `dns.go` 在两端逐字节相同；**不要把 `verifyDNSQueryHeader` 归因于 fork** |
| `b93c30f2a` | UPSTREAM-ABSORBED | “Fix protocol input validation” —— `quic.go:302-304` 的 CRYPTO 长度守卫与 `:351` 在 `e4b7e692` 已存在 |
| `dd4f62edc` | UPSTREAM-ABSORBED | “Rewrite HTTP proxy protocol” —— `http.go:14`、`:95` 在 `e4b7e692` 已存在 |

三行 `UPSTREAM-ABSORBED` 依据**基线处的 blob 相等性**确立，而不是依据可达性：
fork 重新应用了自己基线已经包含的上游补丁。

---

## 9. AI 注意事项（AI cautions）

1. **绝不要让 QUIC 工作副本（`newPacket`）被池化或跨调用。** 它每次调用都是一次全新的 `make`，
   正是为了让保留的分片不会比已释放的数据报活得更久
   （`quic.go:183-184`、`:477-481`）。
2. **绝不要丢弃 `currentPacketEnd < hdrLen+4` 守卫。** 头部保护的解掩码会原地写入 4 个字节
   （`quic.go:125-131`）。
3. **绝不要在调用方的数据报内部解掩码。** 每个边界测试都断言输入之后
   逐字节不变。
4. **绝不要让 `payloadReader` 逃出 `PeekStream`。** 它借用的是池化的 segments（`sniff.go:79-91`）。
5. **绝不要拒绝一个解析器只会要求更多数据的前缀**（`http.go:118-133`、`tls.go:51-54`）。
   那会把 `ErrNeedMoreData` 变成一种判定，并丢掉每一个分片流。
6. **绝不要让 `errClientHelloSniffed` 逃出 `TLSClientHello`**（`tls.go:97-101`）。
7. **绝不要跨轮次重新累积 `roundErrors`，也不要每一轮重建聚合结果**
   （`sniff.go:154-163`、`:191`）。
8. **绝不要跨分片轮次复用数据包缓冲区，也绝不要丢弃放弃时的释放操作**
   （`route/route.go:1318`、`:1343`、`:1386-1391`）。
9. **绝不要在没有重新测量的情况下提高 `maxSniffRounds`。** 64 是一个 DoS 上限，
   附有已记录的修复前测量。
10. **绝不要收紧 UTP 检查，也不要开始检查 WireGuard 的第 4–147 字节**
    （`wireguard.go:54-71`）。该歧义已有记录，且是结构性的。
11. **绝不要用嗅探出的域名重写 `Destination`**，唯一的例外是已废弃的 `OverrideDestination`
    （`route/route.go:1421-1425`），也绝不要在没有 `validSniffedDomain` 的情况下
    解析被嗅探出的名字（`:1764-1802`）。
12. **不要从 `metadata.Domain` 推断整个 FakeIP 状态。**
    当前的反向映射可以改为在 `Destination.Fqdn` 中携带已知域名。
13. **绝不要用 `runtime.NumGoroutine` 做泄漏普查**（`sniff_stream_test.go:21-47`、`:89-126`）；
    这些测试是围绕一个限定作用域的测量工具校准的。
14. **绝不要把 `quic.go:302-304` 或 `http.go:95` 归因于 fork。**
    在 `e4b7e692` 处的 blob 相等性证明并非如此。
15. **不要把 QUIC 单帧 `ClientFirefox` 误分类报告为 fork 回归。**
    它已被测量、记录，且有意排除在范围之外。
