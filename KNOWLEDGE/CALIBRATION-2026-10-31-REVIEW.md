# 知识库全面审查 — 2026-10-31（第二遍）

## 范围与目标

这是与 [`CALIBRATION-2026-10-31.md`](CALIBRATION-2026-10-31.md) 同日的第二次全量盘点。它**不**移动
生产基线：源码树与第一遍相比没有变化。

| 字段 | 值 |
|---|---|
| 仓库 / 分支 | `Piggy-Cat-bit-shadow/JiejieBox` / `testing` |
| 生产基线 | `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`（未变） |
| 发布基线 | `v0.1.34`，注解 tag 对象 `e2dad0bf040`，peel 到 `0067b03` |
| 前一个发布 | `v0.1.33` = `a0bbf666d`，是 `0067b03` 的祖先 |
| 审查 HEAD | `f434b4620a0913cb433b1e88baaf8169dac5e96d` |
| `0067b03..HEAD` | 9 个提交、16 个文件、+3,353/−867；只有 `CHANGELOG.md`、`README.md`、`docs/**`、`KNOWLEDGE/**`——没有 `.go`、CI、构建、依赖、pin 或发布配置变更 |
| 工作树 | 除既有的客户端 gitlink 修改（`clients/android`、`clients/apple`）外干净；未触碰、未暂存 |
| 工具链 | 审查主机没有 Go 工具链；测试**未执行** |

两个发布 tag 在此 clone 中原本缺失。两者都作为不可变对象拉取
（`git fetch --no-tags origin refs/tags/v0.1.34:...`，`v0.1.33` 同理），并核实了 peel 后的提交；
没有移动分支、gitlink 或工作树。

## 审查了什么

9 个提交的增量只有文档，因此协议中的增量审查本身无法发现缺陷。`REFRESH.md` §3 因而适用：
盘点每个现有条目，抽样锚点与跨模块声明，并在抽样发现漂移时扩大范围。

* 对全部 117 个 `KNOWLEDGE/**/*.md` 做机械扫描：相对链接与锚点、反引号路径/`file:line` 锚点、
  Go 符号、提交 SHA，以及声明的文件/行/提交计数。
* 六路独立、只读的源码审计：core/routing、DNS/sniff、transports、data-path/memory、
  platform/build/CI，以及根文档。每个代理都被明确告知：源码和测试高于模块文档，`reports/**`
  是历史，且不得编辑任何文件。
* 对 `AGENTS.md`、`README.md`、`BASELINE.md`、`PROJECT.md`、`ARCHITECTURE.md`、
  `INVARIANTS.md`、`ACCEPTANCE.md`、`COMMIT-INDEX.md`、`INDEX.md`、`modules/README.md`
  做跨文件一致性检查。

## 已做的修正

**新增覆盖**

* `modules/dns-demand.md`：完整的 **QNAME 级 NXDOMAIN 负缓存**章节——可扩宽条件、键命名空间、
  剩余 TTL 重建、容量、`disable_cache`/`disable_expire`、trim/purge、精确缓存优先顺序、
  与 `adblock` 的关系、测试与 cautions。
* `INVARIANTS.md` A21（NXDOMAIN 扩宽）、`ACCEPTANCE.md` 4.6/4.7，以及 `INDEX.md` 中该缓存的
  任务/关键词/源码路径路由。
* `modules/naive-http-transport.md`：fork 的客户端引擎控制（`insecure_concurrency`、
  `_single_engine`、QUIC congestion/receive-window）。
* `modules/xhttp-client-transport.md`：XMUX 失败熔断器，以及 packet-up 首对请求不 gather 的例外
  （旧文对 gather 的描述过强）。
* `modules/masque-connectip.md` / `modules/core-lifecycle-runtime.md` / `modules/README.md`：
  endpoint-local host-port 注册与共享 `adapter.FlowLogger` 的归属边界。
* `modules/build-pruning-release.md` / `modules/ci-release.md`：当前 19 个工作流的清单
  （五个未编目的 Linux/Apple 工作流现在被明确列为需直接检查的来源）。
* `modules/libbox-platform-contract.md`：`experimental/libbox/internal/**` 构建输入子树。
* `modules/data-path-optimization.md`：`BlockerLocalFamilyUnavailable` 作为主机属性阻断项，
  与配置的地址族策略区分。

**错误声明与计数**

* `experimental/boxdd/**`：52 → **55 个 `.go`（51 非测试）+ 1 个 `.proto`**；签名器失败路径
  7 → **6**；command/service/update 行锚点已修正。
* `constant/goos/zgoos_*.go`：20 → **16**；`//go:generate` 指令：5 → **3 条真实指令**
  （第四个命中是引用性文字）；Cronet 原生模块：30 → **29**。
* `transport/http`：90 文件/137 提交（v0.1.6）→ 基线处 **97 个 `.go` / 149 提交**，旧值保留为
  历史 census。
* `common/physicalpath`：历史 35 提交 / 8,543 行 / 16 测试 → 保留为 v0.1.9 census，并补充当前
  **37 提交 / 9,407 行 / 21 文件（17 测试）**。
* `common/trafficsched`：30 → **31 提交**；`common/routeflow/reach.go`：340 → **352 行**；
  `dns/router.go`：~2,628 → **2,753 行**，`Start` `:237` → **`:252`**。
* `experimental/libbox`：`~85` → **110 个 `.go`（90 非测试）**。
* `clients/desktop`：`SagerNet/sing-box-for-desktop`（上游）→ **fork 的
  `Piggy-Cat-bit-shadow/sing-box-for-desktop`**。
* `ci-release.md` 的 Apple ABI pin：`3aedd741…` → **`044e6d5…`**，与 gitlink 和
  `check-libbox-abi.sh` 一致。
* `check-macos-client-config.sh` 曾被记录为 **invoked**；实际上只是注释
  （`client-macos.yml:46`），仍未接线。
* `verify-evidence/`：14 份日志 → **13 份**；“落后 101 个提交”改为相对 `a267e99` 表述，
  并给出到当前基线的距离 **1,214**。101 只对旧的 v0.1.6 比较成立。
* `include/tz_ios.go` 并非“始终编译”：它没有显式 build tag，但 `_ios.go` 文件名把它限制在 iOS。
* `COMMIT-INDEX.md` 中 `common/physicalpath` 测试文件数：6 → **5**；`PROJECT.md` 的 census 行
  现在**恰好划分 1,834** 个提交，process/evidence 占比为 43.7%，不是 44.7%。
* `modules/README.md`：boxdd 52 → 55；“build tags + CI gates/release artifacts”路由拆到
  `build-pruning-release.md` 与 `ci-release.md`。
* `INDEX.md`：SIGPIPE/pipefail、Windows 运行时与发布身份路由现在指向真正拥有该主题的文件
  （第一个只指向历史报告）。
* `PROJECT.md` 的 “current release identity”：过时的 `cb71cf80` / `0.1.31` 替换为基线
  `0067b03` / `0.1.34` 及 v0.1.34 发布事实。

**刷新过时锚点（保留符号名作为稳定定位器）**

* `dns-demand.md` §1/§2/§3/§6：整张 generation、reverse-mapping、cache-key、commit、FakeIP 与
  `systemd-resolved` 表；已移除的 `reverseMappingGenerationCurrent` 不再被当作现状引用。
* `path-selection-validation.md` §2/§3/§4/§5/§7：manager start、dry-run 装置、group/failover 表、
  seam 表，以及 `route-aware` 测试的表述（旧的“注释与自身断言矛盾”缺陷已不存在）。
* `core-lifecycle-runtime.md`、`data-path-optimization.md`、`buffer-ownership.md`、
  `mtu-and-geometry.md`、`power-mobile-lifecycle.md`、`sniff.md`、`masque-connectip.md`、
  `naive-http-transport.md`、`libbox-platform-contract.md`、`build-pruning-release.md`、
  `ci-release.md`、`windows-daemon-security.md`。
* `mtu-and-geometry.md` 的 PTB 路径：错误文件（`protocol/masque/client.go`）→ 真实的
  `transport/masque/client.go:704-715`。
* `dns-adblock-action.md` §7：存储键现在写明 `nxdomainCacheKey`（canonical name + QCLASS +
  transport + subnet + environment），并补充缓存生命周期行。

## 未决发现与限制

1. **`rewrite_ttl` 与零 SOA header TTL（UNVERIFIED，源码矛盾）。**
   `applyResponseOptions` 在 `commitCacheState` 调用 `storeNXDomain` 之前规范化响应 TTL，因此
   `extractNegativeTTL` 重新读取的是 `min(rewritten header TTL, SOA MINIMUM)`。当 header TTL 为
   `0`、MINIMUM `> 0` 时，正的 rewrite 看起来可以把结论提升。现有
   `TestNXDomain_ZeroSOATTLIsNotPromotedByNameWide` 使用 `min(0,0)` 且未设置 `RewriteTTL`，
   没有覆盖该形状。已记录在 `dns-demand.md` 与 `INVARIANTS.md` A21；**未改运行时代码**。
   一个聚焦测试可以定论。
2. **没有执行测试。** 审查主机没有 Go 工具链（也没有 WSL）。本遍所有 `go test` 复检都是
   `BLOCKED_BY_ENVIRONMENT`，不是通过。先前校准结果仍限定在它们自己的 SHA 与主机。
3. **没有做设备、互操作、原生归档或发布 asset 字节检查。** v0.1.34 Release 是通过 GitHub API
   核实的（tag 存在、非草稿、非预发布、发布于 2026-10-29 05:34:42Z）；其 asset 没有在此下载或重算哈希。
4. **没有检查子仓内容。** 重读了父仓 gitlink 和 Go pin；Apple、Android、Desktop 与嵌套 Dashboard
   的源码声明仍限定在各自记录的 revision。本地客户端工作树未动（它们停在不同于父仓 pin 的 revision）。
5. **并非每个历史行锚点都重新推导。** 审计打开了每一个被报告为可疑的显式锚点；没有发现的模块保留
   先前校准。稳定定位器是符号名，不是行号（见 `modules/README.md`）。
6. 历史报告保持 **ARCHIVE**：没有删除，也没有被改写成现状。

## 刷新状态

```text
KNOWLEDGE_REFRESH = COMPLETE (source/knowledge review; execution and device limits above)
OLD_PRODUCTION_BASELINE = 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28
NEW_PRODUCTION_BASELINE = 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28  (unchanged: docs-only delta)

PRODUCTION_CODE_CHANGED_BY_REFRESH = NO
RUNTIME_BEHAVIOR_CHANGED_BY_REFRESH = NO
PROTOCOL_BEHAVIOR_CHANGED_BY_REFRESH = NO
ABI_CHANGED_BY_REFRESH = NO
GIT_HISTORY_REWRITTEN = NO

DELTA_CLASSIFIED = YES (DOC/RELEASE only)
AFFECTED_SUBSYSTEMS_REAUDITED = YES (all 19 module files inventoried; findings fixed)
COMMIT_PROVENANCE_UPDATED = YES (v0.1.33/v0.1.34 objects fetched and peeled; no baseline move)
INVARIANTS_REAUDITED = YES (A21 added; A1 proof SHAs marked pre-rewrite)
ACCEPTANCE_UPDATED = YES (4.6/4.7 added; stale pin/count/log rows corrected)
HISTORICAL_DOCS_RECONCILED = YES (kept as archive; one stale route demoted to history)
CROSS_FILE_CONSISTENCY = PASS (links, anchors, module registry, baseline/version and counts)
FUTURE_AI_INCREMENTAL_READ = PASS (delta is docs-only; modules carry current source anchors)
```
