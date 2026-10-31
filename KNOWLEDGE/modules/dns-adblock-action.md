# DNS Adblock 动作（DNS Adblock Action）

## 原始评审之后的本地判定与多地址族错误（Local verdicts and multi-family errors after the original review）

`dns/verdict_marker.go` 标记本地规则的 rcode，而不改变其 rcode 匹配契约。
`IsLocalVerdict` 检查聚合错误的**每一个叶子**都是本地判定：
被拦截的地址族与真实失败混合时，不得仅凭匹配其中一个分支就掩盖该失败。
上游 NXDOMAIN 缓存是另一回事：`dns/client_negative.go` 存储带可用 SOA 的合格 NXDOMAIN，
但不得把 NODATA 或瞬时错误扩大到整个名字。这些是 `VERIFIED_SOURCE`，位于 `011f3fdcd99`；
本次刷新中本地 DNS 包测试通过，但并非真实设备验证。

> 状态：`VERIFIED_SOURCE`（实现、测试与 schema 已在本工作树中、于引入该特性的提交上核实；
> 见下方 `Provenance`）
>
> 原始的特性评审使用分支 `feat/dns-adblock-action`，基线为 `c32c210b0db`，
> 版本 0.1.10。当前的测试目标 `011f3fdcd99` 已包含该动作；后续改动及其更窄的证据
> 已在上文注明。历史特性分支并不等于当前发布。
>
> 本模块记录 fork 的 **`adblock` DNS 规则动作**：一个仅用于 DNS 的终止型动作，
> 用本地生成的、可负缓存的 NXDOMAIN 来回答规则集已归类为广告的名字，
> 且不发起任何上游查询。其目的是在无需源码审计的情况下回答
> “如何用本 fork 在 DNS 层拦截广告域名？”。如果本文档与当前源码或测试冲突，
> **以源码和测试为准**。

---

## 1. 目的（Purpose）

```text
adblock 是本 fork 新增的 DNS rule action。
命中广告域名后直接在本地返回可 negative-cache 的 NXDOMAIN，
不访问 upstream DNS。
```

`adblock` 是一个**零配置、仅用于 DNS、终止型**的动作：

- 携带该动作的规则已经判定该名字是广告（`domain`、`domain_suffix`、
  `domain_keyword`、`domain_regex`、`rule_set`、…）；
- 该动作不会请求上游服务器确认这一判定；
- 它以 `NXDOMAIN` 加一个携带负 TTL 的 SOA 作答，因此客户端可以自行缓存
  该判定；
- 它从不静默丢弃查询，也从不返回 `REFUSED`、`SERVFAIL` 或沉洞地址（sinkhole address）。

主要源码锚点：

```text
constant/rule.go                     RuleActionTypeAdblock
constant/dns.go                      DefaultDNSNegativeTTL
option/rule_action.go                _DNSRuleAction.Action enum, MarshalJSON,
                                     UnmarshalJSONContext, dnsActionUnion
route/rule/rule_action.go            NewDNSRuleAction, RuleActionAdblock, RuleActionAdblock.Response
route/rule/rule_dns.go               validateDNSRuleAction
route/reference_rule.go              collectDNSRuleReferences (terminal action)
dns/router.go                        matchDNS, walkDNSRules, sweepArmedDNSRules, exchangeLegacy,
                                     legacy Lookup
dns/router_adblock_test.go           end-to-end behaviour and upstream-exchange assertions
route/rule/rule_dns_adblock_test.go  action shape, DNS-only boundary, `race` refusal
option/rule_action_test.go           decode / round-trip / schema shape
```

---

## 2. 配置语法（Configuration syntax）

文档所述的形式是一条 DNS 规则：其条件选择广告名字，其动作是 `adblock`。
此外不需要任何其他内容。

### 2.1 单个规则集（One rule-set）

```json
{
  "dns": {
    "rules": [
      {
        "rule_set": "anti-ad",
        "action": "adblock"
      }
    ]
  }
}
```

### 2.2 一条规则上的多个规则集（Several rule-sets on one rule）

```json
{
  "dns": {
    "rules": [
      {
        "rule_set": [
          "anti-ad",
          "AWAvenue-Ads-Rule"
        ],
        "action": "adblock"
      }
    ]
  }
}
```

### 2.3 直接使用域名条件（Domain conditions directly）

```json
{
  "dns": {
    "rules": [
      {
        "domain_suffix": [
          "doubleclick.net",
          "ads.example"
        ],
        "action": "adblock"
      }
    ]
  }
}
```

`domain`、`domain_suffix`、`domain_keyword`、`domain_regex` 与 `rule_set` 都是普通的 DNS 规则条件，
都与 `adblock` 配合工作；该动作不限制是哪个条件选中了它。

### 2.4 完整的最小配置（Complete minimal configuration）

```json
{
  "dns": {
    "servers": [
      {
        "type": "udp",
        "tag": "remote",
        "server": "8.8.8.8"
      }
    ],
    "rules": [
      {
        "rule_set": "anti-ad",
        "action": "adblock"
      }
    ]
  },
  "route": {
    "rule_set": [
      {
        "type": "remote",
        "tag": "anti-ad",
        "format": "binary",
        "url": "https://example.com/anti-ad.srs",
        "update_interval": "1d"
      }
    ]
  }
}
```

`action` 字段是 `adblock` 唯一接受的字段。
`ttl`、`method`、`drop`、`reply`、`ip_mode`、`cache_size`、`fake_soa` 与 `strategy` 对本动作**不存在**，
解析时会被当作未知字段拒绝，而不是静默忽略。

`race` 不同，而且在阅读配置错误时值得了解这一差异：
它是动作*信封*（envelope）的字段（`_DNSRuleAction.Race`），而不是某个变体的字段，
因此 JSON 解码器确实能看到并接受它。它是在上一层被拒绝的：
构建 DNS 规则时由 `validateDNSRuleAction` 拒绝，给出的信息是：

```text
`race` is not supported by the adblock action: it never consults an upstream lookup
```

无论哪种方式，配置都无法加载；此外已发布的 schema 中 `adblock` 变体只声明了 `action`，
因此按它做校验的编辑器会在运行时看到 `race` 之前就将其标出。

---

## 3. 行为（Behaviour）

```text
domain / domain_suffix / rule_set
        ↓
      adblock
        ↓
local NXDOMAIN + SOA negative TTL
        ↓
   upstream = 0
```

响应是针对正在被回答的请求构建的：

| 字段 | 值 |
|---|---|
| `ID` | 当前请求的 ID |
| `Question` | 当前请求的单个问题，逐字保留 |
| `Rcode` | `NXDOMAIN` |
| `Answer` | 空 |
| `Ns` | 一个由被查询名字拥有的 SOA，类别取自问题 |
| SOA `TTL` / `MINIMUM` | `constant.DefaultDNSNegativeTTL` = `600` |
| 标志位 | 置 `QR`、`AA`、`RD`、`RA`；清 `TC`、`CD`、`AD` |

`AA` 与这棵树里其他本地作答响应的做法一致
（`RuleActionPredefined.Response`、`dns.FixedResponse*`）：解析器依据自己的知识作答，
而不是依据上游服务器。

SOA 自身的 TTL 与其 `MINIMUM` 取相同值，因为 RFC 2308 §3 把负缓存生存期定义为
`min(SOA TTL, SOA MINIMUM)`，而不同客户端遵循的字段不同。

---

## 4. 设计目标（Design goals）

1. 广告名字不得产生**重复的**上游 DNS 请求 —— 不是每种记录类型一次，不是每个调用方一次，
也不是冷启动时一次。
2. 一个广告名字造成的网络、CPU 与唤醒开销，
必须在 DNS 层能够做到的范围内尽可能接近零。
3. 不静默丢弃：被丢弃的查询会让客户端重试或回退，
其开销比节省下来的那次上游查询更大。
4. 不通过 `reject` 实现：拒绝是一种客户端有权重试的失败。
5. 鼓励而非假定客户端侧负缓存：SOA 的存在是为了让符合规范的解析器停止询问，
但本 fork 所作的保证针对的是它自己的上游流量。

---

## 5. 与 reject 的区别（Difference from `reject`）

```text
reject:
  Refuse the current DNS request.
  method=default -> REFUSED, method=drop -> no response at all.
  No negative-caching information, no statement about the name.
  A client is entitled to retry, re-ask another server, or fall back.

adblock:
  The user has already declared this name to be advertising.
  Produce a negative DNS verdict at the DNS layer, locally.
  The verdict is about the NAME, so it covers A / AAAA / HTTPS / SVCB alike.
  Goal: never enter upstream, and let the client negative-cache the result.
```

`reject` 回答的是“我不会处理这个”。`adblock` 回答的是“这个名字不存在”，
而这是唯一能让客户端停止询问的答案。

---

## 6. 与 predefined 的区别（Difference from `predefined`）

```text
predefined = a general custom DNS response builder: the operator chooses the rcode, the answer,
             the authority and the extra section, and therefore also chooses whether the result is
             negative-cacheable at all.

adblock    = a dedicated advertising semantic whose response is FIXED:
             NXDOMAIN, empty answer, one SOA carrying DefaultDNSNegativeTTL.
```

二者共享的是标志位约定，而不是配置契约。
`predefined` 配 `"rcode": "NXDOMAIN"` 且没有 `ns` 条目，是一种有效配置，
它会产出符合规范的客户端无法负缓存的 NXDOMAIN；
`adblock` 的存在，是为了让大型第三方广告规则集能够挂接到正确的否定答案上，而无需每位运维者重新推导该形状。

---

## 7. 与现有 NXDOMAIN 优化的关系（Relationship to the existing NXDOMAIN optimisation）

这是最容易被误述的部分，因此在此精确表述：

> `adblock` 复用本 fork 的 NXDOMAIN **负缓存设计原则与响应语义** ——
> 一个关于该名字的 NXDOMAIN，带真实 SOA 与非零负 TTL —— 但它是
> 一个**本地策略判定**，在 DNS 规则层终止。
> `dns/client_negative.go` 继续拥有**从上游学到的真实 NXDOMAIN**，
> 其契约没有任何改变。

具体来说：

| | 上游学到的 NXDOMAIN（`dns/client_negative.go`） | `adblock` |
|---|---|---|
| 判定由谁产生 | 权威服务器，由响应证明 | 运维者的规则 |
| 存放位置 | `nxdomainKeyFrom`（`nxdomainCacheKey`：规范小写名 + QCLASS + 传输标签 + 客户端子网 + 环境，内存 LRU） | 无处存放 —— 每次查询重新匹配 |
| 生存期 | SOA 的负 TTL，每次命中递减 | 规则的生存期；只有下游客户端缓存 |
| 命名空间 | 规范名、QCLASS、传输标签、客户端子网与环境 | 规则本身 |
| 容量 / 初始化 | `min(dns.cache_capacity, 2048)`；`disable_cache` 下为 no-op；幂等重新初始化（`dns/client_negative.go:89-120`） | 不适用 |
| 裁剪 / 清除 | 已失效判定会被裁剪（`Client.TrimExpiredCaches`、`dns/client.go:1277-1283`），并由 `ClearCache` 清除（`:1287-1293`） | 不适用 |
| 查询顺序 | 精确缓存优先，然后名字缓存，最后上游（`dns/client.go:834-866`）；切换拒绝发生在任何缓存读取之前（`:814-820`） | 在客户端之前的 DNS 规则遍历中 |
| 对 `ClearCache` 语义的存续 | 是，会被这些语义清除 | 不适用 |
| `disable_cache` / `disable_expire` | 管辖它 | 不适用 |

**`adblock` 有意不写入 `nxdomainCache`。** 该缓存的契约是“上游服务器证明了这个名字不存在”，
而规则匹配并不构成这种证明。把策略判定记录在那里，会使二者无法区分，
并且会在规则集更新后留下一个陈旧判定 ——
下文所述的放置位置正是为了避免这种失败。

`client_negative.go` 的容器级不变量未变：只有真正的 `NXDOMAIN` 才扩大，
仅限单个问题，必需 SOA，权威负 TTL 非零，必需空回答，
NODATA/SERVFAIL/REFUSED/FORMERR/超时永不扩大。
“`rewrite_ttl` 不能提升零负 TTL”这一断言，**对于“头部 TTL 为零 / MINIMUM 为正”的形状尚未得到证明**：
报文在存储时重新读取 SOA 之前已被规范化。
参见 `dns-demand.md` NXDOMAIN 一节中的后续说明；
不要把这个重写守卫当作已证明。该缓存自身的键作用域、容量、剩余 TTL 与查询顺序契约，
由 `dns-demand.md` 的“覆盖整个 QNAME 的 NXDOMAIN 负缓存”一节拥有。

---

## 8. 在哪里生效，以及为什么这正是关键所在（Where it takes effect, and why that is the whole point）

`adblock` 是在 **DNS 规则遍历**中处理的，该遍历先于 DNS 客户端运行，
因此也先于其精确缓存、QNAME 级负缓存、singleflight 以及每一个传输：

```text
Router.Exchange / ExchangeAsync
  prepareExchange
    walkDNSRules  ← adblock returns here
      match rule
      action == adblock -> RuleActionAdblock.Response(message) -> return
  (never reached) client.Exchange
                    exact cache
                    nxdomainCache
                    singleflight
                    transport exchange
```

这些后果，每一条都由 `dns/router_adblock_test.go` 中的一个测试覆盖：

1. **首次查询，上游 = 0。**
在判定可用之前，不需要先学到任何东西。
2. **跨 QTYPE，上游 = 0。** 规则会按查询重新求值，
因此 A/AAAA/HTTPS/SVCB 都只花费一次匹配，别无其他。
3. **已存在的正向精确缓存条目无法遮蔽该策略。** 遍历位于缓存的上游，
因此规则会胜过在规则存在之前就已缓存的答案。
4. **移除规则会立即生效。** 没有任何由策略导出的判定被写入缓存，因此下一次查询遵循新规则。
（已经缓存了该 NXDOMAIN 的下游客户端可以合法地保留它，直到其自身的负 TTL 过期 ——
这是 DNS 按规范工作，而不是 sing-box 内部的陈旧判定。）
5. **非广告名字不受影响。** DNS 热路径上没有新增锁、映射、goroutine、定时器或分配管理器；
未匹配的名字所走的路径与之前完全相同。

传统 DNS 模式（`exchangeLegacy` 与传统 `Lookup` 循环）也已处理，因为传统模式是由
地址过滤条件而不是由动作选择的，所以一条 `adblock` 规则可能落在任一路径中。
在 `Lookup` 路径中没有可供作答的报文，因此该判定以调用方已经理解的
“这个名字没有地址”对应的 rcode 错误形式出现。

---

## 9. 范围（Scope）

```text
Currently only for DNS domain rules.
```

适用于：`domain`、`domain_suffix`、`domain_keyword`、`domain_regex`、`rule_set`、逻辑规则、
`invert`，以及任何其他 DNS 规则条件 —— 该动作在匹配之后应用，
并不关心是什么产生了匹配。

### 固定的解析器会完全绕过规则遍历（A pinned resolver bypasses the rule walk entirely）

**本动作不适用于传输由调用方固定（pinned）的查询。**
这是*每一个* DNS 规则动作既有的契约，并非 `adblock` 特有；
之所以记录在此，是因为它令人意外：即使规则拦截了某个名字，
当有东西通过固定的服务器去查询它时，该名字仍然会被解析。

```go
dns/router.go
    if options.Transport != nil {
        ...                                  // the rule walk is never entered
    } else if !legacyDNSMode {
        responseAddrs, err = r.lookupWithRules(...)
```

这种固定来自配置，因此它在生产环境中可达，而不是理论问题：

```text
domain_resolver on an outbound       common/dialer/dialer.go  dnsQueryOptions.Transport, _ = dnsTransport.Transport(...)
route.default_domain_resolver        the same path
```

实测（MEASURED），在针对该名字的 `adblock` 规则已就位的情况下：

```text
Lookup("ads.example.", Transport = pinned)   addresses = [203.0.113.9]   err = <nil>   queryCount = 2
Lookup("ads.example.", Transport = nil)      addresses = []              err = NXDOMAIN  queryCount = 0
```

因此，`domain_resolver` 指向某服务器的拨号器，会通过该服务器解析被拦截的名字并完成连接。
该动作并没有坏 —— 它根本没有被咨询 —— 而改变这一点，将意味着改变固定解析器
对每一个 DNS 动作的含义，这超出了本特性的范围。

**对运维者的后果：** 如果某个名字对于某个拨号器必须不可解析，
规则就必须在拨号器进行解析的地方执行。要么不要为该名字固定 `domain_resolver`，
要么固定一个自身执行拦截的解析器，要么在路由层（对目的地使用 `reject`/`drop`）
而不是在 DNS 层拦截。

---

## 10. 不处理的内容（Not handled）

```text
IP / CIDR                         — not a condition of this action and not implemented by it
TCP route                          — no route-level adblock
UDP route
IP -> domain reverse lookup
transport reject/drop              — adblock is not an alias for reject and shares no code with it
Fake-IP reverse mapping
a dedicated adblock cache/LRU/timer/goroutine
disk persistence of advertising verdicts
a lookup through a PINNED resolver — see §9: the rule walk is not entered at all
```

一条 `ip_cidr` 列表非空的广告规则，仍然只拦截与其匹配的 **DNS** 名字。
从不发起 DNS 查询的直连 IP 广告连接，不属于本动作的范围，
也不受其影响。

---

## 11. race 会被拒绝（`race` is refused）

`race` 的意思是“在这条响应绑定规则作出决定之前就开始真实查询”。
`adblock` 从不咨询查询 —— 它的判定来自选中它的那次匹配 ——
因此这一组合无法被兑现，
接受它就等于静默发起该动作本就是为了阻止的那次上游查询。

两个层面都被强制执行：

- 已发布 schema 的 `adblock` 变体**只**声明了 `action`；没有 `race` 属性
  （`option/rule_action.go` → `dnsActionUnion`）；
- `validateDNSRuleAction` 在构建时拒绝 `race`，这一检查针对 `adblock` 规则
  （`route/rule/rule_dns.go`），这覆盖了在 Go 中构造而非从 JSON 解码的规则。

这与 DNS-only 边界在另一侧得到的处理相同：`adblock` 不出现在非 DNS 的规则动作联合中，
而指名它的路由规则会解码失败。

有一个后果是刻意如此的，很容易被误读为泄漏：在规则列表中，如果 `race` 规则出现在
adblock 规则之前，它仍然会发出那条 `race` 规则的查询，因为到达 adblock 时
该查询已经被武装，而被武装的规则必须根据它所等待的响应来判定。
那次查询属于写 `race` 的人。绝不能发生的是相反情况，而它确实没有发生：
在已匹配的 adblock 规则之后列出的规则不会启动任何东西，
甚至连一个稍后会被取消的查询都不会启动（`TestAdblockCannotBeMadeToLaunchAResolverByANeighbouringRule`
对两个方向都做了测量，并以不含 adblock 的列表作为对照）。

---

## 12. 日志（Logging）

`adblock` 自身不输出任何按查询的日志行。它是一次普通匹配：
现有的 DNS 规则命中调试日志会打印匹配到的规则及其动作（其 `String()` 为 `adblock`），
在启用调试日志时可见，在未启用时不产生任何开销。
若为每次被拦截的查询增加一行 `INFO`/`WARN`，就会让日志 I/O 成为
本特性正是要使其廉价的这类流量的新成本中心。

---

## 13. TTL 选择（TTL choice）

`constant.DefaultDNSNegativeTTL` 是 `600` 秒，别名指向仓库中既有的 `DefaultDNSTTL`。
两方面的理由如下：

- **太短**会把成本推回解析器：客户端会更早再次询问，而每次询问都是一次规则匹配（廉价），
本 fork 接受这一成本，
但本特性的意义在于让客户端停止询问。
- **太长**意味着在运维者移除规则之后，下游操作系统或应用仍可能持有该 NXDOMAIN。
响应会告知一个 TTL，但本代码无法保证下游客户端如何缓存或遵循它。
该动作自身不写入任何 sing-box 缓存条目。

600 秒是仓库自身的 DNS 默认值（此处每个本地作答的记录都报告该值），
处于过滤解析器所使用区间的中部，也是知名产品所公布值中较短的一端
（AdGuard Home 与 Blocky 默认 3600）。
它有意不可配置。

---

## 14. 证据与验收（Evidence and acceptance）

| 要求 | 证据 | 复检 |
|---|---|---|
| 广告名字的首次查询不触达任何传输 | `dns/router_adblock_test.go` `TestAdblockFirstQueryNeverReachesTheTransport` | `go test ./dns/ -run TestAdblock -count=1` |
| A / AAAA / HTTPS / SVCB 各自花费零次上游交换 | `TestAdblockAnswersEveryRecordTypeLocally` | 同上 |
| 配置为失败的传输永远不会被进入 | `TestAdblockNeverReachesASabotagedTransport` | 同上 |
| 128 个并发冷启动调用方、混合记录类型、零交换 | `TestAdblockSurvivesConcurrentColdStart` | 同上 |
| 已匹配 adblock 规则之后的任何规则都不发起查询 | `TestAdblockCannotBeMadeToLaunchAResolverByANeighbouringRule`（含不含 adblock 的对照） | 同上 |
| 规则存在之前缓存的条目无法遮蔽它 | `TestAdblockPolicyBeatsAnExistingPositiveCache` | 同上 |
| 移除规则在下一次查询即生效 | `TestAdblockPolicyRemovalIsImmediate` | 同上 |
| 双向都保持规则顺序 | `TestAdblockObeysRuleOrder` | 同上 |
| 传统分派路径以相同方式终止 | `TestAdblockTerminatesTheLegacyExchangePath`、`TestAdblockLegacyAndModernAnswersAreIdentical` | 同上 |
| 非广告名字保持其自身的规则与传输 | `TestAdblockLeavesOtherNamesOnTheirOriginalPath` | 同上 |
| 异步入口在本地作答，且不发生挂起 | `TestAdblockAnswersTheAsyncExchangeFromTheCallerGoroutine` | 同上 |
| 真实编译出的规则集驱动该动作 | `TestAdblockThroughACompiledRuleSet` | 同上 |
| `disable_cache` 不改变判定产生的位置 | `TestAdblockIsUnaffectedByDisableCache` | 同上 |
| 2000 次被拦截查询保持在测试的保留堆与 goroutine 容差内 | `TestAdblockSteadyStateAllocatesNothing`（额外保留堆小于 128 KiB，且最多多出四个 goroutine；响应本身会分配） | 同上 |
| 问题按原样返回，任意大小写拼写 | `TestAdblockAnswersCaseVariantsWithTheAskedSpelling` | 同上 |
| 畸形请求在动作之上被拒绝，而不是在其内部 | `TestAdblockAgreesWithTheRouterOnMalformedRequests` | 同上 |
| 响应形状、标志位、SOA、ID、问题 | `route/rule/rule_dns_adblock_test.go` `TestAdblockResponseShape`、`dns/router_adblock_test.go` `requireLocalNXDomain` | `go test ./route/rule/ -run Adblock -count=1` |
| adblock 是终止型、无状态，且不需要 logger | `TestAdblockIsATerminalActionThatStopsTheWalk`、`TestAdblockResponseNeedsNoLogger`、`TestAdblockRuleSurvivesNilAuthorizedRules` | 同上 |
| `adblock` 与 `predefined` 只在设计上声明的地方不同 | `TestAdblockDoesNotChangeThePredefinedAnswerForTheSameShape` | 同上 |
| `race` 被拒绝；DNS-only 边界；解码；往返；schema 形状 | `TestDNSRuleActionAdblockRejectsRace`、`option/rule_action_test.go` | `go test ./route/rule/ ./option/ -run Adblock -count=1` |
| 该动作的分配量与本地的 `predefined` 答案处于同一数量级 | `BenchmarkAdblockResponseConstruction` 对 `BenchmarkPredefinedResponseConstruction`（每个答案 3 allocs / 136 B 对 1 / 144 B） | `go test ./route/rule/ -run XXX -bench ResponseConstruction -benchtime 200000x` |
| 上游学到的 NXDOMAIN 行为未变 | 既有的 `dns/client_negative_test.go`、`dns/client_singleflight_test.go` | `go test ./dns/... -count=1` |

该证据表记录的是源码与历史本地单元测试运行，而不是某次具体的 CI 运行。
当前工作流声明与经独立核查的运行 SHA 记录在 `ci-release.md` 与 `ACCEPTANCE.md` 中；
这些记录并不自动在 HEAD 上确立上表中的每一行。

**本模块中有一项断言弱于其余断言，这里明确点名而不是暗示。** 并发测试驱动 128 个同时调用方，
但 `go test -race` 在此无法运行（`-race requires
cgo`，且审计机器上没有安装 C 编译器）。因此“并发答案无竞态”是由构造方式支撑的 ——
每个答案都从请求新鲜构建，该动作不持有状态，调用之间不共享任何东西 ——
而**不是**由竞态检测器支撑的。它被记录为上述证据的诚实边界，
而不是被并入其中。

---

## 15. 来源（Provenance）

| 事实 | 出处 |
|---|---|
| `RuleActionTypePredefined = "predefined"` 是当时最接近的既有终止型动作 | `constant/rule.go`、`route/rule/rule_action.go` |
| 本地作答的 DNS 响应设置 `QR/AA/RD/RA` | `RuleActionPredefined.Response`、`dns.FixedResponseStatus` |
| RFC 2308 的负 TTL 是 `min(SOA TTL, SOA MINIMUM)` | `dns/client.go` `extractNegativeTTL` |
| QNAME 级负缓存只针对从上游学到的结果 | `dns/client_negative.go` 头部与 `storeNXDomain` 守卫 |
| DNS 规则遍历先于客户端运行 | `dns/router.go` `walkDNSRules` 与 `finishPendingExchange` |

在 fork 的历史中，此前没有对该特性的尝试：
在基线提交上 `git grep -i adblock` 为空。

---

## 16. 维护规则（Maintenance rule）

> 本 fork 中任何新增或修改的、用户可见的 JSON 字段、动作、方法或协议参数，
> 都必须在同一次改动中反映到知识库。
> **只存在于 Go 结构体与源码中的配置 ABI 不视为已文档化。**
