# 0.1.45 知识库专题刷新报告（2026-11-13）

## 目标与版本

- 旧广泛 Production Baseline：`0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28`（v0.1.34）。
- 新专题源码目标：`3032ca5199dbd151be0b2cd006fba191b87cc1e8`，专题校准时本地 `testing` 与云端 `origin/testing` 观察一致；其后分支已前进，本报告不自动覆盖后续提交。
- 最新已发布目标：`v0.1.44^{commit}` = `3b6ec64f7145a2add99656d47caaa4f22a1cc478`，为专题目标祖先。0.1.45 未以本报告宣告发布。
- Upstream baseline：本轮未移动；上游历史重写使来源不能只凭祖先关系判定。
- Git 粗扫：v0.1.44..目标 760 提交、1951 个路径；重点分布在 docs、transport、common、protocol、route、daemon、dns、platform、release 与子仓消费。深读采用本地最终审核包的六窗口报告、47/55 项矩阵、源码关键调用点及历史验证记录。详细全量逐提交来源审计未在本轮完成。

## 刷新范围

新增 `modules/protocols-0145.md` 与 `modules/rules-and-groups-0145.md`。按系统链路更新协议、资源、路径、DNS、规则、Provider、XHTTP、Mieru、MASQUE、Naive、libbox、子仓、CI、TUN/MTU、Apple、Windows、安全与嗅探模块，并校准旧说法。同步更新 `README.md`、`BASELINE.md`、`INDEX.md`、`COMMIT-INDEX.md`、`modules/README.md`、`INVARIANTS.md` 与 `ACCEPTANCE.md`。原有历史模块正文保留其固定目标与来源范围。

新增跨模块不变量包括：真实创建计费与复用、逻辑字节和底层容量分别守恒、退役代与迟到结果的 owner 归属、规则产物先校验后发布、匹配解析不改写拨号目标、隐藏组不改变运行引用、诊断记录受条数与字节限制。没有删除或降级旧不变量；旧条目若与当前专题交叉，以新增专题和当前源码重新核对。

验收索引新增协议、资源、DNS/规则、配置与发布合同的定向入口。已有报告的 25/25 指指定代码范围完整；历史 19 项保留回归、设备与最终性能层级没有随知识刷新升级。性能材料中的 WG/AWG 36/36 和协议吞吐来自特定 WSL2 台架，不被当作当前设备或公网事实。旧 XHTTP 内存 A/B 在最终正确性修复之前，未写成当前总体内存收益。

重要来源变化：主仓消费的 sing-tun 与 Mieru pin 已转到各自默认分支；TrustTunnel/AWG 默认档位在目标源码 `release/DEFAULT_BUILD_TAGS*` 中出现。客户端 gitlink、嵌套 gitlink、Go replace 和 source-lock 属不同来源，须按当前目标分别回读；未将分支最新 head 当作父仓固定消费版本。

## 基线裁定与后续核对

此次先修订 [刷新指引](REFRESH.md)，再更新 [项目全貌](PROJECT.md)、[系统边界](ARCHITECTURE.md)、[模块校准台账](MODULE_CALIBRATION_2026-11-13.md) 与全部受影响模块；属于覆盖主要 0.1.45 变化的**专题刷新**，没有把全局 Production Baseline 前移到 `3032ca5`。原因是 760 个提交和 1951 个路径中仍有未逐模块校准的旧知识、来源归类、原生/设备与客户端消费边界。广泛基线继续指向 v0.1.34；专题段各自固定到 `3032ca5`。后续若要完整移动广泛基线，应按 `REFRESH.md` §31 逐模块消除漂移，再统一更新 BASELINE。

本轮实际复跑 `go test -count=1 ./common/resourceguard ./common/artifact ./common/urltest ./route/rule`，四包通过；文档链接和 `git diff --check` 另行核验。专题刷新没有修改生产代码、依赖、构建或发布配置，也没有重写 Git 历史。根目录 `README.md` 在后续文档整合中加入知识库阅读指引和配置入口。本文保留的未验证范围是知识证据等级说明，不是产品发布缺陷清单。
