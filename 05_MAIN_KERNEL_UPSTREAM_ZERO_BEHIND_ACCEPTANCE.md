# 05｜主内核上游融合、零 Behind、全仓验收与分支清理

## A. 总状态

**DONE_WITH_LIMITATIONS**

主内核已真实 merge 上游并零落后；所有被消费 Fork 的 pin、native 矩阵、客户端 gitlink 与 source-lock 已收敛；可清理分支已逐项审计清理。限制项：Apple 无 Xcode（`NOT_VERIFIED_ON_DEVICE`）、`common/tls` 等三个既有基线失败、Dashboard `lint:css` 的既有规则错误。GitHub Actions Verify/Android Core 已在最终 head 通过。

## B. 主内核

| 项 | 值 |
|---|---|
| origin | `Piggy-Cat-bit-shadow/JiejieBox` |
| upstream | `SagerNet/sing-box:testing` |
| canonical/default | `testing` = `integration/0.1.45-final` = **`2528511d90ab876c6c853f23d6ae1deee083027e`** |
| 最后观察 upstream | `a13dbe1f785f66c86acbf3985c33fdafea0cbfd6`（2026-11-10 11:23 UTC，Fix platform auto-redirect close order） |
| pre-merge merge-base | `8eb4395a391b775ad240433a7009f39357c1eba0`（合并前共同祖先） |
| final merge-base at observation | `a13dbe1f785f66c86acbf3985c33fdafea0cbfd6`（上游已并入 HEAD，因此最终共同祖先就是被合并的上游 HEAD） |
| ahead / behind | **3737 / 0** |
| 真实 merge | `303fbb47dfdca4520dd201fea8bd48b3db1f1010`（parents `48103716845e` + `a13dbe1f785f`） |
| 后续提交 | docs/testing merge `03620f67fc0`、bridge 去重 `f4331ac5a18`、module tidy `08b52a30b4d`、source-lock `a9a0cb0c093` |

108 个冲突逐文件融合。上游 85 个提交中大部分行为已在 Fork 既有实现里（MASQUE 背压、TLS pinning、auto-redirect、GSO、FakeIP 等），实际新增/融合集中在 42 个文件：

- parallel dial 单 child context / stagger、listener `followBindInterface` rebind；
- `protocol/direct` 的 `ListenPacketWithDestination`（上游 `ddf3d10b19b`），honour `udp_disable_domain_unmapping`；
- HTTP forward body ownership（`io.NopCloser`）、HTTP/2 extended CONNECT、HTTP outbound 的 `OutboundWithMultiplex`（同时给 `transport/http.Client` 补 `MultiplexEnabled`）；
- DNS local 的 `SystemdResolved` 条件、FakeIP 缓冲写一致性、`service/resolved` 的 `" (deleted)"` 修剪；
- Tailscale/endpoint、OpenVPN/OpenConnect、WireGuard 的上游边界，全部放在 Fork 的 Scope/generation/queue 结构里。

保留的 Fork 语义：Scope/RuntimeCoordinator/generation ownership、trackedConn/trackedPacketConn 生命周期与 splice 诊断、TUN/queue/NAT 预算、FakeIP ledger、MASQUE/HTTP/Tailscale/OpenVPN 的更大实现、release 的 `User=sing-box` + capability 沙箱（明确拒绝上游 root 切换）。未使用 `-X ours/theirs`、整文件覆盖、`merge -s ours` 或 force push。

## C. 依赖总账

| 仓库 | Fork 正式分支 | 正确 upstream | Fork 最终 SHA | upstream SHA | ahead/behind | 最终 pin |
|---|---|---|---|---|---|---|
| sing | `dev` | `SagerNet/sing:dev` | `4abfaed41727` | `27b73774b240` | 65/0 | `v0.9.6-0.20261110222322-4abfaed41727` |
| sing-tun | `dev` | `SagerNet/sing-tun:dev` | `99abf3c27666` | `79ed0653fdc6` | 183/0 | `v0.9.7-0.20261110222329-99abf3c27666` |
| sing-quic | `main` | `SagerNet/sing-quic:main` | `aa6d98964049` | `5253224b67cf` | 11/0 | `v0.7.2-0.20261110234451-aa6d98964049` |
| quic-go | `sagernet` | `SagerNet/quic-go:dev` | `f864f2f12006` | `2e73222e969f` | 20/0 | 消费线 `158445cdda77` → `v0.61.1-0.20261110231807-158445cdda77` |
| utls | `main` | `MetaCubeX/utls:v1.9.0-mod-meta` | `e14b88879d80` | `89a07ad380e2` | 69/0 | `v1.8.8-0.20261110231903-e14b88879d80` |
| wireguard-go | `awg3-inject` | `sagernet/wireguard-go:main` | `a765d957fd81` | `e4d85ef2ed38` | 22/0 | `v0.0.0-20261109042158-a765d957fd81` |
| cronet-go | `main` / `go` | `SagerNet/cronet-go:main` | `4c96192d73f6`（main）/ `7ce9c49b464a`（go） | `73349ff2a587` | main 29/0；go 31/2* | root `362e63b14ce2`，all `7ce9c49b464a`，29 libs `362e63b14ce2` |
| apple | `main` | `SagerNet/sing-box-for-apple:dev` | `7fce711`（pin）/ main `97b35d1` | `768b3de2ba` | main 220/0 | gitlink `7fce7117b33a` |
| android | `main` | `SagerNet/sing-box-for-android:dev` | `ba5c6b161827` | `ab4fad3944` | 15/0 | gitlink `ba5c6b161827` |
| desktop | `main` / `integration/0.1.45` | `SagerNet/sing-box-for-desktop:main` | main `aa63764` / product `e8b48be` | `8aab8eab02` | main 42/0 | gitlink `e8b48beadd7d` |
| dashboard | `main` | `SagerNet/sing-box-dashboard:main` | `b592c65618e` / 消费线 `9bde90e1cd55` | `3e4bf08e5bf` | 10/0 | desktop 内 `9bde90e1cd55` |

\* cronet `go` 的 2 个“落后”是上游从**较早**源 `73349ff` 的平行 generation；fork 的 generation 源 `4c96192` 已包含 `73349ff`。合并会以旧源产物覆盖 fork 的 29 个 native 模块，因此不合并，按生成源 ancestry 记录，而不是伪造分支零落后。

GitHub Compare（base=fork 正式分支, head=正确 upstream）：main、sing、sing-tun、sing-quic、quic-go、utls、android、desktop、dashboard、apple 的 `ahead_by` 全部为 **0**；uTLS 使用 1.9 线，不是父仓默认的 v1.7。

## D. 真实修复与设计融合（重点）

1. **CachedConn Read/Close 竞态**（sing `dcc5d99`）：CAS 只保证交接唯一，不保证 buffer 访问有序；改为 mutex ownership，barrier 测试在旧代码上重放 payload/报 data race。
2. **ACK-only pacing**（sing-quic `5253224`、quic-go `2e73222`）：CUBIC/BBR/Brutal 均只在 retransmittable 时调用 pacer；两个仓库各有回归测试。
3. **Salamander 短包**（sing-quic `a5639ae`）：≤salt 长度返回 0，不再把 salt 暴露为 payload；边界测试覆盖 0/salt-1/salt/salt+1/连续短包。
4. **XPlus 写入**（`8aab71d`）：返回明文长度，删除原地 XOR 的 vectorised writer，避免调用方 buffer 被污染。
5. **go stack ENOBUFS / auto-redirect 关闭/回程 / GSO / 合成 UDP IPv4 ID**（sing-tun）：放在 fork 的 per-session/device byte budget 与 waiter ownership 内；4 个 mutation-checked 测试。
6. **uTLS preset QUIC transport parameters**（`89a07ad`）：fork 的 marshal-time helper 覆盖同一 bug 且覆盖 Set 在 ApplyPreset 之前的顺序，保留 helper。
7. **uTLS/QUIC/pinning**：HTTP client/outbound 恢复 `MultiplexEnabled`/`OutboundWithMultiplex`；上游 TLS SHA-256 pinning 已在 fork。
8. **XHTTP stream-up 非 200**：`96bd665` 修复、`669fa275` 覆盖 400/404/413/500、body 不 drain、晚到拒绝；无重复实现。
9. **Dashboard 无共同祖先**：真实双父 merge `b592c65`，13 冲突逐 hunk；树是 fork 内容超集，97/97 测试、build/lint 通过；消费线 `9bde90e` 98/98。
10. **Android**：上游 5 提交合并，assemble 8m28s + unit 27s 通过。
11. **Desktop**：dashboard gitlink bump `9bde90e`，59/59 + build 通过。
12. **主内核**：108 冲突，保留 Fork 生命周期/预算/所有权；吸收 upstream parallel dial、listener rebind、direct UDP destination、HTTP body ownership、DNS local 条件、resolved deleted-path、HTTP multiplex capability。
13. **release hardening**：拒绝上游 root service 切换，恢复 `User=sing-box` + capability bounding set；`release/config` 测试通过。
14. **schema**：按 canonical tags 重新生成 `docs/schema.json`，`TestTrackedSchemaMatchesTheGenerator` 通过。
15. **module integrity**：root/test `go mod tidy -diff` 均为空，`go mod verify` PASS。

## E. 依赖与 ABI

- root `go.mod` / `test/go.mod` 的 8 个 replace 与 source-lock 完全一致；`go mod tidy -diff` 双侧为空。
- Cronet：root `362e63b`、all `7ce9c49`、29 个 native 模块全部 `362e63b`；没有重建无关平台 native，也没有只升 binding。
- gitlink：`clients/android=ba5c6b1`、`clients/desktop=e8b48be`、desktop 内 `dashboard=9bde90e`、`clients/apple=7fce711`（不变）。
- `release/source-lock.json` 已刷新；`release-freeze.py verify --lock release/source-lock.json` **PASS**。
- `release/dependency-final-shas.json` 重写为本轮最终事实（schema 3）。
- Go 1.25.5；Windows profile `release/DEFAULT_BUILD_TAGS_WINDOWS`，Linux/Darwin 使用对应 profile。

## F. 验证

| 门禁 | 结果 |
|---|---|
| `go build ./...`（Windows） | PASS |
| Windows 全量（profile tags、`-p 1`、DLL） | **106 ok / 3 已知基线失败**（common/tls、tlsspoof、windivert）；`common/httpclient` 在全量负载下偶发 1 次，单独 5/5 PASS |
| `-race`（route、dialer、fakeip、transport/http、masque、http） | PASS（route 464 s） |
| Linux `GOOS=linux CGO_ENABLED=0 go build` | PASS |
| Darwin `GOOS=darwin/arm64 go build` | PASS |
| Android `assembleDebug` + `testOtherDebugUnitTest` | PASS |
| Dashboard main 97/97、消费线 98/98、build | PASS |
| Desktop 59/59、build | PASS |
| source-lock verify | PASS |
| GitHub Actions | Verify **success**（run `42945339834`）、Android Core **success**（run `42945339735`，均为最终 head `b473c21`）；Mobile contract **success**（run `42942954909`，08b52a3；之后仅 CI 脚本/文档变化） |

## G. Git 与分支清理

主仓远端最终只剩：

- `testing`（default，`08b52a3`）
- `integration/0.1.45-final`（`08b52a3`，产品施工线）

删除（逐个明确删除，无通配）：

- `candidate/w07-2026-11-10`（已是 HEAD 祖先）
- `backup/02-ecfffe7d0`（44 个旧提交，已归档）
- `docs/03-overnight-gate-0.1.45`（1 个 docs 提交，已归档）
- `integration/next-round`（8 个 docs 提交，已归档）
- `next/client-consumption-candidate`（6 个 client pin 提交，已归档）

Archive tags（annotated，已推送）：`archive/0.1.45/main/{backup-02,docs-03-overnight-gate,integration-next-round,client-consumption-candidate}`。

其他仓库保留最小集合：sing/sing-tun/sing-quic/utls/android 单正式分支；quic-go `sagernet`+`integration/0.1.45`；wireguard `main`+`dev`（上游参考）+`awg3-inject`；cronet `main`+`go`；desktop `main`+`integration/0.1.45`；dashboard `main`+`integration/0.1.45`+`gh-pages`；Apple `main` + 既有 Apple NE 分支（未合并的独立工作，未删除）。未 force push，未移动 release tag。

## H. 遗留债务

1. **Apple**：无 Xcode/macOS，构建/XCTest/TestFlight 未验证；`clients/apple` 仍 pin 在 `7fce711`（integration 线），main 为 `97b35d1`。需要在 macOS runner 完成。
2. **既有基线失败**：`common/tls`、`common/tlsspoof`、`common/windivert` 在 Windows 上失败，非本轮引入；`common/httpclient` 的 race test 在全量并发下偶发，单独 5/5。
3. **Dashboard `lint:css`**：4 个 fork-only 规则错误（2px/600），非合并引入，未改设计值。
4. **Cronet `go` 的 2 个平行 generation 提交**：不合并，理由与证据见 §C；若未来要统一，需要重新走受控 native 构建流程。
5. **CI**：Verify `42945339834`、Android Core `42945339735` 在 `b473c21` success；Mobile contract `42942954909` 在 `08b52a3` success（之后仅 CI 脚本/文档变化）。
6. **`go vet ./...`** 仍有既有 unsafe.Pointer 警告（daemon/managed_service.go、experimental/libbox/debug.go、experimental/boxdd/authenticode_windows.go），非本轮冲突文件。
7. **未提交/未推送**：无；主工作树与 testing 工作树干净（`_p01_main` 的 submodule 状态除外，见备注）。

备注：`_p01_main` 是既有 testing worktree，已 fast-forward 到 `08b52a3`；它的 `clients/android` 子模块工作树可能仍显示旧 checkout，属于既有 worktree，不是产品分支状态。
