# 值得保留的架构边界（Architecture boundaries worth preserving）

本文列出 11 条需要维护的架构边界。每条边界都附有对应模块，说明设计原因、源码和验证范围。

## 1. 所有权：Scope 拥有其注册的反向清理（Ownership: a Scope owns its registered reverse cleanups）

`Scope.Close` 按注册的相反顺序执行清理函数，也会清理 `Scope.Start` 注册的子 scope。
资源的关闭函数必须由合适的所有者负责，但资源也可能由其他所有者关闭，因此不能只凭缺少
一次直接的 `scope.Add` 就断定发生泄漏。

关闭后才注册的清理函数会**在注册它的 goroutine 中同步执行**；`Close` 不等待正在进行的
`Start`。如果清理函数 panic，本轮清理会中断；`Box.Close` 会捕获并记录错误，不会继续完成
被中断的清理。
→ `modules/core-lifecycle-runtime.md`

## 2. 单一网络代次，重建是租约而不是标志位（One network generation, and a rebuild is a lease, not a flag）

`common/runtimecoord` 使用单调递增的 epoch 标识网络变化。它用 `*RebindLease` 的身份保证
同一资源同时只有一个有效的恢复操作：旧租约被替换后，不能释放新租约。
`Stale()` 和 `Acknowledge()` 分别用于判断资源是否过期、确认资源已跟上当前 epoch。
发布新的 epoch 并不等于立即重建所有资源；过期资源会在需要时按相应策略退役。
→ `modules/core-lifecycle-runtime.md`

## 3. 选择在被提交之前只是预览（Selection is a preview until it is committed）

路由规则先选出出站入口；如果入口是代理组，再由组选择成员。组的选择属于控制面，
**组本身不是物理网络跳点**。不过有些组会包装返回的连接，以管理中断和生命周期
（`protocol/group/selector.go`、`urltest.go`），不能因此认为组从不包装连接。

运行时通过 `commit` 标志区分预览与正式选择：预览不能推进轮询游标。诊断结果也必须保持
物理跳点的实际顺序，不能把代理组当作一跳或反转跳点列表。
→ `modules/path-selection-validation.md`

## 4. 启动期校验只报告，不实际走路径（Start-time validation reports without exercising the path）

出站校验在依赖排序之后、启动出站之前运行；此时 Box 的其他初始化可能已经执行。
校验不会拨号、解析 DNS、改变组选择或消耗轮询槽位。遇到无法判断的叶子节点时，它会返回
`Unknown`，不会把只检查了一部分的路径报告为已验证。

`common/physicalpath/dryrun.go` 区分致命错误和能力警告。例如，detour 虽然存在却无法
满足使用方要求时，可能只产生警告，但相关流量在运行时仍会失败；缺失引用等致命问题不能
一概降级。具体分类见路径选择模块。

物理路径解析器不持有 dialer 或时钟，但这不能证明所有外围调用都没有副作用。已记录的例外
包括一条弃用日志和一次带缓存的 tag 到对象解析；它们不涉及网络解析或拨号。
→ `modules/path-selection-validation.md`

## 5. 平台事实是输入，由核心决定策略（Platform facts are inputs; the core decides policy）

客户端向核心报告操作系统提供的状态和事件，例如内存压力、屏幕状态、锁定状态与设备唤醒。
核心据此决定内存回收、事件合并和连接复用策略。

当前状态（level）、状态变化事件（edge）和快照是不同概念：重新读取状态可以校准当前值，
但不能因此凭空产生一次睡眠或唤醒事件。
→ `modules/power-mobile-lifecycle.md`

## 6. 嗅探元数据只用于规划，不改写目标地址（Sniff metadata informs planning; it does not rewrite the destination）

嗅探出的域名可用于 DNS 规划和选择拨号候选；嗅探本身不会改写 `Destination`，显式使用
已弃用的 `override_destination` 动作除外。FakeIP 反向映射是另一条路径：
`route/route.go` 会保存 `OriginDestination`，并将 `Destination` 替换为映射出的 FQDN。
不要把 FakeIP 映射和嗅探改写目的地址混为一谈。
→ `modules/sniff.md`

## 7. 缓冲区所有权是正确性契约，不是优化面（Buffer ownership is a correctness contract, not an optimisation surface）

借用的 cursor 只能在所属调用期间使用；QUIC 的工作副本刻意不放入缓冲池。批量发送按
chunk 转移所有权：已接受的 chunk 已经交给下游，未接受的部分仍由调用方负责。

替换 context ID 的操作会**释放原始对象**，因此处理受影响的 chunk 时必须遵守对应的拒绝和
更新规则。这些边界有实际故障记录，例如被拒绝批次中的数据包曾被静默丢弃。
→ `modules/buffer-ownership.md`

## 8. MTU 是三个量，而不是一个（MTU is three quantities, not one）

外层链路 MTU、外层 QUIC 包大小和内层隧道 MTU 是不同的量。MASQUE 的初始 QUIC 包大小
受已知的 detour 容量限制；仅此证明不了后续包存在硬上限。TUIC 有一条单独的硬上限路径。
物理路径容量未知，不构成证明。
→ `modules/mtu-and-geometry.md`

## 9. 上传调度器需显式开启，未设置时不产生作用（The upload scheduler is opt-in and inert when unset）

`traffic_scheduler` 和 `traffic_class` 是本项目扩展的配置项。未配置速率时，调度器仍会
统计受管理的字节，但直接放行，不启动队列、锁或定时器。这个事实本身不能证明配置与其他
实现完全兼容，也不能证明端到端行为一致。

配置速率后，流量整形会持续生效，而不是等到发生竞争时才启动。
→ `modules/data-path-optimization.md`

## 10. Direct Offload 是语义等价，其数据面效果仅在 Linux 上（Direct Offload is semantic equivalence, and its data-plane effect is Linux-only）

只有拨号选项确实影响当前流时，它才应影响该流的绕行资格。新增 `DialerOptions` 字段时，
未分类的选项默认拒绝绕行，不能假定其安全。

获得绕行资格不等于实际绕行：当前只有 Linux `auto_redirect` 路径会执行这里的隐式绕行；
普通 TUN 协议栈不会执行该判定。
→ `modules/data-path-optimization.md`

## 11. 发布溯源是下载并重新校验，且范围有限（Release provenance is download-and-re-verify, and it is scoped）

历史发布流程会下载产品工作流针对指定提交生成的产物，重新校验产物字节、哈希和提交记录，
而不是重新构建。这个结论仅适用于当时审阅的工作流；每次发布都应重新核对当前流程和运行
证据。

工作流声明不代表任务已执行。`modules/windows-daemon-security.md` 记录的一次 Windows
守护进程 CI 运行，也不能单独证明完整的发布产物流程。
→ `modules/ci-release.md`

---

## 模块地图（The module map）

`modules/README.md` 把任务映射到该读的模块。只加载任务真正涉及的那几个模块。

## 两条长期约定（Two standing conventions）

这些是**约定**，不是不变量 —— 它们在这里已经成熟，未来的变更只要给出理由，就可以
合理地背离它们：

1. **把源码与其测试产物放在同一个变更里。** 本分支自己的历史演示了不这么做会怎样：
   一个不可能失败的 goroutine 普查测试；一个 TUN 测试直接调 `inbound.Close()`（产品
   从不这么做），产品真泄漏了，测试还是绿的；以及一个只断言成功路径的 splice 测试。
2. **行为断言优先，别用源文本守卫。** 在依赖里 grep 一个字符串，会在
   行移动时失败、在含义改变时通过 —— 恰好反过来。
   `docs/UPSTREAM-SYNC.md:89-94` 讲了这一点，也指出了做不到的地方。
