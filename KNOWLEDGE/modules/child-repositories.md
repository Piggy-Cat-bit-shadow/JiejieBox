# 子仓库与分叉依赖（Child repositories and forked dependencies）

## 0.1.45 依赖融合（2026-11-13 scoped delta）

固定源码目标：`3032ca5199dbd151be0b2cd006fba191b87cc1e8`。以下为本轮源码锚点与既有定向回归层级；设备、性能和发布结论另见验收索引。

主仓 pin 消费 sing、sing-tun、sing-quic、quic-go、uTLS、wireguard-go、sing-anytls、Mieru、cronet-go 等 fork；sing-tun 与 Mieru 已迁到其默认分支提交。核对顺序为父仓 gitlink、嵌套 gitlink、Go replace、release/source-lock.json，再读子仓实现；不把移动分支头等同于固定消费。


## 2026-11-13 依赖仓库清单（Current dependency repositories）

本节只更新**仓库名单与当前 pin**，不重写下面的历史节；完整刷新流程见 `KNOWLEDGE/REFRESH.md` §25A。
机器可读的事实仍是 `release/dependencies.json`（仓库/分支/策略）与 `release/source-lock.json`（pin）；
本节把它们摊平。表中 pin 已按当前 `testing` 的 `release/source-lock.json`、`go.mod` 与客户端 gitlink 核对；Go 依赖与客户端 pin 最初在整合分支 `427889cf24b` 记录，后已并入 `testing`。策略含义见 `release/dependencies.json`
（`update` = 发布准备可前移；`controlled` = 只能重建归档才能动；`report` = pin 在另一个仓库里）。

### Go 依赖（`go.mod` / `test/go.mod` 的 replace）

| 模块 | 仓库 | 分支 | 策略 | 当前 pin |
|---|---|---|---|---|
| `github.com/sagernet/sing` | `Piggy-Cat-bit-shadow/sing` | `dev` | update | `4abfaed41727` |
| `github.com/sagernet/sing-tun` | `Piggy-Cat-bit-shadow/sing-tun` | `dev` | update | `3c0ead27405b` |
| `github.com/sagernet/sing-quic` | `Piggy-Cat-bit-shadow/sing-quic` | `main` | update | `c1b074f1f868` |
| `github.com/sagernet/quic-go` | `Piggy-Cat-bit-shadow/quic-go` | `sagernet` | update | `158445cdda77` |
| `github.com/metacubex/utls` | `Piggy-Cat-bit-shadow/utls` | `main` | update | `e14b88879d80` |
| `github.com/sagernet/wireguard-go` | `Piggy-Cat-bit-shadow/wireguard-go` | `awg3-inject` | update | `7ece3d66d79d` |
| `github.com/sagernet/sing-anytls` | `Piggy-Cat-bit-shadow/sing-anytls` | `main` | update | `f4acc22de770` |
| `github.com/enfein/mieru/v3` | `Piggy-Cat-bit-shadow/mieru` | `main` | update | `b5934f7136b4` |
| `github.com/sagernet/cronet-go` | `Piggy-Cat-bit-shadow/cronet-go` | `main` | update | `362e63b14ce2` |
| `github.com/sagernet/cronet-go/all` | `Piggy-Cat-bit-shadow/cronet-go` | `main` | update | `7ce9c49b464a` |
| `cronet-go/lib/*`（29 个平台归档） | `Piggy-Cat-bit-shadow/cronet-go` | — | controlled | `362e63b14ce2`（与 root 同一个 native build commit；只随重建归档而移动） |

### 客户端与嵌套 gitlink

| 路径 | 仓库 | 分支 | 策略 | 当前 pin |
|---|---|---|---|---|
| `clients/apple` | `Piggy-Cat-bit-shadow/sing-box-for-apple` | `main` | update | `322100096fbf` |
| `clients/android` | `Piggy-Cat-bit-shadow/sing-box-for-android` | `main` | update | `9816e0068c82` |
| `clients/desktop` | `Piggy-Cat-bit-shadow/sing-box-for-desktop` | `main` | update | `a658231fcd97` |
| `clients/desktop:dashboard` | `Piggy-Cat-bit-shadow/sing-box-dashboard` | `main` | report | `d9fca1b1f226` |

### 2026-11-13 窗口 03：客户端 pin 已消费

前表最初记录**整合分支** `work/integration-2026-11-13`（pin 提交 `427889cf24b`）上的 pin，现已并入 `testing`；
取自 `git ls-tree` 与 `release/source-lock.json`，并由
`release-freeze.py verify --strict` 与 `check-libbox-abi.sh --pin-only` 双重核对。

四个值对应本窗口五条整合分支里需要改 pin 的三条（android 的整合分支是 fast-forward，
最终提交与源分支相同）：

| 客户端 | 源分支（中间值） | 整合分支（当前 pin） |
|---|---|---|
| Apple | `15a4ab6e` | `3221000`（main `45253d2a` + 选择/回读） |
| Android | `9816e006` | `9816e006`（fast-forward，未变） |
| Desktop | `a4d77faf` | `a658231`（+ unconfirmed 修复 + 嵌套 dashboard pin） |
| Dashboard（嵌套） | `8039ed0d` | `d9fca1b`（main `d828b130` + 选择账本） |

`scripts/ci/check-libbox-abi.sh` 的 `VERIFIED_APPLE_SHA` 随 gitlink 一起移到了 `3221000`；
`VERIFIED_ANDROID_SHA` 早就是 `9816e006`。这些提交都是**已推送**的（`git submodule
update --init` 从 GitHub 克隆成功即是证据），不是本地提交。

旧 `3032ca5` 快照仍使用旧客户端 pin；不要将其固定目标的结论套用到当前 `testing`。

### 与下面历史节的关系

v0.1.44（2026-11-08）及更早的节保留原样作为历史证据，其中记录的 pin 不是当前 pin。

## v0.1.44 固定消费（Released pins, 2026-11-08）

父仓 `v0.1.44^{commit}` = `3b6ec64f7145a2add99656d47caaa4f22a1cc478`。`git ls-tree` 与 `release/source-lock.json` 核对的客户端 gitlink 为 Apple `e3283104c984d60cdf2a0250b47b7fc446e71ba6`、Android `c7837107dc0126fb704fdf08cf203e10b30c8a22`、Desktop `41722b030ea481523290d932a1d81177f8c13f32`；`release-freeze.py verify` 通过。Apple 从上一固定值 `0c7b10be` 前移到 `e3283104`，主要改动是 iOS 隧道接管等待从服务生命周期锁内移出，并为新会话重置接管标志。父仓的 Apple ABI 已验证 SHA 与此 pin 一致，正式运行构建了 Libbox、iOS、macOS 和自签 IPA。上述是固定源码与 CI 构建证据，不是 iPhone/iPad 上的 NE route/reload 验证。Satelite APK 在独立仓库通过自己的 `coreCommit` 和 tag 发布，不由父仓 `clients/android` gitlink 或内核 AAR 发布自动完成。本版另行回读其客户端 `v0.1.44` tag（`22ba907d62c23f09272117134a04e3bc96962520`）与 [公开 Release](https://github.com/Piggy-Cat-bit-shadow/satelite-one/releases/tag/v0.1.44)；双 ABI 签名构建见 [Release APK 42393794665](https://github.com/Piggy-Cat-bit-shadow/satelite-one/actions/runs/42393794665)。

## 2026-11-07 观察（Historical observed revisions）

Provider 专题目标与 v0.1.42 发布目标的客户端 gitlink 和 `release/source-lock.json` 相同。Apple pin 已在 v0.1.42 前移到 `0c7b10be05811cdd3f31a48a7cbe49f061852084`。Android、Desktop、嵌套 Dashboard 仍沿用下方记录的 pin。四个跟踪分支的远端头与这些 pin 相符。Cronet 的源码分支头与内核绑定包发布 pin 属于不同来源，不能直接替换。详见[专题刷新](../CALIBRATION-2026-11-07.md)。

## v0.1.39 已消费状态（Historical consumed revisions）

当前父仓 pin：Apple ab575dace8190a48a9d7cf666ef009f803e6ca62，Android c7837107dc0126fb704fdf08cf203e10b30c8a22，Desktop 41722b030ea481523290d932a1d81177f8c13f32；Dashboard 0da341722a8a0046fa5f5b602a19f9b4e1efccc8。sing-tun 为 8f65451175c0；Cronet root/libs 362e63b14ce2、all 7ce9c49b464a。
本次独立观察 Apple main、sing-tun dev 与其 pin 相同，读取重要固定源码；cronet-go 的 main/go 两分支分别是源码与发布树。完整观察值与限制见 [增量校准](../CALIBRATION-2026-11-05.md)。下表保留的是 2026-10-31 固定值，不是 v0.1.39 pin 表。
不要从历史十仓 behind=0 推断运行时已经测试，也不要从已归档构建镜像取新的发布版本。

## 范围与如何阅读修订（Scope and how to read the revisions）

客户端仓库和 fork 的 Go 依赖都属于内核知识库的维护范围。本文以父仓库
`0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28` 为基准；下表记录的是该版本固定的源码，
不能据此推断后续子仓库变更或发布产物的内容。

2026-10-31 检查时，Apple、Android、Desktop、Dashboard 和 uTLS 的 `main` 分支头与表中
各自的 pin 一致。子仓库可以独立更新，即使父仓库没有新提交，刷新时也要检查其分支变化。
发现和核对方法见 `REFRESH.md` §25A。

查看父仓库某个历史版本的客户端固定值，可运行
`git ls-tree <parent-sha> clients/apple clients/android clients/desktop`。Go 依赖的实际固定值
看 `replace`，不能只看 `require`；Dashboard 的 gitlink 位于 Desktop 仓库内，而非父仓库。

| 来源 | 在 `cb71cf80d72` 的先前 pin | 当前父/所有者 pin | 使用方式 |
|---|---|---|---|
| [Apple](https://github.com/Piggy-Cat-bit-shadow/sing-box-for-apple) | `3aedd7417488e33a69fe2d46dc34539780d76157` | `044e6d5447eb1285c950bd12cee10effab641f5a` | 父仓库中的客户端 gitlink；手动 Apple 构建会自行解析源码一次 |
| [Android](https://github.com/Piggy-Cat-bit-shadow/sing-box-for-android) | `9d15717a85c5d995f4179457b48213b5c73b511d` | `c7837107dc0126fb704fdf08cf203e10b30c8a22` | 父仓库中的客户端 gitlink；产品分支现为 `main` |
| [Desktop](https://github.com/Piggy-Cat-bit-shadow/sing-box-for-desktop) | `eb2dbf8ba7de0bb4f37e524dada0f30c38417282` | `6d4ccf8c5cc6dd0749cfee7822f8de45eab4f40b` | 父仓库中的客户端 gitlink |
| [Dashboard](https://github.com/Piggy-Cat-bit-shadow/sing-box-dashboard) | `d452c24` | `5e9271e516e437b001dbb51c4258441490b82318` | Desktop 中的嵌套 gitlink；据报告，父仓库没有对其做冻结重写 |
| [sing-tun](https://github.com/Piggy-Cat-bit-shadow/sing-tun) | `147ab164c5ae` | `8b9a380087059f759441c404e26061be218dfa64` | 根与测试的 Go replace 指令 |
| [uTLS](https://github.com/Piggy-Cat-bit-shadow/utls) | `1bcb8e4d74d2` | `b63a2e9a7b1eeba03cb7e2db2b92b5bb027894fc` | 根与测试的 Go replace 指令 |

其余依赖见 `release/dependencies.json` 和 `release/source-lock.json`，包括 `sing`、
`sing-quic`、`quic-go`、Cronet 根模块及聚合模块，以及 29 个受控原生模块。manifest 记录
期望使用的分支和策略，lock 记录实际固定版本；两者都不能单独证明产物包含哪些字节。
Go 模块不一定是 Git 子模块。发布检查详见 `ci-release.md` 与 `build-pruning-release.md`。

## Apple：热点 MTU、订阅 body 与构建来源（Apple: hotspot MTU, subscription body and build source）

在 `16cf87b4`，`Library/Network/ExtensionPlatformInterface.swift` 只在启用自动路由时设置
TUN MTU：请求值不大于零时使用 1500；启用 `includeAllNetworks` 后，大于 1500 的值会被
限制到 1500，以适应热点转发。设置生效后，客户端会记录一份摘要。这是源码观察，并非真实
设备上的热点测试结果。

`Library/Network/HTTPClient.swift`、`RemoteProfileFetcher.swift` 和
`ApplicationLibrary/Views/Profile/NewProfileViewModel.swift` 使用 `sing-box/1.15.0` 作为
订阅内容导入和更新请求的 User-Agent。应用的其他请求与配额兼容性探测使用各自的身份；
修改时应核对调用点，不要把所有 User-Agent 一并替换。

手动 Apple 工作流解析出的提交可能不同于父仓库 gitlink。输入优先级和单 macOS runner
模式见 `ci-release.md`。上面描述的是 `16cf87b4`；当前 Apple 固定版本是 `044e6d5`。
验证符号链接时需要检查 POSIX 检出及目标 blob 的原始字节；Windows 将链接检出为文本文件，
不能据此证明链接有效。本轮没有执行真实设备上的热点测试。

### Apple 上游集成已纳入父仓库（Apple upstream integration — now consumed）

截至 2026-10-29，Apple 上游集成已合并：产品 `main`、父仓库 gitlink 和
`VERIFIED_APPLE_SHA` 均为 `044e6d5447eb1285c950bd12cee10effab641f5a`，候选分支
`sync/upstream-2026-10-28` 已删除。macOS 客户端已在 arm64 macOS runner 上按该 Apple
提交编译通过（run 40776674407）。

该次集成的源码差异涉及 21 个路径。`CommandXPC.swift` 将端点注册交给 `UserServiceClient`，
并在需要时请求更新 Wi-Fi 状态；`UserServiceXPC.swift` 在锁内替换连接、使旧连接失效，
失效回调只清除与其对应的连接。端点发布代码改用 `systemExtensionMachServiceName`，旧端点
注册表被移除。其他改动包括分享扩展、Tailscale 对端视图、更新授权，以及移除上游组件中重复
的启动告警。平台条件以固定版本的源码为准。

两个集成提交修正了 `Tests/SubscriptionQuotaCaption/Sources/Caption/` 的符号链接目标路径，并移除了链接目标 blob 中意外出现的 CRLF 字节。Git 树模式为 `120000`。验证时应在支持 POSIX 符号链接的检出中检查原始 blob 大小和目标字节；Windows 上将链接作为文本文件处理，不能证明链接能够解析。本轮已检查源码 diff。
[Run 40776674407](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40776674407) 用一个不同的
父 SHA 为该 Apple SHA 构建了 macOS 目标；[run 40819615363](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/40819615363)
在 v0.1.34 父 SHA 上构建了 iOS 与 Libbox，macOS 则被跳过。两者都不证明设备行为。

## Android：服务顺序、配置应用与源码收敛（Android: service ordering, profile application and source convergence）

比较 `9d15717a..c7837107` 的源码树可见 43 个文件发生变化，其中包含合并的开发历史；
不能仅凭提交祖先关系判断作者身份。

`app/src/main/java/io/nekohasekai/sfa/bg/BoxService.kt` 在启动时重建服务模式，等待
`Application.application.setupLibbox()` 完成后再启动命令服务器，并丢弃未完成的电源报告
草稿，不把它转成正式报告。

`compose/screen/dashboard/DashboardViewModel.kt` 的 `applySelectedProfile` 在服务模式
未变时重新加载配置；模式变化时则停止服务，在有限轮次内等待 stopped 状态，再请求启动。
当前选中的远程配置发生变化时，也走这条路径。订阅内容请求使用
`HTTPClient.SUBSCRIPTION_USER_AGENT`（`sing-box/1.15.0`），不改变应用其他请求的身份。

固定版本中的 `version.properties` 记录 747 / `1.15.0-alpha.13` / `go1.26.10`；它们是
客户端元数据，不是父仓库的发布版本。该范围还包含自动重定向、应用列表和网络处理的变化，
修改相关路径时应查阅源码。本地只完成了 ABI pin 检查，没有构建 Android 应用或运行设备测试。

## Desktop 与嵌套的 Dashboard（Desktop and nested Dashboard）

Desktop `eb2dbf8..6d4ccf8` 修改了 `src/main/profiles.ts`、`src/shared/subscription.ts`、
订阅抓取测试和 `dashboard` gitlink。`remoteProfileRequest` 对订阅内容请求使用固定的
`sing-box/1.15.0` User-Agent；应用其他请求和配额探测仍使用各自的身份。

GitHub 对 Dashboard `d452c24..5e9271e` 的比较只列出了 `.github/workflows/ci.yml` 与
`.github/workflows/deploy.yml`，因此这次改动涉及部署工作流，不涉及内嵌 Web UI 的源码。
依赖更新顺序是 Dashboard → Desktop → 父仓库：父仓库的 lock 文件无法直接修改 Desktop 内部
的 gitlink。本地没有 Desktop 检出，本次依据远程比较和补丁核对，未执行 Electron 构建或测试。

## sing-tun 路由端口与 uTLS（sing-tun routed port and uTLS）

在 `8b9a3800`，sing-tun 的 `stack_go.go` 实现了 `RoutedPort`：读取配置的首个 IPv4/IPv6
地址，挂接或移除 dispatcher 的返回路径，并将数据包送入运行中的 Go 栈。`stack_go_engine.go`
在处理 loopback hairpin 之后、进入 UDP socket 路径之前调用 `ReturnParsed`。

该 fork 将上游的 routed port 能力接入已有返回机制，没有另建一套返回路径；回归测试见
`flow_dispatch_routed_port_test.go`，父仓库的 `JudgeHostFlow` 边界见 `masque-connectip.md`。
父仓库的 adapter 单元测试不能证明真实设备上的数据包送达，也不能覆盖子仓库协议栈的所有
并发时序。

uTLS `1bcb8e4d..b63a2e9a` 合入了 QUIC 握手构建错误的清理修复；启用 ECH 时，early-data
transcript 改为使用内层 ClientHello 的哈希；ECH ALPN 压缩只用于标准 ClientHello 顺序
（`outerExts == nil`）。子仓库中的回归测试是 `u_quic_build_error_test.go` 和
`u_ech_alpn_test.go`。本轮检查了源码 diff，没有重跑子仓库测试；这些证据不能证明与所有
服务器或平台都兼容。

## 证据与下次刷新（Evidence and next refresh）

本轮从父仓库已提交的源码树读取 gitlink，没有把较旧的本地 Apple/Android 检出当作固定版本；
抓取子仓库对象时也没有改动这些工作树。Desktop、Dashboard、sing-tun 和 uTLS 的对比使用了
GitHub API。

下次刷新时，应分别比较上次记录的 pin 和子仓库分支头。尚未被父仓库引用的改动要单独标注；
不能仅因为子仓库分支前进就推进父仓库基线。上一轮记录的平台和 ABI 限制仍然适用。一次检出、
`SKIPPED` 扫描、提交信息或历史测试报告，都不能当作当前构建或设备测试通过的证据。
