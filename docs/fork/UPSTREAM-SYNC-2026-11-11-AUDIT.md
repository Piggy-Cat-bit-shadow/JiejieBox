# 上游同步审计 — 2026-11-11（0.1.45 之后）

状态：**审计完成，未改源码**。下一阶段（02）从 `sing` / `sing-tun` 开始施工。
机器可读版本：`docs/fork/UPSTREAM-SYNC-2026-11-11-AUDIT.json`。

## 0. 前置任务与施工基线

此前六份“分支合并 / 仓库收口”任务已完成：0.1.45 RC 在 `integration/0.1.45-final`（`e7e9706a456`，已推送，`rc/0.1.45` tag 指向 `cec7bda2725`），依赖与客户端已收口，15 个远端 topic 分支已归档删除，20 个 archive tag 已推送。剩余 Apple 的 Xcode 构建为环境阻塞，不影响本阶段。

| 项 | 值 |
|---|---|
| 主仓 | `Piggy-Cat-bit-shadow/JiejieBox`（`origin` 已修正） |
| 当前工作分支 | `integration/0.1.45-final` @ `e7e9706a456` |
| 主仓默认分支 | `testing` @ `4152caf0e5` |
| 主仓上游 | `SagerNet/sing-box:testing` @ `a13dbe1f78` |
| 主仓上游 merge-base | `8eb4395a391`（2026-11-03） |
| 上游独有 | **85** 个提交；375 文件，+21090/−6137 |
| 本分支相对上游 | ahead 3705 / behind 85；`origin/testing` ahead 3363 / behind 85 |

**上游历史已重写**：旧审计记录的 upstream `a6952ccf690`（2026-10-24）**不再是当前 upstream/testing 的祖先**（`merge-base --is-ancestor` 失败）。旧审计的 22 行语义结论仍代表当时已吸收的代码，但其“behind=0 / upstream observed=a6952cc”已过期；本轮以 `8eb4395` 为 merge-base 重新计算，85 个提交是当前真实增量。

## 1. 动态识别的仓库与正确上游

| 仓库 | 正式分支 | 正确上游 | 分支 tip | 上游 tip | ahead/behind |
|---|---|---|---|---|---|
| sing-box（主） | `testing` / 施工 `integration/0.1.45-final` | `SagerNet/sing-box:testing` | `4152caf0e5` / `e7e9706a45` | `a13dbe1f78` | 3363/85（testing）；3705/85（集成） |
| sing | `dev` | `SagerNet/sing:dev` | `1729220c46` | `27b73774b2` | 64/27 |
| sing-tun | `dev` | `SagerNet/sing-tun:dev` | `57e9d17fa8` | `79ed0653fd` | 182/42 |
| sing-quic | `main` | `SagerNet/sing-quic:main` | `1ef2597cc1` | `5253224b67` | 7/4 |
| quic-go | `sagernet`（消费线 `integration/0.1.45`） | `SagerNet/quic-go:dev` | `2daa76f5bd`（消费 `ca9992bc79`） | `2e73222e96` | 18/1（消费线 29/1） |
| utls | `main` | `MetaCubeX/utls:v1.9.0-mod-meta` | `b63a2e9a7b` | `89a07ad380` | 68/1 |
| cronet-go | `main` | `SagerNet/cronet-go:main` | `4c96192d73` | `73349ff2a5` | 29/**0** |
| wireguard-go（AWG 线） | `awg3-inject` | `sagernet/wireguard-go:main` | `a765d957fd` | `e4d85ef2ed` | 22/**0** |
| apple | `main` | `SagerNet/sing-box-for-apple:dev` | `97b35d1ce2` | `768b3de2ba` | 220/**0** |
| android | `main` | `SagerNet/sing-box-for-android:dev` | `c7837107dc` | `ab4fad3944` | 14/5 |
| desktop | `main` | `SagerNet/sing-box-for-desktop:main` | `aa63764a61` | `8aab8eab02` | 42/**0** |
| dashboard | `main` | `SagerNet/sing-box-dashboard:main` | `fc1dbb4a48` | `3e4bf08e5b` | 9/95，**无 merge base** |
| satelite-one | 独立产品 | 无（不跟踪 SagerNet） | `c9a07350e4`（集成分支 `9cdf782b`） | — | — |

satelite-one 不是主仓 gitlink、也没有上游 fork 关系；不纳入“零落后”目标，但它的内核 AAR 消费契约仍属于本轮依赖验收。
**GitHub Compare API 交叉核对**（2026-11-11）：`SagerNet/sing-box` compare `Piggy-Cat-bit-shadow:testing...SagerNet:testing` 返回 `status=diverged, ahead_by=85, behind_by=3363`；`SagerNet/sing` compare 返回 `ahead_by=27, behind_by=64`。API 的 `ahead_by` 即上游独有提交数，与本地 `rev-list --left-right` 的 behind 列一致。dashboard 因无共同祖先无法 compare，只能按本地提交主题审计。

## 2. 主内核增量（85 个提交）

### 2.1 主题分组

| 主题 | 代表提交 | 风险 |
|---|---|---|
| auto-redirect / TUN / IP 头 | `a13dbe1` 平台关闭顺序、`02838af` 回程路由、`eabdca6` 合成 UDP IP 头、`0b83b66` GSO 段数、`586b480` link-local 回环判定 | 高：与 fork 的 TUN Fast Path、NAT、队列预算交叉 |
| MASQUE / endpoint 背压 | `0c343d8` MASQUE 入站背压、`4719109` per-destination 背压、`307a9ba` on-demand resume | 高：与 fork 的 admission/disposal ledger 重叠 |
| 并行拨号 | `2b974d1` stagger 连接尝试 | 高：与双栈调度/取消语义交叉 |
| NAT / UDP | `075fc22` forward NAT UDP mapping/fragment、`f82c724` 未解析域名丢弃、`ddf3d10` UDP domain unmapping、`9766191` SS UDP 关联 | 高 |
| 协议修复 | OpenVPN ×4、Snell ×5、SOCKS ×2、Shadowsocks、VMess/VLESS mux、ShadowTLS、AnyTLS UDP framing、UoT 泄漏、hysteria obfs、packetaddr/mux 字节数 | 中高，逐个需读代码 |
| TLS / HTTP | `b802693` TLS SHA-256 pinning、`9293479` HTTP/2 extended CONNECT（Go 1.27）、`7880f4c` HTTP proxy rewrite | 高：客户端契约与 pinning 策略 |
| DNS / FakeIP / 进程 | `7a96608` DNS reverse mapping、`b6dc339` FakeIP 缓冲写一致、`47e898a`/`bb9fdd5` 路由错误时的 release/泄漏、`f5e92e1` 替换可执行文件的进程路径、`bc3345f` Windows owner 查找 | 高：DNS/进程所有权是 fork 核心 |
| 缓存/空闲连接 | `e8b8d10`/`b6dc339` cache file 缓冲、`729230a`/`31aacbf` idle 连接回收 | 中 |
| 平台/CI/版本 | `009669` bump、`4e0917` 删除旧 workflow、`f10d31` test module replace 清理、`626964` systemd root | 低/需拒绝判断 |

### 2.2 初步分类

- **已在上游范围内但被 fork 以不同实现覆盖（候选等价）**：85 个提交中 **44** 个的 subject 在 fork 历史中存在匹配（不算 ancestry；patch-id 全部不同，说明是改写/部分吸收，需在 05 阶段逐项 diff 确认）。
- **未匹配、需语义审查**：**41** 个提交，对应上表大部分高风险主题。
- 由于上游重写，`a6952cc..8eb4395` 的旧审计行不再能作为 ancestry 证据；旧 22 行结论仅作“代码是否已在树内”的历史线索。

## 3. 依赖 Fork 增量

| 仓库 | 上游独有 | subject 在 fork 命中（候选等价） | 未命中（需审查） | 主要区域 |
|---|---|---|---|---|
| sing | 27 | 9 | **18** | `common/bufio` 44 文件、`protocol/socks`；CachedConn Read/Close 竞态（`dcc5d99`）、并行拨号取消、vectorised/batch |
| sing-tun | 42 | 31 | **11** | `internal`、`gtcpip`、go stack；ENOBUFS、auto-redirect 关闭/回程、IP 头、GSO、IPv4 ID、Android 网络变化 |
| sing-quic | 4 | 0 | **4** | ACK-only pacing、Salamander 短包/写、XPlus 写 |
| quic-go | 1 | 0 | **1** | ACK-only pacing（`2e73222`） |
| utls | 1 | 0 | **1** | UQUICConn preset transport parameters（`89a07ad`） |
| cronet-go | 0 | — | — | 已领先上游 29；native 保持 controlled |
| wireguard-go | 0 | — | — | AWG 线已含 sagernet main；无落后 |
| apple | 0 | — | — | fork main 已含 upstream/dev；pin 仍受 Xcode 阻塞 |
| android | 5 | 4 | **1** | 版本 bump、DNS search domains、libghostty、auto redirect、浅色主题可读性 |
| desktop | 0 | — | — | 无落后 |
| dashboard | 95 | 0 | **95** | **fork main 与上游 main 无共同祖先**；上游新增 Taildrop、power report、JSON completion、终端布局等；必须制定“重写历史/语义移植/放弃上游”策略，不能硬 merge |

依赖侧的“命中”同样只代表 commit subject 存在；patch-id 全部不同，stages 02–04 必须逐项比对内容和测试，不能按 subject 判等价。

### 3.1 试合并规模（实际执行，未提交）

用临时 worktree / `git merge-tree` 做无提交试合并，只统计真实冲突文件数，作为阶段 02–05 的工作量上界：

| 仓库 | 试合并 | 冲突文件 |
|---|---|---|
| sing | `origin/dev` + `upstream/dev` | **13** |
| sing-tun | `origin/dev` + `upstream/dev` | **31** |
| sing-quic | `origin/main` + `upstream/main` | **0**（可直接 merge） |
| quic-go | `origin/sagernet` + `upstream/dev` | **0**（可直接 merge） |
| utls | `origin/main` + `upstream/v1.9.0-mod-meta` | **1** |
| android | `origin/main` + `upstream/dev` | **0** |
| dashboard | `origin/main` + `upstream/main`（`--allow-unrelated-histories`） | **13** |
| 主内核 | `integration/0.1.45-final` + `upstream/testing`（merge-tree） | **108**（protocol 28、transport 24、common 12、route 7、dns 5） |

dashboard 的树差异只有 22 个文件（+20/−2098），并不是“95 个提交无法融合”：无共同祖先只是历史被重写；实际代码非常接近。阶段 04 建议用 **`git merge --allow-unrelated-histories`** 融合这 13 个冲突（保留 JieJiebox 品牌与自定义页面），得到真实双祖先的零 Behind，而不是 `replace`/graft 伪造祖先。试合并证据：`audit-evidence/upstream/trial-merge-*.txt`。

## 4. 队列分类统计（本阶段）

| 分类 | 数量 | 说明 |
|---|---|---|
| `ALREADY_ANCESTOR` | 0 | 审计范围只取 upstream-only 提交 |
| `PATCH_EQUIVALENT`（已证实） | 0 | patch-id 相同为 0；subject 命中 88 个只是候选 |
| `BEHAVIOR_ALREADY_PRESENT`（待证实） | 88 | main 44 + sing 9 + sing-tun 31 + android 4 |
| `MERGE_AS_IS` | 0（待定） | 依赖分支均有 fork 改动，预计以 merge + 冲突融合为主 |
| `ADAPT_OR_REDESIGN` | 保守估计 77 | main 41 + sing 18 + sing-tun 11 + sing-quic 4 + quic-go 1 + utls 1 + android 1 |
| `OBSOLETE_OR_NOT_APPLICABLE` | 0（待定） | 需在逐项审查时确认（如 systemd root、旧 workflow） |
| `REJECTED_WITH_EVIDENCE` | 0（待定） | 旧审计的 systemd root 拒绝仍有效，但不在本轮 85 内 |
| `BLOCKED_WITH_REASON` | 1 | dashboard：无 merge base，策略未定 |
| `UNKNOWN` | 77 + 95（dashboard） | 未匹配且未读代码；05 前必须降为 0 或给出决定 |

**统计口径**：一个提交可能含多个主题；上表按提交计。88 个 subject 候选不是“已等价”，只是“需要优先比对”。

## 5. 最高优先级真实缺口（建议 02–05 顺序）

1. **sing**：`dcc5d99d` CachedConn Read/Close 竞态（阶段 02 明确候选）；`90f8f35`/`804e650`/`371e9b8` 并行拨号取消与 stagger；`common/bufio` 的 vectorised/batch 写与 IOV_MAX。
2. **sing-tun**：`79ed065` go stack ENOBUFS、`d42b35f`/`74aedeeb` auto-redirect 关闭挂起、`4f879c2`/`73e684c` 回程路由、`cb941cc` GSO 段数、`b091626` IPv4 ID、`27e188f` Android 默认网络变化；全部要对照 fork 的 per-session/device/NAT byte budget、waiter ownership、Close 清理。
3. **sing-quic + quic-go**：`5253224`/`2e73222` ACK-only pacing；`a5639ae`/`fad401c` Salamander；`8aab71d` XPlus 写。
4. **utls**：`89a07ad` UQUICConn preset transport parameters。
5. **主内核协议/传输簇**：MASQUE 背压、forward NAT、endpoint 背压、并行拨号、TLS pinning、HTTP/2 extended CONNECT、AnyTLS UDP framing、OpenVPN/Snell/SOCKS 修复。
6. **dashboard**：无共同祖先，95 个上游提交；需要产品决策（语义移植 vs 采用上游历史 vs 接受持续落后并记录），不能在本轮用 ancestry merge 伪造零落后。
7. **android**：`a9e2732` 浅色主题可读性（唯一未命中）+ 4 个候选等价；上游版本 bump 不应盲目跟随，fork 有自己的版本线。

## 6. 必须保护的 Fork 实现（融合边界）

- `route`/`adapter` 的 admission ticket、disposal ledger、generation/DNS pin、close ownership。
- 双栈候选 provenance、family capability、DNS `LookupFamilies` 复用 `Router.Lookup`。
- H3 verdict / `H3_REQUEST_REJECTED`、body 生命周期、H2 generation gate、Naive H2 engine。
- `sing-tun` 的 per-session/device/NAT byte budget、waiter ownership、跨队列唤醒、Close 清理、TUN Fast Path。
- `sing` 的 batch/vectorised/splice 语义与 fork 扩展（不得为了对齐上游机械改 Mutex）。
- libbox `ApplyServiceChangeReport` / provider refresh 取消/能力文档、`SelectOutboundResponse`、连接流 instance/sequence。
- Apple NE 所有权/回执、Android AAR provenance、Desktop/Dashboard 消费契约。
- wireguard AWG 注入与 `awg3-inject`（上游已 0 落后）。

## 7. 与旧审计的关系

- 旧 `UPSTREAM-INTEGRATION-STATUS.md/json`（2026-10-24，phase 06）记录 22 行关闭、behind=0、upstream `a6952cc`。**这些历史事实保留**，但 upstream 已重写，`a6952cc` 不再是当前上游祖先，behind=0 已过期。
- 旧审计的 22 行代码结论仍可作为“是否已在树内”的线索；本轮 85 个新提交中 44 个 subject 命中，需逐项复核是“旧结论的改写”还是“新缺口”。
- 本文件与 JSON 是本轮唯一新增真值源；旧 STATUS 文件不更新（保留历史），但新增顶部指针说明其过期。

## 8. 下一阶段施工入口

| 阶段 | 仓库 | 分支 | 关键测试入口 |
|---|---|---|---|
| 02 | sing、sing-tun | `dev`（fork）→ 先建 `sync/0.1.45-upstream` 集成分支 | `go test ./...`、`-race`、TUN 测试（管理员）；内核 `go test ./common/...` 消费验证 |
| 03 | quic-go、sing-quic、utls、wireguard-go | quic-go `integration/0.1.45` / `sagernet`；sing-quic `main`；utls `main`；wg `awg3-inject` | `go test ./...`；内核 `./transport/v2rayquic`、`./transport/wireguard`、`./protocol/hysteria*` |
| 04 | cronet-go、naive/xhttp/mieru、apple/android/desktop/dashboard | 各仓 `main` / 集成分支 | Go 测试；dashboard `pnpm test`；desktop `pnpm test`；android Gradle；Apple BLOCKED（Xcode） |
| 05 | 主内核 | `integration/0.1.45-final` → `sync/0.1.45` | `go build ./...`、全量 `go test`、`-race`、`release-freeze.py verify`、零 behind 核对 |

**顺序理由**：`sing`/`sing-tun` 是 `quic-go`、客户端与主内核的公共底座；先收敛它们再改传输层，最后主内核合并上游并统一 pin。dashboard 与主仓历史重写相关，放在 04 末尾做策略决定。

## 9. 已知风险

1. 上游历史重写：不能再用 ancestry 直接 merge 旧 upstream；必须以 `8eb4395` 为基线三方融合，冲突量可能被低估。
2. dashboard 无 merge base：任何 `git merge` 都会产生重复文件树；必须语义移植或重写历史，属于产品决策。
3. subject 命中不等于等价：88 个候选需逐项 diff + 测试，禁止按 subject 标 PASS。
4. fork 分支 `dev/main` 与消费 pin（`integration/0.1.45`）不一致：先合并上游到 fork 正式分支，再更新内核 pin；不能只改 pin 不改分支。
5. Apple 仍缺 Xcode；其上游 `dev` 虽 0 落后，但本轮不能声称 Apple 全平台通过。
