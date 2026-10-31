# 子仓库与分叉依赖（Child repositories and forked dependencies）

## 范围与如何阅读修订（Scope and how to read the revisions）

客户端仓库与分叉的 Go 依赖也属于内核知识范围。父仓库源码目标是 `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`；下面的 pin 表示父仓库实际使用的版本，并不构成对之后每个子仓库变更或每个发布产物的断言。2026-10-31 观测到，Apple、Android、Desktop、Dashboard 与 uTLS 的 `main` tip 与下面各自的 pin 相符。每次刷新都要记录独立的分支移动，即使父仓库没有 diff。`REFRESH.md` §25A 规定了发现、比较、证据与安全检视的流程。确切的历史父树条目用 `git ls-tree <parent-sha> clients/apple clients/android clients/desktop` 查询；Go pin 是 `replace` 指令，不是 `require`；Dashboard 是 Desktop 里的 gitlink，不是父仓库的 gitlink。

| 来源 | 在 `cb71cf80d72` 的先前 pin | 当前父/所有者 pin | 使用方式 |
|---|---|---|---|
| [Apple](https://github.com/Piggy-Cat-bit-shadow/sing-box-for-apple) | `3aedd7417488e33a69fe2d46dc34539780d76157` | `044e6d5447eb1285c950bd12cee10effab641f5a` | 父仓库中的客户端 gitlink；手动 Apple 构建会自行解析源码一次 |
| [Android](https://github.com/Piggy-Cat-bit-shadow/sing-box-for-android) | `9d15717a85c5d995f4179457b48213b5c73b511d` | `c7837107dc0126fb704fdf08cf203e10b30c8a22` | 父仓库中的客户端 gitlink；产品分支现为 `main` |
| [Desktop](https://github.com/Piggy-Cat-bit-shadow/sing-box-for-desktop) | `eb2dbf8ba7de0bb4f37e524dada0f30c38417282` | `6d4ccf8c5cc6dd0749cfee7822f8de45eab4f40b` | 父仓库中的客户端 gitlink |
| [Dashboard](https://github.com/Piggy-Cat-bit-shadow/sing-box-dashboard) | `d452c24` | `5e9271e516e437b001dbb51c4258441490b82318` | Desktop 中的嵌套 gitlink；据报告，父仓库没有对其做冻结重写 |
| [sing-tun](https://github.com/Piggy-Cat-bit-shadow/sing-tun) | `147ab164c5ae` | `8b9a380087059f759441c404e26061be218dfa64` | 根与测试的 Go replace 指令 |
| [uTLS](https://github.com/Piggy-Cat-bit-shadow/utls) | `1bcb8e4d74d2` | `b63a2e9a7b1eeba03cb7e2db2b92b5bb027894fc` | 根与测试的 Go replace 指令 |

其余实际依赖列在 `release/dependencies.json` 与 `release/source-lock.json` 中：`sing`、`sing-quic`、
`quic-go`、Cronet 根与聚合，外加 29 个受控原生模块。清单（manifest）表达期望的分支与策略；
锁文件（lock）记录实际使用的 pin。它不是“某个产物包含这些字节”的证据。请查看 Go 模块与源锁，
不要假定每个依赖都是 Git 子模块。参见 `ci-release.md` 与 `build-pruning-release.md`。

## Apple：热点 MTU、订阅 body 与构建来源（Apple: hotspot MTU, subscription body and build source）

在 `16cf87b4`，`Library/Network/ExtensionPlatformInterface.swift` 只在自动路由路径上设置 TUN MTU：请求值非正时取 1500；`includeAllNetworks` 启用时，超过 1500 的值会被限制为 1500，用于热点转发。设置安装完成后，它会记录一份设置摘要。这是源码行为，不是真实的 iOS 热点测量。`Library/Network/HTTPClient.swift`、`RemoteProfileFetcher.swift` 与 `ApplicationLibrary/Views/Profile/NewProfileViewModel.swift` 让订阅内容的导入与更新使用 `sing-box/1.15.0`；通用应用 UA 是另一套，配额兼容性探测也是。请复核实际调用点，不要替换每一个 UA。

手动 Apple 工作流的选择可能解析出与 gitlink 不同的提交；确切的输入与“只用单一 macOS runner”的规则见 `ci-release.md`。上文 `16cf87b4` 的行为早于当前 Apple 固定版本 `044e6d5`。验证链接需要 POSIX 检出与确切的目标 blob 字节，Windows 上将符号链接检出为文本文件，无法验证链接是否有效。本轮刷新没有做真实设备上的热点测试。

### Apple 上游集成——现已消费（Apple upstream integration — now consumed）

截至 2026-10-29，上游集成已经 MERGED（合并）：产品 `main`、父仓库 gitlink 与 `VERIFIED_APPLE_SHA` 全都是 `044e6d5447eb1285c950bd12cee10effab641f5a`；候选分支 `sync/upstream-2026-10-28` 已删除；macOS 客户端目标已在 arm64 macOS runner 上从该提交编译通过（run 40776674407）。下面描述这次合并携带的内容。实际源码树的 diff 共 21 条路径。`CommandXPC.swift` 把端点注册路由到 `UserServiceClient`，并在需要时请求 Wi-Fi 刷新；`UserServiceXPC.swift` 在锁下替换连接并让旧连接失效，失效回调只清除同一个连接。发布者（publisher）使用 `systemExtensionMachServiceName`；旧的端点注册表已移除。其他集成改动包括分享扩展、刷新后的 Tailscale 对端视图、更新授权，以及移除上游组件中一条重复的启动告警。确切的平台条件请检视固定版本对应的源码树。

两个集成提交修正了 `Tests/SubscriptionQuotaCaption/Sources/Caption/` 的符号链接目标路径，然后从符号链接目标的 blob 中移除意外的 CRLF 字节。Git 树模式是 `120000`。在具备 POSIX 能力的检出中检查原始 blob 大小与目标字节；Windows 上看似成功的文本文件处理并不是符号链接解析。源码 diff 已经检视。
[Run 40776674407](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40776674407) 用一个不同的
父 SHA 为该 Apple SHA 构建了 macOS 目标；[run 40819615363](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40819615363)
在 v0.1.34 父 SHA 上构建了 iOS 与 Libbox，macOS 则被跳过。两者都不证明设备行为。

## Android：服务顺序、配置应用与源码收敛（Android: service ordering, profile application and source convergence）

直接比较源码树的 diff `9d15717a..c7837107` 跨 43 个文件，并包含已合并的开发历史；不能仅凭祖先关系判断作者身份。在 `app/src/main/java/io/nekohasekai/sfa/bg/BoxService.kt` 中，启动时会重建服务模式，等 `Application.application.setupLibbox()` 完成之后再启动命令服务器，并丢弃未完成的电源报告草稿，而不是将其转为正式报告。在 `compose/screen/dashboard/DashboardViewModel.kt` 中，`applySelectedProfile` 在检测到的服务模式匹配时重新加载；不匹配时先停止，在限定轮次内等待服务进入 stopped 状态，再请求启动。选中的远程配置发生变化时，走同一条路径。订阅内容请求使用 `HTTPClient.SUBSCRIPTION_USER_AGENT`（`sing-box/1.15.0`），与通用应用身份分开。已 pin 客户端中的 `version.properties` 记录 747 / `1.15.0-alpha.13` / `go1.26.10`；这些是客户端元数据，不是父仓库的发布版本。平台自动重定向、应用列表与网络变更也在此范围内：涉及这些路径的任务，请检视受影响的源码。仅基于 pin 的 ABI 检查在本地通过；此处没有执行 Android 应用构建或设备运行。

## Desktop 与嵌套的 Dashboard（Desktop and nested Dashboard）

Desktop `eb2dbf8..6d4ccf8` 改动了 `src/main/profiles.ts`、`src/shared/subscription.ts`、订阅抓取测试，以及它的 `dashboard` gitlink。`remoteProfileRequest` 对订阅内容请求使用同一个稳定的 `sing-box/1.15.0`；独立的应用身份与配额探测仍然分开。GitHub 对 Dashboard `d452c24..5e9271e` 的比较只列出 `.github/workflows/ci.yml` 与 `.github/workflows/deploy.yml`：这是部署工作流的改动，不是内嵌 Web UI 的源码 diff。依赖更新的正确顺序是 Dashboard → Desktop → 父仓库；改父锁无法写入嵌套的 gitlink。本地没有 Desktop 检出；远程比较与补丁已经检视，但此处没有执行 Electron 构建或测试。

## sing-tun 路由端口与 uTLS（sing-tun routed port and uTLS）

在 `8b9a3800`，sing-tun 的 `stack_go.go` 实现 `RoutedPort`：它捕获最初配置的 IPv4/IPv6 地址，挂接或摘除 dispatcher 返回路径，并把数据包送入正在运行的 Go 栈。`stack_go_engine.go` 在回环 hairpin 之后、UDP socket 路径之前调用 `ReturnParsed`。这个分叉把上游的路由端口能力适配到已有的返回机制上，没有引入第二套返回实现；`flow_dispatch_routed_port_test.go` 是子仓库的回归锚点。父仓库的 `JudgeHostFlow` 边界见 `masque-connectip.md`。父仓库的 adapter 单元测试不能单独证明真实设备上的数据包送达，也不能证明子仓库协议栈的所有并发交错。

uTLS `1bcb8e4d..b63a2e9a` 集成了 QUIC 握手构建错误的清理路径，在 ECH 活跃时对 ECH 内层 ClientHello 做哈希，供 early-data transcript 使用；并且只对标准 ClientHello 顺序（`outerExts == nil`）压缩 ECH ALPN。该子仓库包含回归测试 `u_quic_build_error_test.go` 与 `u_ech_alpn_test.go`；本轮刷新检视的是子仓库源码 diff，没有运行新的子仓库测试。这些改动与 core 已 pin 的 TLS 行为相关，但不能证明与每个服务器或平台都可互操作。

## 证据与下次刷新（Evidence and next refresh）

父仓库的 gitlink 从已提交的树中读取，不是从较旧的本地 Apple/Android 检出读取；子仓库对象是在不改动那些工作树的情况下抓取的。Desktop 与 Dashboard 的比较使用 GitHub API；sing-tun 与 uTLS 的源码补丁也在那里检视。出现新的子仓库源码时，要分别与上次记录的固定版本和分支 tip 比较。尚未被父仓库引用的分支改动单独归类，不要仅因为某个子仓库 head 移动就移动父基线。上一轮校准的平台与 ABI 限制仍然适用。一次检出、`SKIPPED` 扫描、提交信息或报告里的历史测试，都不是当前的构建或设备结果。
