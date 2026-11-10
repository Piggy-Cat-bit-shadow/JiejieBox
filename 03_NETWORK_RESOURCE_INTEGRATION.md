# 03｜内核网络生命周期、连接管理与资源治理

- 执行时间：2026-11-11 01:15–02:10（Asia/Shanghai）。
- 施工基线：`integration/0.1.45-final` @ `ea5651f2b5b`（阶段 03 的依赖基线 commit），来源 `next/network` @ `b9bc2bc25981`、`closeout/resources` @ `aafc4c11f022`。
- 依赖：sing-tun `v0.0.0-20261110141903-57e9d17fa852`、quic-go `v0.61.1-0.20261110165647-ca9992bc795e`（阶段 02 收敛结果，本阶段已写入 `go.mod`/`test/go.mod` 作为预览 pin；正式 source-lock 留给第 06 阶段）。
- 结论先行：
  1. `next/network` 的 36 个独有提交中，**34 个按原提交 cherry-pick**（0 冲突），2 个依赖 pin 提交按内容拆分处理；全部有效内容已吸收，无遗漏。
  2. `closeout/resources` 的 21 个提交中，6 个测试内容提交被 cherry-pick；10 个 sing-tun pin 递增提交与 6 个 merge 提交被证明为纯 pin/聚合，不重复移植；3 个测试文件与分支 tip **逐字节一致**。
  3. `next/resources` 是 `closeout/resources` 的祖先，无需单独集成。
  4. `backup/02-ecfffe7d0` 已审查：其独有内容（route diagnose API、process-cache reset epoch、libbox/daemon 流控）不属于本阶段的网络/资源生命周期范围，未整支合并，逐项记录给第 05/06 阶段。
  5. `pending/device-axis-reload-window-tests` 已定案：runtimecoord 4 个测试移植后**全部通过**；两个 libbox 测试被拒绝（在它们自己的 base 上就是红的）。
  6. 网络切换不再杀死正在传输的流；旧 generation 的物理关闭移出过渡体，由单一 disposer 按连接预算执行；准入票据与 shutdown 原子。
  7. 全量 Windows-profile 构建通过；route 包 700 个测试函数全量通过；28 个新生命周期测试在 `-race` 下通过；传输/拨号/资源相关包 `-race` 通过；12 个协议包、e2e UDP detour、daemon selector 全通过。

---

## 1. 阶段目标与实际范围

把"连接生命周期"和"资源治理"作为一个相互约束的系统集成：准入 → 使用 → 异常 → 回收 → 关闭，全程可验证；慢消费者、队列、内存、FD 边界不被破坏。实际执行：

- 把主仓库依赖切到阶段 02 的 sing-tun / quic-go 集成 SHA（预览 pin，第 06 阶段才冻结）；
- 逐个提交审查并集成 `next/network` 的网络生命周期改动；
- 去重并集成 `closeout/resources` 与 `next/resources` 的资源治理测试；
- 审查 `backup/02-ecfffe7d0` 与 `pending/device-axis-reload-window-tests`；
- 用构建、包级测试、`-race`、e2e 覆盖行为矩阵；
- 输出状态转移/所有权说明 `03_STATE_AND_OWNERSHIP.md`。

未执行：任何 pin 的最终冻结（第 06 阶段）、Linux 真实 FD/E2E 测试（本机是 Windows）、Apple/Android/Desktop 平台构建（环境缺失）、管理员权限测试（WinDivert/GoKernel/TUN 设备）。

---

## 2. 基线与提交

| 项 | 值 |
|---|---|
| 阶段 03 依赖基线 | `ea5651f2b5ba92e2544dc9546a9890dbee3b5115`（go.mod/test 模块切到阶段 02 依赖） |
| `next/network` 来源 | `b9bc2bc25981e4f4606eaa5298ded56fde8d312d`（merge-base `c4f46af9e1f8`，36 独有提交） |
| `closeout/resources` 来源 | `aafc4c11f022a32de82e065ad03450183b716d97`（merge-base `95ed81392563`，21 独有提交） |
| `next/resources` | `b00dfe1f0e08`，`closeout/resources` 的祖先，未单独处理 |
| `backup/02-ecfffe7d0` | `ecfffe7d0691`，只审查不集成 |
| `pending/device-axis-reload-window-tests` | `0477e327e5daa6a6fc757820154b13a628f1ed74`，选择性移植 runtimecoord |
| 集成结果 | `integration/0.1.45-final`，内容提交 42 个（34 network + 1 TrustTunnel hunk + 6 resources + 1 pending；依赖基线与文档提交另计），45 个文件 +7980/−111 |

---

## 3. 分支关系与取舍

### 3.1 `next/network`：34 个原样 cherry-pick，2 个 pin 提交拆分

| 类别 | 提交 | 处理 |
|---|---|---|
| 路由生命周期修复 | `ce63c190e30`、`8bf1f6d28c7`、`20ef4f164a2`、`2d677f90c82`、`ae56704cef9`、`412ad7742a1` | 全量 cherry-pick（保留作者与说明） |
| v2rayquic 修复 | `42d15811925`、`d13bbc1cb12` | 全量 cherry-pick |
| xhttp 传输语义 | `7c86968d361`（Reset 代替 Close） | 全量 cherry-pick |
| 协议 carrier 测试 | tuic/hysteria2/trusttunnel/anytls/ssh/masque×3/naive/wireguard×2、daemon×2、route 时间线 | 全量 cherry-pick |
| 依赖 pin | `ce4ae88a5e0`（quic-go→`4b11a7be` + TrustTunnel 测试断言）、`dad5fc31bc6`（connid mutex pin） | pin 部分丢弃：阶段 02 的 quic-go `ca9992bc` 已包含 `4b11a7be` 与 connid 互斥；`ce4ae88a5e0` 的**非 pin hunk**（TrustTunnel 服务端 CONNECT 断言）单独应用，源 SHA 记录在提交 trailer |
| 其余测试 | 共 34 个 | 全部 cherry-pick |

验证：把分支触及的 **43 个文件**逐个与分支 tip 比对，**39 个相同**；4 个不同且都是"我们更新"：

- `go.mod` / `go.sum`：我们的 pin 是阶段 02 的 `ca9992bc`（分支是旧的 `4b11a7be`）。
- `route/conn.go`：我们有候选基线更新的 `resourceguard.GrowthBudget`（按方向共享 copy 增长预算），分支没有；分支的回收/准入改动全部存在。
- `transport/v2rayxhttp/client.go`：我们有候选基线更新的 `sharedUplinkBudget` / Box 级 `UploadBudget` 绑定，分支没有；分支的 Reset 改动全部存在。

也就是说，网络分支的改动与候选的"增长预算/上传预算"是**正交叠加**，没有出现"两边各留一个 close"或"用旧实现覆盖新实现"的情况。

### 3.2 `closeout/resources`：只取测试内容，pin 由阶段 02 决定

21 个独有提交拆成三类：

- **10 个 `B16*` pin 递增提交**：只改 `go.mod`/`go.sum`/`release/source-lock.json`/test 模块，目标 pin 从 `1671498` 一路递增到 `57e9d17fa852`。阶段 02 的最终 sing-tun 恰好就是 `57e9d17fa852`，所以这些提交全部不需要移植；`source-lock.json` 也不在本阶段手改（第 06 阶段由 release-freeze 生成）。
- **6 个 merge 提交**（`closeout: merge ...`）：合并的是下面的内容提交，本身不含新内容；按纪律不移植。
- **6 个内容提交**：xhttp shaped A/B、resourceguard relay A/B（含 fixture 修正与 inversion switch）、e2e UDP detour 与窄 MTU 变体——全部 cherry-pick。

验证：`common/resourceguard/relay_shaped_ab_test.go`、`e2e/tun_udp_detour_test.go`、`transport/v2rayxhttp/packetup_real_server_ab_test.go` 三个文件与 `closeout/resources` tip **逐字节相同**，证明跳过的 16 个提交没有携带别的代码。

### 3.3 `next/resources`

`next/resources`（`b00dfe1f0e08`）是 `closeout/resources` 的祖先（阶段 01 已用 `is-ancestor` 证明）。它的 6 个提交全部包含在 closeout 的 21 个里，因此不单独集成；其独有的 3 个文件就是被移植的 3 个测试文件。

### 3.4 `backup/02-ecfffe7d0`：审查结论（不集成）

该分支 44 个提交、94 文件，候选基线没有等价补丁（阶段 01：0 patch 等价）。按本阶段"只找回缺失实现"的要求逐项审查：

| 独有内容 | 文件 | 与候选的关系 | 结论 |
|---|---|---|---|
| Route 诊断 API（`DiagnoseRoute`、`diagnoseWalk`、`diagnoseResolve`…） | `route/route_diagnose.go` 新增 424 行 + router 方法 | 候选**没有** `DiagnoseRoute` | 不属于网络/资源生命周期范围；标记给第 05/06 阶段做产品范围决策 |
| DNS 历史 | `route/route_dns_history.go` | 候选**已有**（Enable/Disable/Query/Stats/Clear） | 不重复移植 |
| Route 预览 | `route/route_preview.go`、`route/dns_preview_bridge_test.go` | 候选已有 `PreviewRoute`；备份额外加的是 `PreviewDNSLookup`（DNS 层预览） | 候选缺的部分属独立特性，未移植 |
| Process cache reset epoch（提交锁 + 代际检查） | `route/process_cache.go` | 候选**没有**该代际逻辑（`processCacheEpoch`/`processCacheCommit` 均不存在） | 与连接归属相关但属独立修复；未在本阶段强行套旧 diff，记录给第 05/06 阶段评估 |
| libbox/daemon/trafficcontrol 连接快照与流控 | `experimental/libbox`、`daemon`、`common` 等 | 客户端侧工作线 | 属第 05 阶段（NE/客户端）范围 |

决定：**不整支 merge、不挑选性移植**。原因：上述三项都不是本阶段两条来源分支要解决的网络生命周期/资源治理问题；强行移植会把 176 个提交前的上下文带进来（原则 15：不扩大变更面）。审查证据与文件清单已保存。

### 3.5 `pending/device-axis-reload-window-tests`：选择性移植

- 分支 tip `0477e327e5da`，只有 1 个提交、3 个测试文件。
- `common/runtimecoord/device_axis_reload_window_test.go`：移植后**4/4 PASS**（`TestPlatformMirrorDivergesFromTheLevelAcrossAReloadWindow`、`TestPlatformMirrorLosesALongLockEntirely`、`TestPlatformReconciliationCanMoveTheLevelBack`、`TestPlatformDirectWriterDoesNotTouchTheMirror`），说明当前实现已具备这些语义，测试有独立回归价值。
- 两个 `experimental/libbox` 测试：阶段 01 已证明它们**在该分支自己的 base `4c7d96c0fbc` 上也同样 4 红 3 绿**，不是候选回归；分支本身是红的，因此**拒绝原样移植**（若需要，应按当前实现重写）。

---

## 4. 决策理由

- **cherry-pick 而不是 merge `next/network`**：分支基线落后 193 个提交，整支 merge 会引入陈旧上下文；cherry-pick 逐个保留作者、说明与测试，遇到冲突可单独审查。实际 34 个提交 0 冲突，说明候选基线已把该分支的上下文保留得足够完整。
- **pin 提交按内容拆分而不是照单 cherry-pick**：`ce4ae88a5e0` 的 pin 目标是 `4b11a7be`，比阶段 02 的 `ca9992bc` 旧；但其 TrustTunnel 断言修正有独立价值。只取非 pin hunk，并在提交信息里写明源 SHA 与被丢弃的部分，避免"patch-id 不一致却声称等价"。
- **closeout 不取 pin 提交**：它的最终目标就是 `57e9d17fa852`，正是阶段 02 已收敛的 SHA；再逐级 cherry-pick 会把 `go.mod`/`source-lock` 变成中间状态，违反"同一事实只有一份"。
- **资源测试直接取内容提交**：merge 提交只是聚合，内容提交独立可验证；三个测试文件与分支 tip 的逐字节一致性是"没有遗漏"的证据。
- **backup 只审查不移植**：其独有内容都不在本阶段的功能边界内；移植旧上下文的风险大于收益。
- **pending 只取 runtimecoord**：测试已在当前实现上通过，属于纯回归覆盖；libbox 测试在自身 base 即红，移植会引入假红。

---

## 5. 关键设计与状态/所有权

完整的状态机、所有权表与加锁顺序见 **`03_STATE_AND_OWNERSHIP.md`**。要点：

1. **准入票据（`adapter.ConnectionAdmission`）**：`AcquireConnection` 在 manager 生命周期锁内预留槽位，`TrackConn`/`TrackPacketConn` 消费票据，失败/取消用 `Release`；shutdown 后 `Track*` 返回 `net.ErrClosed` 并把 socket 交给 disposal 账本。`Close` 等 `admitted` 归零。控制平面（DNS）走独立的 4 槽预留池，不绕过 shutdown。
2. **generation + 过渡体**：网络切换先认领 generation，过渡体只做 drain、DNS 固定、提交；不再等旧 socket 的物理 close。监听器在提交后**并发**执行，共享一个 deadline；vless/vmess/trojan 把 deadline 通过 `ResetContext` 传到 v2rayquic 的迁移候选。
3. **disposal 账本**：`queue`（已入队）、`pending`（含正在关闭）、`condemned`（超预算但 owned）、`syncing`（CloseAll 同步关闭）、`admitted`（票据）。单一 worker（并发 1）执行物理关闭；预算按连接数 64；backlog 满时新拨号在准入处等待并可被 ctx 取消；shutdown 时排队并等待全部归零。
4. **与 sing-tun 的对应**：TUN 队列关闭清空全部 waiter；设备字节预算跨队列唤醒；detach/close 归还配额。资源测试（relay A/B、UDP detour）跑在阶段 02 的 sing-tun 上。
5. **与 quic-go 的对应**：connID 单互斥消除 `Path.Close` 与 run loop `Close` 的竞争；`http3` 引用计数修复让 H3 连接能被 `CloseIdleConnections` 回收。本阶段 `-race` 的 v2rayquic/xhttp 套件覆盖。

---

## 6. 文件变更与行为变化

- 45 个文件（32 新增、13 修改），+7980/−111；内容提交 42 个（34 network + 1 TrustTunnel hunk + 6 resources + 1 pending），依赖基线与文档提交另计。
- 13 个生产文件：`adapter/connection_admission.go`（新）、`common/dialer/default.go`、`dns/transport_dialer.go`、`protocol/{vless,vmess,trojan}/outbound.go`、`route/{conn,conn_reclaim,network,network_environment}.go`、`transport/v2rayquic/client.go`、`transport/v2rayxhttp/client.go`、`transport/v2rayquic/run_dualpath_migration.sh`。
- 行为变化（用户可见）：
  - 网络切换不再把所有流当停机处理：旧 generation 按 reason 回收，空闲超过 20s 的连接才被 drain 清扫；正在传输的流保留，直到它自己结束或转为空闲。
  - 新 generation 的连接不再因为旧 socket 的慢 `Close` 而被拒（"network changed while dialling" 窗口被移除）。
  - 旧 socket 的物理关闭由后台 worker 完成，慢内核关闭不再阻塞 DNS 固定与切换提交。
  - 队列/连接积压超过 64 时，新拨号在准入处背压等待（可取消），而不是无限堆积。
  - 冷启动 DNS 在数据面积压满时仍能建立（4 槽控制平面预留）。
  - xHTTP 在网络切换时走 Reset（保留 generation 语义）而不是 Close；V2Ray QUIC 的候选迁移受切换 deadline 约束，按路径释放清理任务。
- 行为变化（内部/诊断）：新增 transition/disposal/准入相关诊断计数与测试钩子（`disposalStageHook` 等，仅测试可见）。

---

## 7. 测试矩阵

证据：`_int0145/audit-evidence/stage03/`（每条命令的完整输出与退出码）。

| 范围 | 命令 | 退出码 | 结果 |
|---|---|---|---|
| 构建（Windows profile） | `go build -tags <win profile> ./...` | 0 | 45 文件集成后整树通过 |
| 构建（无 tag） | `go build ./...` | 0 | 通过 |
| route 全量 | `go test -vet=off -tags <win> ./route` | 0 | **700 个测试函数**全量通过（447.9 s） |
| 网络相关包 | `./route/rule ./common/dialer ./adapter/... ./dns/... ./daemon ./transport/...` | 0 | 24 个包全部 ok（75.8 s） |
| 新生命周期测试（race） | `go test -race -run <28 个新测试> ./route` | 0 | 28/28 PASS |
| 传输/拨号 race | `-race ./transport/v2rayquic ./transport/v2rayxhttp ./common/dialer ./common/resourceguard` | 0 | 4 包全部 ok |
| 协议 carrier | 12 个 protocol 包（含 tuic/hysteria2/anytls/ssh/trusttunnel/naive/masque/wireguard/tun/vless/vmess/trojan） | 0 | 全部 ok（naive 需把 pin 的 `libcronet.dll` 所在目录加入 PATH，已记录） |
| e2e UDP detour | `./e2e -run TestTunUDPDetourChain` | 0 | 含 detour / 窄 MTU 1280 / 无 detour 三个子测试全部 PASS |
| daemon selector | `./daemon -run <selector 测试>` | 0 | 6/6 PASS |
| runtimecoord（pending 移植） | `./common/runtimecoord -run TestPlatform` | 0 | 全部 PASS（含 4 个新测试） |
| 广域回归 | `./common/... ./config/... ./provider/... ./option/... ./constant/...` | 1 | 32 个包 ok；`common/tls`、`common/tlsspoof`、`common/windivert` 失败 |

失败归因（已验证：在阶段 03 依赖基线 `ea5651f2b5b` 上**同样失败**，与本阶段集成无关）：

- `common/windivert`、`common/tlsspoof`：`windivert: open SCM: Access is denied` —— 需要管理员权限/WinDivert 驱动，**BLOCKED_BY_ENVIRONMENT**。
- `common/tls` 的 `TestWindowsClient*TLS13`：`tls handshake: read handshake: unexpected EOF` —— 候选基线已有的 Windows TLS 1.3 客户端失败，非本阶段引入，记录为既有问题。

未执行/受限：

- `route/disposal_fd_linux_test.go`：Linux-only，Windows 上 **NOT RUN / BLOCKED**；真实 FD 计数属于第 06 阶段的 Linux 验收。
- `TestGoKernel*`、真实 TUN 设备测试：需要管理员/TUN 设备，**BLOCKED_BY_ENVIRONMENT**。
- 全量 `go test ./...`（含所有包的一次性执行）：NOT RUN；本阶段按上层包分类执行（route 全量 + 网络相关包 + 广域回归）。
- `-race` 全树：NOT RUN（只对新生命周期与传输/拨号/资源模块执行，见上）。

---

## 8. 风险、遗留与接口约束

1. **预 pin**：`go.mod`/`test/go.mod` 已指向阶段 02 的两个集成 SHA，但 `release/source-lock.json` 仍是 0.1.44 快照。这是刻意的中间态：第 06 阶段用 release-freeze 重新生成，禁止手改锁文件。
2. **backup 的三项独有内容未集成**（route diagnose、process-cache reset epoch、客户端流控）。它们不属于本阶段边界；第 05/06 阶段若要 0.1.45 包含 route 诊断或该缓存修复，需要单独立项并按其自身测试补齐。
3. **Linux 真实 FD/E2E 未跑**：Windows 上无法验证 `disposal_fd_linux_test.go` 与真实 `/dev/net/tun` 路径；第 06 阶段必须在 Linux runner 上补跑，不能以 Windows 单元测试替代。
4. **管理员权限测试未跑**：TUN/Kernel/WinDivert 相关用例在本机 blocked；平台验收时需在有权限的环境执行。
5. **网络切换的延迟预算**：监听器阶段有统一 deadline（`networkChangeListenerBudget`）与并发上限；第 04 阶段把 H3/H2 回退并入后，需要重新确认该预算对新增监听器的余量。
6. **候选基线的两处"更新内容"被保留**（`GrowthBudget`、`sharedUplinkBudget`），分支没有；后续阶段不要用旧分支文件覆盖这两个文件，否则会回退候选已有功能。
7. **`common/tls` 的 Windows TLS1.3 失败**是既有问题，不属本阶段；若第 06 阶段全平台验收复现，应单独立项修复，不能标 PASS。

---

## 9. 完成条件对照

| 文档要求 | 状态 |
|---|---|
| `next/network` 的有效增量已吸收或逐项延期 | ✅ 34 提交原样 cherry-pick；2 个 pin 提交按内容拆分（pin 由阶段 02 覆盖，测试 hunk 单独应用） |
| `closeout/resources` 与 `next/resources` 已去重并融合 | ✅ closeout 取 6 个内容提交、3 个测试文件与分支逐字节一致；next/resources 为祖先，不单独处理 |
| 旧备份与 pending 测试已审查 | ✅ backup 逐项结论见 §3.4；pending 移植 runtimecoord 4 测试、拒绝 libbox 2 文件并给出原因 |
| 网络生命周期和资源所有权有清晰说明 | ✅ `03_STATE_AND_OWNERSHIP.md` |
| 关键测试、race、E2E 已运行或标记阻塞 | ✅ §7；Linux FD / 管理员 / 平台构建标记 BLOCKED |
| 无未解释的 go.mod/source-lock 旧 pin 被带入 | ✅ 只把两个 replace 提升到阶段 02 SHA，并在首个提交说明；source-lock 未手改 |

阶段 03 内容提交见 `INTEGRATION-LOG.md` 的 Stage 03 记录与 `git log --oneline`。
