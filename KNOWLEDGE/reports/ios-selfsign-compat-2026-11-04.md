# iOS 自签兼容：从样本分析到打通全链路

记录 2026-11-04 这一天里，一个第三方重签（自签）的 iOS 构建从「装得上、打不开」到
「能装、能开、VPN 能连、日志与统计能读」的全过程。写下来的目的不是复盘一次修 bug，而是把
**五次真机失败背后反复出现的同类错误**固定成规则，避免下一次再拿真机去试本可以在本机发现的问题。

> 证据等级沿用知识库约定：`已实测`（本机或 CI 有输出）、`源码级`（读代码可确认，未运行）、
> `高置信推断`（有间接证据缺直接样本）、`NEEDS_DEVICE`（只能真机）。

---

## 1. 起点：先看清「能用的包」和「不能用的包」差在哪

第一批材料是两个 IPA：

| 样本 | 结果 | 关键特征 |
|---|---|---|
| `singer-0.1.0-bare.ipa` | 用户真机能装能开 | `app.singer.ios`，0.1.0，**1 个**扩展（Packet Tunnel），**无内嵌框架** |
| `jiejiebox-…-unsigned-LC.ipa` | 装得上，打不开 | `io.nekohasekai.sfamt`，**1.14.8 (10)**，**5 个**扩展，`Library.framework` 86 MB |

前者是「最小自签样本」，后者是完整的 JiejieBox。两者不可直接类比：样本没有框架、没有 App Group
依赖、扩展也只有一个。**但审计确实发现了一个真缺陷**：包内 `BUILD-INFO-IOS.txt` 写着
`version: 0.1.37`，而 `CFBundleShortVersionString` 是 `1.14.8`、`CFBundleVersion` 是 `10`
—— 无签名构建路径没把 `MARKETING_VERSION`/`CURRENT_PROJECT_VERSION`/`BASE_PACKAGE_IDENTIFIER`
传给 xcodebuild，于是包内还是上游的身份与版本。

**经验 1：拿「能用的包」当基线之前，先比对两者的结构差异（扩展数、框架、身份），否则会把
结构差异误读成签名工具的差异。**

---

## 2. 第一次真机失败：装得上，点开就闪退

### 机制

`FilePath.defaultSharedDirectory` 是 `URL!`：

```swift
private static let defaultSharedDirectory: URL! = FileManager.default.containerURL(...)
public static let sharedDirectory = defaultSharedDirectory!
```

`containerURL(forSecurityApplicationGroupIdentifier:)` 只在进程签名**携带包含该组的
`application-groups` 权限**时返回非 nil。重签后不授予，就返回 nil；App 启动时第一次访问
`sharedDirectory`（打开配置数据库）即 trap。这不是「签名失败」，是**代码对缺失权限没有防御**。

### 修复

- `FilePath.resolveSharedDirectory()`：组不可用时回退到进程私有目录，并置
  `isUsingProcessLocalDirectory`，让「降级」成为可诊断的事实而不是进程死亡；
- `AppConfiguration` 去掉针对隧道 id 的 `fatalError`，改为枚举内嵌 `PlugIns` 发现真实的
  packet-tunnel 标识（重签工具会重写 bundle id，但不会重写自定义 plist 键）；
- `FileProviderExtension` 去掉强解包。

### 证据与边界

真机确认「能打开」。崩溃报告（`.ips`）始终没有拿到，所以「强解包导致首次访问 trap」是
**高置信推断**，不是已证。

**经验 2：`URL!` + 平台可选权限 = 定时炸弹。任何「签名决定其存在性」的资源都必须有回退路径，
并把回退状态暴露出来。**

---

## 3. 第二次失败：`command.sock` 绑定失败（`bind: invalid argument`）

```text
Failed to start service (packet-tunnel): start command server error:
listen unix /var/mobile/Containers/Data/PluginKitPlugin/<UUID>/Library/Application Support/JiejieBox-Shared/command.sock:
bind: invalid argument
```

根因：Darwin `sockaddr_un.sun_path` 是 `char[104]`，`bind(2)` 收 C 字符串，**可用 103 字节**；
这条路径 138 字节。超长时内核返回 `EINVAL`，读起来像「地址格式错误」，极具误导性。

### 第一版修复（后来证明不充分）

路径过长就换到 `os.TempDir()`：

```go
if len(path) <= 103 { return path }
return filepath.Join(os.TempDir(), fmt.Sprintf("jjb-%08x.sock", crc32.ChecksumIEEE([]byte(path))))
```

5 个测试通过。**但测试是在开发机上跑的，而开发机的临时目录很短。**

**经验 3：用「另一个目录」绕开长度限制，只是把一个假设换成了另一个假设。**
**经验 4：测试通过不代表产品路径被覆盖——先问「我的测试喂的输入，和产品运行时拿到的一样吗」。**

---

## 4. 第三次失败：备用目录自己也超了（110 字节）

```text
socket path is 110 bytes, the kernel allows 103:
/private/var/mobile/Containers/Data/PluginKitPlugin/<UUID>/tmp/jjb-13de0c06.sock
```

iOS Packet Tunnel 的 `TMPDIR` **就是容器自己的 tmp**（92 字节），加 17 字节文件名即 110。

### 第二版修复

- 候选目录：basePath → 平台 temp → 其父目录；
- 名字按预算收缩；
- 诊断报「最短候选」。

仍然错了——下一节说明为什么。

**经验 5：iOS 沙盒里没有「短的公共目录」。进程能写的每个目录都在容器深处，
所以「路径长度」必须当作**预算问题**处理，而不是「选哪个目录」。**

---

## 5. 第四次失败：系统临时目录根本没进候选

```text
command server: no bindable socket path within 103 bytes;
3 candidate directories, the shortest leaves -31 bytes for a name
```

代码：

```go
if tempDir != "" {
    add(tempDir)
} else {
    add(os.TempDir())     // ← 只有 tempDir 为空才用
}
```

Apple 客户端传的是 `options.tempPath = FilePath.cacheDirectory.relativePath`
= `<shared>/Library/Caches`（148 字节）。于是三个候选是 133 / 148 / 133（去重后两个），
唯一短的 `TMPDIR`（92）从未参与。

另外两个缺陷：
- 错误信息取 `candidates[len-1]` 当「最短」，`-31` 不是真正最紧的预算；
- 测试直接把开发机 `/tmp` 当平台 temp 传入，**测的是一条设备上不存在的调用**。

### 第三版修复（最终形态）

- `os.TempDir()` **无条件**是候选；容器根由它推导，而不是由调用方传的路径推导；
- 平台 `tempPath` 及其父目录仍保留（macOS 上是真临时目录）；
- 生成名去掉 `.sock` 后缀（容器里只剩 10 字节，后缀白占 5 个）：`h` + 4–8 位十六进制；
- 诊断报**真正最短**候选的预算；
- 服务端与客户端调用同一个函数，且系统临时目录作为**参数**注入
  （`resolveCommandSocketPathIn(basePath, tempDir, sysTemp)`），测试才能建模设备而不是开发机。

真机布局的答案：`<container>/tmp`（92）+ `/` + `h91d35256`（9）= **102 字节**。

### 这一轮的证据

- 单测以真机三条路径为常量（base 133 / Caches 148 / TMPDIR 92），断言落在 `<container>/tmp`、
  恰好 102 字节；
- 边界 103 可用、104 不可用；多字节目录按字节算；
- **Darwin 真 bind**（macOS runner）：

```text
PASS TestDarwinBindsTheResolvedPathInADeepContainer
PASS TestDarwinBindsWithoutThePlatformTempArgument
PASS TestDarwinRejectsAPathOverTheLimit
```

**经验 6：只有内核能回答「这个路径能不能 bind」。算术测试证明的是算术。**
**经验 7：每个修复都要写下「修复前的失败输出」，否则下一轮无法证明自己修的是同一个东西。**

---

## 6. 一个自己造成的坑：修了，但没进包

工作流里 `APPLE_EXPERIMENT_SHA` 是常量，父仓库 pin 更新后它没跟着动。于是**构建成功、包也上传了，
但包里是旧客户端**——诊断代码根本不在 IPA 里（用二进制字符串核验才发现）。

修复：pin 与工作流常量一起移动，并加断言——**两者不一致时构建直接失败**：

```yaml
- name: the experiment builds the client this commit pins
  run: |
    pinned="$(git ls-tree HEAD clients/apple | awk '{print $3}')"
    [ "$pinned" = "$APPLE_EXPERIMENT_SHA" ]
```

**经验 8：产物溯源要能回答三件事——父仓库 SHA、实际客户端 SHA、二进制里到底有没有这段代码。
前两个看 BUILD-INFO，第三个只能查二进制字符串。**

---

## 7. 第五次：VPN 连上，但日志与统计全空

现象：首页显示已连接，网站能打开；日志页写「服务未启动」，统计是省略号。

### 第一条误导性信息（源码级确认）

日志页在命令客户端连不上时直接显示「Service not started」——**它从未检查内核是否运行**。
这句话把排查方向带偏了两轮。修复：无共享容器时改为「状态通道不可用」，并说明 VPN 仍可能已连接。

**经验 9：状态文案必须是「我实际检查了什么」的陈述。**「服务未启动」是一种结论，
而代码只观察到「客户端连不上」。

### 真正的根因：组名不匹配（用户签名实测）

| 检查项 | 实际值 |
|---|---|
| 程序请求的 App Group | `group.io.jiejiebox.app`（Info.plist，构建时固定） |
| 签名与描述文件授权 | `group.<team-hash>.1` … `.5` |
| 是否匹配 | **否** |
| Packet Tunnel 权限 | 有 |

重签工具替换 entitlements 与描述文件，但**不会改自定义 Info.plist 键**。于是
`containerURL` 返回 nil → 两个进程各自回退私有目录 → 扩展的 `command.sock` 在扩展容器里，
App 在 App 容器里找 → 连不上 → 日志、流量、内存、协程、连接列表全部读不到。
**VPN 本身是好的**，坏的只是状态通道。

### 修复：运行时以描述文件为准

`FilePath` 不再只认一个写死的组名：

1. 先试 Info.plist 里的组名（正常签名照旧）；
2. 不行则读**内嵌描述文件**里所有 `group.*` 字符串（appex 自身没有描述文件时向上找 App 的），
   逐个用 `containerURL(forSecurityApplicationGroupIdentifier:)` 探测；
3. 第一个能打开的即为共享容器；
4. App 与扩展读同一份描述文件、同样顺序 → 各自独立得出同一个容器，**不需要手工映射，
   也不需要新建 IPC 通道**；
5. macOS 无 embedded profile，行为不变。

**经验 10：签名相关的「声明」和「授权」是两回事。构建期写进 plist 的是声明，
描述文件/entitlements 才是授权；运行时必须以授权为准。**

**经验 11：跨进程共享失败时，先确认「两个进程是否真的在同一个容器」，
再去查协议、路径、鉴权——顺序反了会浪费很多轮。**

---

## 8. 附带修复：让「连上但不接管」不再静默

隧道可能连上却没有任何流量（配置里没有 tun 入站、或 TUN 打不开）。这种情况以前完全静默：
NE 状态是「已连接」，`startOrReloadService` 没抛错，App 又读不到扩展日志。

现在扩展在首次启动后最多等 3 秒检查 `setTunnelNetworkSettings` 是否真的被调用过；
没有就抛一个 iOS 会展示的错误，并说明配置里是否出现过 tun 入站。

**经验 12：可观测性要建在「无法表态」的地方。** 一个既不报错又不工作的状态，是最贵的状态。

---

## 9. 关键阻塞一览

| 轮次 | 真机现象 | 判定依据 | 修复层 |
|---|---|---|---|
| 1 | 装得上、点开闪退 | 源码 + 高置信推断（无 `.ips`） | Apple 启动路径、App Group 回退 |
| 2 | `bind: invalid argument`（138 B） | 真机错误原文 + sun_path 语义 | libbox 路径选择（第一版） |
| 3 | `socket path is 110 bytes`（110 B） | 真机错误原文 | libbox 路径预算（第二版） |
| 4 | `no bindable socket path…`（-31 B） | 真机错误原文 + 调用方传参 | libbox 候选集（第三版）+ 测试方法 |
| 5 | 连上、网站能开、日志统计空 | 源码 + 用户签名权限实测 | App Group 发现 + 日志文案 |

其中**第 4 轮是本可避免的**：第 3 轮的错误信息里已经给出了 92 字节的 `TMPDIR` 事实，
但第二版实现没有把「系统临时目录」纳入候选。教训是：**报错里已经给出的事实，必须在下一版实现里被使用。**

---

## 10. 经验汇总（按可用性排序）

1. **测试必须复现真实调用参数。** 把「平台给的输入」变成函数参数，测试才能注入设备值；
   否则绿只是绿在开发机上。
2. **长度/权限/容器这类平台约束，只有平台能判定。** 路径长度用 Darwin `bind` 验，权限用
   `containerURL` 探测，不要用算术和推断代替。
3. **每一次「修复」都要问：我换掉的是假设还是根因。** 换目录、换路径、换名字都可能是换假设。
4. **保留修复前的失败输出。** 否则无法证明修复的是同一件事，也无法在下一次复发时快速定位。
5. **构建产物要能自证。** 父 SHA、客户端 SHA、二进制标记三者对齐；pin 与 workflow 常量必须一起动。
6. **状态文案是契约。** 「服务未启动」这种结论性文案必须是检查过的结论，否则会误导排查方向。
7. **跨进程问题先看容器，再看协议。**
8. **可观测性优先覆盖静默失败**（连上但不工作、启动但没接管）。
9. **签名授权 ≠ plist 声明**，运行时以描述文件为准。
10. **不要用「另一个样本能跑」来类比**结构不同的包；先比结构，再比签名。

---

## 11. 残余风险与未验证项

- **代理流量是否真的经过隧道**：用户报告自签成功后可用；「网站能打开」本身不构成走代理的证据，
  严格验证需要看连接列表或出口 IP。
- **无 App Group 且描述文件被剥离**：仍会回退私有目录；此时界面会明确显示「状态通道不可用」。
- **macOS 自签**：本轮只覆盖 iOS；macOS System Extension 的 XPC 仍按 `basePath/command.sock`
  取路径，路径足够短时一致，未做超长验证。
- **压力/长稳**：未做；本轮所有结论都来自单测、Darwin 真 bind、构建产物核验与用户真机反馈。

---

## 12. 代码与产物索引

| 内容 | 位置 |
|---|---|
| socket 路径解析与预算 | `experimental/libbox/command_socket.go` |
| 单测（真机路径常量） | `experimental/libbox/command_socket_test.go` |
| Darwin 真 bind | `experimental/libbox/command_socket_darwin_test.go` |
| 命令服务端/客户端接入 | `experimental/libbox/command_server.go`、`command_client.go` |
| App Group 发现 | `clients/apple/Library/Shared/FilePath.swift` |
| 启动回退与隧道 id | `clients/apple/Library/Shared/AppConfiguration.swift` |
| 接管检查 | `clients/apple/Library/Network/ExtensionProvider.swift`、`ExtensionPlatformInterface.swift` |
| 日志页文案 | `clients/apple/ApplicationLibrary/Views/HakoStyle/HakoLogView.swift`、`Views/Log/LogView.swift` |
| 实验构建工作流 | `.github/workflows/experiment-ios-selfsign.yml` |
| 早前两份专项记录 | `docs/fork/ios-command-socket-path-fix.md`、`docs/fork/ios-selfsign-experiment.md` |
