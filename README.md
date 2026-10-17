一、代理协议与传输层

* MASQUE HTTP/2、HTTP/3 支持
* MASQUE CONNECT 与 CONNECT-UDP
* MASQUE CONNECT-IP 数据通道
* MASQUE 地址动态分配
* MASQUE 路由通告
* MASQUE DNS_ASSIGN 支持
* MASQUE PREF64 地址转换支持
* MASQUE Capsule 回退机制
* MASQUE DATAGRAM 批量传输
* MASQUE HTTP/3 连接池
* MASQUE HTTP/3 至 HTTP/2 有界回退
* MASQUE 双 DNS 解析器隔离
* MASQUE 分片与 MTU 控制
* MASQUE ICMP Packet Too Big 处理
* MASQUE 会话生命周期管理
* 原生 NaiveProxy 服务端
* Cronet NaiveProxy 客户端
* Naive UDP over TCP v1/v2
* Naive Padding 与流量伪装
* Naive 目标访问 ACL
* Naive UDP 逐包目标检查
* Naive SSRF 安全防护
* Naive 认证前资源限制
* Naive HTTP 伪装与回落
* AnyTLS 默认 Fallback
* AnyTLS 按 ALPN 分类回落
* HTTP CONNECT 服务端扩展
* HTTP/2、HTTP/3 资源配额控制
* HTTP/2、HTTP/3 流控优化
* XHTTP / SplitHTTP 客户端
* XHTTP 四种传输模式
* XHTTP HTTP/1.1、H2、H3
* XHTTP packet-up 批量上传
* XHTTP XMUX 连接复用
* XHTTP X-Padding 混淆
* XHTTP 连接池故障退避
* XHTTP 上传节奏与分块控制
* XHTTP Session 与 Sequence 管理
* VLESS 后量子混合加密
* REALITY 混合握手兼容修复
* REALITY 新旧 Xray 版本互通
* REALITY 服务端验证逻辑修复
* TLS/uTLS 指纹兼容与保护
* TLS Fragment 处理优化
* gRPC service_name 兼容修复
* SOCKS5 认证连接池
* SOCKS5 TCP/UDP 目标解析归属
* HTTP CONNECT 目标解析归属
* ShadowTLS v3 与 SS2022 链路适配

二、DNS 与 FakeIP

* DNS Stable/Fastest/Parallel 分组
* A/AAAA 双栈并行解析
* DNS 查询 Singleflight 优化
* DNS Transport 连接复用
* DNS Transport 生命周期管理
* DNS 缓存按网络代际隔离
* DNS 缓存按策略纪元隔离
* DNS 缓存写入竞态保护
* DNS 网络切换缓存保护
* DNS 异步规则求值与恢复
* DNS 上游需求静态验证
* DNS 查询目标归属管理
* DNS 查询并发控制
* DNS 本地 NXDOMAIN 广告拦截
* DNS 拦截结果支持负缓存
* DNS 拦截不请求上游服务器
* DNS 失败与拦截结果分类
* DNS 连接池跨代重建与回收
* DNS 系统解析器资源泄漏修复
* FakeIP 地址发行归属管理
* FakeIP 映射生命周期管理
* FakeIP 过期代际隔离
* FakeIP 重启后旧记录保护
* FakeIP 持久化游标修复
* FakeIP 与真实目标语义隔离
* FakeIP 无效占位地址拒绝
* DNS 家族过滤按请求者生效

三、流量嗅探与协议识别

* TLS ClientHello 轻量解析
* TLS Sniff 避免完整握手
* TLS/QUIC SNI 标准化
* QUIC ClientHello 分片重组
* QUIC 帧解析与内存优化
* HTTP Sniff 按条件执行
* WireGuard 协议嗅探
* WireGuard 与 BitTorrent 误判修复
* PeekStream 常数时间回退
* Sniff 数据缓冲区归属保护
* Sniff 结果状态精确分类
* Sniff 与目标地址语义分离
* Sniff 超时与取消边界保护

四、路由、代理组与智能选路

* ControlPath 逻辑路径建模
* PhysicalPath 物理路径建模
* RouteFlow 运行时可达性分析
* 启动前验证路径有效性
* Detour 链路完整性检查
* 跨类型依赖循环检测
* 路径协议能力静态验证
* 路径预览与实际选择分离
* Selector/URLTest 路由协同
* URLTest 预热后正式测速
* URLTest 统一延迟测量
* URLTest 共享健康状态
* URLTest 后台探测并发控制
* Flow-aware Load Balance
* Round Robin 负载均衡
* Consistent Hashing 负载均衡
* Sticky Sessions 会话粘滞
* 负载均衡按连接固定节点
* 负载均衡健康节点过滤
* 失败后有限备用节点重试
* 跨嵌套分组共享重试预算
* 路径故障惩罚与自动恢复
* 网络代际隔离故障记录
* 代理组三级 Fast Path 策略
* 嵌套代理组 Fast Path 优先级
* Fast Path 按连接独立判定
* AI 代理组自动业务识别
* 路由诊断不改变真实选路

五、IPv4、IPv6 与拨号体系

* 双栈并发拨号与竞争
* IPv4/IPv6 本机能力检测
* IPv6 默认路由可达性判断
* IPv6 地址与出口能力分离
* IPv6 不可用时阻止错误拨号
* IPv4/IPv6 候选地址筛选
* 双栈失败恢复与重新选路
* Windows Socket 错误分类
* 多平台路由表状态读取
* 网络能力统一快照发布
* 网络能力统一 Generation
* 网络能力过渡状态管理
* 单条连接唯一 Admission Policy
* 单条连接统一策略快照
* 串行与并行拨号策略一致性
* 网络切换禁止过期候选建连
* 拨号过程检查网络所有权
* 拨号与连接生命周期统一管理
* 连接失败分类与健康反馈
* 严格地址族策略全路径覆盖
* 物理路径容量与拨号能力检查
* 网络代际切换清理旧健康惩罚
* 网络状态变化保护已有连接

六、数据通道与性能优化

* System Proxy 协同 Fast Path
* Linux Direct Offload
* Direct 旁路语义等价性检查
* Linux auto_redirect 原生旁路
* TUN L0 路由级绕行策略
* Splice 内核数据复制路径审计
* Buffer Pool 复用优化
* Buffer Geometry 分级配置
* 低分配流量处理
* Buffer 借用与释放安全保护
* QUIC DATAGRAM 所有权管理
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
* 内外层 MTU 独立建模
* ICMP Packet Too Big 处理
* 嵌套隧道 UDP 分片兼容修复

七、网络生命周期与资源管理

* Scope 统一资源归属
* Start/Close 分阶段生命周期
* 资源逆序清理
* 并发关闭与清理完成边界
* 网络 Generation 单调递增
* Rebind Lease 独占恢复机制
* 网络切换事件合并
* 新旧连接代际隔离
* 旧连接优雅排空
* 可疑连接禁止新流复用
* 失效连接分级回收
* Half-open 拨号取消管理
* 网络切换后的受控重连
* DNS/HTTP 连接池协同回收
* Rule-set 更新器统一生命周期
* Rule-set Watcher 资源清理
* Rule-set 并发访问保护
* Close 后禁止资源复活
* 连接池分级裁剪与完全释放
* 网络切换压力与泄漏测试

八、移动端省电与内存保护

* 统一 Power Governor
* 屏幕、锁定和前后台状态感知
* Sleep/Wake 状态机
* 休眠和网络断开分别处理
* 唤醒后三级连接复用策略
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

九、Windows 与客户端整合

* Windows 独立服务进程体系
* Windows Daemon 与 Worker 分离
* Named Pipe 本地通信鉴权
* Authenticode 签名身份验证
* 服务执行文件身份校验
* 服务安装与卸载安全检查
* JiejieBox 独立安装目录
* 官方 sing-box 并行安装支持
* JiejieBox 独立服务名称
* JiejieBox 独立配置数据目录
* JiejieBox 独立注册表与 IPC 标识
* 旧版 JiejieBox 数据接管
* 旧服务清理所有权验证
* Windows 客户端自动更新源适配
* 订阅剩余流量解析与展示
* 订阅过期时间解析与展示
* 订阅信息缺失时兼容 UA 重试
* 订阅用量快照持久化
* 订阅地址变更时旧快照清理
* 订阅配置正文与用量查询隔离
* Apple 客户端 Fork 与 Libbox 集成
* Android 客户端 AAR 集成
* Apple/Windows 品牌命名统一
* Native API 与 Clash API 协同
* 客户端设备状态向内核上报
* 核心统一执行功耗与回收策略

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
* Apple 构建源与版本一致性校验
* 上游兼容性自动检测
* 跨平台 ABI 兼容性检查
* Xray REALITY 跨版本互通测试
* XHTTP 多模式实际互通测试
* SOCKS5/HTTP CONNECT 报文验证
* 真实网络 E2E 测试
* Go Race Detector 并发检查
* Mutation Test 反向验证
* 大规模规则集内存基准测试
* 冷节点资源开销测试
* 网络切换并发压力测试
* 生命周期资源泄漏回归测试
* 知识库基线与 Git 增量追踪