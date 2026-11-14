# 08 开放问题与下一轮攻击面（Open questions）

> 每条 = 观察到的事实（带证据） + 为什么本轮不修 + 下一轮最小实验。
> 未标注 `VERIFIED_*` 的内容一律是 `INFERRED` / `UNTESTED`。

## Q1. 同一目的节点的并发首个 session 仍被放大 —— 最高优先级

**事实**

- AnyTLS `DialContext` 先 `takeIdleSession()`，失败才 `createSession()`（`<sing-anytls>/client.go:102-156`）。
- 会话只在**某条 stream 结束**时通过 `releaseSession` 回到 idle 链表（`<sing-anytls>/session.go:165-177`），因此并发首流几乎必然各自创建。
- 实测口径见施工指令的历史诊断：200 UDP flow → 约 201 次 `createSession` 尝试；本仓库无设备无法复测。
- 当前 gate 只限制"同时创建 32 个"，不减少"总共创建 200 个"。

**为什么本轮不修**：修复必须进入 pinned SDK（跨仓），且需要决定"创建失败后 waiter 是否重试"、"是否允许复用未完成握手的会话"等语义。

**下一轮最小实验**：

1. 在 sing-anytls 仓库加一个纯本地 TLS/回环 server 的测试：200 个并发 `DialContext`，统计 `createSession` 次数；
2. 加"创建中 future"：首个创建者发布 `pendingCreation`，其余 dial 等待它并在成功后 `openStream`；
3. 断言：创建次数 ≤ 2、所有 200 个流成功、取消/关闭无泄漏；
4. 通过后再考虑修改 pin，并在主仓加集成测试。

## Q2. XHTTP H3 与 MASQUE H3 的物理创建未准入

**事实**

- `transport/v2rayxhttp/http3.go:149-170`：`Dial` 内直接 `dialer.DialContext(UDP)` + `quic.DialEarlyConn`，无 `resourceguard`。
- `transport/masque/client.go:356-390`：`connect()` 打开 tunnel（H2 经 `common/tls` 计费，H3 不经），随后构建 CONNECT-IP session，同样无准入。
- H1/H2 的 XHTTP 物理连接经 `common/tls` seam 计费（`common/tls/client.go:185-216`），因此**只有 H3 缺口**。

**为什么本轮不修**：H3 有连接迁移、0-RTT、GOAWAY 语义；把 permit 放进 `Dial` 闭包需要证明它与连接生命周期/重试的交互，且不能破坏 Xray 互通。

**下一轮最小实验**：用 `with_quic` + `with_xhttp` 起本地 H3 server，`guard.Limit=1` 并持有一个 permit，断言 `Dial` 不执行；释放后断言只创建一次且计数器为 1。MASQUE 同理（H3 tunnel + CONNECT-IP）。

## Q3. `GuardStats` 是否可能报告"level 与 limit 不一致"

**事实**

- `Stats()` 用 `observe()` 返回的 level 与 `gate.Stats()` 的 plan 组合（`common/resourceguard/guard.go:440-450`）。
- 若在 `observe()` 与 `apply()` 之间有更新的 transition 被另一个 caller 发布，`apply` 会提前返回（`guard.go:380-390`），此时报告的 level 可能是旧的，而 gate plan 是新的。

**为什么本轮不修**：窗口极窄，且无法用现有 seam 稳定复现（需要可控的调度点）；在没有失败测试的情况下改它会违反"先 RED 再修"。

**下一轮最小实验**：给 `Guard` 加一个测试专用的 `afterObserveHook`（仅测试可设），在 hook 里推进另一次 transition 并 apply，然后断言 `Stats()` 的 `Level` 与 `Limit` 来自同一次 transition；修复方向是在同一把 `access` 锁内读取 applied level 与 gate plan。

## Q4. build gate 的第二个 Guard 与 shed 副作用

**事实**

- `box_buildgate.go:42-52` 用同一份配置创建了第二个 `resourceguard.Guard`（无 `DemoteAfter` → 默认 1 s），其 pressure 函数调用 owner guard 的 `Level()`（`box_buildgate.go:59-67`）。
- `Level()` 有副作用：会推进 owner guard 的状态并可能执行 shed hooks（`guard.go:279-291`、`:376-399`）。
- 因此一次后台 build 的准入读取可以触发进程级 `CloseIdleConnections`。

**为什么本轮不修**：它不是新的压力权威（读的是同一个 owner），只是"读取带副作用"的传播；没有观测到故障。修法需要区分"只读 level"与"驱动转移"的 API，属于接口调整。

**下一轮最小实验**：在 `buildgate` 测试里用 fake clock 驱动 owner guard 进入 Critical，断言 build Acquire 只观察、不重复执行 shed hook（记录 hook 调用次数）。

## Q5. shed 与 reclaim executor 的并发 trim

**事实**

- shed hooks 在调用者 goroutine 上同步执行（`guard.go:408-416`）。
- OOMKiller 执行器用 `releaseAccess` 串行化 idle/crisis（`timer.go:943-1024`），但 shed 不持有该锁。
- 两者都会调用 `CloseIdleConnections`/`CloseAllConnections` 类操作。

**为什么本轮不修**：未观测到 double-close 或 panic；要修需要把 shed 也纳入 `releaseAccess`，会改变"准入路径不阻塞在回收锁上"的现有权衡。

**下一轮最小实验**：并发触发 Critical（shed）与 idle/crisis 执行器，`-race` + 计数 hook 断言同一池的 trim 不重入、close 恰好一次。

## Q6. UDP NAT 首包保留与 8 MiB/实例粒度

**事实**：首包引用不计入字节账本；`MaxSize` 按 `natCount` 拆分但 `MaxBytes` 默认值每实例重新应用（见 `03` §1）。

**为什么本轮不修**：都在 pinned `sing-tun` 内，属于依赖仓库改动；本轮边界允许但优先级低于主仓 P0。

**下一轮最小实验**：在 `sing-tun` 的纯 Go 测试里注入阻塞 handler（黑洞 dial），断言会话数与保留首包总字节随 `MaxSize` 变化，而 `MaxBytes` 只约束队列负载；再决定是否把首包纳入账本或把 `MaxBytes` 按 `natCount` 拆分。

## Q7. DNS 非 hijack 查询的 goroutine 并发

**事实**：`dnsHijackConcurrency=256` 只覆盖 TUN hijack 入口（`route/dns.go:91`）；`Exchange/ExchangeAsync` 的内部路径没有统一上限。

**下一轮最小实验**：对本地阻塞 DNS transport 发起 1000 次 `ExchangeAsync`，统计 goroutine 峰值与拒绝/超时行为；再决定是在 router 层加信号量还是维持现状。

## Q8. `Permit.Wait()` 语义

见 `05` §4.7。修复成本低，但生产无调用者；留给下一轮与可观测性工作一起做。

## Q9. 真机证据

| 问题 | 缺什么 |
|---|---|
| NE 真实 `phys_footprint` 曲线与阈值行为 | iPhone + 签名 NE + 诊断 ZIP |
| `task_info` 失败时本修复的 ARMED 行为 | 需要能构造失败的设备/系统版本 |
| Jetsam 是否发生、与 50 MiB 预算的关系 | 设备日志 |
| 蜂窝/Wi-Fi 切换时 UDP setup 取消的实际收益 | 设备侧网络切换 + 计数 |

## Q10. 本轮观察到的环境不稳定

`go test ./route` 第一次以 `exit status 0xffffffff` 终止，后两次通过。未归因；若下一轮把它当门禁，应先确认这是否为 Windows 本机资源限制导致的偶发。
