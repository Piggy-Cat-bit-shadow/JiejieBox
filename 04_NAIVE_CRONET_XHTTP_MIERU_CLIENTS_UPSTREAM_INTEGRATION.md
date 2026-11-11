# 04｜Naive / Cronet / XHTTP / Mieru 与客户端上游融合报告

- 执行时间：2026-11-11
- 前置：`01` 审计、`02` sing/sing-tun、`03` QUIC/TLS/WireGuard
- 结果：XHTTP、Naive、Cronet、Mieru、MASQUE 未发现需要改代码的缺口（已有实现/测试覆盖）；Android 与 Dashboard 两个客户端正式主线已吸收上游并零落后；Desktop 0 落后、Apple 0 落后但无 Xcode。

## 1. Naive / Cronet 版本矩阵

| 项 | 当前 pin | 状态/依据 |
|---|---|---|
| cronet-go root binding | `Piggy-Cat-bit-shadow/cronet-go v0.0.1-143.0.7499.109-2.0.20261105062326-362e63b14ce2`（commit `362e63b`，`Build from 4c96192d`） | 与 29 个 native modules 同一 generation |
| cronet-go `all` | `Piggy-Cat-bit-shadow/cronet-go/all v0.0.0-20261105062941-7ce9c49b464a`（commit `7ce9c49`） | 聚合模块，指向同一组 lib |
| 29 个 `lib/<os>_<arch>` | 全部 `v0.0.0-20261105062326-362e63b14ce2` | `go.mod` 与 `test/go.mod` 各 29 条，一致 |
| 生成源 | `4c96192`（fork main tip） | 已由 `git merge-base --is-ancestor` 确认包含上游 main `73349ff`（“Fix Windows TCP port reuse metrics racing across engines”） |
| 上游 `go` 线 | `095cb9e`（2 个 generation 提交：`3aac4ed Build from 73349ff2` + `095cb9e Generate all package`） | 这两提交是从**较早**的 `73349ff` 重新生成；fork 从包含它的 `4c96192` 生成。按 ancestry 是 behind 2，但按“源提交是否包含”不是缺口；把上游 generation 合进来会用旧源产物覆盖 fork 的 4c96192 产物，属于回退，**不合并** |
| NaiveProxy / forwardproxy | 无子模块/本地 native 源码被主仓 pin；Naive 客户端使用 cronet binding | Naive inbound 仍是本项目 Go H2/H3，不复制 Caddy/forwardproxy 服务端补丁 |

说明：`go` 分支是并行 generation 线，不能用普通 branch compare 判断“落后”；正确判据是生成源 `4c96192` 包含上游源 `73349ff`（已确认）。

## 2. Naive 行为验证

- `go test ./protocol/naive`（Windows、DLL 在 PATH）：**PASS（4.5 s）**。
- 覆盖：outbound dial、H2/H3 回退、TLS trust、Close、DNS bridge、insecure_concurrency 现有测试。
- 未做 WAN 测速、未改生产配置；Darwin/iOS 的 native 路径只做交叉编译，标 `NOT_VERIFIED_ON_DEVICE`。

## 3. XHTTP：候选全部已有实现

| 候选 | 结论 | 证据 |
|---|---|---|
| `dialStreamUp` 上传非 200 未检查 | **已修复，无需再改** | `96bd665d106 fix(xhttp): fail stream-up when the upload POST is refused`；`669fa275261 test(xhttp): cover the refused upload status matrix without draining` |
| 4xx/5xx 立即错误、body close、pending download 取消、晚到拒绝 | **已覆盖** | `transport/v2rayxhttp/stream_up_refusal_test.go`：400/404/413/500 矩阵、blocking body 不 drain、`waitObserved(hang,1)`、late refusal 双向 |
| packet-up `GetBody` / GOAWAY 重放 | **已覆盖** | `TestUplinkBodyGetBody`（`breaker_test.go`），`meta.go` 的 replay 注释 |
| XMUX 故障隔离/关闭/复用 | **已覆盖** | `xmux_*_test.go` 系列 |
| metadata 非默认位置 | **已覆盖** | `configured_query_test.go` |
| race | **PASS** | `go test -race ./transport/v2rayxhttp` 21.5 s |

未引入 LX/Xray 的并发 POST 流水线：现有 packet-up 聚合/预算实现已测试，缺少可证明收益的本地证据，按“无证据不改”保留。

## 4. Mieru

- `go.mod`：`github.com/enfein/mieru/v3 v3.39.0`。
- `go list -m -versions`：v3.39.0 是当前最新发布（无 v3.39.1+）。
- 结论：`NO_CHANGE_REQUIRED`；双 Runtime（控制面统一、数据面独立）保留。
- `go test ./protocol/mieru`：**PASS（94.4 s）**。

## 5. MASQUE

- 无上游版本变化；CONNECT-UDP/IP、H3/H2 回退、owned DATAGRAM、PREF64、MTU/PTB、DNS、双栈、背压全部保留。
- `go test ./protocol/masque ./transport/masque`：**PASS（8.6 s / 7.1 s）**。
- 结论：`NO_CHANGE_REQUIRED`，不重写。

## 6. 客户端

### Android

- fork `main`：`ba5c6b161827a830cb7343850bf547156fbfdfee`（parents `c783710` + upstream `ab4fad3`）。
- 上游 5 提交中，DNS search domains / auto redirect 在 fork 已有；实际合入为浅色主题对比度、`ColorContrast`、QR 生成简化和相关设置页。
- 构建：`./gradlew --no-daemon :app:assembleDebug` → **BUILD SUCCESSFUL，8m28s**（138 tasks，JDK 17，SDK 36，libbox.aar/legacy 来自 0.1.45 产物）。
- 单测：`:app:testOtherDebugUnitTest` → **PASS，27s**（`:app:testDebugUnitTest` 因 flavor 歧义，改用 other flavor 任务）。
- Compare：`Piggy:main...SagerNet:dev` **ahead_by=0 / behind_by=15**。
- 未做真机/模拟器验证：`NOT_VERIFIED_ON_DEVICE`。

### Dashboard

- fork `main`：`b592c65618e13e2c7ea24a06e758db72c71f65be`（parents `fc1dbb4` + upstream `3e4bf08`），`--allow-unrelated-histories` 双父合并。
- 13 个冲突逐 hunk 融合；经核对上游在冲突区只有品牌字符串/旧签名，fork 树是内容超集（翻译 429 ⊇ 425 keys，proto 无上游独有字段），合并树等于 fork 树；未用 `-X ours/theirs`。
- 测试：`pnpm test` **98/98 PASS**（fork 比上游多一个 remoteUpdate 用例）；`pnpm build` PASS；`pnpm lint` PASS。
- `pnpm lint:css` 仍有 4 个**既有**错误（fork-only `ProfileViews.module.css` 的 2px/600 规则），不是合并引入，未改。
- 消费线：`integration/0.1.45` 已把 `b592c65` 合入原 `62f1582`，得到 `9bde90e1cd5577dc3f5549535aa23bea69816d6f`，已推送；该线测试 **98/98 PASS**。
- Compare：`Piggy:main...SagerNet:main` **ahead_by=0 / behind_by=10**。

### Desktop

- fork `main`/origin tip `aa63764`，**0 behind**；产品线 `integration/0.1.45` 已推送（此前只在本地）。
- dashboard 子模块 `62f1582` → **`9bde90e1cd55`**（含上游合并 + 原 fork 提交），桌面产品线提交 **`e8b48beadd7d`** 已推送。
- 升级后 `pnpm test`：**59/59 PASS**；`pnpm build`：**PASS**（4.19 s）。

### Apple

- fork `main` `97b35d1` 已含 upstream/dev `768b3de`，**0 behind**；无合并。
- 本机无 Xcode/macOS：构建、XCTest、TestFlight 均 `NOT_VERIFIED_ON_DEVICE`；Apple pin 保持 `7fce711`。

## 7. 主仓需要在阶段 05 更新的精确值

| 项 | 当前 | 目标（已应用到内核 98097189116） |
|---|---|---|
| `.gitmodules`/gitlink `clients/android` | `c7837107dc01` | `ba5c6b161827a830cb7343850bf547156fbfdfee` |
| `clients/apple` | `7fce7117b33a` | 不变（无 Xcode 验证，不升级） |
| `clients/desktop` | `28dc4a973bcd` | **`e8b48beadd7d11a9bce46f62e12820f3e3773518`**（dashboard 子模块已 bump 到 `9bde90e1cd55`） |
| desktop 内 `dashboard` gitlink | `62f1582` | **`9bde90e1cd55`（已完成并推送）** |
| 其余 Go module pins | 见 02/03 报告 | sing/sing-tun/sing-quic/quic-go/utls 见对应报告；cronet/wireguard/mieru 不变 |

## 8. 未合并内容及理由

1. cronet `upstream/go` 的 2 个 generation 提交：从旧源 `73349ff` 生成，会覆盖 fork 基于 `4c96192` 的 29 个 native 产物；不合并（不是缺失修复）。
2. XHTTP/Xray 并发 POST 流水线：缺本地收益证据，保留现有 packet-up 聚合与预算实现。
3. Apple 上游 UI/系统改动：fork main 已包含 upstream/dev，且无 Xcode 验证环境，不做进一步替换。
4. Dashboard 上游“差异”：实为品牌/旧签名，已通过双父合并记录 ancestry，内容保持 fork 超集。

## 9. 验证与风险

- 本阶段跑过：Android assemble+unit、Dashboard install/generate/test/build/lint、Desktop test/build、Naive/MASQUE/Mieru/XHTTP（含 race）。
- 未跑：任何真机/模拟器、Xcode/XCTest、CI workflow、WAN 测速；均明确标 `NOT_VERIFIED_ON_DEVICE`/`BLOCKED_BY_ENVIRONMENT`。
- Dashboard `lint:css` 的 4 个既有错误、`common/tls` 等既有基线失败与本阶段改动无关。
- cronet 的 29 个 native 模块只做了版本一致性与消费者链接验证，没有重新生成 native archive；没有上游源修复需要重建。
