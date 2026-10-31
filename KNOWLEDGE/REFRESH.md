# sing-box 分支知识刷新协议（sing-box Fork Knowledge Refresh Protocol）

> 仓库：`Piggy-Cat-bit-shadow/sing-box`
>
> 目的：定义一套**长期、可重复的流程**，用来刷新本仓库的 AI 知识库；既不把文档变成第二棵源码树，也不要求每次提交之后都持续维护。

---

# 1. 本文档是什么（What this document is）

本仓库采用**快照 + Git 增量**的知识模型。

知识库不要求永远描述最新的 `HEAD`。

而是：

```text
KNOWLEDGE
= verified project understanding up to a pinned Production Baseline

Current AI Context
= KNOWLEDGE
+ production-relevant Git delta after that baseline
```

因此，知识刷新**不是日常开发工作**。

它是一次显式的维护操作，按周期执行，例如：

- 在一次大版本发布之后，
- 在一次大规模子系统重写之后，
- 在较长的开发间隔之后，
- 在重大发布冻结之前，
- 在上游历史被重写或大幅重新变基（re-based）之后，
- 当 `baseline..HEAD` 增量积累过多、已无法高效做增量阅读时。

正常的功能/修复提交**不需要**更新 `KNOWLEDGE/`。

---

# 2. 事实来源层级（Source-of-truth hierarchy）

始终使用以下顺序：

```text
1. Current source code and executable tests
2. Git history / diffs / tags / branch topology
3. CI / build / artifact / interop / device evidence
4. KNOWLEDGE/
5. Historical reports / closure notes / old docs
```

如果 `KNOWLEDGE/` 与当前源码冲突：

> 以源码为准。

如果一份旧报告与当前源码冲突：

> 把该报告当作历史证据，而不是当前事实。

不要为了让文档与代码一致而悄悄重写历史。

应当改为记录：

```text
old claim
current reality
superseding commit
current evidence
```

---

# 3. 核心规则：把增量评审与校准结合起来（Core rule: combine delta review with calibration）

用增量确定深度评审的优先级，但不要假定未变的文档就是正确的。
盘点每一条现存知识，抽样核对它的源码锚点与跨模块结论，再归类为有效、需要更新、
需重写、已过时或证据不足。抽样一旦发现漂移，就扩大评审范围。
历史报告要明确限定在它原有的代码树范围内。

刷新流程是：

```text
read current knowledge baseline
↓
compute baseline..HEAD
↓
classify the delta
↓
map changed paths/commits to subsystems
↓
deep-read only affected subsystems
↓
calibrate existing claims and check coverage gaps even when the delta is empty
↓
update relevant module knowledge
↓
re-check cross-module invariants
↓
move the Production Baseline
```

目标是在保留高重建成本理解的同时，避免重复做全仓库考古。

---

# 4. 第一步：识别真正的生产基线（First step: identify the real Production Baseline）

先查明仓库根目录、worktree 状态、本地与远端分支、远端 head、标签与
发布记录。从仓库入口点确认当前生效的刷新协议；不要假定另一份检出、或
一个名为 `main` 的分支就是权威。保留已存在的改动。目标 SHA 与已发布的标签
要分开记录，并证明两者的祖先关系。如果网络或发布数据不可用，就标记为未知。
绝不要从版本文件推断发布状态。

阅读：

```text
KNOWLEDGE/BASELINE.md
```

提取当前的：

```text
Production Baseline
Release Baseline
Upstream Baseline
Knowledge reviewed date
```

不要把知识文档所在的提交当作生产基线。

这些是不同的概念：

## 生产基线（Production Baseline）

知识库完整描述的最新生产代码 SHA。

它是下列命令的参照点：

```bash
git log  --oneline <production-baseline>..HEAD
git diff --stat    <production-baseline>..HEAD
git diff --name-only <production-baseline>..HEAD
```

## 知识修订（Knowledge Revision）

恰好包含当前 Markdown 文件的提交。

仅含文档的提交**不会**移动生产基线。

不要跟随知识库自身的提交 SHA。

---

# 5. 阅读 diff 之前，先对增量分类（Before reading diffs, classify the delta）

先取得一个粗粒度的视图：

```bash
git log --oneline --decorate <baseline>..HEAD
git diff --stat <baseline>..HEAD
git diff --name-only <baseline>..HEAD
```

不要立刻阅读每一个 diff。

把变更的文件与提交划分为以下高层分组：

```text
PRODUCTION
TEST
CI
BUILD
RELEASE
PLATFORM
DOC
REPOSITORY-HYGIENE
VENDOR/SUBMODULE
```

再按子系统给生产变更分类。

仅含文档的增量通常**不**需要完整知识刷新。

仅涉及 CI 的增量可能只需要更新：

```text
ACCEPTANCE.md
modules/ci-release.md
possibly build-pruning-release.md
```

协议实现的增量不应连带要求重写无关的平台/发布知识。

---

# 6. 分支增量优先（Fork-delta first）

本知识库的作用是描述：

> 本分支相对上游改动了什么。

它**不是**上游 sing-box 文档的替代品。

刷新期间：

```text
fork-owned changes
fork-modified source
fork-added tests
fork-specific CI/build/release behavior
```

优先处理。

只有在回答下列问题之一时，才阅读上游：

```text
What did the fork change from?
What contract did it preserve?
Was this fix inherited or independently implemented?
Was an upstream fix absorbed?
Did upstream rewrite history or rename the equivalent implementation?
```

不要总结无关的上游包。

---

# 7. 上游历史在本仓库中有其特殊性（Upstream history is special in this repository）

`upstream/testing` 可能被强制重写。

因此：

> 仅凭 SHA 祖先关系，并不总能确定来源。

绝不要假定凡是当前无法从 `upstream/*` 到达的提交，都是 fork 创作的。

来源重要时，要多做几项检查。

推荐：

```bash
git rev-list --count <sha> --not --remotes=upstream
git show <sha>
git patch-id --stable
git diff <candidate-a> <candidate-b>
git hash-object <file>
```

谨慎解读。

使用以下来源类别：

```text
OWN
UPSTREAM-INHERITED
UPSTREAM-ABSORBED
EXTERNAL-ADAPTED
CORE-CLIENT-COORDINATED
SUPERSEDED
ARCHIVE-ONLY
UNKNOWN
```

定义：

## 自有（OWN）

由 fork 创作、仍然存活的变更。

## 上游继承（UPSTREAM-INHERITED）

当前存在的行为，只是因为 fork 保留了上游行为。

不要逐一列出继承来的上游提交。只有当继承本身对未来的决策重要时，才记录它。

## 上游吸收（UPSTREAM-ABSORBED）

上游的某个修复或行为被有意引入 fork，可能换了 SHA，也可能以重写的实现出现。

## 外部改编（EXTERNAL-ADAPTED）

fork 实质上派生自另一个实现或项目，之后作了改编。

只在有真实来源证据时使用。绝不要仅凭代码相似就推断。

## 核心-客户端协同（CORE-CLIENT-COORDINATED）

它的正确性依赖对应的 Apple / Android / libbox 客户端契约变更。

## 被取代（SUPERSEDED）

一个历史 fork 提交，它的行为已不是当前行为。

只有它能解释以下内容时才保留：

- 为什么最终设计看起来不寻常，
- 为什么某个诱人的做法是错的，
- 为什么回归很容易被重新引入。

## 仅归档（ARCHIVE-ONLY）

有意排除在当前行为之外的历史证据。

---

# 8. 提交分类法（Commit classification taxonomy）

每一个重要的存活变更都应按语义归类。

可以使用一个或多个：

```text
FEATURE
FIX
HARDENING
PERF
MEMORY
ZERO-COPY
COMPAT
LIFECYCLE
ROUTING
DNS
SNIFF
CHAIN-PROXY
MASQUE
NAIVE
PLATFORM
LIBBOX
BUILD
PRUNING
TEST
INTEROP
CI
RELEASE
DOC
REPOSITORY-HYGIENE
```

不要强行把提交归入单一类别。

示例：

```text
Commit: abc123
Subsystems:
- MASQUE
- buffer ownership

Types:
- FIX
- HARDENING
- ZERO-COPY

Provenance:
- OWN
```

---

# 9. 不要以扁平列表的方式来记录提交（Do not document commits as a flat list）

提交索引只是导航。

真正的知识必须按**演进主题**组织。

错误示例：

```text
abc123 -> FIX
def456 -> FIX
ghi789 -> TEST
```

正确示例：

```md
## Upload scheduler backpressure

Initial problem:
...

Evolution:
- abc123 introduced ...
- def456 corrected ...
- ghi789 added the regression guard ...

Superseded approach:
- xyz999 ...

Final behavior:
...

Why this design:
...

Evidence:
...

AI caution:
...
```

真正要记录的是**最终设计主题**，而不是单个提交。

---

# 10. 子系统地图（Subsystem map）

每次刷新都不要假定当前的模块地图永久正确。

从既有地图出发，但要重新评估：变更的代码应归入既有子系统，还是需要拆分/合并。

当前主要知识域可能包括：

```text
Core lifecycle / runtime coordination
Path selection / physical-path validation
Routing
DNS / consumer-driven demand
Chain proxy / detour
Sniff
MASQUE / CONNECT-IP
Naive / HTTP transport
Network epoch / reuse
Data-path optimization
Traffic scheduling / classes
Buffer ownership / zero-copy / splice
MTU / buffer geometry
Power / mobile lifecycle
libbox / platform contract
Build tags / pruning
Apple / Android build linkage
CI / release / artifact provenance
Interop / adversarial verification
```

这些是**所有权域**，不是强制的文件名。

---

# 11. 何时拆分模块（When to split a module）

以下条件中至少有两项为真时，就拆分模块：

```text
different invariants
different commit evolution
different acceptance gates
different source ownership
different AI cautions
different likely future tasks
```

示例：

如果将来一个修 `sniff` 的 Agent 必须加载大量无关的 MASQUE 与 DNS 知识，这个模块就太宽了。

---

# 12. 何时合并模块（When to merge modules）

出现下列情况时，合并两个模块：

```text
they always change together
they share the same ownership
they share the same invariants
they share the same verification path
their separation causes duplicated explanations
```

不要为了让目录看起来有条理就拆分。

---

# 13. 每个重要模块的必需结构（Required structure for every important module）

每个被维护的子系统文件只应包含真正增加价值的章节，但推荐的结构是：

```md
# <Subsystem>

## Scope

## Current design

## Why it is designed this way

## Relationship to upstream

### Inherited unchanged
### Modified
### Extended
### Upstream fixes absorbed
### Intentionally not modified

## Provenance

### Important commits
### Superseded attempts
### External/client coordination

## Hard invariants

## Current conventions

## Important evolution

## Verification

## Known limitations

## AI cautions

## Follow-up
```

不是每个模块都需要全部标题。

不要添加空的样板章节。

---

# 14. 硬性不变量必须在每次刷新时重新审计（Hard Invariants must be re-audited on every refresh）

先前的不变量不会自动永久有效。

对每一条现存不变量逐条提问：

```text
Is this still true?
Why must it be true?
What fails if it is violated?
What evidence supports it?
Has a newer implementation made it obsolete?
Is it actually only a convention?
```

只有当某条规则得到以下一项或多项支持时，才保留为**硬性不变量（Hard Invariant）**：

```text
protocol correctness
memory / ownership safety
ABI contract
platform hard constraint
verified concurrency/lifecycle bug
release artifact integrity
explicit product ownership
repeatable test/interop evidence
```

否则降级为：

```text
Current Convention
Optimization Strategy
Design Preference
Implementation Detail
```

不要无意间把当前实现冻结下来。

---

# 15. 硬性不变量与当前约定（Hard Invariant vs Current Convention）

严格区分这两者。

## 硬性不变量（Hard Invariant）

除非架构本身有意改变，否则未来代码必须遵守的规则。

示例：

```text
A buffer cannot be retained after ownership transfer.
A snapshot must not be interpreted as a transition edge.
A route validation pass must not dial a real outbound.
A released artifact must correspond to the verified binary.
```

## 当前约定（Current Convention）

一种当前成熟的设计，日后可以被更好的实现替换。

示例：

```text
Current scheduler data structure
Current cache organization
Current helper naming
Current build-script layout
```

---

# 16. 证据分级（Evidence classification）

每一项重要结论都应标注精确的证据级别。

允许的级别：

```text
VERIFIED_SOURCE
VERIFIED_GIT
VERIFIED_UNIT
VERIFIED_INTEGRATION
VERIFIED_INTEROP
VERIFIED_CI
VERIFIED_ARTIFACT
VERIFIED_DEVICE
INFERRED
UNVERIFIED
UNKNOWN
BLOCKED_BY_ENVIRONMENT
```

绝不要提升证据级别。

禁止的示例：

```text
unit test -> interop proof
CI declaration -> CI execution proof
build success -> runtime proof
static audit -> device behavior
one platform -> all platforms
SKIP -> PASS
```

---

# 17. 验证条目应当可执行（Verification entries should be executable）

重要的验收规则要记录：

```text
Requirement
Subsystem
Why
Evidence level
Current status
Relevant source/test
How to re-check
Required environment
```

优先写命令，不要写散文。

示例：

```text
Requirement:
Start-time physical-path validation performs no dial side effect.

Evidence:
VERIFIED_UNIT

Re-check:
go test ./common/physicalpath/... -run <test>
```

如果命令无法在当前环境中运行：

```text
BLOCKED_BY_ENVIRONMENT
```

不要标记为 PASS。

---

# 18. 嗅探刷新规则（Sniff refresh rules）

嗅探相关文件变更时，检查增量是否影响以下方面：

```text
peek/read behavior
buffer ownership
allocation
partial payload handling
timeout behavior
TCP/UDP semantics
TLS detection
HTTP detection
QUIC detection
fragmentation
metadata propagation
routing contract
chain-proxy interaction
false positive / false negative behavior
```

谨慎地给变更分类：

```text
PERF
FIX
HARDENING
BEHAVIOR CHANGE
TEST ONLY
```

除非行为确实改变，否则不要把分配次数减少描述成协议语义变更。

---

# 19. 链式代理 / 路径选择刷新规则（Chain proxy / path-selection refresh rules）

路由 / selector / detour / 链式逻辑变更时，要显式评估：

```text
ControlPath
PhysicalPath
group selection
detour
failover
cycle detection
start-time validation
runtime selection
DNS lookup side effects
dial side effects
state mutation
```

最重要的问题是：

> 校验只检视了拓扑，还是意外执行了选择/拨号/解析？

行为改变时，更新：

```text
path-selection module
routing module
DNS module if relevant
ACCEPTANCE
INVARIANTS
COMMIT-INDEX
```

---

# 20. DNS / 路由刷新规则（DNS / routing refresh rules）

检查变更是否影响：

```text
consumer demand
DNS transport activation
cache behavior
FakeIP
rule-set demand
route lookup
resolver detour
network epoch
stale state
network switch
leak prevention
```

如果新行为明确是横切的（cross-cutting），就不要把 DNS 与路由分开记录。

但也不要因为一个提交同时触及两者，就把它们合并成一个模块。

---

# 21. MASQUE 刷新规则（MASQUE refresh rules）

MASQUE 是 fork 的高价值领域。

任何 MASQUE 相关的刷新都应检视：

```text
CONNECT-IP
CONNECT-UDP
HTTP/2
HTTP/3
DATAGRAM
capsules
fallback
session lifetime
stream lifetime
buffer ownership
batching
PREF64
PTB
MTU
DNS role
error handling
cleanup
interop
```

始终区分：

```text
upstream inherited base
fork-added feature
fork fix
fork hardening
interop evidence
```

如果某个变更只改了一条传输路径，就不要声称所有 MASQUE 路径都变了。

---

# 22. Naive 刷新规则（Naive refresh rules）

Naive / HTTP 传输变更时，检视：

```text
client
server
CONNECT behavior
auth
headers
TLS
transport reuse
shutdown
error mapping
dependency compatibility
interop
```

不要仅凭依赖变更就推断“原生 Naive 对等性（Native Naive parity）”。

---

# 23. 缓冲区所有权 / 零拷贝刷新规则（Buffer ownership / zero-copy refresh rules）

这个领域要格外谨慎地记录，因为未来的“优化”可能悄悄破坏正确性。

对变更的热路径回答：

```text
Who owns the buffer?
When is ownership transferred?
When can it be reused?
When must it be copied?
Can the callee retain it?
Can it return to a pool?
Can a later async path still reference it?
```

记录：

```text
ownership invariant
copy boundary
pool/release rule
relevant tests
known unsafe tempting optimization
```

不要只写：

```text
"zero-copy optimized"
```

---

# 24. 网络 epoch / 复用刷新规则（Network epoch / reuse refresh rules）

如果网络切换、DNS 失效、路由失效、连接复用、接口切换或资源退役有变更，就要确定：

```text
who produces the epoch
who consumes it
whether the epoch is global or partial
what becomes stale
when stale resources are retired
whether rebuild is eager or lazy
what happens to live connections
```

没有证明时，绝不要把某个子系统内部的代次计数器泛化成全 fork 的不变量。

---

# 25. 移动生命周期 / 平台契约刷新规则（Mobile lifecycle / platform contract refresh rules）

核心/客户端生命周期 API 变更时，检视与以下对象的契约：

```text
sing-box-for-apple
satelite-one
```

仅在必要时。

区分：

```text
raw platform fact
level
edge
snapshot
event
core policy
client policy
```

首选的所有权模型应当经过验证，不能假定：

```text
client reports platform facts
core interprets policy
```

API 变更时，更新：

```text
libbox-platform-contract
power/mobile lifecycle
relevant acceptance rows
client coordination provenance
```

子仓库的变更属于核心知识范围：源码级评审与所有权按 §25A 执行。知识保持简洁，不要复制整个客户端仓库。

---

# 25A. 子仓库属于内核知识范围（Child repositories are part of the kernel knowledge scope）

内核知识库也覆盖内核使用或随产品发布的关联仓库，包括客户端 gitlink、嵌套 gitlink、
fork 的 Go 模块和受控原生组件。每次刷新时，应从 `.gitmodules`、`go.mod`、`test/go.mod`
以及现有发布清单和锁文件发现依赖，不要依赖固定仓库列表。

分别核对并记录：

1. 父仓库基线和目标版本中的 gitlink 或 replace SHA，以及对应子仓库的固定版本。
2. 子仓库当前产品分支的名称和分支头，确认它是否与固定版本一致。子仓库可能在父仓库没有
   新提交时独立更新；尚未被父仓库引用的改动要单独标注，不能写成已发布的内核行为。
3. 比较子仓库的新旧源码与测试，不只看提交标题或历史报告。检查嵌套依赖、ABI 和客户端
   契约、构建输入、生命周期及发布证据。源码对象无法获取，或平台、符号链接影响检查时，
   要明确记录；子仓库变化重大时应扩大审阅范围。
4. 更新受影响的模块、索引、模块表、不变量和验收记录。保留有价值的设计原因、故障模式和
   复查入口，但不复制整个子仓库，也不把 gitlink 本身当作源码已经审阅的证据。
5. 提交前重新核对固定版本和分支头。知识刷新不应改动客户端工作树、gitlink、子仓库分支、
   Go pin、工作流或发布配置；可以安全抓取 Git 对象或读取远端源码。重要且必需的子仓库
   版本无法检查时，应标记覆盖不完整，保留原生产基线。

父仓库基线只说明它固定使用了哪些版本，不代表子仓库各分支的最新状态。应另行记录检查的
子仓库分支头和时间，并区分静态检查、单元测试、CI、构建与真实设备测试的证据范围。

---

# 26. 构建 / 裁剪刷新规则（Build / pruning refresh rules）

构建标签、协议注册、生成器或发布 profile 变更时，确定：

```text
which binaries/platforms are affected
which protocols are included/excluded
whether config validation changes
whether dependency closure changes
whether binary size or memory is the goal
whether a client/server build profile differs
```

没有证据时，绝不要把临时的发布 profile 变成永久不变量。

---

# 27. Apple / Android 构建链接刷新规则（Apple / Android build-linkage refresh rules）

任一客户端 pin 或 libbox 构建契约变更时，验证：

```text
core SHA
client SHA
ABI/API compatibility
build scripts
provenance checks
artifact content
```

单个元数据值不能作为二进制来源证明。

优先使用：

```text
binary inspection
artifact hash
embedded build info
ABI gate
```

前提是仓库已经提供。

---

# 28. CI / 发布刷新规则（CI / release refresh rules）

工作流或发布代码变更时，分别归类：

```text
workflow declared
workflow actually ran
artifact produced
artifact verified
artifact released
```

不要把它们混为一谈。

工作流触发器变更时，记录：

```text
branches
tags
paths filter
manual dispatch
automatic gate scope
```

如果仅文档的 push 触发了昂贵的验证，那属于工作流的属性，不是生产代码的事实。

---

# 29. 历史文档（Historical documents）

历史报告在以下方面有用：

```text
why a fix exists
failed approaches
device evidence
interop evidence
branch consolidation
release incidents
```

但把它们当作当前事实就有危险。

每次刷新都要给重要文档分类：

```text
KEEP
ABSORB
ARCHIVE
DELETE
```

## 保留（KEEP）

仍在运维上有用，且当前有效。

## 吸收（ABSORB）

包含应当归入 `KNOWLEDGE/` 的持久知识。

## 归档（ARCHIVE）

有用的历史证据，但不是当前事实。

## 删除（DELETE）

重复、过期，或已被完全取代、不再有任何证据价值的。

拿不准时：

```text
ARCHIVE
```

---

# 30. AI 注意事项（AI cautions）

每个子系统只应保留高价值的注意事项。

一条有用的 AI 注意事项，描述的是这样一类变更：

```text
looks reasonable locally
but violates a non-local design or proven constraint
```

示例：

```text
Do not preview a route by actually selecting/dialing it.
Do not merge snapshot and event semantics.
Do not retain a transferred pooled buffer.
Do not assume a metadata pin proves binary provenance.
Do not rebuild all stale resources merely because an epoch changed.
Do not duplicate policy interpretation in mobile clients.
```

避免下面这种泛泛的警告：

```text
"be careful"
"run tests"
```

---

# 31. 跨文件一致性检查（Cross-file consistency pass）

移动基线之前，比较：

```text
AGENTS.md
KNOWLEDGE/README.md
KNOWLEDGE/BASELINE.md
KNOWLEDGE/PROJECT.md
KNOWLEDGE/ARCHITECTURE.md
KNOWLEDGE/INVARIANTS.md
KNOWLEDGE/ACCEPTANCE.md
KNOWLEDGE/COMMIT-INDEX.md
KNOWLEDGE/modules/*
README.md
important historical reports
```

查找：

```text
same SHA described differently
same subsystem with conflicting ownership
same claim with different evidence status
fixed issue still listed as active
superseded commit described as current
upstream inherited behavior mislabeled OWN
fork feature mislabeled upstream
release baseline confused with knowledge revision
```

结束之前要解决它们。

检查本地 Markdown 链接与源码路径/符号时，用符号搜索，不要假定历史行号仍然适用。
把 TUN/网络变更、路由/DNS/快速路径权威、双栈拨号与回退、协议/TLS/QUIC 生命周期、
平台回调以及 fork 依赖 pin 放在一起评审。盘点真正注册的协议、平台构建门禁与测试，
不要假定本协议中的列表已经穷尽。每个关键结论都要记录范围、源码锚点与证据级别；
区分源码、实际运行过的测试、CI 运行、互操作、
设备与测量。没有匹配的基准测试时，
不要声称量化性能结果。

如果被中断，保留一份简明的台账，记录已评审条目、目标 SHA、未解决的矛盾和
剩余检查。核对 HEAD 与 worktree 之后，从台账恢复；绝不要让基线越过
未解决的关键缺口继续前移。作为知识刷新的一部分，不要修改运行时代码、依赖、构建或
发布配置。不要递归地刷新本协议。

---

# 32. 更新 COMMIT-INDEX.md（Updating COMMIT-INDEX.md）

`COMMIT-INDEX.md` 是一个导航层。

只加入有助于解释当前产品的提交。

提交属于以下情况时，就应放入其中：

```text
the origin of a surviving feature
a key bug fix
a hardening step
the final replacement of a superseded approach
the test/interop gate that protects a major invariant
a release/build change with lasting consequence
```

不要索引：

```text
every docs commit
every typo
every intermediate retry
every merge with no unique semantic effect
```

---

# 33. 更新 BASELINE.md（Updating BASELINE.md）

所有已变更的子系统都核对完之后，再更新：

```text
Production Baseline
Release Baseline if relevant
Upstream Baseline if relevant
Knowledge reviewed date
Post-baseline production delta
```

不要把生产基线设成仅含文档的提交。

新的生产基线应当是：

> 刷新后的知识所完整描述的最新生产实现 SHA。

---

# 34. 更新 README.md / AGENTS.md（Updating README.md / AGENTS.md）

只有刷新流程或模块地图改变时，才修改这些文件。

不要仅因为基线 SHA 变了就更新它们，除非文件里重复了那个 SHA。

优先用动态引用：

```text
Read KNOWLEDGE/BASELINE.md
```

不要在多个文件中硬编码基线值。

重复的 SHA 越少，将来过期就越少。

---

# 35. 知识刷新报告（Knowledge refresh report）

每次显式刷新都应产出一份简明报告：

```text
KNOWLEDGE_REFRESH_<DATE_OR_RELEASE>.md
```

或者按仓库选定的刷新报告约定更新。

它应包含：

```text
Old Production Baseline
New Production Baseline
Release/tag context
Upstream baseline change
Commits coarse-scanned
Commits deep-read
Subsystems changed
Modules updated
Invariants added/removed/downgraded
Acceptance rows changed
Important provenance changes
Historical docs disposition
Unverified/blocked findings
Production code changed by refresh? NO
Git history rewritten? NO
```

不要给每个子系统各创建一份报告。

---

# 36. 使用 Harness / 多 Agent 系统的推荐刷新工作流（Recommended refresh workflow with Harness / multi-Agent systems）

对大增量，在当前生效的执行指令允许时，可以使用并行 agent；
否则按顺序执行同样的独立评审。

## 阶段 1——粗粒度地图（Phase 1 — coarse map）

主控 Agent：

```text
read baseline
compute delta
classify paths
cluster commits
identify affected subsystems
```

## 阶段 2——子系统 agent（Phase 2 — subsystem agents）

每个受影响的子系统配一个 agent。

每个 agent 获得：

```text
old module knowledge
changed commits
changed source
relevant tests
necessary upstream context
```

每个 agent 返回：

```text
CONFIRMED
CHANGED
REMOVED
NEW
SUPERSEDED
UNVERIFIED
```

## 阶段 3——来源 agent（Phase 3 — provenance agent）

独立审计：

```text
OWN
UPSTREAM-INHERITED
UPSTREAM-ABSORBED
EXTERNAL-ADAPTED
```

尤其在上游历史被重写之后。

## 阶段 4——验证 agent（Phase 4 — verification agent）

独立检查：

```text
tests
CI
interop
artifact
device evidence
```

## 阶段 5——主控收敛（Phase 5 — master convergence）

主控：

```text
deduplicates
resolves contradictions
re-reads disputed source
updates knowledge
runs cross-file consistency
moves baseline
```

不要直接拼接子 agent 的报告。

---

# 37. 小增量刷新工作流（Small-delta refresh workflow）

如果只有少数提交改动了一个子系统：

```text
1. Read BASELINE
2. Inspect baseline..HEAD
3. Read the affected module
4. Read changed source + relevant diffs
5. Run/review relevant tests
6. Update that module
7. Update ACCEPTANCE / COMMIT-INDEX only if needed
8. Check INVARIANTS
9. Move Production Baseline
10. Update reviewed date
```

不要为微不足道的变更启动完整的多 agent 审计。

---

# 38. 大增量刷新工作流（Large-delta refresh workflow）

出现下列情况时，使用完整且经独立核查的刷新：

```text
> several hundred relevant commits
multiple subsystem rewrites
release architecture changed
upstream history rewritten
platform ABI changed
major protocol feature added
knowledge baseline is months old
```

大规模刷新中：

```text
coarse scan all relevant commits
deep-read only semantic clusters
do not deep-read every commit
```

---

# 39. 何时不要移动基线（When NOT to move the baseline）

出现下列情况时，不要移动生产基线：

```text
important changed subsystem remains UNVERIFIED
knowledge still contradicts current source
commit provenance remains unresolved for a load-bearing change
a major module is knowingly missing
cross-file consistency fails
```

这种情况下：

```text
REFRESH = INCOMPLETE
```

保留旧基线。

---

# 40. 最终刷新验收清单（Final refresh acceptance checklist）

只有当新的 AI 能做到以下各项时，刷新才算完成：

```text
1. Read AGENTS.md
2. Read KNOWLEDGE/README.md
3. Identify the Production Baseline
4. Inspect baseline..HEAD
5. Map changed paths to modules
6. Avoid reading unrelated modules
7. Distinguish fork behavior from upstream
8. Trace important behavior to commits
9. See why the design exists
10. Re-run the relevant acceptance checks
11. Recognize superseded approaches
12. Avoid known reasonable-but-wrong changes
```

如果仍有重要子系统需要全仓库考古：

> 刷新尚未完成。

---

# 41. 最终状态块（Final status block）

每份刷新报告都应以以下内容结尾：

```text
KNOWLEDGE_REFRESH = COMPLETE / INCOMPLETE

OLD_PRODUCTION_BASELINE = <sha>
NEW_PRODUCTION_BASELINE = <sha>

PRODUCTION_CODE_CHANGED_BY_REFRESH = NO
RUNTIME_BEHAVIOR_CHANGED_BY_REFRESH = NO
PROTOCOL_BEHAVIOR_CHANGED_BY_REFRESH = NO
ABI_CHANGED_BY_REFRESH = NO
GIT_HISTORY_REWRITTEN = NO

DELTA_CLASSIFIED = YES / NO
AFFECTED_SUBSYSTEMS_REAUDITED = YES / NO
COMMIT_PROVENANCE_UPDATED = YES / NO
INVARIANTS_REAUDITED = YES / NO
ACCEPTANCE_UPDATED = YES / NO
HISTORICAL_DOCS_RECONCILED = YES / NO
CROSS_FILE_CONSISTENCY = PASS / FAIL
FUTURE_AI_INCREMENTAL_READ = PASS / FAIL
```

---

# 42. 需要保留的原则（The principle to preserve）

知识系统在以下条件下才算成功：

```text
source stays authoritative
Git stays authoritative for change history
knowledge stays compact
knowledge is allowed to become stale
staleness is explicit
refreshes are periodic
future AI reads only the necessary delta
```

不要把 `KNOWLEDGE/` 变成代码库的持续同步影子副本。

本系统的目的是：

> **保留那些重建成本高昂的上下文，同时把实现事实留在源码中、把变更事实留在 Git 中。**
