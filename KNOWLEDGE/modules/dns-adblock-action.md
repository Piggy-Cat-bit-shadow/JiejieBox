# DNS Adblock 动作（DNS Adblock Action）

## 原始评审之后的本地判定与多地址族错误（Local verdicts and multi-family errors after the original review）

`dns/verdict_marker.go` 为本地规则结果标记 rcode，不改变 rcode 匹配的原有语义。
`IsLocalVerdict` 要求聚合错误的**每个叶子**都属于本地判定：如果一个地址族被拦截，另一个
地址族发生真实错误，不能只因为前者匹配就掩盖后者。

上游 NXDOMAIN 缓存是独立机制：`dns/client_negative.go` 只把带可用 SOA 的合格 NXDOMAIN
作为名称级负缓存，不能把 NODATA 或瞬时错误扩大到整个名称。这些是 `011f3fdcd99` 上的
`VERIFIED_SOURCE` 结论；本地 DNS 包测试通过，不代表真实设备已经验证。

> 状态：`VERIFIED_SOURCE`（实现、测试与 schema 已在本工作树中、引入这个特性的提交上核实；
> 见下方 `Provenance`）
>
> 原始的特性评审使用分支 `feat/dns-adblock-action`，基线 `c32c210b0db`，
> 版本 0.1.10。当前的测试目标 `011f3fdcd99` 已包含这个动作；后续改动与其更窄的证据
> 已在上文注明。历史特性分支不等于当前发布。
>
> 本模块记录 fork 的 **`adblock` DNS 规则动作**：一个只用于 DNS 的终止型动作，
> 用本地生成的、可负缓存的 NXDOMAIN 回答规则集已归类为广告的名字，
> 且不发起任何上游查询。目的就是不用审计源码也能回答
> “如何用本 fork 在 DNS 层拦截广告域名？”。如果本文档与当前源码或测试冲突，
> **以源码和测试为准**。

---

## 1. 目的（Purpose）

```text
adblock 是本 fork 新增的 DNS rule action。
命中广告域名后直接在本地返回可 negative-cache 的 NXDOMAIN，
不访问 upstream DNS。
```

`adblock` 不需要额外参数，只能用于 DNS 规则；一旦匹配，该动作就结束本次规则处理：

- 域名是否应被拦截由规则条件决定，例如 `domain`、`domain_suffix`、`domain_keyword`、
  `domain_regex` 或 `rule_set`。
- 命中后直接返回本地生成的 `NXDOMAIN` 和携带负缓存 TTL 的 SOA，不再查询上游。
- 客户端可以按响应中的负缓存信息自行缓存结果。
- 该动作不会静默丢弃查询，也不会返回 `REFUSED`、`SERVFAIL` 或 sinkhole 地址。

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

文档里的形式就是一条 DNS 规则：条件选出广告名字，动作是 `adblock`。
除此之外不需要别的东西。

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
都能和 `adblock` 配合；动作不限制是哪个条件选中了它。

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

`action` 是 `adblock` 唯一接受的字段。
`ttl`、`method`、`drop`、`reply`、`ip_mode`、`cache_size`、`fake_soa` 与 `strategy` 对本动作**不存在**，
解析时按未知字段拒绝，不会静默忽略。

`race` 属于动作的公共外层字段（`_DNSRuleAction.Race`），而不是 `adblock` 变体专属字段。因此 JSON 解码可以接受该字段，但构建 DNS 规则时 `validateDNSRuleAction` 会拒绝这一组合并报告：

```text
`race` is not supported by the adblock action: it never consults an upstream lookup
```

两类错误都会导致配置加载失败。已发布 schema 中的 `adblock` 变体只声明 `action`，因此按 schema 校验的编辑器可以在运行前标出 `race`。

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

响应根据当前查询构建：

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
（`RuleActionPredefined.Response`、`dns.FixedResponse*`）：解析器按自己的知识作答，
不依据上游服务器。

SOA 自身的 TTL 和 `MINIMUM` 取同一个值，因为 RFC 2308 §3 把负缓存生存期定义为
`min(SOA TTL, SOA MINIMUM)`，而不同客户端遵循的字段不同。

---

## 4. 设计目标（Design goals）

1. 广告名字不得产生**重复的**上游 DNS 请求 —— 不是每种记录类型一次，不是每个调用方一次，
   也不是冷启动时一次。
2. 一个广告名字带来的网络、CPU 与唤醒开销，
   必须在 DNS 层做得到的范围内尽量接近零。
3. 不静默丢弃：被丢弃的查询会让客户端重试或回退，
   这比省下来的那次上游查询更贵。
4. 不用 `reject` 实现：拒绝是一种客户端有权重试的失败。
5. 鼓励客户端负缓存，但不假定：SOA 的存在是为了让符合规范的解析器停止询问，
   而本 fork 给出的保证只针对它自己的上游流量。

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

`reject` 表示拒绝处理当前请求；`adblock` 返回本地 NXDOMAIN，表示名称不存在，并提供负缓存信息。客户端是否缓存以及保留多久，仍取决于客户端行为。

---

## 6. 与 predefined 的区别（Difference from `predefined`）

```text
predefined = a general custom DNS response builder: the operator chooses the rcode, the answer,
             the authority and the extra section, and therefore also chooses whether the result is
             negative-cacheable at all.

adblock    = a dedicated advertising semantic whose response is FIXED:
             NXDOMAIN, empty answer, one SOA carrying DefaultDNSNegativeTTL.
```

两者共享的是标志位约定，不是配置契约。
`predefined` 配 `"rcode": "NXDOMAIN"` 且没有 `ns` 条目，是一种有效配置，
产出的 NXDOMAIN 符合规范的客户端无法负缓存；
`adblock` 为大型第三方广告规则集提供符合预期的否定响应，避免每位运维者自行配置响应格式。

---

## 7. 与现有 NXDOMAIN 优化的关系（Relationship to the existing NXDOMAIN optimisation）

这部分最容易被说错，所以在这里精确表述：

> `adblock` 复用本 fork 的 NXDOMAIN **负缓存设计原则与响应语义** ——
> 一个关于这个名字的 NXDOMAIN，包含 SOA 记录与非零负 TTL —— 但它是
> 一个**本地策略判定**，在 DNS 规则层终止。
> `dns/client_negative.go` 继续拥有**从上游学到的真实 NXDOMAIN**，
> 它的契约没有任何改变。

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

**`adblock` 有意不写 `nxdomainCache`。** 这个缓存的契约是“上游服务器证明了这个名字不存在”，
规则匹配并不构成这种证明。把策略判定记在那里，两者就无法区分，
规则集更新后还会留下陈旧判定 ——
下文所述的放置位置正是为了避免这种失败。

`client_negative.go` 的容器级不变量没有改变：只有真正的 `NXDOMAIN` 才扩大，
仅限单个问题，必须有 SOA，权威负 TTL 非零，必须有空回答，
NODATA/SERVFAIL/REFUSED/FORMERR/超时永不扩大。
“`rewrite_ttl` 不能提升零负 TTL”这一断言，**在“头部 TTL 为零 / MINIMUM 为正”的情况下尚未得到证明**：
报文在存储时重新读取 SOA 之前已经被规范化。
见 `dns-demand.md` NXDOMAIN 一节中的后续说明；
不要把这个重写守卫当作已证明。缓存自身的键作用域、容量、剩余 TTL 与查询顺序契约，
归 `dns-demand.md` 的“覆盖整个 QNAME 的 NXDOMAIN 负缓存”一节。

---

## 8. 在哪里生效，以及为什么这正是关键所在（Where it takes effect, and why that is the whole point）

`adblock` 在 **DNS 规则遍历**中处理，这个遍历先于 DNS 客户端运行，
因此也先于客户端的精确缓存、QNAME 级负缓存、singleflight 以及每一个传输：

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

以下行为分别由 `dns/router_adblock_test.go` 覆盖：

1. **首次查询无需请求上游。** 规则可以直接给出判定，不依赖先前查询。
2. **不同 QTYPE 分别匹配规则。** A、AAAA、HTTPS 和 SVCB 查询命中后均无需请求上游。
3. **已有的正向精确缓存不会覆盖规则。** 规则在读取缓存之前执行，因此即使答案早已缓存，命中规则时仍按规则处理。
4. **移除规则后，下一次查询遵循新规则。** 策略判定不会写入内部缓存。下游客户端如果已经缓存 NXDOMAIN，仍可按其负 TTL 保留结果。
5. **未命中的域名沿用原有路径。** DNS 热路径没有为此新增锁、映射、goroutine、定时器或分配管理器。

传统 DNS 模式（`exchangeLegacy` 与传统 `Lookup` 循环）也处理了，因为传统模式由
地址过滤条件选择，不由动作选择，所以一条 `adblock` 规则可能落在任一条路径里。
`Lookup` 路径里没有可供作答的报文，因此判定以调用方已经理解的
“这个名字没有地址”对应的 rcode 错误形式出现。

---

## 9. 范围（Scope）

```text
Currently only for DNS domain rules.
```

适用于：`domain`、`domain_suffix`、`domain_keyword`、`domain_regex`、`rule_set`、逻辑规则、
`invert`，以及任何其他 DNS 规则条件 —— 动作在匹配之后应用，
不关心是什么产生了匹配。

### 固定的解析器会完全绕过规则遍历（A pinned resolver bypasses the rule walk entirely）

**本动作不适用于传输由调用方固定（pinned）的查询。**
这是*每一个* DNS 规则动作都有的契约，不是 `adblock` 特有；
记在这里是因为它令人意外：即使规则拦截了某个名字，
只要有东西通过固定的服务器去查它，这个名字仍然会被解析。

```go
dns/router.go
    if options.Transport != nil {
        ...                                  // the rule walk is never entered
    } else if !legacyDNSMode {
        responseAddrs, err = r.lookupWithRules(...)
```

这种固定来自配置，所以在生产环境里可达，不是理论问题：

```text
domain_resolver on an outbound       common/dialer/dialer.go  dnsQueryOptions.Transport, _ = dnsTransport.Transport(...)
route.default_domain_resolver        the same path
```

实测（MEASURED），此时针对这个名字的 `adblock` 规则已经就位：

```text
Lookup("ads.example.", Transport = pinned)   addresses = [203.0.113.9]   err = <nil>   queryCount = 2
Lookup("ads.example.", Transport = nil)      addresses = []              err = NXDOMAIN  queryCount = 0
```

因此，`domain_resolver` 指向某台服务器的拨号器，会通过那台服务器解析被拦截的名字并完成连接。
动作没有坏 —— 它根本没有被咨询 —— 而改变这一点，
等于改变固定解析器对每一个 DNS 动作的含义，超出了本特性的范围。

**对运维者的后果：** 如果某个名字对某个拨号器必须不可解析，
规则就必须在拨号器做解析的地方执行。要么不要为这个名字固定 `domain_resolver`，
要么固定一个自己就会拦截的解析器，要么在路由层（对目的地用 `reject`/`drop`）
拦截，而不是在 DNS 层。

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

一条 `ip_cidr` 列表非空的广告规则，仍然只拦截与它匹配的 **DNS** 名字。
从不发起 DNS 查询的直连 IP 广告连接，不在本动作范围内，
也不受影响。

---

## 11. race 会被拒绝（`race` is refused）

`race` 会在响应绑定规则完成判定之前发起真实查询。`adblock` 的判定来自规则匹配，本身不需要查询上游；如果允许两者组合，就可能发起本应被拦截的查询。因此配置会被拒绝。

两个层面都强制了：

- 已发布 schema 的 `adblock` 变体**只**声明了 `action`，没有 `race` 属性
  （`option/rule_action.go` → `dnsActionUnion`）；
- `validateDNSRuleAction` 在构建时拒绝 `race`，检查针对 `adblock` 规则
  （`route/rule/rule_dns.go`），因此也覆盖了在 Go 里构造、而不是从 JSON 解码的规则。

这与 DNS-only 边界另一侧的处理相同：`adblock` 不出现在非 DNS 的规则动作联合中，
指名它的路由规则会解码失败。

规则顺序仍会影响查询：如果 `race` 规则排在 adblock 之前，该规则可能已经发起查询，需要等待响应才能完成自己的判定；这不表示 adblock 发起了查询。反过来，已匹配 adblock 之后的规则不会启动查询，即使查询随后可以取消也不例外。`TestAdblockCannotBeMadeToLaunchAResolverByANeighbouringRule` 覆盖了两种顺序，并使用不含 adblock 的规则列表作为对照。

---

## 12. 日志（Logging）

`adblock` 不为每次查询单独输出日志。启用调试日志时，现有 DNS 规则命中日志会显示匹配规则和动作（`String()` 为 `adblock`）；未启用时不会输出该调试日志。逐条添加 `INFO`/`WARN` 会增加日志 I/O，应在修改前评估成本。

---

## 13. TTL 选择（TTL choice）

`constant.DefaultDNSNegativeTTL` 是 `600` 秒，别名指向仓库里已有的 `DefaultDNSTTL`。
理由有两方面：

- **太短**会把成本推回解析器：客户端会更早重新查询，每次查询都需要再次匹配规则。单次匹配开销较低，但本特性的目的之一是减少重复查询。
- **太长**意味着运维者移除规则之后，下游操作系统或应用仍可能持有这个 NXDOMAIN。
  响应会告知一个 TTL，但本代码无法保证下游客户端怎么缓存、是否遵循。
  动作自身不写任何 sing-box 缓存条目。

600 秒是仓库自己的 DNS 默认值（此处每个本地作答的记录都报告这个值），
落在过滤解析器所用区间的中部，也是知名产品公布值里比较短的一端
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
| 真实编译出的规则集驱动这个动作 | `TestAdblockThroughACompiledRuleSet` | 同上 |
| `disable_cache` 不改变判定产生的位置 | `TestAdblockIsUnaffectedByDisableCache` | 同上 |
| 2000 次被拦截查询保持在测试的保留堆与 goroutine 容差内 | `TestAdblockSteadyStateAllocatesNothing`（额外保留堆小于 128 KiB，且最多多出四个 goroutine；响应本身会分配） | 同上 |
| 问题按原样返回，任意大小写拼写 | `TestAdblockAnswersCaseVariantsWithTheAskedSpelling` | 同上 |
| 畸形请求由动作外层拒绝，不进入动作内部 | `TestAdblockAgreesWithTheRouterOnMalformedRequests` | 同上 |
| 响应格式、标志位、SOA、ID、问题 | `route/rule/rule_dns_adblock_test.go` `TestAdblockResponseShape`、`dns/router_adblock_test.go` `requireLocalNXDomain` | `go test ./route/rule/ -run Adblock -count=1` |
| adblock 是终止型、无状态，且不需要 logger | `TestAdblockIsATerminalActionThatStopsTheWalk`、`TestAdblockResponseNeedsNoLogger`、`TestAdblockRuleSurvivesNilAuthorizedRules` | 同上 |
| `adblock` 与 `predefined` 只在设计上声明的地方不同 | `TestAdblockDoesNotChangeThePredefinedAnswerForTheSameShape` | 同上 |
| `race` 被拒绝；DNS-only 边界；解码；往返；schema 结构 | `TestDNSRuleActionAdblockRejectsRace`、`option/rule_action_test.go` | `go test ./route/rule/ ./option/ -run Adblock -count=1` |
| 这个动作的分配量与本地的 `predefined` 答案处于同一数量级 | `BenchmarkAdblockResponseConstruction` 对 `BenchmarkPredefinedResponseConstruction`（每个答案 3 allocs / 136 B 对 1 / 144 B） | `go test ./route/rule/ -run XXX -bench ResponseConstruction -benchtime 200000x` |
| 上游学到的 NXDOMAIN 行为未变 | 既有的 `dns/client_negative_test.go`、`dns/client_singleflight_test.go` | `go test ./dns/... -count=1` |

这张证据表记录的是源码与历史本地单元测试运行，不是某次具体的 CI 运行。
当前工作流声明与经独立核查的运行 SHA 记录在 `ci-release.md` 与 `ACCEPTANCE.md` 中；
这些记录不会自动在 HEAD 上确立上表的每一行。

**并发验证的限制：** 测试驱动了 128 个并发调用方，但审计主机没有 C 编译器，无法运行需要 cgo 的 `go test -race`。源码中每个答案都根据请求重新构建，动作本身不持有调用间共享状态；这为并发安全提供了设计依据，但不能替代竞态检测结果。

---

## 15. 来源（Provenance）

| 事实 | 出处 |
|---|---|
| `RuleActionTypePredefined = "predefined"` 是当时最接近的既有终止型动作 | `constant/rule.go`、`route/rule/rule_action.go` |
| 本地作答的 DNS 响应设置 `QR/AA/RD/RA` | `RuleActionPredefined.Response`、`dns.FixedResponseStatus` |
| RFC 2308 的负 TTL 是 `min(SOA TTL, SOA MINIMUM)` | `dns/client.go` `extractNegativeTTL` |
| QNAME 级负缓存只针对从上游学到的结果 | `dns/client_negative.go` 头部与 `storeNXDomain` 守卫 |
| DNS 规则遍历先于客户端运行 | `dns/router.go` `walkDNSRules` 与 `finishPendingExchange` |

在 fork 的历史里，此前没有对这个特性的尝试：
基线提交上 `git grep -i adblock` 为空。

---

## 16. 维护规则（Maintenance rule）

> 本 fork 里任何新增或修改的用户可见 JSON 字段、动作、方法或协议参数，
> 都必须在同一次改动中反映到知识库。
> **只存在于 Go 结构体与源码中的配置 ABI 不算已文档化。**
