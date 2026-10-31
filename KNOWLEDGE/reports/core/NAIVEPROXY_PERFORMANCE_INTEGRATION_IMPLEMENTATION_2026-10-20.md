# NaiveProxy 性能与架构整合：第一轮实施报告

- **日期**：2026-10-20
- **类型**：功能实施、回归验证与提交记录
- **输入**：`01_NaiveProxy_性能领先与架构深度融合_完整施工提示词_testing.md`、`NAIVEPROXY_DEEP_AUDIT_2026-10-20.md`
- **结果**：本轮改动已提交并推送。P1、P3、P7、P9 已实现并验证；P2 因依赖缺少相应开关而标记为 `BLOCKED_BY_NATIVE_API`；P8 保持默认值（§2）。

---

## 1. 工作环境、版本与变更归属

| 项 | 值 |
|---|---|
| 主仓 | `Piggy-Cat-bit-shadow/sing-box`，分支 `testing` |
| 本轮起点 SHA | `11c4c1b27ff87a6ee686c9c1b578a333bbb726d4`（本地 = `origin/testing`） |
| 本轮结束 SHA | 代码提交 `0ef38b8ff`（P3 + 依赖 pin）；本文件所在提交即最终 `origin/testing` 头（推送后 `HEAD...origin/testing` = `0 0`） |
| 审计报告 SHA（历史） | 起点 `6edc18c60` → `7e42ca38e`；本轮的起点已含并行窗口 4 个提交 |
| 并行窗口归属 | `clients/apple`、`clients/desktop` 的 submodule 指针修改**不是本轮的**，未 stage、未提交 |
| cronet-go pin | root 模块升到 `v0.0.1-143.0.7499.109-2.0.20261020055023-fd2f25825e4f`（= fork 分支 `feat/per-dial-quic` 的 `fd2f2582`）；`all` 与 29 个 `lib/*` 保持 `8c68ce89873c`（只改了 Go 侧，Native 二进制未变） |
| sing-tun pin | 由并行窗口在审计末段从 `ff4fbed67c0d` 升到 `276576ea222c`，本轮起点已包含 |
| Native 运行时版本 | Cronet `Engine().Version()` = `154.0.8037.49`（本机实测，Windows/amd64） |
| 工具链 | `go1.25.5 windows/amd64`，`CGO_ENABLED=0`，`-tags with_purego`（Windows 客户端 profile） |
| 服务端 profile | `release/DEFAULT_BUILD_TAGS_OTHERS` |
| 测试机 | Windows 11 x64，16 逻辑核，loopback |

**并行工作期间的检查**：每次写入前检查 `git status` 和 `git rev-parse HEAD`；提交时只暂存本轮创建或修改的文件；推送前重新 `git fetch` 并比对远端（见 §8）。

---

## 2. 结论摘要

| 项目 | 状态 | 结果 |
|---|---|---|
| **P1 Naive × FailureFacts** | **DONE** | 出站现在会**完成 CONNECT 握手**并把结果映射成 `adapter.FailureFacts`；顺带修复了一个比审计描述更严重的真实缺陷：**407 从来不是错误** |
| **P9 padding 全零回归** | **DONE** | 256 个 padding 抽样 × 三条生产写路径；变异验证（`WriteZeroN`→`Extend`）确认测试真的会红 |
| **P7 文档默认值** | **DONE** | `quic_congestion_control` 的默认值从"BBR"改为事实（CUBIC，library default） |
| **P2 Cronet DoH** | **BLOCKED_BY_NATIVE_API** | 当前 pin 无任何 Go 可配置的 Secure DNS 开关；`SetHostResolverRules` 只能解决域名 MAP，不能消除 stray 连接。今日实测该连接仍在（§5） |
| **P3 H3→H2 回退** | **DONE** | 在依赖 fork 补了 per-CONNECT 入口并从 pin 起分支；sing-box 侧实现 attempt 有序的判定核、窄分类、30 s 窗口与网络切换清零；**同 Engine** 真实 H3 失败 → H2 成功（§6） |
| **P8 Engine/窗口默认** | **KEEP** | 无新的活跃负载数据；默认不动 |

**实现过程中发现的关键问题：** 旧版 `Outbound.DialContext` 调用 `DialEarly` 后不会读取 CONNECT 响应，因此认证失败的 407 被误认为隧道建立成功，直到第一次 Read 才得到 EOF。修复后，同一测试能正确报告 `naive CONNECT handshake: unexpected response status: 407`（§3.2）。

---

## 3. P1：Naive × 公共故障事实

### 3.1 旧版异步握手的行为

```
Outbound.DialContext            -- 旧代码 return client.DialEarly(...)
  -> NaiveClient.DialEarly      -- 只 CreateConn + Start(CONNECT)，不读响应
  -> BidirectionalConn.Start    -- 返回 nil 时流已提交，CONNECT 200/407 尚未到达
  -> route/conn.go              -- ReportConnHandshakeSuccess 只通知 inbound 侧，不驱动 Naive 握手
     kickWriteHandshake        -- 检查 EarlyWriter/EarlyConn 接口；NaiveConn 不实现，于是不强制握手
  -> copy loop                 -- 第一次 Read 才 waitForHeaders
```

**结论**：旧代码没有调用 `NaiveConn.HandshakeContext`，由此产生以下问题：
- `DialEarly` 的 error 永远不可能携带协议事实（它看不到协议）；
- 407 不会被识别，客户端看到的是"连接后立刻 EOF"；
- 在 `Outbound.DialContext` 外面包一层 `WrapFailure(err)` 属于审计里说的"只修到表皮"，甚至更糟——它会把 dial 阶段的错误误标成协议事实。

### 3.2 实际改动与证据

新增 `protocol/naive/failure.go`：

- `dialNaiveTunnel(ctx, client, destination)`：`DialEarly` → **`HandshakeContext`** → 成功才返回；失败先 `Close` 再分类。
- `naiveHandshakeFailureFacts(ctx, err)`：窄映射（下表）。
- `naiveDialerClient` 窄接口：让分类逻辑可以**不依赖 Native 库**做单元测试；生产路径零包装（`*cronet.NaiveClient` 直接满足）。
- `naiveResponseStatus`：从依赖的错误文本里取状态码（后缀匹配，外层 `E.Cause` 不影响；非数字/越界返回"不是状态"）。

`protocol/naive/outbound.go`：
- TCP 路径与 UoT 的 `naiveDialer.DialContext` 都改走 `dialNaiveTunnel`。UoT 会话通过 CONNECT 隧道传输，因此必须在写入 session header **之前**确认隧道是否被拒绝。

**故障映射（只覆盖可明确判断的情况）**

| 握手结果 | facts | 理由 |
|---|---|---|
| CONNECT 407 | `{Handshake, Proxy}` | 代理拒绝了凭据；绝不能被当成目标或路径问题 |
| CONNECT 其它状态 | 无 fact | 收到 403 或 502 时，无法区分目标拒绝与代理策略，因此保留为未知 |
| `NetErrorConnectionRefused` / `NetErrorAddressUnreachable` | `{Handshake, Path}` | 内核级的"端点不可达"，与 group 已信任的 ECONNREFUSED/EHOSTUNREACH 同类 |
| `NetErrorNameNotResolved` | `{Handshake, Local}` | DNS 策略/配置，属于本进程 |
| TLS/证书类 | `{Handshake, Proxy}` | 连接已进入 TLS 阶段并失败 |
| **QUIC protocol/handshake 失败** | **无 fact（刻意）** | QUIC 失败只表明 UDP 路径存在问题，**不能证明节点不可用**；标记为 Path 会使 H2 仍正常的节点被全局降权。这也是需要回退机制的原因 |
| timeout / reset / EOF / cancel / closed | 无 fact | group 本来就判中性；超时不是"路径永久消失"的证明 |

消费者对齐（无需改 group）：`RetryThisFlow` 对 `Proxy/Handshake` 与 `Path/Handshake` 都是 RETRY；`PenalizeMemberGlobally` 只对 Path 且非超时降权——已有测试 `protocol/group/loadbalance_failure_facts_test.go` 覆盖这两种形状。

### 3.3 端到端证据（真实 Native + 真实 sing-box 出站）

配置：`mixed` inbound `127.0.0.1:18081` → `naive` outbound → 本地 Naive 服务端（自签 CA，`trusted` 通过 `tls.certificate_path`）。

**正确密码**：`curl -x http://127.0.0.1:18081 http://127.0.0.1:18080/` → **200**。

**错误密码**（服务端用户密码不同）：

```
outbound/naive[n]: open TCP connection to 127.0.0.1:18447
outbound/naive[n]: response received, protocol: h2, status: 407
outbound/naive[n]: handshake failed: unexpected response status: 407
connection: open connection to 127.0.0.1:18080 using outbound/naive[n]:
  naive CONNECT handshake: unexpected response status: 407
```

`curl` 得到 **502**（代理失败）而不是旧行为的空隧道。日志里第一次出现 `status: 407` 也是新代码的副产品：以前握手函数没被调用，这行 debug 都不会出现。

### 3.4 代价（一轮 RTT，有边界）

- 每个**新**隧道在第一次写之前多等一个 CONNECT 响应。参考服务端（Caddy forwardproxy）在拨目标**之前**就发 200，所以这一个是隧道往返，不是目标往返。
- 没有改缓存首包/`kickWriteHandshake` 的既有语义；没有为未启用任何选项的用户增加第二条路径。
- loopback 实测：握手包含在既有 5–8 ms 的 connect 里（审计探针测的是同一条 `DialContext` 语义）。**WAN 的 +1 RTT 未测**（`NOT_RUN`），这是本轮唯一没有真实网络数据的取舍，必须留给专项 Debug 用 120 ms 链路量化。
- 回滚：`git revert` 该提交即可；默认配置、协议字节、认证、ACL、生命周期均未改变。

### 3.5 仍未覆盖 / 已知边界

- **非 407 的状态码**没有 facts（保守）。如果将来证明某类状态码稳定表示目标拒绝，再单独加。
- **QUIC 失败无 facts**：等回退特性落地后，失败会在回退内部被消化，不需要对外解释。
- **数据阶段**（交出连接后的 Read/Write）不产生 facts，符合 `FailureStageData` 的"不是一次新拨号"语义。
- `naiveResponseStatus` 依赖 cronet-go 的错误文本（`naive_conn.go` 的 `E.New("unexpected response status: ", ...)`）。若依赖改文案，行为退化为"无 fact"，不会产生错误 fact；这一条已写进代码注释，下一轮应加一条跨仓 pin 断言。

---

## 4. P9：padding 全零回归（含变异验证）

新增 `protocol/naive/padding_zero_fill_test.go`，覆盖 0..255 全部 256 个 padding 抽样：

| 路径 | 断言 |
|---|---|
| `writeFrame`（分配型，`writeChunked` 使用） | 帧长 = 3+data+pad，payload 不变，**padding 区全零** |
| `writeBufferWithPadding`（原地型，调用方池化 buffer） | 同上，且 free 区先被填 `0xAA`；帧后所有字节**未被覆写**；`Start()==0` |
| `writeChunked`（1 MiB，走满 8 帧窗口） | 每帧 padding 全零；窗口后剩余为 raw，payload 总量守恒 |
| padding=0 | 退化用例固定为显式断言（`{0,3,0,'a','b','c'}`） |

**变异验证（真实执行过，不是声称）**：把 `inbound_conn.go` 的两处 `WriteZeroN(paddingSize)` 换成 `Extend(paddingSize)`：

```
TestAuditPaddingBytesAreAlwaysZeroInPlace  FAIL
  padding byte 0 (of 1) is 0xaa; the Naive layout requires zeros
TestAuditPaddingBytesAreAlwaysZeroChunked  FAIL
  padding byte 0 (of 1) is 0x77
```

恢复后全绿。**这不是修了一个当前存在的泄漏**，而是让"当年造成 `cronet-go#6` / `sing-box#4002` 的那次改动"无法再悄悄通过。

---

## 5. P2：Cronet DoH bootstrap（BLOCKED_BY_NATIVE_API，有当日实测）

### 5.1 今日证据（本轮重新跑出来的，不是引用审计）

用生产 `sing-box-client.exe`（Windows profile）连本地 Naive 服务端时，stderr 仍然出现：

```
DEBUG outbound/naive[n]: open UDP connection to [2001:4860:4860::8888]:443
ERROR outbound/naive[n]: open UDP connection to [2001:4860:4860::8888]:443:
  local network has no public ipv6 path: ...
```

即：`quic:false`（默认）、`SetEnableQuic(false)`、`dns_server_override=127.0.0.1:53`、`UseDnsHttpsSvcb=false` 全部生效的情况下，**引擎启动仍会尝试 DoH/DoQ 到 Google Public DNS**，且该 socket 请求经 cronet dialer 桥进入 sing-box dialer（所以受路由/detour 约束），但**不经过 `dns_router`**。

### 5.2 我核对了什么

| 检查 | 结果 |
|---|---|
| cronet-go 是否暴露 Secure DNS/DoH 开关 | **没有**。`engine_params_experimental_options.go` 只有 `SetAsyncDNS`、`SetDNSServerOverride`、`SetHostResolverRules`、`SetUseDnsHttpsSvcb` |
| `SetHostResolverRules` 能否消除该连接 | **不能**。MAP 只影响指定 host 的解析；本机在 `ServerName` 是 IP 字面量、根本没发生域名解析时，连接照样出现 |
| 是否存在可禁用 DoH 的 Chromium experimental option（可从 Go 侧传 JSON） | 当前 pin 未暴露；native 库是预编译产物，新增选项需要改 C++ → 属于 `BLOCKED_BY_NATIVE_API` |
| 是否应该由我们默认硬编码公共 DNS / 把用户 `server` 变成静态 IP | **不应该**。提示词明确禁止，也会破坏 detour/ECH/双栈策略 |

### 5.3 结论与下一步

- **不做**"看起来修好了"的 MAP 补丁：它治不了这条连接，声称修好会误导。
- 下一步（独立课题）：要么上游在 cronet-go 暴露 `secure_dns`/DoH 配置（需要 Native 改动，成本高），要么接受这是已知边界并在文档里写明；如果只是为了消掉一条连接而自维护 Chromium patch，按提示词的成本判断应放弃。
- 现有较小改善的候选：把 `host_resolver_rules` 作为**用户可配**选项暴露，只解决"代理域名必须走 sing-box 解析"的场景，并明确标注"stray 仍在"。本轮**未做**：它没有解决 P2 的核心问题，且会新增一个用户可见配置面。

---

## 6. P3：H3→H2 回退（**已实现**）

### 6.1 依赖侧：先补 per-CONNECT 选择入口

`DialEarly` 的 `-force-quic` 由 client 级 `quicEnabled` 决定，而它是保留头，`ExtraHeaders` 无法覆盖，所以 sing-box 侧原本**没有入口**。本轮在依赖 fork 上补了最小 API：

- 分支 `feat/per-dial-quic`，**从 pin 的 `8c68ce8` 起**（`main` 已领先一个未评审提交 `049f290`，不从 main 出发，避免顺带吸收）。
- 提交 `fd2f25825e4fe24d69ba67a9b87d49fb3d382246`：`DialEarly` 变一行 wrapper（行为不变），新增 `DialEarlyWithQUIC(ctx, dest, forceQUIC bool)`；header 构造抽成 `dialHeaders(dest, forceQUIC)` 并加纯单测（client 默认 / per-dial 覆盖 / 保留头不可被用户 header 顶掉）。
- 3 条新测试在 `-tags with_purego` 下通过；`vet` 只有依赖里既有的 unsafe.Pointer 警告。
- 主仓只把 **root 模块** pin 到 `v0.0.1-143.0.7499.109-2.0.20261020055023-fd2f25825e4f`；`all` 与 29 个 `lib/*` 不动（只改了 Go 侧）。`go.sum` 两条新条目，`go build` 通过。

**契约边界写进了方法注释**：`forceQUIC=false` ≠ 跨环境确定性 H2——`enable_quic=true` 的引擎仍可能因 Alt-Svc 缓存走 QUIC（cronet-go#11）；要硬保证只能关 Engine 级 QUIC。

### 6.2 sing-box 侧：状态、分类、窗口

| 文件 | 内容 |
|---|---|
| `option/naive.go` | `quic_fallback_to_h2`（默认 false）；与 `quic:false` 同时出现 → **配置错误**，不是静默失效 |
| `protocol/naive/failure.go` | `dialNaiveTunnelWithQUIC`；把两阶段拨号抽成 `prepareNaiveTunnel`，**成功或失败都先完成/终止握手**再返回 |
| `protocol/naive/h3_fallback.go` | `h3Fallback`（attempt 序号 + claim/effect 同一临界区 + 30 s 窗口 + `reset()`）；`h3TransportFailure`；TCP 与 UoT 共用的 `dialNaiveWithH3Fallback` |
| `protocol/naive/outbound.go` | 开关字段、构造期校验、TCP 分支、`InterfaceUpdated` 里 `h3Fallback.reset()`、`naiveDialer` 共享同一判定 |
| `docs/configuration/outbound/naive.md` | 选项文档（含"这不是测速选路"的边界） |
| `protocol/naive/h3_fallback_test.go` | 10 组测试，见下 |

**分类（`h3TransportFailure`）**：QUIC 协议/握手失败、refused、address unreachable、connection closed/reset、timeout → 允许降级；407/其它状态、caller cancel、非 NetError 的本进程 teardown → 不降级。

这里有一个**真实踩坑**：`cronet.NetError` 的 `Is()` 会把 `CONNECTION_CLOSED/RESET` 映射成 `net.ErrClosed`，所以如果先做 `adapter.IsOwnTeardown` 判断，一次死掉的 QUIC 尝试会被当成"我们自己关的"并**静默禁用重试**。实现的顺序是**先按 NetError 码分类，再做 teardown 判断**，测试 `connection_closed` / `connection_reset` 两行就是钉这个顺序的（它们先红后绿，是本轮唯一一次由测试抓出的实现错误）。

**安全边界（不可放宽）**：

- 只在**新 CONNECT 建立阶段**重试；`prepareNaiveTunnel` 保证 H3 那次已经握手完成或失败，才可能发起 H2，应用字节尚未交给任何连接。
- 不迁移、不重放；cached 首包在 `Outbound.DialContext` 返回后才写出。
- 407/其它状态/取消不重试。
- 窗口 30 s（`h3FallbackWindow`，var 便于测试）；窗口内的并行失败**不延长**窗口（一次事件一次决策）；`InterfaceUpdated` 清零；旧 attempt 的迟到失败不能覆盖新成功。
- **零额外 Engine**：两次尝试都在同一个 `*cronet.NaiveClient`（同 Engine）上，`enable_quic`/`enable_http2` 从未改变。

### 6.3 端到端证据（本机真实 Native）

场景：客户端 `quic:true, quic_fallback_to_h2:true`，服务端只开 TCP（UDP 端口无监听），同一 `mixed` inbound 走 curl。

```
[0007 4.5s] outbound/naive[n]: stream failed: quic protocol error
[0007 4.5s] outbound/naive[n]: handshake failed: quic protocol error
[0007 4.5s] outbound/naive[n]: naive H3 attempt failed at the transport, retrying over HTTP/2: ...
[0007 4.6s] outbound/naive[n]: response received, protocol: h2, status: 200
[0007 4.6s] outbound/naive[n]: handshake succeeded
--- 第二个请求（窗口已 armed）---
[0007 1ms]  outbound/naive[n]: response received, protocol: h2, status: 200
```

两次 `curl` 都是 **200**；第一次为 4.5 s 的 H3 失败 + H2 成功，第二次**直接 H2、1 ms**——这就是窗口的作用（不是每个连接都交 4 s 学费）。

同机另有一组对照（同一服务端、`quic_fallback_to_h2` 关闭、`quic:false`）：200，无 H3 尝试。

### 6.4 未覆盖（明确留给下一轮）

- **Alt-Svc 负向对照**：同 Engine 连续 H3→H2→H3 的真实 negotiated protocol 在"服务端广告 Alt-Svc"的第三方场景下未测；当前服务端不发 Alt-Svc，故实测是 H2。
- **-race**：Windows 无 cgo/cronet 的 race 组合；判定核是 mutex + 原子序号，未在 race 下跑过。
- 4.5 s 的 H3 预算是本机/本 Chromium 版本的观察值，不是 SLA；移动网络下的实际值与用户可接受度未测。

---

## 7. 与参考实现的对照：本轮学到了什么、采用了什么、拒绝了什么

| 参考 | 结论 | 本轮动作 |
|---|---|---|
| Caddy forwardproxy（`d62c80d3`）的 CONNECT 流程 | 先 `200 + Flush`，再拨目标；H1 raw、H2/H3 padded；`Padding` 响应头无条件 | **核对后确认我们没有偏差**；P1 的握手等待正好发生在 200 之后，与参考的 200-before-dial 相容 |
| naiveproxy 官方客户端 | 会等 CONNECT 200（`naive_proxy.cc`/`preamble` 路径），407 是硬错误 | **采纳**：我们之前不等，这是相对参考的行为缺陷；P1 修的就是它 |
| naiveproxy #669（2024-10-01，not planned） | "应该由客户端指定协议，这样利于控制性能" | **采纳为 P3 的默认值约束**：默认关闭、显式开启 |
| naiveproxy #756 | QUIC 未特别优化；作者拒绝激进 CC | **不采纳**任何 QUIC 调参；P8 维持默认 |
| cronet-go #11 | `QUIC=false` 曾不传播 `enable_quic`，Alt-Svc 可把 H2 悄悄变 QUIC | 已由 pin 修复；写进 P3 的"无 `-force-quic` ≠ H2"约束 |
| cronet-go #6 / sing-box #4002 | padding 未清零泄漏旧内存 | **P9**：补 256 抽样的回归 + 变异验证 |
| sing-box #4205 / #4249/#4287 / #4458 | ALPN race、H2 wrapper、HTTP2Options | 起点已吸收；**未回退任何已有增强** |
| sing-box #3803 / #4620 | DoH bootstrap 绕过 DNS 策略 | **P2**：实测确认仍在；无 Go 侧开关 → `BLOCKED_BY_NATIVE_API` |

---

## 8. 提交、推送与验证

### 8.1 提交（3 个代码提交 + 2 个报告提交，均只含本轮文件）

| SHA | 主题 |
|---|---|
| `e9a7cbcad` | `test(naive): pin that the padding region is zeros on the wire` |
| `b3ee59151` | `docs(naive): state the real quic_congestion_control default` |
| `1fb42b5f5` | `fix(naive): complete the CONNECT handshake and report failure facts` |
| `fefb91f8a` | `docs(knowledge): archive the NaiveProxy deep audit of 2026-10-20` |
| `0ef38b8ff` | `feat(naive): add opt-in H3 to H2 fallback`（含 cronet-go root 模块重新 pin） |
| `84760a372` | `docs(naive): record the round-one performance and integration construction`（本报告） |
| `df604e303` → 本提交 | 报告自身的 SHA/推送记录修正 |

### 8.2 本轮真正跑过的验证

| 命令 | 结果 |
|---|---|
| `go test -tags "$(OTHERS)" ./protocol/naive/... ./transport/http/ ./common/httpclient/ ./protocol/group/ ./adapter/` | **PASS**（naive 0.35s / quic 0.04s / transport/http 25.3s / httpclient 0.02s / group 3.9s / adapter 0.7s） |
| `go test -tags "$(WINDOWS)" ./protocol/naive/ ./option/` | **PASS**（含 14 行 facts 矩阵、4 个 dial 测试、10 组回退测试、既有 dependency_pin 测试） |
| cronet-go `go test -tags with_purego -run TestDialHeaders .` | **PASS**（3 条：client 默认 / per-dial 覆盖 / 保留头守卫） |
| `go vet -tags "$(WINDOWS)" ./protocol/naive/` | **clean** |
| `gofmt -l protocol/naive/` | **空** |
| 变异验证（`WriteZeroN`→`Extend`） | **按预期变红**，恢复后绿 |
| 端到端 407 / 200（真实 Native + 真实 sing-box 出站） | **407 变错误（curl 502）**；**200 正常** |
| 端到端 H3→H2 回退（服务端无 UDP 监听） | **200**；第一次 4.5 s H3 失败后 H2 成功，第二次 1 ms 直接 H2 |
| Native 运行时版本 | `154.0.8037.49` |
| `-race` | **未跑**（Windows + cronet 的已知限制；P1 新增逻辑是纯分类与包装，无共享状态；并发测试留给第二份 Debug 在可用平台执行） |
| macOS/iOS 构建 | `NOT_RUN`（无环境） |

### 8.3 推送

推送前重新 `git fetch origin testing` 比对（结果 `3 0`，远端未动）；以 fast-forward 推到现有 `testing`：

```
11c4c1b27..84760a372  testing -> testing
84760a372..df604e303  testing -> testing
```

推送后再次 fetch，`HEAD...origin/testing` = `0 0`。未 force、未 rebase 任何他人提交。

> 记录一个本轮真实发生的事故与修复：报告首版提交后，我用 PowerShell 的 `Get-Content`（按系统 ANSI 解码）读改写同一文件，把中文写成了乱码并推送成 `df604e303`。发现后从 `84760a372` 的 blob 恢复，再用 UTF-8 安全的方式重放三处编辑，当前提交即修复版本。教训：本仓中文文档只能用能显式指定编码的工具读写。

---

## 9. KEEP / REJECTED / NOT_RUN（本轮明确不做的）

| 项 | 状态 | 理由 |
|---|---|---|
| 服务端 Go inbound 重写 | **KEEP** | 本轮没有找到可归因的服务端瓶颈；既有 codec 有实测定量（§附录） |
| P4 非 CONNECT 状态码 | **KEEP（不改）** | 参考是交给 next handler，不是固定 407；机械改成 407 是反探测退步 |
| P5 客户端零数据帧 | **KEEP（不改 pin）** | 可达性极低，服务端已硬化 |
| P6 客户端 padding 头分布 | **KEEP（不改）** | 我们的服务端与 Caddy 逐字节一致；只有客户端与官方客户端不同，且无互通证据 |
| P8 Engine/接收窗口默认 | **KEEP** | 空闲态数据不足以翻转默认；活跃负载需 macOS + 真实链路 |
| P2 MAP 补丁 | **REJECTED** | 治不了 stray 连接，属于"看起来修好了" |
| 自维护 Chromium patch 关 DoH | **NOT WORTH IT** | 维护成本高于一条启动期连接的价值 |
| P3 | **DEFERRED** | 需要 cronet-go 分支 + 重新 pin + 独立对抗矩阵（§6） |

---

## 10. 附：本轮可引用的实测数据

**既有基线（审计本轮复测，Windows/amd64，loopback，仅说明 Go 层成本）**

| Benchmark | ns/op | allocs/op |
|---|---|---|
| `PaddingWriteBuffer/framed 1400B` | ~285 | **0** |
| `PaddingWriteBuffer/framed 16KiB` | ~258–288 | **0** |
| `CachedFirstPayload/buffer 1400B` | ~316–336 | **0** |
| `CachedFirstPayload/buffer 16KiB` | ~308–342 | **0** |
| `PaddingWriteChunked 1400B` | ~5.2–5.4 µs | 4 |

**P1 端到端（本机，含引擎启动的新进程）**：正确密码 200；错误密码 407 在 9–10 ms 内返回（`response received, protocol: h2, status: 407`）。这不代表 WAN。

---

## 11. 交给第二份 Debug 提示词的输入

- **base SHA**：`11c4c1b27ff87a6ee686c9c1b578a333bbb726d4`
- **施工 commits**：`e9a7cbcad`、`b3ee59151`、`1fb42b5f5`
- **两个仓库最终 SHA**：sing-box = 推送后的 `testing` 头；cronet-go **未改**（仍 `8c68ce89873c876…`）
- **触碰的真实函数**：`prepareNaiveTunnel`、`dialNaiveTunnel`、`dialNaiveTunnelWithQUIC`、`naiveHandshakeFailureFacts`、`naiveResponseStatus`、`h3Fallback.*`、`h3TransportFailure`、`dialNaiveWithH3Fallback`、`(*Outbound).DialContext`、`(*Outbound).InterfaceUpdated`、`(*naiveDialer).DialContext`、`writeFrame`/`writeBufferWithPadding`/`writeChunked`（仅测试）
- **测试入口**：
  - `go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./protocol/naive/... ./transport/http/ ./common/httpclient/ ./protocol/group/ ./adapter/`
  - `CGO_ENABLED=0 go test -tags "$(cat release/DEFAULT_BUILD_TAGS_WINDOWS)" ./protocol/naive/`（需要 `libcronet.dll` 同目录时才真正加载 Native；纯分类测试不需要）
  - 端到端：`sing-box-client.exe run -c client-{good,bad}.json` + `curl -x http://127.0.0.1:18081 http://127.0.0.1:18080/`
- **尚未覆盖 / 建议攻击**：
  1. P1 的 **+1 RTT**：用 120 ms 链路量化首字节与 TTFB 回归；确认 `kickWriteHandshake`/cached 首包与新握手的时序无重复等待。
  2. 非 407 状态码是否应产生 fact（需要真实 Caddy 403/502 场景）。
  3. `naiveResponseStatus` 对依赖文案的耦合：加一条跨仓 pin 断言。
  4. P9 的零填充测试在 `NaiveH2Conn` 层（flush 路径）还需要一条端到端断言。
  5. P3 落地时的并发/竞态矩阵（§6.2）。
  6. DoH stray：在双栈且 Google 可达的网络上确认它是否真的产生 DNS 事务（本轮只看到 socket 尝试）。
