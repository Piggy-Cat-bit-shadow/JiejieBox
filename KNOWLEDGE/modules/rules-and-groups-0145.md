# 0.1.45 规则、组和版本产物

固定源码目标：`3032ca5199dbd151be0b2cd006fba191b87cc1e8`。

`resolve.match_only` 为规则匹配提供解析地址而保留原拨号目标语义；`hidden` 控制适用客户端的组展示，组的实际成员、引用和路由仍在。URLTest 的显式 failover 有稳定槽位、退化扩选与恢复滞回；省略时保持原策略。等价测速由 `common/urltest/measurement_share.go` 共享，轮 worker 与排队描述分别设界，不能把描述队列界限当作全 Box goroutine 总上限。

`route/rule/domain_keyword_matcher.go` 为大关键词集合构建不可变 Aho–Corasick 匹配器，小集合与超界输入回到逐项匹配，保留规则语义。历史 1024 关键词样本未命中从 13.1–14.5 µs 降到 188–243 ns；这是组件样本，不是整条代理流耗时。

`common/artifact/stage.go` 流式写入临时文件并计算摘要；校验后 `Publish`，租约保护旧读者。`common/configtx/commit.go` 记录配置与规则/Provider 精确引用，使恢复读取同一版本组合。构建准入上限、实际在途和重叠峰值必须分别观察；压力提升不撤销已开始构建。源码复查入口：`go test ./route/rule ./common/urltest ./common/artifact ./common/configtx ./provider/remote`。
