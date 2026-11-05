本版增加 iOS 自签安装与本地回环，改善突发建连时的内存保护，同步 NaiveProxy patch5，并修复 Linux 自定义 DNS、设备切网及 Apple 客户端连接问题。

## 自签安装与客户端连接

- 新增完整未签名 IPA，可自行签名安装；应用与 Packet Tunnel 扩展同步写入版本。
- 适配重签后的团队身份、App Group 与扩展标识，按实际授权选择共享容器，修复自签后启动失败、隧道已连接但日志和统计不可读的问题。
- 缩短 libbox 命令 socket 路径，统一客户端与服务端的路径选择，修复容器路径过深导致连接失败的问题。
- 增加 VPN 已连接但 TUN 未完成接管的诊断，便于区分界面状态与实际网络接管。

## 本地回环

- 新增“更多 → 连接行为 → 配置覆盖 → 本地签名回环”开关，默认关闭；开启后注入 `10.7.0.1` 并配置 iOS 主机路由，保留已有地址和订阅文件。
- 默认 `go` 栈补全 UDP、ICMPv4/v6 反射，修复 IPv4-mapped 地址匹配；其他显式栈提供不支持提示。
- 当前真机反馈：WLOC 开发者模式虚拟定位可用，SideStore 续签仍有兼容问题，续签请使用 LocalDevVPN。详见 [回环说明](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/IOS-SELFSIGN-AND-LOOPBACK.md)。

## 突发建连与低内存保护

- 为 iOS Network Extension 的 AnyTLS TCP/UoT 和共享 TLS 握手增加创建准入，限制并发、排队数量与等待时间，降低集中握手的资源压力。
- 按内存压力逐级收紧普通创建并发（默认 32 / 16 / 8），为交互与实时流量预留额度；排队支持取消、超时和等待老化。
- TLS、AnyTLS 与多跳代理共用一次拨号许可，修复嵌套申请造成的阻塞；成功、失败及取消均归还许可。
- 修复旧压力状态覆盖新策略、重复触发回收的竞态；复用已有 OOM 采样，补充排队、拒绝、取消与在途计数。
- 修复 runtime 内存指标发布滞后导致的误判，更新 sing-tun 并补充 UDP NAT 压力验证。

## NaiveProxy 安全与传输

- 同步 **NaiveProxy v154.0.8037.49-5** 相关修复，Chromium 基线保持 **154.0.8037.49**；证书封锁列表新增 8 个 SPKI 哈希。
- QUIC 流级 / 连接级接收窗口上限调整为 **64 / 128 MiB**，修复窗口下限检查并启用自动调节，保留自定义窗口配置。
- 修复 Linux 匿名 socketpair 无对端地址时 DNS 回复失败的问题，恢复自定义解析与拨号目标。原约 **274 秒超时**用例修复后约 **0.79 秒通过**，Linux purego / cgo 均完成同场景验证。
- 更新 Go 封装和原生依赖，重建 Linux amd64、Windows amd64、Android arm64、macOS arm64、iOS arm64 库。

## DNS、切网与隧道兼容

- DNS 冷连接异步建立，避免 TCP/TLS 握手阻塞收包；打包前复制 DNS 消息，修复并发修改共享请求的问题。
- 设备地址变化时中断对应地址族的旧 UDP socket，使后续连接使用更新后的地址状态。
- 完善 OpenVPN、OpenConnect 端点本地流量处理；吸收严格路由、内核 IPv6、Windows RAS 默认路由和 OpenConnect CSD/F5/GPST 兼容修复。

## 构建与发布

- iOS、macOS 和自签构建复用同次校验的 Libbox，统一客户端来源、应用版本和原生库来源记录。
- 将未签名 IPA 纳入正式附件，完善 Go 模块版本与发布清单校验；完成各平台核心、Apple 包及 Windows 安装包验证。
- Linux 默认服务端档提供 Naive 入站，未启用 Cronet 出站；Apple Intel / 模拟器原生分片沿用已有库，macOS DMG 不在本版正式附件中。

[完整更新日志](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/CHANGELOG.md) · [配置文档](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/tree/testing/docs/configuration)
