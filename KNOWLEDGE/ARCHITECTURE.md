# 值得保留的架构边界（Architecture boundaries worth preserving）

十一条边界。每一条都是那种“看起来合理、实际却是错的”改动所在之处，并且每一条都
连同承载细节的模块一起陈述。

## 1. 所有权：Scope 拥有其注册的反向清理（Ownership: a Scope owns its registered reverse cleanups）

`Scope.Close` 按相反顺序排空其注册的清理函数，包括 `Scope.Start` 安装的子作用域
清理；资源关闭函数必须注册到合适的所有者。其他所有者也可以执行拆除，所以缺少直接的 `scope.Add`
本身并不能证明资源泄漏。在 close 返回之后注册的清理会**在注册它的 goroutine 上同步运行**，
而 `Close` **不会**等待正在进行的 `Start`。清理函数 panic 会中断排空；`Box.Close` 会把该 panic
记录为错误，而不是完成那次排空。
→ `modules/core-lifecycle-runtime.md`

## 2. 单一网络代次，重建是租约而不是标志位（One network generation, and a rebuild is a lease, not a flag）

`common/runtimecoord` 发布一个单调的纪元（epoch）。“一次只做一次逻辑恢复”是一个**租约
身份**（`*RebindLease`），而不是布尔值，所以被取代的租约无法释放更新的租约。`Stale()`
与 `Acknowledge()` 把“纪元移动了”和“资源是当前的”区分开。**发布不是重建**，
过期资源在被需要时惰性退役。
→ `modules/core-lifecycle-runtime.md`

## 3. 选择在被提交之前只是预览（Selection is a preview until it is committed）

路由规则选择一个出站**根**；随后由组进行选择。组是控制面的
选择，**不是物理跳**。某些组会包装返回的连接以做中断与
生命周期管理（`protocol/group/selector.go`、`urltest.go`），所以不要推断“选择”
意味着组从不包装连接。在运行时，
`commit` 标志正是这一区分：预览绝不能消耗轮换槽位。任何把组报告为
一跳、或把跳列表报告为反序的诊断，描述的都是一条流量实际不会走的路径。
→ `modules/path-selection-validation.md`

## 4. 启动期校验只报告，不实际走路径（Start-time validation reports without exercising the path）

出站校验在依赖排序**之后**、出站启动**之前**运行。其他 Box
初始化可能已经运行。它不拨号、不解析、不改变选择、不消耗轮换。物理路径解析器本身
不持有 dialer 或时钟；仅凭这一事实并不能证明每一层外围调用都没有副作用。
当叶子节点不可知时，它报告
`Unknown` 并停止，因此部分验证的路径不会被误认为已验证的路径。当前的
`common/physicalpath/dryrun.go` 区分致命失败与能力警告：一个存在但无法满足
消费者的 detour 可以发出警告，而受影响的流在运行时仍然失败；
缺失的引用和其他致命类别不能被整体降级。精确分类见
路径选择模块。

**存在两处有文档记载的附带副作用**（一条弃用日志和一次记忆化的 tag→对象
解析）。禁令针对的是网络解析、拨号、改变选择和消耗
轮换，而不是日志或所有内部记忆化。
→ `modules/path-selection-validation.md`

## 5. 平台事实是输入，由核心决定策略（Platform facts are inputs; the core decides policy）

客户端报告其操作系统给它的数字（内存级别、屏幕开/关、锁定状态、设备唤醒）。
**核心**拥有裁剪阶梯（trim ladder）、合并窗口、睡眠/唤醒边沿事件以及复用策略。
级别、边沿和快照是三件不同的事：重新读取当前状态会**校准级别**，
且绝不能捏造一个边沿事件。
→ `modules/power-mobile-lifecycle.md`

## 6. 嗅探元数据只用于规划，不改写目标地址（Sniff metadata informs planning; it does not rewrite the destination）

嗅探出的域名成为 DNS 规划与拨号候选；除非使用显式且已弃用的 `override_destination`
动作，嗅探本身不会改写 `Destination`。另外，
`route/route.go` 中的 FakeIP 反向映射会保存 `OriginDestination`，并用映射后的 FQDN
替换 `Destination`。不要把这种翻译与嗅探驱动的覆盖混为一谈。
→ `modules/sniff.md`

## 7. 缓冲区所有权是正确性契约，不是优化面（Buffer ownership is a correctness contract, not an optimisation surface）

借用的游标（cursor）不得存活于其调用之外；QUIC 工作副本刻意不进入池；批量
发送按块是全部或全无的，并且更早被接受的块已经转移了所有权。上下文 ID
替换会**释放原始对象**，并且必须拒绝其块。每条规则的存在都是因为违反它产生过
真实的 bug —— 包括一次被拒绝批次中的每个包都被悄悄丢弃。
→ `modules/buffer-ownership.md`

## 8. MTU 是三个量，而不是一个（MTU is three quantities, not one）

外层链路 MTU、外层 QUIC 包大小和内层隧道 MTU 是不同的量。对 MASQUE，
已知的 detour 容量会钳制初始 QUIC 包大小；仅此并不能证明对
后续包存在硬上限。TUIC 有一条单独的硬上限路径。未知的物理路径容量不构成证明。
→ `modules/mtu-and-geometry.md`

## 9. 上传调度器需显式开启，未设置时不产生作用（The upload scheduler is opt-in and inert when unset）

`traffic_scheduler` 与 `traffic_class` 是分支独有的键。没有速率时，门禁会观察每一个
受管理的字节，全部放行，并且**不启动任何队列、任何锁、任何定时器**。仅一个未激活的整形
门禁并不能证明与另一种实现完全配置兼容或端到端行为一致。配置了速率时，整形会**持续**
运行，而不仅仅在有争用时运行。
→ `modules/data-path-optimization.md`

## 10. Direct Offload 是语义等价，其数据面效果仅在 Linux 上（Direct Offload is semantic equivalence, and its data-plane effect is Linux-only）

一个拨号选项只有在用户态路径确实会把它**应用到该流上**时，才使该流不合格。
新的 `DialerOptions` 字段不得能让绕过默认变得 *安全*：未分类的选项一律拒绝。
而且该判定**仅**在 Linux `auto_redirect` 中才是真正的绕过 —— 没有任何 TUN 协议栈会遵守它。
→ `modules/data-path-optimization.md`

## 11. 发布溯源是下载并重新校验，且范围有限（Release provenance is download-and-re-verify, and it is scoped）

发布流程会**下载**某个产品工作流为精确提交产生的产物，并重新校验
各平台的字节、哈希与记录的提交 —— 它不重新构建。这一描述的范围限于
历史性的发布工作流，每个 release 都需要重新核对当前计划与运行证据。
声明不证明执行；一次具体的 Windows 守护进程 CI 运行记录在
`modules/windows-daemon-security.md` 中，它本身并不证明发布产物的流水线。
→ `modules/ci-release.md`

---

## 模块地图（The module map）

`modules/README.md` 把任务映射到要读的模块。只加载你的任务涉及的部分。

## 两条长期约定（Two standing conventions）

这些是**约定**，不是不变量 —— 它们在这里是成熟的实践，未来的变更可以
在给出理由的情况下合理地背离它们：

1. **把源码与其测试产物放在同一个变更里。** 本分支自己的历史展示了没有它会怎样：
   一个不可能失败的 goroutine 普查测试、一个直接调用 `inbound.Close()`（产品从不这样做）
   并在产品泄漏时仍然保持绿色的 TUN 测试，以及一个只断言成功路径的 splice 测试。
2. **优先使用行为断言，而不是源文本守卫。** 在依赖中 grep 一个字符串，会在
   行移动时失败、在含义改变时通过 —— 恰好反过来。
   `docs/UPSTREAM-SYNC.md:89-94` 陈述了这一点，并指出了无法这样做的地方。
