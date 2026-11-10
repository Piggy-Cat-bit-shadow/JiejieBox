# 产品 A/B/C 夹具（Windows `boxdd`，认证客户端用）

这四个文档与 `daemon/config_recovery_product_test.go` 里 `TestTheProductSequenceUsesTheOldProviderNodeAndRule`
证明的完全相同（固定端口版）。测试在本机已通过；产品侧只差提权 + 认证客户端。

## 组成

| 文件 | 作用 |
|---|---|
| `A.json` | 已保存的配置：Provider 节点 → 回环 18081（body `node-A`），规则拒绝 18091 |
| `B.json` | 新配置：Provider 节点 → 回环 18082（body `node-B`），规则拒绝 18092 |
| `C_parse.json` | 解析拒绝用例：重复 inbound tag，必须保留运行中的 B 且不产生恢复事件 |
| `C_build.json` | 构建失败用例：引用未声明规则集，先通过预览、构建阶段失败，触发恢复 |
| `canned_proxy.go` | 两个回环节点：`go run canned_proxy.go -listen "18081=node-A,18082=node-B"` |

## 为什么可信

- 响应体来自 **Provider 节点自己的出站**（HTTP 代理出站），不是规则 override，也不是固定 outbound；
  摘掉 Provider 或把选择器换成 `direct`，请求就取不到标记（本机负向对照已验证）。
- 规则与节点是两条独立见证：每份文档各自拒绝一个端口（18091 / 18092），另一个端口走节点。
- 恢复后必须同时回到 A 的节点（body `node-A`）和 A 的规则（18091 被拒、18092 通）。

## 期望矩阵（产品 UI / 客户端执行）

| 阶段 | 请求 `127.0.0.1:18093` | 请求 `127.0.0.1:18091` | 请求 `127.0.0.1:18092` |
|---|---|---|---|
| A 保存后 | `node-A` | 拒绝 | `node-A` |
| B 运行（磁盘写失败，`degraded`） | `node-B` | `node-B` | 拒绝 |
| C_parse 提交 | 报错；`failed` / `retained=true` / `activeRevision=B`；无恢复事件；B 仍在服务 | 同左 | 同左 |
| C_build 提交 | `node-A`；`recovered` / `activeRevision=A` / `failedRevision=C_build` | 拒绝 | `node-A` |
| 结束进程、同目录重启、重放 C_build | `node-A`，磁盘 `commit.json` 仍是 A | 拒绝 | `node-A` |

## 执行顺序

1. `go run canned_proxy.go -listen "18081=node-A,18082=node-B"`（另开窗口）。
2. 产品客户端（提权安装的隔离服务 + 真实命名管道）提交 `A.json`，按矩阵验证。
3. 让磁盘提交失败（对数据目录 `icacls <dir> /deny <user>:(W,D)`），提交 `B.json`：B 必须在线、报告
   `persistence=degraded`、`commit.json.current.revision` 仍是 A。
4. 提交 `C_parse.json`：必须报错且不新增恢复事件，B 仍在服务。
5. 恢复写权限，提交 `C_build.json`：必须真实构建失败并恢复 A，业务与报告回到 A。
6. 结束本轮进程，同目录重启服务，以认证客户端重放 `C_build.json`，重复矩阵最后一行。
7. 每一步保存：退出码、报告 JSON、`commit.json` / `commit.candidate` / `active.json`、三次 RPC 返回、
   以及三个端口的实际响应体与状态码。
