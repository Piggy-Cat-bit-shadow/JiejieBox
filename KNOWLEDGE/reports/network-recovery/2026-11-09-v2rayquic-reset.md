# V2Ray QUIC：网络更换不再永久杀死 transport（2026-11-09）

## 问题

`protocol/{vless,vmess,trojan}.Outbound.InterfaceUpdated` 在每次物理网络变动时调用
`h.transport.Close()`。`transport.type=quic` 的 `v2rayquic.Client.Close()` 会取消 client 的
life-time context（`c.cancel`），而后续 `offer` 都从该 context 派生。因此第一次换网之后，
该出站的 QUIC transport 无法再建立会话——新连接只能一直失败，直到整个 outbound/Box 重建。

现有测试只覆盖了 Close 的丢弃语义，没有覆盖“换网后第二次拨号”。

## 修复

- `transport/v2rayquic/client.go` 新增 `Reset()`：摘下并关闭当前连接、标记并取消 in-flight
  pending，但**不取消 client 的 lifetime context**；后续 `offer` 可以重新建连。
- `Close()` 语义不变，仍是最终、不可逆的关闭（Scope 清理仍走它）。
- `protocol/{vless,vmess,trojan}/outbound.go` 的 `InterfaceUpdated` 改为：transport 实现
  `Reset() error` 时调用 Reset，否则保持原来的 Close（兼容其他 transport 实现）。

## 验证

```sh
go test -tags with_quic ./transport/v2rayquic -run TestResetKeepsTheClientAliveButCloseEndsIt
# ok
CGO_ENABLED=1 go test -race -tags with_quic ./transport/v2rayquic   # ok
go build ./...                                                       # exit 0
```

新测试断言：Reset 关闭当前连接恰好一次、client ctx 仍存活、第二次 offer 真的再次 dial；
Close 之后 ctx 取消，保持最终关闭语义。

等级：`VERIFIED_UNIT`（主机 + race）。尚未在真实 `InterfaceUpdated` 生产入口做端到端换网
验证；尚未覆盖 VLESS/VMess/Trojan 各自真实拨号 payload。该切片只解决“换网后 QUIC transport
永久不可用”，不声称已实现旧流迁移或切组恢复。

## 边界

- 只处理 `transport.type: quic`；XHTTP/H3、HY2、TUIC、TrustTunnel 不在本切片。
- Reset 仍会关闭当前 QUIC 连接（旧流不迁移）；旧流保留/迁移是后续切片。
- 真实网络事件、TUN、selector/URLTest 提交合同未在本切片验证。
