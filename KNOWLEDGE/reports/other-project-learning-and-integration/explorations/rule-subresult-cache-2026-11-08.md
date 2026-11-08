# 纯规则子结果缓存评估（F02，2026-11-08）

结论：**本轮不采纳**（`NOT_ADOPTED_WITH_EVIDENCE`）。没有真实 corpus 的命中率证据，而缓存 key 必须覆盖
全部规则依赖与运行时条件；在 256 个谓词的规则上，单次匹配实测约 356ns，模拟 key+查表约 55ns，节省幅度
不足以抵消 key 完整性、代次失效与共享可变状态的风险。

## 实测（主机）

代表性规则：128 个 domain_suffix + 128 个 domain_keyword（后者已走编译自动机）。

```
BenchmarkScratchRuleMatch-16          356 ns/op    16 B/op   1 alloc/op
BenchmarkScratchCacheKeyLookup-16      55 ns/op    48 B/op   1 alloc/op   # 仅 key 拼接 + RWMutex 查 map
```

- 55ns 不含缓存插入/淘汰、规则版本校验、锁竞争、命中后仍需的决策合并；也不含 miss 时的完整匹配。
- 每 flow 的 tuple（outbound/transport/network/port/inbound/域名/DNS 响应条件）很少重复，命中率未知；
  没有命中率就无法证明净收益。
- 规则匹配只发生一次/连接（DNS 另计），不是热点；缓存的正确性要求却覆盖全部规则与运行时条件。

## 为什么 key 很难做对

至少要包含：规则集内容/版本与编译产物指纹、路由规则顺序、出站实例身份、网络 epoch/平台策略、传输与端口、
入站与进程/包条件、DNS 响应/ExpectedStatus 等。漏一个字段就会把不同需求混成一个结果；这与 05 的
“同名不同测”约束冲突。缓存还必须是并发安全、可失效、有容量/TTL 上限，并处理旧代读者。

## 保留与复开条件

- 保留：规则匹配路径不变；F01 的 domain_keyword 编译自动机已单独降低大集合成本。
- 复开条件：真实 corpus/线上 trace 显示同一完整 tuple 的重复率足够高（例如 >30% 命中）且规则匹配占
  路由 CPU 的显著比例；届时先做只读、带完整 key 与显式版本的小缓存，并量化命中/内存/失效成本。
