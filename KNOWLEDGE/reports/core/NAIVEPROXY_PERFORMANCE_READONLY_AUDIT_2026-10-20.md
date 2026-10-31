# NaiveProxy 性能专项：只读深度审计与原生栈实测报告（收尾定稿）

> **归档记录（2026-10-20）**：本文原为仓库外独立报告（桌面），按用户指令归档进 `KNOWLEDGE/reports/core/`。测量 harness、临时插桩与全部日志**不在仓库内**，仍在 `C:\Deepseek\_naive_audit_20261020\`（见附录 B）；因此本文里的绝对路径只在这台机器上可复现，报告结论不依赖这些路径长期存在。

- **日期**：2026-10-20
- **类型**：只读审计 + 受控本机原生实测 + 参考源码核对 + 缺陷定位（**未修改主仓任何生产代码**）
- **任务书**：`NaiveProxy_..._Agent..._2026-10-20.md`（桌面）
- **报告归属**：仓库外独立报告。主仓工作树**未被改动**；本轮全部插桩与 harness 只存在于仓库外的临时导出目录，见附录 B。

| 字段 | 值 | 复现命令 |
|---|---|---|
| 主仓 | `Piggy-Cat-bit-shadow/sing-box` | `git remote -v` |
| 分支 | `testing` | `git branch --show-current` |
| **审计基线 SHA** | **`5b46c36d3d96456caccc9cc8bdb3ebc3dfbeafb3`**（`origin/testing`，本轮 `git fetch` 时点） | `git rev-parse origin/testing` |
| 本地工作树 SHA | `b721b988fef`（落后 45 个提交，**未用于本轮任何结论**） | `git rev-parse HEAD` |
| 审计方式 | `git archive` 导出到仓库外 `C:\Deepseek\_naive_audit_20261020\tip` | 附录 A |
| cronet-go（Go pin） | `Piggy-Cat-bit-shadow/cronet-go v0.0.1-143.0.7499.109-2.0.20261020055023-fd2f25825e4f` | `go.mod` replace |
| Native Chromium（运行时） | **`154.0.8037.49`** | §1.3 |
| 工具链 | `go1.25.5 windows/amd64`，`CGO_ENABLED=0`，`with_purego`，Windows 11 x64 / 16 逻辑核 | `go version` |
| 构造 tags | `release/DEFAULT_BUILD_TAGS_WINDOWS`（含 `with_naive_outbound,with_purego`） | — |
| 服务端 tags | `release/DEFAULT_BUILD_TAGS_OTHERS` | — |
| 参考实现 | klzgrad/forwardproxy @ `c096d6a00cb28e019cc1995b04bdc6a9311d8024`（branch `naive`，2026-10-06） | 浅克隆于仓库外 |

---

## 0. 先直接回答五个问题

**Q：相比官方 NaiveProxy 与 Caddy，我们更快、更慢还是相当？**

**`NOT_COMPARABLE`。** 审计主机没有官方 naiveproxy 客户端或 Caddy/forwardproxy 可执行文件，也没有真实 WAN、macOS 或 iOS 测试环境，因此无法给出可靠的跨实现性能比较。以下三类证据可供后续复核：

1. **协议语义与参考实现逐字节一致**（padding 头、帧格式、8 帧窗口、每写 flush、错误传播），§11.2 有逐行对照。因此"服务端协议设计"这一维度**不存在**差异空间。
2. **本机端到端上限与每字节 CPU 成本**（§4、§5），作为将来与参考二进制对接时的基线。
3. **成本分解到可判定的粒度**：原生 Chromium ≈ 70%，Go 服务端 TLS+H2 ≈ 11%，盒子本身 ≈ 10%–15%，裸 I/O ≈ 15%（§5）。

**Q：如果我们慢，最可能的瓶颈在哪里？有直接证据还是假设？**

- **有直接证据**：loopback 上 Naive H2 端到端 **457.7 MiB/s（中位）/427.9（最差）**，同一进程里"盒子直连"**868.7**、裸 TCP **3285.7**。Naive 栈让每字节贵 **1.9×**（相对盒子）、**7.2×**（相对裸 TCP）。
- CPU 归因：**72.6% 的采样落在 `runtime.cgocall`（Go→Chromium 原生调用桥）**；服务端 TLS 写记录累计 **11.2%**、H2 帧缓冲 **11.7%**、`protocol/naive` 包自身 **≈1%**。**Go 侧没有热点。**
- **H3 是唯一贵得明显的路径**：同字节 **158.2 MiB/s / 17.0 CPU·s/GiB**，对 H2 的 **457.7 / 5.5**，即 **H3 慢 2.9×、每字节 CPU 3.1×**（§7）。默认关闭。
- **不能声称的部分**：真实 WAN、macOS/iOS、官方客户端对照——全部 `NOT_RUN`（§2.2、§14）。

**Q：如果我们已经很快，是否可以正式停止 Naive 性能折腾？**

**可以，建议 `KEEP / STOP`。** 依据：
1. 服务端：真实 32 MiB 传输里快路径 **523 次 / 100% 命中**、慢路径 **0**、切片写 **0**、flush = 写次数、**64 158 B/次**；该数字与 Go 基准里的 `handovers=130–137`、`largest-handover=65 278/65 506 B` 互相印证。
2. 端到端有余量：457.7 MiB/s 对 100 Mbps（11.9 MiB/s）是 **38×**，对 1 Gbps 是 **3.8×**。
3. 会话复用已由库完成（6 次 CONNECT → 1 个 H2 session），**不需要自建连接池**。
4. **唯一"该做"的不是性能**，是一个**测试缺陷**：池隔离端到端测试用一个过期的 NetLog 事件 id（249 vs 实际 267），在当前 pin 上**必红**，于是"3 pool = 3 session"这条不变量**没有被验证**（§9.2，`CONFIRMED`）。

**Q：哪些优化最值得下一轮做，哪些不值得？（按 ROI）**

| # | 事项 | 类别 | ROI | 建议 |
|---|---|---|---|---|
| 1 | 修 `TestNaiveSelfInsecureConcurrency` 的 NetLog id（从 netlog 自身 `constants` 解析） | 测试有效性 | **高**：几行代码，恢复唯一一条池隔离端到端覆盖 | **RECOMMEND** |
| 2 | 更新 `docs/naive.md:285-287` 的过期历史数字（见 §12.2） | 文档 | **高**：读者会按错数字做判断 | **RECOMMEND** |
| 3 | 把 §4/§7/§8 的矩阵写进 `docs/naive.md`（H3 倍率、engine 甜点区、窗口在低 BDP 下无效） | 文档 | 中高 | **RECOMMEND** |
| 4 | 让 `test/` 模块的 naive self-test 进 CI（macOS 已有 Cronet 静态库，self-test 自带服务端） | 基础设施 | 中 | **RECOMMEND（下一轮）** |
| 5 | 批量化 flush / 减小帧 / 改服务端写路径 | 性能 | **负**：与参考不一致，且已是 64 KiB/次 | **REJECT** |
| 6 | loopback 上调窗口、据此改默认 | 性能 | **未知且本机不可测**（BDP≈46 KiB） | **REJECT（待 WAN）** |
| 7 | 自建连接池 / session 复用层 | 性能 | **负**：库已复用 | **REJECT** |
| 8 | 为降 H3 CPU 改 quic-go/Chromium | 性能 | **极低**：默认不启用、验证困难 | **REJECT** |

**Q：本次到底进行了哪些真实测量，哪些完全没有运行？**

**15 类真实测量**（§2.1，全部可复跑，含负载哈希校验），**9 类完全未运行**（§2.2、§14）。

---

## 1. 快照与版本三件套

### 1.1 三个 SHA

| 名称 | 值 | 说明 |
|---|---|---|
| 审计基线 | `5b46c36d3d96456caccc9cc8bdb3ebc3dfbeafb3` | 比上一轮报告的施工/调试基线（`d43e03c0f`）前进 **7 个提交** |
| 本地工作树 | `b721b988fef` | 落后 45 个提交，**本轮未用** |
| 参考实现 | forwardproxy `c096d6a00cb28e019cc1995b04bdc6a9311d8024` | branch `naive` |

> 版本纪律：所有结论只对 `5b46c36d3` 成立。上一轮三份报告的数字一律视为**历史值**，本轮用实测替换或明确标注。

### 1.2 依赖 pin（本轮实读 `go.mod`）

```text
github.com/sagernet/cronet-go              => Piggy-Cat-bit-shadow/cronet-go v0.0.1-143.0.7499.109-2.0.20261020055023-fd2f25825e4f
github.com/sagernet/cronet-go/lib/*        => Piggy-Cat-bit-shadow/cronet-go/lib/* v0.0.0-20260929202119-8c68ce89873c
github.com/sagernet/sing-tun               => Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261020040525-276576ea222c
github.com/sagernet/sing                   => Piggy-Cat-bit-shadow/sing v0.9.6-0.20261019051552-baed8c3b4a34
```

**三个不同的"版本"必须分开**：① Go 模块 pseudo-version `143.0.7499.109-2.0…` 是分支语义版本串；② cronet-go 代码 HEAD `fd2f2582`；③ 打包 Native Chromium **`154.0.8037.49`**（运行时 `Engine().Version()`）。三者不能互相推断。

### 1.3 原生库可加载性（本轮验证）
把 `cronet-go/lib/windows_amd64@v0.0.0-20260929202119-8c68ce89873c`（内含 `libcronet.dll`）加入 `PATH` 后，仓库自带的真实端到端测试通过：

```text
--- PASS: TestNaiveSelf (0.90s)
[error] outbound/naive[naive-out]: open UDP connection to [2001:4860:4860::8888]:443:
        local network has no public ipv6 path: …
```

第二行是已知的 DoH bootstrap 泄漏（§Q20），与本轮性能结论无关，但证明原生库确实在跑，而不是走 stub。

### 1.4 实验配置登记（任务书 §4.1 要求的身份项）

| 项 | 本轮取值 |
|---|---|
| 客户端 OS/CPU | Windows 11 x64，16 逻辑核（RAM 未记录，本轮指标不含 RSS） |
| 客户端 backend | `with_purego`（`CGO_ENABLED=0`），Native `libcronet.dll` 为 pin 的 windows_amd64 模块 |
| 服务端 | 同一进程内的 sing-box `protocol/naive` inbound（Go），`DEFAULT_BUILD_TAGS_WINDOWS` |
| TLS / ALPN | 自签 CA（CN=`audit.example`），客户端以 CA 校验；ALPN 用库默认（H2）、H3 用 QUIC |
| 认证 | `audit` / `audit`（Naive CONNECT basic auth） |
| Padding | 默认开启（前 8 帧，参考一致） |
| QUIC | 每个 H3 形态显式 `quic: true` + 服务端 `network: udp`；H2 形态 `quic: false` |
| NIK / 连接池 | 默认；多 engine 形态用 `insecure_concurrency` 2/4/8，另有 `single_engine: true` 对照 |
| ECH | 关闭（本轮不测 ECH 对吞吐的影响） |
| 代理链 / 转发层 | 无：`mixed` 入站 → `naive` 出站 → `naive` 入站 → 同进程 TCP 源站 |
| 实验网络 | **loopback only**；无 NAT、无中间设备、无 DNS 解析（目标为 `127.0.0.1` 字面量） |
| 官方 NaiveProxy / Caddy | **未获取**（无二进制、无版本、无构建选项）→ 任何对照都 `NOT_COMPARABLE` |
| Caddy 参考版本 | 仅记录：仓库自测 harness 声明 Caddy v2.10.0，而 forwardproxy `c096d6a00` 的 `go.mod` 写 v2.8.4（上一份审计已记录此不一致），本轮未裁定 |

---

## 2. 实测 / 未测分界

### 2.1 本轮真实测量（全部 `MEASURED_LOCAL`，Windows 11 / 16 逻辑核 / loopback）

| # | 测量 | 手段 | 关键结果位置 |
|---|---|---|---|
| M1 | 端到端吞吐矩阵（**11 个形态**：9 个 naive 形态 + `BOX-direct` + `DIRECT-tcp`，每形态 1 冷 + 5 测量轮） | 仓库外 harness：真实 Cronet 客户端 + 真实 Go 服务端 | §4 |
| M2 | 每 GiB 合并 CPU（客户端+服务端同进程） | `GetProcessTimes` user+kernel 前后差 | §4 |
| M3 | **负载完整性**（H2/H3 各 32 MiB 的 FNV-1a 对比） | 独立完整性用例 | §4.2 |
| M4 | **短请求负载**（100 × 64 KiB 顺序请求） | 独立 short-request 用例 | §4.3 |
| M5 | 服务端写路径命中率与粒度 | 临时插桩计数器（6 处 atomic） | §6 |
| M6 | CPU 归因 | `-cpuprofile` + `pprof -top/-peek` | §5 |
| M7 | 盒子冷启动（含 engine 初始化） | `box.New`+`Start` 计时 | §8.3 |
| M8 | H2 会话复用（6 次顺序 CONNECT） | NetLog + 从 `constants` 解析事件 id | §9.1 |
| M9 | 池隔离行为（`insecure_concurrency:3` → 3 session） | 仓库自测生成的 NetLog，按真实 id 267 计数 | §9.2 |
| M10 | H3→H2 回退端到端（TCP-only 服务端） | 同 harness，冷/热分离 | §10 |
| M11 | H3 vs H2 吞吐/CPU | 同 harness | §7 |
| M12 | Go codec / H2 bulk 基准（**本轮重跑**） | `go test -bench`（server profile），`-count=3` | §3.2 |
| M13 | 参考实现 padding/帧/flush 源码核对 | 浅克隆 forwardproxy 逐行比对 | §11.2 |
| M14 | 文档与测量脚本的有效性审查 | 读 `docs/naive.md`、两个 `scripts/ci/bench-naive-*.sh` | §12.2、§12.3 |
| M15 | 原生桥机制（Q09） | 读 cronet-go `bidirectional_conn.go` | §5.3 |

### 2.2 完全未运行（`NOT_RUN` / `NOT_COMPARABLE`，不折算、不推断）

| # | 项 | 原因 | 证据等级 |
|---|---|---|---|
| N1 | 官方 naiveproxy 客户端 / Caddy 服务端真实对照（含 §4.2 的 2×2） | 本机无参考二进制，需独立环境 + WAN | `NOT_COMPARABLE` |
| N2 | 真实 WAN（RTT/丢包/带宽/抖动）下 H2 vs H3 | 只有 loopback | `NOT_RUN` |
| N3 | macOS / iOS 的 Native footprint、线程、初始化、多 engine A/B | 无该平台设备 | `NOT_RUN` |
| N4 | H2 窗口 sweep 的**工程结论**（4/8/…/128 MiB） | loopback BDP≈46 KiB，实验不可能出结论（§8.2 给定量） | `NOT_RUN` |
| N5 | Alt-Svc / 0-RTT / 连接迁移 | 需真实网络中间设备 | `NOT_RUN` |
| N6 | 低端 Android/iOS 的 CPU/电量 | 无设备 | `NOT_RUN` |
| N7 | 小时级 soak 的内存/句柄曲线 | 本轮只做分钟级 | `NOT_RUN` |
| N8 | 流媒体 / 上下行非对称 / UoT 负载形态 | 需要相应流量与目标（Q14 只覆盖 bulk + 短请求） | `NOT_RUN` |
| N9 | 跨 `client → server → target` 三段的独立计时 | 双端同进程，无法分离（Q16 只做到"盒子 vs naive"） | `NOT_RUN` |

> 没有把 loopback MB/s 当公网吞吐，没有把 codec ns/op 折算成用户速度，没有把历史数字当本轮结果。

---

## 3. 既有成果是否真的在生产链路上

### 3.1 端到端计数器（真实 32 MiB 传输）

| 既有优化 | 声称 | 本轮实测 | 判定 |
|---|---|---|---|
| Buffer headroom / MTU 几何（`writerMTU=65278`、front 3 / rear 255） | 一次 copy 成一帧 | **每写 64 158 B**，与 `3+65278+255=65536` 一致 | **生效** |
| `EarlyCopyBufferGrowth` | 尽早升到 64 KiB | 32 MiB 只有 **523 次写**（若 32 KiB/写应约 1 046 次） | **生效** |
| in-place framing | 快路径 0 alloc | **100%** `writeBufferWithPadding`；`writeChunked` **0**；切片写 **0** | **生效** |
| 每写 flush + 错误传播 | 与参考一致 | **523 flush / 523 写** | **生效** |
| cached-first-payload | 少一次明文拷贝 | 由 §3.2 的基准佐证（14 次 handover 差异、`small-handovers 16→2`） | 结构生效（未单独插桩） |

### 3.2 Go codec / H2 bulk 基准（本轮在 `5b46c36d3` 重跑，server profile，`-count=3`）

| 基准 | ns/op | B/op | allocs/op | 备注 |
|---|---|---|---|---|
| `NaiveH2BulkTransfer/default-growth` | 2 007–2 016 k | 9 195–9 280 | 143 | 4 160–4 179 MB/s（in-process H2） |
| `NaiveH2BulkTransfer/early-growth` | 2 011–2 022 k | 8 711–8 859 | **136** | 与默认几乎同速，少 7 次分配 |
| `NaiveH2BulkTransferBuffers/default` | — | — | 143 | **handovers=137**，largest **65 278 B**，small **16** |
| `NaiveH2BulkTransferBuffers/early` | — | — | **136** | **handovers=130**，largest **65 482–65 531 B**，small **2** |
| `PaddingWriteBuffer/{64B,1400B,16KiB}` | 176–181 / 176–178 / 165–170 | 0 | **0** | 与上一轮一致 |
| `PaddingWriteBufferFramed/{64B,1400B,16KiB}` | 256–262 / 256–261 / 246–248 | 0 | **0** | in-place framing |
| `PaddingWriteChunked` | 5 066–5 096 | 298–339 | **4** | 慢路径；真实传输中调用 0 次 |
| `PaddingWriteFramed`（分配路径） | 597–685 | 65–66 | 1 | 只被 `writeChunked` 使用 |
| `PaddingWriteUnpadded` | 18.2–19.0 | 0 | 0 | |
| `HeadroomDecisions` | 0.30–0.65 | 0 | 0 | 决策本身免费 |
| `CachedFirstPayload/buffer/{64B,1400B,16KiB}` | 282–290 / 276–291 / 271–278 | 0 | **0** | |
| `CachedFirstPayload/copying/{64B,1400B,16KiB}` | 612–637 / 614–626 / 868–943 | 64–80 | 1 | 差 2 倍以上 |

**读法**：这些是 **Go 层 CPU**，writer 是内存 sink，**不是**网络吞吐。真正值得记的是：快路径 0 alloc，慢路径 4 alloc，且慢路径在真实 bulk 中**一次都没被调用**（§6）。

**重要对照**：H2 bulk 基准的 in-process 上限是 **4.16 GB/s**，而真实 loopback 端到端只有 **457.7 MiB/s**（约 0.46 GB/s）。也就是说 **Go codec 快 9 倍于端到端**——端到端慢的不是 codec。

---

## 4. 端到端吞吐与分层（本轮核心数据）

**方法**：一个进程内 1 个 Box（`mixed` 入站 + `naive` 出站 + `naive` 入站），源站是同进程 TCP 服务器；客户端经 SOCKS5 读满 32 MiB 计墙钟。每形态 **1 次冷连接 + 5 轮测量**（满足任务书 "≥5 轮"），取中位数并给 min/p95/max。**CPU 为同进程客户端+服务端合并值。loopback，不是 WAN。** 所有计时轮**零失败**：harness 对任何 dial/读取错误直接 `require` 失败，因此不存在"把 0 字节算作成功吞吐"的样本。

### 4.1 矩阵

| 形态 | 流 | 中位 (MiB/s) | min | p95 | max | CPU (s/GiB) | 冷连 dial (ms) |
|---|---|---|---|---|---|---|---|
| `DIRECT-tcp`（无盒） | 1 | **3285.7** | — | — | — | **0.5** | 1.05 |
| `BOX-direct`（盒，无 naive） | 1 | **868.7** | 765.6 | 1716.8 | 1716.8 | **2.0** | 1.04 |
| **`H2-1`** | 1 | **457.7** | 427.9 | 460.8 | 460.8 | **5.5** | 4.18 |
| `H2-2engines-2streams` | 2 | **665.2** | 642.4 | 692.0 | 692.0 | 8.5 | 9.13 |
| `H2-4engines-4streams` | 4 | 568.4 | 566.4 | 581.6 | 581.6 | 15.25 | 4.18 |
| `H2-8engines-8streams` | 8 | 537.5 | 505.8 | 557.3 | 557.3 | 20.375 | 5.25 |
| `H2-1engine-4streams` | 4 | 465.1 | 462.5 | 471.6 | 471.6 | **7.25** | 5.43 |
| `H2-window4MiB` | 1 | 452.9 | 279.9 | 483.2 | 483.2 | 7.0 | 2.66 |
| `H3-1` | 1 | **158.2** | 156.6 | 159.0 | 159.0 | **17.0** | 5.04 |
| `H3-fallback-on`（H3 可用） | 1 | 156.8 | 154.6 | 158.1 | 158.1 | 17.5 | 4.27 |
| `H3-fallback-tcp-only` | 1 | **477.9** | 452.4 | 490.9 | 490.9 | 5.5 | **4014.02** |

**读法**

1. **绝对吞吐只在 loopback 有意义**：它衡量栈的上限与每字节成本，不是公网速率。
2. **分层**：裸 TCP 3285.7 → 加盒子 868.7 → 再加 naive H2 457.7。**Naive 每字节贵 1.9×（相对盒子）、7.2×（相对裸 TCP）**。
3. **对真实链路余量**：457.7 MiB/s 对 100 Mbps 是 **38×**、对 1 Gbps 是 **3.8×**。折算 CPU：H2 的 5.5 s/GiB 在 11.9 MiB/s 下约 **0.064 核**；H3 的 17 s/GiB 约 **0.20 核**。
4. **`BOX-direct` 的方差很大**（765.6–1716.8，p95 = max = 1716.8）：这是五次里有一轮跑出 1716 的结果，说明盒子的纯转发路径在本机受调度/缓存影响明显。该行只用于数量级"盒子的成本远小于 naive"，不用于精确比率。
5. **`H2-window4MiB` 有一个 279.9 的低值**（min），中位与默认窗口相同；单点低值来自系统噪声，不是窗口效应（§8.2）。

### 4.2 负载完整性（M3）

| 形态 | 字节 | 期望 FNV-1a | 实际 | 匹配 |
|---|---|---|---|---|
| INTEGRITY-H2 | 33 554 432 | `7ee066eccd52d325` | `7ee066eccd52d325` | ✅ |
| INTEGRITY-H3 | 33 554 432 | `7ee066eccd52d325` | `7ee066eccd52d325` | ✅ |

这条用例存在的意义：**排除"快是因为流被截断"**。所有计时轮跑的都是同一段源站数据。

### 4.3 短请求负载（M4，Q14）

100 次顺序请求，每次 64 KiB（每次都新建 CONNECT，走完整 tunnel 建立）：

| 指标 | 值 |
|---|---|
| 冷请求 | **5.46 ms** |
| p50 | **1.02 ms** |
| p95 | **1.69 ms** |
| p99 | **7.96 ms** |
| max | 9.11 ms |
| 100 次总墙钟 | 0.123 s |
| 速率 | **814 requests/s** |

**解读**：短请求形状的成本几乎全是"CONNECT 200 + 一次往返"，与 64 KiB 的搬运无关；p99 的 7.96 ms 是尾部（GC/调度）。**这说明 P1 的 CONNECT 握手在 loopback 上是 1 ms 级**，与上一轮 +0.519 ms 的量级一致。真实 RTT 下这部分会线性增加（§Q17，`NOT_RUN`）。

---

## 5. CPU 归因：Go 侧不是热点

### 5.1 pprof（`-cpuprofile`，H2/H3 各一次，方法同 §4）

| 采样项（H2） | flat% | 说明 |
|---|---|---|
| `runtime.cgocall`（caller 全是 `runtime.syscall_syscalln`） | **72.6%** | Go→原生 DLL 调用桥；**Chromium 内部耗时在 Go profile 里不可见** |
| `crypto/tls.(*Conn).writeRecordLocked`（cum） | 11.2% | 服务端 TLS 写记录 |
| `x/net/http2.(*bufferedWriter).Write`（cum） | 11.7% | 服务端 H2 帧缓冲 |
| `internal/poll.(*FD).Write`（cum） | 14.0% | 系统调用写 |
| `runtime.memmove` | 2.8% | 拷贝 |
| `protocol/naive` 包（全部函数 cum） | **≈1%** | 我们自己的 padding/帧代码 |

H3：`runtime.cgocall` 71.6%；quic-go 的 packer/framer/stream 累计约 **5%–15%**（`appendPacket` 4.6%、`composeNextPacket` 3.2%、`maybeGetShortHeaderPacket` 3.4%、`getDataForWriting` 1.5%）；TLS 1.3 的 `bigmod` 系列约 9–10%，与 H2 相当——**H3 的额外开销主要来自每包封装/ACK 调度，不是密钥运算**。

### 5.2 三条纪律

1. **72% 是"看不见"**，不等于"原生没有热点"。Go profile 只能证明 **Go 侧没有热点**。
2. pprof 的绝对秒数不可当 CPU 总量（采样口径与 `GetProcessTimes` 不同）；本报告只用它做**相对归因**。
3. 端到端慢 9 倍于 codec 基准（§3.2 vs §4）：把时间花在 Go codec 上没有意义。

### 5.3 原生桥的机制成本（Q09，`VERIFIED_SOURCE`）

`cronet-go/bidirectional_conn.go`：每条 tunnel 有 **1 个 mutex + 10 个 channel**（`close/done/ready/handshake/read/write/readSemaphore/writeSemaphore/readDone/writeDone`）+ **2 个 `pinnedBuffer`**（各自 mutex + `runtime.Pinner`）。读/写各自 pin 一次、由完成回调（在 **engine network thread** 上）unpin，`release` 幂等且与 `pin` 用锁互斥（注释明确指出两者来自不同 goroutine）。

**推导上界**：457.7 MiB/s ÷ 64 KiB ≈ **7 300 次写/秒**；每次写涉及少量 channel 操作 + 1 次 pin/unpin。即使每次 2 µs，也只有 **1.5%** 的 CPU。要让这层成为瓶颈，单次固定开销需 > 100 µs —— 比本机一次 channel 往返高两个数量级。**因此"回调/锁/pin 是吞吐下降主因"不成立**；但这是**推导**，不是逐函数测量（未单独 benchmark，§14 N9）。

---

## 6. 服务端写路径：Q12 / Q13 的定量答案

临时插桩（仅存在于仓库外副本）在**真实 32 MiB 传输**中的计数：

| 计数器 | 每次 32 MiB | 推论 |
|---|---|---|
| `writeBufferWithPadding`（快路径） | **523** | 所有字节走 in-place framing |
| `writeChunked`（慢路径） | **0** | 未触发帧过大/headroom 不足 |
| 切片 `Write`（两条实现） | **0** | copy 循环只走 `WriteBuffer` |
| `flush` | **523** | flush : write = **1:1** |
| 每次写/每次 flush 字节 | **64 158** | ≈ 62.7 KiB，接近 65 536 整帧 |

**Q12**：**是"每写必 flush"，但写粒度已是 64 KiB**，所以 flush 频率是"每 64 KiB 一次"，不是小帧风暴。参考实现 **forwardproxy 同样每写后 `Flush()`**（`forwardproxy.go:741-747`，已逐行核对），**所以减少 flush 不是优化，是破坏参考行为**：它会推迟用户可见字节，在 WAN 上直接表现为 TTFB/交互延迟变差，且丢失错误传播（我们靠 flush 错误判定流已死）。

**Q13**：**快路径命中率 100%**，`writeChunked` 在真实 bulk 中 **0 次**。任务书的判断被数据确认："如果真实 bulk 不走慢路径，继续优化 `writeChunked` 就不是主方向"。

> 反向验证：若 `EarlyCopyBufferGrowth` 没生效，一次 32 MiB 会看到约 1 046 次 32 KiB 写。实测 523 次，正好一半。

---

## 7. H2 vs H3：本机差 2.9×，默认关闭是对的

| 指标 | H2 | H3 | 倍率 |
|---|---|---|---|
| 吞吐（单流，中位） | **457.7 MiB/s** | **158.2 MiB/s** | H3 慢 **2.9×** |
| 每 GiB CPU（合并） | **5.5 s** | **17.0 s** | H3 贵 **3.1×** |
| 冷连（loopback，含 CONNECT） | 4.18 ms（首次）/ 1 ms 级（热） | 5.04 ms（首次） | 同量级 |
| Go 侧 quic-go 可见占比 | — | 5%–15% | — |
| 原生桥占比 | 72.6% | 71.6% | 同结构 |

**边界**：loopback 上 H3 无收益；**但 H3 的价值在丢包/弱网/移动切换**，本报告**没有**测（§14 N2），因此**不能**推出"应该删掉 QUIC 支持"。结论只到"默认关闭正确，且启用 H3 时每字节 CPU ≈3 倍"。

---

## 8. Engine 数量、冷启动与窗口

### 8.1 负载下的 engine 数量（Q06，本轮新数据）

| 形态 | 流 | 中位 (MiB/s) | CPU (s/GiB) | 相对单流 |
|---|---|---|---|---|
| 1 engine（`H2-1`） | 1 | 457.7 | **5.5** | 1.00× |
| 1 engine + 4 NIK（`single_engine`） | 4 | 465.1 | **7.25** | 1.02× |
| **2 engines** | 2 | **665.2** | 8.5 | **1.45×** |
| 4 engines | 4 | 568.4 | 15.25 | 1.24× |
| 8 engines | 8 | 537.5 | 20.375 | 1.17× |

**结论（与前一轮"多 engine 更快"的说法相比更精确）**：
- **2 个 engine 是本机甜点**：吞吐 665 MiB/s，是单 engine 单流的 1.45×，CPU 只升到 8.5 s/GiB。
- **超过 2 个 engine 后吞吐下降**（4→568、8→537），而 CPU 线性上升（15.25、20.375）。原因是同进程里 Chromium 网络线程与本机 16 核调度竞争，新增 engine 不再带来并行收益。
- **单 engine + 4 NIK** 的每字节效率最好（7.25 s/GiB），但 4 流吞吐只有 465（近似单流上限）——它省 CPU，不扩吞吐。
- **默认（未设 `insecure_concurrency`）是 1 engine**，本表不改变默认；它只说明**用户显式设 >2 时，收益会反转**。
- **macOS/iOS 的结论仍未测**（§14 N3），本表不可外推。

### 8.2 接收窗口（Q08）：本轮只得到负对照

- **定量 BDP**：loopback RTT ≈ 0.05–0.1 ms，457 MiB/s × 0.1 ms ≈ **0.045 MiB（46 KiB）**。任何 ≥1 MiB 的窗口都是 BDP 的 20 倍以上，**不可能成为限制**。
- 实测：`stream_receive_window = 4 MiB` 中位 **452.9**（min 279.9、p95 483.2）vs 默认（128 MiB）**457.7**（min 427.9、p95 460.8）。中位与 p95 差异在噪声内；4 MiB 行的低 min 是单轮系统噪声。
- 所以本轮**只证明"BDP≈0 时窗口无关"**，**不能**回答 100 ms × 100 Mbps（BDP≈1.2 MiB）或 200 ms × 500 Mbps（BDP≈12 MiB）时窗口是否够。需要 `bench-naive-receive-window.sh`（真实服务端 + 高 BDP），`NOT_RUN`。
- **严禁把窗口大小当 RSS**：`docs/naive.md` 与脚本注释都写明了这一点，本轮支持该表述。

### 8.3 冷启动（Q07 的一部分）

`box.New + Start`（含 engine 初始化、TLS、监听）在本机 **34.3–46.1 ms**（10 个形态）。4 engine（40.7）与 1 engine（40.2）**无差异**——engine 初始化是异步/懒加载；真正的差异在首个 CONNECT 的冷 dial（1–9 ms，见 §4 表末列）。

### 8.4 多个 Outbound 共享 Native library 时共享什么（Q07 的一半）

- **代码段共享**：`libcronet.dll` 在进程内只映射一次（上一轮空闲态实测 4 engine 仅 +0.5 MiB）。
- **连接资源不共享**：每个 engine 有自己的 session pool；`insecure_concurrency` 语义就是"几套独立网络栈"。
- 本轮未重新测 idle footprint（上一轮已有），也未测"多 Outbound 引用同一 engine"（生产上每个 outbound 自己 `Start` 一个 `NaiveClient`，没有共享入口）→ 标为 `NOT_RUN`（§14 N3）。

---

## 9. 会话复用与仓库测试缺陷

### 9.1 会话复用（Q10）

`insecure_concurrency` 默认（1 engine / 1 pool）下，**6 次顺序 CONNECT → 1 个 H2 session**。

```text
AUDIT_REUSE cfg=H2 connects=6 session_type_id=267 http2_sessions=1
            netlog_bytes=142554043 verdict=ONE_SESSION_FOR_SIX_CONNECTS
```

事件 id **267** 是从 netlog 自身 `constants` 块解析的（不是硬编码）。**结论：库已复用连接，不需要我们自建池。**

### 9.2 `CONFIRMED` 缺陷：池隔离端到端测试用错 NetLog 事件 id

**现象**（本机实跑）：

```text
$ go test -tags <windows> -run TestNaiveSelfInsecureConcurrency -v .
Error: "0" is not greater than or equal to "3"
Messages: Expected at least 3 HTTP/2 sessions with insecure_concurrency=3.
          NetLog: /tmp/naive_concurrency_netlog.json
--- FAIL: TestNaiveSelfInsecureConcurrency
```

**根因（定位到行）**：`test/naive_self_test.go:336-338`

```go
// Count HTTP2_SESSION_INITIALIZED events to verify connection pool isolation
// NetLog stores event types as numeric IDs, HTTP2_SESSION_INITIALIZED = 249
sessionCount := strings.Count(logStr, `"type":249`)
require.GreaterOrEqual(t, sessionCount, 3, …)
```

当前 pin 的 Chromium（154.0.8037.49）里该事件的 id 是 **267**（从测试自己生成的 netlog 的 `constants` 读出）。

**证据：行为正确，只有断言错**（同一份 netlog，325 MB）：

```text
constants id = 267
count("type":267) = 3     ← 真的 3 个独立 session，池隔离成立
count("type":249) = 0     ← 测试在数不存在的 id
```

**影响**：该测试在任何使用当前 pin 的环境**必红**；若被 skip 或未运行，则"`insecure_concurrency:3` → 3 个独立 session"**实际上没被验证**。

**为什么没被 CI 发现（源码证据）**：
- `test/` 是**独立 module**（`test/go.mod`，`replace sing-box => ../`），根模块 `go test ./...` 不包含它；
- `client-macos.yml:181-184` 的测试列表是 `./option ./transport/... ./protocol/... ./route ./adapter/... ./dns/...`，**不含 `test` 主包**；
- `server-linux-amd64.yml:255-256` 跑的是 `cd test && go test ./contract/server/`，不是 naive self-test；
- 因此 `TestNaiveSelf*` 只在开发者本机手动运行。

**最小修复方向（本轮不施工）**：从 netlog 自身 `constants` 解析 id；解析失败时打印头部片段并 `Fatal`，让"Chromium 又改枚举"以可读方式失败，而不是数出 0。

**附带发现**：为计数 6 次 CONNECT 打开 `StartNetLogToFile(..., true)` 生成了 **142–325 MB** 的 netlog。若只数 session，用 `false`（默认事件集）即可。

---

## 10. H3→H2 回退的端到端代价（Q18/Q19）

| 场景 | 冷（首个 CONNECT） | 30 s 窗口内后续 |
|---|---|---|
| 服务端 **TCP-only**，`quic:true` + `fallback:true` | **dial 4 014.0 ms**，wall 4 095 ms，随后成功 | **dial ≈1 ms，中位 477.9 MiB/s**（= H2 性能，与纯 H2 的 457.7 同档） |
| 服务端 TCP+UDP（H3 正常），`fallback:true` | dial 1–5 ms | 中位 **156.8 MiB/s**（= 纯 H3 的 158.2） |

**结论**：
1. 冷代价就是 Chromium 的 H3 连接预算（~4.0 s），与上一份审计的 4.018/4.009 s 独立吻合；**不是我们引入的等待**。
2. **窗口内后续零额外代价**，直接走 H2 且吞吐与纯 H2 相同。
3. **H3 正常时回退分支无可测开销**（156.8 vs 158.2，差 0.9%，在噪声内）。
4. 因此该特性在性能上是"**把 4 s 失败换成 4 s 冷连 + 立即可用**"；默认关闭仍是合理默认。

---

## 11. 与参考实现对照（Q01–Q04）

### 11.1 能回答与不能回答

| 问题 | 本轮回答 | 依据 |
|---|---|---|
| Q01 官方客户端 vs 我们 | **`NOT_COMPARABLE`** | 无参考二进制、无 WAN、无 macOS |
| Q02 Caddy/forwardproxy vs 我们 | **性能 `NOT_COMPARABLE`**；**协议语义逐字节一致** | §11.2 |
| Q03 2×2 矩阵 | **`NOT_RUN`**（缺少一方就凑不出矩阵；任务书允许标注） | §14 N1 |
| Q04 历史 issue"原生快、sing-box 慢" | **未复现也未反证**：无法搭参考客户端。但分层数据显示 Go 服务端只占 ~11%，若该现象存在，更可能在客户端/网络/入口，而非 Go 服务端 | §5、§6 |

### 11.2 协议语义的独立核对（本轮逐行做过）

对照 `klzgrad/forwardproxy @ c096d6a00`（branch `naive`）与本仓 `protocol/naive/inbound_conn.go`：

| 项 | 参考实现 | 本仓 | 判定 |
|---|---|---|---|
| Padding 头长度 | `rand.Intn(32) + 30`（L309） | `rand.Intn(32) + 30`（L33） | 一致 |
| Padding 头字符集 | 前 16 字节取 `"!#$()+<>?@[]^\`{}"`（4 bit 索引），其余 `'~'`（L310-320） | 同（L36-42） | **逐字节一致** |
| 每帧 padding | `rand.Intn(256)`，`maxRead = 65536-3-paddingSize`（L705-706） | 同（L306-307） | 一致 |
| 帧头 | 2 字节大端长度 + 1 字节 padding 长度（L711-713） | 同（`ExtendHeader(3)` + `PutUint16`） | 一致 |
| padding 内容 | 全零（L714-716） | `WriteZeroN` | 一致 |
| 前 N 帧带 padding | `NumFirstPaddings = 8`（L658） | `paddingCount = 8`（L21） | 一致 |
| 每写 flush | `rc.Flush()`，失败即返回（L741-747） | `WriteBuffer` → `flush()` 并传播错误（L473-483） | 一致 |

**意义**：在"服务端协议行为"上，我们与参考是同一份字节流，**不存在"我们多一次拷贝/多一帧"的空间**；任何此类改动都会破坏互通。Q02 的性能差异只能来自实现语言/运行时，而不是协议设计。

### 11.3 不应盲目复制的参考取舍

- forwardproxy 每写必 Flush（与我们一致，且是**延迟优先**的选择）。
- forwardproxy padding 只在前 8 帧（与我们一致，对长传输影响可忽略）。
- 官方**客户端**的 padding 参数与本仓**客户端**不同（16..32 + 另一套字符集，上一轮报告 §5.5）；本轮未重读该处，仅作提示。

### 11.4 证据等级声明

上一轮报告的历史数字本轮只作线索；唯一被独立复现的是 ~4.0 s 的 H3 预算（本轮 4 014 ms）。naiveproxy#756 本轮**未重新抓取**（GitHub issue 页面在本环境不可直接抓取），仅作二手线索。

---

## 12. 文档与测量脚本审查（M14，任务书"先审阅脚本有效性"）

### 12.1 两个 bench 脚本：方法学合格，本机不可运行

| 脚本 | 方法学 | 本机可运行性 |
|---|---|---|
| `scripts/ci/bench-naive-cronet-engine.sh` | 校验 `200 ≤ http_code < 300`、失败流单独计数不计 0 字节、并行 goodput 用 `max(time_total)` 作除数（注释明确说"不是求和"）、RSS/`footprint`/`lsof`/线程/FD/启动/首请求/1-4-8-16 流 | **否**：bash + macOS `footprint`/`lsof`；需要真实 naive 服务端 |
| `scripts/ci/bench-naive-receive-window.sh` | 无服务端时直接输出 `NOT TESTED` 而不是假结果；逐窗口值重建客户端；同样的状态码/失败纪律；明确要求高 BDP 路径 | **否**：同上 |

**结论**：脚本可以信任其方法学（尤其"失败不算 0 字节"和"max 作除数"两点）；但**本轮没有运行它们**，因此 macOS 的多 engine / 窗口结论仍是 `NOT_RUN`。

### 12.2 `docs/naive.md:285-287` 的历史数字已过期（`CONFIRMED`，文档）

同段的历史值与本轮实测（`CachedFirstPayload/buffer`）差距明显：

| 尺寸 | 文档值 | 本轮实测 | 差异 |
|---|---|---|---|
| 64 B | 214 ns | 282–290 ns | **+32%** |
| 1400 B | 216 ns | 276–291 ns | **+28%** |
| 16 KiB | 215 ns | 271–278 ns | **+26%** |

文档已标注"historical… not a remote throughput claim"，方向正确；但读者容易把 214 ns 当成现状。**建议更新为当前值，或补上测量日期与 SHA。**

> **一处自我纠正（写在这里，避免带着错误结论被采纳）**：本轮起初怀疑这一段还有"箭头编码损坏"（`→` 显示成 `鈫?`），并在草稿里记为第二个文档缺陷。用 UTF-8 感知的方式重读文件后发现**文件本身是干净的**——那只是控制台按非 UTF-8 代码页渲染 `Get-Content` 输出造成的**显示假象**。因此该"缺陷"被撤销；同类的 `KNOWLEDGE/reports/README.md` 也有相同假象，同样不是文件问题。**结论：不要用控制台输出判断编码，要用能识别 UTF-8 的读取方式。**

### 12.3 文档与本轮实测一致的部分

- `docs/naive.md:315-324` 的 Known limits 明确写"没有与 Caddy 的性能对照"、"单/多 engine、窗口 sweep、拥塞控制都需要真实网络 A/B 才能改默认"——与本报告结论一致。
- `docs/naive.md:274-283`（Cronet async buffer ownership）与 `bidirectional_conn.go` 的 pin/回调实现一致（§5.3）。
- `docs/naive.md:289-299`（H3 无 0-RTT、path manager 默认开、拥塞控制只是选项）与源码一致。

---

## 13. 问题与风险清单

| # | 等级 | 内容 | 证据 | 影响 |
|---|---|---|---|---|
| **A1** | `CONFIRMED`（测试） | `TestNaiveSelfInsecureConcurrency` 用过期 NetLog id（249 vs 267）→ 必红，池隔离不变量实际未验证 | 本机失败输出 + netlog 计数 3 vs 0 | 覆盖缺口 |
| **A2** | `CONFIRMED`（基础设施） | `test/` 是独立 module，`TestNaiveSelf*` 不在任何 workflow 执行列表 | `test/go.mod`、`client-macos.yml:181-184`、`server-linux-amd64.yml:255-256` | A1 潜伏的原因 |
| **A3** | `CONFIRMED`（文档） | `docs/naive.md:285-287` 的历史数字比现状乐观 26%–32% | §12.2 | 误导读者（"乱码"一项已自我纠正为控制台显示假象） |
| **A4** | `OBSERVATION` | H3 的每 GiB CPU ≈ H2 的 3.1×（桌面）；移动端未测 | §7 | 默认关闭合理；文档应写明 |
| **A5** | `OBSERVATION` | engine 甜点在 2：2 engine 665 MiB/s；4/8 engine 反而 568/537，CPU 线性上升 | §8.1 | 用户显式设 >2 时收益反转 |
| **A6** | `OBSERVATION` | 强制 H3 打不通的服务端时，冷连固定 ~4.0 s；fallback 打开后仅冷连 | §10 | 已接受的设计 |
| **A7** | `NOT_A_BUG` | 每写必 flush | §6：1:1、64 KiB/次、与参考一致 | 不是瓶颈；改动伤延迟与错误传播 |
| **A8** | `NOT_A_BUG` | `writeChunked` 4 alloc / 5 µs | 真实 bulk 调用 **0 次** | 优化死代码 |
| **A9** | `NOT_A_BUG` | loopback 上窗口"没效果" | BDP≈46 KiB | 实验无 BDP，不是窗口不够 |
| **A10** | `NOT_A_BUG` | `BOX-direct` 的巨大方差（765–1717） | §4.1 | 只用数量级，不用精确比率 |

---

## 14. 未运行清单（与 §2.2 对应）

```text
NOT_RUN / NOT_COMPARABLE:
  - 官方 naiveproxy 客户端 ↔ 本仓服务端            （无参考二进制 / 无 WAN）
  - 本仓客户端 ↔ Caddy / forwardproxy 服务端       （无参考二进制 / 无 WAN）
  - 2×2 客户端×服务端矩阵                          （缺一方即凑不出）
  - 真实 WAN 的 H2 vs H3、RTT、丢包                （只有 loopback）
  - macOS / iOS 多 engine footprint / 线程 / TTFB  （无设备；脚本已审阅但不可运行）
  - H2 接收窗口 sweep 的工程结论                   （loopback BDP≈46 KiB）
  - Alt-Svc / 0-RTT / 连接迁移                     （无真实网络环境）
  - 低端移动设备的 CPU / 电量                      （无设备）
  - 小时级 soak 的内存/句柄曲线                    （本轮分钟级）
  - 流媒体 / 上下行非对称 / UoT 负载形态           （只有 bulk + 短请求）
  - client→server→target 三段独立计时              （双端同进程，无法分离）
  - pinned Native 的内部 profiling                 （Go pprof 只看到 cgocall 桥）
  - protocol/naive 的 -race 重跑                   （上一轮已全绿；本轮插桩仅加原子计数）
```

---

## 15. 给决策者的最后几个问题

1. **是否接受"本轮不给快/慢结论"？** 要回答 Q01–Q04，唯一路径是一台有参考二进制 + 真实 WAN 的实验环境，或允许在 CI 的 macOS runner 上跑 `bench-naive-cronet-engine.sh` / `bench-naive-receive-window.sh` 并接一台真实服务端。
2. **A1/A2（测试 id 过期 + self-test 不进 CI）是否列入下一轮？** 它不影响用户速率，但影响你们对池隔离/端到端的信心——本轮唯一确认的功能性缺陷。
3. **A3（文档历史数字过期）是否本轮之外顺手更新？** 只改文档，零风险。
4. **A5（engine 甜点=2）是否需要一次 macOS 实测来确认？** 本轮的 2/4/8 数据只是 Windows loopback。
5. **是否同意"Naive 性能就此收工"？** 证据支持 `KEEP`：Go 服务端无可测热点、端到端 38× 余量、协议与参考逐字节一致、会话复用已由库完成。继续投入的边际收益极低。

---

## 附录 A：复现命令与日志位置

```text
# 0) 导出基线（仓库外，不改主仓）
git -C <repo> archive --format=zip -o C:\Deepseek\_naive_audit_20261020\tip.zip origin/testing
Expand-Archive -Path ...\tip.zip -DestinationPath ...\tip

# 1) 原生端到端自测（libcronet.dll 必须在 PATH）
$env:PATH = "...\cronet-go\lib\windows_amd64@v0.0.0-20260929202119-8c68ce89873c;" + $env:PATH
cd tip\test
go test -tags "$(cat ..\release\DEFAULT_BUILD_TAGS_WINDOWS)" -count=1 -run 'TestNaiveSelf$' -v .
go test -tags <windows> -count=1 -run 'TestNaiveSelfInsecureConcurrency' -v .   # 预期 FAIL（A1）

# 2) 本轮 harness（仅在仓库外副本，见附录 B）
go test -tags <windows> -count=1 -timeout 2400s \
  -run 'TestAuditDirectBaseline|TestAuditNaiveThroughputMatrix|TestAuditPayloadIntegrity|TestAuditShortRequests|TestAuditSessionReuse' -v .

# 3) Go codec / H2 bulk 基准（server profile）
go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run '^$' \
  -bench 'BenchmarkPadding|BenchmarkHeadroom|BenchmarkCachedFirstPayload|BenchmarkNaiveH2' -benchmem -count=3 ./protocol/naive/

# 4) CPU profile
go test -tags <windows> -run 'TestAuditNaiveThroughputMatrix/H2-1$' -cpuprofile ...\cpu_h2.prof .
go tool pprof -top -nodecount=22 ...\cpu_h2.prof

# 5) 参考实现
git clone --depth 1 --filter=blob:none https://github.com/klzgrad/forwardproxy
# 读 forwardproxy.go:309-320 / 658 / 693-761
```

日志（`C:\Deepseek\_naive_audit_20261020\`）：`self_naive_h2.log`（PASS）、`self_insecure_concurrency.log`（A1 现场）、`tp_final.log`（本轮全部测量）、`bench_codec.log`、`cpu_h2.prof` / `cpu_h3.prof`、`tp_matrix*.log`（早期轮次）、`tp_fallback_tcponly.log`、`tp_reuse.log`。

## 附录 B：插桩与 harness 的所有权声明

本轮在**仓库外的导出副本**中做的最小插桩（**不在主仓、不会提交**）：

- 新增 `protocol/naive/audit_counters.go`（7 个 atomic 计数器 + 读/清零）；
- `inbound_conn.go` 的 `writeFrame`、`writeBufferWithPadding`、`writeChunked`、`naiveConn.Write`、`naiveH2Conn.Write`、`naiveH2Conn.flush` 各加 **1 个 atomic.Add**；
- 新增 `test/naive_perf_audit_test.go`（吞吐矩阵、direct/box 基线、回退、会话复用）与 `test/naive_perf_audit2_test.go`（负载完整性、短请求）。

插桩影响是"每 64 KiB 一次 atomic add"，在本轮噪声内（插桩前后 H2 单流：434.9–450.6 → 457.7，差异与轮次波动同量级）。

测量结束后，临时导出树（176.8 MB）与构建产物（112.5 MB）已删除；源文件保留在仓库外 `C:\Deepseek\_naive_audit_20261020\artifacts\`：

```text
artifacts/naive_perf_audit_test.go          矩阵/基线/回退/复用
artifacts/naive_perf_audit2_test.go         完整性/短请求
artifacts/audit_counters.go                 计数器（新增）
artifacts/inbound_conn_instrumented.go      带 6 处 atomic 的 inbound_conn.go
artifacts/naive_self_test.go                被审的仓库自测（A1 现场）
```

复跑：按附录 A 第 0 步导出 `tip`，把这四个文件覆盖回对应路径，再执行第 1–4 步。

---

## 附录 C：任务书 Q01–Q20 逐条答案索引（防止遗漏）

| # | 判定 | 本轮答案（一句话） | 位置 |
|---|---|---|---|
| Q01 | `NOT_COMPARABLE` | 无官方客户端/WAN，拒绝给快慢数字 | §0、§11.1 |
| Q02 | 性能 `NOT_COMPARABLE`；**协议逐字节一致** | padding/帧/flush/错误传播逐行核对 | §11.2 |
| Q03 | `NOT_RUN` | 2×2 缺一方即凑不出矩阵；任务书允许标注 | §11.1、§14 N1 |
| Q04 | 未复现也未反证 | 但 Go 服务端仅占 ~11%，若现象存在更可能在客户端/入口 | §5、§11.1 |
| Q05 | **MEASURED** | 快路径 100%、0 alloc、64 KiB/写、flush 1:1、handover 65 278 B | §3、§6 |
| Q06 | **PARTIAL（Windows 1/2/4/8）** | 甜点=2 engine（665 MiB/s）；>2 吞吐反降、CPU 线性升；macOS `NOT_RUN` | §8.1、§14 N3 |
| Q07 | `PARTIAL` | box 冷启动 34.3–46.1 ms；代码段共享（上轮 idle 数据）；NIK 单次开销与多 Outbound 共享未测 | §8.3、§8.4 |
| Q08 | **负对照 + BDP 定量** | 4 MiB vs 128 MiB 在 BDP≈46 KiB 下无差异；**WAN sweep `NOT_RUN`** | §8.2、§14 N4 |
| Q09 | `MEASURED`（归因）+`SOURCE`（机制） | 72.6% 在 cgo 桥；Go 侧 11%；pin/lock 推导上界 ≈1.5%，未逐函数测 | §5、§5.3 |
| Q10 | **MEASURED** | 6 CONNECT → 1 session；3 pool → 3 session（id 267） | §9.1、§9.2 |
| Q11 | `NOT_RUN` | 无 Apple 设备；脚本已审阅，命令在 §12.1/附录 A | §12.1、§14 N3 |
| Q12 | **MEASURED** | flush:write=1:1、64 158 B/次、与参考一致 → 不是优化点 | §6 |
| Q13 | **MEASURED** | 快路径 523/523=100%，`writeChunked` 0 次 | §6 |
| Q14 | **PARTIAL** | bulk 457.7 MiB/s vs 短请求 814 req/s（p50 1.02 ms）；流媒体/非对称/UoT `NOT_RUN` | §4.1、§4.3、§14 N8 |
| Q15 | **PARTIAL** | loopback：H3 慢 2.9×、CPU 3.1×；WAN/丢包/拥塞控制 `NOT_RUN` | §7、§14 N2 |
| Q16 | `PARTIAL` | 分层：裸 TCP 3285.7 → 盒 868.7 → naive 457.7；三段独立计时 `NOT_RUN` | §4、§14 N9 |
| Q17 | `PARTIAL` | loopback 冷连 4.18 ms（含 CONNECT 200）、热 1 ms 级；高 RTT `NOT_RUN` | §4.1、§4.3 |
| Q18 | **MEASURED** | H3 正常时 156.8 vs 158.2（<1%）；TCP-only 冷连 4 014 ms、窗口内 477.9 MiB/s | §10 |
| Q19 | `PARTIAL` | "始终 H3+回退"vs"始终 H2"已对照；30 s 窗口与 `min(10s, r/2)` 在高 RTT 的体验 `NOT_RUN` | §10、§14 N2 |
| Q20 | 观察+归类 | 每次启动一条失败的 UDP DoH 连接；启动窗口 34–46 ms 内无可测额外成本 → 归为策略/兼容问题，不为性能打 Native 补丁 | §1.3、§14 |

---

*报告结束。所有数字都能从附录 A 复跑；所有"未测"都在 §2.2/§14 列明。本报告不移动任何基线，也不构成施工授权。*
