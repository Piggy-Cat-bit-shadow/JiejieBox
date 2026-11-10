# 02｜sing 与 sing-tun 上游融合报告

- 执行时间：2026-11-11
- 前置：`01` 审计（`docs/fork/UPSTREAM-SYNC-2026-11-11-AUDIT.md`）
- 结果：两个 Fork 的正式 `dev` 分支均已吸收正确上游，**GitHub Compare `ahead_by=0`**；内核已换 pin 并通过消费验证。

## 1. 最终 SHA

| 对象 | 值 |
|---|---|
| sing fork `dev` | `4abfaed41727e43d1d98f36385a86114925f8484`（父 `1729220c46c4` + `27b73774b240`） |
| sing 正确上游 | `SagerNet/sing:dev` @ `27b73774b240` |
| sing-tun fork `dev` | `99abf3c2766646ccd74c77f7b14c197204506121`（父 `57e9d17fa852` + `79ed0653fdc6`） |
| sing-tun 正确上游 | `SagerNet/sing-tun:dev` @ `79ed0653fdc6` |
| 内核消费提交 | `33d6e4c62a4c3f55f91b3d6484588153a136ef0b` |

GitHub Compare（base=fork:dev, head=upstream:dev）：sing `ahead_by=0`、`behind_by=65`；sing-tun `ahead_by=0`、`behind_by=183`。`ahead_by` 即上游独有，两个仓库都已归零。

## 2. sing：27 个上游提交

- `git cherry` 显示 19 个提交带新内容，8 个与 fork 已有提交等价（fork 曾以不同 SHA 吸收过同一上游快照）。
- 13 个冲突文件的处理：
  - **采纳上游**：`packet_batch_read/mmsg/msgx_darwin/sendto_darwin` 与测试、`bind_batch.go`、`bind_wait.go`、`bind.go`（fork 的 remote-address 互斥被上游同源修复覆盖，最终文件与上游一致）、`protocol/socks/packet_batch.go`（共享 `EncodePacket`，移除 per-batch `remoteAddr` 写）。
  - **保留 fork**：`copy_direct.go`/`counter_packet_conn.go` 的 `batchScratch`；`protocol/socks/lazy.go` 的 lock-first 一次性应答协议。
  - **融合**：`common/network/multi.go`——上游单一 child context（胜者/败者 dial ctx 都取消）+ fork 的 prompt `<-ctx.Done()` + 单地址直接拨号（修复 `DialSerial→ParallelDialer→DialParallel` 递归）+ 空地址列表显式报错。
  - `common/bufio/cache.go`：采用上游 mutex ownership；`taken` CAS 只保证交接唯一，不保证 `Read` 与 `Close` 对 buffer 的访问有序。
- 新增 `common/bufio/cache_test.go`：barrier 式 Read/Close 竞态 + 6 个确定性所有权测试；合并前代码会出现 payload 重放、`ReaderReplaceable` 卡 false，并在 `-race` 下报告两处 data race；合并后全过。

## 3. sing-tun：42 个上游提交

- 34 个上游提交在 fork 历史有同名对应，8 个是新内容；其中 `27e188f`（Android 默认网络）在 fork 已存在。
- 31 个冲突文件：
  - **18 个保留 fork**（fork 已是上游的超集或等价并行实现）：`flow.go`、`flow_fragment.go`、`flow_nat.go`、`stack_go*.go` 的 waiter/预算模型、`tun_memory*.go`、`udp_nat.go` 等。
  - **6 个采纳上游**（fork 侧与上游父提交相同，无竞争内容）：`stack_go_io_linux.go`（ENOBUFS → 丢弃帧）、`redirect_linux.go`、`redirect_server.go`、`redirect_iptables.go`、`redirect_nftables.go`、`redirect_nftables_rules.go`。
  - **13 个融合**：`flow_dispatch.go`（identification）、`flow_mtu.go`/`stack_go_udp.go`（GSO segment + IPv4 ID）、`stack_go_engine.go`/`stack_mixed.go`（`readRetry.Wait()`）、`stack_system.go`（按 family 合成回复头、per-reply IPv4 ID）、`tun_linux.go`（GSO batch read 重写 + fork 的 IPv6 rule priority/self-heal）。
- 新增 4 个测试：`TestGSOSplitResumesAcrossBatches`、`TestGSOSplitSinglePassNeedsNoResume`、`TestSystemUDPReplyHeadersCarryDistinctIPv4IDs`、`TestSystemUDPReplyHeadersAreSynthesizedPerFamily`，均做 mutation 验证。
- 保留的 fork 不变量：per-session / NAT-wide / device-wide byte budget、真实 waiter 身份、跨队列唤醒、Close 清理、quota 一次性归还、TCP backpressure/UDP burst、fragment group、queue identity isolation。

## 4. 内核消费与 pin

- `go.mod`/`test/go.mod`：
  - `sing => github.com/Piggy-Cat-bit-shadow/sing v0.9.6-0.20261110222322-4abfaed41727`
  - `sing-tun => github.com/Piggy-Cat-bit-shadow/sing-tun v0.9.7-0.20261110222329-99abf3c27666`
- sing-tun 的 pseudo-version 需要 fork 远端存在前置 tag `v0.9.6`（已推送）才能解析。
- 内核适配：
  - `ReadRetry.Wait()` 不再接收 error，`transport/device/system.go` 三处改为无参调用；
  - 上游 `IsRecoverableReadError` 不再把 `ErrTooManySegments` 当作可恢复，内核 `readErrorIsRecoverable` 显式保留该分类，避免 GSO 段错误直接终止读循环。
- 验证：`go build ./...`、`go build -tags <Windows profile> ./...`、`go test ./transport/device ./protocol/tun` 全过（含 pin-shape 测试与 `too_many_segments` 瞬时错误用例）；`./route` 全量回归见下。

## 5. 测试证据

| 套件 | 结果 |
|---|---|
| sing `go build ./...` | exit 0 |
| sing 目标包 + `go test ./...` | exit 0 |
| sing `-race`（bufio/network/socks，排除既有 Windows checkptr 用例） | exit 0 |
| sing-tun `go build ./...` / linux 交叉 build+vet | exit 0 |
| sing-tun 根套件（跳过 `TestGoKernel*`） | ok，112 top-level PASS |
| sing-tun 目标 queue/NAT/stack `-race` | exit 0 |
| 内核 build（无 tag / Windows profile） | exit 0 |
| 内核 `./transport/device ./protocol/tun` | exit 0 |
| 内核 `./route` | **PASS**（446.2 s，全量） |
| sing Linux（WSL）build/test/race | **PASS**（`go test ./...` 全绿） |
| sing-tun Linux（WSL，`unshare -Urn`）全套 | 除 `TestGoMemoryDial/unreachable` 外全绿；该用例在合并前 `57e9d17` 同样失败（userns 不投递 ICMP unreachable），属环境限制 |
| 内核 `protocol/mixed` | **PASS**（测试已适配 sing 新增的 `CachedConn` 包装层，提交 `4d1469a923d`） |
| 内核 Windows 全量（新 pin，`-p 1`，含 DLL） | mixed/naive/mieru 全过；仅 3 个已知基线失败（tls/tlsspoof/windivert）+ `common/artifact` 偶发（单独跑 3 次全过） |

**BLOCKED**：`TestGoKernel*`（21 个，TUN 适配器创建需要管理员；Linux 侧已在 `unshare -Urn` 跑通，仅 `TestGoMemoryDial/unreachable` 因 userns 不投递 ICMP 失败，且合并前同样失败）、sing 的 `TestWriteVectorisedPacket`（既有 Windows checkptr 崩溃，未触碰该文件）。

## 6. 风险与遗留

1. Linux 已在 WSL 实际执行（sing 全套、sing-tun `unshare -Urn` 全套）；Darwin 的 msgx/sendto/redirect 路径仍只做编译/类型检查，阶段 05 需在 darwin CI 复跑。
2. `common/artifact` 的 `TestTwoStoresOnOneDirectoryShareLeases` 在全量并发下偶发“旧版本未回收”，单独连跑 3 次全过；与 sing/sing-tun 改动无调用关系，记为既有测试隔离问题，阶段 05 全量时复跑确认。
3. 上游语义变化已消费但需观察：batch 写遇 `EMSGSIZE` 现在“跳过该报文并最终返回一次错误”，不再是立即中止；`NewServerPacketConn` 增加 peer 参数；`AssociatePacketConn.RemoteAddr` 返回 `M.Socksaddr`；空地址并行拨号由 panic/递归改为显式错误。
4. sing-tun 的 `NewForwardDispatcher` MTU 参数、`portNAT.routed`/returns 结构未采纳，fork 继续用 `RoutedPort`/`PortMTU`；这是有意的结构差异，不是遗漏。
5. `internal/winipcfg` 的测试文件存在既有 vet 失败，与本轮合并无关。

## 7. 下一阶段交接

- 阶段 03 入口：`sing-quic`（4 个上游提交，trial 0 冲突）、`quic-go`（1，0 冲突）、`utls`（1）、`wireguard-go`（已 0）。
- 阶段 05 需把本轮的新 pin 写进 `release/source-lock.json` 并跑 `release-freeze verify`；当前 source-lock 仍是 0.1.44 快照。
