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

#### use_all_providers

包含全部已配置的 Provider，在启动时确定。

#### include

正则：只加入**渲染后标签**匹配的 Provider 节点。

#### exclude

正则：跳过**渲染后标签**匹配的 Provider 节点，优先于 `include`。

#### default

默认的出站标签。默认使用第一个出站。

#### interrupt_exist_connections

当选定的出站发生更改时，中断现有连接。

仅入站连接受此设置影响，内部连接将始终被中断。