# 0.1.45 协议扩展：TrustTunnel 与 AmneziaWG

## 0.1.46 TrustTunnel 回退增量（H3 → H2 fallback）

本节固定目标 `c795bb5cbbcd9b6a4d2d2f18e854b629915335f5`，下方 0.1.45 / AWG 章节保留原范围。`VERIFIED_SOURCE`：`option/trusttunnel.go`、`protocol/trusttunnel/outbound.go`、`transport/trusttunnel/client.go` 与 `client_quic.go`。

- `quic_fallback_to_h2` 默认关闭，要求 `quic: true`、QUIC 构建和双 ALPN。H3 建立尝试上限 3 秒，整体创建期限更短时取更短值；失败后 H2 使用剩余期限。
- 可回退的建立失败设置 30 秒冷却，即使 H2 也失败仍保留 H3 判断；冷却内新会话直接 H2。过期后真实需求通过 `recoverableFallbackSession` 退休回退 H2 会话，再试 H3，旧业务继续完成。不是每次请求都等待 3 秒，也不是定时重连。
- `cordonForGeneration` / `clearCordonForGeneration` 在锁内核代次；Reset 清状态，旧失败与旧成功均不可修改新代判断。TLS alert（含 QUIC CRYPTO_ERROR）、证书拒绝、取消不触发回退；CONNECT 认证和目标拒绝不在传输建立重试范围。
- 会话创建仍共享一次准入；不把回退写到每条 stream 或 UDP 包上，不能重放业务字节。配置与完整边界见[出站说明](../../docs/configuration/outbound/trusttunnel.zh.md)。

### 来源与被替代尝试（Provenance）

| 提交 | 作用 |
| --- | --- |
| `d5949d749` | fork 自有回退实现；配置、会话预算、冷却和按需恢复 |
| `e15e335ed` | QUIC 接受用例受 `with_quic` 约束，避免无标签假覆盖 |
| `8789a0de6`、`c5e5a5c0f` | 修初版 TLS 误分类和跨网络代次写入窗口，识别 QUIC TLS alert；仅检查 context 后写状态的做法已被替代 |
| `ce8c07e10` | 真实 TCP/UDP relay 黑洞与带 QUIC 的 Linux CI 接线 |

### 验证与维护注意（Verification / AI cautions）

`VERIFIED_CI`：[Linux kernel verification 43391142474](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43391142474) 在 `ce8c07e10621d9de0570b483a6e9775b1a257e10` 的 `real relay black hole falls back to HTTP/2 (with_quic)` 步骤成功；TrustTunnel 相关源码及该 workflow 至正式 tag 无变化。[正式运行 43404981823](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/actions/runs/43404981823) 另在本节固定 SHA 成功，不把两条运行混为同一证据。复检：`go test -tags with_quic ./option ./protocol/trusttunnel ./transport/trusttunnel`。`client_fallback_relay_test.go` 使用真实 QUIC/TCP relay，但缩短冷却用于测试，不是公网 30 秒实测或真机结果。不要把 Naive/通用 HTTP 的请求重放规则套到这里；不要让 TLS 明确拒绝进入冷却，也不要省掉成功路径的代次核对。

固定源码目标：`3032ca5199dbd151be0b2cd006fba191b87cc1e8`。来源：fork 自有协议实现与依赖 fork 消费；本页只讨论其相对既有协议框架的增量。

## TrustTunnel

`option/trusttunnel.go` 定义入站和出站；`include/trusttunnel.go` 注册 build tag。`protocol/trusttunnel/outbound.go` 要求 TLS 与凭据，H2/TCP 和 H3/QUIC 共享外层会话，业务 TCP stream 和 UDP `_udp2` association 各自保留边界。入站 network 选择监听传输，出站 network 选择业务类型。其 DNS、Dialer、路由、traffic class、GenerationGate 和资源 guard 与既有 owner 接合。相同会话的等待者共享创建，退出的等待者不取消仍有人需要的工作。服务端授权与 UDP 关联归服务端 owner 清理。

## AWG

AWG 通过 `type: "wireguard"` endpoint 根层字段配置，不新增 `type: "awg"`。`option/wireguard_awg.go` 承载 jc/jmin/jmax、s1–s4、h1–h4、i1–i5、声明式伪装、header_protection_key、content_padding_addition、随机尾部及握手时间选项。客户端与 Linux responder 通过 pinned wireguard-go fork 消费相同 Device/peer 模型。未带 `with_awg` 的构建明确拒绝 AWG 配置。并发发送修复位于 wireguard-go fork；主仓 pin 与 release/source-lock.json 要一致。

## 协议性能与优化的证据边界

`docs/final-audit/closure/WG-AWG-B7.md` 的 WG/AWG 并发修复是 old/fixed 对照：旧 pinned wireguard-go 在部分同机多流上行组合停滞，新 pin 修复 outbound-space 唤醒后，WG 四流零间隔为 49.8–54.8 MiB/s，AWG 同组为 41.5–53.8 MiB/s；36/36 矩阵完成。不能对停滞旧样本计算“无限倍”提升。

`docs/final-audit/PERFORMANCE.md` 的协议台架是最终实现的 **1→4 并发流**表现，不是两个版本的前后速度：Vision 上传 264.7→809.1、REALITY 266.2→814.2、AnyTLS 255.9→769.4、TrustTunnel H2 172.5→213.5、H3 145.7→202.2、XHTTP H2 32.7→126.3、H3 32.8→127.4、Mieru TCP 122.6→483.1 MiB/s。四流客户端 CPU 成本另以 s/GiB 计，不可从吞吐直接推出节能。条件为 WSL2 同机 loopback、真实客户端/服务端进程、5 秒载荷、多次运行中位数；不能外推到公网或手机。

协议侧的持续优化还包括 sing-quic/quic-go 的 ACK-only pacing、uTLS ClientHello QUIC 参数接线、WireGuard/AWG 唤醒、AnyTLS 新建与复用分开计费、Mieru underlay close/attach 串行化。每项都须核对父仓 `go.mod` pin 所消费的 fork；更新 fork 分支本身不等于产品获得修复。

## 证据与维护注意

源码及协议包测试确认入口和协议接线；官方双向互通、WG/AWG 36/36 并发台架见 `docs/final-audit/closure/` 历史记录。台架结果不证明所有平台设备。不能把普通 WG 的所有能力自动推断为 AWG 3.x 支持，也不能把 `network` 入站与出站的含义混用。
