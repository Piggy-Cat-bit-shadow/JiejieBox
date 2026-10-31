# 值得保留的架构边界（Architecture boundaries worth preserving）

十一条边界。每一条都对应一处“看着合理、实际是错的”改动，而且每一条都连同承载细节的
模块一起给出。

## 1. 所有权：Scope 拥有其注册的反向清理（Ownership: a Scope owns its registered reverse cleanups）

`Scope.Close` 按相反顺序排空它注册的清理函数，包括 `Scope.Start` 装上的子作用域
清理；资源关闭函数必须注册到合适的所有者。拆除也可以由别的所有者执行，所以单看有没有
直接的 `scope.Add`，证明不了资源泄漏。close 返回之后注册的清理会
**在注册它的 goroutine 上同步运行**，而 `Close` **不会**等正在进行的 `Start`。清理函数
panic 会中断排空；`Box.Close` 把这个 panic 记为错误，而不是把那轮排空做完。
→ `modules/core-lifecycle-runtime.md`

## 2. 单一网络代次，重建是租约而不是标志位（One network generation, and a rebuild is a lease, not a flag）

`common/runtimecoord` 发布单调的纪元（epoch）。“同一时刻只做一次逻辑恢复”靠的是
**租约身份**（`*RebindLease`），不是布尔值，所以被取代的租约释放不了更新的租约。
`Stale()` 与 `Acknowledge()` 区分开“纪元移动了”和“资源是当前的”。**发布不是重建**：
过期的资源等到被需要时才惰性退役。
→ `modules/core-lifecycle-runtime.md`

## 3. 选择在被提交之前只是预览（Selection is a preview until it is committed）

路由规则选出一个出站**根**，之后由组来选择。组的选择属于控制面，**不是物理跳**。
有些组会包装返回的连接，用来做中断与生命周期管理（`protocol/group/selector.go`、
`urltest.go`），所以别推断“选择”意味着组从不包装连接。运行时这个区分落在
`commit` 标志上：预览绝不能消耗轮换槽位。任何把组报成一跳、或把跳列表报成反序的诊断，
描述的都是一条流量实际上不会走的路径。
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

客户端报告操作系统给它的数字（内存级别、屏幕开/关、锁定状态、设备唤醒）。
**核心**掌握裁剪阶梯（trim ladder）、合并窗口、睡眠/唤醒边沿事件以及复用策略。
级别、边沿和快照是三件不同的事：重新读取当前状态会**校准级别**，
绝不能凭空造出一个边沿事件。
→ `modules/power-mobile-lifecycle.md`

## 6. 嗅探元数据只用于规划，不改写目标地址（Sniff metadata informs planning; it does not rewrite the destination）

嗅探出的域名会成为 DNS 规划与拨号候选；嗅探本身不改写 `Destination`，除非用了显式
且已弃用的 `override_destination` 动作。另外，
`route/route.go` 里的 FakeIP 反向映射会保存 `OriginDestination`，再用映射后的 FQDN
替换 `Destination`。别把这种翻译与嗅探驱动的覆盖混为一谈。
→ `modules/sniff.md`

## 7. 缓冲区所有权是正确性契约，不是优化面（Buffer ownership is a correctness contract, not an optimisation surface）

借来的游标（cursor）不能超出它所属的那次调用；QUIC 的工作副本刻意不进池；批量发送
按块算，是全有或全无，而更早被接受的块已经转移了所有权。替换上下文 ID 会
**释放原始对象**，并且必须拒掉它的块。每条规则都有来由：违反它出过真的 bug —— 包括
一次被拒绝的批次里，每个包都被悄悄丢掉。
→ `modules/buffer-ownership.md`

## 8. MTU 是三个量，而不是一个（MTU is three quantities, not one）

外层链路 MTU、外层 QUIC 包大小和内层隧道 MTU 是不同的量。MASQUE 的初始 QUIC 包大小
受已知的 detour 容量钳制；仅此证明不了后续包存在硬上限。TUIC 有一条单独的硬上限路径。
物理路径容量未知，不构成证明。
→ `modules/mtu-and-geometry.md`

## 9. 上传调度器需显式开启，未设置时不产生作用（The upload scheduler is opt-in and inert when unset）

`traffic_scheduler` 与 `traffic_class` 是分支独有的键。没有速率时，门禁照样观察每一个
受管理的字节，全部放行，并且**不启动任何队列、任何锁、任何定时器**。但一个没激活的整形
门禁，证明不了配置与另一种实现完全兼容，也证明不了端到端行为一致。配了速率时，整形会
**持续**运行，不只是有争用的时候才跑。
→ `modules/data-path-optimization.md`

## 10. Direct Offload 是语义等价，其数据面效果仅在 Linux 上（Direct Offload is semantic equivalence, and its data-plane effect is Linux-only）

只有用户态路径确实会把某个拨号选项**应用到该流上**，这个选项才让该流不合格。
新增的 `DialerOptions` 字段不能把绕过默认做成 *安全*：没有分类的选项一律拒绝。
而且这个判定**仅**在 Linux `auto_redirect` 中才是真正的绕过 —— 没有任何 TUN 协议栈会遵守它。
→ `modules/data-path-optimization.md`

## 11. 发布溯源是下载并重新校验，且范围有限（Release provenance is download-and-re-verify, and it is scoped）

发布流程会**下载**某个产品工作流为精确提交产出的产物，再重新校验各平台的字节、哈希
与记录的提交 —— 它不重新构建。这一描述只覆盖历史性的发布工作流；每个 release 都要
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
