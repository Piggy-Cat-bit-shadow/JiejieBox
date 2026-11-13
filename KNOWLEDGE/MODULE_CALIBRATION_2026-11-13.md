# 0.1.45 模块校准与历史断言处置

固定目标：`3032ca5199dbd151be0b2cd006fba191b87cc1e8`。本表记录当前知识的覆盖、旧内容适用范围及跨模块核对点；不把每个历史测试或设备报告重新标成在目标树上执行过。

| 领域 | 当前模块 | 旧内容处置 | 本轮校准重点 |
| --- | --- | --- | --- |
| 协议 | protocols-0145、naive-http-transport、mieru、masque-connectip | 当前接线+旧协议固定版本段 | TrustTunnel/AWG、原生 Cronet、Mieru SDK、MASQUE 的 owner 与互通层级 |
| 网络路径 | path-selection-validation、data-path-optimization、mtu-and-geometry、fast-path-group-policy | 当前接线+旧架构固定版本段 | PhysicalPath、双栈/组、match_only/hidden、MTU、fast_path |
| DNS/规则/配置 | dns-demand、dns-adblock-action、outbound-provider、rules-and-groups-0145 | 当前接线+旧专题固定版本段 | 共享查询、关键词编译、Provider 逐项应用、版本产物 |
| 内存/生命周期 | resource-admission、buffer-ownership、core-lifecycle-runtime、power-mobile-lifecycle、xhttp-client-transport | 当前接线+历史性能分开 | 创建、逻辑字节、数组、copy增长、驻留、回收反馈 |
| 客户端/安全 | libbox-platform-contract、apple-selfsign-loopback、windows-daemon-security、listener-security | 当前父仓入口+旧平台证据分开 | 请求/revision/session、连接快照、原生与真机证据 |
| 依赖/构建 | child-repositories、ci-release、build-pruning-release | 当前 pin/标签重核+旧发布段保留 | root/nested pin、Go replace、native来源、三档标签 |
| 数据入口 | sniff | 当前 deadline 修复+旧识别协议表 | TrustTunnel UDP 期限与 AWG 嗅探识别边界 |

## 已纠正的冲突

- `child-repositories.md` 原称“当前 pin”取自 `e5f5969d`、本地停在 `a3f56ac1`，专题快照曾以 `3032ca5` 核对；随后窗口 03 的客户端 pin 已并入 `testing`，当前消费以父仓 gitlink、`go.mod` 和 source-lock 再核对。
- `resource-admission.md` 中 v0.1.39 AnyTLS 在复用决定前领取许可的说法已标为历史；0.1.45 pinned SDK 在真正创建新 session 时准入。
- `build-pruning-release.md` 的 v0.1.39“当前产品”与 `libbox-platform-contract.md` 的 v0.1.42“本轮”已改成历史范围；`ci-release.md` 旧 Apple gitlink 的“现在”改为 v0.1.34 固定目标。
- `sniff.md` 旧“不支持 AmneziaWG”限于嗅探识别，不能代表 0.1.45 没有 AWG endpoint。
- `PROJECT.md` 与 `ARCHITECTURE.md` 现有最新链路总览；旧提交普查、老模块、性能数字仍在原历史目标下阅读。
- `release/dependency-final-shas.json` 的 main_repo.head 为较早生成记录，当前 pin 优先读 `go.mod`、`release/source-lock.json` 与父仓 gitlink。

## 证据层级和未升级的断言

当前源码与仓库已有测试可确认生产接线；本轮实际复跑 `common/resourceguard`、`common/artifact`、`common/urltest`、`route/rule` 四包通过。六窗口 25/25 是指定代码项目的既有审核结果；其 19 项保留回归、Apple/Android/桌面真机、Cronet 原生全目标、独占性能和长稳态不可被这四包测试替代。历史 0.1.45 台架数据、WG/AWG 36/36、旧 XHTTP A/B 均保留原环境和候选 SHA。

仍未逐提交证明 760 个 v0.1.44 之后提交的全部来源归类，也没有将所有历史模块的每条行号、每项设备声明重新执行。全局广泛基线维持 v0.1.34；当前专题目标覆盖表中领域的源码概述、关键 owner 和已核对交接点。若未来要前移广泛基线，须继续按 `REFRESH.md` §31 核对每条旧知识与最终代码，消除剩余漂移。
