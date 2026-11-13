# Apple 自签、命令通道与本地回环（Apple self-signing, command channel and loopback）

## 0.1.45 客户端消费定位（固定源码 3032ca5）

当前父仓 `git ls-tree HEAD clients/apple` 为 `9330d9018c95231ac9b5d8abb6244dcc33c95830`；窗口 03 的更新候选 `15a4ab6e` 属于另一分支，不能在未合并时写作当前 pin。内核的新配置/请求合同要另核 App 与 NE 的实际调用及 ABI 常量；本文下方 v0.1.44 设备边界是历史固定消费记录，不自动证明 0.1.45 真机。


## v0.1.44 发布增量（Released delta, 2026-11-08）

父仓 `3b6ec64f7145a2add99656d47caaa4f22a1cc478` 固定 Apple `e3283104c984d60cdf2a0250b47b7fc446e71ba6`、sing-tun `8f65451175c085a055a552898758f8594f988239`。内核 `8c1e804ea` 在 TUN 边界对配置中的 IPv4-mapped 回环地址执行 `Unmap` 后按真实地址族拆分，避免开关关闭时原样保留的 `::ffff:10.7.0.1` 被误放进 IPv6 列表；`protocol/tun/loopback_family_test.go` 和 `experimental/libbox/tun_loopback_config_test.go` 覆盖有效选项与 Libbox getter。Apple `e3283104` 将等待 `setTunnelNetworkSettings` 的最多三秒移出 `serviceLifecycleLock`，新启动重置成功标志，停止或后续启动通过 generation 使旧等待失效；reload 保留当前 TUN，不重复检查。正式 [Kernel release 42391992312](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/42391992312) 的 Libbox、iOS、macOS 和自签 IPA 构建成功。以上为源码、测试与 CI 构建证据，不是真机 NE route/reload、SideStore 或签名后运行证明。

## 范围与修订（Scope and revisions）

以下原始专题的源码目标为父仓 `v0.1.39` / `fc7e400243d46a451b2dffbe68cb5a3147c66289`；Apple gitlink `ab575dace8190a48a9d7cf666ef009f803e6ca62`；sing-tun pin `8f65451175c085a055a552898758f8594f988239`。2026-11-05 独立观察的 Apple `main` 和 sing-tun `dev` 与当时 pin 相同；观察值不是永久的分支承诺。Android、Desktop 与嵌套 dashboard 不因该次功能发生移动。当前发布消费值见上方 v0.1.44 增量。

## 三条不同契约（Three distinct contracts）

1. **签名与共享容器**：Info.plist 的 App Group 是声明，实际签名和 provisioning profile 是授权。`FilePath.swift` 发现候选并用系统容器 API 确认访问；无共享容器时的私有目录退化只能保证相关进程自己的存储，不会自动产生跨进程 IPC。
2. **命令 socket**：`resolveCommandSocketPathIn` 由服务端与客户端共同使用，基于真实目录预算选路径。Darwin `sun_path` 104 字节，路径最多 103 字节；候选包括 base、系统临时目录与其父目录、调用方 temp 与其父目录。文件名按剩余空间缩短，不能假设调用者传入的 temp 就是系统 TMPDIR。
3. **本地回环**：客户端开关经 start options 到 `daemon.OverrideOptions.LoopbackAddress`，内存中合并至第一个 TUN；同一列表向 libbox getters 与 NE host route 输出。TUN 反射和路由两端缺一不可。

## 当前回环行为（Current loopback behaviour）

入口为“更多 → 连接行为 → 配置覆盖 → 本地签名回环”，英文 `Local Signing Loopback`；默认 false，注入 `10.7.0.1`。iPad 设置布局不同，使用相同 preference。关闭时无新增覆盖，用户自己的回环地址不被删除；开关不改订阅文件。

`daemon.applyTUNOverrides` 只处理首个 TUN；开关打开时 `mergeLoopbackAddresses` 对“用户地址 + 注入地址”做 Unmap/WithZone、去重并保留顺序。开关关闭时该 merge 保持配置原样（包括用户写下的 IPv4-mapped 拼写），家庭拆分在 TUN 边界完成：`protocol/tun.SplitLoopbackAddresses` 先 Unmap/去 zone，再按 `Is4` 分到 IPv4/IPv6 有效列表。这样 `::ffff:10.7.0.1` 不会进入 IPv6 列表，libbox getter 也不会把 IPv4 字符串交给 Apple 的 IPv6 路由构建器；sing-tun 的 `isLoopbackDestination` 同样先 Unmap 目的地，因此有效列表与匹配谓词使用同一族。普通 IPv4/IPv6、用户原有顺序保持不变。Apple `ExtensionProvider.localSigningLoopbackAddress` 是客户端常量；`#if os(iOS)` 限制注入和 NE 路由，macOS 不因同一平台接口被额外加入路由。

数据面由 sing-tun 默认 `go` 栈的 hairpin 处理 TCP/UDP/ICMPv4/v6。**其他显式栈没有完整的同等反射**，主仓 `loopbackStackWarning` 给出 iOS 警告。地址交换保持相关校验和性质，不需要盲目新增 checksum 改写；邻接地址不应被捕获。

## 来源与重要演进（Provenance and evolution）

| 变更 | 来源 / 提交 | 需要记住的原因 |
| --- | --- | --- |
| 回环覆盖与桥接口 | `3aef9b341`，Apple feature + sing-tun | `CORE-CLIENT-COORDINATED`：UI、ABI、NE route、数据面需要同时到位 |
| 地址规范化与 iOS 路由隔离 | `4730e819b`，sing-tun `2bc6adb`，Apple `36f36e7` | 不能只在配置端 Unmap；也不能给 macOS 偷加路由 |
| 无共享容器启动容错 | Apple 自签系列；父仓 `67926c70b` 等 | 没有 `.ips` 时初始闪退根因属于高置信分析，不是崩溃栈证明 |
| 深路径 socket 三轮修复 | `1f8cbba58`、`a840a016f`、`62a78be5a` | 从换目录改为按真实字节预算选择；系统临时目录必须始终候选 |
| Apple 最终集成与授权组发现 | 父仓 `ff72ea120`、`d05cffe8b`、`90246a280` | 实验分支不再是产品来源；最终 pin 包含 upstream、自签和回环 |

参考 Hako 的行为与测试目标是研究来源，不能把它的全部栈覆盖或源码贡献直接当成本项目实现。本 fork 默认 go hairpin 是已有机制上的扩展。

## 构建、发布与证据（Build, release and evidence）

`client-apple.yml` 提供同提交 Libbox，消费者先验证版本、父 SHA 与库 hash；自签工作流名仍有 `experiment`，但正式 `release.yml` 已消费其完整 IPA 作为公开资产。不能仅凭工作流名字判断产品是否正式发布。

v0.1.39 API 核对：公开、非 prerelease，包含 `jiejie-ios-selfsign-unsigned-0.1.39.ipa`。macOS GUI 可以构建，**DMG 没有进入该版 8 项正式附件**。TestFlight 是独立签名／上传入口。

本次重新读取固定 Apple 源码中的 `FilePath.swift`、`ExtensionProvider.swift`、`HakoProfileOverrideView.swift`，以及固定 sing-tun 的 loopback 测试；证据是 `VERIFIED_SOURCE`。用户真机反馈限定在 [既有调查报告](../reports/ios-selfsign-compat-2026-11-04.md)，不能推广成任意工具都能自签成功。

复查：

```sh
go test ./daemon -run Loopback -count=1
go test ./experimental/libbox -run 'Loopback|DeviceLayout|TemporaryDirectory|Bindable|FallbackName' -count=1
go test ./protocol/tun -run Loopback -count=1
# Darwin：实际 socket bind 测试；Windows 不能替代
go test ./experimental/libbox -run 'TestDarwin' -count=1 -v
```

## AI 注意事项与设备边界（AI cautions and device boundary）

- 不用缩短后的硬编码目录替代平台预算；目录长度按字节，测试注入真实 TMPDIR，不通过测试机目录形状猜设备行为。
- 不把 `containerURL != nil` 之外的 plist 字符串当权限；主应用与扩展要实际访问同一容器。
- 不把界面能打开、VPN 图标亮起或网站可访问分别当作 IPC、TUN 或代理出口已验证。
- 不在普通内存 trim 或平台 snapshot 回调中注入额外路由策略；回环是显式运行时覆盖。
- SideStore 刷新全流程、iPad UI、真实 NE route/reload 与不同签名方式需要分别设备验收。

用户指南：[自签与回环](../../docs/IOS-SELFSIGN-AND-LOOPBACK.md)。早期实现报告中的“未合并／未发布”已被 v0.1.39 的固定源码与公开附件取代，仅保留其写作时语境。
