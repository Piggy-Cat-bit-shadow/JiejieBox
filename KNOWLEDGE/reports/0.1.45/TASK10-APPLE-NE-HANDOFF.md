# 任务 10 · 施工与交接（脱敏合并版）

对象：Apple NetworkExtension 与 Libbox 命令接口。合并了施工交接报告与终轮 01/02 结论，去除重复的阶段叙述，
保留修复内容、失败时序、证据与剩余限制。

## 一、候选

| 仓 | 分支 | 起点 | HEAD | 提交 |
|---|---|---|---|---|
| 内核（JiejieBox） | `fix/task10-apple-ne-runtime-final` | `efd8d3b2eae1ee35c6b71a7b296609fd922ae31d` | `a3340d1a759d520d4a4b9d1730db5ca0add52cc6` | 8 |
| Apple 客户端 | `fix/task10-apple-ne-runtime-final` | `40e59c767085a1cee033177813f8b40e2bde06e7` | `b2596b50fb6ae0592466fc41ae481a3999d2de6f` | 15 |

两分支均已推送。工作树干净，`git diff --check` 通过，GitHub Actions 用量 **0 次**（未 dispatch、未 rerun）。

## 二、修了什么（按链路）

### 1. 命令目标与本地数据边界

- 每个命令显式声明目标（本地隧道 / 远端会话）；本地正文的读与发都在"允许本地"的判定之后。
- 状态页把"会话目标"与"该目标的一份读取"绑定：一次绑定同时持有 token 与服务对象，客户端在同一把锁内按该绑定
  解析；会话在两者之间移动时返回 `targetChanged` 并**不读任何文档**，页面明说"什么都没读"，不渲染另外三行。
- 全仓 client 解析点按类别核对：会话语义的操作跟随会话，Profile 写入与预览钉死本地，无第二条旁路。

### 2. Profile 更新：一次提交、可归属的回执

- 正文提交是一次操作（读-比较-写在同一临界区 + 进程内锁），替代原先"读修订闭包 + 写正文闭包"的两段式；后者在
  并发更新下会让慢的那次覆盖快的，并各自报成功。
- 编辑器保存与远端刷新共用同一把正文锁（编辑器保存是无条件语义：那是用户的决定）。
- 回执按四项归属：请求 id、Profile id、调用方记录的正文修订、回执回传的正文修订；失败保留状态是三态
  （旧文档仍在 / 服务已停 / 未知），未知不得显示成"隧道已停"。
- 元数据所有权在 SQLite 写事务内比较：源 + 两个**存储值**时间戳（NULL 保持 nil）。`Profile.lastUpdated` 的
  `Date()` 默认只用于展示，不参与比较——否则从未刷新过的老行每条都会被误判为"别人已更新"。
- Provider 刷新归属收成一条策略：只有文档真正到达运行实例的更新才刷新 Provider。已替代、仅保存、失败、恢复、
  中断、未确认一律零刷新（中途发现的两条旁路已封：Profile 路径的三个失败分支、以及"正文未变化"分支）。

### 3. NE 应用与启动记录

- 扩展对**每条出口**都回版本化信封（含失败），成功也回；无参 `reloadService()` 按回执判定，不再"回包非空即失败"。
- 应用先做一次、读回执里的 `reinstalledTunnel` 再决定是否强制重建；重建只发生在内核未重开 TUN 的两个状态，
  避免"先关旧 Box 再发现候选不可用"。
- 启动记录在**应用成功之后**写；写失败报 `persistence: degraded`，不让失败候选成为下次 On-Demand 的输入。
- 被拒候选不留下任何状态：`tunnelOptions`、`overridePreferences` 与内核 locale 都在失败出口复原，成功才提交。
- 接收端按"选中的 Profile id"拒绝错目标请求，在 stage 任何东西之前；规则是纯函数并有单测。

### 4. Libbox Provider 刷新

- 调用方的取消与 deadline 进入真实 HTTP 请求；同时保留 Box 的 build gate 与 Provider 生命周期（`Close` 能取消在意下载）。
- 取消检查放在**第一次不可撤销发布之前**（`UpdateNodes`），解析阶段的取消不再导致整份文档被发布并报 `committed`。
- 本次报告由产生它的那次 fetch 返回；全局"最近报告"只作历史查询，拒绝的尝试不会捡到别人的发布。
- 真实 gRPC 下旧 daemon 缺该 RPC → `{"supported":false}` 且 `err == nil`；服务在但未启动 → 明确错误。

## 三、失败时序（最危险的几个）

| 交错 | 未修时 | 现在 |
|---|---|---|
| A1 比较通过后暂停，A2 写入并应用，放行 A1 | A1 报成功、磁盘终为旧正文 | A1 `.superseded`，写入 0、重载 0、Provider 刷新 0 |
| 编辑器保存落在刷新的"比较后、写入前" | 刷新覆盖保存，双方都报成功 | 两者共用一把锁，顺序确定：保存先到 → 刷新让位；刷新先到 → 保存胜出 |
| 解析期间取消调用方 | 整份文档发布、`committed`、`err=nil` | 发布前检查拦截：`unknown` + 取消原因，节点未变 |
| 调用方带 `Background` 手动刷新 | Box 的 build gate 被绕过、`Close` 取消不了下载 | 请求从调用方派生 + gate 携带 + 生命周期绑定 |
| 失败候选之后调无参 reload / On-Demand | 内存与下次启动用被拒的 B | 失败出口复原，磁盘与内存都还是 A |
| 老行（NULL 时间戳）首次刷新 | 被判"别人已更新" | NULL 与 NULL 匹配，正常提交 |

## 四、证据

- 内核：`go build ./...` 通过；`daemon`/`libbox`/`provider/*` 全量通过；`-race` 在 daemon、libbox、provider/remote 上通过
  （mingw gcc 16.2.0）。
- Apple：契约包 **78 例 0 失败**、既有回归 **129 例 0 失败**（Windows 上把 SwiftPM 符号链接源物化后实跑）；
  `swiftc -parse` 全过；结构/命令通道/平台结构三个静态门通过。
- 红→绿：Provider 预算（30.05s 失败 → 0.15s 通过）、写前 CAS、取消守卫、两段式提交模型、gate 携带、
  生命周期绑定、发布边界、接收端准入、元数据 NULL、Provider 刷新策略，各自都有"删掉检查即变红、恢复即变绿"的记录。
- 归约类型检查：`nonisolated` 方法同步调用 `@MainActor` 静态方法在 Swift 6 下报
  "main actor-isolated static method cannot be called from outside of the actor"；改 `nonisolated` 后通过。

## 五、未验证（外部）

1. Apple 真实编译与链接（需要配对的 `Libbox.xcframework`，一次 iOS-only dispatch 会排 `libbox` 与 `ios` 两个 macOS job）。
2. 真机：APNs 与同正文热刷、On-Demand 回读、接管等待、编辑器与刷新并发。
3. App/Intents 真实跨进程、端到端远端零收包。
4. 跨进程正文与 NE 会话代次两项，见 `复审裁定表.md` 的已知限制。
