# TCP 监听默认值与 MPTCP（TCP listener defaults and MPTCP）

## 0.1.45 新入站与监听面（固定源码 3032ca5）

TrustTunnel 增加 H2/TCP 与 H3/QUIC 两种入站监听，认证和 UDP association 在协议 owner 内。原有 Linux 普通 TCP/MPTCP 默认安全规则不能因新入站存在而假定自动覆盖其所有 socket；判断需追踪 listener 创建路径与对应测试。服务端容量和认证前资源上限仍需按各协议实际入口复核。


## 当前设计与来源（Current design and provenance）

目标 `v0.1.39`，源码加固自 v0.1.37 已交付。`common/listener/listener_tcp.go` 在 `TCPMultiPath` 为 false 时调用 `ListenConfig.SetMultipathTCP(false)`，为 true 时显式开启；`common/netx.ListenTCP` 为 API/debug 等非产品入站提供普通 TCP 监听方法。

`debug_http.go`、Clash/V2Ray API、libbox pprof 与 boxdd 的对应监听点使用 netx。应按实际调用点审阅，不把这份清单理解为整个进程、第三方库或原生 Chromium 的所有 socket 都被统一关闭 MPTCP。

`VERIFIED_SOURCE` / `VERIFIED_GIT`：当前源码与 `d3f384953` 等监听加固提交。安全背景来自 Xray v26.10.31 与 Go 官方安全讨论；应用层防护实现属于 fork 的监听策略改造，Linux 内核补丁不在该实现中。

## 不变量与约定（Invariants and conventions）

- 未主动开启时不受 Go listener 默认值变化影响；用户显式 opt-in 仍保持语义。
- 管理与调试入口不能因没有普通 inbound 配置而漏掉默认策略。
- 测试必须查询 Linux socket 的实际 MPTCP 属性；内核不支持时的 SKIP 不是成功开启的证据。
- 关闭 MPTCP 缓解公开利用链的攻击面，不修复 CVE-2026-98432 的底层 IPv4 TCP UAF，不能声明对所有 TCP 利用免疫。

## 重查（Verification）

```sh
go test ./common/listener ./common/netx -run 'MPTCP|Multipath' -count=1 -v
rg -n 'netx.ListenTCP|SetMultipathTCP|net.Listen\(' common experimental debug_http.go
```

`listener_tcp_mptcp_linux_test.go` 覆盖默认与显式开启；`listen_linux_test.go` 覆盖 netx。需要 Linux 运行环境，Windows 上的静态阅读不是 socket 验证。

一手公告、用户可理解的范围与升级建议见 [安全说明](../../docs/SECURITY-MPTCP.md)。不要给未经核对的发行版版本号或内核补丁状态打包票。
