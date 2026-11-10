# FINAL 测试矩阵（0.1.45 RC）

分类：**PASS** = 实际执行成功；**FAIL** = 实际执行失败（含既有/环境失败）；**BLOCKED** = 工具链/权限/设备不可用；**NOT RUN** = 未执行。

最终树：`69e8dcb457a`（source-lock 快照；代码与阶段 05 验证树 `1478cb6` 相同，仅 release 元数据、锁与文档变化）。

## 1. 内核（Windows / Linux）

| 套件 | 平台 | 命令 | 结果 |
|---|---|---|---|
| 构建（无 tag / Windows profile） | Windows | `go build ./...` / `-tags <win>` | **PASS**（exit 0） |
| 全量 `./...` | Windows | `go test -vet=off -tags <win> -timeout 40m ./...` | **106 包 PASS**；`common/tls` **FAIL**（既有 TLS1.3）、`tlsspoof`/`windivert` **FAIL**（WinDivert 权限） |
| 全量 `./...` | Linux (WSL) | 同上，others profile | **113 包 PASS**；`common/netns TestUnshareNamespace` **FAIL**（WSL 不允许命名空间内 fork） |
| 构建 | Linux | `go build ./...` / `-tags <others>` | **PASS** |
| route 全量 | Windows | `./route` | **PASS**（700 测试函数，447.9 s） |
| route 全量 | Linux | `./route` | **PASS**（443.5 s） |
| FD ledger（Linux-only） | Linux | `-race ./route -run TestTheDisposalLedgerMatchesTheOpenFileDescriptors` | **PASS** |
| 真实 TUN（unshare） | Linux | `unshare -Urn env SINGBOX_REAL_TUN_DEVICE=1 ./protocol/tun -run TestRealTunDevice` | **PASS 3/3**（dead-flow release 实测 5 ms） |
| 真实 netns 设备验收 | Linux | `unshare -Urnm` + T09_* fixture | **PASS 2/2**（正向 + 反向） |
| 网络相关 24 包 | Windows | route/rule, dialer, adapter, dns, daemon, transport | **PASS**（75.8 s） |
| 双栈 + H3/H2 联合 | Windows | 根包 + daemon + dialer + dns + httpclient + transport/http + naive + masque | **PASS**（84.3 s） |
| race（dialer/dns/httpclient/naive） | Windows | `-race` | **PASS** |
| race（daemon/provider/libbox） | Windows | `-race` | **PASS**（162 s） |
| race（transport/http 子集） | Windows | `-race -run <fallback/CONNECT/setup>` | **PASS** |
| NE 定向 | Windows | daemon/provider/libbox/boxdd | **PASS**（36 s） |
| 协议包（12） | Windows | protocol/{vless,vmess,trojan,...,naive} | **PASS** |
| e2e | Windows/Linux | `./e2e` | **PASS**（并发满载时曾出现一次负载超时；空闲重跑 5/5 PASS） |
| 广域回归 | Windows | common/config/provider/option/constant | 32 包 PASS；`common/tls`、`tlsspoof`、`windivert` 与环境相关失败（同上） |

## 2. 客户端

| 目标 | 命令 | 结果 |
|---|---|---|
| Dashboard 测试 | `pnpm test`（Node 26.7.0） | **PASS 98/98** |
| Dashboard 构建 | `pnpm build` | **PASS** |
| Desktop 安装/生成 | `pnpm install --frozen-lockfile --ignore-scripts` / `pnpm generate` | **PASS** |
| Desktop 类型检查 | `pnpm typecheck` | **PASS** |
| Desktop 测试 | `pnpm test` | **PASS 59/59** |
| Desktop 构建 | `pnpm build` | **PASS** |
| Apple iOS/macOS 构建 + Swift tests | `xcodebuild` / Swift tests | **BLOCKED**（无 macOS/Xcode） |
| Apple 语义融合 | `integration/0.1.45-ne-fusion` @ `9330d90`（next + NE） | 16 个冲突按所有权/回执语义融合；44 个 Swift 文件；`swiftc -parse` 44/44 PASS；Foundation 子集 `-typecheck` PASS；14 场景探针 PASS；**Xcode 构建与 XCTest NOT RUN**（无 macOS） |
| Android core AAR | `go run ./cmd/internal/build_libbox -target android -android-variant main/legacy`（JDK17 + NDK 28） | **PASS**（arm64/arm/x86/x86_64；provenance commit=`b810d0c`，version=SHA） |
| satelite-one 单元测试/assembleDebug | `./gradlew :app:testDebugUnitTest :app:assembleDebug`（Node 无关，JDK17+SDK+真实 AAR） | **PASS**（unit tests BUILD SUCCESSFUL；`verifyCoreProvenance: OK`；assembleDebug BUILD SUCCESSFUL） |
| Android 客户端构建 | `clients/android`（pin `c783710`）+ 同一对 AAR，`./gradlew :app:assembleDebug` | **PASS**（BUILD SUCCESSFUL，universal/arm64/armv7/x86/x86_64 debug APK；8m10s） |
## 3. 发布门禁

| 门禁 | 结果 |
|---|---|
| `release-freeze.py check` | **PASS**（无 diverged、无 pin 需要移动） |
| `release-freeze.py verify --lock release/source-lock.json` | **PASS** |
| gitlink 与 source-lock 一致 | **PASS**（desktop 28dc4a9 / nested 62f1582 / apple 7fce711 / android c783710） |
| go.mod/test/go.mod replace 与锁一致 | **PASS**（verify 覆盖） |
| ABI 检查 | **PASS**（libbox 仅新增 `ApplyServiceChangeReport`；无删除/签名变化） |
| localization / 平台符号守卫 | **NOT RUN**（仓库无统一本地化检查脚本；平台符号由构建覆盖） |
| CI 工作流状态 | **NOT RUN**（本地集成；未触发远端 CI） |

## 4. 明确不计为 PASS 的项

- Windows 管理员用例（`TestGoKernel*`、WinDivert/tlsspoof）。
- `common/tls` 的 Windows TLS1.3 失败（阶段 03 基线同样失败）。
- Apple 的构建与真机测试；Android/satelite 的**真机**运行（构建与单元测试已 PASS）。
- 本地 WSL 的 `common/netns TestUnshareNamespace`（环境限制；目标 Linux runner 应可执行）。
- 所有未被本轮命令实际执行的 CI 声明。

证据目录：`audit-evidence/stage01…stage06/`（各阶段日志、客户端 98/98 与 59/59 原始输出、netns 与 TUN 实机日志、freeze 的 resolve/check/verify 输出）。
