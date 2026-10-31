# 知识索引：按任务查找文档（Knowledge index — route first, read narrowly）

本索引用来查找资料：根据当前任务、关键词或源码路径，定位需要阅读的模块和章节。
具体的技术结论及其证据在对应模块中。

> 先查索引，再按需阅读相关章节。历史行号和结论可能过期，使用前应在目标版本核对；
> 本次知识校准的范围见校准记录。

## 入口文件，按加载级别（Entry points, by load level）

| 文件 | 何时阅读 | 原因 |
|---|---|---|
| `../AGENTS.md` | **ALWAYS** | 这里最常出错的三件事 |
| `README.md` | **ALWAYS** | 基线指针、状态标签、证据规则 |
| `INDEX.md`（本文件） | **ALWAYS** | 按任务查找相关文档 |
| `BASELINE.md` | **ALWAYS**，在信任任何模块之前 | 模块描述的 SHA，以及此后的增量 |
| `modules/README.md` | TASK_MATCH | 任务 → 模块映射、fork 术语表、覆盖缺口 |
| `PROJECT.md` | TASK_MATCH | fork 是什么，刻意没有改动什么 |
| `ARCHITECTURE.md` | TASK_MATCH | 值得保留的十一个边界 |
| `PERFORMANCE-FACTORS.md` | TASK_MATCH（速度） | 实测出来的速度关键参数与因素：三条第一原理、客户端/服务端参数表、症状路由、测量规则、已否决结论（无地址/凭据） |
| `INVARIANTS.md` | **LOAD_BEARING_ONLY** | A1–A21 硬性不变量，以及它与约定的区别 |
| `ACCEPTANCE.md` | **VERIFICATION_ONLY** | 需求 → 证据 → 复检命令 |
| `COMMIT-INDEX.md` | **PROVENANCE_ONLY** | 某一行为背后的 SHA，按主题分组 |
| `REFRESH.md` | **REFRESH_ONLY** | 刷新协议；不是一种任务模式 |
| `CALIBRATION-2026-10-31.md` | **REFRESH_ONLY** | 当前增量、验证情况与继承下来的限制 |
| `CALIBRATION-2026-10-31-REVIEW.md` | **REFRESH_ONLY** | 同日全面清单、更正与未决发现 |
| `CALIBRATION-2026-10-29.md` | **REFRESH_ONLY** | 2026-10-29 校准及其证据 |
| `CALIBRATION-2026-10-28.md` | **REFRESH_ONLY** | 已完成的校准范围、证据与剩余验证限制 |
| `docs/archive/**` | **HISTORY_ONLY** | 我们怎么走到这里；绝不是当前事实 |
| `sing-box_v0.1.5-v0.1.10_网络链路与Action-Release优化总结.md` | **HISTORY_ONLY** | v0.1.5→v0.1.10 的工程叙事（链路路径 + Action/Release）；一份时点总结，不是模块 |

## 按任务查找（Route by task）

| 需要处理的问题 | 模块 | 精确章节 |
|---|---|---|
| 子仓库改造 / client gitlinks / 子仓库分支头 / 嵌套 dashboard / forked Go 模块 | `modules/child-repositories.md` | 固定源码、子仓库变更与刷新规则 |
| TUN、系统代理 / Mixed、嗅探的路径与优化边界 | `modules/data-path-optimization.md`、`modules/sniff.md` | 入口、嗅探与系统代理路径；嗅探所有权。刷新时另见 `REFRESH.md` §18A |
| 端点本地流量、`JudgeHostFlow`、`HostPort` | `modules/masque-connectip.md` | 贯穿 TUN 栈的端点本地流量 |
| 发布冻结、源码锁定、Apple 单运行器 | `modules/ci-release.md` | 发布冻结与 Apple 验收 |
| 速度慢 / 吞吐上不去 / 判断瓶颈在哪一层 | `PERFORMANCE-FACTORS.md` | §1 基本判断 → §4 根据症状定位 → §5 测量方法 |
| XHTTP 上传慢 / 下载慢 / xmux 与连接数 | `PERFORMANCE-FACTORS.md` | §2 客户端参数（`xmux`）→ §3 服务端参数（h2 请求体缓冲） |
| `Scope` 资源清理、`scope.Add` 清理 | `modules/core-lifecycle-runtime.md` | §1 `adapter.Scope` —— 所有权原语 |
| Box 启动顺序 | `modules/core-lifecycle-runtime.md` | §2 → Start；在 `box.go` 中定位 `PreStart`、`Start`、`preStart` 与 `start` |
| Box 关闭顺序与资源清理 | `modules/core-lifecycle-runtime.md` | §2 → Close；在 `box.go` 中定位 `Close` 与 `close` |
| 网络 generation、重建租约、`runtimecoord` | `modules/core-lifecycle-runtime.md` | §3 一个 generation、基于身份的租约 |
| TCP 半关闭存活检测、观察能力限制与资源清理 | `modules/core-lifecycle-runtime.md` | 网络切换所有权；`route/conn_halfclose.go` |
| 监听器通配族、TUN 读取错误、SSH 会话退役、Linux namespace 持有者 | `modules/core-lifecycle-runtime.md` | 附加入口与生命周期边界 |
| `TrimMemory` 与 `ReleaseMemory` 之别 | `modules/core-lifecycle-runtime.md` | §3 `TrimMemory` 与 `ReleaseMemory` 不是一回事 |
| 空闲资源退役清单、`route/reference.go` | `modules/core-lifecycle-runtime.md` | §4 `route.ReferenceManager` |
| `TrimIdleResources` / `CloseIdleConnections` / `RetireSuspect` | `modules/core-lifecycle-runtime.md` | §4 这三个方法 |
| 启动期校验、`PhysicalPath`、`ControlPath` | `modules/path-selection-validation.md` | §1 三件并不相同的事 |
| `ControlPath` 的方向 | `modules/path-selection-validation.md` | §1 为什么 `ControlPath` 不是反向的 |
| 校验实际按什么顺序检查什么 | `modules/path-selection-validation.md` | §2 启动期校验 |
| `common/routeflow`、可证明的需求 | `modules/path-selection-validation.md` | §3 |
| selector/urltest 组、预览态与已提交态 | `modules/path-selection-validation.md` | §4 选择在提交之前只是预览 |
| 环、只读守卫 | `modules/path-selection-validation.md` | §6 环检测 |
| `RequiredNetworks` —— **不是**契约 | `modules/path-selection-validation.md` | §5 实现细节，**不是**契约 |
| 某个组上的 `fast_path` | `modules/fast-path-group-policy.md` | §4 三态语义 |
| 哪些出站类型可以携带 `fast_path` | `modules/fast-path-group-policy.md` | §5 |
| `fast_path: true` **不会**绕过什么 | `modules/fast-path-group-policy.md` | §6 |
| 嵌套组的优先级 | `modules/fast-path-group-policy.md` | §8 |
| 双栈 DNS、候选准入与网络切换所有权 | `modules/dns-demand.md`, `modules/core-lifecycle-runtime.md` | 基线之后新增的章节 |
| local/site/global 地址族能力与 IPv6 入口决策 | `modules/dns-demand.md`, `modules/libbox-platform-contract.md` | 双栈准入与入口相关章节 |
| 栈启动前后的 TUN 重置 | `modules/libbox-platform-contract.md` | 基线之后的 TUN 重置交接 |
| Naive 从 H3 回退到 H2 重试 | `modules/naive-http-transport.md` | 基线之后的回退 |
| VLESS 加密层与取消 | `modules/xhttp-client-transport.md` | VLESS 加密边界 |
| Mieru 配置、回退与生命周期 | `modules/mieru.md`, `modules/path-selection-validation.md` | 协议集成与失败边界 |
| AnyTLS 服务器地址偏好与错误归因 | `modules/path-selection-validation.md` | AnyTLS 服务器解析与失败归因 |
| ShadowTLS 预认证超时与握手目标清理 | `modules/core-lifecycle-runtime.md` | 协议所有权 |
| TLS 引擎选择、ECH 重试与 fork 固定版本 | `modules/build-pruning-release.md` | TLS 引擎与 ECH 重试 |
| Bridge L3 出站、OpenVPN/OpenConnect 注册与构建标签 | `modules/build-pruning-release.md` | 协议注册与构建门禁 |
| FakeIP 已知域名与分片 QUIC 嗅探 | `modules/sniff.md` | 基线之后的 FakeIP 章节 |
| DNS 路由器、代次、缓存提交 | `modules/dns-demand.md` | §2 缓存提交不变量 |
| FakeIP | `modules/dns-demand.md` | §3 |
| 消费方需求 | `modules/dns-demand.md` | §4 |
| QNAME 级 NXDOMAIN 负缓存：什么时候可以放宽到名字层级、命中与 TTL 语义、容量、`disable_expire` | `modules/dns-demand.md` | “The QNAME-wide NXDOMAIN negative cache”一节 |
| DNS 泄漏 | `modules/dns-demand.md` | §5 DNS 泄漏与 DNS 的作用范围 |
| `adblock` DNS 动作 —— 用本地 NXDOMAIN 拦截广告域名 | `modules/dns-adblock-action.md` | §2 语法、§3 行为、§8 放置位置 |
| `systemd-resolved` goroutine 泄漏 | `modules/dns-demand.md` | §6 —— 已修复；称此处有问题的文档已过时 |
| 任何嗅探器、peek/read 所有权 | `modules/sniff.md` | §2 所有权 |
| 嗅探循环、分片 QUIC | `modules/sniff.md` | §3 两个循环 → Packet |
| 嗅探 → 路由元数据契约 | `modules/sniff.md` | §4 |
| 嗅探检测中**未**修复的薄弱点 | `modules/sniff.md` | §6 |
| MASQUE 数据面、capsule、会话 | `modules/masque-connectip.md` | §1 `transport/masque` |
| MASQUE 端点 | `modules/masque-connectip.md` | §2 `protocol/masque` |
| 两个 MASQUE 解析器 | `modules/masque-connectip.md` | §3 两者不可互换 |
| MASQUE MTU / PTB | `modules/masque-connectip.md` | §4 |
| 原生 Naive 入站 | `modules/naive-http-transport.md` | §2 服务端入站行为 |
| UoT v1/v2 | `modules/naive-http-transport.md` | §2 → UoT v1/v2 的目的地语义 |
| Naive 目标 ACL / SSRF | `modules/naive-http-transport.md` | §3 风险最高的区域 |
| `transport/http`、`common/httpclient` | `modules/naive-http-transport.md` | §4 |
| XHTTP 模式 | `modules/xhttp-client-transport.md` | §4 模式 |
| XHTTP 字段参考 | `modules/xhttp-client-transport.md` | §5 完整字段参考 |
| XMUX 复用、全有或全无的默认设定 | `modules/xhttp-client-transport.md` | §13.4 重要的全有或全无语义默认规则 |
| XHTTP fork 生命周期 / 裁剪 / 复用 | `modules/xhttp-client-transport.md` | §14 fork 特有的 XMUX 生命周期行为 |
| packet-up POST 节奏、高 RTT 吞吐、响应流水线 | `modules/xhttp-client-transport.md` | §12.3 POST 节奏，以及响应完成为何不做流水线化 |
| Direct Offload | `modules/data-path-optimization.md` | Part A |
| 流量类别 + 上传调度器 | `modules/data-path-optimization.md` | Part B |
| 拨号调速器（dial governor） | `modules/data-path-optimization.md` | Part C |
| 缓冲区规则、借用视图、批处理 | `modules/buffer-ownership.md` | §1 这些规则，以及每条被违反时会坏什么 |
| splice 建立交接与历史回退缺陷 | `modules/buffer-ownership.md` | §2 splice 路径 |
| MTU 数量（三个，不是一个） | `modules/mtu-and-geometry.md` | §1 |
| PTB | `modules/mtu-and-geometry.md` | §2 |
| `common/bufgeom` 几何计算 | `modules/mtu-and-geometry.md` | §4 |
| 低内存几何档位（geometry profile） | `modules/mtu-and-geometry.md` | §5 |
| 睡眠/唤醒/息屏/锁屏 | `modules/power-mobile-lifecycle.md` | §2 级别/边沿/快照词汇表 |
| 设备轴、`ReuseSuspect` | `modules/power-mobile-lifecycle.md` | §7 |
| 定时器普查 | `modules/power-mobile-lifecycle.md` | §6 |
| OOM 压力、分层回收、重置与停止/重载屏障 | `modules/power-mobile-lifecycle.md` | §4 OOM 回收协调 |
| Android 裁剪阶梯 | `modules/power-mobile-lifecycle.md` | §3 |
| libbox ABI、`SetupOptions` | `modules/libbox-platform-contract.md` | §1、§5 ABI 版本化 |
| CommandServer / CommandClient | `modules/libbox-platform-contract.md` | §4 |
| Apple 固定点及其缺口 | `modules/libbox-platform-contract.md` | §6 Apple 固定点 —— 以及那个缺口 |
| 两处刻意的上游接口破坏 | `modules/libbox-platform-contract.md` | §8 |
| 构建标签、裁剪、标签极性文件对 | `modules/build-pruning-release.md` | §2 带 `_stub` 孪生文件的标签极性文件对 |
| 发布档位 | `modules/build-pruning-release.md` | §3 |
| 固定依赖前缀规则 | `modules/build-pruning-release.md` | §5 是前缀问题，不是清单问题 |
| 生成器、`cmd/internal/*` | `modules/build-pruning-release.md` | §4 |
| CI 实际运行了什么 | `modules/ci-release.md` | §1 实际运行的内容 |
| **未接线的门禁 —— 系统性风险** | `modules/ci-release.md` | §2 |
| `verify-evidence/` 的陈旧问题 | `modules/ci-release.md` | §3 |
| 需要运行的复检命令 | `modules/ci-release.md` | §4 复检者必须真正运行的命令 |
| 发布溯源 | `modules/ci-release.md` | §5 发布溯源 |

## 按关键词/别名路由（Route by keyword / alias）

| 查询或别名 | 规范主题 | 位置 |
|---|---|---|
| direct, direct offload, L0, native bypass, fast path, fast_path, bypass | fast_path 有**两种**含义 —— 先确认是哪一种 | 组策略 → `modules/fast-path-group-policy.md` §2；按流转卸载 → `modules/data-path-optimization.md` Part A |
| sleep, screen off, lock, pause, wake, governor, reload window, device axis | 电源与移动端生命周期 | `modules/power-mobile-lifecycle.md` §2, §5 |
| epoch, generation, lease, rebuild, reuse band | 运行时协调 | `modules/core-lifecycle-runtime.md` §3 |
| trim, low memory, memory pressure, ReleaseMemory | 内存策略 | `modules/core-lifecycle-runtime.md` §3；Android 阶梯 `modules/power-mobile-lifecycle.md` §3 |
| zero-copy, splice, splice defect, buffer pool, batch | 缓冲区所有权 | `modules/buffer-ownership.md` §1–2 |
| FakeIP, cache commit, DNS leak, domain_resolver | DNS | `modules/dns-demand.md` §2–5 |
| nxdomain, negative cache, negative TTL, QNAME-wide verdict, `nxdomainCache` | NXDOMAIN 域名缓存 | `modules/dns-demand.md` “The QNAME-wide NXDOMAIN negative cache”；不变量 `INVARIANTS.md` A21 |
| adblock, ad blocking, advertising rule-set, NXDOMAIN verdict, negative caching of a blocked name, "why not reject" | adblock 动作 | `modules/dns-adblock-action.md` §1, §5, §7 |
| peek, read ownership, fragmented QUIC, unsniffable | 嗅探 | `modules/sniff.md` §2–3 |
| CONNECT-IP, capsule, DATAGRAM, PREF64, PTB | MASQUE | `modules/masque-connectip.md` |
| UoT, masquerade, padding, `server_limits`, SSRF | Naive | `modules/naive-http-transport.md` §2–3 |
| XMUX, stream-one, packet-up, splitHTTP, X-Padding, H2/H3 | XHTTP | `modules/xhttp-client-transport.md` |
| traffic_class, traffic_scheduler, backpressure, pacing | 数据路径 | `modules/data-path-optimization.md` Part B |
| tag stub, pruning, registry, DEFAULT_BUILD_TAGS | 构建 | `modules/build-pruning-release.md` §1–3 |
| schema, schema.json, option/schema.go, generated docs, stale generated artifact | 生成式 schema 链条及其复检契约 | `modules/build-pruning-release.md` §4；`modules/README.md` §Additional ownership and coverage limits（schema 行） |
| release authority, release contract, canonical asset list, asset allowlist, artifact provenance, exact-SHA provenance, checksum, read-back, draft-first, dry-run, `publish-args`, `expected-assets`, `verify-public-release`, single source of release truth | CI 与发布溯源 | `modules/ci-release.md` §1, §5 |
| pipeline DAG, unwired gate, duplicate gate, gate ownership, which workflow runs what | CI 门禁所有权 | `modules/ci-release.md` §1–2 |
| SIGPIPE, pipefail, exit-code false negative in a gate | shell 门禁正确性 | **仅历史**：`reports/core/_ci_reaudit.md`；当前没有模块章节负责它 |
| Apple source authority, Android AAR provenance, Windows artifact provenance, Unix/macOS release authority | 各平台构建产物溯源 | `modules/ci-release.md` §5；`modules/libbox-platform-contract.md` §6–7 |
| Windows daemon, boxdd, Named Pipe, peer authentication, service install, worker, executable identity, signer trust, Authenticode, certificate validity, code-signing EKU, same-file check | Windows 守护进程信任边界 | `modules/windows-daemon-security.md` |
| unwired gate, stale evidence, VERIFIED_CI, SKIP | 证据纪律 | `modules/ci-release.md` §2–3；`README.md` §the four status labels |
| orphan, provenance, patch-id, upstream twin | Git 溯源 | `BASELINE.md` §the provenance test |
| tag scheme, annotated tag, release identity | 发布身份 | `BASELINE.md` §Fork release lineage; §The three traps this repository has already paid for |
| baseline, delta, "is the KB current?" | 基线 | `BASELINE.md` §post-baseline incremental reading |

## 按源码路径路由（Route by source path）

| 路径或符号 | 主要模块 | 章节 |
|---|---|---|
| `box.go` | `modules/core-lifecycle-runtime.md` | §2 启动/关闭顺序 |
| `adapter/lifecycle.go`、`adapter.Scope` | `modules/core-lifecycle-runtime.md` | §1 |
| `common/runtimecoord` | `modules/core-lifecycle-runtime.md` | §3 |
| `route/reference.go`、`ReferenceManager` | `modules/core-lifecycle-runtime.md` | §4 |
| `common/physicalpath`、`common/routeflow` | `modules/path-selection-validation.md` | §2–3 |
| `route/fast_path.go`、`option/fast_path.go` | `modules/fast-path-group-policy.md` | §2, §11 |
| `protocol/group/selector.go`、`protocol/group/urltest.go` | `modules/path-selection-validation.md` + `modules/fast-path-group-policy.md` | §4 / §9 |
| `common/trafficclass`、`common/trafficsched` | `modules/data-path-optimization.md` | Part B |
| `common/bufgeom` | `modules/mtu-and-geometry.md` §4 + `modules/buffer-ownership.md` §1.1 |
| `common/power` | `modules/power-mobile-lifecycle.md` | §1–2 |
| `internal/memmetrics` | `modules/power-mobile-lifecycle.md` | §4 |
| `dns/transport/group` | `modules/dns-demand.md` | §1, §4 |
| `dns/client_negative.go` —— QNAME 级 NXDOMAIN 判定 | `modules/dns-demand.md` | “The QNAME-wide NXDOMAIN negative cache” |
| `route/rule/rule_action.go` → `RuleActionAdblock`、`dns/router.go` → `walkDNSRules`、`constant/dns.go` → `DefaultDNSNegativeTTL` | `modules/dns-adblock-action.md` | §8 放置位置、§13 TTL 选择 |
| `transport/masque`、`protocol/masque` | `modules/masque-connectip.md` | §1–2 |
| `protocol/naive`、`transport/http`、`common/httpclient` | `modules/naive-http-transport.md` | §2, §4 |
| `transport/v2rayxhttp` | `modules/xhttp-client-transport.md` | §1–22 |
| `experimental/libbox`、`daemon/` | `modules/libbox-platform-contract.md` | §1, §4 |
| `cmd/internal/*`、`release/*` | `modules/build-pruning-release.md` | §4, §6 |
| `option/schema.go`、`docs/schema.json`、`schema/generate.go` —— 生成的 schema 产物 | `modules/build-pruning-release.md` §4（生成器） + `modules/README.md` §Additional ownership and coverage limits（schema 行） | §4 / schema 行 |
| `.github/workflows/*`、`scripts/ci/*` | `modules/ci-release.md` | §1–5 |
| `protocol/vless/encryption` | `modules/xhttp-client-transport.md` | VLESS 加密边界 |
| `experimental/boxdd/**` —— Windows 守护进程、服务、worker、命名管道 | `modules/windows-daemon-security.md` | §1（角色）、§4（握手）、§5（Authenticode）、§6（固定点） |
| `clients/{apple,android,desktop}` | 独立仓库；只记录核心侧 | `modules/libbox-platform-contract.md` §6–7 |

## 按不变量/验收路由（Route by invariant / acceptance）

| 问题 | 位置 |
|---|---|
| 这条规则是硬性不变量，还是一般约定？ | `INVARIANTS.md` §A 硬性不变量 vs §B 约定 vs §C 策略 vs §D 实现细节 |
| 哪条不变量负责 `Scope` 资源清理？ | `INVARIANTS.md` A1 |
| 哪条负责租约 / “一次只做一次恢复”？ | `INVARIANTS.md` A2 |
| 哪条负责缓冲区所有权？ | `INVARIANTS.md` A6, A7 |
| 哪条负责 NXDOMAIN 的名字级放宽？ | `INVARIANTS.md` A21 |
| 哪条负责发布复验？ | `INVARIANTS.md` A12；`ACCEPTANCE.md` §12 |
| 哪条负责失败即拒绝的发布验收？ | `INVARIANTS.md` A20 |
| 哪条负责标签 stub 规则？ | `INVARIANTS.md` A16 |
| 哪条负责上游/fork 所有权边界？ | `INVARIANTS.md` A15 |
| 我如何复检一个子系统？ | `ACCEPTANCE.md` —— 与模块对应的编号章节 |
| 已知不稳定 / 已知失败？ | `ACCEPTANCE.md` §Known-failing and known-flaky set |
| fork 的长期限制？ | `ACCEPTANCE.md` §The fork's own standing limits |

## 历史主题 → 当前事实（Historical topics → current truth）

归档报告是**线索，不是当前事实**（`REFRESH.md` §2）。某个主题已经有归档报告时，
先读模块；只有想理解当初怎么做出的决定，才再查阅归档。

| 历史主题 | 当前事实所在位置 |
|---|---|
| fast_path 组策略 | `modules/fast-path-group-policy.md` |
| L0 / direct 覆盖 | `modules/data-path-optimization.md` Part A |
| 物理路径 / 路由感知的能力校验 | `modules/path-selection-validation.md` §1–3 |
| DNS 对齐 / 消费方需求 | `modules/dns-demand.md` §4 |
| WireGuard 生命周期 / 普查探针 | `modules/core-lifecycle-runtime.md` §3；`COMMIT-INDEX.md` |
| Apple 源码权威 / 精确 SHA 解析 | `modules/libbox-platform-contract.md` §6 |
| Android 构建 / AAR 流水线 | `modules/libbox-platform-contract.md` §7；`modules/build-pruning-release.md` |
| Windows 运行时 / 能力门禁 | `modules/windows-daemon-security.md` §1, §5；历史位置 `modules/libbox-platform-contract.md` §8 |
| CI DAG / 重复门禁的移除 | `modules/ci-release.md` §1–2 |
| 发布产物信任边界 | `modules/ci-release.md` §5；`INVARIANTS.md` A12, A20 |
| 标签处理安全性 | `INVARIANTS.md` A16；`modules/ci-release.md` §5 |
| SIGPIPE / pipefail 假阴性修复 | `modules/ci-release.md` §5；`COMMIT-INDEX.md` §10 |
| 上游被吸收的变更 | `BASELINE.md` §the provenance test；`COMMIT-INDEX.md` 的溯源词汇表 |
| sing-tun 被吸收的修复 / fork 修复 | `modules/build-pruning-release.md` §5；`modules/mtu-and-geometry.md` |
| 发布允许清单 / 校验和 / 试运行 / 回读 | `modules/ci-release.md` §5；`ACCEPTANCE.md` §12 |
| Anywhere 第二轮研究、构建，以及 DNS/规则集/MASQUE/QUIC 所有权工作 | `reports/anywhere-round2/README.md`（归档）；当前行为仍由各模块负责 |

## 维护（Maintenance）

`modules/README.md` 提供任务到模块的总览，本文件进一步索引具体章节、别名、源码路径和符号。两者应保持各自职责。

新增或重命名模块章节时，请同步检查本索引中的章节名称和链接；刷新时还应按 `REFRESH.md` 检查模块结构、跨文件一致性和基线记录。
