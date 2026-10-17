# Windows 共存最终收尾报告

指引来源：`C:\Users\Jie\Desktop\配置文件\WINDOWS_COEXISTENCE_FINAL_CLOSEOUT.md`

两个确认的剩余问题都**已解决并有可执行证据**。上一轮报告中两条结论按 §12 要求**已删除并更正**。

---

## 修改摘要

只说明本轮收口改了什么。

### 任务 A —— 安装目录 identity 隔离

**先验证前提，不盲信。** 两条都用 GitHub 只读检索核实过：

```text
上游 SagerNet/sing-box-for-desktop  electron-builder.yml → nsis.perMachine: true   ← 确认
上游 build/installer.nsh 存在，且与本 fork 的 installer.nsh 同源                ← 确认
上游同样使用 Software\SagerNet\sing-box 作为 installation layout key           ← 确认
```

所以上一轮的两条结论都不成立，必须处理。

**collision 的机制。** electron-builder 的 NSIS `APP_FILENAME` 在 per-machine assisted 安装下取 `appInfo.productFilename`，而 `productFilename = platformSpecificOptions?.executableName ?? info.config.executableName ?? productName`（已读 `app-builder-lib/out/appInfo.js` 构造函数确认）。它跟随**可执行文件名**，不是 productName —— 所以 fork 必要的 `win.executableName: sing-box` 让两个产品同时落到：

```text
C:\Program Files\sing-box
```

**为什么不能从配置改。** `APP_FILENAME` 是 makensis **命令行**参数，而 NSIS 中重复 `!define` 是**致命错误**而非覆盖 —— 在 include 里写 `!define APP_FILENAME` 不会赢，会**直接编译失败**。这条是用 makensis 实测出来的（见"新发现"第 2 条），不是推断。

**实际做法。** 二者必须解耦，而只有目录能移动（`executableName` 是安全链）。改 `$INSTDIR`：`initMultiUser` 在 `.onInit` 里把它设为 `<program files>\${APP_FILENAME}`，`customInit` 紧随其后且在任何页面之前 —— 新宏 `chooseForkInstallationDirectory` 就在这里替换派生默认值，其他来源一律不动：

```text
旧安装自己记录的 InstallLocation          优先采用（升级仍是升级）
/D=<path>                                  不动
electron-builder 派生的默认值              替换为 <program files>\Jiejiebox
```

新宏**不做任何路径解析**，用与 multiUser.nsh 相同的 `$PROGRAMFILES` / `$PROGRAMFILES64` / `APP_64` / `RunningX64` 条件重建默认值。

### 任务 B —— legacy service cleanup 的 ownership proof 重做

**上一轮的 guard 是错的。** 用 `HKLM\SOFTWARE\SagerNet\sing-box` 当"只有旧 fork 会写"的证明 —— 但上游自带 `build/installer.nsh` 且本 fork 的 installer.nsh 由它派生，**上游写同一个 key**。而这一步删的是名为 `sing-box-daemon` 的 service，**正是上游 daemon 用的名字**。所以在同时装了官方与 fork 的机器上，它可能删掉官方的 service。

**重新找证据（按 §6 要求从历史 commit 与旧版本行为里找，不机械采用第一项）。**

从历史 commit 核实：pre-identity fork（`917d2a4`）的 `applicationPaths.ts` 用 `%APPDATA%\sing-box`，`userDataSecurity.ts` 在 **每个 packaged Windows 启动**写 `<userData>\.access-control`（`app.isPackaged` 判断之后，非测试专用）。上游任何版本都不写这个文件 —— 它属于本 fork，目录是旧 fork 用的那个，且**升级替换的是应用树、不是用户数据**，所以升级后仍然存在。

因此 ownership proof 是：

```text
%APPDATA%\sing-box\.access-control 存在
```

**Fail-closed，四项全过才删。** 判定逻辑抽到 `installer-legacy-service-ownership.ps1`，所有来自机器的信号都作为参数传入（因此可测）：

```text
service 存在
service image path 可解析
image 位于 <root>\resources\daemon\sing-box-daemon.exe 布局（experimental/boxdd 要求的形状）
root 在固定卷上
marker 存在            ← ownership proof
```

任一项无法评估即 skip，并把 reason 打印出来。**升级/安装不会因为 cleanup 失败而不可用**（只 DetailPrint，不 Abort）。

**§7 的两道 guard 重新评估。** 旧的"binary 必须在本次安装树内"已**删除**：历史 fork 装在旧目录、新版本装在自己的目录，要求它在**新**树内会恰好拒绝本功能存在的理由。现在用的是"形状 + 固定卷 + marker"三者，能够区分 old fork tree / new fork tree / official tree。

**卸载方向也补上。** 升级会清掉旧 service，但"装过 0.1.5、之后直接卸载本版本"这条路径下 `service uninstall` 只删 `jiejiebox-daemon`，旧 service 会留下 —— `customUnInstall` 现在跑同一个 cleanup（同一份证据，非我方一律不动）。

### 未触碰（§8）

quota parser、fallback UA、primary body preservation、metadata 表、freshness、dashboard UI、AppData/ProgramData/registry/service/pipe/AUMID/single-instance、签名链、signer auth、release asset naming、architecture matrix —— **全部未动**。`executableName: sing-box` **保持**（并由测试断言）。

---

## 新发现

按 §14 要求，本轮在安装目录 / legacy ownership / tooling 三处又找到的隐藏雷。

### 1. BLOCKER（我引入并当场发现）—— `StrCpy $var $in "" -1` 静默返回空

第一版安装目录修复用"读 `$INSTDIR` 最后一段，等于 `${APP_FILENAME}` 就改成 `\..\Jiejiebox`"。读最后一段写成：

```nsis
StrCpy $0 $INSTDIR "" -1
```

这在**未启用 command-line assertion** 时**静默返回空**，NSIS 不报错、不警告。结果是：宏编译通过、运行通过、**什么都没改**。若只做编译检查就会带着"已修复"的结论发布，而 collision 原样存在。

发现方式：`build/rename-probe.nsi` —— 它 include **真实的** `installer.nsh` 并**执行**该宏，`scripts/test-install-directory.ps1` 编译+运行+断言。修复后的版本不再需要任何路径解析。

`build/check-installer.nsh` 只能证明"能编译"，证明不了"决定正确"。这条区别本轮被实证。

### 2. 重复 `!define` 在 NSIS 中是致命错误，不是覆盖

意味着 `APP_FILENAME` 无法从 `nsis.include` 覆盖 —— 任何"在 include 里重定义 APP_FILENAME"的尝试都会**直接编译失败**。用 makensis 实测：

```text
!define: "APP_FILENAME" already defined!
Error in script "probe.nsi" on line 2 -- aborting creation process
```

所以安装目录只能从 `$INSTDIR` 入手。这条现在写在 `installer.nsh` 的宏注释里，并在 `rename-probe.nsi` 里留了可复现的检查。

### 3. BLOCKER（流程性）—— CI deep gate 用的是**另一个 ref 上的同一份脚本**

上一轮报告把 `scripts/ci/` 与 `tooling/scripts/ci/` 描述成"两份需要同步的副本"，**这是误解**。实际是：

```yaml
- name: "[gate 1] check out the publishing tooling"
  uses: actions/checkout@v4
  with:
    path: tooling
    ref: ${{ env.RELEASE_BRANCH }}      # 默认 testing
    fetch-depth: 1
- name: "[gate 1] check out the release core"
  uses: actions/checkout@v4
  with:
    path: core
    ref: ${{ env.CORE_SHA }}
```

**同一个仓库、同一个路径 `scripts/ci/...`，只是两个不同 ref / 不同工作区目录。** 所以：

```text
没有第二份要维护的副本 → "同步 tooling 副本"这个动作不存在
真正的问题是 ref 选择：tooling/ 取 RELEASE_BRANCH（默认 testing），不是 core 的 SHA
```

**后果（必须记住）：** 那些 gate（`GateHelpers.ps1`、`Test-InstallerLifecycle.ps1` 等）会从 `RELEASE_BRANCH` 取脚本。我改过的那几个文件在 `testing` 上仍是旧身份（含 `param([string]$ServiceName = "sing-box-daemon")` 与 `HKLM:\SOFTWARE\SagerNet\sing-box` 默认值）。所以：

```text
只要 scripts/ci/ 的 identity 改动还没进到 release branch（testing）的 tag，
deep gate 会拿旧身份去审计新 artifact，并对一个正确的安装报 FAIL。
```

这是**发布顺序依赖**，不是代码缺陷，但它是"下一次发布会在哪里炸"的真实答案：先把 `scripts/ci/` 的 identity 改动合到 `testing`/tag，再让 window runtime-audit deep gate 跑新 artifact。

### 4. 升级目录的取舍是显式的

因为 `Software\${APP_GUID}` 存在且记录 `InstallLocation = C:\Program Files\sing-box`，从 0.1.5 升级时新版本会**沿用旧目录**（升级仍是升级，用户数据不搬家）。**只有全新安装**才落到 `C:\Program Files\Jiejiebox`。这是设计选择，已由测试断言锁定（"an upgrade reuses the previous installation's directory"）。若将来希望升级也迁移目录，那是另一个决定，需要一并处理旧的 Program Files 目录与用户可见性。

---

## commit

按 repo，SHA + message（40 位全值）：

```text
core       32304df549298f12455a389cce8574878887a2b0  chore(desktop): pin the client with the closeout fixes
core       33458c067ff79d48d52883922a4eb7a6f3b7ba9d  test(ci): assert the installation directory and the ownership proof

desktop    2d4dd0321250434682c1281d2556a0d6f4f31fe7  fix(windows): give the fork its own installation directory
desktop    a917e616190050b845061cc3798ff7cf49d4c3a1  fix(windows): prove ownership before removing the legacy daemon service

dashboard  d452c24ec959d1e30159b6cd5cce1fa48e7dc353  feat(ui): show subscription quota in the profile card  (unchanged)
```

本轮新增文件：

```text
desktop  build/installer-legacy-service-ownership.ps1    判定逻辑（可测）
desktop  build/rename-probe.nsi                          执行真实安装目录宏
desktop  scripts/test-install-directory.ps1               11 cases
desktop  scripts/test-legacy-service-ownership.ps1        13 cases
```

submodule 链（可复现）：

```text
core HEAD               32304df549298f12455a389cce8574878887a2b0
  ↓ clients/desktop     a917e616190050b845061cc3798ff7cf49d4c3a1
desktop HEAD            a917e616190050b845061cc3798ff7cf49d4c3a1
  ↓ dashboard           d452c24ec959d1e30159b6cd5cce1fa48e7dc353
dashboard HEAD          d452c24ec959d1e30159b6cd5cce1fa48e7dc353
```

三仓库 working tree **clean**，**未 push**，未 merge / tag / release，未重写已提交历史（第一、二阶段的既有 SHA 未变）。

---

## 最终结论

```text
fork default install dir isolated:            PASS
sing-box.exe signer contract preserved:       PASS
legacy service cleanup cannot delete official: PASS
tooling deep gate synced:                     N/A（不存在第二份副本，见新发现 3）
                                              但存在发布顺序依赖，已明确记录
makensis installer/uninstaller:               PASS
tests/typecheck/build:                        PASS
full electron package:                        NOT RUN
real dual-install test:                       NOT RUN
```

### 逐项证据

**fork default install dir isolated: PASS**
`scripts/test-install-directory.ps1` 编译并**执行**真实宏，11/11：

```text
fresh                = C:\Program Files\Jiejiebox
previous-recorded    = C:\Program Files\sing-box      （升级沿用旧目录）
previous-custom      = D:\Apps\Elsewhere
```

**sing-box.exe signer contract preserved: PASS**
同一测试断言 `APP_FILENAME == "sing-box"`；`win.executableName: sing-box` 未改；`applicationExecutableName = "sing-box.exe"` 未改；`afterPack.cjs` / `afterSign.cjs` / `secureWindowsInstallation` / `peer_windows.go` 全部未动。`verify-desktop-identity-consistency.py` 把两个名字并排打印：

```text
installation directory <program files>\Jiejiebox (the
                       upstream client's is <program files>\sing-box)
installed executable   sing-box.exe (the daemon authenticates by this path)
```

**legacy service cleanup cannot delete official: PASS**
`scripts/test-legacy-service-ownership.ps1` 13/13，其中 **Case A 就是用户描述的那台机器**：`sing-box-daemon` service、binary 位于完全正常的 `<root>\resources\daemon\` 布局、我方 registry key 存在、**没有 marker** → `MUST NOT DELETE`。
Case C1–C7 覆盖"证据不完整 → skip + 记录 reason"。checker 另外断言 marker 是**失败条件**（不是装饰），且其名字与 `userDataSecurity.ts` 写的一致。

**makensis installer/uninstaller: PASS**
两个 half 均 exit 0，且**执行**探针确认宏行为。

**tests/typecheck/build: PASS**（15 项，见下）

**full electron package / real dual-install test: NOT RUN**
阻塞与前一阶段相同：本机无 `node_modules/electron/dist/electron.exe`、无网络，`pnpm run package:win x64` 在 electron-builder 之前即无法进行。**没有绕过**，没有用任何"跳过校验"的方式让它变绿。

### 定向回归（§10，全部实跑）

| # | 项 | 结果 |
| --- | --- | --- |
| 1 | identity contract tests | **PASS**（15 values + 结构契约） |
| 2 | overlay tests | **PASS**（27 passed, 0 failed） |
| 3 | desktop tests | **PASS**（23 cases, `pnpm test`） |
| 4 | dashboard tests | **PASS**（6 files, 69 tests） |
| 5 | typecheck | **PASS**（`tsc -b`） |
| 6 | core go test / build | **PASS**（`go build ./...`、`go test ./experimental/boxdd/...`） |
| 7 | makensis installer half | **PASS**（exit 0） |
| 8 | makensis uninstaller half | **PASS**（exit 0） |
| 9 | old namespace grep | **PASS**（残留全部归类） |
| 10 | 新增：install directory tests | **PASS**（11 cases） |
| 11 | 新增：legacy ownership tests | **PASS**（13 cases） |
| 12 | desktop build | **PASS** |
| 13 | dashboard lint / lint:css / build | **PASS** |

### 最终架构

```text
Jiejiebox
├─ productName        = Jiejiebox
├─ appId              = io.github.piggycatbitshadow.jiejiebox
├─ AppUserModelId     = io.github.piggycatbitshadow.jiejiebox
├─ install directory  = <Program Files>\Jiejiebox          ← 本轮修复
├─ AppData            = %APPDATA%\jiejiebox
├─ ProgramData        = %ProgramData%\jiejiebox-daemon
├─ registry           = HKLM\SOFTWARE\Jiejiebox\jiejiebox
├─ service            = jiejiebox-daemon
├─ daemon pipe        = \\.\pipe\ProtectedPrefix\Administrators\jiejiebox-daemon
├─ legacy proof       = %APPDATA%\sing-box\.access-control   ← 本轮修复
└─ installed exe      = sing-box.exe                        （安全契约，未动）
```

---

## 是否可以给出 "ready for full package / real-machine validation"

按 §12 的两个条件：

```text
default install directory collision 已解决      → PASS
legacy service cleanup 不会误删官方              → PASS
```

两项都 PASS，因此：

```text
Windows coexistence = ready for full package / real-machine validation
```

但这个结论有**两个前置条件**，真机验证前必须先满足，不能忽略：

1. **先让 `scripts/ci/` 的 identity 改动进入 `testing`（或 release tag 所指的 ref）**，否则 window runtime-audit deep gate 会拿旧身份审计新 artifact 并对正确安装报 FAIL（新发现 3）。
2. **真机验证必须包含"先装官方、再装 fork"这一顺序**，且要确认：官方文件与卸载项仍在、官方 service 仍在、fork 装到 `C:\Program Files\Jiejiebox`、fork 的 `sing-box.exe` 与其 daemon signer 校验仍然通过。Case A 已经在**决策层**锁住了误删，但**文件系统层**的共存仍只有静态证据。

### 剩余风险（真实，不隐藏）

```text
full electron package / 真机双安装            未跑（无 Electron 二进制、无网络）
deep gate 的 ref 顺序依赖                     已定位，需发布流程配合；代码层无法自解
从 0.1.5 升级沿用旧安装目录                   设计选择，已由测试锁定；若将来要迁移目录是另一个决定
legacy 0.1.5 的应用树可能被留在 C:\Program Files\sing-box
                                              旧 uninstall 项与卸载器仍在，用户可自行移除；
                                              本轮不删除另一个安装树（与"证明不了就不删"同一原则）
macOS/Linux 的 login item / 安装目录          本轮范围外（Windows only）
```
