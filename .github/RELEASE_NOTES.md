# JiejieBox 0.1.37

本版汇总这一轮协议升级与上游同步：以可验证的成熟修复为主，并完成对应的 Debug、回归测试和安全加固。

## 协议与传输

- **Mieru `v3.39.0`**：升级到当前稳定 SDK，恢复长单向上传的读超时行为，并补齐空 UDP 与空闲 underlay 覆盖。
- **Naive / Cronet**：服务器名改由 sing-box dialer 解析，恢复 DNS 策略与双栈调度；保留 H3 → H2 回退、`extra_headers` 限制和单/多引擎行为。
- **Naive 服务端**：非法 HTTP/2、HTTP/3 CONNECT 现在返回真实 `400`，不再让客户端把拒绝误读为已接受的隧道。
- **XHTTP**：`stream-up` 上传被服务端拒绝时立即失败，关闭响应体、打断上传管道并终止挂起的下载；补齐拒绝状态矩阵与关闭竞态测试。
- **VLESS Vision**：升级 `sing-vmess`，修复 direct write 后关闭错误连接层的问题；Vision ABI tripwire 已按新版本重新核对。
- **MPTCP 监听安全**：默认关闭 listener 的 MPTCP（`tcp_multi_path` 仍是显式 opt-in），并将 debug/Clash/v2ray/pprof/boxdd 的 TCP 监听统一改为普通 TCP。

## Android 客户端

Android 客户端与本版内核使用同一个版本号 `0.1.37`，并在 `version.properties` 中固定本版内核提交；APK 构建时会重新编译并校验该内核，不使用未固定的 `testing` HEAD。

## 原生说明

NaiveProxy `-3/-4/-5` 的原生 Chromium 归档更新**不在本版内**。其中 `-5` 的 eMudhra 受损中间证书屏蔽仍未进入当前原生库，已明确安排到下一次 Chromium 升级，并保留强制检查清单。Linux 内核侧的 MPTCP 监听风险已由本版的默认关闭处理。

本版没有发布新的原生归档，也没有修改任何已冻结的依赖 pin 或校验和。
