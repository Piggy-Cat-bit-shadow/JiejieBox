# Apple: one macOS runner for the macOS-only acceptance

- 日期：2026-10-29
- 主库：`Piggy-Cat-bit-shadow/sing-box`，分支 `testing`
- 目标：让 macOS-only 候选验收**只申请一次 macOS runner**，并在同一个 job 里跑完
  源码检查 → Libbox 构建 → ABI 验证 → macOS 源码准备 → Xcode 编译 SFM → 产物/版本/身份验证 → 上传

## 1. 问题不是源码，是 DAG

改造前的 `client-apple.yml` 对一次 macOS-only 验收要**两个** macOS runner：

```
libbox (macos)  ──►  构建 Libbox.xcframework、跑层 1–5 ABI 门、上传 artifact
macos  (macos)  ──►  下载同一 artifact、准备 macOS 源码、编译 SFM、出 DMG
```

前一作业成功并不保证后一作业拿到 runner。2026-10-28 深夜的取证：**14 次 macOS 作业派发，只有 3 次
拿到 runner**；被取消的作业特征完全一致——`runner_name` 为空、`steps=0`、排队约 15 分钟后
`cancelled`。其中一次成功的 `libbox`（run `40730518254`）证明了框架与 ABI；一次探针的 `macos`
（run `40739637008`）证明了"提前排队就能拿到 runner"，却因为我自己的 artifact 下载写错作业而失败。

结论：**能省一个 runner 就省一个**。这不是"多试几次"能解决的问题。

## 2. 设计：把 Libbox 构建搬进同一个 job，且只保留一份实现

### 2.1 一份实现：`.github/actions/apple-libbox-build`

一个 composite action 承担：

```
install the gomobile toolchain   → scripts/ci/gomobile-toolchain.sh install apple
warm the Go module cache         → go mod download all
libbox ABI gate                  → scripts/ci/check-libbox-abi.sh --client-root <root>
build Libbox from this fork      → APPLE_CLIENT_DIR=<install-dir> scripts/ci/build-apple-libbox.sh both
pack the Libbox artifact         → scripts/ci/apple-libbox-artifact.sh pack <install-dir>/Libbox.xcframework <output-dir>
```

`libbox` job 与 `macos` job 都调用它，只传不同的路径：

| job | client-root | install-dir | output-dir |
|---|---|---|---|
| `libbox`（双平台） | `clients/apple`（已移到解析出的 SHA） | `clients/apple` | `dist/apple/libbox` |
| `macos`（macOS-only） | `build/apple-client-macos`（本次解析出的 SHA，就是要编译的树） | `build/apple-libbox-build`（临时目录） | `build/apple-libbox` |

两条路径随后**汇合到同一个 `validate and install the Libbox artifact` 步骤**（`--parent-sha` +
`--jj-version` 校验后安装），所以 overlays、品牌检查、`xcodebuild`、DMG、BUILD-INFO 全都只有一份，
"哪种模式产的框架"在这一点之后不可见。

### 2.2 依赖与跳过语义

```yaml
libbox:
  if: ${{ inputs.build_ios }}          # macOS-only 不申请第二个 runner
macos:
  needs: [resolve-apple-source, libbox]
  if: ${{ !cancelled() && inputs.build_macos &&
          (needs.libbox.result == 'success' || needs.libbox.result == 'skipped') }}
```

`!cancelled()` 与 `result == 'skipped'` 缺一不可：GitHub 的规则是**依赖被 skip 时下游也 skip**，
所以单 runner 模式会静默变成"什么都没跑"。`result == 'success'` 那一支保证 libbox **失败**时
`macos` 仍然不跑（不能拿一个不存在的共享 artifact 去装）。

`macos` 内部则用同一对条件二选一：

```yaml
- name: build Libbox inside this job (macOS-only mode)
  if: ${{ needs.libbox.result == 'skipped' }}
  uses: ./.github/actions/apple-libbox-build
  with: { client-root: build/apple-client-macos, install-dir: build/apple-libbox-build, output-dir: build/apple-libbox }

- name: download the verified Libbox artifact (dual-platform mode)
  if: ${{ needs.libbox.result == 'success' }}
  uses: actions/download-artifact@v4
  with: { name: jiejiebox-libbox-${{ github.sha }}, path: build/apple-libbox }
```

### 2.3 候选 SHA 由 dispatch 指定

新增 `workflow_dispatch` 输入 `apple_client_sha`（40-hex，空表示 refs 文件的 branch tip）。
不要求改仓库变量：变量是所有后续 run 共享的，"这次 run 说 X、实际构建 Y" 正是本流水线要消灭的歧义。

优先级与拒绝规则由 `scripts/ci/apple-client-input.sh resolve` 决定（有 13 条行为测试）：

| 情形 | 结果 |
|---|---|
| 只给 input SHA | 用它，`override=input` |
| input SHA + 变量 SHA 相同 | 用它，`override=input` |
| input 与变量 SHA 不同 | **拒绝**（两套真相） |
| input SHA + 变量 REF | **拒绝** |
| 只有变量 SHA / 只有变量 REF | 照旧，`override=variable` |
| 都没有 | 解析 refs 文件的 branch tip，`override=none` |
| input 是缩写 / 大写 / 分支名 / 带空白 | 缩写与大写拒绝；**带空白会 trim**；分支名拒绝 |

解析作业把验证过的答案用 `K=V` 逐字段读入（**不用 `eval`**：仓库变量可能带 shell 元字符），
再交给 `apple-client-source.sh`；随后断言"解析器返回的 SHA == 覆盖要求的 SHA"，覆盖没生效就失败。

## 3. 结构门：不是画出来的图

`scripts/ci/check-release-workflows.py` 新增 `check_apple_single_runner_mode()`（16 项），其中包含一个
**从 YAML 求值的 planner**，对四种输入组合算出"哪些 job 会跑、其中几个在 macOS runner 上"：

| 输入 | 计划 | macOS runner 数 |
|---|---|---|
| dual | `libbox, ios, macos` | 3 |
| **macOS-only** | **`macos`** | **1** |
| iOS-only | `libbox, ios` | 2 |
| neither | — | 0 |

另外断言：`libbox.if` 就是 `inputs.build_ios`；`macos.if` 同时含 `!cancelled()`、`= 'success'`、
`= 'skipped'`；in-job 构建步骤与下载步骤**各一个且条件互补**；validate/install 步骤**无条件**（两条路汇合）；
composite action 存在且类型为 composite；这两个框架步骤里没有 `continue-on-error`/`|| true` 之类的掩码。

## 4. 真跑才暴露的两个缺陷

### 4.1 两个符号链接少了一级 `../`（fork 侧）

`Tests/SubscriptionQuotaCaption/Sources/Caption/` 下的两个源文件是指向 `Library/Network/` 的符号链接，
目标写成 `../../../Library/...`，而同一个仓库里其它 12 条都是 `../../../../Library/...`。从
`Tests/SubscriptionQuotaCaption/Sources/Caption` 上溯三级是 `Tests/`，于是链接指向 `Tests/Library/...`（不存在）。

为什么一直没暴露：**Windows 检出会把符号链接落成"内容是目标路径的小文本文件"**，链接"存在"，
只是永远不是它声称的源码；我在 Windows 上跑的 ABI 扫描甚至因此"通过"了。真实 POSIX 检出里它是断链，
macOS job 的层 4 直接变成 SKIP：

```
{"scanned": 0, "violations": [], "error": "[Errno 2] No such file or directory:
 'build/apple-client-macos/Tests/SubscriptionQuotaCaption/Sources/Caption/SubscriptionInfo.swift'"}
```

修复：两条链接改为四级上溯（候选分支 `13726756`）。验证方式是把 `Tests/` 下**每一条**符号链接按路径
解析回仓库对象（14 条，全部命中）。

### 4.2 安装目录必须先存在

`build-apple-libbox.sh` 只在**已存在的 Apple 客户端检出**里安装框架，不会凭空造目录——这是对的，
它不该制造一棵不是 Apple 源码的树。于是 macOS-only 模式要把框架先装到临时目录时，必须先
`mkdir -p "$install_dir"`：

```
FAIL: build/apple-libbox-build is missing; check the Apple client source out first.
```

## 5. 被保留的不变量

- 双平台路径的**共享 artifact 拓扑不变**：仍然一次构建、两个 GUI 作业下载同一份；
- 层 1–5 ABI 门**没有被削弱**：macOS-only 模式反而扫描的是**要编译的那棵树**（`build/apple-client-macos`），
  比扫描 gitlink 副本更强；SKIP 不是 PASS（脚本原文如此）；
- 产物契约不变：`--parent-sha` 仍指向产出该框架的父内核提交，`--jj-version` 仍与
  `release/JIEJIE_VERSION` 比对；没有"跳过 manifest"或"伪造相等"；
- 未新增长期分支；探针分支已删除；
- 没有为了 GitHub 的 behind 数字制造合并提交。

## 6. 本地验证（可复跑）

```sh
python3 scripts/ci/check-release-workflows.py        # 159 passed, 0 failed（含 16 项 Apple 模式断言）
./scripts/ci/test-apple-client-input.sh              # 13 passed, 0 failed
./scripts/ci/test-gomobile-toolchain.sh              # 13 passed, 0 failed（安装点在 composite action）
python3 scripts/ci/test-release-freeze.py            # 35 passed, 0 failed
python3 scripts/ci/release-freeze.py verify --lock release/source-lock.json
```

## 7. 验收运行

见 §7.1（本文件随后更新）：`gh workflow run client-apple.yml --ref testing -f build_ios=false
-f build_macos=true -f apple_client_sha=<candidate>`。

## 8. 结论

结构改造完成并有测试与结构门守着：macOS-only 验收从"2 个 macOS runner"降到"1 个"，且这一个是
**唯一**的 macOS 作业。是否能拿到这个 runner 不在仓库控制范围内；拿不到时本文件第 7 节记录的是
"作业未获 runner"这一事实，而不是源码失败。
