# NaiveProxy 深度只读审计：Cronet 原生栈、性能架构、公共集成与 H3→H2 回退

- **日期**：2026-10-20
- **类型**：只读审计、参考实现核对、本机受控实测与可行性设计
- **本轮未修改任何生产代码、依赖、配置、CI、子模块 gitlink 或 Native 二进制**
- **结论**：在审计版本上，Go 服务端 codec 和公共集成无需重写。需要进一步处理的有三项：Naive 出站尚不产生 `adapter.FailureFacts`；Cronet 启动时的 DoH bootstrap 不受 sing-box DNS 策略控制，且当时没有关闭开关；H3→H2 回退可不修改 Chromium 实现，但需要单独设计有界判定窗口和状态所有权，不能直接复用现有 H3 verdict 语义。

---

## 1. Executive Verdict

### 1.1 一句话结论

**审计建议：** 服务端保持 `NO_CHANGE`；客户端的三个缺口分别评估。H3→H2 回退标记为 `RECOMMEND`，建议采用显式启用、默认关闭、单 Engine、按 CONNECT 选择的最小方案；本轮不实施。

### 1.2 已验证与未验证的范围

| 结论 | 证据等级 | 依据 |
|---|---|---|
| 打包的 Native Cronet 实际是 **Chromium 154.0.8037.49**，不是 Go module 版本串里的 143.0.7499.109 | `MEASURED_LOCAL` + `VERIFIED_SOURCE` | 本机 `Engine().Version()` 返回 `154.0.8037.49`；`cronet-go/lib/*/libcronet*.go` 的 `Version` 常量同为该值；`naiveproxy/CHROMIUM_VERSION` 也是该值 |
| 引擎默认 `enable_quic=true`、`enable_http2=true`（C API 默认值） | `MEASURED_LOCAL` + `VERIFIED_SOURCE` | 本机 `NewEngineParams()` 打印 `quic=true http2=true`；`cronet.idl_impl_struct.h:87-88` |
| `-force-quic: true` 时 Chromium **不会**退回 H2；UDP 被拒与 UDP 黑洞都停在同一 ~4.0 s 预算上 | `MEASURED_LOCAL` | H2 在同一端口可用（6 ms 连通），forced-H3 4.018 s / 4.009 s 失败，错误 `quic protocol error` |
| 单个 Engine **可以**按每个 CONNECT 选择 H2 或 H3，选择器就是 `-force-quic` header 的存在性 | `MEASURED_LOCAL` + `VERIFIED_SOURCE` | 同一引擎构造（quic+http2 均开启）下，不带 header → `protocol: h2`，带 header → `protocol: quic/1+spdy/3` |
| 认证失败可被识别为应用层错误（`unexpected response status: 407`），与传输错误可分离 | `MEASURED_LOCAL` | 错误密码时 H2/H3 都在 7 ms 返回 407 |
| 引擎启动时会向 `2001:4860:4860::8888:443` 发起一次 UDP 连接（DoH bootstrap），`dns_server_override` 无法阻止 | `MEASURED_LOCAL` | 每次运行都出现，与 QUIC/H2 模式无关，经 cronet Dialer 桥进入 sing-box dialer |
| 多 Engine 的常驻成本远小于"4 套 Chromium"的直觉 | `MEASURED_LOCAL`（仅 Windows 空闲态） | 1 engine 15.9 MiB WS/17 线程/235 句柄；4 engine 16.4 MiB/23/279；1 engine+4 NIK 15.9 MiB/16/229 |
| 服务端 padding/写入契约、ALPN 隔离、H2 边界、UoT guard 已被测试固定 | `VERIFIED_TEST` | `go test ./protocol/naive/...`（server profile）PASS；见 §4 |
| **服务端 padding 头算法与 Caddy forwardproxy 逐字节一致**（长度公式 + 16 字符集 + `~` 填充） | `VERIFIED_SOURCE` | `forwardproxy.go:308-320` vs `inbound_conn.go:32-44`；而官方**客户端**是 16..32 + 另一套字符集（§5.5） |
| **路由层流量计数对 Naive 隧道真实生效** | `MEASURED_LOCAL` | Clash API：`{"upload":41,"download":1078,"type":"naive/naive-in","rule":"final"}`，两次采样一致（§9.5） |
| 与官方客户端 / Caddy 的真实互通矩阵 | `NOT_RUN` | 需要参考二进制与独立试验环境，本机不具备；命令已给出（§12.4） |
| 真实网络 RTT/丢包/带宽下的 H2 vs H3 吞吐 | `NOT_RUN` | 本机只有 loopback；不能用 loopback 吞吐量代替真实网络结论 |

### 1.3 三项待决策工作（按投入产出排序）

1. **P1｜Naive 出站补 `adapter.FailureFacts`**（`INTEGRATION`，小）：全仓已在 `mieru` 建立先例（`protocol/mieru/fallback.go:419-430`），loadbalance/urltest 已消费（`protocol/group/loadbalance_failover.go:160,287`、`urltest.go:1181`）。审计时的 Naive 出站**不产生任何 fact**，因此分组层无法区分“首跳 QUIC 建连失败”和“目标拒绝”。这是本轮**唯一**一个既有明确前例、又有明确消费者、且不改变任何协议行为的施工项。
2. **P2｜Cronet DoH bootstrap 的策略缺口**（`INTEGRATION`/`BUG`，小-中）：已实测存在；`SetHostResolverRules` **在 pin 住的 cronet-go 里已经导出**（`engine_params_experimental_options.go:55-66`）却从未被 `naive_client.go` 或 `outbound.go` 调用。它不能消除那条 stray UDP 连接，但能把"代理域名解析"从 DoH 路径上摘出来，缓解 sing-box#4620 的整条出站不可用。需要一次 probe 决定"MAP 规则"还是"等上游提供禁用 DoH 的开关"。
3. **P3｜H3→H2 自动回退**（`FALLBACK`，中）：可行性已被本机原生实测证明**无需 Native patch**（per-CONNECT header 选择）。但它与现有两套 H3 verdict 语义不同（attempt 有序 vs per-authority），**不应**复制第三套状态机。建议先做 §14 的判定试验，再决定是否施工。

### 1.4 建议保持不变的部分（KEEP）

- Go 服务端的 padding framing、H1/H2/H3 接入、ALPN per-transport 隔离、`serveWebOrReject` 的 parse→auth→tunnel 顺序、`:scheme/:path` 拒绝、hijack 缓冲字节保留、H2 flush 错误传播、headroom/MTU 几何、`EarlyCopyBufferGrowth`。
- 客户端 `trackedNaiveConn`/`NaiveConn` 的 pinning 生命周期、cached-first-payload 几何判定、`IsReservedNaiveHeader` 的双向共用。
- `transport/http` 的 H3 verdict（attempt 序号 + 窗口 + 退避 + 网络切换 reset）。它是全仓最严谨的一份状态机，**不要**为 Naive 改写它。

### 1.5 审计过程中纠正的两项结论

审计过程中进行了第二轮证据核对（直接检出 `klzgrad/forwardproxy@d62c80d3` 与 `SagerNet/naiveproxy@97ae698` 两个参考源码树），推翻了初稿里的两条判断：

| 初稿说法 | 纠正后的事实 | 依据 |
|---|---|---|
| "参考服务端 padding 头是 30..62 + HPACK 字符集，我们 30..61 与之不同" | **Caddy forwardproxy（我们的服务端参考）就是 `rand.Intn(32)+30` + Caddy 的 16 字符常量 + `'~'` 填充，与本仓服务端逐字节相同**；30..62 + HPACK 字符集是 naiveproxy **内置**服务端的行为，不是我们的参考 | `forwardproxy.go:308-320` vs `http_proxy_server_socket.cc:41-42,451-462` |
| "参考对纯 GET 也回 407，我们回 400" | **参考把非 CONNECT 请求交给 `next.ServeHTTP`**（PAC 或后续 handler），自己不给固定状态码；407 只出现在"CONNECT + 认证失败 + 无 probe_resistance" | `forwardproxy.go:252-277` |

两项纠正不影响总体结论：服务端仍为 `NO_CHANGE`，P4 仍为 `ARCHIVE`。原先疑似缺陷的两项均属于设计差异；§5.5 中，服务端与 Caddy 参考实现一致，但客户端与官方客户端不同。

---

## 2. Scope / Git / Versions / Build identity

### 2.1 仓库快照

| 项 | 值 |
|---|---|
| 主仓 | `C:\src\sing-box`，`origin=https://github.com/Piggy-Cat-bit-shadow/sing-box.git`，`upstream=https://github.com/SagerNet/sing-box.git` |
| 分支 | `testing`（未新建分支，未 reset/stash/clean） |
| **审计开始 HEAD** | `6edc18c60266b3a9c776517b56e8dc7ed0c1a518` |
| **审计结束 HEAD** | `7e42ca38eb50afcbe844167a12652ce5c162b981` |
| 审计期间落地的提交 | `1e4fa7fc6 test(masque): make the default build honest about what needs HTTP/3`、`7e42ca38e docs(knowledge): index the round-three construction record and close the test item` |
| 对审计面是否有影响 | **无**。`git diff --name-only 6edc18c60..HEAD` 不含 `protocol/naive/**`、`transport/http/**`、`common/httpclient/**`、`go.mod`、`go.sum` |
| 工作树既有修改（**非本 Agent 产生，未触碰**） | ` M clients/apple`、` M clients/desktop`（子模块指针）；审计末段又出现 ` M go.mod`、` M go.sum`、` M test/go.mod`、` M test/go.sum`（**sing-tun pin `ff4fbed67c0d` → `276576ea222c`**，mtime 12:05:32 起，属并行窗口的 pin bump） |
| 本轮新增文件 | 仅本报告 |
| 并行变更对本审计的影响 | **无**。`protocol/naive`、`transport/http`、`common/httpclient` 均不导入 `sing-tun`（grep 为空）；本轮的 Go 测试与原生探针都在该 pin bump **之前**完成，且 Naive 数据面与之无关。若下一轮在这两个 module 上复跑，请以届时 `git status` 为准 |

> 说明：并行窗口在本轮审计期间推进了 HEAD 两个提交。所有源码行号引用均基于审计结束 HEAD `7e42ca3`，并且上述 diff 证明被审计文件在两快照间未变。

### 2.2 依赖与版本矩阵

| 组件 | 版本 / 提交 | 证据 |
|---|---|---|
| Go module 版本声明 | `go 1.25.5` | `go.mod:3` |
| 实际工具链 | `go1.25.5 windows/amd64`，`CGO_ENABLED=0` | `go version` |
| cronet-go pin（root） | `github.com/Piggy-Cat-bit-shadow/cronet-go v0.0.1-143.0.7499.109-2.0.20260929202119-8c68ce89873c` | `go.mod` replace 块 |
| cronet-go pin（commit） | `8c68ce89873c976b33ac62a3fd3128064d34e0a7`（2026-09-29，`perf(naive): expose the early copy-buffer growth capability`） | GitHub commit API |
| cronet-go 模块树 | root + `all` + **29 个 `lib/<platform>`**（root 与 `test/go.mod` 各 29 条 replace，逐条指到同一 commit） | `go.mod` / `test/go.mod` 的 replace 块 |
| **Native 库实际 Chromium** | **154.0.8037.49** | 运行时 `Engine().Version()`；`lib/windows_amd64/libcronet.go:3`、`lib/darwin_arm64/libcronet.go:3` 的 `const Version` |
| Go 版本串里的 `143.0.7499.109` | **不是 Native 版本**，只是上游 semver 前缀 | 同上；两者不一致是事实，不是笔误推断 |
| naiveproxy 子模块指针 | `SagerNet/naiveproxy @ 97ae698dede50ad27a9dd39b93de13da5a967340`（branch `cronet-go`） | cronet-go@8c68ce8 的 gitlink；`CHROMIUM_VERSION=154.0.8037.49` |
| Naive 参考实现（服务端语义） | `klzgrad/forwardproxy@d62c80d3...`（仓库内多处引用，未在本地检出） | `protocol/naive/inbound.go:574-700` 注释、`KNOWLEDGE/modules/naive-http-transport.md` |
| 服务端 HTTP 引擎 | `golang.org/x/net/http2` + `h2c`；`github.com/sagernet/quic-go`（replace 到 fork `v0.61.1-0.20261019061042-7e08ecaba383`） | `protocol/naive/inbound.go:32-33`；`go.mod` |
| 构建 profile | `DEFAULT`（含 `with_naive_outbound`，Darwin）；`OTHERS`（**不含**，Linux 服务端）；`WINDOWS`（`with_naive_outbound` + `with_purego`） | `release/DEFAULT_BUILD_TAGS{,_OTHERS,_WINDOWS}` |
| **参考服务端（Caddy forwardproxy）** | 仓库 pin `CaddyReferenceCommit = d62c80d3dd2c706b6b87579844d2397bddd18317`；测试 harness 用 `CaddyReferenceVersion = v2.10.0`；该 commit 自己的 `go.mod` 写 `caddyserver/caddy/v2 v2.8.4` + `quic-go v0.44.0` | `test/jiejie/jiejie_naive_forwardproxy_parity_test.go:36`、`jiejie_naive_caddy_differential_test.go:46`、`forwardproxy/go.mod` |
| **参考客户端（naiveproxy）** | `SagerNet/naiveproxy @97ae698`（`CHROMIUM_VERSION=154.0.8037.49`）；官方实现另见 `klzgrad/naiveproxy v148.0.7778.96-5`（#793 修复版本） | gitlink；`CHROMIUM_VERSION` |
| **上游 sing-box（对比基线）** | `upstream/testing @dfb80349b`（2026-10-13） | `git fetch upstream testing` 后 `git show` |
| 测试机 | Windows 11 x64，16 逻辑核，本机 loopback | `-` |

> 注意一处版本不一致（记录，不推断原因）：**测试 harness 用 Caddy v2.10.0，而 forwardproxy 该 commit 的 `go.mod` 写的是 v2.8.4**。differential 测试会校验参考二进制的版本头（`jiejie_naive_caddy_differential_test.go:381-400`），所以真实对照时以 harness 为准；本轮没有跑该测试，故不裁定哪个是"正确"的。

**必须区分（本轮核实）**：cronet-go 的 *代码 HEAD*（`8c68ce8`）、*Go pin 字符串*（`143.0.7499.109-2.0...`）、*打包 Native 二进制版本*（`154.0.8037.49`）是三个不同的东西。仓库内没有任何测试断言这三者一致；`protocol/naive/dependency_pin_test.go` 只断言 API 形状与几何，不断言版本号。

---

## 3. Architecture & Data-path map

### 3.1 客户端（fork 的 `cronet-go` + Chromium 154）

```
route/conn.go (CopyBufferGrowthTuner / cached first payload)
   │
   ▼
protocol/naive/outbound.go  ── DialEarly(ctx, dest) ─────────────────────────┐
   │  (with_naive_outbound)                                                  │
   │  DNSResolver = dnsRouter.Exchange(...)                                  │
   │  Dialer      = sing-box dialer chain (ResolverOnDetour)                 │
   ▼                                                                         │
cronet.NaiveClient.DialEarly ── headers ─────────────────────────────────────┤
   │  "-connect-authority": dest                                            │
   │  "Padding": generatePaddingHeader()      (always)                      │
   │  "proxy-authorization": Basic ...                                      │
   │  "-force-quic": "true"    ONLY when QUIC=true                          │
   │  "-network-isolation-key": ... when singleEngine && concurrency>1       │
   ▼                                                                         │
StreamEngine.CreateConn → BidirectionalConn ─────────────────────────────────┤
   │  Go: pin buffer → bidirectional_stream_read/write → unpin in callback   │
   ▼                                                                         │
Chromium net stack (154)                                                    │
   │  BidirectionalStream::StartRequest                                     │
   │  → net/http/bidirectional_stream.cc:219 解析 -force-quic                │
   │  → HttpStreamFactory::Job (using_quic_ = request_info.force_quic)      │
   │  → H2: BidirectionalStreamSpdyImpl / H3: BidirectionalStreamQuicImpl   │
   │  TLS/ALPN/证书/连接池/流控/拥塞控制/重试：Chromium 所有                  │
   ▼                                                                         │
TCP dialer / UDP dialer 回调（Chromium → Go）
   │  目的地址来自 Chromium 解析结果（IP 字面量）。
   │  特例：目的地址 == 127.0.0.1:53 时改走 socket pair → Go DNS bridge
   ▼
sing-box dialer chain（detour / 物理网卡 / IPv4v6 策略）
```

**所有权边界**（`VERIFIED_SOURCE`）：

- Chromium owns: TLS、证书校验、ALPN、H2/H3、QUIC、连接池与 session 复用、流控窗口、拥塞控制、重试、DoH、Alt-Svc、连接迁移。
- Go owns: 配置映射（`outbound.go:336-365`）、DNS bridge（`naive_dns.go`）、padding 帧（`naive_conn.go`）、buffer pinning（`bidirectional_conn.go:42-119`）、生命周期状态机（`naive_client.go:258-466,637-687`）、并发/NIK 选择（`naive_client.go:594-602`）。
- **不是** Chrome 浏览器：没有 UI、没有 renderer、没有 JS、没有 cookie/缓存预热、没有随机 ClientHello。

### 3.2 服务端（本仓 Go）

```
listener.Listener (listen options)
   │  可选 limitedListener（server_limits 计数，`inbound.go:195-197`）
   ▼
aTLS.NewListener(tcpListener, tls.TransportALPNView(n.tlsConfig, tcpNextProtos()))
   │  TCP ALPN = [h2, "http/1.1", <operator 自定义，剔除 h2/h1/h3>]
   │  QUIC ALPN = ["h3"]   （`inbound.go:255-278`）
   ▼
net/http.Server{Handler: h2c.NewHandler(inbound, http2Server), ReadHeaderTimeout}
   │  h2c = h2 prior-knowledge + h1
   ▼
Inbound.ServeHTTP
   │  1. method != CONNECT          → masquerade / 400
   │  2. H2/H3 带 :scheme/:path     → 400（参考实现同）
   │  3. Proxy-Authorization 校验   → 失败: masquerade / 407
   │  4. usePadding = request.Header["Padding"] != ""
   │  5. 目标 = URL.Host || Host；端口字面量校验（非法 → 400）
   │  6. 响应头 Padding 无条件发送（参考实现同）
   │  7. ResponseController.Flush()（失败 → 不建隧道）
   │  8. Hijacker? → naiveConn{padding.enabled=false}  （H1 原始字节）
   │     else      → naiveH2Conn{padding.enabled=usePadding}
   ▼
route.ConnectionManager → router → outbound → 目标
```

QUIC 侧：`protocol/naive/quic/inbound_init.go` 的 `init()` 安装 `naive.ConfigureHTTP3ListenerFunc`；无 `with_quic` 时由 `include/quic_stub.go` 安装一个**会失败的**构造函数，于是 `decideHTTP3Availability` 能区分"不存在"与"存在但坏了"（`inbound.go:294-336`）。

### 3.3 版本/构建矩阵（实机结论 + 模块 build constraint 核对）

| 平台 | profile | `with_naive_outbound` | `with_purego` | 本轮验证 |
|---|---|---|---|---|
| Linux 服务端 | `OTHERS` | ✗ | ✗ | 不链接 cronet（`docs/RELEASE-CERTIFICATE.md:66`）；本机无法交叉验证运行 |
| Windows 客户端 | `WINDOWS` | ✓ | ✓ | **本机可编译、可加载 `libcronet.dll`、可真实建连**（`MEASURED_LOCAL`） |
| macOS/iOS 客户端 | `DEFAULT` | ✓ | ✗（cgo） | `NOT_RUN`（无 macOS 环境）；Linux 上 `-race` 无法链接 cronet 是已知窗口（`docs/ENGINEERING-NOTES.md`） |
| Android | `DEFAULT` | ✓ | ✗ | `NOT_RUN` |

**为什么 Windows 必须带 `with_purego`，而 Linux/Darwin 不带**（本轮直接读 pin 的模块源码）：

| 约束 | 文件 | 内容 |
|---|---|---|
| Windows 的平台包装 | `cronet-go/all/windows_amd64.go:1` | `//go:build windows && amd64 && with_purego` —— **不带 purego 时整个 all 包没有文件**，链接期就会缺 libcronet |
| Linux 的平台包装 | `all/linux_amd64.go:1` | `//go:build linux && !android && amd64 && !with_musl`（**不含** purego 条件） |
| Linux Native 库 | `lib/linux_amd64/libcronet_cgo.go:1` | `//go:build ... && !with_purego` + `#cgo LDFLAGS: -l:libcronet.a`；同目录 `libcronet_purego.go` **只声明 `Version`**，没有实现 |
| Darwin Native 库 | `lib/darwin_arm64/{libcronet_cgo.go,libcronet_purego.go}` | 同型：cgo 链接 `libcronet.a`，purego 侧只有版本常量 |
| Windows Native 库 | `lib/windows_amd64/` | 只有 `libcronet.dll` + 版本常量（purego 在运行时 `syscall.LoadLibrary`） |

因此：**Windows = purego + 运行时 DLL；Linux/Darwin = cgo 静态链接**。这也解释了 sing-box #3617 的两个报错：Windows 报 `cronet: library not found`（DLL 未随包部署），Linux 报 `build constraints exclude all Go files`（该版本还没有 purego 侧的文件）。本轮不把这两点当作产品缺陷。

**因此：不能把"Linux 服务端 profile 没有 `with_naive_outbound`"写成"Cronet 网络栈不完整"。** 两者无关。

---

## 4. Already Implemented / Known Good

这一节是给下一轮 Agent 的"别改"清单。每条都指向具体源码或测试。

### 4.1 服务端 codec / 协议语义

| 能力 | 位置 | 测试 |
|---|---|---|
| padding first → payload budget = 65536−3−padding | `inbound_conn.go:295-328` | `padding_segmentation_test.go`, `audit_padding_test.go` |
| 短写 = `io.ErrShortWrite`，帧计数不前进 | `inbound_conn.go:206-215,188-194` | `audit_write_contract_test.go` |
| padding 0..255 全范围，绝不被 buffer 截断 | `inbound_conn.go:236-259` | `TestAuditPaddingCoversFullRange` |
| 零数据帧不产生 (0,nil) | `inbound_conn.go:108-165` | `padding_zero_frame_test.go` |
| H1 CONNECT 不加帧（即便带 Padding 头） | `inbound.go:673-704` | `jiejie_naive_h1_framing_guard_test.go`（test 模块） |
| 响应 Padding 头无条件发送 | `inbound.go:596-610` | `audit_padding_test.go` |
| 参考实现的多余帧行为（nr==0 时不跳过 padding）**故意不复刻** | `inbound_conn.go:100-107` 注释；参考缺陷在 `forwardproxy.go:712-726` | `padding_zero_frame_test.go` |
| padding 字节写零 | `inbound_conn.go:179-185,253-259` | **缺失**（P9） |
| H2 flush 失败向上传播 | `inbound_conn.go:449-476` | `audit_flush_contract_test.go` |
| hijack 前已读入的字节保留为首包 | `inbound_conn.go:526-547` | `hijacked_conn_halfclose_test.go` |
| `hijackedConn` 不隐藏 socket 的 CloseWrite/CloseRead | `inbound_conn.go:579-604` | `TestHijackedConnDoesNotHideTheSocketsCloseWrite` |
| 每传输独立 ALPN 视图（h3 不上 TCP，h2 不上 QUIC） | `inbound.go:169-187,255-278` | `audit_alpn_isolation_test.go`（6 项） |
| H3 不可用的 fatal/non-fatal 判定 | `inbound.go:294-336` | `inbound_config_test.go`（quic 包） |
| `:scheme`/`:path` 在 H2/H3 CONNECT 上被拒 | `inbound.go:507-524` | `jiejie_naive_h3_pseudo_header_test.go` |
| 目标端口非法字面量 → 400（不静默归零） | `inbound.go:565-594` | `fuzz_authority_test.go` |
| 源地址只取 `RemoteAddr`，不信任转发头 | `inbound.go:629-650` | `server_limiter_test.go`（键） |
| `server_limits` 在 accept 层计数，失败也归还 | `inbound.go:188-197`、`server_limiter.go` | `server_limiter_test.go` |
| HTTP/2 窗口上下界与 x/net 真实接受区间一致 | `inbound.go:396-458` | `TestHTTP2WindowBoundsMatchXNet` |
| 错误包装边界（H3 连接级故障不被 `net.ErrClosed` 吞掉） | `quic/h3_error_class.go`、`quic/inbound_init.go:249-252` | `h3_error_class_test.go` |

### 4.2 客户端桥接

| 能力 | 位置 |
|---|---|
| read/write buffer pinning + 终态兜底 unpin | `bidirectional_conn.go:42-119,184-226,447-485` |
| 回调里 unpin（deadline/Close 早返回也不释放） | `bidirectional_conn.go:447-457,473-477` |
| engine 启动失败不泄漏 engine、不永久卡 `starting` | `naive_client.go:271-310` |
| `Close` 幂等，`CloseAllConnections` 后等 active 归零 | `naive_client.go:637-687` |
| cached-first-payload 几何判定 + 安全回退 | `route/conn.go:1062-1116` |
| 保留 header 双向共用一份定义 | `naive_client.go:74-106` + `outbound.go:374-382` |
| `EarlyCopyBufferGrowth` 客户端侧 | `naive_conn.go:405-430` |

### 4.3 已知历史结论（不要重复施工）

- "UoT v2 non-Connect 是 P0" 曾是**测试夹具错误**，不是产品缺陷（`docs/naive.md:65-67`）。
- "把 0..255 clamp 进 buffer" 曾修过又**被回滚**（`3c6e0aad3` → `fe5789539`）。
- 缓存首包基准的历史数字（64 B 470→214 ns 等）只对应当时夹具，**不是** 64 KiB 结论，也不是远端吞吐（`docs/naive.md:285-287`）。

#### 4.3.1 `protocol/naive` 的已修 bug 清单（`git log -- protocol/naive`，2026-09-25 起）

这些都已经修好并有对应提交，下一轮**不要**当成新发现重做：

| 提交 | 日期 | 修的是什么 |
|---|---|---|
| `242d8c2cb` | 2026-09-25 | 零长度 padded 帧返回无进展（改为消费并继续） |
| `65e227002` | 2026-09-25 | padded 响应分片对齐 forwardproxy（65536 上限 + 先抽 padding） |
| `1f9014c85` | 2026-09-25 | 非法 CONNECT 端口被静默归零（改为 400） |
| `d22dd8944` | 2026-09-25 | QUIC 调优改成 opt-in，协议默认回到 reference-like |
| `bc50e66ea` | 2026-09-25 | H2/H3 隧道 flush 失败被吞掉（改为上报） |
| `2fbab5b96` | 2026-09-25 | 认证比较非常数时间（改为 constant-time） |
| `f74d9254c` | 2026-09-25 | QUIC 版本列表未与参考一致（显式 pin v1/v2） |
| `d6820d0e7` | 2026-09-25 | tcp+udp inbound 上 ALPN 未按传输隔离 |
| `da34e4ce7` | 2026-09-28 | HTTP/2 接收窗口未按 x/net 真实边界校验 |
| `2e3b614f2` | 2026-09-28 | byteformats 溢出 + 窗口选项未守卫 |
| `724035c76` | 2026-09-29 | 重复的 `idle_timeout`（且证明空闲隧道不会被杀） |
| `d26754689` | 2026-09-30 | quic-go 吞掉的连接级故障未上报 |
| `fa45dcfc2` | 2026-10-15 | `hijackedConn` 隐藏了 socket 的 half-close |
| `9de65142c` | 2026-10-17 | resolve-dialer 断言 panic（改为构造期错误） |

注意 `65e227002`（分片上限）、`242d8c2cb`（零长度帧）、`bc50e66ea`（flush）这三条正是"看起来像新优化点"的入口，实际上它们**就是修复本身**——下一轮若有人重新提出"padding 分片应该按 65535 切"，请先读 `padding_segmentation_test.go`。

---

## 5. Best-practice & Issue/PR research

> 这一节按"移植价值"组织，而不是按 Issue 编号。**closed 不等于已修，open 不等于我们有缺陷。**

### 5.1 sing-box #4205 — Naive inbound ALPN race（**已吸收，且我们做得更远**）

| 字段 | 内容 |
|---|---|
| 链接/日期 | https://github.com/SagerNet/sing-box/issues/4205 ，2026-06-11 开，2026-06-27 关（`bug`，milestone 1.13 Next） |
| 原始症状 | `network: tcp+udp` 时 QUIC 随机不可用，`CRYPTO_ERROR (0x178)` / TLS alert 120 |
| 真正原因 | `Start()` 里 TCP 的 ALPN 初始化在**goroutine** 内，QUIC 的 `qtls.ConfigureHTTP3` 同步跑；两边都是 `if len(NextProtos())==0 { set }`，谁先跑决定 h3 在不在 |
| 解决思想 | 把 ALPN 解析移到 listener 创建之前，一次性完成（上游 `627912a78`，v1.13.14） |
| 目前状态 | **已修**；该提交是当前 `testing` 的祖先（`git merge-base --is-ancestor` exit 0） |
| 对我们适用性 | 我们不仅吸收，还额外做了**per-transport ALPN 视图**：`tcpNextProtos()` 排除 h3，`quicNextProtos()` 只保留 h3（`inbound.go:255-278`）。上游的修法仍是**一张共享列表**，因此 h3 仍会出现在 TCP 的 ALPN 里——这正是本 fork 注释里"已经测到过"的那类协议泄漏 |
| 建议 | `KEEP`。不要为了"跟上游一致"退回共享列表 |
| 维护风险 | 低。视图通过 `tls.TransportALPNView` 委托证书/ACME/Close，`Clone()` 的陷阱已在注释里说明 |

### 5.2 naiveproxy #756 — QUIC 速度异常（**作者本人的权威判断**）

| 字段 | 内容 |
|---|---|
| 链接/日期 | https://github.com/klzgrad/naiveproxy/issues/756 ，2025-10-16 开，2026-05-01 关（not planned） |
| 原始症状 | `quic://` 只有几百 kb/s，VPS 与家宽不限速；同机 Hysteria 可 200 Mbps |
| 维护者结论 | 2025-10-18：**"I can reproduce this behavior. QUIC in Naiveproxy is not particularly tuned for performance as TLS is already as fast as possible."** 2026-05-01 关闭时：**"In my latest testing, QUIC is not as good as TLS but it is also not bad. I'm not interesting in enabling aggressive congestion control like in Hysteria."** |
| 解决思想 | 不解决：作者明确拒绝激进拥塞控制，认为 TLS 已经足够快 |
| 对我们适用性 | 直接相关。**"用 H3 就会更快"在我们这里没有依据**；本机 loopback 也显示 H2 5-8 ms、H3 6-7 ms 无差别（§7.4）。我们的 `quic_congestion_control` 选项（bbr/bbr2/cubic/reno）是**提供选择**，不是宣称某个更快 |
| 建议 | `NO_CHANGE` + 文档化：H3 的主要收益是可用性/路径多样性，不是吞吐；不要因为"H3 新"而改默认 |
| 维护风险 | 若我们擅自调 QUIC 参数（初始窗口/CC）以获得"更好看"的数字，就会与参考实现的 wire 行为分叉 |

### 5.3 sing-box #3837 — naive outbound 性能瓶颈报告（**未确认，不可作为事实**）

| 字段 | 内容 |
|---|---|
| 链接/日期 | https://github.com/SagerNet/sing-box/issues/3837 ，2026-03-01 开，同日以 `not planned` + `not following template` / `no locally reproducible process provided` 关闭 |
| 原始症状 | 用户称 naive outbound 最高 1 Mbps，而"官方 naive 客户端 + sing-box socks"150 Mbps |
| 讨论 | 6 条评论：维护者/协作者只要求最小复现；另有用户附议"延迟和网速都明显比原生 naive 差"；有人建议 `quic: true`，报告者称两种都试过 |
| 真正原因 | **无结论**。没有 profile、没有同目标对照、没有 2×2 客户端/服务端矩阵 |
| 对我们适用性 | **不能**据此判定当前版本有性能缺陷。但报告形态（同一台机器、只换 outbound 实现）本身提示了需要排除的变量：目标 CDN、DNS、UoT、`mixed` inbound 的拷贝路径、引擎数量、`fast.com` 的多流特性 |
| 建议 | `MEASUREMENT_FIRST`：用 §7.5 的 2×2 矩阵 + `scripts/ci/bench-naive-cronet-engine.sh` 在受控链路复测；在此之前不立项 |
| 维护风险 | 低（纯测量） |

### 5.4 sing-box #3803 / #4620 — Cronet DoH auto-upgrade 绕过 DNS 策略（**我们身上仍然成立**）

| 字段 | 内容 |
|---|---|
| 链接/日期 | #3803 https://github.com/SagerNet/sing-box/issues/3803 （2026-02-16，Stale 关闭）；#4620 https://github.com/SagerNet/sing-box/issues/4620 （2026-10-07，**open**） |
| 原始症状 | `WARN udp dial failed address=[2001:4860:4860::8888]:443 ... network is unreachable`；在屏蔽 Google IP 的网络里 `stream failed: name not resolved`，整条 naive 出站不可用 |
| 报告者分析 | Chromium 的 `doh_provider_entry.cc` 硬编码已知 DoH 提供者；`AsyncDNS` 开启后自动升级到 `https://dns.google/dns-query`（QUIC/UDP 443）。`DnsServerOverride` 只覆盖明文 nameserver，不影响 `dns_over_https_config` / `secure_dns_mode` |
| 我们的实测 | **完全复现**：本机系统 DNS 是 `10.8.8.8/10.8.4.4`，与 Google 无关，但每次 `Start()` 仍出现 `open UDP connection to [2001:4860:4860::8888]:443`（H2 与 H3 模式都有）。该 dial 经 cronet Dialer 桥进入 sing-box outbound dialer，因此**受路由/detour 约束，但不经过 `dns_router`** |
| 解决思想（#4620 提案） | 暴露 `EngineParams.SetHostResolverRules("MAP host ip")`，让代理域名在 DNS/DoH 事务之前短路 |
| 我们的现状 | `cronet-go@8c68ce8` **已经导出** `SetHostResolverRules`（`engine_params_experimental_options.go:55-66`），但 `naive_client.go:493-499` 只调 `SetAsyncDNS`/`SetDNSServerOverride`/`SetUseDnsHttpsSvcb`，`protocol/naive/outbound.go` 也没有对应选项 |
| 适用性判断 | **部分可迁移**。MAP 规则能保证"代理域名不会被 DoH 决定"，但**不能**消除那条 stray UDP 连接（本机在 `ServerName` 为 IP 字面量时仍未发生任何域名解析，连接照样出现）——两者同源不同症 |
| 建议 | 见候选 P2：先做一次 probe 判断是否有 API 能彻底关掉 DoH；若没有，只做 `host_resolver_rules` 并写清"stray 连接仍在"的边界 |
| 维护风险 | 低（纯参数）。但**不要**把 MAP 规则硬编码成默认，那会改变用户对 `server` 语义的预期 |

### 5.5 naiveproxy #76 / #72 — padding 的 auto-negotiation 与 HPACK（**我们的服务端正是在实现 #76 的设计**）

| 字段 | 内容 |
|---|---|
| 链接/日期 | #72 https://github.com/klzgrad/naiveproxy/issues/72 （2020-05-22，completed）；#76 https://github.com/klzgrad/naiveproxy/issues/76 （2020-05-25，completed） |
| #72 的核心 | ① 服务端 200 里的 padding 头经 Caddy 不透传 → 无效；② Padding 头会被 HPACK 动态表索引，压缩到 1 字节，失去混淆意义；③ 维护者最后用"每次随机值"解决（并考虑过改 Chromium 的 `hpack_encoder`，因 Caddy 不可改而放弃）；④ `Proxy-Authorization` 被压缩"无所谓，它不是混淆来源" |
| #76 的核心（**我们直接照做的语义**） | "The client will send header padding regardless. **Naive forwardproxy will turn on payload padding automatically if the client request contains header padding. The server will send header padding regardless.**" 并明确 old-client/new-server 两个方向的兼容后果 |
| 目前状态 | 已实现于参考 forwardproxy；本 fork 的实现与之逐条一致 |
| 对我们适用性 | `KEEP`。`inbound.go:539-548`（请求头决定 framing）与 `:596-610`（响应头无条件）正是 #76 的原文语义，**不是**随手写的怪癖 |
| 额外发现之一（**本轮已直接核对 Caddy forwardproxy 源码**，`VERIFIED_SOURCE`） | padding 头的长度与字母表有**三个不同实现**，而我们与服务端参考逐字节一致：<br>• **Caddy forwardproxy**（我们的服务端参考，`forwardproxy.go:308-320`）：`paddingLen := rand.Intn(32) + 30` → **30..61**；前 16 字符取自一个 16 字符常量（`!`、`#`、`$`、`(`、`)`、`+`、`<`、`>`、`?`、`@`、`[`、`]`、`^`、反引号、`{`、`}`），其余填 `'~'`。**这与本仓 `inbound_conn.go:32-44` 的实现逐字节相同**（同一常量、同一长度公式、同一填充字符）。<br>• **naiveproxy 的官方客户端**（`naive_proxy_delegate.cc:64-67`）：`RandIntInclusive(16,32)` → **16..32**，字母表取自 HPACK 表"编码长度 ≥ 8 bit"的字符，第 17 个字符做填充。<br>• **naiveproxy 的内置服务端**（`http_proxy_server_socket.cc:41-42,451-462`）：`RandIntInclusive(30,62)` → **30..62**，同样的 HPACK 字母表。<br>• README 的 non-formal spec 原文："The length of the padding sequence is randomly distributed in **[16, 32] (request) or [30, 62] (response)**"（`README.md:122`）。<br>**结论**：服务端（我们 vs Caddy）**一致**；客户端（`cronet-go` 生成 30..61 + Caddy 字母表）与官方客户端 **不一致**（长度区间与字母表都不同）。互操作不受影响（接收方只读声明长度），但"客户端 wire 外观与官方一致"**不成立**，不能写成已验证 |
| 额外发现之二（服务端形状，`VERIFIED_SOURCE`） | naiveproxy 的**内置**服务端在 `naive_padding_socket.cc:198-210` 对**服务端方向且 payload < 100 B** 的帧把 padding 从 `[max-padding−len, max-padding]` 里抽，使帧长趋于恒定；并在 `:257-262` 对 `200 < len < 1024` 的上行按 100..200 字节再切分。**Caddy forwardproxy 不做这两件事**（它就是我们注释里引用的 `paddingSize := rand.Intn(256)` + `maxRead := 65536-3-paddingSize`）。我们的服务端**与 Caddy 一致、与 naiveproxy 内置服务端不同**——这符合本项目"Caddy 语义优先"的定位，但必须写清，否则下一轮会把"小包 padding 不偏向定长"当成缺陷 |
| 额外发现之三（`VERIFIED_SOURCE`） | README 还描述了一项**客户端**行为：为掩盖"每个会话发出过多 RST_STREAM"这一非常见模式，在 RST_STREAM 之前先发一个带 padding、总长 `[48,72]` 的 END_STREAM DATA 帧。实现位于 `src/net/spdy/spdy_session.cc:1925-1942`（`RandIntInclusive(48,72)`）。**这在 Chromium 里，所以我们的 Cronet 客户端自动拥有**；我们的 Go 服务端**不**做——与 Caddy 相同，仅作为形状差异记录 |

### 5.6 naiveproxy #507 / #793 — 替代服务端与 preamble（**前瞻兼容**）

| 字段 | 内容 |
|---|---|
| #507 | https://github.com/klzgrad/naiveproxy/issues/507 （2023-05-12）。hprox（Haskell）实现了 naive padding + probe resistance；维护者回应：**"the current one used in naiveproxy is very naive and is planned to be replaced soon"**，并说明计划是"新增 padding frame 格式 + 用 HTTP 头协商版本；当前格式只有 1 字节 padding size，更强混淆需要 2 字节；同时 'currently hard coded to pad only 8 packets' 也要变灵活" |
| #793 | https://github.com/klzgrad/naiveproxy/issues/793 （2026-05-07，v148.0.7778.96-4 修复）。preamble 与"普通 H2/H3 CONNECT 服务器"不兼容，`Preamble error: ERR_PROXY_AUTH_UNSUPPORTED` |
| 当前 wire 事实 | pin 住的 154 客户端在 CONNECT 上**已经发送** `padding-type` 请求头（`naive_proxy_delegate.cc:28-36`）；服务端 `ParsePaddingHeaders` 在缺 `Padding-Type` 时回退 `kVariant1`（`http_proxy_server_socket.cc:274-300`）；客户端在收到响应时若**没有** `padding-type` 回包且存在 `Padding` 头，也回退 `kVariant1`（`naive_proxy_delegate.cc:118-135`） |
| 对我们适用性 | **当前互通无碍**：我们的服务端忽略 `padding-type` 请求头、只按 `Padding` 存在性决定 framing，且不发 `padding-type` 回包，客户端按回退规则仍然选 `kVariant1` |
| 风险 | 一旦上游真的落地 variant2（2 字节 padding size / 可变帧数）并通过 `padding-type` 协商，我们的服务端会成为"只会 variant1 的实现"——届时若客户端**默认**发 variant1 则仍兼容，若客户端默认升到 variant2 则断链。现在**不需要**动，但应把"忽略 `padding-type`"这件事写进已知边界，避免下一轮把它当成 bug 随手改 |
| 建议 | `KEEP` + 记录。**不要**现在实现 variant2 |

### 5.7 我们已了解但**不采纳**的方向（记录理由）

| 方向 | 为什么不做 |
|---|---|
| 按 Chrome 指纹随机化 ClientHello / 导入浏览器模块 / 改 H2 SETTINGS | 没有上游 Issue、互操作故障或实测差异支持；违背"保留原生 Cronet 而不模仿 Chrome"的边界 |
| 让 Naive 改走 `transport/http.Client` 的 Go H2/H3 数据面 | 直接摧毁原生 Chromium 路线（本轮明确禁区） |
| 把 padding 0..255 clamp 到 buffer | 已被回滚过一次，见 §4.3 |
| 为 H3 回退新建独立状态机 | 已有两套，语义不同；见 §10.3 |
| 在服务端实现 probe_resistance / preamble | 那是"伪装成 Web"的领域，与 Caddy 的 `file_server` 组合相关；我们的 masquerade 已覆盖"非代理请求"的响应，但没有实现 naive 的 preamble 语义，且没有互通证据要求 |

### 5.8 补充检索到的 6 条高价值 Issue（本轮第二轮检索，按提示词要求的主题逐项覆盖）

> 检索方式：GitHub issue search，`repo:SagerNet/sing-box naive`（2 条命中，全读）、`repo:klzgrad/naiveproxy fallback http2 quic`（30 条，读 6 条）、`repo:SagerNet/cronet-go naive quic padding buffer engine`（1 条，全读）。以下每条都读了正文与评论。

#### (1) naiveproxy #669 — 官方明确拒绝"同一配置同时用 HTTPS 与 QUIC"（**这是 §7.2 要求的"维护者是否拒绝过回退"的直接答案**）

| 字段 | 内容 |
|---|---|
| 链接/日期 | https://github.com/klzgrad/naiveproxy/issues/669 ，2024-09-20 开，2024-10-01 关（**not planned**） |
| 原始诉求 | "Caddy 已经可以同时支持 https 和 quic；能否在一个配置里同时对同一站点使用 `https://` 与 `quic://`，这样更像正常浏览网站" |
| 维护者回答（OWNER） | **"应该由客户端指定协议，这样利于控制性能"** |
| 现状 | 拒绝，未实现。官方客户端至今只有 `--use-quic` 这一显式开关 |
| 对我们适用性 | **直接相关**。它说明参考实现把"协议选择"视为**用户/性能决策**，而不是自动降级。我们若实现自动回退，必须保持"显式开关优先、默认行为不变"，否则与参考的取向相反 |
| 建议 | 写进 §10.7 的结论依据；不改变 `RECOMMEND`（方案 B 仍是"默认关闭的可选项"），但把"参考实现拒绝自动化"列为决策者必须知道的反方证据 |

#### (2) SagerNet/cronet-go #11 — `QUIC=false` 曾不传播到 Chromium，H2 模式会被 Alt-Svc 悄悄升级成 QUIC（**已吸收，且解释了为什么这个修复是必需的**）

| 字段 | 内容 |
|---|---|
| 链接/日期 | https://github.com/SagerNet/cronet-go/issues/11 ，2026-07-01 开，2026-07-02 关（completed，`bug`） |
| 原始症状 | `NaiveClientOptions.QUIC=false` 时，非 QUIC 分支只调 `SetEnableHTTP2(true)`，**没有** `SetEnableQuic(false)`；Chromium 的 `enable_quic` 默认为 true，于是仍可能接受 Alt-Svc 并协商 QUIC。报告者记录到 `response protocol=quic/1+spdy/3 status=200` |
| 修复 | 在非 QUIC 分支加 `params.SetEnableQuic(false)`；之后请求稳定在 `protocol=h2` |
| 我们的 pin | `8c68ce8`（2026-09-29，晚于修复）**已包含**：`naive_client.go:488-491` 明确 `SetEnableQuic(false)`。本机实测 `QUIC=false` 时 `protocol: h2`，无 QUIC 到服务端。 |
| 对我们的设计含义 | **这是 §10 方案 B 的一个硬约束**：在 `enable_quic=true` 的引擎上，"不带 `-force-quic`"并不等于"H2 only"——只要服务端（或中间设备）为该 origin 广告过 Alt-Svc，Chromium 仍可能选 QUIC。我们的 Go 服务端不发 Alt-Svc，所以实践中是 H2；但方案 B 的文档必须写清这一点 |
| 建议 | `KEEP`（已修复）+ 在回退设计里显式引用 |

#### (3) sing-box #4249 / PR #4287 — Naive H2 wrapper 被绕过（**已吸收**）

| 字段 | 内容 |
|---|---|
| 链接/日期 | https://github.com/SagerNet/sing-box/issues/4249 （2026-06-27 开，2026-07-21 关，`completed`）；修复 PR https://github.com/SagerNet/sing-box/pull/4287 |
| 原始症状 | `NewHTTP2Wrapper(conn)` 被创建，但 router 收到的是**裸 conn**，wrapper 的互斥保护被绕过，并发下随机数据损坏 |
| 修复 | 把 wrapper 传给 `RouteConnectionEx` |
| 我们的状态 | `inbound.go:813-820` 传的就是 `wrapper`；`git merge-base --is-ancestor 3c4a5a6ed HEAD` exit 0，且该提交在 `upstream/testing` 中——**已吸收** |
| 建议 | `KEEP`；不要"简化"成传裸 conn |

#### (4) sing-box #4458 — 上游"给 Naive inbound 加 HTTP/2 选项"的提案（**注意：上游 testing 至今没有这个功能**）

| 字段 | 内容 |
|---|---|
| 链接/日期 | https://github.com/SagerNet/sing-box/issues/4458 ，2026-08-28 开，2026-09-02 关（completed） |
| 提案 | 把共享 `HTTP2Options` 嵌入 `NaiveInboundOptions`，转发到 `http2.Server{...}` |
| 上游实际状态 | 我在 `upstream/testing`（`dfb80349b`，2026-10-13）核对：`protocol/naive/inbound.go` 仍是 `h2c.NewHandler(n, &http2.Server{})`（第 110 行），`option/naive.go` 里**没有** `HTTP2Options`。也就是说该 Issue 关闭时**并没有**让上游获得这个能力 |
| 我们的状态 | 本 fork **已有**且更严格：`HTTP2Options` 经自定义 `UnmarshalJSONContext` 暴露（`option/naive.go:74-86`），`validateHTTP2Options` 按 x/net 的真实接受区间校验（`inbound.go:396-458`），并有 `http2_options_test.go`（27 KB）与 `audit_http2_runtime_test.go` |
| 合并风险 | **高**。若上游将来按 Issue 的写法实现（嵌入 `HTTP2Options` 到 option 结构、字段名/默认值可能不同），本 fork 的同名能力会冲突。届时必须逐字段比对，而不是简单取 upstream |
| 建议 | `KEEP` + 在上游同步清单里标注"Naive HTTP2Options 是 fork-only，上游合并时以行为为准" |

#### (5) sing-box #3617 — Windows 运行时找不到 cronet 库 / Linux `with_purego` 编译失败（**部署与构建约束**）

| 字段 | 内容 |
|---|---|
| 链接/日期 | https://github.com/SagerNet/sing-box/issues/3617 ，2025-12-16 开，2025-12-22 关（completed） |
| 症状 | ① Windows 构建含 naive 出站时报 `FATAL create service: ... cronet: library not found`；② Linux 用 `with_naive_outbound,with_purego` 编译报 `lib/linux_amd64: build constraints exclude all Go files` |
| 对我们的适用性 | ① 是**部署约束**：Windows 产品必须把 `libcronet.dll` 放在可执行文件同目录或 PATH（本机探针正是这样加载成功的，DLL 9,889,280 B）。② 已在 pin 上核对：`all/windows_amd64.go` 的约束是 `windows && amd64 && with_purego`，而 `lib/linux_amd64` 只有 cgo 档案（`libcronet.a`）加一个只声明 `Version` 的 purego 文件——所以 **Windows 必须 `with_purego`，Linux/Darwin 走 cgo**，这与三个 profile 文件完全对应 |
| 建议 | 写进 §3.3；不要试图在 Linux 上给 `with_naive_outbound` 搭配 `with_purego` 并期待它是受支持形态 |

#### (6) SagerNet/cronet-go #6 — padding 区曾泄漏未清零的旧 buffer 内容（**安全类，双侧已修，但我们的服务端缺回归测试**）

| 字段 | 内容 |
|---|---|
| 链接/日期 | https://github.com/SagerNet/cronet-go/issues/6 ，2026-04-09 开，2026-04-10 关（completed） |
| 原始症状 | `naive_conn.go` 用 `buffer.Extend(paddingSize)` 扩展填充区但**不清零**，抓包可见 padding 区是旧池化 buffer 的残留字节；Naive 规范要求 `zeros[padding_size]` |
| 关联 | 同源问题在 sing-box 侧也有：https://github.com/SagerNet/sing-box/issues/4002 （修复 PR #4001） |
| 我们的状态 | 服务端 `inbound_conn.go:253-259,179-185` 均用 `WriteZeroN`；客户端 pin 亦已修复 |
| **仍然存在的小缺口** | `cronet-go` 的测试里**有**断言（`naive_conn_test.go:187-191`："padding byte %d is %d, reference writes zero padding"），**本仓服务端没有任何测试断言 padding 区全为零**——只有长度/边界断言。这是一个真实的 `TEST_GAP`（见 P9） |
| 建议 | `ADD_TEST`（P9） |

---

## 6. Reference compatibility matrix

> `VERIFIED_SOURCE` 表示由源码/协议常量可证；`NOT_RUN` 表示本轮没有可执行环境。**没有任何一格是用"我们两端能通"推出来代替官方互通的。**

### 6.1 官方客户端 → 本 fork 服务端

| 场景 | 预期 | 证据 | 状态 |
|---|---|---|---|
| H1 CONNECT（无 Padding） | 200 + 原始字节 | `inbound.go:673-704`；参考 `serveHijack(...false)` | `VERIFIED_SOURCE` |
| H1 CONNECT（带 Padding 头） | **仍然**原始字节，不加帧 | 同上 | `VERIFIED_SOURCE` |
| H2 CONNECT + `Padding` | 200 + 首个 8 帧双向 framing | `inbound.go:548`、`inbound_conn.go:217-270` | `VERIFIED_SOURCE` |
| H2 CONNECT 无 `Padding` | 200 + 原始字节；但响应**仍带** `Padding` 头 | `inbound.go:609` | `VERIFIED_SOURCE` |
| H3 CONNECT | 同上（ALPN 仅 h3） | `quic/inbound_init.go:176-253` | `VERIFIED_SOURCE` |
| 客户端发 `padding-type: variant1` | 被忽略；仍按 `Padding` 存在性选 variant1 | `inbound.go:548` 不读该头 | `VERIFIED_SOURCE`（与 154 客户端回退规则一致） |
| 认证失败 | 407 + `Proxy-Authenticate: Basic realm="Caddy Secure Web Proxy"` | `inbound.go:774-780`；参考 `forwardproxy.go:274-277`（`h.ProbeResistance == nil` 时） | `VERIFIED_SOURCE`（双侧源码） |
| 纯 GET（preamble）无 masquerade | 我们 400；参考**转交给 next handler**（状态取决于 Caddyfile，forwardproxy 本身不产生固定状态码） | 我们：`inbound.go:502-505`。参考：`forwardproxy.go:259-270` —— `if h.Hosts.Match(r) && (r.Method != CONNECT \|\| authErr != nil) { ... return next.ServeHTTP(w, r) }` | `VERIFIED_SOURCE`（双侧源码）；**原报告的"参考返回 407"判断已在本轮纠正**（见 §1.5） |
| 未认证 CONNECT（无 probe_resistance） | 407 + challenge | 参考 `forwardproxy.go:271-277`；我们 `inbound.go:530-537`（无 masquerade 时） | `VERIFIED_SOURCE` |
| 未认证 CONNECT（probe_resistance 开） | 参考：`next.ServeHTTP`（伪装成不存在）或 407+HTML 隐藏页 | `forwardproxy.go:271-274,605-626` | `VERIFIED_SOURCE`（参考有、我们无此机制；我们用 masquerade，见 6.1b） |
| preamble + 旧客户端 | 可能失败，取决于客户端版本 | #793 在 v148 修复；我们无实测 | `NOT_RUN` |
| UoT v1/v2 | fork 扩展，非官方能力 | `docs/naive.md:42-67` | `VERIFIED_SOURCE` |

**关于第 8 行（本轮已纠正）**：先前版本把它写成"参考返回 407"，那来自本仓注释里的一个实测记录，而不是参考源码。本轮直接读 `forwardproxy.go@d62c80d3` 后确认：**非 CONNECT 请求在 `Hosts.Match` 为真时被交给 `next.ServeHTTP`**，forwardproxy 不决定它的状态码；407 只出现在"CONNECT + 认证失败 + 无 probe_resistance"这条路径上。因此这不是一个"400 vs 407"的固定差异，而是"**我们把非代理请求收敛成 400/masquerade，参考把它交给后续 handler**"的语义差异。生产配置有 masquerade 时两者对外表现都可以是正常网页。

### 6.1b 参考实现（Caddy forwardproxy @d62c80d3）的能力清单 —— 直读源码后的对照

| 参考能力 | 位置 | 我们的对应 | 判断 |
|---|---|---|---|
| 认证先于方法判定 | `forwardproxy.go:252-257` | 我们先判 method 再认证（`inbound.go:502,526`） | 差异；对 masquerade 部署无可观察影响（都先给出网页），对裸部署有状态码差异 |
| 非 CONNECT 交给后续 handler / PAC | `:259-270,491-500` | masquerade 或 400 | **设计差异**（我们不做通用前向代理，明确记载于 `docs/naive.md`） |
| `probe_resistance`（隐藏域 + 407+HTML 隐页） | `:76,605-626` | 无；用 masquerade 替代 | 能力差异，非缺陷；两者的"伪装"语义不同 |
| `hide_ip` / `hide_via`（Forwarded/Via） | `:60-66` | 无 | 对 CONNECT 无影响（这些头不进入隧道） |
| `hosts` 匹配器 | `:70` | 无；用 sing-box 路由规则表达 | 设计差异 |
| `acl` / `allowed_ports` / 自建 dialer | `:78-84,502-568` | 用 `route` 规则 + `packet_destination_guard` | 设计差异；我们的 ACL 更细（逐 datagram） |
| `upstream` 代理 / `max_idle_conns` / `dial_timeout` | `:72-80` | 用 sing-box 的 dialer/detour 与超时 | 设计差异 |
| 200 先发、再拨目标（CONNECT Fast Open） | `:290-310` | **相同**：200 + Flush 后才 `newConnection` | `VERIFIED_SOURCE` 一致 |
| 200 后 flush 失败 → 500 | `:323-327` | 我们记录并**不建隧道**（200 已在线上，无法改状态码） | 实质等价 |
| H1：hijack 后先把 `brw.Reader` 缓冲字节写给目标，再双向拷贝 | `:629-655` | `hijackedConn` 把缓冲字节接在隧道读出侧——**语义等价、时序更安全** | `KEEP`（我们更好） |
| H1：payload **不加帧** | `:645-651`（`dualStream(..., false)`） | `inbound.go:701-704` | 一致 |
| H2/H3：`Padding` 请求头决定 framing | `:352` | `inbound.go:548` | 一致 |
| 响应 `Padding` 头无条件 | `:308-320` | `inbound.go:609` | **逐字节一致**（含长度公式与字母表） |
| 8 帧 padding 窗口 | `:654-658` | `paddingCount = 8` | 一致 |
| 帧格式 `[2B size][1B pad][data][zeros]`，`maxRead = 65536-3-pad` | `:700-720` | `inbound_conn.go:295-328` | 一致 |
| 短写检测（`nr != nw` → `io.ErrShortWrite`） | `:737-744` | `writeFull` | 一致 |
| 零数据帧：`if nr > 0` 才写帧（故不发零数据帧） | `:704-716` | 我们同样不发；**读取侧额外硬化**（`inbound_conn.go:150-154`） | 我们更稳 |
| 零数据帧读取侧缺陷：`nr==0` 时不消费 padding → 后续帧错位 | `:712-726` | 我们已修 | **我们更好**（`padding_zero_frame_test.go`） |
| 每帧 flush（`ResponseController.Flush`，失败即断） | `:733-745` | `naiveH2Conn.Write` | 一致 |
| 无端口字面量校验（靠 HTTP 层/dial 失败） | `:311-315` | 我们显式校验（`inbound.go:583-589`） | 我们更严；注释里"参考返回 400"应理解为**Caddy HTTP 层**的行为，而非 handler |
| HTTP/2 资源边界（MaxConcurrentStreams / 两条 receive window / IdleTimeout） | forwardproxy **自己一个都不设**；由 Caddy 的 http server 默认值决定（本仓未检出 Caddy 本体，故不下断言具体数值） | 我们把它做成可配置并校验区间（`HTTP2Options`） | 能力差异；我们更可控，**但不能因此声称"参考没有边界"** |
| H3 流控 / QUIC 参数 | forwardproxy 不设；由 Caddy 的 http3 server 与 quic-go 默认决定（该 commit 的 `go.mod` 是 `quic-go v0.44.0`） | 我们显式 pin QUIC 版本、默认不启用 0-RTT、并发流默认 100 | 能力差异；见附录 D |
| 连接上下文取消 / 关闭顺序 / 请求清理 | `dualStream` 在 client→target 方向结束时 `CloseWrite` 并返回；另一方向 goroutine 收尾（`:664-687`） | 我们交给 route 的 copy/close 语义（`route/conn.go`），H2 侧 `waitForClose` + `wrapper.CloseWrapper()` | 语义等价，实现不同 |
| QPACK 语义（H3 的 padding 头） | 与 H2 的 HPACK 同理：随机值使动态表无法稳定复用；参考未做 QPACK 特化 | 我们同样只是随机值，未做 QPACK 特化 | 一致（都是"靠随机性"，不是"靠协议控制"） |

### 6.2 本 fork Cronet 客户端 → Caddy/forwardproxy

| 场景 | 预期 | 证据 | 状态 |
|---|---|---|---|
| H2（`quic: false`，默认） | 与参考客户端相同的 H2 CONNECT | `naive_client.go:486-491` + `bidirectional_stream.cc` | `VERIFIED_SOURCE` + 本机对自家服务端 `MEASURED_LOCAL` |
| H3（`quic: true`） | H3 CONNECT，`-force-quic` | `naive_client.go:580-582` | `VERIFIED_SOURCE` + `MEASURED_LOCAL` |
| Padding 头 | 每次随机 **30..61** 字节 + Caddy 的 16 字符集（**官方客户端是 16..32 + HPACK 字符集**，见 §5.5） | `naive_conn.go:46-58` | `VERIFIED_SOURCE`（与官方客户端不同，互操作不受影响） |
| `padding-type` 请求头 | Cronet 客户端**不发**（Chromium 侧只发 `Padding`） | `bidirectional_stream.cc` 只解析 `-force-quic`/`-network-isolation-key`；`spdy_http_utils.cc` 只处理 `-connect-authority` | `VERIFIED_SOURCE` |
| UoT | 客户端只用 `uot.Client` 包一层 TCP 隧道 | `outbound.go:231-244` | `VERIFIED_SOURCE` |
| 真实 Caddy 互通 | 未知 | 需要 Caddy + forwardproxy@naive | `NOT_RUN` |

### 6.3 本 fork 客户端 → 本 fork 服务端（本轮实测）

| 场景 | 结果 | 数值 |
|---|---|---|
| H2 | 200，`protocol: h2` | connect 5–8 ms（5 次），首响应 2–3 ms，1080 B |
| H3 | 200，`protocol: quic/1+spdy/3` | connect 6–7 ms（5 次），首响应 1–2 ms，1080 B |
| 错误密码 H2/H3 | `unexpected response status: 407` | 7 ms |
| forced-H3 对 TCP-only 服务端 | 失败，**不回落** | 4.018 s，`quic protocol error` |
| forced-H3 对 UDP 黑洞 + 活 H2 | 失败，**不回落** | 4.009 s，`quic protocol error` |

> 这组数字**只**说明 loopback 上的建连与首响应，不代表 WAN 吞吐，也不代表官方客户端互通。

---

## 7. Cronet Native engine & performance

### 7.1 Engine 配置（pin 的真实值）

`cronet-go/naive_client.go:468-539`（`startEngine`）：

```
QUIC=true : SetEnableQuic(true)                       // HTTP2 保持 C API 默认 true
QUIC=false: SetEnableQuic(false); SetEnableHTTP2(true)
两者都   : SetAsyncDNS(true); SetDNSServerOverride(["127.0.0.1:53"]);
           SetUseDnsHttpsSvcb(ECH enabled)
           SetSocketPoolOptions(2048, 2048, 2040)
QUIC=true : SetQUICOptions("", ccTag, streamWindow=6MiB, sessionWindow=15MiB)
QUIC=false: SetHTTP2Options(receiveWindow, receiveWindow/2)
            iOS 默认 4 MiB，其余平台 128 MiB
```

`SetQUICOptions` 的拥塞控制 tag 采用 `TBBR`/`B2ON`/`CQBC`/`RENO`，注释解释了为什么 Cubic 必须用 `CQBC`（`naive_client.go:28-37`）——这是一个真实的、非显然的上游知识。

### 7.2 协议选择是 per-CONNECT，而不是 per-Engine（本轮最重要的结构发现）

源码链（SagerNet/naiveproxy@97ae698，Chromium 154 补丁）：

1. `src/net/http/bidirectional_stream.cc:219-222`：`-force-quic` 被**读取并从 header 集合移除**，写入 `http_request_info.force_quic`。
2. `src/net/http/http_stream_factory_job.cc:145-153`：`using_quic_ = ... || request_info.force_quic || ...`。
3. 同文件 `:1074-1076`：`if (using_quic_) { if (result < 0) { return result; } ... }` —— **失败直接返回，没有任何跨协议重试**。
4. `src/net/http/http_stream_pool_job.cc:41-46`：`group->force_quic()` 时 `allowed_alpns = {QUIC}`；但 `SwitchToHttpStreamPool()` 要求 `stream_type_ == HTTP_STREAM`（`http_stream_factory_job_controller.cc:1636-1640`），**BidirectionalStream 永远走不到 pool 分支**。
5. `GetProtocol()` 只有两种实现：`BidirectionalStreamSpdyImpl`（H2）与 `BidirectionalStreamQuicImpl`（H3）；没有 H1 实现。

实测（`MEASURED_LOCAL`）：

```
RAW-H2: params defaults quic=true http2=true
RAW-H2: RESULT=ok headers=4ms status="200"      ← 不带 -force-quic
RAW-H3: params defaults quic=true http2=true
RAW-H3: RESULT=ok headers=6ms status="200"      ← 带 -force-quic
        stderr: response received, protocol: quic/1+spdy/3
```

**结论**：`-force-quic` 是"每个 CONNECT 的传输选择器"，不是引擎级开关。单 Engine 同时开启 QUIC 与 H2 是**默认状态**，H2 分支从未被禁用。

### 7.3 H3 失败时的真实行为（本机实测）

| 场景 | 结果 | 耗时 |
|---|---|---|
| QUIC=true，服务端 UDP 端口关闭（ICMP refused），同端口 H2 活 | `quic protocol error` | **4.018 s** |
| QUIC=true，UDP 被 socket 黑洞（无响应），同端口 H2 活 | `quic protocol error` | **4.009 s** |
| QUIC=true，服务端 `network: tcp+udp` 正常 | 200 h3 | 6–7 ms |
| QUIC=false，同服务端 | 200 h2 | 5–8 ms |

两个失败场景停在**同一个 ~4.0 s 预算**上，说明这是 Chromium 的连接尝试预算（不是应用超时），并且 **UDP 被拒与 UDP 黑洞对用户是同一个代价**。这对回退设计很关键：回退窗口不需要自己发明 15 s 的猜测值，但也**不能**假设"黑洞会更慢"。

> 注意：这是 loopback。真实移动网络下 QUIC 握手可能触及同一预算但 RTT 更高，`4.0 s` 不应被写成普适常数。

### 7.4 建连与首响应（loopback，仅作对照，不作吞吐结论）

| | H2 | H3 |
|---|---|---|
| connect（5 次） | 5,8,7,5,5 ms | 7,6,7,6,7 ms |
| 首响应 | 2–3 ms | 1–2 ms |
| 错误密码 | 7 ms → 407 | 7 ms → 407 |

**H3 在 loopback 上没有优势**，与 #756 作者的判断一致。

### 7.5 Engine 数量 × 连接池隔离 × 常驻内存（本机实测，空闲态）

| 形态 | WorkingSet | Private | 线程 | 句柄 |
|---|---|---|---|---|
| `insecure_concurrency: 1`（1 engine） | 15.9 MiB | 14.9 MiB | 17 | 235 |
| `insecure_concurrency: 4`（4 engines） | 16.4 MiB | 15.2 MiB | 23 | 279 |
| `insecure_concurrency: 4` + `single_engine: true`（1 engine + 4 NIK） | 15.9 MiB | 14.6 MiB | 16 | 229 |

**解读（必须谨慎）**：

- 4 个 engine 相对 1 个只多 **+0.5 MiB / +6 线程 / +44 句柄**。原因是 `libcronet.dll` 的代码段在进程内**共享映射**，每个 engine 的增量主要是自己的 network thread、socket pool 与配置对象。
- 这只测了**空闲**态，且只建了**一条**隧道（只有被选中的那个 engine 真的工作）。有流量、有 QUIC session、有 H2 stream 时的增量**未测**。
- 因此**不能**用"4 个 engine 只要 0.5 MiB"去论证单 Engine 优化没有价值；也不能用"4 套 Chromium 网络栈"去论证多 Engine 一定很贵。两种说法都超出本测量。
- 真正的差异可能是**连接池隔离语义**（每个 engine 一套 H2 session/QUIC session），而不是常驻字节数。`docs/ENGINEERING-NOTES.md:87` 已经把这一点写成"必须真实网络 A/B 才能改默认"，本轮维持该判断。

### 7.6 DoH bootstrap（本机实测，与 #3803 完全一致）

每次 `client.Start()` 都出现（H2/H3 都有）：

```
[debug] open UDP connection to [2001:4860:4860::8888]:443
[error] ... dial udp [2001:4860:4860::8888]:443: connect: A socket operation was attempted to an unreachable network.
```

- 本机系统 DNS 是 `10.8.8.8 / 10.8.4.4`，**不是** Google，因此这不是"系统 DNS 被识别为 Google 后升级"的唯一触发条件——它更像该 build 的默认 DoH bootstrap 行为。
- 该 dial 经 cronet 的 UDP dialer 回调进入 sing-box 的 dialer，所以**受路由约束**；但它**不经过** `dns_router`，即不产生 sing-box DNS 日志/规则命中。
- 在本机 IPv6 不可达，所以每次都以 WARN/ERROR 结束；在双栈网络里它会成功，且用户无法配置。
- `SetDNSServerOverride` 只覆盖明文 nameserver，这点 #3803 的分析与我们的实测一致。

### 7.7 未测试的内容

- macOS/iOS 多 Engine 的真实 `footprint`、FD、初始化时间：`NOT_RUN`（无环境）。
- 进程长期运行后网络切换的资源回收：`NOT_RUN`。
- H2 接收窗口 sweep（4/8/16/32/64/128 MiB）与 H3 session window：`NOT_RUN`。脚本存在：`scripts/ci/bench-naive-receive-window.sh`。
- 多 Engine 对单 Engine 的吞吐/并发：`NOT_RUN`。脚本存在：`scripts/ci/bench-naive-cronet-engine.sh`。

---

## 8. Go inbound performance

### 8.1 本轮实测（`MEASURED_LOCAL`，Windows amd64，16 逻辑核）

命令：`go test -run XXX -bench 'BenchmarkPadding|BenchmarkHeadroom|BenchmarkCachedFirstPayload' -benchmem -count=3 -benchtime=20000x ./protocol/naive/`

| Benchmark | ns/op（3 次） | B/op | allocs/op |
|---|---|---|---|
| `PaddingWriteFramed`（1400 B，第 1 帧） | 见注 | – | – |
| `PaddingWriteRaw`（窗口后） | 201.5 / – / – | 64 | 1 |
| `PaddingWriteUnpadded` | 17.4 / 17.3 / 18.2 | 0 | 0 |
| `PaddingWriteChunked`（1400 B，新 buffer 路径） | 5255 / 5399 / 5246 | 265 | 4 |
| `PaddingWriteBuffer/small-64B` | 185 / 188 / 187 | 0 | **0** |
| `PaddingWriteBuffer/medium-1400B` | 189 / 185 / 189 | 0 | **0** |
| `PaddingWriteBuffer/large-16KiB` | 178 / 176 / 180 | 0 | **0** |
| `PaddingWriteBufferFramed/small-64B` | 274 / 284 / 281 | 0 | **0** |
| `PaddingWriteBufferFramed/medium-1400B` | 292 / 285 / 287 | 0 | **0** |
| `PaddingWriteBufferFramed/large-16KiB` | 264 / 258 / 288 | 0 | **0** |
| `CachedFirstPayload/copying/1400B` | 668 / 728 / 681 | 64 | 1 |
| `CachedFirstPayload/buffer/1400B` | 316 / 336 / 317 | 0 | **0** |
| `CachedFirstPayload/copying/16KiB` | 933 / 907 / 964 | 67–69 | 1 |
| `CachedFirstPayload/buffer/16KiB` | 342 / 327 / 308 | 0 | **0** |
| `HeadroomDecisions` | 0.25–0.30 ns | 0 | 0 |

注：`PaddingWriteFramed` 的前几行被输出截断，未取得可信数字，**不引用**。

**如何解读这些数字（避免制造漂亮假数字）**：

- 这些是 **Go 层 CPU**。`io.Discard` 作为 writer，所以 `MB/s` 列只反映 memcpy/循环成本，**不是**网络吞吐，也**不是**用户体感。
- 真正值得记的是**分配数**：`WriteBuffer` 在 framed 与未 framed 两种状态下、64 B/1400 B/16 KiB 全部 **0 alloc**；`writeChunked`（绕过 buffer 的路径）每 1400 B 要 4 alloc / 265 B。
- 因此"让 copy 循环走上 `WriteBuffer` 且几何匹配"是唯一有意义的优化方向，而这个方向**已经做完**（`route/conn.go:1062-1116` + `EarlyCopyBufferGrowth`）。
- `CachedFirstPayload`：buffer 移交路径 0 alloc，copy 路径 1 alloc；16 KiB 时 316 ns vs 933 ns。这是"消除一次明文拷贝"的量化，**不是**吞吐提升承诺。

### 8.2 已实现的服务端热路径（不要重做）

- in-place framing（`ExtendHeader(3)` + `WriteZeroN(padding)`）：`inbound_conn.go:253-260`。
- `writerMTU = 65278`、front 3、rear 255，使 `3+65278+255 = 65536` 恰好一帧：`inbound_conn.go:356-361,330-342`。
- 早期增长：`EarlyCopyBufferGrowth()`（`inbound_conn.go:417,510`），route 层按**方向**选择（`route/conn.go:412-414`）。
- H2 用 `ResponseController`，flush 失败即断：`inbound_conn.go:449-476`。
- 计数/限流在 accept 层，不进入数据路径（nil limiter 时零锁）：`inbound.go:189-197`。

### 8.3 与 Caddy 的对照（`NOT_RUN`）

没有 Caddy/forwardproxy 参考二进制；`test/jiejie/jiejie_naive_caddy_differential_test.go` 在没有参考二进制时会 **SKIP 并明确说明不是 PASS**。需要的条件与命令见 §12.4。

---

## 9. Integration with common sing-box

> 四列：`已有能力` / `Naive 当前接线` / `真实风险与证据` / `建议`。

### 9.1 DNS

| 列 | 内容 |
|---|---|
| 已有能力 | `adapter.DNSRouter.Exchange`；`actionResolve` 先解析后检查；`route/packet_destination_guard.go` 逐 datagram 复查；DNS 规则/hijack |
| Naive 接线 | `outbound.go:150-165` 仅当 `ServerAddress != ServerName` 时把 A/AAAA 重定向到配置地址，并注入 ECH、过滤 ipv4hint/ipv6hint（`naive_dns.go:291-514`）；ECH 关闭时 `SetUseDnsHttpsSvcb(false)`；DNS 服务器固定 `127.0.0.1:53` 由 socket 回调截获 |
| 真实风险 | ① **DoH bootstrap 不经过 dns_router**（实测，§7.6）；② 代理域名本身就是 `ServerName` 时，解析由 Chromium 走该 bridge，受策略约束（OK）；③ 当 `ServerName` 是本机名（如 `localhost`）时 Chromium 会先试 `::1`，TCP 会回退、QUIC 不回退（实测：`open UDP connection to [::1]:18443` 后直接失败）——这是一个**真实但不常见的部署形态风险** |
| 建议 | `IMPROVE`（候选 P2）。同时把"server_name 解析出多地址时 QUIC 只试第一个"写进已知边界 |

### 9.2 Dialer / Detour

| 列 | 内容 |
|---|---|
| 已有能力 | `dialer.NewWithOptions{RemoteIsDomain:true, ResolverOnDetour:true, NewDialer:true}`；detour 链；`ResolverOnDetour` |
| Naive 接线 | `outbound.go:105-114`；socket 回调把 Chromium 的 TCP/UDP 建连原样交给该 dialer（`naive_client.go:330-445`）；`dialer.ResolveDialer` 在构造期断言（`outbound.go:150-153`） |
| 真实风险 | 双解析**不存在**：Chromium 解析一次，回调拿到的是 IP 字面量（`outbound.go:134-149` 注释 + cronet 文档契约）；`TryForceUDP`/socket pair 在非 `*net.TCPConn`（例如经 detour 的代理连接）时启用，多一次用户态拷贝——这是正确性优先的取舍，不是缺陷 |
| 建议 | `KEEP` |

### 9.3 生命周期

| 列 | 内容 |
|---|---|
| 已有能力 | `adapter.StartStage`、`adapter.Scope`、`InterfaceUpdated`、`Close` 幂等 |
| Naive 接线 | `Start` → `client.Start()` + `scope.Add(client.Close)`（`outbound.go:254-265`）；`InterfaceUpdated` → `CloseAllConnections()`（`:290-292`）；`Close` → `doClose()` 先 `CloseAllConnections` 再等 `activeConnections` 归零再 `Shutdown/Destroy`（`naive_client.go:670-687`） |
| 真实风险 | ① `InterfaceUpdated` **不清除**任何 H3 verdict——因为 Naive 根本不使用 `transport/http` 的 verdict（这是"没有状态就没问题"，不是"已正确重置"）；② `CloseAllConnections` 会取消进行中的流，`OnCanceled` → `terminate(context.Canceled)`，上层会把用户取消/网络切换当成 `context.Canceled`，**不应**被计入路径故障（见 §9.4）；③ `trackedNaiveConn.release` 用 `closeOnce` + `setOnTerminate`，close 与 terminate 双路径都只 Done 一次（`naive_client.go:695-714`） |
| 建议 | `KEEP`；若要接回退，必须显式区分"网络切换导致的取消"与"传输失败" |

### 9.4 故障事实（FailureFacts）

| 列 | 内容 |
|---|---|
| 已有能力 | `adapter/failure.go`：`FailureStage{Dial,Handshake,Target,Data}` × `FailureOrigin{...}`；`WrapFailure`/`FailureFactsOf`；消费方：`protocol/group/loadbalance_failover.go:160,287`、`urltest.go:1181` |
| Naive 接线 | **无**。`Outbound.DialContext` 直接返回 `client.DialEarly` 的错误原样（`outbound.go:267-281`），`uotClient` 同理 |
| 真实风险 | 分组层只能按 errno/字符串启发式判断：`quic protocol error`（`cronet.NetError`）与目标拒绝、认证 407（`unexpected response status: 407`）在 Naive 边界上**没有被标注 stage/origin**。按 §9.3 的实测错误形状，可实现但不实现 |
| 建议 | `IMPROVE`（候选 P1）。先例：`protocol/mieru/fallback.go:419-430`；测试先例：`protocol/mieru/early_failure_test.go` |

### 9.5 统计

| 列 | 内容 |
|---|---|
| 已有能力 | route 层 copy 循环负责计数；`WriteBuffer`/`ReaderReplaceable` 让计数不被 wrapper 绕过 |
| Naive 接线 | `naiveConn`/`naiveH2Conn` 实现 `WriteBuffer`、`FrontHeadroom`、`RearHeadroom`、`WriterMTU`、`ReaderReplaceable`、`WriterReplaceable`、`Upstream`；客户端 `trackedNaiveConn` 声明 `Reader/WriterReplaceable=true` 并暴露 `Upstream()` |
| **本轮实测（`MEASURED_LOCAL`，已从 `NOT_PROVEN` 升级）** | 起 `naive` inbound + `direct` outbound + `clash_api`，用原生 Cronet 客户端跑一次 H2 隧道请求，在隧道仍打开时读 `/connections`：`{"upload":41,"download":1078,"type":"naive/naive-in","rule":"final","chains":["direct"],"metadata":{"destinationIP":"127.0.0.1","destinationPort":"18080","network":"tcp","sourceIP":"127.0.0.1"}}`。两次相隔 6 s 采样数值一致（传输已完成）。41 B 正好是请求字节数，1078 B 是响应头剩余部分 + 960 B 正文——方向与量级都对 |
| 结论 | **计数确实生效，且没有被 wrapper 绕开**；`type` 字段正确标识为 `naive/naive-in`，`rule: final` 说明它走的是正常路由判定 |
| 残留未测 | 只验证了 TCP/H2 一条；**UoT 的逐 datagram 计数、H3 连接上的计数、以及"一条物理连接被计成多条逻辑通道"的重复计数**未验证。命令见 §12.4 |

### 9.6 Buffer / Copy 快路径

| 列 | 内容 |
|---|---|
| 已有能力 | `adapter.CopyBufferGrowthTuner`、`N.CachedReader`、headroom 求和、`WriteOwnedBuffer` |
| Naive 接线 | 服务端 `EarlyCopyBufferGrowth` 只作用于**下载方向**（writer 是自己）；客户端 `naiveConn.EarlyCopyBufferGrowth` 作用于上传方向；cached first payload 在几何匹配时直接 `WriteBuffer` 移交（`route/conn.go:1062-1100`） |
| 真实风险 | `writeCachedBuffer` 的三重几何检查（MTU/front/rear）任一不满足就退回 `Write`，**这是正确的**；若有人为了"消掉这次 fallback"放宽检查，会破坏帧几何 |
| 建议 | `KEEP` |

### 9.7 资源配额与安全

| 列 | 内容 |
|---|---|
| 已有能力 | inbound `server_limits`、ACL/SSRF 规则、UoT 逐 datagram guard、`packet_destination_guard` 只在 read 侧 |
| Naive 接线 | 全部保留；`max_connections_per_ip` 用真实 peer；masquerade 仅 Web，永不建隧道；`Proxy-Authorization` 在 masquerade 前被删除（`inbound.go:784-790`） |
| 真实风险 | 无新增；唯一需要注意的是**回退不能削弱**上面任何一条（认证失败绝不能被当成"传输坏了"而重试到另一条路径） |
| 建议 | `KEEP` |

### 9.8 故障恢复 / 网络代际

| 列 | 内容 |
|---|---|
| 已有能力 | `InterfaceUpdated`、`ResetConnections`、H3 verdict 的 `resetHTTP3Verdict`（attempt 序号 + 清零在同一临界区） |
| Naive 接线 | 只用了 `CloseAllConnections`；**没有**接 `ResetConnections` 语义（因为不走 `transport/http.Client`） |
| 真实风险 | 若未来加回退记忆，**必须**绑定到现有的网络代际信号，而不是 Naive 自造一套"上次失败时间"。现状是：`adapter/network.go:239` 的 `InterfaceUpdated(ctx)` **不带 generation**；`common/netcap` 里的 "generation" 是地址族能力快照（`netcap.go:9,129-150`），不是可供 outbound 消费的变更计数。因此"最小改造"是让 `InterfaceUpdated` 在调用 `CloseAllConnections` 的同时清零本地 verdict（保守方向：同代际内的抖动也会清），而不是引入新的代际 API |
| 建议 | `NOT_PROVEN` + 设计约束 |

---

## 10. H3→H2 fallback feasibility

### 10.1 现有公共机制到底暴露了什么

| 机制 | 位置 | 形态 | 可复用性 |
|---|---|---|---|
| `http3Available` / `beginHTTP3Attempt` / `claimHTTP3Outcome` / `markHTTP3Broken` / `clearHTTP3Broken` / `resetHTTP3Verdict` / `noteHTTP3AmbiguousOutcome` | `transport/http/client.go:295-449` | **`*Client` 的未导出字段 + 方法**（`http3Broken`/`http3Backoff`/`http3Attempt`/`http3Outcome`/`http3VerdictMu`） | 语义可借鉴；**不是公共 API** |
| 窗口与退避 | `client.go:655-677` | 3 s 建连窗口；5 s → 5 min 指数退避；caller 取消不记账；strict 模式覆盖所有失败方式 | 与 Naive 需求最接近 |
| 隧道版 | `tunnel_client.go:97-215` | 独立 3 s 窗口；`errHTTP3ResponsePending` → ambiguous；`ErrHTTP3Unavailable` → broken | 分类思想可直接搬 |
| 第二套（普通 HTTP 请求） | `common/httpclient/http3_transport.go:291-376` | per-authority map；broken 5 min → 48 h；happy-eyeballs race + `fallbackDelay` | 语义**不同**，不可混用 |

**关键回答（提示词 §7.1 的问题）**：这套逻辑**不是**公共组件已暴露的 API，而是公共 HTTP Client 的内部成员。但它的**决策核**很小且无 I/O：`attempt 序号 + deadline + 退避步长 + claim/effect 同一临界区`。抽取成 `common/??` 下的纯结构是可行的，**代价**是要同时改两套调用方（`transport/http` 与 `common/httpclient` 的语义并不相同，强行统一会改行为）。

### 10.2 Cronet 的真实能力与契约（已证）

| 问题 | 答案 | 证据 |
|---|---|---|
| H3 不通时 Chromium 会自动 H2 fallback 吗？ | **不会**（在 forced-QUIC 路径上） | 源码 §7.2 + 实测 4.0 s 失败 |
| 一个 Engine 内能否对单次 CONNECT 选 H3/H2？ | **能**，`-force-quic` header 的存在性 | 实测 `RAW-H2`/`RAW-H3` 同一引擎构造 |
| 是否有正式的请求级 API？ | 没有公开 C API；但它已经是 fork 的 header 契约（`IsReservedNaiveHeader` 里就有 `-force-quic`） | `naive_client.go:74-80` |
| 关闭 `-force-quic` 是否保持 Naive CONNECT 语义？ | 是，只是走 H2 的 `BidirectionalStreamSpdyImpl`；`-connect-authority` 仍在 `spdy_http_utils.cc:216` 生效 | 源码 + 实测 |
| **不带 `-force-quic` 就一定是 H2 吗？** | **不一定**。若该 origin 有 Alt-Svc 记录且引擎 `enable_quic=true`，Chromium 仍可能选 QUIC——这正是 cronet-go#11 报出的真实故障（`quic:false` 下仍出现 `protocol=quic/1+spdy/3`）。我们的服务端不发 Alt-Svc，所以实践中不会；但方案 B 必须把这条写进契约 | `naive_client.go:488-491`（已修）+ cronet-go#11 |
| 上游是否已有"QUIC↔H2 自动降级"的 Feature/PR？ | **没有；而且维护者明确拒绝过相邻提案**：naiveproxy #669 请求"同一配置同时用 https 与 quic"，OWNER 回答"应该由客户端指定协议，这样利于控制性能"，not planned 关闭 | #669 正文与评论 |
| `DialEarly` 返回时是否已得到 CONNECT 200？ | **否**。`DialEarly` 只 `Start()` 流；200 在 `HandshakeContext`/`Handshake` 里 `WaitForHeaders`（`naive_conn.go:354-382`，`naive_client.go:617-631`） | 源码 |
| 已缓存的首包会不会已经写出？ | 会。`route/conn.go:950-1005` 在 `NeedHandshakeForWrite` 为真时先握手，但 `serverFirst`/cached buffer 分支可能先写出数据 | 源码；**限制回退只能发生在新 CONNECT 建立阶段** |
| 错误能否区分 DNS/UDP 黑洞/QUIC/TLS/status/取消？ | **部分**：传输失败是 `cronet.NetError`（typed int）；HTTP status 是 `E.New("unexpected response status: ...")`（字符串）；caller 取消是 `context.Canceled`；EOF 是 `io.EOF`。**没有**"UDP 黑洞"这一分类——它与"QUIC 握手失败"在 4.0 s 后同形 | 源码 + 实测 |

### 10.3 四方案对比

| 方案 | 核心思想 | 真实成本 | 结论 |
|---|---|---|---|
| **A. 依赖 Chromium 自身回退** | 不加 Go 策略 | 需要服务端在 CONNECT 200 上广告 `Alt-Svc: h3=...`。但 `BidirectionalStream::OnHeadersReceived` 调 `ProcessAlternativeServices(session_, NetworkAnonymizationKey(), ...)` 用的是**空 NAK**，而单 Engine + NIK 模式下请求用非空 NAK，条目对不上；且 forced-QUIC 的 ALPN 集合被 `CalculateAllowedAlpns` 收成 `{QUIC}`，本请求根本不会看 H2 | **不可用**（`VERIFIED_SOURCE`）。不是"没试过"，是路径不成立 |
| **B. 单 Engine + 公共 H3 verdict** | 建立时按记忆选 H3/H2 | 无需 Native patch：`-force-quic` 的加与不加就是选择器。需要：(a) cronet-go 增加 per-dial 开关（`DialEarly` 变体或 option），(b) Naive 侧一个"哪次失败算传输失败"的分类器，(c) 一个绑定网络代际的记忆。**新增硬约束（来自 cronet-go#11）**：在 `enable_quic=true` 的引擎上"不带 `-force-quic`"**不等于**"H2 only"——若该 origin 存在 Alt-Svc 记录，Chromium 仍可能选 QUIC。我们的服务端不发 Alt-Svc，故实践中是 H2，但设计文档必须写明 | **可行且最小**。见 §10.4 |
| **C. H3 优先 + 懒加载 H2 NaiveClient** | 复用两套原生执行器 | 第二个 Engine（常驻成本本机空闲态 +0.5 MiB/+6 线程，但**有负载时未测**）；两个 Engine 各自的连接池；Close/待决请求双份；macOS 上等于把 engine 数翻倍 | 可行但**重**。只在 B 被否时选 |
| **D. 不实现** | 保留显式 H2/H3 | 0 成本；用户手动切换 | **参考实现的取向**：naiveproxy #669 中维护者明确说"应该由客户端指定协议，这样利于控制性能"，并以 not planned 关闭。默认推荐 D，除非 §14 的试验证明收益 |

### 10.4 方案 B 的最小设计（本轮不施工）

```
[新 CONNECT 建立阶段，且在写出任何应用字节之前]

  attempt := verdict.begin()                // 序号取自网络代际内的单调计数
  if verdict.available() {                  // 未处于 broken 窗口
      conn, err := client.DialEarly(ctx, dest, WithForceQUIC(true))
      if err == nil { verdict.clear(attempt); return conn }
      if ctx.Err() != nil { return err }    // caller 取消：不记 fact、不记 verdict
      if !isTransportFailure(err) { return err }   // 407/status/认证：绝不降级
      verdict.markBroken(attempt)           // 有界窗口，非永久
  }
  return client.DialEarly(ctx, dest, WithForceQUIC(false))   // H2，同一 Engine
```

必须写死的约束：

1. **默认关闭**。新增字段建议 `quic_fallback_to_h2`（bool，默认 false），`quic: false` / `quic: true` 的既有语义**完全不变**。
2. **只在建立阶段**。一旦 `HandshakeContext` 返回成功、或 cached buffer 已写出，就**不允许**重试或迁移。
3. **分类要窄**：只有"连接/握手层的传输失败"（`cronet.NetError` 的 QUIC/TCP 建连类 + 4 s 窗口超时）才算 H3 broken。`407`、`unexpected response status`、`context.Canceled`、`io.EOF`、写超时**都不算**。
4. **记忆要新**：attempt 序号在**发起前**取得，旧 attempt 的迟到失败不能覆盖新成功。这条直接照抄 `transport/http/client.go:313-349` 的理由——它已经用 200000 次对抗测出了"不这么做会怎样"。
5. **一次事件一步退避**：用 CAS 认领窗口，而不是每个失败拨号都 `×2`（同 `client.go:387-433` 的实测理由：16 并发拨号一次性打到 5 min 上限）。
6. **网络切换清零**：现在没有现成的 generation 可读。最小做法是让 `InterfaceUpdated` 同时调用一个 `resetVerdict()`（与 `CloseAllConnections` 同一个回调里），并接受"同代际内的多次接口抖动会重置记忆"这一保守方向。
7. **只回退到 H2，不暗中允许 H1**：`BidirectionalStream` 没有 H1 实现，构造上就不可能（`VERIFIED_SOURCE`）。所以"回退到 H2"是安全默认。
8. **保留原生数据面**：两条路径都是 Chromium；不引入 Go TLS/H2。

### 10.5 代码量估算（前提写清）

| 仓库 | 文件 | 改动 | 估算 |
|---|---|---|---|
| cronet-go | `naive_client.go` | `NaiveClientOptions.QUIC` 从构造期常量变成 per-dial 参数（新增 `DialEarlyWith(ctx, dest, opts)`，旧 `DialEarly` 保持行为） | 40–70 行 |
| cronet-go | `naive_client_test.go` / 新增 | header 选择的红测 | 30–60 行 |
| sing-box | `protocol/naive/outbound.go` | 新 option + 分类器 + 调用 | 60–100 行 |
| sing-box | `protocol/naive/*_test.go` | 分类矩阵（407/取消/黑洞/超时/成功）+ verdict 顺序 | 120–200 行 |
| sing-box | `option/naive.go` + docs + schema | 字段与文档 | 20–40 行 |
| **合计** | | | **≈ 270–470 行**（不含端到端试验） |

维护风险：**低**。不触碰 Chromium 补丁、不新增 Native 依赖、不改变默认行为；失败时删掉一个 bool 即可回滚。

### 10.6 性能价值

- 回退的收益是**可用性**，不是吞吐：本机 H2 5–8 ms vs H3 6–7 ms，无差别；#756 作者也说 QUIC 不如 TLS 快但也"不差"。
- 代价是**一次 4.0 s 的失败窗口**（仅在 H3 真坏、且窗口未记忆时）。若 H3 故障是持续性的，退避会让后续新 CONNECT 直接走 H2，代价摊薄。
- 因此**值不值得做**，取决于"部署中 H3 故障频率"。没有真实数据，这也是为什么 §15 把"是否接受一次 4 s 首连等待"列为决策问题。

### 10.7 结论

**`RECOMMEND`（方案 B，默认关闭）+ 关键证据未到位：真实网络中 H3 故障率与 4 s 首连等待的用户可接受度。**

---

## 11. Security and lifetime invariants

| 不变式 | 现状 | 证据 |
|---|---|---|
| 认证前不得建立隧道 | parse → auth → tunnel；masquerade 无数据面访问 | `inbound.go:491-717`，`serveWebOrReject:729-740` |
| `Proxy-Authorization` 不得进入 masquerade 后端 | 显式 `Del` | `inbound.go:784-790` |
| 源地址只取真实 peer | `RemoteAddr`，不读 XFF | `inbound.go:629-650` |
| 目标端口非法字面量必须拒绝 | `SplitHostPort` + `ParseUint(...,16)` | `inbound.go:583-589` |
| `Padding` 头不得被 `extra_headers` 覆盖 | 双向共用 `IsReservedNaiveHeader`，大小写不敏感 | `naive_client.go:74-106`、`outbound.go:88-94,374-382` |
| Native 回调不得在 buffer 释放后运行 | pin 在 native 调用前，unpin 只在 `OnReadCompleted`/`OnWriteCompleted`/终态 | `bidirectional_conn.go:92-119,184-226,447-485` |
| 失败不得被当成成功 | 短写 `io.ErrShortWrite`；flush 失败向上返回；`writeFrame` 不前进计数 | `inbound_conn.go:188-215,449-476` |
| 回退不得把认证失败/目标拒绝当传输故障 | 本轮**未实现**回退；设计约束已写死（§10.4 第 3 条） | — |
| `Close` 后不得再创建 stream | `closed` 在 teardown 前置位，入口全部检查 | `transport/http/client.go:626-653` 是同型问题的先例；Naive 自己用状态机挡住：`naive_client.go:557-572` 在 RUNNING 之外先等 `started` 再复查 |
| 0-RTT | H3 服务端**不启用**：CONNECT 可被重放 | `quic/inbound_init.go:53-64` |
| padding 区必须是**零**，不能是旧 buffer 残留 | 服务端两条写路径都用 `WriteZeroN`（`inbound_conn.go:179-185,253-259`）。历史缺陷：cronet-go#6 / sing-box#4002 曾用 `Extend` 导致 padding 区泄漏旧池化内容，两侧均已修；**但本仓服务端仍缺"padding 字节全零"的回归测试**（客户端侧已有：`naive_conn_test.go:187-191`） | 源码 + 上游 Issue；缺口见 P9 |

**本轮未发现**：callback-after-free、把 `(0,nil)` 交给调用方的服务端路径、认证绕过、SSRF 削弱。

**记录但未修**（属另一仓库、且不是本 fork 引入）：`cronet-go/naive_conn.go:67-112` 的**客户端** `readWithPadding` 在 `originalDataSize == 0` 时会把 `buffer[:0]` 传给 `Read` 并可能返回 `(0, nil)`；服务端同型路径已在 `inbound_conn.go:150-154` 明确硬化。可达性极低（我们的服务端与参考实现都不会发零数据帧），记为 **ARCHIVE**，不建议为此改 pin。

---

## 12. Measurements & Reproducibility

### 12.1 环境

- Windows 11 x64，16 逻辑核，本机 loopback（127.0.0.1）。
- Go `go1.25.5 windows/amd64`，`CGO_ENABLED=0`。
- `libcronet.dll` SHA-256 `8B005A933AC0D0899C047617A9CD18418810D9426D0919C824520094A2C893F5`（9,889,280 B），取自 pin 的 `lib/windows_amd64` 模块。
- 服务端：`sing-box.exe`（`release/DEFAULT_BUILD_TAGS_OTHERS`，`go build ./cmd/sing-box`，117,240,832 B），Naive inbound `127.0.0.1:18443`（tcp+udp）/`18445`（tcp only）/`18446`（tcp only + UDP 黑洞）/`18447`（错误密码），`outbounds=[direct]`，自签 CA+leaf（SAN `localhost`、`127.0.0.1`）。
- 客户端探针：独立 module（不在 sing-box 工作树内），`replace` 到 pin 的 cronet-go，`-tags with_purego`，只调用公开 API（`NewNaiveClient`、`Engine.StreamEngine().CreateConn`）。

### 12.2 命令与结果（可直接复跑）

```powershell
# 1) 服务端单测（server profile）
$tags = (Get-Content release\DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()
go test -count=1 -tags "$tags" -timeout 300s ./protocol/naive/...
#   ok  protocol/naive        0.278s
#   ok  protocol/naive/quic   0.022s

# 2) H3 verdict / httpclient 回归
go test -count=1 -tags "$tags" -timeout 900s ./transport/http/ ./common/httpclient/
#   ok  transport/http        25.269s
#   ok  common/httpclient     0.022s

# 3) Windows 客户端 profile 下的 outbound pin 测试（真实 cronet-go API）
$win = (Get-Content release\DEFAULT_BUILD_TAGS_WINDOWS -Raw).Trim()
$env:CGO_ENABLED='0'
go test -count=1 -tags "$win" -run 'TestPinnedCronetCodec|TestReservedHeaderPolicy|TestSingleEngine|TestBuilderCarries|TestConcurrencyAndSingleEngine|TestNewOutboundRejects' ./protocol/naive/
#   PASS（-run 过滤到的全部用例，含 TestPinnedCronetCodecHasSafeFrameGeometry / TestReservedHeaderPolicyIsTheSameInBothDirections）

# 4) codec/CPU benchmark
go test -run XXX -bench 'BenchmarkPadding|BenchmarkHeadroom|BenchmarkCachedFirstPayload' -benchmem -count=3 -benchtime=20000x ./protocol/naive/

# 5) 参考源码对照（只读 clone，工作树外）
git clone --branch naive https://github.com/klzgrad/forwardproxy.git
git -C forwardproxy fetch --depth 1 origin d62c80d3dd2c706b6b87579844d2397bddd18317
git -C forwardproxy checkout FETCH_HEAD
#   forwardproxy.go:308-320   = 我们的 padding 头（逐字节一致）
#   forwardproxy.go:259-277   = 非 CONNECT 交给 next / 407 的真实位置
#   forwardproxy.go:629-655   = hijack 缓冲字节写给 target
#   forwardproxy.go:700-745   = 帧格式、短写、每帧 flush

# 6) 上游差异（只 fetch ref，不动工作树）
git fetch upstream testing
git diff --stat upstream/testing -- protocol/naive option/naive.go
#   33 files changed, 8353 insertions(+), 191 deletions(-)

# 7) 流量计数实测（需要 clash_api）
#   server-api.json = naive inbound + direct + experimental.clash_api{127.0.0.1:19090, secret=audit}
probe.exe holdconn true 127.0.0.1:18443 127.0.0.1:18080 ca.pem 127.0.0.1 18
Invoke-RestMethod http://127.0.0.1:19090/connections -Headers @{Authorization='Bearer audit'}
#   {"upload":41,"download":1078,"type":"naive/naive-in","rule":"final","chains":["direct"], ...}
```

原生探针（外置，非仓库）：

```text
probe.exe gencert <dir>                                  # 生成 CA + leaf
probe.exe client H2 false 127.0.0.1:18443 127.0.0.1:18080 ca.pem 15 127.0.0.1
probe.exe client H3 true  127.0.0.1:18443 127.0.0.1:18080 ca.pem 15 127.0.0.1
probe.exe client H3-forced-tcponly true 127.0.0.1:18445 ... 20 127.0.0.1
probe.exe client H3-blackhole     true 127.0.0.1:18446 ... 30 127.0.0.1
probe.exe raw false RAW-H2 https://127.0.0.1:18443 127.0.0.1:18080 ca.pem 10
probe.exe raw true  RAW-H3 https://127.0.0.1:18443 127.0.0.1:18080 ca.pem 10
probe.exe hold true 127.0.0.1:18443 127.0.0.1:18080 ca.pem 127.0.0.1 <concurrency> <singleEngine> 16
```

### 12.3 原始输出摘要

```
H2: engine="154.0.8037.49" startup=2ms | RESULT=connect-ok connect=5ms | "HTTP/1.1 200 OK" after=2ms | body=1080
H3: engine="154.0.8037.49" startup=2ms | RESULT=connect-ok connect=6-7ms (5 次重复) | "HTTP/1.1 200 OK" after=1-2ms | body=1080
     stderr: response received, protocol: quic/1+spdy/3, status: 200
H3-forced-tcponly: RESULT=fail connect=4.018s err=quic protocol error
H3-blackhole:      RESULT=fail connect=4.009s err=quic protocol error
AUTHFAIL-H2/H3:    RESULT=fail connect=7ms err=unexpected response status: 407
RAW-H2: params defaults quic=true http2=true | RESULT=ok headers=4ms status="200"
RAW-H3: params defaults quic=true http2=true | RESULT=ok headers=6ms status="200"  (protocol quic/1+spdy/3)
DoH:    [debug] open UDP connection to [2001:4860:4860::8888]:443   (every run, both modes)
Engine: 1→15.9MiB/17thr/235fd ; 4→16.4MiB/23thr/279fd ; 1+4NIK→15.9MiB/16thr/229fd
```

### 12.4 `NOT_RUN` 清单与可执行计划

| 项 | 阻塞 | 复跑条件与命令 |
|---|---|---|
| 官方 naive 客户端 → 本 fork 服务端 | 无官方二进制 | 构建 klzgrad/naiveproxy v154；`naive.exe --listen=socks://127.0.0.1:1080 --proxy=https://user:pass@host:443`，再 `curl -x socks5h://...`。逐项填 §6.1 |
| Caddy + forwardproxy 差分 | 无参考二进制 | `test/jiejie` 模块，参考二进制缺失时**SKIP**（不是 PASS）：`cd test && go test -tags "<OTHERS>,with_quic" -run TestNaiveCaddy ./jiejie/` |
| 真实网络 RTT/丢包/带宽 A/B | 无独立试验端 | 2×2 矩阵（官方 client / fork client × 官方 server / fork server），H2/H3 各固定一组；指标见 §14.4；脚本 `scripts/ci/bench-naive-cronet-engine.sh`、`bench-naive-receive-window.sh` |
| macOS/iOS native | 无环境 | 在 macOS 上跑同样模板，记录 `footprint`/线程/FD/初始化时间 |
| 真实流量计数的**剩余三格** | 只做了 TCP/H2 一格 | ① UoT 逐 datagram（`clash_api` 对 UDP 会话读 upload/download）；② H3 连接上的计数（把 inbound 配成 `network: udp` 单跑）；③ "一条物理连接被计成多条逻辑通道"的重复计数（同一 H2 session 上开 4 条隧道，看 4 条记录之和是否等于 tunnel 字节） |
| 48h 长稳/网络切换 | 无环境 | 用 `hold` 模式常驻 + 反复开关网卡，采样 WS/线程/FD 斜率 |
| 上游同步后的 Naive 回归 | 未做 | 合并 `upstream/testing` 后必须重跑：`go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./protocol/naive/...` **并**人工核对附录 D 的 0-RTT / path manager / MaxIncomingStreams 三项是否被上游改回 |
| 官方客户端对"裸服务端（无 masquerade）"的 preamble 行为 | 无客户端二进制 | 起一个无 masquerade 的 naive inbound，用官方 v154 客户端连一次，抓包看它是否发 preamble、收到 400 后是否继续 |

---

## 13. Candidate backlog

> 排序：ROI 高 → 低。每项都给出"为什么值得/为什么不做"。

### P1 — Naive 出站产生 `adapter.FailureFacts`

- **类型**：`INTEGRATION`
- **当前实际情况**：`protocol/naive/outbound.go:267-281` 原样返回 `DialEarly` 的错误；`protocol/naive/` 内 `grep WrapFailure` = 0。
- **外部最佳实践**：`protocol/mieru/fallback.go:419-430` 的 `wrapFailureFacts`（target/socket/transport/caller/local 五类，明确"soft evidence 不借确定性"）；消费方 `protocol/group/loadbalance_failover.go:160,287`。
- **需要改什么**：只动 `protocol/naive/outbound.go`（+ 一个内部分类函数 + 测试）。不改 cronet-go，不改协议。
- **证据强度**：`VERIFIED_SOURCE`（消费者存在）+ `MEASURED_LOCAL`（错误形状：`cronet.NetError` / `unexpected response status: 407` / `context.Canceled`）。
- **兼容影响**：无。facts 只是 err 上的附加类型，`errors.Is/As` 语义不变。
- **生命周期/安全风险**：低。必须**不**把 407/取消标成 path 故障（否则分组会重试认证失败）。
- **维护风险**：低。
- **复杂度**：小（约 80–140 行含测试）。
- **可逆性**：删掉 wrap 即回到现状。
- **验收门槛**：先写红测——`407` 必须无 fact；`context.Canceled` 必须是 caller；`quic protocol error` 必须是 `Handshake/Path`；目标 `ECONNREFUSED` 必须是 `Target`。
- **结论**：`READY_TO_BUILD`

### P2 — Cronet DoH bootstrap 的策略缺口

- **类型**：`INTEGRATION` / `BUG`
- **当前实际情况**：实测每次 `Start()` 都向 `2001:4860:4860::8888:443` 发起 UDP；`SetHostResolverRules` 已导出但未使用（`engine_params_experimental_options.go:55-66` vs `naive_client.go:493-499`）。
- **外部最佳实践**：sing-box#3803（实测同形）、#4620（开放提案）。
- **需要改什么**：**先 probe**：确认是否存在能关闭 DoH 的原生参数（当前 pin 未发现）；若没有，只做 `host_resolver_rules` 暴露 + 文档说明"stray 连接仍在"。
- **证据强度**：`MEASURED_LOCAL`（连接确实发生）+ `VERIFIED_SOURCE`（API 存在）。
- **兼容影响**：新增可选字段；默认不改。
- **安全风险**：MAP 规则若被用户误配会把代理指向任意 IP——但那是用户显式配置，且 SNI/证书仍校验域名。
- **维护风险**：低。
- **复杂度**：小（如果只做 MAP）；中（如果要做"禁用 DoH"并需要 Native 改动 → 则 `BLOCKED_BY_NATIVE_API`）。
- **结论**：`READY_AFTER_ONE_PROBE`

### P3 — H3→H2 自动回退（方案 B）

- **类型**：`FALLBACK`
- **当前实际情况**：§10.4。
- **外部最佳实践**：`transport/http/client.go` 的 verdict（attempt 有序 + CAS 一步退避 + 网络切换 reset）；`tunnel_client.go` 的"ambiguous 不记账"分类。
- **需要改什么**：cronet-go `naive_client.go`（per-dial QUIC 选择）+ sing-box `outbound.go`/`option` + 测试。
- **证据强度**：可行性 `MEASURED_LOCAL`；收益 `NOT_PROVEN`（缺真实网络 H3 故障率）。
- **兼容影响**：默认关闭；单 H2、单 H3 用户零影响。
- **生命周期风险**：中（记忆的所有权、取消/切换的区分、cached 首包已写出的边界）。
- **维护风险**：低-中（不下沉到 Chromium 补丁；但会新增一条协议行为分支，需要长期红测）。
- **复杂度**：中（≈270–470 行）。
- **可逆性**：删字段 + 回滚 cronet-go 的 per-dial 参数即可。
- **验收门槛**：§14 的 12 个场景 + 一个"证明不回退更好"的反证（H3 仅慢 1 RTT 时不应降级）。
- **反方证据（必须一起提交给决策者）**：naiveproxy #669 里维护者明确拒绝"同一配置同时用 HTTPS 与 QUIC"，理由是"应该由客户端指定协议，这样利于控制性能"。这条不否决方案 B（B 是**默认关闭**的可选项，且仍由用户显式开启），但它说明上游把"自动选择"视为应避免的方向。
- **结论**：`MEASUREMENT_FIRST`（先测故障率与 4 s 等待的可接受度）

### P4 — 非 CONNECT 请求的处理与参考不同（**本轮已纠正定性**）

- **类型**：`INTEROP`
- **当前实际情况**：我们 `inbound.go:502-505` → `serveWebOrReject(..., http.StatusBadRequest, ...)`（无 masquerade 时 400；有 masquerade 时返回网页）。参考 `forwardproxy.go:259-270`：`Hosts.Match && (non-CONNECT || authErr)` → **交给 next handler**（PAC 文件或 Caddyfile 后续 handler），forwardproxy 自己**不产生**固定状态码。
- **纠正**：原报告写的"参考对纯 GET 也回 407"**不成立**。407 只在"CONNECT + 认证失败 + 无 probe_resistance"这条路径上（`forwardproxy.go:271-277`）。
- **外部最佳实践**：klzgrad/forwardproxy；#793 说明 preamble 与认证错误的交互直到 v148 才修好。
- **证据强度**：`VERIFIED_SOURCE`（双侧源码）；没有证据表明这个差异造成过真实故障（`NOT_PROVEN`）。
- **兼容影响**：把 400 改成 407 会让非代理探测看到更像代理的响应（**反探测方向退步**）；把非 CONNECT 交给后续 handler，则等于要求一个"链式 handler"概念，在 sing-box inbound 里不存在。
- **结论**：`ARCHIVE`（记录，不施工）。若将来官方客户端互通测试失败于此，再升级为 `INTEROP` 候选。

### P5 — 客户端 `readWithPadding` 的零数据帧语义

- **类型**：`BUG`（另一仓库，极低可达性）
- **情况**：`cronet-go/naive_conn.go:67-112` 与本文服务端已硬化的路径不同型。
- **结论**：`ARCHIVE`。不要为一个我们的服务端不会发出的帧去改 pin。

### P6 — 客户端 padding 头与官方客户端的分布不同（**本轮已纠正归因**）

- **类型**：`INTEROP`（wire 外观，非互通）
- **情况**（`VERIFIED_SOURCE`，§5.5）：**服务端我们与 Caddy 逐字节一致**（`rand.Intn(32)+30` + 同一 16 字符集 + `~`）；**客户端我们与官方不同**（我们 30..61 + Caddy 字符集；官方 16..32 + HPACK 字符集 + `X` 填充）。另：naiveproxy 内置服务端对小包做定长偏置、对 200..1024 B 做 100..200 切分，Caddy 与我们都**不**做。
- **影响**：`INFERRED` 无功能影响（接收方只读声明长度），但**"客户端 wire 外观与官方一致"不能作为已验证结论**。
- **结论**：`ARCHIVE`。只有将来做流量形状/指纹对比时才回来；**不要**为了"更像官方"改长度或字母表——那会改变生产流量的既有形状，且收益未被证明。

### P7 — 文档：`quic_congestion_control` 默认值写错

- **类型**：`DOC`
- **情况**：`docs/configuration/inbound/naive.md` 写"`bbr` is used by default"，但代码 unset → nil → quic-go 默认 **CUBIC**（`quic/inbound_init.go:203-207` 的注释自己写的就是 CUBIC）。
- **结论**：`READY_TO_BUILD`（一行文档），但本轮不施工。

### P8 — 多 Engine 默认值（macOS）

- **类型**：`PERF`
- **情况**：本机空闲态测量显示 4 engine 的边际成本很小（+0.5 MiB/+6 线程），但这**不足以**翻转默认。
- **结论**：`MEASUREMENT_FIRST`。维持 `insecure_concurrency_single_engine: false` 默认。

### P9 — 服务端缺少"padding 区必须为零"的回归测试

- **类型**：`TEST_GAP`
- **当前实际情况**：服务端两条写路径都用 `WriteZeroN`（`inbound_conn.go:179-185,253-259`），所以**缺陷不存在**；但 `protocol/naive/*_test.go` 里没有任何断言检查 wire 上的 padding 字节全为零——现有测试只断言长度、边界与帧计数。客户端侧有（`cronet-go/naive_conn_test.go:187-191`）。
- **为什么值得补**：cronet-go#6 / sing-box#4002 是一次**真实发生过的信息泄漏**（padding 区回吐池化 buffer 残留）。把 `Extend` 换回 `ExtendHeader`+`WriteZeroN` 之类的重构，不会让任何现有测试变红。
- **外部最佳实践**：`cronet-go` 的同名断言；Naive README 明确 `uint8_t zeros[padding_size]`。
- **需要改什么**：只加测试（`protocol/naive/audit_padding_test.go` 或新文件）：用真实的 `writeFrame`/`writeBufferWithPadding` 写出 0..255 全部 padding 长度，逐字节断言 padding 区为 0；再对 `writeBufferWithPadding` 的 in-place 路径做同样断言。
- **证据强度**：`VERIFIED_SOURCE`（缺陷不存在）+ `VERIFIED_SOURCE`（测试缺口存在，全仓 grep 无零值断言）。
- **兼容影响**：无（纯测试）。
- **维护风险**：极低。
- **复杂度**：小（约 40–70 行）。
- **可逆性**：删除测试即可。
- **验收门槛**：该测试必须在"把 `WriteZeroN` 改成 `Extend`"时变红（先做一次本地变异验证）。
- **结论**：`READY_TO_BUILD`（与 P1 并列为本轮最安全的两项）

### 已否决（写清楚，避免下一轮重来）

| 提案 | 否决理由 |
|---|---|
| 用 Alt-Svc 让 Chromium 自己回退（方案 A） | 空 NAK + forced-QUIC ALPN 收窄，路径不成立（§10.3） |
| 为 Naive 新建第三套 H3 verdict | 已有两套语义不同的实现；第三套只会增加状态所有权冲突 |
| 改 Chromium 的 hpack_encoder 不索引 Padding | 需要自维护 Chromium 补丁；且当前随机值已使动态表无法稳定命中 |
| 用 Go 的 TLS/H2 替代 Cronet 数据面 | 摧毁原生路线（本轮禁区） |
| 把 `Padding` 值 clamp 到 buffer | 已被回滚过 |
| 为 H3 调更激进的拥塞控制 | 作者明确拒绝（#756） |

---

## 14. Adversarial test plan for the NEXT round

> 只针对 P1/P2/P3。每一项都必须能**失败**。

### 14.1 P3（回退）判定矩阵

| # | 场景 | 允许降级 | 写 verdict | 期望 |
|---|---|---|---|---|
| 1 | UDP 完全黑洞 | 是 | 是（一次） | 首次 4 s 后 H2 成功；后续窗口内直接 H2 |
| 2 | UDP 明确拒绝（ICMP） | 是 | 是 | 同 1，且耗时不高于黑洞 |
| 3 | H3 可连但 CONNECT 200 迟缓（>4 s） | 是（本次） | **否**（ambiguous） | 本次回退成功，下次仍试 H3 |
| 4 | H3 认证失败 407 | **否** | 否 | 直接把 407 交给调用者，不重试 |
| 5 | H3 目标拒绝（200 后立即 EOF/reset） | 否 | 否 | 不重试（已可能写出数据） |
| 6 | H2 同时不可用 | 是 | 是 | 返回 H2 的错误，不吞掉 |
| 7 | caller 取消 / deadline | 否 | 否 | `ctx.Err()` |
| 8 | 网络切换中并发拨号 | 是 | 新代际内允许 | 旧代际的失败不得污染新代际 |
| 9 | 共享 Engine 提前关闭 | 否 | 否 | `net.ErrClosed`，不重试 |
| 10 | H3 瞬态恢复 | 是 | 窗口到期后清 | 窗口结束后重新尝试 H3 并成功 |
| 11 | cached 首包已写出后才失败 | **否** | 否 | 不重放、不迁移 |
| 12 | Native 库缺失 / 不可加载 | 否 | 否 | 配置期或 Start 期明确报错 |

红测要求：第 3、4、11 三条必须能在实现前就写成失败测试（它们定义"不降级"的边界）。

### 14.2 P1（facts）判定矩阵

`407` → 无 fact；`context.Canceled` → `{Dial, Caller}`；`quic protocol error` → `{Handshake, Path}`；`ECONNREFUSED`（目标）→ `{Target, Target}`；DNS `no such host` → `{Dial, Local}`；`net.ErrClosed` → 无 fact（生命周期不是故障）。

### 14.3 P2（DoH）判定

1. 用 `SetHostResolverRules("MAP proxy.example.com 203.0.113.10")` 后，代理域名不再产生 DNS 查询（用 DNS bridge 计数器证明）。
2. **反证**：stray `2001:4860:4860::8888:443` 仍然出现 → 记录为"未解决"，不得声称修好。
3. 在屏蔽 Google IP 的链路上，`stream failed: name not resolved` 消失（需要试验环境）。

### 14.4 性能 A/B（与 P8 共用）

2×2 客户端/服务端 × {H2 固定, H3 固定} × {loopback, LAN, 120 ms/1% 丢包, UDP 阻断}，每格 ≥ 5 轮；指标：`time-to-CONNECT-200`、首字节、goodput、CPU/GB、RSS、失败率、重试次数。**失败请求不按 0 B 计入吞吐分母。**

---

## 15. Decision questions for ChatGPT + user

1. **Q1**：是否接受"Naive 出站补 FailureFacts"作为下一轮唯一施工项？（它不改变任何协议行为，且已有先例与消费者）
2. **Q2**：裸部署（无 masquerade）的官方 Naive 客户端互通是否是目标场景？如果是，P4 的 400→407 需要重新评估；如果不是，P4 归档。
3. **Q3**：H3→H2 回退，是否值得为"一次最多 4.0 s 的首连失败窗口 + 一条长期行为分支"换取"用户无需手动切 H2"？若答案是"部署里 H3 很少坏"，则选 `NOT_WORTH_IT`，保留显式 `quic` 开关。
4. **Q4**：`insecure_concurrency_single_engine` 是否需要在 macOS 上提供"按进程内存上限自动选择"？本机的空闲态数据（+0.5 MiB/engine）**不支持**这种自动化，需要先在 macOS 上测真实 footprint。
5. **Q5**：Cronet DoH 的 stray 连接是否被接受为"已知边界"？如果不可接受，需要确认上游是否愿意在 cronet-go 暴露禁用 DoH 的开关（否则我们只能做 MAP 规则，且必须承认它治不了这条连接）。

---

## 16. Links and Evidence Index

### 16.1 外部 Issue / PR / 提交

| 链接 | 日期 | 状态 | 本轮用途 |
|---|---|---|---|
| https://github.com/SagerNet/sing-box/issues/3837 | 2026-03-01 | closed / not planned | naive outbound 性能报告，无结论（§5.3） |
| https://github.com/SagerNet/sing-box/issues/4205 | 2026-06-11 | closed / completed | ALPN race，上游 `627912a78`，我们做得更远（§5.1） |
| https://github.com/SagerNet/sing-box/issues/3803 | 2026-02-16 | closed / stale | Cronet DoH IPv6 UDP，本机复现（§5.4） |
| https://github.com/SagerNet/sing-box/issues/4620 | 2026-10-07 | **open** | `host_resolver_rules` 提案（§5.4） |
| https://github.com/SagerNet/sing-box/commit/627912a7883c9af699e6feaa1a61ae3d39d525a7 | 2026-06-19 | merged | 已在当前 `testing` 祖先中 |
| https://github.com/klzgrad/naiveproxy/issues/72 | 2020-05-22 | closed / completed | HPACK 与 padding 头（§5.5） |
| https://github.com/klzgrad/naiveproxy/issues/76 | 2020-05-25 | closed / completed | padding auto-negotiation 语义，我们的服务端照做（§5.5） |
| https://github.com/klzgrad/naiveproxy/issues/507 | 2023-05-12 | closed | 替代服务端 hprox + 未来 padding 版本协商（§5.6） |
| https://github.com/klzgrad/naiveproxy/issues/756 | 2025-10-16 | closed / not planned | 作者确认 QUIC 不特别优化（§5.2） |
| https://github.com/klzgrad/naiveproxy/issues/793 | 2026-05-07 | closed / fixed v148 | preamble 与普通 H2 代理不兼容（§5.6） |
| https://github.com/Piggy-Cat-bit-shadow/cronet-go/commit/8c68ce89873c976b33ac62a3fd3128064d34e0a7 | 2026-09-29 | pin | EarlyCopyBufferGrowth + 保留头策略 |
| https://github.com/SagerNet/naiveproxy/commit/97ae698dede50ad27a9dd39b93de13da5a967340 | – | gitlink | `-force-quic` / `-connect-authority` / NIK 的真实实现 |
| https://github.com/klzgrad/forwardproxy/commit/d62c80d3dd2c706b6b87579844d2397bddd18317 | – | 参考 pin | 服务端语义的权威来源（本轮已本地检出并逐行对照，§6.1b） |
| https://github.com/klzgrad/naiveproxy/issues/669 | 2024-09-20 | closed / not planned | **维护者拒绝"同配置同时用 HTTPS 与 QUIC"**："应该由客户端指定协议，这样利于控制性能"（§5.8） |
| https://github.com/SagerNet/cronet-go/issues/11 | 2026-07-01 | closed / fixed | `QUIC=false` 未传播为 `enable_quic=false`，H2 会被 Alt-Svc 升级成 QUIC；我们的 pin 已含修复（§5.8） |
| https://github.com/SagerNet/cronet-go/issues/6 | 2026-04-09 | closed / fixed | padding 区未清零泄漏旧 buffer；服务端缺回归测试（§5.8、P9） |
| https://github.com/SagerNet/sing-box/issues/4002 | 2026-04 | closed | 同源的 sing-box 侧 padding 清零问题（修复 PR #4001） |
| https://github.com/SagerNet/sing-box/pull/4287 | 2026-07-21 | merged | H2 wrapper 真正传给 router（#4249 的修复，已吸收） |
| https://github.com/SagerNet/sing-box/issues/4458 | 2026-08-28 | closed / **未落到 upstream testing** | Naive HTTP2Options：我们是 fork-only（§5.8） |
| https://github.com/SagerNet/sing-box/issues/3617 | 2025-12-16 | closed | Windows DLL 部署 / Linux purego 编译约束（§3.3、§5.8） |
| https://github.com/SagerNet/sing-box/issues/317 | 2023-01-14 | closed / not planned | "web fallback"（网站伪装）——上游拒绝，本 fork 用 masquerade 实现 |
| https://github.com/SagerNet/sing-box/issues/4181 | 2026-06-02 | closed | `quic=false` 的 QUIC 观测争议；结论指向 cronet-go#11 与"浏览器 QUIC 不经代理" |

### 16.2 本仓代码位置

| 主题 | 位置 |
|---|---|
| 出站映射 | `protocol/naive/outbound.go:44-252,336-382` |
| 服务端 CONNECT | `protocol/naive/inbound.go:491-717` |
| ALPN 隔离 | `protocol/naive/inbound.go:169-187,245-292` |
| H3 可用性判定 | `protocol/naive/inbound.go:294-336` |
| H2 边界 | `protocol/naive/inbound.go:396-489` |
| padding codec | `protocol/naive/inbound_conn.go:21-373` |
| H2 隧道 conn | `protocol/naive/inbound_conn.go:419-510` |
| hijack 半关闭 | `protocol/naive/inbound_conn.go:526-604` |
| H3 服务端 | `protocol/naive/quic/inbound_init.go:53-253` |
| H3 错误分类 | `protocol/naive/quic/h3_error_class.go` |
| H3 verdict | `transport/http/client.go:295-449,451-532,541-555,655-677` |
| 隧道回退 | `transport/http/tunnel_client.go:97-215` |
| 第二套 H3 状态 | `common/httpclient/http3_transport.go:291-376` |
| 失败事实 | `adapter/failure.go:7-142`；先例 `protocol/mieru/fallback.go:419-430` |
| 握手/首包边界 | `route/conn.go:950-1005,1062-1116` |
| 文档 | `docs/naive.md`、`docs/ENGINEERING-NOTES.md:62-87`、`docs/configuration/inbound/naive.md` |
| 知识库 | `KNOWLEDGE/modules/naive-http-transport.md` |

### 16.3 测试与脚本

| 主题 | 位置 |
|---|---|
| 写入契约 / short write / 帧计数 | `protocol/naive/audit_write_contract_test.go` |
| padding 范围 / 零帧 / 分段 | `padding_zero_frame_test.go`、`padding_segmentation_test.go`、`audit_padding_test.go`、`fuzz_padding_test.go` |
| 短写与 headroom | `audit_write_contract_test.go`、`inbound_copy_buffer_test.go` |
| flush 契约 | `audit_flush_contract_test.go` |
| ALPN | `audit_alpn_isolation_test.go` |
| H2 边界 | `http2_options_test.go`、`audit_http2_runtime_test.go` |
| 跨仓 pin | `dependency_pin_test.go` |
| 半关闭 | `hijacked_conn_halfclose_test.go` |
| 限流 | `server_limiter_test.go` |
| CPU benchmark | `bench_test.go`、`bench_h2_test.go` |
| Engine / 窗口 harness | `scripts/ci/bench-naive-cronet-engine.sh`、`scripts/ci/bench-naive-receive-window.sh` |
| 官方/Caddy 差分 | `test/jiejie/jiejie_naive_caddy_differential_test.go`（独立 module，缺参考二进制时 SKIP） |

### 16.4 本轮外置探针（未进仓库）

| 文件 | 用途 |
|---|---|
| `C:\Deepseek\_np_audit\probe\main.go` | 原生 engine/raw/hold/origin/blackhole 探针（仅用 cronet-go 公开 API） |
| `C:\Deepseek\_np_audit\bin\server-*.json` | 四个 loopback 服务端配置（tcp+udp / tcp-only / 黑洞 / 错误密码） |
| `C:\Deepseek\_np_audit\hpackcheck\main.go` | HPACK 每字符编码长度复算（§5.5） |
| `C:\Deepseek\_np_audit\naiveproxy\` | SagerNet/naiveproxy@97ae698 sparse 检出，只读 |

---

## 附录 A：本轮对"如何判断"的三条方法论记录

1. **不要把"没有 API"当成"没有算法"**。本轮最初的问题是"H3 回退是否需要 Native 补丁"；答案是"不需要"，因为 `-force-quic` 早就是 fork 的 header 契约（它就写在 `reservedNaiveHeaders` 里），只是没人把它当成选择器用。**先读保留头表，再读 Chromium 路径。**
2. **不要把"closed"当成"已修"**。`#3837` 关闭是因为无法复现，`#3803` 关闭是 stale，两者都不等于"上游确认了原因"。
3. **不要用 loopback 证明任何吞吐结论**。本轮所有原生数字都只用于**行为**（选哪条协议、多久失败、是否回退），不用于**性能**。H2/H3 在 loopback 上的 5–8 ms 与 6–7 ms 不构成"谁更快"的证据。

## 附录 B：本轮的关键"反证"设计（已执行其一）

- **反证 1（已执行）**：如果"forced-H3 会回退"，那么在有活 H2 的同端口上它应该成功。实测失败 4.018 s → 反证成立（不回退）。
- **反证 2（已执行）**：如果"黑洞比拒绝更慢"，两者耗时应不同。实测 4.009 s vs 4.018 s → 同一预算，推翻"黑洞会拖到 15 s"的直觉。
- **反证 3（已执行）**：如果"引擎必须选一个协议"，同一默认引擎不应既能 H2 又能 H3。实测两者都成功 → 选择是 per-request。
- **反证 4（未执行）**：如果"单 Engine 省内存"，4 engine 的增量应该显著。实测只 +0.5 MiB → 在空闲态推翻该假设，但不推翻"有负载时可能不同"。

---

## 附录 C：提示词要求的汇总矩阵（集中放置，便于决策者一屏对照）

### C.1 组件依赖树

```text
sing-box @ testing
├── protocol/naive/                     服务端 inbound（永远注册，无 build tag）
│   ├── net/http + h2c + x/net/http2    H1/H2 服务端
│   └── protocol/naive/quic/            H3 服务端（//go:build with_quic）
│       ├── github.com/sagernet/quic-go (fork pin v0.61.1-...-7e08ecaba383)
│       └── init() → naive.ConfigureHTTP3ListenerFunc
├── protocol/naive/outbound.go          客户端 outbound（//go:build with_naive_outbound）
│   └── github.com/sagernet/cronet-go  (fork pin 8c68ce89873c)
│       ├── cronet-go/all → lib/<platform> ×29（仅目标平台参与编译）
│       │   └── libcronet.{dll,a} = Chromium 154.0.8037.49（预编译产物）
│       └── naiveproxy submodule @97ae698（Chromium 补丁：-force-quic / -connect-authority /
│                                          -network-isolation-key / padding-type）
├── transport/http/                     H3 verdict（MASQUE 用，Naive 不用）
├── common/httpclient/                  第二套 H3 fallback（普通 HTTP/规则集/DoH 用）
├── adapter/failure.go                  失败事实（Naive 目前不产出）
└── route/                              copy / handshake / cached-first-payload / ACL
```

### C.2 公共能力接入矩阵（汇总 §9）

| 能力 | 已有 | Naive 接线 | 结论 |
|---|---|---|---|
| DNS | `adapter.DNSRouter` + 目标 ACL | 127.0.0.1:53 socket 桥；ECH/SVCB 注入与改写 | `KEEP`；DoH bootstrap 是缺口（P2） |
| Dialer/Detour | `dialer.NewWithOptions` + ResolveDialer | 直接复用，无双解析 | `KEEP` |
| Lifecycle | Scope / StartStage / InterfaceUpdated | `Start`+`scope.Add(Close)`；`InterfaceUpdated→CloseAllConnections` | `KEEP` |
| FailureFacts | `adapter.FailureFacts` | **无** | `IMPROVE`（P1） |
| Stats | route 计数 + WriteBuffer | 接口齐全 | `NOT_PROVEN`（缺一次实测） |
| Buffer/Copy | CopyBufferGrowthTuner / CachedReader | 双向 EarlyCopyBufferGrowth + cached handoff | `KEEP` |
| 配额/安全 | server_limits / ACL / UoT guard | 全保留 | `KEEP` |
| 网络代际 | `InterfaceUpdated(ctx)`（无 generation） | 只用于关连接 | `NOT_PROVEN`；回退记忆需最小改造 |

### C.3 Issue/PR 洞见矩阵（汇总 §5）

| Issue/PR | 真实结论 | 是否已吸收 | 对本项目 |
|---|---|---|---|
| sing-box #4205 | ALPN race（上游 `627912a78` 已修） | **是**，且我们做了 per-transport 隔离 | `KEEP`，不要退回共享列表 |
| naiveproxy #756 | 作者确认 QUIC 未特别优化、拒绝激进 CC | — | 不改默认；H3 不是吞吐优化 |
| sing-box #3837 | 无结论（未复现关闭） | — | `MEASUREMENT_FIRST` |
| sing-box #3803 / #4620 | DoH bootstrap 绕过 DNS 策略 | **否** | P2；`SetHostResolverRules` 已可用但未接 |
| naiveproxy #72 / #76 | padding 头随机化 + 自动协商语义 | **是**（逐条照做） | `KEEP`；wire 形状与参考不同（P6，归档） |
| naiveproxy #507 / #793 | 未来 variant 协商；preamble 修复 | 当前兼容 | `KEEP` + 记录前向风险 |

### C.4 性能 A/B 矩阵（本轮未执行，交给下一轮）

| 维度 | 分层 |
|---|---|
| 客户端 | 官方 naive / 本 fork Cronet（`quic:false`）/ 本 fork Cronet（`quic:true`） |
| 服务端 | Caddy+forwardproxy / 本 fork Go inbound |
| 链路 | loopback / 同机端到端 / LAN / 120 ms+1% 丢包 / UDP 阻断 |
| 负载 | 单流长传、1/4/8/16/32 并发、短请求、长稳态、网络切换 |
| 指标 | time-to-CONNECT-200、首字节、goodput、CPU/GB、RSS、FD、threads、allocs、失败率、重试次数 |
| 统计 | 同批 wall clock、≥5 轮、热身、原始样本；失败请求**不**按 0 B 混入分母 |

### C.5 候选优先级矩阵（汇总 §13）

| ID | 类型 | 收益证据 | 风险 | ROI | 结论 |
|---|---|---|---|---|---|
| P1 | INTEGRATION | 消费者与先例都在仓库里 | 低 | 高 | `READY_TO_BUILD` |
| P2 | INTEGRATION/BUG | 本机实测 + 开放 issue | 低 | 中高 | `READY_AFTER_ONE_PROBE` |
| P3 | FALLBACK | 可行性已证，收益未证；且参考实现**明确拒绝**自动化（#669） | 中 | 中 | `MEASUREMENT_FIRST` |
| P4 | INTEROP | 双侧源码已证；参考行为是"交给 next"，不是固定 407 | 低（反探测方向） | 低 | `ARCHIVE` |
| P5 | BUG（另一仓） | 可达性极低 | 低 | 极低 | `ARCHIVE` |
| P6 | INTEROP（外观） | **服务端与 Caddy 完全一致**；仅客户端分布不同 | 低 | 极低 | `ARCHIVE` |
| P7 | DOC | 文档与代码矛盾 | 无 | 低 | `READY_TO_BUILD`（一行） |
| P8 | PERF | 空闲态数据不足以翻默认 | 中 | 低 | `MEASUREMENT_FIRST` |
| P9 | TEST_GAP | 真实发生过的泄漏类型（cronet-go#6）在本仓无回归测试 | 极低 | 高 | `READY_TO_BUILD` |

### C.6 已实现 / 未实现 / 未证实 能力表

| 能力 | 状态 | 依据 |
|---|---|---|
| 服务端 H1/H2/H3 CONNECT + padding framing | **已实现** | §4.1 + `VERIFIED_TEST` |
| 服务端 masquerade / server_limits / H2 边界 | **已实现** | §4.1 |
| 客户端原生 H2/H3、padding、NIK、ECH、自定义 socket/DNS | **已实现** | §3.1 + `MEASURED_LOCAL` |
| 客户端 `DialEarly` + cached first payload | **已实现** | `route/conn.go:950-1005` |
| 失败事实（stage/origin） | **未实现** | §9.4 |
| DoH 策略控制 / 关闭 DoH | **未实现** | §7.6 |
| H3→H2 自动回退 | **未实现**（本轮不建议直接施工） | §10 |
| 与服务端 variant2 padding 协商 | **未实现**（上游也未落地） | §5.6 |
| 官方客户端 / Caddy 真实互通 | **未证实** | `NOT_RUN`，§12.4 |
| 真实网络吞吐/RTT/丢包结论 | **未证实** | `NOT_RUN`，§12.4 |
| macOS/iOS footprint 与长期资源回收 | **未证实** | `NOT_RUN` |

---

## 附录 D：本 fork 与上游 `upstream/testing@dfb80349b` 的 Naive 差异（直接 `git show` 核对）

> 用途：上游同步时，这张表就是冲突点清单。**每一条都是有意为之，不是落后。**

| 维度 | 上游 `dfb80349b`（2026-10-13） | 本 fork | 依据 / 判断 |
|---|---|---|---|
| ALPN | 共享一张列表（`Start()` 里往 `NextProtos()` 追加 h2/h1；h3 由 `ConfigureHTTP3` 另加） | **per-transport 视图**：TCP = [h2, http/1.1, 自定义]，QUIC = [h3] | `inbound.go:169-187,255-278`；修的是"h3 上 TCP、h2 上 QUIC"。`KEEP` |
| HTTP/2 服务端参数 | `h2c.NewHandler(n, &http2.Server{})` —— 全部默认，不可配置 | `HTTP2Options`（4 字段）+ 边界校验 | fork-only；#4458 关闭后上游仍未落地（§5.8）。同步时**逐字段比对**，别直接取 upstream |
| 预认证资源限制 | 无 | `server_limits`（连接数/每 IP/header 超时/tracked IP） | fork-only；`server_limiter.go` |
| masquerade | 无 | 有（与 hysteria2/http 同 schema） | fork-only；上游 #317 明确拒绝"web fallback" |
| QUIC 0-RTT | **`Allow0RTT: true`** | **不启用**（默认 false） | 我们与 Caddy 一致；CONNECT 可重放，`KEEP`。**上游同步会试图加回来** |
| QUIC path manager | **`DisablePathManager: true`** | 默认**启用**，`quic_disable_path_manager` 可关 | 我们与 Caddy 一致（Caddy 只设 Versions/Tracer）。**上游同步会改掉** |
| QUIC 并发流 | **`MaxIncomingStreams: 1 << 60`**（库的"无限"哨兵） | 不设置 → quic-go 默认 **100** | 我们与 Caddy 一致；实测 256 流约 2.5 MiB，100 是刻意上界。**上游同步会放开成无限** |
| QUIC 版本 | 继承库默认 | **显式 pin v1+v2** | `f74d9254c`；参考也是显式。`KEEP` |
| H3 连接级错误 | 被 quic-go 吞掉（只进 Logger） | `h3_error_class.go` 分类后上报 | `d26754689`；fork-only |
| H2 wrapper 传给 router | 已修（#4249 / PR #4287） | 已吸收（`inbound.go:813-820`） | 双方一致 |
| 认证比较 | constant-time（上游也已修） | 同 | 一致 |
| padding 分片 | 上游已按 65536 上限 | 同（`65e227002`） | 一致 |
| 零长度帧 | 上游？ | 我们显式硬化（`inbound.go` 注释引用的缺陷在上游仍在） | 建议上游同步时**不要**回退这段 |
| 出站 `insecure_concurrency_single_engine` | 无 | 有（fork-only） | `4130117ae` |
| 客户端 `EarlyCopyBufferGrowth` | 不适用（出站在另一仓） | cronet-go pin 提供 | `8c68ce8` |

**同步建议**：把本表当作 `git diff upstream/testing -- protocol/naive option/naive.go` 的解读基线；对 0-RTT / path manager / 并发流 三项，**在合并后必须重新跑** `protocol/naive/quic` 的默认值测试与 `docs/naive.md` 的声明，否则会静默偏离 Caddy。

---

**报告结束。** 本轮没有 commit，没有 push，没有修改任何生产文件。`git status` 结束时与开始时一致（仅其他窗口的两处子模块指针变更）。
