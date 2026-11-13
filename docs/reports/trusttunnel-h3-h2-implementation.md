# TrustTunnel H3 → H2 自动回退：施工报告（2026-11-13）

本报告记录 `quic_fallback_to_h2` 的现场勘察、设计决定、实现接线与验证结果。**知识库生产基线未因此更新。**

## 0. 现场记录（执行时读取，不是事先基线）

| 项 | 值 |
| --- | --- |
| 主开发分支 | `testing`（云端唯一长驻分支；`origin/HEAD -> testing`） |
| 起点 SHA | `150e62778757658a85a2ba86e79d497d2f0fb9da`（`docs(release): record the verified v0.1.45 kernel and Satelite publication`） |
| 本机环境 | Windows 10，Go 1.25.5 windows/amd64，标签取 `release/DEFAULT_BUILD_TAGS_WINDOWS` |
| 最终提交 / 远端 | 见第 8 节（推送后回填） |

起点依据：执行前 `git fetch origin`，`testing` 与 `origin/testing` 同为 `150e62778`，工作区干净；没有另建分支。

## 1. 已知信息中，哪些仍成立、哪些被现场推翻

| 前期说法 | 现场结论 |
| --- | --- |
| 云端已有原生 TrustTunnel 客户端/服务端、H2/H3 传输、TCP stream 与 UDP association | **成立**：`protocol/trusttunnel`、`transport/trusttunnel`，`option/trusttunnel.go` 已含 `quic`、凭据、健康检查、空闲、拥塞控制 |
| `connectSession` 只按固定协议分派，不是自动回退 | **成立**：原实现为 `switch c.protocol`；本次改造点就在这里 |
| 已有共享 offer、健康检查、重连、Reset、RetireSuspect、资源准入 | **成立**：`getSession`/`clientOffer`/`completeOffer`、`openStream`、`Reset`、`RetireSuspect`、`admitSessionFor` 均在，本次全部复用 |
| H3→H2 已有独立封装可直接套用 | **部分推翻**：`common/httpclient` 是 `RoundTrip` 竞速、`transport/http` 是 HTTP 隧道自己的版本回退、Naive 绑定 Cronet 引擎。它们的接口、可重试边界与资源归属不匹配 CONNECT 会话创建，**没有直接套用，也没有复制整套**；只借鉴"独立预算 + 有界记忆 + 不把取消当远端故障"的判断 |
| 任务量"中等偏小" | **大致成立**：会话创建边界接入约 160 行生产代码（含注释），其余是测试、文档与 schema |

## 2. 一页设计决定

| 决策 | 结论与理由 |
| --- | --- |
| 接入点 | **会话创建层**（`Client.connectSession`）。它是唯一"尚未对目标发出任何业务请求"的边界：在这里换传输不会产生两次 CONNECT。生产调用者：`completeOffer` → `c.dialSession(dialCtx)`，由 `getSession` 在无可用会话时触发 |
| 复用方式 | **协议内适配**，不抽公共模块：回退需要 session 所有权、offer、admission、Reset/Retire 语义，这些都在 `transport/trusttunnel` 内；抽出公共层会把三种协议的生命周期混在一起。唯一新增的小对象是 `fallbackHTTP2Session`（只做标记与转发） |
| 时序 | **有界串行**，不是延迟竞速：H3 先跑，独立预算内失败才启动 H2。竞速会在同一时刻占用两条昂贵创建，而 admission 只按"一次创建"计费；串行保证任一时刻只有一条物理尝试 |
| 建立边界 | 选定点 = 会话构造返回后（`connectQUIC` 返回 http3 session / `connectHTTP2` 返回 http2 ClientConn）。此前失败都属"外层未建立"，可安全回退；此后（CONNECT、认证、目标拒绝）**不可回退**，也绝不重放 |
| 预算 | H3 单次尝试 `3s`（`h3FallbackBudget`），与既有 `staleConnectBudget` 同量级：覆盖 300–600ms RTT 下的多次往返，又远小于 QUIC idle timeout。创建上下文若带更短 deadline 则取更短值，**不延长**；无 deadline 时仅 H3 有界，H2 由创建本身（offer 生命期/拨号器）约束 |
| 失败分类 | **可回退**：UDP dial 失败、QUIC 握手失败、ALPN 协商不符、自身预算耗尽（`context.DeadlineExceeded` 且创建上下文仍存活）。**不可回退**：创建上下文已结束（`ctx.Err() != nil`，含 Close/Reset 中止）、`context.Canceled`、`ErrQUICNotIncluded`。认证/证书/目标拒绝发生在 CONNECT 阶段，根本不在本边界内 |
| 状态作用域 | 一个 `Client` = 一个节点；冷却存于 `Client.h3CordonUntil`，`Reset()`（`InterfaceUpdated` 换网时调用）清除它 —— 结论属于观察它的网络代次，不跨代次继承 |
| 恢复 | 冷却 `30s` 到期后，**下一次真实需求**在 `getSession` 里把仍然健康的、由回退产生的 H2 会话置为 retiring（与 `RetireSuspect` 同一边界），让新会话重新尝试 H3；旧 H2 上的 stream 继续跑完。无定时器、无主动探测拨号 |
| 兼容 | 默认 `false`：关闭时 `connectSession` 行为与旧代码完全相同（严格 H3 仍严格）。开启要求 `quic: true`；ALPN 为空时派生 `[h3, h2]`，显式 ALPN 必须同时含两者，否则启动报错。H2 腿使用独立 TLS 配置（只含 `h2`），不修改 H3 腿的共享配置。构建标签不变：无 `with_quic` 时开启该选项会在建连时报 `ErrQUICNotIncluded`（与既有 H3 行为一致），无 `with_trusttunnel` 时整个出站不注册 |

## 3. 最终用户行为

开启后：

1. 新会话先尝试 H3（UDP → QUIC 握手 → ALPN h3）。
2. 若这次尝试在 3 秒内失败或耗尽，同一个节点立刻用 H2（TCP → TLS(ALPN h2) → HTTP/2）建立这个会话，CONNECT 在 H2 上发送；TCP stream 与 UDP association 都走它。
3. 随后 30 秒内新建会话直接走 H2，不再重复支付 QUIC 超时；期间已有会话继续服务。
4. 冷却到期后，下一个真实需求会退休当前 H2 会话并再试一次 H3；成功则回到 H3，失败则再次回退（每 30 秒最多一次 H3 尝试，全部由需求触发）。
5. 换网（`InterfaceUpdated` → `Reset`）清除该判断。

下列**不会**发生：已建立 stream 的迁移、业务字节重放、向同一目标发两次 CONNECT、把认证失败/目标拒绝当"UDP 坏了"、定时探测或新增长驻 goroutine。

配置示例（节选）：

```json
{
  "type": "trusttunnel",
  "tag": "tt-out",
  "server": "proxy.example.com",
  "server_port": 443,
  "username": "user",
  "password": "replace-me",
  "quic": true,
  "quic_fallback_to_h2": true,
  "tls": {"enabled": true, "server_name": "proxy.example.com"}
}
```

前提：服务端在同一节点上同时提供 QUIC 与 TCP/H2 入口。客户端不能凭空创建它。

## 4. 真实接线图（生产路径，不是只有测试调用）

```
用户配置 JSON
  └─ option.TrustTunnelOutboundOptions.QUICFallbackToH2   option/trusttunnel.go:26
       └─ protocol/trusttunnel.NewOutbound 校验与 ALPN    protocol/trusttunnel/outbound.go:80,93,103
            └─ trusttunnel.NewClient(QUICFallback: …)     protocol/trusttunnel/outbound.go:143
                 └─ Client.connectSession                 transport/trusttunnel/client.go:340
                      ├─ h3Cordoned ? → connectFallbackHTTP2        client.go:347-350
                      ├─ h3AttemptContext（预算/父 deadline）       client.go:393
                      ├─ connectH3 = connectQUIC                  client.go:318 → client_quic.go:72
                      ├─ 失败且可回退 → cordonH3 + connectFallbackHTTP2
                      │                                            client.go:368-376
                      ├─ connectH2 = connectHTTP2（用 tlsConfigH2） client.go:319 → client_http2.go:18,27-29
                      └─ fallbackHTTP2Session{...}                 client.go:460
                           └─ publishOffer → session.fallbackH2     client.go:861
                                └─ getSession 冷却到期时退休该会话   client.go:764
                                     └─ Reset() 清除判断            client.go:?
```

`connectSession` 由 `completeOffer` 里的 `c.dialSession(dialCtx)` 调用（`client.go` 中 `dialSession = connectSession` 的赋值在 `NewClient` 末尾），因此确实在每次新会话创建的生产路径上，而不是由测试拼接。

## 5. 失败与恢复的状态转移

| 状态 | 触发 | 结果 |
| --- | --- | --- |
| `H3-preferred` | 初始；或冷却到期后的真实需求 | 尝试 H3（预算 3s / 父 deadline 更短者） |
| `H2-fallback` | H3 建立失败且可回退 | 同一创建内建立 H2；打冷却；发布 H2 会话；`fallbackH2=true` |
| `H2-cordoned` | 冷却期内新需求 | 直接 H2，无 H3 尝试 |
| `H3-probe` | 冷却到期 + 健康 H2 回退会话 + 新需求 | 退休该 H2（旧流继续），新创建再试 H3 |
| `H3-preferred`（复原） | 探测成功 | 清除冷却；H3 会话发布 |
| `aborted` | Close / Reset / 创建上下文结束 | 不回退、不记录判断；`Reset` 另清除冷却 |
| `both-failed` | H3 与 H2 都失败 | 返回 `E.Errors(h3Err, h2Err)`，两者原因都在链上 |

owner 责任：UDP socket 与 QUIC conn 由 `connectQUIC`（失败即关闭 socket）；H2 的 TCP/TLS/ClientConn 由 `connectHTTP2`；发布后的会话由 `clientSession` 唯一持有并在 `close` 中释放；被丢弃的迟到成功由 `publishOffer` 关闭一次。

## 6. 验证

标签统一取 `release/DEFAULT_BUILD_TAGS_WINDOWS`（含 `with_quic`、`with_trusttunnel`）。

### 6.1 单元测试（本机已跑）

| 范围 | 命令 | 结果 |
| --- | --- | --- |
| 回退行为 12 例 | `go test -count=1 -timeout 120s -tags "<tags>" ./transport/trusttunnel -run TestFallback -v` | PASS（0.28s） |
| 配置面 2 例 + 生成 schema 断言 | `go test -count=1 -timeout 120s -tags "<tags>" ./option -run 'TestTrustTunnelFallback|TestTheGeneratedSchemaOffers' -v` | PASS（0.14s） |
| 出站校验（新增 4 例：缺 quic、ALPN 缺 h2、默认派生、显式 ALPN） | `go test -count=1 -timeout 180s -tags "<tags>" ./protocol/trusttunnel -run TestOutbound -v` | PASS（0.11s） |
| 生成 schema 与源一致 | `go test -count=1 -tags "<tags>" ./cmd/sing-box -run TestTrackedSchemaMatchesTheGenerator` | PASS（0.02s） |

回退用例覆盖：关闭时严格 H3（H2 尝试数 0）；H3 失败后恰好 1 次 H2 且会话承载 CONNECT；中止的创建不回退也不打冷却；H3 尝试受预算约束且仍在预算内失败后 H2 成功；创建 deadline 更短时不延长；父有富余时间时 H2 拿到剩余时间；冷却期内不再试 H3、到期后由真实需求再探一次；`Reset` 清除判断；H3 成功清除判断；两腿都失败时两个原因都在链上；5 个并发需求只产生 1 次 H3 尝试、至多 1 次 H2；H2 腿的 ALPN 只含 `h2`。

### 6.2 本机构建矩阵（本机已跑）

见第 7 节结果表（`go build` 四种标签组合、`go vet`、全量 `go test`）。

### 6.3 未在本机运行、只记录

| 项 | 原因 | 复现方式 |
| --- | --- | --- |
| macOS arm64 的单元测试与 headless 冒烟 | 本机为 Windows；GitHub macOS runner 当前经常分不到机器，用户指示先不重跑 | 恢复后对同一 SHA 派发 `Verify` 与 `macos / macos-arm64`（`client-macos.yml`） |
| CLI 客户端（Apple/Android/Desktop）消费本内核 | 需对应平台工具链 | 正式发布流程的 `kernel-release` 编排 |
| 真实 UDP 黑洞 + 真实 TrustTunnel 服务端互通 | 本机没有可用的对端与服务端 QUIC/TCP 双入口 | 台架：同一节点同时开 QUIC 与 TCP/H2，注入 UDP 丢弃，观察 H2 会话建立与载荷 |
| Windows 管理员用例、`common/tls` TLS1.3、`tlsspoof`/`windivert` | 与既有基线一致的环境限制（CI 矩阵同样标注 FAIL/BLOCKED） | CI `verify` 与平台作业 |

**没有把未跑项写成 PASS。**

## 7. 验证结果（本机实测）

本机环境：Windows 10 22H2，Go 1.25.5 windows/amd64，标签取 `release/DEFAULT_BUILD_TAGS_WINDOWS`（含 `with_quic`、`with_trusttunnel`）。

### 7.1 构建与静态检查

| 项 | 命令 | 结果 |
| --- | --- | --- |
| 构建（Windows 档位） | `go build -tags "<WIN>" ./...` | PASS，20s |
| vet（Windows 档位） | `go vet -tags "<WIN>" ./...` | PASS，19s |
| 构建（无构建标签，走 stub） | `go build ./...` | PASS，34s |
| 构建（去掉 `with_quic`） | `go build -tags "<WIN minus with_quic>" ./...` | PASS，38s |
| 构建（去掉 `with_trusttunnel`） | `go build -tags "<WIN minus with_trusttunnel>" ./...` | PASS，22s |
| test 模块静态检查 | `cd test && go vet ./...` | PASS |
| test 模块构建 | `cd test && go build ./...` | 与干净树**一致**失败：`runtime.main_main·f: function main is undeclared in the main package`；不是本次改动引入（同一工作树在 stash 后复现） |
| 知识库事实门禁 | `python scripts/ci/check-knowledge-facts.py` | PASS |
| 发布冻结一致性 | `python scripts/ci/release-freeze.py verify --lock release/source-lock.json --strict` | PASS（pin 未变） |

### 7.2 全量单元测试（本机）

命令：`go test -vet=off -count=1 -timeout 60m -tags "<WIN>" ./...`
结果：`EXIT=1`，518s；**107 个包 PASS**、79 个包无测试文件；3 个失败均与环境有关，且已单独复核：

| 失败包 | 实际原因 | 判定 |
| --- | --- | --- |
| `common/tlsspoof` | `open WinDivert: SCM: Access is denied`（需要管理员权限） | 环境限制；与 CI 基线记录的 Windows 权限失败一致 |
| `common/windivert` | 同上 | 环境限制 |
| `protocol/mieru`（2 个 e2e 用例） | `could not find a port free for both TCP and UDP after 200 attempts`（Windows 保留端口段 + 全量并行负载） | 负载/环境：在**干净树**与本树**单独运行**这两个用例均 PASS（10.1s） |

### 7.3 受影响包（本机单独运行）

| 包 | 结果 |
| --- | --- |
| `transport/trusttunnel` | `ok` 36.6s —— 含 13 个回退用例；其中 `TestFallbackCarriesRealPayloadOverTheRecoveredHTTP2Session` 在回退后的真实 HTTP/2 会话上完成 TCP 256 KiB echo 与 UDP 1200 B 往返 |
| `protocol/trusttunnel` | `ok` 0.41s（新增 4 个校验用例） |
| `option` | `ok` 0.28s（解码/序列化 + 生成 schema 断言） |
| `cmd/sing-box -run TestTrackedSchemaMatchesTheGenerator` | PASS（`docs/schema.json` 已重新生成） |

### 7.4 CI（推送后自动触发）

第一次推送 `d5949d74`：

| 运行 | 结果 |
| --- | --- |
| [Verify 43372368131](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43372368131) | **success**（verify 与 race checks 都通过） |
| [Android Core ARM64 43372368176](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43372368176) | **success** |
| [linux-kernel-verify 43372368194](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43372368194) | **failure**：`trusttunnel` 作业在"transport and protocol tests (with_trusttunnel)"失败 |

该失败是**真实缺口，不是环境问题**：`linux-kernel-verify` 用 `with_trusttunnel` 且**不带 `with_quic`** 跑协议测试，而在这种构建里 `quic: true` 会在问回退之前就以 `ErrQUICNotIncluded` 拒绝；我新加的两个"接受 QUIC 回退配置"用例因此在无 HTTP/3 的构建里失败。修复提交 `e15e335` 把它们按仓库既有约定移入 `//go:build with_quic` 的 `outbound_quic_test.go`，并把该层能断言的（两种 ALPN 拼写都被接受、都产生 client）与只在 transport 层可见的（H3 腿 `[h3, h2]`、H2 腿 `[h2]`）分开。

修复后重推 `e15e335`：

| 运行 | 结果 |
| --- | --- |
| [Verify 43373762516](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43373762516) | **success** |
| [Android Core ARM64 43373762445](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43373762445) | **success** |
| [linux-kernel-verify 43373763484](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43373763484) | **success**（含"the tag set still builds without the feature"） |

macOS 作业本轮**没有等待也没有重跑**：按用户指示，本机为 Windows，且 GitHub 的 macOS runner 近期经常分不到机器，明天白天再重跑。因此 macOS 的单元测试、headless 冒烟与 Apple 客户端构建在本报告中保持"未运行"，不是 PASS。


## 8. 提交、推送与云端链接

| 项 | 值 |
| --- | --- |
| 起点 SHA | `150e62778757658a85a2ba86e79d497d2f0fb9da` |
| 功能提交 | `d5949d749291e814f9b999d01c079cd0881c5db1`（11 个文件，+838 / -29） |
| CI 修复提交 | `e15e335ed48fac2a6956d97aef8b4e2b754fbf84`（2 个文件，+58 / -11；`with_quic` 门控） |
| 最终验证 SHA | `e15e335ed48fac2a6956d97aef8b4e2b754fbf84`：本机各套验证 + Verify / Android Core ARM64 / linux-kernel-verify 全绿 |
| 分支 | `testing`（未新建分支、未 force push、未发布 Release、未上传 TestFlight） |
| 远端回读 | `git ls-remote origin refs/heads/testing` = `e15e335ed48fac2a6956d97aef8b4e2b754fbf84`，与本地一致 |
| 报告提交 | 添加本文件的 docs 提交（`git log -1 -- docs/reports/trusttunnel-h3-h2-implementation.md`） |

本任务未创建 PR、未发布版本；`README.md` 与 `CHANGELOG.md` 按要求未改动。

## 9. 未解决项与限制

- **只做串行回退**：如果 H2 也不通，这次创建会付 H3 预算 + H2 的失败代价；没有并发竞速，因为 admission 与资源模型按"一次创建"计费，串行是唯一不用重新计费的做法。
- **冷却常量是判断而非测量**：3s 与 30s 的理由写在代码注释与第 2 节；真实黑洞台架的样本会更有说服力，本轮未取得。
- **回退不由已建立 H3 会话的传输故障触发**：当前只在"创建新会话"时判断。已建立 carrier 之后出现传输故障时，`openStream` 既有的重连会再走一次创建，从而自然获得 H2 机会；没有额外把一个 stream 的错误升级为节点级判断（避免误杀）。
- **`Client.Reset()` 清除冷却**是刻意的：换网后的旧结论不继承；代价是换网后第一次创建会再付一次 H3 预算。
- **服务端前提未在本轮验证**：没有真实的双入口节点，故"服务端支持同节点 TCP/H2"只是客户端设计前提，不是互通结论。
