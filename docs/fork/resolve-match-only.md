# `resolve.match_only`：把「解析用于匹配」与「解析用于拨号」分开

状态：功能分支实现，未合并回主线。

## 1. 要解决的问题

`resolve` 路由动作同时承担两个职责：把域名解析成 IP 让 **IP 规则**可以匹配，以及把解析结果交给
**出站拨号**。第二个职责在 CDN、分地域 DNS、代理端 DNS 与本地不一致的场景里是有害的：用户只是
想用本地 DNS 的答案决定「走哪条规则 / 哪个出站」，却因此让支持远程域名寻址的代理出站收到了一个
IP —— 原本应该由代理端自己解析的域名被换成了本地解析出的某个地址。

`match_only: true` 把这两件事拆开：

```text
输入：原始目标（域名，或 IP + sniff 出的域名）
  │
  ├─ resolve，未启用 match_only（默认，行为不变）
  │     └─ 解析结果 mergeOriginalDestination 后写入 DestinationAddresses（拨号候选）
  │        后续 IP 规则照旧读取它；连接管理器据此走候选拨号
  │
  └─ resolve，match_only: true
        ├─ 同一套查询、同一套 strategy/timeout/cache/ECS
        ├─ 结果写入 MatchAddresses（仅匹配通道）
        ├─ ip_cidr / IP rule_set / ip_is_private / preferred_by 可据此决策
        └─ DestinationAddresses 不被本动作改写：目标仍是域名时，出站实际收到域名
```

最关键的不变量：`match_only: false` / 省略时，行为与改动前逐位一致。

## 2. 实现结构

| 层 | 变化 | 说明 |
| --- | --- | --- |
| 配置 | `option.RouteActionResolve.MatchOnly bool` (`json:"match_only,omitempty"`) | schema 由结构体生成，`docs/schema.json` 同步再生成 |
| 运行时动作 | `rule.RuleActionResolve.MatchOnly`，`String()` 输出 `match_only` | 日志/预览可辨认，且与 `false` 区分 |
| 每流元数据 | `adapter.InboundContext.MatchAddresses []netip.Addr` | 仅本流；不是全局缓存，也不参与拨号 |
| 发布点 | `route.publishResolvedAddresses` | 生产 walk（`actionResolve`）与主动诊断（`diagnoseWalk`）共用，二者不会漂移 |
| 消费点 | `rule.matchAnyDestinationAddress` 统一优先级 | `ip_cidr`、`ip_is_private` 共用；`preferred_by` 单独加一条（见 §4） |
| 生命周期 | override_address 清除；后续普通 resolve 取代；UoT 逐数据报目标清除 | 目标变了，匹配依据就必须跟着变 |

## 3. 消费优先级（这是本功能的语义决定）

```
1. 字面量 Destination      → 只按它判断（客户端真正连接的地址）
2. MatchAddresses          → match_only 的答案（最近一次"用于匹配"的解析）
3. DestinationAddresses    → 拨号候选（未启用本选项时的旧路径，完全不变）
4. 都没有                  → ip_cidr 走 IPCIDRAcceptEmpty；ip_is_private 为 false
```

第 1 条是刻意的保守边界：原始目标是 IP 时，嗅探域名解析出的 IP **不会**取代真实目标参与匹配。
这与 `match_only: false` 的现有行为一致（普通 resolve 也从不改变字面量目标的匹配结果），因此同一个
规则在两种模式下不会对同一个 IP 目标给出不同答案。反过来，一个私网字面量目标不会因为嗅探域名的
DNS 答案"看起来是公网"而丢失 `ip_is_private` 命中。

**不读取 MatchAddresses 的条件（有意为之）：**

| 条件 | 原因 |
| --- | --- |
| `ip_version` | 描述目标自身地址族；两种 resolve 模式都不改变它，保持 true/false 等价 |
| `source_ip_cidr` / `source_ip_is_private` / `rule_set_ip_cidr_match_source` | 目的地址的证据绝不能用于来源判断 |
| DNS 响应专用匹配（`DestinationAddressMatchFromResponse`） | 那是"回应的内容"，不是路由动作的观察结果 |
| 拨号候选、`DialSerialNetwork`、`loadbalance_strategy` 的目的地址、failover 判定 | 这些是拨号输入，本选项的定义就是不进入拨号 |

`rule_set` 的 IP 条件编译成同一个 `IPCIDRItem`，所以现代 IP `rule_set`、嵌套/逻辑规则组、
`DeferredIPCIDRMatchGroups`/snapshot-restore 全部自动获得同样的语义，无需在规则系统里另加分支。

## 4. `preferred_by` 的一处差异

其他条件"字面量优先且短路"，而 `preferred_by` 本来就是加法的（"这里有没有任何证据偏好该地址"）。
因此它对**命名目标**额外检查 `MatchAddresses`，对字面量目标仍只按字面量 + 既有候选列表判断 ——
既保留了原有语义，也避免嗅探域名的答案把字面量目标"变成"被偏好的目标。

## 5. 生命周期与污染边界

| 场景 | 处理 |
| --- | --- |
| 同一流再次普通 `resolve` | 清除 `MatchAddresses`（新的、用于拨号的答案更晚更权威）后写候选 |
| 同一流再次 `resolve(match_only)` | 覆盖（最近一次匹配答案生效） |
| `override_address` 改写目标 | 与 `DestinationAddresses` 一起清除（旧目标的地址不得judge新目标） |
| `override_port` | 保留（地址未变，仍描述同一目标） |
| UoT / 每数据报目标（`packet_destination_guard`） | `childMetadata.MatchAddresses = nil`，会话的匹配证据不得用于另一个数据报 |
| 解析失败 / 空回答 | `dns.Lookup` 把它们作为错误返回，动作报错、不发布任何结果；不会把空结果当成"任意匹配" |
| 没有可解析的名字 | 不发布、不发查询、不动已有证据 |
| 规则之间 | `ResetRuleCache` 不清除它 —— 否则 resolve 之后的规则必然失效 |

## 6. 不进入快速直连 / native bypass

`canFastBypass` 已经在域名目标、FakeIP、嗅探域名、目标被改写、候选列表非空等条件下拒绝 bypass；
`MatchAddresses` 非空必然意味着目标是域名或存在嗅探域名（否则 `resolveLookupName` 为空、动作什么也
不做），所以它落在既有拒绝条件之内，不需要为它新增门禁。反过来，本功能也绝不允许匹配证据让一个
流跳过用户态路径：FakeIP + 流式出站仍要求一个真正可拨号的地址，`match_only` 不提供它，pre-match
会保守地拒绝并在日志里说明原因（而不是误导为"缺少 resolve 规则"）。

## 7. 配置示例

```json
{
  "route": {
    "rules": [
      { "action": "sniff" },
      { "action": "resolve", "match_only": true, "server": "dns-local" },
      { "ip_cidr": ["198.51.100.0/24"], "action": "route", "outbound": "proxy" },
      { "rule_set": "geoip-cn", "action": "route", "outbound": "direct" }
    ],
    "final": "proxy"
  }
}
```

`proxy` 是 SOCKS/VMess/AnyTLS 等支持域名目标的出站时，`198.51.100.10` 只用于选中第一条规则，
代理最终的请求仍是原始域名。若把 `match_only` 去掉，同一份配置下代理会收到 `198.51.100.10`。

## 8. 已知局限与风险

1. **本地答案不等于远端答案。** 匹配只代表"本地解析器观察到的结果"。代理端最终解析到的 IP 可能
   不同，因此基于 IP 的规则（尤其是私网/内网拒绝）不能当作远端目的的强制边界；需要强制阻断时应
   依赖真正的出站目标边界。
2. **不能用于需要本地地址的出站。** `direct` 等流式出站、FakeIP 目标需要可拨号地址；`match_only`
   不会提供，用户不能在这种组合上启用它（日志会明确说明）。
3. **IP 字面量目标上相当于"不改变匹配"。** 嗅探域名解析出的地址不会取代真实目标，这是保守选择，
   见 §3。
4. **`ip_version` 不参与。** 与既有 resolve 行为保持一致，避免 true/false 分叉。
5. **没有新增缓存。** 每次 `resolve(match_only)` 仍然是一次真实的 DNS 查询（受既有 DNS 缓存约束）；
   本功能不引入第二次查询，也不引入全局状态。

## 9. 证据（测试）

| 用例 | 断言 |
| --- | --- |
| `e2e::TestResolveMatchOnlyKeepsTheNameOnTheWire` | mock DNS 返回 `198.51.100.10`，CIDR 命中，**SOCKS 请求的 ATYP=0x03 且 host=`named.test`**；规则命中 `ip_cidr`；`MatchAddresses` 有值而 `DestinationAddresses` 为空；DNS 确实被查询 |
| 同上 / 不命中子用例 | 答案在 CIDR 之外 → 兜底 `direct`，代理零请求 |
| 同上 / pre-match 子用例 | `PreMatch`（`ConsumesBypassVerdict`）对命名目标返回 `PreMatchContinue`，不是 `Bypass`/`Flow` |
| `e2e::TestResolveWithoutMatchOnlyStillDialsCandidates` | 同一配置去掉 `match_only` → **SOCKS 请求 ATYP=0x01 且 host=`198.51.100.10`**，候选列表非空 |
| `route::TestResolveMatchOnlyPublishesMatchingEvidenceOnly` 等 | 发布点、普通 resolve 取代、二次 match_only 覆盖、无查询名不发布、override_address 清除、override_port 保留 |
| `route::TestNewRuleActionCarriesAndDescribesMatchOnly` | 配置字段 → 运行时动作 → `String()` |
| `route::TestMatchOnlyResolutionKeepsTheNameForUDP` | UDP 臂：`ListenPacket` 收到域名（match_only）/ IP（候选） |
| `route::TestPacketDestinationGuardDoesNotInheritMatchOnlyEvidence` | 跨数据报污染差分用例：使用新目标的自身解析结果；旧证据会（对照组）错误拒绝 |
| `e2e::TestResolveMatchOnlyUoTSessionDoesNotJudgeOneDatagramByAnother` | **真实 UoT 会话**：会话目标 `a.test`（→`127.0.0.1`）被允许转发；同一会话内第二个数据报发往 `b.test`（→`127.0.0.2`）被逐数据报规则丢弃（日志 `drop datagram to b.test`），随后同会话继续可用；`MatchAddresses` 只属于会话目标 |
| `e2e::TestResolveMatchOnlyFakeIPFlowKeepsTheMappedDomain` | **TUN + FakeIP 数据面**：设备拿到占位地址、连占位地址，规则由匹配地址选中代理，代理收到**域名**；占位地址与本地解析结果都不出现在线上；`FakeIP=true`、`OriginDestination` 为占位地址 |
| `e2e::TestResolveMatchOnlyPrivateAnswerSelectsTheRule` | 私网答案在两种模式下都选中 `ip_is_private` 规则，线格式分别为域名 / 答案 |
| `e2e::TestResolveMatchOnlyLookupFailureIsNotAnAllow` | 解析失败两种模式一致失败：不落到下一条规则、不触达命名出站、不兜底直连 |
| `e2e::TestResolveMatchOnlyConcurrentFlowsStayIsolated` | 20 条并发、两个互斥 CIDR + 两个独立 socks 服务器：各自只收到自己的 host |
| `experimental/libbox::TestResolveActionStageTellsMatchingEvidenceFromDialCandidates` | 诊断报告的 resolve 阶段区分"匹配证据"与"拨号候选"，不把成功的匹配解析显示为"无地址" |
| `route::TestFakeIPFlowRejectionExplainsMatchOnly` | FakeIP + 流式出站的拒绝原因区分"缺少 resolve 动作"与"resolve 用了 match_only" |
| `route/rule::rule_match_addresses_test.go` | 正/反/优先级：CIDR、私网、字面量优先、来源规则、DNS 响应规则、`ip_version`、`preferred_by`、规则组、空证据 |
| `option::TestResolveActionMatchOnlyDecodes/Roundtrips/Omitted` | JSON 解析、roundtrip、非法类型与省略/`false` 等价 |

运行侧证据：`go test ./route`（446s）、`./e2e` 全包、`-race`（route/rule、route 定向、e2e 定向）全部通过；
`IPCIDRItem.Match` 三条路径 0 allocs/op；25s fuzz（12.5 万次执行）未发现 panic 或 `match_only` 误置位。

## 10. 与 donor 的关系

`match_only` 这个字段名与"匹配/拨号分离"的思路来自
[reF1nd/sing-box](https://github.com/reF1nd/sing-box)（分支 `reF1nd-testing`，GPL-3.0-or-later）：
其 `option/rule_action.go` 新增 `MatchOnly`，`route/route.go` 在发布点分叉到 `metadata.CacheIPs`，
`route/rule/rule_item_cidr.go`、`rule_item_ip_is_private.go`、`rule_item_preferred_by.go` 读取它。

本实现是按本仓结构重新推导的，未逐字复制 donor 代码；与 donor 的差异都是刻意的：

- 字段命名为 `MatchAddresses`，并在 `adapter.InboundContext` 上完整记录用途与生命周期，避免被当作
  DNS 缓存或拨号缓存复用。
- donor 在 `DestinationAddresses` 之后追加 `CacheIPs` 检查（两个列表同时参与 "any of"）；本仓把
  优先级收敛到一个 helper，并且"最近一次解析"胜出 —— 避免出现"旧答案和新答案都命中"的双重语义。
- donor 的字面量目标同样短路，但本仓明确记录这是与普通 resolve 一致的保守选择，并为其写了测试。
- donor 未处理 `CacheIPs` 在 `override_address`、每数据报 UoT 目标上的清理；本仓补齐。
- donor 未改动 `ip_version`、`ip_accept_any` 与拨号侧消费者；本仓同样不动，并写明原因。

两个仓库均为 GPLv3-or-later；本文件不改变任何一方的许可证，也不表示 donor 项目对本改动的认可。
