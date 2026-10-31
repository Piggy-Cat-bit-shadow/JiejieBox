# Mieru 协议集成（Mieru protocol integration）

**范围与证据。** 在测试源码目标 `011f3fdcd99` 上，`include/registry.go` 注册了 Mieru 入站
与出站；`protocol/mieru` 把 `github.com/enfein/mieru/v3` SDK 适配到 sing-box 的路由、
生命周期与 dialer 接口。SDK 拥有自己的协议与多路复用器；适配层拥有配置、
listener/dialer 桥接及其生命周期。这是对集成的源码级覆盖，并不是对所有
链路互通性的断言。本次校准中，选定的 fallback、分类、option 与生命周期测试
已在本地通过；它们的范围见
`ACCEPTANCE.md`。

在记录的上游祖先 `e4b7e692` 处不存在 `protocol/mieru`；本仓库的这项集成由
`3f67490206d` 引入，opt-in fallback 由 `e1efe4cafdc` 引入。底层的 Mieru SDK 是
外部依赖，因此适配层的加入不得被描述为发明了该协议本身。上游引用
可能移动；在做出更宽泛的论断之前，请复检 blob 历史。

## 配置与数据路径（Configuration and data path）

`option/mieru.go` 与 `protocol/mieru/config.go` 接受 TCP、UDP 或两者作为 **Mieru underlay**；
省略 transport 时默认为 TCP。主端口可以附带端口范围。密码与哈希后的
密码是二选一的替代关系，而 Mieru 的多路复用是 SDK 自身的机制。入站
可以同时监听两种 underlay。省略时 UDP MTU 取 SDK 的默认值；校验与确切
范围属于 `config.go`，而不属于外层 TUN 的 MTU。

出站使用 sing-box 的 dialer option 构建 server dialer 与 resolver 桥接，因此
代理服务器路径与经该协议承载的目标具有不同的所有权归属。TCP 目标连接
使用 SDK dial；UDP 目标操作则通过 UoT 适配层使用 Mieru 流
（`outbound.go`、`uot.go`、`packetconn.go`）。在假定已连接的 UDP 流与按数据报
给出的目标具有相同语义之前，请先检查确切的 `DialContext` 与 `ListenPacket` 路径。

## 可选启用的 fallback（Opt-in fallback）

`fallback.enabled` 默认关闭。启用时，backup 必须显式使用另一种单一
underlay（TCP 与 UDP 之别）；primary 或 backup 为 `both`、两者 transport 相同，以及 `no_wait` 握手，都被
拒绝。backup 的 server 与 port 可以继承 primary；仅配置了端口范围的 primary
需要一个显式的 backup 端口。凭据与其他协议设置是共享的，而不是
独立的第二个账号。`resolveFallbackOptions` 会在构建运行时之前校验这一点。

出站持有相互独立的 primary 与 secondary SDK 运行时。偏好变更会为 **新** 会话
选择 transport，其本身并不关闭既有会话。首次冷却为 30 秒，
在恢复探测不成功后增长，并以五分钟为上限；恢复是按需
驱动的，而不是后台轮询循环。selector 允许 **每个冷却窗口内仅一个并发
恢复探测**，并丢弃 stale-generation 报告；失败的探测会清除 `probing`，因此在同一代次中
经过翻倍后的冷却仍允许后续探测 —— 不存在按代次的探测上限
（`fallback.go:485-503`、`:528-538`）。当分类结果允许时，primary 可以重试 secondary 一次；
目标端的 SOCKS 拒绝、调用方取消或含义不明的 TCP EOF，都不能证明
该 transport 不健康。端点地址准备失败可以成为尝试另一个 backup 端点
的理由，而不必将 primary 网络路径标记为损坏。UDP 的 SOCKS 响应 EOF 有其自己的
分类。确切的分支与带类型的 socket 观测见 `fallback.go` 与
`dial_observation.go`；不要从分类测试推断与对端的互通性。

## 生命周期与网络变更（Lifecycle and network change）

两侧都会向各自的 scope 注册清理，并在 `scope.Add` 之前释放生命周期锁，
因为正在关闭的 scope 会同步调用延迟清理。入站启动会在注册 close 之前
启动其 accept 循环；服务器只完成部分启动时，会关闭由桥接创建的 listener。入站关闭
会取消、关闭其拥有的 listener、停止 SDK、关闭被跟踪的会话，并等待 accept 循环
与 handler —— 且仅在其声明的界限之内（`inbound.go`）。超时告警并不能证明每个
handler 都已结束。

出站关闭会阻断新的 dial，最多等待五秒让在途的建立过程完成，然后停止 SDK
运行时；超时会被记录，且不能证明其余的建立过程已经完成。`InterfaceUpdated`
在首次需求之前不做任何事；否则它会先构建并启动替换用的运行时，再
切换代次、重置 fallback 状态，并停止旧的运行时。替换失败会保留
旧代次继续在位。fallback 路径在交出结果连接之前会检查代次；
请另行检查非 fallback 的独立路径，而不要假定每条代码路径都带有
相同的交付检查（`outbound.go`）。网络切换可以终结既有的 Mieru 会话；仅仅
更改 fallback 偏好则是另一种操作。

## 验证与维护（Verification and maintenance）

- `protocol/mieru/config_test.go`：fallback 校验与 option 转换。
- `protocol/mieru/fallback_test.go`、`early_failure_test.go`：分类、回滚、冷却、
  stale-generation 与目标拒绝行为。请检查确切选中的测试与环境。
- `protocol/mieru/inbound_test.go`、`outbound_test.go`、`e2e_test.go`：生命周期与本地 SDK 路径；
  仅凭这些文件存在，并不构成它们被执行过、或与外部对端兼容的证据。
- `KNOWLEDGE/modules/path-selection-validation.md`：失败事实如何影响路由选择；
  `KNOWLEDGE/modules/naive-http-transport.md`：通用的 UoT 按数据报 guard 及其限制。

在更改 SDK pin、transport 选择或网络重建路径之前，请复检 option
校验、在途 dial 与会话的所有权、fallback 分类，以及确切的 SDK
行为。请区分本地单元测试与真实的网络切换或独立的对端测试。
