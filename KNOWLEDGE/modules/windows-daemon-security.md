# Windows 守护进程与守护进程信任边界（Windows daemon and daemon trust boundary）

## 基线后复核与运行证据（Post-baseline review and run evidence）(2026-10-28)

在 `cb71cf80d72`，[Windows 守护进程测试 40605271303](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40605271303)
报告了 33 个测试且无跳过，其日志中包含 `TestTamperedSignedPEIsRejectedByTheFirstLayer`。
这是针对该修订与该命名测试套件的 CI 证据，不是端到端的已安装服务握手，
也不构成对其他二进制文件与平台的保证。

在 `011f3fdcd99`，`peer_windows.go` 仍认证 worker 与应用的身份；`authenticode_windows.go` 只允许枚举出的
链/时间错误，然后分别检查签名者与证书约束。`server.go` 在所有者授权之前先规范化 gRPC 服务方法中的
一个前导斜杠；`owner_gate_spelling_test.go` 检查两种拼写。卸载必须先停止服务再删除它
（`cmd_service_windows.go` 与 `cmd_service_uninstall_contract_test.go`）。这些在目标处为 `VERIFIED_SOURCE`。

GitHub Actions 的 [run 40535897659](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40535897659)
在已发布的提交 `6112aa17052297ec9890e0e7f1541a06120b84b2` 上执行；其 Windows daemon 作业成功，并报告
33 个测试、无跳过，包括被篡改 PE 测试（该提交处为 `VERIFIED_CI`）。从该发布到目标之间，本包及其工作流
都没有变化；`go.mod` 与 `go.sum` 确有变化，因此这不等价于在目标上运行，也不能证明端到端服务握手。
在 Windows 10 build 19044 本地，`go test ./experimental/boxdd -count=1 -v` 成功退出，但被篡改 PE 测试
因 Cert 驱动器不可用而跳过：跳过不是通过。

**范围：** `experimental/boxdd/**`（55 个 `.go` 文件——其中 51 个非测试——外加 `desktop_service.proto`），
它定义的进程角色，以及它们之间的本地信任边界。覆盖 Windows 服务、每用户 worker、命名管道对端握手、
可执行文件/对端认证、Authenticode 语义以及更新路径。`daemon/**` 由 `libbox-platform-contract.md` 覆盖；
本模块只拥有**边界**。

**在以下情况加载本模块：** 改动 `experimental/boxdd/**` 中的任何内容、Windows 服务或安装器路径、
对端认证、签名者处理，或在断言 Windows 守护进程验证或不验证什么之前。

**证据：** 除非另有标注，每个断言相对于 `v0.1.9` 基线都是 `VERIFIED_SOURCE`。
下文 v0.1.9 的断言不携带 CI 运行证据；参见上文的后续运行。本模块没有运行 Windows 端到端
运行时测试；参见 `## Known limitations`。

---

## 1. 当前设计（Current design）

`sing-box-daemon` 是一个二进制文件，带四个命令（`main.go:17`、`:22`、`cmd_run.go:24`、
`cmd_service.go:15`、`cmd_worker.go:30`）：

```text
sing-box-daemon run                    the daemon itself (foreground, or as a service)
sing-box-daemon service start|stop|status        cmd_service.go:64-66
sing-box-daemon service install|uninstall        cmd_service_windows.go:77-78   Windows only
sing-box-daemon service set-insecure-mode <b>    cmd_service_windows.go:59 / cmd_service_linux.go:35
sing-box-daemon worker                 the per-user relay              cmd_worker.go:45
sing-box-daemon version                main.go:35
```

**Windows 上没有独立的 core 进程。** 守护进程通过 `daemon.StartedService`（`server.go:240` →
`daemon/instance.go:85-153`）**在进程内**托管 `box.Box`。`worker` 是由 Electron 应用生成的每用户进程；
它提供 `ApplicationService` 并中继守护进程连接。**被认证的对端身份是 APPLICATION（应用），不是
worker**（`peer_windows.go:237`）——这一区别对 §4 中的每一项检查都很重要。

本包对端与服务行为有平台特定实现。Linux 并非未认证：`peer_linux.go` 使用 Unix `SO_PEERCRED` 与 `/proc`
进程启动时间识别本地对端，然后应用通用的所有者授权。这与 Windows 的已安装文件与签名者检查是不同的
边界；`--listen` 走单独的兜底路径。本模块下文的详细握手分析是 Windows 专用的，
不建立 Linux 设备/运行时覆盖。

## 2. 进程 / 服务 / worker 关系（Process / service / worker relationship）

```text
Windows SCM
    └─ sing-box-daemon run            LocalSystem, session 0
           ├─ hosts box.Box in-process
           ├─ listens on the named pipe
           └─ peer_windows.go decides who may connect

Electron application (per-user, interactive session)
    └─ sing-box-daemon worker         spawned by the application, per-user
           └─ connects to the daemon's pipe on the application's behalf
```

服务入口是 `cmd_run_windows.go:26`（`svc.Run(serviceName, &windowsService{})`），`:54` 处的 `Execute`
处理 `svc.ChangeRequest`，包括会话变更。在服务模式下 `--listen` 被**拒绝**（`cmd_run_windows.go:56-58`）。

## 3. 命名管道信任边界（Named Pipe trust boundary）

**管道 ACL 不是门禁。** `server_windows.go:15` 向 `Authenticated Users` 授予 `GRGW`，因此任何已认证的本地
用户都能*打开*该管道。保护分为两层：管道名位于 `ProtectedPrefix\Administrators` 之下（因此只有管理员
才能创建竞争实例），以及由**握手**决定是否接受该连接。在声称“只有 worker 能连接”之前，请先读这一节。

## 4. 可执行文件 / 对端认证——按顺序的握手（Executable / peer authentication — the handshake, in order）

`peer_windows.go:83-237`。每一行都是一项真实检查，带有真实的失败字符串。

| # | 检查 | 证明 | 行号 |
|---|---|---|---|
| 1 | 端点是 Windows 命名管道 | 不是 TCP 冒名者 | `:86` |
| 2 | 客户端进程映像路径 == `expectedWorkerPath`（不区分大小写） | 已安装的 worker | `:125` |
| 3 | `sameWindowsFile(processImage, daemonExecutable)` | 它是*同一个文件*，不是副本 | `:128` |
| 4 | `authenticodeSigner(worker)` == `daemonSigner` | **签名者字节相等** | `:139` |
| 5 | `validateWorkerProcessRole`——命令行、私有管道路径、父 PID | 它是 worker，不是仿冒者 | `:360-379` |
| 6 | `processIdentity`——令牌用户 SID、交互式会话 | 是哪个用户在连接 | `:313-334` |
| 7 | worker 与应用共享同一进程身份 | 同一用户的一对进程，不是混搭 | `:165` |
| 8 | 父进程创建时间 < worker 创建时间 | 观测到的时间戳满足预期顺序；仅此检查本身并不证明父子关系 | `:175` |
| 9 | 父进程映像路径 == `expectedApplicationPath` | 已安装的应用 | `:205` |
| 10 | `sameWindowsFile(parentProcessImage, expectedApplication)` | 同一文件，不是副本 | `:208` |
| 11 | `validateApplicationProcessRole`——不是 Electron 子进程，父进程即应用 | 看角色，不只是路径 | `:339-355` |
| 12 | `authenticodeSigner(application)` == `daemonSigner` | **再次签名者字节相等** | `:223` |
| 13 | 两个进程在认证期间都没有退出 | 拒绝在检查时被观测到已退出的进程；并不证明消除了所有可能的竞态 | `:235` |

有两个特性使它稳健，而不是一份清单：

* **`openLockedExecutable` 使用 `FILE_SHARE_READ`**（`:466`），因此写入者在映像被检查期间无法重写它——这是反 TOCTOU 措施，不是便利性设计。
* **`finalWindowsPath` 通过已打开的句柄解析路径**（`:482`），因此被比较的路径是实际持有打开的文件的路径，而不是可能被竞态的字符串。

**`expectedApplicationPath` 派生自守护进程自身的路径**（`peer_windows.go:303-304`），这意味着该对端无法通过握手提名一个任意的预期应用路径。

## 5. Authenticode 语义（Authenticode semantics）

`authenticode_windows.go`。§5 与 §6 要一起读：这里涉及**三种不同的属性**，把它们混为一谈是最可能犯的错误。

```text
SIGNATURE INTEGRITY   WinVerifyTrustEx: does the signature match the bytes?     -> MUST hold
CHAIN TRUST           is the signer issued by a trusted CA?                     -> deliberately NOT required
SIGNER IDENTITY       is the signer byte-equal to the daemon's own?             -> MUST hold
```

### 被接受的签名者链条，按顺序（The accepted-signer chain, in order）

1. `WinVerifyTrustEx`（`:114`）——原始判定结果。
2. `isAllowedUntrustedAuthenticodeError`（`:78`，在 `:115` 应用）——**允许列表**（见下文）。
3. `verifiedSignerCertificate(trustData.StateData)`（`:120`）——提取签名者的 DER 字节。
4. `validateCodeSigningCertificate`（`:129`）——解析，并要求代码签名 EKU。
5. 仅在容忍错误的支路上：先 `reportAuthenticodeCertificateTime`（`:153`），再 `validateUntrustedSelfSignedCertificate`（`:154`）——自签名且自签名有效。

### 允许列表恰好是四个错误码加成功（The allowlist is exactly four codes plus success）

```text
err == nil                     accepted outright
CERT_E_UNTRUSTEDROOT     0x800B0109   tolerated
CERT_E_CHAINING          0x800B010A   tolerated
CERT_E_EXPIRED           0x800B0101   tolerated
CERT_E_VALIDITYPERIODNESTING 0x800B0102   tolerated
```

用 `E.IsMulti` → `errors.Is`（`sing common/exceptions/multi.go:60-67`）匹配，因此成员资格是**精确的**——没有区间，没有前缀。

**实测被拒绝的情况**（`TRUST_E_NOSIGNATURE` 0x800B0100、`TRUST_E_SUBJECT_FORM_UNKNOWN` 0x800B0003、
`TRUST_E_BAD_DIGEST` 0x80096010、`CERT_E_WRONG_USAGE` 0x800B0110、任何其他 errno、任何非 errno 错误）。
**被篡改的已签名 PE** 实测错误码是 `TRUST_E_BAD_DIGEST`；**未签名**文件是 `TRUST_E_SUBJECT_FORM_UNKNOWN`，
而未签名但格式良好的 PE 是 `TRUST_E_NOSIGNATURE`。

### 证书有效期不是门禁（Certificate validity period is NOT a gate）

`authenticodeCertificateTimeProblem`（`:197`）**返回一个描述字符串，从不返回错误**，而
`reportAuthenticodeCertificateTime`（`:166`）在有效时以 `Debug` 记录它，在已过期或尚未生效时以 `Warn` 记录。
**不存在从证书时间到失败的路径**，日志器也不能变成门禁：nil 日志器会提前返回（`:168`），
解析失败只发出警告（`:173`）。

> 这是有意为之的可用性/信任权衡：被容忍的信任错误要求有效的
> 自签名，而对端身份绑定到守护进程的签名者字节以及其他进程检查。
> 此前当 CI 证书老化时，过期会阻止产品启动。字节相等本身并不能应对
> 全部证书生命周期或密钥泄露风险；更改该策略需要显式的
> 威胁模型与部署评审。参见 §11。

### 未签名二进制文件永远无法通过——原因是结构性的（Unsigned binaries can never pass — and the reason is structural）

`authenticodeSigner` 有**六**条 `return nil, err` 类失败路径，以及恰好**一条**成功路径
`return signer, nil`（`:159`），后者只有在 `verifiedSignerCertificate` 成功之后才可达——
而该函数拒绝 nil/空的证书上下文（`:222-225`）。因此 nil 签名者**不可能**伴随 nil 错误。

这一点很重要，因为 `bytes.Equal(nil, nil)` 为 **true**。因此防御*不是*比较本身，而是顺序：
**每一个调用点在比较之前都先检查 `err`。** 不要通过添加 `len(signer) > 0` 守卫来“加固”它；
该不变量已经是结构性的，而守卫会暗示比较就是防御，事实并非如此。

## 6. 签名者 pin（The signer pin）

**一个 pin、六次比较，且参考字节始终来自守护进程自身已安装的可执行文件。**
`authenticodeSigner` 有九个调用点：

| 调用点 | 判定的身份 |
|---|---|
| `peer_windows.go:305` | **守护进程自身的签名者——所有其他比较的参考** |
| `peer_windows.go:135` | worker == 守护进程 |
| `peer_windows.go:219` | 应用 == 守护进程 |
| `cmd_worker_windows.go:143` | worker == worker 自身的路径 |
| `cmd_worker_windows.go:147` | 父应用 == 它自身的路径 |
| `cmd_worker_windows.go:396` | 被中继的对端 == 父进程签名者 |
| `security_windows.go:48` | 守护进程（安装时） |
| `security_windows.go:56` | 应用（安装时） |
| `update_windows.go:103` | 安装器 == 守护进程签名者 |

`peer_windows.go:305` 经由 `:63` → `platform_windows.go:63` → `cmd_worker_windows.go:177` 传播。
比较点：`peer_windows.go:139`、`:223`、`cmd_worker_windows.go:151`、`:400`、`security_windows.go:60`、
`update_windows.go:107`。

**实测：** 由两个确实有效且不同的自签名代码签名证书签名的两个二进制文件，产生的签名者负载分别为
**800** 和 **798** 字节，`bytes.Equal` 为 **false**——因此该 pin 能区分它们。同一证书比较为相等，
因此该 pin 并非空洞地为 false。

## 7. 服务安装（Service installation）

`secureWindowsInstallation`（`security_windows.go:29`），从 `cmd_service_windows.go:146` 到达。它检查已安装
路径的布局与祖先（所有者必须是 SYSTEM、Administrators 或 TrustedInstaller），这就是在用户拥有的目录下
安装会以 `code 1` 失败的原因（`docs/fork/WINDOWS-SFW-BUILD-RECORD.md` §P0-2）。

> **并非铁板一块——必须精确表述。** 签名者相等与布局检查是硬性的，
> 但卷/祖先/ACL 检查会被 `--allow-unsafe-installation-directory-permissions` 放宽，
> 而该标志**也会传播到服务命令行**，因此运行时复验同样被放宽。
> **“守护进程总是拒绝不安全的工作目录”是错的。** 若有模块这样写，
> 就会误述一个仍然存在的逃生通道。

## 8. 与桌面客户端的关系（Relationship to the desktop client）

客户端是 `clients/desktop`（一个独立仓库，在干净工作树中为空）。它提供 Electron 应用及其生成的 worker；
守护进程通过 §4 的握手认证两者。打包侧是 `cmd/internal/build_boxdd` 与发布工作流——产物溯源参见
`build-pruning-release.md` 与 `ci-release.md` §5。

**构建配置：** 生产 Windows/amd64 守护进程在 **`DEFAULT_BUILD_TAGS_WINDOWS`**
（`cmd/internal/build_boxdd/main.go:214-220`）下构建，而不是 `DEFAULT_BUILD_TAGS_OTHERS`。
`OTHERS` 用于 windows/386。两者都经过验证可以构建。

## 9. 与发布产物的关系（Relationship to release artifacts）

守护进程随 `Jiejiebox-<version>-windows-x64.exe` 一起发布，使用与应用和 core 相同的证书签名。
`windows-core-amd64.yml` 产出 core 产物；桌面端生产者构建、签名并打包安装器。
**已签名安装器路径仅限 `workflow_dispatch`**，因此一次发布依赖于它已被派发——参见 `ci-release.md` §5。

> **已记录的张力，此处不予解决。** `.github/workflows/windows-core-amd64.yml:5-10` 把
> Windows 桌面实验描述为**已退役**（“no daemon and no Authenticode signing”，即无守护进程、
> 无 Authenticode 签名），而 `clients/desktop/**`、`client-desktop-windows.yml`、
> `client-desktop-windows-runtime-audit.yml` 与 `scripts/ci/release-artifacts.sh` 仍在构建、
> 签名、门禁并暂存安装器。这是**产品所有权决策，而不是代码阅读结论**。
> 状态：`UNKNOWN`——不要通过选择你最先读到的那个文件来解决它。

## 10. 硬性不变量（Hard invariants）

违反其中任何一条，都是任何配置都无法开脱的安全漏洞。

| 不变量 | 理由 | 证据 |
|---|---|---|
| **守护进程与它接受的每一个对端之间签名者字节相等** | 这*就是*身份检查；链信任是被有意省略的 | `peer_windows.go:139`、`:223` |
| **参考签名者是守护进程自身已安装的可执行文件** | 对端不得能够提名什么是合法的 | `peer_windows.go:305`、`:303-304` |
| **要求固定的已安装路径** | 别处一个签名有效的二进制文件不是这个安装 | `peer_windows.go:125`、`:205` |
| **要求 `sameWindowsFile`** | 路径相等不是文件相等 | `peer_windows.go:128`、`:208` |
| **未签名的二进制文件必须被拒绝** | `bytes.Equal(nil,nil)` 为 true；两个未签名对端会匹配 | `:118`、`:222-225` |
| **被认证的对端是 APPLICATION（应用）** | worker 是中继，不是主体 | `peer_windows.go:237` |
| **签名完整性必须成立** | 容忍错误的摘要会让签名沦为装饰 | 允许列表排除了 `TRUST_E_BAD_DIGEST` |

## 11. 当前约定与有意为之的非门禁（Current conventions and deliberate non-gates）

| 规则 | 状态 | 理由 |
|---|---|---|
| 证书有效期**不是**门禁 | **有意为之的非门禁** | 在容忍错误的路径上使用字节钉定的身份与自签名证书；过期此前曾破坏启动。重新考虑这一点需要评估部署与信任模型 |
| **不**要求证书链信任 | 有意为之 | CI 测试证书在构造上就是自签名的 |
| 要求代码签名 EKU | 设计规则 | 但 `ExtKeyUsageAny` **也被接受**（`:237`）——一处已知的削弱 |
| 自签名检查 | 在容忍错误支路上要求 | 校验签名者是自签名的且其自签名有效；它补充签名者相等与其他检查 |
| Worker 管道 ACL | 约定，**不是**门禁 | `Authenticated Users` 可以打开它；由握手决定 |
| 更新产品名允许列表 | 约定 | 源码明确说明它**不是**安全边界 |
| `--listen` 禁用对端认证 | **已知限制** | 仅由服务模式拒绝它来防护 |

## 12. 溯源（Provenance）

| 提交 | 主题 | 影响 |
|---|---|---|
| `cb19a3ba5` | 从 `validateUntrustedSelfSignedCertificate` 移除 NotBefore/NotAfter 硬门禁 | 时间报告变为仅告警 |
| `9f13b33da` | 容忍 WinTrust 时间错误码 | 新增允许列表，并在先前的两码集合上加入 `CERT_E_EXPIRED` + `CERT_E_VALIDITYPERIODNESTING` |
| `8a008c5d0` | 证明篡改、未签名、EKU 与签名者 pin 边界 | 仅测试；无生产变更 |

**已实测但此处未重新实测：** 那些提交中关于 Windows 会在时钟之前报告 `CERT_E_UNTRUSTEDROOT` 的主张
（作者曾针对一个确实过期的已签名 PE 验证过一次）。代码不依赖四个错误码中哪一个到达，
因为四个都被容忍——这也是本模块无需对此定论的原因。

## 13. 重要演进（Important evolution）

该边界**并非**一开始就是字节相等 pin。它通过移除不可依赖的东西走到了这里：先是链信任
（CI 证书是自签名的），然后是有效期窗口（它在过期时破坏了产品），同时保留签名者身份相等。
签名完整性、EKU、文件身份以及容忍错误支路上的自签名检查同样重要。由此产生的可用性与
有效期策略权衡取决于部署；仅有字节相等并不构成完整的边界。

## 14. 验证（Verification）

| 项目 | 方式 | 级别 |
|---|---|---|
| 允许列表的精确成员资格 | `authenticode_windows_test.go`——一个 14 行表格，加上对全部 24 个 `CERT_E_*`/`TRUST_E_*` 错误码的双向枚举 | `VERIFIED_UNIT` |
| 被篡改的已签名 PE 被拒绝 | `TestTamperedSignedPEIsRejectedByTheFirstLayer`——构建并签名自己的 PE，然后篡改它；实测 `TRUST_E_BAD_DIGEST` | `VERIFIED_UNIT` |
| 未签名被拒绝 | `TestUnsignedFileIsRejectedByTheFirstLayer` + 真实的 `WinVerifyTrustEx` 调用 | `VERIFIED_UNIT` |
| 要求代码签名 EKU | `TestCodeSigningEKUIsRequired`（包括接受 `ExtKeyUsageAny`、拒绝 ClientAuth） | `VERIFIED_UNIT` |
| 该 pin 能区分两个有效证书 | `TestSignerPinDistinguishesTwoValidCertificates` | `VERIFIED_UNIT` |
| 过期不能造成阻断 | `TestExpiredCertificateIsNoLongerFatal`、`TestNotYetValidCertificateIsNoLongerFatal`、`TestTimeProblemBoundaries` | `VERIFIED_UNIT` |
| 该包可为 Windows 构建 | `GOOS=windows go build`，amd64 + arm64，两种标签配置 | `VERIFIED_SOURCE` |
| **守护进程确实在证书过期的情况下启动并服务** | 安装服务并连接 | **NOT RUN**——参见 §15 |

## 15. 已知限制（Known limitations）

* §4 中完整的 Windows 对端握手在本轮校准中尚未端到端验证；它需要相应的服务、管道、已签名进程与
  安装。源码检查与单独引用的包 CI 运行都不确立该端到端行为。
* `windows-boxdd-tests.yml` 包含一个在任何跳过时都会失败的步骤；当 `signtool` 或证书不可用时，
  签名测试会跳过。请以本模块顶部的确切运行与本地跳过为准，而不要推断每个 Windows 工作流的历史。
* `ExtKeyUsageAny` 与代码签名一并被接受，这比名称所暗示的更宽泛。
* `--listen` 禁用整个边界；只有服务模式会拒绝它。
* 在 Windows 上 `ProcessStartTime` 从不设置（始终为 0），因此“进程启动于”这一可观测项缺失——
  §4 中创建时间的*顺序*检查不依赖它。
* `secureWindowsInstallation` 的祖先/ACL 检查在安装时**和**运行时都可放宽（§7）。
* 过期证书的端到端路径（守护进程启动、core 启动、IPC）**没有**在真实安装中运行。参见 §14。

## 16. AI 注意事项（AI cautions）

每一条都是本代码库已经为之付出代价的“合理但错误”的改动。

1. **不要在未评估部署威胁模型的情况下更改证书有效期策略。**
   过期此前曾阻断启动；字节钉定建立了身份匹配，但并不证明有效期窗口不提供任何安全收益。
   容忍错误支路还有额外的自签名与 EKU 检查。§11。
2. **不要通过放宽允许列表来“修复” `CERT_E_EXPIRED` 的处理。** 它是有意做成允许列表的。
   尤其是 `TRUST_E_BAD_DIGEST` 绝不能容忍——那是验证签名与装饰文件之间的区别。§5。
3. **不要添加 `len(signer) > 0` 守卫就称之为修复。** 在 nil 错误下 nil 签名者已不可能；
   防御在于调用方先检查 `err`。添加比较守卫会掩盖真正的不变量所在。§5。
4. **不要用比较路径代替比较文件来“简化”握手。** `sameWindowsFile` 之所以存在，是因为路径
   可以被重新指向；`:128`/`:208` 是承重的。
5. **不要把 `openLockedExecutable` 的 `FILE_SHARE_READ` 换成普通打开。** 它在映像被检查期间
   排除写入者。§4。
6. **不要假定 worker 是被认证的主体。** 主体是应用（`:237`）；针对 worker 写的检查会接受
   错误的进程。
7. **不要假定管道 ACL 保护了守护进程。** 按设计 `Authenticated Users` 就能打开管道；
   握手才是门禁。§3。
8. **不要为生产 Windows 守护进程使用 `DEFAULT_BUILD_TAGS_OTHERS`。** 应使用
   `DEFAULT_BUILD_TAGS_WINDOWS`（`build_boxdd/main.go:214-220`）。§8。
9. **不要把更新产品名允许列表当作安全边界。** 源码说明它不是。§11。
10. **不要声称守护进程总是拒绝不安全的安装目录。** 该逃生通道会传播到运行时。§7。
11. **不要通过选边来解决 §9 的退役张力。** 这是产品决策；`UNKNOWN` 才是诚实的状态。
12. **不要引用本模块作为守护进程可用的证明。** 它记录源码验证了什么。不存在端到端运行。§15。

## 17. 后续工作（Follow-up）

* **该信任边界没有已记录的端到端测试。** Windows CI 门禁在 `windows-latest` 上运行 `go test -count=1 -v
  ./experimental/boxdd/`，并在有跳过时失败；确切的发布运行与 33 个测试计数已在上文链接。
  它做不到的是启动一个服务：`peer_windows.go` 仍然没有测试文件，而握手需要
  真实安装、真实的命名管道和两个真实进程。
* **端到端信任边界测试需要一个一次性 Windows VM**，它具备：管理员权限、安装与移除 Windows 服务的
  能力、运行 NSIS 安装器的能力、**控制该 VM 自身系统时钟**的能力、重启以及网络。没有全部六项，
  就无法演练 `§15` 的过期/尚未生效链（服务安装 → 守护进程启动 → core 启动 → IPC → 代理流量），
  而时钟控制正是让一个确实过期的签名无需重新签名即可达成的条件。**不要通过修改开发者工作站的
  时钟来尝试这件事**——签名必须在有效期内产生，然后在过期后验证，这意味着要么可控的 VM 时钟，
  要么真实等待。
* 决定是否应继续接受 `ExtKeyUsageAny`（§11）。
* 决定 §9 的退役问题。

### CI 验证了什么、没验证什么（What was verified by CI, and what was not）

**本表格所依据的运行：** `.github/workflows/windows-boxdd-tests.yml`，运行 id **`38606602012`**，
`head_sha` `58ece9619`，runner `windows-latest`，结论 **success**，报告
`PASS: 27 daemon security test(s) executed with no skips.`

该记录存在于 GitHub API 中，而非树中——所以按照本仓库的证据上限，它是 `VERIFIED_CI`
**并附带一个有名称、可复核的运行 URL**，而不是一个已存产物。复检方式：

```sh
gh run view 38606602012 --repo Piggy-Cat-bit-shadow/sing-box   # or open the URL
gh run list --workflow windows-boxdd-tests.yml --limit 5
```

同一工作流在更晚 SHA 上的后续运行是**更新的证据，而不是同一份证据**。
不要在未重读该运行的情况下把这一行搬到不同的 `head_sha` 上。

| 主张 | 证据 | 级别 |
|---|---|---|
| 允许列表在两个方向上都是精确的 | Windows runner，运行 `38606602012`，14 行表格 + 24 错误码枚举 | `VERIFIED_CI` |
| 被篡改的已签名 PE 被拒绝 | 同一次运行，实测 `0x80096010 TRUST_E_BAD_DIGEST` | `VERIFIED_CI` |
| 未签名文件被拒绝 | 同一次运行，实测 0x800B0003 / 0x800B0100 | `VERIFIED_CI` |
| 要求代码签名 EKU | 同一次运行 | `VERIFIED_CI` |
| 该 pin 能区分两个有效证书 | Windows runner | `VERIFIED_CI` |
| 服务在证书过期的情况下启动并服务 | **无** | **UNVERIFIED——需要上述 VM** |

最后一行是需要格外小心的。它之上的所有内容都关于*函数是否判断正确*。它完全没有说明当一个
Windows **服务**背后的可执行文件证书过期时，该服务能否安装、启动并服务——而那正是这项工作
旨在防止的用户可见故障。**拥有知识不等于证明行为。**
