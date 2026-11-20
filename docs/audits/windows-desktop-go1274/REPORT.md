# Windows Desktop · Go 1.27.4 升级兼容性审计（0.1.55 发布前）

- 审计时间：2026-11-20
- 内核仓库：`Piggy-Cat-bit-shadow/JiejieBox`，候选 `375bbefb0`（`testing`）
- Desktop 仓库：`Piggy-Cat-bit-shadow/sing-box-for-desktop`，pin `1a1c98e`（`version.json`：version `0.1.5`，go_version `go1.27.4`）
- 审计主机：Windows 11 x64（本机未安装 Node.js / Electron 工具链）
- 结论：**PASS_WITH_LIMITATIONS** —— 工具链链、daemon 产物身份、Windows daemon 测试层已验证；发现并修复了一个"版本声明不等于产物"的门禁缺口和一处过期注释；真实机器项目（TUN/WinDivert、睡眠唤醒、换网、长期稳定性、服务生命周期实机）未验证。

## 1. 执行摘要

本轮把"Desktop 声明的 Go 版本 → 真实安装包里的 `sing-box-daemon.exe`"整条链拉直，并用两台工具链（go1.27.4、go1.26.8）对同一份源码做了差分。结论：

1. **工具链选择是可靠的**：`version.json` → `GOTOOLCHAIN=go1.27.4` → go 命令把选中的工具链目录放到 PATH 首位 → `build_boxdd` 内部 `go build`（`GOTOOLCHAIN=local`）解析到的仍是选中的 1.27.4。甚至把 PATH 上的 `go` 换成 1.26.8，最终 daemon 仍报告 `go1.27.4`。
2. **产物身份此前无法证明**：`scripts/ci/verify-desktop-daemon-provenance.py` 检查 PE 架构、`path`、`GOOS/GOARCH`、tags，但**不检查构建它的 Go 版本**。同一源码用 1.26.8 构建的 daemon 能通过全部旧检查——这正是"版本号正确但产物错误"。已修复（见 §6 G-01），并加了五种决策的自测与四个正/负对照。
3. **新运行时的优化确实在生效**：`GOEXPERIMENT` 为空，go1.27.4 的 baseline 实验包含 `GreenTeaGC`、`SizeSpecializedMalloc`、`RandomizedHeapBase64`、`JSONv2`；daemon 构建不设置任何 `GOEXPERIMENT`/`GOGC`/`GOMEMLIMIT`，五个固定 `GODEBUG` 都不涉及 GC/分配器。因此 Green Tea GC 与 1.27 的分配器改动就是出厂行为，没有被旧开关抵消。
4. **Windows daemon 层没有可观测回归**：`experimental/boxdd` 全套（20 项，含 Authenticode 与真实 daemon 的 product RPC）在 1.27.4 和 1.26.8 下都是 20 PASS / 0 SKIP（配好证书与 acceptance 二进制后）。
5. **升级路径不会与运行中的旧 daemon 抢文件**：安装器在写文件前先移除/停止旧服务（`customInstallBeforeInstallationChanges` → `installer-remove-legacy-service.ps1`；`installer-stop-service.ps1` 失败即 Abort），GUI 另有 `version-mismatch` 状态。
6. **未验证**：真实 Windows x64 机器上的 TUN/WinDivert、睡眠唤醒、换网、长期稳定性、服务安装/卸载实机流程；本机没有 Node.js，Electron 编译与 Desktop 单元测试未在本地执行（由 CI 的 Windows 安装器作业覆盖）。

## 2. 版本链证据

| 环节 | 来源 | 预期 | 实际证据 |
|---|---|---|---|
| Desktop 声明 | `clients/desktop/version.json` @ `1a1c98e` | `go1.27.4` | 文件内容；pin 与 `release/source-lock.json` 一致 |
| 声明读取 | `scripts/version.ts:26` `readGoVersion()` | `go1.27.4` | 代码路径 |
| 构建环境 | `scripts/package.ts:47` `goEnvironment()` | `GOTOOLCHAIN=go1.27.4` | 代码路径 |
| 版本自检 | `package.ts:70` `verifyGoVersion()` | `go env GOVERSION == go1.27.4`，不等即失败 | 代码路径；无 fallback |
| 工具链切换 | 实测（PATH 首位为 go1.26.8，`GOTOOLCHAIN=go1.27.4`） | PATH 首位变为工具链目录 | 探针输出：`D:\Go\mod-cache\golang.org\toolchain@v0.0.1-go1.27.4.windows-amd64\bin;...`，`go env GOVERSION=go1.27.4` |
| daemon 构建 | `cmd/internal/build_boxdd`（`GOTOOLCHAIN=local`、`CGO_ENABLED=0`、`GOOS/GOARCH`） | 使用选中的 1.27.4 | 探针同上；`daemon-before.exe` 的 `go version -m` 首行 `go1.27.4` |
| 最终 daemon | `daemon-before.exe`（本机按 Desktop 方式构建） | windows/amd64，go1.27.4 | `PE 0x8664`，82,827,264 bytes，`go1.27.4`，23 个 tags，自报 `jiejiebox-daemon version 0.1.55`、`core api version 4` |
| CI 安装 | `client-desktop-windows.yml`（读 `version.json` → `setup-go`） | 与声明一致 | 工作流代码路径；本轮未重跑 Windows 作业 |

旁证：同一源码用 1.26.8 构建的 `daemon-1268.exe`，除首行 `go1.26.8` 与 `DefaultGODEBUG` 外与 1.27.4 产物完全同类（`path`、tags、GOOS/GOARCH 一致）。

## 3. 调用链（发布输入 → 用户 GUI）

```
Core SHA / Desktop pin (source-lock)
  → clients/desktop/version.json (go_version)
  → CI setup-go / 本地 GOTOOLCHAIN
  → scripts/package.ts: verifyGoVersion()
  → buildBoxdd(): go run ./cmd/internal/build_boxdd -target=windows/amd64
  → go build -trimpath -ldflags "<build_shared.LinkerFlags>" ./experimental/boxdd
  → bin/sing-box-daemon.exe（PE 检查）→ electron-builder 资源打包
  → NSIS 安装器（签名/校验）→ 安装：先移除/停止旧服务 → 写文件 → 注册服务
  → daemon service (jiejiebox-daemon) → Named Pipe → Electron worker relay
  → Connect RPC/Protobuf → GUI
```

每段的所有者：Desktop（版本声明、打包、安装器、GUI/worker）、Core（daemon 源码、build_boxdd、服务与 IPC 实现、provenance 门禁）。失败传播：`verifyGoVersion`/`buildBoxdd`/PE 检查/安装器服务步骤都是硬失败；GUI 对旧版本 daemon 有 `version-mismatch` 状态。

## 4. 发现清单

| # | 分类 | 发现 | 证据 |
|---|---|---|---|
| G-01 | **CONFIRMED_BUG（门禁缺口，已修）** | daemon provenance 门禁不校验构建工具链；1.26.8 产物与 1.27.4 产物都能通过 | 旧脚本仅检查 PE/path/GOOS/GOARCH/tags；四个对照见 §6 |
| G-02 | STALE_METADATA（已修） | `client-desktop-windows.yml` 注释写死 "Go 1.26"，与实际动态读取 `version.json` 不符 | 注释行 167；已改为引用声明文件 |
| G-03 | NO_ISSUE | 工具链链会"名对实错"吗？不会：切换后的工具链目录在 PATH 首位，`local` 不改变解析结果 | PATH 探针 + 两台工具链差分 |
| G-04 | NO_ISSUE（优化生效） | Green Tea GC 与 1.27 分配器 baseline 是否被抵消？没有：`GOEXPERIMENT` 空、无 GOGC/GOMEMLIMIT/GC 相关 GODEBUG | `go env GOEXPERIMENT`；`internal/buildcfg/exp.go` baseline；daemon build settings |
| G-05 | NO_ISSUE | daemon 层是否有 1.27 行为回归？测试层无可观测差异 | boxdd 20 项在 1.27.4 / 1.26.8 均全通过 |
| G-06 | NO_ISSUE | 升级时安装器与运行中的旧 daemon 抢文件？安装器先移除/停止旧服务，失败即 Abort | `installer.nsh:2120-2213`、`installer-remove-legacy-service.ps1`、`installer-stop-service.ps1` |
| G-07 | COVERAGE_GAP / NOT_VERIFIED | 真实机器项与 GC 内存时间线未测 | 见 §7 |

## 5. 测试矩阵

| 测试 | 命令 / 环境 | 结果 |
|---|---|---|
| 工具链切换探针 | PATH=go1.26.8；`GOTOOLCHAIN=go1.27.4 go run .`（程序打印 LookPath/PATH/child GOVERSION） | PATH 首位 = 1.27.4 工具链目录；child = go1.27.4 |
| 旧工具链下的 daemon 构建 | 同上环境执行 `go run ./cmd/internal/build_boxdd -target=windows/amd64` | 成功；`go version -m` = go1.27.4（33s） |
| 对照构建 | PATH=go1.26.8；`GOTOOLCHAIN=local` 执行 build_boxdd | 成功；`go version -m` = go1.26.8（116s） |
| 门禁正例 | `verify-desktop-daemon-provenance.py daemon-before.exe --candidate --expect-go-version go1.27.4` | PASS，记录 `built by go go1.27.4 (== version.json go_version)` |
| 门禁反例 A | 同一 1.27.4 daemon，`--expect-go-version go1.26.8` | 失败（exit 1） |
| 门禁反例 B（缺陷形状） | 1.26.8 daemon，`--expect-go-version go1.27.4` | 失败（exit 1） |
| 门禁非恒假对照 | 1.26.8 daemon，`--expect-go-version go1.26.8` | PASS |
| 门禁自测 | `--self-test` | PASS（parse/match/mismatch/unreadable/opt-in 五种） |
| daemon 套件（1.27.4） | `go test -v ./experimental/boxdd/` + `BOXFDD_TEST_PFX` + `BOXDD_ACCEPTANCE_BIN=daemon-before.exe` + `SIGNTOOL` | 20 PASS，0 SKIP |
| daemon 套件（1.26.8） | 同上，acceptance=daemon-1268.exe | 20 PASS，0 SKIP |
| 运行时实验清单 | `go env GOEXPERIMENT`（1.27.4 / 1.26.8） | 均为空；1.27.4 baseline 含 GreenTeaGC/SizeSpecializedMalloc/RandomizedHeapBase64/JSONv2 |
| daemon build settings | `go version -m` 两台产物 | tags/GOOS/GOARCH 一致；`DefaultGODEBUG` 1.27.4 = tlssecpmlkem=0,tracebacklabels=0,x509sslcertoverrideplatform=0；1.26.8 = tlssecpmlkem=0；均无 GC 项 |

## 6. 修复记录

### G-01 daemon provenance 不校验工具链（Core）

- 根因：`verify-desktop-daemon-provenance.py` 只读 `go version -m` 的 tab 行（path/GOOS/GOARCH/tags），首行的工具链从未被读取；工作流也没有传入期望值。
- 影响：任何"声明 1.27.4、实际用旧 Go 构建"的产物都能通过，发布记录会写成 1.27.4。
- 修复（最小）：
  - 脚本新增 `--expect-go-version`（发布路径必填，`--allow-unasserted-go-version` 仅诊断用），解析 `go version -m` 首行并严格相等；记录写入 provenance 文件；
  - `client-desktop-windows.yml` 的 gate 3 从**同一个** `version.json` 读取 `go_version` 传入，不重复声明；
  - 脚本新增 `--self-test`（五种决策），`release-contract-verify.yml` 每次 push 执行。
- 修复前/后对照：修复前两个产物（1.27.4 / 1.26.8）都能通过；修复后反例 A/B 均失败、正例与非恒假对照均通过（§5）。
- 改动文件：`scripts/ci/verify-desktop-daemon-provenance.py`、`.github/workflows/client-desktop-windows.yml`、`.github/workflows/release-contract-verify.yml`。

### G-02 过期注释（Core）

`client-desktop-windows.yml` 的 toolchain 注释从 "Go 1.26" 改为引用 `clients/desktop/version.json`（实际安装值），避免维护者按注释判断构建版本。

## 7. 未验证项（NOT_VERIFIED）

| 项目 | 原因 | 需要什么 |
|---|---|---|
| 真实机器 TUN / WinDivert 拦截、DNS、系统代理恢复 | 本机不是发布目标环境，且无真实流量场景 | Windows x64 实机 + 实际节点 |
| 睡眠唤醒、换网、长期稳定性 | 需要实机与时间 | 实机验收清单（`docs/WINDOWS-ACCEPTANCE.md`） |
| 服务安装/停止/卸载实机流程 | 本 shell 非管理员；CI 的 runtime audit 作业覆盖托管 runner | 管理员实机或 `client-desktop-windows-runtime-audit` 的 Windows run |
| Electron 编译、Desktop 单元测试（`pnpm test` / `typecheck`） | 本机未安装 Node.js/pnpm | CI 的 Windows 安装器作业（编译 + 打包 + 门禁）；本地可补装 Node 后复跑 |
| GUI ↔ daemon 的 Named Pipe / Connect RPC 运行时行为 | 未构建 Electron 应用 | 安装包实机 + `TestProductConfigurationRPCs` 已覆盖 daemon 侧 RPC |
| Green Tea GC 的内存时间线 / OOM 预算重标定 | Windows daemon 没有 OOM 预算（`service/oomkiller` 是 darwin-only），本机也无真实负载 | 实机内存采样；若有移动端预算，按 W1 重测 |
| 新版安装包覆盖旧版（升级路径）的实机验证 | 未运行安装器 | Windows 实机安装 0.1.54 → 0.1.55 |

## 8. 发布建议

- 工具链与产物身份：**已验证**（含新门禁与正负对照）。0.1.55 的 Windows 安装器作业现在会证明 daemon 由 `go1.27.4` 构建；这是本轮之后新增的发布阻断条件。
- 运行兼容性：**已验证到测试层**（daemon 套件两工具链一致、安装器先停服务、GUI 版本错配状态存在）。
- 发布前最后必须完成的动作：
  1. 重新跑 `kernel-release`（本候选包含 Desktop pin `1a1c98e` 与新的 provenance 门禁），确认 Windows 安装器作业通过并回读 `daemon-provenance.txt` 中出现 `built by go go1.27.4`；
  2. 在 Windows 实机上完成一次 0.1.54 → 0.1.55 覆盖安装并确认服务被替换、GUI 连接正常（可在发布后按验收清单执行，但必须在"整轮完成"记录里注明）。
- 不建议为了"吃到优化"再调 GOGC/GOMEMLIMIT：当前没有 Windows 侧的内存预算，Green Tea GC 已是默认；按 W1 的说法，重标定应基于实机内存曲线，而不是猜一个值。
