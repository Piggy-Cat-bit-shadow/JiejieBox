# 09 下一轮最值得解决的 5 个问题（Next round questions）

> 排序依据：证据强度 × 对 iOS footprint 的影响 × 改动可证明性。每项给出"已有证据 / 最小实验 / 通过标准 / 代价与风险"。
> 本轮不实现这些改造；只准备事实与实验设计。

## Q1. 逻辑流准入应该在 TUN→route 边界，而不是只在协议创建 seam

**已有证据**

- 上游 per-flow 资源在协议选择前创建：NAT entry、每会话 goroutine、首包引用（`sing-tun udp_nat.go:383-455`、`stack_go_udp.go:221`）。
- 准入在 outbound 创建 seam（`common/tls/client.go:197`、`protocol/anytls/admission.go`）。
- 实测：1000 并发 dial 时 gate 等待者 189–255 个（`raw/metrics/run1|run2`），每个等待者在真实 TUN 路径上还持有一套上游资源（`INFERRED`，本 harness 无 TUN）。
- 历史上 200 UDP flow → 约 201 次 createSession + 200 NAT goroutine（施工指令 §3.1 的历史诊断，非当前测量）。

**最小实验**

1. 在 `sing-tun` 的纯 Go NAT 测试里注入 200/500/1000 个新流，测量：NAT entries、每会话 goroutine、首包保留字节、dial 等待时间；
2. 在同一实验里让 outbound `ListenPacket` 延迟 500 ms，观察 gate 排队时上游资源的真实驻留；
3. 得出 `flow N → NAT entries / goroutines / bytes` 乘数。

**通过标准**：乘数可重复（±10%），且能证明 gate 排队期间上游资源与排队数成正比。

**代价/风险**：依赖仓库测量（不改代码）；若决定加入口 gate，会改变 UDP 首包延迟语义。

## Q2. 同键 session 创建 coalescing

**已有证据**

- AnyTLS 并发首流各自创建：`DialContext` 先 `takeIdleSession`，会话要在某条 stream 结束后才回 idle（`sing-anytls client.go:102-156`、`session.go:165-177`）。
- gate 只限速不合并：1000 并发里 288–306 次准入创建（`raw/metrics`）。
- 依赖 SDK 已有单飞先例：sing-quic 的 pending offer（`quic.go:364-382`），可与 v2rayquic 的 `clientOffer` 对照。

**最小实验**

1. 在 sing-anytls 的本地 server 上并发 200 个 `DialContext`，统计 `createSession` 次数（当前预期 ≈200）；
2. 增加 pending-creation future：首个创建者发布 future，等待者复用成功后 `openStream`；
3. 断言：创建次数 ≤ 2、200 流全成功、取消/Close 无泄漏、失败后等待者能重试。

**通过标准**：创建次数下降 ≥ 90%，无新增泄漏/死锁，`-race` 通过。

**代价/风险**：跨仓（需要单独分支/pin）；会话复用语义变化影响服务端兼容性。

## Q3. 后台测速/刷新在压力态降级

**已有证据**

- URLTest/DNS refresh 不读内存压力（`protocol/group/urltest.go`、`common/urltest/*` 无 pressure 引用）；`dns/client.go` 只读 power governor。
- buildgate 已经示范了"消费同一 pressure 收紧"的做法（`box_buildgate.go:59-67`、`common/buildgate`）。

**最小实验**

1. 给 URLTest round 与 DNS optimistic refresh 注入一个 `BackgroundAllowed() bool`（读同一个 `Service.pressure`/guard Level，不新建状态机）；
2. Critical 时暂停或降频，Armed 时减半；在单测里用可控 pressure 断言 round 不发起/频率下降；
3. 测量 Critical 期间后台创建的 gate 计数占比。

**通过标准**：Critical 下后台 probe 创建数下降 ≥ 80%，恢复后自动恢复；交互流不受影响。

**代价/风险**：低-中；需要避免多个组件各自读 atomic 的重复判断，建议一个共享接口。

## Q4. H3 物理创建接入统一准入

**已有证据**

- XHTTP H3 `quic.DialEarlyConn` 与 MASQUE H3 均无 guard（`transport/v2rayxhttp/http3.go:149-170`、`transport/masque/client.go:356-390`）。
- H1/H2 已经过 `common/tls` seam 计费；XMUX 默认 3 条连接，但 `max_concurrency` 模式无硬上界（`xmux.go:778-792`）。

**最小实验**

1. 本地 H3 server + `guard.Limit=1` 持有一个 permit，断言 H3 dial 不执行；
2. 释放后断言只创建一次、计数器为 1；
3. 增加 XMUX `max_connections` 默认值实验（不改变显式配置语义）。

**通过标准**：H3 拒绝时构造器不执行；复用不计费；Xray 互通用例不回归。

**代价/风险**：H3 连接迁移/0-RTT/GOAWAY 语义；需要独立 seam 审计。

## Q5. 观测缺口（为统一预算铺路）

**已有证据**：`raw/metrics` 中 `queue_bytes/nat_entries/sessions/quic_open/http_transports/mieru_underlays` 全部 `UNAVAILABLE`；`UDPNat` 无导出计数；guard 无 `maxWaiters`；buffer pool 无高水位（`01_RESOURCE_OWNERSHIP_MAP.md` §4）。

**最小实验**

1. `sing-tun` 增加只读 `Stats()`（entries、per-session bytes、drops），在 NAT 测试里断言数值正确；
2. `Gate` 增加 `MaxWaiters`（原子计数，2–3 行），断言峰值 ≥ 采样值；
3. 评估 `buf` pool 高水位（可在 `bufgeom` 侧计数，避免热路径原子）。

**通过标准**：新增观测在 200/1000 流测试里与实际对象数一致；开销 < 1% 基准。

**代价/风险**：依赖 bump；热路径计数需要基准证明。

## 优先级建议

```text
Q1（上游乘数测量） → Q2（coalescing） → Q3（后台让路） → Q4（H3 准入） → Q5（观测补齐）
```

Q1 是其他预算决策的前置事实；Q2 是收益最大且可独立验证；Q3/Q4 是缺口补齐；Q5 为统一预算提供输入。
