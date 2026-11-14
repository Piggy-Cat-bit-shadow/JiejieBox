# 06 测试矩阵（Test matrix）

> 原则：先写能失败的测试，再修复；`SKIP` 不等于 `PASS`；没有运行的项标 `UNTESTED`。
> 所有 raw 证据在 `raw/`，文件名在下表引用。

## 1. 新增测试与它们钉住的行为

### 1.1 `route/packet_setup_ownership_test.go`（提交 `719af51e7`）

| 测试 | 钉住的行为 | 基线 RED 原因 |
|---|---|---|
| `TestNetworkTransitionCancelsAnInFlightPacketSetup` | 网络切换取消非 connected UDP 的在途 setup | else 分支用调用方 ctx，`Reclaim` 无法取消，2 s 超时 |
| `TestPacketSetupCarriesTheTrafficClass` | 解析出的 traffic class 到达 outbound 的准入 seam | 基线不 `WithClass`，`ClassFromContext` 报告未设置 |
| `TestEstablishedPacketFlowSurvivesATransition` | 建立即转移所有权：setup ctx 被释放，切换不关闭已建立 socket | 基线 setup ctx 就是调用方背景 ctx，永不取消 |

### 1.2 `service/oomkiller/footprint_unavailable_test.go`（提交 `8f00f91f4`）

| 测试 | 钉住的行为 | 基线 RED 原因 |
|---|---|---|
| `TestUnmeasurableFootprintDoesNotPublishAllClear` | 不可测量 → 发布 Warning（ARMED），不是 None | 0 被当作测量 → 发布 None |
| `TestUnmeasurableFootprintKeepsCrisis` | 已测得的 Triggered 不被丢失的测量清除 | 0 < armed → 回到 Normal |
| `TestUnmeasurableFootprintHoldsArmed` | ARMED 保持，不抖动 | 同上 |
| `TestFootprintZeroIsUnavailable` | 0 读被分类为不可用；非 0 仍是测量 | 新 API 不存在（编译失败） |
| `TestTimelineReportsUnavailableFootprint` | 报告显式带 `footprintUnavailable`，已知样本不带 | 字段不存在（编译失败） |

### 1.3 `transport/v2rayquic/admission_test.go`（提交 `858868d96`）

| 测试 | 钉住的行为 | 基线 RED 原因 |
|---|---|---|
| `TestSaturatedAdmissionRefusesTheSharedCreation` | 预算饱和时拒绝创建，且不执行构造器 | 完全没有准入，构造器被调用 |
| `TestEstablishedSessionIsReusedWithoutASecondPermit` | 复用不计费；许可在创建返回时释放 | `Admitted=0`（从未计费） |
| `TestTheSharedCreationSeesTheTriggeringFlowClass` | 触发创建的 flow class 进入创建 ctx | 创建 ctx 不携带调用方值 |
| `TestAnAlreadyAdmittedDialIsNotChargedTwice` | 已持有 permit 的嵌套 dial 不再领第二份 | 基线通过（没有准入）；作为修复实现的回归护栏 |

## 2. RED / GREEN 证据

| 证据 | 文件 | 结果 |
|---|---|---|
| 基线（`fa6150073`，干净工作树）相关包 | `raw/00_base_tests.txt` | 全部 `ok` |
| 基线 + 本轮测试文件（生产代码未改） | `raw/10_red_on_base.txt` | 3 组测试按预期失败/编译失败；`TestAnAlreadyAdmittedDialIsNotChargedTwice` 通过（符合预期） |
| 修复后同组测试 | `raw/20_green_branch.txt` | 全部 PASS；相关包回归全绿 |
| 全量 `route` 包 | `raw/32_route_full_summary.txt` | PASS（446.9 s，1182 条 PASS / 0 条 FAIL） |
| 构建 | `raw/30_build_and_vet.txt` | `go build ./...` exit 0；`go vet` 受影响包 exit 0 |

RED 的具体断言（摘自 `raw/10_red_on_base.txt`）：

```text
--- FAIL: TestNetworkTransitionCancelsAnInFlightPacketSetup (2.00s)
    a network transition did not cancel the in-flight non-connected UDP setup
--- FAIL: TestPacketSetupCarriesTheTrafficClass
    the resolved traffic class must travel to the outbound's admission seam
--- FAIL: TestEstablishedPacketFlowSurvivesATransition
    Expected error with "context canceled" in chain but got nil
--- FAIL: TestSaturatedAdmissionRefusesTheSharedCreation
    Expected error with "resource guard: admission queue is full" in chain but got nil
--- FAIL: TestEstablishedSessionIsReusedWithoutASecondPermit
    expected: int(1) actual: uint64(0x0)   (a reuse was charged as a creation)
--- FAIL: TestTheSharedCreationSeesTheTriggeringFlowClass
    the creation ran without the triggering flow's values
```

## 3. 竞态检测（`-race`）

环境：`CGO_ENABLED=1`，`C:\msys64\mingw64\bin\gcc`。

| 命令 | 结果 |
|---|---|
| `go test -race ./service/oomkiller -count=1` | ok |
| `go test -race ./common/resourceguard -count=1` | ok |
| `go test -race ./route -run 'TestNetworkTransition\|TestPacketSetup\|TestEstablishedPacketFlow\|TestCallerCancellation\|TestCloseCancelsInFlightDials\|TestRepeatedTransitions' -count=1` | ok |
| `go test -race -tags with_quic ./transport/v2rayquic -run '<admission + shared offer tests>' -count=1` | ok |

## 4. 未运行 / 无法运行

| 项 | 状态 | 缺什么 |
|---|---|---|
| iOS Network Extension 真机 footprint / Jetsam | `NEEDS_DEVICE` | iPhone + 签名 NE 构建 + 诊断导出 |
| Apple 交叉构建（iOS/macOS arm64） | `BLOCKED_BY_ENVIRONMENT` | Apple SDK、cgo 交叉工具链 |
| 真实 TUN 设备下的 200/500/1000 流 UDP 压测 | `BLOCKED_BY_ENVIRONMENT` | 本机 Windows 无 TUN 设备；现有纯 Go 注入测试不能替代 |
| Xray 互通 / XHTTP H3 长稳 | `UNTESTED`（本轮未改该路径） | 未在本轮范围 |
| 吞吐/延迟基准 | `UNTESTED` | 见 `07` |

## 5. 复现命令

```sh
# 基线与 RED（需要把本轮三个测试文件复制到一个 fa6150073 的干净 worktree）
git worktree add --detach /tmp/rg-base fa6150073
cp route/packet_setup_ownership_test.go            /tmp/rg-base/route/
cp service/oomkiller/footprint_unavailable_test.go /tmp/rg-base/service/oomkiller/
cp transport/v2rayquic/admission_test.go           /tmp/rg-base/transport/v2rayquic/
(cd /tmp/rg-base && go test ./route ./service/oomkiller -run 'Packet|Footprint|Timeline' -count=1)
(cd /tmp/rg-base && go test -tags with_quic ./transport/v2rayquic -run 'Admission|SharedCreation|SecondPermit|ChargedTwice' -count=1)

# 修复后
go test ./route -run 'TestNetworkTransitionCancelsAnInFlightPacketSetup|TestPacketSetupCarriesTheTrafficClass|TestEstablishedPacketFlowSurvivesATransition' -count=1
go test ./service/oomkiller -run 'TestUnmeasurableFootprint|TestFootprintZeroIsUnavailable|TestTimelineReportsUnavailableFootprint' -count=1
go test -tags with_quic ./transport/v2rayquic -run 'TestSaturatedAdmissionRefusesTheSharedCreation|TestEstablishedSessionIsReusedWithoutASecondPermit|TestTheSharedCreationSeesTheTriggeringFlowClass|TestAnAlreadyAdmittedDialIsNotChargedTwice' -count=1

# 竞态
go test -race ./common/resourceguard ./service/oomkiller -count=1
go test -race ./route -run 'TestNetworkTransition|TestPacketSetup|TestEstablishedPacketFlow' -count=1
go test -race -tags with_quic ./transport/v2rayquic -run 'Admission|SharedCreation|SecondPermit|ChargedTwice|OneOffer|AWaiter' -count=1
```
