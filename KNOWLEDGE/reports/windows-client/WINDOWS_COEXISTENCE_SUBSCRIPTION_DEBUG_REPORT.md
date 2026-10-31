# Windows 共存 + 订阅剩余流量：定向 Debug 报告

指引来源：`C:\Users\Jie\Desktop\配置文件\02_WINDOWS_COEXISTENCE_AND_SUBSCRIPTION_DEBUG.md`

**结论：** 本轮发现并修复 1 个 BLOCKER（升级后遗留官方同名 service）、1 个 HIGH（卸载回退路径指向官方 AppData）和 2 个 MEDIUM（缺少 body 保留规则及旧库升级测试）。第 61 节列出的 13 条 blocker 标准均未触发，第 7–44 节已逐项检查，第 59 节安全检查项均为 **YES**。

安装包构建和真机双安装仍为 **NOT RUN**：当时主机缺少 Electron 二进制且无法联网。本轮补充了安装器脚本编译验证和旧库升级运行验证，但二者不能替代真机双安装验收。

---

## 1. Debug 基线

```text
core       feat/windows-coexistence-subscription-quota  959fa57ad34970469aa447d23d613f57ded70221
desktop    feat/windows-coexistence-subscription-quota  01d5ebedc9dae15e8cc1327aef317515b6426d09
dashboard  feat/windows-coexistence-subscription-quota  d452c24ec959d1e30159b6cd5cce1fa48e7dc353

base SHAs（第一阶段起点，均未改动）
core base       testing        abd75e0e2c3faae643e0c1b2645017446dd0b2e1
desktop base    jiejiebox/main 917d2a4e9864528e1c10c4c6a39f3949b6f5d878
dashboard base  main           3d32338fdc35b050cd9728afa7f66c18206e4fb9
```

三仓库 working tree **clean**，无未提交施工改动，无人在 main/testing 上施工。

### source identity 链（第 5 节逐项）

| 检查 | 结果 |
| --- | --- |
| core HEAD → `clients/desktop` gitlink == desktop feature commit | PASS `01d5ebe` |
| desktop HEAD → `dashboard` gitlink == dashboard feature commit | PASS `d452c24` |
| core / desktop / dashboard 均不在 detached HEAD | PASS（三个 worktree 都在 feature branch 上） |
| pin 指向的 commit 真实存在（`cat-file -t` = commit） | PASS ×2 |
| pin 是 feature branch 的祖先（可达，非临时 commit） | PASS ×2 |
| 没有把 desktop/dashboard 悄悄升到未审计最新 main | PASS（dashboard 仅 1 个 feature commit；desktop 仅 2 个） |
| core 用 exact core SHA 时可 recursive checkout 出完整图 | PASS（三层 gitlink 各自记录 child，child 都在自身 feature branch 上） |
| 是否已 push | **未 push**（三仓库 `ls-remote --heads origin` 均无该分支） |

不在 detached HEAD 上施工、不存在不可达 commit、不需要 remote 备份解析。

---

## 2. Diff 范围

### dashboard（9 文件，+552 / −6）

```text
A  src/app/subscriptionQuota.ts / .test.ts     D  quota UI 派生
M  src/app/desktop.ts                          C  DesktopProfile.subscription
M  src/app/translations.ts                     D  i18n
M  src/views/ProfileViews.tsx / .module.css    D  ProfileCard 扩展
A  src/lib/subscription.test.ts                F  tests
M  tsconfig.json / vite.config.ts              F  test 与 @shared alias
```

### desktop（19 文件，+1235 / −152）

```text
A  src/main/identity.ts                        A  identity 单一真源
M  electron-builder.yml                        A  win.appId
M  src/main/{applicationPaths,installationLayout,daemon,index,repair}.ts   A
A  build/installer-stop-service.ps1            A  只停自己的服务
A  build/check-installer.nsh                   F  makensis 静态检查
M  build/installer.nsh / -preflight.ps1 / -data.ps1   A
M  src/main/database.ts                        C  quota 表接入
M  src/main/profiles.ts                        B+C  fetch→persist→IPC
M  src/shared/ipc.ts                           C  ProfileSubscriptionQuota
M  src/shared/subscription.ts                  B  parser（+ 快照/状态机类型）
A  src/shared/subscriptionFetch.ts             B  HTTP 层（可测）
A  src/main/subscriptionStorage.ts             C  schema + SQL + 状态机（可测）
A  src/{shared,main}/*.test.ts                 F  23 cases
A  scripts/resolve-typescript.mjs / test-loader.mjs  F  node --test 解析
M  tsconfig{,.node,.web}.json / + tsconfig.test.json  F
A  build/installer-remove-legacy-service.ps1   A  升级清理（本轮新增）
M  package.json                                F  test script
M  dashboard（gitlink）                        E
```

### core（17 文件，+465 / −51）

```text
A  experimental/boxdd/service_name.go          A  serviceName 单一真源
M  experimental/boxdd/{main,server_windows,cmd_service_windows}.go  A
A  scripts/ci/verify-desktop-identity-consistency.py  F  契约检查
M  scripts/ci/{apply-desktop-branding-overlay,prepare-desktop-client,verify-desktop-daemon-provenance}  A
M  scripts/ci/windows/{GateHelpers,Test-DaemonService,Test-DaemonServiceRegister,Test-InstallerLifecycle,Test-SecureServiceInstall}  A
M  .github/workflows/client-desktop-windows*.yml  A  service/目录字面量
M  docs/WINDOWS-ACCEPTANCE.md                 A
M  clients/desktop（gitlink）                  E
```

```text
unrelated diff = NO
```

每个 changed file 都能归入 A–F。**无 `G. unrelated`**：没有格式化全仓、没有 README 大改、没有无关 cleanup、没有 Linux rebrand、没有协议/DNS/TUN 改动。第一阶段也没有误带无关文件，无需剥离。

> 注：`apply-desktop-branding-overlay.py` / `prepare-desktop-client.sh` / `verify-desktop-daemon-provenance.py` 属 A —— 它们的改动全部是 identity 契约（appId 规则、service name 在 `version` 输出里的前缀）。

---

## 3. 发现的问题

### BLOCKER-1 — 升级后遗留官方同名的 `sing-box-daemon` service

```text
symptom       fork 从 0.1.5 升级到本版本后，`Get-Service sing-box-daemon` 依然存在，
              指向 <install>\resources\daemon\sing-box-daemon.exe —— 一个刚被升级替换掉的路径。
              该服务是自动启动：每次开机都启动失败，并按 recovery action 反复重试。
              fork 卸载后它仍然留下（新 daemon 的 `service uninstall` 只删 jiejiebox-daemon）。
root cause    `serviceName` 从 sing-box-daemon 改成 jiejiebox-daemon 之后，
              "旧名字的 service" 只被 installer 的 Stop-Service 步骤停掉，
              从来没有任何一步去 unregister 它。这是一个 pure upgrade 路径缺陷：
              fresh install 不会出现，CI 也不会出现（CI 只装一次）。
              同族的第二处：`installer.nsh` 的 `customUnInstall` 只调用新 daemon 的
              `service uninstall`，对旧名字同样无能为力。
affected      install → upgrade（第二次安装）→ uninstall → 下次冷启动；重启后服务复活重试
lifecycle
fix           build/installer-remove-legacy-service.ps1（新增），在 customInstall 里
              位于 preflight 之后、新 service 注册之前：
                * 守门条件 = HKLM\SOFTWARE\SagerNet\sing-box 存在
                  （只有"改身份之前"的 fork 会写这个 key；官方客户端从不写它）
                * 第二道守门 = 该 service 的二进制必须落在本次安装树内
                * 停 + `sc.exe delete` + 清掉同名 event log source
                * 失败只 DetailPrint，不 Abort（新 service 是另一个名字，
                  留下残留比装坏产品好）
test          verify-desktop-identity-consistency.py 新增两项契约：
              (a) 每个 build/installer-*.ps1 都必须被 `File /oname=$PLUGINSDIR\...` stage
                  （未 stage = NSIS 在 build 期就解析不到 = 安装包里根本没有这个脚本，
                   运行期静默 no-op —— 正是本 bug 的形态）
              (b) legacy service 名与守门 key 必须是让守门正确的值，
                  且守门 key 不得等于当前 key（否则每次安装都会去动官方 service）
commit        78b25f4
```

### HIGH-1 — uninstall 回落路径会删除"官方客户端"的 AppData

```text
symptom       installer.nsh 的 customUnInstall 在 `$applicationDataDirectory == ""` 时执行
              `RMDir /r "$APPDATA\sing-box"`。那是 UPSTREAM 客户端的目录；
              本 fork 现在是 %APPDATA%\jiejiebox。这是整个 installer 里
              唯一一条能伸到"不属于自己的安装"里的破坏性路径。
root cause    该回落是第一阶段的路径默认值，身份改名后没有跟着改；
              而"layout 没写目录"其实意味着"本次安装从未创建过目录"，
              正确做法是什么都不删。
affected      uninstall（layout key 缺失或为空时）；跨产品数据破坏
lifecycle
fix           直接删掉该回落分支，并写明为什么"什么都不做"才是对的：
              有目录时 preflight 带 installation-id 校验去删；没有目录时本安装没建过。
test          checker 新增第 3 项契约：任何 `RMDir /r "<不是本产品目录>"` 直接 FAIL，
              匹配的是"命令里的目录名"而不是"命令本身"，换任何写法都挡得住
commit        78b25f4（与 BLOCKER-1 同一 commit —— 同属"installer 破坏性路径"这一类根因）
```

### MEDIUM-1 — body 保留规则（第 24 节最重要的雷）没有任何测试

```text
symptom       规则本身在第一阶段是对的（`querySubscriptionQuota` 从不读 retry 的 body），
              但它此前只存在于 Electron 主进程里，没有任何测试能锁住它；
              未来一次"顺手优化"就能把它改坏，而表现是用户订阅被静默换成另一套节点。
root cause    HTTP 层与数据库、daemon、Electron 耦合在 profiles.ts 里，无法单测。
fix           抽出 src/shared/subscriptionFetch.ts（只依赖 fetch/Headers/AbortSignal，
              不碰 Node 专属 API —— 顺带把 Buffer/Buffer.concat 换成
              TextDecoder/Uint8Array 与一个 base64 helper）。
test          subscriptionFetch.test.ts 11 cases，关键是
              "primary body = VALID_CONFIG_A / retry body = VALID_CONFIG_B" 用例：
              断言最终 content === A 且 !== B，同时 quota 来自 retry。
              另有 retry 抛错、retry 404、primary 非 200 不重试、auth 保留、大小上限。
commit        01d5ebe
```

### MEDIUM-2 — 老 `settings.db` 升级（第 28 节"必须实测"）此前未实测

```text
symptom       第一阶段的结构选择是对的（独立表 + CREATE TABLE IF NOT EXISTS），
              但"老库不会炸"只有论证，没有运行证据。这恰好是"只有正式升级才爆"的那一类。
root cause    建表 SQL 在 database.ts 里，而 database.ts 要 Electron 的 userData 路径，
              测试无法构造老库。
fix           抽出 src/main/subscriptionStorage.ts（schema + 3 条语句 + 状态机，
              接收一个已打开的 DatabaseSync）；database.ts 只负责 exec 它。
test          subscriptionStorage.test.ts 12 cases，用 node:sqlite（与 app 同一个模块）
              对真实 SQLite 文件跑生产语句：
                * 先建"改造前"的 schema（profiles/preferences/remote_servers，
                  无 quota 表），插入旧 profile 与旧 preference，再 exec 新 schema：
                  旧行保留、quota 读出 none（`no such table` 会在这里抛）
                * 之后第一次 quota 写入成功
                * 关掉再重开同一文件（"只有重启才炸"的形态）
                * 快照整体替换而非合并；0 与 absent 不混淆；1 PiB 边界不失精度
                * 三态状态机：无 header 保留快照与时间戳 / 有 header 整体替换 /
                  URL 变更清空且不可复活
commit        01d5ebe
```

### 判定为"不修、只记录"的项

| 项 | 判定 |
| --- | --- |
| `installer.nsh` uninstaller half 里 `ciDiagnosticPath` 的 warning 6000 | 上游原生行为（该 Var 只在非 `BUILD_UNINSTALLER` 分支声明），第一阶段未引入。修它会扩大 diff 且与身份无关。已在 `check-installer.nsh` 输出基线里记录：**新增** warning 才是回归信号 |
| `scripts/ci/` 与外部 `tooling/` 仓库是两份副本 | 第一阶段已记录。CI 实际调用 `./tooling/scripts/ci/...`（独立仓库，本 checkout 不存在），本仓库 `scripts/ci/` 是副本。**同一改动必须同步到 tooling 仓库**，否则 CI deep gate 仍按旧服务名判断 —— 这条仍是本阶段最需要紧接着做的事（见第 11 节） |
| 安装目录 collision | 条件性：`APP_FILENAME` 取自 `win.executableName`（fork = `sing-box`，per-machine），故 fork 装到 `C:\Program Files\sing-box`；官方未设 `perMachine`（默认 per-user）。若官方曾以 per-machine 安装才会争同一目录。修它必须动安全链或重写 NSIS，两者都被指引禁止 |

---

## 4. Windows identity 一致性矩阵（最终值）

| Identity | desktop build | desktop runtime | installer | core daemon | preflight / tests |
| --- | --- | --- | --- | --- | --- |
| productName | `Jiejiebox` | `app.getName()` → Jiejiebox | `${PRODUCT_NAME}` | — | — |
| executableName | `sing-box` | `process.execPath` 末段 | `$INSTDIR\sing-box.exe` | `applicationExecutableName="sing-box.exe"` | `--require sing-box.exe` |
| appId | `io.github.piggycatbitshadow.jiejiebox` | 同值 → AUMID | `${APP_ID}` → 卸载键 GUID | — | `Test-InstallerLifecycle` 按 InstallLocation 匹配 |
| AppUserModelId | （由 appId 派生） | `applicationUserModelId` | `WinShell::SetLnkAUMI ${APP_ID}` | — | — |
| AppData directory | 默认 `%APPDATA%\jiejiebox` | `applicationDataDirectoryName` | `$applicationDataDirectory` | — | `installer-data.ps1` 按 marker 校验 |
| install layout registry key | — | `installationRegistryPath` | `INSTALLATION_LAYOUT_REGISTRY_KEY` | — | `GateHelpers` 参数化；checker 断言相等 |
| daemon data dir | `%ProgramData%\jiejiebox-daemon` | `daemonDataDirectoryName` | `COMMON_APPLICATION_DATA\${DAEMON_DATA_DIRECTORY_NAME}` | `defaultServiceWorkingDirectory` | `installer-preflight.ps1` fallback 同值 |
| service internal name | — | `daemonServiceName`（dev socket 路径） | `DEFINE_SERVICE_NAME` | `serviceName = "jiejiebox-daemon"` | `$daemonServiceName`（SID 校验） |
| service display name | — | — | （由 daemon 写入） | `"Jiejiebox Service"` | 仅审计元数据，**从不用作键** |
| service description | — | — | — | `"Privileged service for Jiejiebox"` | — |
| event log source | — | — | （由 `service uninstall` 清） | `eventlog.InstallAsEventCreate(serviceName)` | — |
| service SID | — | — | — | `windowsServiceSID()` = SHA-1(大写 serviceName) | `Get-ServiceSid $daemonServiceName` |
| daemon pipe | — | dev 回落 `...\Administrators\${daemonServiceName}` | — | `daemonPipePath = ...\Administrators\` + serviceName | — |
| worker relay pipe | `\\.\pipe\sing-box-worker.<uuid>` | 同（随机，已隔离） | — | `workerPipePrefix` 校验前缀 | 无需改（随机即隔离） |
| Taildrop shell verb | — | — | `...\shell\JiejieboxTaildrop` 写/删同键 | — | checker 断言 |
| transition state | — | — | `...\jiejiebox-installer\data-transition.json` | — | `installer-data.ps1` 自我校验路径 |
| installation-id marker | — | 目录内 `.access-control`（不同物） | `:jiejiebox.installation-id` | — | `installer-preflight.ps1` 同值 |
| URL scheme | — | 注册 `jiejiebox://`，**仍解析** `sing-box://` | — | — | — |
| file association | `bpf` + mimeType `application/x-sing-box-profile` | — | electron-builder `FileAssociation.nsh`，按扩展名 | — | **共享，已接受** |
| login item | — | `setLoginItemSettings` 值名 **默认 = AUMID** → 天然隔离 | — | — | 见下 |
| 安装目录 | `APP_FILENAME` = `sing-box` | — | `$INSTDIR` | — | 见第 11 节风险 |

### 第 7–22 节逐项

| 节 | 项 | 结论 |
| --- | --- | --- |
| 7 | executable auth | **PASS**。`executableName` 未改（`sing-box`），`applicationExecutableName` 未改，`afterPack.cjs` 仍签 `daemon/sing-box-daemon.exe`，`afterSign.cjs` 用 `appInfo.productFilename`（= `sing-box`）动态解析 —— installer / service install / peer handshake / repair / runtime audit 全部同一真实路径。**没有用关闭 signer check 的方式修任何东西** |
| 8 | appId / AUMID 半改 | **PASS**。全仓搜 `io.nekohasekai.sfw` 仅剩 4 处，全部是注释/规则 anchor（见第 60 节）。`setAppUserModelId(applicationUserModelId)` 与 `win.appId` 同源 |
| 9 | single instance | **PASS（代码路径）**。`configureApplicationPaths()`（内部 `app.setPath("userData", …)`）在 `requestSingleInstanceLock()` **之前**；Electron 的 lock 由 userData 派生 ⇒ official singleton ≠ fork singleton，而 fork 第二个实例仍会聚焦已有窗口。**未删除 fork 自己的 singleton 保护**。真机双 GUI 实测见第 5 节（NOT RUN） |
| 10 | layout registry 只改一端 | **PASS**。NSIS 写 / runtime 读 / uninstall 删 / 升级读 / preflight 期望五处同值，checker 断言。附带发现并修正了"NSIS 用 `$APPDATA` 而 PS fallback 用 `CommonApplicationData`"的**三处不一致**（第一阶段已修） |
| 11 | daemon data 默认值 | **PASS**。`applicationPaths`（dev 与 packaged fallback）、installer 默认、installer 保存值、core `defaultServiceWorkingDirectory`、repair `--working-directory`（来自 `applicationPaths().daemonData`）、preflight 期望 ACL、runtime 工作目录 —— 全部 `%ProgramData%\jiejiebox-daemon`。development 路径策略不同但不落盘到该目录（dev 用 `bin/`） |
| 12 | service 不只改 Go const | **PASS**。追踪了 `serviceName` / display / description / SCM create-open-delete / `Get-Service` / `Stop-Service` / `eventlog.Install`+`Remove` / SID / ACL / preflight / runtime audit / installer cleanup。全仓搜索 `Get-Service -Name "sing-box-daemon"` 与 `sc.exe delete "sing-box-daemon"` 均 **0 命中**。**"Go 改名而 installer 仍停旧名"这一隐藏炸点已消除**（第一阶段接入 `installer-stop-service.ps1` 时就已同源，本轮补上旧版本清理） |
| 13 | service SID / ACL | **PASS**。`serviceName` 变更 ⇒ `windowsServiceSID()`（SHA-1 over 大写名）自动变更；preflight 的 `$daemonServiceName` 同值派生同一 SID；preflight 期望 3 条 ACE 与 `secureWindowsWorkingDirectory` 写的 3 条一致。**ACL validation 未被删除或放宽** |
| 14 | event log source | **PASS**。install/update 走 `eventlog.InstallAsEventCreate(serviceName)`，uninstall 走 `eventlog.Remove(serviceName)` —— 都是 fork 名字，**不会 Remove 官方的 source**。新增的 legacy 清理也只移除同名 `sing-box-daemon` source，且仅在该安装确实是"改身份前的 fork"时 |
| 15 | daemon pipe + worker relay | **PASS**。连接图：renderer → IPC → main → application worker（随机 `\\.\pipe\sing-box-worker.<uuid>`）→ daemon relay（同一随机 pipe）→ daemon listen `...\Administrators\jiejiebox-daemon`。core `listenEndpoint` 拒绝自定义路径（只接受默认），dev 直接路径与之一致。**随机 worker pipe 未被 rebrand**（无需改） |
| 16 | Taildrop shell verb | **PASS**。install 写 `...\shell\JiejieboxTaildrop`，uninstall 删**同一个键**，不 touch `Taildrop`。满足"一边卸载不破坏另一边" |
| 17 | URL scheme / `.bpf` | **PASS + 记录边界**。scheme 改为注册 `jiejiebox://`；`parseDeepLink` 仍规范化接受 `sing-box://`（含 `sing-box://import-remote-profile?url=…#name`，`deepLinkFromArguments` 亦匹配两种写法）⇒ 其它 sing-box 客户端分享的链接仍可用，且不再抢官方默认 handler。`.bpf` 仍共享：electron-builder 的 `FileAssociation.nsh` 按扩展名写 ProgId 并在卸载时**只在该 ProgId 仍属于自己时**删除 ⇒ 不会删对方的 association；默认打开者最后安装者胜 —— **已知边界，符合第 62 节** |
| 18 | login item | **PASS，无需改**。Electron 文档（`electron.d.ts` `interface Settings`）：`name` 是 "value name to write into registry. Defaults to the app's AppUserModelId()"，而 `loginItem.ts` 不传 `name` ⇒ Run 项 / 任务管理器启动项 / `StartupApproved` 全部按 AUMID 键控，随 appId 天然隔离。`migrateLoginItem()` 只读写本客户端自己的项，**无法看到或关闭官方的启动项** |
| 19 | transition state / installation-id | **PASS**。state 目录改为 `%APPDATA%\jiejiebox-installer`（并顺带修正"NSIS 写 `$APPDATA` 而 PS 校验 `CommonApplicationData` ⇒ 该校验此前永远不可能通过"）；marker 改为 `:jiejiebox.installation-id`。生命周期推演：官方存在 + fork fresh install ⇒ `MigrateLegacyApplicationData=1` 且 `PreviousApplicationDataDirectory` 为空 ⇒ `Resolve-LegacyApplicationDataProfile` 找的是官方**旧**目录名 `%APPDATA%\sing-box-for-desktop`，官方现用 `%APPDATA%\sing-box` ⇒ 返回 null ⇒ **不会把官方目录当 legacy source**。fork 升级 ⇒ layout 存在 ⇒ 另一分支，按 previous 目录 + installation-id 校验。中断恢复（`Recover`）只读 fork 自己的 state 路径 |
| 20 | updater regression | **PASS**。`RELEASES_URL` 仍 `Piggy-Cat-bit-shadow/sing-box/releases`，asset matcher 仍 `Jiejiebox-v` 前缀。全仓搜 `SagerNet` 在 release source 无命中。**未触碰 updater** |
| 21 | release asset contract | **PASS**。`scripts/package.ts` 的 Windows artifactName 仍 `Jiejiebox-v${version}-windows-${arch}.exe`；`release-artifacts.sh` 按该精确名选择（`Jiejiebox-v*-windows-x64.exe`）；`updates.ts` 的 `findWindowsAsset` 用同一前缀。x64/x86/arm64 三档的 `artifactArchitecture` 未被身份改动触及 |
| 22 | package.ts / afterSign 动态路径 | **PASS**。`afterSign.cjs` 用 `context.packager.appInfo.productFilename`（动态）拼可执行文件路径 —— 未改成硬编码。`afterPack.cjs` 仍签 `["daemon","sing-box-daemon.exe"]` 与 `["native","windows_share.node"]`（文件名未变，故仍正确）。`signing.local.json` 判定逻辑未动 |

---

## 5. coexistence 验收

| 项 | 状态 | 证据 / 缺口 |
| --- | --- | --- |
| official → fork install | **NOT RUN** | 无法产出安装包。静态证据：卸载键由 appId 的 UUIDv5 派生（互异）；installer 脚本 makensis 编译通过；无 `RMDir` 指向非本产品目录 |
| fork → official install | **NOT RUN** | 同上，反向 |
| dual GUI | **NOT RUN** | single-instance lock 随 userData 隔离（代码路径已核实，见第 4 节第 9 项） |
| config isolation | **NOT RUN** | AppData 目录、layout 键、daemon data 三处已分离；两边读各自键 |
| service isolation | **NOT RUN** | service name / SID / eventlog / display 全部分离；`Stop-Service` 带归属判断；升级清理带双重守门 |
| pipe isolation | **NOT RUN** | daemon pipe 名随 serviceName 派生，官方与 fork 互异 |
| uninstall isolation | **NOT RUN** | 卸载只删自己的 layout 键、自己的 service、自己的 Taildrop verb；**指向官方 AppData 的回落已删除**；官方 service/event source 不在任何卸载路径上 |
| upgrade isolation | **NOT RUN** | 新增 legacy service 清理（双重守门）；`Stop-Service` 只停本次安装树内的服务 |

**共同阻塞**：本机无 `node_modules/electron/dist/electron.exe`、无网络 ⇒ `pnpm run package:win x64` 在 electron-builder 之前即无法进行。
**本阶段新增的静态/编译证据**：installer 与 uninstaller 两个 half 的 `makensis` 真实编译（均 exit 0）；identity 契约检查 14 项值 + 3 组结构契约。

---

## 6. quota 状态机验收

| 项 | 状态 | 证据 |
| --- | --- | --- |
| normal header | **PASS** | `subscription.test.ts` + 状态机测试（有 header 整体替换、时间戳前进） |
| partial header | **PASS** | `download=2; total=10` 解析；存储层"整体替换而非合并"断言 upload/expire 被清空 |
| header 存在但全字段非法 | **PASS** | 与"完全缺失"**区分**：`{}` 是权威空快照（清空），`undefined` 保留 —— 两侧都有测试 |
| no header | **PASS** | 保留快照 + `updatedAt` 不变（更新 1 有 header、更新 2 无 ⇒ 内容新鲜而 quota 陈旧，正是第 27 节场景） |
| fallback | **PASS** | 单元层：retry 拿到 header、body 仍是 primary |
| **primary body preserved** | **PASS** | `subscriptionFetch.test.ts`：retry 返回 `VALID_CONFIG_B` 时断言 `content === A` 且 `!== B`。结构上保证（`querySubscriptionQuota` 从不读 retry body，直接 `cancel()`） |
| fallback fail | **PASS** | retry 抛错 / 404 / 无 header 三种，均返回 `undefined`，更新成功且 quota 不变 |
| unchanged body | **PASS（代码层 + 注释）** | `applySubscriptionQuota` 位于 `oldContent !== remote.content` 分支**之外**、体写入之后。运行时 e2e 未跑 |
| invalid config | **PASS（顺序层）** | `await checkConfig(remote.content)` 在任何提交**之前**且是唯一 gate；抛错则 body/quota/`last_updated` 全不动 |
| old DB | **PASS（本轮新增，真实 SQLite）** | 见 MEDIUM-2。含"改造前 schema → 新 schema → 读 none → 首次写入 → 关掉重开"完整序列 |
| URL change | **PASS** | 状态机测试：`{quota:null}` 清空且后续 `undefined` 不可复活；`updateMetadata` 在 `remoteUrlChanged` 时 `quota ?? {quota:null}` |
| delete | **PASS** | 同事务 `deleteSubscriptionQuota(store, id)`；测试断言只删目标行 |
| auto update | **PASS（同一条代码路径）** | `runDueProfileUpdates` → `updateRemoteProfile`（与手动 Update 同一个函数、同一个 `runProfileOperation` 串行化）⇒ quota 采集必然一致；`PROFILES_CHANGED` 在该函数末尾统一发出 |
| cold start | **PASS（存储层）** | 关闭并重开同一 `settings.db` 后数据仍在；layout 由注册表持久化，runtime 启动时读回（代码路径已核实） |

**未跑**：任何需要 Electron 主进程整体启动的 e2e（无二进制、无网络）。

---

## 7. UI

| 项 | 状态 | 说明 |
| --- | --- | --- |
| 新顶级 Card / 新页面 / 新导航 / 新组件库 | **NO** | 只扩展现有 `ProfileCard` |
| no quota | **PASS** | `subscriptionQuotaDisplay` 返回 `undefined` ⇒ 组件 `return null`：不显示、不占位、不显示 `0 B / 0 B`、不显示 Unknown |
| quota | **PASS** | `Remaining X / Y`（`--weight-medium` + `--text`），到期行 `--text-secondary` |
| partial quota | **PASS** | 只有用量 ⇒ `Used X`；只有 expiry ⇒ 只显示到期行 |
| expired / no-expire | **PASS** | `expire=0`/缺失 ⇒ 无日期（不会出现 1970-01-01）；已过期 ⇒ `Expired {date}` 且 `--danger` |
| used > total | **PASS** | `remaining = max(total-used, 0)`，测试断言为 0 且格式化为 `Remaining 0 B / 1 KB` |
| total <= 0 | **PASS** | 视为"无固定配额"，不生成百分比、不做除法 |
| dark/light | **PASS（token 层）** | CSS 只用 `var(--text)` / `var(--text-secondary)` / `var(--danger)` / `var(--weight-medium)` / `var(--space-*)` / `var(--text-sm)`；stylelint `color-no-hex` + `declaration-strict-value` 通过 ⇒ **不可能**有硬编码颜色或尺寸 |
| picker unaffected | **PASS** | quota **未**进入 `ProfileInfo`；picker row 渲染路径未改，行高不变 |
| stale quota | **PASS** | UI **不**用 `profile.lastUpdated` 冒充 quota freshness；`updatedAt` 独立存储且不参与渲染（只在无数据判定中使用快照存在性） |

---

## 8. 安全

| 项 | 结果 |
| --- | --- |
| daemon executable auth 仍启用 | **YES** —— `initializeServerIdentity()` 未被触碰，`authenticodeSigner` 校验仍在 |
| same-signer expectation 仍启用 | **YES** —— worker/daemon 与 application/daemon 两处 `bytes.Equal(...Signer)` 均保留 |
| installer preflight 仍启用 | **YES** —— `customInit`/`customInstall`/`customUnInstall` 三处 preflight 调用全部保留；退出码映射未改 |
| service working dir ACL 仍严格 | **YES** —— `applyProtectedTree` 与 `validateWindowsWorkingDirectory`（要求 owner=SYSTEM、DACL protected、恰好 3 条 ACE）未改；仅 SID 随 serviceName 派生 |
| named pipe ACL 仍严格 | **YES** —— `pipeSecurityDescriptor` 与 `ProtectedPrefix\Administrators\` 前缀未改，仅前缀下的名字改为 fork 名 |
| unsafe installation 没被默认打开 | **YES** —— `$allowUnsafeInstallation` 仍初始化为 0，仍由用户显式确认后才置 1 |
| signing pipeline 没绕过 | **YES** —— `signtoolOptions` / `forceCodeSigning` / `afterPack` / `afterSign` 逻辑未改；`forceCodeSigning: false` 是第一阶段既有的 unsigned-build 设计（证书存在时 `package.ts` 会重新打开为 true） |
| 为过测试禁用证书/校验 | **NO** —— 未发生 |

---

## 9. build / CI

全部本机实跑，非"应该通过"：

| # | 命令 | 结果 |
| --- | --- | --- |
| 1 | `python scripts/ci/verify-desktop-identity-consistency.py` | **PASS**（14 values + 3 contract groups） |
| 2 | `python scripts/ci/test-desktop-overlay-version.py` | **PASS**（27 passed, 0 failed） |
| 3 | `python -c "ast.parse(verify-desktop-daemon-provenance.py)"` | **PASS** |
| 4 | `go build ./...`（core 全量） | **PASS** |
| 5 | `go test ./experimental/boxdd/...` | **PASS** |
| 6 | `makensis check-installer.nsh`（installer half） | **PASS**（exit 0） |
| 7 | `makensis /DBUILD_UNINSTALLER check-installer.nsh`（uninstaller half） | **PASS**（exit 0） |
| 8 | `pnpm run test`（dashboard） | **PASS**（6 files, 69 tests） |
| 9 | `pnpm run lint`（dashboard） | **PASS** |
| 10 | `pnpm run lint:css`（dashboard） | **PASS** |
| 11 | `pnpm run build`（dashboard） | **PASS** |
| 12 | `pnpm run test`（desktop，**本轮新增**） | **PASS**（3 files, 23 tests） |
| 13 | `pnpm run typecheck`（desktop） | **PASS** |
| 14 | `pnpm run build`（desktop） | **PASS** |
| 15 | `git status --porcelain` × 3 repo | **clean** |
| 16 | `git submodule status --recursive` | **PASS**（链闭合） |
| 17 | 旧 namespace 静态复查 10 组 | **PASS**（残留全部归类，见第 10 节） |

**NOT RUN**：`pnpm run package:win x64`（Layer 5）、`client-desktop-windows*.yml`（Layer 6）、真机双安装矩阵。原因见第 5 节。

**未新增 workflow / job / build step**：Layer 6 未执行，也未改造。

---

## 10. Action / Release contract

```text
新增 workflow            NO
新增 job                 NO
新增 build step          NO
新增 build mode          NO
修改 asset naming        NO   （仍 Jiejiebox-v<version>-windows-<arch>.exe）
修改 signing pipeline    NO
修改 parallel build      NO
修改缓存策略             NO
修改 x64/x86/arm64 策略  NO
修改 release 上传方式    NO
弱化 security gate       NO
修改已存在 workflow 内的标识符字面量  YES（仅服务名/scratch 目录名/ProgramData 目录名，
                                     无新增 step/job；见下）
```

workflow 内改动逐条（`client-desktop-windows.yml`、`client-desktop-windows-runtime-audit.yml`）：

```text
SERVICE_WORK_DIR            C:\ProgramData\sing-box-daemon-ci-smoke → jiejiebox-daemon-ci-smoke
Test-SecureServiceInstall   -WorkingDirectory ...\sing-box-daemon     → ...\jiejiebox-daemon
cleanup: Get-Service -Name  "sing-box-daemon"                          → "jiejiebox-daemon"
cleanup: sc.exe delete      "sing-box-daemon"                          → "jiejiebox-daemon"
cleanup: 残留目录列表        C:\ProgramData\sing-box-daemon              → jiejiebox-daemon
```

这些是**运行时审计清理步骤**里的字面量：不改则卸载/清理找不到 fork 自己的服务，"下一次运行的前置条件"会失败。无任何 step 被新增、删除或重排。YAML 解析通过。

---

## 11. 剩余风险

1. **安装包与真机验收未跑（最高）。** 本机无 Electron 二进制、无网络。installer 的**语法/符号**层已由 makensis 真实编译覆盖（两个 half），但 preflight 退出码、service SID ACL 生效、数据迁移的实际文件操作、以及双安装/双 GUI/双向卸载/升级隔离全部未实测。

2. **`scripts/ci/` 与 `tooling/` 仓库副本未同步（紧接第一件该做的事）。** CI 的 Windows workflow 调用 `./tooling/scripts/ci/...`（独立仓库，本 checkout 不存在）。我改的是本仓库 `scripts/ci/` 下的副本；`GateHelpers.ps1`、四个 Windows gate、`audit-windows-installer-payload.sh`、`verify-desktop-package-integrity.cjs` 的对应改动**必须同步到 tooling 仓库**，否则 CI deep gate 仍按 `sing-box-daemon` / `SOFTWARE\SagerNet\sing-box` 判断，会在正确安装上失败。

3. **安装目录 collision 是有条件的。** `APP_FILENAME` 取自 `win.executableName`（fork = `sing-box`，`perMachine: true`）⇒ fork 装到 `C:\Program Files\sing-box`；官方未设 `perMachine`（默认 per-user ⇒ `%LOCALAPPDATA%\Programs\sing-box`），当前不冲突。若官方曾以 per-machine 安装则争同一目录。修它必须动 `win.executableName`（安全链）或重写 NSIS 安装目录（指引禁止），故本轮按指引不动，作为真机测试的**明确观察项**。

4. **`ciDiagnosticPath` warning 6000** 在 uninstaller half 存在（上游原生）。已记录基线；**新增** warning 才是回归信号。

5. **历史整理说明。** 第二阶段的 4 个 commit 从未 push；其中 desktop 的测试 commit 与 core 的 pin commit 各被 soft-reset/amend 过一次（把后续补充折回同一 root cause，避免出现"同一件事三个 commit"）。若曾从别处取过中间 SHA，请以第 1 节与本报告末尾的 SHA 为准。

6. **未推送。** 三个 feature branch 仍仅在本地。

---

## 附：commit 清单（40 位全值）

```text
dashboard  d452c24ec959d1e30159b6cd5cce1fa48e7dc353  feat(ui): show subscription quota in the profile card

desktop    1603c4e4800d3eb4226a6ed428a81008ac690ddd  fix(windows): isolate the fork's application identity
desktop    93986e0398d6eb0f414ee404042293f5d31f279f  feat(subscription): persist subscription quota metadata
desktop    78b25f46ffc9791e3e5f92c6398243835ba68f37  fix(windows): remove the previous version's daemon service on upgrade
desktop    01d5ebedc9dae15e8cc1327aef317515b6426d09  test(subscription): cover the remote fetch, the state machine and the upgrade

core       8a35f2ecf53a664c49aa91c306ef1f327f175b54  fix(windows): isolate the fork's daemon identity
core       2b2cf48b9ae7728f1835b12ca463d6e87e5b6fdc  test(ci): check installer staging, the legacy guard, and destructive paths
core       5b06e5fc6f54838a35aae0bf0af0b5d2ea7db9f5  chore(desktop): pin the client that carries the identity and quota work
core       959fa57ad34970469aa447d23d613f57ded70221  chore(desktop): pin the client with the debug-round fixes
```

第二阶段新增（本轮）：

```text
desktop  78b25f46f  fix(windows): remove the previous version's daemon service on upgrade
desktop  01d5ebedc  test(subscription): cover the remote fetch, the state machine and the upgrade
core     2b2cf48b9  test(ci): check installer staging, the legacy guard, and destructive paths
core     959fa57ad  chore(desktop): pin the client with the debug-round fixes
```

按第 63 节要求：每个 fix 独立、以 root cause 命名，无 "fix bugs" 这类含糊 commit，未 squash 成一条，未重写第一阶段已提交的历史（第一阶段的 3 个 commit SHA 与第一阶段报告完全一致）。

---

## 附：第 60 节最终 grep 审计

旧 upstream identity —— 对每个剩余结果标注：

| 字面量 | 命中 | 标注 |
| --- | --- | --- |
| `io.nekohasekai.sfw` | 4 | **DOC/TEST**：`electron-builder.yml` 注释、overlay 的 anchor 与说明、`prepare-desktop-client.sh` 说明。**0 个是取值** |
| `Software\SagerNet\sing-box` / `SOFTWARE\SagerNet\sing-box` | 2 | **INTENTIONAL COMPAT**：`installationLayout.ts` 注释（说明为何不读该键）、overlay anchor。另有 `installer.nsh` 的 `LEGACY_INSTALLATION_LAYOUT_REGISTRY_KEY` —— 这是**升级守门键**，必须保留，checker 断言其值 |
| `C:\ProgramData\sing-box-daemon` | 0 | — |
| `ProtectedPrefix\Administrators\sing-box` | 0 | — |
| `Get-Service -Name "sing-box-daemon"` | 0 | — |
| `Stop-Service -Name "sing-box-daemon"` | 0 | — |
| `sc.exe delete "sing-box-daemon"` | 0 | — |
| `sing-box.installation-id` / `sing-box-installer` | 1 | **TEST**：checker 的 legacy 检测列表 |
| `shell\Taildrop"` | 0 | — |
| `:sing-box.installation-id` | 0 | — |

quota 关键词（`subscription-userinfo` / `subscription` / `upload` / `download` / `total` / `expire` / `updatedAt`）：集中在 `shared/subscription.ts`、`shared/subscriptionFetch.ts`、`main/subscriptionStorage.ts`、`main/profiles.ts`、`shared/ipc.ts` 与 dashboard 三处；无第二套 quota 状态存储（第 43 节 PASS —— `useDesktopProfiles` 是唯一来源，UI 只派生）。
