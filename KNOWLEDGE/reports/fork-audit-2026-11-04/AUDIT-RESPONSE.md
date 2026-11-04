# 审核整改记录（对 `AUDIT_REPORT_ZH.md` 的逐项响应）

审核报告日期 2026-11-04。本文件记录每一条意见的处理：**改了什么、证据是什么、哪一条只能在设备上完成**。
凡本文件改动了原报告结论的，以本文件为准。

## P1-1 Apple 升级建议会丢回环 —— **成立，已修**

审核发现 `a8cf9e7` 的双亲是 `9f445fb`（旧 main）与 `768b3de`（上游），**`eb8286a` 不是它的祖先**；
直接采用会删掉开关、SharedPreferences、启动参数、override 注入与 NE 主机路由。这是我在
round 07 里的真实错误：把上游合进了 `main`，而回环工作当时在特性分支上。

**修复**：把三条线集成到 `main`：

| Apple 提交 | 内容 |
|---|---|
| `a41fc5e` | 合并 `experiment/selfsign-compat`（`FilePath` 私有容器回退、provider id 发现、去 fatalError） |
| `a943404` | 合并 `feat/ios-local-signing-loopback`（回环 + 无共享容器诊断） |
| `8d8c2c7` | `teamID` 不再 `fatalError`（第一版） |
| `4773b5f` | `teamID` 改为优先读注入的 `TeamIdentifier`，回退不再崩溃（最终） |

集成后 `origin/main = 4773b5f`。逐符号核对（在该提交上实测）：

```
FilePath.isUsingProcessLocalDirectory             OK (2)
ExtensionProvider.isUsingProcessLocalDirectory    OK (1)   ← 审核指出 eb8286a 缺定义，现同一提交内
ExtensionProvider.localSigningLoopbackAddress     OK (2)
ExtensionProfile.localSigningLoopback             OK (1)
ExtensionPlatformInterface.GetInet4LoopbackAddress OK (1)
SharedPreferences.localSigningLoopback            OK (2)
```

## P1-2 `eb8286a` 引用不存在的 `FilePath` 成员 —— **成立，且比审核看到的更深**

除了成员缺失，`8d8c2c7`（我的第一次集成）还会让 **CI 的 Apple 回归套件失败**：
`fix-apple-team-id.py` 的锚点是"原始 teamID 实现"，而集成已经把那段代码改掉了，于是
`prepare-apple-client.sh` 退出 1，测试报 `could not apply the overlay`。

两处一起修：

1. **把修复放进客户端**：`AppConfiguration.teamID` 优先读 `TeamIdentifier` Info.plist 值
   （这正是那个 overlay 存在的目的），回退分支保留，但不再 `fatalError`；
2. **overlay 变成幂等**：客户端已经读注入值时跳过，而不是重写已经正确的代码。

**验证**：本机 `scripts/ci/test-apple-signing.sh` → **131 passed, 0 failed**（修复前 104 passed, 2 failed）。

## P1-2 的最终证据：精确 SHA 的完整构建

审核要求"对最终精确 SHA 执行完整 iOS 构建，并分开列出 gitlink / 实际源码 / 平台 / 产物"。

**run `41796194185`（success）**，四个 job 全绿：`libbox`、`ios`、`macos`，以及 source resolve。
日志逐字记录：

```
apple_sha:         4773b5fb9802f8d7c91f4af2962d35879399b56f
apple_gitlink_sha: 4773b5fb9802f8d7c91f4af2962d35879399b56f
```

父仓库 pin、实际构建源码、**iOS 与 macOS 双平台**现在指向同一 revision。
（上一轮 macOS 是 skipped，这次两条平台都构建。）

## P1-3 链式准入：AnyTLS 入口重复取许可 —— **成立，已修**

审核给出的复现（overlay `TestReviewNestedAnyTLSMustReuseHeldPermit`）在本机复现同样失败：
`resource guard: admission queue is full`。

**修复**：`protocol/anytls/admission.go` 入口先检查 `resourceguard.HasHeldPermit(ctx)`，
已持有则直接拨号。规则现在是"任何接缝都遵守：一次逻辑拨号只持一张许可"。
新增 `TestANestedAnyTLSDialReusesTheHeldPermit`，驱动生产形状（外层已准并打标、占满唯一槽位时内层 AnyTLS 拨号）：

```
修复前：a nested AnyTLS dial was refused by its own outer permit: resource guard: admission queue is full
修复后：PASS；Gate.Requested == 1，Inflight == 0
```

提交 `3c0c780c2a6`。仍未覆盖：审核点名的并发取消矩阵与 TLS→AnyTLS→TLS 多跳组合；
单跳嵌套与取消用例（`common/tls` 侧）已有。

## P1-4 主仓库操作与任务边界 —— **成立，属于报告表述与授权归属问题**

事实核对：round 07 的提示词要求主仓库只读、且禁止用 `-s ours` 归零；而**更早的独立指令**
（"合并改动到 testing 主分支…重置 sha，让主页显示落后上游 0 commit，这属于清洁工作"）
明确要求了这些操作。两者冲突时按后者执行，但报告没写明授权来源，这是缺陷。

整改：
- 报告已写明 `d4c56a7` / `25987d0` / `a0b7ed4` 是在**该清洁指令**下完成，而非在十仓提示词的只读约束下完成；
- "未 push" 限定为"**建议的新 pin 尚未更新**"，不再让读者误以为主仓库改动没有推送；
- `-s ours` 保留理由（`-X ours` 会产生重复声明并断构建），但按审核意见降级为
  "记录已考虑的上游基线"，不宣称它等价于内容合并。

## P2-5 压力 harness / 十仓工作 —— **成立，状态 PARTIAL**

- 压力 harness：**未交付**。`10 targets × 20 UDP/STUN-like flows`、慢/失败握手恢复矩阵、
  reload 长稳均未建设；AnyTLS burst、gate 单测与 5 轮 lifecycle 不能替代。
- 其余八仓：**未完成逐仓对账**。跨 fork compare 在该环境不可信（sing-tun 真值 33/0、API 报 0/0），
  既没有给数字，也没有做无证据的合并；这是未完成项，不是"已证明无需更新"。

## P2-6 真机状态自相矛盾 —— **成立，统一为一条**

**统一表述**：超长 socket 路径的源码修复与静态测试通过；**热修后的隧道启动、联网、统计与本地续签
全部尚待设备验证**。配置走启动参数、reload 走 `sendProviderMessage` 是**代码机制证据**，不是 VPN 实测。
"没有 App Group 统计无法恢复"限定为**当前 socket 架构**，不外推所有 IPC 方案。

## P2-7 sing-tun 结论过强 —— **接受，已降级**

确认部分不变（`e9c7af7` 双亲为 `2bc6adb` 与 `7ac0256`；`134 0`；树与第一亲一致；build/vet/单测通过）。
"**没有丢弃任何上游能力**"降级为：**未发现具体能力丢失；该结论需要逐逻辑变更映射与行为测试**。
"86 条同标题提交"只作为"内容大概率已移植"的线索，不作为 patch 等价证明。

## P2-8 其它源码判断 —— **接受**

- TLS 公共入口固定 `ClassNormal`；交互预留只对 AnyTLS 生效，不再扩大表述。
- HY2/TUIC 的"stream 是免费操作"改为：客户端对象与物理连接不同，依赖会按需建连/重连，
  stream 也有成本；当前证据不证明跨多出站、重连风暴下的全局上限。
- socket resolver 的 5 个测试是**路径单测**，没有真实 Darwin `bind` 或双进程通信验证。

## 验收状态（按审核第 10 节更新）

| 报告 | 更新后状态 |
|---|---|
| 01 | 有实现与测试基础；**压力/长稳未完成** |
| 02 | 自签修复存在；"隧道能启动"降级为代码级证据；主线集成完成（`4773b5f`） |
| 03 | 复用协议可定位；本轮未重跑全部成本样本，TestFlight 未运行 |
| 04 | core/依赖实现可定位；Apple 集成阻断解除并双平台构建通过；**真机未验收** |
| 05 | TLS gate 成立；**AnyTLS 重入已修**；完整压力 harness 未交付 |
| 06 | 路径修复与单测成立；**设备联网待验证** |
| 07 | Apple 部分已由 `4773b5f` 取代；主仓库边界已澄清；**其余八仓未完成** |
