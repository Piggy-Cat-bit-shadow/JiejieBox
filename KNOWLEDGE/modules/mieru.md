# Mieru 协议集成（Mieru protocol integration）

**范围与证据。** 以下描述针对源码版本 `011f3fdcd99`。该版本的 `include/registry.go` 注册了
Mieru 入站和出站；`protocol/mieru` 将 `github.com/enfein/mieru/v3` SDK 接入 sing-box 的
路由和生命周期。协议及多路复用由 SDK 提供，适配层负责配置、listener、dialer 和资源管理。
这些源码只能说明集成方式，不能证明所有网络环境都可互通。校准时，本地运行了部分 fallback、
错误分类、配置和生命周期测试；具体范围见 `ACCEPTANCE.md`。

记录的上游祖先 `e4b7e692` 不包含 `protocol/mieru`。本项目在 `3f67490206d` 引入适配层，
又在 `e1efe4cafdc` 增加了需要显式开启的 fallback。Mieru SDK 是外部依赖，本项目新增的是
集成代码，而非协议本身。上游引用可能变化，扩大归因范围前应重新检查源码历史。

## 配置与数据路径（Configuration and data path）

`option/mieru.go` 与 `protocol/mieru/config.go` 接受 TCP、UDP 或两者作为 **Mieru underlay**；
省略 transport 时默认使用 TCP。主端口可以附带端口范围。密码与哈希后的密码二选一；
Mieru 的多路复用由 SDK 自身提供。入站可以同时监听两种 underlay。省略 UDP MTU 时取
SDK 默认值；校验规则与确切范围在 `config.go` 中，不属于外层 TUN 的 MTU。

出站根据 sing-box 的 dialer 选项构建到代理服务器的 dialer，并接入 resolver；代理服务器的
连接路径与通过 Mieru 访问的目标需要分别考虑。TCP 目标通过 SDK 拨号，UDP 目标则通过 UoT
适配层使用 Mieru 流（`outbound.go`、`uot.go`、`packetconn.go`）。已连接的 UDP 流和逐数据报
指定目标的语义不一定相同，修改前应分别检查 `DialContext` 与 `ListenPacket`。

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

入站和出站分别向各自的 scope 注册清理函数，并在调用 `scope.Add` 前释放生命周期锁：
如果 scope 已开始关闭，清理函数可能立即在调用方同步执行。入站先启动 accept 循环，再注册
关闭函数；部分启动失败时会关闭桥接层创建的 listener。关闭入站时会取消相关工作、关闭其
listener 和已跟踪会话、停止 SDK，并在规定时限内等待 accept 循环和 handler（`inbound.go`）。
出现等待超时告警时，不能认为所有 handler 都已退出。

出站关闭后不再接受新的拨号，最多等待五秒让正在建立的连接完成，然后停止 SDK 运行时。
等待超时会被记录，但不能说明剩余连接已经建立或退出。首次使用之前，`InterfaceUpdated`
不会重建运行时；使用之后收到更新时，它先构建并启动新运行时，再切换 generation、重置
fallback 状态并停止旧运行时。如果构建失败，继续使用旧 generation。

fallback 路径在返回连接前会检查 generation；不经过 fallback 的路径需要单独核对，不能假定
它们都有相同检查（`outbound.go`）。网络切换可能终止现有 Mieru 会话；单纯更改 fallback 偏好
则不会因为这一操作主动关闭既有会话。

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
