# 其他项目的学习和缝合（Other-project learning and integration）

本目录是本轮工程的规范归档位置。工程目标：以性能为指向，从既有参考项目（Anywhere、ProjectClash/Clash 系列、reF1nd、sing-box-lx、Ghostlane 等）中吸收适合本内核的机制，先建立可执行的覆盖账与最小观测基础，再按 01→12 施工；不照抄形状，不建立第二套压力源或生命周期权威。

**当前阶段：01–03 已完成，04 进行中（批 1 已落地：R01 owner 盘点 + XHTTP XMUX 首个真实 owner + R07 有界关闭诊断；批 2/3 待做）。** 本目录随每阶段追加 `stages/NN.md` 并更新 `MASTER_PLAN.md`、`CONTRACTS.md`、`DECISIONS.md`、`EVIDENCE.md`、`HANDOFF.md`、`DOCUMENT_INDEX.md`。

## 阅读顺序

1. `MASTER_PLAN.md` —— 55 项覆盖矩阵的当前状态、主责阶段与证据入口。
2. `ANALYSIS.md` —— 三类资源的现状调用图、等待/锁序、创建→发布→退役→关闭路径与作用域。
3. `CONTRACTS.md` —— 三种账（创建许可、排队字节、资源驻留）的 owner、计费边界、观测字段与后续阶段接点。
4. `SOURCES.md` —— 参考项目/依赖/客户端锁文件的来源核验（含闭源边界与本地实际 revision）。
5. `DECISIONS.md` —— 本轮采纳/调整/否决的依据与删除条件。
6. `EVIDENCE.md` —— 命令、revision、环境、结果与证据等级。
7. `HANDOFF.md` —— 跨上下文阅读缓存、已确认事实、下一阶段可直接使用的接口。
8. `stages/01.md` —— 本阶段实际改动、验证与交接。
9. `explorations/` —— 值得保留的特别大重构/重要未采纳方案结案；01 未产生此类项（没有把任何大重构推进到需要结案评估的程度）。
10. `DOCUMENT_INDEX.md` —— 本轮文档清单、脱敏状态、提交与远端核验记录。

## 证据与边界

- 本目录是**工程记录**，不是源码事实来源。源码、Git 与当次实测优先；与知识库冲突时以源码/证据为准，并记录冲突。
- 证据等级沿用 `KNOWLEDGE/README.md` 的四标签与 `VERIFIED_*` 限定：`VERIFIED_SOURCE`、`VERIFIED_GIT`、`VERIFIED_UNIT`、`VERIFIED_CI`、`VERIFIED_DEVICE`、`INFERRED`、`UNVERIFIED`、`BLOCKED_BY_ENVIRONMENT`。
- 本轮的测试与基准都在主机（Windows 10 build 19044、Go 1.25.5、AMD Ryzen 7 4800H）上完成；主机基准不是设备结论，也不是吞吐承诺。
- 归档前已脱敏：不含凭据、私钥、认证头、私人节点/域名、账号/设备标识或用户名绝对路径；主机路径写成 `<workspace>`/`<toolchain>`。处理范围与验证方式见 `DOCUMENT_INDEX.md`。
- 本目录不移动 `BASELINE.md` 的广泛生产基线，也不声称全仓认证。01 只对直接触及的模块（OOM 回收、资源准入观测）做有证据的定向说明，见 `KNOWLEDGE/INDEX.md` 新增入口。

## 阶段状态

| 阶段 | 范围 | 状态 |
|---|---|---|
| 01 | S01 来源核验；G01 唯一压力源、资源观测、回收收益反馈、平台内存口径 | 已完成（本目录） |
| 02 | 统一预算与真实高成本创建（G02–G06） | 已完成（`stages/02.md`；QUIC/Cronet 见 `explorations/`） |
| 03 | 数据面字节预算反压与能力保真（B01–B06） | 已完成（`stages/03.md`；其余积压点见 `explorations/data-plane-byte-budgets-2026-11-08.md`） |
| 04 | 驻留资源惰性实例与生命周期（R01–R08） | 已完成（`stages/04.md`；R03/R04 重构与 R08 见 `explorations/`） |
| 05 | 探测选路进程查询与 DNS 并发（P01–P04、D01–D02） | 未开始 |
| 06 | 规则编译构建预算与版本化产物（F01–F05） | 未开始 |
| 07 | 配置分析生效报告变更与恢复事务（C01–C06） | 未开始 |
| 08 | 决策解释诊断树事件与跨语言接口（O01–O06） | 未开始 |
| 09 | 协议兼容平台集成与全范围收口（X01–X05、M01–M03） | 未开始 |
| 10–12 | 资源守恒、并发生命周期、互通性能真机验收 | 未开始 |

