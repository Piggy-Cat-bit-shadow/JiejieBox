# 第三阶段证据包：跨层资源治理（rg-collection-2026-11-14）

> 本阶段只做**只读审计、本机测量与证据整理**：没有修改任何生产代码，没有连接生产 VPS / 代理节点 / 公网压力目标。
> 最终判定：**`COLLECTION_STATUS = PARTIAL`**（本机可得的证据已收齐；iOS 真机与部分跨层指标明确列为 `NEEDS_DEVICE` / `UNAVAILABLE`，未虚构）。

## 最终状态

```text
COLLECTION_STATUS        = PARTIAL
BRANCH                   = work/resource-governance-closure-2026-11-14
HEAD                     = 本证据包提交（SHA 见 raw/90_collection_push.txt；以此为准）
BASE_SHA                 = fa61500731b1237e9ef44029b7469ee5373af4ba
INITIAL_IMPLEMENTATION   = ce95f50ea1cc98b57560cec79cf3e361bd0dbc0b
DEBUG_HEAD               = f1f0ac4e9d94d21e04b38b8e10cdea6aa8b2074f
PUSHED                   = YES（本包提交后推送并核对）
REMOTE_HEAD_MATCH        = YES（核对命令见 00 §1 与 raw/90_collection_push.txt）
PRODUCTION_CODE_CHANGED  = NO
NEXT_ROUND_TOP_ISSUES    = 上游 per-flow 预算(F1/Q1) > 同键创建 coalescing(F8/Q2) > 后台让路(F3/Q3) > H3 准入(Q4)
NEEDS_DEVICE             = iOS phys_footprint 时间序列 / Jetsam / 200-1000 STUN UDP 乘数 / 设备 sleep
```

## 交付物索引

| 文件 | 内容 |
|---|---|
| `00_REPOSITORY_SNAPSHOT.md` | 仓库/分支/SHA/提交/文件分类/依赖 pin/CI 状态（含"最终 HEAD 无 CI 运行"的事实） |
| `01_RESOURCE_OWNERSHIP_MAP.md` | 从 PacketTunnel 到 footprint 的真实调用图、每层资源与所有权、观测缺口 |
| `02_PROTOCOL_POOL_MATRIX.csv` | 17 个协议/路径的连接池、物理创建点、admission、上限、Close/Retire |
| `03_MEMORY_TIMELINE.csv` | 11 个阶段的两次 run 对照（延迟分位、waiters、goroutines、working set） |
| `04_PRESSURE_STATE_MACHINE.md` | 信号链、压力真值表、恢复/抖动/采样失败、后台工作行为 |
| `05_LIFECYCLE_CROSS_MATRIX.csv` | 11 个生命周期交叉组合的所有者/关闭权/竞态/测试状态 |
| `06_PERFORMANCE_COMPARISON.csv` | 改前/改后基准、绝对测量、可比性说明 |
| `07_TEST_EVIDENCE.md` | 本轮实际执行的测试、竞态、SDK 契约、未运行项 |
| `08_SURPRISE_FINDINGS.md` | 12 条意外发现（证据→机制→影响→方案→裁决） |
| `09_NEXT_ROUND_QUESTIONS.md` | 下一轮 5 个问题与最小实验、通过标准 |
| `10_DEVICE_VALIDATION_PLAN.md` | 真机最小采集计划（不含虚构结果） |
| `raw/` | metrics（两次 run 的原始 CSV）、benchmark、test-logs、harness 源码文本 |

## 十个核心问题的回答（每条的完整证据见对应文件）

1. **资源在哪里创建？** TUN NAT/flow dispatch（依赖仓库）→ route 连接管理/准入 → 协议创建 seam → 物理传输；见 `01` 的调用图与 `02` 的矩阵。
2. **谁拥有？** 每层 owner 表见 `01` §2；跨 Go/native 的三处边界（PacketFlow、Cronet、Apple TLS）见 `01` §1。
3. **哪些被统计/估算/不可见？** `01` §2 的 MEASURED/COUNTED/ESTIMATED/UNOBSERVED 列；缺口集中在 `sing-tun` 计数、DNS cache、buffer pool、Swift/native。
4. **物理连接池与逻辑流关系？** 6 个协议 N:1、shared TLS 与 plain SS/Snell/Trojan 1:1；见 `02`。
5. **是否在分配前准入？** 已接入的 seam 都在真实创建前；XHTTP H3 / MASQUE H3 是明确例外；见 `02`、`09` Q4。
6. **压力是否改变创建行为？** 是；truth table 与实测 Critical 40/200 admitted、plan 8/32/100 ms 见 `04`。
7. **活跃/空闲/等待者/缓存/Buffer/Goroutine 能否分别治理？** 部分：active/idle 由各池负责，等待者由 gate 计数，buffer/Goroutine 无统一账本（`01` §4）。
8. **压力+切换+Reload+Sleep+Stop 同时发生是否仍能释放？** 见 `05`；多数组合有单元证据，组合场景多为 PARTIAL，设备项为 NEEDS_DEVICE。
9. **iOS footprint 与 Go Runtime 差距？** 本机只能测 Windows working set（~8.9 MB 空闲到 ~36 MB 峰值），**不能**代表 iOS；真实差距 `NEEDS_DEVICE`。
10. **不确定性/最值得投入？** 见 `08`/`09`：上游 per-flow 乘数、同键创建 coalescing、后台让路、H3 准入、观测缺口。

## 已证实 / 已排除 / 未验证

- **已证实（本阶段）**：准入账本守恒（requested 分区 + inflight 归零）在 2424 请求后成立；Critical 的 plan 收紧与恢复真实生效；1000 并发下队列上界表现为 694–712 次即时拒绝；H1/H2 已准入、H3 未准入；后台测速不读内存压力；链式代理一份 permit 可对应多条物理连接。
- **已排除**：AnyTLS/HY2/TUIC/v2rayquic 的"复用被错误计费"（SDK 契约测试 + 本仓测试）；"压力状态不改变 gate plan"；"host 构建会套用 50 MiB NE 预算"。
- **未验证**：iOS `phys_footprint`/Jetsam、NAT/协议物理连接时间序列、端到端吞吐与延迟、Cronet/native 内存、500/2000 flow 与长稳。

## 性能对照（摘要，完整见 `06`）

| 项 | base | HEAD | 说明 |
|---|---|---|---|
| route 非 connected UDP setup | 863 ns / 1144 B / 10 allocs | 1869 ns / 1440 B / 16 allocs | 每 flow setup 一次；+1 µs 量级 |
| v2rayquic 共享创建（NoGuard） | 1494 ns / 320 B / 5 allocs | 1930 ns / 512 B / 10 allocs | 每共享创建一次 |
| v2rayquic 共享创建（WithGuard） | 1497 ns（hook 不存在） | 2808 ns / 672 B / 14 allocs | admission 增量 ≈ +0.9 µs / +4 allocs |
| gate Acquire/Release | 244.4 ns / 48 B | 251.1 ns / 80 B | Permit 增加 granted 时间戳 |
| 1000 并发准入（本机） | 未测量 | 288–306 admitted / 694–712 refused | 绝对测量，非 A/B |

## 复现

见 `raw/metrics/README.md`（harness 源码文本、运行步骤、指标口径）与 `07_TEST_EVIDENCE.md`（测试命令）。
