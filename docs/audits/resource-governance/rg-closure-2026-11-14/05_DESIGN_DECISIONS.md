# 05 设计决策与候选裁决（Design decisions）

> 每条记录格式：观察到的证据 → 当前实现 → 替代方案 → 预期收益 → 风险与兼容性 → 最小实验 → 裁决。

## 1. 总原则

1. **压力事实只有一个来源**：OOMKiller 的 `tun.MemoryPressure` atomic。ResourceGuard 只消费，不采样。
2. **准入看创建，不看复用**：permit 包住"真实昂贵创建"，返回即释放；不包裹会话生命周期。
3. **取消权按代次划分**：setup context 只拥有 setup，建立即转移所有权；已建立资源不被回溯杀死。
4. **采样失败不等于安全**：宁可收紧（ARMED），不制造危机（Triggered），不宣称正常（Normal）。
5. **改动落在拥有该决策的层**：路由层管 setup/class/代次；OOMKiller 管压力事实；传输层管自己的物理创建。

## 2. 修复 1：非 connected UDP setup 的三件套

**证据**

- `route/dial_setup.go:48-57` 明确契约："begin：每个 dial 一次，来自 `NewConnection` / `NewPacketConnection`"。
- 基线代码里 `NewPacketConnection` 只有 `UDPConnect` 分支调用 `dialSetups.begin`、`enterDialGate`、`trafficclass.WithClass`；else 分支直接把调用方 ctx 传给 `ListenPacket`。
- TUN 的 `UDPNatConn` 不声明 `IsUDPConnect`，因此普通 UDP 流走 else 分支（`adapter/inbound.go:70-72`、`route/conn.go:757`）。

**替代方案与裁决**

| 方案 | 结论 | 理由 |
|---|---|---|
| A. 只补 `trafficclass.WithClass` | 不足 | 只解决分类，不解决切换时 setup 不可取消 |
| B. 只补 setup gate | 不足 | 分类仍然缺失，交互流仍被当后台 |
| C. 三件套一起补（选定） | **KEEP** | 三个属性同属"这个 dial 和其它 dial 一样"，分开补会留下半正确状态 |
| D. 在 outbound 内部给 `ListenPacket` 套 context | 否决 | 每个 outbound 重复实现，且无法拿到 `ConnectionManager` 的代次与 dial gate |
| E. 把 TUN NAT 改成声明 `IsUDPConnect` | 否决 | 语义错误：unconnected UDP 的目的地可以变化，声明 connected 会破坏 NAT 语义 |

**风险与兼容性**

- 建立后 `finishSetup()` 取消 setup context。已审计：会话型 outbound（sing-quic/tuic/masque/mieru）的 PacketConn 使用 client 生命周期 ctx，不依赖调用 ctx；`direct/trojan/shadowsocks/naive` 只在拨号阶段使用 ctx。与 TCP/connected-UDP 分支的既有假设一致。
- `enterDialGate` 在切换窗口内最多延迟 500 ms（软限），与 connected 分支一致。
- 新增每 flow setup 一次 context + 一个 `AfterFunc` 注册，建立/失败即释放；稳态转发路径不变。

## 3. 已经修过、本轮不得重做的候选

| 历史候选 | 当前事实 | 证据 | 结论 |
|---|---|---|---|
| AnyTLS 在确认复用前领取许可 | **已修**：pinned SDK 只在 `createSession` 里调用 `AdmitCreation`，`DialContext` 先 `takeIdleSession` | `<sing-anytls>/client.go:110-156`、`protocol/anytls/admission.go:17-26` | `EXCLUDED`（不要重做） |
| `observe()→apply()` 陈旧状态覆盖新状态 | **已修**：transition 版本号 + 同一发布锁 | `common/resourceguard/guard.go:160-171`、`:376-400`、`guard_publish_race_test.go` | `EXCLUDED` |
| `Permit.Wait` 把持有时间混入等待 | **仍存在**，但生产无调用者 | `common/resourceguard/gate.go:409-413` | `DEFER`（见 §5） |
| TUN NAT 只限数量不限字节 | **不成立**：字节与包数都有界 | `<sing-tun>/udp_nat.go:73-76`、`:516-533` | `EXCLUDED`（但预算粒度问题见 §5） |
| ResourceGuard 专项 CI 是否覆盖路由/class 改动 | 工作流存在，运行记录未核实 | `.github/workflows/resource-guard.yml` | `UNTESTED` |

## 4. 候选方向：KEEP / REJECT / DEFER

### 4.1 同键 session 创建 coalescing —— `DEFER`

- **证据**：AnyTLS 的 `takeIdleSession` 只在 `idleSessions` 链表中寻找可复用会话；会话要等某条 stream 结束才 `releaseSession` 回到 idle 链表（`<sing-anytls>/client.go:204-253`、`session.go:165-177`）。因此 N 个并发首流几乎同时到达时，会创建接近 N 个 session——gate 只限制并发创建速率，不减少总量。这与历史诊断的 201 次 `createSession` 一致。
- **替代方案**：在 SDK 内增加"创建中 future"，让并发 dial 等待同一个创建并复用结果；或在 sing-box 的 UoT/outbound 层做 singleflight（拿不到 SDK 的 session 对象，不可行）。
- **收益**：理论上把 200 次握手压到个位数，是本轮最有价值的下一步。
- **风险**：必须修改 pinned SDK（跨仓）、需要处理"创建失败后 waiter 是否重试"、会话复用语义变化影响 AnyTLS 服务端兼容性。
- **裁决**：`DEFER`。理由：改动跨仓且语义敏感，本轮无法在无真机的条件下证明收益大于回归风险。最小实验已在 `08` 给出。

### 4.2 XMUX 物理连接数上界 —— `DEFER`

- **证据**：默认 `max_connections = 3`；一旦配置 `max_concurrency` 且未配 `max_connections`（合法且互斥校验允许），池满即新建连接（`transport/v2rayxhttp/xmux.go:778-792`、`:811-814`、`:847`）。这是与 Xray 对齐的行为。
- **收益**：给 iOS 一个物理连接硬上界。
- **风险**：改变 Xray 兼容语义与性能特征，违背"本轮不重开 XHTTP 改造"的边界。
- **裁决**：`DEFER`，只记录事实，不改代码。

### 4.3 XHTTP H3 / MASQUE H3 创建准入 —— `DEFER`

- **证据**：两处 `quic.DialEarlyConn` 都不在 guard 内（`transport/v2rayxhttp/http3.go:149-170`、`transport/masque/client.go:356-390`）。
- **收益**：补齐最后两个共享物理连接的准入。
- **风险**：H3 传输有连接迁移、0-RTT、GOAWAY 等语义；准入 hook 的 ctx 需要保留传输生命周期，否则会破坏建连重试。
- **裁决**：`DEFER`。`02` 已标注 `否`，下一轮做独立 seam 审计与 loopback H3 实验。

### 4.4 UDP NAT 首包引用不计入字节账本 —— `DEFER`

- **证据**：`sing-tun` 在 `prepare` 后保留首包引用直到 `HandshakeSuccess`（`<sing-tun>/stack_gvisor_udp.go:101-127`、`stack_go_udp.go:221`、`:459-464`），而字节账本只统计队列负载（`udp_nat.go:516-533`）。
- **收益**：把"黑洞目标"场景下的驻留内存纳入预算。
- **风险**：依赖仓库改动；需要精确的所有权/释放测试。
- **裁决**：`DEFER`（依赖仓库，非主仓适配层可解决）。

### 4.5 NAT 字节预算按实例拆分 —— `DEFER`

- **证据**：`MaxSize` 按 `natCount` 拆分（`<sing-tun>/stack_go.go:167-189`），但 `MaxBytes` 默认值在每个实例重新应用（`udp_nat.go:167-175`），聚合上限为 `8 MiB × natCount`。
- **裁决**：`DEFER`，记录事实；需要先确认 iOS 实际 mapping 配置（默认 `NATMappingEndpointIndependent` 时 natCount=1）。

### 4.6 DNS 非 hijack 查询并发上限 —— `DEFER`

- **证据**：`dnsHijackConcurrency = 256` 只覆盖 `Router.HijackDNSPacket`（`route/dns.go:91`、`:130-143`）；`Exchange/ExchangeAsync` 的调用方仍可各自起 goroutine。
- **裁决**：`DEFER`。需要先测清楚 DNS 查询在 iOS 突发里占多少 footprint，再决定是拒绝还是排队。

### 4.7 `Permit.Wait()` 语义 —— `DEFER`（低成本，可随时做）

- **证据**：`acquired` 在 `Acquire` 入口记录，`Wait()` 返回 `time.Since(acquired)`，因此持有后再调用会得到"等待+持有"（`common/resourceguard/gate.go:204`、`:227`、`:246`、`:409-413`）。
- **现状**：生产代码无调用者；两个测试在 Acquire 后立即调用，因此现有证据未被污染。
- **裁决**：`DEFER`（P2 观测质量）。修复方式：在 grant 时记录 `grantedAt`，`Wait()` 返回 `grantedAt.Sub(acquired)`；需同时更新注释与测试。

## 5. 修复 2：不可测量 footprint 的状态选择

| 方案 | 结论 | 理由 |
|---|---|---|
| A. 采样失败保持原状态 | 部分采用 | 用于 Triggered：丢失测量不是恢复证据 |
| B. 失败 → ARMED（选定） | **KEEP** | 收紧准入但不触发破坏性回收；不需要证据即可安全执行 |
| C. 失败 → Triggered | 否决 | 会凭空执行网络 reset/强制 GC，破坏活跃路径 |
| D. 失败 → Normal（原实现） | 否决 | 正是缺陷本身：把"看不见"当成"安全" |
| E. 在平台层修 `memory.Total()` 返回可用性 | 未选 | 需要改 `sing` 依赖仓库；且 Darwin 的 `TotalAvailable()` 恒为 true，无法表达"本次读取失败" |

**字段设计选择**：新增 `footprintUnavailable bool`（零值 = 真实测量），而不是 `usageKnown bool`。原因：`usageKnown` 的零值会让所有既有测试 fixture（`memorySample{usage: N}`）默认变成"未知"，制造大范围语义变更；负向标志让生产路径显式置位、测试 fixture 保持不变。

## 6. 修复 3：v2rayquic 创建准入的接点选择

| 方案 | 结论 | 理由 |
|---|---|---|
| A. 在 `completeOffer` 包住 `dialConn`（选定） | **KEEP** | 与 sing-quic 的"每次共享创建一次"完全同形；复用不经过 |
| B. 在 `DialContext` 外层包 | 否决 | 每次 stream dial 都计费，把创建预算变成流预算 |
| C. 在 `offerNew` 内部包 | 等价但更靠下 | 拿不到 guard 的 key 与 class 上下文，且会与 `dialConn` 测试 seam 冲突 |
| D. 修改 pinned SDK/QUIC 库 | 否决 | 根因在主仓适配层可解决，无需跨仓 |

**ctx 拆分**：`newOfferContext` 用 `context.WithoutCancel(caller)` 保留值（traffic class、`HeldPermit`），用 `context.AfterFunc(c.clientCtx)` 加上客户端生命周期。理由：既满足"waiter 取消不得拆掉共享 dial"，又满足嵌套 seam 只领一份许可的不变量（`common/resourceguard/held.go:19-31`）。
