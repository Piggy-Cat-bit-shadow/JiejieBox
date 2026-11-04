# 09｜十个依赖 Fork 的实测状态（补充 07 的未完成项）

审核指出"其余八仓未完成逐仓对账"。本轮补上了：十个 fork 全部用**本地克隆 + 双远端**算，
不用 GitHub compare（它在跨 fork 时给出 0/0 的假数据，sing-tun 真值 33/0 也报 0/0）。

| Fork | 分支 | HEAD | Ahead | Behind |
|---|---|---|---|---|
| `sing` | `dev` | `1729220` | 55 | **0** |
| `sing-quic` | `main` | `1ef2597` | 7 | **0** |
| `sing-tun` | `dev` | `e9c7af7` | 134 | **0** |
| `quic-go` | `sagernet` | `2daa76f5` | 18 | **0** |
| `cronet-go` | `main` | `bac0169` | 15 | **0** |
| `utls` | `main` | `b63a2e9` | 68 | **0** |
| `sing-box-for-apple` | `main` | `4773b5f` | 22 | **0** |
| `sing-box-for-android` | `main` | `c783710` | 10 | **0** |
| `sing-box-for-desktop` | `main` | `41722b0` | 37 | **0** |
| `sing-box-dashboard` | `main` | `0da3417` | 5 | **0** |

## 结论

- **七个 fork 本来就在 0 behind**，未做任何合并——不移动内容的合并不是同步。
- **`sing-tun`**：`e9c7af7`，33 条"缺失"多为已移植（86 个同标题提交），合并记录祖先，冲突保留 fork 超集。
- **`cronet-go`**：`bac0169`，三条上游里两条真新（Windows TCP 端口复用度量竞态、BSD connected UDP EOF），
  第三条是本 fork 的 `bd2b705`；`naiveproxy` 子模块随上游移到 `7c64cae`；native 构建由 CI 证明。
- **`sing-box-for-apple`**：`4773b5f`，把上游 4 提交、自签启动修复与回环功能集成到同一提交，
  iOS 与 macOS 均构建通过（run `41796194185`）。

## 不宣称

`behind = 0` 是**祖先关系**陈述，不是行为等价证明；本轮没有在设备上运行任何 fork。
复现命令与仓库内记录：`docs/fork/dependency-forks-sync-status.md`（随 `deeea914789` 提交）。
