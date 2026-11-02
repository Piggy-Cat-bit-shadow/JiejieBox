本次更新重点改善 Mieru、NaiveProxy、XHTTP 和 VLESS Vision 的连接稳定性，吸收上游成熟修复，并进一步加强 TCP 监听安全。

### 协议更新与修复

- **Mieru**：升级至 v3.39.0，修复持续单向上传期间可能出现的异常读超时，完善 UDP 边界与空闲连接测试。
- **NaiveProxy / Cronet**：优化代理服务器地址解析，使其由 sing-box 拨号器处理，更好地兼容自定义 DNS 策略与 IPv4/IPv6 双栈调度。
- **Naive 服务端**：修复 HTTP/2、HTTP/3 非法 CONNECT 请求可能被错误响应为 200 的问题。
- **XHTTP**：修复 stream-up 上传被服务端拒绝时可能持续挂起的问题，改善 HTTP 错误传递、连接取消及关闭处理。
- **VLESS Vision**：更新 sing-vmess 依赖，修复 direct-write 场景下的连接关闭问题。

### 网络与安全

- Linux TCP 监听默认禁用 MPTCP，仍支持通过 `tcp_multi_path` 显式启用。
- 统一 Clash API、V2Ray API、Debug HTTP、libbox pprof 等入口的 TCP 监听策略，避免意外启用 MPTCP。

### 稳定性与兼容性

- 完善 Mieru、Naive、XHTTP 和 Vision 的回归测试。
- 复核 XHTTP packet-up 与 XMUX 的并发、背压和连接生命周期行为，保留现有稳定实现。
- 对齐部分上游依赖，继续保持现有协议配置与架构兼容性。
- 完成相关平台构建、竞态检测及 CI 验证。

### 说明

- NaiveProxy 原生引擎当前基于 Chromium 154.0.8037.49，本次更新主要改进 Cronet 集成层，并未升级原生 Chromium。部分后续原生安全与性能补丁计划在下一轮 Chromium 更新时统一处理。
- 本次更新以稳定性修复为主，不涉及大规模配置变更。

[查看完整更新日志](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/CHANGELOG.md) · [本版原发布说明](https://github.com/Piggy-Cat-bit-shadow/JiejieBox/blob/testing/docs/archive/2026-10-dev-cycle/release-notes-original/v0.1.37.md)
