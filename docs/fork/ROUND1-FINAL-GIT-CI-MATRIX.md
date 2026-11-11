# Round 1 最终 Git / CI / 依赖矩阵

观测时间：2026-11-11T11:34:50+08:00（GitHub Compare 查询时间；本地 `git fetch` 后同一窗口）。
比较方向：`upstream...fork`，第一个数 = upstream 独有（behind），第二个 = Fork 独有（ahead）。表中 `behind` 为第一个数。

## 1. 主内核

| 项 | 值 |
|---|---|
| repo | `Piggy-Cat-bit-shadow/JiejieBox` |
| canonical/default | `testing` = `integration/0.1.45-final` = **`2528511d90ab876c6c853f23d6ae1deee083027e`** |
| upstream | `SagerNet/sing-box:testing` @ **`a13dbe1f785f66c86acbf3985c33fdafea0cbfd6`** |
| pre-merge merge-base | `8eb4395a391b775ad240433a7009f39357c1eba0` |
| final merge-base | `a13dbe1f785f66c86acbf3985c33fdafea0cbfd6`（merge 的第二父，已在 HEAD 中） |
| merge commit | `303fbb47dfdca4520dd201fea8bd48b3db1f1010`（parents `48103716845e` + `a13dbe1f785f`） |
| behind / ahead | **0 / 3737** |
| local ancestry | `git merge-base --is-ancestor upstream/testing HEAD` = 0 |
| GitHub Compare | `https://api.github.com/repos/SagerNet/sing-box/compare/Piggy-Cat-bit-shadow:testing...SagerNet:testing`；`ahead_by=0`, `behind_by=3736` |

## 2. 依赖与客户端

| 仓库 | Fork 正式分支 | 正确 upstream | Fork SHA | Upstream SHA | behind/ahead | GitHub Compare（head=真实 SHA） |
|---|---|---|---|---|---|---|
| Verify | 42953972668 | success | 2528511 | 最终审计文档/脚本提交 |
| Android Core ARM64 | 42953972663 | success | 2528511 | 同一审计提交 |
| sing | `dev` | `SagerNet/sing:dev` | `4abfaed41727e43d1d98f36385a86114925f8484` | `27b73774b240c645099704ba533d2615727c3410` | 0/65 | ahead_by=0 |
| sing-tun | `dev` | `SagerNet/sing-tun:dev` | `99abf3c2766646ccd74c77f7b14c197204506121` | `79ed0653fdc66eef261d4147a04db9e16b9e6c50` | 0/183 | ahead_by=0 |
| sing-quic | `main` | `SagerNet/sing-quic:main` | `aa6d98964049e4a7b1446e0758dd4a6ec2e225e6` | `5253224b67cf495b6faba0899a6e3155ee93ebb0` | 0/11 | ahead_by=0 |
| quic-go（正式） | `sagernet` | `SagerNet/quic-go:dev` | `f864f2f12006f053f22d577c90fdd8f3b45d4d47` | `2e73222e969f886968e02d993e6903007e969002` | 0/20 | ahead_by=0 |
| quic-go（消费线） | `integration/0.1.45` | `SagerNet/quic-go:dev` | `158445cdda77c05e4430fe40721709bf7bc2ffe2` | 同上 | 0/32 | 祖先包含；消费线比 sagernet 多 fork 提交 |
| uTLS | `main` | `MetaCubeX/utls:v1.9.0-mod-meta` | `e14b88879d809306332be7d2c226384a6f3e8fba` | `89a07ad380e263f7c3b406a3f7beb2a70dea263c` | 0/69 | ahead_by=0（1.9 线，不是父仓默认 v1.7） |
| WireGuard | `awg3-inject` | `sagernet/wireguard-go:main` | `a765d957fd814e5c0ee579936a0780e73d66e533` | `e4d85ef2ed387cc07233b5324d4a40f3df94566e` | 0/22 | 本地已确认祖先（跨 fork 网络 API 不提供 compare） |
| Cronet（main） | `main` | `SagerNet/cronet-go:main` | `4c96192d73f67c48a91cba3bce358a3c91692444` | `73349ff2a5870631807ce5f69944f55e2f52e1b1` | 0/29 | ahead_by=0 |
| Cronet（go，生成线） | `go` | `SagerNet/cronet-go:go` | `7ce9c49b464ad90629a13061309a58c89890fea6` | `095cb9e060468c80ca31d977c020dca82d101a8f` | **2/31** | **例外，见 §4** |
| Apple | `main` | `SagerNet/sing-box-for-apple:dev` | `97b35d1ce24207ac373f3b2235f16c8831939daf`（pin `7fce711` 在其前 9 个提交） | `768b3de2ba` | 0/220 | ahead_by=0 |
| Android | `main` | `SagerNet/sing-box-for-android:dev` | `ba5c6b161827a830cb7343850bf547156fbfdfee` | `ab4fad3944` | 0/15 | ahead_by=0 |
| Desktop | `main` | `SagerNet/sing-box-for-desktop:main` | `aa63764a61c312562a348e174522d1dc1c86d954`（产品 `e8b48be` 在其前） | `8aab8eab02` | 0/42 | ahead_by=0 |
| Dashboard | `main` | `SagerNet/sing-box-dashboard:main` | `b592c65618e13e2c7ea24a06e758db72c71f65be`（消费线 `9bde90e`） | `3e4bf08e5bf5b9417cf9c36e5a2d5abcb21bdd34` | 0/10 | ahead_by=0 |

API 原始输出：`audit-evidence/upstream/round1-github-compare.txt`（含查询时间、URL、ahead_by/behind_by）。

## 3. 主仓 gitlink 与 source-lock

| 项 | 值 |
|---|---|
| `clients/apple` | `7fce7117b33a38ef4c73c81db67c49f0be7a7dad` |
| `clients/android` | `ba5c6b161827a830cb7343850bf547156fbfdfee` |
| `clients/desktop` | `e8b48beadd7d11a9bce46f62e12820f3e3773518` |
| desktop 内 dashboard | `9bde90e1cd5577dc3f5549535aa23bea69816d6f` |
| source-lock | `release-freeze.py verify --lock release/source-lock.json` → PASS |
| root/test module | `go mod tidy -diff` 空；`go mod verify` PASS |

## 4. Cronet 生成线例外

- `main` 与上游 `main` 零 behind。
- `go` 生成线：fork `7ce9c49`（`362e63b Build from 4c96192d`），上游 `095cb9e`（`3aac4ed Build from 73349ff2`）。
- `73349ff` 是 `4c96192` 的祖先（`git merge-base --is-ancestor` 通过）。fork 的 generation 源更新，包含上游 main 的 Windows TCP reuse 修复；上游 `go` 的两个提交是从更早源 `73349ff` 的平行 generation。
- 合并上游 `go` 会用旧源产物覆盖 fork 的 29 个 native 模块，因此**不合并**；正确判据是生成源 ancestry，不是把平行 generation 伪装成零 behind。解除条件：由受控 native 构建流程从更新源重新生成并对 29 个模块做 ABI/符号/消费者验证。

## 5. CI

| workflow | run | 结果 | head | 备注 |
|---|---|---|---|---|
| Verify | 42945339834 | success | `b473c21` | 最终代码树；其后 HEAD 仅 docs/JSON |
| Android Core ARM64 | 42945339735 | success | `b473c21` | 同一个 head |
| Mobile contract | 42942954909 | success | `08b52a3` | 之后的非文档变更是 `scripts/ci/check-libbox-abi.sh`；Verify 在 b473 已覆盖该脚本 |

`git diff --name-status b473c21..4ec0dec` 只有 5 个文档/审计 JSON 文件；`git diff --name-status 08b52a3..4ec0dec` 的非文档变更只有 `scripts/ci/check-libbox-abi.sh`（Verify 覆盖，mobile 构建本身不依赖它）。因此旧 CI 绿灯与新 HEAD 的文件级边界成立，但这不替代下一轮真实 runner 重跑。

最终报告提交后（`4ec0dec` 之后）的 docs-only 提交没有重新触发全量功能门禁；在本报告中明确记录，不把旧 run 冒充为新 HEAD 的验证。
