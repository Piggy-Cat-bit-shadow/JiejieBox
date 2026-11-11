# 第二阶段报告：统一运行时与八协议系统耦合

> 本报告按施工提示词 §13 的 16 项顺序组织。证据以 `path:line` 标注，均为本阶段在源码中实际核对；
> 标「已修复」的条目有回归测试，标「已记录」的条目附最小实验。第一阶段数据只用于对照，
> 没有把旧数字套到本阶段代码上。

## 1. 当前分支和 HEAD

- 分支：`work/coupling-audit-2026-11-11`，从第一阶段头 `53a9f5dc8e2` 拉出（该提交本身基于
  `origin/integration/0.1.45-final` 的 `4ec0decd9f2d`）。
- 内核代码在审计期间未变；远端领先的 3 个提交为 docs/audit only（0 个 `.go`），已在第一阶段记录。
- 本阶段提交见 §12；分支不合并，保留失败样本与复现命令。

## 2. 第一阶段输入完整性

| 输入 | 状态 |
|---|---|
| 实验室 README / 运行器 / Schema | 完整（`scripts/perf/`，gofmt/vet/test 绿） |
| 八协议资产盘点 | 完整（`docs/perf/ASSET-INVENTORY.md`） |
| 基线报告（25 run，含原始 JSON 与日志） | 完整（`docs/perf/BASELINE.md`、`docs/perf/results/`） |
| Linux/WSL2 能力报告 | 完整（`docs/perf/ENVIRONMENT.md`） |
| 失败/阻塞清单 | 完整（`docs/perf/FAILURES.md`，B1–B8） |
| 第一阶段变更摘要 | 6 个提交，见第一阶段报告 |

审计中发现并修正的输入偏差：第一阶段把 WireGuard/AWG 的失败写成“数据面缺陷”并归入 B7，
本阶段用同拓扑的单进程探针证明用户态栈本身可承载 6 条并发流
（`test/wireguard_concurrent_flows_test.go`），因此 B7 是**两进程形态**下的问题，范围比原记录更窄。

## 3. 八协议资源所有者矩阵

完整矩阵见 [`OWNERSHIP-MATRIX.md`](OWNERSHIP-MATRIX.md)。结论：

- **所有权清晰的四个**：VLESS/REALITY、TrustTunnel、XHTTP、WireGuard。每个资源的创建者、
  持有者、关闭者可在一条调用链上闭合，Close 幂等（XHTTP 在本次修复前缺 `closed` 标志）。
- **所有权跨模块的四个**：AnyTLS（session 池在 `sing-anytls`）、Mieru（mux/underlay 在 SDK）、
  Naive（引擎在 Cronet 原生侧）、MASQUE（Tunnel 设备 + HTTP 客户端分属两层，且 endpoint 没有 Close）。
  跨模块本身不是缺陷，缺陷是**关闭语义无法被上层确认**，而 route 的释放走查按“已询问”计数（F-11）。
- **只有 WireGuard 一个 owner 读 generation**；其余协议要么依赖 `InterfaceUpdated` 广播，
  要么在会话发布时不检查 Reset（AnyTLS、Mieru 的非 fallback 路径）。
- **只有 TrustTunnel 在发布共享创建时检查丢弃标志**（`transport/trusttunnel/client.go:640-662`），
  这是本阶段认定的参考实现。

## 4. ResourceGuard 覆盖范围

详见 [`RESOURCEGUARD.md`](RESOURCEGUARD.md)。覆盖表：

| 创建动作 | 是否准入 | 位置 |
|---|---|---|
| 普通 TLS 握手（H1/H2、REALITY、Vision、Trojan/VMess…） | 是 | `common/tls/client.go:178-205`，拨号前（覆盖 DNS+TCP+握手） |
| AnyTLS 每次流拨号（含纯复用） | 是 | `protocol/anytls/admission.go:55-66`，池判定之前 |
| TrustTunnel 共享 Session 创建 | 是（每个 offer 一次，waiter 免费） | `transport/trusttunnel/client.go:601-625` |
| Mieru underlay socket | 是 | `protocol/mieru/dialer.go:85-120` |
| MASQUE H3 / XHTTP H3 / v2rayquic / gRPC | 否 | 各自 QUIC/TLS 路径 |
| Naive Cronet / WireGuard / AWG | 否 | 原生引擎/设备 |

三个结论：

1. **准入单位是“协议自己认为昂贵的那一步”，不是统一接口**。四家准入、三个层级，这是有意的
   （见 [`DECISIONS.md`](DECISIONS.md) D-1），因为要限流的东西不同。
2. **复用被计费只发生在一处，而且是错的一处**：AnyTLS 在池判定前取许可，饱和时一个不创建任何
   东西的流也会排队或被 `ErrQueueFull` 拒绝；而 TrustTunnel 的 waiter 不重复计费。
   记录为 F-06，修复需要外部模块暴露“是否会复用”。
3. **许可会计时错误已修复**：四个 seam 都在 `Acquire` 之前取 `started`，队列等待被算成创建时间，
   导致“排队 300ms 后瞬失败”被判为慢失败，fast-fail 限速在最该生效的饱和状态下永不武装。
   修复后从许可之后开始计时，回归测试见 §13（F-04）。

## 5. QUIC/H3 准入结论

- QUIC 路径**不经过** `handshakeWithAdmission`：MASQUE H3 在 `transport/http/client_h3.go:424,428`
  自建 UDP+QUIC；XHTTP H3 在 `transport/v2rayxhttp/http3.go:153-157` 自建一套；v2rayquic 在
  `transport/v2rayquic/client.go:399`；gRPC 在 `transport/v2raygrpc/tls_credentials.go:37`。
- TrustTunnel 的 QUIC 握手包含在 Session 许可内，但握手本身不再单独计费。
- **本阶段不新增通用 QUIC 准入**。理由与前置条件写在 [`DECISIONS.md`](DECISIONS.md) D-2：
  MASQUE 是长寿命隧道、XHTTP 是按请求的池化传输、TrustTunnel 是带 waiter 的共享 Session，
  三者不是同一资源类；一条“QUIC 连接”预算会把隧道和按请求传输按同一价格计费。
- 可观测的不对称保留为 F-10：guard 饱和时 H2 创建会被拒，H3 照常进行；实验是一次饱和 guard + 两次拨号。

## 6. Route / Idle / On-Demand 结论

详见 [`ROUTE-LIFECYCLE.md`](ROUTE-LIFECYCLE.md)。

- **三种“关闭”语义被正确区分**：`CloseIdleConnections`（只关无流量）、`RetireSuspect`（拒新活、
  排空旧活）、endpoint 的 `SetKeepIdleConnections(false)`（挂起隧道）。on-demand 隧道只实现后者，
  `route/reusable_owner_inventory_test.go` 双向钉住这个边界，是第一阶段留下的最有价值的约束。
- **危险点在 eligibility 而不是 idle 走查**：设备暂停时 `keep = referenced && !devicePaused`
  （`route/reference.go:339`）会挂起仍在承载流量的 on-demand 隧道；MASQUE 的 `Suspend` 无条件
  cancel 当前 Session（`transport/masque/client.go:429-442`），WireGuard 直接 `Down()`。
  这是 F-01（High/risk），需要产品决策，本阶段只记录并给出实验，没有静默改行为。
- **“已释放”不可验证**：走查返回的是“被询问的 owner 数”，`Box.CloseIdleConnections` 丢弃返回值，
  各池合法保留忙碌连接。契约本身没错，错在措辞与调用者的期待（F-11）。
- **reclaim 先返回、后关 socket**，`Close` 错误被丢弃；只有 `CloseAll` 等待（F-12）。
- **MemoryPressure**：两级 shed hook（AnyTLS/TrustTunnel，均 idle-only），恢复期创建被限流而非禁止；
  但 MASQUE 的 TUN、WireGuard 的设备、Cronet 的原生内存都不在 hook 视野内——这是回收覆盖缺口，
  不是语义错误（§9）。

## 7. RuntimeCoordinator / Generation

详见 [`RUNTIME-GENERATIONS.md`](RUNTIME-GENERATIONS.md)。

- epoch 的唯一发布点是 `route/network.go:1426-1432`；唯一注册 rebind lease 的 owner 是 WireGuard
  （`transport/wireguard/endpoint.go:153-157`），其余协议靠 `InterfaceUpdated` 广播。
- `Registration.Stale()`/`Acknowledge()` 无生产调用者（F-13）：API 存在，但唯一生效的检查在
  WireGuard 的 lease 路径。
- 过渡窗口：epoch 发布后监听器阶段最多 6s，且允许放弃 straggler（`route/network.go:1715-1727,
  1802-1807`）；新流量可在窗口内复用尚未 Reset 的池化会话。拨号侧只校验“交出的 socket 是否跨代”
  （`common/dialer/default.go:797-803`），不校验“复用的会话是否跨代”。记录为 F-15。
- 各协议在换网时的行为差异见矩阵第 5 列：XHTTP 排空、TrustTunnel 丢弃 pending、MASQUE 重启
  Session、Cronet 只关连接不重建引擎、Mieru 换 runtime（非 fallback 路径不回查）、AnyTLS 由外部
  `Reset` 关闭且发布不检查。

## 8. DNS / Dial / 双栈

- **DNS 归属**：域名的解析发生在拨号路径内（`dialOptions.DomainResolver` → `NewDNSQueryOptions`，
  `common/dialer/dual_stack.go:78-96`），解析后再生成候选；解析器本身可经 detour（`ResolverOnDetour`）。
  因此 TLS 准入许可覆盖了解析时间——这是有意的“按创建整体限流”，但也意味着慢解析会占住许可。
- **双栈**：`planCandidates`（`dual_stack.go:133`）是共享的候选规划器，family 能力门与
  `dualStackAdmission` 策略都在 `common/dialer`，八个协议中走公共 dialer 的（VLESS/REALITY、AnyTLS
  的底层、TrustTunnel H2、XHTTP H1/H2、Naive 的 H2 fallback）共享同一套。
- **旁路**：MASQUE H3 用 raw dialer + bootstrap 竞速器自建候选；Mieru 在 SDK 前自行解析；
  Cronet 在原生侧解析；WireGuard 的 endpoint 对域名 peer 有独立解析。
  这些不是重复实现同一策略，而是被原生/QUIC 生命周期迫使的旁路；代价是双栈与 family 策略在
  这些路径上不一致，且没有测试对比。记录为 F-16（Low，documented）。
- **连接账本**：拨号结果在交出前原子地取 ticket 并 Track（`common/dialer/default.go:805-826`），
  这是与 resourceguard 不同的另一种准入（pending socket 账本，保证 shutdown 原子性），
  两者不应合并——前者限“创建成本”，后者限“未记账 socket”。

## 9. MemoryPressure / OOM

- 发布：`service/oomkiller` 的 timer 写 level，guard 读 level；回收阶梯 L1 缓存 → L2 idle 池 →
  L3 GC，crisis 追加整网 reset（`service/oomkiller/timer.go:818-867,983-986`）。
- 主动 shed hook 只有两个（AnyTLS、TrustTunnel），均 idle-only 且每次进入 Critical 只触发一次。
- 恢复创建被限流（Critical: limit/4, queue/8, wait 100ms），不被禁止——对以“被系统杀”为失败模式的
  extension 是正确取舍。
- 缺口：MASQUE 的 TUN 栈、WireGuard 的设备、Cronet 原生内存在 hook 视野之外；这些内存主要在
  原生侧，Go 侧 hook 即使被调用也释放不了。应在第三阶段用原生侧可观测性补齐，而不是加 Go 接口。

## 10. Cronet / Native

- **引擎归属**：每 outbound 一组引擎（数量=concurrency，iOS 单引擎），H2 fallback 再加一组；
  隔离键是按拨号槽位的 header，不是身份/目标。`Close` 非 nil 幂等（第二次返回 `net.ErrClosed`），
  `doClose` 等待 Go 侧计数后 Shutdown+Destroy，但丢弃 Shutdown 的 Result。
- **网络变化不重建引擎**：只 `CloseAllConnections` + H3 fallback window reset；旧引擎与其 QUIC/H2
  池在换网后继续被复用。这是与 WireGuard 的 generation 处理最不一致的一处。
- **原生资源不在回收视野**：Chromium 线程/连接池/HTTP 缓存/QUIC session 无 Go 侧计数，RSS 只有进程级。
  与第一阶段结论一致（FAILURES.md 的 Cronet RSS 不可分）。
- **WireGuard/AWG**：设备、bind、egress pool 的所有权与关闭顺序可闭合；pause 与 rebind 是唯一
  带 generation 的路径；`stackDevice.DialContext` 无 closed 检查（`stack_go_dial.go:63-68` 在
  selectEngine 里挡），Close 后 dial 的错误语义依赖 sing-tun。
- **B7 的收窄**：同拓扑单进程 6 并发流通过（`test/wireguard_concurrent_flows_test.go`）；
  两进程形态下失败。第三阶段的三个假设与判别实验见 §16。

## 11. 问题和严重性

完整清单见 [`ISSUES.md`](ISSUES.md)。

| 编号 | 严重性 | 问题 | 状态 |
|---|---|---|---|
| F-01 | High | 设备暂停可挂起仍在承载流量的 on-demand 隧道 | risk |
| B7 | High | 两进程用户态 WG/AWG 并发流失败 | documented（范围收窄） |
| F-03 | Medium-High | XHTTP 池在 Close 后仍可新建连接 | **fixed** |
| F-04 | Medium | 准入限速把队列等待算成创建时间 | **fixed** |
| F-06 | Medium | AnyTLS 纯复用被计费/可被拒 | documented |
| F-07 | Medium | Mieru 无创建 singleflight；cleaner×AddSession 竞态；非 fallback 不回查 generation | **generation 部分已修**；SDK 部分 documented |
| F-08 | Medium | REALITY 验证失败 fallback 在已关闭连接上跑 | **fixed** |
| F-10 | Medium | QUIC/H3 创建不受准入 | documented |
| F-11/12/13/14/15/16 | Low–Medium | 释放契约措辞、reclaim 异步、Stale 死代码、引用图事件源、6s 过渡窗口、DNS 旁路 | documented |

## 12. 修复及 commit

- `992087a57bb` — `fix(coupling): keep the XHTTP pool closed and time admission from the permit`
  （F-03、F-04 与三个回归测试；`test/wireguard_concurrent_flows_test.go` 收窄 B7）。
- `47288906810` — `docs(coupling): record the stage-2 runtime ownership audit`（本目录）。
- `cde28636176` — `fix(coupling): hand REALITY's fallback its socket and reject stale Mieru dials`
  （F-08、F-07 的 generation 部分与两组回归测试；`perflab -stagger` 诊断）。
- 本报告所在提交 — `docs(coupling): close the REALITY and Mieru findings and record the B7 stagger`
  （F-08/F-07 状态、B7 判别结果与 Mieru 复测）。
- 分支保持独立，不合并。

四处修复都遵循“先证据、最小改动、回归测试”：

1. **F-03**：`xmuxManager` 增加 `closed`（与池交换同锁）；`get` 关闭后拒绝新建；
   `getContext` 把空结果映射为错误而不是等到 deadline。
2. **F-04**：四个 seam 的 `started` 移到 `Acquire` 之后；只影响限速分类。
3. **F-08**：验证失败改为带 `ConnectionHandedOff` 标记的错误；共享 TLS dialer 不再关闭
   该连接，由 fallback 持有并在 `C.TCPTimeout` 内关闭。
4. **F-07（generation 部分）**：Mieru 单传输路径复用 `acceptGeneratedConn`，跨代连接关闭并
   返回 `errFallbackStale`。

没有做的事，以及为什么，写在 [`DECISIONS.md`](DECISIONS.md)：不新增通用 QUIC 准入、
不把 epoch 穿进每个池、不抽象共享 Session 池、不改变 on-demand 挂起策略、不给释放契约加完成确认。

## 13. 新增测试和输出

| 测试 | 位置 | 钉住的不变量 |
|---|---|---|
| `TestAQueuedFastFailureStillArmsTheRateLimit` | `common/tls/handshake_rate_window_test.go` | 排队超过 fast-fail 窗口后的瞬失败仍计数并武装限速 |
| `TestXmuxDoesNotOpenAConnectionAfterClose` | `transport/v2rayxhttp/xmux_close_boundary_test.go` | Close 之后不再新建池化连接 |
| `TestXmuxCloseRacingDialsLeavesNoConnectionUnclosed` | 同上 | Close×并发拨号后每条已建连接恰好关闭一次 |
| `TestDialerLeavesAHandedOffConnectionOpen` 等三项 | `common/tls/reality_fallback_ownership_test.go` | REALITY fallback 持有连接；普通失败恰好关闭一次；标记穿透错误包装 |
| `TestSingleTransportDialRejectsAConnectionFromAnotherGeneration` 等两项 | `protocol/mieru/generation_boundary_test.go` | 跨代连接关闭并失败；同代连接原样返回 |
| `TestWireGuardConcurrentFlowsInOneProcess` | `test/wireguard_concurrent_flows_test.go` | 同一进程内用户态栈承载 4 条并发 TCP 流（收窄 B7） |

全量回归（修复前基线，13 个包全绿）：`common/resourceguard`、`common/runtimecoord`、
`common/tls`、`route`(454s)、`protocol/{anytls,mieru,trusttunnel,masque,naive,wireguard}`、
`transport/{trusttunnel,wireguard}`、`adapter`。修复后针对受影响包的复跑结果见 §14 的表注。

## 14. 性能前后变化

只重跑修复触及的路径：XHTTP（池关闭改动）、VLESS Vision / AnyTLS / TrustTunnel / Mieru
（准入计时改动）。方法与第一阶段相同（loopback，4 KiB 页，负载由目标端计数），但本轮为
3 次重复（第一阶段 5 次），单次时长同为 5 s，所以比较的是中位数，不是逐次对齐。

| cell（MiB/s，中位数） | 第一阶段 | 本阶段 | 变化 |
|---|---|---|---|
| xhttp-h2 upload s=1 / s=4 | 32.6 / 125.6 | 32.6 / 127.2 | 0 % / +1.3 % |
| xhttp-h2 download s=1 / s=4 | 83.8 / 213.4 | 83.4 / 224.6 | −0.5 % / +5.3 % |
| xhttp-h3 upload s=1 / s=4 | 32.6 / 121.9 | 32.5 / 127.2 | −0.3 % / +4.3 % |
| xhttp-h3 download s=1 / s=4 | 83.3 / 169.3 | 73.1 / 181.4 | −12.2 % / +7.1 % |
| vless-vision-tcp download s=4 | 1372.8 | 1362.7 | −0.7 % |
| anytls-tcp download s=4 / upload s=4 | 1198.2 / 750.3 | 1182.1 / 751.6 | −1.3 % / +0.2 % |
| mieru-tcp download s=4 / upload s=4 | 701.3 / 429.7 | 664.5 / 453.3 | −5.2 % / +5.5 % |
| trusttunnel-h2 download s=4 / udp s=1 | 137.8 / 9.6 | 140.1 / 12.1 | +1.7 % / +26.7 % |

结论：**没有可归因于修复的回归**。两处改动都不在测量热路径上——实验室没有安装
`resourceguard`（guard 为 nil），F-04 只影响限速分类；F-03 只改变“池已关闭”之后的行为。
表里的离群值（H3 单流 −12 %、VLESS 四流上传 +26 %、TrustTunnel UDP +27 %）都在单机
loopback 的轮间波动范围内，且方向不成体系；需要更严格对比时应固定 reps 与 duration 再跑。

透明性说明：这批 run 的 `run.json` 记录的是基线提交 `53a9f5dc8e2` + `dirty: true`，因为
二进制在修复提交之前从含修复的工作树构建（`sing-box-stage2`，构建时间 13:55）。报告中的
“本阶段”列即该二进制；提交后的代码与它只差同一个提交的内容。原始记录在
[`results/`](results/)，含 `run.json`、渲染后的配置和 gzip 日志，共 6 个 run、2 MiB。

修复后受影响包的复跑：`common/tls`、`protocol/{anytls,mieru,trusttunnel}`、
`transport/{trusttunnel,v2rayxhttp}` 全绿；三个新测试在 `-race` 下通过。

修复后只重跑了 Mieru（唯一落在拨号路径上的改动：F-07 给单传输拨号加了一次
`TryRLock` 与代际比较）：upload s=1 122.7 / s=4 478.9 MiB/s，download s=1 104.2 /
s=4 694.0 MiB/s（3 次中位数），相对本阶段表里的 121.9/453.3 与 103.8/664.5 没有回归。
F-08 只改变验证失败后的路径；F-03/F-04 不在数据面热路径上（实验室 guard 为 nil）。

## 15. 未解决风险

1. **F-01 是唯一可能直接切断用户流量的条目**，且需要产品决策；第三阶段应先用实验证明，再决定挂起是否延迟到隧道空闲。
2. **B7 只在两进程形态复现**，根因未定；三个假设（内存队列静默丢弃、peer 端 4 元组复用、设备暂停）都能解释现象，需按 §16 的实验区分。
3. **F-10 的准入缺口在 iOS NE 之外不可观测**（guard 为 nil），所以性能基线无法暴露它；这是安全/预算问题，不是吞吐问题。
4. **原生资源（Cronet/WG/MASQUE TUN）的内存与生命周期**仍只能靠进程级 RSS 观察，没有 per-owner 证据。
5. **6s 过渡窗口**内复用旧会话的概率未被测量；F-15 的测试需要可控的慢 listener。
6. 外部模块（sing-anytls、Mieru SDK）的三处语义问题（复用计费、无 singleflight、发布不回查）无法在本仓库内修，需要 fork 或上游补丁。

## 16. 第三阶段最值得 Debug 的对象

按价值排序：

1. **B7：两进程用户态 WireGuard/AWG 并发流**。已完成：`-stagger` 判别（`perflab run -stagger`）——
   同一二进制、同一 cell，`streams=2` 在 0s 与 200ms 间隔下 2/2 失败，在 1s 间隔下 2/2 通过
   （51.7 / 12.0 MiB/s）。所以触发条件是**握手重叠**，不是传输路径或队列饱和。
   下一步（按成本）：(a) 在客户端记录每条流的源端口，并用 peer 侧 `HasEndpoint` 检查新 4 元组，
   看 peer 是否已见同端口而未回 SYN-ACK（指向 `handleTCPSyn` 的 4 元组复用分支，
   `stack_go_tcp_input.go:100-121`）；(b) 给 sing-tun 的丢弃点加计数器——**必须用 `go.mod`
   钉住的版本**（`v0.9.7-0.20261109145802-79ed0653fdc6`）；本阶段曾误用 module cache 里更新的
   版本，结果不算证据，已从 B7 记录中撤回。任一假设成立都应先补失败测试，再改。
2. **F-01：设备暂停 × 活跃隧道**。用一个 fake pause manager + 真实 MASQUE/WG endpoint 承载一条流，
   观察挂起是否切断它；这是唯一能把“策略正确”与“数据面正确”分开的实验。
3. **F-08：REALITY fallback 的关闭时序**（本阶段已修，待真实网络复核）。fake conn 测试已固定
   “fallback 持有连接、普通失败只关一次”；真机上仍应观察验证失败后的 camouflage 请求是否完整。
4. **F-06/F-07 的外部池语义**。若决定 fork `sing-anytls`，先用一个复用饱和测试固定
   “reuse 不计费”的期望，再改模块；Mieru 的 singleflight 可以先在 fork 适配层做。
5. **F-10 的 QUIC 准入设计**。只有在 §5 的四个前置条件被满足后才动手；否则只保留实验与文档。

不合并主分支；本报告、`docs/coupling/` 与三个修复提交即本阶段交付。
