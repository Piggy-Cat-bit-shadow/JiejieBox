# EVIDENCE —— 命令、revision、环境与结果

本文件是本轮 01 的证据索引。**每条都写明证据等级**；主机单测/基准不能当设备或互通证据。

## 环境

| 项 | 值 |
|---|---|
| OS | Windows 10（`Microsoft Windows NT 10.0.19044.0`，build 19044） |
| CPU | AMD Ryzen 7 4800H with Radeon Graphics（`GOMAXPROCS` 报告 16） |
| Go | `go1.25.5 windows/amd64`（GOROOT `<toolchain>`，`GOPATH <toolchain>/gopath`） |
| C 编译器（仅 `-race` 需要） | `gcc.exe (Rev6, Built by MSYS2 project) 16.2.0`，`CGO_ENABLED=1` |
| 采集时间 | 2026-11-08（Asia/Shanghai） |

复现时先设置工具链，再在仓库根执行命令；`-race` 需要可用的 gcc。

## E01 仓库同步与阶段 revision

```text
进入时：git status -sb → ## testing...origin/testing；HEAD=0de1c13dcb3（2026-11-07 09:19:36 +0800，release(0.1.40)）
git rev-list --left-right --count origin/testing...HEAD → 0    0（本地无提交，落后 39）
git fetch origin --prune；git ls-remote origin refs/heads/testing → 25f5c9a638b
git merge --ff-only origin/testing → 快进；HEAD=25f5c9a638b（2026-11-08 02:08:39 +0800）
git submodule update --init clients/apple → e3283104c98c（父仓 pin）
```

- 结果：`testing` = `25f5c9a638bdae4a1e2bb460641795f10b169bb2`，与远端一致。
- 收尾：阶段结束时 `clients/android` 已从进入前检出的本地分支 `fix/libbox-stringbox-callsites`（tip `9d15717`，2 个仅本地提交）恢复到父仓 pin `c7837107dc01`；子模块工作树干净，分支与提交保留，可 `git -C clients/android checkout fix/libbox-stringbox-callsites` 恢复。该操作没有删除任何提交，也没有进入父仓提交。
- 等级：`VERIFIED_GIT`。

## E02 本阶段触及区域的知识增量

```sh
git rev-list --count fc7e400243d46a451b2dffbe68cb5a3147c66289..25f5c9a638bdae4a1e2bb460641795f10b169bb2   # 49
git diff --name-only fc7e400243d46a451b2dffbe68cb5a3147c66289..25f5c9a638bdae4a1e2bb460641795f10b169bb2 -- '*.go' ':(exclude)*_test.go'
git log --oneline 3b6ec64f7145a2add99656d47caaa4f22a1cc478..25f5c9a638bdae4a1e2bb460641795f10b169bb2   # 6 个纯文档提交
```

- `common/resourceguard`、`service/oomkiller`、`common/tls`、`protocol/anytls` 在 v0.1.39→施工基线区间**没有生产代码变更**；唯一变化是 `protocol/anytls/session_burst_test.go`（测试）。生产变更集中在 provider/outbound/route/TUN/生成器与文档。
- 结果：01 依据的 gate/guard/recorder/timer 代码与施工基线一致；知识库 `modules/resource-admission.md`（v0.1.39 校准）仍适用。
- 等级：`VERIFIED_GIT`。

## E03 参考项目远端 refs（S01）

```sh
git ls-remote --symref <repo> HEAD
```

| repo | ref/HEAD |
|---|---|
| `NodePassProject/Anywhere` | `main` = `8b1ec05bc5e0e2d4e477d9aced0b9832bbeff61d` |
| `ProjectClash/Clash` | `main` = `e6334ad18cc7ee7582bf0f3a70681d58a2b61eb5` |
| `ProjectClash/Clash-Legacy` | `main` = `da4952d3cac6794138835da23f293171d2593704` |
| `TokenPLS/Hako` | 解析到同一 `da4952d3cac…`（重定向） |
| `ProjectClash/Clash-Client` | `main` = `e3e647283b1593a61dba622f13cd499deeda9b8b` |
| `ProjectClash/Clash-Adapter` | `main` = `d47c04a3556a67f95929aaa15ae624c7df631e74` |
| `reF1nd/sing-box` | `reF1nd-stable` = `732618a5989cd2566f072c374fe2f619200f33f9`；`testing` = `a6952ccf6907c18e18917fcb763a433e8e36937b`；`reF1nd-testing` = `17397d022647b73378705e1f91ff031dddaaec21` |
| `reF1nd/sing-box-releases` | `v1.14.4-reF1nd` 标签对象 = `de5581802e18e99c5040da1e23404dee2312b253` |
| `Leadaxe/sing-box-lx` | `lx` = `2d9a0a52df7881f55484d48f9daecb21d135f3e9` |
| `ghostlane-project/ghostlane` | `main` = `479019064d1b5cd035275a5a79cd841b2f463a70` |
| `pickrui/oixCloud-docs` | `main` = `25320145fad64b76d38090b8d4bbedc64565c938` |
| `pickrui/oixcloud-external-proxy-program` | `main` = `ff8b13a29575f82293a9968551279e027a311e0a` |
| `jichangbaike/oixcloud` | `main` = `72d76d1f1351e28eb4cfbf1469f56b78cd345c30` |

- 结果：研究引用的 reF1nd 发布内核 `732618a` 是内核仓的 `reF1nd-stable`/`v1.14.4-reF1nd`，不是发布仓标签对象；Hako 重定向由相同 HEAD 证实。
- 等级：`VERIFIED_GIT`（远端读取）。

## E04 目标依赖与客户端锁

```sh
git submodule status
Select-String -Path go.mod -Pattern '^replace'
Get-Content release/source-lock.json
```

- `sing 1729220c46c4`、`sing-tun 8f65451175c0`、`sing-quic 1ef2597cc129`、`quic-go 2daa76f5bd52`、`utls b63a2e9a7b1e`、`cronet-go 362e63b14ce2`、`cronet-go/all 7ce9c49b464a`。
- 客户端：apple `e3283104`、android `c7837107`（父仓 gitlink）、desktop `41722b0`；nested dashboard `0da34172`；cronet native 29 模块 `controlled`。
- `release/source-lock.json` 的 `release_version` = `0.1.44`。
- 等级：`VERIFIED_GIT` + `VERIFIED_SOURCE`（`dependencies.json` 的 intent/derived 分工由文件自述与 `release-freeze.py` 消费者印证）。

## E05 单元测试（改动包）

```sh
go test ./service/oomkiller -count=1        # ok  0.716s
go test ./common/resourceguard -count=1     # ok  0.823s
```

- `service/oomkiller` 覆盖：原 layered/rhythm/overlap/executor 用例 + 本轮新增的 7 个观测用例（记录、未知 vs 零、冷却、环上界、pressure/crisis 分支、报告写入路径、metadata 形状）。
- 等级：`VERIFIED_UNIT`（主机）。

## E06 竞态检测

```sh
CGO_ENABLED=1 go test -race ./common/resourceguard ./service/oomkiller -count=1
ok  	github.com/sagernet/sing-box/common/resourceguard	1.884s
ok  	github.com/sagernet/sing-box/service/oomkiller	1.765s
```

- 等级：`VERIFIED_UNIT`（race detector，主机）。

## E07 全仓编译

```sh
go build ./...    # exit 0
```

- 等级：`VERIFIED_BUILD`（主机，默认构建标签）。未覆盖带 `with_purego`/`with_utls`/平台标签的发行档位。

## E08 负载定义：准入 burst

```sh
go test ./common/resourceguard -run 'TestAdmissionLoadDefinition|TestAdmissionOverloadLoadDefinition' -count=1 -v
LOAD admission burst: callers=192 limit=32 queue=256 work=2ms admitted=192 refused=0 maxInflight=32 p50=7.0998ms p95=11.9904ms total=14.1516ms
LOAD admission overload: callers=128 limit=8 queue=16 admitted=24 refused=104 maxInflight=8 total=2.7062ms
```

- 解释：192 个调用者全部被吸收（limit 32 + queue 256），`maxInflight=32` 证明上限；超载场景 104 个在常数时间内被拒，`maxInflight=8`。
- 证据等级：主机合成负载（`VERIFIED_UNIT` 级），**不是**吞吐/设备结论。

## E09 负载定义：回收控制环

```sh
go test ./service/oomkiller -run 'TestReclaimCycleLoadDefinition' -count=1 -v
LOAD reclaim cycles: samples=200 actions=100 suppressed=100 totalGainMiB=100 recent=8 elapsed=0s
```

- 解释：100 个“有下降”的采样各触发一次 pass，100 个稳定采样被抑制；模拟 footprint 下环保持 8 条、收益累计 100 MiB；`elapsed≈0` 说明观测与判定本身没有引入可测的固定开销。
- 等级：主机合成（`VERIFIED_UNIT` 级），模拟 footprint，不是真实回收量。

## E10 微基准基线（准入记账）

```sh
go test ./common/resourceguard -run '^$' -bench 'BenchmarkGateAcquireRelease' -benchtime=200000x -count=3
BenchmarkGateAcquireRelease-16           200000  252.2 ns/op  48 B/op  1 allocs/op
BenchmarkGateAcquireRelease-16           200000  252.6 ns/op  48 B/op  1 allocs/op
BenchmarkGateAcquireRelease-16           200000  252.6 ns/op  48 B/op  1 allocs/op
BenchmarkGateAcquireReleaseContended-16  200000  1975 ns/op   504 B/op  6 allocs/op
BenchmarkGateAcquireReleaseContended-16  200000  1901 ns/op   504 B/op  7 allocs/op
BenchmarkGateAcquireReleaseContended-16  200000  1937 ns/op   504 B/op  7 allocs/op
```

- 早一次同命令的结果：uncontended 267.4/271.4/261.2 ns/op；contended 2018/1958/2006 ns/op。**用区间描述，不选最好值**：uncontended ≈ 250–272 ns/op，contended ≈ 1.9–2.0 µs/op。
- 用途：以后给 gate 加计数/时间戳时用同一负载比较，避免“凭感觉说没影响”。
- 等级：主机微基准（`VERIFIED_UNIT` 级），非生产吞吐。

## E11 相邻包回归

```sh
go test ./common/tls ./protocol/anytls -count=1
ok  	github.com/sagernet/sing-box/protocol/anytls	5.960s
--- FAIL: TestWindowsClientHandshakeTLS13 / TestWindowsClientRoundtripTLS13 / TestWindowsClientTLS13PostHandshakeConcurrentWrite
    windows_client_test.go:... tls handshake: read handshake: unexpected EOF
```

- 结论：AnyTLS 全通过；`common/tls` 的 3 个 `TestWindowsClient*TLS13` 失败与本轮改动无关——`KNOWLEDGE/ACCEPTANCE.md` 的“已知失败与已知不稳定集合”已在同一主机/OS build/Go 版本记录同名三个失败与相同错误，成因未确立。
- 等级：`VERIFIED_UNIT`（AnyTLS）、`BLOCKED_BY_ENVIRONMENT`（3 个 TLS 用例，既有失败，不计 PASS）。

## E12 生产写入路径

- 代码：`Service.startTimer` 在 `start()` 前挂 `reclaim`；`NewService` 在 `recorder != nil` 时注册 `SetExtraMetadata("memoryReclaim", s.reclaimMetadata)`。
- 测试：`TestReportMetadataCarriesTheReclaimRecordNextToTheClientDocument` 用真实 `Recorder` 建 draft、写 `metadata.json`，断言 `memoryReclaim` 与客户端键（`schema`、`peakMemory`）同时存在，且 `actions=1`。
- 等级：`VERIFIED_UNIT`（真实文件写出）。

## E14 CI（VERIFIED_CI）

```text
workflow: Verify
run:      42471833521
head_sha: 37b666f7a6bb713c3025ce527820a442b899c797
event:    push (testing)
result:   success（2026-11-08T10:32Z 启动，约 19 分钟完成）
jobs:     verify = success；race checks = success
```

`verify` job 的成功步骤包括：executed-coverage guard 自检、gofmt、client gitlinks 与 ABI pin、ABI pin 失败闭合、依赖 pin 与 release freeze 一致、gomobile 单一来源、嵌套 Go module 完整性、嵌套 test module 类型检查、NetLog parser 测试、build-tag/linkname 门禁、schema 与源一致、pinned-upstream 假设、untagged/tagged 全包构建、kTLS、监听器 MPTCP 默认、build matrix、Cronet provenance。

- 等级：`VERIFIED_CI`，只适用于该 run 与该 SHA 的树。**其后的提交若只改文档，不复用这条 CI 结论去证明代码变化**；代码在本 run 的树里已包含 01 的全部源码改动。
- 限制：CI 是 Linux `ubuntu-latest`；不证明 Darwin/iOS/Android 设备行为。

## E15 02 阶段：速率策略与本地分类

```sh
go test ./common/resourceguard -count=1                     # ok 0.83s
go test -race ./common/resourceguard ./service/oomkiller ./protocol/anytls -count=1   # ok
go test ./common/tls -run 'TestHeldPermitMarker|TestNestedHandshake' -count=1 -v       # ok
go test ./protocol/anytls -run 'TestDialAdmissionMarkerIsScoped' -count=1 -v           # ok
go test ./protocol/mieru -count=1                           # ok 98.8s（含 e2e）
go test -race ./protocol/mieru -run 'TestServerDialer' -count=1                        # ok
go build ./...                                              # exit 0
```

- 覆盖：fast-fail 窗口/退避/上限/抖动、交互豁免、本地拒绝不入账、成功清零、预算不足拒绝；
  AnyTLS/TLS 标记生命周期与嵌套一份许可；Mieru underlay 并发上限、标记传递、Fallback 分类为 Local。
- 等级：`VERIFIED_UNIT`（主机；`-race` 子集）。

## E16 02 阶段：热路径 A/B 基准

```text
基线 worktree（11933a6，独立 worktree）：
  BenchmarkGateAcquireRelease-16           259.1 / 259.6 / 262.7 / 267.4 / 271.3 ns/op   48 B/op 1 alloc
  BenchmarkGateAcquireReleaseContended-16  2110 / 2130 / 2293 / 2329 / 2451 ns/op         503–504 B/op 6–7 alloc
本阶段（同一主机、同一命令）：
  BenchmarkGateAcquireRelease-16           251.4 / 251.6 / 263.8 / 267.6 / 283.5 ns/op    48 B/op 1 alloc
  BenchmarkGateAcquireReleaseContended-16  2130 / 2226 / 2297 / 2625 / 2772 / 2837 / 2916 / 3349 ns/op
```

- 结论：无窗口时只增加一次原子读；uncontended 区间重叠（本阶段低端更低），contended 区间也重叠但
  方差较大，**不能据此声称有/无性能回归**。真正结论是“没有可测的固定开销”；需要更严格比较时用
  同一 worktree 交替 A/B。
- 等级：主机微基准（`VERIFIED_UNIT` 级），非生产吞吐。

## E17 02 阶段：CI

```text
workflow: Verify
run:      42474314237
head_sha: d14933eada21a958c36a4490fa8b34bd930af945
result:   success；jobs: verify = success, race checks = success
```

- 等级：`VERIFIED_CI`（仅对该 SHA 的树）；其后的记录回填提交只改文档，不复用该结论证明代码变化。

## E18 03 阶段：XHTTP 字节预算

```sh
go test ./transport/v2rayxhttp -count=1                 # ok 17.8s（全套，含原 amortize 测试）
go test -race ./transport/v2rayxhttp -run 'TestPacketUp|TestGather' -count=1   # ok
go build ./...                                          # exit 0
```

- 新增 `packetup_budget_test.go`：超大 Write 不越界（高水位 ≤ maxPending）、队列满时写阻塞、
  in-flight 与 pending 分开计数、posted==accepted。
- 等级：`VERIFIED_UNIT`（主机 + race 子集）。

## E19 03 阶段：CI

- 代码提交 `cd1d8160e3c` 的 `Verify` run 42475816951 被随后的记录推送按并发策略取消；
  最终 head `5bcded4` 的 run 42476106354 为 **success**（`VERIFIED_CI`，仅对该 SHA 的树）。
- 本地：`go test ./transport/v2rayxhttp`（全套）、`-race` 子集、`go build ./...` 均通过。
- 等级：`VERIFIED_UNIT` + 以 `DOCUMENT_INDEX` 的 CI 记录为准（本地结果不冒称 CI）。

## E20 11 阶段（部分范围）：已实现路径的并发审计

```sh
CGO_ENABLED=1 go test -race ./transport/v2rayxhttp -run 'TestPacketUpConcurrentWritersAndClose' -count=1
# ok（race 通过）
```

- 内容：8 个写者并发争用 XHTTP packet-up 队列，期间 `Close`；断言无死锁、pending 不越界、
  高水位 ≤ 上限、关闭后队列排空且 `posted==accepted`。
- **范围限定**：只覆盖 03 已实现的字节预算路径。V02 的完整并发/生命周期审计需要 04–09 的
  驻留账本、共享任务、关闭屏障与协调机制，本阶段未执行、未记完成。
- 等级：`VERIFIED_UNIT`（主机 + race），不代表 V02 通过。

## E21 04 批 1：residency owner 盘点、XMUX 库存与有界关闭诊断

```sh
go build ./...                                                                        # exit 0
go test ./route ./service/oomkiller ./transport/v2rayxhttp ./adapter -count=1          # 全 ok
go test . -run 'TestBoxClose|TestNoResurrectionAfterClose|TestCloseDisconnectsTheStatusView|TestOwnedHistoryStorageClose|TestAWarnedStartClosesCleanly|TestRepeatedStartAndCloseOnARefusedBox' -count=1   # ok
CGO_ENABLED=1 go test -race ./adapter ./service/oomkiller -count=1                     # ok
CGO_ENABLED=1 go test -race ./transport/v2rayxhttp -count=1                            # ok（全套）
CGO_ENABLED=1 go test -race ./route -run 'TestResidencySnapshot|TestTheReleaseCapabilityIsWhatMakesAnOwnerReleasable' -count=1   # ok
```

- 代码提交：`6c10b42df37`（residency owner 盘点）、`f523e49ece3`（XMUX 池库存）、`9a04cc1c1e8`
  （有界关闭诊断）。
- `residency`：四类 owner（outbounds/endpoints/dnsTransports/httpClients）的 `reachableOwners` /
  `releasableOwners`，顶层 `measure=owner-reach`；快照是纯读（不关闭、不拨号、不动 owner 计数）；
  无 reporter 时不写键；真实 `metadata.json` 写入且客户端键保留。
- XMUX：`Pooled/Active/Idle/Draining/DrainingOldGeneration/Opened/Released`；每个状态点断言
  `Opened == Pooled + Draining + Released`；`Released` 只在底层 Close 真正执行时 +1；deferred close
  期间活跃流的 transport 不被切断；retire 调用本身不算释放。
- 关闭诊断：drain 记录 completed/pending/current；watchdog 一次性、超时报告 owner/阶段/耗时/4 KiB
  有界栈；`defer` 停表（panic 路径同样停）；并发 Close 仍汇合、结果与错误过滤不变。
- **知识冲突修正**：本机存在 mingw gcc（`<toolchain>`），`CGO_ENABLED=1` 的 `-race` 可用；
  `modules/core-lifecycle-runtime.md` §5 原先“本主机没有 C 编译器，race 为 BLOCKED_BY_ENVIRONMENT”
  的记录对当前环境已不成立，已做定向更新。
- **CI**：首个 head `91c0c292db6` 的 `Verify` 只败在 `gofmt`（新增测试文件以 CRLF 提交）；修复提交
  `205412ba498` 的 `Verify` run 42484328424 的 `verify` 与 `race checks` 均 **success**（`VERIFIED_CI`，
  仅对该 SHA 的树；其后的 CI 记录回填提交只改文档）。
- 等级：`VERIFIED_UNIT`（主机 + race）+ `VERIFIED_CI`（限上述 SHA 的主机运行）。不证明 Darwin/NE
  设备行为；不证明 H2/QUIC/AnyTLS/Cronet/Mieru/WG 的驻留账；报告开销未在真实压力下测量。

## E22 04 批 2：共享创建、共享 underlay 身份与探测路径

```sh
go build -tags "with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname" ./...
# exit 0
go test -tags with_quic ./transport/v2rayquic -count=1                          # ok
CGO_ENABLED=1 go test -race -tags with_quic ./transport/v2rayquic -count=1     # ok
go vet -tags "…" ./transport/v2rayquic                                          # exit 0
```

- 代码提交：`64cfa9a8630`（v2rayquic 共享 offer）。
- R05：`transport/v2rayquic` 的共享创建改为 pending offer；每个 waiter 的 cancel/deadline 只结束自己；
  dial 用 client 生命周期上下文；Close 标记 discarded 并取消，迟到成功由 owner 关闭一次；失败可重试。
  测试覆盖并发单次 dial、取消隔离、deadline、Close 丢弃、失败发布、最后 waiter 离开后接纳。
- R06：XHTTP 每 flow 的 ctx/slot 行为由既有 `packetup_dial_ctx_test.go`、`deadline_test.go` 覆盖；
  v2rayquic 新增 `TestKeepSessionIsPerFlowNotPerUnderlay`；MASQUE 存储 ctx 的源审计结论为 endpoint 生命周期，
  未做运行测试；Mieru 在 SDK 内。`external` 标记由 route 每 flow 设置、group 按 caller ctx 读取，共享 conn
  不保存（源审计，`common/interrupt/context.go` 与 `protocol/group/*.go`）。
- R02：`ALREADY_PRESENT_VERIFIED`。既有 `protocol/group/urltest_background_probe_test.go` 与
  `transport/wireguard/recovery_test.go` 的 4 个探测/业务 gate 测试复跑通过；本轮无新代码。
- 等级：`VERIFIED_UNIT`（新路径 + race）+ `VERIFIED_SOURCE`（MASQUE/R02 源审计）。CI：head
  `5a3add01bca` 的 `Verify` run 42485997186 `verify` 与 `race checks` 均 **success**（`VERIFIED_CI`，仅对
  该 SHA 的树；其后的回填提交只改文档）。不证明 sing-quic 库内 pending 的运行行为、Mieru 或 Darwin/NE 设备行为。

## E23 04 批 3：WG 生命周期、惰性重构与并行关闭评估

```sh
go test -tags "with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname" ./transport/wireguard -count=1
# ok 9.331s（全套）
# R08 合成测量（临时测试，运行后删除）：8 个独立 scope，每个 cleanup 10ms
# serial=84ms  parallel=10ms  workers=8  delay=10ms
```

- 无代码提交：批 3 交付两份探索结案与 R01–R08 终态复核。
- R04：WG 现有 suspend/wake、rebind、listen_port、teardown、并发关闭测试全部通过；never-built 惰性 device
  `NOT_ADOPTED_WITH_EVIDENCE`（`explorations/lazy-heavy-runtime-2026-11-08.md`）。
- R03：descriptor/runtime 拆分 `NOT_ADOPTED_WITH_EVIDENCE`（`DISPROPORTIONATE_REFACTOR`，同报告）。
- R08：独立 endpoint 子 scope 并行关闭 `NOT_ADOPTED_WITH_EVIDENCE`；合成上界收益只在多个慢 endpoint 时成立，
  Scope 无独立性元数据（`explorations/endpoint-close-parallel-2026-11-08.md`）。
- 等级：`VERIFIED_UNIT`（WG 全套）+ `VERIFIED_SOURCE`（结构/依赖分析）；R08 为合成测量，不代表真实分布。
  CI：head `b8590d75cc8` 的 `Verify` run 42487268576 `verify`+`race checks` success（`VERIFIED_CI`，仅对该 SHA）。
- 未验证：Darwin/NE 真机；真实大配置中 Provider 构造与 endpoint 关闭占比。

## E13 未验证 / 边界

| 项 | 状态 | 说明 |
|---|---|---|
| iOS NetworkExtension 真机 | `BLOCKED_BY_ENVIRONMENT` | 需设备；本轮没有运行 NE，`memoryReclaim` 在 NE 下的实际内容未设备验证 |
| CI 对本提交的运行 | `VERIFIED_CI`（37b666f） | 见 E14：run 42471833521 的 `verify` 与 `race checks` 均 success；其后的文档提交不复用该结论 |
| 平台发行档位 / 产物 | `NOT_RUN` | CI 的 tagged build 使用 `DEFAULT_BUILD_TAGS_OTHERS`（server profile）；Darwin 档位（`DEFAULT_BUILD_TAGS`/`with_naive_outbound`）、`with_purego`、Cronet 原生库、Android AAR 与 Windows 产物不在本轮构建。本地只跑了默认 `go build ./...`。 |
| 回收收益的真实量级 | `UNMEASURED` | 主机测试用模拟 footprint；真实 `GainBytes` 取决于进程与平台 |
| 常驻/关闭/构建占用观测 | `NOT_IMPLEMENTED` | 归 04/06，见 `MASTER_PLAN.md` 的 G01 拆分 |
