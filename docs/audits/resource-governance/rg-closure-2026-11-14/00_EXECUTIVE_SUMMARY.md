# 第一阶段｜跨层资源预算与生命周期：收尾报告（rg-closure-2026-11-14）

> 后续：第二阶段的独立对抗 Debug 见 [`09_DEBUG_REPORT.md`](09_DEBUG_REPORT.md) 与
> [`10_BUG_LEDGER.md`](10_BUG_LEDGER.md)。Debug 在同一分支上修复了 3 个真实缺陷并补充了
> 变异/性能证据；本文保留第一阶段收尾时的结论，未按 Debug 结果改写。

> 分支：`work/resource-governance-closure-2026-11-14`
> 基线：`testing` @ `fa61500731b1237e9ef44029b7469ee5373af4ba`
> 本文所有结论都带证据级别。没有真机的地方标 `NEEDS_DEVICE`；没有运行的项标 `UNTESTED`，不写成 PASS。

## 1. 本轮做了什么

在最新基线（v0.1.47 changelog 之后，27 个提交、生产代码与文档混合）上做了三个有证据支持的系统级修复，每个都是"先写会失败的测试，再修复"：

| # | 提交 | 层 | 修复的高置信度缺口 |
|---|---|---|---|
| 1 | `719af51e7` | `route/conn.go` | 非 connected UDP 的 `ListenPacket` 路径绕过了 dial setup gate 与 traffic class 传播：网络切换无法取消在途 setup，交互流在准入 seam 里被当成后台流量，重连雪崩没有 dial gate |
| 2 | `8f00f91f4` | `service/oomkiller/timer.go` | `phys_footprint` 读取失败时 `memory.Total()` 返回 0，被当成真实测量 → 发布 `MemoryPressureNone` → 准入回到最宽松档。现在 0 被分类为"不可测量"，状态保持在 ARMED 线，并写入报告 |
| 3 | `858868d96` | `transport/v2rayquic/client.go` | `transport.type=quic` 的 VLESS/VMess/Trojan 共享 QUIC 连接创建从未进入 resourceguard 准入，是共享 QUIC 家族里唯一完全未计费的成员 |

三项均为 1 个生产文件 + 1 个新测试文件，未改依赖仓库、未改 go.mod、未改配置格式、未触碰 XHTTP/Mieru/QUIC 协议内部实现。

## 2. 为什么是这三项

本轮按"证据强度 × 系统级影响 × 改动面"排序，而不是按协议清单逐个加 gate。

1. **压力事实必须诚实**（修复 2）。OOMKiller 是唯一压力权威，ResourceGuard 只消费它发布的等级。若压力事实在测量失败时变成"安全"，后面所有准入与回收都建立在错误前提上。这直接命中施工指令 §5.6 "采样失败不能错误返回 0 并表示安全" 与 §5.1 单一职责原则。
2. **生命周期断点必须在真实流量路径上**（修复 1）。普通 UDP 流（含诊断里的 STUN 突发）走的是非 connected 分支；该分支此前不受 setup gate 约束，而 `dial_setup.go` 的契约明确写着 `begin` 由 `NewConnection / NewPacketConnection` 调用。这是契约漏洞，不是优化缺失。
3. **昂贵的物理创建必须被计费**（修复 3）。该项与既有 sing-quic `CreationAdmission` 设计同形，属于把家族内已有机制补齐到"最后一个未计费成员"，不引入新抽象。

## 3. 证据状态

| 问题 | 状态 | 证据 |
|---|---|---|
| 非 connected UDP 无 setup gate / 无 traffic class | `REPRODUCED` → `FIXED` | 基线 RED（`raw/10_red_on_base.txt`）、修复后 GREEN（`raw/20_green_branch.txt`）、`-race` 通过 |
| 采样失败被当作 0 / 安全 | `REPRODUCED` → `FIXED` | 基线 RED（`raw/10_red_on_base.txt`，测试在新 API 上编译失败）、修复后 GREEN、`-race` 通过 |
| v2rayquic 共享 QUIC 创建未准入 | `REPRODUCED` → `FIXED` | 基线 RED（`raw/10_red_on_base.txt`）、修复后 GREEN、`-race` 通过 |
| 基线相关包回归 | `VERIFIED_UNIT` | `raw/00_base_tests.txt`（基线）与 `raw/20_green_branch.txt`（修复后）逐包对比 |
| 全量 `route` 包 | `VERIFIED_UNIT` | `raw/32_route_full_summary.txt`（第三次运行通过；第一次运行曾以 `exit status 0xffffffff` 终止，见 §5） |
| 全仓构建（Windows/others tags） | `VERIFIED_ARTIFACT`（本机构建） | `raw/30_build_and_vet.txt`：`go build -tags <OTHERS> ./...` exit 0 |
| `go vet` 受影响包 | `VERIFIED_UNIT` | `raw/30_build_and_vet.txt` exit 0 |
| iOS 真机 footprint / Jetsam | `NEEDS_DEVICE` | 本机为 Windows，无 NE 运行环境 |
| Apple 交叉构建 | `BLOCKED_BY_ENVIRONMENT` | 无 Apple SDK / cgo 工具链 |
| 吞吐 / 延迟回归 | `UNTESTED` | 理由与结构性论证见 `07_PERFORMANCE_COMPARISON.md`，不冒充测量 |

## 4. 没有做什么（明确边界）

- **没有**强行给每个协议加 `Acquire`。已确认 AnyTLS、Hysteria/Hysteria2/TUIC、TrustTunnel、Naive/Cronet、Mieru、共享 TLS 都有各自正确的创建接点（见 `02_RESOURCE_OWNERSHIP_MAP.md`）。
- **没有**重开 XHTTP/XMUX 性能或兼容性改造。本轮只记录 XHTTP H3 与 XMUX 连接数上界的未覆盖事实（`08_OPEN_QUESTIONS.md`），未改代码。
- **没有**改 Mieru 或任何依赖仓库。三个修复都在主仓适配层可正确解决。
- **没有**建立任何新的 ResourceManager、第二个压力采样器或第二个 GC loop。
- **没有**为提高"看起来的收益"而降低 baseline 测试量；基线包测试全部实际运行。

## 5. 需要如实说明的异常

本轮第一次执行全量 `go test ./route` 时进程以 `exit status 0xffffffff` 结束（Windows 上的异常终止，无 Go test 失败行）。随后两次完整运行均通过（447 s / 见 raw）。该现象**未归因**：

- 不是本轮新增测试失败（三次运行中新增测试都通过）；
- 可能与同时运行的并行构建/测试造成的资源竞争有关，但**没有证据**；
- 按仓库规则，这属于 `UNTESTED` 的稳定性项，不能因为重跑通过就宣称不存在。

若下一轮要使用 `route` 全量作为门禁，建议在干净的 CI 环境重跑并观察是否复现。

## 6. 下一轮最值得攻击的三个点

1. **同键 session 创建 coalescing**：AnyTLS 在突发下仍会创建 N 个 session（200 并发 flow → 最多 200 次真实握手，只是被 gate 限速）。收益最大，但需要改 pinned SDK 或新增 SDK 内机制，本轮 `DEFER`（证据见 `05_DESIGN_DECISIONS.md` §4）。
2. **XHTTP H3 / MASQUE H3 的物理创建未准入**：两者都直接 `quic.DialEarlyConn`，绕过了共享准入。改动面涉及 H3 传输语义，本轮 `DEFER`。
3. **采样不可用期间的准入姿态**：本轮选择 ARMED（收紧但不破坏）。真机上 `task_info` 长期失败时的实际行为、以及 GOMEMLIMIT 与 footprint 的关系仍需设备证据（`NEEDS_DEVICE`）。
