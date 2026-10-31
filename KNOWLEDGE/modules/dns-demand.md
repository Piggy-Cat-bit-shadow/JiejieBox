# DNS（域名系统）

## 基线之后的地址族探测日志（Post-baseline family-probe logging, testing, 2026-10-28）

**VERIFIED_SOURCE** 在 `011f3fdcd99173acd3e80483b50cb76b07bf6ff4`：`dns/router.go` 使用
`QuietEmptyFamilyResult` 以 debug 级别记录空的地址族探测，而不是将其记为查询错误；
独立的 unquiet 情形仍然保持区别。关于正例与反例，参见 `dns/empty_family_log_test.go`。
这是一种日志层面的区别，并不能证明所有双栈恢复（dual-stack recovery）或设备行为。
请针对确切的目标修订，检查当前实现与测试结果。

### 双栈查询与候选所有权（Dual-stack lookup and candidate ownership）

`dns/router_dual_stack.go` 的 `LookupFamilies` 复用 `Router.Lookup`，并保留路由、传输、
缓存与响应检查。生效的严格 IPv4/IPv6 策略只发出其所选地址族；
其他策略则在每个地址族完成时即发布。`common/dialer` 为每条流捕获一份不可变的
`admissionPolicy`，将配置的策略与本地能力快照及其
所有权 epoch 组合起来。未知能力本身不会拒绝某个地址族。候选调度器
对尝试做错峰安排，在快速失败时推进，并关闭落败的连接，包括迟到的成功
（`dual_stack_scheduler.go`）。套接字创建并不等于完成 QUIC 握手。测试包括
`dns/cross_generation_family_test.go`、`common/dialer/admission_policy_test.go` 和
`common/dialer/dual_stack_scheduler_test.go`。这些是测试目标上的 `VERIFIED_SOURCE` 观察结果；
运行时与特定网络的结果需要各自的证据。

本地地址族门禁（local-family gate）只适用于在此处拨出的套接字，包括代理服务器自身的地址，
而不适用于交给远程代理的目标。过期或切换时点的能力快照不能硬性拒绝某个候选；
缺少能力信息意味着不作判断。配置的严格地址族策略仍然独立，仍可将该候选排除。
观察者优先采用合并的地址族/代次/稳定性快照；回退到各自独立的可选 getter 可能产生
两种不同的读数。批量门禁针对其候选列表只评估一次所有权
（`network_observation.go`、`family_capability.go`）。

`common/netcap` 另外把目的地范围分类为 local、site、global 或 unknown，并评估
观察到的接口与出站证据。不能仅仅因为全局 IPv6 出站不可用就拒绝 site 和 local 地址；
证据未知时按失败即放行（fail-open）处理。IPv4-mapped IPv6 在分类前会被解映射。
这是一个地址/观察模型，而不是实际的连通性探测；其 `global` 类别并不证明
目的地位于公共互联网上。在本次校准中，未加过滤的 `./common/netcap` 单元测试
在 Windows 上以 Go 1.26.10 通过。

调度器按代次维护的地址族健康度比硬性能力拒绝更柔和：一次符合条件的路径失败，或
对面地址族一次足够长的落败尝试，都可能使两个地址族的首个候选在下一次竞速中一起启动。
该条目在 15 秒后过期，或在该地址族成功时清除，且映射表有界。
它不会把该地址族列入黑名单，也不证明 QUIC 握手已经成功。
参见 `dual_stack_scheduler.go` 及其地址族健康度测试。

当盒子已提交仅 IPv4 的入口呈现（entry presentment）时，`dns/entry_ipv6.go` 会在规则、缓存、
FakeIP 或上游处理之前抑制应用侧的 TUN AAAA 问题，并以无数据的 NOERROR 作答。
它不会把该门禁施加到拨号器的 `Lookup`，也不会施加到由远程端点转发的 DNS：
即使本地入口没有 IPv6，远程代理仍可能有 IPv6。
这与每次尝试的地址族准入是分开的。参见 `dns/entry_ipv6_test.go`
以及 `libbox-platform-contract.md` 中的 TUN 呈现契约。

IPv4-mapped IPv6 目的地必须按 IPv4 分类：TUN 会在 DNS、FakeIP 与 route-set 比较之前
将其规范化（`protocol/tun/inbound.go`），而准入检查的是解映射后的地址族（`common/dialer/admission.go`）。
参见 `protocol/tun/mapped_address_test.go` 与 `common/dialer/dual_stack_mapped_asis_test.go`；
本地 TUN 与拨号器测试套件在本次刷新中通过。

### 由端点拥有的私有解析器（Private resolvers owned by endpoints）

Tailscale、OpenVPN 与 OpenConnect 的 DNS 传输可以拥有解析器，而这些解析器本身并不是
全局 DNS 管理器中的条目。它们的 `CloseIdleConnections` 方法在读锁下对这些解析器引用做快照，
释放锁并转发空闲释放动作；`Reset` 则单独转发重置。
参见三个 `protocol/*/dns_transport.go` 实现。要求更宽泛的 `IdleConnectionKeeper` 接口，
或只遍历全局传输，可能漏掉这些私有池。
这是 `VERIFIED_SOURCE`，位于 `011f3fdcd99`；它既不是设备证据，也不是外部网络证据。

对 Tailscale 而言，`tailnetDomains`（位于 `protocol/tailscale/dns_transport.go`）会在同一份快照中
规范化主机、搜索域与路由后缀。匹配的、无解析器的本地后缀优先于转发后缀；
否则转发后缀按最长优先检查。单标签搜索展开是有条件的，且必须找到一个已知主机
或转发路由，而不仅仅是任一本地后缀。端点与 DNS 传输在各自的优选域决策中使用该模型。
当使用平台接口时，`protocol/tailscale/system_binding.go` 从网络管理器提供 Tailscale 的
默认路由钩子，并注册对共享接口 getter 的清理。
带标签的域测试与 getter 清理测试在本地通过；
它们并不能确立真实的 tailnet DNS 或设备交接。

**范围（Scope）：** `dns/router.go`、`dns/client.go`、`dns/transport_manager.go`、`dns/transport/*`（包括
`fakeip` 与 fork 创建的 `group`）、面向 DNS 的路由动作，以及
`adapter/fakeip_issuance.go`。

**来源（Provenance）：** 有 94 个 fork 提交触及 `dns/`。fork 创建的那个包是 **`dns/transport/group`**
（stable / fastest / parallel 传输组，`5b0a33c`）。**不要把上游 DNS 行为归因于 fork**：
上游会变基 `testing`，该范围内 343 个 `世界` 署名的提交中有 328 个是重写前的孤儿提交，
其内容已存在于基线中（见 `BASELINE.md` §provenance）。
判定某次具体改动的是 **blob 比较**（`e4b7e692` 与 `a267e99`），
而不是作者名。

**在改动以下内容时加载本模块（Load this module when changing）：** DNS 路由、缓存或代次、FakeIP、
`domain_resolver`、解析器所有权、DNS 泄漏，或跨网络切换的 DNS 行为。

---

## 1. 路由器（The router）

`dns.Router`（`dns/router.go`，在 `0067b03` 时为 2,753 行）。`Start(stage, scope)` 位于 `:252`。

**规则求值是带挂起的显式遍历（walk with suspension）**，而不是递归匹配：

| 元素 | 锚点 |
|---|---|
| 遍历（the walk） | `walkDNSRules` `:709` |
| 带规则交换（exchange with rules） | `exchangeWithRules` `:923` |
| 挂起后恢复（resume after suspension） | `resumeExchangeWithRules` `:933` |
| 清扫已武装规则（sweep armed rules） | `sweepArmedDNSRules` `:965` |
| 完成待处理交换（finish a pending exchange） | `finishPendingExchange` `:1063` |
| 异步 `evaluate` 动作 | `launchDNSEvaluate` `:637`、`dnsEvaluatedFuture` `:571` |

**为什么是遍历而不是匹配：** `evaluate` 动作可能需要先完成自己的交换，外层查询才能被解析。
挂起正是为了让外层交换可以被*恢复*（resumed），而无需重新运行已经触发的规则。
**把 evaluate 动作改成同步，要么会死锁，要么会重入规则集**，
这正是 `Future` 机制存在的原因。

**传统/响应匹配模式（Legacy / response-matching mode）** 是独立的，并在启动时前置校验：
`resolveLegacyDNSMode` `:2412`、`dnsRuleModeRequirements` `:2426`、
`validateLegacyDNSModeDisabledRules` `:2507`、`validateEvaluateFakeIPRules` `:2570`。
这些只在启动时运行。

---

## 2. 代次与缓存提交不变量（Generations and the cache-commit invariant）

### 一句话说明该不变量（The invariant, in one sentence）

> **交付与状态变更是分开的。** 在已稳定网络上开始的交换，可能在切换之后才返回
> 其在切换前时点取得的答案（dated answer），但它不得在新代次或已变更的策略下提交该答案。
> 在切换待定期间开始的交换，不能作为稳定结果交付。
> 参见 `responseDeliverable` 与 `stateMutationAllowed`（位于 `dns/client.go`）。

### 机制（The machinery）

| 关注点 | 锚点 |
|---|---|
| DNS 环境代次（the DNS environment generation） | `dns/router.go` — `dnsGeneration` `:1397`、`dnsGenerationLocked` `:1408`、`observeDNSEnvironment` `:1418`、`observeDNSEnvironmentLocked` `:1449` |
| 环境的身份（the environment's identity） | `dns/router.go` — `environmentHashEntry` `:1547`、`dnsEnvironmentFingerprintOnly` `:1585`、`dnsEnvironmentDescriptionFor` `:1627`、`dnsEnvironmentFingerprintNow` `:1665` |
| 反向映射（FakeIP→domain） | `dns/router.go` — `reverseMappingAnswersFrom` `:1685`、`recordReverseMappingFrom` `:1741`、`commitReverseMappingAnswers` `:1780` —— **提交会做代次检查**；`LookupReverseMapping` `:2288` 在读取前重新观察环境。旧的 `reverseMappingGenerationCurrent` 谓词已不存在；不要引用它 |
| 策略纪元（the policy epoch） | `dns/router.go` — `policyStoreGuard` `:2231`、`policyEpoch` `:2259` |
| 缓存键（cache keys） | `dns/client.go` — `dnsCacheKey` `:230`、`dnsExchangeKey` `:237`、`rdrcNamespace(transportTag, environment)` `:260`、`newCacheKey` `:280` |
| 检查（the checks） | `dns/client.go` — `captureGeneration` `:305`、`policyStillCurrent` `:327`、`generationStillCurrent` `:338`、`responseDeliverable` `:381`、`stateMutationAllowed` `:403` |
| 切换状态（the transition state） | `dns/client.go` — `errNetworkTransitioning` `:425`、`networkTransitionStable` `:454` |
| 提交（the commit） | `dns/client.go` — `finishCacheKey` `:485`、`transportEnvironment` `:524`、`commitCacheState` `:1009` |
| 重置（reset） | `dns/router.go` — `ClearCache` `:2186`、`ResetNetwork` `:2309`；客户端级清除是 `dns/client.go:1287` |

**如果变更守卫被破坏，失败模式是：** 一个旧的 Wi-Fi 答案可能被存储并提供，
就好像它属于新的蜂窝网络环境。故意允许交付一个在已稳定网络上已经开始的查询；
这与把该答案记录为当前答案是不同的。

### 哪些子系统使用协调器纪元，哪些不使用（Which subsystems use the coordinator epoch, and which do not）

这一点很重要，因为早先的总结过度声称存在一个覆盖所有状态的单一全局“网络纪元”（network epoch）。

| 子系统 | 是否与纪元耦合？ | 证据 |
|---|---|---|
| DNS 缓存与反向映射 | **是** —— 除了协调器的代次外，还有**自己的**代次（`dnsGeneration`） | `dns/router.go:1397-1449` |
| 仅 DNS 的环境变化 | **是，而且即使没有网络接口变化，它也会获得自己的代次** | `ffec0df1` |
| 策略存储 | **是** —— 独立的 `policyEpoch` | `dns/router.go:2259` |
| `runtimecoord.Coordinator.Epoch` | 拨号门禁与退役遍历读取的共享计数器 | `common/runtimecoord/coordinator.go` |
| `TrimMemory` | **不推进代次** | `1331e8567` |
| `ReleaseMemory` | **会推进** | 同上 |

`common/runtimecoord/coordinator.go:72-79` 指出两个计数器描述的是同一次切换 ——
它们是两个视图，而不是两个彼此独立的真相。**不要把纪元描述为“所有网络状态的唯一事实来源”。**
它是那些选择加入的子系统所共享的身份。

---

## 覆盖整个 QNAME 的 NXDOMAIN 负缓存（The QNAME-wide NXDOMAIN negative cache, `dns/client_negative.go`）

RFC 2308 指出 `NXDOMAIN` 是关于**名字**的陈述，而精确缓存以完整问题（包括 Qtype）为键。
没有这个缓存，一个不存在的名字每记录类型（A、AAAA、HTTPS、SVCB）就要花费一次上游查询；
若前面还有过滤解析器，则每种类型还要各做一次规则求值。
`nxdomainCache` 按名字记录一次判定，并在本地回答后续类型。
它与精确的 `dnsCacheKey` 缓存是分开的，也**不是** `adblock` 动作的存储
（`dns-adblock-action.md` §7）。

### 什么情况可以扩大为名字级判定（What may widen to the name）

`storeNXDomain`（`dns/client_negative.go:139`）在把类型特定答案扩大为名字级判定的
那一点上重新检查每个条件；`commitCacheState`（`dns/client.go:1009`）只对 `RcodeNameError`
调用它：

* **仅限 `NXDOMAIN`。** NODATA（空回答的 `NOERROR`）、`SERVFAIL`、`REFUSED`、`FORMERR`、
  超时与传输错误永不扩大；
  上游的瞬时故障不得变成所有记录类型的持续中断。
* **仅限单个问题。** 多问题报文没有可记录的单一名字。
* **仅限空回答区段。** 终止性的 `NXDOMAIN` 若经由 `CNAME` 得到，描述的是别名目标，而不是 QNAME；
  扩大它会把针对已存在别名的 CNAME 查询回答成 `NXDOMAIN`。
* **可用的 SOA。** `min(SOA TTL, SOA MINIMUM)` 必须非零（`extractNegativeTTL`
  `dns/client.go:677`）。守卫在存储时会重新读取 SOA，但该读取发生在 `applyResponseOptions`
  规范化报文**之后**（`dns/client.go:1582-1589`）。其意图是重写不能把权威的零值提升为可用值；
  **对于“头部 TTL 为零 / MINIMUM 为正”这一情形，该意图尚未得到证明** ——
  见下文后续说明。

### 键、命中与过期语义（Key, hit and expiry semantics）

* 键会丢弃 Qtype，但保留精确缓存的命名空间：规范小写 FQDN、QCLASS、传输标签、
  客户端子网与 DNS 环境（`nxdomainCacheKey` `dns/client_negative.go:63`、`nxdomainKeyFrom` `:124`）。
  两个传输、两个 ECS 子网或两个网络代次永不共享同一判定。
* 命中是**针对当前问题重建的**：ID、名字、QTYPE 与 QCLASS 都来自请求；
  只复用 `NXDOMAIN` 判定及其 SOA（`loadNXDomain` `:205`、`nxdomainResponseFor` `:254`）。
  所存储的报文会被复制并重新设置问题，
  而不是重放。
* 报告的 TTL 是 SOA 负 TTL 的**剩余值**，而不是它入库时的值（`:240-245`）。
  已失效的条目在读取时被移除，查询转而上游。
  对负判定没有乐观服务（optimistic serving）。
* 查询顺序是精确缓存优先，然后是名字缓存，最后是上游（`dns/client.go:834`、`:862`）：
  有效的正向精确答案总是胜过名字级判定。

### 边界与配置（Bounds and configuration）

* 容量是 `min(dns.cache_capacity, 2048)`（`nxdomainCacheCapacity` `dns/client_negative.go:89`、
  `nxdomainCacheForCapacity` `:94`）：名字缓存保持在配置的 DNS 缓存预算之内，
  而不是在预算之外额外增加。
* `disable_cache: true` 会同时禁用两个缓存（`initializeNXDomainCache` `:103`）。
* `disable_expire: true` 会**不带生存期**地存储判定，与精确缓存完全一样，
  命中时报告 TTL 0，而不是凭空编造一个值（`:187-189`、`:216-224`）。
* `Router.ClearCache` 会清除它（`dns/router.go:2186`；客户端清理见 `dns/client.go:1287`），
  而 `TrimExpiredCaches` 会移除已失效的判定（`dns/client.go:1268`）。
  键中的环境字段与受守卫的提交，共同防止判定跨代次混用。
* 这些判定**不会被持久化**，即使精确缓存使用磁盘后端也是如此
  （`TestNXDomain_WorksWithPersistentBackend`）。

### 与 adblock 的关系（Relationship to `adblock`）

`adblock` DNS 动作返回本地的 `NXDOMAIN` + SOA，并有意**不**写入 `nxdomainCache`：
运维者的规则并不能证明上游服务器说过该名字不存在。二者共享的是响应形状，而不是状态。
参见 `dns-adblock-action.md` §7。

### 证据（Evidence）

为 `VERIFIED_SOURCE`，位于 `0067b03`。`dns/client_negative_test.go` 包含 23 个 `TestNXDomain_*` 用例，
覆盖按名字复用、按类型回答、大小写折叠、NODATA/错误/CNAME/零 SOA 的拒绝、剩余 TTL、
传输/ECS/环境隔离、`disable_cache`/`disable_expire`、清除、持久化后端，以及
“正向精确答案获胜”。复检命令：`go test -count=1 -run TestNXDomain ./dns/`。
**在 2026-10-31 的评审中未执行**（评审主机上没有 Go 工具链）：
因此这里的状态是 `BLOCKED_BY_ENVIRONMENT`，而不是通过。
由该契约引出的三条告诫是 §8 的第 13–15 项。

**来源层面的矛盾 —— `rewrite_ttl` 与零 SOA 头部 TTL。** 守卫的意图是：重写不能把权威的零负 TTL
变成名字级判定。源码顺序表明，对某一种形状该检查可能并未执行：`applyResponseOptions` 会先
重写报文的 TTL（`normalizeTTL`），**之后** `commitCacheState` 才调用 `storeNXDomain`，
因此 `extractNegativeTTL` 看到的是 `min(rewritten header TTL, SOA MINIMUM)`。
当头部 TTL 为 `0` 而 MINIMUM `> 0` 时，原始的 `min` 为 `0`，但正的 `rewrite_ttl`
会使重新读取的值 `> 0`。现有的 `TestNXDomain_ZeroSOATTLIsNotPromotedByNameWide` 使用 `min(0,0)`，
且未设置 `RewriteTTL`，因此没有覆盖这种形状。
**这是 `UNVERIFIED`，不是已证明的保证。**
一个针对 `adapter.DNSQueryOptions{RewriteTTL: ...}` 的测试，在 `header=0, MINIMUM>0` 夹具上，可以定论；
在那之前，不要依赖“重写提升”这一断言，
也不要把运行时改动作为知识刷新的一部分。

---

## 3. FakeIP 虚拟地址（FakeIP）

| 元素 | 锚点 |
|---|---|
| 存储（the store） | `dns/transport/fakeip/store.go` — `Store` `:28`、`Start` `:98`、`recordRetired` `:170`、`Create` `:293`、`Lookup` `:358`、`Reset` `:362` |
| 发放台账（fork 新增，the issuance ledger） | `adapter/fakeip_issuance.go` — `FakeIPIssuanceLedger` `:111`、`Advance` `:158`、`RecordSeed` `:179`、`RecordIssued` `:193`、`Issued` `:212`、`IssuedInterval` `:219`、`Clear` `:240`；在 `route/route.go:1180` 消费（`issuedFakeIPAddress`，定义在 `:1236`），对 nil 台账返回 `(false, zero)` |
| NAT 重写（the NAT rewrite） | `route/fakeip_conn.go:15` `fakeIPNATPacketConn`、`:32` `rewriteReadDestination` |
| 传输管理器（the transport manager） | `dns/transport_manager.go` — `TransportManager` `:18`、`Start` `:43`、`startTransports` `:102`、`Default` `:169`、`FakeIP` `:175`、`Create` `:181`、`isDefaultCandidate` `:269` |

**规则：** FakeIP 服务器**不得回答目的地查询**，而 `domain_resolver` 可能指向这样的服务器。
已由 `8fad3e9b` 修复。映射后的 FakeIP 地址是一个合成占位符，路由器会恢复其对应的域名。
TUN 的 route-set 守卫对**当前存储范围**会直接放行（falls through）；其范围比路由器的
发放检查与退役范围检查更窄（`data-path-optimization.md` §A.5）。

当前的 `route/route.go` 会拒绝位于已配置的当前 FakeIP 范围内、但其存储中没有映射的地址。
在该范围之外，它会拒绝被本进程的发放台账覆盖的地址，或被退役范围覆盖的地址 ——
后者以安装级的发放历史为支撑；退役范围是更粗粒度的跨进程记录，
并不能证明每个单独地址都曾被发放。除此之外，地址不会仅仅因为
形似占位符就被拒绝。关于确切的所有权与持久化边界，参见
`adapter/fakeip_issuance.go` 与 `route/route.go`。

`experimental/cachefile/fakeip.go` 会返回元数据刷写错误；`dns/transport/fakeip/store.go`
在该错误之后不会推进其预留。Round 2 oracle 中旧的“无条件成功”结论属于历史；
`TestFakeIPSaveMetadataReportsAFailedFlush` 覆盖了失败路径。
这一元数据保证不应被推广到每个缓存写入 API。
`experimental/cachefile/pending.go` 内部的 `flush()` 会在写入前替换待处理批次；
失败时它返回错误，但不会重新排队该批次。公开的 `Flush()` 丢弃错误，属于尽力而为。
FakeIP 元数据则专门传播该错误，以便其存储可以避免推进预留；
这并不意味着每个待处理的缓存写入都是持久化的或被重试的。

---

## 4. 消费者需求（Consumer demand）

`dns/transport/requirement.go` 为每种 DNS 类型给出其传输需求（transport requirement），
形状与 HTTP 客户端的相同（`common/httpclient/transport_requirement.go`）：

```text
udp  ⇒  AllOf{udp}  +  Conditional{tcp}
```

**为什么 TCP 这条腿是 `Conditional` 而不是 `AllOf`：** TCP 重试只在响应被**截断**时发生。
要求两者都为必需，会拒绝一个本来完全可用的配置。

通过 `dns/transport/capability.go` 报告；按类型的断言位于
`dns/transport/{tcp,tls,udp,https}.go` 与 `dns/transport/quic/{quic,http3}.go`。这些断言在启动时检查接口，
**不拨出任何连接**。在当前的启动校验中，已存在但缺少所需能力的 detour 只是警告；
缺失的 detour 仍然是致命的。受影响的运行时流程仍可能失败。
分类说明参见 `modules/path-selection-validation.md`。

可证明需求（provable-demand）模型本身是 `common/routeflow/reach.go` ——
参见 `path-selection-validation.md` §3，了解 UNKNOWN 透明策略、`route.final` 残余量，
以及为什么有意排除 ICMP。

**`dns/transport/group`** 由 fork 创建：`stable`、`fastest` 与 `parallel` 传输组（`5b0a33c`）。
它在已配置的传输**之间**做选择；它不改变传输本身是什么。

在所考察的目标上，stable 选择会坚持使用一个干净成员；parallel 会向各干净成员扇出；
fastest 会保留一个存活赢家，或在没有存活赢家时按代次发起一次选举。
当没有任何成员干净时，每种模式都会做一次“最不脏”的尝试，而不是扇出。
失败记录与胜利会惰性过期，并按成员设上限；传输失败或 SERVFAIL 算失败，
而 NXDOMAIN 与空 NOERROR 是有效答案。调用方取消或组关闭不得记为成员失败。
缓存只看到组返回的那个答案。参见 `group.go`、`fan.go` 以及所有权/选举测试；
本地 `./dns/transport/...` 测试在本次刷新中通过，
但并未声称具备外部 DNS 互操作性。

---

## 5. DNS 泄漏以及 fork 中 DNS 的触达范围（DNS leaks and the reach of DNS in the fork）

每一个防止 DNS 逸出预期路径的机制，以及各自的作用：

| 机制 | 它防止什么 | 锚点 |
|---|---|---|
| 按传输的 **detour** 能力检查 | 当已存在的 detour 无法承载查询时在启动阶段发出警告；但不会让该运行时路径变得可用 | `dns/transport/capability.go` |
| **bootstrap 与隧道内**解析器分离（MASQUE） | 通过一条尚不存在的隧道解析服务器自己的主机名，或把隧道内名字泄漏到外部 | `masque-connectip.md` §3 |
| `action_resolve` 写入 `metadata.DestinationAddresses` | 域名目标绕过基于 IP 的拒绝规则 —— **这就是为什么 `resolve` 是必需而非可选的** | `naive-http-transport.md` §3 |
| **数据包目的地守卫**（packet destination guard） | UoT 的非 Connect 会话把后续数据报发到会话级检查从未见过的地方 | `route/packet_destination_guard.go`；`naive-http-transport.md` §3 |
| TUN DNS 劫持的次序 | 已配置的 DNS 查询在 DNS 路径能够处理它之前就被绕过 | `protocol/tun/inbound.go`（`dnsHijackByPort`、`judgeFlow`）；参见 `libbox-platform-contract.md` |
| FakeIP 不能作为目的地解析器 | 占位地址被当作真实答案 | 上文 §3 |
| 缓存提交上的环境代次 | 网络变化后陈旧答案被记录为当前答案 | 上文 §2 |

**`route/resolve_uot_sentinel_test.go`** 固定了 UoT 哨兵契约。注意路径：
该文件位于 `route/`，而不是仓库根目录。

`dns_server_address` 与 `dns_search_domain` 规则项是从上游吸收的（`a2df2cdc1` 家族）——
它们不是 fork 的工作。

独立的 `service/resolved` 传输不再通过其原先的 `TransportLink.nameList`/`ndots` 路径展开查询名。
在 `cb71cf80d72` 上，它会选择一个链路、校验原始问题名，并用该报文做顺序尝试。
不要把这一移除推广到上文所述 Tailscale 那种不同的短名/搜索域匹配；
两者是不同的路径。
这是源码层面的发现，不是对运行中 systemd resolver 的测试。

DHCP DNS 是另一条路径：`dns/transport/dhcp/dhcp_shared.go` 会校验问题，用其提供的搜索后缀
展开单标签名字，然后尝试原名；多标签名字则按原样保留。
`dns/transport/local/systemconfig` 移除了它此前通用的 `NameList` / `ndots` 展开。
不要把 resolved 服务的行为照搬到 DHCP，也不要假设每个短名解析器都使用同一策略。
现有 DNS 传输测试在本地通过，但并非运行中的 DHCP 网络。

---

## 6. systemd-resolved goroutine 泄漏 —— 已修复；声称未修复的文档已过期（The `systemd-resolved` goroutine leak — fixed, and the document that says otherwise is stale）

**`docs/RELEASE-CERTIFICATE.md:70-85` 与 `verify.yml:403-418` 都把该泄漏记为未修复。
这些记录已经过期。** `git log -S'signalLoopStop' e4b7e692..a267e99` 恰好返回一个提交 ——
**`6b495134c`**，"fix(protocol,dns): early-data ownership, SOCKS identity, and network-scoped DNS
cache"（2026-10-09），属于 fork 侧，且是发布基线的祖先。

该修复做了什么，可在 `a267e99` 上验证：

| 元素 | 锚点 |
|---|---|
| `signalLoopStop chan struct{}` / `signalLoopDone chan struct{}` / `signalLoopOnce sync.Once` | `dns/transport/local/local_resolved_linux.go:60-62` |
| 在 `Start` 中分配 | `:188-189` |
| `Close` 关闭停止通道一次，并**等待**该循环结束 | `:221-223`（`close`，然后 `<-t.signalLoopDone`） |
| 循环自行发出退出信号 | `:356`（`defer close(t.signalLoopDone)`） |
| 循环对停止通道做 `select`，并用 `RemoveSignal` 注销 | `:367`、`:362` |

在更晚的测试目标上，`Close` 仍会等待信号循环结束，但 `postUpdateStatus` 还会启动异步更新工作。
仅等待该循环结束，并不能证明每个异步更新都已完成；针对具体的生命周期断言，
应检查更新锁、取消与已关闭检查。

**影响范围，仅作记录：** 只在 **存在 `systemd-resolved` 的** Linux 上 —— 该文件是 `*_linux.go`，
而 `local_resolved_stub.go` 带有 `//go:build !linux`。
在该路径上它是无条件的，这正是首次在 Linux runner 上运行 `test/` 模块时暴露出该问题、
以及该模块的 `TestMain` 使运行失败的原因。

> **对未来的 AI 有两点后果：**
>
> 1. **不要把历史上的信号循环泄漏重新报告为未修复。** 早先的 CI 排除是一次性决定；
>    当前的 Verify 会为某个选定测试进入独立的 `test/` 模块，但这并不意味着会运行其完整的
>    盒子级测试套件。请检查当前工作流以确认确切范围。
> 2. **不要采用该文档提出的修复方案。** 关闭 dbus *signal* 通道在这里会**panic**，
>    因为 dbus 是发送方。已交付的修复是单独一个停止通道加一次等待（join）。
>    这才是应当保留的形状。

**证据级别：** `VERIFIED_SOURCE`（这些符号在发布基线上存在，且引入该修复的提交已确认）。
**执行层面为 `BLOCKED_BY_ENVIRONMENT`** ——
该路径需要带 `systemd-resolved` 的 Linux。

---

## 7. 证据（Evidence）

| 保证 | 级别 | 复检 |
|---|---|---|
| DNS 规则遍历、缓存、反向映射与 FakeIP 代次 | `VERIFIED_UNIT` | `go test -count=1 ./dns/...` |
| DNS 传输的需求模型 | `VERIFIED_UNIT` | `go test -count=1 ./dns/transport/...` |
| FakeIP 不能回答目的地查询 | `VERIFIED_UNIT` | `go test -count=1 -run FakeIP ./dns/...` |
| 覆盖整个 QNAME 的 NXDOMAIN 负缓存 | `VERIFIED_SOURCE`；23 个 `TestNXDomain_*` 测试存在，但在 2026-10-31 的评审中**未重新执行** | `go test -count=1 -run TestNXDomain ./dns/` |
| 启动时由路由证明的 DNS 需求 | `VERIFIED_UNIT`（需要 tags） | `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -v -run 'TestADNSTransportDetourMustCarryTheQuery\|TestTheDefaultDomainResolverIsAGraphEdge' .` |
| 解析器权威一致性检查 | `VERIFIED_UNIT`（需要 tags） | `-run TestResolverAuthorityParity .` |
| `systemd-resolved` goroutine 泄漏 | **它确实已修复**，由 `6b495134c` 修复 | `git log -S'signalLoopStop' e4b7e692..a267e99`；符号见 `local_resolved_linux.go:60-62`、`:221-223`、`:356`、`:367` |
| 多平台 DNS 环境矩阵 | 本次校准未运行 | **此处为 `UNVERIFIED`** |
| 真实设备上的 DNS 与 FakeIP | 本次 Windows 刷新未实际使用真实 TUN | **此处为 `UNVERIFIED`**；请查证具体构建的设备证据 |
| 无竞态 | `-race` 需要 cgo | **`BLOCKED_BY_ENVIRONMENT`** |

在 2026-10-28 校准中测得：`go test -count=1 ./dns/...` → **all ok**（`dns` 9.5 s、`dns/transport` 6.4 s、
`dns/transport/fakeip` 8.7 s、`dns/transport/group` 0.7 s、`dns/transport/hosts` 0.4 s、
`dns/transport/quic` 0.4 s）。2026-10-31 的评审没有重新运行它们（没有 Go 工具链）。

---

## 8. AI 注意事项（AI cautions）

1. **不要使 DNS 规则遍历变成同步。** 挂起的存在，是为了让 `evaluate` 动作可以运行自己的交换，
然后恢复外层交换。
2. **不要在代次或策略已变更的情况下提交响应。**
保留“在已稳定网络上开始的查询”这一独立的交付契约；
在切换待定期间开始的查询，不能作为稳定结果交付。
3. **不要移除反向映射的代次检查。** 在旧代次下提交的 FakeIP→domain 映射，
对已被重新分配的地址来说是错误的域名。
4. **不要把纪元描述为全局的。** DNS 在协调器代次之外有自己的代次，
策略存储也有自己的纪元。
5. **不要让 `TrimMemory` 推进代次**，也不要让 `ReleaseMemory` 停止推进代次。
6. **不要让 FakeIP 服务器回答目的地查询**，
也不要让 `domain_resolver` 指向这样的服务器。
7. **不要要求 DNS 传输同时具备 UDP 与 TCP。** TCP 这条腿是 `Conditional`，
因为重试只在截断时触发。
8. **不要把 `systemd-resolved` 泄漏重新报告为未修复**，
也**不要采用其文档提出的修复方案** —— 关闭 dbus *signal* 通道在这里会 panic，因为 dbus 是发送方。
已交付的修复是单独一个停止通道加一次等待（join）。
9. **不要把上游 DNS 行为归因于 fork。** 比较 `e4b7e692` 与 `a267e99` 处的 blob。
10. **不要为 MASQUE 相关的 DNS 模型引用 `assigned_dns_*` 测试文件** ——
`e93e297aa` 用不可变快照替换了该模型，并删除了这些文件。
11. **不要让 DNS 查询绕过 DNS 劫持**以触发 Direct Offload 快速路径；
劫持的次序正是 DNS 过滤得以继续工作的原因。
12. **不要把连接上“设置了 Domain”当作“是嗅探器设置的”** ——
FakeIP 反向映射也会设置它。
13. **不要把 NODATA、错误 rcode、超时或 CNAME 终止的答案扩大到 QNAME。**
`storeNXDomain` 中的资格守卫是名字级缓存的正确性边界，
而不是优化细节。
14. **不要重放已存储的 NXDOMAIN 报文或其原始 TTL。** 针对当前请求重建 ID/问题
并报告剩余生存期；重放会回答成不同的 QTYPE。
15. **在“头部为零 / MINIMUM 为正”的情形得到测试之前，不要声称 `rewrite_ttl` 不能提升零负 TTL。**
源码顺序（`normalizeTTL` 在存储时的 `extractNegativeTTL` 之前）使该情形仍然悬而未决；
参见 NXDOMAIN 一节中的后续说明。
也不要把传输/ECS/环境从名字缓存键中去掉：
那会把一个限定范围内的判定变成跨命名空间的判定。
