# NAT 首包保留的决定（仅 Go Stack）

> 范围：当前生产使用的 **Go Stack**。gVisor 已退休，本文件不再把它作为并行路径分析；历史 gVisor 实现只作为"另一套 stack 曾如此"的注记，不再参与结论。
> 判定保持不变：**不为 NAT 首包建立字节账本**（`DECISION`）。但首包的释放路径已在 2026-11-14 补齐并验证，详见 §1.1。

## 1. 事实（Go Stack）

| 事实 | 证据 |
|---|---|
| TUN UDP NAT 在协议选择之前创建 session：`UDPNatConn` + `packetChan(64)` + 每会话 goroutine + 字节预留 | `sing-tun` `udp_nat.go:383-455` |
| 首包被克隆为堆上的 `[]byte`，保存在 `GoPacketConn.snapshot`，用于握手失败时构造 ICMP unreachable；**不计入** `MaxBytes` 账本 | `stack_go_udp.go` 中 `snapshot: slices.Clone(data.packet)`；`MaxBytes` 只统计入队 payload |
| 释放点一：`HandshakeSuccess` 置 `snapshot=nil` | `stack_go_udp.go` |
| 释放点二：`HandshakeFailure` 取出 snapshot、置 nil，再构造 ICMP；无论 reply 是否成功，引用已释放 | `stack_go_udp.go` |
| 释放点三：`Close`（2026-11-14 修复）在 `snapshotAccess` 锁内清空 snapshot | 本次提交 `938b89d`；测试 `stack_go_udp_snapshot_test.go` |

### 1.1 Close 的保留窗口（已修复，不是泄漏）

修复前 `Close` 只把 flow 从 `directory.udpFlows` 删除并处理 splice，**没有清 snapshot**。若 flow 在握手回调之前被关闭，且 `GoPacketConn` 仍被 NAT 侧对象引用，首包会保留到对象不可达（GC 回收）。这是**保留窗口**，不是已证明的泄漏：NAT 释放 conn 后对象不可达；但没有证据显示跨 Generation 或永久持有。

修复：`Close` 在 `snapshotAccess` 锁内清空 snapshot，与两个握手回调同一把锁；没有改 NAT 账本。测试覆盖 success / failure（含 `BuildICMPError` 拒绝路径）/ Close 三条路径，以及 Close 与 success 的并发竞争（`-race` 通过）。gVisor 未触碰。

## 2. 量化上界

首包保留字节 ≤ 在途首包数 × 单包字节：

| 场景 | 在途首包（每条新 UDP 流 1 个） | 单包（MTU≈1400） | 保留字节 |
|---|---|---|---|
| 历史诊断 200 flow | 200 | 1400 | ≈ 0.27 MiB |
| 1000 flow 压力 | 1000 | 1400 | ≈ 1.34 MiB |
| 硬上界（iOS MaxSize 4096） | 4096 | 最大 64 KiB | 理论 ≤ 256 MiB；实践受 MTU 与 burst 规模约束（≤ 5.5 MiB @1400） |

对照 iOS 50 MiB 观测预算：在观测过的 burst 规模下，首包保留是**亚 MiB 到单 MiB 级**，不是主导项；主导项是 TLS/QUIC 临时状态与 goroutine 栈。

## 3. 为什么不做账本

1. **所有权在 Go writer**：保留者是 `GoPacketConn.snapshot`，退账点必须与 `HandshakeSuccess/Failure/Close` 对齐；这需要新的 writer 接口或 `UDPNatConn` 桥接，属于跨接口语义变更。
2. **无 TUN 验证环境**：本机 Windows 无 TUN 设备；纯 Go 注入测试能证明释放与算术，不能证明真实 TUN 生命周期。
3. **收益有限**：上表显示在观测规模下是亚 MiB–单 MiB 级；把它纳入 8 MiB NAT 字节预算会挤压正常队列容量。
4. **已有硬边界**：`MaxSize` × 单包上界；`udp_nat_max` 可配置。

## 4. 如果下一轮要做，Go-only 最小设计

```text
UDPNat
  + retainedBytes atomic.Int64
  + maxRetainedBytes int  (默认 = min(MaxBytes/2, 4 MiB)，0 表示不启用)
  GoPacketConn 初始化时:
    retained := len(snapshot)
    若超预算 → 丢弃该流（不排队）
  GoPacketConn.releaseSnapshot(): CAS 到 0 并退账（幂等）
  HandshakeSuccess / HandshakeFailure / Close 共用 releaseSnapshot()
```

验收：`sing-tun` 纯 Go 测试注入 200/1000 个新流 + 阻塞 handler，断言 `retainedBytes` 单调、关闭后归零、超预算时丢流而非排队；主仓 iOS 侧 `NEEDS_DEVICE` 验证真实 footprint。gVisor 不参与。

## 5. 复审触发条件

- `udp_nat_max` 默认值或配置被提高（大于 4096）；
- 观测到单次 burst 的新 UDP 流 > 2000；
- iOS footprint 诊断中出现"dial 等待期间"的无法解释的 MiB 级增长；
- 依赖升级窗口里 `sing-tun` 已经暴露 `RetainedBytes` 类接口（届时只需接线）。