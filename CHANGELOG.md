# JiejieBox 更新日志

按正式版本倒序记录改动。每版先概述目的，再保留协议、配置、修复与当时的验证细节；历史实验只适用于标注的提交、平台和测试条件。当前配置用法见[扩展配置示例](docs/FORK-CONFIGURATION.md)。

[正式发布与下载](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases) · [原始详细日志归档](docs/archive/2026-10-dev-cycle/CHANGELOG-DETAIL-2026-11-01.md) · [工程架构与改造评估](KNOWLEDGE/reports/core/JiejieBox_工程架构与改造最终评估报告_2026-11-01.md)

## [v0.1.48](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.48) — 2026-11-14

### 本次更新

v0.1.48 是**跨层资源治理收口 + 依赖分支整合**版本。本版把共享传输里最后几处"物理创建不经过预算"的入口补齐到既有 resourceguard 体系，合入 AnyTLS 会话创建合并与 sing-tun 流观测，修正 HTTP/MASQUE 入站拥塞控制配置不生效的问题；三个依赖仓库的工作分支先合入各自主线，主仓再消费合并后的提交，不再引用临时分支。

#### 一、跨层资源治理收口

本轮不是给每个协议都加一层 gate，而是找出创建与生命周期上的真实断点，在既有 seam 上补齐；没有新增预算类型、第二个压力采样器或新的状态机。

- **HTTP/3 物理创建纳入准入**：共享 HTTP/3 客户端（MASQUE、DoH3、provider 拉取）与 XHTTP 的 H3 池此前直接 `quic.DialEarlyConn`，不受 `AdmitCreation` 约束；现在从构造 context 取 guard，在 UDP dial 与 QUIC 握手外包一次 permit，连接发布后归还，池化复用不重复计费。饱和时构造器不执行、连接不发布，拒绝仍归类为本地背压而不是节点故障。
- **v2rayquic 共享创建**：`transport.type=quic` 的 VLESS/VMess/Trojan 共享 QUIC 连接创建是共享 QUIC 家族里唯一未计费成员，现按同一 `CreationAdmission` 设计补齐。
- **非 connected UDP 的 setup gate 与 traffic class**：`route/conn.go` 的 `ListenPacket` 路径此前绕过 dial setup gate 和 traffic class 传播；网络切换无法取消在途 setup，交互流会被当成后台流量。现在该路径与 connected 路径共用同一套 gate/class 语义，已建立的 packet flow 不因网络代次迁移被误关。
- **OOMKiller 采样诚实性**：`phys_footprint` 读取失败时 `memory.Total()` 返回 0，曾被当成真实测量并发布 `MemoryPressureNone`，让准入回到最宽松档。现在 0 被分类为"不可测量"，状态保持在 ARMED 线并写入报告；轮询顺序与平台作用域同时补了测试。
- **DNS 并发上界**：`Router.Exchange` / `ExchangeAsync` / `Lookup` 统一经过 256 并发 / 256 队列 / 2 s 等待的 query gate；异步路径的 permit 保持到回调，成功、失败、重复回调都恰好释放一次。gate 不读内存压力——DNS 是恢复路径的控制面工作，不应被额外掐死。
- **Critical 压力下的后台让路**：URLTest 周期探测与 DNS optimistic refresh 在 Critical 压力下不再发起新的后台创建；Armed 仍放行，避免把正常调度误伤成停摆。
- **准入观测与拒绝分类**：gate 增加等待者高水位 `Stats.MaxWaiters`；H3 的本地拒绝（队列满、等待超时、rate window、调用方 deadline）不再推进 H3→H2 的 broken ladder，真实握手失败仍然推进。
- **XHTTP 请求体生命周期**：包装体的 `Close` 现在委托给底层 body，避免提前释放或漏释放。
- **AnyTLS 创建者语义**：共享创建保留首个调用者的 deadline（无 deadline 时 30 s 上限），等待者的取消不会拆掉创建；混合优先级等待者共享创建者的 admission 类别。
- **NAT 首包**：本轮给出 `DECISION` 而不是代码改动——首包归两套 stack writer 所有，释放点在 handshake 回调，主仓没有 TUN 环境可验证跨栈桥接；量化上界在观测预算内（200 flow 约 0.3 MiB），保留为后续设备验证项。

#### 二、AnyTLS 会话创建合并（sing-anytls main）

突发首流此前在每个 goroutine 里都看到空池，各自建连、握手、首写；gate 只限速（32 并发），不减少总量（200 flow 约 200 次握手）。sing-anytls 现在：

- 维护单一在途创建，首个调用者创建，其余等待同一结果并在成功后直接开流；
- 创建使用首个调用者的 deadline 与客户端生命周期，等待者取消不影响共享创建；
- 失败时同一错误唤醒全部等待者，成功但 session 在 waiter 使用前关闭时允许一次新创建；
- `Close` 取消创建并唤醒所有等待者；
- 暴露 `PendingCreationWaiters` 诊断 gauge。

主仓集成测试让 200 个并发首流等到 199 个 waiter 全部附着后放行创建：**1 次创建、1 次 admission、1 个物理 accept、200/200 成功**，`-race` 同样通过。

#### 三、TUN 流观测与 Go stack 首包快照（sing-tun dev）

- 新增只读 `UDPNatStats`（entries、sessions、queued bytes、dropped 等）与 TCP dispatcher 统计（dispatches、active goroutines、per-dispatch 成本），供主仓诊断和预算评估消费。
- 修复 Go stack 首包快照的生命周期：握手成功、握手失败与 `Close` 三条路径都会释放快照，不再把首包引用留到连接结束；补了"Close 与握手并发"的回归。
- 增加 2000 / 4096 流的规模测量测试，给出 entries、sessions、queued、goroutines、heap 的实测值与回收后的基线。

#### 四、HTTP / MASQUE 入站的拥塞控制

`quic_congestion_control` 在 HTTP/MASQUE 入站会被解码，但服务端此前只读 `bbr_profile`，显式写 `cubic` / `bbr` / `reno` 是静默 no-op。现在：

- `bbr_profile` 保持原语义（生产拓扑的 `standard` 不变）；
- `quic_congestion_control` 仅在 `bbr_profile` 未设置时生效，`cubic` / `reno` 安装 `congestion_meta1` sender，`bbr` 选择标准 BBR；
- 两者同时设置是**构造期错误**，不再让其中一个静默胜出；
- 未知算法在 `sing-box check` 与 `run` 都拒绝；
- 两者都不设置时保持 quic-go 自带 sender（本 pin 为 Reno），不改变既有部署；
- 用真实 QUIC 握手断言安装的 sender 类型，而不是只测解析函数。

#### 五、依赖与分支整合

- `sing-anytls`：`work/creation-coalescing-2026-11-14` 合入 `main`（fast-forward，`8473492`）。
- `sing-tun`：`work/flow-observation-2026-11-14` 合入 `dev`（fast-forward，`ab63d63`）。
- `JiejieBox`：`work/resource-governance-closure-2026-11-14` 合入 `testing`，保留已合入的 PR #6（双栈 ALPN / 日志 / CUBIC 默认）与 PR #7（HTTP/MASQUE 拥塞控制），未回退任何一项。
- 主仓 `go.mod`、`test/go.mod` 与锁文件改为消费合并后的提交：`sing-tun v0.9.7-0.20261114051221-ab63d63ac306`、`sing-anytls v0.0.0-20261114030206-8473492a5afe`。
- 客户端 gitlink 本轮**保留**在已验证组合：Apple 分支头 `a867f61` 在 iOS/macOS 编译中报 `missing argument label 'report:'`，不能固定一个未编译的客户端；Desktop 与 Apple 作为一组保留，待客户端修复后一起更新。

#### 六、验证与发布

- 本地：`go build ./...` 与全量 Release 标签构建均为 exit 0；`go vet` 两种标签集通过；受影响包单测与 `-race`（`common/resourceguard`、`common/httpclient`、`transport/v2rayxhttp`、`transport/v2rayquic`、`route`、`service/oomkiller`、`protocol/group`、`protocol/anytls`、`dns`、`protocol/naive`、`transport/http`、`option`）全部通过。`route` 的 `TestBackpressure_KernelGiveUpWindow` 在 Windows 仍会挂起，与基线一致，属环境限制，已用 `-skip` 排除并记录。
- CI：`5017b8864` 上 Verify（含 race checks）与 Release contract 全绿；Android Core ARM64、Mobile contract 成功。
- [Kernel release 43569490140](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43569490140) 的 freeze、contract、Verify/race、Linux、macOS、Windows core、Desktop 安装器、Android AAR、Apple Libbox 与 iOS/macOS/自签、stage、publish 全部通过。
- 内核 tag `v0.1.48` 指向 `5017b8864dca20eaa6bec203dd912d9f7cba0e14`；公开附件 8 项：Linux/macOS/Windows 内核、Windows x64 安装器、Android ARM64 AAR、Apple Libbox、未签名 IPA、`SHA256SUMS`。
- - [Satelite v0.1.48](https://github.com/Piggy-Cat-bit-shadow/satelite-one/releases/tag/v0.1.48) 已发布：`versionCode=59`、`coreCommit=5017b8864dca20eaa6bec203dd912d9f7cba0e14`，`arm64-v8a` / `x86_64` 双签名 APK、`build-info.json` 与 `apksigner-verify.txt` 均已回读（run 43571517986）。
- TestFlight 上传、签名与真机验收尚未执行，按独立指引继续；本版不把未运行的设备项写成 PASS。

## [v0.1.47](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.47) — 2026-11-14

### 本次更新

v0.1.47 是 0.1.46 之后的稳定性与移动端能力收口版本。除发布门禁和移动端 Libbox 能力修复外，本版集中处理了 0.1.45→0.1.46 审计中剩余的并发 map、连接生命周期、协议代次和诊断边界问题；下面按影响面列出。

#### 一、双栈 / IPv6 崩溃修复（重点）

**这是本版最重要的崩溃修复，之前日志里只被笼统写成“dual-stack recovery 的并发 map”，没有说清楚。**

`LookupFamilies` 会把 A 和 AAAA 查询放在两个 goroutine 中并行执行，并分别从各自的 goroutine 调用 publisher。恢复消费者仍按“只有一个 lookup goroutine”的旧假设，让两个 family 的回调同时对 `dual_stack_recovery.go` 的 `seen` map 做读、比较和写；两个 family 同时返回时，Go runtime 直接以 `concurrent map read and map write` 致命错误终止进程，在 Apple Network Extension 中表现为连接恢复阶段崩溃。

修复方式：

- 给单个 lookup 的 `deliver()` 加 per-lookup mutex，使“读取地址 → 比较代次 → 写入 seen → 发布批量结果”成为一个原子步骤。
- 锁一直持有到向内部 channel 发送结果，避免从半更新的 identity 集合中发布批次。
- 保持 A/AAAA 并行查询、source generation 升级规则、去重和两个 recovery consumer 的行为不变。
- 增加同时释放两个 family publisher 的回归测试，覆盖双 family 发布、IPv4 / IPv4-mapped 重复只发一次、最新 generation 保留，以及 `stop()` 唤醒持锁等待的回调。
- 验证：`go test -race -count=1 ./common/dialer ./dns`。

#### 二、协议家族恢复与代次

- **TUIC / Hysteria2**：并发 `DialConn` 会同时判定 `established=false`，各自竞争同一对 client，并各自宣布不同 family 为 winner；一个 race 的 loser cleanup 会把另一个 race 已开始使用的 client `CloseWithError`。`creationAccess` 现在 single-flight 首次创建，后来者等待后直接拨已发布 winner，并在 stream dial 前释放锁。
- 会话在 `CloseWithError` 已移动代次后才建立时，旧的 `retireFamily` generation 检查会跳过它，导致 QUIC session、socket 和 goroutine 永久不可达；现在按实例身份关闭旧实例，仍拒绝触碰同代次的新实例。
- 全失败分支不再丢失 `settled` 并遗留两个失败 client，而是先退休再返回。
- **GenerationGate.Close**：Scope cleanup 和 manager 的重复 tag loser 都可能调用 Close，原来对 `g.remove` 的读/调用/置 nil 没有同步，既产生字段竞态，也可能执行两次 unregister；改为 `sync.Once`，32 goroutine 并发 Close 只执行一次 remove。
- **URLTest FailoverState**：健康轮修改状态，而 `HealthSnapshot` / `ApplyFailoverPolicy` 可能并发读取或替换；`FailoverState` 内部加锁，group 的 failover 指针在 `g.access` 下读写，消除竞态。
- **URLTest 共享测量**：`Join` 对 display/diagnostic key 返回的 leader wait 是已经关闭、且从未注册的 wait，`Publish` 只检查 `wait.closed` 为 false 就再次 close，任何遵循 leader-then-Publish 契约的调用都会确定性 panic；现在把该 wait 预先标记为 closed，`Publish` 成为 no-op。

#### 三、连接池与传输生命周期

- **DNS ConnPool `AcquireFresh`**：原来把 fresh 拨号包进 `trackingDial`，但 fresh 连接从不进入池，池的 Release/空闲淘汰/Reset/Close 都不会清理 `dialPaths`，于是每个 stale-connection retry 都把已死亡的 `*multiplexConn` 和 step slice 留到 transport 生命周期结束。现在直接拨号，caller 自己的 DialPath recorder 仍从 context 收到步骤；回归测试做 32 次 AcquireFresh 并断言 `dialPaths` 为空、池未关闭任何 caller-owned 连接。
- **XHTTP XMUX**：选池与 `addOpenUsage(1)` 之间存在窗口，换网/trim/Close 可以在两者之间关闭 client，随后 `Add(1)` 把已释放连接重新唤醒，可能发出请求或创建孤儿 HTTP/2 连接。现在在 client 锁内用 `acquireUsage()` 原子预约，失败则 `discardRetired` 后重新选池；新增回归测试禁止已退休连接复活。
- **TrustTunnel 在途 CONNECT**：CONNECT 已经发出、尚未收到 200 时没有被计入 `streams`，`RetireSuspect` / `CloseIdleConnections` 可能据此关闭 carrier，造成重复 CONNECT 或在已关闭 session 上开流。现在在把 CONNECT 放上线之前 `streams.Add(1)`，所有失败路径归还并 `drainIfUnused()`，session 会等这次 CONNECT 结束再退休。
- **v2rayquic Reset**：Reset 先清 pending，再等待 `migrateAccess`；等待期间新 demand 可以创建并发布新代次连接，旧 Reset 随后会错误地退役/迁移这个新连接。现在 Reset 开始时捕获连接，拿到迁移锁后只有仍是同一对象才处理。
- **MASQUE 会话释放**：旧顺序先把地址归还池、再通知 handler 释放 flow；新 session 可能拿到回收地址并注册 flow，随后被旧 session 的 ReleaseSession 关闭。现在先 `ReleaseSession`，再把地址还给池；flow registry 增加 closed 状态，teardown 后到达的 flow 立即关闭并回调 onClose。
- **HTTP/3 attempt**：H3 attempt context 在成功后仅停 timer，从未 cancel，长生命周期 parent 会保留子 context；现在错误路径直接 cancel，成功响应在 body Close 时 cancel，且重复 Close 安全。
- **artifact `Version.Release`**：普通 bool 的幂等判断在并发 double release 下会重复递减 lease；改为 atomic one-shot latch。

#### 四、路由、诊断与连接关闭

- **route `CloseAll`**：原来先把连接从 tracked 集合摘除、解锁后才 `beginSynchronousCloses`，并发的最终 `Close` 可能看到 pending/syncing 全为 0 而提前返回，此时 socket 还没有真正 Close。现在同步关闭计数与摘除在同一 `m.access` 临界区完成。
- **route admission**：`AcquireConnection` 只在等待分支检查 context；即使 ctx 已取消，只要还有 room 仍会发放 ticket，可能让已撤回的拨号安装 socket。现在每轮在锁内检查 `ctx.Err()`，取消的请求不再消费 admission slot。
- **libbox 诊断 emitter**：`Publish` 在锁内检查 `closed` 后解锁，再调用 `appendAndOffer` 重新加锁；Close 可以在这两步之间写入 end event，随后 Publish 再追加 stage/finding。现在 Publish 与 Close 共用同一个 finalisation 临界区，end 事件之后不可能再有事件。

#### 五、DNS NXDOMAIN 日志

原来的 NXDOMAIN 每个地址族打一条 ERROR，继承该 resolver error 的每个失败连接再打一条 ERROR。NXDOMAIN 是正常负答案，RCODE、返回错误、负缓存和拨号行为都不变，只把日志级别移到 DEBUG。

新增类型化 `dns.IsNXDomain`：递归遍历 wrapped/aggregated error，要求**每一个叶子**都是 `RcodeError(NXDOMAIN)`；混合 NXDOMAIN+SERVFAIL、纯 SERVFAIL、超时、网络/协议错误和普通文本都不算 NXDOMAIN，仍保持 ERROR。DNS lookup 分支和 `ConnectionManager` 的 TCP / connected-UDP / listen-packet 失败日志都使用该分类。

#### 六、TrustTunnel 双栈 ALPN

双栈 TCP+QUIC 入站原来共享一个 ALPN 列表，TCP 可能协商 h3，QUIC 可能协商 h2。现在：

- TCP 列表只保留 h2 和配置中的非 h3 协议，绝不含 h3；
- QUIC 列表只提供 h3；
- 双栈时 QUIC 使用 `tls.TransportALPNView`，只隔离 ALPN，证书 provider、ACME 和 file watcher 仍只有一份，不复制生命周期；
- 启动时打印最终 ALPN；
- 服务端 QUIC 协商到非 h3、客户端 ALPN 不匹配时输出 WARN；
- `sing-box check` 对旧式共享 ALPN 配置给出告警（不直接报错，因为 view 接入后双栈形态已安全）。

#### 七、Apple / Android Libbox 能力

v0.1.46 的 Apple Libbox 是在移动端共享 tag 集缺少 `with_trusttunnel` 时构建的，所以 TF App 加载 trusttunnel 配置会报 `trusttunnel is not included in this build`。本版：

- `cmd/internal/mobilebuildtags` 补齐 **`with_trusttunnel`** 和 **`with_awg`**，Apple/Android Libbox 不再在运行时拒绝这两类配置；
- `build-apple-libbox.sh` 在构建前断言解析出的 Apple tag 集包含二者；
- Libbox manifest 和 `BUILD-INFO-LIBBOX.txt` 记录 `build_tags`，`validate()` 要求同时包含 `with_trusttunnel`、`with_awg`；
- 没有 `build_tags` 的旧 v0.1.46 Libbox 会被拒绝，不再静默安装；
- Mieru 没有构建标签，`include/registry.go` 无条件注册，所有移动端产物均已包含；不添加无实际 gating 的 `with_mieru` 空标签。

#### 八、Naive 拥塞控制说明

`quic_congestion_control` 未设置时，库默认是 **CUBIC**，不是 BBR。文档和启动日志现在明确说明这一点；BBR 需要显式设置，行为没有改变。

#### 九、验证与发布

- 内核 tag `v0.1.47` 固定于 `c50336676eb90e03792c81284c736c1a152ff684`。
- [Kernel release 43519793788](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43519793788) 的 freeze、Verify/race、Release contract、Linux、macOS、Windows、Android、Desktop、Apple Libbox 与消费端、stage、publish 全部通过。
- 公开附件 8 项：Linux/macOS/Windows 内核、Windows x64 安装器、Android ARM64 AAR、Apple Libbox、未签名 IPA、`SHA256SUMS`。
- 公开 Libbox manifest 已回读：`parent_sha=c503366...`、`build_tags` 同时含 `with_trusttunnel` 和 `with_awg`、slices 为 `ios-arm64` 和 `macos-arm64`。
- macOS 单测暴露的 half-close 契约测试初始化竞态也已修复：`newHalfCloseFixture` 启动 copy goroutine 后才设置 `configErr`，导致 `SkippedUnarmable` 永远等不到；修复为在 `NewConnection` 之前预置配置错误，并断言 `Pending == 0`。这是测试修复，不改变 half-close 产品行为。
- [Satelite v0.1.47](https://github.com/Piggy-Cat-bit-shadow/satelite-one/releases/tag/v0.1.47) 已发布：`versionCode=58`、`coreCommit=c503366...`，双 ABI 签名 APK、`build-info.json` 和 `apksigner-verify.txt` 均已回读。
- TestFlight 上传、签名与真机验收尚未执行，按独立指引继续。

## [v0.1.46](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.46) — 2026-11-13

### 本次更新

v0.1.46 围绕 **TrustTunnel 在 UDP/QUIC 不可用时的连接恢复**，增加显式启用的 HTTP/3 → HTTP/2 会话建立回退。默认配置保持原来的传输选择；开启后，优先尝试 H3，在可回退的建立失败后使用同一节点的 H2，并按网络状态和真实业务需求恢复 H3。

### 一、TrustTunnel H3 → H2 回退

#### 配置与服务端条件

- 新增 `quic_fallback_to_h2`，默认关闭，需同时设置 `quic: true`。服务端须在同一地址和端口提供 UDP/H3 与 TCP/H2，回退不切换节点或用户身份。
- 未指定 `tls.alpn` 时自动配置 H3/H2；显式指定时必须同时包含 `h3`、`h2`。缺少 `with_quic`、未开启 `quic` 或 ALPN 不完整时，在构造阶段明确拒绝配置。
- 出站的 `network` 仍选择隧道内 TCP/UDP 业务；入站的 `network` 才选择 TCP/H2 与 UDP/H3 监听。

在已有 TrustTunnel 出站中启用：

```json
{
  "quic": true,
  "quic_fallback_to_h2": true
}
```

这是字段片段；完整节点、服务端条件与排查方法见 [TrustTunnel 出站配置](docs/configuration/outbound/trusttunnel.zh.md#h3--h2-回退)。

#### 有限尝试与按需恢复

- **H3 尝试上限 3 秒**：父级创建期限更短时以父级为准，H2 使用剩余时间，不额外延长业务等待期限。
- **失败后冷却 30 秒**：符合回退条件的 H3 建立失败会暂时抑制新的 QUIC 尝试，窗口内新建会话直接走 H2，减少连续请求反复等待 UDP 超时。
- **有需求才恢复 H3**：冷却到期后，新业务让回退 H2 会话退出新流分配，再尝试 H3；没有业务时不定时探测。旧 H2 会话上的 TCP stream 与 UDP association 继续完成，不迁移、不重放业务字节。
- **换网重新判断**：清除原网络的冷却状态；异步成功和失败均核对网络代次，避免旧网络结果清除或重新设置新网络的判断。
- **保留真实错误**：H2 也失败时同时返回两条路径的原因，便于区分 UDP 不通与节点整体不可达。

#### 回退的安全边界

回退只处理新会话的传输建立。认证、证书校验、对端 TLS alert、业务目标拒绝与创建取消不会被当成普通 QUIC 故障；补齐 QUIC `CRYPTO_ERROR` 所携带 TLS alert 的分类，避免绕过明确拒绝。多个等待者仍共享一次会话创建，单个等待者退出不取消其他请求仍需要的工作。

### 二、回归验证与发布修复

- 新增真实 TCP/UDP relay 黑洞回归：同一端口丢弃 UDP、保留 TCP，确认实际 QUIC 尝试失败后由真实 H2 传输完整载荷；恢复 UDP、等待缩短的测试冷却期到期后，由新需求重建 H3，原 H2 流仍可传输。[Linux CI 43391142474](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43391142474) 在 `ce8c07e10` 显式启用 `with_quic` 并通过该场景；相关源码至正式 tag 无变化。台架验证不等于所有公网或移动网络实测。
- 覆盖默认关闭、短期限、取消、共享创建、节点隔离、TLS 错误分类，以及换网后旧成功/失败的并发交错。
- 修复 DNS fastest 组选举过期测试的同步窗口：先等待上一轮选举收集器结束，再验证过期重选。保留重新向三个上游查询的断言，不改变生产选举策略；本地按 1/2/8/32 CPU 各运行 1,000 次通过，正式 macOS 单测通过。
- 重新生成维护账本，更新源码 marker 行号与回归测试文件计数，修复发布前的账本门禁失败。

### 三、正式发布与客户端对齐

- 内核 tag `v0.1.46` 固定于 `c795bb5cbbcd9b6a4d2d2f18e854b629915335f5`。[Kernel release 43404981823](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43404981823) 的平台构建、Verify/race、发布契约、Apple Libbox 与消费端、stage 和 publish 全部通过，公开附件共 8 项。
- [Satelite v0.1.46](https://github.com/Piggy-Cat-bit-shadow/satelite-one/releases/tag/v0.1.46) 对齐同一内核，`versionCode=57`，发布 arm64-v8a / x86_64 两个签名 APK 及验证元数据。Apple 源码 pin 保持 `aa8e16aa21a7f28aefc944fac76086c144195e3b`；本轮不包含 TestFlight 上传或真机验收。
- 发布耗时、失败原因与恢复证据见[交接记录](docs/releases/RELEASE-0.1.46-HANDOFF.md)；维护账本、平台冒烟和重跑身份的前置核对纳入 [Release 指引](docs/RELEASE-WINDOWS.md)。

## [v0.1.45](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.45) — 2026-11-13

### 正式发布与最后整合

- 正式版 tag `v0.1.45` 指向内核提交 `97091ce3637e68533c060ebac4ff9e309e512f25`。[Kernel release 43360468121](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43360468121) 的冻结、Verify、发布契约、平台构建、stage 和 publish 完成；公开 Release 含 Linux/macOS/Windows 内核、Windows 安装器、Android ARM64 AAR、Apple Libbox、未签名 IPA 和 SHA256SUMS，共 8 项附件。
- Apple 客户端最终固定为 `aa8e16aa21a7f28aefc944fac76086c144195e3b`，与 `release/source-lock.json` 的 gitlink 一致；iOS、macOS 和自签构建消费同一次 Libbox。发布候选的 `release/pin-runs.json` 记录实际构建过这组 pin 的运行，冻结检查再次核对，避免只凭版本锁或工作流声明推断已经执行。
- 最后一轮还补齐了独立 Native API 服务的实例身份，使连接事件订阅在独立 `run` 形态下也能拿到初始 Reset 快照；HTTP/3 并发竞速取消后的能力判断保留有效代次，不因取消一条落败路径而抹掉仍适用的观察。

### 本次更新

v0.1.45 是一次覆盖**协议能力、内存与资源管理、网络恢复、规则与配置系统、诊断和客户端协作**的集中更新，并包含相关依赖仓库的同步融合。

新增 TrustTunnel H2/H3 客户端与服务端、AmneziaWG 3.x / 3.1；在现有协议栈上完善共享创建、UDP 转发、连接重置和并发唤醒。与此同时，围绕复杂配置和多协议共同运行的场景，推进跨流内存预算、资源生命周期、网络代次、配置与规则产物恢复，以及客户端操作回执的一致性。

本次更新的主要内容：

- **协议扩展**：TrustTunnel、AWG，以及 QUIC、NaiveProxy、Mieru、MASQUE、WireGuard 等协议的适配与改进。
- **内存与系统整合**：真实创建准入、XHTTP 共享上传预算、relay 按需扩容、TUN/UDP 背压、构建并发与回收反馈。
- **网络适应性**：IPv4/IPv6 候选调度、DNS 来源隔离、换网后的连接池和协议状态更新、活跃 QUIC 流迁移。
- **策略与规则**：`resolve.match_only`、策略组 `hidden`、URLTest 省电 failover、共享测速和大型关键词编译匹配。
- **配置与订阅**：配置分析、内容级变更计划、Provider 应用报告、流式规则产物和精确版本恢复。
- **诊断与客户端**：DNS 历史、主动连接诊断、连接事件与快照、切组回读、重载关联及跨端会话处理。
- **依赖与发布**：基础网络库、协议 SDK、Cronet 绑定与客户端消费版本整合，更新默认协议构建档位。
- **性能与可靠性**：协议台架、并发传输修复、规则匹配实测，以及故障注入和生命周期回归工具。

### 一、版本全貌：协议扩展与系统协同

这次更新围绕两件相互关联的事情展开：扩充可用协议与配置能力，并让这些能力在同一套内核中更协调地运行。协议连接会消耗内存、依赖 DNS 与路由、受到换网和休眠影响，也要接受客户端的切组、重载与诊断。因此，本版同时调整这些环节之间的交接方式。

整个版本可以沿着一条实际使用链路理解：**配置与订阅进入系统 → 规则与节点发布 → DNS 和策略选择 → 创建或复用连接 → 转发数据 → 处理换网、压力与关闭 → 将真实状态反馈给客户端**。后面的更新按这条链路涉及的主要能力展开。

#### 系统整合：把新能力接入同一套运行时

本版的整合重点是复用公共机制，同时保留协议差异。

| 系统环节 | 本版的实际接点 | 带来的作用 |
|---|---|---|
| DNS 与候选调度 | 查询来源、逐族答案、字面 IP 恢复、候选列表向组成员传递 | 地址选择与真实执行环境一致，减少重复选路和恢复失效 |
| 昂贵创建 | TLS、QUIC shared offer、AnyTLS session、Mieru socket、Cronet native dial | 在实际分配昂贵资源的位置计费，复用不重复算创建 |
| 网络变化 | runtimecoord、协议 GenerationGate、池借出与结果发布检查 | 补上“代次已更新、接口回调尚未处理”的窗口 |
| 内存压力 | 同一 Guard/OOM 压力来源连接创建、copy growth 与构建预算 | 压力越高越少接纳新负担，已有工作仍按真实寿命记账 |
| 生命周期 | Scope、endpoint/client closer、租约与旧代 drain | 失败创建有人关闭，旧代有用户时保留，最后释放有归属 |
| 配置与产物 | Stage/Publish、manifest、配置组合提交、精确引用 | 配置、节点和规则可以按同一版本组合恢复 |
| 客户端操作 | daemon/libbox、request/revision/session、真实 readback | 界面结算对应实际应用的那次操作 |
| 观测 | queue/build/residency/reclaim、DNS path、协议阶段 | 解释谁在排队、谁还驻留、哪层回收以及请求走了什么路径 |

公共层不强行把 Cronet Engine、QUIC session、TCP 池和按需 tunnel 当作同一种资源。不同协议继续拥有自己的创建、复用、重置和关闭方式，统一的是预算、身份、结果发布和可核验的观测边界。

### 二、新协议与现有协议栈升级

本版新增 TrustTunnel 和 AmneziaWG 协议能力，同时推进现有 QUIC、HTTP 隧道、NaiveProxy、Mieru、MASQUE 与 WireGuard 的适配。工作覆盖认证、业务承载、共享连接、UDP 关联、并发唤醒及底层 SDK 生命周期。新协议进入已有的 DNS、路由、拨号与资源管理体系，现有协议也一起调整公共机制的接入方式。

#### 新增 TrustTunnel：H2 / H3、TCP / UDP 与双向角色

新增 `type: "trusttunnel"` 的原生 outbound 和 inbound，将 TrustTunnel 纳入本内核的 TLS、拨号器、DNS、路由、流量统计与关闭生命周期。

##### 传输与协议能力

- **HTTP/2 路径**：通过 TLS/TCP 建立共享连接，用 CONNECT 承载业务。
- **HTTP/3 路径**：通过 QUIC 建立共享连接，`quic: true` 选择 H3；两种传输分别保留自身的连接和错误语义。
- **TCP 转发**：业务连接通过共享 session 中的独立 stream 转发，而不是每条流都重新建立完整外层会话。
- **UDP 转发**：实现 `_udp2` framing 与 association 管理，保持数据报边界和目标地址信息。
- **用户名/密码认证**：出站配置单用户凭据，入站配置用户列表；TLS、SNI 与 ALPN 在构造阶段校验。
- **QUIC 拥塞控制**：提供 `bbr`、`cubic`、`reno` 选项。
- **健康与空闲管理**：提供 `health_check`、`idle_timeout`，共享创建、空闲退休和活动 stream 分开管理。
- **内核集成**：使用统一 Dialer、DNS query options 与 traffic class，可参与组选择及适用的 detour 链。

##### 客户端与服务端使用边界

出站的 `network` 表示允许承载的 TCP/UDP 业务；入站的 `network` 用于选择监听传输：`tcp` 对应 HTTP/2，`udp` 对应 HTTP/3。H2/H3 的网络监听与隧道内承载的业务类型分别处理。

以下为 H3 出站片段；H2 将 `quic` 省略或设为 `false`：

```json
{
  "type": "trusttunnel",
  "tag": "trusttunnel-out",
  "server": "proxy.example.com",
  "server_port": 443,
  "username": "user",
  "password": "替换为密码",
  "quic": true,
  "quic_congestion_control": "bbr",
  "health_check": true,
  "idle_timeout": "5m",
  "tls": {
    "enabled": true,
    "server_name": "proxy.example.com"
  }
}
```

服务端实现对应的认证、CONNECT、UDP association 和标准路由交接。已有互通记录覆盖本实现客户端连接官方端点、官方客户端连接本实现服务端，以及 H2/H3 的 TCP 完整载荷与 UDP 往返。

##### 新协议也进入公共资源治理

同一 session 的多个等待者加入一次共享创建，创建许可只计一次；一个等待者退出不会取消其他等待者仍需要的工作。网络变化、reset、迟到创建与关闭分别核对归属。UDP 以关联槽位、每关联队列和共享字节预算约束资源，不靠无限缓存吸收过载。

#### 新增 AmneziaWG 3.x / 3.1 与完整 WireGuard 设备接线

在现有 `type: "wireguard"` endpoint 上接入 AmneziaWG 扩展，支持主动连接与 Linux 监听应答。配置继续使用原有 endpoint、peer、路由和按需设备模型，AWG 字段位于 endpoint 根层。

##### 混淆与 3.x 参数

| 能力 | 配置字段 | 实际用途 |
|---|---|---|
| 握手前 junk | `jc`、`jmin`、`jmax` | 控制垃圾包数量和大小范围 |
| 消息前缀 | `s1`–`s4` | 对握手、响应、cookie 与传输消息增加对应前缀 |
| 魔数/范围 | `h1`–`h4` | 替换 WireGuard 消息类型标识，接受单值或范围 |
| Controlled Packet Sequence | `i1`–`i5` | 按顺序发送配置的伪装包序列 |
| 声明式伪装 | `id`、`ip`、`ib` | 用域名、QUIC/DNS/STUN/SIP 形态及适用浏览器模板生成伪装序列 |
| 3.x 头保护 | `header_protection_key` | 保护消息类型、receiver index 与 counter 等头部信息 |
| 加密内容 padding | `content_padding_addition` | 在加密数据内部添加指定范围的 padding |
| 握手与重密钥时序 | `rekey_after_time`、`rekey_timeout`、`reject_after_time`、`keepalive_timeout`、`max_handshake_attempts` | 以单值或范围配置适用的协议时序 |
| 3.1 随机尾部 | `random_trailers` | 为消息增加随机长度尾部，改变固定长度形态 |
| 3.1 cookie 策略 | `disable_cookies` | 显式配置相应的 cookie 行为 |

范围字段支持数字及 `"min-max"`，统一作边界和规范化处理；单值回写保持数字形式，范围保留字符串形式。配置对象由代码构造时也执行相同校验。

##### 设备、握手与安全处理

- 使用包含 AWG 扩展的 `wireguard-go` fork，参数从 option 经过设备构造进入真实 IPC/Device 配置。
- 同一条设备路径支持普通 WireGuard 与 AWG，保留 peer 路由、UDP socket family、MTU 和按需恢复。
- 完善 responder、cookie challenge、重放拒绝、多 peer、未授权 peer、roaming 与反向业务数据处理。
- Header Protection 与 padding 长度作配套校验；声明式伪装和显式序列保持各自的配置约束。
- `with_awg` 进入相应默认发布 profile；精简构建未启用 AWG 时，对配置给出明确拒绝，不静默降级成普通 WireGuard。
- 修复并发业务的 outbound-space 等待/唤醒链，具体载荷结果见后面的性能与实测章节。

#### TrustTunnel 与 UDP 关联治理

##### 共享会话、TCP 与 UDP

- 完善 H2/H3 共享 session 的创建、等待者取消、重置、退休与空闲清理。
- 复用既有 session 时直接使用，昂贵新建按一次共享创建执行准入。
- 保持 TCP、UDP2 stream 与 association 的各自所有权，关闭时归还对应槽位和队列字节。
- 完善 UDP 关联、单 session 及全局队列预算，饱和时明确拒收而不是无限积压。

##### 修复单向上传被误判空闲

- 成功进入 UDP 队列的上行数据报刷新关联活跃时间，合法的“持续上传、对端不回包”不再被当成静默关联。
- 队列满或关闭后拒收的数据不延长关联寿命。
- 空闲扫描将快照视为候选，在实际关闭前使用同一截止条件复核，避免扫描与新包到达交错时误关。
- Close、过期与 session 关闭共用单次清理路径，防止重复释放关联和字节额度。

##### 真实读超时与分段归因

- 为服务端 UDP flow 实现真实读 deadline，支持设置、到期、更新与清空。
- `SetDeadline` 按能力应用读半边，使 DNS、嗅探等调用者获得实际的读等待上界。
- 分开统计队列交付、关闭排空、拒收、丢弃以及回复写失败，改善异常归因。
- 明确区分“已交给 handler”与“回复已经成功写出”，避免把内部交付直接当作端到端成功。
- 增加 H2/H3 方向级黑洞、恢复、满队列和取消的故障测试，并加强真实载荷完整性校验。

#### Hysteria2、TUIC 与 QUIC 共享创建

- 为 Hysteria2 / TUIC 的首个会话建立增加双地址族竞速，改善一族握手静默无响应时的恢复路径。
- 清理落败分支时同时核对 client 身份与代次，防止上一轮回调摘掉新一轮客户端。
- 精确区分已消费的失败结果与仍未读取的分支，避免重复等待或遗漏清理。
- 迟到赢家在发布前重新核代次，过期结果不会成为当前会话。
- QUIC 共享创建以真正的创建操作为单位接入资源准入，多请求等待同一次创建时不会重复计费。
- TUIC 认证写入纳入创建完成边界，认证失败按创建失败结算，再决定是否发布会话。
- 单个等待者取消不会撤销其他请求仍需要的共享创建，也不会提前归还创建许可。

#### HTTP/3 → HTTP/2 回退与请求安全

##### 通用 HTTP 请求

- 为冷 HTTP/3 建立设置独立尝试预算，避免耗尽整个请求时间后才进入 HTTP/2 路径。
- 改善并发请求对尚在进行的 HTTP/3 建连的等待，减少无关请求之间的阻塞。
- 按 authority 隔离 H3 判断，避免一个目标的失败影响其他目标。
- 区分传输建立失败、对端明确拒绝和已经收到响应，避免将所有错误统一视为 H3 不可用。
- 完善请求体可重放性、非幂等请求和显式拒绝的处理，按安全边界决定能否重试。
- 修复竞速胜者在响应头返回后上传体被过早取消的问题。
- 调用者放弃请求时结束对应上传，及时关闭落败分支和请求体副本。
- 改善冷连接、暖连接、同时完成和明确拒绝等不同场景的状态归属。

##### HTTP CONNECT 与 CONNECT-IP

- 发送 HTTP/3 CONNECT 前检查对端扩展 CONNECT 能力。
- 显式 `h3-only` 配置保留自身语义，回退能力按实际 ALPN 和传输条件确定。
- 收到对端 CONNECT 答复时保留该事实，不因建立窗口结束将其改写成纯传输失败。
- 改善慢 CONNECT-IP 响应与后续尝试的协调，使晚到的有效状态能够被正确处理。
- 不再持有 HTTP/2 池锁执行 TLS 拨号；等待拨号名额时支持调用者取消。
- 完善会话发布、响应读取与连接关闭之间的锁和所有权关系。

#### NaiveProxy / Cronet 集成

- 为 HTTP/2 回退使用独立、禁用 QUIC 的 Cronet Engine，明确主路径与回退路径的行为。
- 改善 Chromium 自身连接计时器触发后的回退判断，并保留代理端真实答复。
- 完善回退 Engine 的初始化、取消与关闭，关闭后不再接纳新的创建。
- 在 native 实际发起 socket 创建的接点接入准入与观测，区分新建、复用和原生层活动。
- 记录 native 绑定代次、引擎版本和回调能力，便于将运行结果关联到实际加载的库。
- DNS bridge 失败时返回明确的失败响应，避免向原生引擎交付虚假的成功答案。
- 补充真实 DLL 路径上的业务、回退、取消及生命周期验证。
- 原生引擎继续采用本项目既有 Chromium 154.0.8037.49 / NaiveProxy patch5 构建代次；本版的重点是消费接线、回退、准入和来源验证。

#### Mieru 连接与 SDK 生命周期

- 对单传输与回退路径统一核对运行时代次，网络变化后不返回属于旧运行时的连接。
- 改善在途拨号与关闭顺序，减少 SDK 等待与上层退出之间的相互拖延。
- 在实际使用的 SDK fork 中修复向已关闭或正在拆除的 underlay 挂接新 session 的竞态。
- 将会话附着、状态推进和相关循环启动纳入同一关闭互斥边界，使关闭能够看到完整已附着会话。
- 关闭先发生时，新的附着被明确拒绝；附着先完成时，关闭负责清理相应会话。
- 保持 TCP / UDP underlay、SDK 复用与现有回退错误分类的区别，不用盲目重试掩盖所有权问题。
- 补充关闭后附着、开放 underlay 正常使用、attach/teardown 交错与真实消费者回归。

#### MASQUE、WireGuard / AWG 与按需隧道

##### MASQUE CONNECT-IP

- 为失败创建和重复注册等路径补齐 endpoint 关闭归属，及时清理未被接纳的资源。
- 修复旧 CONNECT-IP session 退休后，内层连接与流仍被保留的问题。
- 将 session、用户态网络栈和内层流的回收边界关联起来，使旧资源可以完成释放。
- 保持 CONNECT-UDP/IP、PREF64、MTU/PTB、双栈和 owned DATAGRAM 的既有语义。
- 补充饱和单流与共享控制面的隔离测试，验证慢数据流不会阻止控制操作前进。

##### WireGuard / AWG 并发与唤醒

- 修复并发流在等待 outbound 空间时可能错过唤醒并持续停滞的问题。
- 额度递减、设备启动和等待注册的先后关系统一处理，避免队列已经腾空但写入者仍睡眠。
- 覆盖同时起流、错开起流、双进程真实载荷以及 WG/AWG 两种路径。
- 完善 Device、Peer、Bind、Endpoint 与用户态栈之间的关闭和暂停状态传递。
- 补充 AWG 重放、cookie challenge 压力、合法业务与未授权流量隔离，以及反向应用载荷验证。

##### 活跃按需隧道保护

- 设备暂停判断纳入真实业务活动，保护仍承载流量的 on-demand 隧道。
- 动态出站图变化能够刷新资格判断，减少 Provider 或组成员更新后使用陈旧引用信息。
- 区分空闲池裁剪、停止接收新工作、旧代排空与设备暂停，按不同资源合同处理。

### 三、内存、资源管理与生命周期整合

本版把资源管理贯穿到创建、传输、更新和回收几个阶段：昂贵连接在真正创建时准入；传输中的缓冲按字节或增长许可管理；订阅和规则构建控制瞬时并发；旧对象按照实际使用关系退出；回收动作补充收益反馈。

这些调整共同面向多节点、多协议、慢连接和集中更新叠加时的资源压力。设计上区分正在创建的资源、正在使用的资源和等待释放的资源，也区分业务数据、控制工作与短流所需的空间，尽量减少一个繁忙环节挤占其他环节的进展。具体预算及适用范围见下文。

#### 内存优化全貌：从分配之前到最后一个引用释放

本轮内存工作的重点，是在现有 OOM 与 ResourceGuard 基础上，把控制前移到资源增长发生的位置，并补齐数据离开队列、连接退役和任务取消后的释放过程。它涉及的不只是 Go 堆：网络会话、原生连接、goroutine、等待记录和文件映射也各有生命周期。

| 优化方向 | 本版采取的措施 | 控制的主要占用 |
|---|---|---|
| 减少重复创建 | 共享会话创建、等价测速与 DNS 查询合并，复用路径不重复领取创建许可 | 握手状态、socket、临时对象和后台任务 |
| 限制聚合缓冲 | Box 共享 XHTTP 上传预算；TUN/NAT 分层字节上限 | 多流、多节点同时积压的数据 |
| 控制实际分配 | XHTTP 同时记录逻辑字节与底层数组容量，先准入再分配 | 小切片背后的大数组、等待中的空数组、新旧数组重叠 |
| 按需使用大缓冲 | relay 大缓冲增长许可随压力收紧 | 并发长流持续保留的扩容缓冲 |
| 完整归还额度 | 出队、失败、取消、detach、Close 和最后引用释放分别结算 | 已无用途却仍占用的缓冲和预算 |
| 管理新旧代重叠 | 池停止旧代借出、迟到结果清理、实际终结后结账 | 换网与更新期间同时驻留的旧会话、新会话 |
| 降低更新峰值 | Provider/规则共享构建准入，流式落盘、校验与发布 | 下载正文、解析中间对象和新旧规则重叠 |
| 控制后台与观测开销 | 固定 worker、有限排队描述、历史字节预算、分页与有界事件 | goroutine、排队记录、诊断结果和客户端快照 |
| 反馈回收效果 | 记录回收层级、前后 footprint、低收益与冷却 | 无效重复回收及其额外运行成本 |

这些机制按各自所有者协同：XHTTP 聚合预算属于 Box，relay 增长预算属于 ConnectionManager，NAT 队列和 Memory TUN 各自管理其字节账。预算约束的是明确可追踪的资源，不把这些局部额度简单相加成进程物理内存上限。

#### 资源准入、内存预算与跨流背压

本版把“限制连接创建”“限制排队字节”“限制大缓冲驻留”“回收已有资源”分开治理。它们共用既有压力信号，但不混用计数：一个创建许可不是一条存活连接，一个已出队 POST 也不代表其数组已经释放。

##### 让许可对应真正的创建

- 修复连接准入票据在成功登记后可能再次扣减额度的问题，票据按单次结算处理。
- AnyTLS 先尝试复用健康 session，仅实际创建新 session 时进入创建准入。
- 为 QUIC 共享创建接入对应 hook，避免每个等待者或每条复用 stream 重复计费。
- 保留嵌套 TLS、detour 和多跳拨号中的单次许可语义，避免内外层互相等待。
- 快速失败计时从取得许可之后开始，不把排队时间误当成底层创建耗时。
- 快速失败退避按目的地作用域组织，减少坏节点对无关健康目标的影响。
- 成功、失败、取消与拒绝分别记录，permit 在相应创建边界结算。

##### 字节预算与数据转发

- 完善 XHTTP 跨流共享字节预算以及 relay 缓冲增长额度，减少多个流各自扩张产生的叠加压力。
- TUN、UDP NAT 和设备队列分别维护适用的局部及共享预算。
- 排队、取消和关闭时按真实 waiter 身份处理唤醒与清理，防止释放错对象或遗留等待者。
- 控制帧、关闭和取消保留前进路径，避免被大数据流占满预算后卡住。
- 保持 buffer geometry、批处理和相关能力接口透传，资源统计不会通过隐藏底层接口改变数据路径。

###### XHTTP：从单流上限到 Box 共享预算

原来每条 packet-up 流可以守住自己的上限，但同一 Box 中多个流、多个 XHTTP 出站同时上传时，总保留量仍会叠加。本版由 Box 在构造上下文安装 UploadBudgetProvider，第一个 XHTTP client 确定共享池尺寸，后续 client 使用同一个聚合预算，单流上限继续有效。

共享预算分为三个用途明确的池：

| 池 | 谁可以使用 | 解决的问题 |
|---|---|---|
| Main | 常规批量上传 | 控制所有流合计的数据在途量 |
| Control | 新流开场必须交付的字节，且有每流门槛 | 大流占满主池后，新流仍能把握手/首包送到服务端 |
| Reserve | 满足条件的短流数据 | 短请求不必一直排在连续 bulk POST 后面 |

- 超大 `Write` 按剩余额度分块接纳，不再因队列为空就一次吞入整个大写入；调用仍按原合同交付完整字节。
- pending 与已经交给 POST 的 in-flight 同时记账，字节从队列移出不会提前归还。
- 数组按完整 `cap` 单独计费；pending 后缀与 in-flight 共用同一底层数组时不重复算，最后引用离开才归还分配额度。
- 数组扩容期将新旧重叠记入峰值，避免只统计新数组而漏掉仍被 POST 持有的旧数组。
- 逻辑入场与数组申请协调，bulk 等待者不先占住大量空数组；首包分配保留与普通数据分配区分开。
- 分配不足时进入等待，不通过反复申请/退还逻辑额度忙等；取消、失败和 Close 都能唤醒并结清。
- 诊断分别提供逻辑使用量、受控数组容量、峰值、等待者和累计归还，使池的约束能够从实际数据核对。

一个确定性容量反例中，逻辑负载仍为 **65,536 B**，但底层数组实际占用至少 **81,920 B**。新分配账会正确记录 **81,920 B**，而不是把切片后缀容量当作整个数组；这项改进解决的是实际保留的可见性和约束，不只是给队列加一个长度限制。

###### Relay：只限制缓冲增长，不拒绝业务复制

`ConnectionManager` 在 Start 时安装 GrowthBudget。上传与下载是两个独立 copy 方向，每个方向分别申请增长许可，并持有到该方向的复制 goroutine 返回。

| 压力等级 | 普通可增长方向 | 交互/实时额外保留 | 新请求行为 |
|---|---:|---:|---|
| Normal | 256 | 16 | 获得许可后按既有阈值增长 |
| Armed | 128 | 8 | 收紧新的缓冲增长 |
| Critical | 0 | 0 | 普通可控方向继续复制，保持小缓冲 |

这些是当前代码的策略默认值，计数对象是 **copy 方向**，不是 TCP 连接数，也不是进程 RSS 上限。

- 无增长许可时不排队等内存政策，直接使用小缓冲继续传输，避免把资源保护变成额外的业务等待。
- 许可不按计时器到期：缓冲增长后仍会一直使用，直到真实 copy 结束才归还计数。
- 升压只限制新的增长，不凭空撤销已经占用的额度；已有方向随结束自然退出。
- 协议 framing 或 buffer geometry 明确要求立即增长的路径保留能力豁免，单独记诊断，避免改变 padding 和底层复制语义。
- 半关闭只结清已经结束的方向，另一方向继续持有自己的许可。

###### TUN / UDP：预算与唤醒一起修

本版保留并整合 per-session、NAT-wide、device-wide 字节边界，同时修复“有额度却没人再被唤醒”的等待协议。

- 等待需求归属于真实连接，迁移到新队列前撤销旧记录，避免旧 owner 删除新队列写者。
- 额度释放发生在登记前后不同窗口时，通过先发布 interest、再复查额度关闭丢唤醒窗口。
- 按活等待者的最小实际包长判断可写性：释放的空间能容纳小包时，小写者可前进，大写者继续等待。
- 不再因不同等待尺寸达到一个固定集合上限而清掉仍活跃的写者。
- 队列关闭遍历全部等待记录，清理每个连接的归属；取消与反复失败不积累幽灵需求。
- 已经移交的数据包、被拒绝的剩余数据和关闭 drain 使用各自的释放路径，避免双重归还或丢失所有权。

##### 构建预算与三类资源账

- 远程 Provider 和远程规则集的构建准入接入既有压力来源，根据状态调整新建额度。
- 区分当前准入上限、实际在途构建和已经观察到的重叠峰值。
- 压力升高后，如已有在途量大于新上限，预算仍如实反映实际重叠，而不是把旧工作当作已经消失。
- 构建成功、失败与放弃分别结算，失败和取消归还名额。
- 资源驻留按对象身份、owner、单位与代次组织，区分 session、connection、generation 和 device。
- 同时呈现创建准入、排队/字节和实际驻留，避免把“已经从池列表删除”误当成“已经释放”。

##### OOM：从触发一次回收到解释每层做了什么

沿用现有单压力源和单回收执行器，本版补上真实收益反馈与跨 owner 资源记录，不额外启动第二套采样器或回收循环。

- L1 清理过期缓存、L2 释放空闲资源、L3 在必要时执行 runtime 回收；层间仍依据实际状态和收益决定是否继续。
- 新增每次回收前后 footprint、归因收益、执行层、原因、低收益判断和冷却截止时间。
- L1 保留能力接口返回的真实删除条目数，区分“删了 0 条”和“该层不提供计数”。
- 普通 idle、系统 pressure 与 crisis 分开记录；report-only 与真正 network-reset 不再在报告中混淆。
- 最近动作使用固定 **8 条环形记录**，同时保留累计次数，避免诊断历史随会话无限增长。
- 低收益路径维持 **30 秒冷却**，避免稳定占用下频繁重复遍历和强制回收；收益门槛仍沿用既有策略。
- creation、queue、build、object residency 与 reclaim 文档一起解释压力，既看正在创建什么，也看旧代为什么还驻留、上次回收做了哪一层。

footprint 前后差是该动作获得的观测收益，不把它伪装成逐对象精确释放量；session、connection、generation、device 等单位也不合成一个没有意义的总连接数。

#### TUN / NAT 的存量控制：同时约束单会话和聚合占用

TUN 与 UDP 的改动将预算落到实际排队的数据上，避免每条会话单独合规、聚合后仍占用大量内存。

- **UDP NAT 两层字节预算**：依赖实现的默认值为每会话 256 KiB、NAT 聚合 8 MiB；入队前预留，读取、丢弃和 teardown 后归还。它约束排队数据，独立于连接创建许可。
- **Memory TUN 设备级预算**：默认 8 MiB 的排队字节额度覆盖入站环和该设备的出站队列，多条队列共享同一设备容量。
- **快速路径同样结账**：修复单包 read waiter、批量读取后续分支的额度归还，避免普通读路径正常、快速读路径却持续占住预算。
- **按传输语义处理压力**：适用的 TCP 写入在字节不足时反压等待；UDP 保留明确的丢弃处理和计数，避免过载继续扩大缓存。
- **释放空间能让其他队列前进**：入站读取、另一出站队列排空或关闭，都能通知受同一设备额度限制的写者。
- **关闭按实际所有权释放**：设备关闭只归还设备自己持有的数据；调用者队列持有的存量由该队列释放，避免账面提前归零。栈 detach 逐帧归还，重新 attach 后能够正常使用额度。

这些默认值属于依赖中相应队列实现，不代表所有平台 TUN 模式或整个进程统一采用一个 8 MiB 内存上限。

#### 等待与取消也是内存优化的一部分

预算用尽后，等待者本身仍会占用对象、引用和 goroutine。本轮同时处理等待过程的空间与退出方式。

- XHTTP 在获得逻辑额度之前不为大批等待写者分配数据数组，减少“尚未获准发送，却已经占住内存”的情况。
- 分配额度不足时等待真实资源变化，修复额度反复领取、归还、唤醒自己的忙等，减少高压下的无效操作。
- sing-tun 将等待需求放入连接自己的记录，同一写者反复失败更新原记录；成功、取消、迁移和关闭撤销对应记录，减少幽灵需求和失效引用。
- 测速轮的公平排队描述设置固定上限；超出者等待状态变化，避免每轮继续向描述队列追加对象。该上限针对排队描述，不等同于所有外部调用者或 goroutine 的总量上限。
- 数据报嗅探在底层无法提供有效读期限时结束补充读取并回放已有数据，避免首包和相关流状态被无界读取长期占住。

#### 共享工作与闲置资源：减少重复常驻的成本

内存占用还取决于同时存在多少份相同工作，以及完成后保留多久。

**共享创建与复用。** 在具有共享创建机制的协议中，多个等待者加入同一次创建；AnyTLS 将准入放到新 session 创建处，使复用已有 session 与新建资源分开计费。一个等待者退出时，仍有其他等待者需要的工作可以继续；迟到结果则由对应 owner 接纳或释放。

**共享探测与查询。** URLTest 对符合相同条件的测量共享真实工作，DNS 对符合共享键和策略的查询复用进行中的请求，减少重复连接、请求状态和结果构造。每个调用者仍保留自己的取消与结果检查边界。

**减少不必要的后台活动。** 显式省电 failover 在稳定健康状态下集中探测稳定槽位，退化后再扩大候选；进程查询按需获取路径，并合并同 UID 工作。这些改动减少重复工作所需的临时分配和系统调用。

**闲置与活跃分开。** 按需隧道通过实际 payload 活动判断适用的活跃豁免，豁免随活动窗口到期重新裁决；空闲资源仍可进入原有回收流程。既避免刚有业务就反复销毁重建，也不让一次历史活动永久阻止回收。

#### 换网与更新时的内存：旧对象要真正退出

网络切换、节点更新和并发地址族竞争都会短暂产生多份资源。本轮将这种重叠纳入生命周期处理。

- HTTP/2、gRPC-lite、XHTTP 等池在取用处检查代次，旧代停止接受新工作，已有使用者按协议合同排空。
- 退休对象仍计为 draining；最后使用者返回、实际 transport 释放后才计为 terminated，保留对真实驻留的观察。
- HY2/TUIC 双栈竞争按轮次和 client 实例清理失败、落败及迟到结果；已消费的失败结果及时结算，旧轮清理不再误删新一代对象。
- Mieru SDK 将 underlay 附着与关闭状态协调，避免把新 session 挂到正在退出的承载；关闭和取消路径由实际 owner 负责资源收尾。
- 驻留统计按 session、connection、generation、stream、device 分开，帮助判断占用来自会话数量、活动流还是旧代排空。

这里的优化同时包含真实释放修复和诊断改进：统计本身不释放内存，但能够准确显示何时仍持有对象，支持现有压力与回收机制作判断。

#### 规则和订阅：降低大对象构建与替换时的峰值

下载与更新往往同时持有旧规则、新正文和新解析结果。本轮将这一阶段作为独立资源负载管理。

- 下载正文流式写入临时产物，同时计算摘要和大小，先校验再发布，减少为了哈希和发布而完整保留一份正文的需要。
- Provider 与远程规则构建共用准入机制，压力升高时收紧后续工作；已有构建继续按真实寿命计账，避免低报新旧对象同时存在的峰值。
- 产物用租约维持读取生命周期；更新后的新读者使用新版本，旧读者结束后才允许对应资源清理，协调映射读取和回收。
- 大型关键词集合采用不可变编译匹配结构，查询热路径保持零分配；构建规模设边界，适用的小集合与超界输入保留原有匹配路径。
- 规则产物使用兼容信息与精确引用管理，减少恢复过程重新寻找、下载或混配不对应版本资源的需要。

已有专项样本提供了具体量级：20,000 个合成域名的 JSON 加载/构建累计分配为 **52,041,328 B**，对应 SRS 加载为 **724,496 B**；带 4 MiB 尾部的流式加载样本分配为 **279,712 B**。这些分别说明格式和加载路径的成本差异，不是全系统常驻内存降幅，也不代表所有规则文件具有相同比例。

#### 诊断与事件本身也限制占用

本版新增观测能力时，同步约束其存储与返回成本，避免排查内存问题的功能又成为新的长期占用来源。

- DNS 历史同时按条数、保留字节和 TTL 管理，计费覆盖事件中的可变长内容，避免很长的域名或规则描述绕过仅按条数设置的限制。
- 查询结果使用分页和输出预算；客户端获取历史不必一次复制全部记录。
- 主动诊断 emitter 默认队列 64、历史保留 128 条、历史字节预算 32 KiB，按条数与字节共同淘汰。
- 连接事件使用共享扇出和订阅侧丢失记录，消费者通过快照重同步；快照分片，减少全量结果一次性复制。
- OOM 动作记录采用固定 8 条环形历史；关闭 watchdog 使用有预算的堆栈采样和阶段记录。

这些额度约束对应队列、历史或报告，不将单个组件的预算表述为所有诊断调用的全局内存上限。

### 四、网络切换、双栈与连接恢复

网络变化会同时影响 DNS 答案、地址候选、正在建立的连接和已经缓存的会话。本版围绕这些状态的共同变化进行调整：双栈候选更及时地参与连接，迟到结果核对当前网络归属，连接池借出和发布检查代次，协议重置与永久关闭分开处理。已有活跃流、后续新请求及旧连接清理各自遵循对应的生命周期。

#### 网络切换、连接池与跨代结果处理

##### 新连接使用当前网络状态

- 在协议实际接收新拨号、借出池会话和发布创建结果的位置接入网络代次检查。
- 改善网络变化已经开始、接口监听尚未完成期间的新请求处理，减少继续使用切换前会话的情况。
- 区分网络代次、连接池代次和连接管理器的排空代次，按各自负责的资源边界执行更新。
- 创建跨过网络变化时，在结果发布前重新核对归属；迟到成功由相应所有者接纳或关闭。
- 改善并发 reset、关闭、空闲清理与新请求之间的协调，避免旧清理误操作新安装的对象。

##### HTTP、gRPC-lite 与 XHTTP 池代次

- 完善 HTTP/2 连接池在取用点的网络代次识别。
- 将代次池生命周期接入 gRPC-lite 与 XHTTP 的实际请求路径。
- 网络变化后，旧代不再承接新的工作；仍承载活动业务的资源按排空合同处理。
- 在响应或连接发布点拦截跨代迟到结果，防止旧请求把资源重新放回当前状态。
- 完善活跃、闲置、退休代和关闭状态的统计，使池列表与实际驻留不再混为一谈。
- 修复 XHTTP 池在关闭后仍可能创建新连接的边界。

##### VLESS / VMess / Trojan：换网重置不再等同于永久关闭

- 三类出站接入 BeforeDial / AfterDial 的代次检查，TCP 与 ListenPacket 入口都在取用承载前核对网络状态。
- 网络变化时，优先使用 transport 的 `ResetContext` / `Reset`；终止对象寿命的 `Close` 不再被当作普通重建操作使用。
- 这样可以重置当前连接和 pending 创建，同时保留 client 后续重新创建承载的能力，避免换一次网后所有后续请求继续失败。
- 支持 Context 的 reset 使用监听阶段的同一预算，取消正在验证的迁移候选，不让一个协议无限拖住整个切换。
- mux 按自身生命周期 reset；transport owner 提供实际驻留信息，协议壳不伪造底层连接数。
- REALITY fallback 明确交接 socket 所有权，避免继续使用调用者已经关闭的连接；Vision 继续保留 direct-write 与实际底层连接能力。

##### QUIC 活跃流迁移

- 完善 V2Ray QUIC 的路径变化处理与迁移候选 socket 创建方式。
- 使用适合路径验证的未连接 UDP socket，避免候选路径被旧目标绑定方式限制。
- 区分调用者取消、父上下文结束与迁移尝试自身预算耗尽，改善失败归因。
- 补充活跃业务流、第二条 socket、共享连接上的其他流及关闭交错测试。

#### IPv4 / IPv6、DNS 来源与拨号恢复

##### 流式地址候选与双栈调度

- 改善逐批到达的 A/AAAA 答案调度，不再简单按整族追加，使健康的另一族有机会更早进入拨号。
- 保持 `prefer_ipv4`、`prefer_ipv6`、严格单族与默认策略的区别，禁止族不会被恢复逻辑重新引入。
- 快速失败时及时推进候选；静默黑洞与仍在查询的另一族分别处理。
- 地址持续到达时保留原有尝试顺序，已启动候选不因后续结果反复重排。
- 对重复地址和 IPv4-mapped 地址作规范化处理，减少重复起拨。
- selector、loadbalance 与嵌套组向下传递完整候选列表，避免每个候选重新选择一个节点。

##### 字面 IP 与嗅探域名恢复

- 原始目标为 IP 时，保留立即尝试原地址的路径，不为等待慢 DNS 增加不必要延迟。
- 根据现有恢复条件使用可信域名补充候选，首批重复地址或空答案不再过早结束恢复来源。
- 支持同族多批答案和迟到异族答案继续进入仍存活的竞速。
- 取消与关闭时清理候选生产者及落败连接，保持唯一赢家的所有权。

##### DNS 环境与网络命名空间

- 为答案记录其解析操作实际捕获的环境来源，缓存命中、禁用缓存和逐族查询使用一致的来源语义。
- 当前环境重新确认的候选可以更新排队中的地址状态；较旧环境的候选按策略降序处理。
- DNS-only 变化与 transport reset 保持独立，避免仅 DNS 配置变化就重建全部承载。
- 修复共享查询元数据在并发地址族操作中的相互影响。
- 改善显式地址族、DNS 路由策略与默认策略的优先级。
- 修复用宿主网络采样直接拒绝网络命名空间内拨号的问题，按实际执行环境处理能力判断。
- 降低拨号侧读取 DNS 代次信息的成本，避免把只读查询变成额外采样路径。

### 五、策略选择、DNS 与规则处理

本版同时扩展路由表达能力与大规模配置的运行效率。`match_only` 让解析结果用于规则判断而保留原拨号目标语义；`hidden` 将策略组的展示与运行关系分开；URLTest 引入显式省电 failover，并共享等价探测工作；大型关键词集合使用编译匹配，规则文件通过流式产物管理减少加载与更新过程的内存负担。

这些变化服务于同一个使用场景：节点和规则越来越多时，仍能清楚地组织策略、及时选择可用路径，并控制后台探测和规则处理的成本。

#### 新增 `resolve.match_only`：解析用于匹配，拨号保留原有目标语义

新增 `resolve` 路由动作的 `match_only` 选项，将“为了匹配规则而解析域名”与“将解析结果用于连接目标”分开。

- `match_only: true` 时，DNS 解析结果进入独立的匹配地址通道，可供后续目的 IP 条件和规则集作判断。
- 本次匹配解析不会直接把新答案写入拨号候选，支持域名寻址的代理出站可以继续按原有目标语义处理域名。
- 省略该字段或设为 `false` 时，保持普通 `resolve` 的既有行为，包括地址候选构造、策略与双栈处理。
- 对此前由其他合法动作生成的拨号候选，保持独立的生命周期；不会为了启用匹配解析就无条件清空。
- 完善连续执行普通 `resolve` 与 `match_only` 的顺序语义，使后续匹配与拨号分别使用对应的地址证据。
- 覆盖普通路由匹配、TUN 预匹配、FakeIP、UoT 每数据报目标及相关上下文传递。
- 目标地址被改写时，清除属于旧目标的匹配地址和拨号候选，避免新目标继续命中旧域名的解析结果。
- 同名确认、仅端口变化等不改变地址身份的操作，保留仍然有效的地址证据。
- 目的地址匹配、源地址匹配与 DNS 响应地址匹配保持各自的数据来源。
- 同步配置 schema、动作输出、配置说明和专项回归测试。

例如，希望用本地解析结果决定规则，同时让代理继续按域名寻址，可在现有配置中使用以下规则片段：

```json
{
  "route": {
    "rules": [
      {
        "action": "resolve",
        "match_only": true
      },
      {
        "ip_cidr": ["203.0.113.0/24"],
        "outbound": "proxy"
      }
    ]
  }
}
```

示例中的 `proxy` 需对应现有出站，DNS 使用现有配置。这个选项不禁止 DNS 查询，也不会把原本只有 IP 的连接自动变成域名连接；最终寻址方式仍由实际目标、后续动作和出站能力共同决定。

#### 新增策略组 `hidden`：简化展示，保留完整运行关系

为 `selector`、`urltest` 和 `loadbalance` 增加可选的 `hidden` 展示属性，便于将地区池、中间策略组等组织节点隐藏在顶层列表之外。

- `hidden: true` 时，仅从内核生成的顶层组展示列表中省略该组。
- 组本身继续创建、启动并参与路由，仍可作为父组成员、规则目标和 `detour`。
- 隐藏组的成员关系、已选节点、测速、健康状态、选择缓存与展开状态保持有效。
- 父组仍可列出并选择隐藏的子组，支持多层嵌套组织。
- URLTest 的展示标记保存在外层对象，Provider 发布替换内部成员时保持该属性。
- 省略或设为 `false` 时保持可见；单节点组和可持续空组不因成员数量被隐式隐藏。
- 使用可选能力接口，保持现有组接口和第三方实现的兼容性。
- 过滤集中在组订阅载荷的构造处，不进入正常数据转发路径。
- 此属性作用于约定的内核组展示通道；Clash API 和第三方 Dashboard 保持各自展示行为。

以下配置中，顶层可展示 `MAIN`，而 `REGION` 继续作为可选择、可路由的中间组工作：

```json
{
  "outbounds": [
    {
      "type": "selector",
      "tag": "MAIN",
      "outbounds": ["REGION"]
    },
    {
      "type": "selector",
      "tag": "REGION",
      "outbounds": ["LEAF"],
      "hidden": true
    },
    {
      "type": "direct",
      "tag": "LEAF"
    }
  ]
}
```

#### URLTest、共享测速与大规模节点调度

##### 新增显式省电 failover 策略

URLTest 新增可选 `failover` 块。省略时维持每轮全成员探测；显式启用后，健康稳定阶段只探测稳定成员，连续退化再扩大候选集，连续恢复后收窄。

- `widen_after`：稳定成员连续失败或缺失达到阈值后扩大探测。
- `settle_after`：挑战者持续超过 tolerance 后才替换稳定成员，避免一次低延迟样本就切组。
- `recover_after`：连续健康轮次满足条件后收窄探测集。
- `tolerance`：与稳定性判断配合，减少节点延迟轻微波动时的来回切换。
- Provider 同 tag 替换保留逻辑稳定槽位，健康证据仍按实际发布和来源重新核验。

例如，在已有 URLTest 对象中加入 `"failover": {}` 使用该策略默认值；需要明确调节时再填写上述字段。这项优化减少的是稳定周期的重复探测工作，不通过把未测成员伪报为测量成功实现。

##### 共享工作与大规模调度

- 在真实 Box 中接入共享测量，对符合等价条件的探测复用同一次工作。
- 共享任务不绑定第一个等待者的寿命；单个调用者退出不会取消其他调用者仍需要的测量。
- 统一手动与后台测速的调度入口，支持交互优先以及后台等待老化。
- 在创建 worker 和轮内任务队列之前取得轮预算，控制同时生产探测任务的轮数。
- 为轮等待描述增加上限，超出描述队列的需求进入可取消背压路径，并单独记录 overflow。
- 保留大组完整测量语义，不通过静默丢弃节点来满足队列限制。
- 测量等待与实际探测计时分开，调用者整体 deadline 继续生效。
- 探测结果在提交时重新核对网络、节点和轮次归属，迟到旧结果不能覆盖较新的健康证据。
- 改善稳定成员、退化扩大探测、连续恢复与故障切换的协调，减少单次失败引发的抖动。
- 补充 2,000 / 10,000 节点、并发轮、取消、替换、恢复及结束排空测试。

#### 大型关键词规则：编译匹配与流式版本产物

##### `domain_keyword` 接入 Aho–Corasick 自动机

此前大型关键词集合需要为每个请求逐个执行 `strings.Contains`，未命中或命中靠后关键词时会扫描大量 pattern。本版对适用的大集合在构建时编译，运行期在冻结的平坦 node/edge 表上查找。

- 当前源码阈值为 **64 个关键词**；小集合继续使用开销更低的顺序循环。
- 编译上限为 **4,096 个 pattern、1 MiB 输入、65,536 个节点**，超出任一边界自动退回原匹配方式，不丢规则。
- host 按既有语义处理大小写，keyword 保持字节语义；空 host、空 keyword、非 ASCII 和异常字节由差分测试固定。
- 普通路由、DNS 与 headless/rule-set 消费既有 DomainKeywordItem，不另建一套路由权威。
- 编译结果不可变，更新通过新规则版本发布，不在每次匹配时重新构建。

本轮 1,024 关键词的主机微基准中：

| 匹配场景 | 原循环 | 编译匹配 | 分配 |
|---|---:|---:|---|
| 全部未命中 | 13.1–14.5 µs/op | 188–243 ns/op | 查询路径保持零分配 |
| 命中靠后关键词 | 15.1–15.3 µs/op | 187–189 ns/op | 两种方式均为 0 allocs/op |

这是大关键词集合的匹配算子加速，作用点明确；不是整条代理链路的吞吐倍数。小集合与早命中仍使用适合自身成本的路径。

##### 规则下载不再先完整堆内存再发布

- `Stage → Validate → Publish`：响应流写入临时文件的同时计算摘要和长度，校验通过才 rename 发布并取得租约。
- binary SRS 从 staged 文件流式解析，避免纯 artifact 路径先对整响应 `ReadAll`。
- manifest 记录 schema、compiler、policy、ABI、platform、依赖、长度与 digest；加载校验产物身份与字节。
- 旧版本读者持 lease，Collect 不会删掉仍被 mmap/流式读取的内容；崩溃临时项按生命周期回收。
- configtx 组合提交把正文与精确依赖摘要绑定，规则和 Provider 不因同路径文件已经变更而在恢复时串版本。

专项用例给有效规则附加 **4 MiB** 尾随数据，完整 load 的累计分配为 **279,712 B**，同时验证规则生效及临时目录清理。这证明响应不再作为同尺寸完整下载副本常驻堆内存。

同一组 **20,000 域名的合成规则**中，已有 SRS 与源 JSON 的加载对照为：

| 指标 | Source JSON | Binary SRS |
|---|---:|---:|
| 编码大小 | 580,036 B | 435 B |
| load + compile 中位数 | 152.01 ms | 994.2 µs |
| 加载累计分配 | 52,041,328 B | 724,496 B |

这组语料压缩率很高，数值描述的是该样例与两种已有格式的加载成本；本版把版本产物、租约、校验和恢复接到真实消费者上，不把样例压缩率外推成所有规则文件的比例。

### 六、配置、订阅更新与版本恢复

配置更新涉及内存中的运行状态、磁盘上的配置、Provider 节点和规则文件。本版围绕这一整组内容完善分析、变更计划、逐项应用、产物发布与恢复。用户能够更清楚地了解配置声明了什么、哪些部分发生变化、哪些更新实际生效；恢复时也能找到对应版本使用的规则和订阅产物。

#### 配置分析、变更预览与真实应用

##### 配置分析与能力投影

- 扩展 `ConfigAnalysis`，输出声明对象、对象类型、引用位置和引用状态。
- 引用从配置结构自身的标签提取，与 dangling findings 共用同一份投影，减少分析结果与告警互相矛盾。
- 区分 resolved、pending、dangling 和 unresolved，使尚待 Provider 发布的成员与真正缺失引用能够分别表达。
- 增加当前二进制 registry 的能力清单，反映这个构建实际注册的可构造类型。
- 输出设置容量边界，超限明确标记截断，保持大配置报告可控。

##### 内容级变更计划

- 配置变更按内容与顺序生成指纹，识别真正变化的 section，而不是只比较对象数量。
- 预览结合运行实例实际使用的 overrides，减少预览与应用使用不同配置视角的情况。
- 区分 unchanged、stale、首次启动、需要重启和可以在线更新等结果。
- 修正 Windows 与 Apple 对 `started` 的理解：它表示当前已有运行实例，而不是本次操作是否已经启动。
- 变更结果关联实际执行的 apply，报告明确指出最终留下的实例和配置修订。
- 请求在等待、读取、解析或发布前取消时保留对应边界，避免取消操作误关现有服务或继续发布新状态。

#### 配置、规则产物与故障恢复

- 将配置正文、引用关系和规则产物的提交组织为可恢复事务，减少不同版本组合被误用。
- 规则产物经过 stage 与 publish，发布时绑定实际引用；更新失败保留已经验证的版本。
- 保存 last-good 时保留精确规则集及 Provider 内容引用，恢复不依赖同路径当前文件恰好仍是旧内容。
- 支持从缓存修复缺失的已固定产物，恢复后继续提供相应规则与节点。
- 区分解析失败保留当前服务、构建失败恢复旧版本、运行成功但持久化结果不同等情况。
- 恢复结果包含实际 active revision、失败候选和 retained/recovered 等事实，便于客户端准确显示。
- 存储损坏时不伪造恢复成功，也不凭空补出一个“正在运行”的实例。
- 补充同目录重新启动、写入中断、进程退出、引用覆盖与真实 A/B/C 业务夹具。
- 改善并发 Store 与产物租约的配合，保持仍被使用版本的生命周期。

#### Provider 更新与客户端订阅消费

- 远程抓取同时继承调用者预算和 Box 生命周期，不再只受其中一侧约束。
- 在读取、解析和实际发布之间检查取消，避免请求已经结束却继续替换节点。
- 更新报告区分抓取、解析、发布及实际生效结果，减少单一成功状态掩盖不同阶段。
- 客户端区分订阅配置刷新和运行实例内的 Provider 刷新，分别处理其结果。
- 发送更新前核对配置归属与实例身份，降低配置切换后请求落到错误目标的风险。
- 失败回滚结合修订和归属判断，较晚发生的新写入不会被旧请求的失败处理覆盖。
- 复用既有发布与租约机制，让节点替换、组成员与在途使用保持一致的所有权关系。

### 七、诊断、状态反馈与客户端协作

本版扩展内核对外提供的配置分析、DNS 历史、连接事件和主动诊断，并同步调整客户端的请求与回执处理。诊断关注请求实际经过的环节；连接列表通过事件和快照恢复一致状态；切组与重载带上请求、实例或会话身份，让界面能够对应到真实执行的操作。Apple、Android、Satelite、Desktop 与 Dashboard 的相关接线也属于这一轮更新范围。

#### DNS 历史、实际路径与连接诊断

##### DNS 历史与查询事件

- 完善查询历史的管理、清空、分页和真实 RPC 消费路径。
- 按完整记录计量预算，为首条记录及绑定响应设置独立边界，避免大字段绕过容量控制。
- 为查询事件补充执行时的实际拨号路径、组选择修订及相关来源。
- 区分新建连接、池内复用和无法逐请求追踪的复用情况，不将无法观测的路径伪装成精确结果。
- 为 TCP/TLS/HTTPS 等连接池保留实际 dial 链，使复用时仍能解释连接最初如何建立。
- 嵌套 detour 记录各层实际跳转，内层 DNS 交换不会污染外层查询的路径。
- 事件解释使用执行时事实，减少事后按当前 selector 状态重算历史路径造成的偏差。

##### DNS 组共享查询与赢家提交

- DNS 组在统一请求预算内组织并发候选和救援窗口，赢家通过提交 token 发布，落败任务收到取消。
- 共享查询按问题、transport、ECS、环境和 timeout 等实际条件隔离；每个调用者继续独立执行响应接受条件。
- 调用者取消只退出自己的等待，不能抢先终止其他调用者仍需要的共享交换。
- 查询旧结果、缓存来源与策略检查在各自边界核验，避免一个调用者的结果条件污染另一调用者。
- 将本地解析/接口错误与上游响应区分，组健康和节点惩罚不再只看一个宽泛的失败字符串。

##### 主动连接诊断

- 主动诊断增加并发边界，拒绝执行时正确结束对应事件流。
- 报告实际使用的端点和路径，而非仅照抄用户请求中的声明。
- 在同一次拨号中由真实协议层记录 TLS、REALITY、stream 和 HTTP CONNECT 等阶段。
- 使用类型化信号区分取消、超时、证书、对端答复及其他错误，减少依赖错误字符串猜测。
- 协议阶段作为 dial 的子阶段进入报告与事件流，记录容量有界并明确截断。
- 预览、路径解释和诊断不推进真实组选择游标，不以排查操作改变正常流量的选择顺序。

##### 进程与包名查询

- 按实际需要补全过程和包名信息，改善进程规则与 DNS/路由匹配的配合。
- 完善 Linux socket owner、文件描述符传递和多 owner 情况的处理与测试。
- 进程缓存与查询结果按提交边界更新，失败与取消不会发布不完整状态。

#### 连接事件、快照与客户端重连

- 在事件源头组织序号与发布顺序，减少并发通知在消费者处出现倒序。
- 为连接流增加实例身份、序列和快照分片信息，区分不同运行实例的数据。
- 对订阅侧的事件丢弃增加独立计数，消费者能够识别需要重新同步的情况。
- 完善快照与增量事件之间的交接，改善断连重连和实例切换时的状态恢复。
- 补充静默尾部、分片快照、旧实例事件、订阅兼容与并发发布测试。
- 客户端恢复以收到的实例和序列为依据，避免把旧实例残留事件拼到新实例列表中。

#### 切组、重载与五端请求回执

##### 切组回读

- 为切组操作贯通 `requestId`、预期 revision 和实际应用结果。
- 返回实际应用的出站标签与修订，让客户端能够依据 readback 结算操作，而不只依赖乐观显示。
- Apple、Android、Satelite、Desktop 托盘与 Dashboard 的选择入口接入请求感知路径。
- 各组保留最新尝试，迟到旧回执和较旧修订不会覆盖新的选择状态。
- 旧 daemon 的能力通过明确结果表达，客户端不会虚构已确认的 revision。
- 同步 Desktop 内嵌 Dashboard 的协议副本、组列表和托盘菜单调用。

##### Reload 请求关联与绑定兼容

- 新增 request-aware reload RPC，保留旧接口及兼容路径。
- 为重载携带请求身份和预期实例信息，回执报告实际实例、active revision 与 stale 状态。
- Apple NE、Android 和 libbox 命令客户端消费新的关联结果。
- gomobile 参数使用能够正确绑定到 Java/Kotlin 与 Swift 的整数类型，修复方法可能被绑定器跳过的问题。
- 调整服务身份结果的 Go/移动绑定边界，同步 ABI pin、客户端 gitlink 和 source-lock。
- 增加新旧客户端与 daemon 的真实 gRPC 组合测试，覆盖能力回退、乱序、读回和请求身份。

#### Apple、Android、Satelite 与 Desktop 客户端

##### Windows TLS 与跨平台接口

- Windows Schannel 根据系统能力建立可用 TLS 版本范围，保留 TLS 1.2，并在握手后核对实际协商版本是否满足配置。
- 版本能力、用户要求和最终协商结果分别检查，避免系统不支持的版本被配置表面接受。
- 移动绑定区分 Go 内部身份对象与可跨 ABI 的参数/结果，显式核对 Kotlin/Java 与 Swift 的实际可见方法。
- 错误桥接覆盖 UTF-8、typed-nil 和原始错误链，减少“Go 侧有错误，移动侧却看不到”的边界歧义。

##### Apple App / Network Extension

- 配置正文写入增加跨进程目录锁、持久 revision sidecar、commit journal 与原子提交。
- App 与 Intents 更新同一 Profile 时，通过共同持久化边界协调，避免只依赖单进程锁。
- 写入中断后按 journal 和 revision 恢复，防止无法归属的正文被误认为较新的有效版本。
- NE 请求增加发送前声明、扩展端准入与回执校验，明确同一 Profile 下不同 tunnel session 的身份。
- 扩展在处理配置内容之前识别 session，降低旧扩展/旧会话请求串入新连接的风险。
- 配置预览判定下沉到可测试的模型层，统一首次启动、在线应用、重启与陈旧计划的表达。
- 组列表与 macOS 状态栏使用同一请求回执规则处理选择。

##### Android

- 组选择接入 request-aware readback，维护 pending 请求和已应用 revision。
- reload 消费请求与实例关联，改善界面操作和实际运行状态的对齐。
- 同步上游浅色主题对比度、颜色处理、二维码生成及相关设置页调整。
- 配套调用使用重新生成的 Libbox 绑定核对参数、方法和返回结果。

##### Satelite

- 手动选择与 pending-pick 恢复共用请求感知入口，只有最新有效尝试能够写回设置。
- raw 配置增加持久摘要与 PENDING / VERIFIED / UNVERIFIED 验证记录。
- raw 内容原子写入，验证状态与 metadata 保存协调提交。
- apply、服务启动与 reload 检查对应记录，防止未验证或损坏内容进入运行。
- 完善失败恢复与并发新写入的归属，避免旧恢复覆盖较新配置。

##### Desktop / Dashboard

- 配置预览、应用结果、选择回执和连接流身份同步使用更完整的内核合同。
- 系统托盘、Dashboard 组页面与托盘菜单对齐选择顺序。
- 订阅卡片与菜单更新入口区分配置更新和运行中 Provider 更新。
- Desktop 与嵌套 Dashboard 通过固定源码版本配对消费，保持客户端、协议与内核接口一致。

### 八、依赖仓库更新与发布集成

本版的改动分布在主内核、协议 SDK、基础网络库、原生绑定和客户端仓库。依赖更新包含上游修复吸收、本地扩展保留，以及主仓对新接口和行为的实际接入。发布侧同步整理构建标签、固定依赖、原生来源和验证记录，使这些分散在多个仓库中的成果能够由同一条消费链构建出来。

#### 底层依赖仓库更新与融合

本版不仅更新模块版本，还针对 fork 已有扩展逐项融合上游行为，并将后续正确性修复纳入实际消费链。根模块、嵌套测试模块、客户端 gitlink 和发布 source-lock 同步核对。

##### `sing`：缓冲区所有权、批处理与并行拨号

- 融合上游共享网络与 buffer 层修复，保留本项目的数据路径优化。
- `CachedConn` 使用明确的互斥所有权处理 Read / Close，修复缓存交接、payload 重放与关闭竞争。
- 并行拨号同时保留上游 child context 生命周期和本项目的父取消快速退出。
- 单地址走直接拨号，空地址列表返回明确错误，避免递归与无效路径。
- 合并 Darwin `msgx`、`sendto`、批量读取及 bind 相关修复。
- SOCKS 批处理共享编码路径，减少重复的 remote-address 状态写入。
- 保留 `batchScratch` 与 lock-first 一次性应答等 fork 既有机制。
- 内核消费测试适配新的 CachedConn 包装层，验证 early-data 所有权不被改变。

##### `sing-tun`：GSO、UDP 回复、队列和系统栈

- 融合 GSO 批量读取与跨 batch 恢复处理，保持分段状态正确。
- UDP 回复按实际地址族合成报文头，并为适用的 IPv4 回复生成独立 identification。
- 更新 Linux Auto Redirect、iptables/nftables 和相关服务路径。
- 吸收 ENOBUFS 等读取错误处理，内核与 bridge 适配新的 ReadRetry 接口。
- 保留本项目 per-session、NAT-wide、device-wide 字节预算、真实 waiter 身份、跨队列唤醒及单次额度归还。
- 保留 TCP 背压、UDP burst、fragment group 和队列身份隔离等扩展。
- 改善 unreachable、序号回绕和 SACK reneging 测试的环境独立性，使用实际内核通告和完整载荷校验。
- 完善 Linux 隔离网络与真实 TUN/窄 MTU 验证入口。

##### `sing-quic`：混淆报文、pacing 与共享创建

- 修复 Salamander 短包边界，长度不足时不把 salt 当作有效 payload 暴露。
- 修正 XPlus 写入返回值，并调整会原地修改调用方 buffer 的相关向量写入路径。
- 调整 CUBIC、BBR、Brutal 的 pacing 计费，ACK-only 包不再消耗可重传数据的发送预算。
- 保留 owned DATAGRAM 与硬 packet ceiling 机制。
- 为 Hysteria、Hysteria2、TUIC 的真实共享创建提供 admission hook。
- 多等待者加入同一次创建，复用已发布连接不收费；TUIC 认证写入在发布前完成。

##### `quic-go`：拥塞调度与消费验证

- 同步 CUBIC 的 ACK-only pacing 修复，纯 ACK 不再推进相关发送预算和状态。
- 保留 fork 既有 QUIC/DATAGRAM 数据路径，与 MASQUE、XHTTP、TrustTunnel 和 QUIC 承载进行消费者验证。
- 补充可重传与不可重传包的正向/反向测试，固定 pacing 更新顺序。

##### `uTLS`：预设 ClientHello 与 QUIC 参数

- 融合上游 QUIC transport parameters 的问题修正，确保参数进入实际序列化的 preset extension。
- 保留 fork 对 ApplyPreset 与 SetTransportParameters 不同调用顺序的同步处理。
- 对最终 ClientHello 中的 transport parameters 作逐字节验证。
- 保持本项目既有指纹、REALITY 与扩展顺序的兼容行为。

##### `wireguard-go`：WG / AWG 的真实唤醒修复

- 消费 outbound 空间等待与唤醒修复，覆盖额度归还、启动和等待注册的竞态。
- 保留 AWG 定制能力及 WireGuard 共用路径，在两种模式下验证并发流完整载荷。
- 通过依赖 pin 将修复带入主内核与测试模块，而不只保留在依赖开发分支。

##### `sing-anytls`：复用与创建分开计费

- 将创建 admission 移到真正的 `createSession`，先尝试已有 idle session。
- 健康复用不等待创建额度，新 session 按实际创建结算。
- hook 接收真实创建结果，保持失败和取消的归还时机。
- 补充拒绝不创建、复用不计费与创建失败回传测试。

##### Mieru SDK fork：关闭与附着串行化

- 在实际依赖仓库修复 closed-underlay attach，使用共同的 close mutex 保护关闭状态与会话附着。
- 将修复与 SDK 主线的 fake clock、UDP associate API 等已有变化融合。
- 保持上层错误与协议语义，通过 SDK 定向 race、负控及内核消费者验证闭环。

##### `cronet-go` / NaiveProxy：绑定、生成线与原生来源

- 区分源码线、构建发布提交、`all` 聚合模块和平台 `lib/*`，按实际生成关系记录版本。
- 核对根模块与嵌套模块使用的 binding / native generation，避免生成线比较造成旧产物回退。
- 完善平台化 provenance manifest，验证原生归档和 DLL 的实际字节摘要。
- 为 Windows 等平台补齐来源记录与离线契约检查。
- 保留 purego 与对应 native 加载方式，消费者验证使用真实库路径。
- 维持既有 Chromium / patch5 原生产物代次，增强 Go 集成层与产物来源之间的关联。

##### 其他依赖与客户端源码

- 吸收上游 gliderssh 的 Unix forwarding 符号链接检查修复。
- 同步 Android、Apple、Desktop 与 Dashboard 的相关源码和接口适配，保留本项目已有能力。
- 维护 Desktop → Dashboard 的嵌套引用，以及客户端源码 → Libbox ABI → 主仓 source-lock 的配对关系。
- 对已经等价吸收的改动保留可追踪记录，避免重复引入同一补丁。

#### 构建档位、发布契约与维护

##### TrustTunnel 与 AWG 构建档位

- 将 `with_trusttunnel` 与 `with_awg` 纳入本版相应默认发布 profile。
- Darwin、Windows 与其他目标分别使用目标专属标签，Windows 的 Naive 路径配套 `with_purego`。
- 构建能力以具体目标 profile 和实际 registry 为准，配置分析同步提供当前二进制能力信息。

##### 固定真正被验证的消费版本

- source-lock 同时记录 Go fork、客户端 gitlink 和嵌套 Dashboard 引用。
- 新增 pin/run 对应检查，从记录提交读取锁定投影，核对当前依赖是否由实际运行覆盖。
- 发布路径递归检出子模块并严格校验，防止未读取的嵌套引用被当成已验证。
- 为浅克隆验证补充获取必要历史提交的路径，使检查在 CI 中真正执行。
- ABI 检查常量、客户端消费 pin 与冻结锁同步更新。
- 核对构建标签、模块替换、生成物和原生来源，减少不同清单各自记录不同版本的问题。

##### 知识与维护机制

- 整理本轮工程报告、设计理由、来源、测试与维护结论，更新知识库导航。
- 为版本、来源锁、知识入口和报告登记增加一致性检查。
- 维护逐逻辑补丁索引，记录实际提交、patch-id、触发、消费者与回归范围。
- 为上游等价性与退役判断保留固定来源，避免仅按提交数量或分支名称判断是否可以删除修复。
- 盘点维护循环和后台任务，保留其所有者、触发与关闭边界，降低重复实现与后续维护成本。

### 九、性能改进与验证建设

本轮性能工作同时观察协议吞吐、并发扩展、CPU 成本、规则匹配及内存占用方式。具体改进包括 WG/AWG 并发唤醒、关键词编译匹配、共享创建和探测、流式规则产物，以及数据面的预算与背压。下面将有前后对照的修复收益与最终台架表现分别说明，保留测试条件，方便理解这些数字对应的实际场景。

#### 协议性能实测与本轮优化收益

本轮将协议性能分为三种证据：有前后反例的正确性/活性修复、相同输入下的算子或数据路径对照，以及多个协议在同一台架上的运行特征。下面分别列出，表中的单位保持原始报告口径。

##### WG / AWG：从并发停滞到多流完整传输

这项结果对应真实代码改动：`wireguard-go` 中 outbound-space 等待/唤醒修复，随后由主内核更新实际依赖。

条件为同主机、WSL2、loopback、两个独立进程、用户态 endpoint；对照旧 pin `a765d957fd81` 与修复 pin `7ece3d66d79d`。

| 上传场景 | 修复前 | 修复后 |
|---|---|---|
| WireGuard，2 流同时起流 | 2/2 停滞，sent=0 | 3/3 完成，42.9–53.9 MiB/s |
| WireGuard，2 流间隔 200 ms | 2/2 失败 | 3/3 完成，47.5–51.8 MiB/s |
| WireGuard，4 流同时起流 | 3/3 失败 | 3/3 完成，49.8–54.8 MiB/s |
| AWG，2 流同时起流 | 阶段对照无法完成 | 3/3 完成，38.6–53.7 MiB/s |
| AWG，4 流同时起流 | 3/3 失败 | 3/3 完成，41.5–53.8 MiB/s |

完整正向矩阵包含 **WG / AWG × 0 / 200 ms / 1 s 起流间隔 × 2 / 4 流 × 3 次重复，共 36/36 完成**。这里的提升是修复特定并发场景下的活性和真实载荷传递，不把失败样本的零速率算成夸张的百分比。

[对应前后对照](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/3032ca5199dbd151be0b2cd006fba191b87cc1e8/docs/final-audit/closure/PERF-COMPARISON.md)

##### 八协议台架：吞吐、并发扩展与成本一起看

本轮实验室覆盖八个协议域；H2/H3、TCP/UDP underlay 等按实际传输拆成不同 scenario。使用真实客户端/服务端进程和 socket，XHTTP 使用参考 Xray 服务端，Naive 加载 Cronet 原生库。

下面是三阶段性能报告记录的最终批次中位数，单位 **MiB/s**；**“1 → 4”表示从 1 条流增加到 4 条流，不表示版本前后提速**。环境为同主机 WSL2 / loopback、5 秒测量窗口，初始 3 次重复，指定复测场景使用 5 次重复。

| 协议 / 承载 | 上传：1 → 4 流 | 下载：1 → 4 流 |
|---|---:|---:|
| VLESS Vision | 264.7 → 809.1 | 542.3 → 1,409.0 |
| VLESS REALITY | 266.2 → 814.2 | 528.0 → 1,455.5 |
| AnyTLS | 255.9 → 769.4 | 538.9 → 1,234.3 |
| TrustTunnel H2 | 172.5 → 213.5 | 140.3 → 140.7 |
| TrustTunnel H3 | 145.7 → 202.2 | 194.8 → 233.0 |
| Naive H2 / Cronet | 130.5 → 238.2 | 234.6 → 371.6 |
| Naive H3 / Cronet | 58.5 → 78.1 | 91.5 → 76.4 |
| XHTTP H2 / 参考服务端 | 32.7 → 126.3 | 89.6 → 368.6 |
| XHTTP H3 / 参考服务端 | 32.8 → 127.4 | 119.8 → 203.9 |
| MASQUE CONNECT-IP | 70.1 → 80.5 | 72.3 → 79.2 |
| Mieru TCP underlay | 122.6 → 483.1 | 103.5 → 689.4 |
| Mieru UDP underlay | 30.4 → 73.0 | 31.2 → 39.9 |

UDP 形态也单列测量，避免把基于可靠 stream 的数据报包装与 QUIC DATAGRAM 当成同一种数据路径：

| 1200 B 数据报场景 | 1 → 4 流吞吐 |
|---|---:|
| AnyTLS UoT | 23.6 → 22.0 MiB/s |
| TrustTunnel H2 UDP | 10.3 → 10.0 MiB/s |
| TrustTunnel H3 UDP | 34.3 → 31.5 MiB/s |
| MASQUE DATAGRAM | 49.5 → 53.5 MiB/s |

该组数据用来理解协议形态：VLESS/REALITY/AnyTLS 的连接复用与多流扩展、TrustTunnel H2/H3 的 framing 差异、Cronet 原生调用、XHTTP 上下行模式，以及 MASQUE 用户态 IP 栈的成本。它是开发阶段固定条件下的台架记录，不是最终 v0.1.45 在公网或手机上的带宽承诺，也不将不同批次的主机状态变化归因于某条优化提交。

[完整吞吐、CPU 与资源分析](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/3032ca5199dbd151be0b2cd006fba191b87cc1e8/docs/final-audit/PERFORMANCE.md)

##### 单位业务量 CPU：避免只追求最高吞吐

同一报告按成功传输量计算客户端 CPU s/GiB。四流场景的代表结果如下，数值越低表示该台架上完成同样业务字节所需的客户端 CPU 时间越少：

| 协议 / 承载 | 上传 CPU s/GiB | 下载 CPU s/GiB |
|---|---:|---:|
| VLESS Vision | 5.21 | 3.00 |
| VLESS REALITY | 5.16 | 2.92 |
| AnyTLS | 5.47 | 4.11 |
| TrustTunnel H2 | 10.12 | 13.10 |
| TrustTunnel H3 | 13.60 | 15.49 |
| Naive H2 / Cronet | 9.39 | 6.09 |
| MASQUE CONNECT-IP | 33.86 | 40.89 |

由此可以将普通 TLS/mux、HTTP 隧道、原生 Engine 和用户态 IP 隧道分开评估。H3 并非所有场景都会比 H2 快，吞吐提高也不能自动解释为单位字节 CPU 下降。本版针对真实创建、重复探测、数据保留和唤醒推进做整合，保留协议自身模型，而不是为了统一接口增加额外复制或全局串行化。

##### 内存优化的具体收益点

| 改动 | 本轮能够直接确认的结果 |
|---|---|
| 大关键词自动机 | 1,024 pattern 的 miss 从 13.1–14.5 µs 降到 188–243 ns；late hit 从 15.1–15.3 µs 降到 187–189 ns |
| 流式规则产物 | 4 MiB 尾随数据样例完整加载累计分配 279,712 B；不再为该响应保留同尺寸的完整下载副本 |
| 预编译 SRS 加载 | 20,000 域名合成样例的 load+compile 为 994.2 µs，源 JSON 为 152.01 ms；累计分配分别为 724,496 B 与 52,041,328 B |
| XHTTP 分配容量账 | 同一反例中逻辑字节 65,536 B、实际数组 81,920 B，新账准确记录真实受控容量 |
| XHTTP 首包保证 | 数据 POST 被屏障阻塞、主池满时，新流 1 KiB 开场仍可到达真实 RoundTripper，不依赖 bulk 先完成 |
| Relay growth | 两个方向各自申请/归还；拒绝增长的方向仍完成相同字节复制，几何必需的大缓冲不被政策错误禁用 |
| TUN 等待协议 | 多尺寸/迁移/Close 的确定性交错中，释放足够字节后对应活写者被唤醒并入队，不靠轮询空转 |

这些改动共同控制“多个看似有界的模块叠加后仍然变大”的情况。优化目标包括减少重复工作、限制受控高水位和保证小请求前进，不把队列长度、Go heap、RSS 与 native 内存混成一个所谓统一降幅。

#### 测试、故障注入与实验工具

- 扩展跨模块的生命周期测试，覆盖 Dial、Close、reset、InterfaceUpdated、压力变化和迟到回调交错。
- 关键反例使用 barrier、可控服务端和负控，验证守卫、身份检查与结算路径确实生效。
- 增加真实 Box、daemon RPC、代理服务端、TUN、窄 MTU、网络命名空间和多进程载荷夹具。
- 建立八协议资源所有者与调用链审查材料，按 session、connection、engine、device 等实际资源理解复用和释放。
- 完善性能实验室的 client/server/target 路径与参考服务端标记，记录成功业务量、CPU、RSS、FD、线程及相关负载参数。
- UDP 负载增加 pacing、序号、epoch、重复/乱序/迟到分类以及尾部观察状态，区分发送、实际完成和观察期限内未返回。
- 目标端按测量 epoch 计数，避免预热和上一轮迟到数据污染本轮结果。
- 采样与延迟使用单调时间，补充有效参数、runner 身份、内核二进制摘要和生效 replace 来源。
- Windows 采样以 PID 加创建时间识别进程，降低 PID 复用造成的归属错误。
- 调整测试对个人目录、宿主转发开关和固定内核窗口的依赖，增强异地复跑能力。
- 完善 race、schema、模块与工作流结构检查，明确区分真正执行的用例与构建检查。

##### 关闭诊断也保持有界

- Scope 按现有逆序依赖关闭，等待已经开始的清理真正汇合，不用超时返回伪装资源已经释放。
- 慢关闭记录当前 owner/stage、已完成数、pending 数和阶段耗时。
- watchdog 的栈采集限制为 **4 KiB**，单次报告，避免内存压力下反复输出大堆栈反而放大资源消耗。
- watchdog 只观测，不跳过、取消或重排真正的清理动作；保持关闭顺序与诊断职责分离。

---

本版继续沿用 JiejieBox 现有配置、协议和生命周期体系。新增选项采用显式启用或兼容默认值；核心改进集中在多任务并发、动态更新、网络变化和资源压力下的行为一致性。

[项目与源码](https://github.com/Piggy-Cat-bit-shadow/JiejieBox) · [配置说明](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/tree/testing/docs/configuration) · [扩展配置示例](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FORK-CONFIGURATION.md)

## [v0.1.44](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.44) — 2026-11-08

本版重点解决远程订阅节点尚未下载时，纯 Provider 策略组作为链式代理的 `detour` 被误判为断链、导致内核无法启动的问题；同时修复节点热更新期间的环路与在途拨号交接。iOS 同步修复隧道接管等待与本地回环的边界情况。

使用远程订阅并将 Provider 节点放入多跳链时，先检查订阅下载和域名解析是否能独立到达网络；[远程订阅与链式代理示例](docs/FORK-CONFIGURATION.md#远程订阅与链式代理)展示了具名 `http_client` 与纯 Provider 组的配合。

### Provider、链式代理与热更新

- **机场节点未拉取到时**：若首次导入本地配置并启动后，机场节点分组仍为空，请先停止 VPN，在 **设置 → 核心 → 清理工作目录** 后，重启 VPN 再重试。
- **Provider 配置解析范围**：支持 sing-box / JiejieBox JSON 的 `outbounds`、Clash / Mihomo（Clash Meta）YAML 的 `proxies`、Shadowsocks SIP008 JSON 的 `servers`，以及明文或 Base64 分享链接列表（包括 v2rayN 等客户端导出的对应链接）。链接协议支持 `ss://`、`vmess://`、`vless://`、`trojan://`、`tuic://`、`hysteria://`、`hy2://` / `hysteria2://`、`anytls://`。
- 上述格式仅用于 Provider 提取代理节点，不导入原客户端的策略组、规则、DNS、入站设置，也不递归拉取 Clash `proxy-providers`；不代表可直接作为完整主配置运行或兼容全部扩展字段。V2Ray / Xray 原生 JSON、Surge、Quantumult X 完整配置未接入解析，WireGuard / Tailscale endpoint 暂不由 Provider 发布；协议可用性仍取决于平台与构建档位。
- 仅由 Provider 供给成员的空组，在节点尚未发布时可作为 `detour` 目标等待；没有 Provider 供给的断链仍会报错。远程订阅下载路径若反过来依赖自己尚未加载的节点，会在启动时给出明确错误，避免下载引导环。
- Provider 发布节点或 selector、URLTest 更新成员前，校验动态出站图及跨 DNS、出站的环路。无效更新不会把正在使用的节点替换为不可拨号路径；发布后的组成员和链式代理会重新参与校验。
- 同标签节点替换时，旧对象保留到策略组重建完成；连接拨号期间为选中的节点持有租约。已移除或关闭的对象不再被新连接误用，仍在使用的对象等最后一个租约释放后再关闭。
- selector 和 URLTest 遇到租约拒绝会重读当前成员；无法取得有效节点时返回明确错误。补齐并发替换、启动顺序、环路和租约交错测试。

### iOS 隧道与本地回环

- Apple 客户端把隧道接管等待移出服务生命周期锁，停止或新一次启动可中断旧等待；新会话重置接管成功标记，避免复用上次启动结果。
- 本地签名回环按 IPv4-mapped 地址的实际地址族判断，修复这类地址绕过回环识别的问题。

### 构建与验证

- Apple 客户端固定到 `e3283104`，重新核对 Libbox ABI。修复发布契约检查在 `pipefail` 下偶发的 SIGPIPE 误判，以及一项会丢失对端读取信号的并发测试。
- [正式发布运行](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42391992312)中的构建、契约、Verify、race、stage 与 publish 全部通过；8 项公开附件已回读，包含各平台内核、Android ARM64 AAR、Apple Libbox、Windows 安装器、未签名 IPA 和校验清单。真机 Network Extension 行为不由这些构建结果证明。

[完整更新日志](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/CHANGELOG.md) · [出站 Provider 配置](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/v0.1.44/docs/configuration/provider/index.zh.md) · [本地回环说明](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/v0.1.44/docs/IOS-SELFSIGN-AND-LOOPBACK.md)

## [v0.1.43](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.43) — 2026-11-07

本版新增出站 Provider。可从远程订阅、本地文件或内联配置导入代理节点，让 selector 和 URLTest 随节点更新调整成员，无需手工复制节点列表或重启内核。

### 出站 Provider 与配置

- 新增根级 `providers`，支持 `remote`、`local` 和 `inline`。远程与本地订阅支持 sing-box JSON、Clash YAML、SIP008 及原始或 Base64 分享链接，导入范围限于出站节点。
- selector 和 URLTest 新增 `providers`、`use_all_providers`、`include` 和 `exclude`。节点以 `provider/node` 命名，可与静态出站一起加入策略组。组过滤匹配完整标签，远程 Provider 自己的过滤匹配文档内的原始标签。
- 远程订阅异步下载，支持定期刷新、缓存和 ETag 条件请求。本地文件变更后重新加载，也可配置节点健康检查和拨号参数覆盖。

### 热更新与稳定性

- 未改变的节点及其现有连接可以继续使用。需要替换时先启动新节点，单节点替换失败则保留原对象。selector 优先保留仍可用的选择，URLTest 按新成员重建探测组。
- 修复畸形订阅导致的崩溃与无效文档清空节点的问题。下载设有 30 秒超时和 10 MiB 正文上限，并对日志中的订阅 URL 凭据做脱敏。
- 修复缓存写入失败后仍更新 ETag、无已加载文档时收到 304 可能覆盖有效缓存的问题。Windows 本地文件监视改用解析后的绝对路径。

### 构建与验证

- Provider、策略组、配置解码、缓存及 Box 集成测试通过。生成的配置 schema 与源码一致，配置示例通过 `sing-box check`。
- 本版内核发布编排通过，包含各平台内核、Android ARM64 AAR、Apple Libbox、Windows 安装器、未签名 IPA 和校验清单。[发布构建](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42343308769)

[完整更新日志](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/CHANGELOG.md) · [出站 Provider 配置](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/v0.1.43/docs/configuration/provider/index.zh.md) · [扩展配置示例](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/v0.1.43/docs/FORK-CONFIGURATION.md#出站-provider)

## [v0.1.42](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.42) — 2026-11-07

本版更新 Apple 客户端，修复配置切换、隧道设置和页面错误提示，同时整合正式版构建流程，减少手动派发时漏掉 Windows 安装器或 iOS 自签 IPA 的情况。

### Apple 客户端

- 修复切换配置时界面与实际使用的配置不同步，按可用空间调整模式选择栏。
- 将 Packet Tunnel 和按需连接设置写入系统，补全日志、崩溃报告和内存报告页面的命令失败提示。
- 补齐界面本地化，修复工具页部分服务入口的显示问题。更新 Apple 客户端到 `0c7b10be` 并复核 Libbox 调用接口。

### 构建与发布

- 新增统一的 Kernel release 入口。各平台构建并行启动，Windows 安装器可在正式 tag 创建前参与候选构建。
- 将自签 IPA 并入 Apple 工作流，与 iOS、macOS 构建共用同次 Libbox。TF 可从指定正式 Release 获取 Libbox，并按需只发布 iOS 或 macOS。
- 发布前核对产物来源、版本和摘要，逐项检查草稿及公开附件。恢复中断的发布时，可保留已上传且摘要一致的附件。

### 验证

- 正式发布运行通过，8 项公开附件全部回读。7 个二进制文件逐项与 `SHA256SUMS` 一致，Apple Libbox 的归档摘要及 iOS、macOS arm64 分片也已核对。[发布交接记录](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/releases/RELEASE-0.1.42-HANDOFF.md)

[完整更新日志](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/CHANGELOG.md) · [配置文档](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/tree/testing/docs/configuration)

## [v0.1.39](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.39) — 2026-11-05

本版增加 iOS 自签安装与本地回环，改善突发建连时的内存保护，同步 NaiveProxy patch5，并修复 Linux 自定义 DNS、设备切网及 Apple 客户端连接问题。

### 自签安装与客户端连接

- 新增完整未签名 IPA，可自行签名安装；应用与 Packet Tunnel 扩展同步写入版本。
- 适配重签后的团队身份、App Group 与扩展标识，按实际授权选择共享容器，修复自签后启动失败、隧道已连接但日志和统计不可读的问题。
- 缩短 libbox 命令 socket 路径，统一客户端与服务端的路径选择，修复容器路径过深导致连接失败的问题。
- 增加 VPN 已连接但 TUN 未完成接管的诊断，便于区分界面状态与实际网络接管。

### 本地回环

- 新增“更多 → 连接行为 → 配置覆盖 → 本地签名回环”开关，默认关闭；开启后注入 `10.7.0.1` 并配置 iOS 主机路由，保留已有地址和订阅文件。
- 默认 `go` 栈补全 UDP、ICMPv4/v6 反射，修复 IPv4-mapped 地址匹配；其他显式栈提供不支持提示。
- **SideStore 续签（真机反馈）**。纯 IPv4 的 TUN 配置下，开启本地签名回环即可续签。IPv4/IPv6 双栈下，在 SideStore 的 **Developer Options** 中再开启 **Accept IPv6 Config**，即可保留 TUN IPv6 并完成续签。此前的 `DeviceEndpointNotInitialized` 在该设备上也通过这一设置解决。[续签步骤](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/IOS-SELFSIGN-AND-LOOPBACK.md#当前兼容性与续签方式)
- 另有真机反馈，WLOC 的开发者模式虚拟定位可正常使用。

### 突发建连与低内存保护

- 为 iOS Network Extension 的 AnyTLS TCP/UoT 和共享 TLS 握手增加创建准入，限制并发、排队数量与等待时间，降低集中握手的资源压力。
- 按内存压力逐级收紧普通创建并发（默认 32 / 16 / 8），为交互与实时流量预留额度；排队支持取消、超时和等待老化。
- TLS、AnyTLS 与多跳代理共用一次拨号许可，修复嵌套申请造成的阻塞；成功、失败及取消均归还许可。
- 修复旧压力状态覆盖新策略、重复触发回收的竞态；复用已有 OOM 采样，补充排队、拒绝、取消与在途计数。
- 修复 runtime 内存指标发布滞后导致的误判，更新 sing-tun 并补充 UDP NAT 压力验证。

### NaiveProxy 安全与传输

- 同步 **NaiveProxy v154.0.8037.49-5** 相关修复，Chromium 基线保持 **154.0.8037.49**；证书封锁列表新增 8 个 SPKI 哈希。
- QUIC 流级 / 连接级接收窗口上限调整为 **64 / 128 MiB**，修复窗口下限检查并启用自动调节，保留自定义窗口配置。
- 修复 Linux 匿名 socketpair 无对端地址时 DNS 回复失败的问题，恢复自定义解析与拨号目标。原约 **274 秒超时**用例修复后约 **0.79 秒通过**，Linux purego / cgo 均完成同场景验证。
- 更新 Go 封装和原生依赖，重建 Linux amd64、Windows amd64、Android arm64、macOS arm64、iOS arm64 库。

### DNS、切网与隧道兼容

- DNS 冷连接异步建立，避免 TCP/TLS 握手阻塞收包；打包前复制 DNS 消息，修复并发修改共享请求的问题。
- 设备地址变化时中断对应地址族的旧 UDP socket，使后续连接使用更新后的地址状态。
- 完善 OpenVPN、OpenConnect 端点本地流量处理；吸收严格路由、内核 IPv6、Windows RAS 默认路由和 OpenConnect CSD/F5/GPST 兼容修复。

### 构建与发布

- iOS、macOS 和自签构建复用同次校验的 Libbox，统一客户端来源、应用版本和原生库来源记录。
- 将未签名 IPA 纳入正式附件，完善 Go 模块版本与发布清单校验；完成各平台核心、Apple 包及 Windows 安装包验证。
- Linux 默认服务端档提供 Naive 入站，未启用 Cronet 出站；Apple Intel / 模拟器原生分片沿用已有库，macOS DMG 不在本版正式附件中。

[完整更新日志](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/CHANGELOG.md) · [配置文档](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/tree/testing/docs/configuration)

## [v0.1.37](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.37) — 2026-11-02

本次更新重点改善 Mieru、NaiveProxy、XHTTP 和 VLESS Vision 的连接稳定性，吸收上游成熟修复，并进一步加强 TCP 监听安全。

### 协议更新与修复

- **Mieru**：升级至 v3.39.0，修复持续单向上传期间可能出现的异常读超时，完善 UDP 边界与空闲连接测试。
- **NaiveProxy / Cronet**：优化代理服务器地址解析，使其由 sing-box 拨号器处理，更好地兼容自定义 DNS 策略与 IPv4/IPv6 双栈调度。
- **Naive 服务端**：修复 HTTP/2、HTTP/3 非法 CONNECT 请求可能被错误响应为 200 的问题。
- **XHTTP**：修复 stream-up 上传被服务端拒绝时可能持续挂起的问题，改善 HTTP 错误传递、连接取消及关闭处理。
- **VLESS Vision**：更新 sing-vmess 依赖，修复 direct-write 场景下的连接关闭问题。

### 网络与安全

- Linux TCP 监听默认禁用 MPTCP，仍支持通过 `tcp_multi_path` 显式启用。
- 统一 Clash API、V2Ray API、Debug HTTP、libbox pprof 等入口的 TCP 监听策略，避免意外启用 MPTCP。

### 稳定性与兼容性

- 完善 Mieru、Naive、XHTTP 和 Vision 的回归测试。
- 复核 XHTTP packet-up 与 XMUX 的并发、背压和连接生命周期行为，保留现有稳定实现。
- 对齐部分上游依赖，继续保持现有协议配置与架构兼容性。
- 完成相关平台构建、竞态检测及 CI 验证。

### 说明

- NaiveProxy 原生引擎当前基于 Chromium 154.0.8037.49，本次更新主要改进 Cronet 集成层，并未升级原生 Chromium。部分后续原生安全与性能补丁计划在下一轮 Chromium 更新时统一处理。
- 本次更新以稳定性修复为主，不涉及大规模配置变更。

## [v0.1.36](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.36) — 2026-11-01

Windows 启动 Apple 构建后，Mac 可以跟随同一次运行提前获取 Libbox、开始 TestFlight 归档。版本、核心提交、客户端源码与正式 Release 的产物一起固定，上传前再检查标签和相应平台的验证结果。

- Apple Actions 先固定核心版本、客户端源码和构建来源，Libbox 就绪后即可供 Mac 下载，客户端验证继续并行。
- Mac 可以跟随指定的 Actions 运行，在独立工作目录准备源码和签名归档；上传前核对版本标签与对应平台的验证结果。
- 正式 Release 使用指定运行及其重跑次数，复用同一份 Libbox；归档后的主应用和扩展也会检查版本号与构建号。
- 单平台发布跳过另一平台的准备工作，已有的同提交构建可以复用；Apple 嵌套依赖在确定客户端提交后再初始化。
- 修正仓库识别：同时配置 origin 和 upstream 时，发布脚本明确使用 origin 对应的仓库。

本版的改动集中在构建、发布和文档，没有修改代理内核的数据处理逻辑。操作步骤见 [Apple TestFlight 发布说明](docs/APPLE-BETA-RELEASE.md)。

按正式发布版本记录主要功能、修复、优化与验证工作。日期使用北京时间；各版本在线说明见 [GitHub Releases](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases)。

### 发布流程与版本对齐

Libbox 的 manifest 记录内核提交、发布版本和文件哈希，并附带本次解析的 Apple 客户端提交与 Actions 运行身份。Mac 跟随这次运行使用独立工作目录，正式 Release 指定同一运行和重跑次数；如果运行被重跑或标签指向另一提交，流程会拒绝把不同来源的库混在一起。TF 的 App 版本取自固定提交的 `release/JIEJIE_VERSION`；构建号用于区分同版本的多次上传。

这让 Mac 在库就绪后即可与云端客户端验证并行归档。iOS 上传仍需相应平台的验证和正确标签，签名与上传在用户自己的 Mac 上完成。见 [TestFlight 流程](docs/APPLE-BETA-RELEASE.md)。

## [v0.1.35](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.35) — 2026-11-01

本版整理 Apple 和桌面客户端的源码入口，修复 Apple 构建中的链接处理，并更新项目文档。相对 v0.1.34，内核没有 Go 源码改动。

### 客户端

- Apple 客户端“Source Code”入口指向 JiejieBox 内核仓库；Hako Source Code 的副标题保持原样。
- 桌面客户端更新内嵌 Dashboard，同步源码链接。Android 客户端的固定版本不变。

### Apple 构建与发布

- 修复 Apple 客户端构建时的链接处理：同时识别仓库旧名 `sing-box` 与现名 `JiejieBox`，避免正确的源码链接导致构建检查失败。
- 同步 Apple、桌面客户端和 Dashboard 的源码固定值，并更新 Apple ABI 校验基准。

### 文档

- 重新整理首页，介绍协议与网络能力、链式代理、DNS、低内存优化和生命周期管理，并补充 Linux / macOS 命令行内核入口。
- 更新配置示例、NXDOMAIN、Fast Path 与内存管理说明；整理知识库的中文表述、索引和历史记录。

### 发布与验证

- 本版本提供 Linux amd64、macOS arm64 和 Windows amd64 命令行内核，Android arm64 与 Apple Libbox，以及 Windows 客户端安装包。
- 对发布提交完成 Verify、发布契约、各平台内核、Apple 客户端和 Windows 安装包相关工作流验证。具体产物及校验信息见 GitHub Release 附件。

## [v0.1.34](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.34) — 2026-10-29

本版统一配套客户端的订阅请求行为，并整合 Apple 客户端的源码选择、构建和发布流程。

### 订阅与客户端

- 配套客户端订阅 User-Agent 固定为 `sing-box/1.15.0`。
- 订阅流量与到期时间展示依赖服务端返回有效的 `Subscription-Userinfo` 响应头。
- 更新 Apple、Android 与桌面客户端引用，以及 uTLS 依赖。

### Apple 构建与验证

- Apple 客户端更新包含上游修改、服务端点注册表关闭修复和测试链接修复。
- 建立单 macOS runner 的专项验证路径，补充源码选择与工作流结构检查。
- 修复共享构建步骤重构后，检查仍引用旧构建位置和错误归档路径的问题。
- 修复 Libbox 构建前临时安装目录未创建的问题。

### 源码固定与维护

- 在标签创建前固定依赖源码，完善版本计算、重复执行与 ABI 检查时序。
- 将构建证据关联到对应源码版本，补充内核及关联仓库的知识库记录。

### 配套客户端接线

- Android 启动服务前先等待 Libbox 初始化完成；切换所选配置时区分当前服务模式，相同模式重载，不同模式执行停止、等待和重新启动。
- Apple 端点注册经对应服务客户端转发，更新连接时在锁内切换并使旧连接失效，避免旧失效回调清掉新连接。
- 订阅正文统一稳定 UA，用量兼容探测与一般应用身份继续分开，避免一个 UA 修改影响所有 HTTP 请求。

## [v0.1.33](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.33) — 2026-10-28

与 satelite-one v0.1.33 配套发布，统一内核版本标识，沿用 v0.1.32 的协议、路由与配置行为。

### 版本与发布

- 内核版本更新至 0.1.33，无新增运行时代码或配置字段。
- 提供 Windows、Linux amd64、macOS arm64 命令行核心，Android arm64 AAR、Apple Libbox 及 Windows 客户端安装包。
- 附件提供 `SHA256SUMS`，用于核对下载文件。

[完整更新日志](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/CHANGELOG.md)

## [v0.1.32](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.32) — 2026-10-28

本版让 endpoint 本地产生的流量通过 TUN 栈处理，并更新配套客户端，便于沿用内核的流量处理和日志上下文。

### 路由与客户端

- 通过 TUN 栈处理 endpoint 本地产生的流量，共享流量日志上下文。
- 更新 Apple 客户端引用，纳入 TUN MTU 限制相关修改。
- 整理更新后的模块依赖。

## [v0.1.31](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.31) — 2026-10-28

本版集中处理内核审计发现的问题，覆盖网络接口变化、TUN 与 FakeIP、代理组、TLS / QUIC、嗅探和连接生命周期。

### TUN 与网络变化

- 修复启动后网络接口更新时未完整触发重置的问题。
- 修复网络重置交接窗口中的唤醒丢失，避免已到达的更新被遗漏。
- 改进 TUN DNS 劫持配置和启动 / 关闭边界处理。

### 代理组与协议

- 为 selector、URLTest 和 loadbalance 贯通完整候选地址列表与网络策略，修复首个地址黑洞耗尽共享超时后，另一地址族根本没有机会尝试的问题。
- 代理组对一条流只选择一次成员，再把候选列表传给该成员或下一层代理组；不会因逐地址尝试意外重复选择成员。
- 对不支持 ECH 的 uTLS 指纹，在配置阶段明确拒绝该组合，修复配置启用了 ECH 却可能静默不生效的问题。
- 修复 Naive 出站选项传递、TUIC 数据包大小处理和远程规则集相关边界。
- 更新 TUIC / HTTP3 数据包大小限制的 schema，使配置描述与实现保持一致。

### 嗅探、地址与连接

- 完善 FakeIP 目标的嗅探处理，避免已知域名或特殊地址在后续路径中被错误处理。
- 补充 IPv4 映射地址和地址族判断的边界修复。
- 修复 TLS / uTLS、HTTP/3、kTLS 和零拷贝相关审计问题，并增加相应回归测试。

### 配置生效与数据完整性

- 修复显式 TUN DNS 地址与 DNS Hijack 接线，覆盖普通 TUN 的查询识别，避免 DNS 流量遗漏规则处理。
- Naive 对不支持的证书 pin / uTLS 选项明确拒绝，避免配置被接受却未执行相应安全约束。
- uTLS 对不支持 ECH 的指纹组合在配置阶段报错，避免 ECH 静默失效。
- TLS 控制记录或错误出现在已读数据之后时，先交付尾部数据，再在后续读取报告错误。
- 远程规则集下载增加 HTTP 超时与关闭状态保护，修复关闭后的更新边界。
- TUIC 将已知下层隧道容量传递到 QUIC 包上限，而不是仅修改初始包偏好。

### 审计落实与回归建设

- 将 TUN 重置问题拆成接口更新触发与交接唤醒两条路径修复，补充启动前、启动后及连续更新的状态检查。
- 为 Naive 出站配置传递、uTLS 初始化和 TUN DNS 劫持增加专项回归，避免配置被接受但未实际到达使用位置。
- 对 selector、URLTest、负载均衡组及远程规则集的资源释放和状态更新补充边界保护。
- 将 Linux 专用 kTLS 等待路径纳入检查，并完善 TLS 关闭时尾部数据的处理。

### 审计与修复的范围

本版将此前全内核审计中的多项问题落实到生产路径和回归测试。TUN 重置、候选地址穿透代理组、FakeIP、嗅探和协议关闭属于不同的故障边界；不能以其中一项修复推断所有设备与所有组合均已完成验证。历史发现与当时的证据见[全内核审计报告](KNOWLEDGE/reports/core/KERNEL_FULL_AUDIT_REPORT_20261028.md)。

## [v0.1.30](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.30) — 2026-10-28

本版修复双栈选路、macOS 网络恢复和连接关闭中的数据保留问题，也补充了相应回归检查。

### 双栈与 AnyTLS

- 流连接与数据包路径统一使用有效地址族策略，在拨号前规范化 IPv4 映射地址。
- 调整双栈主机上的 AnyTLS 代理服务器 IPv4 优先策略，并记录实际选择的地址族。

### 嗅探与连接关闭

- 已有可信 FakeIP 域名时，不再继续等待分片 QUIC 嗅探。
- CommandServer 关闭时释放其拥有的 Box。
- 零拷贝读取遇到 TLS close-notify 时保留已读取数据。
- 修复半关闭流的释放结果与观察状态发布顺序。

### 平台与维护

- macOS 按休眠周期去重恢复事件，主机唤醒时重置网络。
- 更新配置 schema 和 ABI 检查，修复相关审查边界问题。

## [v0.1.28](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.28) — 2026-10-27

本版调整网络交接的状态发布顺序，并完善旧连接的清理，减少切网时新旧状态交错。

### 网络切换

- 在唤醒等待操作前确定交接，并先发布默认网络状态。
- 跨接口切换时及时关闭旧连接和数据报流，减少旧路径状态残留。
- UDP 收到返回数据才形成有效存活证据，避免仅凭持续发送将失效会话判为可用。
- 更新 Apple 路径监控，跟随当前活动接口。

## [v0.1.27](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.27) — 2026-10-26

本版修复双栈 DNS 对空结果与查询失败的区分，避免一个地址族没有记录就掩盖另一地址族的结果。

### DNS

- 某个地址族没有记录时，不再将其直接视为整个解析失败。
- 保留另一地址族的有效结果，并补充相关回归测试。

## [v0.1.26](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.26) — 2026-10-25

本版更新默认 Dashboard 的来源和品牌界面，并加入来源回归检查，防止后续合并时无意恢复旧界面。

### 本次更新

- 默认 Dashboard 改为本项目维护的版本，接入 JiejieBox 品牌及订阅配额展示界面。
- 对默认 Dashboard 来源增加回归检查，防止后续合并时静默恢复为上游界面。
- 补充真实 Dashboard 归档的解压与服务测试：检查归档顶层目录裁剪、入口页面、静态资源、ETag 和页面品牌，避免只返回 HTTP 200 却显示空白页面。
- 同步 sing、uTLS 和 Apple 客户端引用，保持主模块与测试模块的依赖版本一致。
- 更新关联仓库目录，明确各客户端和依赖 fork 的入口。

## [v0.1.25](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.25) — 2026-10-25

本版结合上游修复和六个底层依赖 fork 的整合，处理 DNS、Tailscale、TUN、路由、低内存与资源回收之间的兼容和生命周期问题。

### DNS、搜索域与 Tailscale

- 修复 DHCP 下发的 DNS 搜索域展开，改进域名查询与搜索路径处理。
- 修复本地 DNS 服务器的 systemd-resolved 检测，并在 DNS 配置变化时处理相关缓存；配置未变化时保留已有服务器信息。
- 修复 Tailscale 短名称解析与 DNS 路由匹配，更新 Tailscale 依赖及默认路由处理。
- 修复 DNS 缓存命中时地址过滤规则未正确检查的问题。
- 修复 Windows 本地 DNS 状态通知通道关闭后可能持续循环的问题。

### TUN、路由与快速路径

- 同步 sing-tun 的 TCP 连接保留、绑定和关闭等修复，并更新相关依赖固定版本。
- 修复显式配置 `fast_path: false` 时，直连路径仍可能将其当作允许的问题；显式禁止优先于自动授权。
- 将网络策略应用到 ICMP 拨号，并修复不同平台解析 IPv6 重定向原始目标端口的处理。
- 修复 Linux 延迟 TCP Fast Open 拨号进入网络命名空间的时序问题。

### MASQUE、WireGuard 与低内存

- MASQUE 内存 TUN 接入阻塞式入站写入路径，完善关闭和数据处理的交接。
- WireGuard 设备发布时继承已经生效的暂停状态；关闭时释放暂停中的 bind。
- 低内存清理限制并发重复执行，关闭时等待相关清理到达安全边界；内存占用稳定后停止无意义的周期性释放。
- 修复 Linux 网络命名空间持有对象和 user namespace 凭据的生命周期问题。

### 内存回收执行器与分层收益

- 闲时机会、Darwin 压力和危机请求合并到单执行器，优先级为 crisis > pressure > idle；同时只运行一项回收，避免多个来源重置同一组连接池并重复 GC。
- L1 先清理过期缓存，L2 再释放空闲池，L3 仅在前两层收益不足且压力 / runtime 页面有依据时强制 GC 与 scavenge。
- 每层从同次起始 footprint 判断收益，达到门槛即停止；下一次仍从 L1 开始。低收益进入冷却，修复稳定占用下每秒反复 FreeOSMemory。
- 更紧急的请求可替换尚未执行项，不能强行打断已经进入的池遍历；普通回收在层间检查危机并让出。
- Darwin 压力回收不直接重置网络；紧急路径执行前重新采样，压力已回落时避免使用旧请求重置健康连接。
- Triggered 在跌破 armed 边界后退出，不再错误等待更低 idle resume 水位，修复危机态长期保持和持续压力问题。

### 关闭屏障与重载状态

- stop 停止接纳新观察与回收，丢弃还在排队的请求，等待已开始的 poll、回调和动作完成，再等待执行器退出。
- 等待不持有状态锁，避免回收结束路径需要同一锁而死锁；昂贵工作也不阻塞正常状态观察。
- 重载不携带旧实例临时 pressure intent、强制采样请求和未执行危机；继承的 Triggered 转成 Armed，下一次真实采样重新决定。
- 紧急回收后给普通空闲整理冷却，避免刚回收完又将 footprint 下降误当成新机会再执行一轮。
- 补充分层提前停止、GC 条件、排队危机重检、关闭丢弃、幂等 stop、重载隔离和压力回调合并等回归测试。

### 并行拨号取消与 UDP 批处理

- 修复底层并行拨号仅等待结果或 fallback timer、却未直接监听父上下文取消的问题；调用者退出后及时返回并清理迟到连接。
- 用忽略子上下文的拨号器和一小时 fallback delay 构造回归，验证父取消不再被备用启动延迟拖住。
- 核对 UDP 批量写入的 message-too-long 语义：单个包过大后继续尝试后续包，其他错误则停止并释放仍持有的剩余缓冲区。
- 补充连接与非连接批量写入的直接回归；对没有发现生产缺陷的 SOCKS 批量路径保留已有实现。

### 平台与协议修复

- 修复 Apple TLS 引擎的连接泄漏，以及旧版 Android 连接所属进程查询。
- 修复 IPv6 字面地址作为主机名时的处理。
- 更新内置证书，修复配置格式化输出与部分无选项路由动作的序列化。
- 修复 Tailscale SSH 用户切换的权限检查及其构建标签。

### 底层依赖整合与行为核对

- 对 sing、sing-tun、QUIC 和 Cronet 的多条分支进行整合，核对上游已吸收内容与 fork 仍需保留的修复，再将主模块和测试模块固定到一致版本。
- 将暂停等待改为可取消的生命周期操作，并验证 WireGuard 暂停状态能传递至实际 bind，而非只更新上层标志。
- 同步 TCP FIN 窗口、keepalive、已接受连接保留和弱主机绑定相关修复，补充 Linux 内核与 TUN 场景的验证入口。
- 对 TUN 快速路径进行资格判断和真实载荷验证，检查 FakeIP、跟踪、显式禁止等条件；分别记录绕行决定与实际复制路径的证据。
- 修复规则集更新器关闭测试对在途请求的假设，并补充网络命名空间、DNS / Tailscale、Apple 文件描述符泄漏和 Android procfs 格式的回归检查。
- 区分端到端载荷验证、平台专项测试和仍受环境限制的验证项，保留各自适用范围。

### 依赖与兼容性

- 更新并固定 sing、sing-tun、sing-quic、quic-go、uTLS、Cronet 和桌面客户端相关引用；Cronet 的根模块、平台库和聚合模块保持在一致的构建代次。
- 本次包含来自上游及 fork 自身的修复。不同平台的构建标签、内核权限和设备环境仍会影响实际可用能力。

### Fast Path 从平台判断收敛到消费能力

- 新增由入口报告的 `ConsumesBypassVerdict`，隐式 Direct Fast Path 仅在调用方能实际执行 bypass 时判断；当前对应 nfqueue / auto_redirect。
- 同一 Linux 二进制运行普通 TUN 时也不评估这条无效分支；不以操作系统常量代替入口运行模式。
- 显式 bypass 规则和 L0 路由地址集合保持自己的行为边界，不被隐式优化开关重新解释。
- 修复显式 `fast_path: false` 被 direct 透明链固有授权覆盖的问题，按外到内的首个显式策略作决定。
- 移除不能改变任何现有生产形态判定的 AI 标签授权回退；`traffic_class` 仍独立使用业务分类。
- 保留透明链、叶子能力和逐流资格检查，并以生产形态等价性、真假条件和模式边界测试约束行为。

[Fast Path 与内存管理的当前设计、源码和配置边界](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FAST-PATH-AND-MEMORY.md)。

### 为什么依赖更新需要跨仓库核对

这次整合覆盖 sing、sing-tun、sing-quic、quic-go、uTLS 和 Cronet 等维护线。除了更新固定版本，还需核对模块路径、主模块与测试模块、Go / Native ABI、客户端引用和内核中的实际调用。上游历史发生重写时，也要比较补丁和行为，不能仅凭 SHA 不同认定某项修复缺失。见[依赖整合说明](docs/fork/UPSTREAM-DEPENDENCY-INTEGRATION.md)与[子仓库记录](KNOWLEDGE/modules/child-repositories.md)。

各依赖影响的路径也不同：sing 提供拨号、socket、缓冲区和复制等基础能力；sing-tun 涉及 TUN 栈、NAT 和回包；sing-quic 与 quic-go 影响 QUIC、HTTP/3、数据报和包大小；uTLS 涉及指纹与握手兼容；Cronet 则有 Go / Native ABI 和平台原生库。某一处编译通过，不能替代其他模块、原生资产或客户端调用点的核对。

## [v0.1.24](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.24) — 2026-10-22

本版改善 Direct TCP 接管和半关闭后的存活观察，让对端离开后的资源可以回收，同时保留连接交接的原有职责。

### 连接生命周期

- 对半关闭连接观察对端状态，对端已离开时释放残留流。
- 为交接后的 Direct 连接补充观察，不依赖该路径中不存在的普通复制循环。
- 整理观察状态、释放结果与定时器所有权，增加相应回归覆盖。

### 交接后的 Direct TCP 存活

- 被下层接受接管的流没有普通复制循环，因此不能等待复制方向退出后才启动半关闭观察。
- 使用已有 TCP keepalive 状态机观察对端，而非另加一个可能抢走数据的读取者；对端失效后走原有 detach、release 与统计关闭链。
- 配置静默与探测参数来缩短残留连接等待，同时保持正常活跃连接和可响应探测的空闲连接。

[Fast Path 与内存管理的当前设计、源码和配置边界](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FAST-PATH-AND-MEMORY.md)。

## [v0.1.23](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.23) — 2026-10-21

本版收紧 ShadowTLS 认证前连接和握手目标的资源边界，避免异常连接长期占用资源。

### ShadowTLS

- 为认证前读取增加超时边界，减少异常连接长期占用资源。
- 握手目标连接超时按实际故障处理，避免被误判为普通探测。
- 释放异常握手路径中 SDK 遗留的目标连接。
- 保留已接受连接的能力接口，避免包装后丢失后续处理所需的信息。

## [v0.1.22](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.22) — 2026-10-21

本版增加可选的 Naive H3 到 H2 回退，并改进 MASQUE 数据报、TUN 回包、启动校验和协议关闭路径。

### Naive H3 → H2 回退

- 新增可选 HTTP/3 到 HTTP/2 回退，为后续 H2 尝试保留调用者的剩余时间预算。
- 完成 CONNECT 握手后再交付连接，准确报告握手失败。
- 该能力用于主传输故障恢复，需要显式配置。

### MASQUE 与资源关闭

- CONNECT-UDP 使用数据包所有权发送路径，减少不必要的复制。
- 更新 sing-tun 数据返回路径及关闭边界相关修复。
- 关闭作用域时及时释放 MASQUE 隧道客户端。
- SSH 接口变化时在同步保护下退役旧会话。

### 回包路径的分配优化

- sing-tun 常见的单写回路径直接整理调用方切片，不再每次构造返回分组及分组内的临时包切片。
- 仅在存在被丢弃或传回的数据间隙时压缩切片，完整写回批次保持输入不变，修复首版优化扩大写入范围的问题。
- 本轮真实 dispatcher + portNAT 基准中，单包由约 236–238 ns、72 B / 2 次分配变为约 156 ns、0 B / 0 次分配；多包批次也检查了零临时分配边界。
- MASQUE L4 CONNECT-UDP 单包接入 owned DATAGRAM 路径，移除该边界的一次载荷复制与一次分配，并验证成功、拒绝、过大和回退情况下的所有权。
- 关闭过程另做真实静默 QUIC 路径测量，区分受 idle timeout 限制的等待与死锁；未将没有证据需要修改的关闭语义作为“性能修复”。

### 启动校验

- 区分能力不匹配警告与致命结构错误；已存在 detour 的能力问题不再一律阻止启动。
- 警告不承诺尚未发生的启动结果，修复校验日志与状态快照的并发访问。

### Naive 回退字段

放在 Naive 出站对象内：

```json
{
  "type": "naive",
  "tag": "naive-out",
  "server": "proxy.example.com",
  "server_port": 443,
  "username": "user",
  "password": "替换为密码",
  "quic": true,
  "quic_fallback_to_h2": true,
  "tls": {
    "enabled": true,
    "server_name": "proxy.example.com"
  }
}
```

回退要求 quic: true 及对应平台的 Naive / Cronet 构建。[更多配置示例](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FORK-CONFIGURATION.md)。

### 数据路径的复用边界

MASQUE CONNECT-UDP 接入已在 CONNECT-IP 数据面使用的拥有数据包所有权的发送能力，目的是减少明确存在的复制。批处理和零拷贝都依赖具体接口及缓冲区交接，不能推广为所有协议都走同一发送路径。TUN 回包优化与此分别发生在共享返回路径。

## [v0.1.21](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.21) — 2026-10-20

本版引入 Mieru 入站和出站及可选的 TCP / UDP 回退，同时推进 IPv6 入口、跨协议故障识别、MASQUE、DNS、规则集和数据路径修复。

相比 v0.1.20，本次累计包含 **116 个提交**。

### 新增 Mieru 协议

- 基于官方 **Mieru SDK v3.38.1**，新增原生客户端 Outbound 与服务端 Inbound。
- 支持 TCP、UDP、Both 传输模式，以及 Multiplexing、Traffic Pattern、端口范围、MTU、握手模式等配置。
- 兼容 mbox 风格的主要配置字段及官方枚举名称。
- 接入内核统一 DNS、Dialer、Listener、路由与连接生命周期，避免建立独立的网络管理体系。
- 支持 IPv4、IPv6、FQDN 目标地址、域名服务器解析以及 UDP 数据转发。
- 新增可选的 **TCP ↔ UDP 自动回退**：主传输出现可确认的故障时，为新连接尝试备用传输。
- 自动回退支持冷却时间、指数退避、单飞恢复探测及网络切换后的代际隔离。
- 回退功能默认不影响现有单 TCP、单 UDP 配置；未开启时不创建额外的备用运行时。

#### Mieru 专项修复

- 修复 UDP 数据帧解析可能越过声明长度的问题。
- 修复并发读取共享 UDP 帧解析器的数据竞争。
- 修复 Listener、UDP Underlay 和异常启动路径中的 Socket 资源泄漏。
- 修复服务端关闭时偶发长时间等待的问题。
- 修复 Linux 下 UDP 不可达返回 EOF 时未正确触发回退的问题。
- 修复 DNS 解析失败、Socket 连接失败、身份验证失败等场景的错误分类。
- 修复请求取消、客户端关闭、网络切换过程中错误状态和回退结果的归属问题。
- 修复双传输均失败时有效故障证据可能丢失的问题。
- 优化首次拨号前的网络事件处理，避免无意义地重建 SDK 客户端。

已补充官方 mita 双向互通、Mihomo 对比、IPv6 回环、并发回退、短连接压力及生命周期测试。

### 自适应 IPv6 入口优化

- 新增 IPv6 入口能力判断，根据真实网络地址及路由信息决定是否向应用提供 IPv6。
- 在确认本地网络不具备可用 IPv6 条件时，可自动调整 TUN 的 IPv6 地址、路由及 DNS 设置。
- 对符合条件的本地 TUN AAAA 查询返回 NODATA，减少应用反复尝试不可用 IPv6 地址。
- 保留局域网 ULA IPv6 能力，避免因为没有公网 IPv6 出口而误伤本地通信。
- 网络状态未知或处于切换阶段时采用保守策略，避免错误关闭有效 IPv6。
- 统一多 TUN 入口的判断与执行，防止不同入口出现不一致的网络状态。
- 不需要入口 IPv6 管理的配置跳过相关检测，避免产生额外开销。
- 同步适配 Apple 客户端的 Network Extension IPv6 配置接口。

### 跨协议故障识别与代理组优化

- 新增轻量级 `FailureFacts` 机制，传递已确认的连接失败阶段与原因。
- 区分本地 DNS/接口问题、代理线路故障、目标服务器拒绝及调用者主动取消。
- 优化 LoadBalance 故障重试与节点惩罚策略，避免将目标服务器错误误判为代理节点故障。
- 优化 URLTest 健康状态维护，防止本地错误或测速目标故障错误清除有效节点记录。
- 保留既有超时保守策略：连接超时可以尝试其他节点，但不直接视为节点永久失效。
- 失败信息仅在错误路径使用，不为正常数据转发增加额外计时器、后台任务或全局状态。

### MASQUE 与 HTTP/3 修复

- 修复 CONNECT-IP 在 UDP 黑洞环境下长时间等待 HTTP/3，导致 HTTP/2 回退无法执行的问题。
- 为 HTTP/3 隧道建立设置独立时间窗口，避免耗尽整个连接的超时预算。
- 修复 QUIC 连接已建立、但 CONNECT-IP 响应较慢时，被错误记录为 HTTP/3 不可用的问题。
- 改善不同连接尝试结果的先后顺序处理，避免旧的失败结果覆盖新的有效判断。
- 新增真实 HTTP/3 CONNECT-IP 回环数据测试，验证隧道建立后能够正常传输数据。
- 优化批量 DATAGRAM 的数据包所有权与分块处理，避免大批次处理中的额外资源占用。

### 规则集与内存优化

- 优化大型二进制规则集的 Slice 扩容方式，减少加载阶段的反复分配与数据复制。
- 无缓存模式下，远程 SRS 规则集支持直接流式读取和解析，避免完整下载缓冲。
- 修复规则集 HTTP 响应截断时可能错误接受并发布新规则的问题。
- 修复 SRS 解析未完整验证 Adler-32 校验尾部的问题。
- 增加解压工作量上限，防止异常规则数据引发过量内存分配。
- 改善下载取消、异常尾随数据和解析失败时的处理，确保旧规则集继续有效。

#### 实测优化数据

同一测试环境、相同测试样例下：

项目 | 优化前 | 优化后
-- | -- | --
100 万域名加载累计分配 | 42.5 MiB | 20.6 MiB（−51.5%）
100 万域名稳定驻留内存 | 8.3 MiB | 7.4 MiB（−10.8%）
100 万域名加载期 GC 次数 | 10 | 5（−50%）
10 万域名加载累计分配 | 4.1 MiB | 2.9 MiB（约 −29%）

本次优化重点减少规则集加载时的临时内存开销，而不只是压缩最终驻留内存。

### DNS、QUIC、Hysteria2、TUIC 与 TUN 优化

- **DNS**：修复异步劫持查询跨 Goroutine 传递时引用可复用缓冲区的问题，避免报文被后续读取覆盖。
- **DNS**：移除劫持查询中的一次重复报文解析，减少每请求的分配和处理开销。
- **QUIC**：及时释放已取出的 DATAGRAM 接收队列槽位引用，减少无意义的内存保留。
- **QUIC**：优化小型数据帧的缓冲区尺寸，避免较小的帧长期占用过大的池化缓冲区。
- **Hysteria2 / TUIC**：优化 Owned DATAGRAM 发送路径及缓冲区所有权移交。
- **TUN / MASQUE**：优化数据包跨接口传递过程，减少一次不必要的数据复制。
- **QUIC**：新增接收 DATAGRAM 丢弃计数，改善拥塞和队列异常排查。
- **UDP NAT**：新增队列已满及会话关闭后的数据包丢弃统计。
- **底层生命周期**：强化数据包 Close/Flush 的并发序列化，避免清理与发送相互竞争。

相关依赖分叉 `sing`、`quic-go`、`sing-quic`、`sing-tun` 已同步更新，并通过主仓库的依赖引用纳入正式构建。

### 规则集与数据包所有权的实现细节

- 规则集计数切片改为有界的几何增长，避免逐元素扩容的累计分配，同时不直接信任远端声明数量一次性申请超大空间。
- 解压工作量增加 128 MiB 预算，保留 EOF、校验和与截断检查；无缓存下载直接进入解析，失败时继续保留旧规则集。
- DNS 劫持在跨 goroutine 前取得消息所有权，少做一次报文解析，避免原缓冲区复用后查询 ID 或内容改变。
- MASQUE 大批次数据按不超过 32 包的块处理，覆盖部分拒绝时剩余数据的所有权，避免释放或重发已移交部分。
- QUIC 接收队列取出元素后清空槽位；对明显过大的小型排序帧缓冲按条件缩小保留空间。
- Hysteria2 / TUIC 使用 owned DATAGRAM 路径，TUN / MASQUE 端口交接也接入数据包所有权能力。
- 补充 QUIC 接收丢弃、UDP NAT 队列满及会话关闭后的丢弃统计，便于观察资源与队列异常。

### 测试与质量保证

- 增加 Mieru 客户端、服务端、TCP/UDP 自动回退及官方协议互通测试。
- 增加网络切换、代际失效、并发关闭、错误分类及资源释放的对抗性测试。
- 增加 SRS 截断、校验损坏、解压边界和流式下载取消测试。
- 增加 MASQUE HTTP/3 建连、回退及真实数据传输测试。
- 增加 DNS 缓冲区复用、数据所有权及并发处理回归测试。
- 完成核心 Verify、Race 检查、Android ARM64 Core 和移动端构建合约相关 CI 验证。


### Mieru 回退字段

放在 Mieru 出站对象内：

```json
{
  "type": "mieru",
  "tag": "mieru-out",
  "server": "proxy.example.com",
  "server_port": 8443,
  "username": "user",
  "password": "替换为密码",
  "transport": "tcp",
  "fallback": {
    "enabled": true,
    "transport": "udp",
    "server_port": 8444
  }
}
```

服务端须监听相应 TCP / UDP 端口；主备不能使用 both 或 no_wait。[更多配置示例](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FORK-CONFIGURATION.md)。

### 已知限制

- Mieru SDK 的部分握手等待仍受官方实现限制，调用者取消不一定能够立即终止内部等待。
- 部分数据路径优化已通过单元测试和微基准，但尚未完成所有平台的真实网络吞吐验证。
- Apple/Android 真实设备的内存占用与功耗改善尚未形成完整对比数据。
- Android 规则集 mmap 优化未在本版本启用。

### Mieru 与共享核心的关系

集成使用官方 SDK 的协议实现，项目的工作重点是把它的监听、拨号和解析接入内核，并维护 SDK 对象、UDP 会话与网络切换的所有权。启用回退时，两种传输各有独立 Runtime；偏好变化只影响新会话，不迁移已有会话。

Mieru 的错误包装也推动了 `FailureFacts` 的接线：在拥有类型化错误的拨号边界记录事实，再供代理组判断是否需要重试或降低成员权重。历史互通与微基准的环境、对照和未覆盖项见 [Mieru 集成报告](docs/fork/mieru-integration.md)和[最终专项验收](docs/fork/mieru-masque-final-acceptance.md)。

故障归因直接影响回退是否合理：代理服务器的 socket 被确认拒绝，可以作为路径故障证据；代理已经连通但最终目标拒绝，并不等于代理节点失效。本地接口不可用、DNS 解析失败、用户取消和缺少上下文的 EOF 也各有不同处理边界。遇到无法确认的原因，应保留未知状态，而不是根据 SDK 的错误文字猜测来源。

历史 Windows / Ryzen 4800H 环境中的 1200 字节 UDP 帧微基准，记录了适配层写入约 118.8 ns、64 B 分配；对应官方 SDK 包装路径约 666.1 ns、1,280 B 分配。后续并发审计又为同一 Association 的读取暂存增加互斥，接受少量微基准成本以保证正确性。这些是指定帧处理路径的测量，不是完整 Mieru 网络吞吐对比。

## [v0.1.20](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.20) — 2026-10-18

本版整理单条流从路由、解析到拨号的地址族准入：为一次执行捕获一致的策略，并让流与数据包路径遵守同一网络事实。

### 单流候选准入

- 为每条流捕获一份不可变的候选准入策略，并将它传递到流连接和数据包连接的各条处理路径。
- 域名解析与字面地址、串行与并行拨号、已连接与未连接 UDP 使用一致的策略边界。
- 修复未连接 UDP 域名分支未应用准入策略，以及串行分支重复捕获策略的问题。
- 连接建立被取消后，不再启动迟到的新候选；已启动的工作仍由对应生命周期负责清理。
- 网络变化发生在流处理期间时，避免同一次决策前后读取到不同代次的网络事实。

### 验证与维护

- 补充候选策略矩阵、取消交错和网络变化相关回归测试，并修复测试夹具中发现的数据竞争。
- 更新与网络能力、DNS 缓存及资源回收相关的知识库和测试记录。

### 客户端品牌

- 统一 macOS 应用、Windows 客户端和服务的 JiejieBox 名称。

### 全路径接入与交错验证

- 候选准入策略从流的起点贯穿解析、候选生成、连接竞争和交付，不再由各分支分别推断当前网络。
- 覆盖 TCP、已连接 UDP、未连接 UDP、域名与字面地址，并对串行和并行路径进行一致性检查。
- 使用可控交错测试驱动网络从 A 切换到 B，检查旧策略、缓存边界和资源回收的实际结果。
- 对取消发生在候选生成、等待和启动边界的情况增加屏障测试，修复“取消后仍启动迟到候选”的行为。
- 处理测试夹具中的六处数据竞争，并在报告中区分测试代码修复与生产行为修复。

### 策略、候选与生命周期对齐

- 将配置地址族策略、本地网络能力和网络归属封装为一份流级决定，后续分支读取同一份事实。
- 对 DNS 结果发布、候选接纳、实际启动和连接交付分别检查边界，避免旧结果被新网络接收。
- 补充跨阶段矩阵与确定性交错测试，检查流在网络 A → B 变化中不会前半段遵循旧策略、后半段又重新捕获新策略。

### 双栈策略的一次读取

地址族策略需要在同一条流的解析和拨号过程中保持一致。网络变化可能与异步解析、候选启动和取消交错；因此捕获一份准入策略、明确其网络代次，再由各执行分支使用，比每个候选重新读取全局状态更容易保持一致。设计演变见[双栈跨组件审计](KNOWLEDGE/reports/core/DUAL_STACK_CROSS_COMPONENT_ARCHITECTURE_AUDIT_2026-10-20.md)。

## [v0.1.19](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.19) — 2026-10-17

本版减少设备休眠期间不必要的 DNS 和 Dashboard 网络活动，让后台工作遵守设备状态。

### 后台活动

- DNS 乐观后台刷新遵守设备休眠状态，避免无业务时唤醒网络。
- Dashboard 后台下载按设备状态延后。
- 修复 DNS 休眠状态来源的初始化顺序，确保构造阶段取得正确管理对象。

## [v0.1.18](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.18) — 2026-10-17

本版处理复杂代理链和网络切换中的能力传递、双栈候选、路由与资源归属，避免一层的状态被误用于另一层。

### 代理链能力与资源所有权

- 将能力读取限制在正确的跳点边界，避免一个出站声明的能力被通用接口误当成底层拨号器能力，影响 WireGuard 等协议绑定。
- 对真实 Direct、普通流连接与数据包连接补充生产对象组合测试，验证能力经过包装后仍可到达正确实现。
- 网络切换中一次候选计划只捕获一次归属，避免逐候选重新读取而跨越两个网络代次。
- 对规则集启动、重载、更新器和关闭交接增加生命周期接线测试；修复发现的并发读写。
- 改进嗅探结果与路由报告，使“未运行”“未匹配”“需要更多数据”等状态可区分，便于追查真实处理路径。

### 双栈候选与拨号策略

- 对域名解析、字面地址、并行连接和数据包拨号应用一致的 IPv4 / IPv6 策略，避免某条入口遗漏限制。
- 修复严格地址族策略下，候选列表已排除某个地址后又被重新加入的问题。
- 地址族恢复过程只能尝试策略允许、并且当前具备拨号条件的地址族。
- 连接竞争结束后停止并回收相关恢复任务；尝试返回错误但同时返回连接时，正确关闭该连接。
- 限制地址族健康记录的规模，并改进日志中对实际策略、拒绝原因和网络状态的说明。

### 网络代次与代理链

- 一次候选计划使用同一份网络状态和归属快照，防止网络切换期间把两个代次的信息混合使用。
- 修复经由出站跳点读取地址族能力时过早停止的问题；不跨越 detour 错误推断本地网络能力。
- 网络切换时统一相关状态更新的时序，避免拨号和路由分别看到不一致的过渡状态。
- 改进 Direct 出站的能力接口使用，对无法识别的拨号选项采取保守处理。

### 路由、代理组与嗅探

- 修复故障切换代理组丢失逐流拨号选项的问题。
- 已连接 UDP 会话继续使用其指定的网络策略，避免后续操作意外改变路由行为。
- 明确区分嗅探未匹配、需要更多数据及其他结果，修复流量类型和日志中的错误标识。
- 修复规则集并发访问、解析拨号器能力断言和其他可能导致异常退出的边界。

### 使用影响

使用严格 IPv4 / IPv6 策略或多层代理链的配置会受到这些修复影响；当策略排除了所有可用地址时，连接应明确失败，而不是绕过策略选择另一个地址族。

### 单次网络观察与能力边界

- 在生成候选计划时一次捕获地址族能力、稳定状态和归属信息，避免同一流因连续读取混入不同网络代次。
- 修正把代理跳点能力当作本地拨号能力的接口设计，保留真正拥有 socket 的一层职责。
- 地址族恢复任务由创建方停止并等待；失败时同时返回的连接也要关闭，避免隐藏资源泄漏。
- 逐流拨号选项穿过故障切换组时继续保留，避免重试改变用户要求的接口或策略。

### 快速路径与正常拨号保持一致

- 使用有效地址族策略，而非仅看出站自己是否设置 resolver，防止绕行重新允许普通拨号已禁止的字面地址。
- 将本地能力、全局绑定 / 路由策略与目标范围纳入对应判断，避免用户态与绕行路径遵循不同的网络限制。
- 未分类选项即使当前是零值也拒绝 profile，覆盖默认即生效的字段；未构造的 profile 同样保守拒绝。
- 运行中的功耗报告也是流量观察消费者，不能仅检查 tracker 后就绕过其统计。

[Fast Path 与内存管理的当前设计、源码和配置边界](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FAST-PATH-AND-MEMORY.md)。

### 链式代理中的能力归属

代理组的控制选择与数据经过的物理跳点并不相同。某个包装出站声明的能力不能自动当成底层 socket 的能力；DNS 解析归属、接口绑定和候选地址也需要沿实际路径传递。路径预览只用于诊断，不能代替一次真实拨号。见[代理路径与启动校验](KNOWLEDGE/modules/path-selection-validation.md)。

## [v0.1.17](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.17) — 2026-10-17

本版保存和识别退役 FakeIP 地址范围，并修复 Windows 客户端安装与旧服务清理中的问题。

### FakeIP 与拨号

- 保存退役 FakeIP 地址范围，使进程重启后仍可识别相关占位地址。
- 退役范围按实际签发游标约束，避免将整个未签发范围误当作已签发地址。
- 对无法确认归属的旧占位地址进行保护，防止直接发往代理对端。
- 修复字面地址恢复路径的入口接线。

### 客户端

- 按程序声明的身份区分客户端，修复安装 ID、64 位注册表视图和安装布局识别。
- 修复守护进程工作目录和旧服务清理。
- 修正 Apple 发布时的客户端源码选择。

## [v0.1.16](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.16) — 2026-10-17

本版汇总 v0.1.10 后的多项改造：本地 DNS 广告拦截、双栈与网络状态修复、XHTTP 性能和故障验证，以及 Windows 安装与共存处理。

### DNS 本地广告拦截

- 新增 DNS `adblock` 规则动作，支持域名条件与规则集匹配，命中后直接本地返回带 SOA 的 NXDOMAIN。
- 拦截不访问上游，客户端可按负 TTL 缓存；规则动作无需额外的伪地址或丢弃模式。
- 将本地策略结果与上游解析错误区分，避免正常的广告拦截被当作服务器故障。
- 增加配置读取、序列化、schema、终止动作与 DNS 规则边界测试。

### 双栈、DNS 与监听

- 入站监听使用明确的地址族绑定方式，改善 IPv4 / IPv6 监听行为的一致性。
- 只看到网络接口并不足以证明某个地址族可用；网络能力判断同时考虑实际地址条件。
- DNS 地址族过滤按每个调用方分别应用，避免一次请求的过滤条件污染共享缓存中的完整结果。
- 被路由策略明确阻止的 DNS 查询作为策略结果处理，而不是简单归类为普通解析失败。
- 网络重置重新观察当前网络状态，避免沿用已经过期的旧判断。

### FakeIP 与休眠状态修复

- 损坏的持久化 FakeIP 游标返回解析错误，避免 panic 扩散到缓存事务并删除整个缓存文件，连带损坏无关缓存和持久化选择。
- 修复设备状态观察者列表的并发访问，以及通知过程中新增观察者永久错过复用边界的问题。
- 通知回调使用独立列表快照；补发有次数上限，避免平台线程陷入无限通知。

### 连接、协议与资源释放

- 修复路由运行上下文并发发布、关闭标志设置时序及网络切换过程中的竞态。
- 恢复 WireGuard endpoint 的暂停注册，修复设备休眠相关状态传递。
- 改进 Windows 上 TUN 读取错误、Socket 错误以及 AnyTLS / ShadowTLS 对端关闭的分类。
- XHTTP 上传响应停滞不再被当作真实业务数据持续流动。
- 修复拨号、路由组合使用时的能力判断与平台适配问题。

### XHTTP 性能与真实故障验证

- 修复首轮上传握手被聚合等待窗口额外延迟的问题，首轮交换跳过可选等待，后续数据仍保留聚合以维持上传吞吐。
- 对真实参考服务器执行 packet-up、stream-up、stream-one、auto、REALITY / Vision / encryption 及 H3 相关互通组合。
- 本轮记录了干净链路、5% 数据报丢失、30 ms 附加延迟、周期中断与永久中断五类 H3 故障场景，并检查恢复、载荷一致性与任务数量。
- 长时混合负载记录为 70 分钟、681 轮、0 次失败，并包含故意终止参考服务器后的恢复检查。这是该轮环境与源码的记录，不代表所有平台的统一成绩。
- 修复测试工具 ping-pong 模式未保留 deadline 导致测量本身挂起的问题，给长测加入 watchdog，区分工具故障与客户端故障。

### Windows 共存、订阅与安装生命周期

- 隔离应用身份、数据位置、服务状态与命名管道等共享资源，减少与其他安装互相覆盖。
- 对旧服务清理补充所属安装身份判断；保留与签名校验相关的程序名和兼容约定。
- 接入订阅用量信息与 Dashboard 展示，区分配置正文请求与用量信息请求。
- 安装器 / 卸载器记录其实际身份和生命周期点，修复诊断通道、路径与静默提示问题，使失败可以定位到真实步骤。

### Windows 安装与卸载

- 调整静默安装及清理提示，避免无人值守安装被交互提示阻塞。
- 改进服务停止、安装目录和卸载数据清理过程中的状态检查。
- 服务无法停止时，卸载流程不再静默报告成功。

### Apple 与构建

- Apple 构建支持从发布标签或 detached HEAD 选择对应源码，统一 Libbox 归档名称。
- 修复跨平台构建和验证脚本中的平台假设，并将对应检查纳入构建流程。

### 组合场景修复与验证工作

- 对网络重置、地址族判定、路由关闭和协议错误分类进行组合审查，修复单独运行时不易暴露的交接问题。
- 修复运行上下文的数据竞争；路由关闭先发布关闭状态，再执行各项资源释放，约束迟到操作。
- 为 Windows 常见 Socket 错误、TUN 读取错误及对端生命周期补充实际平台分类，避免沿用其他系统的错误常量。
- 通过安装与卸载状态探针定位静默提示阻塞、服务未停止却返回成功等问题，并将错误反馈接入产品流程。
- 修复 Darwin 测试依赖宿主机网络环境、Linux 断言和跨平台构建假设，减少环境差异掩盖产品问题。

### adblock 配置与 TTL

```json
{
  "dns": {
    "rules": [
      {
        "rule_set": "anti-ad",
        "action": "adblock"
      }
    ]
  }
}
```

anti-ad 须为已定义规则集，也可直接写 domain_suffix。命中返回 NXDOMAIN + SOA，TTL / MINIMUM 为 600 秒，不查询上游；没有 adblock.ttl 子字段。[更多配置示例](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FORK-CONFIGURATION.md)。

### XHTTP 性能记录的时间边界

v0.1.6 已包含 XHTTP 客户端的初始集成；之后的 packet-up 聚合、对抗测试和长时间负载验证发生在更晚阶段，不能全部归入 v0.1.6。历史 TLS / HTTP/2 受控测试中的单流上传由 2.12 MB/s 提升至 26.8 MB/s；该数字只反映当时的参考服务端、环境和负载。背景及反证见[工程评估的 XHTTP 章节](KNOWLEDGE/reports/core/JiejieBox_工程架构与改造最终评估报告_2026-11-01.md#九xhttp移植跨层修复与专项性能重构)。

这次优化定位到的成本并非单纯的系统调用次数：旧的 packet-up 写入可能分别提交 POST 并等待响应，使首包和连续小写入反复承担往返及间隔。改造后的有界队列与单个 flusher 聚合有序数据，同时保留背压和失败后的中止语义。历史收尾记录还包括约 70 分钟、681 轮混合负载，以及参考服务端中断、H3 延迟和丢包等检查；这些结果不能代替当前版本在真实设备和公网中的长期验证。

## [v0.1.10](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.10) — 2026-10-15

本版修复透明代理组对 Direct Fast Path 授权的传递，并完善 Windows 守护进程的信任检查。授权传递后仍需逐流判断。

### 路由

- 直连经过透明代理组时保留快速路径授权，随后继续执行原有逐流资格判断。

### Windows

- 调整 WinTrust 时间错误处理，避免过期 CI 测试证书阻止内核启动。
- 增加篡改、未签名、证书用途和签名者固定等边界测试，并接入 Windows 测试环境。

### 透明包装与实际拨号语义

- 修复 direct 被纯选择型组包装后，因组未声明额外策略而失去固有授权的情况。
- 只允许没有额外拨号 / 重试语义的透明选择链，不将所有“最终指向 direct”的复杂组都视为等价。
- 这一修复恢复的是考虑资格，原有 FakeIP、绑定、统计及其他拒绝条件继续执行；显式 false 的优先级问题在后续版本继续修正。

[Fast Path 与内存管理的当前设计、源码和配置边界](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FAST-PATH-AND-MEMORY.md)。

## [v0.1.9](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.9) — 2026-10-14

本版将发布资产清单、草稿发布和完整性检查的顺序收拢，减少正式页面出现缺失或错误资产的机会。

### 发布流程

- 以统一清单约束公开内容，在输出发布参数前检查完整集合。
- 先创建草稿、完成上传，再进入公开发布流程，减少不完整版本被提前公开的情况。

## [v0.1.8](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.8) — 2026-10-14

本版为 selector 和 URLTest 增加可选的 `fast_path` 策略，并完善 Apple 客户端源码选择和多平台构建。策略允许尝试不等于流量已经发生内核绕行。

### 代理组 Fast Path 策略

- 为 selector 和 URLTest 代理组增加可选的 `fast_path` 布尔字段，用于表达通过该组的流量是否允许尝试既有的 Direct Fast Path。
- 设置为 `true` 时，允许符合条件的流量尝试快速路径；设置为 `false` 时明确禁止；不设置时沿用自动检测。
- 该字段表达的是代理组的授权，不会把组内代理节点变成直连节点，也不会绕过原有的逐流资格判断。
- FakeIP、流量跟踪及其他不满足快速路径语义的情况，仍由路由器按原有条件处理。
- `fast_path` 是本 fork 的配置扩展；使用该字段的配置需要对应版本的 JiejieBox 内核。

### Apple 与多平台集成

- 以 Apple 客户端仓库作为对应客户端源码来源，统一一次构建中使用的源码选择，并记录实际来源。
- 修复源码选择、客户端构建和发布脚本中的路径、换行及依赖引用问题。
- 改进 Windows 服务启动时对证书时间错误的处理，以及 Android 和桌面客户端的构建集成。
- 完善构建结果与对应源码版本的关联检查，减少版本与构建内容不一致的风险。

### fast_path 的放置位置

加入 outbounds 中的 selector / urltest 对象：

```json
{
  "type": "selector",
  "tag": "direct-group",
  "outbounds": [
    "direct"
  ],
  "fast_path": false
}
```

direct 须为已有出站。后续版本调整过默认授权与 false 优先级，请按对应版本使用。[更多配置示例](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FORK-CONFIGURATION.md)。

### 组级策略与热路径成本

- 通过配置策略表按当前流的逻辑链读取授权，保持实际出站对象类型，避免为配置开关增加包装而破坏能力解析。
- 授权与实际资格分开：组允许尝试不能让代理叶子具备 direct 能力，也不能跳过路由与 socket 检查。
- 策略在建立流时解析，不作为节点全局标志，也不进入逐包读写循环。

[Fast Path 与内存管理的当前设计、源码和配置边界](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FAST-PATH-AND-MEMORY.md)。

### 授权与实际执行不是同一步

`fast_path` 是代理组对逐流尝试的策略，不会把代理节点转换成 direct；是否具备资格、入口是否能消费 bypass、是否接管连接以及是否实际发生 splice，各自需要检查。普通 TUN 接收到 bypass 判断，也不等于系统已在内核中绕行。

## [v0.1.6](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.6) — 2026-10-12

这是一次贯穿入站、路径证明、代理链、协议传输和客户端的综合改造。XHTTP、REALITY、DNS、MTU、网络恢复与资源生命周期共同纳入验证。

### 入站到传输的全链路接线

- 将真实 TUN 收包、路由、目标解析、出站与回包串起来验证，覆盖 TCP 拒绝 / Reset、黑洞连接、UDP 过期与重建、IPv4 分片重组及 ICMP 包过大反馈等路径。
- 将尚在建立的连接纳入网络重置与关闭管理，避免它们因为还未进入活动连接表而逃过取消。
- 保持嗅探元数据与真实目标地址的语义边界，修复 WireGuard 握手被误判为 BitTorrent / uTP 的顺序问题。
- 处理协议 early-data、SOCKS 身份传递和 DNS 网络归属相交的边界，减少各层单独正确但组合后失效的问题。

### XHTTP 客户端传输

- 新增 XHTTP 客户端，支持 `packet-up`、`stream-up`、`stream-one` 和 `auto` 模式，并接入配置读取、传输注册和实际构建。
- 覆盖 HTTP/1.1、HTTP/2、HTTP/3 的传输选择，处理上传请求、连接复用及 packet-up 数据发送的生命周期。
- 将 XHTTP 纳入网络变化、休眠恢复和资源回收流程，修复旧连接池无法被回收路径触达的问题。
- 修复配置中的 URI 查询参数丢失、拨号所有权、连接复用测试和不同 HTTP 版本选择规则的一致性。
- 本次提供客户端能力；没有新增 XHTTP 服务端入站。

### REALITY 与 VLESS 兼容能力

- 支持 REALITY 使用的 `X25519MLKEM768` 混合密钥交换，处理当前 Xray 服务端对密钥份额和客户端版本的兼容要求。
- 增加 `tls.reality.key_share` 配置，支持沿用指纹、`classical` 和 `hybrid` 三种行为；无法满足混合密钥要求的指纹不会静默降级。
- 修复 REALITY 路径绕过 TLS 首包包装的问题，使配置的 TCP 分片和 TLS record 分片能够到达实际握手连接。
- 修复 REALITY 校验器、Session ID 认证及握手时间读取相关问题，并更新携带现代浏览器混合密钥份额的 uTLS 依赖。
- 新增 VLESS 客户端应用层后量子加密，处理加密握手、数据帧和重密钥相关逻辑；握手受拨号上下文和取消控制。
- 对 Vision 使用的私有接口增加检查，避免底层接口变化后静默产生不正确的数据路径。
- VLESS 加密的服务端解密不在本次支持范围内；浏览器指纹、服务器版本与网络条件仍需相互匹配。

### DNS 分组与故障恢复

- 新增 `group` DNS 服务器类型，支持 `stable`、`fastest`、`parallel` 三种模式，可作为路由目标或被其他 DNS 分组引用。
- `stable` 优先复用仍可用的成员；`fastest` 根据近期成功记录选取成员；`parallel` 对当前可用成员并发查询。
- 并发冷启动合并竞选扇出，避免多个请求同时触发重复全组查询。
- 所有成员均有失败记录时只进行一次生存尝试，避免断网期间大量请求成倍放大上游拨号。
- 成员错误与胜出记录使用有效期和容量限制，每成员记录上限为 64；状态随网络代次隔离。
- 调用者取消、休眠导致的未执行探测与服务器实际故障分别处理，避免误伤成员健康状态。

### 代理组有限故障切换

- 为负载均衡增加可选故障切换，在明确的路径失败后重新选择一个允许的备用成员。
- 重试受逐流预算约束，嵌套代理组共享预算，避免多层组合时反复放大拨号次数。
- 保留轮询、一致性哈希和粘性会话语义；故障筛选改变候选资格，不重新排列整个哈希空间，也不迁移已经建立的流。
- 节点惩罚按网络代次和有效期管理，成功连接可恢复节点，避免旧网络的失败永久影响新网络。
- 主动取消与部分目标拒绝不会直接视为代理线路失效；故障切换默认需要显式启用。

### 代理路径与启动前校验

- 引入 PhysicalPath / ControlPath 模型，分别描述实际数据经过的路径和控制决策顺序，为多层代理、detour 和代理组提供统一的路径视图。
- 启动前对可达路径进行只读检查，识别结构性依赖错误、循环引用、截断路径以及无法满足的路径条件。检查不会通过主动拨号来证明远端可用。
- 修复嵌套组和多分支路径中的顺序、编号与失败归属问题，避免一条未失败的路径掩盖另一条路径上的错误。
- 网络需求按实际可达的路由及路径边界判断，避免把某个无关出站的 UDP 能力要求强加给所有路径。
- 支持可由下游代理承担的目标域名解析，完善 HTTP CONNECT、SOCKS 等链路中的解析归属。
- 增加只读的逐跳路径状态视图，用于观察路径结构与失败位置；状态视图不代表对远端可用性的实时保证。

### 嵌套隧道 MTU 与数据包大小

- 为 WireGuard 等路径描述传输封装开销，并在已知下层隧道容量时约束上层数据包大小。
- 将已证明的路径容量用于 Hysteria2、TUIC 和 MASQUE 外层 QUIC 数据包限制，减少嵌套隧道因包过大而出现的黑洞风险。
- 对无法满足必要 IPv6 MTU 条件的路径进行保护，改进嵌套 WireGuard MTU 配置错误提示。
- 修复 WireGuard 指定监听端口、重绑定和网络代次交接中的边界问题；不让被撤销的重绑定操作占用新代次。

### TLS、QUIC 与 HTTP 嗅探

- TLS 嗅探不再为识别流量执行不必要的完整服务端握手，减少临时工作与分配。
- QUIC 嗅探只复制当前需要处理的数据包，对分片 ClientHello 的读取次数和长度增加边界，并及时释放缓冲区。
- HTTP 嗅探提前排除不可能的请求行，避免对明显不匹配的数据构造完整 HTTP 解析器。
- 修复异常输入、取消和重试路径中的缓冲区与任务生命周期问题。

### 网络切换、休眠与连接复用

- 建立初始网络环境后再完成启动；DNS 状态变化也有自己的环境代次，避免沿用已经失效的状态。
- 网络切换或恢复时取消过期的在途拨号，避免旧网络的结果被交给新网络使用。
- 改进休眠与唤醒边界、连接复用和 HTTP / XHTTP 连接池退役：能够排空的连接池按其生命周期处理，不可继续复用的旧池不再遗漏。
- 修复接口监控被新一代监控替代后的事件归属，以及并发网络重置的交接问题。
- 修复两个地址族拨号均已结束却仍可能等待的问题。

### 运行时协调与 TUN 栈迁移

- 新增运行时资源协调器，将网络代次、重建所有权和后台探测许可集中到共享边界，减少各协议独立恢复产生的重复工作。
- WireGuard 增加失效握手恢复，并避免后台测速唤醒已空闲或暂停的昂贵资源。
- 完成 TUN 向 Go 栈的迁移，退役 gVisor 栈及强制相关构建设置；补充实际数据包路径、黑洞、负载下关闭和 MTU 回归覆盖。
- 内存压力尚未达到临界条件时即可清理空闲资源；修复清理过程创建额外传输对象和 HTTP/2 阻塞写入导致关闭受阻的问题。
- Libbox 处理移动端返回值与诊断保留边界，配套迁移 Apple / Android 调用点，并固定能够匹配新接口的客户端版本。

### FakeIP、DNS 与协议修复

- 防止本实例签发的 FakeIP 占位地址被当作普通目标发送给远端代理。
- 修复将 FakeIP DNS 服务器用于不适用的目标地址解析的配置边界。
- 修复 XHTTP 请求 URI 中配置的查询参数可能丢失的问题。
- HTTP/3 可用性判断以较新的有效连接尝试为准，避免旧失败结果覆盖新结果；取消的尝试不应错误触发降级判断。
- 改进 MASQUE、TUN、规则集回调和 WireGuard 的资源所有权，关闭时释放对应资源。

### Libbox 与客户端接口迁移

- 调整 gomobile 返回值接口，以 StringBox 等配套形式处理结果帧边界，清理旧接口并限制诊断信息保留。
- 对应迁移 Apple 和 Android 调用点，固定能与新接口编译配套的客户端源码，并维护跨层接口检查。
- Android 的平台状态接入共享移动端策略；Apple 的屏幕、锁定和唤醒事件接入真正的设备状态发布路径。
- 区分平台状态快照与一次性唤醒事件，避免每次重读状态都制造新的恢复边界。

### 客户端与平台集成

- 增加 Windows 桌面客户端构建、品牌及守护进程集成，并修复更新程序寻找本项目安装包的问题。
- 改进 Apple 平台 Libbox 来源、设备状态传递、iOS / macOS 构建与品牌配置。
- 调整 Android、Windows 和 Apple 构建中的标签、路径及发布检查。

### 回归、互通与对抗性验证

- 增加 Xray 参考互通测试环境、REALITY 握手兼容与 XHTTP 模式 / 版本组合相关检查。
- 补充多跳路径、目标 DNS 归属、嵌套 MTU、循环依赖及只读状态的回归测试。
- 针对取消、网络切换、重绑定撤销和关闭交错补充测试，检查旧操作不能重新获得新网络资源。
- 对 QUIC 分片、HTTP 误识别、缓冲区所有权和任务泄漏增加边界测试，并修正若干原本不能有效捕获缺陷的测试断言。
- 各阶段报告记录了不同层级的验证结果；不把尚未完成的真实互通或设备验证表述为通过。

### 新增字段的配置入口

DNS 分组放在 dns.servers，再由 final 引用：

```json
{
  "dns": {
    "servers": [
      {
        "type": "group",
        "tag": "dns-group",
        "servers": [
          "dns-a",
          "dns-b"
        ],
        "mode": "fastest",
        "error_ttl": "2m",
        "win_ttl": "5m"
      }
    ],
    "final": "dns-group"
  }
}
```

dns-a / dns-b 须为已有 DNS。XHTTP 放在节点 transport，REALITY 策略放在 tls.reality：

```json
{
  "transport": {
    "type": "xhttp",
    "path": "/xhttp",
    "mode": "packet-up",
    "xmux": {
      "max_connections": "3"
    }
  },
  "tls": {
    "enabled": true,
    "reality": {
      "enabled": true,
      "public_key": "替换为服务端公钥",
      "short_id": "0123456789abcdef",
      "key_share": "hybrid"
    }
  }
}
```

这段合并到自己的节点，保留服务器、UUID、SNI 和指纹配置。[更多配置示例](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FORK-CONFIGURATION.md)。

### 复用可信度与连接池排空设计

- 区分短时休眠、可疑旧连接与应退役的闲置资源，将复用边界与普通 trim 分开。
- 对多路复用连接停止接新工作，并让已有流排空；避免立即关闭伤及活跃流，也避免把仍有流的旧会话当作路径存活证明。
- 回收范围覆盖出站、endpoint、DNS transport 与独立 HTTP client manager；使用实际需要的释放接口，修复因要求更宽接口而始终漏掉 HTTP 池的问题。
- 普通空闲回收不主动拨号或重置网络，网络变化与紧急释放仍保留自己的更强处理路径。

[Fast Path 与内存管理的当前设计、源码和配置边界](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FAST-PATH-AND-MEMORY.md)。

### 从 v0.1.5 到本版的架构演变

此前的设备状态与 Power Governor 为休眠和后台工作提供基础；本版进一步把网络 generation、恢复租约、Scope 关闭、资源退役和协议接入连接起来。它覆盖从入站到传输的完整调用链、代理链与启动检查、DNS 和代理组、TUN / MTU、Libbox 与客户端，而不是孤立地增加某一个协议。

报告中对 v0.1.5 到 v0.1.6 的历史比较约有 602 个可见提交，这一数量包含不同类型的工作和可能吸收的上游内容，不能解释成 602 项独立原创功能。后续 XHTTP 性能重构和长测发生在本版之后。背景见[工程评估的版本演变](KNOWLEDGE/reports/core/JiejieBox_工程架构与改造最终评估报告_2026-11-01.md#十八版本里程碑与-v015--v016)。

把这些改动放在一起看，可以看到几条相互依赖的工作线：TUN、嗅探和路由决定流量如何进入内核；PhysicalPath / ControlPath 与 DNS 归属约束多跳执行；双栈 Dialer、协议传输和 MTU 约束影响连接能否建立并交付数据；Scope、网络代次和恢复租约负责网络变化与关闭时的资源交接。它们的作用不是让所有协议共享同一个状态机，而是让各自的状态机遵守共同的执行边界。

## [v0.1.5](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.5) — 2026-10-07

本版引入结合设备状态与真实业务流量的低功耗管理，减少锁屏和后台阶段不必要的周期性工作，并区分活跃连接与可回收资源。

### 休眠与恢复

- 统一休眠状态机，由平台生命周期驱动，真实业务流量可推迟进入深度空闲。
- 延后休眠期间的远程规则集刷新与流量统计写入。
- 分批恢复后台检查和刷新，减少唤醒瞬间的集中任务。
- 屏幕关闭不再直接清空连接池；按空闲条件回收资源，保留仍可复用的连接。
- 修复过期截止时间回调干扰新状态，以及深度空闲无法按预期进入的边界问题。

### 低功耗策略与资源回收职责

- 将设备休眠、真实业务流量、深度空闲截止时间和后台工作许可统一到 governor，避免每个后台任务独立决定是否唤醒。
- 保留连接复用与空闲释放的区别：屏幕关闭本身不意味着清空所有连接；业务流量可以推迟深度空闲。
- 各类后台任务分批恢复，修复旧回调在新截止时间上继续生效的竞态，减少恢复风暴与重复工作。

[Fast Path 与内存管理的当前设计、源码和配置边界](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FAST-PATH-AND-MEMORY.md)。

### 低功耗管理的职责

设备暂停、真实业务流量和后台工作许可共同影响何时进入深度空闲。规则集刷新与统计等可延后工作应减少不必要唤醒；首次必要工作、活跃连接和仍需维持隧道的恢复观察不能一概停止。

## [v0.1.4](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.4) — 2026-10-07

本版改进网络切换后的分级排空、重连和 DNS 复用，分别处理较轻的路径变化与持续的网络中断。

### 网络切换

- 区分较轻的路径变化与持续网络中断，分级排空和回收旧连接。
- 设备暂停时避免因网络事件直接拆除会话，破坏性回收需满足持续中断条件。
- 将 UDP 数据包活动纳入存活判断，并区分内核持有连接与普通空闲连接。
- 重连使用等待与并发限制，减少集中拨号；旧路径扫描结束后停止周期唤醒。

### DNS 与 TUN

- 限制 DNS 串行传输的并发建连，避免切换网络后重复建立连接。
- 同步 TUN Forward NAT 的映射、分片和相关选项。

## [v0.1.3](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.3) — 2026-10-07

本版围绕上传调度、业务分类、负载均衡、URLTest、双栈 DNS 与拨号和 Direct Offload 展开，同时完善移动端低内存配置。

### 调度与选路的具体行为

- 上传整形覆盖内核管理的用户态上传路径，按配置速率持续执行；没有配置速率时不建立整形队列。
- 轮询仅在实际提交一次流选择时推进；一致性哈希使用目标身份维持亲和；粘性会话按源与目标维持有界记录。
- URLTest 支持预期状态码配置，测速记录按真实叶子节点及测试目标归属；组间复用同一份健康证据。
- 运行中的检查被取消、代理组关闭或某成员异常时，回收本轮任务，避免后台任务越过所属组的生命周期。

### 业务感知上传调度

- 新增上传流量调度器，可使用 `route.traffic_scheduler.upload_rate` 配置管理路径的上传速率。
- 新增 `traffic_class`，用于表达流量类别，使调度器能够区分不同业务的需求。
- 调整调度器在路由写入路径中的接入方式，处理并发流创建、速率变化、关闭和网络切换时被等待的流量。
- 上传速率整形需要配置；不设置速率时，不应理解为默认对所有上传流量施加限速。

### 负载均衡与节点测速

- 新增按流选择成员的 `loadbalance` 出站组，支持轮询、一致性哈希和粘性会话等策略。一个 TCP 连接或 UDP 会话的选路决定由该流保持。
- 改进 URLTest 的测速与健康状态处理，增加统一延迟测量方式、预热与强制重检相关逻辑。
- 嵌套代理组测速时，改进实际测量节点与测试目标的归属；成员检查失败或取消时释放本轮任务。
- 代理组状态展示不再为负载均衡组虚构一个固定的当前成员。

### DNS 与双栈连接

- DNS 解析增加地址族增量返回能力，较晚到达的 IPv4 或 IPv6 候选可以加入仍在进行的连接竞争，减少必须等待两种结果全部返回的情况。
- 改进字面地址恢复、候选连接取消与失败处理；已失效的候选连接不应在网络切换后交给新网络使用。
- TCP DNS 连接可以复用，同时对闲置连接数量和重建生命周期增加约束。
- DNS 缓存、反向映射和查询结果按网络环境及网络代次归属，避免旧网络上的结果覆盖新网络的状态。
- 优化名称级 NXDOMAIN 缓存和查询取消处理。

### 路由与直连路径

- Direct Fast Path 根据具体流、拨号选项和路由条件判断资格，并提供拒绝原因的诊断信息。
- FakeIP 占位地址、无法取得的进程信息等情况采用保守判断；快速路径不会自动绕过必要的路由语义。
- 修复 TUN、DNS 劫持、预匹配与完整路由匹配之间的选项一致性，补充 TCP / UDP splice 诊断。
- 修复 SOCKS5 空域名、入站认证和混合入站中已经缓冲的首包数据处理。

### 移动端与 Apple 客户端

- 调整 iOS Network Extension 的低内存构建与 GC 配置，并避免低内存标签意外影响 macOS。
- 改进 Apple 客户端品牌、更新入口、构建和签名配置。

### 配置兼容性

`loadbalance`、`traffic_class` 和上传流量调度属于本 fork 的扩展。使用这些字段的配置需要对应版本的 JiejieBox 内核，不能直接假定可被官方 sing-box 加载。

### 上传调度实验

在 4 条普通流竞争 2 MB/s FIFO 的受控实验中，按容量 85% 聚合整形后，高优先级消息的接收端 p99 延迟从 146 ms 降至 9 ms，平均队列积压从 257 KiB 降至 15 KiB。持续上传后的首条高优先级消息 p50 从 127 ms 降至 2 ms。该结果描述此实验条件，不等同于所有真实网络的提升比例。

### 数据正确性与回归覆盖

- 对流量整形的速率替换、算术边界、网络切换和关闭时等待队列释放进行修复。
- 为 DNS 网络代次、反向映射、两个地址族到达顺序和迟到连接增加回归保护。
- 将快速路径诊断改为按需构造，并区分 TCP / UDP splice 的实际处理结果。

### 上传预算与会话语义

- 上传速率以配置值直接执行，持续对聚合用户态上传整形；大块写入按预算分段计费，避免大块载荷绕过速率约束。
- 调度器关闭、网络变化和速率替换时释放对应等待工作，避免队列状态跨越生命周期。
- 一致性哈希按目标域名或规范化地址建立亲和，粘性会话使用有界记录；节点变化和健康筛选不等同于迁移活动连接。
- 嵌套 URLTest 以所属目标测量实际叶子节点，强制重检合并并受组自身生命周期控制。

### NXDOMAIN 跨查询类型负缓存

- 符合条件的 NXDOMAIN 以域名为单位复用，A 查询确认名字不存在后，AAAA、HTTPS、SVCB 等查询可直接本地回答，减少重复上游往返。
- 仅单问题、空 Answer、带有效 SOA 和非零负 TTL 的响应建立域名级结论；NODATA、临时错误和经 CNAME 的结果不扩大到整个域名。
- 命中时使用当前问题与请求 ID 重建响应，并返回剩余 TTL，避免错误重放旧 QTYPE 或延长负缓存周期。
- 缓存键保留解析 transport、客户端子网和网络环境；容量有界，并与禁用缓存、禁用到期和清缓存操作保持一致。
- 这是普通 DNS 缓存启用后的自动行为，没有新增 `nxdomain_cache` 或 `nxdomain_ttl` 开关，也不依赖特定广告 DNS 产品。

### 本版本扩展字段位置

上传速率在 route 内，分类与组策略在出站内：

```json
{
  "route": {
    "traffic_scheduler": {
      "upload_rate": "8 MiB/s"
    }
  },
  "outbounds": [
    {
      "type": "direct",
      "tag": "interactive-out",
      "traffic_class": "interactive"
    },
    {
      "type": "loadbalance",
      "tag": "balanced",
      "outbounds": [
        "node-a",
        "node-b"
      ],
      "strategy": "round_robin",
      "url": "https://www.gstatic.com/generate_204",
      "expected_status": "204"
    }
  ]
}
```

node-a / node-b 使用自己的已有节点，并由路由选择相应出站。[更多配置示例](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FORK-CONFIGURATION.md)。

### Direct Offload 的逐流语义设计

- 将路由资格与 socket 语义拆成两层：路由检查 FakeIP、目标改写、元数据和观察要求；拨号器检查接口绑定、mark、namespace 和协议选项。
- 构造拨号器时预计算选项影响范围，后续逐流判断使用紧凑 profile，减少重复解析配置与日志构造。
- 按选项对当前流的真实影响判断：TCP 专用设置不应一律排除 UDP，仅解析使用的 resolver 也不应一律排除字面地址。
- 保留严格策略和统计等必要处理，提供拒绝原因，避免将“未走快速路径”压成无法诊断的单一结果。

[Fast Path 与内存管理的当前设计、源码和配置边界](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/FAST-PATH-AND-MEMORY.md)。

### 受控实验如何阅读

历史上传调度实验中，4 条普通流竞争 2 MB/s FIFO，聚合整形设置为容量的 85% 时，高优先级小消息接收端 p99 从 146 ms 降至 9 ms。这个结果依赖该实验的瓶颈、流量和速率设置，不表示所有网络都会获得同一比例的改善。完整测试与配置边界见[数据路径优化](KNOWLEDGE/modules/data-path-optimization.md)。

本版的负载均衡、测速和上传调度分别承担不同职责：代理组为连接或会话选择成员，URLTest 提供健康测量，`traffic_class` 表达业务类别，而上传调度器在显式配置速率后管理共享上传预算。它们可以配合使用，但测速状态码兼容并不意味着可以直接加载 Mihomo YAML；未设置上传速率也不会自动建立整形队列。

## [v0.1](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1) — 2026-09-30

首个正式版本以 Native Naive、MASQUE / CONNECT-IP、共享 HTTP/3 与数据包路径、SOCKS 代理链为重点，同时恢复上游协议注册、CLI 和 Clash API 能力。

### Native Naive 与 HTTP 服务端

- 提供认证 CONNECT、Padding / Framing 和 UoT v1/v2，处理固定目标与逐包目标两种会话形态。
- 普通请求与认证失败可进入伪装响应；代理隧道仍要求认证成功。
- 增加可选总连接数、单 IP 连接数、请求头超时和来源地址记录上限，区分握手、请求阶段与已建立隧道。
- 校验 HTTP/2 接收窗口等参数，将配置实际传递到服务器，避免接受了配置但仍使用无约束默认值。
- 改进目标解析、访问规则顺序与 UoT 逐数据报目标检查，处理本机地址回环和目标变化等边界。
- 对填充协商、短写、空填充帧、刷新失败和 HTTP/1 CONNECT 原始载荷维持不同的处理规则。

### MASQUE / CONNECT-IP 与 DNS

- 完善 CONNECT、CONNECT-UDP 与 CONNECT-IP 的传输和隧道生命周期。
- 接入地址分配、路由通告、DNS_ASSIGN 和 PREF64 Capsule，以及会话状态更新。
- 分离服务器 Bootstrap DNS 与隧道内部解析，处理下发 DNS 的优先级与已有 H3 连接上的 DoH。
- 在 QUIC 握手完成阶段竞争 Bootstrap 候选，关闭未胜出的尝试；重连重新取得适用的解析结果。
- 为 DNS 下发和能力选择使用一致快照，校验 URI 模板、SVCB 必选项和地址数量边界。
- 修复 Capsule 重叠检查的高复杂度路径，并限制条目数，避免异常输入大量消耗 CPU 和内存。

### 数据报与复制路径

- MASQUE 使用拥有数据包所有权的 DATAGRAM 发送能力，预留协议头空间，并对批量发送接入回归检查。
- 路由通告匹配由线性扫描改为二分查询，改进逐包处理开销。
- 满足几何与写入能力条件的已缓存首包直接交给写入方，减少再次复制。
- Shadowsocks 向复制路径声明原地帧处理限制，避免忽略真实封装余量。
- 修复首包写入失败时的缓冲区所有权，检查批量数据的字节、顺序和长时间驻留。

### SOCKS 与链式代理

- SOCKS5 可选启用已完成认证的 TCP 预连接池，提前处理问候和认证；该能力不套用到 UDP ASSOCIATE 或 UoT。
- 提供独立的链式复制调优选项，供中转与住宅代理等拓扑配置使用。
- 修复 SOCKS4 目标解析未使用对应出站解析器的问题，明确代理服务器与最终目标的 DNS 归属。
- AnyTLS 入站支持默认回落与按 ALPN 分类回落。

### HTTP/3 与核心集成

- 区分调用者取消、连接关闭和真实 QUIC 故障，保留 stateless reset、版本协商等错误信息。
- 修复 HTTP/3 CONNECT 隧道生命周期、旧连接驱逐与替代连接的隔离。
- 恢复完整上游协议注册和 CLI，并启用 Clash API；移除阶段性产品裁剪设计。
- 补充实际服务器隧道报告、DNS 下发、资源释放和协议数据矩阵测试。
