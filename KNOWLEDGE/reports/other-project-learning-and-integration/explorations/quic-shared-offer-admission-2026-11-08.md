# QUIC 共享 offer 准入的跨仓 hook：已探索，本轮不实施（2026-11-08）

状态：`NOT_ADOPTED_WITH_EVIDENCE`，reason=`DISPROPORTIONATE_REFACTOR`（跨仓 pin/发布冻结链 + 异步认证无法在本环境验证，相对收益与侵入不相称）。
适用阶段：02（G03 的 QUIC 子项）。目标 revision：`11933a6`（02 开工基线），依赖 `sing-quic` pin `1ef2597cc129`（`github.com/Piggy-Cat-bit-shadow/sing-quic`，policy `update`，branch `main`）。

## 1. 问题与目标症状

Hysteria2 / TUIC 的客户端把“打开一条 QUIC 连接”做成一个**共享任务**：第一个 waiter 创建
`clientOffer`，后台 `completeOffer` 执行 `offerNew`（UDP 拨号 + QUIC/TLS 握手 + TUIC 认证请求），
后来的 waiter 挂在同一个 pending 上。并发上限只约束 waiter 数量，不约束共享任务的真实分配：
大量首次连接、节点重连风暴或快速失败重试时，`offerNew` 的握手/加密分配可以同时发生。
目标：在**共享任务开始昂贵分配前**取得一份创建许可，由共享任务完成/退出归还；一个 waiter
退出不能提前释放任务许可；已有 session/stream 复用不重复领完整握手额度。

## 2. 原架构边界（不可破坏）

- 进程压力与创建政策唯一 owner 是 `common/resourceguard.Guard`（由 `service/oomkiller.Service` 安装），
  回收执行器唯一，`HeldPermit` 保证一个逻辑拨号最多一份许可。
- QUIC 客户端实现（`hysteria2`、`tuic`、`hysteria`）属于 **pinned 依赖** `sing-quic`，本仓只通过
  `ClientOptions` 构造它；依赖不能反向 import 主仓。
- `release/source-lock.json` 是 release freeze 的提交产物；`Verify` 的
  “the dependency pins are the ones the release froze” 门禁要求 `go.mod` 与 lock 一致。
- 现有接点：`ClientOptions.Dialer` 只覆盖 UDP socket 打开，**不覆盖** socket 之后的 QUIC/TLS 握手、
  TUIC 认证与 0-RTT 状态。

## 3. 候选方案与较小替代

| 方案 | 结论 |
|---|---|
| A. 在 repo 侧包一层 `DialConn`/`ListenPacket` 调用，进出各取/放许可 | **不可靠**：共享任务在 waiter 返回后仍活着；所有 waiter 取消时许可提前释放，等于没限住真实分配 |
| B. 包装传给 `ClientOptions` 的 `N.Dialer`，在 UDP dial 时取许可、连接关闭时释放 | **不可靠**：UDP socket 的关闭不等于共享任务结束，且 QUIC 握手在 dial 返回之后 |
| C. 在 `sing-quic` 的 `completeOffer` 里加 `Admission` 回调：`offerNew` 前 `Acquire`，返回后 `Release` | 正确形状；需改 3 个包 + 仓 pin + source-lock |
| D. 在 C 的基础上把许可持有到 TUIC 认证完成/0-RTT 首次可用 | 语义最正确；需要认证完成的 hook，且无互通/真机证据 |

C/D 都需要修改 pinned fork：`hysteria2/client.go`、`tuic/client.go`、`hysteria/client.go` 的
`completeOffer` 形状一致（`conn, err := c.offerNew(offerCtx); pending.cancel(nil); …`），
hook 本身约 20–30 行/包；`tuic.offerNew` 内含 `qtls.DialEarly`（0-RTT）与认证请求写入，
认证完成的时机在返回之后。

## 4. 证据与成本

- 源码证据（`VERIFIED_SOURCE`，pinned module cache）：三包均有 `completeOffer → offerNew`；
  `tuic/offerNew` 使用 `qtls.DialEarly` 并写 `CommandAuthenticate`；`ClientOptions` 无任何 admission/hook 字段。
- 仓侧证据：`go.mod` replace、`release/dependencies.json`（policy `update`）、
  `scripts/ci/release-freeze.py`（`resolve → snapshot → verify`）、`Verify` 的 pin 门禁。
- 成本：fork 提交 + push + `go get` 伪版本 + `release/source-lock.json` 重新生成 + 三个协议的
  option 接线 + 依赖测试；若走 D，还要给 TUIC 认证完成加回调并设计“认证失败/迟到成功”的归还路径。
- 收益：约束 QUIC 握手/加密分配的并发峰值，防止重连风暴放大驻留与 CPU。收益真实，但依赖
  0-RTT/认证的时序才谈得上“正确覆盖”；仅 C 可能在认证仍进行时提前归还。

## 5. 为什么本轮不做

- 该改动跨出本仓：需要动 public fork 的 `main`、重建 release freeze 链（`source-lock`），
  并让所有消费该 fork 的构建重新对齐；这不是“稍复杂的中改”，而是依赖发布面的改动。
- 正确性取决于 TUIC 异步认证 / 0-RTT 的完成语义；本环境没有互通服务器与真机，无法给出
  “认证完成后归还”的可信验证。只做 C 会把“共享任务已完成握手”当成“任务已完成”，
  在认证失败/迟到的路径上可能提前释放——比不接更危险。
- 仓侧 A/B 包装是错的形状，不能为了“有接线”而采用；这正是本工程明确禁止的“存在接口当完成”。

## 6. 本轮保留的小改

- TLS/AnyTLS 的许可生命周期与嵌套去重（`HeldPermit`）已审计并加测试（`marker_scope_test.go`）。
- Mieru underlay 已接同一 `Guard`（真实 newUnderlay 接点，非 QUIC 共享任务）。
- 快速失败的创建速率/退避（G06）已在 `Guard` 内实现，QUIC 一旦接入 hook 可直接复用同一 `ObserveOutcome`。

## 7. 适用与重开条件

- 需要把 `sing-quic` pin 移到含 admission hook 的提交，并同步 `release/source-lock.json`；
  fork 的 policy 是 `update`，此动作属于发布冻结流程，应在有 release 窗口时做。
- 需要明确 TUIC 认证完成/0-RTT 可用的 hook 点，或证明“握手完成即释放”在本仓使用方式下足够。
- 需要有可达的 Hysteria2/TUIC 服务端做互通与取消/迟到成功验证；没有证据前保持 `NOT_ADOPTED`。

## 8. 脱敏、索引与提交

- 本文不含凭据、节点地址或私有路径；只引用公开仓库、pin 短 SHA 与源码符号。
- 索引：`MASTER_PLAN.md` G03、`CONTRACTS.md` §4、`KNOWLEDGE/INDEX.md`。
- 提交与远端核验见 `DOCUMENT_INDEX.md`。
