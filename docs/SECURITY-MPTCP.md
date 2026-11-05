# 默认关闭意外的 MPTCP 监听：安全加固范围

JiejieBox 从 v0.1.37 起，在普通 TCP 入站和相关管理／调试监听入口明确选择普通 TCP，避免 Go 1.24 及之后的默认行为在 Linux 上意外创建 MPTCP 监听器。v0.1.39 继续保留这一加固。

## 做了哪些工作

- `common/listener/listener_tcp.go`：未设置 `tcp_multi_path` 或其为 `false` 时，调用 `SetMultipathTCP(false)`；明确设置 `true` 时保留主动启用能力。
- `common/netx/listen.go`：为不经过普通入站 listener 的入口提供统一的普通 TCP 监听方法。
- Clash API、V2Ray API、Debug HTTP、libbox pprof 和 Windows boxdd 对应调用点接入该策略，减少“主入口关了，管理入口仍继承默认值”的遗漏。
- Linux 回归测试查询实际 socket 的 MPTCP 属性，分别检查默认关闭和显式开启；内核不支持时应识别测试的 SKIP，不能宣称测试覆盖了 MPTCP 开启路径。

普通用户无需新增配置即可获得这个默认值；有意配置 `tcp_multi_path: true` 的监听器仍会启用 MPTCP，需自行确认系统已修补并评估使用场景。

## 与公开 TCP 漏洞的关系

2026 年 10 月公开的 **CVE-2026-98432** 根因位于 Linux IPv4 TCP 处理的 use-after-free，公开利用链涉及 MPTCP listener。Go 项目说明，自 Go 1.24 起 listener 默认启用 MPTCP；显式关闭可缓解该已公开利用链的暴露，但漏洞本身并非 MPTCP 独有。

因此本项目的准确卖点是：**主动控制监听 socket 的默认行为，减少意外 MPTCP 暴露与已知攻击面的触达机会。** 它是应用层缓解措施，系统管理员仍应更新到发行版已修复的内核；不能把它写成“修复 Linux 内核漏洞”或“关闭后所有 TCP 攻击都失效”。

一次资料核对不应把动态的发行版受影响清单固化成永久说明。当前内核是否修复，以发行版安全公告和实际补丁为准。

## 来源、实现与复查

- [Go 官方讨论与维护者安全说明](https://github.com/golang/go/issues/82717)：根因、Go 默认值与缓解边界。
- [Xray v26.10.31 安全发布说明](https://github.com/XTLS/Xray-core/releases/tag/v26.10.31)：同类默认监听加固的背景。
- [普通 TCP 入站实现](../common/listener/listener_tcp.go)、[API／调试监听实现](../common/netx/listen.go)。
- [普通 listener 的 Linux socket 测试](../common/listener/listener_tcp_mptcp_linux_test.go)、[netx 的 Linux socket 测试](../common/netx/listen_linux_test.go)。

Linux 上复查：

```sh
go test ./common/listener ./common/netx -run 'MPTCP|Multipath' -count=1 -v
```

本说明于 2026-11-05 对照上述源码和公开一手资料整理，没有在本次文档维护中执行漏洞利用测试。
