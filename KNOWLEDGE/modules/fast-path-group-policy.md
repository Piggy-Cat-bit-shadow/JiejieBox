# fast_path —— 逻辑组策略（logical group policy）

> 状态：`VERIFIED_SOURCE`
>
> 审阅对象为仓库 `Piggy-Cat-bit-shadow/sing-box`，分支 `testing`。
>
> 本模块记录这个 fork 的 **`fast_path` 配置契约**：该字段的含义、可以在哪里设置、它如何与透明
> 直连链以及路由器的资格门禁交互，以及哪些配置会被拒绝。
>
> 如果本文档与当前源码或测试冲突，**以源码和测试为准**。

---

## 1. 范围（Scope）

本模块覆盖：

- `fast_path` 是什么，以及它不是什么；
- 分别针对 `selector` 与 `urltest` 的确切 JSON 语法；
- 该字段的三态语义；
- 哪些 outbound 类型可以携带它、哪些不可以；不允许携带它的类型一旦携带，会发生什么；
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

`fast_path` 是这个 fork 新增的**逻辑组策略**。它是**组**的属性，只回答一个问题：

> 该组是否授权它选中的一条流*尝试*一次 Direct Fast Path？

它**不是**节点能力。设置它不会把某个节点标记为“fast”，也不会跟着节点传递到别处。一个节点即使能通过 `fast_path: true` 组到达，也不会因此成为 fast-path 节点——见 §7。

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

这个字段与 `outbounds`、`type` 同级，不是逐 outbound 或逐节点的条目。

---

## 4. 三态语义（Three-state semantics）

`fast_path` 有三种状态：未设置、显式开启和显式关闭。三者的区别如下：

| 取值 | 含义 |
|---|---|
| **未设置** | 没有显式策略。如果链路只经过透明 selector，并以实际的 `direct` 出站结束，仍可尝试绕行。 |
| `"fast_path": true` | 显式允许该组选中的流尝试 Direct Fast Path。 |
| `"fast_path": false` | 显式禁止组授权，即使透明直连链原本具有尝试资格也不能覆盖它。 |

这里的 `false` 表示明确禁止，不等同于没有设置。普通布尔值无法区分两者，因此配置使用
三态表示；嵌套组的优先级见 §8。

### 以上一切生效前的两个前提（Two preconditions before any of this applies）

解析组策略前，以下两种情况会直接返回 `false`，不产生组授权：

```text
the chain has fewer than two elements
        -> there is no logical group to carry a policy, so there is nothing to authorise

the leaf cannot be bypassed at all
        -> the leaf must implement adapter.BypassableOutbound
```

第二个前提意味着，组的授权不能让不具备绕行能力的出站绕过自身。没有显式策略时，只有由透明组组成、最终到达实际 direct 出站的链，才具有内在的尝试资格。

---

## 5. 哪些 outbound 类型可以携带它（Which outbound types may carry it）

允许：

```text
selector
urltest
```

两者在 **Fast Path 链检查中都是透明的**：它们只选择一个 outbound，不构成物理跳。这不代表它们没有其他行为：selector 与 urltest 可以包装返回的连接，用于中断与生命周期管理。某条特定流是否真的可以绕过，仍由绕过资格与入口消费方检查决定。

拒绝：

```text
loadbalance
leaf outbounds — VLESS, Trojan, Hysteria2, TUIC, Shadowsocks, and any other
any other outbound that is not a FastPathAuthorizedGroup
```

**`loadbalance` 被有意拒绝。** 它不是透明 selector：它可能带有拨号、重试与再均衡语义，因此一条通过 Fast Path 跳过它的流，也会跳过这些语义。只“是一个组”还不够；必须是*透明*组。

### 配置错误是错误，不是警告（Misconfiguration is an error, not a warning）

如果在不支持的 outbound 上设置 `fast_path`，配置会直接加载失败，不会忽略该字段后继续运行。这样可以在启动时发现配置错误，避免运行行为与配置不一致。

---

## 6. `fast_path: true` 授权了什么——以及没有授权什么（What fast_path: true authorises — and what it does not）

它**只**授权以下这一件事：

```text
this logical group authorises this flow to ATTEMPT a Direct Fast Path
```

它**不是**强制指令。获得授权之后，这条流仍然必须通过 `canFastBypass` / `BypassVerdict` 资格门禁，真正的决定在那里做出。

只有会使用绕过裁决（bypass verdict）的入口才会应用这项资格判断，例如 nfqueue 自动重定向。普通 TUN 不会依据该裁决绕过用户态路径；显式绕过动作需要另行区分。具体行为取决于入口模式，参见 `route/fast_path.go`、`route/route.go` 和 `adapter/inbound.go`。

`canFastBypass` 接收 `fastPathAuthorized bool` 参数。获得授权时，只有以下一项阻断条件可能被放宽：

```text
BypassRefusedOutboundChain
```

放宽这一条件还需要同时满足两点：最终出站之前的每个元素都是透明选择组（`fastPathChainReachesLeaf`），且最终出站通过原有 `BypassableOutbound` / `CanBypass` 检查。进入组的流必然满足 `len(chain) >= 2`；如果没有这一例外，组上的授权就无法改变出站链拒绝结果。

**未获授权**的组流仍会得到 `BypassRefusedOutboundChain`。`loadbalance` 没有声明透明组能力，也会被拒绝；它可能负责所选流的拨号与重试。

**这些阻断条件不受 `fast_path: true` 影响**，这份清单正是该字段可以安全暴露的原因：

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

因此完整含义是：一条符合条件的透明组链不再构成基于链的拒绝，其他每一项资格检查仍然适用。所以，只有当链式拒绝是唯一阻断条件时，该授权才能改变最终裁决；它无法覆盖其他阻断条件。

---

## 7. 按流处理，永不写回（Per-flow, never written back）

Fast Path 授权**按流**解析。它不存储在节点上，也不跨流共享可变状态。

这一点有具体影响：一个节点可能同时被多个组引用：

```text
节点A   <-  "🤖 AI"          group with fast_path: true
节点A   <-  "🌍 普通国外流量"  group with no fast_path
```

两条流会根据各自经过的组链和最终出站分别评估，结果可能相同，也可能不同。授权只属于当前流，不会写回节点，也不会影响以后经过该节点的其他流。

---

## 8. 嵌套组：优先级（Nested groups: precedence）

组嵌套时，解析从最外层向最终出站依次进行，**第一个显式设置的 `fast_path` 决定结果**。

```text
outer group        fast_path: false   <-- decided here; inner values not consulted
  └─ middle group  fast_path: true
       └─ leaf
```

三个推论：

1. 找到第一个显式值后，不再检查链中后续的设置。
2. **`fast_path: false` 是显式禁止**：它会终止扫描，并覆盖默认授权。
3. 全链均未显式设置时，只有最终到达真实 direct 出站的全透明链会获得默认授权。此前的
   AI-tag 回退已移除（`c78edf21084`），标签本身不提供授权。

---

## 9. `fast_path` 与 `urltest` 是两个不同的问题（fast_path and urltest are different questions）

两者容易混为一谈，因为它们都出现在同类组上，但回答的是不同问题：

```text
urltest     decides WHICH NODE is selected
fast_path   decides whether the flow, having been selected, may ATTEMPT a Direct Fast Path
```

`urltest` 负责测量与挑选。`fast_path` 既不挑选，也不测量。没有 `fast_path` 的 `urltest` 组仍然会挑选节点；一条通往 direct 的透明链仍可能获得上文所述的内在授权。

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

以下测试覆盖了上述配置与策略行为：

| 属性 | 测试 |
|---|---|
| 该字段是三态且仅限组 | `option/fast_path_test.go` |
| 组策略解析与优先级 | `route/fast_path_group_test.go` |
| 不允许携带的组设置 `fast_path` 会在加载时被拒绝 | `box_fast_path_group_policy_test.go` |
| 该策略的调试/真值表行为 | `route/fast_path_debug_test.go` |

实现代码位于 `route/fast_path.go`（策略解析）与 `route/route.go`（`canFastBypass`），标记接口
在 `adapter/experimental.go`。
