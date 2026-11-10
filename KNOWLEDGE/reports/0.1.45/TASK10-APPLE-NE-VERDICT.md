# 任务 10 · 复审裁定表（脱敏合并版）

三轮独立复审 + 一轮终轮施工的合并裁定。每行给真实入口、注入与观测、证据层级、候选 SHA 与剩余动作。
"本机执行"指真实 HTTP / 真实 gRPC / 真实文件 / 契约包；源码推导单独标注。

## 一、必攻矩阵

| 裁定项 | 真实入口 | 注入与观测 | 证据层级 | SHA | 裁定 | 剩余动作 |
|---|---|---|---|---|---|---|
| Provider 发布边界 | `provider/remote` 的 fetch → applyContent → UpdateNodes | 在注册 option 的 `UnmarshalJSON` 中取消：节点不发布、报告 `unknown`、错误含取消原因 | `VERIFIED_UNIT`（真实 HTTP + 真实 harness） | 内核 `a3340d1` | `PASS` | 无 |
| Provider 三组边界 | 同上 + 手动刷新入口 | build gate 拒绝时零 HTTP；`Close()` 能取消在意下载；读体后取消不发布 | `VERIFIED_UNIT` | 内核 `a3340d1` | `PASS` | 无 |
| 报告归属 | `UpdateWithReportContext` | 拒绝的尝试返回 nil 报告；定时刷新与手动刷新交错时只返回本次 | `VERIFIED_UNIT` | 内核 `a3340d1` | `PASS` | 无 |
| 旧 daemon 能力缺口 | 真实 gRPC | 未注册 RPC → `{"supported":false}` 且 `err == nil`；未启动 → 明确错误 | `VERIFIED_UNIT`（真实 gRPC） | 内核 `a3340d1` | `PASS` | 无 |
| 目标与会话绑定 | 状态页读取 | 一次绑定 token+服务对象；移动即 `targetChanged` 且不读文档 | `VERIFIED_SOURCE` + 判决单测 | Apple `b2596b5` | `PASS` | 真实连接计数需网络夹具 |
| 错目标请求 | NE 消息处理 | 请求的 Profile id ≠ 选中 id → 失败回执，零 stage、零写盘、零 daemon 调用 | `VERIFIED_UNIT`（规则）+ `VERIFIED_SOURCE`（接线） | Apple `b2596b5` | `PASS` | 真机点击验证 |
| 正文提交 | `RemoteRefreshApplier` + 文件 | 两段式模型下磁盘终为旧正文；单闭包下 `.superseded`、写入 0、磁盘为新正文 | `VERIFIED_UNIT` | Apple `b2596b5` | `PASS`（进程内） | 跨进程见限制 1 |
| 编辑器与刷新并发 | `Profile.saveBody` / `commitBody` | 保存落在比较与写入之间会被挡住；两种顺序都已定义 | `VERIFIED_UNIT`（机制）+ `VERIFIED_SOURCE` | Apple `b2596b5` | `PASS` | 真机冒烟 |
| 元数据所有权 | `ProfileManager.update(_:ticket:)` 事务内 | 源 + 两个存储时间戳比较；NULL 与 NULL 匹配；存储更新则让位 | `VERIFIED_UNIT` + `VERIFIED_SOURCE` | Apple `b2596b5` | `PASS`（判定 + 接线） | 真实 GRDB 写路径 `BLOCKED_BY_ENVIRONMENT` |
| Provider 刷新归属 | Applier + Profile 路径 | 只有 `.applied`/`.unchanged` 刷新；已替代/仅保存/失败一律零刷新 | `VERIFIED_UNIT` | Apple `b2596b5` | `PASS` | 无 |
| 回执解码与六入口 | `ExtensionApplyReceiptParser` + 无参 reload | 14 例覆盖空应答、恢复、中断、保留三态、他人请求、缺会话、未来版本、垃圾；六入口共用同一归属 | `VERIFIED_UNIT` + `VERIFIED_SOURCE` | Apple `b2596b5` | `PASS` | 旧扩展组合需历史产物 |
| 启动记录与内存候选 | NE 消息处理 | 应用成功后才写记录；失败出口复原 `tunnelOptions`/`overridePreferences`/locale | `VERIFIED_SOURCE` | Apple `b2596b5` | `PASS`（源码） | 真机 On-Demand 回读 |
| 平台设置安装 | 强制重建 / 代理分支 | 只在真正重开 TUN 时记录已安装设置；代理失败回传错误并回滚记录 | `VERIFIED_SOURCE` | Apple `b2596b5` | `PASS`（源码） | 真机观察实际系统调用 |
| 结果消费 | 更新快捷指令 / 编辑页 / 批量 | 除 `.applied`/`.unchanged`/`.saved` 外都抛带原因的错误 | `VERIFIED_UNIT`（映射）+ `VERIFIED_SOURCE` | Apple `b2596b5` | `PASS` | 无 |
| 跨进程正文 | 两个进程各自的提交原语 | 本机无共享容器与第二进程夹具，Darwin 锁原语不可测 | — | Apple `b2596b5` | `FAIL` | 见限制 1 |
| NE 会话代次 | 回执 `sessionID` | App 无法在发送前声明期望会话 | `VERIFIED_SOURCE` | Apple `b2596b5` | `FAIL` | 见限制 2 |

## 二、三轮复审累计发现

共 **21 条**，20 条已修并各带同入口红绿，1 条按论证改为声明修正。

| 严重度 | 缺陷（一行） | 处理 |
|---|---|---|
| BLOCKER | 扩展改为"成功也回信封"后，无参 `reloadService()` 把成功当失败，6 个存量入口成功时全部抛错 | 已修：按回执判定，六入口共用 `confirmApplied` |
| BLOCKER | 失败回执丢掉"旧文档仍在服务"，未知保留状态被显示成"隧道已停" | 已修：三态保留，nil 归为"失败但状态未知" |
| BLOCKER | `nonisolated` 方法同步调用 `@MainActor` 静态方法（编译阻断） | 已修：改 `nonisolated`，并有归约类型检查复现 |
| HIGH | 回执归属在缺字段时跳过比较 | 已修：四项全必须成立 |
| HIGH | 重建路径先关旧 Box 再验证候选 | 已修：先应用、按 `reinstalledTunnel` 决定 |
| HIGH | 正文提交是"读修订 + 写正文"两次调用，中间有 await | 已修：单闭包 + 进程内锁 |
| MEDIUM | live apply 就把新平台设置记为"已安装" | 已修：只在真正重开 TUN 时记录 |
| MEDIUM | 写后检查与 send 之间还有两个 await | 已修：检查移到 send 之前 |
| MEDIUM | 调用方 ctx 取消会关闭运行中的 Box | 已修：入口守卫 + 刷新期守卫，红绿实测 |
| MEDIUM | 启动记录写在应用之前 | 已修：应用成功后写，失败报降级 |
| MEDIUM | 状态页先取 token 后按会话重新解析（可能错标来源） | 已修：一次绑定 + `targetChanged` |
| MEDIUM | Provider 刷新在失败/已替代后仍执行（Applier 内） | 已修：策略化，只有成功才刷新 |
| MEDIUM | Provider 刷新旁路（Profile 路径三个失败分支） | 已修：策略共用，删除旧参数 |
| MEDIUM | 被拒候选留下 locale | 已修：失败出口一并复原 |
| MEDIUM | `proxyOnly` 吞掉系统错误、平台记录先改后确认 | 已修：错误回传 + 失败回滚 |
| LOW | `overridesConsidered` 无法表达全零 override | 已修：`empty_override_set` |
| LOW | 规划只看实例存在、不看已启动 | 已修：改用已启动谓词 |
| LOW | 无生产调用点的归属守卫（`stillOwns`/`generation`） | 已删：改为结构性归属 |
| LOW | 旧包装入口只比对请求 id | 已修：补 Profile 与正文归属 |
| NOTE | 真实 provider 不会产生"已发布后遇取消" | 声明修正：守卫保留，不再声称真实链路会产生 |
| MEDIUM | "正文未变化"分支对两种结果都刷新 Provider | 已修：策略共用，已替代零刷新 |

## 三、已知限制

**限制 1 · 跨进程正文（`FAIL`）**
两个进程各持一把进程内锁，只共享文件路径；同一 Profile 的两次跨进程写入若落在同一秒，后写者覆盖先写者，
两边都可能报成功。本机缺少 GRDB/共享容器、第二进程运行环境，且 Darwin 跨进程原语在 Windows Swift 上不可编译
不可测。最小下一步按风险递增：① 正文写入收敛到 App 进程；② `profiles.body_revision` 列 + 正文内容入 DB
（带旧行回填与恢复规则的协议迁移）；③ 用系统文件协调器包住两个写者（需真机或模拟器验证）。

**限制 2 · NE 会话代次（`FAIL`）**
接收端已按选中 Profile id 拒绝错目标请求，但同一 Profile 停止再连接后的新会话没有权威身份可供请求在发送前声明，
因此"旧回执被当成这次更新的确认"这一交错未被证明安全。两条路：新增一条只读查询（App 询问 NE 当前会话）并把
期望会话带进应用请求（需旧版本兼容分支），或接受现状并把该项作为已知缺陷保留、措辞不得声称会话代次已绑定。

## 四、放行结论

- 本地可执行项全部通过，五条已知反例均闭环并带红绿；跨进程正文与 NE 会话代次两项仍为 `FAIL`。
- 冻结需要领航人对上述两项给出范围取舍（批准排除并如实记录，或指定方案与责任人）；在此之前裁定是"本地可复跑、
  暂缓无条件冻结"。
- Apple 真实编译链接、真机、跨进程与端到端远端零收包独立记账，不与本地放行混算。
