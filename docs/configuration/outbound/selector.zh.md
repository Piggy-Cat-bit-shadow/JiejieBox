### 结构

```json
{
  "type": "selector",
  "tag": "select",

  "outbounds": [
    "proxy-a",
    "proxy-b",
    "proxy-c"
  ],
  "providers": ["airport"],
  "use_all_providers": false,
  "include": "",
  "exclude": "",
  "default": "proxy-c",
  "interrupt_exist_connections": false
}
```

!!! quote ""

    选择器目前只能通过 [Clash API](/zh/configuration/experimental/clash-api/) 来控制。

### 字段

#### outbounds

==必填==

用于选择的出站标签列表。若已配置 `providers` 或 `use_all_providers`，可以留空。

#### providers

要加入本组的出站 Provider 标签列表，见 [出站 Provider](/zh/configuration/provider/)。

纯 Provider 组可在首次订阅下载前以空成员启动，作为其他出站的 `detour` 目标时也会等待节点；这段时间的拨号会返回点名所等 Provider 的错误。下载路径若反过来依赖此组，则会形成引导环，参见[远程订阅与链式代理示例](../../FORK-CONFIGURATION.md#远程订阅与链式代理)。

#### use_all_providers

为 `true` 时使用启动时已配置的全部 Provider，覆盖显式 `providers` 列表。

#### include

正则：只加入**渲染后标签**匹配的 Provider 节点。

#### exclude

正则：跳过**渲染后标签**匹配的 Provider 节点，优先于 `include`。两个过滤字段均不影响静态 `outbounds`。

#### default

默认出站标签。Provider 节点用 `provider/node`。没有可用的已保存选择时，缺失的显式默认标签会报错。Provider 尚未加载时，建议省略此字段或使用静态成员。

#### interrupt_exist_connections

当选定的出站发生更改时，中断现有连接。

仅入站连接受此设置影响，内部连接将始终被中断。
