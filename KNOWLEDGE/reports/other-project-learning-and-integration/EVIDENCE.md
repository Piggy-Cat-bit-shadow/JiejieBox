# EVIDENCE —— 命令、revision、环境与结果

本文件是本轮 01 的证据索引。**每条都写明证据等级**；主机单测/基准不能当设备或互通证据。

## 环境

| 项 | 值 |
|---|---|
| OS | Windows 10（`Microsoft Windows NT 10.0.19044.0`，build 19044） |
| CPU | AMD Ryzen 7 4800H with Radeon Graphics（`GOMAXPROCS` 报告 16） |
| Go | `go1.25.5 windows/amd64`（GOROOT `<toolchain>`，`GOPATH <toolchain>/gopath`） |
| C 编译器（仅 `-race` 需要） | `gcc.exe (Rev6, Built by MSYS2 project) 16.2.0`，`CGO_ENABLED=1` |
| 采集时间 | 2026-11-08（Asia/Shanghai） |

复现时先设置工具链，再在仓库根执行命令；`-race` 需要可用的 gcc。

## E01 仓库同步与阶段 revision

```text
进入时：git status -sb → ## testing...origin/testing；HEAD=0de1c13dcb3（2026-11-07 09:19:36 +0800，release(0.1.40)）
git rev-list --left-right --count origin/testing...HEAD → 0    0（本地无提交，落后 39）
git fetch origin --prune；git ls-remote origin refs/heads/testing → 25f5c9a638b
git merge --ff-only origin/testing → 快进；HEAD=25f5c9a638b（2026-11-08 02:08:39 +0800）
git submodule update --init clients/apple → e3283104c98c（父仓 pin）
```

- 结果：`testing` = `25f5c9a638bdae4a1e2bb460641795f10b169bb2`，与远端一致；工作树唯一改动是**进入前已存在**的 `clients/android` 子模块回退（`9d15717a85c5`），本轮未触碰。
- 等级：`VERIFIED_GIT`。

## E02 本阶段触及区域的知识增量

```sh
git rev-list --count fc7e400243d46a451b2dffbe68cb5a3147c66289..HEAD          # 49
git diff --name-only fc7e400243d46a451b2dffbe68cb5a3147c66289..HEAD -- '*.go' ':(exclude)*_test.go'
git log --oneline 3b6ec64f7145a2add99656d47caaa4f22a1cc478..HEAD              # 6 个纯文档提交
```

- `common/resourceguard`、`service/oomkiller`、`common/tls`、`protocol/anytls` 在 v0.1.39→HEAD 区间**没有生产代码变更**；变更集中在 provider/outbound/route/TUN/生成器与文档。
- 结果：01 依据的 gate/guard/recorder/timer 代码与当前 HEAD 一致；知识库 `modules/resource-admission.md`（v0.1.39 校准）仍适用。
- 等级：`VERIFIED_GIT`。

## E03 参考项目远端 refs（S01）

```sh
git ls-remote --symref <repo> HEAD
```

| repo | ref/HEAD |
|---|---|
| `NodePassProject/Anywhere` | `main` = `8b1ec05bc5e0e2d4e477d9aced0b9832bbeff61d` |
| `ProjectClash/Clash` | `main` = `e6334ad18cc7ee7582bf0f3a70681d58a2b61eb5` |
| `ProjectClash/Clash-Legacy` | `main` = `da4952d3cac6794138835da23f293171d2593704` |
| `TokenPLS/Hako` | 解析到同一 `da4952d3cac…`（重定向） |
| `ProjectClash/Clash-Client` | `main` = `e3e647283b1593a61dba622f13cd499deeda9b8b` |
| `ProjectClash/Clash-Adapter` | `main` = `d47c04a3556a67f95929aaa15ae624c7df631e74` |
| `reF1nd/sing-box` | `reF1nd-stable` = `732618a5989cd2566f072c374fe2f619200f33f9`；`testing` = `a6952ccf6907c18e18917fcb763a433e8e36937b`；`reF1nd-testing` = `17397d022647b73378705e1f91ff031dddaaec21` |
| `reF1nd/sing-box-releases` | `v1.14.4-reF1nd` 标签对象 = `de5581802e18e99c5040da1e23404dee2312b253` |
| `Leadaxe/sing-box-lx` | `lx` = `2d9a0a52df7881f55484d48f9daecb21d135f3e9` |
| `ghostlane-project/ghostlane` | `main` = `479019064d1b5cd035275a5a79cd841b2f463a70` |
| `pickrui/oixCloud-docs` | `main` = `25320145fad64b76d38090b8d4bbedc64565c938` |
| `pickrui/oixcloud-external-proxy-program` | `main` = `ff8b13a29575f82293a9968551279e027a311e0a` |
| `jichangbaike/oixcloud` | `main` = `72d76d1f1351e28eb4cfbf1469f56b78cd345c30` |

- 结果：研究引用的 reF1nd 发布内核 `732618a` 是内核仓的 `reF1nd-stable`/`v1.14.4-reF1nd`，不是发布仓标签对象；Hako 重定向由相同 HEAD 证实。
- 等级：`VERIFIED_GIT`（远端读取）。

## E04 目标依赖与客户端锁

```sh
git submodule status
Select-String -Path go.mod -Pattern '^replace'
Get-Content release/source-lock.json
```

- `sing 1729220c46c4`、`sing-tun 8f65451175c0`、`sing-quic 1ef2597cc129`、`quic-go 2daa76f5bd52`、`utls b63a2e9a7b1e`、`cronet-go 362e63b14ce2`、`cronet-go/all 7ce9c49b464a`。
- 客户端：apple `e3283104`、android `c7837107`（父仓 gitlink）、desktop `41722b0`；nested dashboard `0da34172`；cronet native 29 模块 `controlled`。
- `release/source-lock.json` 的 `release_version` = `0.1.44`。
- 等级：`VERIFIED_GIT` + `VERIFIED_SOURCE`（`dependencies.json` 的 intent/derived 分工由文件自述与 `release-freeze.py` 消费者印证）。

## E05 单元测试（改动包）

```sh
go test ./service/oomkiller -count=1        # ok  0.716s
go test ./common/resourceguard -count=1     # ok  0.823s
```

- `service/oomkiller` 覆盖：原 layered/rhythm/overlap/executor 用例 + 本轮新增的 7 个观测用例（记录、未知 vs 零、冷却、环上界、pressure/crisis 分支、报告写入路径、metadata 形状）。
- 等级：`VERIFIED_UNIT`（主机）。

## E06 竞态检测

```sh
CGO_ENABLED=1 go test -race ./common/resourceguard ./service/oomkiller -count=1
ok  	github.com/sagernet/sing-box/common/resourceguard	1.884s
ok  	github.com/sagernet/sing-box/service/oomkiller	1.765s
```

- 等级：`VERIFIED_UNIT`（race detector，主机）。

## E07 全仓编译

```sh
go build ./...    # exit 0
```

- 等级：`VERIFIED_BUILD`（主机，默认构建标签）。未覆盖带 `with_purego`/`with_utls`/平台标签的发行档位。

## E08 负载定义：准入 burst

```sh
go test ./common/resourceguard -run 'TestAdmissionLoadDefinition|TestAdmissionOverloadLoadDefinition' -count=1 -v
LOAD admission burst: callers=192 limit=32 queue=256 work=2ms admitted=192 refused=0 maxInflight=32 p50=7.0998ms p95=11.9904ms total=14.1516ms
LOAD admission overload: callers=128 limit=8 queue=16 admitted=24 refused=104 maxInflight=8 total=2.7062ms
```

- 解释：192 个调用者全部被吸收（limit 32 + queue 256），`maxInflight=32` 证明上限；超载场景 104 个在常数时间内被拒，`maxInflight=8`。
- 证据等级：主机合成负载（`VERIFIED_UNIT` 级），**不是**吞吐/设备结论。

## E09 负载定义：回收控制环

```sh
go test ./service/oomkiller -run 'TestReclaimCycleLoadDefinition' -count=1 -v
LOAD reclaim cycles: samples=200 actions=100 suppressed=100 totalGainMiB=100 recent=8 elapsed=0s
```

- 解释：100 个“有下降”的采样各触发一次 pass，100 个稳定采样被抑制；模拟 footprint 下环保持 8 条、收益累计 100 MiB；`elapsed≈0` 说明观测与判定本身没有引入可测的固定开销。
- 等级：主机合成（`VERIFIED_UNIT` 级），模拟 footprint，不是真实回收量。

## E10 微基准基线（准入记账）

```sh
go test ./common/resourceguard -run '^$' -bench 'BenchmarkGateAcquireRelease' -benchtime=200000x -count=3
BenchmarkGateAcquireRelease-16           200000  252.2 ns/op  48 B/op  1 allocs/op
BenchmarkGateAcquireRelease-16           200000  252.6 ns/op  48 B/op  1 allocs/op
BenchmarkGateAcquireRelease-16           200000  252.6 ns/op  48 B/op  1 allocs/op
BenchmarkGateAcquireReleaseContended-16  200000  1975 ns/op   504 B/op  6 allocs/op
BenchmarkGateAcquireReleaseContended-16  200000  1901 ns/op   504 B/op  7 allocs/op
BenchmarkGateAcquireReleaseContended-16  200000  1937 ns/op   504 B/op  7 allocs/op
```

- 早一次同命令的结果：uncontended 267.4/271.4/261.2 ns/op；contended 2018/1958/2006 ns/op。**用区间描述，不选最好值**：uncontended ≈ 250–272 ns/op，contended ≈ 1.9–2.0 µs/op。
- 用途：以后给 gate 加计数/时间戳时用同一负载比较，避免“凭感觉说没影响”。
- 等级：主机微基准（`VERIFIED_UNIT` 级），非生产吞吐。

## E11 相邻包回归

```sh
go test ./common/tls ./protocol/anytls -count=1
ok  	github.com/sagernet/sing-box/protocol/anytls	5.960s
--- FAIL: TestWindowsClientHandshakeTLS13 / TestWindowsClientRoundtripTLS13 / TestWindowsClientTLS13PostHandshakeConcurrentWrite
    windows_client_test.go:... tls handshake: read handshake: unexpected EOF
```

- 结论：AnyTLS 全通过；`common/tls` 的 3 个 `TestWindowsClient*TLS13` 失败与本轮改动无关——`KNOWLEDGE/ACCEPTANCE.md` 的“已知失败与已知不稳定集合”已在同一主机/OS build/Go 版本记录同名三个失败与相同错误，成因未确立。
- 等级：`VERIFIED_UNIT`（AnyTLS）、`BLOCKED_BY_ENVIRONMENT`（3 个 TLS 用例，既有失败，不计 PASS）。

## E12 生产写入路径

- 代码：`Service.startTimer` 在 `start()` 前挂 `reclaim`；`NewService` 在 `recorder != nil` 时注册 `SetExtraMetadata("memoryReclaim", s.reclaimMetadata)`。
- 测试：`TestReportMetadataCarriesTheReclaimRecordNextToTheClientDocument` 用真实 `Recorder` 建 draft、写 `metadata.json`，断言 `memoryReclaim` 与客户端键（`schema`、`peakMemory`）同时存在，且 `actions=1`。
- 等级：`VERIFIED_UNIT`（真实文件写出）。

## E13 未验证 / 边界

| 项 | 状态 | 说明 |
|---|---|---|
| iOS NetworkExtension 真机 | `BLOCKED_BY_ENVIRONMENT` | 需设备；本轮没有运行 NE，`memoryReclaim` 在 NE 下的实际内容未设备验证 |
| CI 对本提交的运行 | `NOT_RUN` | 提交后在远端 CI 可能运行；本轮不以本地结果冒称 `VERIFIED_CI` |
| 带平台标签的构建（`with_purego`/`with_utls`/Darwin/Android） | `NOT_RUN` | 默认 `go build ./...` 通过，不代表发行档位 |
| 回收收益的真实量级 | `UNMEASURED` | 主机测试用模拟 footprint；真实 `GainBytes` 取决于进程与平台 |
| 常驻/关闭/构建占用观测 | `NOT_IMPLEMENTED` | 归 04/06，见 `MASTER_PLAN.md` 的 G01 拆分 |
