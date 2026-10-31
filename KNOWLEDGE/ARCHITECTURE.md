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

出站校验在依赖排序**之后**、出站启动**之前**运行。别的 Box 初始化可能已经跑过了。
它不拨号、不解析、不改变选择、不消耗轮换。物理路径解析器自己不持 dialer 或时钟；
但仅凭这一点，证明不了每一层外围调用都没有副作用。叶子节点不可知时，它报 `Unknown`
然后停下，所以只验证了一半的路径不会被当成已验证的路径。现在的
`common/physicalpath/dryrun.go` 区分致命失败与能力警告：一个存在、但满足不了消费者的
detour 可以发警告，受影响的流在运行时照样失败；缺失的引用和其他致命类别不能整体降级。
精确分类见路径选择模块。

**有两处附带副作用是有文档记载的**（一条弃用日志和一次记忆化的 tag→对象 解析）。
禁令管的是网络解析、拨号、改变选择和消耗轮换，不管日志，也不管所有内部记忆化。
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

借来的游标（cursor）不能超出它所属的那次调用；QUIC 的工作副本刻意不进池；批量发送
按块算，是全有或全无，而更早被接受的块已经转移了所有权。替换上下文 ID 会
**释放原始对象**，并且必须拒掉它的块。每条规则都有来由：违反它出过真的 bug —— 包括
一次被拒绝的批次里，每个包都被悄悄丢掉。
→ `modules/buffer-ownership.md`

## 8. MTU 是三个量，而不是一个（MTU is three quantities, not one）

外层链路 MTU、外层 QUIC 包大小和内层隧道 MTU 是不同的量。MASQUE 的初始 QUIC 包大小
受已知的 detour 容量限制；仅此证明不了后续包存在硬上限。TUIC 有一条单独的硬上限路径。
物理路径容量未知，不构成证明。
→ `modules/mtu-and-geometry.md`

## 9. 上传调度器需显式开启，未设置时不产生作用（The upload scheduler is opt-in and inert when unset）

`traffic_scheduler` 与 `traffic_class` 是分支独有的键。没有速率时，门禁照样观察每一个
受管理的字节，全部放行，并且**不启动任何队列、任何锁、任何定时器**。但一个没激活的整形
门禁，证明不了配置与另一种实现完全兼容，也证明不了端到端行为一致。配了速率时，整形会
**持续**运行，不只是有争用的时候才跑。
→ `modules/data-path-optimization.md`

## 10. Direct Offload 是语义等价，其数据面效果仅在 Linux 上（Direct Offload is semantic equivalence, and its data-plane effect is Linux-only）

只有拨号选项确实影响当前流时，它才应影响该流的绕行资格。新增 `DialerOptions` 字段时，
未分类的选项默认拒绝绕行，不能假定其安全。

获得绕行资格不等于实际绕行：当前只有 Linux `auto_redirect` 路径会执行这里的隐式绕行；
普通 TUN 协议栈不会执行该判定。
→ `modules/data-path-optimization.md`

## 11. 发布溯源是下载并重新校验，且范围有限（Release provenance is download-and-re-verify, and it is scoped）

发布流程会**下载**某个产品工作流为精确提交产出的产物，再重新校验各平台的字节、哈希
与记录的提交 —— 它不重新构建。这一描述只覆盖历史发布工作流；每个 release 都要
重新核对当前计划与运行证据。声明不证明执行；一次具体的 Windows 守护进程 CI 运行记在
`modules/windows-daemon-security.md` 里，它本身证明不了发布产物的流水线。
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
