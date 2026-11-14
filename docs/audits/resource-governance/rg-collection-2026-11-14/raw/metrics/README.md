# raw/metrics — 采样环境、口径与复现

## 运行边界

- 全部数据来自**本机 loopback**：一个本地 `crypto/tls` 服务器（自签证书，`127.0.0.1`），客户端通过仓库真实的 `common/tls` dialer 建立 TCP+TLS 连接。没有连接生产 VPS、代理节点或公网压力目标。
- 每个连接都真实经过 `common/tls` 的 `handshakeWithAdmissionFor` → `Guard.AdmitCreation` **生产准入路径**，因此时间序列描述的是真实准入行为，而不是 mock。
- 一次 run 约 17–20 秒，跑两次（`run1`/`run2`），两次都保留，未挑选最好的一次。

## 文件

| 文件 | 内容 |
|---|---|
| `run1/timeline.csv`, `run2/timeline.csv` | 20 ms 采样的完整时间序列（row=sample），含阶段名、压力等级、gate plan、全部计数器、inflight/waiters、Go runtime、working set、`tcp_open_server`/`tcp_accepted_total` |
| `run1/phase_summary.csv`, `run2/...` | 每阶段：请求数、成功/拒绝/取消、全量与"仅已准入"的 P50/P95/P99、各计数器增量、阶段峰值 |
| `run1/level_plan.csv` | Normal/Armed/Critical 的实际 plan（32/256/5000、16/128/2500、8/32/100） |
| `run1/ledger_check.csv` | 结束状态的两条账本不变量（全部 true） |
| `run1/os_timeline.csv` | 进程外采样：`Get-Process` 的 HandleCount / WorkingSet64 / PrivateMemorySize64 / Threads（约 50 ms，实际间隔受 PowerShell 调用开销影响） |
| `run1/phase_errors.csv` | 未分类错误的原文（run1 为空；run2 有 6 条 `dial tcp ...: i/o timeout`，来自黑洞阶段的 TCP 连接超时） |
| `run1/environment.txt` | Go 版本、GOOS/GOARCH、CPU、guard 配置、采样间隔 |

## 指标口径（必须与报告一起读）

| 指标 | 口径 | 证据级别 |
|---|---|---|
| `level` / `plan_*` | `Guard.Stats()` 在采样时刻自报的 applied level 与 gate plan | `MEASURED`（真实代码路径） |
| `requested/admitted/queued/refused/timed_out/canceled/released` | gate 计数器原值；`queued` 是累计入队次数，不参与分区 | `MEASURED` |
| `inflight` / `peak_inflight` | gate 的当前/历史最大并发许可；`peak_inflight` 是**累计**峰值（gate 不重置） | `MEASURED` |
| `waiters` / `phase_peak_waiters` | 当前等待者 / 该阶段 20 ms 采样到的最大等待者。真实峰值可能落在两次采样之间，因此是 **下界**，标 `counted_sample` | `COUNTED (sampled)` |
| `goroutines` | `runtime.NumGoroutine()` | `MEASURED` |
| `go_heap_alloc` / `heap_inuse` / `stack_inuse` / `go_sys` | `runtime.MemStats` 字段：heap_alloc 是 alloc-space（含未清扫垃圾），heap_inuse 是 span 在用，stack_inuse 是 goroutine 栈，sys 是 runtime 向 OS 申请的总量 | `MEASURED` |
| `go_gc_pause_total_ms` | 由 `/gc/pauses:seconds` 直方图按桶中值累计，是**近似值**（直方图无逐次事件） | `ESTIMATED` |
| `working_set` | Windows `GetProcessMemoryInfo` 的 workingSetSize（`sing/common/memory.Total()`）。这是 **OS working set，不是 iOS `phys_footprint`**，不能替代 | `MEASURED (host)` |
| `available_mem` | `sing/common/memory.Available()`；`available_known=false` 时不可用。Windows 上为物理可用内存，不是 NE headroom | `MEASURED (host)` |
| `handles` | 进程外 `Get-Process.HandleCount`；不是 FD 精确列表 | `MEASURED (external)` |
| `tcp_open_server` / `tcp_accepted_total` | 本地 TLS server 的 accept 计数：真实物理 TCP 连接数 | `MEASURED`（服务器侧，非客户端 socket） |
| `queue_bytes` | **UNAVAILABLE**：准入队列的 waiter 不持有 payload buffer；gate 没有字节账本 | `UNOBSERVED` |
| `nat_entries` / `active_sessions` / `idle_sessions` | **UNAVAILABLE**：没有经过 TUN，也不存在 TUN NAT 会话；本 harness 不伪造 | `NOT_APPLICABLE`（本 harness） |
| `quic_open` / `http_transports` / `mieru_underlays` | **UNAVAILABLE**：本 harness 只驱动 TCP+TLS 创建路径 | `NOT_APPLICABLE`（本 harness） |
| `phys_footprint`（iOS） | **没有采集**。Windows 上没有该接口；本阶段不虚构 | `NEEDS_DEVICE` |

## 不能从这里得出的结论

- 不能把 `working_set` 当作 iOS NE footprint；
- 不能把本 harness 的 1000-dial 结果当作 1000 个 STUN UDP flow 的真实资源乘数（UDP NAT 的 entry/goroutine/首包不在本 harness 内）；
- 不能从两次 run 的差异得出"退化/改善"结论；差异是采样与调度噪声（见 `phase_summary` 两次对照）。

## 复现

harness 源码以文本形式保存在 `../harness/main.go.txt`（`go.mod.txt` 为模块依赖说明）。复现步骤：

1. 在一份该 branch 的干净 worktree 内创建临时目录（例如 `harness_tmp/`），把 `main.go.txt` 复制为 `harness_tmp/main.go`；
2. `go build -o <out>/harness .\harness_tmp`；
3. 运行 `harness -out <dir>`，同时可用 `Get-Process` 采样 handles；4. 运行后删除临时目录（本阶段不提交任何 Go 文件到仓库模块内，避免污染 `go build ./...`）。

harness 使用 `resourceguard.DefaultNetworkExtensionConfig()`（32/256/5 s、reserve 4、DemoteAfter 2 s）与真实 `common/tls` dialer；压力等级由 harness 直接驱动 guard 的 pressure 函数，因此 Normal/Armed/Critical 的 plan 切换是真实的 `observe→apply→gate.setPlan` 路径。
