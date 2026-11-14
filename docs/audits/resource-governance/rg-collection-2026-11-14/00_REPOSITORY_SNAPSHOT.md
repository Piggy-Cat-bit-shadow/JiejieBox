# 00 仓库与改造快照（Repository snapshot）

> 采集时间：2026-11-14（Asia/Shanghai）
> 证据级别：Git/SHA 为 `VERIFIED_GIT`；CI 为 `VERIFIED_CI`（但**不绑定最终 HEAD**，见 §6）；环境为 `VERIFIED_SOURCE`。

## 1. 快照字段

| 字段 | 值 |
|---|---|
| Canonical repository | `https://github.com/Piggy-Cat-bit-shadow/JiejieBox.git`（旧 `Piggy-Cat-bit-shadow/sing-box` URL 重定向到同一仓库 id `1383685561`） |
| Base branch | `testing` |
| Base SHA | `fa61500731b1237e9ef44029b7469ee5373af4ba`（2026-11-14 07:44:39 +0800） |
| Feature branch | `work/resource-governance-closure-2026-11-14` |
| Initial implementation HEAD（第一阶段最终） | `ce95f50ea1cc98b57560cec79cf3e361bd0dbc0b` |
| Debug HEAD（第二阶段最终） | `f1f0ac4e9d94d21e04b38b8e10cdea6aa8b2074f` |
| Collection HEAD（本阶段最终） | 本证据包的提交；SHA 记录在 `README.md` 与 `raw/90_collection_push.txt`（提交后写入） |
| Push status | 第一阶段：已推送并核对；第二阶段：已推送并核对；第三阶段：本包提交后推送 |
| Working tree（采集前） | `CLEAN`（全新 clone，`git status --short --branch` 只有分支行） |
| Remote parity（采集前） | 本地 HEAD = `origin/work/...` = `f1f0ac4e9d94d21e04b38b8e10cdea6aa8b2074f` |
| CI | 见 §6：最近一次绑定 SHA 的成功运行在 `354a74fbb`；最终 HEAD 没有运行 |

`base..HEAD` 规模：**47 files changed, 3549 insertions(+), 66 deletions(-)**（`git diff --stat fa6150073...HEAD`）。

## 2. 提交清单（`git log --oneline fa6150073..HEAD`）

| 提交 | 阶段 | 作用 |
|---|---|---|
| `719af51e7` | 1 | route：非 connected UDP setup 接入 class / dial gate / setup gate |
| `8f00f91f4` | 1 | oomkiller：采样失败不再被当成"无压力" |
| `858868d96` | 1 | v2rayquic：共享 QUIC 创建接入准入 |
| `a81a2db01` | 1 | 第一阶段证据包（00–08 + raw） |
| `c685a5a7a` | 1 | push 记录 |
| `ce95f50ea` | 1 | push 记录（第一阶段最终 HEAD） |
| `6d887ee49` | 2 | `Guard.Stats()` 快照一致性 |
| `f52b8102a` | 2 | `Permit.Wait()` 只计准入等待 |
| `c92ae7f34` | 2 | route：本地准入拒绝不再计入节点健康（含被全量套件抓住后的收窄） |
| `26eaf64a1` | 2 | 第一轮对抗测试 |
| `5914597b4` | 2 | 队列上界 + 平台隔离测试 |
| `a6b7d5135` | 2 | setup 路径基准 |
| `354a74fbb` | 2 | Debug 报告与台账 |
| `f1f0ac4e9` | 2 | Debug push 记录（第二阶段最终 HEAD） |

## 3. 生产代码变更（6 个文件）

```text
common/resourceguard/gate.go        Permit.granted/Wait 语义（第二阶段）
common/resourceguard/guard.go       Stats() 快照一致性（第二阶段）
route/conn.go                       非 connected UDP setup gate/class（第一阶段）
                                    + recordSetupFailure/admissionBackpressure（第二阶段）
service/oomkiller/recorder.go       报告字段 footprintUnavailable（第一阶段）
service/oomkiller/timer.go          不可测量 footprint → ARMED（第一阶段）
transport/v2rayquic/client.go       共享创建 admission + offer context（第一阶段）
```

## 4. 测试与基准变更（13 个文件）

```text
common/resourceguard/admission_ledger_test.go
common/resourceguard/guard_stats_snapshot_test.go
common/resourceguard/permit_wait_test.go
common/resourceguard/queue_bound_test.go
route/admission_rejection_health_test.go
route/packet_setup_bench_test.go
route/packet_setup_ownership_test.go
service/oomkiller/footprint_unavailable_test.go
service/oomkiller/platform_scope_test.go
service/oomkiller/poll_ordering_test.go
transport/v2rayquic/admission_lifecycle_test.go
transport/v2rayquic/admission_test.go
transport/v2rayquic/offer_bench_test.go
```

## 5. 文档/证据与排除项

- 文档/证据：28 个路径（`docs/audits/resource-governance/**`），不含生产行为。
- **CI 配置无变更**：`git diff --name-only fa6150073...HEAD -- .github` 为空。
- **依赖 pin 无变更**：`git diff --name-only fa6150073...HEAD -- go.mod go.sum test/go.mod` 为空。`sing-tun` / `sing-anytls` / `sing-quic` / `mieru` / `quic-go` / `sing` 的 pin 与基线相同（见第一阶段 `01_BASELINE_AND_SCOPE.md` §6）。
- **未触及其他仓库**：`clients/` 是树内目录，无 gitlink 变更；无嵌套子模块提交。
- 明确排除、未改动的模块及原因：
  - XHTTP/XMUX、MASQUE、Mieru、Naive/Cronet 的生产代码：不属于本轮三项改造；只做只读审计与测试，未修改。
  - TUN / UDP NAT（`sing-tun` 依赖）：问题记录为 `DEFERRED`，本轮不改依赖仓库。
  - Cronet / native 运行时：无 Apple 环境，未构建未测量。

## 6. CI 状态（绑定到 SHA 的核实）

- 工作流 `resource-guard.yml`（name: **iOS resource guard**）在 `push` 且路径命中 `common/resourceguard/**`、`common/tls/**`、`protocol/anytls/**`、`service/oomkiller/**` 时运行。
- **Run `43545040780`**：head SHA `354a74fbb975c2621c2becdf2316dfd1299a941d`，event `push`，status `completed`，conclusion **`success`**；两个 job 均 success：
  - `race and unit`（ubuntu-latest）：gofmt、unit tests under the race detector；
  - `burst matrix`（ubuntu-latest）：admission burst and session burst repeated、refusal behaviour at critical、admission overhead。
- **最终 HEAD `f1f0ac4e9` 没有任何 workflow run**（`actions/runs?head_sha=f1f0ac4e9…` → `total_count: 0`）。原因：最后一个提交只改 `docs/**` 与 `raw/**`，不命中该工作流的路径过滤器；而 `f1f0ac4e9` 与 `354a74fbb` 的生产代码完全相同（`git diff --stat 354a74fbb..f1f0ac4e9` 只有 docs/raw）。
- `verify.yml` 的 `push` 触发器只包含 `branches: [testing]`，因此在 feature branch 上**从未运行**；`pull_request` 触发器需要 PR 存在。不得把"工作流存在"或"其它 SHA 通过"当作最终 HEAD 的 PASS。
- 如实结论：**CI 在最近一个包含生产代码的提交上成功；最终 HEAD 的 CI 状态为 `NOT_RUN`（路径过滤），不是 PASS，也不是 FAIL。**

## 7. 环境

```text
OS          : Windows 11 本机
Go          : go1.25.5 windows/amd64
CGO         : 0（常规）；1 + mingw64 gcc（race）
CPU         : AMD Ryzen 7 4800H（16 逻辑核）
GOMODCACHE  : D:\Go\mod-cache
构建标签     : release/DEFAULT_BUILD_TAGS_OTHERS（Windows）：
              with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,
              with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,
              with_openconnect,with_xhttp,with_trusttunnel,with_awg,badlinkname
```

Apple 构建、Network Extension、Cronet 均不在本机能力内（`BLOCKED_BY_ENVIRONMENT` / `NEEDS_DEVICE`）。
