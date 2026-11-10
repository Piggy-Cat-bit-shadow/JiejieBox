# 双栈 DNS 来源、字面恢复重确认与验收归档（2026-11-10）

- **归档目录**：`0.1.45`（由交接指定）。**实际构建与发布版本是 `0.1.44`**；截至归档没有 `0.1.45` 的构建或发布记录，目录名只表示"面向下一版的收尾材料"。
- **源码基线**：集成分支 `integration/next-round`，本分支切出时 HEAD 为 `edbfe8e0b2b`；此后集成头前移到 `a635ded940a`（仅知识库文档提交，未触及本报告涉及的源码）。
- **候选**：分支 `fix/task09-repair`，HEAD `31a3e9acea1`，已推送到 GitHub 远端；**未合并**，由收尾/集成负责人按既有分工处理。
- **证据等级**：沿用 `KNOWLEDGE/README.md` 的 `VERIFIED_SOURCE` / `VERIFIED_GIT` / `VERIFIED_UNIT` / `VERIFIED_INTEGRATION` / `VERIFIED_CI` / `VERIFIED_ARTIFACT` / `VERIFIED_DEVICE` / `INFERRED` / `UNVERIFIED` / `BLOCKED_BY_ENVIRONMENT`。本报告没有 CI run，也没有公网互通；凡未实际运行的一律注明，不按通过记账。
- **脱敏**：本机路径、用户名与临时目录替换为 `<workdir>`、`<evidence>`、`<machine>`；测试地址只使用文档保留段（`192.0.2.0/24`、`2001:db8::/32`、`10.98.0.0/24`、`fd00::/8`）；不含真实节点、订阅、账号、令牌、密钥、内网地址、设备标识或 SSID。

## 结论摘要

| 项 | 结论 | 证据等级 |
|---|---|---|
| C1 字面恢复丢掉跨代重确认 | 已修复：生产器保留可比较来源，重复只在来源更新时放行；当前确认领先其族并只拨一次 | `VERIFIED_UNIT`（真实恢复入口 + 真实调度器；旧实现红、复原绿） |
| C2 直接热缓存用返回时代次打戳 | 已修复：戳在缓存键固定后、条目选中前读取并随命中返回 | `VERIFIED_UNIT`（旧实现 `stamp=2`，修复后 `1`；键前换代仍为 `2`） |
| C3 禁用缓存丢失可知来源 | 已修复：来源对所有交换捕获，代次守卫作用域不变；无回调/本地合成仍 unknown | `VERIFIED_UNIT`（真实 Router + 真实 client；旧实现 `known=false`） |
| B4 组 → Direct → 候选列表同流 | 已补：真实 `DialSerialNetwork` → 真实 `Selector`/`LoadBalance` → 真实 `Direct` → 叶子逐候选尝试，首个失败、次个成功、ping/pong 可达 | `VERIFIED_UNIT`（叶子为替身） |
| D1 netns 设备验收（两方向） | 已执行：正向命名空间到达宿主不可达目标且本端 IP 属于目标命名空间；镜像方向宿主可达、镜像命名空间以 socket 路由错误失败 | `VERIFIED_DEVICE`（WSL2 Ubuntu，root/CAP_SYS_ADMIN） |
| E3 元数据隔离、缓存归属、进程匹配与分流 | 已验：并发 A/AAAA 的 `QueryType`/`IPVersion`/ECS 隔离；同名跨 transport 各自缓存；进程+域名规则同流选路 | `VERIFIED_UNIT` |
| 两处自主攻击 | 同一条目两条命中路径同源；禁缓存不绕过禁族判定 | `VERIFIED_UNIT` |
| F Hysteria2/TUIC 静默丢包跨族回退 | 红灯复现：现 pin 两个真实 client 各 1 个 UDP socket、6 次握手写入后超时，无第二次尝试 | 组件红灯；补丁与互通 `BLOCKED`（依赖所有者） |

## 变更（候选分支提交）

| 提交 | 内容 |
|---|---|
| `c5e81ed5377` | C1：恢复生产器改为保存 `known + generation` 的可比较来源，重复按来源新旧放行；不再保存"到达时是否过期" |
| `4284af66679` | C2：`questionCache` 在键固定后、条目选中前读取来源并随命中返回 |
| `a69725c3d85` | C3：新增 `captureSource` 对所有交换记录来源；代次守卫保持原作用域 |
| `d0f1aa14005` | 两处自主攻击的回归（同条目双路径同源；禁缓存不绕过禁族） |
| `3570d315f21` | D1 镜像方向设备测试与自包含脚本约定 |
| `1a9edbe35cb` | B4 同流：组 → Direct → 候选列表 + 逐候选失败切换 |
| `ad16d251701` | 缓存归属：同名跨 transport 不共享条目 |
| `31a3e9acea1` | 进程匹配 + 分流规则同流（进程规则选自己的 transport，其他进程落默认，重复命中本 transport） |

均只改变"来源如何确定"或新增测试；答案交付、缓存隔离与拨号热路径的计数器读取未变。

## 复现与红绿

历史审计 overlay（审计方自己的三个用例）在修复前候选 `f232cc2160e` 上逐字复现：

```text
TestAuditWarmResponseKeepsStampAcrossChange    cached address selected in generation 1; ... stamp=2 known=true   FAIL
TestAuditUncachedFamilyReportsSource           generation=1 captures=[] stamp=0 known=false                       FAIL
TestAuditRecoveryReconfirmationAfterActualFlip attempts=[2001:db8::2c 2001:db8::30 192.0.2.1]（.2 未尝试）   FAIL
```

同一 overlay 在候选 `31a3e9acea1` 上三条全部 PASS。五个变异在独立工作树上逐个执行并复原：

| 移走的保护 | 变红 |
|---|---|
| 换回修复前的恢复生产器 | C1 用例（`.2` 未拨） |
| 读戳移回命中返回之后 | C2 用例（expected 1 / actual 2） |
| 删掉无缓存来源捕获 | 两条 C3 用例（`known=false`） |
| `DialSerialNetwork` 不再走富接口分派 | 只新分派用例红，旧直调用例绿 |
| 组在自己的 `DialContext` 上作答（窄转发） | 新分派用例与旧直调用例同时红 |

## 验证命令与结果（候选 `31a3e9acea1`，`<workdir>`）

```text
go build ./...                                                     exit 0
go test -count=1 ./dns ./common/dialer ./adapter ./protocol/direct ./protocol/group ./daemon
  ok 10.0s / 23.0s / 2.3s / 0.3s / 17.6s / 45.1s                   exit 0（最终 SHA 复跑）
CGO_ENABLED=1 CC=gcc go test -race -count=1 ./common/dialer ./dns ./adapter
  ok 23.4s / 9.7s / 7.0s                                           exit 0（最终 SHA）
go test -count=1 ./route/...                                       ok 457.2s + 4.1s（最终 SHA）
GOOS=windows GOARCH=amd64 go build -tags "$(cat release/DEFAULT_BUILD_TAGS_WINDOWS)" ./cmd/sing-box
  exit 0，35s（最终 SHA）；sing-box version unknown / go1.25.5 windows/amd64
go version -m <binary>：四个替换 pin 与 release/source-lock.json 一致
```

Linux 设备验收（交叉编译测试二进制后在 WSL2 root 运行，脚本 `d1_wsl.sh <binary> <echo.py>`）：

```text
宿主 ip -6 route get 2001:db8:99::1          -> Network is unreachable
netns=t09rtarget dial                        -> connected [2001:db8:99::2]:... -> [2001:db8:99::1]:9000
宿主 dial 10.98.0.1:9001                     -> ping/pong（目标真实可达）
netns=t09rmirror dial 10.98.0.1:9001         -> ENETUNREACH（来自目标命名空间自己的路由）
两测试均 PASS，脚本 trap 清理命名空间与接口
```

F 红灯（隔离探针，不进入候选）：Hysteria2 `dials=1 handshake_writes=6`、TUIC 同，均 `context deadline exceeded`。

## 平台覆盖

- Windows 10 x64，`go1.25.5 windows/amd64`，`CGO_ENABLED=0`；race 用 MSYS2 gcc 16.2.0。
- 交叉编译：linux/amd64、linux/arm64、android/arm64、darwin/arm64 均 exit 0。
- 设备：WSL2 Ubuntu 24.04.5，内核 6.18.33.2-microsoft-standard-WSL2，root/CAP_SYS_ADMIN；仅证明 netns 方向性，不代表公网或真实节点。
- 无 CI run、无公网互通、无 Android/iOS/真实路由器。

## 已知失败与既有 flaky（保留原样本）

- `protocol/group` 三条测量型用例偶发：`TestAgentFBurstMustNotCreateAWorkerPerFailure`（父候选 3/8；全包再现 `12>4`）、`TestTheBoxBudgetHoldsAtProviderScale/10000_nodes`（父候选 2/6；全包再现 5267/10000）、`TestRecheckSingleFlightCollapsesABurst`（单独 0/8，全包偶发 `3>2`）。三者不涉及本轮改动；单独复跑均通过；集成窗口遇红先按 3 次重跑判定并保留原样本。
- `route` 第一次全量运行失败（494.8s），失败用例名未保留；同 SHA 两次复跑通过（492.3s / 456.2s）。按**未解释失败**保留，不当作已解释的 flake，也不抹去第一次失败。

## 未完成项与负责人

1. **F 依赖补丁与真实互通**：现 pin 的两个 client 在"首选族 UDP 可建、发包无回音"时只建一个 socket、无第二次尝试。补丁与 pin 由依赖所有者按既有分工接续；真实服务端互通另验。**若版本承诺 Hysteria2/TUIC 静默丢包跨族回退，F 是发布门禁。**
2. **组合场景的接口层同流**：多地址+换代+失败切换已在同一流验证；接口层 socket 计数、组内 failover、峰值与清理仍是分段测量，缺"同一流叠加"的夹具（需要 `DefaultDialer` 接口层可控替身，属本窗口可继续项）。
3. **B4 的 URLTest 同流**：URLTest 选择需要真实探测目标；其选择路径由自身包用例覆盖，未纳入组→Direct 同流。
4. 合并候选、更新依赖 pin、正式签名与发布由收尾/集成负责人执行；本报告不构成发布证明。
