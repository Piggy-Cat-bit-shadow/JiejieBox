# TrustTunnel H3 → H2 回退：定向 Debug 与对抗审核（2026-11-13）

本报告是对 `quic_fallback_to_h2`（提交 `d5949d74`、`e15e335`）的独立核验记录：我按审核指引重新走接线、按攻击矩阵制造失败、定位并修复了两个真实缺陷，并逐项标注哪些项目真正跑过、哪些受环境限制。**没有把未跑项写成 PASS。**

## 0. 现场记录

| 项 | 值 |
| --- | --- |
| 主开发分支 | `testing`（`origin/HEAD -> testing`） |
| 审核起始 SHA | `49fc0026e93d127102c34fb944f4d9c9b7740b00`（施工报告提交） |
| 审核范围如何确定 | 当前源码 + `d5949d74`（功能）、`e15e335`（`with_quic` 门控修正）两个提交的 diff + `docs/reports/trusttunnel-h3-h2-implementation.md`；不按"最近一个提交"猜 |
| 修复后验证 SHA | `8789a0de69be6e7e2b829ad523be0265d2a7c65e`（仅本报告追加的文档提交在其后） |
| 本机环境 | Windows 10，Go 1.25.5 windows/amd64，MSYS2 gcc（race 需要 cgo），标签 `release/DEFAULT_BUILD_TAGS_WINDOWS` |

## 1. 对施工设计的独立理解

目标不变：**新会话**先试 H3，外层建立失败时用同一节点的 H2 承载这次会话；坏路径短期记住，冷却后由真实需求再试 H3；已交付的流不迁移、不重放；认证/证书/目标拒绝/取消不被回退绕过。

审核后确认成立的判断：

- 回退点必须在**会话创建**层。业务 CONNECT 一旦发出，任何"再选一次"都会变成重放；这一层的选择在本次审核中没有发现重复 CONNECT 的路径。
- 串行而非竞速：`completeOffer` 对一次逻辑创建只拿一次准入许可，竞速会让"一次创建"真实占用两条昂贵连接；审核未发现需要竞速才能满足用户结果的证据。
- 冷却必须与网络代次绑定：`Reset` 由 `InterfaceUpdated` 调用，是既有的代次边界。
- 恢复必须由需求驱动：只要 H2 健康被复用，任何定时器/主动探测都违背"不新增常驻成本"。

审核中被证据修正的地方（细节见第 3 节）：

- 施工时把"HTTP/3 尝试的任何非取消错误"都当作路径故障。**证书校验失败与 TLS alert 不是路径故障**，它们会被同一张证书在对端重复拒绝，且不该让节点在 30 秒内被判定"UDP 不可用"。
- 施工时只挡住了"旧失败污染新代次"，**没有挡住"旧成功清掉新代次的判断"**。

## 2. 接线核验（先证明确实执行）

追踪路径与现场核对：

```
JSON → option.TrustTunnelOutboundOptions.QUICFallbackToH2        option/trusttunnel.go:26
   → NewOutbound 校验/ALPN 派生                                  protocol/trusttunnel/outbound.go:80,93,103
   → trusttunnel.NewClient(QUICFallback: …)                       protocol/trusttunnel/outbound.go:143
   → Client.connectSession                                       transport/trusttunnel/client.go
        ├─ h3Cordoned → connectFallbackHTTP2
        ├─ h3AttemptContext（预算 clamp）
        ├─ connectH3 = connectQUIC                               client_quic.go:72
        ├─ 失败归类 → cordonH3 → connectFallbackHTTP2
        └─ connectH2 = connectHTTP2（tlsConfigH2）                client_http2.go:18,27
   → publishOffer → session.fallbackH2                            client.go（publishOffer）
   → getSession 冷却到期退休回退会话                              client.go（getSession）
```

证明要点（都在本机跑过，命令见第 6 节）：

- `option` 包用**真实 JSON** 解码出 `quic_fallback_to_h2: true`，并验证 `omitempty` 不会把它写回。
- `protocol/trusttunnel` 用解码后的选项构造出站，确认 `NewOutbound` 接受并产出 client（`//go:build with_quic` 文件）。
- `transport/trusttunnel` 的回退用例直接驱动 `DialContext` → `getSession` → `connectSession` → 两条腿，不是调用某个 helper：`TestFallbackOpensHTTP2WhenTheHTTP3AttemptFails`（真实 `DialContext` + 计数）、`TestFallbackCarriesRealPayloadOverTheRecoveredHTTP2Session`（真实 HTTP/2 服务端 + 真实 CONNECT + 载荷）。
- TCP 与 UDP 走同一创建：`DialContext` 与 `ListenPacket` 都经由 `openStream → getSession`，后者在 `TestFallbackCarriesRealPayloadOverTheRecoveredHTTP2Session` 中被实际调用（UDP 1200 B 往返）。
- 无 QUIC / 无 TrustTunnel 构建：`go build` 三种标签组合通过；回退用例在无 `with_quic` 构建里 SKIP 并打印原因，不会假 PASS（该缺口正是 `e15e335` 修的 CI 失败）。

## 3. 实际发现的缺陷

### D1（已修）：证书/TLS 结论被当成"UDP 路径坏了"

| 项 | 内容 |
| --- | --- |
| 严重性 | 中：多付一次握手、错误信息混淆、把节点在 30 秒内推离 H3 |
| 触发条件 | QUIC 握手返回证书/名称/alert 类错误（未知 CA、过期、hostname 不符、服务端 alert），且 `quic_fallback_to_h2` 开启 |
| 用户影响 | 用户在配置错误或证书过期时看到的是"H3 失败 + H2 失败"的双重错误，且该节点 30 秒内不再尝试 H3；若证书只对 UDP 侧失效（罕见）则会把可用路径记成坏路径 |
| 根因 | `fallbackEligible` 只排除了 `context.Canceled` 与 `ErrQUICNotIncluded`，把 x509 / `*tls.CertificateVerificationError` / `tls.AlertError` 一并当作路径证据 |
| 最小复现 | 见 `TestFallbackEligibilityClassification` 的 6 个负例与 `TestFallbackDoesNotReactToTLSVerdicts`：修复前 6 个分类用例 FAIL（`unknown_authority`、`certificate_verification`、`hostname_mismatch`、`invalid_certificate`、`remote_alert` 等 expected false got true），集成用例同样 FAIL（H2 被调用、冷却被打开） |
| 修复 | `transport/trusttunnel/client.go`：`fallbackEligible` 增加证书校验错误、`x509.UnknownAuthorityError`、`x509.HostnameError`、`x509.CertificateInvalidError`、`tls.AlertError` 的 `errors.As` 排除，并写明"同一张证书在同名 H2 腿会被同样拒绝"的理由 |
| 验证 | 同一批用例修复后 PASS；ALPN 不符仍被判为可回退（服务端只答 H2 正是功能要覆盖的场景） |

### D2（已修）：旧代次的迟到成功会清掉新代次的判断

| 项 | 内容 |
| --- | --- |
| 严重性 | 低-中：只在"旧代次握手在 Reset 之后才成功、且新代次已经写下判断"的交错下发生；结果是新网络多试一次 H3（把刚建立的坏路径结论抹掉），并可能让一条旧网络的连接被发布周期短暂持有 |
| 触发条件 | 旧创建的 H3 尝试在飞行中 → `Reset()`（换网）→ 新代次创建失败并写入冷却 → 旧尝试随后成功 |
| 用户影响 | 换网瞬间可能多付一次 QUIC 尝试；不产生重复 CONNECT、不泄漏连接（`publishOffer` 的丢弃路径会关闭它），但判断归属错误 |
| 根因 | 成功分支只做了 `clearH3Cordon()`，没有校验创建上下文是否已被 Reset/Close 取消；旧成功与新失败之间没有代次排序 |
| 最小复现 | `TestFallbackLateHTTP3SuccessDoesNotClearTheNewGenerationsVerdict`。负向对照已实际执行：把成功分支的 `ctx.Err()` 守卫移除后该用例失败（`Should be true` on `client.h3Cordoned()`），恢复后通过；迟到连接在修复后为 `closed=true` |
| 修复 | `transport/trusttunnel/client.go` 成功分支：若 `ctx.Err() != nil`（创建已被中止），关闭该连接、返回 `ctx.Err()`，**不清冷却**；与失败分支的 `ctx.Err()` 检查对称 |
| 验证 | 新用例 PASS；既有 18 个回退用例全部 PASS |

### H1（加固，非已证缺陷）

`Reset()` 原先在解锁之后才 `clearH3Cordon()`，理论上存在"创建在 Reset 之后开始、却读到旧网络判断"的窄窗口。现把清除移入与丢弃 pending 相同的锁区。没有构造出用户可见失败，故只作为顺序加固记录，不计入缺陷数。

## 4. 攻击矩阵逐项结论

图例：PASS = 有本机/CI 证据；FAIL = 发现缺陷（已修并复验）；BLOCKED = 本机无法制造该条件；N/A = 不在本次范围。

| 项 | 结论 | 证据 / 理由 |
| --- | --- | --- |
| A1 原 H2-only 配置 | PASS | 回退只能在 `protocol == h3` 分支进入（`connectSession` 首行）；`with_trusttunnel` 无 quic 构建的整个包测试通过 |
| A2 严格 H3、回退关闭 | PASS | `TestFallbackDisabledKeepsStrictHTTP3`：H3 失败直接返回，H2 调用数 0 |
| A3 显式开启 | PASS | 解码 + 出站 + client 三段证据（第 2 节） |
| A4 开回退但 `quic=false` | PASS | `TestOutboundValidation/fallback without quic`：启动即报 `quic_fallback_to_h2 requires quic: true` |
| A5 无 QUIC / 无 TrustTunnel 构建 | PASS | 四种标签组合 `go build` 通过；无 `with_quic` 时回退用例 SKIP（打印原因）；无 `with_trusttunnel` 时出站不注册 |
| A6 ALPN 默认 / h3-only / 双写 / 非法 | PASS | 默认派生 `[h3,h2]`、显式双写接受、`h3` only 拒绝并指名缺 `h2`（`protocol/trusttunnel` 两个测试文件）；H2 腿配置只含 `h2`（`TestFallbackKeepsTheHTTP2LegsOwnALPN`） |
| A7 H3+uTLS、TLS 关闭等原有校验 | PASS | 原有校验用例保持通过，回退未放宽任何一条 |
| B1 H3/H2 都正常 | PASS | `TestFallbackPrefersHTTP3WhenItWorks`：H3 一次、H2 零次、发布会话无回退标记 |
| B2 UDP 立即拒绝、H2 正常 | PASS | `TestFallbackOpensHTTP2WhenTheHTTP3AttemptFails`（H3 立即错误 → 恰好一次 H2） |
| B3 UDP 静默黑洞 | **BLOCKED_BY_ENVIRONMENT** | 本机没有"同节点 QUIC+TCP 双入口"的真实服务端，也不能在不改用户防火墙的前提下注入真实 UDP 丢弃。可用的替代证据是：注入**不返回、直到自身预算耗尽**的 H3 腿（`TestFallbackBoundsTheHTTP3AttemptAndStillOpensHTTP2`、`TestFallbackLeavesTheRemainingTimeToHTTP2`），证明预算是真实 deadline 而不是立即返回的 mock；真实黑洞下的首字节与完整载荷 **未验证** |
| B4 握手成功但 HTTP setup 不就绪 | PASS（按设计） | 回退边界 = 会话构造返回（QUIC 握手 + http3 ClientConn 建立）。此后的不可用在 CONNECT 阶段体现，由调用方 deadline / `staleConnectBudget` 兜底，**不**触发第二条连接；这是设计选择，不是遗漏 |
| B5 CONNECT 已发、服务端慢答 | PASS（不重复连接） | 回退只在创建层，CONNECT 阶段没有任何分支创建第二条会话；`openStream` 的重试只新建"会话"，不复发已发出的 CONNECT 载荷 |
| B6 两腿都失败 / 只开 UDP | PASS | `TestFallbackReportsBothLegs`：两个原因都在错误链上，尝试有界（H3 一次 + H2 一次） |
| B7 短父 deadline / 无 deadline / 预算耗尽 | PASS | `TestFallbackHonoursAShorterCreationDeadline`、`TestFallbackLeavesTheRemainingTimeToHTTP2`、`TestFallbackBoundsTheHTTP3AttemptAndStillOpensHTTP2`。说明：生产创建上下文由 shared offer 承载（不带调用方 deadline），调用方期限只约束"等待"，这是既有设计；预算 clamp 针对直接调用与将来接线 |
| C 错误分类（407/403/404/405、X-Warning、GOAWAY/QUIC 认证码、证书、SNI、ALPN、准入拒绝、取消、内部超时） | **FAIL → 已修（D1）** | 证书/alert 曾被当路径故障；修复后分类表 12 例 PASS。CONNECT 阶段的状态码/认证语义未改动，由既有 `StatusError`/`ErrAuthenticationRequired` 测试覆盖 |
| C 单 stream reset 不判 carrier 坏 | PASS | 回退不读取 stream 级错误；冷却只在**创建**失败时写 |
| D 共享创建（1/32/128 并发） | PASS | `TestFallbackKeepsOneSharedCreation` 三档：H3 尝试恒为 1，H2 ≤ 1 |
| D 等待者取消 / 短 deadline 先退 | PASS | 既有 `client_shared_offer_test.go` 的等待者用例保持通过；`TestFallbackDoesNotFireOnAnAbortedCreation` 覆盖"创建被中止不算判断" |
| D 最后等待者退出 | PASS（既有设计） | 创建在 client 生命期内继续，供后续需求复用；这是 `getSession` 注释写明的既有语义，不是本次引入 |
| D 迟到成功关闭、不发布 | PASS | `TestFallbackLateHTTP3SuccessDoesNotClearTheNewGenerationsVerdict`（迟到连接 closed）+ 既有 `publishOffer` 丢弃用例 |
| D 冷却边界并发 | PASS | 单飞创建 + `TestFallbackKeepsOneSharedCreation`；并发下不会每个等待者各探测一次 |
| D 准入拒绝不污染偏好 | PASS | `admitCreation` 在 `connectSession` 之前执行，拒绝时根本不会进入回退；许可只在一次创建内持有一次（串行两腿不嵌套申请） |
| E1/E2 旧代次失败/成功与 Reset 交错 | **FAIL → 已修（D2）**；失败域 PASS | `TestFallbackResetForgetsTheVerdict`、`TestFallbackDoesNotFireOnAnAbortedCreation`、D2 新用例 |
| E3 H2 胜出但未发布 → Close | PASS | `publishOffer` 的 `discarded` 分支关闭迟到结果；既有用例 + D2 用例复用同一路径 |
| E4 校验通过 → Reset/Close → 副作用 | PASS（D2 修复后） | 成功/失败两侧都在写状态前检查 `ctx.Err()`；H1 把 Reset 的清零与丢弃 pending 放在同一锁区 |
| E5 回退成功后 cancel 不影响载荷 | PASS | `TestFallbackCarriesRealPayloadOverTheRecoveredHTTP2Session`：函数返回后仍完成 TCP 256 KiB 与 UDP 1200 B |
| E6 Trim/idle 与恢复交错 | PASS（源码 + 既有测试） | `CloseIdleConnections` 不拨号（既有测试）；恢复退休走 `drainIfUnused`，只关自己拥有的会话 |
| F 冷却到期后真实需求触发 H3 恢复 | PASS | `TestFallbackCordonsHTTP3AndProbesAgainByDemand`：冷却内 H3 不重试、到期后由真实需求退休健康 H2 并再试一次 |
| F 恢复不切断旧 H2 流 | PASS（源码 + 退休语义） | 退休走 `retiring` + `drainIfUnused`，与 `RetireSuspect` 同一边界；第 5 节给了交错说明 |
| F 无新需求不建连 | PASS | 实现无定时器/无后台 goroutine；冷却只被读写 |
| F 换网清除旧冷却 | PASS | `TestFallbackResetForgetsTheVerdict` |
| F 一个节点不污染另一个 | PASS | `TestFallbackVerdictIsPerNode` |
| G TCP 双向完整字节 | PASS | 256 KiB echo 校验（回退后的真实 H2 会话） |
| G UDP 多目标/边界 | PASS（单目标） | 回退会话上 1200 B 往返；多目标/域名解析沿用既有测试（未改动该层） |
| G 两 stream 共享、互不误杀 | PASS（既有测试） | 本次未修改 stream 归属；`service_test.go` 的并发/限额用例保持通过 |
| G health_check / idle / Trim / Retire / Reset 作用于真实会话 | PASS（源码） | 这些路径都读 `client.session`，回退只是同一会话的另一种来源；`fallbackHTTP2Session` 仅转发 |
| G 日志/trace 不泄凭据 | PASS（源码） | 新增日志只打印 server 与错误；未新增 header/载荷输出 |
| H 共享模块直接回归 | N/A | 本次 diff 未修改 `common/httpclient`、`transport/http`、`protocol/naive`、MASQUE 等共享组件；不以此为名全面重测其它协议 |
| 未启用路径成本 | PASS（源码） | 关闭时 `connectSession` 首行即走单腿分支；回退新增的状态只有两个 atomic 与一次 `time.Now` 读取 |

## 5. 关键结果（回退、恢复、迟到结果、资源归还）

- **回退**：H3 外层失败 → 同一次逻辑创建内用 H2 建立 → 会话发布并承载真实 CONNECT；H2 腿的 ALPN 只有 `h2`（独立 TLS 配置），不会把 `h3` 的列表拿去握 H2。
- **恢复**：冷却到期后，真实需求把健康 H2 回退会话置为 retiring（旧流继续），新创建再试 H3；成功则发布 H3 会话。没有定时器，也没有"等 H2 自己断"的隐性依赖。
- **迟到结果**：旧代次的成功在 Reset 之后返回时被关闭且不清除新代次判断（D2）；失败侧本来就因 `ctx.Err()` 不写判断。
- **资源归还**：H3 失败路径由 `connectQUICOnce` 关闭 UDP socket；`publishOffer` 丢弃路径关闭迟到连接；退休会话只在最后一个 stream 归还后关闭；准入许可在一次创建内只取一次、释放一次。

## 6. 本机验证命令与结果

标签：`WIN = $(cat release/DEFAULT_BUILD_TAGS_WINDOWS)`。

| 验证 | 命令 | 结果 |
| --- | --- | --- |
| 回退行为（18 例） | `go test -count=1 -timeout 300s -tags "$WIN" ./transport/trusttunnel -run TestFallback` | PASS |
| 真载荷（回退后的真实 H2） | 同上，`-run TestFallbackCarriesRealPayloadOverTheRecoveredHTTP2Session` | PASS（TCP 256 KiB、UDP 1200 B） |
| race（整个 transport 包） | `CGO_ENABLED=1 CC=<msys2 gcc> go test -count=1 -race -tags "$WIN" ./transport/trusttunnel` | PASS（39.3s，无报告） |
| 受影响包 | `go test ... ./transport/trusttunnel ./protocol/trusttunnel ./option` | PASS |
| 构建矩阵 | `go build`：Windows 档位 / vet / 去掉 `with_quic` / 去掉 `with_trusttunnel` | 全部 PASS |
| 全量套件 | `go test -vet=off -count=1 -timeout 60m -tags "$WIN" ./...` | `EXIT=1`，520s；**111 个包 PASS**；仅 `common/tlsspoof` 与 `common/windivert` 失败（WinDivert `Access is denied`，需管理员，与基线一致）；上一轮记录的 `protocol/mieru` 端口失败本轮未复现 |
| 业务 CONNECT 计数 | 同上（`TestFallbackCarriesRealPayloadOverTheRecoveredHTTP2Session`） | PASS：回退后恰好 1 次 TCP CONNECT、1 次 UDP association，证明回退不是业务重试 |
| CI（推送后同 SHA） | Verify / Android Core ARM64 / linux-kernel-verify | 全部 success（见第 7 节） |
| 生成 schema 与门禁 | `go test ./cmd/sing-box -run TestTrackedSchemaMatchesTheGenerator`、`check-knowledge-facts.py`、`release-freeze.py verify --strict` | PASS / PASS / PASS |

未跑（BLOCKED_BY_ENVIRONMENT）：真实 UDP 黑洞与真实双入口服务端互通（本机无该服务端与隔离网络）；macOS 单元测试/headless 冒烟与 Apple 客户端构建（本机为 Windows，且 GitHub macOS runner 近期常分不到机器，按用户要求明天白天重跑）。这些项目在本报告中保持未运行，不是 PASS。

## 7. 提交与远端

| 项 | 值 |
| --- | --- |
| 审核起始 SHA | `49fc0026e93d127102c34fb944f4d9c9b7740b00` |
| 修复提交 | `8789a0de69be6e7e2b829ad523be0265d2a7c65e`（2 个文件：`transport/trusttunnel/client.go`、`transport/trusttunnel/client_fallback_test.go`；+247 / −32） |
| 修复后 CI（同 SHA） | [Verify 43378564177](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43378564177) **success**；[Android Core ARM64 43378564214](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43378564214) **success**；[linux-kernel-verify 43378563984](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43378563984) **success**（含无 `with_quic` 标签集的构建与协议测试） |
| 本报告提交 | 仅文档，位于 `8789a0de6` 之后；报告中的测试与 CI 结果都对应 `8789a0de6`，不声称在报告提交上重跑 |
| 分支 / 远端 | `testing`，`git ls-remote origin refs/heads/testing` 与本地一致；未新建分支、未 force push、未发 Release、未建 PR |

云端链接：https://github.com/Piggy-Cat-bit-shadow/JiejieBox/commit/8789a0de69be6e7e2b829ad523be0265d2a7c65e


## 8. 尚存限制与建议

- **真实黑洞效果未在本机验证**：预算与回退顺序由确定性 seam 证明（dealine 真实、不是立即返回），但"UDP 静默丢弃下 H2 仍在父期限/预算内完成并传完整载荷"需要真实服务端 + 隔离黑洞台架。建议明天在有双入口节点时补一轮，记录 H3 起止、H2 起止、首字节与载荷摘要。
- **3 秒预算与 30 秒冷却是判断值**：高 RTT（>600ms）链路上 3 秒覆盖 1-RTT QUIC 握手有富余，但极端卫星链路应实测；若需要调参，建议以常量（而非新配置项）先验证。
- **回退不覆盖"已建立 H3 carrier 之后"的传输故障**：那由既有 `openStream` 重连触发一次新创建，从而自然获得 H2 机会；没有把单 stream 错误升级为节点级判断（这是刻意避免的误杀）。
- **恢复探测依赖"新需求"**：长期无新流量的节点会一直停在 H2，这是"无新需求不建连"的直接代价，符合任务约束。
- **H3 握手成功但 HTTP/3 层不就绪**不在回退范围内（B4），由 CONNECT 的 deadline 兜底；若未来发现该窗口在实际网络中频繁出现，需要单独的"就绪"判据，不应把回退边界上移。
- 未覆盖平台：macOS / Apple 客户端、Android 实机、真实 TestFlight；共享模块（`common/httpclient`、`transport/http`、`protocol/naive`）本次未修改，故未重测其旧验收。
