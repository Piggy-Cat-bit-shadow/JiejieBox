# JiejieBox 更新日志

本文件记录 JiejieBox fork 的主要变化，按发布版本倒序排列；日期为北京时间。它是面向使用者的摘要，不等同于完整提交记录。上游 sing-box 的更新见 [上游更新日志](docs/changelog.md)，完整发布文件和历史版本见 [GitHub Releases](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases)。

## 未发布

暂无已整理的用户可见变更。

## [v0.1.34](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.34) — 2026-10-29

- 更新配套客户端的订阅 User-Agent 为 `sing-box/1.15.0`；订阅剩余流量与到期时间的展示依赖服务端返回 `Subscription-Userinfo` 响应头。
- 更新 Apple 客户端源码引用，并完善 Libbox 构建、ABI 检查及 Apple 发布流程的源码固定和验证步骤。
- 整理内核及关联仓库知识库，补充版本基线和验证范围。

## [v0.1.33](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.33) — 2026-10-28

- 作为与 satelite-one v0.1.33 配套的内核版本发布，更新版本标识。

## [v0.1.32](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.32) — 2026-10-28

- 调整路由：通过 TUN 栈处理 endpoint 本地产生的流量，并共享流量日志上下文。
- 更新 Apple 客户端引用及依赖，包含客户端 TUN MTU 限制相关变更。

## [v0.1.31](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.31) — 2026-10-28

- 修复启动后网络接口更新时的 TUN 重置，以及重置交接中的唤醒丢失问题。
- 修复审计中发现的 TUN、TLS、QUIC、代理组和生命周期问题；完善 FakeIP 嗅探、IPv4 映射地址及 kTLS 数据路径处理。
- 更新 TUIC / HTTP3 数据包大小限制的 schema，并补充相关测试与内核知识库记录。

## [v0.1.30](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.30) — 2026-10-28

- 已知 FakeIP 域名时，不再为分片 QUIC 嗅探继续等待。
- 调整 AnyTLS 在双栈主机上的代理服务器地址族选择与日志。
- 改进双栈拨号、网络恢复、资源关闭和零拷贝关闭路径。

## [v0.1.28](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.28) — 2026-10-27

- 改进网络接口切换时的连接交接：及时关闭旧数据报流，并调整网络变化事件的处理顺序。
- 更新 Apple 网络路径监控引用。

## [v0.1.27](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.27) — 2026-10-26

- 修复 DNS 查询中某一地址族结果为空时被误判为查询失败的问题。

## [v0.1.26](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.26) — 2026-10-25

- 更新配套 Dashboard 和 fork 依赖引用，并整理关联仓库信息。

更早版本请参阅 [标签和发布历史](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases)。版本间的详细差异以 Git 标签、源码和对应发布记录为准；验证结果请查看对应版本的证据，不能由此摘要推断所有平台均已验证。
