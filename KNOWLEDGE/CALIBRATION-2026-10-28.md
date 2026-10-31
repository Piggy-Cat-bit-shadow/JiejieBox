# 知识校准台账 — 2026-10-28（Knowledge calibration ledger — 2026-10-28）

**状态：源码级知识刷新已完成（COMPLETE）。** 本台账记录证据与
局限；它不会把未运行的设备检查、互操作性检查、产物字节检查或完整客户端 ABI
检查算作通过。新的生产基线为 `cb71cf80d7224a51823b455f0be5959ad8556933`。

- 旧生产基线：`5fc4f9366a60202fdc7c98cd6fa280cceace65ba`（v0.1.9）。
- 受检目标：`011f3fdcd99173acd3e80483b50cb76b07bf6ff4`；基线之后有 791 个提交。
- 已发布的 v0.1.30 解析为 `6112aa17052297ec9890e0e7f1541a06120b84b2`，是该目标的祖先。检查时尚未通告 origin `main`。
- 并发分支推进：`cb71cf80d7224a51823b455f0be5959ad8556933` 把版本文件更新为 0.1.31，并更新 `cronet-go/all` 固定点。标签剥离后指向该提交。较早的一次检查没有发现 Release；后来用 GitHub API 检查，发现 [v0.1.31 Release](https://github.com/Piggy-Cat-bit-shadow/sing-box/releases/tag/v0.1.31) 发布于 2026-10-28 00:38:59 UTC，非草稿、非预发布，列出七个资产。这些资产的字节没有独立下载或验证。依赖处于先前状态时执行的测试，不会自动证明新固定点下的行为。
- 最终增量是从 v0.1.9 到源码目标的 795 个提交。已按路径
  和语义簇粗分类：协议、拨号器、路由、DNS、TUN、传输、平台、依赖、CI、
  发布、测试和历史记录。259 个变更的非 `_test.go` Go 路径包含测试工具；
  只检查了代表性的承重实现及相关测试，没有逐提交深读。
- 源码核对后的更正：TUN 启动/重置交接、双栈准入与所有权、空地址族日志记录、FakeIP/sniff 转换、透明组快速路径、能力警告严重级别、Naive 回退、VLESS 客户端加密、MASQUE 关闭、渐进式内存裁剪、TUIC 路径上限、Linux kTLS 尾部所有权、TLS 选择与工作流声明。锚点与测试见相关模块及 `ACCEPTANCE.md`。旧的全局“无增量”与“无 CI 工作流”主张已更正。
- 现存活动模块、根导航、不变量和验收条目已清点，并
  用变更路径审查、源码锚点和跨文件抽样做了校准。历史报告
  仍是时点存档；相关发现归并到各自的负责模块，没有当成
  当前结论重述。这既不是重新执行每个历史基准，也不是逐条证明旧行锚点。
- 源码测试与本地测试不能确立设备、互操作性、发布产物或 CI 运行行为。本次校准没有更改任何生产代码、依赖、构建或发布配置。
- Windows 10 Enterprise LTSC 2021 build 19044、Go 1.25.5：`go test -tags with_utls ./common/tls -count=1` 在三个 Windows TLS 1.3 握手测试中失败，报 `unexpected EOF`；加过滤条件重新执行一次，复现了这三个失败。原因未知。针对性的 uTLS/ECH 测试通过。Linux kTLS 测试无法在这台 Windows 主机上执行。
- `go test -tags with_naive_outbound ./protocol/naive -run TestH3Fallback -count=1` 停在 Cronet fork 的下载步骤上并被中断；没有产生测试结果。待所需依赖可用时重试。
- 后来一次离线重试带 `with_naive_outbound,with_purego`，在设置阶段就失败了：两个固定版本的
  Cronet 模块不在这份 Go 缓存中，且 `GOPROXY=off`；没有执行任何 Naive 测试。
- 再后来一次重试用了临时 modfile，把两个 Cronet 模块指向位于精确固定版本 `c9c0e6ec8845` 的干净本地
  `cronet-fork` 检出；`TestH3Fallback` 各用例在 `with_naive_outbound,with_purego` 下通过。
  这次验证的是本地源码替换与测试替身，
  不是下载模块的来源、原生库或实时互操作性。
- 额外的交叉检查更正了 DNS 投递与缓存契约、MASQUE 每块所有权、
  固定的 sing splice 回退、fork 来源解释、当前 sing-tun 替换，以及
  历史 MASQUE 初始大小与硬上限主张。`go test ./route -count=1` 在
  `TestTeardown_OneSidedCompletionNeverRunsOnClose` 上、于 120 秒设置超时后失败；单独隔离
  重新执行一次则通过。这次失败仍记录在 `ACCEPTANCE.md` 中，没有悄悄改判为通过。
- 针对发布 Windows 守护进程作业以及后续的
  Verify/Android 作业，分别独立查询了 GitHub 运行记录；每一项都限定到对应模块中的实际 SHA。根 DNS/HTTP
  detour 测试、Mieru 分类、XHTTP 包测试和 `with_quic` MASQUE 块测试在
  本地通过；证据范围见 `ACCEPTANCE.md`。
- 对模块文档中反引号包裹的裸 `name()` 引用做了机械扫描，在当前树中找到了 Go 符号；
  剩下两个名称核实为 shell 函数。这只是一次有限的
  拼写检查，不能证明每个主张或历史行号都正确。知识树中的相对 Markdown
  链接已在排除围栏代码后检查；两个损坏的存档
  链接已更正。这并不验证正文路径引用或 Markdown 锚点。
- 额外的源码审查区分了 ShadowTLS 的首记录截止时间与目标清理、
  端点拥有的 DNS 解析器退役、电源观察器通知复制，以及 Android 独立的
  procfs 辅助程序与已移除的 `UseProcFS` 平台接口方法。相应的针对性
  测试记录在 `ACCEPTANCE.md` 中；客户端设备 ABI 行为仍未单独验证。
- 当前位于 TUN L0 决策处的 FakeIP 范围保护，已与路由器的
  发放台账、退役范围检查区分开；前者不查询后两者记录的内容。
  元数据刷新失败现在会直接返回，取代了那条特定的历史 oracle 发现。
  Scope panic 行为与 Box 启动/关闭阶段锚点已对照源码检查。针对性的
  Windows Go 1.26.10 单元测试结果记录在 `ACCEPTANCE.md` 中。
- 当前固定的 Cronet fork 同时有 Linux 和 Windows 平台文件；Windows 文件需要
  `with_purego`。已更正两处旧说法：仅 Apple 构建，以及自相矛盾的 uTLS profile
  描述。AnyTLS 的仅服务端 IPv4 偏好与每次调用错误命名，已通过
  其拨号器和出站源码追踪；这些不证明对端互操作性或吞吐量。
- 独立的交叉阅读更正了项目概览里旧的发布与依赖陈述、
  架构概览的范围/验证与 MASQUE 大小过度主张，以及相应的
  MASQUE 验收行和不变量行。历史计数明确限定在其修订版本内。
- 双栈准入已对照观察器、本地地址族门控和调度器检查：
  过时的本地判定不能拒绝，严格配置的策略保持独立；地址族健康记录
  有界、会过期，它改变的是竞争时序，而不是把某个地址族列入黑名单。
- Libbox 监视器的刷新/发布顺序、以及刷新期间关闭的围栏，已对照源码检查，
  针对性测试在 Windows 上使用 Go 1.26.10 和 `p2ios` 通过。Apple 生命周期的睡眠测量
  起点与重用边界发布已区分开；两个测试结果都不是设备验证。
- MTU 模块关于只有 Hysteria2 通过 `InitialPacketSizeCeiling` 的主张已更正：TUIC
  也会通过，而 MASQUE 只做初始钳制，不能证明后续数据包有硬上限。
  针对性的调度器默认值、仲裁和拒绝所有权测试在本地通过。
- 历史 XHTTP 基准的来源已通过祖先关系检查；记录的测量值不是
  当前性能证据，也不支持“该 fork 从未落后”的主张。
  性能索引现在会限定带日期的数值，并避免与协议无关的结论。
- 移除了一条过时的 CI 模块诊断，它声称 Windows TLS 1.3 测试会联系远程主机并
  证明不支持 Schannel。这些测试的本地握手失败已复现，原因仍
  未知；历史失败不能排除后来的回归。
- 直接卸载（direct-offload）层表现在区分路由前缀设置与自动重定向流
  判定，并且不再断言内核辅助复制在每个操作系统、每条流上都可用。
- Windows 守护进程源码审查更正了 Linux 没有对端认证的主张：Linux 的本地
  套接字路径使用 `SO_PEERCRED` 和进程启动身份。Windows 证书过期策略
  记录为有范围的权衡，不是无条件的安全保证。
- 重新审视了完整审计 BUG-009/015：当前远程客户端有五分钟 HTTP 超时，
  发布路径会检查关闭状态，TUN 把重定向更新与关闭串行化。Updater Close 不
  join，已经复制的通用回调也不会被全局召回；针对性的 TUN 顺序
  测试通过，没有提出 Linux 集成主张。
- 历史 BUG-011/014 已对照 uTLS 防护和精确的本地 Cronet 固定点复查：不支持的
  ECH 指纹在配置阶段就被拒绝；Cronet 恢复时的启动 panic 被赋给
  一个命名返回值。原生启动失败，以及所有允许的 ECH 线上格式组合，仍未测试。
- 历史 BUG-002/010 已对照 `common/ktls/ktls.go` 和精确固定的 sing
  splice 源码复查：成功的钩子路径会转发返回的字节并恢复，握手后的
  消息也被排空。Linux kTLS splice/KeyUpdate 执行在这台 Windows 主机上仍未验证。
- 历史 BUG-005 已对照精确的 quic-go 固定点 `2daa76f5bd52` 检查：成功的无 headroom
  复制会释放载荷；批次失败时保留调用方所有权，即使已复制的部分前缀
  已经入队。因此块所有权原子性不能确立恰好一次传输。
- 历史审计其余已索引条目已映射到 TUN、Naive、组、生命周期
  和 MTU 章节。活动流的静默停滞、启动时固定的 IPv6 入口呈现，仍
  是记录在案的局限，没有被标成普遍修复。
- 当前工作流声明已交叉核对：Android ARM64 除 dispatch 外还有按路径过滤的测试推送，
  严格的发布验收自测在发布契约
  工作流中运行；Verify 中的提及是注释，不是直接调用。
- 本地 libbox ABI 仅固定点脚本在 MSYS2 bash 下对两个客户端 gitlink 均通过。它没有
  执行完整的客户端 ABI 扫描；脚本里的 gomobile 固定点在这套环境中显示为未知。
- 受跟踪的 schema 比较在本地使用 Go 1.26.10 和 `DEFAULT_BUILD_TAGS_OTHERS` 通过。
  当前 Verify 的竞争声明还包括 `protocol/mieru`；历史命令列表
  和包计数已更正，或标记为限定于特定修订版本。
- UoT 每数据报防护按目的地缓存，并在检查
  解析后的地址之后转发原始域名。只做这项检查并不能固定转发的地址；没有下游证据时，
  会话级的 resolve/check/dial 保证不能扩展到这条路径。
  针对性的 memo/key/no-match 测试在本地通过；memo 增长测试直接插入键，
  并不衡量端到端攻击。验收行已相应收窄。
- 另一次验收/基线检查更正了 Apple 固定点门禁、根 CI 测试范围、历史
  发布自测的归因、祖先计数图，以及“空
  增量证明旧知识有效”这一错误含义。历史测试仍限定在其自身修订版本内。
- 可重用所有者测试检查的是命名类型，并不禁止每一个第二个 `ReuseSuspect`
  实现；它先前引用的测试命令没有匹配到任何测试。相关
  注意事项已更正，并区分了 sing fork 的 splice 工作流声明与实际观察到的一次运行。
- 测试名称扫描发现，所引用的重复成员 lint 测试并不存在；实现改由
  源码验证，附近那个名字相近的测试覆盖的是另一套设置。前缀正则和显式
  历史名称需要人工判断，不能整体计为缺失测试。
- 对模块文档以及主验收/架构/不变量文档做了一次有限的源码路径扫描，发现
  234 个不同的 `.go` 路径候选：223 个存在于本仓库，九个位于精确
  固定的 sing 依赖下，两个明确属于外部 gomobile/Xray 源码。这次扫描
  只检查路径是否存在，不检查历史行锚点、外部源码内容或主张准确性。
- 同一次扫描发现 `TestQUICOwnership` 没有匹配到定义。验收命令现在改为选择
  实际的 `TestQUICSniffContext` 测试，它们在本地通过；其他未匹配的名称已确认
  为历史引用或非测试文本，没有被自动当成当前测试。
- 更正一处较早的校准陈述：Verify 在 `cd test` 之后运行 `TestNetLogEventCount`，
  该测试位于单独的测试模块中。它不是主根包的测试；检查工作目录
  和测试定义后，验收条目与 CI 条目已更正。
- 离线模块完整性脚本在 Go 1.26.10 下对发现的所有三个模块通过；根/测试
  替换对等性包含 36 个条目和全部 31 个 Cronet 路径。这次没有运行 `go mod verify`，
  也没有确立原生归档/产物的来源。
- Mieru 是基线之后一个相当大的协议新增项，现在有专门的集成模块，覆盖
  配置、底层、回退选择和生命周期。源码与选定的本地测试支持
  这些边界；真实网络恢复和独立对端行为仍属另一类证据。
- DNS 组选择、惰性有界健康记录、取消和 NXDOMAIN 分类已
  对照源码与较早的本地 DNS 传输套件审查。FakeIP 概览已与
  范围更窄的当前 TUN 防护、以及单独的路由器发放检查对齐。
- Tailscale 域名匹配、无解析器与转发后缀优先级、平台默认路由
  钩子和共享 getter 清理已对照源码检查。针对性的带标签测试在 Windows 上通过；没有
  演练真实 tailnet 或物理切换。
- 基线之后的故障转移 shim 通过组转发并行拨号选项，但有意
  不暴露误导性的首成员拨号链；它的数据包方法和生产数据包路径
  具有不同的所有权。三个针对性路由测试在本地通过。
- 已检查精确的 sing 分配器和 `Buffer.Release`：释放在
  归还托管存储之后清除 Buffer 结构体，而不是底层字节。历史几何基准限定在其
  测量环境内，没有被说成精确或通用的性能结果。
- Sniff 解析器判定与数据包读取致命错误已区分开，续读轮次
  上限与先前捕获数据包的重放也已区分。记录在案的 HTTP 请求行分片测试
  和 SSH 横幅截断测试，在本地使用 Go 1.26.10 通过。历史节奏测量
  不能确立与主机无关的吞吐量上限。模块注册表现在区分
  独立发布验收检查器与实际发布路径。
- Bridge L3 转发、可选的 OpenVPN/OpenConnect 端点与 DNS 注册，现在都
  有导航条目。针对性的 bridge 索引所有权测试和端点解析器退役测试在
  Windows 上使用 Go 1.26.10 通过；这不是实时 bridge 或 VPN 测试。MTU 文本现在把
  未知路径的主张限定在 MASQUE 计算上；bridge 后端确实会读取接口 MTU。
- 基线之后的 OOM 执行器、分层空闲/压力/危机回收、复检、关闭屏障和
  重载状态，已在 power 模块中审查并记录。未过滤的 OOM 包套件
  在 Windows Go 1.26.10 上通过。已更正固定 100 毫秒轮询的主张，以及“
  内部 JSON 排除标志意味着 killer 默认禁用”的推断。
- 半关闭观察现在描述可观察的用户空间套接字门控、内核套接字跳过、
  终止与本地截止时间判定、关闭结算，以及配置的定时器。选定的路由
  单元测试在 Windows 上通过；活动流和真实设备恢复仍不在这一证据范围内。
- Naive 服务器限流器已对照其 accept 路径检查：连接数超限的拒绝会
  直接关闭套接字，而不是返回先前记录的 HTTP 429。默认限制测试和拒绝连接
  测试在本地通过；不足以支持该服务不可识别的推断。
- 当透明链是唯一阻断因素时，Fast Path 授权可能改变判定；它
  仍然不能覆盖其他阻断因素。Naive 参考客户端互操作性仍是明确的
  证据缺口，不能从兼容的协议结构推断出来。
- systemd-resolved 循环仍保留自己的 stop/join，但只靠 join 不能确立所有
  单独生成的异步更新都已完成。DNS 需求表中的 `\|` 是用于渲染正则交替的 Markdown
  转义，不是字面 Go 正则转义；kTLS 包
  不包含测试的过时主张已被移除。
- DNS adblock 的堆/goroutine 测试断言的是容差，不是严格为零；下游解析器的
  实际保留量也不能只由通告的 TTL 保证。Windows 证书
  注意事项现在反映有效性策略上的权衡，与安全模块的分析一致。
- 一次跨模块检查调和了四处内容：验证器副作用范围、本地完成的离线模块
  对等性检查、当前 Cronet 平台支持，以及 MASQUE 初始大小与后续数据包主张。
- Windows 进程创建时间排序与已退出进程检查都是特定检查，不是对
  祖先关系、或不存在任何 TOCTOU 竞争的笼统证明。历史 Apple 设备结果
  限定在其测量的配置、修订版本和睡眠事件内。
- XHTTP 模式选择、XMUX 全有或全无的默认值、池退役和 packet-up 批处理，已
  对照源码与针对性本地测试交叉检查。池退避在可疑退役之后同样适用；
  这些结果不能确立实时对端互操作性。
- 检查了监听器通配地址族绑定、TUN 设备读取重试、SSH 会话退役和 Linux
  命名空间持有者。监听器、设备和 SSH 包在 Windows 上通过；仅 Linux 的
  行为没有运行。仍有一处源码/注释不一致：原始 Winsock 10004/10008/10055 在
  Windows Go 1.26.10 上不匹配所识别的 POSIX 错误，也没有列为显式异常；
  设备的实际投递仍未验证。本次刷新没有更改运行时代码。
- v0.1.9 之后移除的 `service/resolved` 短名称扩展，与 Tailscale 的
  单独搜索域逻辑已区分开。历史 Cronet 竞争/链接观察限定在当时的固定点和
  profile 内，不是对每个产品或当前依赖状态的断言。
- `common/netcap` 的范围与失败即放行模型已独立检查，其未过滤的单元
  包在 Windows Go 1.26.10 上通过。AI 入口点现在还拒绝“
  空 Git 增量证明旧知识正确”这一过时推断，并改用执行时记录的基线。
- 独立的导航与命令审查更正了重命名的模块注册表章节，以及一个
  没有匹配任何测试的验收过滤器（`TestGate`）。替换后选用 HTTP 和 TLS 门禁
  测试，它们在本地通过。对 115 个文件的 Markdown 链接/锚点扫描发现 23 个相对链接，
  没有缺失目标、也没有检测到坏锚点；大多数导航使用反引号引用，需另行
  检查，因此这次扫描不是对索引的笼统证明。
- Apple 源码选择已在 refs 文件、源码脚本、工作流、
  libbox 模块和不变量之间独立调和：工作流冻结一个已解析的分支/覆盖 SHA，而
  gitlink/ABI 常量检查是另一回事，不证明构建使用了该 gitlink。旧的
  两条相反陈述——单一 gitlink 即权威、ABI 未接线——已移除。
- `check-apple-source-selection.sh` 最初退出 1（65 通过，2 失败），原因是 `python3` 解析
  到了 Windows 应用执行存根（状态 49）。改用临时 PATH shim 指向捆绑的
  Python 后重新执行，在 MSYS2 bash 下 72 项检查全部通过，零失败。这属于源码/fixture 验证，
  不是设备、原生构建或已发布产物检查。
- 对照 `TrimLevel.Action` 交叉阅读两个平台模块，发现并更正了两行错误：
  Android 级别 5（`TrimRunningModerate`）和 20（`TrimUIHidden`）都映射到 `TrimIgnore`，而不是
  `TrimProgressive`。
- 检查了精确目标 v0.1.31 的 GitHub 运行日志：Verify 选定的竞争包和带标签的
  kTLS 单元包报告 `ok`；Apple 解析出 `3aedd7417488`，但 ABI 检查报告 PARTIAL，
  原因是跳过了 Swift 访问器探测，签名预检也在未签名模式下被跳过。Release
  的运行步骤报告成功，而独立的资产字节检查和设备检查仍不在本次审计范围内。
- 不变量检查把分片的 sniff 解析器判定与致命读取/取消路径对齐，并
  区分了 release 作业/产物成功与被跳过的内部检查。这对
  观察到的 v0.1.31 Apple ABI PARTIAL 结果很重要；作业成功不能抹去那次跳过。
- 一次 Git 对象检查覆盖了提交索引、基线和模块中 279 个不同的反引号十六进制样引用。
  大多数能在父仓库中解析；例外包括客户端/依赖 SHA、一个数值
  和一个运行 ID。两个历史父提交前缀无法解析，已如实标记；
  桌面恢复锚点已更正为匹配的当前提交 `7a46125ded0`。索引中
  过时的“live”发布允许列表缺陷，已与历史修复和当前运行调和。
- 更正了 power 模块中剩余的表格，它仍声称命名所有者测试禁止第二个 `ReuseSuspect`
  实现。真实设备和平台验证仍限定在实际目标内，没有
  被当成普遍的发布阻断结论。
- 另一次单独的导航路径扫描覆盖了根条目、
  索引、架构、不变量和验收文件中的 320 个反引号 Markdown 文件引用。四十个裸模块名被明确为
  `modules/...` 路径；对该有限集合重复扫描，未发现缺失文件。单模块索引行里引用的数字模块
  章节，在机械检查中也与标题匹配。
- 一处较早的检出假设需要更正：Apple 和 Android 子模块已在
  `3aedd7417488` 和 `9d15717a85c5` 初始化并保持干净；桌面未初始化。Android Kotlin 探测
  扫描了 309 个文件，报告零违规。Apple 探测名义上对
  423 个文件返回零违规，但 Windows 的 `core.symlinks=false` 会把受跟踪链接变成文本文件，且有一个目标
  缺失，所以这不是完整的 Swift 扫描；这个本地退出码不会逆转 CI 的跳过。
  Android 已检入的空闲广播 → 排队暂停/唤醒路径也已阅读；没有执行设备竞争测试。
- 上游祖先关系已重新测量：本地 `upstream/testing` 与后一
  目标的合并基为 `a6952ccf6907c18e18917fcb763a433e8e36937b`；当前本地与 v0.1.9 的合并基
  为 `d630fa94c53b2e2bdf256fad8b9d1cff809146ed`。先前的 `e4b7e692` 仍是历史比较
  锚点，它不是 `a6952ccf` 的祖先。后来一次远程检查返回上游 `testing` 位于
  `e5d91fa79f6319d5de940d6c81d81f0fe53b9476`；该对象已在本地，并与目标有相同的合并基
  `a6952ccf`。祖先关系不是作者身份证明，这些 ref 都可能移动。
- 其余变更的 Go 路径按子系统分组，没有从提交标题推断。
  Flow tracker 的附加/关闭串行化和无锁计数器边界已对照源码检查，
  针对性测试通过；Linux 原始目的地解码、Android 进程查找、DERP 主机处理
  和生成的信任根变更，仍属于特定平台/协议的源码路径，不是笼统的互操作
  或设备证据。带标签的 DERP 主机格式单元测试在 Windows 上通过了域名、IPv4 和 IPv6
  用例；它并不演练真实 DERP 网格。
- 在干净的客户端扫描之后，`clients/apple/Library/Network/ExtensionPlatformInterface.swift` 上
  出现了并发的未提交变更。它不在本次刷新范围内，本次刷新没有修改或暂存它；
  较早的扫描结果描述的是这次变更之前观察到的干净检出。
- DNS 搜索处理已在 DHCP、本地系统配置、resolved 服务和
  Tailscale 中检查；它们不共享同一条通用扩展规则。缓存刷新失败的范围也已收窄：
  内部错误会返回给 FakeIP 元数据调用方，但一般失败的待处理批次
  不会自动重新入队，公共 `Flush()` 也只是尽力而为。

### 模块处置（Module dispositions）

清单记录的是已审查的源码边界与明确的证据限制，并不声称每个
历史行锚点或测量值都被重新推导：

| 条目 | 状态 | 证据或剩余工作 |
|---|---|---|
| `buffer-ownership`、`mtu-and-geometry` | updated | 精确的 sing/quic 固定点、池释放、MASQUE 块所有权、kTLS 以及 TUIC/MASQUE 上限边界；真实内核 splice 与物理 MTU 未验证 |
| `build-pruning-release`、`ci-release` | updated | 注册表、profile 门禁、依赖、工作流触发器、精确 SHA 的运行记录和发布选择；没有独立产物字节审计 |
| `core-lifecycle-runtime`、`power-mobile-lifecycle` | updated | Scope/Box、转换/半关闭、监听器/设备/SSH、OOM 回收、电源与 trim 映射；真实设备转换未验证 |
| `dns-demand`、`dns-adblock-action` | updated | 代次、地址族准入、FakeIP、搜索路径、端点解析器、组与否定应答；真实 DNS/TUN 环境矩阵未验证 |
| `fast-path-group-policy`、`data-path-optimization` | updated | 显式/固有授权、所有列出的阻断因素、入口消耗和调度器；历史性能结果仍限定于环境 |
| `sniff` | updated | 计划、所有权、QUIC/FakeIP、HTTP 与 SSH 局限及失败处理；任意对端互操作未验证 |
| `path-selection-validation` | updated | dry-run 效果、能力与结构严重性、拓扑、组转发和规则集闭包；没有断言全图健全性 |
| `libbox-platform-contract` | updated | TUN、Android trim、监视器与 Apple 源码权威；本地 Kotlin 扫描通过，Swift CI 扫描为 PARTIAL，并记录了本地符号链接注意事项 |
| `masque-connectip`、`naive-http-transport`、`xhttp-client-transport` | updated | 所有权、回退、UoT、客户端加密与 XMUX；独立对端互操作仍未验证 |
| `mieru` | added | 配置、底层、回退与生命周期的源码已审查；选定测试和精确目标的 CI 竞争包通过，独立对端恢复未验证 |
| `windows-daemon-security` | updated | 信任边界、证书策略与精确目标的 CI 测试；没有运行已安装服务的端到端测试，本地签名 fixture 被跳过 |
| 根架构、不变量、验收、索引和历史报告 | updated / archival | 矛盾、测试过滤器、路径、版本与证据范围已调和；旧报告仍属历史，旧锚点没有被普遍重新推导 |

### 刷新状态（Refresh status）

```text
KNOWLEDGE_REFRESH = COMPLETE (source-level; limitations above remain)
OLD_PRODUCTION_BASELINE = 5fc4f9366a60202fdc7c98cd6fa280cceace65ba
NEW_PRODUCTION_BASELINE = cb71cf80d7224a51823b455f0be5959ad8556933
PRODUCTION_CODE_CHANGED_BY_REFRESH = NO
RUNTIME_BEHAVIOR_CHANGED_BY_REFRESH = NO
PROTOCOL_BEHAVIOR_CHANGED_BY_REFRESH = NO
ABI_CHANGED_BY_REFRESH = NO
GIT_HISTORY_REWRITTEN = NO
DELTA_CLASSIFIED = YES (coarse; semantic clusters deep-read)
AFFECTED_SUBSYSTEMS_REAUDITED = YES (source boundaries; evidence limitations recorded)
COMMIT_PROVENANCE_UPDATED = YES (unresolved historical references labeled)
INVARIANTS_REAUDITED = YES
ACCEPTANCE_UPDATED = YES
HISTORICAL_DOCS_RECONCILED = YES (point-in-time archives retained)
CROSS_FILE_CONSISTENCY = PASS (reviewed claims and limited mechanical checks)
FUTURE_AI_INCREMENTAL_READ = PASS (baseline, navigation and delta instructions checked)
```
