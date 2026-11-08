# Cronet 原生连接创建的准入覆盖：边界与不可执行部分（2026-11-08）

状态：`BLOCKED_EXTERNAL`（原生侧不可精确计费/不可重建），保守边界与控制范围见下。
适用阶段：02（G04）。目标 revision：`11933a6`；依赖 `cronet-go` pin `362e63b14ce2`（29 个 `lib/*` 原生模块 `controlled`）。

## 1. 问题与目标症状

Naive/Cronet 出站有三类“创建”事实：引擎初始化（native net stack/线程/事件循环）、引擎驻留、
以及原生连接创建与复用。目标是把创建许可接在真实昂贵工作前，并分清“控制了引擎启动”与
“控制了原生连接创建”，不能把 `Start` 处的一个 gate 宣传成全 native 覆盖。

## 2. 现状（源码证据）

- `protocol/naive/outbound.go` 构造 `cronet.NaiveClient`；引擎与原生连接生命周期都在
  `cronet-go` 内。`FailureFacts` 只消费 `cronet.NetError` 做失败分类。
- `cronet-go` 的 29 个 `lib/*` 平台模块在本仓政策里是 `controlled`：归档由本仓库不存在的
  Chromium 工具链构建，改 SHA 必须重建并复验归档。本环境没有该工具链。
- 因此 repo 侧没有“原生连接即将创建”的回调；只有 `DialEarly`/`DialEarlyWithQUIC` 调用与
  每个出站的引擎对象。

## 3. 候选与较小替代

| 方案 | 结论 |
|---|---|
| A. 在 `outbound.Start` 取一个许可包住引擎构造 | **会误导**：引擎启动是出站生命周期，不是每次连接；且不能覆盖原生连接 |
| B. 每个 `DialEarly` 外层包 gate | 会限制的是调用并发而非原生连接数；Naive 流在共享引擎上复用连接，可能白白削减健康吞吐 |
| C. 在 `cronet-go` 加原生连接创建/关闭回调 | 需要重建 native 归档（Chromium 工具链不在本仓），本轮 `BLOCKED_EXTERNAL` |
| D. 只做观测（引擎/连接计数） | 依赖 `cronet-go` 暴露计数；当前无接口，且不能替代准入 |

## 4. 本轮的控制与不控制

- **控制**：Naive 出站的引擎对象仍由既有生命周期构造/关闭；错误分类沿用 `cronet.NetError`；
  创建许可继续覆盖 TLS/AnyTLS/Mieru 的真实 socket/握手。
- **不控制**：Cronet 引擎内部的 native 连接创建与复用没有接入 `Guard`；本轮不声称覆盖。
- **保守边界**：不新增 per-stream 的伪 gate（B），避免用错误的形状换“看起来接过了”。

## 5. 重开条件

- `cronet-go` 暴露“原生连接即将创建/已关闭”的回调或可查询的连接计数；且能在 release 的
  native 归档重建链里验证。
- 或有真机/互通证据表明 Naive 的 native 连接风暴是主要瓶颈，值得承担归档重建成本。

## 6. 索引与脱敏

- 索引：`MASTER_PLAN.md` G04、`CONTRACTS.md` §4。
- 本文不含凭据、节点或私有路径；只引用公开 pin、源码符号与仓库政策文件。
