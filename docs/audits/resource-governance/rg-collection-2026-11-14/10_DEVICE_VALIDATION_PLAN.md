# 10 真机 iOS 验收准备（Device validation plan）

> **本文件是计划，不是结果。** 没有 iPhone 与 NE 签名环境，本阶段**没有**采集任何真实 `phys_footprint`；不虚构、不把模拟器/CI 当 NE、不声称已证明 Jetsam。
> 采集对象：branch `work/resource-governance-closure-2026-11-14` 的 Collection HEAD（SHA 在 README 中；构建必须从该 SHA 起）。

## 0. 前置条件

| 项 | 要求 |
|---|---|
| 构建 | 从 Collection HEAD 构建 Apple Libbox + NE（`docs/TESTFLIGHT-MAC.md`），记录构建 SHA 与 tag |
| 设备 | 至少一台 iPhone（记录型号、iOS 版本、Go 版本、内核版本） |
| 工具 | 客户端诊断导出（OOM report ZIP）、`os_proc_available_memory` 可用性、Jetsam Event Report（若发生） |
| 隔离 | 所有压力端点使用本地/授权测试服务器；**禁止**连生产 VPS 或公网压力目标 |

## 1. 采集矩阵

| # | 动作 | 观察 | 记录 |
|---|---|---|---|
| 1 | NE 启动后空闲 2 分钟 | `phys_footprint`、Go Heap/Stack/Sys、Goroutine、connections | 诊断 ZIP + 时间戳 |
| 2 | System Proxy 模式，低并发（8 条 TCP 流） | 同上 + `resourceGuard` 元数据 | ZIP |
| 3 | TUN（Packet Tunnel）模式，低并发 | 同上 | ZIP |
| 4 | 单协议 burst：AnyTLS/UoT 200 flow | `admitted/queued/refused/timed_out`、goroutines、footprint 峰值 | ZIP + 关键时间线 |
| 5 | 单协议 burst：HY2 200 flow | 同上 + 物理 QUIC 连接数（服务端计数） | ZIP |
| 6 | 单协议 burst：TUIC 200 flow | 同上 | ZIP |
| 7 | 单协议 burst：Mieru 200 flow | 同上 + SDK underlay/session（服务端计数） | ZIP |
| 8 | 单协议 burst：XHTTP (H2) 与 H3 各 200 请求 | XMUX 连接数、H3 未准入事实、footprint | ZIP |
| 9 | 混合协议 200 flow（AnyTLS + HY2 + Mieru + XHTTP） | 同上 + 分类（interactive vs normal） | ZIP |
| 10 | STUN-like UDP 200 / 500 / 1000 flow（隔离测试端点） | NAT entries（若客户端诊断可见）、goroutines、footprint、gate 计数 | ZIP + `ps`/诊断 |
| 11 | 慢握手/黑洞：目标 accept 但不响应 | gate 等待者、`timed_out`、恢复时间 | ZIP |
| 12 | 压力触发：人为提高分配速率到 Critical | Pressure/Critical 时间点、shed 次数、恢复时间 | ZIP |
| 13 | Idle trim / 恢复：停止流量 5 分钟 | footprint 回落曲线、idle sessions | ZIP |
| 14 | Stop / Reload：NE stop、配置 reload | 资源是否归零、是否有残留 goroutine | ZIP |
| 15 | 若发生 Jetsam | Jetsam Event Report（原始文件） | 原始文件 + 时间 |

## 2. 必须区分并标注的指标

- `phys_footprint`：只来自 `TASK_VM_INFO`（真实 NE）；**不是** RSS、不是 Go Heap。
- `available_memory`：`os_proc_available_memory()`；不可用时写 `UNAVAILABLE`。
- Go Heap：`/memory/classes/heap/objects` 或 `MemStats`，并与 `HeapInuse`/`HeapReleased` 分开。
- `sessions`：区分逻辑 Session（协议）与物理连接（socket/QUIC）。
- `NAT entries`：若客户端诊断不导出，标 `UNAVAILABLE` 并记录在报告中（不要用估算冒充）。

## 3. 真机判定标准（采集前先定，避免事后解释）

| 场景 | 通过标准（建议） |
|---|---|
| 200 flow burst | footprint 峰值不超过观测预算（约 50 MiB）；恢复后 60 s 内回落到基线 +5 MiB 以内 |
| 1000 flow burst | 准入拒绝/超时可解释；无 Jetsam；恢复后 gate inflight/waiters 归零 |
| Critical | 触发时间 ≤ 2 个采样周期；shed 只关闭空闲 session；无活跃流被误关 |
| 恢复 | 压力解除后 ≤ 5 s 恢复准入；无重连风暴（连接数不高于 burst 峰值） |
| NE Stop/Reload | 无残留 goroutine；footprint 在停止后回落 |
| Jetsam | 如果发生：保留原始 Jetsam Report，作为失败证据而不是"已证明安全" |

## 4. 交付物

```text
device-<date>/
  build-sha.txt
  device.txt                 (model, iOS, Go, kernel)
  mode.txt                   (system proxy / TUN)
  reports/*.zip              (客户端诊断导出，脱敏)
  jetsam/*.ips               (若发生)
  timeline.md                (手动记录的关键时间点与观察)
```

**脱敏**：不放真实节点地址、UUID、密码、Token、用户域名、个人信息；节点用测试端点或本机回环。

## 5. 与 CI/本机证据的边界

| 证据 | 能证明 | 不能证明 |
|---|---|---|
| 本机 harness（`raw/metrics`） | 准入逻辑、队列/拒绝、Go 内存趋势 | iOS footprint、Jetsam、NAT 乘数 |
| CI `resource-guard`（run 43545040780） | Linux 上 race + burst 矩阵 | 设备行为、最终 HEAD（见 00 §6） |
| Apple 构建 | 编译/链接/ABI | 运行内存 |
| 真机 NE | 真实 footprint/Jetsam | 其他设备/系统版本 |

## 6. 当前状态

```text
NEEDS_DEVICE:
  - phys_footprint 时间序列（空闲/突发/Critical/恢复/Stop）
  - available memory 的真实可用性
  - 200/500/1000 STUN-like UDP flow 的真实乘数
  - AnyTLS/HY2/TUIC/MASQUE/Mieru/XHTTP 真机创建路径的 footprint 贡献
  - Jetsam 是否发生（目前没有证据）
  - 设备 Sleep/Wake 与内存压力叠加
```
