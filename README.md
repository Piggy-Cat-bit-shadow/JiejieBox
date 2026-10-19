安卓客户端
https://github.com/Piggy-Cat-bit-shadow/satelite-one
一、代理协议与传输层

* MASQUE DATAGRAM 热路径减少两次完整 Payload Copy
* MASQUE 1280B Owned DATAGRAM 局部吞吐约为复制路径的 2.7 倍
* MASQUE Capsule 重叠检查由 13.5 秒降至 16 毫秒，约 800 倍
* MASQUE Capsule 条目限制为 8192，防止 149796 条目消耗 17.65 MiB
* Naive 64B 缓存首包处理耗时由 470 ns 降至 214 ns
* Naive 1400B 缓存首包处理耗时由 482 ns 降至 216 ns
* Naive 16KiB 缓存首包处理耗时由 796 ns 降至 215 ns
* Naive 缓存首包 Fast Path 分配由 1 次降至 0 次
* XHTTP 0ms RTT 上传实测 26.93 MB/s，Xray 为 22.84 MB/s
* XHTTP 30ms RTT 上传实测 24.25 MB/s，Xray 为 19.67 MB/s
* XHTTP 50ms RTT 上传实测 15.57 MB/s，Xray 为 14.53 MB/s
* XHTTP 100ms RTT 上传实测 7.88 MB/s，Xray 为 7.80 MB/s
* XHTTP 200ms RTT 上传实测 3.92 MB/s，Xray 为 4.04 MB/s
* XHTTP 0–200ms RTT 对照覆盖 30 轮、0 次载荷不一致

二、DNS 与 FakeIP

* DNS 分组 8/32/128 并发冷启动查询仅执行一次竞选扇出
* DNS 全成员失效时限制为一次尝试，避免无效并发探测
* DNS 分组历史状态限制每成员最多 64 条记录
* DNS 跨网络缓存隔离通过 100 轮 Reset 压力测试
* DNS 同网络重复 Reset 通过 100 轮缓存保持测试
* 设备休眠时 DNS 乐观后台刷新请求由 1 次降至 0 次
* 100 次被抑制的 DNS 刷新不遗留 In-flight 记录
* DNS 缓存命中路径 A/B 测试变化处于约 ±2.4% 噪声范围

三、流量嗅探与协议识别

* TLS Sniff 热路径耗时降低 96.2%
* TLS Sniff 热路径内存分配次数降低 35.3%
* TLS Sniff 移除每流约 127 µs 的无效完整握手工作
* TLS Sniff 消除每流约 13 KiB 的临时握手开销
* QUIC 分片 ClientHello 嗅探耗时降低 91.9%
* QUIC 分片 ClientHello 内存分配次数降低 64.5%
* HTTP Sniff 按需执行使分配量由 4889 B 降至 205 B
* HTTP Sniff 按需执行使分配次数由 9 次降至 3 次
* 历史异常嗅探测试曾造成额外 16.7 MiB 缓冲区驻留
* QUIC Sniff 增加最多 64 轮的读取边界，限制异常资源增长
* QUIC Sniff 异常输入累计分配曾达约 541.7 MB，现增加轮次与释放保护

四、路由、代理组与智能选路

* 10 节点全部失败时，Failover 最多仅执行 2 次拨号
* 多层嵌套 Failover 仍共享一次备用节点重试预算
* 8/32/128 并发 DNS 选举均保持单次扇出
* Sticky Sessions 通过 10 万步参考模型一致性测试
* URLTest 与 LoadBalance 共享健康测量，避免独立重复测速状态
* 负载均衡节点失败与全局惩罚采用独立错误判定，避免扩大故障范围

五、IPv4、IPv6 与拨号体系

* 单流 Admission Policy 判断耗时 11–22 ns，0 次分配
* 网络能力 Verdict 读取约 9.4 ns，0 次分配
* LocalFamilyGate 候选判断约 12–23 ns，0 次分配
* LocalFamilyFilter 单候选判断约 12.2 ns，0 次分配
* 候选计划网络所有权读取由逐候选读取收敛为一次读取
* 双候选路径网络能力读取次数由 4 次降至 2 次
* 网络能力判断不新增后台定时器与轮询 Goroutine
* 网络能力变化采用事件驱动路由表读取，避免每流查询路由表

六、数据通道与性能优化

* Direct Offload 资格判断耗时约 14–41 ns，0 次分配
* Direct Offload 快速拒绝判断耗时约 5–11 ns，0 次分配
* Direct Outbound Profile 查询约 5.3 ns，0 次分配
* Direct Offload 移除每流约 95 ns 的无效 Debug 参数构造
* Direct Offload 移除每流 5 次不必要的日志分配
* Direct BypassDecision 实测约 32.05 ns，0 次分配
* Direct Fast Path Eligibility 实测约 16.85 ns，0 次分配
* 无统计跟踪的模型流量中，85% 权重可通过 L1 资格判断
* 上传调度将高优先级流量 p99 延迟由 146 ms 降至 9 ms
* 上传调度将平均队列积压由 257 KiB 降至 15 KiB
* 持续整形将首条交互请求 p50 延迟由 127 ms 降至 2 ms
* 全通道整形将高优先级探测 p99 延迟由 114 ms 降至 8 ms
* 85% 带宽整形条件下 Bulk 吞吐约 1.72 MB/s
* 固定 85% 带宽整形条件下高优先级 p95 约 8 ms
* 未启用限速的调度路径额外开销约 9 ns/write，0 次分配
* UDP Batch-64 分配保持 66 次，与未包裹路径一致
* 上传整形速率上界回归测试限制为配置速率的 102%
* 64KiB 上传分块保持带宽整形精度与 Backpressure 约束

七、网络生命周期与资源管理

* 100 轮 Sleep/Wake 压力测试验证资源回收一致性
* 100 轮网络切换压力测试验证跨代资源归属
* 100 轮连接复用与回收测试验证不产生主动重拨
* 32 条繁忙连接下验证 Close 及资源回收
* 本地 E2E 测试中 ResetNetwork 约 140 µs 返回
* 三层 Failover 仍保持最多一次备用节点重试
* 空闲回收验证不创建额外拨号及后台连接

八、移动端省电与内存保护

* 低内存 Buffer Geometry 由 32KiB/16KiB 降至 16KiB/8KiB
* 低内存配置每流驻留 Heap 由 64.1 KiB 降至 32.1 KiB
* 低内存配置每次 Live Buffer 获取分配由 1.75 B 降至 0.94 B
* 低内存配置每次 Idle Buffer 获取分配由 0.93 B 降至 0.53 B
* 40MiB 内存约束下 GOGC 50→100，GC 次数减少 53.4%
* 40MiB 内存约束下 GOGC 50→100，GC 总暂停减少 51.8%
* 40MiB 内存约束下 GOGC 50→100，基准吞吐提高 1.23%
* GOGC 对照采用每组 10 轮交替测试
* iPhone 真机修复将一次睡眠周期错误拆分的 12 个边界收敛为 1 个
* iPhone 真机 13 分 13 秒锁屏周期测量精确到秒
* 设备休眠时 DNS 后台刷新网络请求由 1 次降至 0 次
* 设备休眠时 Dashboard 测试窗口内请求由 11 次降至 2 次
* 休眠期间保留 Dashboard 首次必要下载，避免首次显示空白
* macOS ARM64 无 TUN 实测空闲 RSS 约 43 MiB
* macOS ARM64 100 连接场景每条连接约占用 20–40 KB
* macOS ARM64 两轮连接测试间 RSS 增长约 1.6 MiB

九、Windows 与客户端整合

* Windows 核心构建辅助链接由 3 次收敛为 1 次
* Apple 客户端 iOS/macOS 共用一次 Libbox 产物构建
* Apple Libbox 工具链安装由 2 次收敛为 1 次
* Windows 安装包复用已构建内核，减少重复编译
* Apple Release 校验构建源与实际发布 Commit SHA 一致
* Windows 订阅用量查询仅读取响应头，不重复下载配置正文

十、构建、发布与工程质量

* 百万域名规则集实测常驻内存约 8.3 MiB
* 百万域名规则集实测首次加载分配约 42.5 MiB
* 百万域名规则集命中查询约 429 ns
* 百万域名规则集未命中查询约 338 ns
* 十万条 IP-CIDR 规则集常驻内存约 1.0 MiB
* 十万条 IP-CIDR 规则集首次加载分配约 7.8 MiB
* 十万条 IP-CIDR 规则集命中查询约 79 ns
* 千个冷态 Trojan 节点每节点约 2035 B
* 千个冷态 Trojan 节点启动后不增加 Goroutine
* 千个冷态节点通过全部四阶段启动测试
* Apple Libbox C-Archive 编译由 3 份减少至 2 份
* Apple 构建删除约 116 MB 的未使用 Intel Framework 切片
* Apple Gomobile 工具链重复安装由 2 次减少至 1 次
* Windows 核心辅助编译由 3 次收敛为 1 次
* Release 无效 CI 等待由 5400 秒缩短至约 2 秒
* 废弃 GitHub Actions 工件清理约 7191 MiB
* Sniff 性能回归覆盖时间与内存分配指标
* 上传调度对比覆盖 p50、p95、p99 与吞吐量
* XHTTP 吞吐对照覆盖 0–200ms RTT
* GC 对照记录吞吐、循环次数、暂停时间与峰值内存
* 大规模规则集测试区分常驻内存与加载峰值
* Direct Offload 零分配由测试断言保护

十一、专项 Bug 修复

* 修复 MASQUE Capsule 二次方复杂度导致的约 800 倍 CPU 放大
* 修复 MASQUE Capsule 恶意大规模条目导致的 17.65 MiB 堆占用
* 修复 TLS Sniff 不必要完整握手造成的 CPU 与内存浪费
* 修复 QUIC Sniff 重复 Reader 构造与无效缓冲分配
* 修复 Sniff 异常数据引起的缓冲区持续驻留
* 修复 Direct Offload 关闭 Debug 时仍执行日志分配
* 修复上传调度仅调整发送顺序却无法改善接收延迟的问题
* 修复上传整形大块写入未正确计入未来带宽债务的风险
* 修复网络代际切换时候选计划重复读取网络状态
* 修复睡眠期间 DNS 乐观后台刷新持续唤醒网络
* 修复睡眠期间 Dashboard 周期下载未被抑制
* 修复 Apple 睡眠周期被多次 Resume 错误截断
* 修复 CI 重复编译与工具链重复安装
* 修复 Release 长时间等待不存在的 CI 运行

详细清单
一、代理协议与传输层

* MASQUE HTTP/2、HTTP/3 支持
* MASQUE CONNECT 与 CONNECT-UDP
* MASQUE CONNECT-IP 数据通道
* MASQUE ADDRESS_ASSIGN 地址分配
* MASQUE ROUTE_ADVERTISEMENT 路由通告
* MASQUE DNS_ASSIGN 支持
* MASQUE PREF64 前缀处理
* MASQUE Capsule 编解码
* MASQUE Capsule 回退机制
* MASQUE DATAGRAM 批量传输
* MASQUE HTTP/3 连接池
* MASQUE HTTP/3 连接复用
* MASQUE HTTP/3 到 HTTP/2 自动回退
* MASQUE HTTP/3 失败退避
* MASQUE 回退状态网络切换重置
* MASQUE Bootstrap DNS 独立解析
* MASQUE Inner DNS 独立解析
* MASQUE DNS_ASSIGN 服务器优先级
* MASQUE IPv4/IPv6 候选端点竞争
* MASQUE 外层 QUIC 包大小约束
* MASQUE 内层隧道 MTU 管理
* MASQUE ICMP Packet Too Big 处理
* MASQUE 传输缓冲区所有权管理
* MASQUE 会话生命周期管理
* MASQUE 服务端 HTTP 请求伪装
* MASQUE 未认证连接回落
* 原生 NaiveProxy 服务端
* Cronet NaiveProxy 客户端
* Naive HTTP/1.1、HTTP/2、HTTP/3
* Naive UDP over TCP v1/v2
* Naive Padding 与 Framing
* Naive HTTP 流量伪装
* Naive HTTP 请求回落
* Naive 目标访问 ACL
* Naive UDP 逐包目标检查
* Naive SSRF 访问防护
* Naive 认证前连接数量限制
* Naive 认证前资源配额限制
* Naive CONNECT 目标权限校验
* Naive TCP 半关闭能力透传
* AnyTLS 默认 Fallback
* AnyTLS 按 ALPN 分类回落
* HTTP CONNECT 服务端扩展
* HTTP/2 Stream 资源配额
* HTTP/3 Stream 资源配额
* HTTP Header 超时与大小控制
* HTTP/2 Flow Control 参数管理
* HTTP/3 Flow Control 参数管理
* QUIC 拥塞控制参数支持
* XHTTP / SplitHTTP 客户端
* XHTTP auto 传输模式
* XHTTP packet-up 传输模式
* XHTTP stream-up 传输模式
* XHTTP stream-one 传输模式
* XHTTP HTTP/1.1、HTTP/2、HTTP/3
* XHTTP packet-up 批量上传
* XHTTP XMUX 连接复用
* XHTTP XMUX 连接池管理
* XHTTP X-Padding 混淆
* XHTTP Session 与 Sequence 管理
* XHTTP 上传分块控制
* XHTTP 上传节奏控制
* XHTTP H2 Keepalive 适配
* XHTTP H3 故障退避
* XHTTP 拨号取消与 Deadline 传播
* XHTTP 连接池休眠与唤醒回收
* VLESS 后量子混合加密
* VLESS Encryption 与 Vision 兼容
* REALITY 经典密钥交换兼容
* REALITY 混合密钥交换兼容
* REALITY 新旧 Xray 版本互通
* REALITY ClientHello 认证实现
* REALITY AEAD Session ID 处理
* REALITY 服务端证书验证
* TLS/uTLS 指纹兼容与保护
* TLS Fragment 与 Record Fragment
* TLS ClientHello 分片兼容
* TLS Fingerprint random 兼容约束
* gRPC service_name 路径兼容
* SOCKS5 认证连接池
* SOCKS5 TCP CONNECT 目标解析归属
* SOCKS5 UDP ASSOCIATE 目标解析归属
* SOCKS5 UDP over TCP 目标解析归属
* HTTP CONNECT 目标解析归属
* ShadowTLS v3 链路适配
* Shadowsocks 2022 链路适配
* ShadowTLS 与 SS2022 链式部署

二、DNS 与 FakeIP

* DNS Stable/Fastest/Parallel 分组
* DNS 分组并发查询扇出
* DNS 分组成员健康管理
* DNS 分组 TTL 驱动重新选举
* DNS 分组取消输家请求
* DNS 分组存活成员保底
* DNS 分组竞选 Singleflight
* A/AAAA 双栈并行解析
* DNS 查询 Singleflight 优化
* DNS Transport TCP 连接复用
* DNS Transport 生命周期管理
* DNS Transport 环境指纹绑定
* DNS 缓存按网络代际隔离
* DNS 缓存按策略纪元隔离
* DNS 缓存读写键一致性
* DNS 缓存写入竞态保护
* DNS 网络切换缓存保护
* DNS 远程服务器跨网缓存隔离
* DNS NXDOMAIN 跨网缓存隔离
* DNS 持久化缓存跨网隔离
* DNS RDRC 持久化结果隔离
* DNS 网络代际过期结果拒绝
* DNS 异步规则求值与恢复
* DNS 上游需求静态验证
* DNS 查询目标归属管理
* DNS Hijack 并发上限控制
* DNS Hijack 与 TUN 收包循环隔离
* DNS 本地 NXDOMAIN 广告拦截
* DNS 拦截结果支持负缓存
* DNS 拦截不请求上游服务器
* DNS 拦截与解析失败区分
* DNS 调用者取消不惩罚上游
* DNS 内部超时独立记录上游失败
* DNS 分组跨代重新竞选
* DNS 分组旧竞选令牌失效
* DNS 连接池网络切换重建
* DNS systemd-resolved 资源清理
* FakeIP 地址发行归属管理
* FakeIP 映射生命周期管理
* FakeIP 过期代际隔离
* FakeIP 重启后旧记录保护
* FakeIP 已退役地址段追踪
* FakeIP 发行代际游标管理
* FakeIP 持久化游标修复
* FakeIP 无效旧记录拒绝
* FakeIP 与真实目标语义隔离
* FakeIP 反向映射归属校验
* FakeIP 发行记录容量限制
* DNS 家族过滤按请求者生效

三、流量嗅探与协议识别

* TLS ClientHello 轻量解析
* TLS Sniff 避免完整握手
* TLS Sniff 避免不必要密钥生成
* TLS Sniff Reader 结构复用
* TLS Sniff 热路径分配优化
* TLS/QUIC SNI 标准化
* QUIC ClientHello 分片重组
* QUIC Frame 解析优化
* QUIC PeekPacket 缓冲优化
* HTTP Sniff 按条件执行
* WireGuard 协议嗅探
* WireGuard 与 uTP 识别优先级修正
* PeekStream 常数时间回退
* PeekStream 缓冲区边界保护
* Sniff 数据缓冲区归属保护
* Sniff 缓冲区重复释放保护
* Sniff 结果状态精确分类
* Sniff 未匹配状态独立记录
* Sniff 数据不足状态独立记录
* Sniff 与目标地址语义分离
* Sniff 超时与取消边界保护
* Sniff Goroutine 泄漏监测
* Sniff 重复运行性能回归测试

四、路由、代理组与智能选路

* ControlPath 逻辑路径建模
* PhysicalPath 物理路径建模
* RouteFlow 运行时可达性分析
* 启动前验证路径有效性
* Detour 链路完整性检查
* Detour 目标不存在时提前报错
* 跨类型依赖循环检测
* Tailscale Detour 依赖图检查
* 路径协议能力静态验证
* 路由需求与出口能力匹配
* 路由截断路径不可达判定
* 路径预览与实际选择分离
* Selector/URLTest 路由协同
* Selector 中间节点透明选路
* URLTest 预热后正式测速
* URLTest 统一延迟测量
* URLTest 共享健康状态
* URLTest 后台探测并发控制
* URLTest 真实业务失败触发复测
* URLTest 休眠节点跳过后台探测
* Flow-aware Load Balance
* Round Robin 负载均衡
* Consistent Hashing 负载均衡
* Sticky Sessions 会话粘滞
* Sticky Sessions 有界缓存
* Sticky Sessions 失效记录回收
* 负载均衡按 TCP 连接固定节点
* 负载均衡按 UDP 会话固定节点
* 负载均衡健康节点过滤
* 负载均衡失败后有限重试
* 嵌套代理组共享重试预算
* Failover 默认关闭兼容旧配置
* 连接重试与节点惩罚分离
* 网络错误按失败阶段分类
* 保守节点惩罚记录
* 路径故障惩罚与自动恢复
* 网络代际隔离故障记录
* 代理组三级 Fast Path 策略
* 嵌套代理组 Fast Path 优先级
* Fast Path 按连接独立判定
* Fast Path 不修改节点全局状态
* AI 代理组自动业务识别
* 路由诊断不改变真实选路

五、IPv4、IPv6 与拨号体系

* 双栈并发拨号与竞争
* IPv4/IPv6 本机能力检测
* IPv4/IPv6 本地地址能力读取
* IPv6 默认路由可达性判断
* IPv6 全局出口能力判断
* IPv6 地址与出口能力分离
* IPv6 无全局路由时拒绝候选
* IPv4/IPv6 候选地址筛选
* 双栈失败恢复与重新选路
* Windows Socket 错误分类
* Linux IPv6 非默认路由表读取
* Windows/macOS/Linux 路由表适配
* 网络能力统一快照发布
* 网络能力统一 Generation
* 网络能力过渡态 Settling 管理
* 网络能力发布与消费一致性
* 单条连接唯一 Admission Policy
* 单条连接统一策略快照
* 单条连接策略只捕获一次
* 串行与并行拨号策略统一
* Strict IPv4/IPv6 全路径约束
* 域名与字面量目标一致性
* TCP 与 UDP 地址族策略一致性
* 网络切换禁止过期候选建连
* 拨号过程检查网络所有权
* Socket 建立前最终资格校验
* 拨号与连接生命周期协同
* 连接失败分类与健康反馈
* 网络切换健康记录代际隔离
* 物理路径容量与拨号能力检查
* 既有连接不被新策略追溯关闭
* 不可达地址族恢复策略管理

六、数据通道与性能优化

* System Proxy 协同 Fast Path
* Linux Direct Offload
* Direct 旁路语义等价性检查
* Linux auto_redirect 原生旁路
* TUN L0 路由级绕行策略
* Direct 出站选项能力建模
* Direct 旁路逐连接资格判断
* Direct 旁路拒绝 FakeIP 目标
* Direct 旁路拒绝不明选项
* Direct 旁路拒绝流量跟踪器
* Splice 数据复制路径兼容检查
* Splice 交接与实际复制分别计数
* Buffer Pool 复用优化
* Buffer Geometry 分级配置
* 低分配流量处理
* 缓冲区借用与释放契约
* 缓冲区所有权转移保护
* QUIC DATAGRAM 所有权管理
* MASQUE 批量发送拒绝原子性
* UDP 批量发送与资源回收
* 业务感知上传流量调度
* Interactive/Realtime 优先级
* Default/Bulk 流量分类
* AI 流量自动分类
* 上传限速与 Backpressure
* 未启用调度时保持轻量
* Dial Governor 并发控制
* 流量统计与旁路一致性检查
* Hysteria2 传输包大小限制
* 路径 MTU 与协议封装开销校验
* QUIC 外层包大小单独控制
* 隧道内层 MTU 单独控制
* ICMP Packet Too Big 处理
* 嵌套隧道 UDP 分片兼容修复
* TLS Sniff 耗时降低 96.2%（报告基准）
* QUIC 分片 Sniff 耗时降低 91.9%（报告基准）
* Direct Offload 判定微基准测试

七、网络生命周期与资源管理

* Scope 统一资源归属
* Start/Close 分阶段生命周期
* 子 Scope 独立管理
* 资源逆序清理
* Close 并发调用结果一致
* Start 中取消正确报告失败
* Close 后注册资源立即清理
* 网络 Generation 单调递增
* Rebind Lease 独占恢复机制
* Rebind Lease 按代际失效
* Rebind Lease 取消信号传播
* 网络切换事件合并
* 网络切换事件去抖
* 新旧连接代际隔离
* 旧连接优雅排空
* 可疑连接禁止新流复用
* 失效连接分级回收
* Half-open 拨号取消管理
* 网络切换后的受控重连
* 首次启动同步建立网络环境
* DNS/HTTP 连接池协同回收
* HTTP 空闲连接生命周期管理
* Rule-set 更新器统一生命周期
* Rule-set Watcher 资源清理
* Rule-set 并发访问保护
* Close 后禁止资源复活
* 网络切换资源泄漏监测
* 连接池退役不主动拨号
* 引用管理器只清理空闲资源
* 网络切换压力与泄漏测试

八、移动端省电与内存保护

* 统一 Power Governor
* 屏幕、锁定和前后台状态感知
* Sleep/Wake 状态机
* 屏幕点亮与设备解锁分别处理
* 休眠事件与状态快照分别处理
* 休眠和网络断开分别处理
* 唤醒后三级连接复用策略
* 唤醒后可疑连接隔离
* 休眠期间抑制后台探测
* 唤醒后探测错峰执行
* 后台规则刷新休眠感知
* 后台 DNS 刷新休眠感知
* Dashboard 更新休眠感知
* 空闲连接主动清理
* 定时任务与唤醒频率控制
* 休眠期间保留活跃业务连接
* iOS NetworkExtension 低内存模式
* iOS 16KiB 缓冲区配置
* NetworkExtension OOM 保护
* Go Runtime 内存限制管理
* Go Heap 与进程内存分别监测
* Darwin 内存压力事件响应
* 进程 Memory Footprint 监控
* 内存压力分级回收
* Android TrimMemory 分级处理
* 主动 GC 与空闲内存归还
* 内存泄漏与 Goroutine 监测
* 休眠时长按真实睡眠周期计算
* 休眠唤醒事件重复上报抑制
* 功耗策略与网络恢复策略协同

九、Windows 与客户端整合

* Windows 独立服务进程体系
* Windows Daemon 与 Worker 分离
* Named Pipe 本地通信鉴权
* Named Pipe 对端进程身份校验
* Authenticode 签名身份验证
* 签名发布者固定验证
* 服务执行文件身份校验
* 服务安装与卸载安全检查
* 服务停止失败时拒绝卸载
* JiejieBox 独立安装目录
* 官方 sing-box 并行安装支持
* JiejieBox 独立服务名称
* JiejieBox 独立配置数据目录
* JiejieBox 独立注册表标识
* JiejieBox 独立应用身份
* 旧版 JiejieBox 数据接管
* 旧服务清理所有权验证
* 旧版安装路径识别
* 旧版工作目录恢复
* Windows 客户端自动更新源适配
* 订阅剩余流量解析与展示
* 订阅已用流量解析与展示
* 订阅过期时间解析与展示
* 订阅信息缺失时兼容 UA 重试
* 订阅用量快照持久化
* 订阅地址变更时旧快照清理
* 订阅配置正文与用量查询隔离
* Apple 客户端 Fork 与 Libbox 集成
* iOS/macOS Libbox 接口适配
* Android 客户端 AAR 集成
* Apple/Windows 品牌命名统一
* Native API 与 Clash API 协同
* 客户端设备状态向内核上报
* 核心统一执行功耗与回收策略
* Apple 构建源 Gitlink 固定
* Windows 安装器生成产物检查
* Windows 托盘停止状态修复

十、构建、发布与工程质量

* 保留上游协议注册体系
* 保留上游配置兼容性
* 平台专属 Build Tags
* 可选协议编译裁剪
* iOS/tvOS 独立低内存构建
* Linux/macOS/Windows 内核构建
* Android ARM64 内核构建
* Apple Libbox XCFramework 构建
* Windows 安装包自动构建
* Git Submodule 精确版本固定
* GitHub Actions 多平台验证
* Release 单一版本权威
* Release 精确 Commit SHA 验证
* SHA256 产物完整性校验
* 发布资产严格白名单
* Release Dry Run 预演
* 仅发布已验证的构建产物
* 发布后重新下载验证
* Windows 安装生命周期审计
* Windows 安装/卸载多阶段取证
* Apple 构建源与版本一致性校验
* Apple Release 禁止复用旧版源码
* Android AAR Build Tags 记录
* 上游兼容性自动检测
* 跨平台 ABI 兼容性检查
* Xray REALITY 跨版本互通测试
* XHTTP 四模式真实互通测试
* VLESS Encryption 与 Vision 互通测试
* SOCKS5/HTTP CONNECT 报文验证
* Go Race Detector 并发检查
* Mutation Test 反向验证
* 大规模规则集内存基准测试
* 冷节点资源开销测试
* 网络切换并发压力测试
* 生命周期资源泄漏回归测试
* 完整 JSON 配置加载回归测试
* XHTTP 长连接 Soak 测试
* Release 资产完整性反向测试
* CI 门禁空跑检测
* CI SKIP 与 PASS 严格区分
* 跨操作系统构建矩阵
* Linux、Windows、Darwin 构建验证
* FreeBSD 构建兼容验证
* 低内存构建配置验证
* 知识库基线与 Git 增量追踪

十一、专项 Bug 修复

协议与传输修复

* 修复 REALITY AEAD 附加数据错误
* 修复 REALITY Session ID 加密顺序
* 修复 REALITY 握手时间戳二次读取
* 修复 REALITY 证书验证回调丢失
* 修复 REALITY 连接新版 Xray 失败
* 修复 REALITY random 指纹不兼容
* 修复 REALITY short_id 过长导致崩溃
* 修复 TLS 禁用时 VLESS 空指针崩溃
* 修复 TLS 禁用时 Trojan 空指针崩溃
* 修复 XHTTP 配置反序列化缺失
* 修复 XHTTP 已取消请求仍返回连接
* 修复 XHTTP packet-up 忽略拨号取消
* 修复 XHTTP 停滞响应误判为数据流
* 修复 XHTTP HTTP/3 回退超时缺口
* 修复 XHTTP 严格禁止回退选项失效
* 修复 HTTP/2 Close 被阻塞写入拖住
* 修复 HTTP/3 异常关闭类型误判
* 修复 HTTP/3 正常关闭误记为故障
* 修复 QUIC 分片超出路径 MTU
* 修复 MASQUE H3 退避长期锁定 H2
* 修复 MASQUE 回退超时无法触发
* 修复 MASQUE H3/H2 回退状态跨网残留
* 修复 MASQUE 会话关闭后错误发布 Ready
* 修复 MASQUE 启动与关闭之间的竞态
* 修复 MASQUE DATAGRAM 上下文重复添加
* 修复 MASQUE 批量发送拒绝后的丢包
* 修复 MASQUE 路由截断误报可达
* 修复 Naive 连接包装丢失 TCP 半关闭
* 修复 gRPC service_name 转义不一致
* 修复 SOCKS5 UDP 中继地址 0.0.0.0 失效
* 修复 SOCKS5 UDP 中继地址 :: 失效
* 修复 TUIC 无效 udp_relay_mode 静默回退

SOCKS、首包与连接修复

* 修复 Mixed Inbound Early Data 数据竞争
* 修复 Mixed Inbound 首包载荷丢失
* 修复 SOCKS Inbound 首包载荷截断
* 修复 SOCKS 握手成功却触发关闭回调
* 修复 SOCKS4 USERID 伪造已认证用户
* 修复 SOCKS Early Data 缓冲区未释放
* 修复 SOCKS 首包 Read/Close 并发竞争
* 修复 SOCKS 首包重复消费风险
* 修复 SOCKS LazyConn 响应标志竞争
* 修复 SOCKS 连接重复发送响应帧
* 修复 SOCKS UDP 目标解析路径绕过
* 修复 HTTP CONNECT 目标解析归属错误
* 修复成功建连后元数据错误上报

DNS 与 FakeIP 修复

* 修复 DNS Hijack 阻塞 TUN 收包循环
* 修复 DNS Hijack 无限等待上游超时
* 修复 DNS 远程 Transport 跨网缓存污染
* 修复 DNS NXDOMAIN 跨网错误压制
* 修复 DNS RDRC 结果跨网络复用
* 修复 DNS 持久化记录环境隔离失效
* 修复 DNS 缓存清空后被旧查询回填
* 修复 DNS 缓存写入跨代竞态
* 修复 DNS 分组取消请求误记节点故障
* 修复 DNS 分组旧选举阻塞新网络
* 修复 DNS 过期选举错误释放新选举
* 修复 DNS 家族过滤污染共享缓存
* 修复 DNS Adblock 拒绝被当成查询失败
* 修复 DNS 后台刷新唤醒睡眠设备
* 修复 DNS 休眠权限初始化时序错误
* 修复 systemd-resolved 监控 Goroutine 泄漏
* 修复 FakeIP 发行游标写入失败被吞掉
* 修复 FakeIP 发行游标格式错误导致崩溃
* 修复 FakeIP 跨重启旧地址失效判断
* 修复 FakeIP 已退役地址段记录丢失
* 修复 FakeIP 旧代地址错误进入真实拨号
* 修复 FakeIP 网络切换后旧记录重新生效

WireGuard、TUN 与网络恢复修复

* 修复 WireGuard Detour 拨号无限阻塞
* 修复 WireGuard 已关闭 Bind 返回 nil 导致死循环
* 修复 WireGuard Bind 连接指针数据竞争
* 修复 WireGuard IPv6 初始化失败误杀 IPv4
* 修复 WireGuard 初始化与 Close 资源竞态
* 修复 WireGuard 关闭事件通道引发 Panic
* 修复 WireGuard 提前恢复触发器无法执行
* 修复 WireGuard Rebind 跨代 ABA 竞态
* 修复 WireGuard Rebind 取消信号未传播
* 修复 WireGuard Pause 注册路径丢失
* 修复 WireGuard 端点锁与 Pause 注册冲突
* 修复 WireGuard 休眠期间错误恢复
* 修复 Windows TUN 临时读错误导致数据通道退出
* 修复 TUN 停止过程资源重复关闭
* 修复 Tailscale Detour 缺失依赖导致栈溢出
* 修复 Tailscale 控制端口 80 黑洞问题
* 修复 NetworkManager 启动未完成时空指针
* 修复 NetworkManager 关闭后继续发布状态
* 修复启动后首条连接被网络观察事件取消
* 修复网络切换遗漏半连接取消边界

IPv4、IPv6 与拨号修复

* 修复 IPv6 无全局路由仍被判定可用
* 修复 IPv6 路由读取遗漏非默认表
* 修复 IPv6 路由读取错误拒绝现有路由
* 修复 IPv6 有接口却无地址时错误放行
* 修复 Windows Socket 错误码分类不完整
* 修复 Windows 缺失 Socket 常量引用
* 修复网络快照与候选规划跨代混用
* 修复串行拨号重复捕获网络策略
* 修复并行拨号丢失网络能力约束
* 修复严格地址族策略未覆盖字面量
* 修复严格地址族策略未覆盖 UDP
* 修复 Packet Dial 未过滤排除地址
* 修复已排除的地址被重新合成候选
* 修复迟到候选绕过 Admission Policy
* 修复网络能力过渡期错误硬拒绝
* 修复地址族恢复目标实际不可拨
* 修复失败连接同时返回 Socket 时资源泄漏
* 修复失败候选未正确记录过滤策略
* 修复连接计划跨代读取网络所有权
* 修复地址族惩罚跨网络代际延续
* 修复健康记录表无界增长
* 修复拨号网络所有权误取一般状态
* 修复出站 Detour 能力链错误穿透
* 修复拨号能力检测止步于中间节点
* 修复协议断言失败触发 Panic
* 修复 Failover 丢失每条流拨号选项
* 修复网络变化后旧策略污染新连接

路由与代理组修复

* 修复负载均衡嵌套重试次数倍增
* 修复 Failover 默认启用破坏旧配置
* 修复节点重试与全局惩罚混用
* 修复超时错误错误惩罚全部节点
* 修复 Sticky Sessions 历史队列无界
* 修复 Sticky Sessions 新粘滞记录被旧记录驱逐
* 修复 URLTest 后台探测唤醒休眠节点
* 修复 URLTest 过期检查轮次残留
* 修复 URLTest 健康记录更新竞态
* 修复直连路径穿过代理组后丢失 Fast Path
* 修复网络状态复位继承旧路由判决
* 修复路由运行上下文发布数据竞争
* 修复路由未识别 Sniff 状态混淆
* 修复 Stream/Packet Sniff 状态写反
* 修复 Direct UDP 连接诊断遗漏目标
* 修复连接失败诊断缺少真实流信息
* 修复路径诊断报告截断仍为可达

内存、并发与低功耗修复

* 修复 TLS Sniff 每流创建完整握手对象
* 修复 QUIC Sniff 每轮重复构建 Reader
* 修复 PeekPacket 产生无效临时分配
* 修复 Sniff SNI 未标准化导致路由误匹配
* 修复连接状态 Map 并发读写崩溃
* 修复连接迭代器返回可变共享切片
* 修复屏幕状态同步误造睡眠事件
* 修复设备 Resume 重复计算睡眠时长
* 修复睡眠设备仍执行 Dashboard 更新
* 修复 Power Observer 通知丢失
* 修复休眠后旧连接池无复用边界
* 修复网络恢复后旧连接继续接受新流
* 修复缓冲区提前 Close 无法归还池
* 修复 MASQUE Packet 缓冲区所有权错误
* 修复 TUN 匹配规则异常路径丢失缓冲区

Windows 与客户端修复

* 修复 Windows 过期测试证书阻止内核启动
* 修复 Windows 守护进程证书时间码误判
* 修复 Windows 旧服务误清理风险
* 修复 Windows 旧版服务归属识别错误
* 修复 Windows 安装 ID 为空
* 修复 Windows 误复用旧版安装 ID
* 修复 Windows 64 位注册表视图读取错误
* 修复 Windows 服务工作目录错误
* 修复 Windows 数据迁移路径识别错误
* 修复 Windows 旧版数据目录接管失败
* 修复 Windows 卸载失败未正确阻断
* 修复 Windows 静默安装被提示窗口阻塞
* 修复 Windows 卸载程序产物取证遗漏
* 修复 Apple Release 复用旧版本源码
* 修复 Apple Detached HEAD 发布失败
* 修复 Apple 构建源解析器错误
* 修复 Android AAR Build Tags 记录不一致

CI、构建与发布修复

* 修复 Release 资产定义多处重复
* 修复 Apple Libbox 发布名称不一致
* 修复 Libbox 产物未正确 gzip 打包
* 修复 Release Stage 目录缺失
* 修复发布前资产集合验证不完整
* 修复发布参数部分输出后才报错
* 修复 Release Gate 等待不存在的 CI
* 修复 Release Workflow 删除自身工具
* 修复 CI 空跑 SKIP 误判为 PASS
* 修复 Windows CI 读取 Linux 构建依赖图
* 修复 CI SIGPIPE 导致门禁误报
* 修复 Release Android 资源混入非公开文件
* 修复 Android 构建附加资源路径错误
* 修复 Windows 安装器取证脚本路径错误
* 修复 Apple 工作流声明测试却未执行
* 修复 Apple Gitlink 与构建源权威不一致
* 修复跨平台脚本 CRLF 换行错误
* 修复 CI Workflow 正则解析误判
* 修复 Windows Race 测试夹具数据竞争
* 修复测试门禁并发计数受全局状态干扰
* 修复 WireGuard 端口复用测试误报
* 修复 Release 证据与实际构建 SHA 不一致
* 修复版本发布脚本环境依赖遗漏
