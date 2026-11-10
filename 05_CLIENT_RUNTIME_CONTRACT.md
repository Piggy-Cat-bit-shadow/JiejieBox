# 05｜内核 ↔ 客户端接口契约（0.1.45）

本文件是阶段 05 的接口契约，客户端（Apple/Android 的 libbox、Desktop 的 boxdd/gRPC）与内核实现必须按同一套语义理解。所有结论都来自本树代码与阶段 05 的集成提交，不是从旧分支文档转抄。

---

## 1. 术语与所有权

| 术语 | 含义 |
|---|---|
| **文档（document / profileContent）** | 一份完整配置文本；客户端可以编辑它，但**只有内核**能证明它是否可运行。 |
| **revision** | 内核从文档内容派生的内容标识；同内容同 revision，不随重启变化。 |
| **apply attempt** | 一次"把这份文档变成运行实例"的尝试；每次尝试有自己的 `ServiceChangeReport`。 |
| **running instance** | 当前正在运行的 `Instance`；由 `StartedService` 拥有，客户端只读。 |
| **owner** | 发起 apply 的那次调用及其 context。报告、取消、超时都属于**这次调用**，不是属于客户端 UI 或"最近一次"。 |

**所有权规则（不可协商）**：

1. 客户端**创建请求并拥有自己的 context**；内核不把请求挂在全局状态上。
2. 配置的生产者是客户端；配置的**可运行性判定**和**运行实例**归内核。
3. 只有内核可以 publish 一次 apply 的结果；客户端不得把"请求已发出"或"文件已保存"当作成功。
4. 取消由调用方发起；内核只在**提交点之前**取消，提交点之后的结果必须如实返回。
5. 失败/中断后，内核报告**实际仍在运行**的 revision（可能为空），客户端不得自行推断"应该"在运行什么。

---

## 2. 客户端 → 内核的三条路径

| 路径 | 使用者 | 形态 | 契约入口 |
|---|---|---|---|
| **libbox（gomobile）** | Apple（iOS/macOS/tvOS）、Android | 进程内 JSON 字符串 | `CommandServer.ApplyServiceChangeReport` / `StartOrReloadService` |
| **gRPC（StartedService）** | Desktop（通过 boxdd 启动的 daemon）、诊断工具 | protobuf over gRPC | `PreviewServiceChange`、`GetLastServiceChange`、`GetLastConfigRecovery`、`RefreshRemoteProviders`、`SubscribeConnections` 等 |
| **boxdd（Desktop service）** | Desktop 守护进程生命周期 | protobuf over gRPC | `StartService`/`ClaimService`/`TakeOverService` 等；配置应用仍走运行实例的 StartedService |

---

## 3. 配置应用（apply）

### 3.1 入口

```go
// libbox: 返回 JSON 文档，不因"需要报告的事实"而抛错
func (s *CommandServer) ApplyServiceChangeReport(configContent string, options *OverrideOptions) *StringBox

// libbox: 旧入口，等价于上面丢弃报告，只保留 error
func (s *CommandServer) StartOrReloadService(configContent string, options *OverrideOptions) error

// Go（boxdd/测试）: 报告是本次调用的返回值
func (s *StartedService) ApplyServiceChange(ctx context.Context, profileContent string, options *OverrideOptions) (*ServiceChangeReport, error)
```

**为什么 libbox 返回 `*StringBox` 而不是 `(report, error)`**：gomobile 的桥接会把 error 变成 Swift 的 `throws`，结果被丢掉；"旧内核不支持这个 RPC"这类**能力事实**不是失败，必须能随成功调用返回。

### 3.2 返回文档

```json
{
  "version": 1,
  "ok": true,
  "report": {
    "plan": { "targetRevision": "...", "restartRequired": false, "provider": {...} },
    "status": "started|unchanged|applied_live|restarted|recovered|interrupted|failed",
    "restartRequired": false,
    "retained": true,
    "activeRevision": "...",
    "persistence": "saved|degraded|not_configured",
    "persistenceWarning": "...",
    "reason": "...",
    "time": "RFC3339",
    "applied": [...],
    "provider": { "results": [...] }
  }
}
```

失败时仍是 `ok:true` + `report.status="failed"` + `report.reason`（如果失败发生在能生成报告之前，`ok:false` + `error`）。

### 3.3 状态语义

| status | 含义 | 客户端应做什么 |
|---|---|---|
| `started` | 之前没有运行实例，这次建起来了 | 刷新为 `activeRevision` |
| `unchanged` | 目标与运行文档一致，没有重启 | 保持运行状态，可只刷新 provider 结果 |
| `applied_live` | 同一实例上热应用（provider/规则等） | 按 `applied` 更新，不重建 UI 模型 |
| `restarted` | 需要新实例，旧实例已被替换 | 丢弃旧连接/状态，按 `activeRevision` 重建 |
| `recovered` | 目标失败，回退到 last-good | 显示 recovery，运行的是 `activeRevision` |
| `interrupted` | 启动被中断（崩溃/进程退出） | 显示"未完成"，不要假装成功 |
| `failed` | 没有可运行的目标；可能保留了旧实例（`retained`） | 以 `retained`/`activeRevision` 为准 |

### 3.4 取消与超时

- 调用方 context 已取消：内核**不关闭**正在运行的服务；返回 `status=failed`、`retained=true`、`activeRevision=当前运行 revision`，`reason` 说明取消。
- 取消发生在 provider refresh 期间：不 publish 半成品，不拆运行实例；返回失败报告。
- 调用方 deadline **透传**到 provider 下载；内核不会把调用方 deadline 换成自己的固定超时。
- libbox 批量 refresh 的整批上限由每个 provider 的 bound 派生：`perProvider × 4`，下限 30 s，上限 120 s；每 provider 默认 30 s。

### 3.5 读回接口

- `GetLastServiceChange`：**最近一次** apply 的报告，可能是别人写的；客户端要的是"我这次"的报告时，必须用 `ApplyServiceChangeReport` 的返回值。
- `GetLastConfigRecovery`：最近一次恢复记录。
- `PreviewServiceChange`：只读预演，返回 `JSONDocument`（plan），不启动、不查询、不产生 side effect。

---

## 4. Provider Refresh

### 4.1 请求与响应

```proto
rpc RefreshRemoteProviders(RefreshRemoteProvidersRequest) returns (RemoteProviderRefreshResponse);

message RefreshRemoteProvidersRequest { repeated string tags = 1; int64 timeout_ms = 2; }
message RemoteProviderRefreshResult {
  string tag = 1;
  string status = 2;              // 内核证明的结果
  uint64 requested_revision = 3;  // 调用方请求时的运行 revision
  uint64 applied_revision = 4;    // 实际生效的 revision
  repeated string pending_tags = 5;
  bool restart_required = 6;
}
message RemoteProviderRefreshResponse {
  repeated RemoteProviderRefreshResult results = 1;
  bool cancelled = 2;
  bool box_changed = 3;
}
```

### 4.2 语义

1. **绑定到调用**：`requested_revision` 是调用开始时的运行 revision；结果里的 `applied_revision` 是内核实际生效的 revision。二者不同且 `box_changed=true` 时，客户端不能把结果套到旧实例上。
2. **parse 与 publish 分离**：内核先解析/校验，再在提交点 publish；取消发生在两者之间时**不 publish**，运行实例保持原样。
3. **取消后不再 publish**：任何"取消后仍然写回"的路径都是缺陷。
4. **不关闭运行实例**：取消/超时的 refresh 只结束自己的等待，不触发 `CloseService`。
5. **旧内核能力**：`RefreshRemoteProviders` 返回 gRPC `Unimplemented` 时，libbox 返回能力文档而不是错误：

```json
{"supported": false, "reason": "the running kernel predates RefreshRemoteProviders"}
```

客户端据此显示"内核不支持"，而不是"刷新失败"。

6. **批次上限**：libbox 客户端把多个 provider 的刷新限制在 `refreshBatchDeadline` 内；整批失败只报告失败项，不污染未请求的 provider。

---

## 5. 组选择（SelectOutbound）

```proto
rpc SelectOutbound(SelectOutboundRequest) returns (SelectOutboundResponse);
```

- 请求携带 `groupTag`、`outboundTag` 和调用方的 **requestIdentity**。
- 响应是**读回**：组在请求之后实际持有的 outbound，不是"请求已收到"。
- 响应回显 identity；晚到的确认不能结算另一个请求。
- 客户端不得在收到响应前把 UI 切到目标 outbound；以响应中的实际状态为准。

---

## 6. 连接事件流

```proto
rpc SubscribeConnections(SubscribeConnectionsRequest) returns (stream ConnectionEvents);
```

- 每个流带 **instance id**：reload 之后新 Box 的流是不同实例，客户端不得把新事件套到旧模型。
- 每个事件带 **sequence**；`dropped` 是累计丢事件数。出现 gap 或 `dropped>0` 时，客户端必须用快照重新同步，而不是在缺失事件上做增量。
- `Reset_=true` 表示重新开始，客户端要清空旧行。

---

## 7. 平台守卫与 ABI

- 阶段 05 的 8 个内核提交只新增 libbox API：`CommandServer.ApplyServiceChangeReport`；没有删除或改写既有公开方法签名。
- `daemon` 的新增导出：`StartedService.ApplyServiceChange`、`ApplyProviderDomainContext`；内部函数签名变化不构成 ABI。
- 没有新增 Apple-only 构建标记或符号；这些代码在 `windows/amd64`、`linux/amd64`、`darwin` 的共享包中编译（本阶段 Windows/Linux 构建通过）。
- proto 的 RPC/消息在阶段 05 未变；变化来自更早阶段（`SelectOutboundResponse`、`PreviewServiceChange` 等已在基线）。

---

## 8. 客户端必须遵守的规则

1. 不根据"请求成功发出"推断应用成功；只信 `ServiceChangeReport.status` 与 `activeRevision`。
2. 不用 `GetLastServiceChange` 代替自己调用的报告。
3. 不重试非幂等请求，除非内核明确证明"未处理"（H3 `H3_REQUEST_REJECTED` 同理）。
4. 取消后不假设内核已停止；以返回的 `retained`/`activeRevision` 为准。
5. UI 只展示内核报告的状态；不得自行合成"已更新"。
6. 收到旧内核的 capability 文档时显示"不支持"，不显示"失败"。
7. 连接流出现 gap/实例变化时重新拉快照。
