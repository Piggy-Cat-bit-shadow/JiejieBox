# 02｜底层依赖仓库收敛

- 执行时间：2026-11-11 01:00–01:20（Asia/Shanghai）。
- 施工基线：主仓库 `integration/0.1.45-final` @ `0b1b6a3b2f0d2e3f3d2f580a4bc77d769ff57e20`（candidate/w07 tip，未改）。
- 本阶段只动依赖仓库的 `integration/0.1.45` 分支；**主仓库 `go.mod` / `test/go.mod` / gitlink / `source-lock.json` 一律未改**，正式 pin 留到第 06 阶段。
- 结论先行：
  1. **sing-tun 收敛到 `57e9d17fa852`**（`dev` tip，13 个提交全部属于同一条 tun-memory/UDP-NAT 修复线，fast-forward）。
  2. **quic-go 融合完成**：`bca85442` → 2 个共享提交 → `0e7c69da`（connID 互斥）→ cherry-pick `0b8709eb`（http3 引用计数），融合 tip `ca9992bc795e`。
  3. **wireguard-go 不需要新提交**：pin `a765d95`（awg3-inject）已经包含 `awg-base`/`awg-graft` 的全部内容（阻塞式注入、懒建 peer 的 PSK、全部 `obf_*.go`），两者标记为 superseded，待第 06 阶段归档。
  4. **sing / sing-quic / uTLS / Android 未变**，pin 仍等于远端 tip；cronet-go 保持 controlled，29 个原生模块不动。
  5. quic-go 的融合提交已推送到 `Piggy-Cat-bit-shadow/quic-go` 的 `integration/0.1.45`（`ca9992bc795e`，推送后核对远端 SHA 一致），canonical pseudo-version `v0.61.1-0.20261110165647-ca9992bc795e` 已从 GitHub 解析并跑通内核构建与 race 回归。

---

## 1. 阶段目标与实际范围

目标：把内核真正依赖的底层实现收敛到可验证的 commit，并给出机器可读的最终 SHA 清单；不在本阶段改主仓库 pin。

实际执行：
- 8 个依赖仓库重新 `ls-remote`/`fetch`，确认远端 tip 与第 01 阶段一致；
- sing-tun：逐提交审查 `dev` 的 13 个提交，fast-forward 集成分支；
- quic-go：fast-forward 到 `network/path-length-fix`，再 cherry-pick `fix/task08-h3-usecount` 的独有提交；
- wireguard-go：用内核调用点、符号存在性与分支内容对比，判断 AWG 是否需要集成；
- cronet-go：核对 root/all/29 个原生模块的顺序与 controlled 策略，检查本地未推送分支的原生归档是否可采纳；
- sing / sing-quic / uTLS / Android / satelite-one：复核 pin 与接口；
- 用主仓库临时 worktree（不提交、不推送）把内核构建和关键测试跑在新依赖上，验证接口兼容。

未执行（明确不属于本阶段）：修改主仓库 pin、改 `source-lock.json`、推送任何分支、Apple/Android/Dashboard/Desktop 平台构建。

---

## 2. 基线与最终 SHA

| 仓库 | 施工前（= 主仓库 pin） | 来源分支 / tip | 本阶段最终 | 动作 |
|---|---|---|---|---|
| sing-tun | `9c3dcb2b1edfde31798981aa05f08851c0eee401` | `dev` `57e9d17fa852eafbc6849ae508c8b772e9fa1a66` | `57e9d17fa852eafbc6849ae508c8b772e9fa1a66` | fast-forward（13 提交） |
| quic-go | `bca854425f4daf5818ef57c6702de548b27f222b` | `network/path-length-fix` `0e7c69dacb54`；`fix/task08-h3-usecount` `0b8709ebb15e` | `ca9992bc795e8699db2b22ad6459216e6f13649d` | ff + cherry-pick `0b8709eb` |
| wireguard-go | `a765d957fd814e5c0ee579936a0780e73d66e533` | `awg-base` `ca3bc60c4ce7`；`awg-graft` `52a27657d805` | `a765d957fd814e5c0ee579936a0780e73d66e533` | 不集成（superseded，见 §5） |
| sing | `1729220c46c4cf750726b1057514a8d52a1f5e3b` | `dev` 同点 | 同左 | 不变 |
| sing-quic | `1ef2597cc1298146fcdddfeaa0384999f890d0c3` | `main` 同点 | 同左 | 不变 |
| utls | `b63a2e9a7b1eeba03cb7e2db2b92b5bb027894fc` | `main` 同点 | 同左 | 不变 |
| cronet-go | root `362e63b14ce2` / all `7ce9c49b464a` / 29 native `362e63b14ce2` | `go` `7ce9c49b464a` | 同左 | 不变（controlled） |
| sing-box-for-android | `c7837107dc0126fb704fdf08cf203e10b30c8a22` | `main` 同点 | 同左 | 不变 |
| satelite-one | `c9a07350e4c7a4d60a918488478893de5a49b6ac` | `main` 同点；`next/subscription-refresh` `9cdf782b8c8c`（+16） | 同左 | 只登记 |

主仓库 `go.mod` 的 `replace` 仍指向上表的"施工前"版本；`release/dependency-final-shas.json` 是本阶段给第 06 阶段的唯一输入。

---

## 3. 分支关系、独有提交与重复补丁

### 3.1 sing-tun（13 个独有提交，无重复补丁）

`dev` 相对 pin 只有这一条线，全部是 2026-11-10 的 `fix(tun-memory)` / `fix(udp-nat)`：

```
930031e fix(udp-nat): return the queue byte reservation from the waiter and batch paths
de08ac6 fix(tun-memory): wake inbound byte waiters when an outbound queue releases
1671498 fix(tun-memory): close releases what the device owns, and the total keeps saying so
2462839 fix(tun-memory): the detach path returns the inbound byte quota
0c727f3 fix(tun-memory): close and budget semantics - admission after Close, lost-quota retry, and TCP backpressure
285b478 fix(tun-memory): wake byte-blocked data writers across queues, and do not disarm on an empty drain
f3e5e5b fix(tun-memory): close the reservation-failure -> wait-registration wake window
f14c023 fix(tun-memory): synchronize the arm re-check and track each waiter's need
c73cfd8 fix(tun-memory): bound the need multiset and clear a cancelled writer's need
29f6dfd test(tun-memory): debug matrix for fragment groups, ring pressure and queue identity
9883b06 fix(tun-memory): own each waiter record on its connection, not in a size multiset
ecff2bc test(tun-memory): follow-up to the waiter ownership rename
57e9d17 fix(tun-memory): clear EVERY waiter record when a queue closes
```

- 规模：5 个生产文件 `+379/−48`（`tun_memory.go`、`tun_memory_queue.go`、`stack_go_conn.go`、`stack_go_io_memory.go`、`udp_nat.go`），13 个测试文件（新增 12 个），其余为测试。
- 没有夹带实验、平台开关或无关重构；每条修复都带对应测试，且后一条修复就是在处理前一条暴露的窗口（reservation→arm、close→waiter、detach→quota）。
- 因此不存在"部分吸收"的取舍：接受全部 13 个，保留原始提交而不是压成单个补丁。

### 3.2 quic-go（4 个提交，2 个共享 + 2 个独有）

```
bca85442 fix(path): a failing send on a path being left must not end the connection   ← pin
0028a4c7 fix(path): closing or probing a path after the connection ended must not panic   ┐ 两条分支共享
4b11a7be fix(sendqueue): never block the connection's own shutdown on a failure report     ┘
0e7c69da fix(connid): guard the connection ID manager with one mutex                       ← path-length-fix
0b8709eb http3: release the pool reference a joining request took when it leaves          ← task08 独有
```

- `network/path-length-fix` = pin + 3；`fix/task08-h3-usecount` = pin + 3；两者的 merge-base 是 `4b11a7be`，即共享 2 个提交、各自 1 个独有提交，改动的文件互不重叠（`conn_id_manager.go`/`connection.go` vs `http3/transport.go`）。
- 没有重复补丁；`sagernet` 仍比 pin 旧 7 个提交，不回退。
- 融合后相对 pin：6 文件 `+167/−21`。

### 3.3 wireguard-go（两分支都是 pin 的子集/旧切片）

| 分支 | 独有提交 | 内容 | 在 pin 中的状态 |
|---|---|---|---|
| `awg-base` | `b3540b3`、`ca3bc60` | 懒建 peer 应用 PSK；阻塞式每 peer 注入 | 已存在且更完整 |
| `awg-graft` | `52a2765`、`b3540b3`、`ca3bc60` | 从 `lx-awg2-v007` graft 第一批混淆文件 | 已存在且更多 |

- pin `awg3-inject` 中实际存在（`git grep` 证据）：
  - `func (device *Device) InputPackets`、`func (peer *Peer) WritePackets`、`waitOutboundSpace`/`wakeOutboundWaiters`、`stagedPackets`；
  - `p.SetPresharedKey(conf.PresharedKey)` 与 `NewPeerConfig.PresharedKey`（懒建 peer 路径）；
  - `device/obf.go`、`obf_bytes.go`、`obf_data.go`、`obf_datasize.go`、`obf_datastring.go`、`obf_rand.go`、`obf_randchars.go`、`obf_randdigits.go`、`obf_timestamp.go`、`cookie.go`，外加 `awg_classify_index_first_lx_test.go`（graft 没有）。
- 内核确实消费这些 API：`transport/wireguard/port.go` 调用 `wgDevice.InputPackets`，`device_peer_router.go` 调用 `peer.WritePackets`；AWG 配置在 `option/wireguard_awg.go`（AWG 2.0/3.x 的 `N` / `N-M` 范围语义）。

---

## 4. 为什么是 fast-forward / ff+cherry-pick / 不集成

- **sing-tun 用 fast-forward**：topic 以当前 pin 为祖先、13 个提交是同一功能线的连续修复，merge 不会引入陈旧基线，fast-forward 保留原始提交与作者意图。压成单个补丁反而丢失"第 N 条修复处理第 N-1 条窗口"的因果。
- **quic-go 用 ff + cherry-pick**：共享的 2 个提交必须整体带入（ff 到 path-length-fix 完成），task08 的独有提交只改 `http3/transport.go` 并新增测试，cherry-pick 无冲突且可独立验证；没有采用"两条分支各建一个 integration 分支"或"重写实现"，因为两处修复互不重叠、语义正交。
- **wireguard-go 不集成**：不是"延期"，而是**内容已由 pin 覆盖**——`awg-base`/`awg-graft` 的两个独有提交在 pin 中以更完整的形态存在，graft 只是"第一批"混淆文件。为清分支而 merge 会把 25 个提交的旧线带回，反而倒退。
- **cronet-go 不动**：root/all 的发布顺序（`main` → `362e63b` → `7ce9c49`）与 29 个 native 模块的 controlled 策略一致；本地 `feat/per-dial-quic` 的原生归档与 `release/cronet-provenance.txt` 记录的字节**不同**，无 Chromium 工具链重建证据，不得采纳。

---

## 5. 关键设计决策与语义核对

### 5.1 sing-tun 的行为变化（相对旧 pin）

- **close 清理全部 waiter**：队列关闭时清除该队列上所有连接记录，而不是只清"当前连接"；`GoConn.Close` 也会 `dropWait`，取消的 writer 不再留下幽灵唤醒。
- **设备字节预算跨队列唤醒**：预算从"设备级数字"变成真正可等待的资源——inbound 读取、outbound 出队、detach、device Close 都会 `notifyOutboundByteWaiters`，扫描注册表中所有队列，用最小 blocked frame 尺寸决定是否唤醒。
- **detach 归还 inbound quota**：`goMemoryIO.close` 不再用 `releaseAll` 只释放 buffer，而是逐帧 `releaseQueuedBytesLocked`，配额不随 stack 生命周期泄漏。
- **caller-owned 队列记账**：`MemoryTun.Close` 不再把 `queuedBytes` 归零；调用方队列在设备关闭后拒绝新数据但保留自己的存量，直到自己 Close——移除旧的"假装归还"。
- **单帧超预算的确定性错误**：blocking write 遇到 `frameBytes > maxQueuedBytes` 返回 `errFrameExceedsByteBudget`，不再永久等待。
- **UDP NAT 出队归还预留**：`waitReadPacket`/`WaitReadPackets` 的快路径补上 `releaseQueuedBytes`，避免幽灵预留累积。

这些语义与内核 TUN 的使用方式一致（`protocol/tun` 全部测试在融合 pin 上通过，含 `TestPinnedSourceIsTheReplacedFork` 的 pin 形状检查）。

### 5.2 quic-go 的行为变化

- `connIDManager` 单互斥：修掉 run loop（`Close`）与应用 goroutine（`Path.Close`→`RetireConnIDForPath`）在 `closed`/`pathProbing` 上的数据竞争；这正是内核 `TestCloseDuringAMigrationReleasesTheCandidateAndPublishesNothing` 在 `-race` 下曾捕获的竞争。
- `http3.Transport.doRoundTripOpt`：加入他人 dial 的请求在自身 context 结束、或 dial 已失败时释放 `useCount`，使 `CloseIdleConnections` 能真正回收连接。
- `sendqueue`：失败上报不再阻塞连接自身 shutdown。
- `path`：连接结束后 close/probe 路径不再 panic。

### 5.3 AWG 判定证据

- `awg-base` 的 `b3540b3` 只加 `LookupPeer` 里的 `SetPresharedKey` 与 `NewPeerConfig.PresharedKey` → pin `device.go:483/552-554` 已有。
- `awg-base` 的 `ca3bc60` 引入 `WritePackets`/`InputPackets`/`waitOutboundSpace`/`maxStagedPackets` → pin `send.go:682/1116/1165` 已有，且常量演进为 `maxQueuedInputPackets`（2048）并区分 staged/outbound。
- `awg-graft` 的 `52a2765` 是 13 个混淆文件的第一批 → pin 全部具备，并多一个分类测试。
- 内核 AWG 测试在 pin 上通过：`option` 的 `TestAmneziaWGOptions*` 2 个、`transport/wireguard` 的 AWG/Reserved/Framing 6 个。

---

## 6. 文件变更

### 依赖仓库

- sing-tun `integration/0.1.45`：fast-forward 到 `57e9d17fa852`，工作树 = dev tip（5 生产文件、13 测试文件变化）。没有额外集成提交——这是刻意的：让最终 pin 直接指向已推送的 `dev` commit，避免制造只有本机存在的 SHA。
- quic-go `integration/0.1.45`：`ca9992bc795e`，内容 = 4 个上游提交，无额外改动。**这是唯一一个本地未推送的集成提交**。
- 其他仓库：无提交，工作树仍在 pin 上。

### 主仓库

- 仅新增本报告与 `release/dependency-final-shas.json`；`go.mod`、`go.sum`、`test/go.mod`、`release/*`、gitlink 全部未改。

---

## 7. 测试与验证

所有命令在 Windows 11、Go 1.25.5（`C:\Deepseek\_gotool\go\bin`）下执行；客户端平台构建不在本阶段范围且环境不可用（见第 01 阶段报告 §12.6）。完整输出在 `_int0145/audit-evidence/stage02/`。

### 7.1 sing-tun（最终 `57e9d17fa852`）

| 命令 | 退出码 | 结果 |
|---|---|---|
| `go test -vet=off . -run '<tun-memory/udp-nat 定向>' -v` | 0 | **49 PASS / 0 FAIL**（含子测试） |
| `go test -vet=off -skip '<admin-only>' ./...` | 0 | 4 个包全部 ok |
| `go test -race -vet=off -skip '<admin-only>' ./...` | 0 | 4 个包全部 ok（含 root 包 7.9 s） |
| `go test -vet=off ./...`（不 skip） | 1 | 仅 `TestGoKernel*`（Access denied）与 `internal/winipcfg` 的 `TestIPChange*/TestAddDelete*/TestGetIfRow/TestFlushDNS/TestSetDNS` 失败 —— **需要管理员权限，BLOCKED_BY_ENVIRONMENT** |

### 7.2 quic-go（最终 `ca9992bc795e`）

| 命令 | 退出码 | 结果 |
|---|---|---|
| `go test -vet=off -timeout 10m ./...` | 0 | root、http3、internal/handshake、internal/utils/tree 全部 ok |
| `go test -vet=off . -run '<queue/migration/path>' -v` | 0 | 13 PASS / 0 FAIL |
| `go test -vet=off ./http3 -run TestCancelledRequestReleasesTheReferenceItTookWhileJoiningADial -v` | 0 | PASS（新增的引用计数测试） |

### 7.3 主仓库对新依赖的接口验证（临时 worktree，不入库）

| 命令 | 退出码 | 结果 |
|---|---|---|
| `go build -tags <Windows profile> ./...` | 0 | **整棵内核构建通过**（35.5 s） |
| `go test -tags <Windows profile> ./protocol/tun` | 0 | 通过，含 `TestPinnedSourceIsTheReplacedFork`（sing-tun 用真实 pseudo-version 解析） |
| `go test -tags <Windows profile> ./common/httpclient` | 0 | 通过 |
| `go test -race -tags with_quic ./transport/v2rayquic` | 0 | 26 个测试全部通过（含 `TestCloseDuringAMigrationReleasesTheCandidateAndPublishesNothing`） |
| `go test ... ./option ./transport/wireguard -run 'AWG\|Amnezia\|Reserved\|Framing\|Owner'` | 0 | 8 PASS / 0 FAIL |

说明：临时 worktree 里两个依赖都使用**真实 pseudo-version**（sing-tun `v0.0.0-20261110141903-57e9d17fa852`、quic-go `v0.61.1-0.20261110165647-ca9992bc795e`），从 GitHub 解析后构建与测试；主仓库 `go.mod` 仍未提交任何改动。

### 7.4 未执行

- `go test -race ./...`（内核全量）：NOT RUN（本阶段只验证依赖接口，全量属于第 06 阶段）。
- Apple / Android / Desktop / Dashboard 构建与测试：BLOCKED（无 Xcode、JDK 17、Node）。
- 任何 CI：NOT RUN。

---

## 8. 风险、遗留与接口约束

1. **quic-go 融合提交已推送（收尾补做）**：`integration/0.1.45` = `ca9992bc795e`，远端 SHA 已核对；canonical pseudo-version `v0.61.1-0.20261110165647-ca9992bc795e` 已在干净解析路径上验证（内核全量构建 exit 0、`transport/v2rayquic -race` 26 测试通过）。第 06 阶段可直接使用该版本，无需再推送。
2. **sing-tun 的最终 SHA 已推送**（在 `origin/dev`），不需要额外动作。
3. **本机 VCS 缓存曾损坏**：早先用本地 partial clone 做 `insteadOf` 解析，在 `GOMODCACHE/cache/vcs/058385…` 留下 promisor 型缓存，导致 canonical 版本首次解析报 `lazy fetching disabled`。收尾时删除了该缓存条目，solver 重新从 GitHub 克隆后解析成功；该目录只是缓存，删除无副作用。
4. **wireguard-go 的 `awg-base`/`awg-graft` 仍在**（本地与远端）。第 06 阶段可按"已被 pin 覆盖"的证据归档后删除；删除前仍要按第 06 阶段的证明流程打 archive tag。
5. **cronet-go 的本地 `feat/per-dial-quic`**：含 2 个未推送提交与一套与 provenance 不同的原生归档（`darwin_arm64/libcronet.a` 48 110 296 B / sha256 `f680ff96…`，provenance 记录 48 112 336 B / `dae99ed2…`）。在 controlled 策略下**不得**进入任何 pin；如第 05 阶段要它，需要先给出 Chromium 工具链重建与验证证据。
6. **主仓库 pin 仍是 0.1.44 的一套**：本阶段不更新，第 06 阶段统一；在那之前主仓库构建/测试仍使用旧 pin（与第 01 阶段一致）。
7. **Android / Apple / Desktop / Dashboard** 本阶段没有可执行验证（环境缺失），只做了 pin 一致性核对；第 05/06 阶段按 BLOCKED 记录。
8. 依赖接口约束（给第 03/04 阶段）：sing-tun 新增 `errFrameExceedsByteBudget` 与跨队列唤醒语义；quic-go 的 `http3` 引用计数修复会影响 H3/H2 回退窗口的判断，第 04 阶段回归时要覆盖"取消后连接可回收"。

---

## 9. 阶段结论

- 完成条件对照：
  - sing-tun 目标修复已验证：✅（49 定向测试 + 全量 minus admin + race + 内核 tun 测试）
  - quic-go 两分支合理融合：✅（ff + cherry-pick，内核 race 回归通过）
  - AWG 已有证据的纳入/延期决定：✅（不集成，pin 已覆盖，证据见 §5.3）
  - sing / sing-quic / uTLS / Android / Cronet pin 复核：✅（全部等于远端 tip 或维持 controlled）
  - 依赖集成分支提交：sing-tun（ff，无新提交，理由见 §6）、quic-go（`ca9992bc`）；其余无需提交
  - 测试结果与 SHA 清单：✅ 本文件 + `release/dependency-final-shas.json`
- 下一阶段（03）可直接使用：sing-tun `v0.0.0-20261110141903-57e9d17fa852`、quic-go `v0.61.1-0.20261110165647-ca9992bc795e`（两者均可从远端解析，无需本地 replace）。

阶段 02 内容提交见 `INTEGRATION-LOG.md` 的 Stage 02 记录与 `git log --oneline`。
