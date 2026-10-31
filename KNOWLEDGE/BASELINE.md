# 基线（Baseline）

## 当前校准基线——2026-10-31（Current calibrated baseline — 2026-10-31）

| 字段 | 取值与范围 |
|---|---|
| 仓库 / 分支 | `Piggy-Cat-bit-shadow/JiejieBox` / `testing`；先前的 `sing-box` 远端 URL 会重定向到本仓库 |
| 生产基线 | `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28` —— 最后审阅的源码/发布/CI 变更；审阅时更晚的提交改的是文档 |
| 发布基线 | [v0.1.34](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/releases/tag/v0.1.34)，发布于 2026-10-29 05:34:42 UTC，非 draft 且非 prerelease；附注标签（annotated tag）剥离后指向生产基线 |
| 上游祖先 | 观察到的 `upstream/testing` `e5d91fa79f6319d5de940d6c81d81f0fe53b9476` 是合并基；祖先关系不能证明作者身份 |
| 上一个生产基线 | `dbcfc1d359d6ed818dfaaefb6d4e637b6e785f07`；基线到目标区间内有 12 个可达提交，包含知识库/文档 |
| 知识修订 | `git log -1 --format=%H -- KNOWLEDGE/BASELINE.md` |
| 基线之后的增量 | `git diff --name-only 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28..HEAD` |

关于范围与继承下来的限制，见 [2026-10-31 校准](CALIBRATION-2026-10-31.md)、它的
[全面的第二轮审阅](CALIBRATION-2026-10-31-REVIEW.md)，以及更早的台账。运行证据是与 SHA 和
具体任务绑定的；源码审阅或 CI 并不证明真实设备行为。子仓库源码 pin 与独立观察到的分支头是不同的事实。

## 历史性的 v0.1.9 基线（Historical v0.1.9 baseline）

下面的表格与叙述保留了 v0.1.9 的审阅。它们不是当前基线；其取值与主张的范围限于
那个历史修订。

| 字段 | 取值 | 如何重新测量 |
|---|---|---|
| 仓库 | `Piggy-Cat-bit-shadow/sing-box` | `git remote -v` |
| 产品分支 | `testing`（唯一的长期源码分支） | `git branch --show-current` |
| **生产基线** | `5fc4f9366a60202fdc7c98cd6fa280cceace65ba` | `git rev-parse v0.1.9^{commit}`；这不一定是 HEAD |
| 发布基线 | 标签 **`v0.1.9`** = 同一个提交 | `git rev-parse v0.1.9^{commit}` |
| 发布日期 | 2026-10-14 23:02:14 +0800（“test(wireguard): the census poll must not starve the goroutine it waits for”） | `git log -1 --format=%ci v0.1.9` |
| 基线时的分支版本 | `release/JIEJIE_VERSION` = `0.1.9` | `git show v0.1.9:release/JIEJIE_VERSION` |
| 上游项目 | `https://github.com/SagerNet/sing-box.git`（远端 `upstream`） | `git remote -v` |
| **历史上游锚点** | `e4b7e692f4545890bfc9d43960328e1449fa803b` —— 供 v0.1.9 时期的审阅使用 | 历史快照；针对目标与当前上游引用重新测量 |
| **分支分叉点（fork branch point）** | `0d91c4547921a7b72e9c9913e2bc6f3a556801e2` | 见下文 |
| 知识审阅时间 | 2026-10-14（在 v0.1.9 发布时刷新） | 本文件的 Git 历史 |
| 知识修订 | 动态解析 —— `git log -1 --format=%H -- KNOWLEDGE/BASELINE.md` | 不要硬编码 |
| **基线之后的生产增量** | 很大；动态检查，见上方的校准警告 | `git diff --name-only v0.1.9..HEAD` |

## 本基线取代了什么，以及为何移动是被迫的（What this baseline RE placed, and why the move was forced）

上一个基线是 `a267e99e0`（= `v0.1.6`），它的增量行写着：

> **基线之后的生产增量**：**没有。** `v0.1.6..HEAD` 是 5 个提交，且**没有任何 `.go` 源码发生变化**。

**那一行后来变成假的，并且在两个 release 期间一直是假的。** 刷新时的实测：

```sh
git rev-list --count a267e99e0..5fc4f9366     # 181        (the row said 5)
git diff --shortstat  a267e99e0 5fc4f9366     # 141 files changed, 33110 insertions(+), 1816 deletions(-)
git diff --name-only  a267e99e0 5fc4f9366 -- '*.go' ':(exclude)*_test.go'   # 12 files  (the row said 0)
```

发生变更的 12 个生产文件：

```text
adapter/experimental.go              option/fast_path.go        route/fast_path.go
box.go                               option/outbound.go         route/route.go
cmd/internal/build_libbox/main.go    option/schema.go           route/router.go
experimental/boxdd/authenticode_windows.go
protocol/group/selector.go           protocol/group/urltest.go
```

这就是知识模型被设计来防止的失败模式，它被记录在这里
而不是被悄悄改正：**增量行是一条带有效期的声明，没有任何东西会自动复检它。** 在 `v0.1.7` 与
`v0.1.9` 之间相信那一行的读者，会得出“模块仍然描述着这棵树”的结论，并会在一个已发布特性
（`fast_path` 组策略）、根包（`box.go`）以及 Android/Apple 构建路径上判断错误。

刷新把基线移动到 `HEAD`，是因为变更的子系统已经对照当前源码审计过，
而不是因为时间流逝。`REFRESH.md` §39 列出了基线**不得**移动的条件；没有一条适用。

**这里“审计过”的含义要精确说明，因为这一区分正是重点。** 各模块已对照当前源码检查了
本次增量所改变的行为，其中两个被发现与之**矛盾** —— 两者都在同一次刷新中被修正：

* `modules/fast-path-group-policy.md` §6 声称 `canFastBypass` 资格门禁 *“unchanged
  by this field”*（中文：未被该字段改变），并且 *“none of the existing blocking conditions is bypassed.”*（中文：现有阻断条件都没有被绕过。）实际上不成立：
  `route/route.go` 增加了一个 `fastPathAuthorized` 参数，并且恰好放宽了一个阻断条件
  （`BypassRefusedOutboundChain`），用于已授权且全透明的链。没有那个分支，该特性就是惰性的，
  因为被路由进组的流总是有 `len(chain) >= 2`。
* `modules/data-path-optimization.md` §A.8 把出站链列为不在范围内。对**不透明**组（opaque group）
  来说仍然成立，这也是 `loadbalance` 仍然拒绝的原因——但对声明了 `FastPathTransparent() bool`
  的组来说不再成立。

`modules/core-lifecycle-runtime.md` §2 被发现**行锚点过期约 +47**，但其结构性主张仍然成立；
锚点被标记出来，而不是被统一平移，因为统一的偏移从未逐锚点验证过，而猜测的行号看起来会像权威结论。

**在 v0.1.9 审阅时，那个基线对所审计的子系统是最新的；它并不是“每个模块的每一行都重新推导过”的声明。**
增量未触及路径的模块没有被重读。
任何带有 `file:line` 的模块陈述，在依赖它之前都应重新测量，测量时按 `modules/README.md`
§How to decide whether a module is still accurate 使用
`git log --oneline v0.1.9..HEAD -- <path>`。

## 分支发布谱系（Fork release lineage）

以下数据是实测的，这样标签自身的对象就不会与被它们命名的提交混淆。**每个分支标签都是附注标签
（annotated）**，所以 `git rev-parse vX` 返回的是 *标签对象*，而 `git rev-parse vX^{commit}` 返回的是
提交。用错对象做 diff 的读者，得到的是针对标签对象的 diff，而那是另一回事。

| 标签 | 标签对象 | 提交（剥离后） | 日期 | 主题 |
|---|---|---|---|---|
| `v0.1.5` | `0b4725149` | `e70f3d2c8` | 2026-10-07 18:45:11 +0800 | release: correct version to v0.1.5 |
| `v0.1.6` | `a34dac049` | `a267e99e0` | 2026-10-12 11:53:53 +0800 | release: include Windows and Android artifacts |
| `v0.1.7` | `46361ea86` | `ccab3645d` | 2026-10-12 23:33:22 +0800 | fix(release): stage the Android AAR, its checksum and its record together |
| `v0.1.8` | `e4df57f03` | `455458c76` | 2026-10-14 21:12:08 +0800 | fix(release): resolve staged desktop installer path |
| `v0.1.9` | `58f99c630` | `5fc4f9366` | 2026-10-14 23:02:14 +0800 | test(wireguard): the census poll must not starve the goroutine it waits for |

**`v0.1.8` 是一个历史身份，绝不能被移动。** 它以 GitHub Release 的形式
发布时带有**六个**资产，而契约要求七个，所以它既是一个*已知缺陷*的发布，也是一个
被修复的发布。缺陷及其修复是 `v0.1.9`；无论如何该标签不可变。

## 两个基线不同，且两者都重要（The two baselines are different, and both matter）

```text
historical upstream anchor e4b7e692  used by the v0.1.9-era review
        │
        │  1,834 commits in the historical ancestor range to v0.1.6
        │  2,015 commits in the historical ancestor range to v0.1.9
        ▼
production baseline 5fc4f9366  tag v0.1.9 — last declared broad knowledge baseline
        │
        │  substantial production and documentation changes since that baseline
        ▼
HEAD                          current branch tip; re-measure, not necessarily a release
```

以下为实测，读者无需相信上面的示意图：

```sh
git merge-base v0.1.9 refs/remotes/upstream/testing   # remeasure: the ref may be rewritten
git rev-parse refs/remotes/upstream/testing           # remeasure the locally recorded ref
git rev-list --count e4b7e692..a267e99e0              # 1834  (the v0.1.6 baseline)
git rev-list --count e4b7e692..v0.1.9                 # 2015
git rev-list --count v0.1.9 --not --remotes=upstream  # 2014 at that review; unreachable ancestry, not an authorship count
git rev-list --count a267e99e0..v0.1.9                # 181   (the delta this refresh absorbed)
git diff --shortstat e4b7e692 v0.1.9                  # see the row above
```

> 关于最后一行：**不要**把 `.go` 过滤器写成 `... | grep -c '\.go$'`。`grep -c` 在没有匹配时
> 打印 `0` **并以退出码 1 退出**，所以用脚本复检的人会把一个正确结果读成
> 失败。使用 `--` 路径规范形式：
> `git diff --name-only <a> <b> -- '*.go' ':(exclude)*_test.go'`。

`e4b7e692..a267e99` 是一个**祖先到后代的区间**，不是分支区间：`e4b7e692` 是这个分支所吸收的
上游顶端，而分支基线是它的后代。该区间不是作者身份普查；上游重写和其他引用意味着仅凭可达性
无法确定每个补丁的来源。

## 来源检验——用它，绝不要用 `git describe`（The provenance test — use this, never use `git describe`）

### `<sha>` 是从上游继承的，还是分支侧的？（Is `<sha>` inherited from upstream, or is it fork-side?）

```sh
git rev-list --count <sha> --not --remotes=upstream
```

```text
0  ⇒ <sha> is reachable from an upstream ref
≥1 ⇒ at least one ancestor of <sha> is not reachable from the current upstream refs
```

这是一次祖先关系检验，不是作者身份分类器：被重写的上游孤儿提交可能无法从当前上游引用到达，
而一个不可达的祖先也不能确定所命名提交的来源。当来源很重要时，比较补丁、blob 和历史证据。

仔细读方向。`--remotes=origin` 是拿**本分支**做比较，不是上游；
对它的可达性无法确定上游来源。

### 本仓库已经付过代价的三个陷阱（The three traps this repository has already paid for）

1. **`git describe` 在这里会说谎。** 对于某个并不携带该标签的提交，它报告
   `v1.14.3-38-ge4b7e692f`，因为本分支的历史包含重写前的上游孤儿提交。永远不要用它做
   来源判断。
2. **上游历史会被重写。** `refs/remotes/upstream/testing` 从 `e4b7e692` 移动到了
   `dfb80349b`，而本分支并未吸收它。因此仅凭 SHA 祖先关系并不总是足够；当来源是承重信息时，
   使用不止一种检查。
3. **绝不要按 SHA 统计上游工作量。** 一次逻辑上的上游变更在重写后，在一个区间里最多可能出现十一次。

## 基线之后的增量阅读——预期工作流（Post-baseline incremental reading — the intended workflow）

```sh
# 1. What moved since the knowledge baseline?
git log --oneline v0.1.9..HEAD
git diff --name-only v0.1.9..HEAD

# 2. An empty/code-free delta does not prove old entries are correct.
#    Map changed paths and independently verify existing load-bearing claims.

# 3. Nothing in KNOWLEDGE/ needs editing for a normal development commit.
```

## 本文件的证据级别（Evidence level of this file）

`VERIFIED_GIT` —— 每一行都是在刷新时针对本工作树产生的命令输出。没有任何一行
依赖报告、文档或推断。唯一一行是**关于模块的声明**而非关于 Git 的声明，
即生产基线那一行；它的可信度仅取决于 `REFRESH.md` 刷新报告中记录的模块审计。

## 本基线的已知局限（Known limitations of this baseline）

* 客户端**源码**仓库（`clients/apple`、`clients/android`、`clients/desktop`）是
  独立仓库，在干净工作树中为空。它们的修订记录为 gitlink；
  在历史性的 v0.1.9 审阅中，其内容未被覆盖。当前子仓库的覆盖情况见 `modules/child-repositories.md`。
* `experimental/boxdd/**`（Windows 守护进程）在本次增量中发生了变化，由
  **`modules/windows-daemon-security.md`** 拥有。它曾是最后一个无主的变更子系统；所有权
  地图现在解析了它，`daemon/**`、构建驱动与打包被记录为 CROSS-MODULE。
  **它的信任边界没有文档化的端到端测试** —— 见该模块的 §15。现在声明了一个 Windows
  测试工作流；实际执行需要运行证据。
* 这里的历史 v0.1.9 条目在代码树中没有关联的 CI 运行记录。后来一次经独立
  检查的 Windows 守护进程运行连同其 SHA 记录在 `modules/windows-daemon-security.md` 中。
