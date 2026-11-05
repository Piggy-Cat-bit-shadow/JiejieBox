# Cronet patch5、Linux DNS 与 v0.1.39 消费记录

本记录于 2026-11-05 将依赖侧修复与父仓正式消费状态集中索引。当前父仓目标为 `v0.1.39` / `fc7e400243d46a451b2dffbe68cb5a3147c66289`。证据分为固定源码、API 独立核对与此前测试结果；本次文档维护没有重跑原生构建或压力测试。

## 已消费的版本

| 对象 | 固定值 |
| --- | --- |
| 主仓 root replace | `v0.0.1-143.0.7499.109-2.0.20261105062326-362e63b14ce2` |
| 29 个 lib 模块 | `v0.0.0-20261105062326-362e63b14ce2` |
| all | `v0.0.0-20261105062941-7ce9c49b464a` |
| 实际 Chromium | `154.0.8037.49` |
| 原生源码 fork | `Piggy-Cat-bit-shadow/naiveproxy@0ca66a00765564593eb4d2e6d061fe950ac7cce2` |

root 与 libs 在发布提交 `362e63b14ce2`，all 在后续聚合模块提交 `7ce9c49b464a`。这是实际发布结构，不要求末尾 SHA 相同。Go pseudo-version 中的历史 `143` 前缀不表示引擎退回 Chromium 143。

父仓 `go.mod`、`test/go.mod`、`release/source-lock.json` 与 `release/cronet-provenance.txt` 已接入对应版本；不再沿用旧 `d841c37`。原生五目标重建不等于 29 个 lib 目录全部重建；darwin_amd64 与两个 Apple 模拟器分片沿用已有字节。

## 原生补丁与适用性

证书 blocklist 新增 8 个 SPKI；QUIC 最大接收窗口为 stream 64 MiB、session 128 MiB，包含 EnsureWindowAtLeast 与自动调节修复，保留 fork 的配置窗口边界。上游 CONNECT 强制隧道变更经调用路径评估未机械套用；不将 Naive 独立程序的全部后续提交等同于 Cronet 必需改动。

这轮仍为 Chromium 154，属于 patch5 相关补丁同步与集成修复，不是 154 → 157 迁移。

## Linux DNS 根因与最小修复

固定消费源码 `cronet-go@362e63b14ce2:naive_dns.go` 已独立读取：`remoteAddress == nil` 时调用 `writePacketConnWithoutAddress`，其他情况保留 `bufio.NewBindPacketConn(...).Write`；写失败计入 `dnsReplyFailures`。

匿名 Unix 数据报 socketpair 没有可用的 reply address。旧包装最终 `WriteTo(nil)` 失败，而已连接端点的 `Write` 可以回复；不能因 UDPConn 也实现 net.Conn 就把所有 PacketConn 一律切到 Write，否则会破坏普通 UDP listener。

先前集成日志记录自定义拨号首次写入约 274 秒超时；后续 PipeProxy 的独立堆栈显示拨向公网代理端口。修复后集成拨号目标是 `127.0.0.1:10443`，用例约 0.79 秒通过。**完整 DNS NetLog 未采集**：已形成缺陷复现与修复恢复证据，不把系统解析 fallback 的每一步都写成已抓取的运行事件。

## 独立核对的 CI 运行

以下 status/conclusion/head_sha 于本次通过 GitHub API 查询；详细用例与耗时为此前执行报告所记，未在本次重复运行。

| 目的 | run / SHA | 实际结论 |
| --- | --- | --- |
| 旧路径复现 | [41976091212](https://github.com/Piggy-Cat-bit-shadow/cronet-go/actions/runs/41976091212)，`37b5e1cc632e92b3b7463c91bde69e634347904a` | failure，复现运行应为红灯 |
| Linux purego | [41977265010](https://github.com/Piggy-Cat-bit-shadow/cronet-go/actions/runs/41977265010)，`63ef17afb2dcc982abd244c27b450647f8ebaa86` | success |
| Linux cgo | [41979882299](https://github.com/Piggy-Cat-bit-shadow/cronet-go/actions/runs/41979882299)，`aecf8fd3a1106d6cbb6932c03af8491f020139bb` | success |
| 五平台构建、发布与消费 | [41979233815](https://github.com/Piggy-Cat-bit-shadow/cronet-go/actions/runs/41979233815)，`4c96192d73f67c48a91cba3bce358a3c91692444` | success；逐作业范围需读取该运行 |

Windows 34 项与五平台细项来自先前验收记录，不能由本表一个 success 推导所有测试逐项通过。

## 仓库和历史报告的替代关系

2026-11-05 独立观察：cronet-go 只保留 `main@4c96192d...` 和 `go@7ce9c49...` 两分支。原构建镜像、原 Actions 故障、旧发布头均是历史上下文，不是新的消费入口。父仓是否使用新版本应查自己的 module graph，不能只看依赖仓最新 HEAD。

`docs/fork/dependency-forks-sync-status.md` 所述 `d841c37`、四平台重建及 publisher 的 unknown revision 是当时状态，已由后续五目标发布与新 pin 取代。保留原报告以解释问题演进，阅读当前版本先读本文。

遗留 DoH 地址的归属与产品是否要抑制该流量属于独立议题；仅观察到某个 IP:443 连接不足以宣称不存在 DNS 外发问题。不得把本修复扩大成“Chromium DNS 的所有路径已被完全约束”。
