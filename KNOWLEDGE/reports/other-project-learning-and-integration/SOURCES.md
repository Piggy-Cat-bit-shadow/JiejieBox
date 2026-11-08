# SOURCES —— 参考项目、依赖与闭源边界（S01）

日期：2026-11-08（Asia/Shanghai）。**本文件只陈述来源事实与核验方式，不是本项目的运行证据。** 参考项目的历史 SHA 用于定位研究材料，不指定本项目施工起点；本项目始终在最新 `testing` 施工。

## 核验方式

```text
目标：git remote -v / git rev-parse / git ls-remote / git submodule status / release/source-lock.json
参考：git ls-remote --symref <url> HEAD（远端真实 refs）；本地 checkout 另记 git rev-parse HEAD
```

远端读取是 `VERIFIED_GIT`；本地 clone 的 HEAD 只证明该 checkout，不证明父仓 pin 或分支头。二者在表中分列。

## 目标仓库（本项目）

| 项 | 值（2026-11-08 核验） |
|---|---|
| origin | `https://github.com/Piggy-Cat-bit-shadow/sing-box.git`（README 说明其指向 JiejieBox 仓库） |
| 产品分支 | `testing` |
| `origin/testing` | `25f5c9a638bdae4a1e2bb460641795f10b169bb2`（2026-11-08 02:08:39 +0800，8 项 `git ls-remote` 核验） |
| 本轮进入时本地 HEAD | `0de1c13dcb355773981c89a13158e532c75f61c3`（落后远端 39 个提交，`ahead=0`，可快进） |
| 最近一次专题发布目标 | v0.1.44 剥离后 `3b6ec64f7145a2add99656d47caaa4f22a1cc478`（`KNOWLEDGE/CALIBRATION-2026-11-08.md`）；v0.1.44 之后 6 个纯文档提交 |
| 广泛知识基线 | `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`（v0.1.34 时期；本轮不移动） |

### 依赖 pin（`go.mod` replace 与 `release/source-lock.json`）

| 模块 | 目标仓库 | pin（短 SHA） |
|---|---|---|
| `github.com/sagernet/sing` | `Piggy-Cat-bit-shadow/sing` | `1729220c46c4` |
| `github.com/sagernet/sing-tun` | `Piggy-Cat-bit-shadow/sing-tun` | `8f65451175c0` |
| `github.com/sagernet/sing-quic` | `Piggy-Cat-bit-shadow/sing-quic` | `1ef2597cc129` |
| `github.com/sagernet/quic-go` | `Piggy-Cat-bit-shadow/quic-go` | `2daa76f5bd52` |
| `github.com/metacubex/utls` | `Piggy-Cat-bit-shadow/utls` | `b63a2e9a7b1e` |
| `github.com/sagernet/cronet-go` | `Piggy-Cat-bit-shadow/cronet-go` | `362e63b14ce2` |
| `github.com/sagernet/cronet-go/all` | `Piggy-Cat-bit-shadow/cronet-go/all` | `7ce9c49b464a` |

`release/source-lock.json` 是上述 pin 与客户端 gitlink 的**派生快照**（`release_version` 为 `0.1.44`）；`release/dependencies.json` 只记录 intent（仓库+分支+policy），不重复 SHA。两者不能互相替代，也不能把本地 replace 当最终依赖交付。

### 客户端与嵌套锁定

| 路径 | 父仓 gitlink | 本地工作树 | 说明 |
|---|---|---|---|
| `clients/apple` | `e3283104c984d60cdf2a0250b47b7fc446e71ba6` | 已同步到 pin（clean） | 独立仓库，父仓只记 gitlink |
| `clients/android` | `c7837107dc0126fb704fdf08cf203e10b30c8a22` | `9d15717a85c5`（`fix/libbox-stringbox-callsites`，**进入本轮前已存在的回退状态**） | 本轮未改动、未提交；父仓 gitlink 仍指向 `c783710` |
| `clients/desktop` | `41722b030ea481523290d932a1d81177f8c13f32` | 已检出到 pin | — |
| `clients/desktop/dashboard`（嵌套） | `0da341722a8a0046fa5f5b602a19f9b4e1efccc8` | 由 `source-lock.json` 记录 | `report` policy：SHA 在另一仓库，移动需先改 Desktop |

`clients/android` 的本地回退是用户/此前工作留下的状态，本轮**不 reset、不 stash、不提交**；父仓提交不会携带它。后续若需客户端证据，应先确认该回退是否为预期。

### 内置闭源/受控边界

- **Cronet native 归档**：29 个 `cronet-go/lib/*` 平台模块为 `controlled` policy，归档由本仓库不存在的 Chromium 工具链构建；改 SHA 而不重建/复验归档等于“新绑定配旧二进制”。因此这些模块只能核验 pin 与消费状态，不能在本机重建。
- **Apple 客户端**：源码在独立仓库（当前 pin `e328310`），父仓只能核验 gitlink；真机行为另属 `NEEDS_DEVICE`。
- **oixCloud**：未取得内核源码。公开可核验的只有 App Store 产品页、`docs.dler.io` 文档和三个公开仓库（见下表），它们都不是内核实现。

## 参考项目（远端 refs 核验）

| 项目 | 远端与 ref | 核验值 | 与既有研究的关系 / 限制 |
|---|---|---|---|
| Anywhere | `NodePassProject/Anywhere` `main` | `8b1ec05bc5e0e2d4e477d9aced0b9832bbeff61d` | 与研究 pin 一致；Swift 数据面，仅借鉴问题意识与契约，不迁移形状 |
| Clash 维护线 | `ProjectClash/Clash` `main` | `e6334ad18cc7ee7582bf0f3a70681d58a2b61eb5` | 与研究历史定位一致 |
| Hako 历史线 | `TokenPLS/Hako` | 解析到 `ProjectClash/Clash-Legacy` 的 `main` = `da4952d3cac6794138835da23f293171d2593704` | **重定向已核验**：`ls-remote` 对 Hako 返回与 Clash-Legacy 相同的 HEAD；本地无 checkout，未做页面归档 |
| Clash-Client | `ProjectClash/Clash-Client` `main` | `e3e647283b1593a61dba622f13cd499deeda9b8b` | 与本地 checkout 一致；客户端锁文件（`Dependencies.lock.json`）把 kernel 与 Adapter 版本分开，核验消费时必须读锁文件 |
| Clash-Adapter | `ProjectClash/Clash-Adapter` `main` | `d47c04a3556a67f95929aaa15ae624c7df631e74` | 研究的 `09fbc0d7` 是更早历史；本地无 checkout，后续若用需先取回并重新定位 |
| reF1nd 内核 | `reF1nd/sing-box`，分支 `reF1nd-stable` / 标签 `v1.14.4-reF1nd` | `732618a5989cd2566f072c374fe2f619200f33f9`（分支与标签同指） | **本轮新增核验**：研究所称“发布内核 732618a”确实存在于内核仓（分支+标签），不是发布仓对象 |
| reF1nd 内核 `testing` | `reF1nd/sing-box` `testing` | `a6952ccf6907c18e18917fcb763a433e8e36937b` | 这是 `testing` 头，**不是**发布内核；本地 `<workspace>/_provresearch/ref1nd` 正停在这里 |
| reF1nd 内核 `reF1nd-testing` | `reF1nd/sing-box` `reF1nd-testing` | `17397d022647b73378705e1f91ff031dddaaec21` | 本地 `<workspace>/_provresearch/ref1nd_testing` 停在这里；与发布内核是两个版本，不能混用 |
| reF1nd 发布仓 | `reF1nd/sing-box-releases` 标签 `v1.14.4-reF1nd` | 标签对象 `de5581802e18e99c5040da1e23404dee2312b253`；当前最新 `v1.15.0-alpha.9-reF1nd` | 发布仓 ≠ 内核；标签 SHA 与内核 SHA 不同是正常事实 |
| reF1nd 依赖 fork | `reF1nd/sing`、`reF1nd/sing-tun` | 研究记录 `65f0a080`、`64cf1168`（历史） | 本轮未取本地 checkout；按目标实际 replace 追踪，不整体换依赖 |
| sing-box-lx | `Leadaxe/sing-box-lx` `lx` | `2d9a0a52df7881f55484d48f9daecb21d135f3e9` | 与研究 pin 一致；本地无 checkout。SPECS/FEATURES 是文档线索，历史设备数字不是本项目结果 |
| LX 子模块 | wireguard-go-awg2-lx / sing-tun-lx / gvisor-lx / utls-lx | 研究记录 `9115d902`、`94280f68`、`117243aa`、`ad69b191`（历史） | 未取本地；子模块 pin 与分支头是两类事实 |
| Ghostlane | `ghostlane-project/ghostlane` `main` | `479019064d1b5cd035275a5a79cd841b2f463a70` | 单 Go Runtime 设计案例；提案/未发布/未真机部分不能当本项目实测 |
| oixCloud 文档仓 | `pickrui/oixCloud-docs` `main` | `25320145fad64b76d38090b8d4bbedc64565c938` | 文档性质，不是内核 |
| oixCloud 辅助程序 | `pickrui/oixcloud-external-proxy-program` `main` | `ff8b13a29575f82293a9968551279e027a311e0a` | 辅助程序，不是内核 |
| oixCloud 介绍仓 | `jichangbaike/oixcloud` `main` | `72d76d1f1351e28eb4cfbf1469f56b78cd345c30` | 介绍性质，不是内核 |
| 上游 sing-box | `SagerNet/sing-box` | 本地 `<workspace>/_ext_ref_20261020/singbox-upstream` = `dfb80349`（观察到的上游头） | 只作对照；上游历史会被重写，祖先关系证明不了作者身份 |
| mihomo / Xray | `MetaCubeX/mihomo`、`XTLS/Xray-core` | 本地 checkouts `94b13aed`、`92ccc939` | 协议行为参考，不是移植目标 |

## 本地 checkout 快照（只证明该 clone）

| 路径（脱敏） | HEAD | 说明 |
|---|---|---|
| `<workspace>/_anywhere_src` | `89905e0e3641` | 比远端 `main` 旧；研究历史版本 |
| `<workspace>/_refs/Clash` | `5365826544dd` | 与远端 `main` 不同（更旧），使用前需重新定位 |
| `<workspace>/_refs/Clash-Client` | `e3e647283b15` | 与远端 `main` 一致 |
| `<workspace>/_provresearch/ref1nd` | `a6952ccf6907` | 等于 reF1nd `testing` 头，不是发布内核 |
| `<workspace>/_provresearch/ref1nd_testing` | `17397d022647` | reF1nd `reF1nd-testing` 头 |
| `<workspace>/_provresearch/sing-box-1.14.4` | `c005f35300ce` | 上游 1.14.4 参考 |
| `<workspace>/sing-fork` | `1729220c46c4` | 与目标 `sing` pin 一致 |
| `<workspace>/sing-tun-fork` | `8f65451175c0` | 与目标 `sing-tun` pin 一致 |
| `<workspace>/_deps/quic-go` | `2daa76f5bd52` | 与目标 `quic-go` pin 一致 |

## 冲突与不确定项

1. **reF1nd 版本**：`_provresearch/ref1nd` 停在 `testing` 头 `a6952ccf`，而研究引用发布内核 `732618a`。两者都在同一仓库的不同 ref；引用“reF1nd 发布内核”时必须写 `732618a`，不能用 `a6952ccf` 代替。
2. **Clash-Adapter**：研究的 `09fbc0d7` 与远端 `main` `d47c04a3` 不同；本项目当前没有使用该仓的代码，后续若需要，先取回并记录当时 ref。
3. **Clash 本地 checkout**：`53658265` 与远端 `main` `e6334ad1` 不同；本轮只用远端 ref 做来源核验，没有从本地旧 checkout 读实现。
4. **oixCloud**：`UNKNOWN`。只有产品/文档/辅助仓证据，不能推断其内核实现，更不能与 Anywhere 共用代码。
5. **Hako 重定向**：以 `ls-remote` 相同 HEAD 证明，未保存网页快照；若后续需要审计链，补一次页面/API 记录。
