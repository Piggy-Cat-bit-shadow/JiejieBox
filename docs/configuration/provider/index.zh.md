# 出站 Provider

此功能已进入 JiejieBox 的 `testing`，需要包含出站 Provider 实现的构建。已发布的 v0.1.42 尚不支持这些字段。

出站 Provider 从订阅 URL、本地文件或内联配置发布节点。发布的节点进入与静态
`outbounds` 相同的出站命名空间，[selector](/zh/configuration/outbound/selector/) 或
[urltest](/zh/configuration/outbound/urltest/) 通过 `providers` 引用它们。

Provider 只提供**出站**。它不会替换配置中的 DNS、路由、入站或实验性设置：订阅里即使带有这些键也会被
忽略，只读取其中的 `outbounds`。

### 结构

```json
{
  "providers": [
    {
      "type": "remote",
      "tag": "airport",
      "url": "https://example.com/sing-box-subscription",
      "update_interval": "6h"
    }
  ],
  "outbounds": [
    {
      "type": "selector",
      "tag": "proxy",
      "outbounds": [],
      "providers": ["airport"]
    }
  ]
}
```

### 字段

| 键     | 格式                            |
|--------|---------------------------------|
| `type` | `remote`、`local` 或 `inline`   |
| `tag`  | Provider 标签                   |

Provider 标签在 Provider 之间必须唯一，且不能包含 `/`（斜杠保留为 Provider 与节点的分隔符）。标签同时是
节点标签的前缀：`airport` 中名为 `hk-01` 的节点会以 `airport/hk-01` 发布。策略组写
`providers: ["airport"]` 即可包含该 Provider 的全部节点。

### Remote

从 `url` 下载文档，缓存最后一次有效副本，并按 `update_interval` 刷新。

```json
{
  "type": "remote",
  "tag": "airport",
  "url": "https://example.com/sing-box-subscription",
  "path": "provider/airport.json",
  "user_agent": "sing-box",
  "update_interval": "6h",
  "include": "hk|sg|jp",
  "exclude": "expired"
}
```

| 字段              | 说明                                                                     |
|-------------------|--------------------------------------------------------------------------|
| `url`             | 必填。必须是绝对的 `http`/`https` URL。                                   |
| `path`            | 下载文档的缓存文件，与 `initial_path` 互斥。                              |
| `initial_path`    | 启动时无可用缓存便先加载的种子文档，与 `path` 互斥。                                     |
| `user_agent`      | 请求的 `User-Agent`，与非空 `http_client` 冲突。使用 HTTP 客户端时把 User-Agent 配在其 `headers` 中。                      |
| `http_client`     | 下载使用的 [HTTP 客户端](/zh/configuration/shared/http-client/)。         |
| `download_detour` | 已弃用。下载使用的出站标签，请改用 `http_client`。                         |
| `update_interval` | 刷新间隔，最小 `1h`，默认 `24h`。                                          |
| `include`         | 正则：只发布**文档内原始标签**匹配的节点。                                     |
| `exclude`         | 正则：跳过**文档内原始标签**匹配的节点，优先于 `include`。                     |
| `override_dialer` | 应用到每个已发布节点的拨号字段：`detour`、绑定、超时、域名解析器。          |
| `health_check`    | 见下文。                                                                   |

`http_client` 也可使用顶层 `http_clients` 的标签或内联对象，与旧的 `download_detour` 二选一。

本阶段不实现 Provider 的**端点**、`override_tls` 与 `override_anytls`，这些键不在 schema 中：文档里
设置它们会以指明键名的 unknown-field 错误被拒绝，而不是接受后静默忽略。`override_dialer` 在 remote 与
local Provider 上均可用。

URL 在配置加载时就会解析并检查 scheme：`ftp://` 或相对路径会以指明 Provider 的错误拒绝启动，而不是等后台
下载时才暴露。查询参数按凭据处理：日志中的 URL 会去掉 query、userinfo 和 fragment。

未配置 `path` 时，缓存写入 [cache file](/zh/configuration/experimental/cache-file/) 对应的记录；
关闭 cache file 后缓存也随之消失。配置 `path` 时，缓存先写入同目录的临时文件再重命名，写入中断不会破坏
上一份有效文档。

没有可用缓存或种子文件、启动时又无法访问 URL 的 Provider 会以零节点启动：引用它的策略组在拨号时报告
指明 Provider 的等待错误（`... has no available member yet: it is waiting for outbound provider <tag> to publish nodes`），
并会在更新间隔到达后重试首次下载。

下载路径本身不能依赖尚未出现的节点。未配置 `http_client` 时，Provider 使用 Box 创建的隐式默认 HTTP 客户端，
它的拨号器跟随 `route.final`；显式 `http_client.detour` 或已弃用的 `download_detour` 则跟随该出站。若这条路径
只经过由本 Provider 填充、且当前没有成员的组，首次下载会等待自己的节点：Provider 会以指明该组和路径的错误
拒绝启动，而不是留下一个永远为空、只在日志里重复失败的组。组有静态成员、由其他 Provider 提供成员，或已有
缓存/`initial_path` 时不受影响。

DNS 解析也是下载路径的一部分。Provider 的 URL 主机名是域名时，拨号器先用 `http_client.domain_resolver`
（若配置）或 `route.default_domain_resolver`（若配置）解析它；该 DNS 服务器自身的 `detour` 链如果进入
“只由本 Provider 供应且当前无成员”的组，同样会在首次下载时自等待。Provider 会以
`cannot resolve the download host through outbound group …` 拒绝启动，并点名解析器。没有配置解析器（走系统
DNS）或 URL 是 IP 字面量时不检查。

下载是异步的：`sing-box` 不会阻塞 Box 的启动去等待订阅 URL。单次请求有 30 秒超时（含读取 body）和
10 MiB 文档上限；服务器卡住或返回无限长度的 body 只会让这一次刷新失败，不会拖垮 Provider。

### Local

从本地文件读取同样结构的文档，并在文件变化时重新加载。

```json
{
  "type": "local",
  "tag": "local-nodes",
  "path": "provider/local.json"
}
```

文件必须在 Box 启动时存在且可读；与 remote 不同，local 没有缓存可回退，文件缺失会以指明 Provider 的错误
拒绝启动。路径在安装监视前会解析为绝对路径，这也是 Windows 上使用正斜杠书写的路径能够生效的原因。无法解析
的编辑和文件删除都会保留上一次发布的节点集，下一次有效编辑会替换它。

### Inline

直接在配置里声明节点，适合在没有订阅的情况下验证节点集，或由外部工具生成配置。

```json
{
  "type": "inline",
  "tag": "inline-nodes",
  "outbounds": [
    {
      "type": "socks",
      "tag": "local-socks",
      "server": "127.0.0.1",
      "server_port": 1080
    }
  ]
}
```

### 健康检查

三种 Provider 都支持可选健康检查，默认关闭。它探测已发布节点并写入共享 URLTest 历史，本身不选择节点。当前写入接口按默认目标记录结果；自定义 Provider 探测 URL 不保证与 URLTest 组自己的目标隔离，依赖精确选路证据时建议由 URLTest 组自行探测。

```json
{
  "health_check": {
    "enabled": true,
    "url": "https://www.gstatic.com/generate_204",
    "interval": "10m",
    "timeout": "3s"
  }
}
```

| 字段       | 说明                                  |
|------------|---------------------------------------|
| `enabled`  | 是否启用周期检查。                     |
| `url`      | 请求的 URL，默认 `https://www.gstatic.com/generate_204`。                           |
| `interval` | 检查间隔，最小 `1m`，默认 `10m`。       |
| `timeout`  | 单节点超时，默认 `3s`。                 |

### 节点文档

支持 sing-box JSON（只导入 `outbounds`）、Clash YAML、SIP008，以及原始或 Base64 分享链接列表。Clash 和链接格式只转换解析器支持的协议与选项，最终节点还需要当前构建的出站 registry 支持。

节点按以下顺序处理：

1. 原生 sing-box JSON 文档会跳过 `selector`、`urltest`、`direct`、`block` 和 `dns`。
2. 应用 Provider 的 `exclude` 与 `include`（仅 remote，按文档内原始标签匹配）。
3. 同一文档内重复的标签会被确定性地重命名：第一次出现保留原名，之后依次为 `name (2)`、`name (3)`。

remote/local 文档中的同级 `detour` 会加 Provider 前缀，外部 `detour` 会被清空。若要为这些节点指定主配置中的出站，使用 `override_dialer.detour`，它只填补没有有效同级 detour 的节点。inline 会重写同级引用并保留主配置引用。

### 失败处理

- 无法解析的文档（空 body、HTML 登录页、没有可用节点的 JSON）都算**刷新失败**，已发布的节点集保持不变。
  空文档绝不会被理解为“该 Provider 现在没有节点”；要移除节点请从配置中删除 Provider。
- 单个节点创建失败（本构建不支持的选项、非法服务器地址）会被跳过，文档其余部分仍然生效；若该标签原本已有
  节点，则该标签继续使用旧对象。
- 下载失败、非 2xx、重定向循环、超时、超大 body 都走同一条路径：本次刷新失败，保留最后一次有效集合，下个
  周期再试。

### 更新

更新是增量 diff：选项未变化的节点保留原有对象（因此保留现有连接）；变化的节点先准备并启动，再摘除旧
对象；替换失败的节点继续使用旧对象。只调整顺序或格式的订阅刷新不会打扰现有流量。

被替换节点的旧对象不会在 swap 时立刻关闭：引用它的组要等 Provider 通知后才会重建成员，这中间新拨号仍可能
拿到旧对象。对象的生命周期规则是：

1. **标签立即指向新对象**，按标签解析的新连接（路由、规则、`route.final`）看到新版本；
2. **旧对象保持可用**，直到该 Provider 的一轮通知中所有订阅组都成功重建成员（`UpdateGroups` 返回 nil）。
   组在拨号前会对取得的成员加一个交接租约：从取得成员到 `DialContext`/`ListenPacket` 返回（连接建立
   或失败）期间，旧对象不会被关闭；窗口期内经过组的拨号继续使用仍可工作的旧版本，不会拿到已关闭对象；
3. **失败的一轮不结束等待**：组成员更新被图校验拒绝或回调报错时，旧对象继续服务上一份选择，标签留给下一次
   成功轮次再完成回收；
4. **外部直接引用会延长保留**：静态出站/DNS transport 的 `detour` 直接指向该标签时，它们可能在第一次解析时
   缓存了旧对象；为避免“新拨号拿到已关闭对象”，旧对象保留到该引用消失或 Box 拆除。这是有意选择的保守行为，
   代价是这种（文档已建议避免的）配置会在每次刷新后多保留一份旧对象。

租约只覆盖“取得成员到拨号交接完成”，不会覆盖已建立的连接，也不会在拨号期间持有会阻塞 Provider 更新的全局锁：
计数更新是一次短锁，网络拨号在锁外进行。最后一个使用者退出后，若公布屏障已经通过，旧对象恰好关闭一次。
`Selected()` 等公开接口返回的是**快照**：可以立即读取 `Tag()`/`Network()`，但不要在跨过一次更新后再用它拨号；
要拨号请走组的 `DialContext`/`ListenPacket`（内部会加租约）。

被替换节点最终关闭时，正在使用它的连接会中断；只有未变化的节点能跨刷新保留连接。从文档中删除的节点在移除时
关闭，原因相同：已经没有对象可供新连接解析。若该节点仍被某个组发布为成员，它会保持存活到该组的成员更新完成，
然后才关闭。Box 拆除时，尚未完成回收的旧对象由生命周期 scope 关闭，每个对象只关闭一次。

`update_interval` 是两次下载之间的最小间隔。本版本没有通过 Clash API 暴露手动刷新，Provider 按自己的
周期刷新。

### 与策略组配合

- 一个组可以引用多个 Provider。组的 `include`/`exclude` 匹配最终发布的 `provider/node` 标签，不影响静态 `outbounds`。
- `use_all_providers` 表示包含全部已配置的 Provider，集合在启动时确定。没有任何 Provider 且没有静态成员时，
  该配置永远不可能有成员，组会以 `use_all_providers is set but no outbound providers are configured` 拒绝启动。
- Provider 节点通过渲染后的标签寻址，`default` 和 Clash API 看到的都是 `airport/hk-01`。
- selector 依次使用可用的已保存选择、刷新前仍存在的选中成员、显式 `default`、第一个成员。显式 `default` 不存在时会报错。纯 Provider 组可能在首次下载前无节点，建议省略 `default` 或选静态成员。
- 组的 `include`/`exclude` 把 Provider 已发布的节点全部过滤掉时，拨号错误会说明
  “provider 已发布 N 个节点，但本组的过滤器把它们全部排除”，便于区分“还没下载”和“过滤错了”。
- 尚无任何成员且未指定缺失默认节点的组可以正常启动（作为其他出站的 `detour` 目标时也一样）：启动期物理路径校验
  把等待 Provider 的路由记为 pending，而不是“永远到不了远端”的结构性失败。拨号时以指明组名和所等 Provider 的
  错误失败，而不是悄悄选择一个用户没有配置的节点。动态发布或组成员变更在对外可选之前会校验最终图；成环的更新
  被拒绝并保留上一份有效成员与选择。
- Provider 成员发布时只做图校验，不重跑启动期的 TCP/UDP 能力试运行。Provider 节点不承载组所投递的网络时，
  该流在运行时失败（与静态成员的运行时行为一致），但启动期不会出现静态配置那样的 capability 警告。
