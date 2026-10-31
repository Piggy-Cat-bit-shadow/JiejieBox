# sing-box v0.1.5 → v0.1.6 全链路升级与 Action / Release 优化总结

> 仓库：`Piggy-Cat-bit-shadow/sing-box`  
> 本文汇总两个历史阶段：`v0.1.5 → v0.1.6` 的网络链路改造，以及 `v0.1.5 → v0.1.10` 的 GitHub Actions / Release 优化。内容整理自当时已有分析；使用时应以对应版本的源码和证据为准。

---

# 一、先给总判断

`v0.1.5 → v0.1.6` 的主要工作覆盖了网络数据面与控制面：从 App/TUN 入口、DNS、FakeIP、嗅探和路由，到拨号、加密、协议传输、连接复用、内存管理以及网络切换后的恢复，多个关键阶段都经历了审计、修复或优化。并非所有代码都被重写；具体改动和证据见下文。

同期另一条主线是多平台构建与发布流程的完善：

```text
网络内核：从“能跑”走向“有证据、有所有权、有生命周期语义”
构建发布：从“各平台会编”走向“exact SHA + evidence + dry-run + allowlist + release gate”
```

---

# 二、v0.1.5 → v0.1.6：整条网络链到底发生了什么

## 第一部分：入口识别与决策

这一部分负责回答：

> **“这到底是什么流量，它属于哪个网络世界，它原本想访问谁，它现在应该去哪？”**

链路可以概括成：

```text
App
 ↓
TUN / Inbound
 ↓
IP / Fragment / ICMP
 ↓
DNS / FakeIP
 ↓
Sniff
 ↓
Route rule walk
 ↓
routeflow
 ↓
“这条流量应该去哪”
```

### 1. TUN / Inbound：从函数测试变成真实数据面的 E2E

`v0.1.6` 很重要的一点，是 TUN 不再只是“若干函数能过”。

它开始真正验证：

- 真实 `ECONNREFUSED`
- 真实 TCP RST
- `SO_LINGER=0` reset
- blackhole TCP
- UDP expiry / recreate
- IPv4 fragmentation / reassembly
- ICMP fragmentation-needed
- 4000-byte UDP datagram 通过 memory TUN 和真实协议栈

这些端到端测试也暴露了跨函数的生命周期问题，例如：

```text
连接正在 dial
但还没进入 established connection map
↓
网络切换来了
↓
ResetNetwork 只处理 established connection
↓
half-open / in-flight dial 没被真正收掉
↓
blackhole dial 可能继续活到 connect timeout
甚至没有 timeout 时无限挂着
```

因此，在途连接同样需要纳入 generation、reset 和 close 的管理，不能只清理已建立的连接。

### 2. IP / Fragment / ICMP：网络不再只等于“socket 能连上”

仅凭连接建立成功，不能判断整条网络路径是否可用：

```text
TCP connect 成功
=
网络路径没问题
```

`v0.1.6` 的审计还将以下因素纳入网络正确性范围：

- MTU
- IPv4 fragmentation
- fragment reassembly
- ICMP PTB / fragmentation-needed
- MSS
- packet geometry

因此，除了检查 socket 是否连接成功，还要检查实际路径能否承载指定大小、协议和封装的数据包。4000-byte UDP 与 ICMP fragmentation-needed 端到端测试用于验证这些链路约束。

### 3. DNS：从 cache 变成 network generation / policy ownership

`v0.1.6` 里 DNS 的变化很大。

DNS cache 不再只是：

```text
domain -> answer
```

而是开始显式绑定：

- network generation
- environment ownership
- policy epoch
- cache ownership
- commit validity

#### 一个典型 TOCTOU 问题

假设：

```text
请求开始于 policy epoch 7
↓
查询完成
↓
检查：当前还是 epoch 7
↓
ClearCache
↓
epoch 变成 8
↓
旧请求继续把 epoch 7 的结果写回 cache
```

仅在写入前检查一次仍可能留下检查与提交之间的竞争窗口。处理方式是让 epoch 决定提交资格：

```text
旧 generation / 旧 policy epoch
=
没有资格向新世界提交状态
```

reverse mapping 和 background refresh 也需要遵守相同的提交边界。

### 4. FakeIP：从简单映射变成 issuance ownership ledger

FakeIP 的基本映射关系可以表示为：

```text
FakeIP
→
domain
```

实际管理还需要发行与所有权记录。`v0.1.6` 涉及的边界包括：

- 谁发行了这个 FakeIP
- 在哪个 network environment 下发行
- 属于哪个 generation
- 生命周期是否仍有效
- TUN / proxy 路径上如何 map / unmap

FakeIP 地址是合成占位符，不能仅凭地址形式就决定 Direct 路径。相关语义还包括：

- 原始 domain
- DNS policy
- FakeIP ownership
- sniff / route metadata
- 当前 generation

处理 FakeIP 时，需要同时考虑映射、所有权和路由元数据。

### 5. Sniff：从“做太多”改成“只做我真正需要的事”

这是 `v0.1.6` 里面非常典型的一类性能修复。

TLS sniff 之前会走到近似 server handshake 的路径。

也就是说，为了知道 ClientHello 里是什么，代码实际可能继续走：

- `tls.Server(...)`
- `HandshakeContext()`
- `processClientHello`
- ECDHE key share
- ML-KEM / X25519MLKEM768 key share
- 然后因为没有证书失败

嗅探只需要识别 ClientHello，不需要完成服务端握手。优化的重点是移除这些额外工作。

已有 benchmark 里，大致出现过：

- TLS sniff 时间约 `-96.2%`
- fragmented TLS sniff 时间约 `-91.9%`
- cached 场景约 `-98.8%`
- allocation 同时明显下降

同时：

- `PeekStream` 改为 O(1) rewind cursor
- 避免反复堆 `MultiReader`
- HTTP sniff 加 gate
- QUIC frame summary / zero-copy / ownership 收敛
- fragmented sniff 更稳
- 新增 WireGuard sniffer

设计原则是：嗅探只执行识别所需的工作，不继续执行完整协议握手。上面的性能数字属于历史测试结果，实际收益取决于环境和版本。

### 6. Route：从“配置里有规则”升级为“运行时真的能到达这个规则”

`common/routeflow` 用于分析路由规则的可达性。只扫描配置可能得出过于宽泛的结论：

```text
扫描配置文件
↓
找到某条 rule
↓
认为这条 flow 有可能走到这里
```

实际路由遍历还受以下因素影响：

- 规则顺序
- first-match
- logical rule
- invert
- sniff
- resolve
- action
- group
- detour
- 前面规则是否已经终止

因此，配置中存在规则，并不代表某条流在运行时一定能到达该规则。`routeflow` 根据路由语义分析可证明的需求和可达性，对无法确定的情况应保留不确定性。推理方式从：

```text
配置文本推理
```

变成：

```text
运行时语义推理
```

这对于后面的 Fast Path、Direct、offload、安全判断都非常重要。

### 第一部分压缩成一句话

`v0.1.5` 更像：

```text
包进来
→ DNS
→ sniff
→ route
```

`v0.1.6` 更接近：

```text
真实 packet
→ packet / fragment / PTB
→ generation-aware DNS
→ ownership-aware FakeIP
→ low-cost sniff
→ runtime-semantic rule walk
→ real reachability proof
→ 得出可信路径决策
```

上述变化共同改善了流量识别和路由决策。

---

# 三、第二部分：选路、建连、失败以后怎么办

这一部分介绍系统如何选择路径、建立连接，并处理连接失败。

链路：

```text
Route decision
 ↓
Group / selector / URLTest / load-balance
 ↓
ControlPath
 ↓
PhysicalPath / detour chain
 ↓
Destination resolve
 ↓
Dial capability / destination ownership
 ↓
real dial
 ↓
failure classification
 ↓
generation-scoped penalty
 ↓
bounded alternate retry
 ↓
proof-of-life clears penalty
 ↓
Direct / Proxy 真正开始传数据
```

## 1. Group / URLTest / Load Balance：选择机制和路径证据分开

组选择、路径健康和重试决策之间需要共享适当的失败信息。selector / urltest / load-balance 原本容易被视为只负责：

```text
自己测
自己打分
自己选
```

但真实连接失败本身也是最有价值的证据。

所以 v0.1.6 开始把：

```text
真实 dial failure
```

重新反馈给：

```text
group / path health / retry decision
```

选择机制负责选择成员，真实连接失败提供判断路径健康的证据；两者的职责需要区分。

## 2. ControlPath 与 PhysicalPath 正式分开

这是非常关键的一步。

### ControlPath

逻辑上的组层级：

```text
外层 group
→ 内层 group
→ leaf
```

它描述选择决策经过的组。

### PhysicalPath

真正承载字节的路径：

```text
device-nearest hop
→ detour hop
→ exit hop
```

它描述数据实际经过的物理跳。group 是选择节点，本身不是物理跳；detour 则可能引入多个物理跳。`common/physicalpath` 将两种路径分别建模。

## 3. Static validation：启动前检查路径

v0.1.6 逐渐形成的顺序是：

```text
dependency lint
↓
physical-path dry-run
↓
start
```

启动前的试运行检查可证明的结构和能力需求：致命的结构问题会阻止启动，能力不足在当前契约下可能只发出警告，不能据此保证真实网络可达。诊断也不得消耗 round-robin 或 selector 的运行时选择状态，否则检查本身会改变后续流的选路。

## 4. Destination resolve：谁拥有 destination

在一条真实网络流里，destination 可能有很多形态：

```text
domain
resolved IPv4
resolved IPv6
FakeIP
sniffed domain
rewritten destination
```

如果没有明确的所有权，多个模块可能重复改写 destination，难以追溯最终地址的来源。v0.1.6 的 resolve / dial 架构因此关注：

- unresolved domain
- resolved IP
- dual-stack
- resolver ownership
- resolve action
- detour DNS semantics
- destination ownership

各阶段应保持明确的目的地解析和改写责任。

## 5. Dial：不只是调用 `DialContext`

拨号决策需要考虑以下条件：

- 当前 network generation 是否仍有效
- destination 是谁解析的
- outbound 是否具备对应 capability
- detour 是否成立
- 当前 physical path 是否仍有效
- half-open dial 是否受生命周期管理

因此，拨号不仅要调用连接函数，还要遵守网络状态、路径、目的地所有权和能力约束。

## 6. Adaptive routing / resilience：失败是证据，不是命令

这套逻辑可以压成：

```text
真实 dial error
↓
classify：是不是路径死亡证据
↓
generation-scoped penalty
↓
最多一次 alternate retry
↓
新路径 proof-of-life
↓
清除 / 修正旧 penalty
```

主要约束如下。

### 第一：最多一次 alternate retry

即使 group 有多个成员，也不能无限重试：

```text
A 失败
→ B
→ C
→ D
→ 无限试
```

限制重试次数可以避免离线时产生重试风暴。

### 第二：failure 是 evidence

不能将所有错误都视为节点失效，需要结合以下信息分类：

- 错误类型
- 当前 generation
- 是否是自己 cancel 的
- 是否是网络 transition 导致的
- 是否真正测量过

### 第三：不能用 teardown 污染 health

例如，网络切换时主动取消旧连接，不应将这次取消记为节点故障。否则系统自身的清理行为会污染健康状态。

### 第四：penalty 必须属于 generation

网络 A 下的失败证据：

```text
Wi‑Fi generation 10
```

不能直接带到：

```text
5G generation 11
```

旧网络上的失败证据不能直接用于惩罚新网络上的路径。

## 7. Direct：不是“绕过越多越快”

`v0.1.6` 对 Direct 资格、路径检查，以及 FakeIP、嗅探、目的地所有权和 tracker 防护等语义做了调整；这些改动不意味着最底层的字节搬运路径全部重写。

Direct 要满足的不是：

```text
route=direct
→ 直接旁路
```

而是：

```text
route 语义允许
FakeIP 语义允许
sniff 语义允许
destination ownership 清晰
tracker/dashboard guard 允许
physical path proof 成立
DNS / 广告过滤要求没有被绕掉
```

才能进入更快路径。

Direct 相关路径可以按以下层次理解：

```text
L0：OS / TUN route-set bypass
L1：semantic Direct Offload
L2：splice / kernel socket path
L3：buffered copy
```

选择 Direct 不等于可以跳过该流原本需要的 DNS 解析或广告过滤；是否需要解析仍取决于目的地和配置。

## 第二部分小结

上述机制分别处理成员选择、物理路径、连接建立以及失败后的有限恢复。

---

# 四、第三部分：协议、加密和真正的数据传输

现在已经：

```text
知道是谁
知道去哪
知道走哪条 physical path
socket 也建立了
```

接下来才轮到：

> **真正把字节送出去。**

链路大致是：

```text
Dial established
 ↓
TLS / uTLS / REALITY
 ↓
VLESS / protocol layer
 ↓
XHTTP / H2 / H3 / MASQUE
 ↓
pool / mux
 ↓
traffic scheduler / backpressure
 ↓
buffer ownership / copy path
 ↓
MTU / headroom / packet geometry
 ↓
wire bytes
```

## 1. TLS / uTLS / REALITY：握手本身也进入统一语义

这一层涉及以下握手与兼容性功能：

- REALITY client / server
- Xray hybrid compatibility
- key_share classical / hybrid
- session ID
- short ID
- uTLS
- TLS fragment
- fail-closed fingerprint
- auth fallback
- minimum client version

握手需要与连接建立、失败处理和生命周期管理共同考虑。

## 2. VLESS：开始引入 PQ encryption 路线

`v0.1.6` 里新增的一个很明显的协议扩展，是 VLESS post-quantum encryption。

包括：

- ML-KEM-768
- X25519
- BLAKE3 DeriveKey
- AES-GCM / ChaCha20

这些能力扩展了协议层支持的加密方式。

## 3. XHTTP：这是 v0.1.6 真正的大型新能力

这一代 XHTTP 传输涉及：

- `packet-up`
- `stream-up`
- `stream-one`
- `auto`
- H1
- H2
- H3
- XMUX pool
- breaker / backoff
- error boundary
- local-close classification
- idle trim
- `with_xhttp` compile gate

这些功能共同构成 XHTTP 客户端传输子系统。

## 4. MASQUE：从“能 CONNECT-IP”到真实 packet 生命周期

MASQUE 的基础支持来自上游；本 fork 在 CONNECT-IP 数据面和生命周期上进行了扩展，涉及：

- capsule bound
- overlap handling
- H3 pooling
- H3 fallback
- fragmentation
- ownership
- MTU
- lifecycle

这些路径还需要分别验证所有权、错误处理及连接生命周期；源码或单元测试不能代替真实网络和对端互操作测试。

## 5. Connection pool / mux：旧连接不是简单 close

网络变化以后，连接池不能只做：

```text
close all
```

更合理的是：

- idle
- suspect
- retire
- reset
- drain

其中一个很重要的设计：

> **旧 session 可以继续 drain 已经存在的 stream，但不能再接收新的 stream。**

这比“一刀切全关”高级很多。

因为它区分：

```text
已有业务连续性
```

和：

```text
未来复用资格
```

## 6. Traffic scheduler / backpressure

你的 scheduler 并不是 v0.1.6 才第一次出现。

但这一代明显继续把：

- accounting
- ownership
- contention
- endpoint backpressure
- pacing
- failure semantics

往统一模型收。

生产上仍然应该清楚：

> 没配置 rate 时必须近似 inert。

而 research-only adaptive learned rate 不应该污染正式 release gate。

## 7. Buffer / ownership / copy：从“有没有 copy”变成“谁拥有这块内存”

`common/bufgeom` 是 v0.1.6 很典型的新基础设施。

低内存 geometry：

```text
normal:
32 KiB / 16 KiB

low-memory:
16 KiB / 8 KiB
```

重点其实不是大小。

而是 ownership。

典型原则：

```text
handoff 成功
→ 接收方拥有

handoff 失败
→ 当前方释放剩余 ownership
```

而不是：

```text
两边都以为对方会释放
```

或者：

```text
两边都释放
```

这里还包括：

- pooled cap
- UDP NAT buffer
- MASQUE DATAGRAM ownership
- splice / kTLS audit
- low-memory geometry
- bounded copy

所以所谓 zero-copy 也开始被更严格地区分。

不能把：

```text
减少了一次 copy
```

宣传成：

```text
全链路 zero-copy
```

## 8. MTU / headroom：每层协议都有自己的真实容量

这一代逐渐把 MTU 拆得更清楚：

- outer link MTU
- QUIC packet size
- inner tunnel MTU
- protocol headroom
- detour path capacity

例如：

- MASQUE inner 1280
- PTB
- Hysteria2 `InitialPacketSizeCeiling`
- ChromeParrot ceiling
- `DetourPathCapacity`
- `PortMTUProvider`

原则是：

> **容量不够就明确失败，而不是凭空发明一个能用的 MTU。**

## 第三部分一句话

前两部分是在决定：

> **“怎么走。”**

第三部分是在解决：

> **“真正走的时候，这些字节怎样被加密、封装、复用、调度、复制，并在真实 MTU 下送出去。”**

---

# 五、第四部分：网络变化、休眠恢复和生命周期闭环

这一部分是最容易在普通代理内核里被低估的。

连接不是：

```text
Start
→ 永远不变
→ Close
```

移动设备真实世界是：

```text
Wi‑Fi
→ 蜂窝
→ Wi‑Fi
→ 锁屏
→ suspend
→ resume
→ 内存压力
→ 网络重配
→ 旧 session 仍在 drain
→ 新 session 需要新网络
```

所以这一代的核心之一，就是把“网络世界”做成真正的 generation / lease / epoch。

## 1. runtimecoord：generation / epoch / lease

核心思想：

> **网络状态不能用几个 bool 拼起来。**

它需要一个单调递增的身份：

```text
generation 10
generation 11
generation 12
```

同时 lease 也有身份。

一个非常重要的原则：

> **被 supersede 的旧 lease 没资格完成新 generation 的状态。**

否则会发生：

```text
旧网络慢操作最后返回
↓
覆盖新网络已经建立的正确状态
```

这本质上也是 ownership。

## 2. Network transition：不要 global close

以前最粗暴的网络切换：

```text
网络变化
→ 全关
→ 全重建
```

代价非常大。

更成熟的行为是：

- stale
- suspect
- retire
- rebind
- drain

把：

```text
已有流
```

和：

```text
新流
```

分开对待。

这就是从：

> reset everything

转向：

> **stateful transition**

## 3. Sleep / Wake：休眠不是网络死亡

移动端锁屏以后：

- 网络可能暂时挂起
- socket 可能还存在
- path 可能失效
- session 可能可以继续，也可能不能复用

所以你的体系逐渐形成 sleep band：

```text
<5s      KEEP
5–15s    SUSPECT
>=15s    RETIRE
```

重点：

> **active stream 不应该被无脑杀掉。**

而是控制：

> 旧 session 是否还能承载“新流”。

## 4. Power Governor / Reuse Epoch

`v0.1.5` 已经引入了 Power Governor。

`v0.1.6` 则继续把：

- power state
- network epoch
- reuse epoch
- reuse boundary

拆得更清楚。

也就是说：

> **“网络变了”**
>
> **“设备睡了”**
>
> **“旧连接不能再复用了”**

这三件事不应该被强行当成同一个事件。

## 5. Trim / ReleaseMemory / Close

这三个也不应该混。

### Trim

更像：

```text
主动清 idle
```

不应该随便 bump network generation。

### ReleaseMemory

属于紧急内存回收。

必要时可以更激进。

### Close

是生命周期终态。

必须：

- bounded
- idempotent
- concurrent-safe

并且所有 Scope cleanup 最终都应该闭环。

## 6. E2E lifecycle 证明

这一代的意义还在于：

> 生命周期不再只凭“看代码应该没问题”。

而是开始真实验证：

- reset under traffic
- active trim 后连接仍存活
- busy connections 下 Close bounded
- repeated Start / Close
- goroutine count 不持续增长

这就是所谓：

> **evidence-driven runtime**

---

# 六、29 个真实链路阶段：v0.1.6 覆盖面总表

| # | 链路阶段 | v0.1.6 主要变化 |
|---|---|---|
| 1 | App → Inbound/TUN | 真实 TUN E2E、TCP/UDP frame、reset/close |
| 2 | IP packet / fragment | IPv4 fragmentation / reassembly、4000B UDP |
| 3 | ICMP / PTB / MSS | fragmentation-needed、MSS、path MTU |
| 4 | DNS request entry | router/client、dual-stack、policy |
| 5 | DNS transport | TCP/TLS/UDP/HTTPS/QUIC lifecycle、DNS group |
| 6 | DNS transport selection | stable/fastest/parallel、anti-storm |
| 7 | DNS cache | network/environment ownership + policy epoch |
| 8 | DNS cache commit | TOCTOU closure、epoch owns commit |
| 9 | Reverse mapping | atomicity / invalidation window |
| 10 | FakeIP issuance/storage | issuance ledger、ownership、proxy/TUN E2E |
| 11 | Sniff entry | PeekStream/Packet、bounded copy、ownership |
| 12 | TLS sniff | 移除 server handshake 式错误工作，性能巨幅改善 |
| 13 | HTTP/QUIC/WG sniff | gates、frame summary、zero-copy、WG sniffer |
| 14 | Route rule walk | runtime semantics、顺序、resolve/sniff/action |
| 15 | Route reachability | `common/routeflow` |
| 16 | Group / URLTest / LB | health evidence、real failure feedback |
| 17 | PhysicalPath / detour | `common/physicalpath`，ControlPath 与 PhysicalPath 分开 |
| 18 | Destination resolve | dual-stack、resolver ownership |
| 19 | Dial | capability、destination ownership、epoch guard |
| 20 | Dial failure / retry | classifier → penalty → bounded alternate → proof-of-life |
| 21 | Direct | eligibility / FakeIP / sniff / path semantics |
| 22 | TLS/uTLS/REALITY | hybrid key share、session、fragment、fingerprint |
| 23 | Protocol encapsulation | VLESS PQ、early-data ownership |
| 24 | XHTTP/H2/H3/MASQUE | XHTTP 大规模引入、H3 fallback、MASQUE lifecycle |
| 25 | Connection pool / mux | idle/suspect/retire/drain |
| 26 | Traffic scheduler/backpressure | accounting / contention / endpoint backpressure |
| 27 | Buffer/ownership/copy | `bufgeom`、handoff ownership、pool、copy audit |
| 28 | MTU/headroom/geometry | path capacity、inner/outer MTU、protocol cost |
| 29 | Network change → Sleep → Reclaim → Close | runtimecoord、generation/lease、Power/Reuse Epoch、Trim/Close |

这张表基本解释了为什么：

> **`v0.1.5 → v0.1.6` 可以称为一次 end-to-end network-stack audit + rewrite。**

---

# 七、v0.1.6 最核心的代际变化

如果要把 `v0.1.5 → v0.1.6` 的变化再抽象一层：

`v0.1.5` 还是更偏：

> **mechanism-driven**

也就是：

```text
有这个机制
→ 就尝试用
```

`v0.1.6` 明显开始变成：

> **evidence-driven runtime**

也就是：

```text
我知道什么时候有资格做
我知道为什么做
我知道依据是什么
我知道这个证据属于哪个 generation
我知道什么时候必须停止相信旧判断
```

所以：

```text
v0.1 → v0.1.3
≈ 我能做更多事情

v0.1.5 → v0.1.6
≈ 我知道什么时候有资格做、为什么做、依据什么做、什么时候停止相信之前的判断
```

这是比“新增了几个协议”更大的变化。

---

# 八、关于上游来源：版本跨度和原创工程必须分开

分析 `v0.1.5 → v0.1.6` 时，要分两个地图。

## 地图 A：version-span map

只问：

> 这个版本跨度里到底发生了什么变化？

不管来源。

## 地图 B：fork / user engineering map

再问：

> 哪些是自己真正设计、修改、调试、补强的？哪些是上游 replay / patch-equivalent？

这是因为这个跨度里确实包含上游 replay。

所以：

> **602 commits 是跨度背景，不是“602 个原创改动”。**

但这不影响另一个结论：

> **从最终运行时覆盖面看，v0.1.6 确实几乎把整条网络链重新摸了一遍。**

---

# 九、现在进入第二大主题：Action / Release 到底优化了什么

Action / Release 这条线，也不能只看：

```text
“跑了多少分钟”
```

真正要看的是：

- 工作总量
- 并行程度
- critical path
- 重复验证
- artifact producer
- release consumer
- exact SHA
- evidence
- allowlist
- provenance
- checksum
- dry-run
- publish boundary
- 取消和失败是否被正确区分

最理想的职责拆分其实是：

```text
Verify
=
验证 source / code truth

Product Build
=
验证 artifact truth

Release
=
验证 identity / evidence / release boundary
```

也就是：

> **Release 不应该再把 Verify 的大测试全部重新跑一遍。**

---

# 十、Action / Release 的核心职责模型

## 1. Verify：验证源代码

负责：

- source correctness
- tests
- static checks
- buildability
- contract checks

它回答：

> **“这个 SHA 的代码本身是否可信？”**

## 2. Product Build：验证产物

负责：

- Linux core
- macOS core
- Windows core
- Android libbox / core
- Windows Desktop
- Apple 相关产物

它回答：

> **“这个 exact SHA 真正编出来的东西是什么？”**

## 3. Release：验证身份与边界

Release 更应该聚焦：

- exact-SHA run selection
- producer conclusion
- artifact identity
- checksum
- provenance / BUILD-INFO
- expiry / freshness
- release tree allowlist
- dry-run
- publish
- read-back

它回答：

> **“我要发布的，是不是刚才那些已经验证过的 exact SHA 产物，而且没有多、没有少、没有串版本？”**

这才是 Release gate 真正应该干的事。

---

# 十一、早期 CI 的重复工作在哪里

之前分析过几个明显重复。

例如 release gate 相关逻辑曾经同时存在于：

- Verify
- Release contract verification
- Release

同一类约 90+ case 的 self-test 可能被重复执行。

Android 也有与 Verify 重复的内部测试。

还有：

- Windows core 可能为 release / unstripped / upstream baseline 分别构建
- Android 曾经实际编多个 AAR，但正式只发布一个
- Apple libbox 曾构建额外 macOS amd64 slice
- Windows Desktop 的安装 / service lifecycle gate 很重

这类优化的重点不是：

> “把测试删掉。”

而是：

> **把同一证明只放在最合适的一层证明一次，然后让后续层消费 evidence。**

---

# 十二、v0.1.6：Action / Release 真正的“构建系统大爆炸”

`v0.1.6` 的 exact-SHA producer 基本是同时起跑的。

大致如下：

| v0.1.6 任务 | 实际耗时 |
|---|---:|
| Verify | 10m21s |
| Linux amd64 | 20m07s |
| macOS arm64 | 14m23s |
| Android ARM64 | 7m14s |
| Apple Client | 28m16s |
| Xray interop | 2m58s |
| Windows Core | 4m15s |
| Windows Desktop | 22m48s |

这些任务如果串行加起来，大约是：

> **110 分钟 runner 工作量**

但因为并行：

```text
03:54:53 左右开始
↓
04:23:17 最慢 Apple Client 完成
```

producer critical path 大约只有：

> **28m24s**

也就是说，单纯依靠并行：

> **约 1 小时 50 分钟总工作量，被压成了约 28 分钟墙钟时间。**

墙钟压缩大约：

> **74%**

---

# 十三、v0.1.6 真正的 Release 时间线

这一段非常关键。

```text
03:54:53
候选构建开始

↓ 并行 producers

04:23:17
最慢 producer（Apple）完成

04:23:44
Release 尝试

04:24:08
gate 自己失败

↓
继续修 release pipeline

04:35:34
v0.1.6 正式 GitHub Release published
```

所以从：

```text
正式候选开始
→ 正式 Release 可见
```

实际大约：

> **40m41s**

GitHub Release 自身记录：

```text
created_at:   03:54:30
published_at: 04:35:34
```

约：

> **41m04s**

---

# 十四、v0.1.6 为什么体感甚至接近 1 小时 20 分钟

因为 04:35 的时候：

> core Release 已经发布。

但一些后补资产，比如 Windows Desktop：

- installer
- BUILD-INFO
- `.sha256`

一直到约：

> **05:14:07**

才全部补齐。

所以如果使用者的心理标准是：

> **“所有这次该出的东西真的都齐了。”**

那么从第一轮候选开始到资产全部齐，大约：

> **1 小时 19 分钟**

所以 v0.1.6 是一个非常典型的攻坚版本：

> **≈110 分钟并行 CI 工作量 + 28 分钟 producer critical path + Release gate 现场爆雷 + 多平台资产后补。**

---

# 十五、v0.1.7：不是普通 Release，而是拿一个版本号狠狠干发布系统

`v0.1.7` 很特殊。

它有 annotated tag。

tag 最终指向：

```text
6f0d5519c74816f0a4c255f46a08fe0f298b3cd7
```

但：

> **没有正式 GitHub Release 页面。**

所以必须明确：

```text
有 tag
≠
一定有 GitHub Release
```

`v0.1.7` 就是反例。

它更像：

> **Release pipeline 攻坚候选。**

---

# 十六、v0.1.7 的 dry-run 真正抓到了什么

当时大致：

```text
19:21:39
Release Contract Verification 开始

19:22:09
PASS
≈30s

19:22:04
Release dry-run

19:22:44
FAIL
≈40s
```

失败内容是：

```text
unexpected files:
SHA256SUMS.txt
libbox.provenance
```

不是产品坏了。

而是 staging layout 认错。

真实问题：

```text
gh run download
↓
保留 artifact 自己的目录层级
↓
release staging layout
和脚本原来的假设不一致
↓
root-relative rm 没删到目标文件
↓
rm -f 还不会报错
↓
dry-run allowlist 最终把多余文件抓出来
```

这恰好说明：

> **dry-run 不是形式主义。**

它真的挡住了一次错误发布。

---

# 十七、为什么不能简单把 allowlist 放宽

一种最简单但很差的修法是：

```text
既然多了 SHA256SUMS.txt / libbox.provenance
那就把它们加到 allowlist
```

但这会破坏 release boundary。

因为以后真的混进一个意外文件：

```text
malformed
stale
wrong version
debug artifact
unexpected evidence
```

也可能一起被允许。

所以正确思路是：

> **修 staging cleanup / artifact classification，而不是扩大“什么都能发”的边界。**

这也是 release engineering 开始成熟的标志。

---

# 十八、v0.1.7 同 SHA 后续攻坚

同一个 SHA 上继续跑：

```text
Linux
19:23:42 → 19:34:05
≈10m23s
PASS

macOS
19:23:44 → 19:38:48
≈15m04s
FAIL

Windows runtime audit
19:31:07 → 19:44:30
≈13m23s
PASS
```

如果只看这一轮能观察到的 exact-SHA runtime audit 收口：

> **大约 22m51s**

但它没有进入正式 Release publish。

所以这个版本不能写成：

> “等 XX 分钟就发成功。”

更准确的定位是：

> **tag + evidence/gate 攻坚版本。**

---

# 十九、v0.1.6 与 v0.1.7 在历史上的角色

一句话概括：

### v0.1.6

> **我把所有平台和所有东西塞进 CI，终于让它们都开始跑。**

### v0.1.7

> **我发现“能跑完”不等于“可以安全发布”，于是狠狠干 Release contract、artifact staging、allowlist、dry-run 和 runtime audit。**

然后：

### v0.1.8 以后

> **这套东西才真正开始工业化收敛。**

---

# 二十、从 v0.1.5 到 v0.1.10 的 Action / Release 演进

一个比较准确的阶段划分：

| 版本 | 构建 / 发布角色 |
|---|---|
| v0.1.5 | 旧式多平台成熟期，仍有较多历史结构 |
| v0.1.6 | 多平台 producer 大爆炸 + Release 第一次大规模硬仗 |
| v0.1.7 | Release contract / dry-run / artifact staging / runtime audit 攻坚 |
| v0.1.8 | supply-chain / release engineering 开始稳定 |
| v0.1.9 | Libbox release artifact correctness 热修 |
| v0.1.10 | Fast Path 语义 + Windows security / CI / KB 收口，发布链进入成熟态 |

---

# 二十一、墙钟时间的核心变化

前面分析里有一个非常直观的对比：

```text
v0.1.6
正式候选 → 正式 Release
≈ 40m41s

v0.1.10
正式发布
≈ 23m52s
```

少了：

> **16m49s**

墙钟下降约：

> **41%**

重点还不是单纯“快 41%”。

而是：

> **后面的 evidence、identity、Windows Desktop、Android、Libbox、checksum、release contract 比 v0.1.6 更完整。**

也就是：

```text
证明更多
边界更严
平台更多
但反而更快
```

这才是真正有价值的优化。

---

# 二十二、为什么这不是“删测试换速度”

如果只是删掉 gate，当然谁都能快。

真正的优化是：

```text
Verify
只负责 source truth

Build
只负责 artifact truth

Release
只负责 identity + boundary
```

然后建立 evidence chain：

```text
source SHA
↓
producer run
↓
artifact
↓
hash
↓
provenance / BUILD-INFO
↓
release staging
↓
strict allowlist
↓
dry-run
↓
publish
↓
read-back
```

这叫：

> **减少重复证明，而不是减少证明。**

---

# 二十三、Release 最该保留的东西

即使继续追求速度，这些也不应该删：

- exact-SHA selection
- producer success conclusion
- artifact identity
- checksum verification
- provenance / BUILD-INFO
- artifact freshness / expiry
- strict release allowlist
- dry-run
- publish boundary
- release read-back

因为这些是：

> **供应链正确性。**

---

# 二十四、Release 最该移走的东西

如果某个大测试已经在 exact SHA 的 Verify 里证明过：

```text
92-case self-test
大量 source-level regression
重复 static validation
重复 unit test
```

那么 Release 不需要再完整证明一次。

Release 应该：

> **消费 Verify 的 evidence。**

否则你会得到：

```text
Verify 花 10 分钟证明一次
Release contract 再证明一次
Release 又证明一次
```

这不是更安全。

只是：

> **重复烧 runner 和墙钟。**

---

# 二十五、取消和失败必须区分

还有一个 Release 系统里非常容易被忽略的问题：

```text
旧 run 正在跑
↓
新 commit / 文档 push
↓
concurrency 把旧 run cancel
```

这时：

```text
cancelled
```

不能被逻辑当成：

```text
failed
```

然后自动重跑。

否则会形成：

```text
新提交
→ 取消旧 run
→ release 驱动器看到“不是 success”
→ 又重跑
→ 再被新 run 取消
```

所以：

> **failure 是验证失败，cancellation 是调度事件。**

必须分开。

这和网络运行时里：

> **自己的 teardown 不能被当成节点健康失败**

其实是同一种设计哲学。

---

# 二十六、Action / Release 最值得保留的总体设计原则

## 原则 1：exact SHA 是身份根

任何发布都不能靠：

```text
“最新 run”
```

必须是：

```text
这个 exact SHA 的正确 producer run
```

## 原则 2：producer 和 consumer 分开

Producer：

```text
负责产生并证明 artifact
```

Release：

```text
负责消费 artifact
```

不要让 Release 再偷偷 rebuild 一份“看起来一样”的东西。

## 原则 3：evidence 跟 artifact 一起走

包括：

- SHA256
- provenance
- BUILD-INFO
- source SHA
- workflow identity
- build metadata

## 原则 4：严格 allowlist 比“文件差不多齐了”更重要

Release tree 应该满足：

```text
应该有的，一个不少
不应该有的，一个不多
```

## 原则 5：dry-run 必须走真实 release staging

否则无法抓：

- artifact nesting
- path assumption
- extra file
- wrong name
- stale output
- wrong producer

## 原则 6：read-back 是最后闭环

发布成功以后还应该反查：

```text
Release 页面是否真的存在
资产是否齐
tag 是否对
SHA 是否对
draft / prerelease 是否符合预期
```

---

# 二十七、网络内核和 Release 工程其实在做同一件事

这两条线看起来完全不同。

一条是：

```text
DNS
FakeIP
route
dial
pool
MTU
lifecycle
```

另一条是：

```text
Actions
artifacts
checksum
provenance
allowlist
dry-run
release
```

但 v0.1.6 以后它们的设计哲学其实越来越一致。

## 网络侧

```text
旧 generation 没资格写新 cache
失败只是 evidence
diagnostics 不能消耗业务状态
Direct 必须先证明 eligibility
旧 session 可以 drain，但不能随便继续承载新流
```

## Release 侧

```text
旧 run 没资格发布新 SHA
失败和 cancel 必须区分
dry-run 不能改变 release identity
artifact 必须证明 provenance
Release 必须先证明 exact-SHA eligibility
旧 artifact 不能混进新 Release
```

所以从系统设计角度看：

> **你后期真正统一起来的，不只是网络生命周期，而是“所有状态都必须有身份、所有优化都必须有证据、所有复用都必须有资格、所有提交都必须属于当前世界”。**

这是整个项目后期最明显的架构风格。

---

# 二十八、最终总结

如果只看 v0.1.6 的“功能列表”，很容易低估这个版本。

它不是：

```text
新增 XHTTP
新增 PQ
优化 DNS
优化 sniff
加一些 TUN 测试
加几个 workflow
```

这么简单。

更准确的总结是：

> **v0.1.6 把一个拥有大量新能力的 fork，开始重新整理成一个有路径语义、有 ownership、有 network generation、有 evidence、有 lifecycle contract 的网络系统。**

而 v0.1.7 的意义，是把同样的思路延伸到 Release：

> **产物也必须有身份，workflow 也必须有证据，发布也必须有严格边界。**

从 v0.1.6 到 v0.1.10 的 Action / Release 优化也不是单纯：

> “把构建从 40 分钟优化到 24 分钟。”

而是：

> **在平台更多、证据更多、发布边界更严格的前提下，把总墙钟继续压低。**

所以这段版本历史最准确的主线可以写成：

```text
v0.1.5
已有大量能力和移动端生命周期基础

↓

v0.1.6
整条网络链从 mechanism-driven
转向 evidence-driven runtime
同时多平台 CI / Release 全面爆发

↓

v0.1.7
狠狠干 release contract / staging / dry-run / evidence

↓

v0.1.8
供应链闭环开始稳定

↓

v0.1.9
修正 Libbox release artifact correctness

↓

v0.1.10
网络语义、Windows 安全、CI 和知识库进一步收口
发布墙钟进入约 24 分钟成熟区间
```

最后一句：

> **0.1.6 是“把整条网络链犁了一遍”；0.1.7 是“把发布链也犁了一遍”；后面的版本则是在这两块大地基上不断收敛、提速和封口。**
