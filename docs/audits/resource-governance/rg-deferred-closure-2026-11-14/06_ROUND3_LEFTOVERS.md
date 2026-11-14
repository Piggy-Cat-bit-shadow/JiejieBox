# 第三阶段遗留项：本轮能收尾的与明确留给下一轮的

> 依据：`rg-collection-2026-11-14` 的所有权地图（§4 观测缺口）、意外发现（F3/F4/F5/F8/F9/F12）、下一轮问题（Q1/Q3/Q5）与压力真值表（§4）。
> 本文件只处理"不需要 iOS 真机、不需要产品决策"的部分；其余逐条给出状态与触发条件，不伪装成已解决。

## 1. 本轮收尾（代码已改）

### 1.1 后台工作在 Critical 让路（round-3 §4 / Q3）

**第三阶段事实**：`protocol/group/urltest.go` 与 `common/urltest/*` 不读内存压力；`dns/client.go` 的 optimistic refresh 只读 power governor。Critical 期间后台探针与刷新继续创建连接、占用 gate 与 goroutine。

**收尾**：

- `resourceguard.BackgroundAllowed(ctx)` 成为唯一策略：无 guard → 允许；Armed → 允许（容量收缩，但仍需测量与新鲜答案）；Critical → 抑制。
- `URLTestGroup` 的**周期**健康轮询在每轮前检查；**手动/强制**轮询不限制（运维测量不是后台工作）。
- `Client.backgroundRefreshSuppressed()` 在 power governor 检查之前检查内存压力；刷新只是续期一个已服务过的缓存项，Critical 下不值得再发一次往返。
- 既有 guard 的升/降 dwell 不变；策略每轮读取，天然可逆。

**证据**：`common/resourceguard/background_test.go`、`dns/background_pressure_test.go`、`protocol/group/background_pressure_test.go`；三包 `-race` 通过；CI run `43550248961`（HEAD `3f92a0f8b`，`race and unit` + `burst matrix` 均 success）。

**限制**：策略只作用于"周期"与"投机"两类；手动测量、用户请求的解析不受影响。DNS 只抑制 optimistic refresh，不抑制正常查询。

### 1.2 准入队列高水位（round-3 §4 观测缺口 #3 / Q5 的一部分）

**第三阶段事实**：`Guard.Stats()` 只有瞬时 `Waiters`，审计只能采样估计峰值；无法回答"队列实际到达过多深"。

**收尾**：`Gate` 在 `pending` 变化处记录 `maxWaiters`，`Stats.MaxWaiters` 与 `MaxInflight` 并列；一次快照即可读到队列峰值。

**证据**：`TestGateStatsReportTheWaiterHighWaterMark`（1 槽 gate + 3 个排队者 → `MaxWaiters=3`，排空后保持）；resourceguard 全包与 `-race` 通过。

## 2. 本轮明确留给下一轮（不是"已解决"）

| 项 | 状态 | 原因 | 触发/下一步 |
|---|---|---|---|
| Q1 TUN 上游 per-flow 预算（NAT entry + goroutine + 首包） | `OPEN_BY_DEPENDENCY` | 资源在 `sing-tun`，主仓只能测量乘数；改入口准入会改变 UDP 首包语义 | 下一轮先做 200/500/1000 流乘数测量；若乘数 > 观测预算，再在 `sing-tun` 加只读 status 或入口预算 |
| Q5 sing-tun NAT / buffer pool 高水位观测 | `OPEN_BY_DEPENDENCY` | 需要依赖仓库导出计数（`UDPNat` 无 status、`buf` 池无高水位） | 与 Q1 同批；只读、不改热路径 |
| XMUX `max_concurrency` 模式下无物理连接硬上界 | `DECISION_REQUIRED` | 改默认值会改变 Xray 兼容行为 | 需要产品/互通决策；当前只在文档与矩阵中标注 |
| interactive 类比例上限 | `DECISION_REQUIRED` | 会改变"配置即语义"的既有约定 | 若下一轮扩大 reserve，再引入比例上限 |
| `Level()` 返回观察值而非已发布 plan | `DECISION_REQUIRED` | 现有调用方（报告、build gate）依赖当前语义；改成 applied 需要单独评审 | 若报告需要"已生效 plan"，先加只读 `AppliedLevel()` 而不是改 `Level()` |
| build gate 第二个 Guard 的 1 s dwell | `DECISION_REQUIRED` | 只影响 2 个 build 槽，风险与收益都小 | 与下一轮 build 预算一起评审 |
| 链式代理 chain depth / 物理创建计数 | `OBSERVATION_ONLY` | 一份 permit 对应多条物理连接是设计选择（避免自阻塞） | 先加 chain depth 到报告元数据，再决定是否引入 cost token |
| crisis reset 与网络切换的双重重连 | `OBSERVATION_ONLY` | 有软限（guard 32 + dial governor 24/3s），无统一预算 | 先记录 reset 来源计数，再决定是否合并 generation 租约 |

## 3. CI 与验证

本轮第一次在**代码 HEAD** 上拿到 CI：

```text
run 43550248961  "iOS resource guard"  HEAD 3f92a0f8b  conclusion: success
  race and unit   success
  burst matrix    success
```

此前 `a2582df67` 的 run `43549797420` 也 success（docs commit 恰好命中 `protocol/anytls/**` 的路径过滤）。本轮新增的 `common/resourceguard/**` 修改再次命中过滤，CI 覆盖了 `maxWaiters` 与 `BackgroundAllowed` 的改动。

本机回归（`raw/leftovers_regression.txt`）：

```text
go test ./common/resourceguard ./dns/... ./protocol/group   PASS
go test -tags with_quic ./common/httpclient                PASS
go build -tags DEFAULT_BUILD_TAGS_OTHERS ./...             exit 0
go test -race ./common/resourceguard ./dns ./protocol/group PASS
```

## 4. 结论

第三阶段清单里，凡是不依赖 iOS 真机、不改变产品语义的项，本轮已经收尾（后台让路 + 队列高水位）；其余 8 项按上表明确分类为 `OPEN_BY_DEPENDENCY` / `DECISION_REQUIRED` / `OBSERVATION_ONLY`，并给出触发条件——不再以笼统的 `DEFERRED` 挂着。
