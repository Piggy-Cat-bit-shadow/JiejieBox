本次更新把 NaiveProxy 原生侧同步到上游 v154.0.8037.49-5，并修复 Linux 上自定义拨号被系统 DNS 拖垮的问题；同时合入资源准入、libbox 命令套接字、DNS 与设备回调等一批稳定性修复。

### NaiveProxy / Cronet

- **仍为 Chromium 154**：原生引擎保持 Chromium 154.0.8037.49，本轮不升级原生版本。
- **吸收 NaiveProxy patch5 补丁**：证书黑名单吸收上游新增的 8 个 SPKI 哈希；QUIC 接收窗口上限与动态窗口自适应按上游实现，同时保留现有 `stream_receive_window`、`quic_session_receive_window` 自定义配置和边界处理；CONNECT 隧道行为按上游实现核对，未对全部流量强制 CONNECT。
- **修复 Linux DNS 回复**：域名解析由进程内 DNS 桥接代理，Linux 默认使用匿名 AF_UNIX 数据报套接字对。修复前回复被发往空地址而丢弃，Chromium 回退系统解析并把代理服务器拨到公网地址，最终表现为连接超时。修复后按对端地址选择回复方式，新增失败计数与套接字对回归测试。
- 五个产品目标（linux/amd64、windows/amd64、android/arm64、darwin/arm64、ios/arm64）的原生库以同一源码提交重建并重新发布。

### 资源准入与稳定性

- 新增压力感知的准入闸门：TLS 握手、AnyTLS 会话与 OOM killer 归属的 iOS NetworkExtension 共用进程级资源配额。
- 交互流量按 traffic class 预留额度；一次逻辑拨号只持有一个握手许可，许可沿调用链传递时仍然生效。
- 修复评审发现的 5 个准入缺陷；OOM 元数据文档补充准入计数。
- libbox 命令套接字在基础路径过深时仍可绑定，并把系统临时目录始终作为候选。
- DNS：连接建立移出调用方 goroutine，打包前复制消息，避免修改调用方对象。
- 设备：设备地址变化时中断 UDP 套接字；endpoint 系统模式按主机端口判定。
- TUN：所选栈无法回显回环地址时给出明确提示。

### 兼容性与说明

- 配置保持兼容：新增字段均可选，原有 Naive 窗口、UDP over TCP、QUIC 回退等配置语义不变。
- Windows 仍以 `with_purego` 加载 `libcronet.dll`；Linux 产品档不含 Naive 出站（`DEFAULT_BUILD_TAGS_OTHERS` 未启用 `with_naive_outbound`），本轮 DNS 修复在启用该标签的构建与测试中验证。
- Apple 的 darwin_amd64 与两个模拟器分片本轮未重建，字节与上一版一致；`release/cronet-provenance.txt` 如实记录这一组合。

[查看完整更新日志](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/CHANGELOG.md)
