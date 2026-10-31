# JiejieBox 扩展配置与 DNS 负缓存

本文按当前 `testing` 源码说明扩展字段的放置位置、默认行为和实际作用。示例为配置片段，合并时需保留自己的节点凭据、证书、路由和 DNS 标签。旧版本应使用对应标签下的说明；客户端必须使用能识别这些扩展的配套内核。

## NXDOMAIN 到底优化了什么

### 从按记录类型缓存，扩展到按域名复用

普通 DNS 缓存把问题类型也作为键。客户端先后查询同一个不存在的域名的 A、AAAA、HTTPS、SVCB 时，可能向上游重复询问四次。

本 fork 对满足条件的 NXDOMAIN 建立 **QNAME 级负缓存**：已经确认名字不存在后，后续其他类型的问题可以在本地回答。它按 DNS 语义工作，不依赖 AdGuard 等产品的特定 SOA 标记。

| 场景 | 是否扩大到整个域名 |
| --- | --- |
| 单问题、空 Answer、带有效 SOA 且负 TTL 大于零的 NXDOMAIN | 是 |
| NOERROR 但无此类记录，即 NODATA | 否；没有 AAAA 不代表没有 A |
| SERVFAIL、REFUSED、FORMERR、超时、传输失败 | 否；临时故障不能宣告名字不存在 |
| Answer 中包含 CNAME 等记录，最终目标返回 NXDOMAIN | 否；不能将别名目标的不存在扩大到别名本身 |
| 无 SOA、SOA 负 TTL 为零、多问题报文 | 不建立 QNAME 级结论 |

### 缓存命中仍然是正确的 DNS 响应

- 域名按大小写无关的规范形式生成键，保留 QCLASS、DNS transport 标签、客户端子网和网络环境，不在不同命名空间间共享结论。
- 命中时复制保存的响应，用当前请求的 ID、域名、QTYPE 和 QCLASS 重建问题；不会拿 A 问题原封不动地回答 AAAA。
- SOA 的负 TTL 依据 `min(SOA TTL, SOA MINIMUM)`。普通到期模式下返回剩余 TTL，避免每次命中都重新给客户端一个完整缓存周期。
- 到期结论从负缓存删除，正常重新查询上游；负结论没有乐观过期复用例外。
- `rewrite_ttl` 不能把权威负 TTL 为零的响应提升成可跨类型复用的结论。
- 清理 DNS 缓存同时清理域名级负缓存；跨网络的过期查询不能在新代次写入结论。

### 怎么启用，在哪些设置下生效

**这是缓存内部行为，没有 `nxdomain_cache`、`nxdomain_ttl` 或 `nxdomain_optimization` 新开关。** 普通缓存启用且上游响应满足条件时自动使用。

```json
{
  "dns": {
    "disable_cache": false,
    "disable_expire": false,
    "cache_capacity": 4096
  }
}
```

这三个是已有 DNS 控制字段，并非本 fork 新发明的字段：

| 字段 | 配置位置 | 对负缓存的影响 |
| --- | --- | --- |
| `disable_cache` | `dns` | `true` 时不启用此负缓存；查询自身禁用缓存时也不应依赖缓存命中。 |
| `cache_capacity` | `dns` | 负缓存为有界内存 LRU，上限为配置容量与 2048 中较小者。 |
| `disable_expire` | `dns` | 为与普通缓存一致，此模式下负结论无到期寿命，响应 TTL 为零；不适合希望严格按上游 TTL 到期的配置。 |

负缓存是内存中的热点结论，不写入新的持久化格式。普通精确缓存使用磁盘后端时，这一内存负缓存仍独立初始化。关闭程序后不会以独立 NXDOMAIN 数据库恢复它。

实现：[负缓存](../dns/client_negative.go)、[缓存与网络代次](../dns/client.go)。

## 本地 adblock 与上游 NXDOMAIN 的区别

`adblock` 是 **DNS 规则动作**，写在 `dns.rules[]`，由你提供的域名条件或规则集决定拦截对象：

```json
{
  "dns": {
    "rules": [
      {
        "domain_suffix": ["ads.example"],
        "action": "adblock"
      }
    ]
  }
}
```

也可将 `domain_suffix` 替换为已定义的 `"rule_set": "anti-ad"`。这不会自动下载广告库，规则集需在 `route.rule_set` 中自行定义。

| 项目 | 上游学到的 NXDOMAIN | 本地 `adblock` |
| --- | --- | --- |
| 判断来源 | 上游的有效 DNS 响应 | 当前命中的 DNS 规则 |
| 上游请求 | 首次查询需要；缓存命中可省去 | 命中规则后不发起上游查询 |
| 本内核是否保存负缓存结论 | 满足条件时保存 QNAME 级结论 | 不通过上游负缓存保存规则结论 |
| TTL | 来自上游有效 SOA，命中时倒计时 | 每次按规则生成 SOA，TTL / MINIMUM 均为 600 秒 |
| 取消拦截后 | 受缓存清理与到期控制 | 内核立即按新规则判断；下游 OS / 应用可能仍持有此前的负缓存 |

`adblock` 没有 `ttl`、`cache_size`、`reply`、`drop`、`ip_mode` 等子字段，不能给它编造这些参数。它只用于 DNS 规则，不能放到 `route.rules[]` 中。优先级按 DNS 规则顺序决定；需要放行的例外应放在拦截规则之前。完整行为见 [adblock 专项说明](../KNOWLEDGE/modules/dns-adblock-action.md)。

## 常用扩展字段速查

下表给出放置位置；后文说明限制与示例。

| 能力 | 配置位置 | 默认 / 条件 |
| --- | --- | --- |
| DNS 分组 | `dns.servers[]` 的 `type: "group"`、`servers`、`mode`、`error_ttl`、`win_ttl` | `stable`，错误记录 2 分钟、胜出记录 5 分钟 |
| DNS 广告拦截 | `dns.rules[].action: "adblock"` | 仅命中配置规则时执行 |
| 上传整形 | `route.traffic_scheduler.upload_rate` | 未配置或零表示不整形 |
| 流量分类 | `outbounds[].traffic_class` | 未写可使用自动分类；显式 `default` 阻止自动推断 |
| 负载均衡 | `outbounds[]` 的 `type: "loadbalance"` | `round_robin`；按新连接 / 会话选择 |
| 有限重试 | `loadbalance` 出站的 `failover` | 默认 `false` |
| 测速状态码 | `urltest` / `loadbalance` 的 `expected_status` | 空表示不限制响应状态码 |
| 快速路径策略 | selector / urltest 出站的 `fast_path` | 逐流授权；实际绕行只在能消费 bypass 的入口生效 |
| 目标 DNS 归属 | 支持该声明的出站的 `destination_dns_ownership` | 默认 `false`；不是服务器域名解析开关 |
| XHTTP | 节点的 `transport` 对象，`type: "xhttp"` | 客户端；需要构建包含 `with_xhttp` |
| REALITY 密钥份额 | 节点的 `tls.reality.key_share` | 空值沿用指纹；`classical` / `hybrid` 显式选择 |
| VLESS 加密 | VLESS 出站的 `encryption` | 配套服务端生成的客户端参数串 |
| Mieru 回退 | Mieru 出站的 `fallback` | 默认关闭；备用须为另一种单传输 |
| Naive 回退 | Naive 出站的 `quic_fallback_to_h2` | 默认关闭，必须同时 `quic: true` |
| Naive 单引擎并发 | Naive 出站的 `insecure_concurrency_single_engine` | 默认 `false` |
| Naive 入站限制 | Naive 入站的 `server_limits` | 未配置不启用这些显式限制 |
| HTTP 认证前限制 | HTTP 入站的 `unauthenticated_limits` | 显式 `enabled: true` |
| HTTP 连接参数 | `http_clients[]` 或接受内联 HTTP client 的消费者 | 按使用方实际引用生效 |
| MASQUE 内部解析 | MASQUE endpoint 的 `inner_domain_resolver` | 未写使用普通 DNS 路由规则 |
| SOCKS TCP 预连接 | SOCKS 出站的 `tcp_preconnect` | 默认关闭，仅 SOCKS5 TCP CONNECT |
| SOCKS 复制调优 | SOCKS 出站的 `tcp_tuning.early_buffer_growth` | 默认关闭 |

## DNS 分组：定义之后还要被使用

```json
{
  "dns": {
    "servers": [
      {"type": "udp", "tag": "dns-a", "server": "1.1.1.1"},
      {"type": "udp", "tag": "dns-b", "server": "8.8.8.8"},
      {
        "type": "group",
        "tag": "dns-group",
        "servers": ["dns-a", "dns-b"],
        "mode": "fastest",
        "error_ttl": "2m",
        "win_ttl": "5m"
      }
    ],
    "final": "dns-group"
  }
}
```

- `stable` 优先复用当前仍干净的成员，失败后执行救援选择。
- `fastest` 使用近期胜出记录，在需要时进行竞选；`win_ttl` 仅该模式使用。
- `parallel` 对干净成员并发查询。全成员异常时三个模式都收敛为一次生存尝试。
- 只有 DNS 规则、`dns.final` 或其他消费者真正引用 `dns-group`，请求才经过该组。仅增加一段定义不会自动替换所有 DNS。
- 这些 TTL 管理的是成员选择记录，不是查询结果 TTL 或 NXDOMAIN TTL。

## 上传整形与 traffic_class

```json
{
  "route": {
    "traffic_scheduler": {"upload_rate": "8 MiB/s"}
  },
  "outbounds": [
    {"type": "direct", "tag": "interactive-out", "traffic_class": "interactive"},
    {"type": "direct", "tag": "bulk-out", "traffic_class": "bulk"}
  ]
}
```

需用自己的路由规则选择相应出站。`upload_rate` 是实际执行的速率，`8 MiB/s`、`20 MB/s`、`20 Mbps` 的单位不同；应依据实测上传能力配置，不是填写套餐标称带宽。

作用范围是内核管理的用户态上传复制路径，持续整形；下载、绕过内核的流量和内核 splice 路径不受其限制。分类可以写 `default`、`interactive`、`realtime`、`bulk`。高优先级仍计入总预算，不是免限速通道。未配速率时不会因为写了分类就自动得到一个带宽上限。

`traffic_class` 写在出站对象上，不在 `route.traffic_scheduler` 里；当前调度配置没有 `mode`、自动测速限速或单独下载速率字段。详见 [上传调度](fork/traffic-scheduler.md)。

## loadbalance、健康过滤与有限故障切换

在自己的 `outbounds` 列表中加入：

```json
{
  "type": "loadbalance",
  "tag": "balanced",
  "outbounds": ["node-a", "node-b"],
  "strategy": "round_robin",
  "url": "https://www.gstatic.com/generate_204",
  "expected_status": "204",
  "interval": "3m",
  "failover": true
}
```

`node-a` / `node-b` 必须是已定义节点，规则或 `route.final` 选择 `balanced` 后生效。策略还可为 `consistent_hashing`、`sticky_sessions`。不写 `url` 则不启用按此 URL 的健康过滤；没有健康记录本身不等于节点已坏。

`failover` 是全流最多一次备用重试，嵌套组共享预算；不会把已经传数据的连接迁移到其他节点。`expected_status` 也适用于 URLTest。`loadbalance.tolerance` 虽接受但不用于选路，它是 URLTest 的延迟容忍概念。详见 [负载均衡](fork/load-balance.md)。

## fast_path：不是全平台加速开关

```json
{
  "type": "selector",
  "tag": "direct-selector",
  "outbounds": ["direct"],
  "fast_path": false
}
```

写在 selector / urltest 逻辑组上，不能写到 direct、代理叶子或 loadbalance 上。

- `false` 显式禁止尝试快速路径，包括原本可获得固有授权的直连透明链。
- `true` 表达授权，仍必须满足逐流资格判断，不能使代理节点变成直连或忽略跟踪要求。
- 未写不表达额外策略；当前实现不再通过 AI 关键字推断快速路径权限，不能套用旧版本的标签启发式说明。
- 实际数据路径绕行针对 Linux `auto_redirect` 的相应入口。普通 TUN 平台可以接受同一配置，但不会因此绕过用户态或开启 splice。

`traffic_class` 的自动业务分类与此字段是两件事。详见 [组策略](../KNOWLEDGE/modules/fast-path-group-policy.md)。

## 双栈策略与自动优化的边界

双栈候选准入、网络代次和连接竞争是内核机制，不需要也没有 `admission_policy`、`network_generation`、`family_health` 这类用户 JSON 字段。可配置的地址族策略仍使用相应 DNS / resolver 的策略字段，例如直连出站：

```json
{
  "type": "direct",
  "tag": "direct-v4",
  "domain_resolver": {"server": "dns-a", "strategy": "ipv4_only"}
}
```

`dns-a` 须事先定义。`ipv4_only` / `ipv6_only` 为严格限制，`prefer_ipv4` / `prefer_ipv6` 为偏好；二者不能互换。严格策略也会约束相应字面地址路径，而本地能力不明不能被自动等同于该地址族不可用。策略所属位置还决定它作用于代理服务器解析还是最终目标解析，不能只在一个无关 resolver 上设置后就期待约束全系统。

TUN IPv6 呈现由内核能力和客户端平台交付条件共同决定；本地入口的 AAAA 抑制不等于禁止远端代理访问 IPv6。低内存缓冲区的 `with_low_memory` 是构建标签，不是可放进 JSON 的同名字段。

## destination_dns_ownership：最终目标由哪层解析

```json
{
  "type": "socks",
  "tag": "downstream",
  "server": "proxy.example.com",
  "server_port": 1080,
  "version": "5",
  "domain_resolver": "dns-a",
  "destination_dns_ownership": true
}
```

`domain_resolver` 解析代理服务器 `proxy.example.com`；`destination_dns_ownership` 决定要发送给下游的最终目标域名是否先经过本内核 DNS 策略解析。后者启用后，支持的 SOCKS / HTTP CONNECT 等路径发送解析出的 IP，原域名仍留在路由元数据中。

默认关闭，允许下游按原语义解析目标，有利于下游所在地区的 CDN 选择。这个布尔声明并不把所有类型都变成支持该能力的代理；不支持的明确声明会在路径校验中拒绝。使用示例中的 DNS 标签前须定义它。

## XHTTP 与 XMUX

放在使用 V2Ray transport 的出站（例如 VLESS）的 `transport` 中：

```json
{
  "transport": {
    "type": "xhttp",
    "path": "/xhttp",
    "mode": "packet-up",
    "x_padding_bytes": "100-1000",
    "sc_max_each_post_bytes": "1000000",
    "sc_min_posts_interval_ms": "30",
    "xmux": {"max_connections": "3"}
  }
}
```

| 字段组 | 作用与条件 |
| --- | --- |
| `mode` | `auto` / `packet-up` / `stream-up` / `stream-one`；`auto` 在 REALITY 下选择 stream-one，否则 packet-up。 |
| `host`、`path`、`headers` | 请求 Host、路径前缀和附加头，须与服务端及反代匹配。 |
| `session_placement` / `session_key`、`seq_placement` / `seq_key` | 会话与上传序号在 path / query / header / cookie 中的位置及键名。 |
| `session_table`、`session_length` | 成对配置会话 ID 字符表与长度，不足够的 ID 空间会被拒绝。 |
| `uplink_data_placement`、`uplink_data_key`、`uplink_chunk_size`、`uplink_http_method` | 上传载荷位置、分块和方法；header/cookie 等形式受 packet-up 模式约束。 |
| `x_padding_bytes` | padding 长度范围，默认 `100-1000`。 |
| `x_padding_obfs_mode` | 默认 false，沿用 Referer 查询里的旧式 padding；true 时启用以下可配置布局。 |
| `x_padding_key`、`x_padding_header` | obfs 布局的键名与头名，默认 `x_padding` / `X-Padding`。 |
| `x_padding_placement`、`x_padding_method` | obfs 位置为 cookie / header / query / queryInHeader，默认 queryInHeader；生成方式 repeat-x / tokenish，默认 repeat-x。 |
| `sc_max_each_post_bytes` | packet-up 每个上传 POST 的载荷上限，过小会增加请求数。 |
| `sc_min_posts_interval_ms` | packet-up POST 启动间隔的下限，默认 30 ms；`"0"` 取消该等待，需配合服务端设置。 |
| `no_grpc_header` | 取消流式模式默认 Content-Type；反代可能依赖该头避免缓冲，不能盲目打开。 |

XMUX 各范围支持单值 / `min-max`，部分兼容输入也支持二元素数组：

| 字段 | 生效位置 |
| --- | --- |
| `max_connections` / `max_concurrency` | 连接池数量与单连接并发边界，互斥。 |
| `c_max_reuse_times` | 连接被用于新流的次数。 |
| `h_max_request_times` | HTTP 请求次数；packet-up 一条流会产生多个 POST。 |
| `h_max_reusable_secs` | 连接可继续复用的时间。 |
| `h_keep_alive_period` | H2 保活秒数；零用默认，负值禁用 ping。 |

**XMUX 是整体默认规则**：省略或完全空对象会使用默认池设置；一旦设置任意字段，其余未写范围按零 / 不限处理，不会逐个补齐默认。示例仅指定连接数量，因此没有同时指定请求次数与寿命上限。

`sc_max_concurrent_posts` 为接受但忽略的旧参数；`server_max_header_bytes`、`no_sse_header`、`sc_max_buffered_posts`、`sc_stream_up_server_secs` 在本客户端实现不产生服务端功能。所有细项见 [XHTTP 字段说明](../KNOWLEDGE/modules/xhttp-client-transport.md)。

## REALITY 与 VLESS 加密

REALITY 的新增策略放在出站 `tls.reality` 对象中：

```json
{
  "tls": {
    "enabled": true,
    "server_name": "example.com",
    "utls": {"enabled": true, "fingerprint": "chrome"},
    "reality": {
      "enabled": true,
      "public_key": "替换为服务端公钥",
      "short_id": "替换为服务端短ID",
      "key_share": "hybrid"
    }
  }
}
```

密钥占位必须替换。`key_share` 省略 / 空值沿用指纹携带的份额；`classical` 移除混合份额；`hybrid` 要求指纹能够提供混合份额，不能提供则报错。服务端版本与指纹必须兼容。`fragment`、`fragment_fallback_delay`、`record_fragment` 属于外层 `tls` 对象，不放在 `reality` 里面。

VLESS 的 `encryption` 写在 VLESS 出站对象本层，与 `uuid`、`flow`、`tls` 同级；它是配套服务端生成的参数串，不是 `true` / `false`，也不能把 `key_share` 当成它的替代。不要自行填入示意公钥或套用其他服务端字符串。实现范围是客户端加密；具体格式和兼容边界见 [协议说明](fork/protocol-compatibility-phase2.md)。

## Mieru 主备传输

```json
{
  "type": "mieru",
  "tag": "mieru-out",
  "server": "proxy.example.com",
  "server_port": 8443,
  "username": "user",
  "password": "替换为密码",
  "transport": "tcp",
  "fallback": {"enabled": true, "transport": "udp", "server_port": 8444}
}
```

- `transport` 默认 TCP；`fallback` 默认关闭。启用时主备必须为相反的单传输，不能使用 `both`，也不能配 `no_wait` 握手。
- 备用 `server` / `server_port` 可按规则继承主配置；主配置只有端口范围时必须显式给备用端口。两端服务要真实提供相应 TCP / UDP 监听。
- 凭据共享；偏好切换只影响新会话。冷却与恢复为内建行为，不存在用户可任意填写的 `fallback.cooldown` 配置。
- `multiplexing`、`handshake_mode`、`mtu`、`traffic_pattern`、`server_ports` 属于 Mieru 出站对象；其中 MTU 是 Mieru UDP underlay 的参数，不是 TUN MTU。

详见 [入站](configuration/inbound/mieru.md)、[出站](configuration/outbound/mieru.md)。

## Naive 客户端回退与单引擎

```json
{
  "type": "naive",
  "tag": "naive-out",
  "server": "proxy.example.com",
  "server_port": 443,
  "username": "user",
  "password": "替换为密码",
  "quic": true,
  "quic_fallback_to_h2": true,
  "tls": {"enabled": true, "server_name": "proxy.example.com"}
}
```

`quic_fallback_to_h2` 默认关闭，只有 `quic: true` 时合法；它在适用的 H3 传输失败且尚未交付应用数据时尝试 H2，并非比较两者速度。客户端需要 `with_naive_outbound` 与平台 Cronet 支持。

`insecure_concurrency_single_engine: true` 配合 `insecure_concurrency` 控制多会话共用一个引擎，默认关闭；不是把并发数无条件调大。`stream_receive_window`、`quic_session_receive_window` 是客户端窗口参数；`quic_congestion_control` 选择客户端算法。不要把服务器的窗口或 `bbr_profile` 直接抄到这里。Naive 当前不支持的证书 pin / uTLS 组合会拒绝，不能用 `insecure` 代替所需安全校验。

## 入站限制与 HTTP/2 / HTTP/3 参数

Naive 的限制放在 Naive 入站对象中：

```json
{
  "server_limits": {
    "max_connections": 512,
    "max_connections_per_ip": 16,
    "header_timeout": "10s",
    "max_tracked_ips": 4096
  }
}
```

这是可合并片段，数值仅演示位置。总连接数包含已认证连接；来源按真实 transport peer 计，不信任转发头。请求头超时不用于已建立的隧道。TLS 握手超时在 `tls.handshake_timeout`，不存在 `server_limits.handshake_timeout` 或 `server_limits.idle_timeout`。

HTTP 入站用另一套 `unauthenticated_limits`：

```json
{
  "unauthenticated_limits": {
    "enabled": true,
    "max_concurrent_per_ip": 8,
    "requests_per_second": 10,
    "burst": 20,
    "idle_timeout": "10s",
    "max_tracked_ips": 4096
  }
}
```

该限制只用于相应 HTTP 认证前 / 伪装路径，不限制已认证代理流量；与 Naive 的总连接计数语义不同。

`stream_receive_window`、`connection_receive_window`、`max_concurrent_streams`、`idle_timeout`、`keep_alive_period` 在接受这些选项的 HTTP / Naive 入站本层，**不是自行套一个 `http2` 子对象**。具体可用集合按协议与 HTTP 版本解析。

HTTP 入站还可配置 `max_header_bytes` 与服务端 `bbr_profile`（`conservative` / `standard` / `aggressive`）；不写 BBR 配置不代表自动选 BBR。共享 HTTP/3 参数如 `initial_packet_size`、`initial_packet_size_ceiling`、`disable_path_mtu_discovery` 只适用于暴露它们的对象；不能把任意一个 QUIC 字段放到所有协议上。

## MASQUE 的两套解析与传输选项

MASQUE endpoint 的 `domain_resolver` 解析服务器域名，`inner_domain_resolver` 解析隧道内的目标；二者写在 endpoint 本层，取值为对应的 DNS 解析选项 / 服务器引用格式。

`inner_domain_resolver` 未配置时保留普通 DNS 路由规则。不能让 Bootstrap 解析依赖尚未建立的隧道，也不能因为写了一个 DNS 标签就认定它已经被 endpoint 使用。

`version` 未写时选择 HTTP/3；`disable_version_fallback` 控制是否禁止版本回退；`quic_congestion_control` 为客户端选择，区别于 HTTP 服务端 `bbr_profile`。`mtu` 为内层隧道 MTU，不能当作物理网卡 MTU 或所有外层 QUIC 包的硬上限。详见 [MASQUE 配置与实现](masque.md)。

## SOCKS 预连接与复制调优

```json
{
  "type": "socks",
  "tag": "socks-out",
  "server": "proxy.example.com",
  "server_port": 1080,
  "version": "5",
  "tcp_preconnect": {"enabled": true, "min_idle": 2, "max_idle": 4, "idle_timeout": "20s"},
  "tcp_tuning": {"early_buffer_growth": true}
}
```

两项均默认关闭，彼此独立。预连接仅对 SOCKS5 TCP CONNECT 有效，不用于 SOCKS4、UDP ASSOCIATE、BIND 或 UoT；准备连接仍使用出站自己的接口、detour 和拨号设置。复制调优让支持的路径更早扩展缓冲区，不能把它当作适合所有负载的通用提速开关。完整说明见 [SOCKS](configuration/outbound/socks.md)。

## HTTP client 定义与引用

`http_clients[]` 位于顶层配置。支持 `http_client` 的消费者可以引用其 `tag` 或接受内联配置，例如远程规则集。只定义 client、不让消费者引用，不会改变所有请求。

`engine`、`version`、`disable_version_fallback`、TLS、detour 及对应版本窗口参数写在 HTTP client 对象；Apple engine 与 Go engine 的平台能力不同。HTTP/3 的传输参数按 `version` 选择，JSON 中没有任意通用的 `HTTP2Options` / `HTTP3Options` 内部结构名。详见 [HTTP Client](configuration/shared/http-client.md)。

## 内存管理服务与平台策略

当前内存服务为 `services[]` 中的 `type: "oom-killer"`。配套客户端可能已经注入服务，需先确认其管理方式。服务接受的主要用户字段如下：

| 字段 | 位置 | 含义与条件 |
| --- | --- | --- |
| `memory_limit` | oom-killer 服务本层 | 对应模式的内存预算；不能解读为所有原生与 Go 分配都受 Go 软限制。 |
| `safety_margin` | 同上 | 预算安全余量，参与模式对应阈值推导。 |
| `min_interval` / `max_interval` | 同上 | 自适应观察间隔边界；普通配置默认 100ms / 10s，均须正值且 max 不小于 min。 |

Network Extension 使用统一的平台安全策略，普通配置不能任意改变其关键采样间隔和余量。`KillerDisabled` / `MemoryLimitOverride` 是内部字段，不是可以照抄进 JSON 的开关；空闲收益门槛、执行器优先级、复用 freshness 也不是任意用户字段。

回收按过期缓存、空闲池和有条件 runtime 回收逐层执行；紧急重置另有重采样与状态保护。完整示例、关闭 / 重载和 Fast Path 分层见 [Fast Path 与内存管理](FAST-PATH-AND-MEMORY.md)。

## 配置自查

1. 确认字段所属对象：`dns.rules`、`dns.servers`、`route`、出站、endpoint、TLS 和 transport 不可互换。
2. 确认定义被引用：DNS group、代理组、HTTP client 和命名 resolver 需要真实消费者。
3. 确认开关条件：例如 Naive H3 回退要求 `quic`，Mieru 主备要求不同单传输。
4. 确认平台 / 构建：`with_xhttp`、Cronet 和 Linux `auto_redirect` 各有条件；被接受的字段不一定改变每个平台的数据路径。
5. 合并后使用目标内核执行 `sing-box check -c config.json`。这能发现配置错误，但不代替对真实服务器与网络的连接验证。
