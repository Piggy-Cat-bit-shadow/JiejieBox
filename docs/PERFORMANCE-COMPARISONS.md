# 性能与内存：实现对照、优化前后及测量条件

本文汇集仓库知识库和专项报告中可以追溯的数字，供选型、调优和复测使用。**这些是各自标注日期与实现版本的历史实验，不是 v0.1.39 刚刚重新测得的统一成绩。** MB/s 与 MiB/s 保留原报告单位；微基准、回环端到端、人工瓶颈与公网测试分别列出，不能相互换算成产品承诺。

[返回首页](../README.md) · [速度参数与瓶颈定位](../KNOWLEDGE/PERFORMANCE-FACTORS.md) · [架构与改造评估](../KNOWLEDGE/reports/core/JiejieBox_工程架构与改造最终评估报告_2026-11-01.md)

## 1. 代表性的优化前后

| 对象、条件 | 改造前 | 改造后 | 应如何理解 |
| --- | ---: | ---: | --- |
| XHTTP packet-up 单流上传；2026-10-15、TLS/H2、同一 Xray v26.9.30 参考服务端 | 2.12 MB/s | 26.8 MB/s | 该场景约 12.6 倍，来自上传聚合、首包例外和有界队列重构 |
| 同组 XHTTP 下载 | 137 MB/s | 140 MB/s | 上行重构基本保留原有下行水平 |
| 同组 XHTTP 建连 p99 | 31.7 ms | 约 4.3 ms | 特定短连接负载；不能推广到任意服务端响应时间 |
| 上传整形；4 条普通流争用约 2 MB/s FIFO，按容量 85% 持续整形 | 交互接收端 p99 146 ms | 9 ms | 受控共享瓶颈试验，需显式配置调度与速率 |
| 同组上传整形平均积压 | 257 KiB | 15 KiB | 减少已排入发送队列的大流量 |
| 持续上传后首条交互消息 p50 | 127 ms | 2 ms | 同一受控台架，非任意公网时延 |
| 百万域名规则集加载累计分配；2026-10-22 第二轮、Windows/Ryzen 4800H | 42.5 MiB | 20.6 MiB | 约减 51.5%；稳态存活内存另从 8.3 降至 7.4 MiB |
| 同组百万域名加载 GC 次数 | 10 | 5 | 单次 fixture 记录，不等于实际设备 GC 减半 |
| 10 万域名加载累计分配 | 4.1 MiB | 2.9 MiB | 约减 29.3%；该规模稳态内存反而从 882.4 增至 986.3 KiB，保留扩容余量有代价 |
| 无 cache_file 的远程 SRS；有效规则附加 4 MiB 非压缩尾部 | 21,229,120 B 累计分配 | 完整性修复后约 125 KB | 流式读取移除完整下载副本；不是进程峰值或典型规则文件的等比例下降 |
| TUN ReturnPackets；v0.1.22 真实 dispatcher + portNAT 基准 | 约 236–238 ns、72 B、2 次分配 | 约 156 ns、0 B、0 次分配 | 单包处理微基准，不能表述成所有 TUN 流量零拷贝 |
| 每流活跃缓冲区驻留 heap；默认与 low-memory 构建对照 | 64.1 KiB | 32.1 KiB | 构建档位的缓冲区基准，不是整台设备内存减半 |
| 非 HTTP 的 DNS-over-stream 样例经过 HTTP 嗅探 | 4889 B、9 次分配 | 205 B、3 次分配 | 提前排除不匹配输入，非全部嗅探统一收益 |

来源分别见：[XHTTP 与流量调度汇总](../KNOWLEDGE/reports/core/JiejieBox_工程架构与改造最终评估报告_2026-11-01.md)、[规则集扩容原始对照](../KNOWLEDGE/reports/anywhere-round2/anywhere-round2-construction.md)、[流式读取及完整性修复](fork/anywhere-delivery-summary.md)、[TUN 路径评估](../KNOWLEDGE/reports/tun-fast-path/tun-fast-path-value-review.md)。各轮有不同基线，不把旧的 166 ns/3 allocs 与后来的 156 ns/0 allocs 拼成同一次 A/B。

缓冲区与嗅探的原始基准入口分别见 [MTU/bufgeom 模块](../KNOWLEDGE/modules/mtu-and-geometry.md) 和 [sniff 模块](../KNOWLEDGE/modules/sniff.md)。

### 嗅探：首包解析与分片处理

TLS 对照来自 2026-10-09 的 [`e053cc733`](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/commit/e053cc73335e56a4ffe1a1f0a9c1e9aa86ada624)，九次交错采样，经 benchstat 比较。提交明确记录主机有共享负载，时间比例只代表该轮测量；分配数据较稳定。改造停止在取得 ClientHello 后继续生成 ECDHE / ML-KEM 密钥，并将反复构造的 reader 链改成可重置游标。

| 场景 | 历史 A/B 结果 | 测量对象 |
| --- | --- | --- |
| `PeekStreamTLS` | 耗时 −96.2%，B/op −32.6%，allocs/op −35.3% | 完整 TLS ClientHello 的流嗅探 |
| `PeekStreamTLSFragmented` | 耗时 −91.9%，allocs/op −64.5% | 同一 TLS ClientHello 分为 8 次读取 |
| 被移除的无效握手工作 | 每 TLS 流约 127 µs、13 KiB | 旧 profile 中生成并丢弃的密钥等工作；不是修复后整个调用的耗时 |
| HTTP 前置检查：DNS-over-stream | 4,889 → 205 B/op；9 → 3 allocs/op | 仅 HTTP 嗅探器的有 / 无前置检查对照 |

**分片 TLS 的 91.9% 不能归给 QUIC。** QUIC 另有包边界拷贝、视图读取、帧摘要和最多 64 轮的续读边界；它们有各自的正确性、容量和异常输入测试，没有在这里构成同一组速度 A/B。也不把曾出现的异常分配量当作当前每流固定节省量。

来源：[阶段报告](fork/overnight-hardening-report.md)、[HTTP 前置检查配对基准](../common/sniff/http_gate_bench_test.go)、[流嗅探基准](../common/sniff/sniff_bench_test.go)、[当前机制与限制](../KNOWLEDGE/modules/sniff.md)。该阶段报告中的其他未完成事项保留其历史范围，不作为当前产品状态。

### 低内存构建：并发流与规则加载

[Android 移动端对照报告](fork/mobile-parity-android-report.md#the-measured-case-host-benchmark--apple-m1-loopback-in-process-never-device-or-battery)记录 Apple M1、主机进程内和 loopback 条件。每流持有两个缓冲区，GC 后观察活跃 heap；默认 / 低内存档分别使用 32/16 KiB 与 16/8 KiB 的流 / 包缓冲区参数。

| 并发流数 | 默认档总驻留 | 低内存档总驻留 | 每流默认 / 低内存 |
| --- | ---: | ---: | ---: |
| 32 | 2,052 KiB | 1,028 KiB | 65,664 / 32,896 B |
| 128 | 8,208 KiB | 4,112 KiB | 65,664 / 32,896 B |
| 512 | 32.06 MiB | 16.07 MiB | 65,664 / 32,907 B |

512 流的该工作集减少约 16 MiB。普通 socket 的 TCP/UDP、延迟与并发对照区间重叠，未测出明确差异；特定 Shadowsocks MTU 上传路径的代价为：`ShadowRealCopyLoop/writer-mtu` 耗时 +4.1%、allocs +80%；`ShadowMTUWrappedPath` 耗时 +19.4%、allocs +71%。较小档位下，单流初期每 256 KiB 的写入次数从 8 增至 16，分配次数从 15 增至 22。

报告中的 `copyExtended` 长流测试在越过约 512 KiB 后，两种档位都会增长到相同的大缓冲区；这项结果只适用于对应复制路径。报告同时记录了先后运行时 TIME_WAIT 累积污染结果的问题，最终采用连接清理与顺序交替控制。上述 heap、吞吐与分配结果均不能代替手机 footprint 或电池测试。

规则加载对照见本页首表：[第二轮施工记录](../KNOWLEDGE/reports/anywhere-round2/anywhere-round2-construction.md)包含 Windows / Ryzen 4800H 的 10 万与百万域名样例；[SRS 交付记录](fork/anywhere-delivery-summary.md)包含流式解析及完整性修复。分别报告累计分配、稳态存活内存与 GC 次数，不能将累计分配的降幅称作设备峰值内存降幅。

### 受限内存下的 GC 策略

2026-10-03 的 SS2022 内存压力基准固定 `GOMEMLIMIT=40 MiB`，比较 `GOGC=50` 与 `100`。每组 10 次配对交替运行，每次新进程，`benchtime=6x`。数据采用修正采样和中位数口径后的 [`710351ba2`](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/commit/710351ba2340fc23bb412f6033a3d655a68ac81a) 记录；不混用更早报告的数字。

| 指标，中位数 | GOGC 50 | GOGC 100 | 变化 |
| --- | ---: | ---: | ---: |
| GC 次数 | 29 | 13.5 | −53.448% |
| GC 累计暂停，`MemStats.PauseTotalNs` 差值 | 861,564 ns | 415,604 ns | −51.762% |
| 基准吞吐 | 5,706.39 MB/s | 5,776.48 MB/s | +1.228% |
| Go runtime 管理内存的连续采样峰值 | 39,323,912 B | 40,745,224 B | 约 37.5 → 38.9 MiB |

GC 次数的 13.5 是十次样本中央两值的平均数，单次实际计数仍为整数。连续峰值按 `/memory/classes/total:bytes − /memory/classes/heap/released:bytes` 在工作负载全程每 1 ms 采样；累计暂停来自精确计数器，不是暂停直方图桶的上界相加。此处软限制针对 Go runtime 管理内存，不能据此给整个进程或 iOS Jetsam 作保证。

[汇总 JSON](fork/bench/gogc-40mib-50-vs-100.json)由[原始样本](fork/bench/gogc-40mib-50-vs-100-raw.json)生成；前后对照体现减少 GC 与保留更多内存的取舍。吞吐是进程内压力负载的数据处理速率，不能视为 Shadowsocks 公网代理吞吐。

### CONNECT-UDP：包装与真实连接

CONNECT-UDP owned 路径还有两层不同的对照，不能把其中较大的倍数写成网络端到端收益：

| 2026-10-20 测量层 | 复制路径 | owned 路径 |
| --- | --- | --- |
| 1200 B 包装边界微基准 | 409–428 ns、1345 B、2 allocs | 83–85 ns、64 B、1 alloc |
| 真实 quic-go 连接、1000 B、5000 次发送 | 10.6 µs/包 | 9.5 µs/包 |

前一行消除一份载荷复制，后一行受真实连接的排队和传输成本影响。来源：[六协议效率施工报告](../KNOWLEDGE/reports/core/SIX_PROTOCOL_EFFICIENCY_IMPLEMENTATION_2026-10-20.md)。

## 2. XHTTP：与 Xray 的实现速度对比

### 同一参考服务端上的客户端对照

2026-10-15 的 TLS/HTTP2 受控实验，服务端为真实 Xray v26.9.30：

| 指标 | JiejieBox 改造前 | JiejieBox 改造后 | Xray 参考客户端 |
| --- | ---: | ---: | ---: |
| packet-up 单流上传 | 2.12 MB/s | 26.8 MB/s | 22.9 MB/s |
| 建连 p99 | 31.7 ms | 约 4.3 ms | 约 4.35 ms |

该组上行比参考客户端约高 17%。实现变化包括：用有界字节队列承接写入；单一 flusher 合并小写入；最初两次写入不等待聚合窗口；保持顺序、背压、POST 失败中止及关闭时尾部数据完整。512 次 8 KiB 写入在历史测试中聚合为约 5–6 次 POST，并验证完整字节内容。

### RTT 改变后仍需重新比较

后续模拟 RTT 扫描的历史记录如下；它是另一轮对照，不将其与上表混成一个固定提升比例。

| 模拟 RTT | JiejieBox | Xray 参考客户端 |
| ---: | ---: | ---: |
| 0 ms | 26.93 MB/s | 22.84 MB/s |
| 30 ms | 24.25 MB/s | 19.67 MB/s |
| 50 ms | 15.57 MB/s | 14.53 MB/s |
| 100 ms | 7.88 MB/s | 7.80 MB/s |
| 200 ms | 3.92 MB/s | 4.04 MB/s |

200 ms 时参考客户端略快。因此“本实现始终快于 Xray”不符合记录。另一个被否决的方案是取消所有小 POST 的聚合等待，实测上传约下降 13%；项目保留了有数据支持的聚合窗口。

历史稳定性验收还包括约 70 分钟、681 轮混合负载、0 次记录失败，以及服务器终止、恢复、H3 丢包和中断注入。这些是当时测试的结果，不是当前版本所有网络长期无故障的保证。

来源：[性能重构提交 1f86a13](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/commit/1f86a13fe324eb98fb15baebfb892d3c1ed396cb)、[完整历史条件与结果](../KNOWLEDGE/reports/core/JiejieBox_工程架构与改造最终评估报告_2026-11-01.md#九xhttp移植跨层修复与专项性能重构)、[当前 XHTTP 模块与配置](../KNOWLEDGE/modules/xhttp-client-transport.md)。

## 3. Mieru：官方 SDK、官方客户端与 Mihomo 对照

### UDP-over-Stream 适配层微基准

2026-10-18 的 Windows / AMD Ryzen 7 4800H / Go 1.26.8，1200 字节载荷：

| 操作 | JiejieBox 适配层 | 官方 SDK 包装路径 | 含义 |
| --- | ---: | ---: | --- |
| 写入耗时 | 118.8 ns/op | 666.1 ns/op | 该帧处理基准约 5.6 倍 |
| 写入分配 | 64 B/op、1 alloc/op | 1280 B/op、1 alloc/op | 分配字节约少 95%，分配次数没有减少 |
| 读取耗时 | 79.83 ns/op | 86.93 ns/op | 小幅改善，不描述为网络吞吐倍增 |
| 读取分配 | 0 B/op、0 alloc/op | 4 B/op、2 allocs/op | association 专属 framer 降低暂存成本 |

后续为并发读正确性加入互斥，历史记录约多 9 ns/包。原表不是加入互斥后的重新测量结果。复查入口：`go test ./protocol/mieru -run '^$' -bench UOTFrame -benchmem`。

### 与官方同版本客户端的端到端对照

历史 phase-2：双方使用 Mieru SDK v3.38.1、相同官方 `apis/server` 台架与本地 SOCKS5 入口，4 轮 ABBA；4 条 TCP 连接、64 MiB 负载、5000 个 UDP 数据报、1000 个延迟样本。下表是 JiejieBox 相对于官方客户端：

| 场景 | 吞吐 / 延迟结果 |
| --- | ---: |
| TCP underlay、低复用、下载 | 吞吐 +10.2% |
| TCP underlay、高复用 + no_wait、下载 | +7.0% |
| UDP underlay、下载 | +3.2% |
| TCP underlay、低复用、上传 | **−6.1%** |
| UDP underlay、上传 | +16.0% |
| UDP underlay、UDP echo | 吞吐 +47.4%，p50 为参考的 0.61 倍 |
| TCP p95 延迟 | 参考的 0.98–1.01 倍，接近 |

TCP 上传落后是重复观察到的负面结果；该场景 CPU/GiB 低 32%，体现吞吐与 CPU 成本的取舍。启动到 SOCKS-ready 约 253–255 ms，对照约 248–258 ms，没有可信的启动加速结论。

### 与 Mihomo 的产品实现对照

Mihomo v1.19.32 使用 SDK v3.38.0；JiejieBox 当时使用 v3.38.1。**这是产品对照，不是统一 SDK 的适配层纯对照。** 数字为 JiejieBox / Mihomo：

| 场景 | 吞吐比值 |
| --- | ---: |
| TCP underlay、低复用、下载 | 1.07× |
| TCP underlay、低复用、上传 | 0.95× |
| TCP underlay、高复用、上传 | 0.85× |
| UDP underlay、上传 | 1.27× |
| UDP echo | 0.93–1.05× |

### 真实 mita 服务端复核

后续从官方源码编译真实 mita 3.38.1，再做 4 轮对照：JiejieBox 的两组 TCP 下载约为官方客户端的 1.15× / 1.14×，UDP echo 为 1.05–1.46×；但首字节 p50 约 1.0 ms，对照约 0.52 ms，UDP underlay 上的 TCP 上传仅 0.739×。这些结果应与收益一起阅读。

当前 v0.1.39 使用 **Mieru v3.39.0**，包括后续单向上传读 deadline 修复；上面的 v3.38.1/v3.38.0 比较没有在 v3.39.0 全矩阵重跑。mbox、Hiddify、extended 在那轮环境中没有可用的完整对照，不能补造排名。

来源：[Mieru 集成报告及其原始矩阵入口](fork/mieru-integration.md#performance-evidence-2026-10-18)、[当前模块](../KNOWLEDGE/modules/mieru.md)、[后续边界验收](fork/mieru-masque-final-acceptance.md)。归档矩阵只用于对应历史测量，不能当作当前基线验收。

## 4. Naive / Cronet：端到端能力与实际瓶颈

2026-10-20 Windows/amd64、真实 Cronet DLL、本机 loopback；一个进程内同时运行客户端与服务端，经 SOCKS5 传输 32 MiB。每种形态 1 次冷连接、5 轮测量，以下为中位数：

| 形态 | 吞吐 | 结论范围 |
| --- | ---: | --- |
| H2，单 engine 单流 | 457.7 MiB/s | 不是公网吞吐；包含客户端、服务端和 TLS |
| H3，单 engine 单流 | 158.2 MiB/s | 该回环场景低于 H2；没有测出弱网移动场景的收益 |
| H2，2 engines | 约 665 MiB/s | 此机最高点；继续增至 4/8 engines 反而降为约 568/537 MiB/s |

Go profile 中约 72% 位于原生调用桥，不能据此判断 Chromium 内部没有热点。padding 快路径在该 bulk 实验为 523/523 次命中；进程内 codec 的 4.16 GB/s 也不能拿来充当端到端吞吐。

v0.1.39 消费的 Linux DNS 修复另有功能复现：旧路径首次写入约 274 秒超时，修复后 `TestNaiveCustomDialer` 约 0.79 秒通过。这是**错误恢复为正确连接**，不是“全体 Naive 连接提速 347 倍”。

来源：[Naive 性能只读审计](../KNOWLEDGE/reports/core/NAIVEPROXY_PERFORMANCE_READONLY_AUDIT_2026-10-20.md)、[Cronet 修复与发布证据汇总](../KNOWLEDGE/reports/release/cronet-patch5-linux-dns-2026-11-05.md)。

## 5. 如何复测与使用这些数据

1. 固定核心、依赖与对端提交，记录 OS、CPU、Go、构建标签、协议模式和参数。
2. 对照使用相同载荷、并发和入口，安排预热与交错轮次，保留失败而非只取成功最快值。
3. 分别测吞吐、p50/p95/p99、CPU/GiB、累计分配、稳态内存和进程 footprint；不要以 Go heap 代替 iOS NE 内存预算。
4. 说明 RTT、丢包和限速发生的位置。回环、模拟 WAN 和实际公网分别报告。
5. 遇到窗口、复用或调度问题先查 [PERFORMANCE-FACTORS](../KNOWLEDGE/PERFORMANCE-FACTORS.md)，不要把不同实验中各自最好的参数拼成默认配置。

所有表格都是可追溯的工程记录。更换链路、对端、SDK 或版本后，以新一轮对照为准。
