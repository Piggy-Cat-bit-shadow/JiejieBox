# 独立 endpoint 子 scope 的有界并行关闭（R08，2026-11-08）

结论：**不采纳**（`NOT_ADOPTED_WITH_EVIDENCE`）。保留 `adapter.Scope` 现有的逆序串行关闭与并发 Close 汇合
语义；并行化需要 Scope 之外的依赖信息，收益只有在大配置上才可能成立。

## 当前结构

- `adapter.Scope.Close` 取走 cleanup 队列，按注册逆序**串行**执行；后续 Close 通过 `closeDone` 汇合；
  错误按 closed/cancelled 过滤后聚合。这是已测试的完成边界（`adapter/lifecycle_close_boundary_test.go`）。
- `Box` 的 scope 关闭顺序是：status view → pause callback → governor → scope drain；endpoint 是 scope 的子
  scope，注册顺序在 outbound 之后，因此先于 outbound/network 关闭。
- endpoint 子 scope 的 cleanup 由各 endpoint 的 `Close` 组成；WG 的 Close 包含设备关闭、worker join 与
  listen_port 释放（见 `lazy-heavy-runtime` 报告）。

## 测量（主机，合成上界）

8 个互相独立的 scope，每个 cleanup 固定 `10ms`：

```
serial=84ms  parallel=10ms  workers=8  delay=10ms
```

这是并行化的**上界收益**：8 个同时关闭的慢 endpoint 才从约 84ms 降到约 10ms。测量脚本为临时测试，运行后删除；
命令与结果见 `EVIDENCE.md` E23。

## 为什么不实施

1. **Scope 不知道独立性**：cleanup 只是一个 `func() error`，没有依赖/分组元数据。要在 Scope 内部并行，需给
   `Scope.Start`/`Add` 增加“可并行组”概念，或为 endpoint 单独开并行 drain；两者都改变完成边界与错误聚合语义。
2. **真实依赖跨 endpoint**：listen_port 的唯一性、共享 tun 设备、runtimecoord 的关闭顺序、Box 的全局 owner
   次序，都不能从 endpoint 子 scope 自身推断。只在“依赖已证明独立的组”内并行需要新的显式依赖表。
3. **收益未在真实关闭中观测到**：当前没有真实配置的 Box.Close 被 endpoint 子 scope 支配的测量；合成上界只在
   多个慢 endpoint 同时关闭时成立。加入并行会违反已记录的逆序依赖不变量，风险大于收益。

## 保留与复开条件

- 保留：`Scope.Close` 的串行逆序、并发汇合、错误过滤与 04 批 1 的一次性有界关闭诊断（watchdog 仍能指出
  卡住的 owner）。
- 复开条件：在真实大配置（≥8 个慢关闭 endpoint）上测得 `Box.Close` 的显著部分花在 endpoint 子 scope，且能
  为可并行组提供显式、可测试的依赖声明；届时只在该组内有界并行并聚合错误，保留全局 owner 次序。
