# 出站 Provider

出站 Provider 从一个配置之外的文档发布节点：订阅 URL、本地文件，或配置本身。发布的节点进入与静态
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
      "outbounds": ["self-hosted"],
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
  "initial_path": "provider/airport-initial.json",
  "user_agent": "sing-box",
  "http_client": "download",
  "update_interval": "6h",
  "include": "hk|sg|jp",
  "exclude": "expired"
}
```

| 字段              | 说明                                                                     |
|-------------------|--------------------------------------------------------------------------|
| `url`             | 必填。必须是绝对的 `http`/`https` URL。                                   |
| `path`            | 下载文档的缓存文件，与 `initial_path` 互斥。                              |
| `initial_path`    | 仅在无缓存且首次下载失败时使用的文档。                                     |
| `user_agent`      | 请求的 `User-Agent`，与 `http_client.headers` 冲突。                      |
| `http_client`     | 下载使用的 [HTTP 客户端](/zh/configuration/shared/http-client/)。         |
| `download_detour` | 已弃用。下载使用的出站标签，请改用 `http_client`。                         |
| `update_interval` | 刷新间隔，最小 `1h`，默认 `24h`。                                          |
| `include`         | 正则：只发布**渲染后标签**匹配的节点。                                     |
| `exclude`         | 正则：跳过**渲染后标签**匹配的节点，优先于 `include`。                     |
| `override_dialer` | 应用到每个已发布节点的拨号字段：`detour`、绑定、超时、域名解析器。          |
| `health_check`    | 见下文。                                                                   |

本阶段不实现 Provider 的**端点**、`override_tls` 与 `override_anytls`，这些键不在 schema 中：文档里
设置它们会以指明键名的 unknown-field 错误被拒绝，而不是接受后静默忽略。`override_dialer` 在 remote 与
local Provider 上均可用。

URL 在配置加载时就会解析并检查 scheme：`ftp://` 或相对路径会以指明 Provider 的错误拒绝启动，而不是等后台
下载时才暴露。查询参数按凭据处理：日志中的 URL 会去掉 query、userinfo 和 fragment。

未配置 `path` 时，缓存写入 [cache file](/zh/configuration/experimental/cache-file/) 对应的记录；
关闭 cache file 后缓存也随之消失。配置 `path` 时，缓存先写入同目录的临时文件再重命名，写入中断不会破坏
上一份有效文档。

既无缓存、启动时又无法访问 URL 的 Provider 会以零节点启动：引用它的策略组在拨号时报告
“no available member”，并会在更新间隔到达后重试首次下载。

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

可选的健康检查会测量每个已发布节点，并把结果写入共享的 URLTest 历史（与 urltest 组读取的是同一份
存储）。它本身不选择节点，如何使用这些结果由策略组决定。

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
| `url`      | 请求的 URL。                           |
| `interval` | 检查间隔，最小 `1m`，默认 `10m`。       |
| `timeout`  | 单节点超时，默认 `3s`。                 |

### 节点文档

文档可以是完整的 sing-box 配置，也可以是只含 `outbounds` 的对象；其余顶层键全部忽略。节点使用与
静态出站相同的选项类型和 outbound registry 解码，因此本构建支持的协议无需第二套解析器。

节点按以下顺序处理：

1. 文档中的 `selector`/`urltest`/`loadbalance`、`direct`、`block`、`dns` 条目被跳过。
2. 应用 Provider 的 `exclude` 与 `include`（按渲染后的标签匹配）。
3. 同一文档内重复的标签会被确定性地重命名：第一次出现保留原名，之后依次为 `name (2)`、`name (3)`。

指向同级节点的 `detour` 会被改写为该节点的渲染后标签；指向其他对象的 `detour` 会被清空——Provider
无法确认主配置里是否还有那个标签，保留悬空依赖会让该节点创建失败。

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

被替换节点的旧对象在切换后关闭，因此正在使用它的连接会中断；只有未变化的节点能跨刷新保留连接。从文档中
删除的节点在移除时关闭，原因相同：已经没有对象可供新连接解析。

`update_interval` 是两次下载之间的最小间隔。本版本没有通过 Clash API 暴露手动刷新，Provider 按自己的
周期刷新。

### 与策略组配合

- 一个组可以引用多个 Provider。
- `use_all_providers` 表示包含全部已配置的 Provider，集合在启动时确定。
- Provider 节点通过渲染后的标签寻址，`default` 和 Clash API 看到的都是 `airport/hk-01`。
- 刷新删除了组当前选中的成员时，组按既有顺序回退（已保存的选择、default、第一个成员）；成员仍存在时
  保留选择。
- 尚无任何成员的组（Provider 还没有发布节点）可以正常启动，拨号时以指明组名的错误失败，而不是悄悄选择
  一个用户没有配置的节点。
