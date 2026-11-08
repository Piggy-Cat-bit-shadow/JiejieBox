# 惰性重型 Runtime：Provider 描述符拆分与 WG never-built（R03 / R04，2026-11-08）

结论：两项都按**特别大重构、收益不足**结案（`NOT_ADOPTED_WITH_EVIDENCE`，原因 `DISPROPORTIONATE_REFACTOR`）。
保留现有能力：Provider 的图校验/替换所有权、WG 的 suspend/wake、rebind、listen_port 与关闭 teardown。
本文是复开条件的依据，不是“永不实施”。

## 适用范围与 revision

- 目标：`transport/wireguard`（Endpoint/device/recovery）、`adapter/outbound/manager.go`、`adapter/outbound` 契约、
  `protocol/group` 的 Provider 成员、`route/reference.go` 的引用闭包。
- 观测 revision：阶段 04 基线 `a6f1ca5` 起的 `testing`；WG 包测试在当前 HEAD 通过（见 E23）。
- 未测：真实大配置（数百 Provider 成员）的启动内存/时间；Darwin/NE 设备行为。

## R03：轻量 descriptor 与重 Runtime 分离

**候选设计**：为每个节点保留 descriptor（Dependencies/capability/physical path/MTU/原生流能力），只在实际
需要时构建 outbound runtime；配置展示、静态校验、大库遍历只碰 descriptor。

**当前结构**（源码事实）：`adapter/outbound.Manager.Create` 构建完整 outbound 对象；Provider 的
`UpdateNodes` 同样构建对象并以 `Replace` 发布，`UpdateGroups` 之后组才看到新成员；`route` 的引用闭包遍历
`Outbounds()`/组 `Selected` 并依赖真实对象回答 `Dependencies`/capability。不存在第二层 runtime 抽象。

**成本**：需要新增 descriptor 类型并改写 `adapter.Outbound` 契约、Provider 的创建/替换/引用闭包、所有组与
`route` 的遍历；还要保持同标签替换、in-flight lease、失败发布与 `Reopen` 语义。这是一次跨 owner 的
对象模型重写，回归面覆盖所有协议。

**收益/证据**：未观察到 heavy engine 构建是启动瓶颈的证据；WG/SDK/QUIC 的重资源已由各自生命周期单独管理，
Provider 只按配置构建，不存在“遍历整个大库就建每个引擎”的现成路径。收益不确定，而正确性风险高。

**结论**：不实施。保留现有 Provider 所有权与图校验。复开条件：在真实大配置上测得 outbound 构造占启动时间/
峰值内存的显著比例（例如 >30%），且 descriptor 化能覆盖 `Dependencies`/capability 查询的绝大多数路径。

## R04：WG never-built 惰性 device

**候选设计**：构造时只保留轻量 recipe/端口描述；首次合法需求创建 device；并发首拨共享一次构建；库存状态
never-built/up/asleep/torn-down/building/closing。

**当前结构**（源码事实）：`Endpoint.Initialize` 创建 TUN device，`Start` 创建 wg device、绑定 listen_port、
注册 pause/recovery/rebind 状态；`SetIdle(true)` 只做 `wgDevice.Down()` + `suspended` 标记，`resume` 做 `Up()`；
`Close` 做设备关闭、worker 停止、端口释放。已具备：并发关闭/启动安全、teardown 幂等、listen_port 占用与释放、
rebind 恰好一个 socket、pause/wake、恢复 worker 不累积（测试见 E23）。

**成本/收益**：never-built 需要重排 `Start`（TUN、端口、路由、inbound 依赖都在 start 期），并为“首次拨号构建失败”
定义与 inbound/端口绑定一致的新失败语义；现有 suspend/wake 已经把空闲期的数据面停下，额外收益主要是未使用
endpoint 的静态内存。没有证据表明这部分是当前内存/启动瓶颈。

**结论**：不实施 never-built；`SetIdle/resume` 的现行布局视为 `ALREADY_PRESENT_VERIFIED`。
复开条件：真实配置中大量未使用 WG endpoint 的构造内存被测量为显著开销，且 TUN/端口/路由能安全延迟到首次需求。

## 保留的小改/边界

- 保留阶段 04 批 2 的共享创建与阶段 03 的字节预算，不替换任何既有 owner。
- WG 的“小 UDP 累计流量判空闲”没有被引入；suspend 只由既有 idle 策略与设备轴触发。
- 未覆盖：真实设备冷/热启动内存与首包成本（`BLOCKED_BY_ENVIRONMENT`）；Provider 大库基准（`UNMEASURED`）。
