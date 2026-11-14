# 04 压力状态机与准入策略真值表（Pressure state machine）

> 依据：`service/oomkiller/timer.go`、`service/oomkiller/policy.go`、`common/resourceguard/{guard,gate,growth}.go`、`route/conn.go`、`common/trafficclass`；本轮本机测量见 `raw/metrics/`。
> 关键事实：**没有独立的 "Pressure" 或 "Recovering" 状态机**。真实实现只有三档压力事实（None/Warning/Critical）和三档准入 plan（Normal/Armed/Critical），"Recovering" 是降档前的 dwell 窗口，不是第四个状态。

## 1. 真实信号链

```text
[iOS] TASK_VM_INFO phys_footprint  ─┐
[host] os_proc_available_memory    ─┼─ service/oomkiller adaptiveTimer.poll (单 timer)
                                    │     ├─ footprintUnavailable? → Armed/hold（round 1）
                                    │     ├─ threshold: 45 / 40 / 30 MiB (50 MiB budget, 5 MiB margin)
                                    │     └─ rate prediction (growth lookahead)
                                    ▼
                          atomic Service.pressure (Uint32)
                                    │
              ┌─────────────────────┴──────────────────────┐
              ▼                                            ▼
   tun.MemoryPressure consumers                 Service.guardLevel()
   (sing-tun pools / buffers)                     │ Normal / Armed / Critical
                                                  ▼
                                   resourceguard.Guard.observe→apply
                                                  │ gate.setPlan(32/256/5s |16/128/2.5s |8/32/100ms)
                                                  ▼
                              AdmitCreation / Acquire (protocol create seams)
                                                  │
                                                  ▼
                                   真实物理创建（TCP+TLS / QUIC / SDK）
```

**存在第二个写者，但它是同一权威的一部分**：`timer.releaseMemory()` 在 Darwin 压力通知或阈值危机处置中会主动发布 `MemoryPressureCritical`（销毁期间的背压），随后下一次测量 poll 覆盖它。本轮确定性测试证明该"人造 Critical"会在一个采样周期内被真实测量纠正（`TestCriticalPublicationFromReclaimIsCorrectedByTheNextMeasuredPoll`）。

## 2. 真值表（实测 + 源码）

| 状态 | 进入条件 | 退出条件 | Admission | Queue | Wait | Idle Trim | Background Work | Active Session | 恢复条件 |
|---|---|---|---|---|---|---|---|---|---|
| **Normal** | footprint < 40 MiB；或 available 模式的阈值未触发；或从 Armed/Critical 连续低 2 s | footprint ≥ 40 MiB | limit **32**（+4 interactive reserve） | **256** | **5 s** | 按 idle 机会（footprint 回落 + 可回收量） | 不因内存压力改变 | 不触碰 | — |
| **Armed** | 40 MiB ≤ footprint < 45 MiB；或采样不可用；或 rate prediction 提前 | footprint < 40 MiB 持续 2 s → Normal；≥45 → Critical | limit **16**（+4） | **128** | **2.5 s** | 同上；TUN 池按 Warning 收缩 | 同上 | 不触碰 | 观测到低压力并持续 2 s |
| **Critical** | footprint ≥ 45 MiB；或 rate prediction 越过触发线 | footprint < 40 MiB（armed 线，不是 resume 线） | limit **8**（+4） | **32** | **100 ms** | 进入 Critical 的边沿执行 shed hooks（仅关闭**空闲** session）；OOMKiller 执行 crisis（ReleaseMemory/缓存 flush/GC） | 同上（见 §4） | shed 不关闭有流的 session | 回到 Armed 线以下 |

实测 plan（`raw/metrics/run1/level_plan.csv`）：

```text
normal   32 / 256 / 5000 ms
armed    16 / 128 / 2500 ms
critical  8 /  32 /  100 ms
```

## 3. 必须回答的问题

| 问题 | 答案 | 证据 |
|---|---|---|
| 状态来源唯一吗？ | 是。只有一个 `Service.pressure` atomic；guard 不采样，只读 `guardLevel()`；TUN 读同一个 atomic | `service/oomkiller/service.go:120-129`、`common/resourceguard/guard.go:140-154` |
| OOMKiller 和 ResourceGuard 是否双重判断？ | 不采样两次；但 build gate 有第二个 `resourceguard.Guard`，它读 owner 的 `Level()`，有自己的 1 s dwell（owner 是 2 s）。只影响 2 个 build 槽 | `box_buildgate.go:42-67`；台账 D12 记录 `Level()` 语义问题 |
| 每个状态真正改变了什么？ | Admission 并发/队列/等待、TUN 池收缩、guard shed（仅 Critical 边沿）、growth budget（256/128/0） | `guard.go:112-138`、`growth.go:160-179`、`protocol/anytls/outbound.go:116` 等 |
| 是否有 hysteresis / cooldown？ | 有：升立即、降需 2 s 连续低；idle 回收无收益时 30 s cooldown；rate window 100 ms–2 s 指数退避；Critical 不因 Stats 读取重复 shed | `guard.go:326-369`、`timer.go` idle cooldown、`rate.go` |
| shed 没进展会怎样？ | 执行器有 low-yield 判定与 30 s cooldown，且同一时刻只有一个 pending 动作；不会永动 | `timer.go` reclaim executor、`reclaim_observation.go` |
| 采样失败如何降级？ | footprint 读失败（0）→ 保持/升到 Armed；已测得 Critical 保持，不制造新 crisis；报告写 `footprintUnavailable` | 第一阶段 `timer.go`、`TestUnmeasurableFootprint*` |
| 交互类/普通类真实来源？ | route 解析的 `traffic_class`（规则/元数据）→ `trafficclass.WithClass` → `Class.IsHighPriority()` → admission 用 `ClassInteractive`（reserve 4），growth 用 reserve 16；老化 1 s 防饿死 | `route/conn.go:482,762,827`、`common/trafficclass/class.go:84-95`、`guard.go:151-176` |
| 交互类是否可能被滥用？ | 可以：配置/规则可以把任意流量标为 interactive；它只获得 reserve（4 个创建槽/16 个增长方向）并且**不豁免**容量上限；但会绕过快失败 rate window | `guard.go:308-323`（interactive 不等待 rate window） |
| 后台测速/恢复探测是否在压力态停止/降级？ | **不会**。URLTest 后台轮询不读内存压力，也不读 power governor；只有设备睡眠/屏幕状态（power governor）会影响部分后台工作。这是一个明确的下一轮问题 | `protocol/group/urltest.go` 无 pressure/power 引用；`common/power` 使用方为 box/dns/provider 等 |
| 压力解除后是否自动恢复？ | 是：低压力连续 2 s 自动降档；本轮实测 recovery_burst 200/200 成功、plan 恢复 32/256/5 s | `raw/metrics/run1/phase_summary.csv`、`guard_cycles_test.go` |
| 是否存在永远不回 Normal 的情况？ | 有三种**有界**情形：(a) 采样持续不可用 → 保持 Armed；(b) footprint 长期在 40–45 MiB → 保持 Armed（不是 Critical）；(c) 压力抖动使低压力不连续 2 s → 保持当前档。都不是"永久 Critical"，但 (a)(b) 需要设备证据确认实际发生频率 | 源码 + `TestUnmeasuredFootprintRuleIsScoped` |

## 4. 压力态下的后台工作（诚实结论）

- **DNS**：`dns/client.go` 的 optimistic refresh 使用 power governor（设备状态），**不使用内存压力**。也就是说内存 Critical 时，后台刷新仍可能发起。
- **URLTest**：后台 health round 不读压力，继续按 ticker 探测；探测会经过 `common/tls` / 协议创建 seam，因此**会被准入 gate 限流**（如果它走同一路径），但不会被"暂停"。
- **Provider/规则更新**：走 `common/buildgate`（2 个槽、8 队列、30 s 等待），其 plan 由同一 owner Level 驱动（Armed 1/4 队列、Critical 1/2），因此**会**被压力收紧。
- 这三条差异是下一轮"低优先级后台任务让路"的起点。

## 5. 本轮实测的状态切换（run1）

```text
idle            normal   goroutines 3     working set  8.9 MB
200 burst       normal   peak waiters 127  inflight 32   p99(admitted) 51.5 ms
1000 burst      normal   peak waiters 189  refused 712   working set 35.6 MB
critical burst  critical plan 8/32/100   admitted 40   refused 160   p99 16 ms
recovery burst  normal   admitted 200/200  plan 32/256/5s
final           —        inflight 0 waiters 0
```

（完整时间序列：`raw/metrics/run1/timeline.csv`；两次 run 对照：`03_MEMORY_TIMELINE.csv`。）

## 6. 状态机缺口（下一轮候选）

1. 后台测速/刷新没有内存压力降级（事实，见 §4）。
2. `Level()` 返回观察值而非已发布 plan（台账 D12）。
3. build gate 的第二个 Guard 有自己的 dwell（只影响 2 个槽）。
4. Guard 峰值 waiter 只能采样估计（无 `maxWaiters` 计数）。
