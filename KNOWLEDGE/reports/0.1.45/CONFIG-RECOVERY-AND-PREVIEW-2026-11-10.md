# 配置规则与恢复、两端预览语义与采样验收（2026-11-10）

- 归档版本：`0.1.45/`（目录名）；实际构建版本 `0.1.44`
- 源码基线：`integration/next-round` @ `edbfe8e0b2b`（归档提交前）
- 证据等级见各表；`BLOCKED_BY_ENVIRONMENT` 的项不按通过计

## 1｜结论摘要

| 领域 | 状态 | 关键证据 |
|---|---|---|
| Windows 配置恢复（入口） | 完成 | 4 条入口回归 + 产品夹具 + 损坏存储负向对照，全部 `VERIFIED_UNIT`/`VERIFIED_INTEGRATION` |
| Windows 配置恢复（产品进程） | 阻塞 | 需要提权服务 + 认证客户端；console 模式被 SYSTEM 目录所有权拒绝，`--listen` 无 peer 身份 |
| 两端 `started` 预览语义 | 完成 | 真实内核 fixture 复现 5/6 失败 → 修复；Windows 19/19、Apple harness 7/7 |
| Windows 消费构建 | 完成 | 安装包 run 与审计 run 均 success，消费 core/desktop/dashboard 三个 SHA |
| Apple 构建 | 完成（无签名） | iOS 与 macOS 两个 run 均 success，产物 SHA256 与 `SHA256SUMS` 一致 |
| Apple 运行期 | 阻塞 | 无设备/签名环境；包结构、标识与配方已核对 |
| D 独占采样 | 部分完成 | d5 低负载对照（`NON_EXCLUSIVE_LOCAL`）；d6 判定器已自证，合格窗口待命中 |

## 2｜Windows 配置恢复

**入品回归（`VERIFIED_UNIT`，`daemon` 包，本机全量 92 s 通过）**

| 用例 | 断言要点 |
|---|---|
| `TestAParseRejectionAfterAnUpdateIsNotARecovery` | `duplicate inbound tag` 在解析阶段被拒：报错、`failed`、`retained=true`、`activeRevision` 仍是 B、无恢复事件、磁盘仍 B |
| `TestABuildFailureAfterAParseableCandidateRecoversToTheStoredDocument` | 未声明规则集先通过预览、构建阶段失败：`recovered`、`activeRevision`=A、`failedRevision`=C_build、磁盘仍 A |
| `TestTheRecoverySurvivesAProcessRestart` | 关服务 → 同目录新对象 → 重放 C_build：恢复报告、修订、运行实例、磁盘 A、无候选标记全部来自磁盘 |
| `TestARecoveryFromACorruptedStoreDoesNotClaimSuccess` | 损坏 `active.json` 后提交构建失败候选：启动失败、无实例、报告 `failed`、`LastConfigRecovery` 为 nil |

**既有用例覆盖"外部引用被覆盖"**

- `TestARecoveryPinsTheExactRuleSetVersion`：远端规则集来自回环 HTTP，保存的配置钉住其版本；B 内容改变不影响恢复使用的版本。
- `TestARecoveryRepairsAMissingPinnedArtifactFromTheCache`：钉住的产物缺失时由缓存修复。
- `TestARecoveryServesTheOldRoutingRuleAndProviderNodes`：同一文件路径被覆盖为 B 后，恢复仍按存储的 A 提供规则与 Provider 节点，且 A/B 的业务结果可区分（`retained=false`）。

**产品入口夹具（`daemon/testdata/product-abc/`，`VERIFIED_UNIT`）**

- 节点是 `http` 出站 → 回环 canned proxy：A 节点回 `node-A`、B 节点回 `node-B`；两份文档各拒一个端口（18091/18092），规则与节点在每一步都可观察。
- 序列：A 保存 → B 在线但磁盘失败（`degraded`）→ C_parse 拒绝保留 B → C_build 恢复 A → 同目录新对象重放。
- 负向对照：关恢复 → C_build 步骤失败；selector 换 `direct` → 标记断言失败；损坏存储 → 不得报恢复。

**竞态检查（`VERIFIED_UNIT`）**：在 MSYS2 mingw gcc 16.2.0 + `CGO_ENABLED=1` 下对本轮三条恢复用例运行 `go test -race`（产品序列、重启重放、损坏存储），通过（4.9 s）。其余路径的 `-race` 不在本轮范围。

**产品进程（`BLOCKED_BY_ENVIRONMENT`）**：`DesktopService.StartService` 要求 Windows peer 身份；console 模式要求工作目录由 SYSTEM 拥有（原始错误：`daemon working directory is not owned by SYSTEM`），`--listen` 开发传输不提供身份，命名管道需要管理员安装隔离服务。步骤与断言见 `HANDOFF-ACCEPTANCE.md`。

## 3｜两端 `started` 预览语义（P1）

- 内核契约：`ServiceChangePlan.Started` 表示"当前已有运行实例"；`started=false` 是首次启动。
- 复现：用真实内核 `PreviewServiceChange` 输出做 fixture，旧实现 **5/6 失败**——首次启动显示"无需重启"，运行中的 live/restart/stale 全被"将启动新实例"覆盖。
- 修复：Windows 判定顺序改为 `unchanged → stale → !started → restartRequired → liveOnly → needsNoRestart`，重启原因优先用 `changedSections`；Apple 把判定下沉到 `ConfigurationPlan.verdict`（Foundation-only），view 只渲染。
- 验证：Windows 与 P1 直接相关的两个测试文件在消费版本 `fc1dbb4` 上 **19/19**；桌面工作区完整 dashboard 套件 **98/98**（该树 dashboard 为 `62f1582`）；Apple verdict harness 重新编译后 **7/7**。独立 dashboard 工作树因缺少共享包只能收集 9 文件/83 测试，属环境限制，已与真实失败分开记录。

## 4｜消费构建

| 平台 | 产物 | 消费版本 | 结果 |
|---|---|---|---|
| Windows | 安装包 run `42735401015` | core `5778c8417d3` / desktop `aa63764` / dashboard `fc1dbb4` | success；artifact SHA256 `6599cf6f…`（116,372,152 B） |
| Windows | 运行时审计 run `42736254206` | 同上（日志实记 audited build SHA 与 installer build run） | gate 5 / 5b / 6 / 7 与共存冒烟均为独立步骤 success |
| iOS | run `42740100569` | core `5778c8417d3` / apple `7fce711` | success；IPA SHA256 `4d5b0b31…`（45,263,411 B） |
| macOS | run `42742558200` | core `a1bf528637a` / apple `7fce711` | success；DMG SHA256 `8122922f…`（80,141,993 B） |

- 发布引用：`clients/apple` 与 `release/source-lock.json` 同指 `7fce711`；`release-freeze verify` PASS。
- Apple 包标识（从包内 `Info.plist` 核对）：iOS `io.nekohasekai.sfamt`、扩展 `io.nekohasekai.sfamt.extension`（packet-tunnel）、App Group `group.io.nekohasekai.sfamt`；macOS `io.nekohasekai.sfamt.standalone`、系统扩展 `io.nekohasekai.sfamt.system`；应用路径分别是 `Payload/sing-box.app` 与 `SFI` 的 `SFM.app`。按错误的包标识重签会导致扩展与 App Group 失配。
- IPA 结构：`Payload/sing-box.app` + `PlugIns/Extension.appex` 等 75 条；DMG：`SFM.app` + `Library/SystemExtensions/io.nekohasekai.sfamt.system.systemextension` + 30 个 `.appex`。均无签名（预期）。

## 5｜D 采样

**d5 批次（`NON_EXCLUSIVE_LOCAL`，仅作低负载对照）**

- 每边 6 轮、1,926 次尝试全部 success；queue 中位数 6.480→6.616 ms（+2.1%）、build 2.140→2.159（+0.89%）、fetch 8.984→9.009（+0.28%）。
- 精确 Mann–Whitney（每边 6 个轮中位数，单侧）p=0.090 / 0.066 / 0.066；候选 queue 轮间 σ=0.236 ms 大于中位数差 0.175 ms → **无可复现回归**。
- 口径限制：该批 `valid` 只检查退出码、CSV 与采样数；轮间只有"其他进程合计"、轮内只有含被测进程的全机负载，不能证明单进程竞争持续 ≤2%。因此降为低负载对照，不称"独占通过"。

**d6 协议（判定器自证，`VERIFIED_UNIT`）**

- 采样：每 100 ms 记录全机 `GetSystemTimes` 与每进程累计 CPU/RSS/PID/创建时间；判定：实际增量 ÷ 实际跨度 ÷ 逻辑核数，按 PID+创建时间排除被测与采样器，逐轮给理由。
- 自检 **10/10**：合法的两个 1.8% 进程（合计 3.6%）有效；单进程 2.5%、断样、中途出现、覆盖不足、PID 复用、非零退出、340 行仅 321 成功、98/100 成功均被拒；被测进程自身 10% 被正确排除。
- 门禁：干净 30 秒窗口（2%/1.5%）合格；8%/4% 拒绝；5 秒截断窗口拒绝（`window-short`）；采样器自身占用按记录 PID 排除。
- 受控负向实跑：注入竞争 `competition-18%~25%`、采样器提前停 `coverage-0.35~0.75`、非零退出 `exit-code-3`、目标 5 `csv-rows-5-of-321`；真实负载轮点名 `compile.exe 65.75%`。
- 独立复算：用 Python 从原始 jsonl+meta 重算一轮，coverage/maxGap/maxSingle/maxSum 与判定文件完全一致。
- 现状：截至归档仍无合格窗口（后台按 9 小时连续门禁，门禁数值逐次留档）；合格批出现后由 `analyze_d6.py` 直接产出比较结论。

## 6｜复现命令（脱敏）

```text
# 入口回归（daemon 包，Windows）
go test ./daemon -count=1

# 产品夹具（同一组文档已导出到 daemon/testdata/product-abc/）
go test ./daemon -run TestTheProductSequenceUsesTheOldProviderNodeAndRule -count=1

# 资产与发布引用
release-freeze.py verify --lock release/source-lock.json

# d6 判定器
d6tool.exe selftest
d6tool.exe gate -samples <samples.jsonl> -window 30s
d6tool.exe validate -samples <samples.jsonl> -round <round.json>
```

## 7｜限制

- Windows 产品进程 A/B/C（真实命名管道 + 认证客户端）未执行，`BLOCKED_BY_ENVIRONMENT`。
- Apple 运行期（启动、App Group、扩展激活、三行 UI 与 RPC 对照）未执行，`BLOCKED_BY_ENVIRONMENT`。
- D 的合格独占窗口在归档时未出现；d5 不能替代 d6 合格批。
- 本归档不移动 `KNOWLEDGE/BASELINE.md`，也不代表知识刷新。
