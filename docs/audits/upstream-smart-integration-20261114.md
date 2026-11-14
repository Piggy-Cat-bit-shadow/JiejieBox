# 上游与自管依赖智能融合 · 施工审计报告（2026-11-14）

> 施工对象：`Piggy-Cat-bit-shadow/JiejieBox` 及 QUIC / TUN / WireGuard 自管依赖仓库。
> 本报告只记录本轮实际执行过的命令、实际推送的提交和实际观察到的结果；未执行的项一律按
> `未执行` 或 `BLOCKED_BY_ENVIRONMENT` 标注，不以“编译通过”代替运行证据。
> 主内核改动只存在于 `work/upstream-smart-integration-20261114` 分支；`testing` 未被本任务修改。
> 交付入口：Draft PR <https://github.com/Piggy-Cat-bit-shadow/JiejieBox/pull/8>（base `testing`，由施工方
> 创建且不合并）。集成交付提交为 `b5590ae008b8fe314f1f8217edb7d3933261e983`，评审补充与 CI 证据随后
> 追加（见 §7）；分支 head 以 PR 页面为准。

## A. 高层结论

1. **QUIC 双仓的 socket 缓冲区缺失已真实修好**，并形成“quic-go 导出 → sing-quic 入口调用”的成套 API：
   obfuscated Hysteria/Hysteria2、Realm punch conn 这类把 socket 藏在包装层后面的路径，此前
   `SetReadBuffer` 根本打不到内核 socket，QUIC 传输退化成 basicConn、缓冲区停在系统默认值；
   旧实现还会把 UDP-over-TCP 的 TCP 底座按 datagram 尺寸设置。现在由 quic-go 自己做
   `SO_TYPE` 判定 + `SO_RCVBUFFORCE/SO_SNDBUFFORCE` 回退，sing-quic 只在把 conn 交给 quic-go 的
   四个入口调用一次。
2. **`sing-tun` 的 auto-redirect 更正了四条真实缺陷**：nftables 与 nfqueue verdict 会覆盖别的程序
   的 packet mark；Android 厂商 QoS 在 mangle POSTROUTING 改写整包 mark 后 bypass NAT 被跳过；
   nftables 缺 `nft_fib_inet` / `nft_queue` / `inet nat` 时直接失败、没有受控回退；单地址族 strict
   route 会把自己的 ICMPv6/邻居发现一起 reject 成黑洞。另补齐了 4.10 前 uid 规则静默失效、
   nftables 大规则集 flush ENOBUFS、已关闭 flow 残留 drop 表项三处边角。
3. **已成熟的自研实现没有被回退**：`sing-tun` 的 2000/4096 流量测量、观察接口、首包 snapshot、
   memory TUN 每目的地队列、flow dispatcher、`goDispatch` 封装、异步 JudgeFlow、macOS utun 缓冲
   等全部保留；上游 Go 栈并发/内存重写（`2b8cf65`、`e5c595b`、`9fbd68a`）**未合并**，理由见 §5。
4. **其余自管依赖是 NO-OP**：`sing`、`utls`、`sing-anytls`、`mieru` 相对各自真实上游均 0 落后；
   `cronet-go` 的 29 个 native 模块未触碰。`wireguard-go` 出现了审计之后新增的 3 个上游修复，已
   保护 AWG 逻辑的前提下吸收其中与 device 包相关的 3 项。
5. **主内核已在新分支消费上述精确提交**：`go.mod` / `test/go.mod` / `go.sum` / `release/source-lock.json`
   同步到真实 SHA，`release-freeze.py verify` PASS；同时按上游 `fb54d9e` 修正 platform auto-redirect
   的关闭顺序，并在 `sing-tun` 侧补了“关闭可重复”的生命周期回归测试。
6. 客户端（Apple / Android / Desktop / Dashboard）**未升级**：它们落后于各自分支 tip，但属于次级
   范围且可能与其他窗口的 UI 施工冲突，按 `DEFER` 处理，见 §5。

## 1. 开工快照（2026-11-14，复核用）

| 仓库 | 授权分支 | 开工 HEAD | 当时上游基线 | 关系 |
|---|---|---|---|---|
| JiejieBox | `testing`（只读） | `c63086991b6d1d1f2e6f5d66ae5e6c4957d02179` | SagerNet/sing-box testing `3dd8aa48d403f50cced084f46b4b0d5758bd4bd1` | 上游独有 68（历史数） |
| quic-go | `sagernet` | `158445cdda77c05e4430fe40721709bf7bc2ffe2` | SagerNet/quic-go dev `60006eccf11cd9aded03fec6a334b660cc804a5d` | 上游独有 1 |
| sing-quic | `main` | `c1b074f1f86828fa968e1186ab522cc1c61aa527` | SagerNet/sing-quic main `0979e001618dbb21133a41602359a21fccf5d215` | 上游独有 1 |
| sing-tun | `dev` | `53aeb6bfa60b581fa74e9ad994eda347c5a48a11` | SagerNet/sing-tun dev `40bd6cff5647927af8db349bef9869809960a034` | fork 231 / 上游 50 |
| sing | `dev` | `4abfaed41727e43d1d98f36385a86114925f8484` | SagerNet/sing dev `27b73774b240c645099704ba533d2615727c3410` | 上游独有 0 |
| utls | `main` | `e14b88879d809306332be7d2c226384a6f3e8fba` | MetaCubeX/utls `v1.9.0-mod-meta` `89a07ad380e263f7c3b406a3f7beb2a70dea263c` | 上游独有 0（SagerNet/utls 的 6 个提交是另一条 module-rename 线，不是本 fork 的上游） |
| sing-anytls | `main` | `8473492a5afe56222de4da366c3859b560127842` | SagerNet/sing-anytls main `580984e4d8cb10f1c5e88d7ec14cfe4d955d66e6` | 上游独有 0 |
| mieru | `main` | `b5934f7136b4bfe15d0bd9611256c45b741b2d2c` | enfein/mieru main `1367cf2d2dd81c5f27d455be7cea691d7948c420` | 上游独有 0 |
| wireguard-go | `awg3-inject` | `7ece3d66d79dcea5bbf872ebe76b1c7863b69ea6` | WireGuard/wireguard-go master `b3bc704ef0ee21a36298601a9e1e5ff90377ac52` | 上游独有 3 |
| cronet-go | `main`（controlled） | `4c96192d73f67c48a91cba3bce358a3c91692444` | 源码线 main；pin 在 `go` 发布线 | 未触碰 |

`sing-tun` 的 50 个上游独有提交按 patch-id 去重后仍是 50 个不同补丁（上游 `dev` 曾重写历史，
同名补丁以不同上下文出现两次），因此按“功能是否已存在”而非提交数判断；文件级比对结果：
213 个文件与上游 `dev` 完全一致（含审计点名的 `stack_go_io_linux.go`、`stack_go_splice.go`、
`stack_go_io_darwin.go`、`tun_darwin.go`），41 个文件有差异。

## 2. 仓库交付矩阵

| 仓库 | 上游基线 | fork 开工 HEAD | 最终 HEAD | 处理 | 推送分支 | 测试证据 |
|---|---|---|---|---|---|---|
| quic-go | SagerNet/quic-go `dev` `60006ecc` | `158445cdda77` | `a99a3c489772d58b6cdd82ea70c9e194c9046563` | 修改（导出 `SetDesiredBufferSizes` + 单测） | `sagernet` | `go test -run TestSetDesiredBufferSizes` PASS（Windows）；windows/linux/darwin 交叉编译 + linux `go vet` 通过 |
| sing-quic | SagerNet/sing-quic `main` `0979e001` | `c1b074f1f868` | `08c6abd9b3fc6b2429a06337b7b49b2190aed095` | 修改（入口调用 + 去重复调用 + pin 升级 + 单测） | `main` | `go test ./...` 全包 PASS；新增 `TestSetDesiredBufferSizes*` 6 个子用例 PASS；linux/darwin 编译通过 |
| sing-tun | SagerNet/sing-tun `dev` `40bd6cff` | `53aeb6bfa60b` | `24e6bfd21ba8993e3f29376128f8a4027ab641fd` | 修改（mark/nfqueue/nftables 探测/Android connmark/strict route/内核兼容/flow 过期 + 5 个新测试，含评审补充） | `dev` | 完整 linux 测试二进制在隔离 netns（root, kernel 6.18）PASS；windows/darwin/linux 编译 + linux `go vet` 通过 |
| wireguard-go | WireGuard/wireguard-go `master` `b3bc704e` | `7ece3d66d79d` | `c308e20419f54aa0b24df9cbce5dd21ef74758b7` | 修改（device 两处 + GRO virtio header + GRO 定向测试） | `awg3-inject` | `go test ./device/` PASS（Windows 与 WSL linux）；`go test ./tun/`（linux）PASS；linux/darwin/windows 编译 + `go vet` 通过 |
| sing | SagerNet/sing `dev` `27b73774` | `4abfaed41727` | 未改动 | NO-OP（0 落后） | — | 无 |
| utls | MetaCubeX/utls `v1.9.0-mod-meta` `89a07ad3` | `e14b88879d80` | 未改动 | NO-OP（0 落后） | — | 无 |
| sing-anytls | SagerNet/sing-anytls `main` `580984e4` | `8473492a5afe` | 未改动 | NO-OP（0 落后） | — | 无 |
| mieru | enfein/mieru `main` `1367cf2d2dd8` | `b5934f7136b4` | 未改动 | NO-OP（0 落后） | — | 无 |
| cronet-go | controlled native 29 模块 / `all` | `4c96192d73f6` | 未改动 | NO-OP（受控 pin） | — | 未执行 native 重编译（不需要也不允许） |
| **JiejieBox** | 自身 | **base `c63086991b6d` (`origin/testing`)** | 集成 `b5590ae008b8fe314f1f8217edb7d3933261e983`；head 为本报告提交 | 修改（pin + 关闭顺序 + 本报告） | `work/upstream-smart-integration-20261114` | 见 §4：构建、包测试、config check、netns 端到端起停、模块完整性、release freeze verify |

**`testing` / `main` 未被本任务修改、推送或合并。**

## 3. 上游差异吸收矩阵

分类：`EXACT_BLOB` / `PATCH_EQ` / `SEMANTIC_EQ` / `NEEDS_PORT`（已移植）/ `NEEDS_FUSION` / `NOT_APPLICABLE` / `DEFER`。

### 3.1 QUIC 双仓

| 官方提交/主题 | 原本根因 | fork 现状 | 采用方式 | 最终修复与回归证据 |
|---|---|---|---|---|
| quic-go `60006ecc` 导出 `SetDesiredBufferSizes` | 包装 conn 不实现 `syscall.Conn` 时传输退化为 basicConn，socket 永不设置缓冲区 | 无该导出；只有 fork 自己 8 MiB 的 `SetReadBuffer` 写法 | `NEEDS_PORT` | 新增导出函数（含 `isDatagramSocket` 前置判定 + `setReceiveBuffer/setSendBuffer` 回退）；测试覆盖 datagram 与 TCP（非 datagram 缓冲区必须一字不变） |
| sing-quic `0979e001` 隐藏 socket 的缓冲区设置 | 同上；Hysteria xplus、Hysteria2 salamander/gecko、Realm punch conn 都在 quic-go 之前包了一层且不转发 `SetReadBuffer` | 在 3 个构造点调用 `qtls.SetDesiredBufferSizes`，对包装对象是**空操作**；且对 UDP-over-TCP 的 TCP 底座会误设 8 MiB | `NEEDS_PORT`（保留 fork 的 port hopping 逐跳调用） | 入口改为 `withSyscallConn` / `CreatePacketTransport` / `ListenWithOptions` / `ListenEarlyWithOptions` 四处；`common.Cast[syscall.Conn]` 沿 `Upstream()` 穿透 salamander/gecko/realm 包装；新增测试逐条验证穿透与“无 fd 不 panic” |
| sing-quic fork 的 port hopping 逐跳设置 | `HopConn` 没有 `Upstream()`，不参与上面的穿透 | fork 自有调用 | `NEEDS_FUSION`（保留） | 保留 `hysteria/hop.go` 两处逐跳调用；新实现下它们同样走 quic-go 的判定与回退 |

### 3.2 sing-tun

| 官方提交/主题 | 原本根因 | fork 现状 | 采用方式 | 最终修复与回归证据 |
|---|---|---|---|---|
| `010332d` mark 覆盖其他程序 | nftables 用无限定 `meta mark set` 整体覆盖；nfqueue verdict 在 nftables 模式传 mask=0 | fork 的 nfqueue 有条件合并但 nftables 路径 mask=0，nftables 全量覆盖 | `NEEDS_PORT` | 新增 `nftablesMarkMatch/nftablesMarkSet`（mask 比较 + xor 合并保留外部位）、verdict 无条件保位、iptables 恢复限制在 ORIGINAL 方向；测试固定 mask 代数（packet mark + connmark 两种寄存器） |
| `7d7649e` Android 厂商 mark 重写 | Qualcomm QoS 在 mangle POSTROUTING 覆盖整包 mark，`-m mark` 不再命中，bypass NAT/MSS 被跳过 | 仍用 `-m mark` | `NEEDS_PORT`（该补丁对 fork 完全干净） | POSTROUTING NAT/TCPMSS 改 `-m connmark`；无实机，未做 Qualcomm 专项验证（见 §5） |
| `e51634c` / `8e272a1` nft 缺 `nft_fib_inet`/`nft_queue` | 缺模块时建规则失败，auto-redirect 直接起不来或半安装 | 无探测 | `NEEDS_PORT` | `nftablesProbe` 探测 inet nat / reject-in-prerouting / fib / queue；fib 缺失回退到地址集合排除本地地址，queue 缺失 fail closed；WSL 内核 root 下真实探测通过 |
| `8855883` 缺 `inet nat` 时回退 iptables | 5.2 之前内核没有 inet nat，直接初始化失败 | 直接失败 | `NEEDS_PORT` | 仅当 `NetNs==""`、`!ExcludeMPTCP`、`iptables` 存在时回退；否则维持硬失败（不引入泄漏式降级） |
| `bcbc8d2` / `1c2bf98` 5.9 前 prerouting reject | 旧内核不支持 prerouting reject，规则安装失败 | 无 | `NEEDS_PORT` | 探测失败则改为在 input/forward 链 reject，并用 reset mark + prereouting reset-return 保持语义 |
| `5d1132d` / `7ae968b` strict route 误拒自身 ICMPv6 | 单地址族 strict route 在 route 链 reject 整个禁用族，连邻居发现和自身 ICMP 错误都被黑洞 | 无条件 reject | `NEEDS_PORT` | `nftablesUnreachableFamily()`：route 链改为 skip，仅 NAT/forward 拒绝新建连接；测试固定四种族组合 |
| `40bd6cf` / `78e734e` 4.10 前 uid 规则被忽略 | 内核接受带 uid range 的规则但静默丢弃 range，被排除用户的流量反而进隧道 | 无检查 | `NEEDS_PORT`（评审后收紧） | 安装后回读规则；无 UIDRange 或回读本身失败都启动失败（fail closed） |
| `65f9c2a` nftables 大规则集 flush ENOBUFS | nftables 库批量 flush 在 ENOBUFS 下失败 | 旧库 | `NEEDS_PORT`（依赖升级） | 升级到 `nftables v0.3.0-mod.4.0.20261114074246-0b310c3e010b`（`go.mod`/`go.sum`/`source-lock` 同步） |
| `488b53a` 关闭 flow 后丢包 | `CloseFlow` 后表项保留为 drop tombstone，同四元组新连接被丢 | 无 `aborted` 标记 | `NEEDS_PORT` | `aborted` 立即过期 + sweeper 先处理过期再 tombstone；`flow_dispatch_aborted_test.go` 固定“同四元组会重新 JudgeFlow” |
| `38f31a2` Go 栈 ENOBUFS | tun 写 ENOBUFS 被当致命错误 | 已实现 | `EXACT_BLOB` | `stack_go_io_linux.go` blob 与上游一致 |
| `49ccf0b` darwin splice | 读取语义错误 | 已实现 | `EXACT_BLOB` | `stack_go_splice.go` 一致 |
| `ec94453` tun flow 回程 | 回程未回 tun | 已实现 | `EXACT_BLOB` | 两个文件 blob 一致 |
| `a38a0ab` Android 静态路由 | 路由出现在 NetworkStack | 已实现 | `EXACT_BLOB` | `redirect_route_bypass_android.go` 一致 |
| `64e5a33` NEON 校验和 | 性能 | 已实现 | `EXACT_BLOB` | 7 个文件 blob 一致 |
| `275a476` UDP IPv4 ID | 单 ID 复用 | fork 已有 `fragmentIdentification.Add(1)` | `SEMANTIC_EQ` | 源码核对 |
| `ee2e114` 输入校验/MSS | MSS 溢出与 FIN 后投递 | fork 已有 `goNetworkHeaderLength` 夹取与 `finReceived` 守卫 | `SEMANTIC_EQ` | 源码核对 |
| `e86e58a` IPv6 扩展头 | 扩展头包进入转发器 | fork 已按扩展头丢弃 | `SEMANTIC_EQ` | `flow_parse.go` 现有 switch |
| `e466195` 零 UDP 校验和 | 校验和为 0 时按 0 发送 | fork 已有 `0xffff` 处理 | `SEMANTIC_EQ` | 源码核对 |
| `f4456b9` 网络头切片化 | 分配 | fork 已用 `network []byte` | `SEMANTIC_EQ` | 源码核对 |
| `53c746e` / `48c04ef` 校验和优化 | 分配/指令 | 校验和实现文件与上游一致 | `EXACT_BLOB` | `checksum_unsafe.go`、`tschecksum/checksum_amd64.go` 一致 |
| `fff6dbe` DNS hijack 被 input connmark 跳过 | `-p tcp` 缺失 | fork 已有 `-p tcp` | `SEMANTIC_EQ` | 源码核对 |
| `b6f3f5e` TPROXY 端口 reject | TPROXY 连接被 reset | fork 已有 tproxy connmark 判断 | `SEMANTIC_EQ` | 源码核对 |
| `1b49f15` 本机地址被捕获 | 缺 `addrtype LOCAL` | fork 已有该规则 | `SEMANTIC_EQ` | 源码核对 |
| `06ae25a` bridge 干扰 | bridge ingress 未放行 | fork 已有 `BridgeInterface` 放行 | `SEMANTIC_EQ` | 源码核对 |
| `5f28bad` / `f475e5b` Android auto redirect | Android VPNService 路径 | fork 已有 vpnservice 路由/bypass 实现 | `SEMANTIC_EQ` | 源码核对（保留 fork 版本） |
| `c2e8d1a` 关闭后悬挂连接 | 关闭顺序/连接表 | fork 已有 `goDispatch` 版本 | `SEMANTIC_EQ` | 与主内核 `fb54d9e` 配套，另加关闭可重复测试 |
| `9d81f76` `AutoRedirectOutputMarkOrDefault` | 默认 mark | fork 已有 | `SEMANTIC_EQ` | `tun.go` 现成 |
| `8f3b634` 不可恢复读错误退出 | 引擎重试策略 | fork 已有 `IsRecoverableReadError` + `ReadRetry` | `SEMANTIC_EQ` | 源码核对 |
| `d495096` forward NAT/UDP mapping/fragment | 转发 NAT 能力 | fork 已有 `udpMapping`/`fragmentTable` | `SEMANTIC_EQ` | 源码核对 |
| `25e798a` routed port | 本机栈回程端口 | fork 已有 `routedPort` 与测试 | `SEMANTIC_EQ` | 源码核对 |
| `4d196be` memory TUN 每目的地队列 | 队列 | fork 自研 `OutboundQueue` + 15 个专项测试 | `SEMANTIC_EQ`（fork 实现更新） | 保留 fork 设计 |
| `a546eff` 阻塞式入站写 | 背压 | fork 自研帧级配额与等待协议 | `SEMANTIC_EQ` | 保留 fork 设计 |
| `7aaf65c` 非阻塞 JudgeFlow | 回调阻塞 | fork 已移植（`1559fbd`） | `SEMANTIC_EQ` | 现有 `flow_dispatch_judge_test.go` |
| `7417471`/`e2779ba`/`9fbd68a`/`e5c595b`/`2b8cf65`/`b094599` Go 栈重构与批处理 | 并发/内存/重写 | fork 为自有深度改造的 Go 栈 | `DEFER`（`b094599` 的 hairpin 批处理已在 fork 的 `handleLoopbackHairpin` 中存在） | 见 §5：需要 Linux 实机 TUN 全量集成套件才能判定收益 |
| `1387ff6` lint 修正 | 风格 | 部分文件一致 | `NOT_APPLICABLE` | 不影响行为 |

### 3.3 主内核

| 官方提交/主题 | 原本根因 | fork 现状 | 采用方式 | 最终修复与回归证据 |
|---|---|---|---|---|
| `fb54d9e` platform auto-redirect 关闭顺序 | 先关 session 再关 redirect server，规则已撤但监听仍在收连接 | 仍为反向 | `NEEDS_PORT` | `common.Close(redirectServer, session)` + 注释；`sing-tun` 新增 `TestRedirectServerCloseIsRepeatable`；netns 起停实测退出码 0、无悬挂 |
| `603c46d` 并行拨号 | 官方 `N.DialParallel` | fork 已有自定义候选调度器/地址族健康/admission | `NOT_APPLICABLE`（不回退自研） | 本轮未改 |
| 其余 68 个历史独有提交（MASQUE 背压、Endpoint 恢复、FakeIP、DNS、HTTP/2 Go 兼容、Tailscale 等） | 混合 | 审计已确认对应实现存在 | `SEMANTIC_EQ`（抽样复核） | 本轮未发现需要新增的独立修复 |

### 3.4 wireguard-go

| 官方提交 | 根因 | 采用方式 | 证据 |
|---|---|---|---|
| `2631ce9` noise 双重消费 response | 两份相同 response + 期间新 initiation 会二次安装 session | `NEEDS_PORT` | `ConsumeMessageResponse` 增加 state + ephemeral 双重校验，命名返回 peer |
| `b3bc704` IPv6 载荷长度 uint16 溢出 | 64695 以上声明长度可绕过长度检查 | `NEEDS_PORT` | `length := uint32(...)` |
| `ecfc5a8` GRO 删除项 virtio header 未清零 | 被删包带着脏 virtio header 写回 TUN | `NEEDS_PORT` | 删除路径新增 `clear(...)`，其余 `hdr.encode` 改为 `clear(...)`；`tun/offload_linux_test.go` 覆盖删除路径与 passthrough，删除路径用例已用旧代码验证会失败 |

## 4. 验证结果

### 4.1 依赖仓库

| 命令 | 平台 | 结果 |
|---|---|---|
| `go test -run "TestSetDesiredBufferSizes" -v .`（quic-go） | Windows amd64 | PASS（TCP 不变、UDP 尝试且不缩小） |
| `GOOS=linux go vet ./...` / `GOOS=linux|darwin go build ./...`（quic-go） | 交叉编译 | 通过 |
| `go test ./...`（sing-quic） | Windows amd64 | 全部包 PASS（含 owned-datagram / admission / congestion 既有测试） |
| `go test -run "TestSetDesiredBufferSizes" -v ./hysteria2/` | Windows amd64 | 6 个包装链子用例 + 无 fd 用例全 PASS |
| 完整 sing-tun 测试二进制，`unshare -n` + `ip link set lo up`，root | WSL2 Linux 6.18 | **PASS**（0 FAIL，0 SKIP；1590 个 RUN） |
| 新增 sing-tun 测试：mark 掩码、不可达族、真实 nftables 探测、redirect server 关闭可重复 | WSL2 Linux 6.18 root | 全 PASS（探测确认 inet nat / reject / fib / queue 均被内核接受） |
| `GOOS=linux|darwin|windows go build ./...` + `GOOS=linux go vet ./...`（sing-tun） | 交叉编译 | 通过 |
| `go test ./device/`（wireguard-go） | Windows / WSL linux（隔离 netns） | 均 PASS |
| `GOOS=linux|darwin go build ./device/... ./tun/... ./conn/...` + `go vet` | 交叉编译 | 通过（仓库根 `package main` 缺少 main.go 为既有状态，不影响被消费包） |

### 4.2 主内核

| 检查 | 命令 | 结果 |
|---|---|---|
| 默认构建 | `go build ./...` | 通过 |
| 发布档位构建 | Windows：`go build -tags "$(DEFAULT_BUILD_TAGS_WINDOWS)" ./...`；Linux/amd64：`DEFAULT_BUILD_TAGS_OTHERS`（CGO off） | 均通过 |
| Darwin 交叉 | `GOOS=darwin GOARCH=arm64 go build -tags "$(DEFAULT_BUILD_TAGS_OTHERS)" ./...` | 通过；**完整 Darwin 档位（`with_naive_outbound` + cronet cgo）未执行 → `BLOCKED_BY_ENVIRONMENT`**（需要 macOS + cgo 工具链） |
| 包测试 | `go test ./protocol/tun/`（Windows） | PASS（4.5s） |
| Linux 包测试 | 交叉编译 `protocol/tun` 测试二进制，在隔离 netns 运行（root） | PASS，含 3 个“pinned-source tripwire”用新 sing-tun 源码做断言 |
| 配置校验 | Linux 二进制 `sing-box check -c config.json`（tun + auto_redirect + strict_route） | 退出码 0；顺带验证旧字段（inbound 内 `sniff`）被正确拒绝 |
| 端到端起停 | 隔离 netns：`sing-box run` → 6s → SIGTERM | 双栈/v4-only/v6-only 三个用例：`tun0` 建立，策略路由 9000/9001/32768 与 table 2022 默认路由存在，SIGTERM 后退出码 0、无悬挂；auto-redirect 初始化无回退告警（WSL 无 iptables，必然走 nftables） |
| 模块完整性 | 4 个 Go module 的 `go mod tidy -diff` + `go list -mod=readonly -m all` | 全部 clean / resolves |
| 版本锁 | `release-freeze.py resolve/check/snapshot/verify` | `check`：8 个 Go 模块 pin 与分支 tip identical，cronet ahead（受控），客户端 behind（未动）；`verify`：**PASS**（每个 pin 与锁一致） |
| 真实 replace 解析 | `go list -m` 四个模块 | `github.com/sagernet/quic-go => .../quic-go v0.61.1-0.20261114142722-a99a3c489772`、`sing-quic v0.7.2-0.20261114143018-08c6abd9b3fc`、`sing-tun v0.9.7-0.20261114160640-24e6bfd21ba8`、`wireguard-go v0.0.0-20261114161408-c308e20419f5` |
| PR #8 CI 门禁 | `verify` / `race checks` / `linux-kernel-verify`（awg、linux-process、trusttunnel）/ `Release contract verification` / `Maintenance ledger` | 首轮：除 `verify` 的 pin-runs 步骤外全部通过；该步骤的修复见 §4.4，修复后本地 `check-pin-runs.py` 与 20 项自测均 PASS |

### 4.3 未执行及原因

| 项 | 原因 |
|---|---|
| macOS / iOS / Android 原生构建与设备回归 | 当前环境为 Windows + WSL，无 Xcode / Android SDK / 真机 → `BLOCKED_BY_ENVIRONMENT` |
| Android/Qualcomm 实机 connmark 验证 | 无设备/root 环境；只做规则生成与源码级验证 |
| iptables 回退路径的实际安装 | WSL 未安装 iptables；只验证了“不满足条件时 fail closed”的判定与源码路径 |
| 新 pin 的 push 事件 CI 运行记录 | PR 分支上的 `linux-kernel-verify`（43610259083）与 `Release contract verification`（43610259097）已在同一 pin 集上真实成功，并记入 `release/pin-runs.json`；`testing` 合并后的首个 push run 仍应补记——合并前无法触发该事件 |
| `verify-upstream-assumptions.sh` | 需要 bash + go + gh + jq 的完整环境；未执行，标注为未覆盖门禁 |
| `gvisor` 相关 tag 构建 | 该线已退役（`with_gvisor` 不再进入产物），按仓库既定策略不构建 |
| 性能/吞吐对照 | 本任务不伪造 benchmark；无同平台同负载对照数据 |

### 4.4 `verify` 的 pin-runs 步骤（PR #8 首轮唯一失败）

首轮失败信息是 `no recorded run carries the current pin set` 与四条
`the observation is about pin <old> but the lock now pins <new>`。根因是 `release/pin-runs.json`
仍然描述旧 pin：pin 移动后，`check-pin-runs.py` 要求至少一条**已完成且成功**的运行，其提交上的
`release/source-lock.json` 与当前锁具有相同 pin 投影；同时 `observed_tips` 的 `pin` 必须与锁一致。
修复内容：

- 记入同一分支、同一提交（`99bdccbe`）上真实成功的两个 PR 运行：`linux-kernel-verify`
  43610259083（真正构建并运行了内核）与 `Release contract verification` 43610259097；
- 把 `observed_tips` 更新为 2026-11-14 的实测值：quic-go / sing-quic / sing-tun / wireguard-go
  的 pin 与 tip 一致；apple 记为 `behind`（tip `ef37275`）、android 为 `identical`、cronet-go 为
  `ahead`（发布提交是 main tip 的后代）。
- 本地复核：`python scripts/ci/check-pin-runs.py --json build/pin-runs.json` → **PASS**
  （`every pin in the lock is carried by an executed run at the same pin set (43610259083, 43610259097)`），
  且 `python scripts/ci/test-check-pin-runs.py` → **20 passed / 0 failed**。

## 5. 剩余问题与风险

1. **Android 厂商 mark 重写（connmark 改动）无实机证据**（P2）。规则语义与上游一致，但 Qualcomm
   `qcom_qos_reset_POSTROUTING` 的实际行为只能在真机复现；建议在具备 root 的 Android 设备上跑一次
   `auto_redirect` + 分流回程的专项验证。
2. **iptables 回退路径未被实际执行**（P2）。探测与 fail-closed 条件已测，但真正安装 iptables 规则
   的路径只能在没有 nftables 的老内核上跑；建议在 5.4/4.19 内核+iptables 环境补一次。
3. **Go 栈上游重写未合并**（P3）：`2b8cf65`（连接并发）、`e5c595b`（go stack 修复）、`9fbd68a`
   （内存重构）。这些是针对 fork 已经深度改造过的热路径的大规模重写，缺少可判定的缺陷复现与
   Linux 实机全量集成套件，按 `DEFER` 处理；若后续要吸收，应在 fork 的 Go 栈上单独一轮，
   以 `stack_go_integration_*` 全量套件 + 2000/4096 流量测量作为门槛。
4. **客户端落后**（P3）：`clients/apple` 落后 1 个提交、`clients/desktop` 落后（含嵌套 dashboard）。
   本轮不动，避免与其他窗口的 UI 施工冲突；按 `Dashboard → Desktop → kernel` 顺序单独收尾。
5. **新 pin 的 CI 证据来自 PR 运行**（P3）：`release/pin-runs.json` 现在记录 PR #8 的
   `linux-kernel-verify` 与 `Release contract verification`（head `99bdccbe`，两者都在该 pin 集上真实成功）。
   合并后建议用 `testing` 的首个 push run 追加覆盖；若采用 squash merge，被引用的 PR 提交将不再可达，
   `check-pin-runs.py` 会因读不到该提交而失败，需要把记录改指向合并提交。
6. **Darwin 完整档位未编译**（P3）：需要 macOS + cgo；建议在 Apple 构建机上跑
   `build-apple-libbox.sh` 的 tag 档位确认 cronet/naive 分支。

## 6. 施工边界核对

- [x] 主内核 `testing` / `main` 没有被修改、推送或合并（`origin/testing` 仍为 `c6308699`）。
- [x] 主内核新分支由开工快照 `c6308699` 创建（独立 `git worktree`），成果只在该分支及其 Draft PR。
- [x] 依赖仅在指定产品分支直接施工（`sagernet` / `main` / `dev` / `awg3-inject`），未触碰其他分支。
- [x] 无 `push --force`、无 `reset --hard`、无 `git clean`、无清理或覆写用户未提交改动
      （用户 `testing` 工作目录的 6 个未提交文件全程未被触碰）。
- [x] 未修改 native Cronet 受控版本，未创建 tag / release，未改 `release/JIEJIE_VERSION`。
- [x] 已成熟的 MASQUE、FakeIP、TUN、AnyTLS、QUIC、iOS 资源治理、memory TUN、flow dispatcher 等
      实现未被粗暴回退。
- [x] 所有测试、锁文件、pin 与远端 HEAD 的说明都有实际命令输出支撑；未执行项已逐条标注。
- [x] 未执行自动发布，未合并主内核 PR。

### 复现入口

```bash
# 依赖
cd sing-tun && GOOS=linux go test -c -o /tmp/singtun.test . \
  && unshare -n bash -c 'ip link set lo up; /tmp/singtun.test'   # root
cd quic-go && go test -run TestSetDesiredBufferSizes -v .
cd sing-quic && go test ./...
cd wireguard-go && go test ./device/
cd wireguard-go && GOOS=linux go test -c -o /tmp/wg-tun.test ./tun/ \
  && /tmp/wg-tun.test -test.run TestHandleGROClears -v   # Linux

# 主内核（新分支）
go build ./... && go build -tags "$(cat release/DEFAULT_BUILD_TAGS_WINDOWS)" ./...
go test ./protocol/tun/
python scripts/ci/release-freeze.py verify --lock release/source-lock.json
```

## 7. 评审反馈与处置（2026-11-15）

外部评审提出 3 项，全部在本轮落实；三项都先复现“没有修复时的失败”，再验证修复后通过。

| 级别 | 评审意见 | 处置 | 证据 |
|---|---|---|---|
| P2 | `flow_dispatch` 的 aborted 修复缺少专门回归测试 | **已补** `flow_dispatch_aborted_test.go`：建立 flow → `CloseFlow()` → 同四元组再发包 → 断言 `JudgeFlow` 第二次执行且安装了新 flow；同时断言 abort 后表项仍在（正是旧代码回包的状态） | 旧代码下失败：`the aborted flow swallowed the new connection: JudgeFlow ran 1 times`；修复后 PASS（Windows + linux 全量套件） |
| P2 | `UIDRange` 回读失败（`listErr != nil`）时仍继续启动 | **已收紧为 fail closed**：回读失败返回错误，隧道不启动；只有带 UIDRange 的规则才会触发回读，未配置 UID 规则的场景不受影响 | `tun_linux.go` 改动 + 注释说明；行为无法在无 root 的 Windows 上单测，已在报告标注为源码级验证 |
| P3 | WireGuard GRO 清理缺少针对性测试 | **已补** `tun/offload_linux_test.go`（linux）：构造带 virtio headroom 的合法 IPv4/TCP 段，一个用例覆盖“校验和无效被踢出合并表”的删除路径，另一个覆盖非 GRO 候选的 passthrough 路径 | 删除路径用例在无 `clear()` 的旧代码下失败：`the virtio header still carries abababababababababab after GRO`；修复后两个用例 PASS |

由此产生的 pin 变化：`sing-tun dev` → `24e6bfd`（新增测试 + UIDRange 收紧），`wireguard-go awg3-inject`
→ `c308e20`（GRO 测试）。两者的 `go.mod` / `test/go.mod` / `go.sum` / `release/source-lock.json`
已同步，`release-freeze.py verify` 仍为 PASS。

> 注：`tun/offload_linux_test.go` 里 passthrough 用例在旧代码下也能通过——旧实现用“编码一个零值
> virtioNetHdr”达到同样效果，该用例守的是不变量而不是本次改动，保留它是为了防止以后把 `clear()`
> 改回去时静默退化；真正修复的是删除路径，其用例已用旧代码验证会失败。
