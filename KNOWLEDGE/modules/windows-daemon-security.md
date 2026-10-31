# Windows 守护进程及其信任边界（Windows daemon and daemon trust boundary）

## 基线后复核与运行证据（Post-baseline review and run evidence）(2026-10-28)

在 `cb71cf80d72`，[Windows 守护进程测试 40605271303](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40605271303)
报告 33 个测试通过、没有跳过，其中包括 `TestTamperedSignedPEIsRejectedByTheFirstLayer`。
这份 CI 证据只适用于该提交和运行中列出的测试；它不等同于已安装服务的端到端握手测试，也
不能推广到其他二进制文件或平台。

在 `011f3fdcd99`，`peer_windows.go` 校验 worker 和应用身份；
`authenticode_windows.go` 只允许列出的证书链错误和时间错误，随后仍会检查签名者与证书
约束。`server.go` 在所有者授权前，先统一 gRPC 服务方法的前导斜杠；
`owner_gate_spelling_test.go` 覆盖两种写法。卸载流程先停止服务，再删除服务，见
`cmd_service_windows.go` 和 `cmd_service_uninstall_contract_test.go`。这些结论的证据级别
是目标版本上的 `VERIFIED_SOURCE`。

GitHub Actions 的 [run 40535897659](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40535897659)
跑在已发布的提交 `6112aa17052297ec9890e0e7f1541a06120b84b2` 上；其中 Windows daemon 作业成功，
报告 33 个测试、无跳过，包含被篡改 PE 测试（该提交处为 `VERIFIED_CI`）。从这个发布到目标之间，
本包和工作流都没有变化；但 `go.mod` 与 `go.sum` 确实变了，所以这不等于在目标上运行，
也不能证明端到端服务握手。在本地 Windows 10 build 19044 上，`go test ./experimental/boxdd -count=1 -v`
成功退出，但被篡改 PE 测试因 Cert 驱动器不可用而跳过：跳过不是通过。

**范围：** `experimental/boxdd/**` 中定义的进程角色及其本地信任边界，包括 Windows 服务、
每用户 worker、命名管道握手、对端和可执行文件认证、Authenticode 语义及更新路径。
审阅时该目录有 55 个 `.go` 文件，其中 51 个不是测试文件，另有 `desktop_service.proto`。
`daemon/**` 的职责见 `libbox-platform-contract.md`；本模块重点记录两侧的信任边界。

**遇到以下情况时加载本模块：** 改动 `experimental/boxdd/**` 中的任何内容、Windows 服务或安装器路径、
对端认证、签名者处理，或者在断言 Windows 守护进程验证了什么、没验证什么之前。

**证据：** 除非另有标注，每条断言相对于 `v0.1.9` 基线都是 `VERIFIED_SOURCE`。
下文这些 v0.1.9 断言不带 CI 运行证据；后续运行见上文。本模块没有跑过 Windows 端到端
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
`daemon/instance.go:85-153`）**在进程内**承载 `box.Box`。`worker` 是 Electron 应用为每个用户生成的进程；
它提供 `ApplicationService`，并中继到守护进程的连接。**经过认证的对端身份是 APPLICATION（应用），
不是 worker**（`peer_windows.go:237`）——这个区别对 §4 的每一项检查都成立。

本包对端与服务行为有各平台的实现。Linux 并非不做认证：`peer_linux.go` 用 Unix `SO_PEERCRED` 和 `/proc`
里的进程启动时间识别本地对端，再套用通用的所有者授权。这与 Windows 的已安装文件加签名者检查是两条不同的
边界；`--listen` 走单独的保底路径。下文详细的握手分析只针对 Windows，
不构成 Linux 设备或运行时覆盖。

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

服务入口在 `cmd_run_windows.go:26`（`svc.Run(serviceName, &windowsService{})`），`:54` 处的 `Execute`
处理 `svc.ChangeRequest`，会话变更也在其中。服务模式下 `--listen` 会被**拒绝**（`cmd_run_windows.go:56-58`）。

## 3. 命名管道信任边界（Named Pipe trust boundary）

**能够打开管道不等于通过认证。** `server_windows.go:15` 为 `Authenticated Users` 授予 `GRGW`，因此已认证的本地用户可以打开管道。管道名位于 `ProtectedPrefix\Administrators` 下，只有管理员才能创建同名竞争实例；连接是否被接受则由后续握手决定。

## 4. 可执行文件 / 对端认证——按顺序的握手（Executable / peer authentication — the handshake, in order）

下表按 `peer_windows.go:83-237` 的顺序列出检查及其作用。每项失败时都有对应错误信息。

| # | 检查 | 证明 | 行号 |
|---|---|---|---|
| 1 | 端点是 Windows 命名管道 | 不是 TCP 冒名者 | `:86` |
| 2 | 客户端进程映像路径 == `expectedWorkerPath`（不区分大小写） | 已安装的 worker | `:125` |
| 3 | `sameWindowsFile(processImage, daemonExecutable)` | 它是*同一个文件*，不是副本 | `:128` |
| 4 | `authenticodeSigner(worker)` == `daemonSigner` | **签名者字节相等** | `:139` |
| 5 | `validateWorkerProcessRole`——命令行、私有管道路径、父 PID | 它是 worker，不是仿冒者 | `:360-379` |
| 6 | `processIdentity`——令牌用户 SID、交互式会话 | 是哪个用户在连接 | `:313-334` |
| 7 | worker 与应用共享同一进程身份 | 同一用户的一对进程，不是混搭 | `:165` |
| 8 | 父进程创建时间 < worker 创建时间 | 观测到的时间戳顺序符合预期；这项检查单独并不证明父子关系 | `:175` |
| 9 | 父进程映像路径 == `expectedApplicationPath` | 已安装的应用 | `:205` |
| 10 | `sameWindowsFile(parentProcessImage, expectedApplication)` | 同一文件，不是副本 | `:208` |
| 11 | `validateApplicationProcessRole`——不是 Electron 子进程，父进程即应用 | 看角色，不只看路径 | `:339-355` |
| 12 | `authenticodeSigner(application)` == `daemonSigner` | **再次签名者字节相等** | `:223` |
| 13 | 两个进程在认证期间都没有退出 | 检查时已观测到退出的进程会被拒绝；这不证明消除了全部竞态 | `:235` |

此外，两项实现细节用于降低检查期间的竞态风险：

* **`openLockedExecutable` 使用 `FILE_SHARE_READ`**（`:466`），阻止检查期间重写映像。
* **`finalWindowsPath` 从已打开的句柄解析路径**（`:482`），使比较基于实际打开的文件，而不只依赖可能发生变化的路径字符串。

**`expectedApplicationPath` 派生自守护进程自身的路径**（`peer_windows.go:303-304`），所以对端无法通过握手指定一个任意的预期应用路径。

## 5. Authenticode 语义（Authenticode semantics）

以下流程位于 `authenticode_windows.go`。签名完整性、证书链信任和签名者身份是三个独立属性，应结合 §6 一起理解。

```text
SIGNATURE INTEGRITY   WinVerifyTrustEx: does the signature match the bytes?     -> MUST hold
CHAIN TRUST           is the signer issued by a trusted CA?                     -> deliberately NOT required
SIGNER IDENTITY       is the signer byte-equal to the daemon's own?             -> MUST hold
```

### 被接受的签名者链条，按顺序（The accepted-signer chain, in order）

1. `WinVerifyTrustEx`（`:114`）——原始判定结果。
2. `isAllowedUntrustedAuthenticodeError`（`:78`，在 `:115` 应用）——**允许列表**（见下文）。
3. `verifiedSignerCertificate(trustData.StateData)`（`:120`）——提取签名者的 DER 字节。
4. `validateCodeSigningCertificate`（`:129`）——解析证书，并要求代码签名 EKU。
5. 只在容忍错误的支路上：先 `reportAuthenticodeCertificateTime`（`:153`），再 `validateUntrustedSelfSignedCertificate`（`:154`）——要求证书自签名，且自签名本身有效。

### 允许列表恰好是四个错误码加成功（The allowlist is exactly four codes plus success）

```text
err == nil                     accepted outright
CERT_E_UNTRUSTEDROOT     0x800B0109   tolerated
CERT_E_CHAINING          0x800B010A   tolerated
CERT_E_EXPIRED           0x800B0101   tolerated
CERT_E_VALIDITYPERIODNESTING 0x800B0102   tolerated
```

匹配走 `E.IsMulti` → `errors.Is`（`sing common/exceptions/multi.go:60-67`），因此成员资格是**精确的**——没有区间，也没有前缀。

**实测会被拒绝的情形**（`TRUST_E_NOSIGNATURE` 0x800B0100、`TRUST_E_SUBJECT_FORM_UNKNOWN` 0x800B0003、
`TRUST_E_BAD_DIGEST` 0x80096010、`CERT_E_WRONG_USAGE` 0x800B0110、任何其他 errno、任何非 errno 错误）。
**被篡改的已签名 PE** 实测为 `TRUST_E_BAD_DIGEST`；**未签名**文件返回 `TRUST_E_SUBJECT_FORM_UNKNOWN`，
未签名但格式良好的 PE 则返回 `TRUST_E_NOSIGNATURE`。

### 证书有效期不是门禁（Certificate validity period is NOT a gate）

`authenticodeCertificateTimeProblem`（`:197`）**只返回描述字符串，从不返回错误**；
`reportAuthenticodeCertificateTime`（`:166`）在证书有效时以 `Debug` 记录，已过期或尚未生效时以 `Warn` 记录。
**从证书时间通向失败的路径根本不存在**，日志器也无法变成门禁：日志器为 nil 时提前返回（`:168`），
解析失败只发一条警告（`:173`）。

> 这是有意的可用性/信任权衡：容忍信任错误的前提是自签名有效，
> 而对端身份绑定在守护进程的签名者字节上，另有其他进程检查。
> 此前 CI 证书老化时，过期会把产品挡在启动之外。字节相等本身应付不了
> 全部证书生命周期或密钥泄露风险；要改这个策略，需要有明确的
> 威胁模型与部署评审。参见 §11。

### 未签名二进制文件永远无法通过——原因是结构性的（Unsigned binaries can never pass — and the reason is structural）

`authenticodeSigner` 有**六**条 `return nil, err` 类失败路径，成功路径恰好**一条**
`return signer, nil`（`:159`）。后者只有在 `verifiedSignerCertificate` 成功之后才可达，
而这个函数会拒绝 nil 或空的证书上下文（`:222-225`）。所以 nil 签名者**不可能**伴随 nil 错误。

这一点很关键，因为 `bytes.Equal(nil, nil)` 为 **true**。所以防御*不在*比较本身，而在顺序：
**每个调用点都先检查 `err`，再作比较。** 不要靠添加 `len(signer) > 0` 守卫来“加固”它；
这个不变量已经是结构性的，加守卫反而暗示比较就是防御，而事实并非如此。

## 6. 签名者 pin（The signer pin）

**一个 pin、六次比较，参考字节始终来自守护进程自身已安装的可执行文件。**
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

`peer_windows.go:305` 经 `:63` → `platform_windows.go:63` → `cmd_worker_windows.go:177` 一路传递。
比较发生在：`peer_windows.go:139`、`:223`、`cmd_worker_windows.go:151`、`:400`、`security_windows.go:60`、
`update_windows.go:107`。

**实测：** 用两张确实有效且互不相同的自签名代码签名证书分别签两个二进制文件，得到的签名者负载为
**800** 与 **798** 字节，`bytes.Equal` 为 **false**——可见这个 pin 能区分两者。同一张证书比较结果为相等，
所以这个 pin 并不是空洞地为 false。

## 7. 服务安装（Service installation）

`secureWindowsInstallation`（`security_windows.go:29`），入口在 `cmd_service_windows.go:146`。它检查安装
路径的布局与祖先（所有者必须是 SYSTEM、Administrators 或 TrustedInstaller），所以在用户拥有的目录下
安装会以 `code 1` 失败（`docs/fork/WINDOWS-SFW-BUILD-RECORD.md` §P0-2）。

> **检查有明确的例外。** 签名者相等和布局检查仍然强制执行；`--allow-unsafe-installation-directory-permissions` 会放宽卷、祖先和 ACL 检查。该标志也会传入服务命令行，使运行时复验采用同样的放宽策略。因此不能声称守护进程总会拒绝不安全的安装目录。

## 8. 与桌面客户端的关系（Relationship to the desktop client）

客户端是 `clients/desktop`（独立仓库，在干净工作树里为空）。它提供 Electron 应用和应用生成的 worker；
守护进程用 §4 的握手认证这两者。打包侧是 `cmd/internal/build_boxdd` 与发布工作流——产物溯源见
`build-pruning-release.md` 与 `ci-release.md` §5。

**构建配置：** 生产环境的 Windows/amd64 守护进程用 **`DEFAULT_BUILD_TAGS_WINDOWS`**
（`cmd/internal/build_boxdd/main.go:214-220`）构建，不用 `DEFAULT_BUILD_TAGS_OTHERS`。
`OTHERS` 用于 windows/386。两者都验证过可以构建。

## 9. 与发布产物的关系（Relationship to release artifacts）

守护进程随 `Jiejiebox-<version>-windows-x64.exe` 一起发布，签名证书与应用和 core 相同。
`windows-core-amd64.yml` 产出 core 产物；桌面端生产者负责构建、签名并打包安装器。
**已签名安装器这条路径仅限 `workflow_dispatch`**，所以一次发布取决于它是否已被派发——参见 `ci-release.md` §5。

> **这是已记录的张力，此处不解决。** `.github/workflows/windows-core-amd64.yml:5-10` 把
> Windows 桌面实验描述为**已退役**（“no daemon and no Authenticode signing”，即无守护进程、
> 无 Authenticode 签名），但 `clients/desktop/**`、`client-desktop-windows.yml`、
> `client-desktop-windows-runtime-audit.yml` 与 `scripts/ci/release-artifacts.sh` 仍在构建、
> 签名、门禁并暂存安装器。这是**产品所有权决策，不是读代码能得出的结论**。
> 状态为 `UNKNOWN`。需要产品决策明确支持范围，不能只凭其中一处描述下结论。

## 10. 硬性不变量（Hard invariants）

以下约束共同保护守护进程的身份验证和信任边界；修改时应逐项核对。

| 不变量 | 理由 | 证据 |
|---|---|---|
| **守护进程与它接受的每一个对端之间签名者字节相等** | 这*就是*身份检查；链信任是有意省略的 | `peer_windows.go:139`、`:223` |
| **参考签名者是守护进程自身已安装的可执行文件** | 对端不能自己指定什么是合法的 | `peer_windows.go:305`、`:303-304` |
| **要求固定的已安装路径** | 别处一个签名有效的二进制文件不算这个安装 | `peer_windows.go:125`、`:205` |
| **要求 `sameWindowsFile`** | 路径相等不是文件相等 | `peer_windows.go:128`、`:208` |
| **未签名的二进制文件必须被拒绝** | `bytes.Equal(nil,nil)` 为 true；两个未签名对端会匹配 | `:118`、`:222-225` |
| **被认证的对端是 APPLICATION（应用）** | worker 是中继，不是主体 | `peer_windows.go:237` |
| **签名完整性必须成立** | 容忍摘要错误会让签名沦为装饰 | 允许列表排除了 `TRUST_E_BAD_DIGEST` |

## 11. 当前约定与有意为之的非门禁（Current conventions and deliberate non-gates）

| 规则 | 状态 | 理由 |
|---|---|---|
| 证书有效期**不是**门禁 | **有意为之的非门禁** | 在容忍错误的路径上用字节钉定的身份与自签名证书；过期此前曾破坏启动。重新考虑这一点，需要先评估部署与信任模型 |
| **不**要求证书链信任 | 有意为之 | CI 测试证书按构造就是自签名的 |
| 要求代码签名 EKU | 设计规则 | 但 `ExtKeyUsageAny` **也被接受**（`:237`）——已知的一处削弱 |
| 自签名检查 | 在容忍错误支路上要求 | 校验签名者是否自签名、自签名是否有效；它补充签名者相等与其他检查 |
| Worker 管道 ACL | 约定，**不是**门禁 | `Authenticated Users` 就能打开它；由握手决定 |
| 更新产品名允许列表 | 约定 | 源码明确写了它**不是**安全边界 |
| `--listen` 禁用对端认证 | **已知限制** | 只靠服务模式拒绝它来防护 |

## 12. 溯源（Provenance）

| 提交 | 主题 | 影响 |
|---|---|---|
| `cb19a3ba5` | 从 `validateUntrustedSelfSignedCertificate` 移除 NotBefore/NotAfter 硬门禁 | 时间报告变为仅告警 |
| `9f13b33da` | 容忍 WinTrust 时间错误码 | 新增允许列表，在原先两个错误码之上加入 `CERT_E_EXPIRED` + `CERT_E_VALIDITYPERIODNESTING` |
| `8a008c5d0` | 证明篡改、未签名、EKU 与签名者 pin 边界 | 仅测试；无生产变更 |

**已实测，但此处没有重新实测：** 那些提交里说 Windows 会在时钟之前报 `CERT_E_UNTRUSTEDROOT`
（作者曾用一个确实过期的已签名 PE 验证过一次）。代码不依赖四个错误码里哪一个到达，
因为四个都容忍——所以本模块不必在这个问题上定论。

## 13. 重要演进（Important evolution）

这个边界**并非**一开始就是字节相等 pin。它是一路删掉靠不住的东西走过来的：先删链信任
（CI 证书是自签名的），再删有效期窗口（过期时会导致产品损坏），同时保留签名者身份相等。
签名完整性、EKU、文件身份，以及容忍错误支路上的自签名检查，同样重要。由此形成的可用性与
有效期权衡取决于部署；光有字节相等并不构成完整边界。

## 14. 验证（Verification）

| 项目 | 方式 | 级别 |
|---|---|---|
| 允许列表的精确成员资格 | `authenticode_windows_test.go`——一个 14 行表格，加上对全部 24 个 `CERT_E_*`/`TRUST_E_*` 错误码的双向枚举 | `VERIFIED_UNIT` |
| 被篡改的已签名 PE 被拒绝 | `TestTamperedSignedPEIsRejectedByTheFirstLayer`——构建并签名自己的 PE，再篡改它；实测 `TRUST_E_BAD_DIGEST` | `VERIFIED_UNIT` |
| 未签名被拒绝 | `TestUnsignedFileIsRejectedByTheFirstLayer` + 真实的 `WinVerifyTrustEx` 调用 | `VERIFIED_UNIT` |
| 要求代码签名 EKU | `TestCodeSigningEKUIsRequired`（包括接受 `ExtKeyUsageAny`、拒绝 ClientAuth） | `VERIFIED_UNIT` |
| 该 pin 能区分两个有效证书 | `TestSignerPinDistinguishesTwoValidCertificates` | `VERIFIED_UNIT` |
| 过期不能造成阻断 | `TestExpiredCertificateIsNoLongerFatal`、`TestNotYetValidCertificateIsNoLongerFatal`、`TestTimeProblemBoundaries` | `VERIFIED_UNIT` |
| 该包可为 Windows 构建 | `GOOS=windows go build`，amd64 + arm64，两种标签配置 | `VERIFIED_SOURCE` |
| **守护进程确实能在证书过期时启动并对外服务** | 安装服务并连接 | **NOT RUN**——参见 §15 |

## 15. 已知限制（Known limitations）

* §4 里完整的 Windows 对端握手在本轮校准中还没有端到端验证；它需要相应的服务、管道、已签名进程和
  安装。源码检查、以及单独引用的包 CI 运行，都不能确立这个端到端行为。
* `windows-boxdd-tests.yml` 里有一个只要出现跳过就失败的步骤；`signtool` 或证书不可用时，
  签名测试会跳过。请以本模块顶部给出的那次运行和本地跳过为准，不要推断每个 Windows 工作流的历史。
* `ExtKeyUsageAny` 与代码签名一起被接受，比名字暗示的范围更宽。
* `--listen` 会让整个边界失效；只有服务模式拒绝它。
* Windows 上 `ProcessStartTime` 从不设置（始终为 0），所以“进程启动于”这个可观测项是缺的——
  §4 里创建时间的*顺序*检查不依赖它。
* `secureWindowsInstallation` 的祖先/ACL 检查在安装时**和**运行时都可放宽（§7）。
* 过期证书的端到端路径（守护进程启动、core 启动、IPC）**没有**在真实安装上跑过。参见 §14。

## 16. AI 注意事项（AI cautions）

每一条都是本代码库已经付过代价的“合理但错误”的改动。

1. **不要在没评估部署威胁模型时改动证书有效期策略。**
   过期此前阻断过启动；字节钉定建立了身份匹配，但不能证明有效期窗口没有任何安全收益。
   容忍错误支路还有额外的自签名与 EKU 检查。§11。
2. **不要靠放宽允许列表来“修复” `CERT_E_EXPIRED` 的处理。** 允许列表是有意这么设计的。
   尤其是 `TRUST_E_BAD_DIGEST` 绝不能容忍——这是验证签名与装饰文件之间的区别。§5。
3. **不要加了 `len(signer) > 0` 守卫就称之为修复。** 在 nil 错误下 nil 签名者已经不可能出现；
   防御在于调用方先检查 `err`。加比较守卫只会掩盖真正的不变量在哪里。§5。
4. **不要用比较路径代替比较文件来“简化”握手。** `sameWindowsFile` 之所以存在，是因为路径
   可以被重新指向；`:128`/`:208` 是关键的。
5. **不要把 `openLockedExecutable` 的 `FILE_SHARE_READ` 换成普通打开。** 它在检查映像期间
   挡住写入者。§4。
6. **不要假定 worker 是被认证的主体。** 主体是应用（`:237`）；照着 worker 写的检查会接受
   错误的进程。
7. **不要假定管道 ACL 保护了守护进程。** 按设计 `Authenticated Users` 就能打开管道，
   门禁是握手。§3。
8. **不要给生产环境的 Windows 守护进程用 `DEFAULT_BUILD_TAGS_OTHERS`。** 应当用
   `DEFAULT_BUILD_TAGS_WINDOWS`（`build_boxdd/main.go:214-220`）。§8。
9. **不要把更新产品名允许列表当成安全边界。** 源码说了它不是。§11。
10. **不要声称守护进程总会拒绝不安全的安装目录。** 放宽权限的选项也会影响运行时复验，见 §7。
11. **不要靠选边来解决 §9 的退役张力。** 需要产品决策；在此之前应标记为 `UNKNOWN`。
12. **不要把本模块当成守护进程可用的证明。** 它记录的是源码验证了哪些东西，端到端运行并不存在。§15。

## 17. 后续工作（Follow-up）

* **这个信任边界没有已记录的端到端测试。** Windows CI 门禁在 `windows-latest` 上跑 `go test -count=1 -v
  ./experimental/boxdd/`，一旦有跳过就失败；确切的发布运行与 33 个测试的计数
  已在上文给出链接。它做不到的是启动一个服务：`peer_windows.go` 至今没有测试文件，而握手需要
  真实安装、真实命名管道和两个真实进程。
* **端到端信任边界测试需要一台一次性 Windows VM**，它要具备：管理员权限、安装与移除 Windows 服务的
  能力、运行 NSIS 安装器的能力、**控制这台 VM 自身系统时钟**的能力、重启以及网络。六项缺一，
  就无法演练 `§15` 的过期/尚未生效链路（服务安装 → 守护进程启动 → core 启动 → IPC → 代理流量），
  受控时钟可以在证书有效期内签名，再于过期后验证。请使用可控制时钟的一次性 VM，或等待证书自然过期，不要修改开发者工作站的时钟。
* 决定是否继续接受 `ExtKeyUsageAny`（§11）。
* 定下 §9 的退役问题。

### CI 验证了什么、没验证什么（What was verified by CI, and what was not）

**本表格依据的运行：** `.github/workflows/windows-boxdd-tests.yml`，运行 id **`38606602012`**，
`head_sha` `58ece9619`，runner `windows-latest`，结论 **success**，报告
`PASS: 27 daemon security test(s) executed with no skips.`

这条记录存在于 GitHub API 中，不在树里——按本仓库的证据上限，它是 `VERIFIED_CI`
**外加一个具名、可复核的运行 URL**，而不是一份留存的产物。复检方式：

```sh
gh run view 38606602012 --repo Piggy-Cat-bit-shadow/sing-box   # or open the URL
gh run list --workflow windows-boxdd-tests.yml --limit 5
```

同一工作流在更晚 SHA 上的后续运行是**新的证据，不是同一份证据**。
没有重读那次运行，就不要把这一行搬到别的 `head_sha` 上。

| 主张 | 证据 | 级别 |
|---|---|---|
| 允许列表在两个方向上都是精确的 | Windows runner，运行 `38606602012`，14 行表格 + 24 错误码枚举 | `VERIFIED_CI` |
| 被篡改的已签名 PE 被拒绝 | 同一次运行，实测 `0x80096010 TRUST_E_BAD_DIGEST` | `VERIFIED_CI` |
| 未签名文件被拒绝 | 同一次运行，实测 0x800B0003 / 0x800B0100 | `VERIFIED_CI` |
| 要求代码签名 EKU | 同一次运行 | `VERIFIED_CI` |
| 该 pin 能区分两个有效证书 | Windows runner | `VERIFIED_CI` |
| 服务在证书过期时启动并提供服务 | **无** | **UNVERIFIED——需要上述 VM** |

最后一行要格外小心。它上面的所有内容只说明*函数是否判断正确*。至于一个
Windows **服务**背后的可执行文件证书过期时，这个服务还能不能安装、启动并对外服务——它一个字都没讲，
而那正是这项工作要防的用户可见故障。**懂知识，不等于有行为证据。**
