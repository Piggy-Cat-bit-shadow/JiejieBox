# 构建、裁剪与发布配置（Build, pruning and release profiles）

## 2026-10-31 目标下的依赖固定（Dependency pins at the 2026-10-31 target）

根 `go.mod` 与 `test/go.mod` 都把 `sing-tun` 替换为 fork 修订 `8b9a38008705`，并把
`metacubex/utls` 替换为 fork 修订 `b63a2e9a7b1e`。`release/JIEJIE_VERSION` 与源码锁
记录 `0.1.34`；[v0.1.34](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.34)
是一个经过独立验证的已发布版本，其标签剥离（peel）到 `0067b03e377`。
源码冻结清单、受控的原生 Cronet 策略以及仪表盘上报，在
`ci-release.md` 中描述。锁与 gitlink 本身并不证明产物内容。

## 测试目标下的协议注册与构建门禁（Protocol registration and build gates at the testing target）

在 `011f3fdcd99` 处为 **VERIFIED_SOURCE**：`include/registry.go` 注册 TUN、redirect、SOCKS、
HTTP、Shadowsocks、Snell、VMess、Trojan、Naive inbound、VLESS、AnyTLS 与 Mieru；出站
注册包含 selector、URLTest 与负载均衡，以及那些适用的协议。
MASQUE 是一个 endpoint。`include/quic.go` 在 `with_quic` 下注册 Hysteria、TUIC 与 Hysteria2；
`include/naive_outbound.go` 仅在 `with_naive_outbound` 下注册 Naive 出站。受跟踪的
`release/DEFAULT_BUILD_TAGS` 包含该标签，而 `DEFAULT_BUILD_TAGS_OTHERS` 不包含。请针对特定目标
阅读当前的 profile 与 stub 文件：仅有注册或 option 类型，并不证明分发的二进制包含该实现，
也不证明互操作性。

出站注册表还包含 `bridge`：它通过一个平台后端实现 L3 流转发，
而不是普通的 `DialContext` 或 `ListenPacket`（两者都返回错误）。它的
预匹配会拒绝本地目标；平台后端与不支持平台的 stub 位于
`protocol/bridge`。Linux 的后端会读取出口接口的 MTU，这并不是整条
路径 MTU 的证明。OpenVPN 客户端/服务器与 OpenConnect endpoint 及其 DNS 传输分别通过
`with_openvpn` 与 `with_openconnect` 注册；相反标签的 stub 返回一个
可行动的构建错误。它们在注册表中的存在并不证明某个特定 profile 包含
它们。端点自有的 DNS 解析器退役在 `core-lifecycle-runtime.md` 中讨论。这些
是源码边界，不是实时 VPN 或 bridge 互操作证据。

### TLS 引擎与 ECH 重试（TLS engines and ECH retry）

`common/tls/client.go` 在已配置时选择 Apple 或 Windows 引擎，否则选择 Go 的引擎、
Reality 或 uTLS；`common/tls/utls_client.go` 需要 `with_utls`。默认 TLS 拨号器会关闭
失败的连接，并仅当错误是 `ECHRejectionError` 且带有重试列表、并且所选配置支持 ECH 时，
才使用服务器提供的 ECH 重试配置重试一次。该单次重试标志
防止无界重试循环。Reality 与 uTLS 共享 `common/tls/utls_client.go` 中的首飞变换辅助函数；
在假设功能对等之前，请检查所选引擎。这些是
`011f3fdcd99` 处的 `VERIFIED_SOURCE` 事实，并非“每个平台或服务器都已被检验”的主张。

`NewUTLSClient` 在构造客户端之前，会拒绝指纹不在
`utlsFingerprintSupportsECH` 显式允许列表内的 ECH，包括 `random` 这一写法。历史完整
审计中“这些组合会静默继续”的主张早于这道门禁。这是配置
守卫，不是针对每个允许预设或服务器的线缆级证据。

Go 模块的 `replace` 指令 —— 而非仅 `require` 版本 —— 选择 fork 后的 `sing`、
`sing-tun`、`sing-quic`、`quic-go`、`cronet-go` 与 `utls` 源码。请在 `go.mod` 中解析实际的
伪版本（pseudo-version），并单独比较嵌套的测试模块；客户端的 Git 子模块 gitlink
与 Go 模块固定不同，且不确立已发布二进制的内容。

在 `cb71cf80d72` 处，根与 `test/go.mod` 都把 `github.com/metacubex/utls` 替换为同一
fork 修订，且都把 `cronet-go/all` 替换为 `c9c0e6ec8845`。独立的
`scripts/ci/check-go-module-integrity.sh` 中的 `REQUIRED_FORK_REPLACES` 列表列出四个模块加上
Cronet 前缀；uTLS 并未被列在那个特定的必需列表中。不要推断该列表是
每个 fork 替换的完整清单，也不要推断文本匹配就证明构建产物内容。

**范围：** `include/*`（注册表与标签极性的裁剪对）、`release/*`（profile、版本、
固定值）、`cmd/internal/*`（构建器与生成器）、`Makefile`、`.gitmodules`。

**在修改以下内容时加载本模块：** 一个构建标签、协议注册表、一个发布 profile、一个生成器，
或一个依赖固定值。

---

## 1. 注册表是一个显式构造函数 —— 而非基于 `init()`（The registry is an explicit constructor — not `init()`-based）

`include/registry.go` **没有全局 map，也没有 `init()` 注册**。`Context(ctx)`（`:50`）
安装六个注册表，每个都是一串显式的 `Register*` 调用：

| 注册表 | 构建者 | 代表性条目 |
|---|---|---|
| Inbound | `InboundRegistry()` `:54` | `:57 tun.RegisterInbound`, `:70 naive.RegisterInbound` |
| Outbound | `OutboundRegistry()` `:83` | `:91-93 group.Register{Selector,URLTest,LoadBalance}`, `:101 registerNaiveOutbound` |
| Endpoint | `EndpointRegistry()` `:115` | `:121 masque.RegisterEndpoint` |
| DNS 传输 | `DNSTransportRegistry()` `:127` | `:130-139` |
| Service | `ServiceRegistry()` `:150` | `:153-162` |
| 证书提供方 | `CertificateProviderRegistry()` `:167` | — |

**对未来的 AI 的后果：** 注册一个协议可能需要注册表之外的改动。
`docs/BUILD-PROFILES.md:50-52` 描述了继承关系，但独立的覆盖列表与 profile
测试也可能需要改动。请审查以下位置是否适用；它们并不是每个协议都必须做的九处
改动（例如，新的源码根或标签是有条件的）：

| # | 位置 | 内容 |
|---|---|---|
| ① | `include/registry.go` | 正确的位置块，**外加**一个带标签的文件**以及**其匹配的 stub（如果该能力是可选的） |
| ② | `constant/` | 类型字符串 |
| ③ | `option/` | option 结构体 |
| ④ | `.github/workflows/server-linux-amd64.yml:518-559` | **31 条硬编码的必需导入路径** |
| ⑤ | `scripts/ci/verify-full-capabilities.sh:105-243` | **约 55 个能力用例** |
| ⑥ | `cmd/internal/build_libbox/tag_golden_test.go:43-99` | 冻结的已解析标签字符串，针对 **8 个平台** |
| ⑦ | `cmd/internal/modlayout/layout.go:54-74` | `SourceRoots`，用于新的顶层目录 |
| ⑧ | `cmd/internal/{mobilebuildtags,applebuildtags}` + profile 文件 | 如果它受标签门控 |
| ⑨ | `include/registry.go:180-193` | 一处**移除**要放进墓志铭（tombstone）列表，而不是删除 |

`include/registry.go:77-80,111-113` **无条件地**调用这些注册表符号，这正是
两种极性都必须定义它们的原因。Endpoint、DNS 传输与服务注册表
在 `:118+`、`:130+` 与 `:153+` 各自带有自己的列表。

### 有意保留的墓志铭（The deliberate tombstones）

`include/registry.go:180-193`，在 `:80` 与 `:113` 处**最后**调用，因此没有任何东西遮蔽它们：

| 墓志铭 | 消息 | 来源 |
|---|---|---|
| ShadowsocksR（inbound + outbound） | *"deprecated and removed in sing-box 1.6.0"*（中文：已在 sing-box 1.6.0 中弃用并移除） | 上游 |
| `C.TypeWireGuard` + `option.StubOptions` | *"WireGuard outbound is deprecated in sing-box 1.11.0 and removed in sing-box 1.13.0, use WireGuard endpoint instead"*（中文：WireGuard 出站已在 sing-box 1.11.0 中弃用、在 sing-box 1.13.0 中移除，请改用 WireGuard endpoint） | **fork 新增**（在 `e4b7e692` 处不存在） |

它们**不是死代码**。移除它们会把一个声明 `type: shadowsocksr` 或
出站 `wireguard` 的配置，从一条具名、可行动的报错退化为通用的 `unknown outbound type`。

> **没有任何东西守卫它们：`include/*_test.go` 文件数为零。**

### 为什么是失败的 *stub* 而不是缺失的符号（Why a failing *stub* and not an absent symbol）

具体到 `quic_stub.go`，有三个理由，而它们是可推广的：

1. **缺失的符号不会裁剪一项能力 —— 它会让模块编译失败。** 调用点是
   无条件的，而 `include` 被每个产品入口点导入。
2. **stub 用一个会失败的构造函数注册 option *类型***，因此配置仍然
   能**解码**，并以一条可行动的提示失败，而不是 `unknown inbound type`：
   `quic_stub.go:38-41` 注册 `option.HysteriaInboundOptions`，返回 `C.ErrQUICNotIncluded`，
   其文本是 *"QUIC is not included in this build, rebuild with -tags with_quic"*
   （中文：本构建未包含 QUIC，请用 -tags with_quic 重新构建）（`constant/err.go:7`）。
3. **其中两处裁剪是包级钩子，而不是注册表条目** ——
   `quic_stub.go:27-36` 安装一个会失败的 `v2ray.RegisterQUICConstructor`，而 `:48-50` 赋值
   `naive.ConfigureHTTP3ListenerFunc`，后者由 `protocol/naive/inbound.go:226` 在 `:213`
   的存在性检查之后调用。

---

## 2. 裁剪机制：带 `_stub` 孪生文件的标签极性文件对（The pruning mechanism: tag-polar file pairs with `_stub` twins）

`include/` 有 34 个文件。每项可选能力都是一对（正向标签下的 `X.go`，
其否定标签下的 `X_stub.go`），或者，对于 `with_xhttp`，是一个没有 stub 的正向文件。

| 功能标签 | 开启文件 | 关闭文件 |
|---|---|---|
| `with_acme` | `acme.go:1` | `acme_stub.go:1` (`!with_acme`) |
| `with_ccm && (!darwin \|\| cgo)` | `ccm.go:1` | `ccm_stub.go:1` + `ccm_stub_darwin.go:1` (`with_ccm && darwin && !cgo`) |
| `with_clash_api` | `clashapi.go:1` | `clashapi_stub.go:1` |
| `with_cloudflared` | `cloudflared.go:1` | `cloudflared_stub.go:1` |
| `with_dhcp` | `dhcp.go:1` | `dhcp_stub.go:1` |
| `with_naive_outbound` | `naive_outbound.go:1` | `naive_outbound_stub.go:1` |
| `with_ocm` | `ocm.go:1` | `ocm_stub.go:1` |
| `with_openconnect` | `openconnect.go:1` | `openconnect_stub.go:1` |
| `with_openvpn` | `openvpn.go:1` | `openvpn_stub.go:1` |
| `with_quic` | `quic.go:1` | `quic_stub.go:1` |
| `with_tailscale` | `tailscale.go:1` | `tailscale_stub.go:1` |
| `with_usbip && (linux \|\| (darwin && cgo) \|\| windows)` | `usbip.go:1` | `usbip_stub.go:1` |
| `with_v2ray_api` | `v2rayapi.go:1` | `v2rayapi_stub.go:1` |
| `with_wireguard` | `wireguard.go:1` | `wireguard_stub.go:1` |
| `with_xhttp` | `v2rayxhttp.go:1` | *（无 stub —— 该符号直接不存在）* |

有三个文件不带显式 `//go:build` 行：`include/oom_killer.go`、`include/registry.go` 与
`include/tz_ios.go`。前两个总是被编译；`tz_ios.go` **不是** —— 它的 `_ios.go` 文件名
把它限制到 iOS（并且它以 cgo 导入 Foundation）。`include/tz_android.go` 是一个随源码附带（vendored）的 Go 标准库
文件，带有 Go 版权头，同样受其文件名限制。

### 真正重要的极性规则 —— `quic_stub.go`（The polarity rule that matters — `quic_stub.go`）

`include/quic_stub.go:48` 安装一个会**失败**的 `naive.ConfigureHTTP3ListenerFunc`，而不是
让该字段保持 nil。这正是让 `protocol/naive/inbound.go:316 decideHTTP3Availability`
能够区分 **“构造函数缺失”** 与 **“构造函数存在但 H3 已坏”** 的原因，从而
为一个缺失的可选传输选择致命或非致命。

> **一个让字段保持 nil 的 stub 会让两种不同状态无法区分。** 当你为一个带有
> *运行时*可用性问题的能力添加标签极性文件对时，请安装一个会失败的
> stub，而不是什么都不装。

### 有意从 profile 标签文件中缺失的那一个条目（The one entry deliberately missing from the profile tag files）

`tfogo_checklinkname0` 不出现在 `DEFAULT_BUILD_TAGS*` 中，尽管两个产品确实都传入
`-checklinkname=0`。原因是结构性的：那些文件也会被 `go build ./...` 与
`go test ./...` 在这些标签下读取，而 `cmd/go` 无法对单个包施加 `-ldflags` —— 并且在
该标签集合下，`experimental/libbox` 对运行时内部符号的 `//go:linkname` 拉取会停止链接。**该标签
改为随 flag 一起传递：** libbox 产物由 `cmd/internal/mobilebuildtags` 负责，daemon 由
`cmd/internal/build_boxdd` 负责。`TestProfileTagFilesDoNotClaimChecklinkname0` 要求
profile 标签文件**停止**声称它。

---

## 3. 发布 profile（Release profiles）

```text
release/DEFAULT_BUILD_TAGS         the DARWIN profile
release/DEFAULT_BUILD_TAGS_OTHERS  the same, MINUS with_naive_outbound
release/DEFAULT_BUILD_TAGS_WINDOWS the same as DEFAULT, PLUS with_purego
release/LDFLAGS                    -X runtime.godebugDefault=multipathtcp=0,tlssha1=1 -checklinkname=0
release/JIEJIE_VERSION             0.1.31 at cb71cf80d72; re-read for another target
```

共同内容：`with_quic, with_dhcp, with_wireguard, with_utls, with_acme, with_clash_api,
with_tailscale, with_ccm, with_ocm, with_cloudflared, with_usbip, with_openvpn, with_openconnect,
with_xhttp, badlinkname`。

> Darwin 与 OTHERS 的标签文件差别在 `with_naive_outbound`；单独的 Windows 文件在 Darwin 列表上
> 添加了 `with_purego`。
> `docs/BUILD-PROFILES.md:43-44` 说*客户端* profile “添加了 `with_utls`” —— **这是错误的**；
> `with_utls` 在两个文件中都有。

> `DEFAULT_BUILD_TAGS` 尽管名字如此，却是 Darwin profile。在当前 Cronet fork 固定值处，
> `cronet-go/all` 也有 Linux 文件与 Windows 文件；其 Windows amd64/arm64 文件要求
> `with_purego`。Windows profile 显式添加了该标签。请使用目标的实际 profile 与
> 工具链；OTHERS 省略 Naive 出站。本地替换为确切固定版本的 Cronet 源码在 Windows 上通过了
> Naive purego 单元测试，但这并不证明原生库加载或发布构建。

### 一条声称某已发布能力被删除的过期注释（A stale comment that claims a shipped capability was deleted）

`scripts/ci/build-macos-client.sh:19-22` 说：

> *"The single deviation from upstream's tag file is the removal of `with_clash_api`, which no longer
> names any file: the Clash API was deleted from this fork as a control-plane decision."*

（中文：与上游标签文件的唯一偏离是移除该标签，它不再指向任何文件：Clash API 已作为一个
控制面决策从此 fork 中删除。）

**在发布基线上，这三个子句全都是假的。** 该脚本逐字读取
`release/DEFAULT_BUILD_TAGS`（`:44`、`:49`），而 `with_clash_api` **确实**在该文件中；
`include/clashapi.go:1` 与 `include/clashapi_stub.go:1` 是指向它的标签极性文件；并且
`server-linux-amd64.yml:350-449` 会实时探测 Clash API。

`build-server.sh:16-29` 中同样的主张被**就地显式撤回**，并附有四条反驳；
同一改动的 macOS 那一半从未被更正。**这是该子系统中最危险的过期注释，因为它告诉读者
一个已发布的能力已经消失。**

Clash API 的真实历史是一次移除随后一次**恢复**：`bbfc31582`（−1908 行）→
`968395304`（+2254 行）→ `76cd68eaf`、`332e20194`。它确实存在，且 `with_clash_api` 在每个
profile 中。

两个标签文件的内容都是**上游的**，不是 fork 发明的；fork 的新增内容是
`badlinkname` 处理、`JIEJIE_VERSION` 以及 Apple 固定值元数据。

### 能力边界是上游的（The capability boundary is upstream's）

当前树使用通用的 `include` 注册表，而不是一个单独的产品专用
允许列表。已注册的能力仍然取决于构建标签、平台约束以及
实际并入此 fork 的上游改动。Darwin/OTHERS 的实际标签差异是：

* **服务器** profile 省略 `with_naive_outbound`，因为 Cronet 出站是一项客户端
  能力。**Naive inbound 在两者中都存在** —— 生产配置声明了一个。
* **客户端** profile 添加 `with_naive_outbound`；`with_utls` 在两者中都有。它**不**添加 `with_gvisor`：
  上游在迁移到进程内 Go TUN 栈时就让该标签退役了。
  `scripts/ci/build-macos-client.sh` **双向**断言这一点 —— 必需标签存在、
  退役标签不再出现。

`with_clash_api` 在两个 profile 中都启用。Clash API 与原生 `api` 服务是**同一个**实例之上的两个
管理表面：它们共享流量管理器与路由模式管理器，
因此通过原生 API 的 `SetClashMode` 设置的模式就是 Clash API 报告的模式。

### 注册表变更与上游同步（Registry changes and upstream synchronization）

一个上游新增内容只有在相关上游改动被并入、并且所选目标 profile 构建并注册它之后，
才会在这里可用。没有单独的产品允许列表，意味着没有
自动同步，也没有普遍的产物覆盖。`verify-full-capabilities.sh` 通过一个已构建的二进制
检验它自己命名的配置用例；在声称某个特定产物支持某项能力之前，请检查该脚本与实际运行，
以确定实际覆盖。

### 版本字符串中的一个陷阱（A trap in the version string）

`cmd/internal/build_shared.ReadTag()` 的 `currentTagRev[1:]` 会对不以 `v` 开头的标签做错误的切片：
`tooling-v0.1.5` 变成 `ooling-v0.1.5`，而版本字符串退化为 `0.0.0-v0.1-<sha>`。
**`0.0.0-v0.1-<sha>` 意味着“由该 sha 构建”，而不是“安装了错误的核心”。**
`ReadTag()` 会先读取 `SING_BOX_BUILD_VERSION`。

---

## 4. 生成器与构建器 —— `cmd/internal/*`（Generators and builders — `cmd/internal/*`）

| 类别 | 条目 |
|---|---|
| **写入树内的生成器** | `format_docs`、`protogen`（重新生成 `*.pb.go`）、`update_android_version`、`update_apple_version`、`update_desktop_version`、`update_certificates`、`read_tag`、`schema`（经由 `schema/generate.go`） |
| **构建驱动** | `build`、`build_boxdd`、`build_libbox`、`merge_aar`、`merge_apple_xcframework`、`app_store_connect` |
| **标签定义 —— 共享的单一事实来源，无 `main.go`** | `applebuildtags`、`mobilebuildtags`、`modlayout`、`build_shared` |

`appletags` **确实**有一个 `main.go`，其文档注释说明了原因：低内存 CI 门禁与
内存基准此前读取 `DEFAULT_BUILD_TAGS_OTHERS`，而它*"does not match what libbox
builds"*（中文：与 libbox 构建的内容不匹配），因此那些检查*"were testing a configuration no Apple binary ships"*
（中文：在测试一个没有任何 Apple 二进制会发布的配置）。几何标签
现在从一个地方读取：`go run ./cmd/internal/appletags -mobile-only`。

`applebuildtags/tags.go:177` 保留了一个未使用的 `bindTarget` 参数，“for signature stability”
（中文：为了签名稳定性）。

`cmd/internal/build_libbox/main.go:175-176` 输出一个能力清单，以便 CI 能断言
“能力蕴含匹配的标签”；`TestNoShippedVariantShipsGVisor` 拒绝携带 gVisor 的变体。

**重新生成命令：** `Makefile:57 proto`（daemon protobuf）与 `Makefile:35 schema`
（`docs/schema.json`）。`//go:generate` 指令很少 —— 整棵树中只有三条真实指令
（`common/schannel/syscall_windows.go:8`、`constant/goos/goos.go:12`、
`service/powerreport/syscall_windows.go:3`；第四处文本命中是测试中被引用的散文），其中
实质的一条是 `constant/goos/goos.go:12`（`go run gengoos.go` → `constant/goos/zgoos_*.go`，16 个文件）。

`build_libbox` 必须显式设置 `Dir`：`gomobile` 相对于进程 CWD 写入，且**没有 flag
也没有环境变量覆盖**（`gomobile@v0.1.12 cmd/gomobile/bind_iosapp.go:106`、
`bind_androidapp.go:373`）。

---

## 5. 固定的依赖树 —— 前缀问题，而非列表问题（The pinned dependency tree — a prefix problem, not a list problem）

`.gitmodules` 声明三个子模块：

| 路径 | 仓库 |
|---|---|
| `clients/apple` | `Piggy-Cat-bit-shadow/sing-box-for-apple` |
| `clients/android` | `Piggy-Cat-bit-shadow/sing-box-for-android` |
| `clients/desktop` | **`Piggy-Cat-bit-shadow/sing-box-for-desktop`**（fork 的） |

它们在 `a267e99` 处的 gitlink 为：apple `d0524912…`、android `ec61030d…`、desktop `32f915ba…`。它们的
内容**不**在一个干净的工作树中，这就是为什么 `Makefile:94` 会 shell 进入
`../sing-box-for-android`，也是 Android 客户端无法从此工作树构建的原因。

**`cronet-go` 是一棵模块树，而不是一个模块。** 一条 `replace` 指令只重写**一个模块
路径**，因此只固定根会让每个嵌套模块 —— `/all` 与 29 个按平台的 `lib/<os>_<arch>`
路径 —— 解析到上游：一个没有任何东西构建或测试过的配对，而在 linux/amd64 上是一个无法
链接的配对。因此，`scripts/ci/check-go-module-integrity.sh` 的平行规则是一条**前缀规则**，从
根 `go.mod` 推导而来，而不是来自静态列表，因此一个新的平台路径会强制要求一个固定值，而不是
被漏掉。`REQUIRED_FORK_REPLACES`（`:105-115`）要求四个：`sing`、`sing-tun`、`quic-go`、
`sing-quic`。

> **`docs/FORK-DIFF.md:27` 已在 2026-10-28 被更正，以列出全部五个被替换的模块路径以及
> `cronet-go` 前缀树**；在此之前它只概括了三个。守卫仍然比概括更具权威性：
> 它不是从该文档推导出来的，并且当根不再替换某个受守卫路径时它会失败。

同一脚本的历史值得了解：`check-go-module-integrity.sh:60-66` 记录 `test` 模块曾经静默链接到
**上游 sing-tun**，且*"the old code could not fail on it, by construction"*（中文：按构造，旧代码
无法在这一点上失败）。**本仓库有三个 Go 模块** —— 根、`test/` 与
`test/jiejie/reference/` —— 而从根运行的 `go test ./...` 只覆盖第一个。

在当前测试目标下，`sing-anytls`、`tailscale` 与 `sing-cloudflared` 在 `go.mod` 中没有 fork
替换，而 `sing-tun` **确实**通过 fork 替换解析。请始终检查实际的 `replace` 指令，
而不是从 `require` 块推断。`gVisor` 在历史发布基线上仍以 `// indirect` 留在 `go.mod` 中，并且
`protocol/tun/inbound.go`、`cmd/internal/build_libbox` 与 `route/dns.go` 仍在构建标签下引用它。

> **“gVisor 退役”意味着 Go TUN 栈成为默认，且强制 gVisor 的 Android 被移除 —— 而 NOT
> 意味着 gVisor 离开了模块图。** 旧的 `replace` 是一条**有意**保留的注释
> （`go.mod:367-389`）。

---

## 6. 打包表面（Packaging surfaces）

`Dockerfile`、`Dockerfile.binary`、`.fpm_systemd`、`.fpm_openwrt`、`.fpm_pacman`、
`.github/build_{alpine_apk,openwrt_apk}.sh`、`.github/deb2ipk.sh`、`.github/detect_track.sh`。

在发布基线上存在两个 Windows 工作流，而它们是**不同的产品**：
`windows-core-amd64.yml`（“Windows amd64 core”）为外部加载器构建 `sing-box.exe`，而
`client-desktop-windows.yml`（“Windows desktop installer”）构建并签名 NSIS 安装器，带有
daemon SCM 服务生命周期。桌面基线在 `7a46125ded0` 处被**逐字恢复**
（“restore(windows): recover the retired desktop installer baseline verbatim”，2026-10-10）。**不要
在不指明你指哪个工作流的情况下把此 fork 描述为“仅核心、不含桌面安装器”。**

Windows 签名是一道**硬性安装门禁**：`WinVerifyTrustEx` 加上 `secureWindowsInstallation` 在不匹配时
拒绝服务注册。恰好允许**两**个 i386 PE 例外
（`Jiejiebox-v*-windows-x64.exe`、`resources/elevate.exe`）。

---

## 7. 证据、来源与注意事项（Evidence, provenance, cautions）

### 证据（Evidence）

| 保证 | 级别 | 复检 |
|---|---|---|
| 无标签的 `go build ./...` | **`VERIFIED_UNIT`**（在此运行） | `go build ./...` |
| `go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./...` | **`VERIFIED_UNIT`**（在此运行） | 如上所写 |
| `go build -tags "$(cat release/DEFAULT_BUILD_TAGS)" ./...` | 在较旧固定值/profile 上的历史 Windows 失败；不是当前平台的裁决 | 检查实际的平台 profile 与 Cronet 固定值；Windows Cronet 文件现在要求 `with_purego` |
| 标签组合与 linkname 契约 | `VERIFIED_UNIT` | `go test -count=1 ./cmd/internal/...` |
| 嵌套模块的替换平行性 | `check-go-module-integrity.sh --require-checked test` 在本次校准中于 MSYS2 bash 下对三个被发现的模块离线通过；没有 `go mod verify` 或产物来源 | 该脚本 |
| `verify-full-capabilities.sh` 对某个已发布二进制通过 | **仅声明** | 需要一个已构建产物 + bash |
| 可复现性（构建两次，比较 sha256） | **仅声明**，且仅在 Linux runner 上的 `deep_checks` 下 | `server-linux-amd64.yml` 的 deep 路径 |
| Windows/Android/Apple 已构建产物的内容 | 不由这些本地检查确立；另行观测到的工作流运行在 `ci-release.md` 中按 SHA 限定 | 检查确切的工作流、运行与产物 |

### 来源（Provenance）

`badlinkname`、`with_xhttp`、`with_purego`、`tfogo_checklinkname0` 的处理与 profile 文件是
上游的结构；fork 的新增内容是 `JIEJIE_VERSION`、`apple-client-refs.env`、
`jiejie-production-topology.json`（一个完整的生产配置 fixture：六个 inbound —— `masque-h2`、
`masque-h3`、`anytls`、`naive`、`shadowtls`、`ss2022` —— 以及八条路由规则）、`cronet-provenance.txt`
（按大小与 sha256 记录的五个 `libcronet.a` 归档），以及
`check-go-module-integrity.sh` 中的可达性/幂等性工作。

一条值得了解的**已退役分支线**：一个更早的“裁剪架构”，带有 fork 专用
注册表与能力允许列表，被 `3bab501db`、`dcd8b10cd`、`4c39a835b`、`b175f04dd`
与 `523db47e4` 退役。**不再有任何 fork 专用注册表或能力允许列表** —— 不要从更旧的文档中
复活这个想法。

### AI 注意事项（AI cautions）

1. **不要通过编辑 map 来添加协议。** 没有 map；请编辑注册表函数。
2. **不要移除注册表墓志铭。** 它们承载着特定的弃用消息。
3. **不要在“缺失”与“损坏”必须不同的地方，让 stub 把某项能力留为 nil。**
4. **不要把 `tfogo_checklinkname0` 放回 profile 标签文件。** 它破坏了裸 `go build`。
5. **不要假设 Darwin 标签文件会为另一个平台选出正确的 profile。**
6. **不要因为名字就假设 `DEFAULT_BUILD_TAGS` 是通用默认值。**
7. **不要添加 `with_gvisor`。** 它已退役，且构建脚本会断言它不存在。
8. **不要只固定一个 `cronet-go` 模块路径就称之为已固定。** 它是一棵前缀树。
9. **不要相信一份固定依赖的概括列表胜过 `check-go-module-integrity.sh` 的
   `REQUIRED_FORK_REPLACES`。**
10. **不要忘记有三个 Go 模块。** 从根运行的 `go test ./...` 只覆盖其中一个。
11. **不要写一个依赖进程 CWD 来决定 `gomobile` 输出的构建脚本。**
12. **不要把 `0.0.0-v0.1-<sha>` 读成版本错误。** 它意味着“由该 sha 构建”。
13. **不要在你的描述中把两个 Windows 产品合并。**
14. **不要现在就把 Windows 桌面安装器诊断为文件投放。** 当前的安装器
    工作位于另一条分支上；在发布基线上，使用任何 diff 前请先验证。
