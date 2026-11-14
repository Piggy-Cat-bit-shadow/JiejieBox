# NAT 首包保留的决定（DECISION）

> 判定：**本轮不为 NAT 首包建立字节账本**。这是一个有边界的工程决定，不是又一条 `DEFERRED`。
> 触发复审的条件写在文末。

## 1. 事实

| 事实 | 证据 |
|---|---|
| TUN UDP NAT 在协议选择之前创建 session：`UDPNatConn` + `packetChan(64)` + 每会话 goroutine + 字节预留 | `sing-tun` `udp_nat.go:383-455` |
| 首包在 dial/handshake 期间被保留，且**不计入** `MaxBytes` 账本（账本只统计入队 payload） | gvisor：`UDPBackWriter.packet = packetBuffer.IncRef()`（`stack_gvisor_udp.go:101-127`）；go stack：`goUDPUserData.packet` 快照（`stack_go_udp.go:130-141`、`:459-464`） |
| 释放点：gvisor `HandshakeSuccess`/`HandshakeFailure` 里 `packet.DecRef()`；go stack 在 session 完成后清理 | `stack_gvisor_udp.go:120-140`、`stack_go_udp.go:459-464` |
| 会话数量上界 `MaxSize`：iOS 4096，其他 16384 | `udp_nat.go:140-149` |
| 首包最大长度受 UDP 报文上界约束（实践为 MTU 量级，理论上限 64 KiB） | IPv4/IPv6 UDP 长度字段 + TUN MTU |

## 2. 量化上界

首包保留字节 ≤ 在途首包数 × 单包字节：

| 场景 | 在途首包（每条新 UDP 流 1 个） | 单包（MTU≈1400） | 保留字节 |
|---|---|---|---|
| 历史诊断 200 flow | 200 | 1400 | ≈ 0.27 MiB |
| 1000 flow 压力 | 1000 | 1400 | ≈ 1.34 MiB |
| 硬上界（iOS MaxSize） | 4096 | 最大 64 KiB | 理论 ≤ 256 MiB；实践受 MTU 与 burst 规模约束（≤ 5.5 MiB @1400） |

对照 iOS 50 MiB 观测预算：在观测过的 burst 规模下，首包保留是**亚 MiB 到单 MiB 级**，不是主导项；主导项是 TLS/QUIC 临时状态与 goroutine 栈（第一阶段诊断：11.3 MiB stacks / 15.5 MiB heap objects）。

## 3. 为什么本轮不实现账本

1. **所有权不在本仓**：保留者是两套 stack 的 writer（gvisor `UDPBackWriter`、go `GoPacketConn`），释放点是 `HandshakeSuccess/Failure`。要在 `UDPNat` 记一笔并在释放点退账，需要：
   - 给两种 writer 增加 `RetainedPacketBytes() int` 与释放通知；
   - 在 `UDPNatConn` 上实现 `PacketConnHandshakeSuccess` 并对 route 的 `N.ReportPacketConnHandshakeSuccess(conn, remote)` 做桥接（当前 `UDPNatConn` 没有该方法，因此 writer 的 `HandshakeSuccess` 不会由这条路径到达）；
   - 两套 stack 各自测试，且失败/取消/关闭路径都要退账。
   这不是"加一个原子计数"，而是跨两层接口的语义变更。
2. **无验证环境**：本机 Windows 无 TUN 设备；纯 Go 注入测试能证明账本算术，不能证明 handshake 桥接与真实 TUN 生命周期。
3. **收益有限**：上表显示在观测规模下是亚 MiB–单 MiB 级；把它纳入 8 MiB NAT 字节预算会挤压正常的队列容量。
4. **已有替代边界**：`MaxSize`（会话数）× 单包上界已经是硬边界；`udp_nat_max` 可配置，下一轮如需更强约束可在配置层处理。

## 4. 如果下一轮要做，最小设计

```text
UDPNat
  + retainedBytes atomic.Int64
  + maxRetainedBytes int  (默认 = min(MaxBytes/2, 4 MiB)，0 表示不启用)
  getOrCreate constructor:
    if w, ok := writer.(retainedPacketReporter); ok {
        retained := w.RetainedPacketBytes()
        if retained > 0 && !reserveRetained(retained) { return nil,false } // 丢流，不排队
        newConn.retained = retained
    }
  releaseRetained(conn):  CAS 到 0，退账（幂等）
  UDPNatConn.PacketConnHandshakeSuccess(conn net.PacketConn):
    转发给 writer 的 handshake 接口；成功后 releaseRetained
  UDPNatConn.Close(): releaseRetained（失败/取消路径）
```

验收：`sing-tun` 纯 Go 测试注入 200/1000 个新流 + 阻塞 handler，断言 `retainedBytes` 单调、关闭后归零、超预算时丢流而非排队；主仓 iOS 侧 `NEEDS_DEVICE` 验证真实 footprint。

## 5. 复审触发条件

满足任一条件时重新打开：

- `udp_nat_max` 默认值或配置被提高（大于 4096）；
- 观测到单次 burst 的新 UDP 流 > 2000；
- iOS footprint 诊断中出现"dial 等待期间"的无法解释的 MiB 级增长；
- 依赖升级窗口里 `sing-tun` 已经有 RetainedBytes 类接口（届时只需接线）。
