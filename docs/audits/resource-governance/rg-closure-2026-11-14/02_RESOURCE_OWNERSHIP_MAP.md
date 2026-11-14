# 02 资源所有权地图（Resource ownership map）

> 证据级别：除标注 `NEEDS_DEVICE` 外均为 `VERIFIED_SOURCE`（在当前 HEAD 阅读源码）。
> 依赖仓库以 `go.mod` 的 pin 为准，路径写作 `<mod>/...` 并从模块缓存读取；它们不是本轮施工对象。

## 1. 端到端链路

```text
iOS PacketTunnel / system proxy / TUN
  → sing-tun stack（go stack 为本分支默认）
  → UDPNat / TCPNat / DNS hijack / FakeIP
  → route.Router（规则、traffic_class、detour、分组）
  → route.ConnectionManager.NewConnection / NewPacketConnection
  → common/dialer（拨号、族选择、网络代次校验、连接登记）
  → 协议适配层（session 池 / creation-only admission）
  → 物理传输（TCP+TLS / QUIC / HTTP2 / HTTP3 / SDK underlay）
  → route copy / trafficsched / growth budget
  → Close / Cancel / Retire / Drain / Reload / Network Change / NE Stop
```

## 2. 每层的首批资源与所有者

| 层 | 首批资源 | 谁创建 | 谁拥有/关闭 | 证据 |
|---|---|---|---|---|
| TUN UDP NAT | `UDPNatConn`、`packetChan`(cap 64)、每会话一个 goroutine | `sing-tun` `UDPNat.getOrCreate` | `UDPNat` 的 LRU + cleanup goroutine | `sing-tun` `udp_nat.go:383-455` |
| TUN NAT 字节预算 | 每会话队列字节、NAT 全局字节 | `tryQueueBytes` 预留 | 出队/关闭路径返还 | `sing-tun` `udp_nat.go:516-533`、`:766-786` |
| DNS hijack | 每个 hijack 包一个解析 goroutine | `route/dns.go` | 解析结束 | `route/dns.go:91`、`:130-143` |
| route 连接登记 | 接纳票据 + 受管连接节点 | `ConnectionManager.AcquireConnection` | `TrackConn/TrackPacketConn`、`CloseAll`、drain | `route/conn_reclaim.go:415-458`、`:524-560` |
| route setup 代次 | 每代一个 context + 每在途 dial 一个 watch | `dialSetupGate.begin` | dial 建立/失败时 `finishSetup` | `route/dial_setup.go:89-128` |
| 协议创建许可 | ResourceGuard permit（有界队列、有限等待） | `Guard.AdmitCreation/AcquireFor` | 创建返回时 `Release` | `common/resourceguard/admission.go:33-49`、`gate.go:203-252` |
| AnyTLS session | 一条 TLS 会话 + readLoop goroutine + stream 表 | pinned SDK `Client.createSession` | `Client.Close/CloseIdleConnections`，SDK 内池 | `<sing-anytls>/client.go:102-156`、`:158-201` |
| Hysteria2/Hysteria/TUIC | 一条共享 QUIC 连接 | sing-quic `offerNew` / `DialEarlyConn` | SDK client `Close` / idle 回收 | `<sing-quic>/quic.go:364-382` |
| TrustTunnel | 一条共享 session | `transport/trusttunnel/client.go` | client `Close/CloseIdleConnections` | `transport/trusttunnel/client.go:102-108`、`protocol/trusttunnel/outbound.go:153` |
| Mieru | underlay socket（+ SDK 内 Mux/加密 session） | pinned mieru SDK `newUnderlay` | SDK `Stop`；wrapper 记录活跃连接 | `protocol/mieru/dialer.go:85-122` |
| v2rayquic | 一条共享 QUIC 连接 | `Client.offerNew` | `Client.Close` / idle 回收 | `transport/v2rayquic/client.go:529-548`（createConnection）、`:550`（offerNew）、`:623`（Close） |
| 共享 TLS | 每流一条 TCP+TLS 连接 | `common/tls` `handshakeWithAdmissionFor` | 调用方（route copy 结束） | `common/tls/client.go:185-216` |
| HTTP/XMUX | 物理 HTTP/1.1、H2、H3 连接 | `transport/v2rayxhttp` | XMUX pool `close/drain` | `transport/v2rayxhttp/xmux.go:761-799`、`http3.go:149-170` |
| MASQUE | CONNECT-IP session + H1/H2/H3 tunnel | `transport/masque/client.go` | `ClientEndpoint` | `transport/masque/client.go:356-390` |

## 3. 每个协议的真实创建入口与准入结论

| 协议/路径 | 真实创建入口 | 准入 | 单位 | 备注 |
|---|---|---|---|---|
| AnyTLS TCP/UoT | pinned SDK `createSession` → `AdmitCreation` hook | 是（创建后置） | 物理 session | 复用**不再**收费；0.1.45 修复，见 `05` §3 |
| Hysteria2 / Hysteria | sing-quic `offerNew` → `AdmitCreation` | 是 | 物理 QUIC 连接 | hook 由 sing-quic 在 create 内调用 |
| TUIC | sing-quic `offerNew`（含 auth 写） | 是 | 物理 QUIC 连接 | 认证写入在 create 内完成 |
| TrustTunnel | `transport/trusttunnel` session 创建 | 是 | 共享 session | `AdmitCreation` 字段 |
| Mieru | SDK `newUnderlay` → `serverDialer.DialContext` | 是（socket 创建前） | 物理 underlay socket | 许可在 dial 返回即释放；Mux/加密 session 在其上 |
| Naive（native/Cronet，仅 Darwin 构建） | engine socket callback `nativeCreationDialer` | 是（250 ms 有界等待，无队列） | 物理 socket | Windows/others 构建为 `NOT_BUILT` |
| **v2rayquic**（`transport.type=quic`） | `Client.completeOffer` → `dialConn` | **本轮补齐**：`createConnection` | 物理 QUIC 连接 | 见 `05` §5 |
| VLESS/VMess/Trojan + TLS/REALITY | `common/tls` `handshakeWithAdmissionFor` | 是 | 物理 TCP+TLS 连接 | 每流一条，无共享 session |
| VLESS/VMess/Trojan + XHTTP H1/H2 | `tls.NewDialer` → `common/tls` seam | 是 | 物理 HTTP 连接 | 由 TLS seam 计费 |
| VLESS/VMess/Trojan + XHTTP H3 | `transport/v2rayxhttp/http3.go` `Dial` 内 `quic.DialEarlyConn` | **否** | 物理 QUIC 连接 | `DEFER`，见 `08` |
| MASQUE H2 | tunnel client 的 TLS dial | 是（经由 `common/tls`） | 共享 tunnel | — |
| MASQUE H3 / CONNECT-IP session | `quic.DialEarlyConn` / `OpenTunnelWithInfo` 后的 session 构建 | **否** | session/transport | `DEFER`，见 `08` |
| Shadowsocks / Snell | 每流普通 TCP/UDP，无共享 session | 不适用 | — | 不属于"昂贵共享创建"；由 route 连接登记与 copy 预算约束 |
| WireGuard | 启动期 `device.NewDevice`，每流在 device 内 | 不适用 | 每 endpoint 一个 device | 配置有界 |
| grpclite | `tls.NewDialer` | 是 | 共享 HTTP/2 连接 | 走 `common/tls` seam |
| 完整 gRPC（`with_grpc`） | 无 | — | — | 当前两个 profile 都 `NOT_BUILT` |

## 4. 准入位置是否正确（按施工指令 §5.3 检查）

**满足**：所有已接入的 seam 都在"真实昂贵分配"之前，且许可只覆盖创建本身：

- sing-quic / Mieru / Naive / TrustTunnel / 共享 TLS 的 hook 都在 socket/handshake 之前；
- `AdmitCreation` 在 `create` 返回后立即释放，不包裹返回连接（`common/resourceguard/admission.go:41-49`）；
- 嵌套 dial 通过 `HeldPermit` 只领一份（`common/resourceguard/held.go:19-31`）。

**不满足的两处（未修，已记录）**：

- XHTTP H3 与 MASQUE H3 的物理 QUIC 创建没有经过任何共享准入；
- 同一目的节点的并发"首次创建"没有 coalescing：gate 限速但不合并，N 个并发流仍可产生 N 次真实 session 创建（AnyTLS 最明显）。

## 5. 所有者与取消

| 资源 | 取消来源 | 已覆盖的证据 |
|---|---|---|
| route 在途 stream/UDP setup | 调用方 ctx + 代次 context | `route/dial_setup.go:89-128`；本轮补齐非 connected UDP 分支（`route/conn.go:827-838`） |
| 已建立连接 | `CloseAll` / drain / idle reclaim | `route/conn.go:221-270`、`route/conn_reclaim.go:330-381` |
| 准入等待者 | 调用方 ctx / 等待预算 | `common/resourceguard/gate.go:241-252`、`withdraw` |
| AnyTLS session | client Close / CloseIdleConnections | `protocol/anytls/outbound.go:111-117` |
| 共享 QUIC | 各自 client Close / generation reset | `transport/v2rayquic/client.go:623-660` |
| UDP NAT 会话 | LRU 淘汰 / timeout / cleanup | `sing-tun` `udp_nat.go`、`udp_nat_cleanup.go` |
| OOM 回收 | `releaseAccess` 串行化的单执行器 | `service/oomkiller/timer.go:943-1024` |
