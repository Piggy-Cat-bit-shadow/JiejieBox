# 0.1.45 · Apple NE / Libbox 接口施工与复审归档

本目录归档任务 10（Apple NetworkExtension 与 Libbox 命令接口的施工、独立复审与终轮收尾）的可读结论。
`0.1.45` 只是**归档名称**，不代表该版本已发布。

- 归档日期：2026-11-10
- 归档范围：Apple 侧 Profile 写入与结果消费、NE 应用与启动记录、命令目标归属、Libbox Provider 刷新
- 归档来源：两仓本地任务分支；原始报告保存在内部交付目录，本目录是其脱敏与合并版

## 候选与分支

| 仓 | 分支 | HEAD | 说明 |
|---|---|---|---|
| 内核（JiejieBox） | `fix/task10-apple-ne-runtime-final` | `a3340d1a759d520d4a4b9d1730db5ca0add52cc6` | 起点 `efd8d3b2eae1ee35c6b71a7b296609fd922ae31d`，8 笔 |
| Apple 客户端 | `fix/task10-apple-ne-runtime-final` | `b2596b50fb6ae0592466fc41ae481a3999d2de6f` | 起点 `40e59c767085a1cee033177813f8b40e2bde06e7`，15 笔 |

两个分支都已推送到各自 `origin`。合并顺序：内核先生成配对的 `Libbox.xcframework`，Apple 再消费；父仓 gitlink、
`VERIFIED_APPLE_SHA`、发布与签名由集成流程处理。

## 本目录内容

| 文件 | 内容 |
|---|---|
| `施工与交接报告.md` | 修了什么、为什么、失败时序与证据边界、双 SHAS 与提交序列 |
| `复审裁定表.md` | 逐项裁定（含证据层级、红→绿、SHA、剩余动作）与已知限制 |

## 证据词汇

- `VERIFIED_UNIT`：本机真实执行（真实 HTTP / 真实 gRPC / 真实文件 / 契约包）。
- `VERIFIED_SOURCE`：源码级核对（含调用点枚举）。不等于运行。
- `BLOCKED_BY_ENVIRONMENT`：本环境无法执行的项（Apple 编译链接、真机、跨进程夹具）。
- 未标注的内容最多按 `VERIFIED_SOURCE` 对待。

## 已知限制（用户可见影响）

1. **跨进程正文**：App 与 Intents 扩展各持一把进程内锁，只共享文件路径。同一 Profile 的两次跨进程写入
   （一次刷新 + 一次保存或刷新）若落在同一秒，后写者覆盖先写者，两边都可能报成功。触发需要快捷指令与前台刷新
   同时作用于同一 Profile。
2. **NE 会话代次**：接收端已按"选中的 Profile ID"拒绝错目标请求；但同一 Profile 停止再连接后的新会话尚无权威
   身份可被请求预先声明。因此"旧回执被当成这次更新的确认"这一交错**未被证明安全**，归档不作此声明。

## 本机可复现的验证

```
# 内核
go build ./...
go test ./daemon/ ./experimental/libbox/ ./provider/... -count=1
CGO_ENABLED=1 go test -race ./daemon/ ./experimental/libbox/ ./provider/remote/ -count=1

# Apple（Windows 需把 SwiftPM 的符号链接源物化为真实文件后再跑）
swift test            # 契约包 78 例 / 既有回归 129 例，均 0 失败
```

未验证：Apple 真实编译与链接、真机（APNs 与同正文热刷、On-Demand 回读、接管等待）、App/Intents 真实跨进程、
端到端远端零收包。这些在裁定表中按 `BLOCKED_BY_ENVIRONMENT` 或 `NOT_RUN` 单独记账。

## 脱敏说明

本目录内容在归档时做了脱敏：本机绝对路径、开发机用户名、临时目录与被审计的注入夹具路径均已替换或删除；
示例订阅地址为合成域名；提交 SHA、分支名与公开仓库名保留。原始本地报告未被修改。
