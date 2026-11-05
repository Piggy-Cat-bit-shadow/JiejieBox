# iOS / iPadOS：自签安装与本地签名回环

JiejieBox v0.1.39 提供用于自行签名的 IPA，并在配套 iPhone / iPad 客户端加入本地签名回环开关。它们分别解决“安装及签名兼容”和“签名工具需要本机网络回环”两类问题。

[返回首页](../README.md) · [v0.1.39 下载](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.39) · [自签排障全过程](../KNOWLEDGE/reports/ios-selfsign-compat-2026-11-04.md)

## 下载与签名

在 Release 附件下载 `jiejie-ios-selfsign-unsigned-0.1.39.ipa`。它是**未签名的完整客户端包**，需由自己的签名工具、证书和描述文件签名后安装；不是点击即安装的 App Store 包，也不包含能自动授予 VPN 权限的证书。

签名时应保留并正确签署主应用、Packet Tunnel 扩展及嵌入框架，确保描述文件允许 Packet Tunnel / Network Extension 所需权限。App Group 的实际授权应能让主应用与隧道扩展访问同一共享容器。某些重签工具会改 Bundle ID、App Group 和团队身份，因此不能只改主应用的一个标识。

v0.1.39 的配套修复包括：

- **启动容错**：共享容器不可用时有明确退化处理，避免启动路径强制解包导致闪退。
- **实际签名授权优先**：从可访问的签名描述文件中发现候选 App Group，并以系统的容器访问结果确认可用性；不再只信构建时写入的组名。
- **发现真实隧道扩展**：按包内 Packet Tunnel 扩展识别 provider，适应重签后的标识。
- **命令通道路径预算**：Darwin Unix socket 路径最多 103 字节；根据候选目录余量生成短文件名，始终考虑系统临时目录，服务端与客户端使用同一计算方法。
- **更可解释的失败**：区分状态通道不可用与服务未启动，并对“VPN 显示连接、TUN 却未完成接管”给出诊断。
- **一致的产物身份**：主应用和扩展版本、核心与 Apple 客户端提交、Libbox 来源和文件哈希记录在构建清单中。

这些修复提高重签兼容性，但不能绕过系统签名、账号有效期或描述文件权限限制。仓库记录过用户真机反馈与 Darwin bind 验证；它们不等于所有签名工具、账号和设备均已验收。

## 打开本地签名回环

1. 打开配套的 iOS / iPadOS JiejieBox 客户端。
2. 进入 **更多 → 连接行为 → 配置覆盖 → 本地签名回环**（英文为 **More → Connection Behavior → Profile Override → Local Signing Loopback**；iPad 的设置布局可能不同，在“配置覆盖”中查找同名开关）。
3. 打开开关，让客户端完成服务重载；未启动服务时，在下一次启动生效。
4. 客户端将 `10.7.0.1` 加入本次运行的 TUN 本地回环地址，并为同一地址配置 iOS 主机路由。按签名工具的要求使用该本地连接。

开关**默认关闭**；地址通过运行时覆盖注入，保留用户已有的 `loopback_address`，不会改写订阅或原始配置文件。关闭后重载，下一次有效配置不再注入该地址；用户自己写入配置的回环地址仍保留。

### 它适合什么场景

该入口用于本机回环连接。流量在匹配地址时由 TUN 数据面反射回设备，TCP、UDP、ICMPv4/v6 在默认 `go` 栈具有对应实现。

### 当前兼容性与续签方式

截至 2026-11-05，根据当前用户真机反馈，**WLOC 的开发者模式虚拟定位可正常使用，使用本项目回环进行 SideStore 续签仍有问题**。这些是对应设备与场景的反馈，不代表所有同名 WLOC 实现或系统版本均已验证。

SideStore 续签请暂时切换至 **LocalDevVPN**：连接其本地隧道后，打开 SideStore，在“My Apps”中点击应用右侧的剩余天数进行刷新。名称和标准操作已按 [SideStore 安装与刷新指南](https://docs.sidestore.io/docs/installation/install)核对；该指南不构成 JiejieBox 回环兼容性已通过的证据。

**完整反射实现当前仅覆盖默认 `go` 栈。** 不建议为了开启功能额外设置已弃用的 `stack` 字段；若旧配置显式选择 `system`、`mixed` 或 `gvisor`，应先核对适用性。核心会对 iOS 上相关不支持组合发出警告，开关亮起本身不构成数据面已经工作的证据。

### 开启后如何确认

- 服务应能完成启动／重载，不能仅观察开关状态。
- 如工具仍提示本地连接失败，先检查有效配置的 TUN、回环地址和所选栈，再检查签名权限与系统路由。
- 用连接记录或出口 IP 验证代理是否接管；“网站可以打开”本身不能证明经由代理。
- SideStore 续签问题仍待修复；iPad UI 和 NE 路由安装应按对应设备分别验收，不能把源码测试写成真机通用承诺。

## 自签与 TestFlight 的关系

自签 IPA 是正式 Release 的独立资产；TestFlight 仍通过 [Apple 发布脚本](APPLE-BETA-RELEASE.md) 使用自己的签名与上传流程。macOS GUI 构建、DMG 与 TestFlight 路径也独立于 iOS 自签包。

v0.1.39 的 Release 附件包含 iOS 自签 IPA 和 Apple Libbox；**该版没有把 macOS DMG 作为正式附件发布**。macOS CLI 可直接从 Release 获取，GUI 构建及 TestFlight 入口见对应文档。

## 开发与排障资料

- [回环实现记录](LOCAL_SIGNING_LOOPBACK_IMPLEMENTATION_REPORT.md)：开关、覆盖、TUN、libbox 与 NE 路由的完整链路。
- [回环专项审计](LOCAL_SIGNING_LOOPBACK_DEBUG_REPORT.md)：地址规范化、平台隔离、栈限制与设备待验项。
- [自签兼容全过程](../KNOWLEDGE/reports/ios-selfsign-compat-2026-11-04.md)：重签身份、共享容器、五轮现象与后续修复。
- [命令 socket 路径修复](fork/ios-command-socket-path-fix.md)：真实路径预算与 Darwin bind 测试。
- [Apple 单一 Libbox 生产链](fork/apple-build-consolidation.md)：复用前核对来源，避免重复构建或链接错库。

以上施工记录中的“未合并／实验分支”是其写作时状态；v0.1.39 的集成状态以 [2026-11-05 增量校准](../KNOWLEDGE/CALIBRATION-2026-11-05.md) 和固定源码为准。
