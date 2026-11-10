# 资源治理最终报告（2026-11-10）

归档目录 `0.1.45`。源码基线：集成分支 `integration/next-round`，归档前 HEAD `a635ded940a`。本文件只保留可复核的结论、提交标识与限制；原始日志不入库。证据等级沿用 `KNOWLEDGE/README.md` 的标签。

**一句话状态**：TUN 等待协议与 relay 夹具的范围内缺陷已修复并有红绿证据；Box UDP detour（含窄 MTU 配置）已在本机 e2e 数据路径上验真；UDP 嗅探暂存、MASQUE/H2/QUIC、昂贵建连与跨代驻留仍缺实现或外部条件。

## 1. 最终状态

| 位置 | SHA | 说明 |
|---|---|---|
| sing-tun fork `dev` | `57e9d17fa852` | 等待者归属三 P1 + 队列关闭全量清理；已推 origin（`VERIFIED_GIT`） |
| `next/resources` | `b00dfe1f0e0` | relay 夹具纠偏、反转开关、Box UDP detour e2e（含窄 MTU） |
| 收尾候选 `closeout/resources` | `aafc4c11f02` | pin `57e9d17fa852`；根/嵌套/源锁一致（`VERIFIED_SOURCE`） |
| 隔离合并预检 | `85793aaa846` | 候选合入 `a635ded940a`，仅改 sing-tun pin；未写入集成分支（`VERIFIED_INTEGRATION`，预检身份） |

## 2. 完成项与证据

### 2.1 队列关闭只清理首个等待者（审核 de6d P1）

- `clearWaits` 先清空 `conn.waitNext` 再循环读取，只处理第一个节点；其余连接保留 `waitQueue`/`pendingNeed` 指向已关闭队列，重新入队时 `noteWait` 视为外部 owner 直接返回，活写者对新队列不可见。
- 修复：先取 `next` 再清字段。回归 `TestQueueCloseClearsEveryWaiter`（三条连接、关闭、重新注册、链表计数与最小值）：旧树 3/3 红，新树绿（`VERIFIED_UNIT`）。
- 并发面：`TestWaitRegistrationRacesQueueCloseSafely`（注册/撤销与 Close 并发）在 `waitAccess` 下两树均通过、`-race` 干净；未发现缺陷（`VERIFIED_UNIT`）。
- 回归：等待者矩阵 3×、相关测试 20× `-race`、全矩阵 `-race`（`VERIFIED_UNIT`）。

### 2.2 等待者归属三 P1（审核 526cfc）

- 第 65 个不同尺寸清空活等待者、重复失败积幽灵需求、迁移删除新队列活写者：等待记录改为连接内嵌（每连接一条、队列内嵌链表、迁移前撤销旧队列），`minWaitNeed` 走活连接；无上限、无 1 B 兜底。三例回归在修复前 3/3 红、修复后绿（`VERIFIED_UNIT`）。

### 2.3 relay 夹具与证据口径（审核 526cfc P2）

- 每流独立 observation、join 后校验阈值/许可；`RELAY_AB_ORDER` 交替两臂；`RELAY_AB_INVERT` 反转时 granted 臂必失败。`-race -count=2` 通过（`VERIFIED_UNIT`）。
- 交替顺序实测 granted 557/603 ms vs refused 665/572 ms（同 8,388,608 B/臂），差异在噪声内。口径：真实链路（veth MTU 1280 + netem 50 ms）上的**单方向 copy 组件 A/B**，不是生产 relay 双方向验收，也不是独占性能（`VERIFIED_INTEGRATION` 限于本机链路）。

### 2.4 Box UDP detour 与窄 MTU（e2e）

- 真实 IPv4/UDP 帧经 Go 栈与已启动链的真实路由，从 SOCKS outbound（其连接由第二个 SOCKS outbound 承载 = detour）到真实 UDP echo 并回写设备；两个真实 Box 作跳点。断言：正常往返、entry 必被请求 exit 服务器地址、无 detour 对照 entry 零新流（`VERIFIED_INTEGRATION`，e2e 级）。
- 窄 MTU 变体：设备与栈 MTU 1280，1,000 B 往返；随后发送 2,000 B（**结果未断言**），下一 1,000 B 仍往返。不得表述为"大包已证明失败"（`VERIFIED_UNIT` 限于恢复路径）。
- 反转：`UDP_DETOUR_INVERT=1` 时 with_detour 断言失败；`-race -count=2` 通过（`VERIFIED_UNIT`）。
- 限制：loopback 对端；MTU 由配置施加而非 veth；TUN 夹具是 `protocol/tun.Inbound` 的替身，**嗅探暂存未执行**（`UNVERIFIED`）。

### 2.5 此前已验切片（沿用）

TUN/UDP 字节预算、NAT 会话表上限、关闭/detach、真实 socket detour、XHTTP 控制池/分配账/双预算、真实 h2c 整形 A/B、WSL 真实 TUN 组、route 全包 WSL PASS 527.1 s（`ecff2bc` 生产代码；本轮 `clearWaits` 单点改动后未重跑 route，`VERIFIED_UNIT` + 历史 `VERIFIED_INTEGRATION`）。

## 3. 未完成项与原因

| 项 | 状态 | 原因/所需条件 |
|---|---|---|
| UDP 嗅探暂存与真实 `protocol/tun.Inbound` | 未验收 `UNVERIFIED` | 设备可用性已实测：WSL `/dev/net/tun` 存在，`unshare -rn` 内 `ip tuntap add` 成功；缺真实 inbound 的测试装配（配置、路由、客户端注入），本轮未构建 |
| 窄 MTU 经真实 veth 的 UDP detour | 未验收 `UNVERIFIED` | 现有 e2e 为 loopback、MTU 由配置施加；veth+netem 仅用于 h2c/relay A/B |
| 02.3 MASQUE/H2/QUIC | 未施工 `BLOCKED_BY_ENVIRONMENT` | 需 `sing-quic`/`quic-go` 唯一写入窗口与 pin |
| 03 HY2/Mieru 昂贵建连准入 | 未施工 `BLOCKED_BY_ENVIRONMENT` | 同上 |
| 04 跨 outbound/跨代驻留账 | 局部 `UNVERIFIED` | 诊断仅 `Box.XHTTPUploadBudgetStatus`；跨代账需 Box 装配与设备 |
| Cronet | 未验收 `BLOCKED_BY_ENVIRONMENT` | 固定原生归档与回调 |
| 真机/外网/最终独占采样 | 未验收 `BLOCKED_BY_ENVIRONMENT` | 设备与排期 |
| 真实引擎异常终止撤销、迁移回原队列往返 | 未单测 `UNVERIFIED` | 需真实引擎路径；当前仅用户 Close 与队列关闭路径 |
| Windows route 全包 | 未复跑 `BLOCKED_BY_ENVIRONMENT` | 本机 loopback 端口表耗尽；Linux 已通过 |

## 4. 审核结论摘要

- 审核 526cfc：三项等待者 P1（尺寸上限清空活等待者、幽灵需求、迁移误删）+ relay 夹具数据竞争；修复与红绿见 §2.2/§2.3。
- 审核 de6d：队列关闭只清首个等待者；修复与红绿见 §2.1；同时纠正 UDP 证据口径（大包结果不断言、1280 为配置、嗅探未执行），见 §2.4。
- 本文件不把隔离合并预检当实际集成消费；集成分支由集成窗口单独推进。
