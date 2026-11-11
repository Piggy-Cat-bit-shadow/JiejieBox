---
icon: material/new-box
---

!!! quote "sing-box 1.13.0 中的更改"

    :material-plus: [bypass](#bypass)  
    :material-alert: [reject](#reject)

!!! quote "sing-box 1.14.0 中的更改"

    :material-plus: [resolve.disable_optimistic_cache](#disable_optimistic_cache)  
    :material-plus: [resolve.timeout](#timeout)  
    :material-plus: [tls_spoof](#tls_spoof)  
    :material-plus: [tls_spoof_method](#tls_spoof_method)

!!! quote "sing-box 1.12.0 中的更改"

    :material-plus: [tls_fragment](#tls_fragment)  
    :material-plus: [tls_fragment_fallback_delay](#tls_fragment_fallback_delay)  
    :material-plus: [tls_record_fragment](#tls_record_fragment)  
    :material-plus: [resolve.disable_cache](#disable_cache)  
    :material-plus: [resolve.rewrite_ttl](#rewrite_ttl)  
    :material-plus: [resolve.client_subnet](#client_subnet)

## 最终动作

### route

```json
{
  "action": "route", // 默认
  "outbound": "",
  
  ... // route-options 字段
}
```

`route` 继承了将连接路由到指定出站的经典规则动作。

#### outbound

==必填==

目标出站的标签。

#### route-options 字段

参阅下方的 `route-options` 字段。

### bypass

!!! question "自 sing-box 1.13.0 起"

!!! quote ""

    仅支持 Linux，且需要启用 `auto_redirect`。

```json
{
  "action": "bypass",
  "outbound": "",

  ... // route-options 字段
}
```

`bypass` 在预匹配中为 auto redirect 连接在内核层面绕过 sing-box。

对于非 auto redirect 连接和已建立的连接，如果指定了 `outbound`，行为与 `route` 相同；否则规则将被跳过。

#### outbound

目标出站的标签。

如果未指定，规则仅在来自 auto redirect 的[预匹配](/zh/configuration/shared/pre-match/)中匹配，在其他场景中将被跳过。

#### route-options 字段

参阅下方的 `route-options` 字段。

### reject

!!! quote "sing-box 1.13.0 中的更改"

    自 sing-box 1.13.0 起，您可以通过 `reject` 动作拒绝（或直接回复）ICMP 回显（ping）请求。

```json
{
  "action": "reject",
  "method": "default",  // 默认
  "no_drop": false
}
```

`reject` 拒绝连接。

如果尚未执行 `sniff` 操作，则将使用指定方法拒绝 tun 连接。

对于非 tun 连接和已建立的连接，将直接关闭。

#### method

对于 TCP 和 UDP 连接：

- `default`: 对于 TCP 连接回复 RST，对于 UDP 包回复 ICMP 端口不可达。
- `drop`: 丢弃数据包。

对于 ICMP 回显请求：

- `default`: 回复 ICMP 主机不可达。
- `drop`: 丢弃数据包。
- `reply`: 回复以 ICMP 回显应答。

#### no_drop

如果未启用，则 30 秒内触发 50 次后，`method` 将被暂时覆盖为 `drop`。

当 `method` 设为 `drop` 时不可用。

### hijack-dns

```json
{
  "action": "hijack-dns"
}
```

`hijack-dns` 劫持 DNS 请求至 sing-box DNS 模块。

## 非最终动作

### route-options

```json
{
  "action": "route-options",
  "override_address": "",
  "override_port": 0,
  "network_strategy": "",
  "fallback_delay": "",
  "udp_disable_domain_unmapping": false,
  "udp_connect": false,
  "udp_timeout": "",
  "tls_fragment": false,
  "tls_fragment_fallback_delay": "",
  "tls_record_fragment": false,
  "tls_spoof": "",
  "tls_spoof_method": ""
}
```

!!! note ""

    当内容只有一项时，可以忽略 JSON 数组 [] 标签

`route-options` 为路由设置选项。

#### override_address

覆盖目标地址。

#### override_port

覆盖目标端口。

#### network_strategy

详情参阅 [拨号字段](/zh/configuration/shared/dial/#network_strategy)。

仅当出站为 `direct` 且 `outbound.bind_interface`, `outbound.inet4_bind_address`
且 `outbound.inet6_bind_address` 未设置时生效。

#### network_type

详情参阅 [拨号字段](/zh/configuration/shared/dial/#network_type)。

#### fallback_network_type

详情参阅 [拨号字段](/zh/configuration/shared/dial/#fallback_network_type)。

#### fallback_delay

详情参阅 [拨号字段](/zh/configuration/shared/dial/#fallback_delay)。

#### udp_disable_domain_unmapping

如果启用，对于地址为域的 UDP 代理请求，将在响应中发送原始包地址而不是映射的域。

此选项用于兼容不支持接收带有域地址的 UDP 包的客户端，如 Surge。

#### udp_connect

如果启用，将尝试将 UDP 连接 connect 到目标而不是 listen。

#### udp_timeout

UDP 连接超时时间。

设置比入站 UDP 超时更大的值将无效。

已探测协议连接的默认值：

| 超时    | 协议                   |
|-------|----------------------|
| `10s` | `dns`, `ntp`, `stun` |
| `30s` | `quic`, `dtls`       |

如果没有探测到协议，以下端口将默认识别为协议：

| 端口   | 协议     |
|------|--------|
| 53   | `dns`  |
| 123  | `ntp`  |
| 443  | `quic` |
| 3478 | `stun` |

#### tls_fragment

!!! question "自 sing-box 1.12.0 起"

通过分段 TLS 握手数据包来绕过防火墙检测。

此功能旨在规避基于**明文数据包匹配**的简单防火墙，不应该用于规避真的审查。

由于性能不佳，请首先尝试 `tls_record_fragment`，且仅应用于已知被阻止的服务器名称。

在 Linux、Apple 平台和需要管理员权限的 Windows 系统上，可自动检测等待时间。
若无法自动检测，将回退使用 `tls_fragment_fallback_delay` 指定的固定等待时间。

此外，若实际等待时间小于 20 毫秒，同样会回退至固定等待时间模式，因为此时判定目标处于本地或透明代理之后。

#### tls_fragment_fallback_delay

!!! question "自 sing-box 1.12.0 起"

当 TLS 分片功能无法自动判定等待时间时使用的回退值。

默认使用 `500ms`。

#### tls_record_fragment

!!! question "自 sing-box 1.12.0 起"

通过分段 TLS 握手数据包到多个 TLS 记录来绕过防火墙检测。

#### tls_spoof

!!! question "自 sing-box 1.14.0 起"

!!! quote ""

    仅支持 Linux、macOS 和 Windows，需要提升的权限。

在真实 ClientHello 之前注入携带本字段所指定 SNI 的伪造 TLS ClientHello，
用于欺骗仅放行特定主机名的 SNI 过滤中间盒。

详情与所需权限参阅出站 TLS [`spoof`](/zh/configuration/shared/tls/#spoof)。

#### tls_spoof_method

!!! question "自 sing-box 1.14.0 起"

控制伪造报文被真实服务器拒绝的方式。完整取值表与平台说明参阅出站 TLS
[`spoof_method`](/zh/configuration/shared/tls/#spoof_method)。

### sniff

```json
{
  "action": "sniff",
  "sniffer": [],
  "timeout": ""
}
```

`sniff` 对连接执行协议嗅探。

对于已弃用的 `inbound.sniff` 选项，被视为在路由之前执行的 `sniff`。

#### sniffer

启用的探测器。

默认启用所有探测器。

可用的协议值可以在 [协议嗅探](../sniff/) 中找到。

#### timeout

探测超时时间。

默认使用 300ms。

### resolve

```json
{
  "action": "resolve",
  "server": "",
  "strategy": "",
  "match_only": false,
  "disable_cache": false,
  "disable_optimistic_cache": false,
  "rewrite_ttl": null,
  "timeout": "",
  "client_subnet": null
}
```

`resolve` 将请求的目标从域名解析为 IP 地址。

#### server

指定要使用的 DNS 服务器的标签，而不是通过 DNS 路由进行选择。

#### strategy

DNS 解析策略，可用值有：`prefer_ipv4`、`prefer_ipv6`、`ipv4_only`、`ipv6_only`。

默认使用 `dns.strategy`。

#### match_only

将解析得到的地址仅用于规则匹配，而不是作为拨号候选。

默认情况下 `resolve` 同时完成两件事：返回的地址会被 IP 类条件（`ip_cidr`、IP `rule_set`、
`ip_is_private`、`preferred_by`）用于匹配，并且出站会使用其中某个地址拨号，而不是原始域名。
启用 `match_only` 后，同样的查询照常执行、同样的条件照常匹配，但这些地址只作为匹配依据发布：
拨号仍使用原始目标，因此支持域名目标的出站依然收到域名，并由该出站自行解析。

- 查询本身不变：仍使用相同的 DNS 服务器、策略、超时、缓存与 ECS 选项。该选项不会禁用 DNS，
  也不会增加第二个解析器。
- 字面量目标仍然优先于解析结果，与本选项不存在时一致：客户端实际连接的地址才是该连接使用的地址。
- 地址是按连接保存的。目标被改写（`override_address`）时会被清除，后续 `resolve` 会将其替换，
  也不会被 UoT 会话的逐数据报目标继承。
- `false` / 省略时保持原有行为。
- 本选项不会删除此前其他动作已发布的拨号候选，也不会让一个不支持域名的出站凭空收到域名：
  出站拿到的目标由该出站自身的能力与当前已有的候选共同决定（见下）。

出站是否收到域名，取决于出站本身和当前候选，而不是只看 `match_only`：

- 支持域名目标的代理出站（SOCKS、VMess、AnyTLS 等）会在代理端解析该域名；
- 配置了 `domain_resolver` 的 `direct` 出站同样可以收到域名并自行解析；
- 需要真实地址的路径（FakeIP 占位地址交给 flow 出站、TUN native pre-match 提交）会安全拒绝或
  继续走用户态处理，而不会把占位地址或匹配地址发出去；
- 同一条规则列表里更早的普通 `resolve` 候选会被保留：拨号仍使用它们，本选项不会删除。

这些地址是本地解析器的观察结果：它们决定规则，但不代表代理端最终访问的地址，因此"目标是私网地址"
之类的规则判定的是本地答案，而不是远端解析器的答案。

#### disable_cache

!!! question "自 sing-box 1.12.0 起"

在此查询中禁用缓存。

#### disable_optimistic_cache

!!! question "自 sing-box 1.14.0 起"

在此查询中禁用乐观 DNS 缓存。

#### rewrite_ttl

!!! question "自 sing-box 1.12.0 起"

重写 DNS 回应中的 TTL。

#### timeout

!!! question "自 sing-box 1.14.0 起"

覆盖此查询的 DNS 查询超时时间。

将覆盖 `dns.timeout`。

#### client_subnet

!!! question "自 sing-box 1.12.0 起"

默认情况下，将带有指定 IP 前缀的 `edns0-subnet` OPT 附加记录附加到每个查询。

如果值是 IP 地址而不是前缀，则会自动附加 `/32` 或 `/128`。

将覆盖 `dns.client_subnet`.
