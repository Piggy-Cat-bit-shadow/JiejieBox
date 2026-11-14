# 07 定向修复轮（2026-11-14）

> 范围：上一轮列出的 7 项定向工作。**没有新增资源管理抽象**，没有改 Mieru、没有重写 XHTTP、没有动 UI，也没有提前处理 XMUX 与 sing-tun 的长期决策。
> 所有改动都是"在既有 seam 上补一个判断/一个 gate/一组测试"，不引入新的预算层或状态机。

## 1. H3 本地拒绝不再污染故障状态（`FIXED`）

**问题**：`http3FallbackTransport.recordH3AttemptFailure` 会把任何非取消/非 UNPROCESSED 的错误记为 authority 故障，进入 5 分钟起、48 小时封顶的 H3→H2 降级阶梯。准入拒绝（队列满、等待超时、rate window）与调用方 deadline 都属于"本进程没开始"，peer 从未参与，却会因此让健康 authority 被降级 5 分钟。

**改动**：在既有拒绝分类之后、`markH3Broken` 之前加 `resourceguard.IsLocalRejection(err)` 判断；本地拒绝直接返回，不推进 ladder。真实握手失败仍然推进（测试同时断言两点）。

**证据**：`common/httpclient/h3_broken_state_test.go`；覆盖 queue full / wait timeout / rate limited / caller deadline / caller cancel 五种，且真实失败仍标记。

## 2. DNS `Router.Lookup` 并发治理（`FIXED`）

**问题**：上一轮的 query gate 只加在 `Exchange`/`ExchangeAsync`。`Lookup` 走自己的 rule walk 与 client lookup，不经过 Exchange——拨号解析仍然可以无界并发。

**改动**：在 `lookupWithSource` 的 early-close 检查之后获取同一把 `queryGate`，`defer Release`；完整双栈 lookup 的两个 family 共用这一个 permit（一个调用者的工作）。没有新增 gate、没有新配置。

**证据**：`dns/router_query_gate_test.go::TestSaturatedDNSGateRefusesRouterLookup`——饱和时 client lookup 调用 0 次、返回 `ErrQueueFull`；释放后调用一次、inflight 归零。

## 3. `ExchangeAsync` 等待与 Permit 生命周期（`VERIFIED`）

**验证点**：

- 异步交换进行中，permit 必须**保持占用**（这就是并发上界），不能被"已提交"误释放；
- 回调成功/失败都必须恰好释放一次；
- 同一 client 重复回调（违约传输）不能二次释放；
- 槽被占用时第二个异步调用同步收到 `ErrQueueFull`，不进入 client。

**证据**：`TestAsyncExchangeHoldsOnePermitUntilCallback`（等待中 `Inflight=1`、第二个被拒且未到 client、成功后归零、二次回调 `Released` 不变）、`TestAsyncExchangeErrorReleasesThePermit`。释放实现仍是原有的 `sync.Once`，没有新增抽象。

## 4. AnyTLS 创建者 Deadline 与混合优先级（`VERIFIED`）

**Deadline**：`TestSharedCreationKeepsTheCreatorsDeadline`——创建者 300 ms 预算、DialOut 阻塞到 ctx 结束；等待者无 deadline。结果：创建者与等待者都在 deadline 失败、只发生 1 次 dial、DialOut 观察到 ≤300 ms 的 deadline。这直接守住"coalescing 分离取消但不能丢 deadline"的契约。

**混合优先级**：`TestMixedPriorityWaitersShareTheCreatorsAdmission`——创建者 `ClassInteractive`、等待者 `ClassDefault`，两者共享一次创建；admission hook 只调用一次，且看到的 class 是创建者的 interactive；gate `Admitted=1/Released=1`。即"等待者的优先级不会改写正在创建的工作的 admission 类别"。

## 5. H3 0-RTT：冷启动握手已验证，恢复路径未验证

**原结论**（已降级）：曾把"`DialEarlyConn` 在静默 peer 上阻塞到握手超时"当成 0-RTT 的完整生命周期。

**实际验证范围**：`TestH3PermitIsHeldAcrossTheColdStartHandshake` 用的是**冷启动**连接——没有 session ticket、没有 early data、没有 `Allow0RTT`。在这个路径上，`DialEarlyConn` 阻塞到握手成功/超时：500 ms 时 permit 仍持有（`Inflight=1`、`Released=0`），约 2 s 握手失败后释放一次（`Released=1`）。这是**冷启动握手已验证**。

**未验证**：真实 0-RTT 恢复路径（session ticket 命中、early data、`Allow0RTT`）。quic-go 在 resumed 连接上可能提前返回，permit 生命周期不同；本仓库没有对应的 TLS session cache 测试。状态：**NOT_VERIFIED**，测试名与代码注释已改为 cold-start，避免再次把两者混为一谈。
## 6. 真正的 Release 标签构建 + Race（`FIXED`）

- **Darwin arm64（Release profile）**：`release/DEFAULT_BUILD_TAGS` + `with_purego`，`GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./...` → **exit 0**。
  - `with_purego` 是必须的：Release 默认含 `with_naive_outbound` → cronet-go 的 `darwin_arm64` 只有 `cgo` 或 `purego` 两个实现；从 Windows 交叉编译没有 Apple 工具链，只能选 purego。这是"同一套 Release feature 标签、纯 Go 传输实现"的完整构建，不是另一套功能集。
  - 直接用 Release 标签、不加 `with_purego` 时，cronet 的 cgo 文件被构建约束排除，构建失败——这一事实也记录在 `raw/targeted/builds.txt`。
- **Windows 宿主**：`release/DEFAULT_BUILD_TAGS_OTHERS` 全量构建 → exit 0。
- **Race**：`-race` 跑 `common/httpclient`（with_quic）、`dns`、`protocol/anytls` → 全部 `ok`（4.0 s / 9.9 s / 8.4 s）。

## 7. 文档统计修正（`FIXED`）

- 闭环台账与报告的总数从 3 项 FIXED + 1 项 DECISION 校正为 5 项 FIXED + 1 项 DECISION（原来漏计了上一轮追加的后台让路与队列高水位）；
- 本文件补上本轮 7 项的准确状态；
- 测试耗时以本轮实测为准（AnyTLS 7.1 s、DNS 8.6 s、httpclient with_quic 2.7 s），旧数字保留在对应轮次的原始日志里，不混用。

## 边界

- 未改：Mieru、XHTTP 协议实现、UI、XMUX 默认值、sing-tun 依赖、NAT 首包账本。
- 未新增：预算类型、gate 实例、状态机、配置项。新增的只有：一个拒绝分类判断、一个 `Lookup` 的 permit、以及若干测试与注释。

## 8. CI

本轮推送后 `iOS resource guard` 在 HEAD `324a3b011` 上运行：run `43555135120`，`conclusion: success`，`race and unit` 与 `burst matrix` 均 success。CI 覆盖了 `protocol/anytls/**` 的测试改动（路径过滤命中），JSON 在 `raw/targeted/ci_run.txt`。