# 0.1.45 协议扩展：TrustTunnel 与 AmneziaWG

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
