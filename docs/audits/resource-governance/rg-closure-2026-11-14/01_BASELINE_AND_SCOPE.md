# 01 基线与施工范围

## 1. 仓库与基线的判定过程

施工指令要求"不要仅凭历史文档猜仓库地址"。本轮按以下顺序核实（`VERIFIED_GIT`）：

1. `https://api.github.com/repos/Piggy-Cat-bit-shadow/sing-box` 返回 **HTTP 200 但内容为仓库 id `1383685561` / name `JiejieBox`**，即旧 `sing-box` 名称已被 GitHub 重定向到 `JiejieBox`。
2. `https://api.github.com/repos/Piggy-Cat-bit-shadow/JiejieBox` 同样是 id `1383685561`，`default_branch: testing`，`parent: SagerNet/sing-box`，`pushed_at: 2026-11-13T23:44:45Z`。
3. 两个远端 URL 的 `git ls-remote --symref origin HEAD` 都返回 `refs/heads/testing`，HEAD 均为 `fa61500731b1237e9ef44029b7469ee5373af4ba`，且远端只有这一个分支。

结论：**canonical 仓库是 `Piggy-Cat-bit-shadow/JiejieBox`**，`Piggy-Cat-bit-shadow/sing-box` 是同一仓库的旧名称重定向。没有在两个仓库重复施工。

## 2. 全新 Clone 与工作分支

```text
clone dir      : C:\Deepseek\rg-closure-2026-11-14   （全新空目录，未复用任何已有工作树）
origin         : https://github.com/Piggy-Cat-bit-shadow/JiejieBox.git
remote HEAD    : refs/heads/testing
base SHA       : fa61500731b1237e9ef44029b7469ee5373af4ba
base commit    : docs(release): expand the v0.1.47 changelog and name the IPv6 crash fix
                 (2026-11-14 07:44:39 +0800)
feature branch : work/resource-governance-closure-2026-11-14（远端此前不存在同名分支）
in-tree version: release/JIEJIE_VERSION = 0.1.47
```

施工期间远端 `testing` 未前进；本轮结束时本地与远端核对结果见 `00_EXECUTIVE_SUMMARY.md` 与最后一次 push 记录（`raw/40_push.txt`）。

## 3. 为什么以 `testing` 为基线

- 仓库 `AGENTS.md` 与 `KNOWLEDGE/BASELINE.md` 都指定产品分支为 `testing`；
- 远端默认分支就是 `testing`，且远端没有 `main`；
- 基线之后的 27 个提交（`c795bb5c..fa6150073`）包含真实生产代码变更（`common/runtimecoord`、`dns/transport`、`protocol/masque`、`common/dialer` 等），说明它是活跃开发集成分支，而不是仅供发布的静态分支。

## 4. 基线增量与知识库的关系

知识库最后一轮广泛基线是 `0067b03e3`（v0.1.34），0.1.46 专题固定 `c795bb5cbbcd9b6a4d2d2f18e854b629915335f5`，当前 HEAD 比 0.1.46 多 27 个提交（含 0.1.47 changelog 与多项生产修复）。

因此：**不把知识库中的"已知限制"直接当作当前结论**。本轮逐条在源码中重核了与本任务相关的条目（例如"AnyTLS 在复用前领取许可"在 0.1.45 已修复，见 `05_DESIGN_DECISIONS.md` §3）。

## 5. 环境

```text
OS        : Windows (本机)
Go        : go1.25.5 windows/amd64
CGO       : 0（常规测试/构建）；1 + mingw64 gcc（-race 证据）
GOPATH    : C:\Users\Jie\go
GOMODCACHE: D:\Go\mod-cache
```

用于 Windows/others 的构建标签（`release/DEFAULT_BUILD_TAGS_OTHERS`）：

```text
with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,
with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,
with_trusttunnel,with_awg,badlinkname
```

Darwin/iOS 的 `release/DEFAULT_BUILD_TAGS` 额外包含 `with_naive_outbound`，且不含 `with_usbip/with_openvpn/with_openconnect`。**本机没有 Apple SDK**，因此 iOS/macOS 构建与任何设备侧结论均为 `NEEDS_DEVICE` / `BLOCKED_BY_ENVIRONMENT`。

## 6. 依赖 pin（只记录，未升级）

从当前 `go.mod` 读取（`VERIFIED_SOURCE`）：

| 依赖 | pin |
|---|---|
| `github.com/Piggy-Cat-bit-shadow/sing-tun`（replace） | `v0.9.7-0.20261112091034-3c0ead27405b` |
| `github.com/Piggy-Cat-bit-shadow/sing-anytls`（replace） | `v0.0.0-20261112100203-f4acc22de770` |
| `github.com/Piggy-Cat-bit-shadow/sing-quic`（replace） | `v0.7.2-0.20261112101651-c1b074f1f868` |
| `github.com/Piggy-Cat-bit-shadow/mieru/v3`（replace） | `v3.39.1-0.20261112135526-b5934f7136b4` |
| `github.com/Piggy-Cat-bit-shadow/quic-go`（replace） | `v0.61.1-0.20261110231807-158445cdda77` |
| `github.com/Piggy-Cat-bit-shadow/sing`（replace） | `v0.9.6-0.20261110222322-4abfaed41727` |

本轮**没有**修改 `go.mod` / `go.sum`，没有升级依赖，没有跨仓提交。

## 7. 范围界定（本轮施工边界）

**在范围内并已实施**

- `route`：非 connected UDP setup 的 dial setup gate / traffic class / dial gate 一致性。
- `service/oomkiller`：不可测量 footprint 的压力事实与报告标记。
- `transport/v2rayquic`：共享 QUIC 创建的准入。

**在范围内、只审计不改代码**

- ResourceGuard / OOMKiller 的单一权威关系、shed 与 reclaim 执行器、`Permit.Wait` 语义。
- AnyTLS / Hysteria2 / Hysteria / TUIC / TrustTunnel / Naive / Mieru / TLS 的创建接点覆盖。
- XHTTP/XMUX、MASQUE 的 H3 创建路径与连接数上界。
- TUN / UDP NAT 的对象、字节与作业上限（依赖仓库 `sing-tun` 内实现）。

**明确不在本轮**

- 协议握手实现重写、拥塞控制、MTU/几何、UI/配置体验、Release 流程。
- 依赖仓库修改；如需，须另起仓库、另起分支、单独提交（本轮不需要）。
- 非 iOS 平台套用 50 MiB 观测预算。

## 8. 施工前基线测试（全部实际运行）

`raw/00_base_tests.txt`，基线 SHA `fa6150073`，`CGO_ENABLED=0`：

```text
ok  common/resourceguard    ok  common/trafficclass      ok  protocol/anytls
ok  service/oomkiller       ok  common/buildgate         ok  common/tls (admission 子集)
ok  transport/v2rayquic (with_quic)
```

即：本轮三个修复都建立在"基线相关包本身是绿的"之上，RED 只来自新增测试。
