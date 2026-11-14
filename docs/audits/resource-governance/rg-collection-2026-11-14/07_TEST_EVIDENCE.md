# 07 测试证据（Test evidence）

> 全部命令在 branch HEAD 的全新 clone 中执行（本机 clone 根目录以 `<clone-root>` 表示）；原始输出在 `raw/test-logs/`。`SKIP` 记为 `NOT_RUN`，不是 PASS。

## 1. 本轮实际执行的测试

| 范围 | 命令 | 结果 | 原始输出 |
|---|---|---|---|
| 准入/压力/分类 | `go test ./common/resourceguard ./service/oomkiller ./common/trafficclass ./common/buildgate ./common/tls -count=1` | 全部 `ok` | `raw/test-logs/packages.txt` |
| 协议创建/复用 | `go test ./protocol/anytls ./protocol/hysteria2 -count=1` | 全部 `ok` | `raw/test-logs/packages.txt` |
| v2rayquic 全包 | `go test -tags with_quic ./transport/v2rayquic -count=1` | `ok` 9.66 s | `raw/test-logs/interfaces.txt` |
| Mieru 全包（真实 SDK/e2e） | `go test ./protocol/mieru -count=1` | `ok` 84.0 s | `raw/test-logs/mieru.txt` |
| XHTTP/XMUX 交界面 | `go test -tags with_xhttp ./transport/v2rayxhttp -count=1` | `ok` 20.13 s | `raw/test-logs/interfaces.txt` |
| 全量 route | `go test ./route -count=1 -v` | 见下方结果 | `raw/test-logs/route_full.txt` |
| 竞态 | `go test -race ./common/resourceguard ./service/oomkiller -count=1`；`go test -race ./route -run '<admission/setup>'`；`go test -race -tags with_quic ./transport/v2rayquic -run '<admission>'` | 全部 `ok` | `raw/test-logs/race_and_sdk.txt` |
| 依赖 SDK 契约 | `go test github.com/sagernet/sing-anytls -run AdmitCreation -v`；`go test github.com/sagernet/sing-quic/{hysteria2,tuic} -run Admission -v` | 全部 PASS | `raw/test-logs/race_and_sdk.txt` |
| 关键命名用例 | 见 `raw/test-logs/packages.txt` 的 `-v` 段（reuse/creation/nested/failure、queue bound、stale poll、pressure→gate、platform scope） | 全部 PASS | `raw/test-logs/packages.txt` |

**全量 route 结果**：`1186 PASS / 0 FAIL / 446.5 s`，`ok github.com/sagernet/sing-box/route 446.472s`（`raw/test-logs/route_full.txt`）。

## 2. 对抗/压力证据（来自第二阶段 Debug，当前树未变）

| 能力 | 证据 | 当前状态 |
|---|---|---|
| 变异测试（6 项：绕过 setup gate / 删除 footprint 分支 / 绕过 v2rayquic admission / 队列上界恒假 / 删除 poll 顺序检查 / 取消不移除 waiter） | `../rg-closure-2026-11-14/raw/50_mutation_log.txt` | 全部被抓住；生产代码自那次提交后未再改动 |
| 确定性交错（Stats 快照、陈旧 poll、shed 边沿、取消离队） | `common/resourceguard/*_test.go`、`service/oomkiller/poll_ordering_test.go` | 全部 PASS（本轮 `-v` 复跑） |
| 200/1000 并发准入（进程内） | 本轮 loopback harness：`raw/metrics/run1|run2/phase_summary.csv` | 见 `03_MEMORY_TIMELINE.csv` |
| 500/2000 flow burst | **未执行**：200/1000 已覆盖队列饱和；500/2000 需要 TUN/设备或更长的 loopback 资源 | `NOT_RUN` |
| 真机 NE / Jetsam | 无设备 | `NEEDS_DEVICE` |

## 3. 测试自身审计（§6.3）

- 所有等待都有上界：`require.Eventually(..., 2s)`、`select + time.After(2s)`、harness 每个 dial 8 s ctx 超时。
- 每个新用例都有可失败断言：批次计数、inflight/waiters 归零、`planFor(level).limit` 一致性、错误分类。
- harness 触发的是真实生产路径（`common/tls` → `Guard.AdmitCreation`），不是 mock gate；本地 server 的 accept 计数证明物理连接真实存在。
- 两次 harness run 都保留，未挑选；差异（峰值 goroutine/working set）如实记录。
- `route_full` 的 `SKIP` 项（若有）在输出中可见，本报告不把 skip 当 pass。

## 4. 未运行项与原因

| 项 | 原因 | 需要什么 |
|---|---|---|
| `go test ./...` 全仓 | 时间成本高且与资源治理无关；本轮改动面已由上述包覆盖 | 完整 CI runner |
| Apple 构建 / iOS 模拟器 | 无 Apple SDK | macOS runner |
| 真机 Network Extension | 无设备 | iPhone + 签名 NE |
| Cronet 路径 | 当前 profile 不含 `with_naive_outbound`；Cronet 仅 Darwin | Apple 构建 |
| Xray 互通 | 本轮未改协议线格式 | 已有 harness（不在本轮范围） |
| 500/2000 flow 与 30–60 min soak | 本机 200/1000 已给出队列行为；长稳需 CI/设备 | 专用 runner |
