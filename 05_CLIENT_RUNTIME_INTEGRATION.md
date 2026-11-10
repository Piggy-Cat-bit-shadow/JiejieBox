# 05｜NE/Libbox 运行时与客户端集成

- 执行时间：2026-11-11 04:00–05:00（Asia/Shanghai）。
- 施工基线：`integration/0.1.45-final` @ `7192a10cc9c`（阶段 04 收尾）。
- 源分支：
  - 内核：`fix/task10-apple-ne-runtime-final` @ `a3340d1a759d`（相对基线 8 个独有提交，23 文件，+1957/−83）；
  - Dashboard：`next/client-consumption`（当前 `62f158255ce9`）；
  - Desktop：`next/client-consumption`（当前 `28dc4a973bcd`，4 提交）；
  - Apple：`next/client-consumption` @ `7e2f5c43633a`、`fix/task10-apple-ne-runtime-final` @ `b2596b50fb6a`；
  - Android：`main` @ `c7837107dc01`（pin 与 main 相同）；
  - satelite-one：`next/subscription-refresh` @ `9cdf782b8c8c`。
- 结论先行：
  1. 内核 8 个 NE/Libbox 提交全部 cherry-pick（0 冲突）；daemon/provider/libbox/boxdd 定向测试与 `-race` 通过；Windows 全量 106 包 ok（仅 3 个既有/权限失败）。
  2. **Dashboard 与 Desktop 真正完成了构建和测试**：在没有 Node 的环境里装了便携 Node 22.23.4 + pnpm 11.13.0，跑通 dashboard 98/98、desktop 59/59、typecheck、build；Dashboard 已先 fast-forward 到 `62f1582`，Desktop 随后 fast-forward 到 `28dc4a9` 并把内核 gitlink 指到已验证的 Desktop。
  3. Apple 两条功能线经 trial merge 确认只有 **3 个冲突文件**（34 个自动合并），但本机没有 Xcode/macOS：**BLOCKED_BY_ENVIRONMENT**，pin 保持 `7fce711`，不做未经验证的合并。
  4. Android pin 已等于 main，未改动；satelite-one 是独立 Android 产品，补装 JDK17 并复用现有 Android SDK 后真正跑了 Gradle：**卡在内核 `app/libs/libbox.aar` 产物缺失**（不是 JDK/SDK），未动它的 integration 分支。

---

## 1. 阶段目标与实际范围

目标：先稳定内核对外接口与运行时语义（Provider Refresh 所有权、配置应用/取消/超时、Service Change 线性化、command wire contract），再让 Dashboard/Desktop/Apple/Android 消费同一套契约。

实际完成：
- 内核：逐提交集成 `fix/task10-apple-ne-runtime-final` 的 8 个独有提交，并用现有代码核对接口语义；
- 接口契约：新增 `05_CLIENT_RUNTIME_CONTRACT.md`（apply 报告、取消/超时、provider refresh、SelectOutbound、连接事件流、平台/ABI 约束）；
- Dashboard：在 desktop 布局中生成代码、跑测试与构建，fast-forward 集成分支到已验证提交；
- Desktop：在 next 分支（含 dashboard 子模块）安装、生成、typecheck、测试、构建，fast-forward 集成分支并把内核 `clients/desktop` gitlink 指向它；
- Apple：核实三条分支关系与差异，做 trial merge 统计冲突，记录语义融合输入；不合并（无 Xcode）；
- Android：核对 pin 与 main 一致，保持不动；
- satelite-one：核对分支、补装 JDK17 并复用 Android SDK，跑 Gradle 单元测试/assembleDebug，卡在内核 `libbox.aar` 产物（BLOCKED）；
- 输出本报告 + 契约文档 + 各客户端最终 SHA。

未执行：Apple iOS/macOS 构建与 Swift 测试（无 macOS/Xcode）；Android APK 构建（satelite-one 需要内核 `libbox.aar`，见 §6/§8）。

---

## 2. 基线与最终 SHA

| 对象 | 基线/来源 | 最终 SHA |
|---|---|---|
| 内核集成分支 | `7192a10cc9c` | `integration/0.1.45-final` 见 §9 |
| 内核源分支 | `a3340d1a759d` | 8 个 pick 的源 SHA 见 `audit-evidence/stage05/cherry-task10-done.txt` |
| Dashboard 集成分支 | `fc1dbb4a48bb` | **`62f158255ce981ffa7ea2d37ff2150c9ced516ed`** |
| Desktop 集成分支 | `aa63764a61c3` | **`28dc4a973bcd76eecf1af1d8d37a633f5c5bfb58`** |
| Desktop 的 dashboard gitlink | `fc1dbb4` | **`62f1582`** |
| 内核 `clients/desktop` gitlink | `aa63764` | **`28dc4a9`**（提交 `1fe0d9a6432`） |
| 内核 `clients/apple` gitlink | `7fce7117b33a` | `7fce7117b33a`（未动，BLOCKED） |
| 内核 `clients/android` gitlink | `c7837107dc01` | `c7837107dc01`（= main，未动） |
| Apple 集成分支 | `7fce711` | `7fce711`（未动） |
| satelite-one 集成分支 | `c9a07350e4c7` | `c9a07350e4c7`（= main，未动） |

---

## 3. 内核分支关系与内容核对

- 8 个独有提交（merge-base `efd8d3b2eae`）：apply 报告、caller deadline、overrides 计划、取消不关闭运行实例、解析与 publish 之间的取消、provider 预算/报告、真实下载预算测试、取消点测试。
- 逐个 cherry-pick：**8/8 成功，0 冲突**。
- 文件级核对：分支触及 23 个文件，其中 **21 个与本树逐字节一致**；2 个不同：
  - `daemon/started_service.go`、`experimental/libbox/command_client.go`——本树带有**更新的** `SelectOutboundResponse`（组选择读回 + request identity）与连接流 instance/sequence 语义，而源分支基于旧 API（`emptypb.Empty`）。逐行检查确认源分支对这 2 个文件的改动（apply 报告、取消守卫、overrides 计划）已全部落在本树，差异是本树更新的部分，不是丢失。
- 8 个提交没有改 proto RPC/消息；libbox 只新增 `CommandServer.ApplyServiceChangeReport`（无删除/签名变化），daemon 新增 `StartedService.ApplyServiceChange`、`ApplyProviderDomainContext`。

---

## 4. Dashboard/Desktop 的顺序与验证

### 4.1 环境

本机原本没有 Node/npm/pnpm。为了真正验证客户端而不是"静态检查通过就说完成"，安装了**便携**工具链到 `_int0145/tools/`（不进任何仓库）：

- Node.js **v22.23.4** 与项目声明的 **v26.7.0**（官方 win-x64 zip，35.6 / 41.2 MB，两个都保留）；
- pnpm **11.13.0**（`npm install -g`，各自 node 目录内）；
- `@bufbuild/buf`（仓库 devDependency）生成 protobuf 代码；
- dashboard 的 `vendor/iterm2-color-schemes` 子模块初始化（主题生成需要）。

最终验证在项目声明的 **Node 26.7.0** 上重跑：install/generate/typecheck/test/build 全部 exit 0（`07-clients-node26.log`）；Node 22.23.4 同样全绿，作为兼容性证据保留。

### 4.2 顺序

1. **Dashboard 先**：`fc1dbb4`（integration/0.1.45）→ fast-forward 到 `62f1582`（"name a superseded provider refresh"，3 文件 +14）。
2. **Desktop 后**：`aa63764`（integration/0.1.45）→ fast-forward 到 `28dc4a9`（4 提交，5 文件 +1065/−36），dashboard gitlink 随之为 `62f1582`。
3. 内核最后：`clients/desktop` gitlink → `28dc4a9`。

### 4.3 验证（在 Desktop 布局中，dashboard 作为子模块）

| 步骤 | 命令 | 退出码 | 结果 |
|---|---|---|---|
| Desktop 安装 | `pnpm install --frozen-lockfile --ignore-scripts` | 0 | 8.7 s |
| Dashboard 安装 | `pnpm install --frozen-lockfile` | 0 | 30.1 s（首次） |
| Dashboard 生成 | `pnpm generate`（buf + 图标 + 主题） | 0 | 67 icons / 533 themes |
| Desktop 生成 | `pnpm generate`（用本内核 proto） | 0 | 3 个 `*_pb.ts` |
| Desktop 类型检查 | `pnpm typecheck` | 0 | 通过 |
| Desktop 测试 | `pnpm test`（node test runner） | 0 | **59/59** |
| Desktop 构建 | `pnpm build`（electron-vite） | 0 | 通过 |
| Dashboard 测试 | `pnpm test`（vitest） | 0 | **98/98**（10 文件） |
| Dashboard 构建 | `pnpm build`（tsc -b && vite build） | 0 | 通过（PWA 34 项） |

证据：`audit-evidence/stage05/02-clients-dashboard-desktop.log`。

**为什么 Dashboard 独立跑会失败**：dashboard 的 `@shared/*` 别名指向 `../src/shared`，只有在 Desktop 仓库布局中（dashboard 作为其子模块）才成立；独立仓库跑测试会报 `Cannot find package '@shared/subscription'`，这是布局约束，不是代码缺陷。

---

## 5. Apple 分析（BLOCKED，未合并）

| 项 | 值 |
|---|---|
| `main` | `97b35d1ce242` |
| `next/client-consumption` | `7e2f5c43633a`（相对 main +15） |
| `fix/task10-apple-ne-runtime-final` | `b2596b50fb6a`（相对 main +25） |
| 两条功能线 | next-only 5 个提交，NE-only 15 个提交 |
| Trial merge（next + NE） | **3 个冲突文件**，34 个文件自动合并 |
| 当前 pin | `7fce7117b33a`（next 线上，落后 next tip 6 个提交） |

冲突文件（语义融合的入口）：
- `Library/Database/Profile+Update.swift`（两条线都改写了 apply 的所有权/提交点）
- `Library/Database/RemoteRefreshApplier.swift`（provider refresh 的 receipt/policy）
- `Tests/HakoSubscriptionUsage/Tests/CoreTests/RemoteProviderRefreshTests.swift`

未合并原因：本机没有 macOS/Xcode，无法编译 Swift，也无法运行 iOS/macOS 测试；按纪律不把"文件合并干净"当作完成，也不把 `next` 或 NE 任一方整体覆盖另一方。融合所需的语义判断（谁拥有 update、receipt 归属、metadata-only refresh 走 policy）已记录在 `audit-evidence/stage05/03-apple-analysis.txt`，留给有 Apple runner 的环境。

---

## 6. Android 与 satelite-one

- **Android**：内核 pin `c7837107dc01` 与 `origin/main` 完全相同，`clients/android` 干净；按文档"没有新改动就保持不动"，未做任何提交。
- **satelite-one**：独立产品仓库（不是 `clients/android` 子模块）。当前 `main=c9a0735`，`next/subscription-refresh=9cdf782`（+16 提交，22 文件），集成 worktree 仍在 main。收尾时补装 **JDK 17.0.21**（Temurin 便携版）并复用已有的 `C:\Deepseek\sdk`（Android 36 / build-tools 36），用缓存的 Gradle 9.7 真正跑了 `:app:testDebugUnitTest` 与 `:app:assembleDebug`：**失败在 `app/libs/libbox.aar` 缺失**（`verifyCoreProvenance` 的输入不存在），Kotlin 编译随之报大量 libbox 符号未解析。阻塞点是**内核 Android AAR 产物**（需要 gomobile + Android NDK 构建并带 provenance），不是 JDK/SDK。因此 **BLOCKED_BY_ENVIRONMENT**，未合并；证据 `08-satelite-jdk17.log`。

---

## 7. 内核测试矩阵

| 范围 | 命令 | 退出码 | 结果 |
|---|---|---|---|
| 构建（无 tag） | `go build ./...` | 0 | 通过 |
| 构建（Windows profile） | `go build -tags <win> ./...` | 0 | 通过 |
| NE 运行时定向 | `./daemon ./provider/remote ./provider/local ./experimental/libbox ./experimental/boxdd` | 0 | 全部 ok（daemon 28.0 s） |
| NE 运行时 race | `-race ./daemon ./provider/remote ./experimental/libbox` | 0 | 全部 ok（162 s） |
| Windows 全量 | `go test -vet=off -tags <win> ./...` | 1 | **106 包 ok**；仅 `common/tls`（既有 TLS1.3）、`tlsspoof`/`windivert`（权限）失败 |
| Linux 全量 | `go test -vet=off -tags <others> ./...` | 见 §7.1 | 见 §7.1 |

### 7.1 Linux 全量

结果：**113 包 ok**；唯一失败 `common/netns TestUnshareNamespace`（WSL 不允许命名空间内 fork），属环境限制。证据 `audit-evidence/stage05/05-linux-full.txt`。

### 7.2 未执行

- Apple iOS/macOS 构建与 Swift tests：无 macOS/Xcode。
- satelite-one Gradle 构建/测试：JDK17 与 Android SDK 已具备，卡在内核 `libbox.aar` 产物缺失（gomobile + NDK），见 §6/§8。
- 客户端真机/端到端：无设备。

---

## 8. 风险、遗留与接口约束

1. **Apple 语义融合未完成**（BLOCKED）：trial merge 只有 3 个冲突文件，但必须由能编译 Swift 的环境按 §5 的语义逐条融合并跑 iOS/macOS tests；不得直接二选一覆盖。
2. **Node 版本已对齐**：声明的 **26.7.0** 上全部通过（`07-clients-node26.log`），22.23.4 也通过；engines 无需修改。
3. **生成代码不随仓库提交**：`src/shared/gen/**`、dashboard `src/gen/**` 由 `pnpm generate` 产生；CI/打包必须先 generate，否则测试/build 会报缺失模块（本阶段已实测）。
4. **Dashboard 独立测试不成立**：`@shared` 别名依赖 Desktop 布局；验收必须在 Desktop 仓库内跑 dashboard 测试。
5. **客户端 pin 的 intent 仍是 `main`**：`release/dependencies.json` 记录 branch=main，而本阶段集成在 `integration/0.1.45`；第 06 阶段 freeze 时要么从 gitlink 读取（当前 gitlink 已指向验证过的 SHA），要么按流程合并到 main，不能直接按 intent 解析出旧 tip。
6. **第 06 阶段注意**：内核 `clients/desktop` 已指向 `28dc4a9`，dashboard 嵌在其 gitlink `62f1582`；Apple/Android/satelite-one 的 pin 仍未变。
7. **`source-lock.json` 仍是 0.1.44 快照**：第 06 阶段统一生成，包含上述客户端 gitlink。
8. **satelite-one 需要内核 Android AAR**：`app/libs/libbox.aar` 必须由内核用 gomobile + Android NDK 构建并带 provenance；本机没有 NDK，也没有该构建流程。该产物应与内核 Release 一起产出（仓库 AGENTS 的 Satelite APK 流程），第 06 阶段处理。

---

## 9. 最终提交

- 内核 NE/Libbox：8 个 cherry-pick（源 SHA 见 `audit-evidence/stage05/cherry-task10-done.txt`）。
- `1fe0d9a6432` `chore(clients): consume the verified desktop and dashboard integration`（`clients/desktop` → `28dc4a9`）。
- 本报告与接口契约的文档提交（`05_CLIENT_RUNTIME_INTEGRATION.md`、`05_CLIENT_RUNTIME_CONTRACT.md`），见 `git log --oneline` 顶部。
- 集成分支 tip：`integration/0.1.45-final`（文档提交即为 tip）。
