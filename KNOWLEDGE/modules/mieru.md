# Mieru 协议集成（Mieru protocol integration）

**范围与证据。** 测试源码目标为 `011f3fdcd99`。在这个目标上，`include/registry.go` 注册了 Mieru 入站
与出站；`protocol/mieru` 把 `github.com/enfein/mieru/v3` SDK 适配到 sing-box 的路由、
生命周期与 dialer 接口。SDK 自带协议与多路复用器；适配层负责配置、
listener/dialer 桥接以及它们的生命周期。这是源码级的集成覆盖，并不断言所有链路都能互通。
本次校准中，选定的 fallback、分类、option 与生命周期测试已在本地通过；覆盖范围见
`ACCEPTANCE.md`。

记录的上游祖先 `e4b7e692` 处没有 `protocol/mieru`；本仓库的集成由 `3f67490206d` 引入，
opt-in fallback 由 `e1efe4cafdc` 引入。底层 Mieru SDK 是外部依赖，因此不能把适配层的
加入说成发明了该协议本身。上游引用可能移动；在下更宽泛的结论之前，请复检 blob 历史。

## 配置与数据路径（Configuration and data path）

`option/mieru.go` 与 `protocol/mieru/config.go` 接受 TCP、UDP 或两者作为 **Mieru underlay**；
省略 transport 时默认使用 TCP。主端口可以附带端口范围。密码与哈希后的密码二选一；
Mieru 的多路复用由 SDK 自身提供。入站可以同时监听两种 underlay。省略 UDP MTU 时取
SDK 默认值；校验规则与确切范围在 `config.go` 中，不属于外层 TUN 的 MTU。

出站用 sing-box 的 dialer option 构建 server dialer 与 resolver 桥接，因此代理服务器路径
与经该协议承载的目标分属不同的所有权。TCP 目标连接走 SDK dial；UDP 目标则经 UoT 适配层
使用 Mieru 流（`outbound.go`、`uot.go`、`packetconn.go`）。在假定已连接的 UDP 流与按数据报
给出的目标语义相同之前，请先检查 `DialContext` 与 `ListenPacket` 的确切路径。

## 可选启用的 fallback（Opt-in fallback）

`fallback.enabled` 默认关闭。启用后，backup 必须显式使用另一种单一 underlay（TCP 或 UDP）；
以下配置都会被拒绝：primary 或 backup 为 `both`、两者 transport 相同、`no_wait` 握手。
backup 的 server 与 port 可以继承 primary；primary 只配置了端口范围时，必须给出显式的
backup 端口。凭据与其他协议设置由主备共享，不是第二个独立账号。`resolveFallbackOptions`
在构建运行时之前完成校验。

出站持有相互独立的 primary 与 secondary SDK 运行时。偏好变更会为 **新** 会话选择 transport，
本身并不关闭既有会话。首次冷却为 30 秒；恢复探测不成功后冷却增长，上限五分钟。恢复按需
触发，而不是后台轮询。selector 在 **每个冷却窗口内只允许一个并发恢复探测**，并丢弃
stale-generation 报告；探测失败会清除 `probing`，因此同一代次内冷却翻倍后仍允许后续探测
—— 每个代次没有探测次数上限（`fallback.go:485-503`、`:528-538`）。分类结果允许时，
primary 可以重试 secondary 一次；目标端 SOCKS 拒绝、调用方取消或含义不明的 TCP EOF，
都不能证明该 transport 不健康。端点地址准备失败可以成为尝试另一个 backup 端点的理由，
不需要把 primary 网络路径标记为损坏。UDP 的 SOCKS 响应 EOF 有自己的分类。确切分支与
带类型的 socket 观测见 `fallback.go` 与 `dial_observation.go`；不要从分类测试推断与对端的
互通性。

## 生命周期与网络变更（Lifecycle and network change）

两侧都向各自的 scope 注册清理，并在 `scope.Add` 之前释放生命周期锁，因为正在关闭的 scope
会同步调用延迟清理。入站启动时先启动 accept 循环，再注册 close；服务器只完成部分启动时，
会关闭桥接创建的 listener。入站关闭会取消、关闭自己拥有的 listener、停止 SDK、关闭被跟踪
的会话，并等待 accept 循环与 handler —— 但只在声明的界限之内（`inbound.go`）。超时告警
不能证明每个 handler 都已结束。

出站关闭会阻断新的 dial，最多等五秒让在途建立完成，然后停止 SDK 运行时；超时会被记录，
但不能证明其余建立过程已经完成。`InterfaceUpdated` 在首次需求之前什么都不做；否则它先
构建并启动替换运行时，再切换代次、重置 fallback 状态，并停止旧运行时。替换失败时，旧代次
继续在位。fallback 路径在交出结果连接之前检查代次；非 fallback 的独立路径需要另行检查，
不要假定每条代码路径都有相同的交付检查（`outbound.go`）。网络切换可以终结既有的 Mieru
会话；只改 fallback 偏好则是另一种操作。

## 验证与维护（Verification and maintenance）

- `protocol/mieru/config_test.go`：fallback 校验与 option 转换。
- `protocol/mieru/fallback_test.go`、`early_failure_test.go`：分类、回滚、冷却、
  stale-generation 与目标拒绝行为。请检查确切选中的测试与环境。
- `protocol/mieru/inbound_test.go`、`outbound_test.go`、`e2e_test.go`：生命周期与本地 SDK 路径；
  这些文件存在，并不能证明它们被执行过，也不能证明与外部对端兼容。
- `KNOWLEDGE/modules/path-selection-validation.md`：失败事实如何影响路由选择；
  `KNOWLEDGE/modules/naive-http-transport.md`：通用的 UoT 按数据报 guard 及其限制。

改动 SDK pin、transport 选择或网络重建路径之前，请复检 option 校验、在途 dial 与会话所有权、
fallback 分类，以及 SDK 的确切行为。请区分本地单元测试与真实网络切换或独立对端测试。
