# 阶段一报告：测试基础设施与八协议性能基线

> 对应 `01-测试基础设施与八协议性能基线.md`。基线代码、环境、命令和原始数据见
> `docs/perf/BASELINE.md`、`docs/perf/ENVIRONMENT.md` 和 `scripts/perf/`。
> 本文只写结论和证据位置，不重复表格里的数字。

## 1. 分支与动态基线

- 主仓库 `Piggy-Cat-bit-shadow/JiejieBox`。执行前 `git fetch --all --prune`，
  当时远端只剩 `origin/testing` 与 `origin/integration/0.1.45-final`，两者都指向
  `4ec0decd9f2dd7e3116ab130b3af56291f73988b`（2026-11-11T10:36:43+08:00，
  `docs(upstream): pin the final docs head and record the flake stress result`）。
- 因此选择 **`origin/integration/0.1.45-final` @ `4ec0decd9f2d`** 作为集成基线：
  它是远端最新提交，且与 `testing` 同点，不存在"更新但未合并"的分支。
- 新分支 **`work/perf-lab-2026-11-11`**，跟踪 `origin/integration/0.1.45-final`。
- 没有使用固定旧 commit，也没有沿用旧报告里的数字作为本次基线。
- **施工期间远端前进了 3 个提交**（`2528511d90a`、`40dfed27474`、`7712bbe236c`，
  均为 docs/audit 收口）。逐个文件核对：20 个文件里 **0 个 `.go`**，只有
  `docs/fork/*`、`release/dependency-final-shas.json` 和一个 CI 检查脚本。
  因此本次测量的内核代码与当前远端一致，基线仍有效。
- 未做 rebase：未知用户意图的 rebase 属于高风险操作，且内核零差异时它只会
  制造冲突。阶段二开始时若远端继续前进，应先决定是重放基线还是重新测量。

## 2. 工作区保护

- 原工作区 `C:\Deepseek\_int0145\jiejiebox` 在工作分支上、状态干净。为避免任何
  覆盖风险，仍使用 **独立 worktree** `C:\Deepseek\_perf\kernel` 施工。
- 未执行 `git reset --hard`、`git clean`、force push、rebase，未修改原工作区文件。
- 所有提交只落在新分支；阶段结束不合并回主分支。

## 3. 八协议资产与缺口

完整表见 `docs/perf/ASSET-INVENTORY.md`，摘要：

| 协议 | 原有能力 | 关键缺口 | 本次 |
|---|---|---|---|
| MASQUE / CONNECT-IP | 17 个 in-process dataplane/ingress 基准；VPS 验收脚本 | 真实 QUIC 吞吐/PPS、CPU/GiB、弱网 | `masque-connect-ip` |
| VLESS XHTTP | `test/xhttpbench` 真机 A/B | 本仓库只有 client transport；CPU 统计仅 Windows | `xhttp-h2`/`xhttp-h3`（参考 Xray 服务端） |
| REALITY / Vision | 正确性、ABI、tripwire | 无吞吐/握手数据 | `vless-reality-vision` |
| AnyTLS | 准入成本微基准 + 压力测试 | 无吞吐、无延迟、无 UoT 速率 | `anytls-tcp` |
| Naive / Cronet | 两个 Darwin-only A/B 脚本 | Linux 端到端、原生引擎资源 | `naive-h2`、`naive-h3` |
| Mieru | `scripts/mieru-bench` 完整 PowerShell 台架 | Windows/loopback only | `mieru-tcp`、`mieru-udp` |
| TrustTunnel | 无任何基准 | 吞吐、CPU、PPS、弱网 | `trusttunnel-h2`、`trusttunnel-h3` |
| AWG / WireGuard | 报文开销测量、MTU 预算 | 吞吐、PPS、CPU、弱网 | `wireguard-endpoint`、`awg-endpoint` |

复用顺序按提示执行：先保留 XHTTP/Mieru/Naive 原台架不动，再为其余协议补真实进程对。
XHTTP 本仓库无服务端 transport，因此新增 `xhttp-h2`/`xhttp-h3` 两个 **参考服务端** 单元格
（本仓库客户端 × Xray 26.9.30 服务端，二进制来源与 sha256 记在 ENVIRONMENT.md），
在 `run.json` 中标注为 reference-server，不冒充本仓库进程对。

## 4. 基础设施

- **统一运行器** `scripts/perf/cmd/perflab`（独立 Go module，仅标准库）：
  `env` / `run` / `target` / `reserve` 四个子命令；一个场景 = 一对真实
  `sing-box` 进程 + 一个受控负载 + 一个被校验的目标端。
- **结果 Schema** `scripts/perf/schema/perf-run.v1.schema.json`，Go 结构在
  `internal/schema`。关键约定：缺值写 `null` 不写 0；单位进字段名；
  `reps[]` 是原始行，`summary[]` 由它派生并带样本数、中位数、MAD。
- **Linux 采样** `internal/sampler`：`/proc/<pid>/stat` 的 utime/stime/子进程、
  `schedstat` 纳秒、`status` 的 VmRSS/VmHWM/Threads、`fd` 计数、`CLK_TCK` 解析；
  逐轮 CPU 用边界精确读数，场景总量用 `wait4` rusage，`cpu_source` 标明来源。
  100 ms 采样，尖峰可能漏采，这一点写进 `host.sample_limit_note`，不隐藏。
- **网络实验室** `scripts/perf/netlab/netlab.sh`：`unshare --user --map-root-user
  --net` 内建 **两个** network namespace，一条 veth 跨两侧，双向各自 netem/HTB，
  `--dry-run`，ns 退出即销毁，不需要 root，不触碰任何既有网卡。
- **矩阵与汇总** `run-baseline.sh`（逐场景调用、记录 JSON/日志/matrix 索引）、
  `summarize.py`（按传输语义分组生成 Markdown/JSON，不重算统计量）。
- 结果、配置、日志、证书都在 run 目录内；`run.json` 记录 commit/tags/CGO/内核/
  glibc/CPU/qdisc/接口/启动耗时，敏感值不落盘。

## 5. WSL2 / Cronet / AWG 验证

详见 `docs/perf/ENVIRONMENT.md`。结论：

- WSL2 Ubuntu 24.04.5，内核 `6.18.33.2-microsoft-standard-WSL2`，glibc 2.39，
  Go 1.25.5（与 `go.mod` 一致），16 vCPU / 7.5 GiB。
- 非特权 user+net namespace **可用**（实测 `unshare -Urn` 内 uid=0，可建 veth/netem）。
  `sudo` 需要密码，全程未用。
- netem 语义实测：单侧 `delay 20ms` → UDP echo RTT 20.4 ms；双侧 → 40.5 ms。
  因此 profile 的 RTT 按每方向一半下发。
- Cronet：`cronet-go` 的 `linux_amd64` 模块含 `libcronet.a`(60 MB)、
  `libcronet.so`(12 MB) 与 purego 绑定。**cgo 链接在本机失败**（ld 报 archive
  incompatible），**purego 构建成功**，`CGO_ENABLED=0`，运行时从可执行文件同级
  目录加载 `libcronet.so`；Naive H2/H3 因此可测。
- AWG：实现文件用 `with_awg`，注册还依赖 `with_wireguard`，两者都不在发布 tag
  集合里；lab 二进制显式加上。用户态 `system=false` 下 AWG 与普通 WG 同负载均跑通。
- 参考服务端：XHTTP 用 Xray 26.9.30 linux-64（commit `b26a91d`，zip sha256
  `f851110b…690e`），来源与校验值记在 ENVIRONMENT.md；首次运行零 payload 的原因
  是 Xray 26.x 对 VLESS 入站的私有/保留地址默认黑洞，模板加 freedom `finalRules`
  放行后才通 —— 这是产品安全默认值，不是台架 bug。
- 明确不可在本机完成：macOS/Android/iOS 真机、需要 CAP_NET_ADMIN 的 `system=true`
  TUN 路径、`perf`/`pidstat`/`qlog` 级观测。

## 6. 跑通的测试与命令

复现入口：

```bash
# 构建（lab tag 集合）
go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS),with_trusttunnel,with_awg,with_naive_outbound,with_purego" \
  -o "$HOME/perf/bin/sing-box" ./cmd/sing-box

# 单协议自检
scripts/perf/netlab/netlab.sh --profile remote -- "$PERFLAB" run \
  -scenario scripts/perf/scenarios/vless-vision-tcp -out /tmp/perf \
  -bin-lab "$HOME/perf/bin/sing-box" -pattern connect -reps 1

# 矩阵
scripts/perf/run-baseline.sh --bin "$HOME/perf/bin/sing-box" --out "$HOME/perf/baseline" \
  --profile loopback --reps 5 --streams 1,4

# XHTTP 单元格需要参考服务端；不提供时它们被记为 skipped 而不是悄悄跳过
scripts/perf/run-baseline.sh --bin "$HOME/perf/bin/sing-box" --out "$HOME/perf/baseline" \
  --xray "$HOME/perf/bin/xray" --reps 5 --streams 1,4
```

14 个场景全部跑通并产出 `run.json`（含 XHTTP H2/H3 参考服务端、MASQUE UDP、
TrustTunnel H2/H3、Mieru TCP/UDP、WG/AWG、Naive H2/H3、REALITY、AnyTLS）。
shaped 路径另有 netem 场景（`remote` 80 ms/1%、100 Mbit/s 限速、80 ms 下
0%/0.1% 丢包的 UDP 阈值组）。

## 7. 当前性能摘要

数字、误差和表格见 `docs/perf/BASELINE.md` 与 `docs/perf/matrix.md`（19 个 run
目录的原始 `run.json` 在 `docs/perf/results/`）。要点：

- **loopback 峰值不是结论，CPU/GiB 才是。** 4 流下载：VLESS Vision 1373、
  Reality 1365、AnyTLS 1198 MiB/s；TrustTunnel H2/H3 只有 138/202 MiB/s，Cronet
  Naive H2/H3 303/61 MiB/s，MASQUE 70 MiB/s。对应客户端 CPU：VLESS/AnyTLS
  2–4 s/GiB，TrustTunnel 10–13，Naive 7–24，MASQUE 37–48，用户态 WG/AWG 55–65。
- **方向不对称**：同一协议上传与下载在 loopback 上差 2 倍以上（Vision 上传
  243.7 对下载 532.5 MiB/s）；丢包环境下方向还会反转（下传因重传出现 4.1× 线速
  开销）。单方向基准只测了一半。
- **H3 不一定优于 H2**：TrustTunnel H3 119–202 vs H2 135–208；Cronet H2 90–303
  vs H3 47–70。在本机无丢包条件下，QUIC 的用户态加密与拥塞控制成本高于 H2 收益。
- **UDP-over-Stream 代价明确**（loopback）：每 GiB 需要 90–234 CPU 秒，而 MASQUE
  QUIC DATAGRAM 为 81–94；H2 承载 1200B 数据报只有 9.6 MiB/s（H3 32）。
- **限速验证**：100 Mbit/s + 20ms 档，VLESS/TrustTunnel 均落在 11.1–11.8 MiB/s
  （93–99 Mbit/s payload），wire 计数与 overhead≈1 自洽 —— 证明 shaped 链路真的生效。
- **弱网（80ms/1% 双向丢包）**：单流 TCP 被 RTO 支配，Vision 上传 1.5、下载
  0.6 MiB/s，4 流 5.6/2.6。TrustTunnel H2 上传 0.3 MiB/s，下载 1 流 3 次里 2 次
  完全无 payload、4 流 3 次里 1 次；MASQUE 上传 0.3/0.2、下载 0.1，UDP 在 1 流
  3 次里 2 次零回收、4 流全部低于 0.05 MiB/s。这些记为 `failures[]`
  （class `no-payload`），不是 0 吞吐。
- **原生依赖**：Cronet 引擎启动后首个代理请求 `first_request_ms` 已单列；其 RSS/
  线程/FD 与吞吐同记录，不能用 Go heap 解释。
- **XHTTP（参考 Xray 服务端）**：1 流上传 32.6、下载 83.8 MiB/s，4 流 125.6/213.4；
  H3 与 H2 在 1 流相同、4 流更差（169 vs 213 下载）。XHTTP 上传约为同机 VLESS+TLS
  （243.7）的 1/7，代价在 packet-up 组帧而非 TLS。
- **用户态 WG/AWG**：仅 1 流上传可稳定量化（49.2 / 42.2 MiB/s，3/3）；下载首个
  flow 分别 0.2 / 1.8 MiB/s，其余 flow 在 10 s 内握手失败；UDP 3/3 零回收。
  同进程第一条 flow 可用、后续失败，服务端能看到连接并已拨号目标 —— 记入 B7。
- **UDP 与丢包阈值**（80 ms RTT，3 轮）：0% 丢包 10.6 / 6.6 MiB/s；0.1% 降到
  0.8 / 0.9；1% 只剩万分之一量级的数据报。两种 UDP 封装同时退化，不能据此排名。

## 8. 失败与未完成

完整清单见 `docs/perf/FAILURES.md`。要点：

1. XHTTP 无本仓库服务端 transport —— 已用参考 Xray 26.9.30 服务端补出
   `xhttp-h2`/`xhttp-h3` 两个 reference-server 单元格；仍缺 in-fork 服务端与
   参考客户端 A/B（后者留给 `xnettool`）。
2. Cronet cgo 链接失败；改用 purego，cgo 路径未验证。
3. 生产二进制无 pprof 端点，`go_heap_peak_bytes` 全为 null；原生内存只能看 RSS。
4. 无 root：`system=true` 的 TUN 路径、host 级 qdisc/抓包、`perf` 计数未做。
5. 用户态 WG/AWG 只能量化 1 流上传；下载/UDP 失败已在 B7 记录证据。
6. UDP over tunnel 在 1% 均匀丢包下跌到万分之一量级（B8），0.1% 仍可用。
7. macOS/Android/iOS/Windows 真机单元格未跑。
8. 首批 shaped 数据一度无效（单 namespace 导致本地路由短路，qdisc 不生效）；
   已修复并重跑 —— 这是本次自主发现的最重要缺陷。

## 9. 是否修改生产代码

**未修改任何协议生产数据路径。** 新增内容全部在：

- `scripts/perf/`（独立 module，内核构建不依赖它，它也不依赖内核内部包）；
- `docs/perf/`（报告）。

根模块 `go.mod` / `go.sum` / `protocol/` / `transport/` / `route/` / `common/` 均无改动。
`go build ./cmd/sing-box` 与 `go vet` 仍按原 tag 集合通过。

## 10. 文件与 commit

- 分支：`work/perf-lab-2026-11-11`（基于 `4ec0decd9f2d`）
- 基础设施提交：`bf0e0bec560 perf(lab): add the stage-1 performance laboratory`
- 基线数据与报告提交：见 `git log --oneline -3`（`docs/perf/results/` 19 个 run
  目录、`docs/perf/matrix.md`、`BASELINE.md`、`STAGE-1-REPORT.md` 及采集器修正）

## 11. 第二阶段风险

1. **准入与复用耦合**：AnyTLS 的 Dial 在 Session Pool 复用前申请创建许可这一假设
   尚未用调用链证实；本阶段的吞吐数字不能替代该验证。
2. **shaped 场景的统计强度**：每格 3 轮，P95/P99 只在样本足够时输出；弱网结论需要
   更多轮次和突发丢包模型，否则容易把 RTO 抖动读成协议差异。
3. **原生依赖的可移植性**：Cronet 走 purego 是本机可行路径，不代表 macOS/移动端；
   cgo 路径的失败原因尚未定位到发布产物层面。
4. **CPU/GiB 的归属**：目标端与负载端同进程（loopback）或分进程（netns）时，
   负载端 CPU 不计入协议，但会争抢核心；4 流以上场景需确认没有生成器饱和。
5. **旧数据诱惑**：历史报告里的 XHTTP/Mieru/Cronet 数字与本次 commit、环境、
   统计口径都不同，第二阶段不得混用。

## 12. 自主发现

- **单 namespace 伪测量**：两端 veth 地址同处一个 namespace 时，内核 local 路由
  把流量短路到 loopback，`tc` 显示 qdisc 已挂但流量不过 qdisc。80 ms profile 实测
  RTT 3.6 ms、100 Mbit/s 限速实测 1.36 Gbit/s。修复为双 namespace，并把
  `network.qdiscs`/`network.topology` 写进结果，避免再次静默发生。
- **校验器成为瓶颈**：逐字节校验让负载端 CPU 超过被测进程（1 流上传：负载端
  2.32 s vs 客户端 1.27 s，2 s 窗口），吞吐被生成器限制。改为按页 memcmp 后，
  同规格 1 流上传格负载端降到 1.12 s、客户端升到 2.11 s，payload 从 322 MiB
  提升到 473 MiB（同一 1 流/2 s 规格的前后对照，不是协议对比）——说明之前的
  数字是台架上限而非内核上限。
- **计费口径错误**：上传吞吐原先按发送端计数，会把排入 socket 缓冲但未到达的字节
  算进 goodput。改为目标端到达字节后，`payload_bytes` 与 `target_bytes` 严格一致。
- **drvfs 上的 git 成本**：在本仓库执行一次 `git status` 约 9 s、`git log -1` 约
  6 s，每格重复会让固定开销超过测量本身。改为矩阵开始时解析一次并通过
  `PERF_SOURCE_JSON` 传入。
- **端口属于 namespace**：shaped 场景最初在客户端 namespace 里为服务端选端口，
  直接失败；现在通过 `perflab reserve` + `nsenter` 在服务端 namespace 内预留。
- **参考服务端的私有地址黑洞**：Xray 26.x 对来自 VLESS 入站的私有/保留目标默认
  黑洞（`blocked target ... blackholing connection`），第一版 XHTTP 单元格因此
  72 MB 全部丢弃。加 freedom `finalRules` 放行私网段后恢复。
- **同进程多 flow 的状态污染**：WG/AWG 台架里第一条 flow 正常、后续 flow 握手
  超时。为此新增 `restart_per_pattern`（每个 pattern 重建进程对），把"是协议
  限制还是台架串味"分开 —— 结果证明限制在协议端，同时避免了把前一 pattern 的
  状态算到后一 pattern 头上。
- **同 profile 名合并不同单元格**：`--rtt-ms 80 --loss-pct 0` 与 `0.1` 都写成
  `custom`，汇总时被当成同一格丢掉一半数据；netlab 现按实际参数生成
  `custom-rtt80-loss0.1` 这类标签。
