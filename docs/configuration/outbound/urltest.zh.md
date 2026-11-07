### 结构

```json
{
  "type": "urltest",
  "tag": "auto",
  
  "outbounds": [
    "proxy-a",
    "proxy-b",
    "proxy-c"
  ],
  "providers": ["airport"],
  "use_all_providers": false,
  "include": "",
  "exclude": "",
  "url": "",
  "expected_status": "",
  "interval": "",
  "tolerance": 50,
  "idle_timeout": "",
  "interrupt_exist_connections": false
}
```

### 字段

#### outbounds

==必填==

用于测试的出站标签列表。若已配置 `providers` 或 `use_all_providers`，可以留空。

#### providers

要加入本组的出站 Provider 标签列表，见 [出站 Provider](/zh/configuration/provider/)。

#### use_all_providers

为 `true` 时使用启动时已配置的全部 Provider，覆盖显式 `providers` 列表。

#### include

正则：只加入**渲染后标签**匹配的 Provider 节点。

#### exclude

正则：跳过**渲染后标签**匹配的 Provider 节点，优先于 `include`。两个过滤字段均不影响静态 `outbounds`。

Provider 刷新会重建测速组：新成员集合会重新测量，共享的 URLTest 历史保留仍然适用的结果。

#### url

用于测试的链接。默认使用 `https://www.gstatic.com/generate_204`。

#### expected_status

被视为可达的 HTTP 状态码。留空表示接受任何状态。

支持的写法：

```text
*                 任何状态
204               仅 204
200-299           范围
200/204/301-399   多个，用 / 分隔
200,204,301-399   多个，用 , 分隔
```

范围可以反向书写（`299-200` 等同于 `200-299`），列表最多 28 项。

它属于该组的测量标识：要求 `204` 的组与接受任何状态的组问的是不同的问题，因此即使 URL 相同，
两者也不会共用历史记录。

#### interval

测试间隔。 默认使用 `3m`。

#### tolerance

以毫秒为单位的测试容差。 默认使用 `50`。

#### idle_timeout

空闲超时。默认使用 `30m`。

#### interrupt_exist_connections

当选定的出站发生更改时，中断现有连接。

仅入站连接受此设置影响，内部连接将始终被中断。
### 统一延迟（unified delay）

一次测量在一条连接上只发两个 HTTP 请求：

```text
dial（不计时）
  ↓
HEAD #1  预热：代理链路、TLS、HTTP 与多路复用建链
  ↓
HEAD #2  计时：用于显示和比较的延迟
```

第一个请求承担了一次性的开销，因此报告出来的延迟反映的是链路本身而不是建链过程。如果第二个请求
以「不能说明节点已坏」的方式失败（例如服务器关闭了复用的连接），节点仍被视为可用，此时报告的延迟
是整次尝试的耗时。

每次测量最多只建立一条出站连接，并且该连接最多只交给 HTTP 客户端一次。

### 历史记录

保留两层互不影响的记录：

- **显示（display）** — 每个节点一条：最近一次成功的测量结果，无论用的是哪个 URL。节点列表和
  Clash 代理条目显示的就是它。手动测速会更新它。
- **健康（health）** — 每个（节点、URL、状态集合）一条。只有该组自己的自动检查会写入，组的选路、
  容差和间隔跳过都只读它。

因此手动测速不会影响组选路，测试任意 URL 也不会累积状态。检查失败只会删除它自己的那一条，绝不会
抹掉节点上一次成功的测量结果。
