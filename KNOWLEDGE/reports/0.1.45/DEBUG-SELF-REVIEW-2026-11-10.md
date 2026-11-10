# 定向自审：反例、负向对照与证伪记录（2026-11-10）

- 范围：本窗口直接改动的路径——Windows `boxdd` 的 A/B/C 与重启、D 采样与判定、Apple 包标识与配方。
- 方法：入口 → 分支 → 所有权与代次 → 实际结果 → 可区分证据；对每个"通过"追问"如果实现没生效，这条断言还会绿吗"。
- 结论：确认并修复 5 个缺陷（下表 1、2、4、5、6），其余为漏洞排查与证伪；三项外部条件仍为 `BLOCKED_BY_ENVIRONMENT`。

## 1｜发现的缺陷

| # | 预期 | 反例与复现 | 实际观察 | 影响 | 修复与回归 | 证据 |
|---|---|---|---|---|---|---|
| 1 | 恢复后应能证明旧 Provider 节点被消费 | 旧 A/B 夹具声明 Provider 但路由到固定 outbound；把 Provider 从拨号路径摘掉仍会通 | `pp` 节点从未承载请求，HTTP 成功无法区分节点 | 产品验收会假绿 | 节点体返回标记（A=`node-A`/B=`node-B`），selector 换 `direct` 时标记断言失败 | `daemon/config_recovery_product_test.go`、`daemon/testdata/product-abc/` |
| 2 | 独占判定应把单进程竞争纳入每轮有效性 | d5 的 `valid` 只查退出码/CSV/采样数 | 无法核对单进程是否持续 ≤2% | "独占通过"被高估 | d6 把每进程归一化占用、覆盖率、出现进程、PID 复用纳入逐轮判定；d5 降为低负载对照 | d6 判定器与批次记录 |
| 3 | Apple 重签必须沿用构建时的标识 | 按 `SFI.app`/`io.jiejiebox.app` 重签 | 包内实为 `sing-box.app`、`io.nekohasekai.sfamt` 系列、`group.io.nekohasekai.sfamt` | 真机安装会白屏或扩展不激活 | 从 IPA/DMG 的 `Info.plist` 提取真值并改正配方 | 包内 `Info.plist` 提取记录 |
| 4 | 覆盖不足的轮次必须失效 | 轮截止时间取自最后一条样本，覆盖恒为 1.0 | coverage 用例误判为有效 | 判定器会放过掉样 | 给 meta 增加真实截止偏移；`coverage-0.50` 被拒；自检 5/8→8/8→9/9→10/10 | `d6/selftest.json` |
| 5 | 门禁窗口必须真的覆盖 30 秒 | 采样器晚启动或在负载下延迟，样本只覆盖数秒 | 新增 `window-short` 检查；合成 5 秒样本判 `window-short-5000ms`；实跑中两次（8.99 s、23.8 s）被拒 | 门禁会假绿 | `gate` 校验实际跨度（容忍 500 ms 离散）；干净 30 秒仍合格、8%/4% 仍被拒 | 门禁 fixture 与运行日志 |
| 6 | 门禁的"其他进程"不应包含测量装置 | gate 的 `SamplerPID` 恒为 0，采样器被计入 | 实跑样本 `sampler=<pid>` 与进程一致；修复前会出现在 perProcess | 门禁偏严，可能挡住合法窗口 | 样本行记录采样器 PID，gate 排除该 PID；合成 fixtures 结论不变 | 采样样本与 perProcess 记录 |
| 7 | 采样器 CPU 必须是时间量 | Filetime 当绝对时间 | 逐轮 CPU 显示 13 万亿 ms | 证据不可读 | 改为 `(High<<32\|Low)/10000`；复跑 1827/1624 ms、RSS 22.9/19.9 MB | 原始样本与判定 |
| 8 | 恢复读到的磁盘证据损坏时不得凭空报恢复 | 损坏 `active.json` 后提交构建失败候选 | 启动失败、无实例、报告 `failed`、无恢复事件 | 会误报恢复 | 新增回归 `TestARecoveryFromACorruptedStoreDoesNotClaimSuccess` | `daemon/config_recovery_candidates_test.go` |
| 9 | 审计作业的每条 gate 必须实际执行 | 逐个检查 run `42736254206` 的步骤名与结论 | gate 5/5b/6/7 与共存冒烟均为独立步骤 success | 生命周期证据链会虚 | 按步骤级记录，而非只看作业结论 | CI run 步骤清单 |
| 10 | 测试数必须对应可运行环境 | 同一 dashboard 套件在两个工作树分别跑 | 桌面树 98/98；独立树 83/83 + 1 文件缺共享包 | 报告数字会误导 | 报告按环境分列，P1 两文件在消费版本 19/19 | 运行输出 |

## 2｜负向对照（全部按预期失败）

| 干预 | 期望 | 实际 |
|---|---|---|
| `RecoverToLastGood=false` | C_build 步骤失败 | `rule-set not found: missing` |
| selector 换成 `direct`（摘掉 Provider） | 标记断言失败 | 请求无法到达节点，标记断言失败 |
| 损坏 `active.json` | 不得报恢复 | 启动失败、无实例、无恢复事件 |
| 注入竞争（3 秒 spin） | 该轮 `competition-` | `competition-18%~25%` |
| 采样器提前停止 | 该轮 `coverage-` | `coverage-0.35~0.75` |
| 非零退出 | 该轮 `exit-code-` | `exit-code-3` 且行数 0 |
| 目标降到 5 | 行数/成功数不足 | `csv-rows-5-of-321` |
| 340 行仅 321 成功 | `non-success-19` | 被拒 |
| 5 秒截断门禁窗口 | `window-short` | 被拒 |

## 3｜证伪的猜想（避免重复施工）

- "审计 run 的 gate 可能被条件跳过而仍报成功"——步骤清单显示每条 gate 都执行。
- "Apple 产物与 pin 可能不一致"——重算 SHA256 与 `SHA256SUMS` 一致，gitlink 与发布锁同指 `7fce711`。
- "d6 判定可能不可重算"——用 Python 独立复算一轮，四个统计量与判定文件完全一致。
- "d5 的 12 份 CSV 可能成功数不足"——每轮 321 行、321 成功，共 1,926/边。
- "同一 URL/文件被覆盖后恢复可能读了新内容"——既有 `TestARecoveryServesTheOldRoutingRuleAndProviderNodes` 与引用钉住用例已覆盖，恢复读存储事务而非文件。
- "产品夹具可能因连接复用产生假阳性"——每个请求新建 client，reload 后旧连接不作证。

## 4｜仍缺环境

- Windows 产品进程 A/B/C（提权 + 认证客户端）。
- Apple 运行期（设备/签名）。
- D 的合格独占窗口（门禁连续采样中）。

三项的下一步命令与通过标准见 `HANDOFF-ACCEPTANCE.md`。
