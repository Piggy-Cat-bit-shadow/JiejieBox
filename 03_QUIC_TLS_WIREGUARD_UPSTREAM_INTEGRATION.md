# 03｜QUIC / TLS / WireGuard 传输层上游融合报告

- 执行时间：2026-11-11
- 前置：`01` 审计、`02` sing/sing-tun 融合
- 结果：`sing-quic`、`quic-go`、`utls` 三个正式分支均已吸收正确上游并 `ahead_by=0`；WireGuard 无需合并（已 0 落后）；内核换 pin 后传输层消费者全部通过。

## 1. 最终 SHA

| 对象 | 值 |
|---|---|
| sing-quic fork `main` | `aa6d98964049e4a7b1446e0758dd4a6ec2e225e6`（merge `7075cd7`、Salamander 测试 `4a95023`、pacing 测试 `8b53953`、go.sum `aa6d989`） |
| sing-quic 上游 | `SagerNet/sing-quic:main` @ `5253224b67cf` |
| quic-go 正式 `sagernet` | `f864f2f12006f053f22d577c90fdd8f3b45d4d47`（merge `7fde0eb4` + 测试 `f864f2f1`） |
| quic-go 消费线 `integration/0.1.45` | `158445cdda77c05e4430fe40721709bf7bc2ffe2`（merge `sagernet`） |
| quic-go 上游 | `SagerNet/quic-go:dev` @ `2e73222e96` |
| utls fork `main` | `e14b88879d809306332be7d2c226384a6f3e8fba` |
| utls 正确上游 | `MetaCubeX/utls:v1.9.0-mod-meta` @ `89a07ad380` |
| WireGuard `awg3-inject` | `a765d957fd814e5c0ee579936a0780e73d66e533` |
| WireGuard 上游 | `sagernet/wireguard-go:main` @ `e4d85ef2ed` |
| 内核消费提交 | `a6450700129495dbd12a5e2b3221a0b4a0aea51b` |

GitHub Compare（base=fork, head=正确上游）：sing-quic `ahead_by=0 / behind_by=9`；quic-go `sagernet` `ahead_by=0 / behind_by=20`；utls `ahead_by=0 / behind_by=69`；WireGuard `ahead_by=0 / behind_by=22`。`ahead_by` 均为 0。

## 2. sing-quic（4 个上游提交）

合并 `upstream/main`，零冲突，5 个文件：

| 上游提交 | 内容 | 测试 |
|---|---|---|
| `a5639ae` | Salamander `ReadFrom`/`Read` 对 `n <= saltLen` 返回 0 长度，而不是裸 `return`（旧代码会把 salt 当 payload 暴露） | 新增 `hysteria2/salamander_test.go`：0、salt-1、salt、salt+1、完整 payload、短包后接有效包、错误包夹在中间；旧代码 7/8 字节用例失败，合并后全过 |
| `fad401c` | Salamander 写入路径的同类修正 | 同上，写入/读取 round-trip |
| `8aab71d` | XPlus `WriteTo` 返回明文长度；删除 vectorised XPlus 写入（它会原地 XOR 调用方 buffer，发送失败后调用方无法重试） | 上游代码本身；fork 没有 fork-only 的 vectorised XPlus，删除是上游意图 |
| `5253224` | CUBIC / BBR / Brutal 的 `pacer.SentPacket` 移到 `isRetransmittable` 判断之后，ACK-only 包不再消耗 pacing 预算 | 新增三个 sender 的 pacer 预算/时间断言：ACK-only 不更新 last-sent、不消耗预算；retransmittable 仍更新。三个包全过 |

保留：fork 的 `quic_ceiling_test.go`、`hysteria2/owned_datagram_test.go`、`tuic/owned_datagram_test.go` 全部通过；硬 packet ceiling 与 owned datagram 路径未改。

## 3. quic-go（1 个上游提交）

- `2e73222`：CUBIC `OnPacketSent` 在 `isRetransmittable` 之后才调用 `pacer.SentPacket`，ACK-only 包不再改变 pacer 的 last-sent/budget。
- `sagernet` 合并后新增 `internal/congestion/cubic_sender_test.go`：
  - ACK-only 包：`pacer.lastSentTime` 保持 zero、预算不变、`largestSentPacketNumber` 不前进；
  - retransmittable 包：pacer 被更新、预算下降、包号前进。
  - 恢复旧顺序时测试在第一条断言失败；合并后通过。
- `sagernet` 推送后，合并进实际消费的 `integration/0.1.45`，内核 pin 指向该 merge。
- 全量 `go test ./...`（quic-go 全部包）通过。

## 4. utls（1 个上游提交）

- `89a07ad`：`SetTransportParameters` 只写 `quic.transportParams`，而 preset ClientHello 序列化的是 `ApplyPreset` clone 出来的 `QUICTransportParametersExtension`；参数永远到不了对端。上游改为直接写该 extension 的 `marshalResult`。
- 冲突处理：fork 已有 `syncQUICTransportParameters()`，在 marshal 时解析参数、写入 clone 的 extension 并清缓存，覆盖同一 bug，且覆盖上游直接循环做不到的顺序（先 `SetTransportParameters` 后 `ApplyPreset` 时还没有 extension 可写）。保留 fork 实现，冲突注释记录上游结论。
- 测试：`TestUQUICPresetTransportParametersReachClientHello` 走 preset + SetTransportParameters + Start，断言 ClientHello 的 `quic_transport_parameters` 与输入逐字节一致；utls 全量 `go test ./...` 通过。
- 正确上游固定为 `v1.9.0-mod-meta`，未使用仓库默认 `v1.7.0-mod-meta`。

## 5. WireGuard

- `awg3-inject` 已包含 `sagernet/wireguard-go:main`（`merge-base --is-ancestor` 通过，0 behind / 22 ahead），无需合并。
- `go test ./...`：`device` ok（7.9 s），`ipc`/`ratelimiter`/`replay`/`rwcancel`/`tai64n`/`tun` 无测试文件；全绿。
- 说明：`go build ./...` 在仓库根会报 `function main is undeclared in the main package`，这是 wireguard-go 根包按平台 build tag 提供 main 的既有布局，与合并无关；实际库包/测试均通过。

## 6. 内核消费与验证

- pin：
  - `sing-quic => Piggy-Cat-bit-shadow/sing-quic v0.7.2-0.20261110234451-aa6d98964049`
  - `quic-go => Piggy-Cat-bit-shadow/quic-go v0.61.1-0.20261110231807-158445cdda77`
  - `utls => Piggy-Cat-bit-shadow/utls v1.8.8-0.20261110231903-e14b88879d80`
  - WireGuard 保持 `a765d957`。
- 消费者测试（Windows，Windows profile tags）全部通过：
  `transport/v2rayquic`、`transport/v2rayxhttp`、`transport/masque`、`protocol/masque`、`protocol/hysteria2`、`protocol/tuic`、`transport/wireguard`、`protocol/naive`。
- `common/tls` 仍是既有基线失败（`unexpected EOF`），与本轮 pin 无关，阶段 05 统一记录。
- 内核全量（新 pin，`-p 1`，Windows profile tags，含 DLL）：除 `common/tls`、`common/tlsspoof`、`common/windivert` 三个既有基线失败外全部 PASS；`kernel-windows-full-3.txt`。
- Linux（WSL）：`go build -tags <profile> ./...` 与传输层消费者测试
  （v2rayquic / masque / hysteria2 / tuic / wireguard）全部 PASS。
  这一步发现并修复了一个只在 Linux/Darwin 编译的残留：`protocol/bridge` 的读循环仍调用旧的 `ReadRetry.Wait(err)`，导致 Linux 构建失败；已改为无参调用并显式保留 `ErrTooManySegments` 可恢复语义，提交 `8cc1dbcc63f`。

## 7. 未解决/风险

1. Darwin 的 `msgx`/`sendto`/`udp` socket 路径本轮无 macOS 主机，只能依赖上游与交叉编译（bridge/device 已交叉编译通过）；阶段 05 目标平台 CI 复跑。
2. ~~ACK-only pacing 的 sing-quic 三个 sender 没有独立单测~~——已补 `congestion_meta1/cubic_pacing_test.go`、`congestion_meta2/bbr_pacing_test.go`、`hysteria/congestion/brutal_pacing_test.go`，三个包全过。
3. 上游删除 vectorised XPlus/Salamander 写入是有意修正（原地修改调用方 buffer）；fork 的 owned-datagram/ceiling 路径未受影响，但若有外部调用方依赖这些类型，需在发布说明中标注（它们不是公开 API 稳定性承诺）。
4. utls 的 fork helper 与上游直接写 `marshalResult` 的差异保留：fork 更严格（会 parse 参数并在失败时返回错误）；若未来上游参数包含 fork parser 不支持的类型，需要同步 parser。
5. WireGuard 的 AWG 分支领先上游 22 个提交，未做逐提交与上游主线的语义对照；本轮确认 ancestry 与测试，阶段 05 不再改它。
