# NaiveProxy 性能与架构深度融合：第一轮施工报告

- **日期**：2026-10-20
- **类型**：实际施工 + 回归 + 提交（非审计）
- **输入**：`01_NaiveProxy_性能领先与架构深度融合_完整施工提示词_testing.md`、`NAIVEPROXY_DEEP_AUDIT_2026-10-20.md`
- **本轮定性**：**施工一轮，已提交并可推送**；P3（H3→H2 回退）经核对依赖现状后**按成本/收益显式推迟到下一轮**，理由与精确设计见 §6（不是等人批准，是一个有证据的范围判断）。

---

## 1. 现场、SHA 与归属

| 项 | 值 |
|---|---|
| 主仓 | `Piggy-Cat-bit-shadow/sing-box`，分支 `testing` |
| 本轮起点 SHA | `11c4c1b27ff87a6ee686c9c1b578a333bbb726d4`（本地 = `origin/testing`） |
| 本轮结束 SHA | `df604e303`（最终 `origin/testing` 头：3 个代码提交 + 2 个报告提交；每步推送后 `HEAD...origin/testing` = `0 0`） |
| 审计报告 SHA（历史） | 起点 `6edc18c60` → `7e42ca38e`；本轮的起点已含并行窗口 4 个提交 |
| 并行窗口归属 | `clients/apple`、`clients/desktop` 的 submodule 指针修改**不是本轮的**，未 stage、未提交 |
| cronet-go pin | 未改动：`Piggy-Cat-bit-shadow/cronet-go@8c68ce89873c`（root + `all` + 29 个 `lib/*`） |
| sing-tun pin | 由并行窗口在审计末段从 `ff4fbed67c0d` 升到 `276576ea222c`，本轮起点已包含 |
| Native 运行时版本 | Cronet `Engine().Version()` = `154.0.8037.49`（本机实测，Windows/amd64） |
| 工具链 | `go1.25.5 windows/amd64`，`CGO_ENABLED=0`，`-tags with_purego`（Windows 客户端 profile） |
| 服务端 profile | `release/DEFAULT_BUILD_TAGS_OTHERS` |
| 测试机 | Windows 11 x64，16 逻辑核，loopback |

**并行窗口防护**：本轮每次写入前比对 `git status` / `git rev-parse HEAD`；三次提交都只 stage 自己创建或修改的文件；推送前再次 `git fetch` 比对远端（见 §8）。

---

## 2. 结论摘要

| 项目 | 状态 | 一句话 |
|---|---|---|
| **P1 Naive × FailureFacts** | **DONE** | 出站现在会**完成 CONNECT 握手**并把结果映射成 `adapter.FailureFacts`；顺带修复了一个比审计描述更严重的真实缺陷：**407 从来不是错误** |
| **P9 padding 全零回归** | **DONE** | 256 个 padding 抽样 × 三条生产写路径；变异验证（`WriteZeroN`→`Extend`）确认测试真的会红 |
| **P7 文档默认值** | **DONE** | `quic_congestion_control` 的默认值从"BBR"改为事实（CUBIC，library default） |
| **P2 Cronet DoH** | **BLOCKED_BY_NATIVE_API** | 当前 pin 无任何 Go 可配置的 Secure DNS 开关；`SetHostResolverRules` 只能解决域名 MAP，不能消除 stray 连接。今日实测该连接仍在（§5） |
| **P3 H3→H2 回退** | **DEFERRED（下一轮）** | 关键能力（per-CONNECT 选择）已证，但需要**先改 cronet-go 的 `DialEarly`**；依赖仓 `main` 已领先 pin 一个提交，须开分支最小改动 + 重新 pin + 独立对抗矩阵。设计与插入点已写好（§6） |
| **P8 Engine/窗口默认** | **KEEP** | 无新的活跃负载数据；默认不动 |

**本轮最重要的新发现**（审计没写、施工中实测出来的）：`Outbound.DialContext` 之前调用 `DialEarly` 后**从不读取 CONNECT 响应**，所以错误密码（407）在 sing-box 里**不是错误**——隧道"建立成功"，第一次 Read 得到 EOF。修完之后同一个测试从"空隧道"变成 `naive CONNECT handshake: unexpected response status: 407`（§3.2）。

---

## 3. P1：Naive × 公共故障事实

### 3.1 审计没说透的异步真相

```
Outbound.DialContext            -- 旧代码 return client.DialEarly(...)
  -> NaiveClient.DialEarly      -- 只 CreateConn + Start(CONNECT)，不读响应
  -> BidirectionalConn.Start    -- 返回 nil 时流已提交，CONNECT 200/407 尚未到达
  -> route/conn.go              -- ReportConnHandshakeSuccess 只通知 inbound 侧，不驱动 Naive 握手
     kickWriteHandshake        -- 检查 EarlyWriter/EarlyConn 接口；NaiveConn 不实现，于是不强制握手
  -> copy loop                 -- 第一次 Read 才 waitForHeaders
```

**结论**：旧代码里 `NaiveConn.HandshakeContext` **没有任何调用者**。因此：
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
- TCP 路径与 UoT 的 `naiveDialer.DialContext` 都改走 `dialNaiveTunnel`。UoT 会话骑在一个 CONNECT 里，必须在写 session header **之前**知道隧道被拒。

**映射表（故意窄）**

| 握手结果 | facts | 理由 |
|---|---|---|
| CONNECT 407 | `{Handshake, Proxy}` | 代理拒绝了凭据；绝不能被当成目标或路径问题 |
| CONNECT 其它状态 | 无 fact | 有东西应答了，403/502 无法区分目标拒绝与代理策略 → unknown 优于猜 |
| `NetErrorConnectionRefused` / `NetErrorAddressUnreachable` | `{Handshake, Path}` | 内核级的"端点不可达"，与 group 已信任的 ECONNREFUSED/EHOSTUNREACH 同类 |
| `NetErrorNameNotResolved` | `{Handshake, Local}` | DNS 策略/配置，属于本进程 |
| TLS/证书类 | `{Handshake, Proxy}` | 端点跑到了 TLS 并失败 |
| **QUIC protocol/handshake 失败** | **无 fact（刻意）** | QUIC 失败只证明 UDP 路不通，**不证明节点坏了**；标 Path 会把一个 H2 健康的节点全局降权。这一条正是回退特性存在的理由 |
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

## 6. P3：H3→H2 回退（下一轮，设计与阻塞已落地）

### 6.1 为什么没在本轮施工（精确的阻塞，不是"没时间"）

per-CONNECT 选择能力在 Chromium 侧已证（`-force-quic` header），但**sing-box 侧没有入口**：

- `cronet.NaiveClient.DialEarly(ctx, dest)` 的 header 由 `c.quicEnabled` 决定（`naive_client.go:580-582` 无条件追加 `-force-quic`）；
- `-force-quic` 是保留头，`ExtraHeaders` 会被拒绝/跳过，sing-box 无法从外部关掉它；
- 因此"先 H3、失败再 H2"**必须**先在 cronet-go 增加 per-dial 入口。

我核对了依赖仓：`Piggy-Cat-bit-shadow/cronet-go` 的 `main` 已到 `049f290`（"Fix Windows UDP socket close callback registration"），**领先我们 pin 的 `8c68ce8`**。正确做法是**从 pin 开分支**做最小改动（不是推到 main 顺带吸收未评审的提交），然后重新 pin root 模块（`all`/29 个 `lib/*` 不需要动，因为只改了 Go 侧）。

### 6.2 下一轮的精确设计（可直接执行）

**cronet-go 侧（约 30–50 行）**

```go
// 旧 API 行为不变
func (c *NaiveClient) DialEarly(ctx context.Context, destination M.Socksaddr) (NaiveConn, error) {
    return c.dialEarly(ctx, destination, c.quicEnabled)
}

// 新窄入口：per-dial 选择，不改 Engine 参数
func (c *NaiveClient) DialEarlyWithQUIC(ctx context.Context, destination M.Socksaddr, quic bool) (NaiveConn, error) {
    return c.dialEarly(ctx, destination, quic)
}
```
把 `-force-quic` 的追加从 `c.quicEnabled` 改为参数；`buildDialHeaders(dest, quic)` 抽成纯函数并加单测（无需 Native 库）。

**sing-box 侧（约 120–180 行 + 测试）**

插入点就是本轮新增的 `protocol/naive/failure.go::dialNaiveTunnel`：

```text
if !fallbackEnabled || !h.quic {
    return dialNaiveTunnel(ctx, h.client, dest)          // 旧行为，默认路径
}
attempt := verdict.begin()
if verdict.available() {
    conn, err := dialNaiveTunnelWithQUIC(ctx, h.client, dest, true)   // 含 HandshakeContext
    if err == nil { verdict.clear(attempt); return conn }
    if ctx.Err() != nil || !isH3TransportFailure(err) { return nil, err }
    verdict.markBroken(attempt)
}
return dialNaiveTunnelWithQUIC(ctx, h.client, dest, false)
```

**判定核**（`protocol/naive/h3_fallback.go`，约 60 行）：复用 `transport/http/client.go` 的**算法**（不是代码，也不是第三个全局 verdict）：

- `begin()` 在发起前取序号；
- `claim(attempt)` 只接受更新的序号（CAS），保证旧 attempt 的迟到失败不覆盖新成功；
- `markBroken(attempt)`：claim + 设置固定窗口（建议 30 s，可选配置），窗口内不再试 H3；
- `clear(attempt)`：claim + 清零；
- `InterfaceUpdated` 里与 `CloseAllConnections` 一起清零（现有回调，无需新代际 API）。

**只有这些算 H3 传输失败**（`isH3TransportFailure`）：`{Handshake, Path}` 且不是超时；`cronet.NetError` 的 QUIC 协议/握手族。**407 / 其它状态 / 取消 / EOF / 超时一律不降级**。

**必须同时写的测试**（fake client，无需 Native）：UDP 拒绝与黑洞都降级一次、H3 成功、慢 200（ambiguous 不写 broken）、407 不降级、H2 也坏、caller cancel、网络切换竞态（-race）、Close 后 Dial、窗口到期复苏、cached 首包场景不重放、Alt-Svc 负向对照（同 Engine 连续 H3→H2→H3 的真实 negotiated protocol）。

**默认值**：`quic_fallback_to_h2: false`；`quic:false` 与 `quic:true` 的现有语义完全不变；命名待按 `option/naive.go` 的相邻字段风格定稿。

### 6.3 为什么这仍值得做（反方证据一并列出）

- 反方：naiveproxy #669 维护者明确拒绝"同配置混用 HTTPS/QUIC"，主张由客户端指定协议；#756 作者也说 QUIC 不比 TLS 快。
- 正方：轮子已经证明 forced-H3 在 UDP 被拒/黑洞时 4.0 s 失败且**不会**回落，而同端口 H2 6 ms 可用；这是**可用性**缺口，不是速度卖点。做成默认关闭的可选项，与维护者的"客户端控制"并不冲突。

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
| `84760a372` | `docs(naive): record the round-one performance and integration construction`（本报告） |
| `df604e303` → 本提交 | 报告自身的 SHA/推送记录修正 |

### 8.2 本轮真正跑过的验证

| 命令 | 结果 |
|---|---|
| `go test -tags "$(OTHERS)" ./protocol/naive/... ./transport/http/ ./common/httpclient/ ./protocol/group/ ./adapter/` | **PASS**（naive 0.35s / quic 0.04s / transport/http 25.3s / httpclient 0.02s / group 3.9s / adapter 0.7s） |
| `go test -tags "$(WINDOWS)" ./protocol/naive/` | **PASS**（含新的 14 行 facts 矩阵 + 4 个 dial 测试 + 既有 dependency_pin 测试） |
| `go vet -tags "$(WINDOWS)" ./protocol/naive/` | **clean** |
| `gofmt -l protocol/naive/` | **空** |
| 变异验证（`WriteZeroN`→`Extend`） | **按预期变红**，恢复后绿 |
| 端到端 407 / 200（真实 Native + 真实 sing-box 出站） | **407 变错误（curl 502）**；**200 正常** |
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
- **触碰的真实函数**：`dialNaiveTunnel`、`naiveHandshakeFailureFacts`、`naiveResponseStatus`、`(*Outbound).DialContext`、`(*naiveDialer).DialContext`、`writeFrame`/`writeBufferWithPadding`/`writeChunked`（仅测试）
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
