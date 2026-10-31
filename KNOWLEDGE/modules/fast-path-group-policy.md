# fast_path —— 逻辑组策略（logical group policy）

> 状态：`VERIFIED_SOURCE`
>
> 审阅对象为仓库 `Piggy-Cat-bit-shadow/sing-box`，分支 `testing`。
>
> 本模块记录该 fork 的 **`fast_path` 配置契约**：该字段的含义、可以在哪里设置它、它如何与透明
> 直连链以及路由器的资格门禁交互，以及哪些配置会被拒绝。
>
> 如果本文档与当前源码或测试冲突，**以源码和测试为准**。

---

## 1. 范围（Scope）

本模块覆盖：

- `fast_path` 是什么，以及它不是什么；
- 确切的 JSON 语法，分别针对 `selector` 与 `urltest`；
- 该字段的三态语义；
- 哪些 outbound 类型可以携带它、哪些不可以，以及当不允许携带它的类型携带它时会发生什么；
- `fast_path: true` 授权了什么、没有授权什么；
- 组嵌套时的优先级；
- 为什么授权是按流（per-flow）的，且永不写回节点；
- 它与 `urltest` 的节点选择以及透明直连链的关系；
- 常见错误配置。

主要源码锚点：

```text
option/fast_path.go
option/outbound.go
option/schema.go
adapter/experimental.go
adapter/outbound.go
route/fast_path.go
route/route.go
protocol/group/selector.go
protocol/group/urltest.go
box.go
option/fast_path_test.go
route/fast_path_group_test.go
box_fast_path_group_policy_test.go
```

---

## 2. `fast_path` 是什么（What fast_path is）

`fast_path` 是本 fork 新增的一项**逻辑组策略**。它是**组**的属性，回答一个问题：

> 对于该组所选中的一条流，该组是否被授权让这条流*尝试*一次 Direct Fast Path？

它**不是**节点能力。设置它并不会把某个节点标记为“fast”，也不会随节点在任何地方传递。一个
碰巧可通过 `fast_path: true` 组到达的节点并不会因此成为 fast-path 节点——见 §7。

---

## 3. 配置语法（Configuration syntax）

`fast_path` 是组自身对象上的一个布尔字段，与 `outbounds` 并列。

### selector

```json
{
  "type": "selector",
  "tag": "🤖 AI",
  "outbounds": [
    "节点A",
    "节点B"
  ],
  "fast_path": true
}
```

### urltest

```json
{
  "type": "urltest",
  "tag": "🚀 海外高速",
  "outbounds": [
    "节点A",
    "节点B"
  ],
  "fast_path": true
}
```

该字段与 `outbounds` 和 `type` 同级，而不是逐 outbound 或逐节点的条目。

---

## 4. 三态语义（Three-state semantics）

该字段是**三态**的，而这三者之间的区分正是整个设计的意义所在：

| 取值 | 含义 |
|---|---|
| **未设置** | 没有显式决定。一条以真实 `direct` 叶结尾的透明 selector 链本身就被授权尝试绕过。 |
| `"fast_path": true` | **显式授权**该组所选中的流尝试一次 Direct Fast Path。 |
| `"fast_path": false` | **显式禁止**组授权，包括那条内在的直连链授予。 |

`false` 是一个**决定**，不是缺失。它会覆盖内在授予——见 §8。

三态配置类型的存在，正是为了让“显式 false”*可被找到*：一个普通布尔值无法区分“操作者写了
false”与“操作者什么都没写”，而这两者必须表现不同。

### 以上一切生效前的两个前提（Two preconditions before any of this applies）

解析在两个情形下返回 `false`——且没有任何组授权——这两个情形先于一切，在检查链之前就生效：

```text
the chain has fewer than two elements
        -> there is no logical group to carry a policy, so there is nothing to authorise

the leaf cannot be bypassed at all
        -> the leaf must implement adapter.BypassableOutbound
```

第二个情形正是一个代理叶永远无法被绕过的原因：授权针对的是*组*，但*叶*仍然必须是平台能够承载
的东西。当不存在显式策略时，一个位于仅由透明组构成的链之后的真实 direct 叶会提供那份内在授予。

---

## 5. 哪些 outbound 类型可以携带它（Which outbound types may carry it）

允许：

```text
selector
urltest
```

两者在 **Fast Path 链检查中都是透明的**：它们选择一个 outbound，而不构成一个物理跳。这并不
意味着它们没有其他行为：selector 与 urltest 可以包装返回的连接，用于中断与生命周期管理。绕过
资格与入口消费方检查仍然决定某条特定流是否真的可以绕过。

拒绝：

```text
loadbalance
leaf outbounds — VLESS, Trojan, Hysteria2, TUIC, Shadowsocks, and any other
any other outbound that is not a FastPathAuthorizedGroup
```

**`loadbalance` 被有意拒绝。** 它不是透明 selector：它可能拥有拨号、重试与再均衡语义，因此
一条通过 Fast Path 跳过它的流也会跳过那些语义。仅仅“是一个组”并不够；必须是*透明*组。

### 配置错误是错误，不是警告（Misconfiguration is an error, not a warning）

把 `fast_path` 放在不允许携带它的 outbound 上会使**配置加载失败**。它不会被忽略，也不会被
记录后继续：一个要求运行时不会兑现之物的配置会被拒绝，这样操作者在加载时就能得知，而不是通过
观察与文件静默不符的行为来发现。

---

## 6. `fast_path: true` 授权了什么——以及没有授权什么（What fast_path: true authorises — and what it does not）

它**只**授权以下这一件事：

```text
this logical group authorises this flow to ATTEMPT a Direct Fast Path
```

它**不是**强制指令。获得授权之后，该流仍然必须通过 `canFastBypass` / `BypassVerdict` 资格
门禁，真正的决定在那里做出。

只有当入口消费绕过裁决（bypass verdict）时才会查询这个隐式门禁：即 nfqueue 自动重定向消费方。
普通 TUN 不会用该裁决来绕过用户态路径；这是入口模式层面的区分，而不是笼统的操作系统层面区分。
显式绕过动作是另一回事。参见 `route/fast_path.go`、`route/route.go` 与 `adapter/inbound.go`。

**该门禁因这个字段而被细化，而不是保持不变。** `canFastBypass` 接收一个
`fastPathAuthorized bool` 参数，并且恰好有**一个**阻断条件因此被放宽：

```text
BypassRefusedOutboundChain
```

并且只有当叶之前的**每一个**元素都是透明的纯选择组（`fastPathChainReachesLeaf`），**并且**
该叶仍然通过未改动的 `BypassableOutbound` / `CanBypass` 检查时才成立。如果没有这一个分支，
该字段在每一种组配置上都会是惰性的，因为被路由进某个组的流必然有 `len(chain) >= 2`——因此是
这一细化让该特性得以存在，而不是它额外授予了一项许可。

一条**未获授权**的组流仍然会得到 `BypassRefusedOutboundChain`，没有变化。`loadbalance` 仍然
拒绝，因为它没有声明该能力（它可能拥有其选中流的拨号与重试，因此它不是透明的）。

**这些阻断条件不受 `fast_path: true` 影响**，而这份清单正是该字段可以安全暴露的原因：

```text
FakeIP destination
domain destination
sniffed domain
resolved candidates
destination rewrite
route original destination
UoT
UDPConnect
domain unmapping
custom UDP timeout
network options
TLS options
tracker
active power-report recorder
unproven process metadata
```

因此完整含义是：一条符合条件的透明组链可以不再构成基于链的拒绝，而其他每一项资格检查仍然适用。
所以，当那条链式拒绝是唯一阻断条件时，该授权可以改变最终裁决；它无法覆盖其他阻断条件。

---

## 7. 按流处理，永不写回（Per-flow, never written back）

Fast Path 授权是**按流**解析的。它不存储在节点上，也不跨流使用共享的可变状态。

这一点有具体影响。一个节点可能同时被多个组引用：

```text
节点A   <-  "🤖 AI"          group with fast_path: true
节点A   <-  "🌍 普通国外流量"  group with no fast_path
```

这两条流被独立评估；取决于它们各自的叶与链，它们可能得到相同的答案。授权属于该条流的链，而不是
永久属于某个节点。

正是这一属性让该特性保持为策略而非模式：没有它，一次选择就会永久改变某个节点被对待的方式。

---

## 8. 嵌套组：优先级（Nested groups: precedence）

当组嵌套时，链会**从最外层组向内、朝向叶**扫描，且**第一个显式的 `fast_path` 说了算**。

```text
outer group        fast_path: false   <-- decided here; inner values not consulted
  └─ middle group  fast_path: true
       └─ leaf
```

三个推论：

1. 第一个显式值胜出；链中后续的值不会被查询。
2. **`fast_path: false` 是一个显式决定**，因此它终止扫描并覆盖内在授予。
3. 在没有任何显式策略时，一条全透明链若指向真实 direct 叶则获得内在授予；其他链不会。先前的
   AI-tag 回退已被移除（`c78edf21084`）；标签不作授权。

---

## 9. `fast_path` 与 `urltest` 是两个不同的问题（fast_path and urltest are different questions）

两者容易被混为一谈，因为它们都出现在同类组上。但它们回答的是不同问题：

```text
urltest     decides WHICH NODE is selected
fast_path   decides whether the flow, having been selected, may ATTEMPT a Direct Fast Path
```

`urltest` 做测量与挑选。`fast_path` 不挑选任何东西，也不测量任何东西。没有 `fast_path` 的
`urltest` 组仍然会挑选节点；一条通往 direct 的透明链仍可能获得上文所述的内在授权。

---

## 10. 常见错误配置（Common misconfigurations）

```text
1. Putting fast_path on a leaf outbound (VLESS / Trojan / ...).       -> load-time error
2. Putting fast_path on a loadbalance group.                        -> load-time error
3. Expecting fast_path: true to FORCE a Direct Fast Path.            -> it only permits an attempt;
                                                                        the eligibility gate still decides
4. Expecting fast_path: true to bypass FakeIP / sniffing / tracker
   / process / TLS or network options.                              -> none of them is bypassed
5. Setting fast_path: false expecting "no opinion".                  -> it is an explicit DENY, and it
                                                                        overrides the intrinsic grant
6. Setting fast_path on an outer group while expecting an inner
   group's true to win.                                             -> the OUTER value is seen first
7. Assuming a node selected once by a fast_path group becomes a
   fast node for every later flow.                                  -> authorisation is per-flow only
```

---

## 11. 该行为在哪里被固定（Where the behaviour is pinned）

使这些陈述可被检查而非仅停留于愿望的测试：

| 属性 | 测试 |
|---|---|
| 该字段是三态且仅限组 | `option/fast_path_test.go` |
| 组策略解析与优先级 | `route/fast_path_group_test.go` |
| 不允许携带的组设置 `fast_path` 会在加载时被拒绝 | `box_fast_path_group_policy_test.go` |
| 该策略的调试/真值表行为 | `route/fast_path_debug_test.go` |

实现代码位于 `route/fast_path.go`（策略解析）与 `route/route.go`（`canFastBypass`），标记接口
在 `adapter/experimental.go`。
