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

Provider 标签在 Provider 之间必须唯一，同时是节点标签的前缀：`airport` 中名为 `hk-01` 的节点会以
`airport/hk-01` 发布。策略组写 `providers: ["airport"]` 即可包含该 Provider 的全部节点。

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
| `url`             | 必填。订阅 URL。                                                          |
| `path`            | 下载文档的缓存文件，与 `initial_path` 互斥。                              |
| `initial_path`    | 仅在无缓存且首次下载失败时使用的文档。                                     |
| `user_agent`      | 请求的 `User-Agent`，与 `http_client.headers` 冲突。                      |
| `http_client`     | 下载使用的 [HTTP 客户端](/zh/configuration/shared/http-client/)。         |
| `download_detour` | 已弃用。下载使用的出站标签，请改用 `http_client`。                         |
| `update_interval` | 刷新间隔，最小 `1h`，默认 `24h`。                                          |
| `include`         | 正则：只发布**渲染后标签**匹配的节点。                                     |
| `exclude`         | 正则：跳过**渲染后标签**匹配的节点，优先于 `include`。                     |
| `health_check`    | 见下文。                                                                   |

未配置 `path` 时，缓存写入 [cache file](/zh/configuration/experimental/cache-file/) 对应的记录；
关闭 cache file 后缓存也随之消失。既无缓存、启动时又无法访问 URL 的 Provider 会以零节点启动：引用它
的策略组在拨号时报告 “no available member”，并会在更新间隔到达后重试首次下载。

下载是异步的：`sing-box` 不会阻塞 Box 的启动去等待订阅 URL。

### Local

从本地文件读取同样结构的文档，并在文件变化时重新加载。

```json
{
  "type": "local",
  "tag": "local-nodes",
  "path": "provider/local.json"
}
```

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

### 更新

更新是增量 diff：选项未变化的节点保留原有对象（因此保留现有连接）；变化的节点先准备并启动，再摘除旧
对象；替换失败的节点继续使用旧对象。只调整顺序或格式的订阅刷新不会打扰现有流量。

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
