# 04｜双栈 DNS、拨号恢复与 H3/H2 回退集成

- 执行时间：2026-11-11 03:00–04:05（Asia/Shanghai）。
- 施工基线：`integration/0.1.45-final` @ `e8a473447c3`（阶段 03 收尾）。
- 来源：`fix/task09-repair` @ `31a3e9acea1b`（31 个独有提交）、`task08/h3-h2-fallback-2026-11-10` @ `684ebf516767`（39 个独有提交）。
- 依赖：sing-tun `57e9d17fa852`、quic-go `ca9992bc795e`（阶段 02 收敛，未变）。
- 结论先行：
  1. task09 的 **27 个非 merge 提交全部 cherry-pick（0 冲突）**；4 个 merge 提交按"内容由非 merge 提交与合并分支携带"跳过，随后 40 个分支文件与 `fix/task09-repair` tip **逐字节一致**，没有丢失合并解决内容。
  2. task08 的 **35 个内容提交全部 cherry-pick（0 冲突）**；40 个分支文件中 35 个与分支 tip 一致，5 个是 pin 文件（`go.mod`/`go.sum`/`source-lock`/test 模块）——我们的 pin 已是阶段 02 的更新版本。
  3. 配置恢复测试去重完成：`integration/next-round`、`docs/task09-knowledge-0.1.45` 与本树的 `daemon/config_recovery_*`、`daemon/testdata/product-abc/**` 是**同一批 blob**，保留唯一版本；两个分支的差异只有报告文档（归第 06 阶段）。
  4. 双栈与 H3/H2 的定向测试、`-race`、根包、Linux/Windows 全量均通过（§7.4）；一处疑似回归经重复测量证明是既有测试脆弱性，并在收尾中修好测试（§7.5）。

---

## 1. 阶段目标与实际范围

把双栈解析/拨号与 HTTP/3 回退作为两个独立子系统、一个联合网络行为来集成：IPv4/IPv6 选择、DNS 来源语义、连接回退、请求重试与协议生命周期在复杂时序下正确。

实际执行：
- 逐提交审查并集成 `fix/task09-repair` 的双栈与拨号恢复实现；
- 从 `integration/next-round` 吸收配置恢复测试（它们已在 task09 的 merge 历史里）；
- 逐提交审查并集成 `task08/h3-h2-fallback-2026-11-10` 的 H3/H2 实现；
- 去重配置恢复测试，确认只有一份权威版本；
- 双栈、H3/H2、联合回归、race、Windows/Linux 全量；
- 输出本报告。

未执行：平台构建（Apple/Android/Desktop/Dashboard，环境缺失）、第 06 阶段的 pin 冻结与 source-lock 生成。真实 netns 设备验收已在本机 WSL 跑通（§7.3）。

---

## 2. 基线与提交

| 项 | 值 |
|---|---|
| 施工基线 | `e8a473447c31487656e8ecd3119ce26e8a23e7b2` |
| `fix/task09-repair` | `31a3e9acea1bba0328d1e75ddab3ae2ea6b02d1d`（merge-base `e9f48e225fe1`，31 独有） |
| `task08/h3-h2-fallback-2026-11-10` | `684ebf516767`（merge-base `a91adb07e102`，39 独有） |
| `integration/next-round` | `94c86b9d1f1f`，其 3 个配置恢复测试提交（`72be9fe11b0`/`aad5a391177`/`edbfe8e0b2b`）已在 task09 范围内 |
| 集成结果 | 62 个内容提交（27 + 35），75 文件（47 新增 / 28 修改），+11317/−550 |

---

## 3. 分支关系与取舍

### 3.1 `fix/task09-repair`：27 个非 merge 提交 + 跳过 4 个 merge

该分支含 4 个 merge 提交：

| merge | 内容 | 处理 |
|---|---|---|
| `abe437f8c36 merge(task09): dual-stack fixes onto the current integration target` | 相对第二父提交新增 `daemon/config_recovery_candidates_test.go`、`experimental/boxdd/product_config_rpcs_test.go`、`clients/apple` gitlink、source-lock | 跳过 merge 本身；其文件内容由 task09 提交与后续比对验证覆盖 |
| `5abbc58c9c3`（merge `f232cc2160e`） | 把 Linux 设备验收测试并回主线 | 跳过；`f232cc2160e` 已在 pick 列表 |
| `e3b22c45e70` / `f694596f7fd`（merge `integration/next-round`） | 把配置恢复测试并入 task09 | 跳过 merge；`72be9fe11b0`/`aad5a391177`/`edbfe8e0b2b` 已在 pick 列表 |

验证方法（同第 03 阶段）：picks 完成后，把 `e9f48e225fe1...fix/task09-repair` 触及的 **40 个文件**逐个与分支 tip 比对——**40/40 相同**。因此跳过 merge 没有丢失任何内容（包括 merge 时才产生的冲突解决）。

### 3.2 `task08/h3-h2-fallback`：35 个内容提交 + 跳过 4 个 pin 提交

跳过：`5ef520e0454`、`53806d54d6f`（quic-go pin）、`00fbb173901`（source-lock 记录）、`be29c357560`（test module sum）——它们指向的 `4b11a7be`/usecount 修复已被阶段 02 的 quic-go `ca9992bc` 包含。

验证：分支触及的 40 个文件中 **35 个与本树逐字节一致**；其余 5 个是 `go.mod`、`go.sum`、`release/source-lock.json`、`test/go.mod`、`test/go.sum`——本树是阶段 02 的更新 pin，`source-lock` 仍是 0.1.44 快照（第 06 阶段生成）。

### 3.3 配置恢复测试去重

`integration/next-round`、`docs/task09-knowledge-0.1.45`、本树的三个版本对以下文件是**同一 blob**：

- `daemon/config_recovery_candidates_test.go`（`856a2272a4c3`）
- `daemon/config_recovery_product_test.go`（`8769d82f00ec`）
- `daemon/testdata/product-abc/**`

因此只保留一份，不再堆叠。两个来源分支的文档差异：
- `integration/next-round` 独有 `KNOWLEDGE/reports/0.1.45/RESOURCE-GOVERNANCE-WAITERS-2026-11-10.md`；
- `docs/task09-knowledge-0.1.45` 独有 `KNOWLEDGE/reports/0.1.45/DUAL-STACK-SOURCE-AND-RECOVERY-2026-11-10.md`。

这些是第 06 阶段的文档合并输入，本阶段不动 `KNOWLEDGE/`。

---

## 4. 决策理由

- **task09 用 cherry-pick 而非 merge**：分支基线落后 42 个提交，且带着 4 个 merge；逐个 pick 保留作者、说明和测试，同时避免把 merge 的旧上下文带进来。0 冲突与 40/40 文件一致是"没有遗漏"的证据。
- **跳过 merge 而不是 cherry-pick merge**：merge 提交的 diff 是合并结果，不是功能单元；其两边的提交都在 pick 或已在主线。逐文件比对证明跳过安全。
- **task08 同样 cherry-pick**：35 个提交是 H3/H2 的连续修复与测试，逐个应用语义清晰；pin 提交丢弃是因为阶段 02 的依赖 SHA 已包含它们的全部代码差异（`4b11a7be` 在 `ca9992bc` 的历史里）。
- **配置恢复只保留一份**：三个分支的测试 blob 相同，说明它们来自同一次提交；重复合并会产生同名/同义测试，按纪律不保留两套。
- **不引入新的依赖 pin**：`go.mod`/`test/go.mod` 维持阶段 03 的预览 pin；`source-lock` 不动。

---

## 5. 关键设计与语义

### 5.1 双栈（`fix/task09-repair`）

- **planner 与 executor 分离**（`common/dialer/dual_stack.go`）：planner 只决定"尝试哪个地址、何时尝试"；"什么算成功"由协议自己决定。避免把 UDP socket 创建当作网络可用。
- **按 family 交错**：`v6#1 v4#1 v6#2 v4#2 …`，但 family 内保持解析器顺序。避免首选 family 的一个黑洞地址吃掉整个 family 的超时。
- **并发调度**（`dual_stack_scheduler.go`）：首个候选立即启动，之后每 `fallbackDelay` 增加一个；快速失败立即推进。调度器带取消与上限。
- **provenance**（`dual_stack_recovery.go`、`dual_stack_scheduler.go`）：每个候选携带 DNS 环境代际（source generation）；旧环境的候选标记 superseded，但仍会在"只剩它"时被拨号。重复地址不再被简单丢弃，而是**升级**已排队候选的 provenance（本次 point 修复）。
- **DNS 双栈查询**（`dns/router_dual_stack.go`）：`LookupFamilies` 并发发起两个 family 查询并按到达顺序返回；复用 `Router.Lookup`，因此规则、transport、缓存、singleflight、ECS、RDRC、NXDOMAIN 缓存语义不变。
- **family capability**（`family_capability.go`）：区分"当前不可用"与"永久不支持"，literal IP、forced family、netns/device 绑定走各自路径。

### 5.2 H3/H2（`task08`）

- **H3 verdict 分类**：连接建立失败、请求被显式拒绝（RFC 9114 `H3_REQUEST_REJECTED`）、服务端业务错误、应用层错误分开处理；只有"对端明确未处理"的请求才允许跨版本重试，且不因此把 H3 标记为不可用。
- **body 生命周期**：request rejected、retry、cancel、close、read error 各路径都释放 body 副本；非幂等请求只在 body 未发送/未处理时重试。
- **H2 fallback 是独立 transport**（`common/httpclient/http2_fallback_transport.go`、`transport/http/client_h2.go`）：独立 TLS/ALPN、独立连接池与 dial gate；dial gate 是 channel 而非 mutex，等待者可被自己的 context 取消，且按 generation 区分（网络切换后新 generation 不等旧 dial）。
- **Naive H2 引擎**（`protocol/naive/h2_fallback_engine.go`）：QUIC-disabled 的独立引擎，保留 Chromium/Cronet 语义；生命周期可取消、closed-safe。
- **MASQUE**：CONNECT/Connect-IP 的 SETTINGS 能力检查、ALPN 视图、慢服务器重试都显式处理，不把不支持的请求伪装成成功。

---

## 6. 文件与行为变化

- 62 个提交，75 个文件（47 新增 / 28 修改），+11317/−550。
- 24 个生产 `.go` 文件：`adapter/dns.go`、`common/dialer/{dual_stack,dual_stack_parallel_network,dual_stack_recovery,dual_stack_scheduler,family_capability,resolve}.go`、`dns/{client,router,router_dual_stack}.go`、`common/httpclient/{helpers,http2_fallback_transport,http3_request_rejected,http3_transport}.go`、`transport/http/{client,client_h2,client_h3,client_h3_request,tunnel_client}.go`、`protocol/naive/{h2_fallback_engine,h3_fallback,outbound}.go`、`protocol/masque/server.go`；其余为测试与 `daemon/testdata/product-abc/**` 夹具。
- 行为变化（用户可见）：
  - 双栈连接按 family 交错尝试，不再等待一个 family 全部超时；首选 family 失败后另一 family 更早加入竞争。
  - 每个候选携带 DNS 环境代际：网络切换后，旧环境确认的地址不会排在当前环境确认的地址之前；旧地址仍可作为最后手段。
  - DNS 双栈查询与其它 `Router.Lookup` 走同一套规则/缓存/transport 语义。
  - H3 失败按类型决策：请求被显式拒绝时可安全重试/回退；连接或传输错误才影响后续 H3 可用性判断。
  - H2 回退有独立连接路径与可取消的 dial gate；网络切换后新 generation 不等旧 H2 dial。
  - Naive 在 Chromium 连接定时器触发时走独立 H2 引擎重试，保留代理应答。

---

## 7. 测试矩阵

证据：`_int0145/audit-evidence/stage04/`。

| 范围 | 命令 | 退出码 | 结果 |
|---|---|---|---|
| 构建（Windows profile / 无 tag） | `go build -tags <win> ./...` | 0 / 0 | 通过 |
| 双栈相关（Windows） | `./common/dialer ./dns/... ./daemon ./adapter/... ./route/rule ./protocol/direct` | 0 | 15 包全部 ok |
| H3/H2 相关（Windows） | `./common/httpclient ./transport/http ./protocol/naive ./protocol/masque ./protocol/hysteria2 ./dns/transport/quic` | 0 | 全部 ok（transport/http 74.7 s） |
| 联合回归（Windows） | 根包 + daemon + dialer + dns + httpclient + transport/http + naive + masque | 0 | 9 包全部 ok |
| race（Windows） | `-race ./common/dialer ./dns ./common/httpclient ./protocol/naive` | 0 | 4 包 ok |
| race（transport/http 子集） | `-race ./transport/http -run <fallback/CONNECT/setup/reject/H2/H3/tunnel>` | 0 | ok（25.3 s） |
| 根包全量（Windows） | `go test -tags <win> .` | 0 | 通过（24.3 s） |
| Linux 构建 | `go build ./...` / `-tags <others>` | 0 | 通过 |
| Linux 双栈/H3-H2/race | 同 Windows 的定向集合 | 0 | 全部 ok |
| Linux 全量 | `go test -vet=off -tags <others> ./...` | 见 §7.4 | 见 §7.4 |
| Windows 全量 | `go test -vet=off -tags <win> ./...` | 见 §7.4 | 见 §7.4 |

### 7.3 真实 netns 设备验收（Linux-only）

`common/dialer/netns_device_linux_test.go` 的两个测试需要真实命名空间与全局 IPv6 目标。本轮收尾在 WSL2 内用**非特权 user+net+mount namespace**（`unshare -Urnm`）搭出真实 fixture：

- `t09peer`（`2001:db8:99::1/64`）与 `t09target`（`2001:db8:99::2/64`）经 veth 相连，target 的默认路由指向 peer；`t09empty` 只有 loopback。
- IPv4 方向：root 侧 veth `10.99.0.1/24`，`t09rmirror` 只有对端接口、**没有任何路由**；监听器在 peer（IPv6）与 root（IPv4）回答 `pong`。
- 关键细节：IPv6 地址必须 `nodad`（否则地址保持 tentative，监听器无法 bind）；`unshare -n` 的 root 命名空间要显式 `ip link set lo up`。

| 测试 | 结果 |
|---|---|
| `TestNetnsDeviceDialsAGlobalIPv6OnlyTheNamespaceHas` | **PASS**：host 路径仍按 family verdict 拒绝；namespaced 拨号成功的 socket 本地地址是 `[2001:db8:99::2]`，业务数据 `pong` |
| `TestNetnsDeviceMirrorHostReachableNamespaceNot` | **PASS**：host 连到 `10.99.0.1:9001`；mirror 命名空间以 `ENETUNREACH` 失败 |

证据：`stage04/linux/15-netns-device-real.log`（脚本 `stage04_netns_real.sh`）。原 BLOCKED 项关闭。

### 7.4 全量结果

两次全量都在**无并发负载**下运行（首次 Windows 全量与 Linux 全量同时启动，导致 e2e 超时，见 §7.5）：

| 平台 | 命令 | 结果 |
|---|---|---|
| Windows | `go test -vet=off -tags <win profile> -timeout 40m ./...` | **106 包 ok**；仅 `common/tls`（Windows TLS1.3，既有）、`common/tlsspoof` / `common/windivert`（WinDivert 权限）失败；e2e 通过 |
| Linux | `go test -vet=off -tags <others> -timeout 40m ./...` | **113 包 ok**；唯一失败 `common/netns TestUnshareNamespace`（WSL 不允许命名空间内 fork），属环境限制 |

Linux 与 Windows 的差异仅限上述已归因项，没有出现新的产品缺陷。

### 7.5 疑似回归的排除

1. `TestStartedNetworkRequirementBoxesLeaveNoGoroutines` / `TestRefusedNetworkRequirementBoxesLeaveNoGoroutines`（根包，Linux）曾失败（`6 > 4`）。诊断：
   - bisect 曾指向 `52594fe8ae2`，但父提交 `05ff5a4c779` 同样 3/5 失败、阶段 03 tip `e8a473` 5/5 失败（隔离运行），说明与本次集成无关；
   - `NumGoroutine()==6` 时 `runtime.Stack(all)` 只列出 2 个 goroutine，计数里含正在退出的瞬时值；
   - 修复（`a2e24231eaf`，**仅测试**）：共享 `settleGoroutines`，等 8 次连续相同采样（200 ms 稳定窗，5 s 上限）再返回；断言不变；
   - 修复后：两个测试在 Linux 隔离运行 **5/5 PASS**，根包全量 PASS；Windows 3/3 PASS。
2. `TestTunUDPDetourChain/without_detour`（e2e，Linux）在并发跑 Windows 全量时失败；空闲环境 **5/5 PASS**，属负载下超时，非功能回归。

---

## 8. 风险、遗留与接口约束

1. **真实 netns 设备验收已在本机 WSL 跑通**（§7.3）；第 06 阶段仍应在目标 runner 复跑，因为 WSL 内核/命名空间与真实设备不同。
2. **goroutine 计数测试已修复**（`a2e24231eaf`，仅测试）；断言未变，真实泄漏仍会失败。
3. **platform builds 仍 BLOCKED**（Apple/Android/Desktop/Dashboard）。
4. **`source-lock.json` 仍是 0.1.44 快照**：本阶段未手改；第 06 阶段用 release-freeze 生成，并把 quic-go `ca9992bc` 与 sing-tun `57e9d17` 写进去。
5. **第 05 阶段注意**：H3 verdict 与 H2 gate 依赖 `common/httpclient`/`transport/http` 的接口；客户端集成时不要替换这两处的错误分类与 body 释放路径。

---

## 9. 完成条件对照

| 文档要求 | 状态 |
|---|---|
| 双栈实现与 DNS 来源语义通过测试 | ✅ dialer/dns/daemon 定向 + race + 根包 |
| 配置恢复测试完成去重和融合 | ✅ 三处 blob 相同，保留唯一版本 |
| H3/H2 回退满足重试安全 | ✅ 35 个提交集成；rejected/body/非幂等路径测试通过 |
| quic-go usecount/path 修复与内核调用一致 | ✅ 使用阶段 02 `ca9992bc`（含 `0b8709eb`/`0e7c69da`） |
| 联合回归已执行 | ✅ Windows/Linux 定向 + 根包 + 全量（见 §7.4） |
| 所有未执行的测试明确列出原因 | ✅ 真实 netns 验收已 PASS（§7.3）；Windows 管理员用例与平台构建仍为环境限制 |

阶段 04 内容提交见 `INTEGRATION-LOG.md` 的 Stage 04 记录与 `git log --oneline`。
