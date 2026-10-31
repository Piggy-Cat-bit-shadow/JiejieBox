# Windows 共存与订阅剩余流量：实施交付报告

指引来源：`C:\Users\Jie\Desktop\配置文件\01_WINDOWS_COEXISTENCE_AND_SUBSCRIPTION_CONSTRUCTION.md`

本轮在**新分支**上完成两个项目的实现、测试、构建和提交，并核对了三个仓库的 submodule pin 链。当时尚未合并、打标签、发布或推送。

由于本机没有 `electron.exe` 且无法联网，本轮未能产出 NSIS 安装包，真机双安装验收留待第二阶段。安装器脚本已通过 **makensis 实际编译**；该结果不能代替完整打包和真机验收。

---

## 1. 基线

实施起点（三个仓库的工作树均为干净状态）：

| 仓库 | 路径 | 基线分支 | 基线 SHA |
| --- | --- | --- | --- |
| core | `C:\src\sing-box` | `testing` | `abd75e0e2c3faae643e0c1b2645017446dd0b2e1` |
| desktop | `C:\src\sing-box\clients\desktop` | `jiejiebox/main` | `917d2a4e9864528e1c10c4c6a39f3949b6f5d878` |
| dashboard | `C:\src\sing-box\clients\desktop\dashboard` | `main` | `3d32338fdc35b050cd9728afa7f66c18206e4fb9` |

三个固定版本与指引 1.2 节记录的 SHA **完全一致**。

实施结束时的版本：

| 仓库 | feature 分支 | SHA |
| --- | --- | --- |
| core | `feat/windows-coexistence-subscription-quota` | `5b06e5fc6f54838a35aae0bf0af0b5d2ea7db9f5` |
| desktop | `feat/windows-coexistence-subscription-quota` | `93986e0398d6eb0f414ee404042293f5d31f279f` |
| dashboard | `feat/windows-coexistence-subscription-quota` | `d452c24ec959d1e30159b6cd5cce1fa48e7dc353` |

`git submodule status --recursive`（core feature branch）：

```text
93986e0398d6eb0f414ee404042293f5d31f279f clients/desktop (heads/feat/windows-coexistence-subscription-quota)
d452c24ec959d1e30159b6cd5cce1fa48e7dc353 clients/desktop/dashboard (heads/feat/windows-coexistence-subscription-quota)
```

**远端未推送**。三个 feature branch 仅存在于本地。

---

## 2. 架构审计结论

### 2.1 需要修改的共存冲突（已修改）

| 资源 | 为什么是全局键 |
| --- | --- |
| `win.appId` | electron-builder 用 `UUID.v5(appId)` 派生 **卸载注册表键** 与 **卸载器文件名** |
| `setAppUserModelId` | 任务栏分组、跳转列表、通知归属 |
| `%APPDATA%\sing-box` | `settings.db` / `profiles/` / `session/` / `crash_dumps/` |
| `HKLM\SOFTWARE\SagerNet\sing-box` | runtime **读**它才知道自己的 AppData 与 daemon 目录 |
| `%ProgramData%\sing-box-daemon` | SYSTEM 服务的状态树 |
| SCM `serviceName = sing-box-daemon` | SCM 以该字符串为键；event log source、service SID、工作目录 ACL 全部由它派生 |
| `\\.\pipe\ProtectedPrefix\Administrators\sing-box` | 同名 pipe 只能有一个 server |
| `Software\Classes\*\shell\Taildrop` | 卸载 `DeleteRegKey` 整键删除，会删掉对方的右键菜单 |
| `%APPDATA%\sing-box-installer\data-transition.json` | 两个安装程序未完成的迁移状态互相干扰 |
| `sing-box://` 默认 handler | 后安装的客户端会覆盖先安装客户端的设置 |

### 2.2 为安全与兼容保留的配置

| 资源 | 为什么保留 |
| --- | --- |
| `win.executableName: sing-box` | `experimental/boxdd` 用 `<install>/sing-box.exe` 解析应用并比对 Authenticode signer；这是安全链的一环，不是品牌。且两客户端装在不同目录，本身不冲突 |
| `daemonExecutableName = "sing-box-daemon.exe"` | `experimental/boxdd` 按名解析，只在单一安装树内读取 |
| `.bpf` file association | 两边是**同一种** profile 格式，注册表以扩展名为键，无法在不发明新格式的前提下隔离。记录为 known boundary：后装者胜 |
| `linux.appId = io.nekohasekai.sfl` | 本轮范围是 Windows；Linux 全量 rebrand 明确不做 |
| updater feed / asset matcher | 已指向 fork releases + `Jiejiebox-v` 前缀，**未触碰** |

### 2.3 新发现（指引没有明确写、但确实重要）

这一节是本轮审计的主要产出。

1. **`$APPDATA` 不是 `%ProgramData%`。** installer 把 daemon 数据目录默认成
   `"$APPDATA\sing-box-daemon"`，而 `SetShellVarContext all` **不会**重定向 profile 变量。
   于是 installer 写在**调用者**的 roaming profile，runtime 的 fallback
   (`installationLayout.ts` → `CommonApplicationData`) 和 preflight 校验器
   (`installer-preflight.ps1` → `CommonApplicationData`) 都指向 `%ProgramData%`——
   **三处不一致**，且 SYSTEM 服务拿到的是一个每用户不同的私有目录。已统一到 `%ProgramData%`。

2. **`$COMMONAPPDATA` 在 NSIS 里不存在。** 这是我在修 (1) 时自己引入的，被
   `build/check-installer.nsh`（本轮新增的 makensis 静态检查）当场抓到：NSIS 对未知
   `$NAME` 只发 **warning 6000** 并解析为**空串**，daemon 状态目录会变成盘根下的
   `\jiejiebox-daemon`，而安装**照常报成功**。现在写作 `$PROFILE\..\..\ProgramData`，
   并把两个错误拼法及原因写在旁边。

3. **`installer-data.ps1` 的状态目录校验永远不可能通过。** NSIS 侧写
   `$APPDATA\sing-box-installer\...`，而 PS 侧 `Initialize-TransitionStateStorage()`
   拿 `CommonApplicationData` 去比。两边从未相等，那个 guard 形同虚设。
   现在两边都指向当前用户 roaming profile（用 `$PROFILE`，不受 shell 变量上下文影响）。

4. **`Stop-Service -Name <name>` 没有"这是我自己的服务吗"的判断。** 在服务名共享的前提下，
   这句会**停掉官方客户端的 daemon**，而下一步就是替换它的二进制。现在新增
   `build/installer-stop-service.ps1`：读取 SCM 自己记录的 daemon 命令行，只有落在本次安装树内
   才停，否则原样放过。

5. **CI gate 里的服务名是"默认参数"，不是必填。** `GateHelpers.ps1` 的
   `Get-DaemonServiceCommandLine` / `Get-InstallationLayoutRegistry` 各自带一个旧默认值。
   改为 `Mandatory`，强制调用方传入——默认值会让 gate 去检查一个本次安装并不拥有的服务，
   在空机器上通过、在正确的机器上失败。

6. **`ci(state)` 的其它 workflow 字面量。** `client-desktop-windows*.yml` 的 runtime-audit
   清理步骤直接 `sc.exe delete "sing-box-daemon"`，并在清理列表里删
   `C:\ProgramData\sing-box-daemon`。这些不随 Go 常量移动，会**清理不掉残留服务**，
   使下一次运行的前置条件失败。已同步。

7. **Electron single-instance lock 确实随 `userData` 隔离**（指引 9.1 要求核实，不凭印象）。
   证据链：`src/main/index.ts:109` 调用 `configureApplicationPaths(...)`，其内部
   `app.setPath("userData", ...)`（`applicationPaths.ts:50`）；`requestSingleInstanceLock()`
   在该文件后面才执行（`index.ts:445`）。Electron 在 Windows 上的实现是用
   `userData` 路径派生一个命名 mutex（`electron/atom/browser_process_impl` → `ProcessSingleton`），
   因此官方 `%APPDATA%\sing-box` 与 fork `%APPDATA%\jiejiebox` 得到**不同的 mutex**，
   两个 GUI 可同时打开。**未新增 mutex framework。**

8. **`removeProfile` 与 quota 的孤儿行。** 删除 profile 必须在**同一事务**里删 quota 行，
   否则新表会永久累积无人读取的行。

9. **overlay 与实际文件内容耦合。** `apply-desktop-branding-overlay.py` 是**逐字匹配**规则引擎，
   而 desktop 现在钉在 fork 上（品牌已提交）。我第一版把说明性注释放在 `win:` 与 `appId:` 之间，
   导致 overlay 的 block 规则**匹配失败并 fail-closed**——即 workflow 的 gate 2 会直接红。
   已把注释移到 `win:` 之上，并在 overlay 与 `electron-builder.yml` 双方都写明
   "不要在 `win:` 与 `appId:` 之间插入内容"。
   `scripts/ci/test-desktop-overlay-version.py` 27/27 通过。

### 2.4 静态复查残留（每条分类，不追求 zero result）

| 残留 | 类别 | 说明 |
| --- | --- | --- |
| `electron-builder.yml:45` 注释里的 `io.nekohasekai.sfw` | C 已记录 | 说明"从哪来"，不是值 |
| `apply-desktop-branding-overlay.py:277/290` | C 已记录 | **旧值**是规则的 anchor（迁移用）；新值在规则 new 文本里 |
| `prepare-desktop-client.sh:42` | C 已记录 | 说明 appId 已从"不动清单"移出 |
| `installationLayout.ts:20` 注释里的 `SOFTWARE\SagerNet\sing-box` | C 已记录 | 说明为什么**不能**读那个键 |
| `verify-desktop-identity-consistency.py:295` 的 legacy 字面量 | C 已记录 | 检查器本身的 legacy 检测列表 |
| `sing-box-daemon.exe` / `sing-box-daemon` 进程名 | A 安全契约 | 文件名，非身份 |
| `executableName: sing-box` | A 安全契约 | signer 认证按此路径解析 |
| `cli`/`linux` 下的 `sing-box-daemon.service` | D 本轮不改 | Linux systemd unit，属 41 节排除项 |

---

## 3. coexistence 实现

命名表（先列表后实施，全部围绕 `jiejiebox`）：

| resource | before | after | files | reason |
| --- | --- | --- | --- | --- |
| `applicationIdentity` | — | `jiejiebox` | `desktop/src/main/identity.ts` | 单一真源 |
| `win.appId` | `io.nekohasekai.sfw` | `io.github.piggycatbitshadow.jiejiebox` | `electron-builder.yml` | 派生卸载键/卸载器名 |
| `AppUserModelId` | `io.nekohasekai.sfw` | 同上 | `src/main/index.ts` | 任务栏/通知归属 |
| AppData | `%APPDATA%\sing-box` | `%APPDATA%\jiejiebox` | `applicationPaths.ts` | settings/profiles/session |
| install layout key | `SOFTWARE\SagerNet\sing-box` | `SOFTWARE\Jiejiebox\jiejiebox` | `identity.ts`, `installer.nsh` | installer 写 / runtime 读 |
| daemon data | `%ProgramData%\sing-box-daemon` | `%ProgramData%\jiejiebox-daemon` | `installer.nsh`, `installer-preflight.ps1`, `installationLayout.ts`, `cmd_service_windows.go` | SYSTEM 状态树 |
| service name | `sing-box-daemon` | `jiejiebox-daemon` | `boxdd/service_name.go` | SCM 键 + eventlog + service SID |
| service display/desc | `sing-box Service` | `Jiejiebox Service` | `cmd_service_windows.go` | 仅显示，与键分离 |
| named pipe | `...\Administrators\sing-box` | `...\Administrators\jiejiebox-daemon` | `boxdd/server_windows.go`, `desktop/src/main/daemon.ts` | 单 server 资源 |
| daemon `version` 前缀 | `sing-box-daemon version` | `jiejiebox-daemon version` | `boxdd/main.go`, `desktop/src/main/repair.ts` | 由常量派生 |
| installer state | `%APPDATA%\sing-box-installer` | `%APPDATA%\jiejiebox-installer` | `installer.nsh`, `installer-data.ps1` | 迁移状态互串 |
| install-id ADS | `:sing-box.installation-id` | `:jiejiebox.installation-id` | `installer-preflight.ps1`, `installer-data.ps1` | 目录归属标记 |
| Taildrop verb | `...\shell\Taildrop` | `...\shell\JiejieboxTaildrop` | `installer.nsh` | 卸载整键删除 |
| 注册的 URL scheme | `sing-box://` | `jiejiebox://`（**仍解析** `sing-box://`） | `identity.ts`, `src/main/index.ts` | 默认 handler 抢占 |
| `executableName` | `sing-box` | **不变** | — | signer 认证路径 |
| `productName` | `Jiejiebox` | **不变** | — | 已隔离 |
| artifact name | `Jiejiebox-v…` | **不变** | — | release 契约 |
| updater feed | fork releases | **不变** | — | 32 节要求 |

新增的守卫：

- `clients/desktop/src/main/identity.ts` — 每个名字+**为什么**+**哪些刻意不隔离**
- `clients/desktop/build/installer.nsh` 顶部 identity 说明块
- `scripts/ci/verify-desktop-identity-consistency.py` — 把 4 份副本钉在一起
  （TS / NSIS / PowerShell / Go 无法相互 import，只能在同时可见处断言）
- `clients/desktop/build/check-installer.nsh` — makensis 静态编译检查（两个 half）
- `build/installer-stop-service.ps1` — 只停自己的服务

---

## 4. quota 数据链

```text
HTTP response (main process, 已有 fetch)
   ↓  fetchRemoteProfile(url)
   ├─ 第一次请求 = 本客户端 UA，body 即正文
   ├─ response.headers.get("subscription-userinfo")
   │     ├─ 有 → parse → 权威快照，不重试
   │     └─ 无 → querySubscriptionQuota(url)   ← 只读 header，body 立即 cancel
   │             兼容 UA 仅 2 个（clash.meta, clash-verge/1.7.7），逐个尝试
   │             任一成功即返回；全部失败返回 undefined
   ↓  checkConfig(正文)         ← 唯一 gate，先校验后提交
   ↓  写正文（body 未变则跳过）
   ↓  UPDATE profiles SET last_updated
   ↓  applySubscriptionQuota   ← 在 body 分支**之外**
   ↓  profile_subscription_quota 表
   ↓  ProfileMetadata.subscription
   ↓  PROFILES_CHANGED → list() → IPC
   ↓  dashboard DesktopProfile.subscription
   ↓  SubscriptionQuotaInfo（仅 selected remote ProfileCard）
```

三类语义（数据链的核心）：

| 情况 | 行为 |
| --- | --- |
| header 完全缺失 | 保留 last-known 快照，**不动** `updatedAt` |
| header 存在且至少解析出一个字段 | 权威快照；未给字段**清空**（不继承旧 `expire`/`total`）；`updatedAt = now` |
| header 存在但全部字段非法 | 视为权威空快照（不保留旧值） |
| remote URL 改成另一个机场 | 删除旧快照，直到新 URL 报出自己的数字 |
| profile 删除 | 同事务删除 quota 行 |

parser 语义（`src/shared/subscription.ts`，无 Node/Electron API 以便测试）：
key 大小写容错、`=`/`;`/值两侧空白容错、未知 key 忽略、无 `=` 段忽略、重复 key 后者胜；
非法数字**丢弃而非修复**（`upload=abc` 不得变 0，`total=-1` 不得变 quota）；
拒绝非 `Number.isSafeInteger`、负数、>1 PiB（避免 >2^53-1 静默精度损坏）；
`expire` 保持**秒**，只在 UI 转 Date 时 `*1000`。

**fallback 的铁律**：retry **只**用于读一个 header，结构上不可能替换 body——
`querySubscriptionQuota` 从不读取 retry 的 body，直接 `body.cancel()`。
因此不存在"body=B"的代码路径。retry 失败 ≠ update 失败。

---

## 5. DB upgrade strategy：为什么老 `settings.db` 不会炸

现状：`database.ts` 只有 `CREATE TABLE IF NOT EXISTS`，**没有** schema version 表，**没有** ALTER 迁移框架。

因此本轮新增的是**独立表**，不是 5 个新列：

```sql
CREATE TABLE IF NOT EXISTS profile_subscription_quota (
  profile_id TEXT PRIMARY KEY,
  upload INTEGER, download INTEGER, total INTEGER, expire INTEGER,
  updated_at INTEGER NOT NULL
);
```

- 对已有的 `settings.db`，`CREATE TABLE IF NOT EXISTS` **天然安全**：老库直接长出这张新表。
- 如果加在 `profiles` 上，`CREATE TABLE IF NOT EXISTS profiles (...)` 对已存在该表的库是 **no-op**，
  于是：新装正常 → CI fresh DB 看不出来 → 正式升级 `UPDATE profiles SET subscription_total...`
  直接 `no such column`。这正是本轮要避免的雷，已被结构性排除。
- 没有外键：`PRAGMA foreign_keys` 默认关闭且本类从未打开，声明 FK 只会产生"写了但不生效"的约束；
  改由 `remove` 在同一事务里显式 `DELETE`。
- 读写全用 `INSERT ... ON CONFLICT(profile_id) DO UPDATE`（UPSERT），新库老库同一路径。

**已知未完成**：`npm run typecheck` 级别的验证**不能**证明老库升级，见第 11 节。
我无法在本环境构造"已有旧版 settings.db"的运行时验证（需要 Electron 主进程 + `node:sqlite`），
故此项**未执行**，明确留给第二阶段。

---

## 6. UI

```text
新顶级 Card = NO
新页面       = NO
新导航       = NO
新组件库     = NO
Tailwind     = NO
硬编码颜色   = NO（仅 var(--text) / var(--text-secondary) / var(--danger) / var(--weight-medium)）
CSS 行数     = 31
i18n 新增    = 4 条
```

`SubscriptionQuotaInfo` 挂在 **selected profile 的 ProfileCard** 主区域，位于 `profileMeta` 之下：

```text
我的订阅

🌐 远程   🕘 3 分钟前
剩余 734 GB / 1 TB
2026-12-31 到期
```

- **显示条件**：`selected profile is remote` **且** 快照中有至少一个可用事实。
- **无 quota 行为**：`return null` — **完全不显示**、不留空白占位、不显示 `0 B / 0 B`、不显示 "Unknown"。
- 只有 `upload+download` 没有 `total` → `已用 X`。
- 有 `total` → `剩余 X / Y`。
- `expire > 0` → 日期（`toLocaleDateString(language)`）；`expire = 0`/缺失 → 无日期（**不会**出现 1970-01-01）；
  已过期 → 加 `已到期` 且用 `--danger` 着色。
- `total <= 0` → 视为"无固定配额"，不生成百分比，不做除法。
- `used > total` → `remaining` clamp 到 0，不出现负流量。
- **没有**塞进 `ProfileInfo`：picker 每一行仍只有 type + last update，行高不变。
- 复用了现有 `useNow(60_000)`：到期状态在窗口开着时会自行从"到期"翻成"已到期"。

---

## 7. 修改文件清单

### dashboard（9 文件，+552 / -6）

新增：`src/app/subscriptionQuota.ts`、`src/app/subscriptionQuota.test.ts`、`src/lib/subscription.test.ts`
修改：`src/app/desktop.ts`、`src/app/translations.ts`、`src/views/ProfileViews.tsx`、
`src/views/ProfileViews.module.css`、`tsconfig.json`、`vite.config.ts`

### desktop（17 文件，+1078 / -57）

新增：`src/main/identity.ts`、`src/shared/subscription.ts`、`build/installer-stop-service.ps1`、`build/check-installer.nsh`
修改：`electron-builder.yml`、`src/main/applicationPaths.ts`、`installationLayout.ts`、`daemon.ts`、`index.ts`、
`repair.ts`、`database.ts`、`profiles.ts`、`src/shared/ipc.ts`、
`build/installer.nsh`、`installer-preflight.ps1`、`installer-data.ps1`、`dashboard`（gitlink）

### core（17 文件，+465 / -51）

新增：`experimental/boxdd/service_name.go`、`scripts/ci/verify-desktop-identity-consistency.py`
修改：`experimental/boxdd/main.go`、`server_windows.go`、`cmd_service_windows.go`、
`scripts/ci/apply-desktop-branding-overlay.py`、`prepare-desktop-client.sh`、
`verify-desktop-daemon-provenance.py`、
`scripts/ci/windows/GateHelpers.ps1`、`Test-DaemonService.ps1`、`Test-DaemonServiceRegister.ps1`、
`Test-InstallerLifecycle.ps1`、`Test-SecureServiceInstall.ps1`、
`.github/workflows/client-desktop-windows.yml`、`client-desktop-windows-runtime-audit.yml`、
`docs/WINDOWS-ACCEPTANCE.md`、`clients/desktop`（gitlink）

---

## 8. commit 清单

| repo | SHA | message |
| --- | --- | --- |
| dashboard | `d452c24ec959d1e30159b6cd5cce1fa48e7dc353` | `feat(ui): show subscription quota in the profile card` |
| desktop | `1603c4e4800d3eb4226a6ed428a81008ac690ddd` | `fix(windows): isolate the fork's application identity` |
| desktop | `93986e0398d6eb0f414ee404042293f5d31f279f` | `feat(subscription): persist subscription quota metadata` |
| core | `8a35f2ecf53a664c49aa91c306ef1f327f175b54` | `fix(windows): isolate the fork's daemon identity` |
| core | `5b06e5fc6f54838a35aae0bf0af0b5d2ea7db9f5` | `chore(desktop): pin the client that carries the identity and quota work` |

完整 SHA：

```text
dashboard d452c24ec959d1e30159b6cd5cce1fa48e7dc353  feat(ui): show subscription quota in the profile card
desktop   1603c4e4800d3eb4226a6ed428a81008ac690ddd  fix(windows): isolate the fork's application identity
desktop   93986e0398d6eb0f414ee404042293f5d31f279f  feat(subscription): persist subscription quota metadata
core      8a35f2ecf53a664c49aa91c306ef1f327f175b54  fix(windows): isolate the fork's daemon identity
core      5b06e5fc6f54838a35aae0bf0af0b5d2ea7db9f5  chore(desktop): pin the client that carries the identity and quota work
```

提交边界清晰：**共存 diff 与 quota diff 完全不混**（desktop 两个 commit 文件集互斥，见第 7 节清单），
core 的 `chore(desktop): pin` 独立于 daemon identity commit。

---

## 9. Action / Release 是否变化

| 项 | 结果 |
| --- | --- |
| 新增 workflow | **NO** |
| 新增 job | **NO** |
| 新增 build step | **NO** |
| 新增 build mode | **NO** |
| 新增第二套 package.ts | **NO** |
| 修改 release asset naming | **NO**（仍 `Jiejiebox-v<version>-windows-<arch>.exe`） |
| 修改 signing pipeline | **NO**（未触碰 afterPack/afterSign/signing.local.json 逻辑） |
| 弱化 security gate | **NO**（preflight / secure install / service SID / protected working dir / signer 认证全部保留；
仅新增一个更严格的"只停自己的服务"判断） |
| 修改并行构建策略 | **NO** |
| 修改缓存策略 | **NO** |
| 修改 x64/x86/arm64 策略 | **NO** |
| 修改 artifact 上传方式 | **NO** |
| 修改已存在的 workflow 内的标识符字面量 | **YES**（仅服务名/目录名/service name 字面量，见 2.3-6；无新增 step/job） |

---

## 10. 验证（实际执行的命令与结果）

全部在本机执行，非"应该通过"。

| # | 命令 | 结果 |
| --- | --- | --- |
| 1 | `python scripts/ci/verify-desktop-identity-consistency.py` | **PASS**（12 values compared） |
| 2 | `python scripts/ci/test-desktop-overlay-version.py` | **PASS**（27 passed, 0 failed） |
| 3 | `python scripts/ci/verify-desktop-daemon-provenance.py`（语法） | **PASS**（`ast.parse`） |
| 4 | `go build ./experimental/boxdd/...` | **PASS** |
| 5 | `go build ./...`（core 全量） | **PASS** |
| 6 | `go test ./experimental/boxdd/...` | **PASS**（no test files） |
| 7 | 构建 daemon 并执行 `version` | **PASS** → `jiejiebox-daemon version unknown / core api version 4` |
| 8 | `makensis` 编译 installer half（`check-installer.nsh`） | **PASS**（exit 0） |
| 9 | `makensis /DBUILD_UNINSTALLER` 编译 uninstaller half | **PASS**（exit 0） |
| 10 | `pnpm run test`（dashboard） | **PASS**（6 files, **69 tests**） |
| 11 | `pnpm run lint`（dashboard） | **PASS** |
| 12 | `pnpm run lint:css`（dashboard） | **PASS** |
| 13 | `pnpm run build`（dashboard，`tsc -b && vite build`） | **PASS** |
| 14 | `tsc -b`（desktop typecheck） | **PASS** |
| 15 | `pnpm run build`（desktop，electron-vite） | **PASS** |
| 16 | `git status --porcelain` × 3 repo | **clean** |
| 17 | `git submodule status --recursive` | **PASS**（pin 链闭合） |
| 18 | 静态复查 10 组旧 namespace 字面量 | **PASS**（残留全部归类，见 2.4） |

**NOT RUN（附原因）**：

| 项 | 原因 |
| --- | --- |
| `pnpm run package:win x64`（NSIS 安装包） | 本机 `node_modules/electron/dist/electron.exe` 不存在（安装时用 `ELECTRON_SKIP_BINARY_DOWNLOAD=1` 规避无网络），且 `tar` 缓存中无 NSIS 二进制解包 + 无网络下载 Electron。打包链在 `electron-builder` 之前即无法进行。 |
| Windows runtime audit workflow | 需要 GitHub Actions Windows runner + 管理员权限 + 签名证书 |
| 真机双安装验收矩阵 | 同上 |
| 老 `settings.db` → 新库升级运行验证 | 需要 Electron 主进程 + `node:sqlite` 运行时；本机无法构造。**这是 quota 最需要第二阶段确认的一项** |

> 说明：`pnpm run typecheck` 的**首次**运行曾因环境缺失而报错（`src/shared/gen/**` 未生成、
> dashboard `node_modules` 未安装）。执行 `pnpm run generate` + dashboard `pnpm install` +
> `pnpm run generate` 后，typecheck 与 build 全部通过。这不是代码问题，已一并排除。

---

## 11. coexistence 验收矩阵

本环境**无法**执行真机双安装。逐项给出状态与已有静态证据。

| 场景 | 状态 | 已有静态证据 / 缺口 |
| --- | --- | --- |
| A. official → fork install | **NOT RUN** | 安装目录不同（官方 per-user `%LOCALAPPDATA%\Programs\sing-box`，fork per-machine `C:\Program Files\sing-box`）；uninstall 键由 appId 派生，已分离；installer script makensis 编译通过 |
| B. fork → official install | **NOT RUN** | 同上，反向 |
| C. 双 GUI 同时打开 | **NOT RUN** | single-instance lock 随 `userData` 隔离（代码路径已核实，见 2.3-7） |
| D. 配置隔离 | **NOT RUN** | AppData 目录 + layout 注册表键已分离；两边读各自键 |
| E. 单边网络 | **NOT RUN** | 不测双 connected（本轮非硬要求）；服务名/pipe 已分离 |
| F. uninstall isolation | **NOT RUN** | 卸载脚本只删自己的 layout 键、自己的 service、自己的 Taildrop verb；`RMDir /r "$APPDATA\sing-box"` 只在 fork 自己目录为空时触发 |
| G. upgrade isolation | **NOT RUN** | `Stop-Service` 现带"是否本次安装树内"判断；service uninstall 只按自己的名字 |

**共同阻塞**：本机无法产出安装包（第 10 节 NOT RUN 表首行）。
**已在静态层面完成的证明**：identity map 全量核对 + 一致性检查器 + makensis 真实编译 + 静态复查分类。

---

## 12. quota 验收矩阵

| 场景 | 状态 | 证据 |
| --- | --- | --- |
| normal header | **PASS** | `subscription.test.ts` — `upload=1; download=2; total=10; expire=2000000000` |
| partial header | **PASS** | `download=2; total=10`；`expire=2000000000` 单字段 |
| mixed case / spaces | **PASS** | ` Upload = 1 ; DOWNLOAD=2 ; total = 10 ` |
| garbage | **PASS** | `upload=abc; total=-1; whatever=123` → `{}`，不 crash |
| no header | **PASS** | `parseSubscriptionUserInfo(null/undefined)` → `undefined`，与 `""`（存在但不可用）**区分** |
| fallback header | **PASS**（单元层） | 策略测过（有 header 不重试、非 200 不重试、列表有界）；**"最终 body=A"** 由 `querySubscriptionQuota` 从不读 retry body 结构性保证，但**集成层未运行** |
| unchanged body | **PASS**（代码层） | `applySubscriptionQuota` 位于 body 比较分支**之外**，注释写明"stable subscription 是常态"；**运行时未跑** |
| invalid body | **PASS**（代码层） | `await checkConfig(remote.content)` 在任何提交**之前**；抛错则 quota 不写、`last_updated` 不动 |
| URL change | **PASS**（代码层） | `remoteUrlChanged` → `quota ?? { quota: null }` → `deleteSubscriptionQuota` |
| old DB upgrade | **NOT RUN** | 结构上安全（新表 + `CREATE TABLE IF NOT EXISTS`，第 5 节），**必须留第二阶段实测** |
| delete profile | **PASS**（代码层） | `remove` 同事务 `DELETE FROM profile_subscription_quota` |
| UI no-data | **PASS** | `subscriptionQuota.test.ts` — `undefined` / `{updatedAt}` / 全非法字段 → `undefined`，组件 `return null` |
| UI total/expire | **PASS** | `Remaining X / Y`、`Used X`、`total<=0` 视为无配额、`used>total` clamp 0、`expire=0` 不出 1970、秒→日期正确（2033-05-18） |
| UI picker 行高 | **PASS**（设计约束） | quota 未进入 `ProfileInfo`，picker row 渲染路径未变 |

---

## 13. 风险 / 未完成

按重要性排序，不隐藏。

1. **安装包未产出（最高）。** 本机无 Electron 二进制、无网络，`package:win` 无法进行。
   因此 installer 的**运行时**行为（preflight 退出码、service SID ACL、数据迁移）全部未实测。
   已用 makensis 编译（两个 half 均 exit 0）覆盖"语法/符号"层，但**覆盖不了运行时语义**。

2. **老 DB 升级未实测。** 结构设计已针对该风险（独立表），但 `settings.db` 升级路径
   必须在第二阶段的真实 Electron 运行时确认：`应用能打开 / 原 profiles 保留 /
   新 quota storage 可用 / 没有 no such column`。

3. **安装目录 collision 是有条件的。** `electron-builder` 的 `APP_FILENAME` =
   `win.executableName`（本 fork 为 `sing-box`，因 `perMachine: true` 取 productFilename），
   所以 fork 装到 `C:\Program Files\sing-box`。**官方上游 `electron-builder.yml` 未设
   `perMachine`**（默认 per-user → `%LOCALAPPDATA%\Programs\sing-box`），因此当前不冲突。
   但若官方曾以 per-machine 方式安装，两者会争同一目录。**这是本轮唯一没有修、
   也按指引不能修的 collision**：修它必须动 `win.executableName`（安全链）或 NSIS 安装目录
   （指引禁止重写 NSIS installer）。第二阶段真机测试应把它作为**明确观察项**。

4. **`interactive rebase` 被用于整理 desktop 的两个 commit。** 该分支从未 push，
   所以不是改写共享历史；但若用户已从别处取过 `0984c33`/`5da7f60`，请丢弃旧值，
   以第 8 节 SHA 为准。

5. **`ciDiagnosticPath` 在 uninstaller half 产生 warning 6000。** 上游原生行为
   （该 Var 只在非 BUILD_UNINSTALLER 分支声明），非本轮引入，未修（会扩大 diff）。
   `check-installer.nsh` 的输出基线已记录，**新增** warning 才是回归信号。

6. **`scripts/ci/` 与外部 `tooling/` 仓库是两份。** CI 的 Windows workflow 实际调用
   `./tooling/scripts/ci/...`（独立仓库，本 checkout 中不存在），而本仓库
   `scripts/ci/` 下是同一批脚本的副本。我改了本仓库副本；**同一改动必须同步到 tooling 仓库**，
   否则 CI deep gate 仍会按旧服务名/旧注册表键判断。这是本阶段最需要紧接着做的一件事。

7. **未推送。** 三个 feature branch 仅在本地；远端备份（如用户需要）未做。

---

## 附：第二阶段 debug prompt 建议关注点

1. `pnpm run package:win x64` 真实产出 + `Test-InstallerLifecycle.ps1` / `Test-DaemonService.ps1` /
   `Test-SecureServiceInstall.ps1` 三个 gate。
2. 同步第 13.6 条的 tooling 仓库副本。
3. 老 `settings.db` → 新库升级（第 12 节唯一 NOT RUN 的功能项）。
4. 官方 + fork 真机双安装矩阵（第 11 节 A–G）。
5. 安装目录 collision 观察项（第 13.3 条）。
6. 用一个真实"只对 Clash UA 返回 `subscription-userinfo`"的机场验证 fallback 集成路径，
   并**确认最终 profile 正文来自第一次请求**。
