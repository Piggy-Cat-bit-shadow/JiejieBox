# JiejieBox 扩展配置示例

按需要复制对应片段，合并到自己的配置。替换示例节点、UUID、密钥、密码、证书路径和文件地址；不要用示例覆盖已有列表。

## DNS 广告拦截

放在根配置的 `dns.rules`。

```json
{
  "dns": {
    "rules": [
      {
        "domain_suffix": [
          "ads.example"
        ],
        "action": "adblock"
      }
    ]
  }
}
```

命中后本地返回 NXDOMAIN + SOA，TTL 为 600 秒，不查询上游。

## 使用本地广告规则集

根配置同时定义规则集和 DNS 规则。

```json
{
  "dns": {
    "rules": [
      {
        "rule_set": "anti-ad",
        "action": "adblock"
      }
    ]
  },
  "route": {
    "rule_set": [
      {
        "type": "local",
        "tag": "anti-ad",
        "format": "binary",
        "path": "./anti-ad.srs"
      }
    ]
  }
}
```

将路径换成你的规则集文件。

## DNS 分组

放在根配置的 `dns`。

```json
{
  "dns": {
    "servers": [
      {
        "type": "udp",
        "tag": "dns-a",
        "server": "1.1.1.1"
      },
      {
        "type": "udp",
        "tag": "dns-b",
        "server": "8.8.8.8"
      },
      {
        "type": "group",
        "tag": "dns-group",
        "servers": [
          "dns-a",
          "dns-b"
        ],
        "mode": "fastest",
        "error_ttl": "2m",
        "win_ttl": "5m"
      }
    ],
    "final": "dns-group"
  }
}
```

`mode` 可选 `stable`、`fastest`、`parallel`；`win_ttl` 只用于 fastest，组需被规则或 final 引用。

## 上传限速与流量分类

`upload_rate` 写在 route 内；`traffic_class` 写在出站上。

```json
{
  "route": {
    "traffic_scheduler": {
      "upload_rate": "8 MiB/s"
    },
    "rules": [
      {
        "domain_suffix": [
          "api.example.com"
        ],
        "action": "route",
        "outbound": "interactive-out"
      }
    ],
    "final": "bulk-out"
  },
  "outbounds": [
    {
      "type": "direct",
      "tag": "interactive-out",
      "traffic_class": "interactive"
    },
    {
      "type": "direct",
      "tag": "bulk-out",
      "traffic_class": "bulk"
    }
  ]
}
```

分类可选 `default`、`interactive`、`realtime`、`bulk`。限速作用于受管理的用户态上传，不是下载限速。

## 负载均衡与故障重试

节点和代理组都放在 `outbounds`，由路由选择组。

```json
{
  "outbounds": [
    {
      "type": "socks",
      "tag": "node-a",
      "server": "127.0.0.1",
      "server_port": 1080,
      "version": "5"
    },
    {
      "type": "socks",
      "tag": "node-b",
      "server": "127.0.0.1",
      "server_port": 1081,
      "version": "5"
    },
    {
      "type": "loadbalance",
      "tag": "balanced",
      "outbounds": [
        "node-a",
        "node-b"
      ],
      "strategy": "round_robin",
      "url": "https://www.gstatic.com/generate_204",
      "expected_status": "204",
      "interval": "3m",
      "failover": true
    }
  ],
  "route": {
    "final": "balanced"
  }
}
```

替换示例 SOCKS 节点。strategy 还可选 `consistent_hashing` / `sticky_sessions`；failover 默认关闭，全流最多一次备用重试。

## URLTest 状态码限制

加入 `outbounds`；node-a / node-b 是已有节点。

```json
{
  "outbounds": [
    {
      "type": "urltest",
      "tag": "auto",
      "outbounds": [
        "node-a",
        "node-b"
      ],
      "url": "https://www.gstatic.com/generate_204",
      "expected_status": "204",
      "interval": "3m"
    }
  ]
}
```

`expected_status` 支持 Mihomo 风格的状态码表达式，例如 `204`、`200-299`、`200/204/301-399` 或 `200,204,301-399`；未设置时不限制 HTTP 响应码。`loadbalance` 的同名字段使用相同的写法。

## Fast Path 组策略

`fast_path` 写在 selector / urltest 上。

```json
{
  "outbounds": [
    {
      "type": "direct",
      "tag": "direct"
    },
    {
      "type": "selector",
      "tag": "direct-group",
      "outbounds": [
        "direct"
      ],
      "fast_path": false
    }
  ],
  "route": {
    "final": "direct-group"
  }
}
```

false 显式禁止；true 允许尝试但仍需逐流检查。实际隐式绕行需要 auto_redirect 等可执行 bypass 的入口，普通 TUN 不会因此绕行。

## 指定地址族策略

把 resolver 策略写在对应出站。

```json
{
  "dns": {
    "servers": [
      {
        "type": "udp",
        "tag": "dns-a",
        "server": "1.1.1.1"
      }
    ]
  },
  "outbounds": [
    {
      "type": "direct",
      "tag": "direct-v4",
      "domain_resolver": {
        "server": "dns-a",
        "strategy": "ipv4_only"
      }
    }
  ]
}
```

严格限制为 `ipv4_only` / `ipv6_only`；仅偏好使用 `prefer_ipv4` / `prefer_ipv6`。

## SOCKS 目标解析、预连接与复制调优

三个扩展都写在 SOCKS 出站对象内。

```json
{
  "dns": {
    "servers": [
      {
        "type": "udp",
        "tag": "dns-a",
        "server": "1.1.1.1"
      }
    ]
  },
  "outbounds": [
    {
      "type": "socks",
      "tag": "downstream",
      "server": "proxy.example.com",
      "server_port": 1080,
      "version": "5",
      "domain_resolver": "dns-a",
      "destination_dns_ownership": true,
      "tcp_preconnect": {
        "enabled": true,
        "min_idle": 2,
        "max_idle": 4,
        "idle_timeout": "20s"
      },
      "tcp_tuning": {
        "early_buffer_growth": true
      }
    }
  ]
}
```

domain_resolver 解析代理服务器；destination_dns_ownership 控制最终目标先由内核解析。预连接仅用于 SOCKS5 TCP CONNECT。

## XHTTP 与 XMUX

XHTTP 放在 VLESS 节点的 `transport`。

```json
{
  "outbounds": [
    {
      "type": "vless",
      "tag": "xhttp-out",
      "server": "proxy.example.com",
      "server_port": 443,
      "uuid": "00000000-0000-4000-8000-000000000000",
      "tls": {
        "enabled": true,
        "server_name": "proxy.example.com"
      },
      "transport": {
        "type": "xhttp",
        "host": "proxy.example.com",
        "path": "/xhttp",
        "mode": "packet-up",
        "x_padding_bytes": "100-1000",
        "sc_max_each_post_bytes": "1000000",
        "sc_min_posts_interval_ms": "30",
        "xmux": {
          "max_connections": "3",
          "h_max_request_times": "600-900",
          "h_max_reusable_secs": "1800-3000"
        }
      }
    }
  ]
}
```

需要 with_xhttp。模式可选 auto / packet-up / stream-up / stream-one；XMUX 设置任一字段后，其余未写范围按零 / 不限处理。

## XHTTP 会话布局与 padding

下面只替换节点的 `transport`，布局必须与服务端一致。

```json
{
  "transport": {
    "type": "xhttp",
    "path": "/xhttp",
    "mode": "packet-up",
    "session_placement": "query",
    "session_key": "x_session",
    "seq_placement": "query",
    "seq_key": "x_seq",
    "session_table": "Base62",
    "session_length": "16",
    "uplink_data_placement": "body",
    "uplink_http_method": "POST",
    "x_padding_bytes": "100-1000",
    "x_padding_obfs_mode": true,
    "x_padding_key": "x_padding",
    "x_padding_header": "X-Padding",
    "x_padding_placement": "queryInHeader",
    "x_padding_method": "repeat-x"
  }
}
```

这不是通用提速配置。不要把仅兼容接受的 server_max_header_bytes / sc_max_concurrent_posts 当作客户端生效参数。

## REALITY 混合密钥份额

`key_share` 放在节点的 `tls.reality`。

```json
{
  "outbounds": [
    {
      "type": "vless",
      "tag": "reality-out",
      "server": "proxy.example.com",
      "server_port": 443,
      "uuid": "00000000-0000-4000-8000-000000000000",
      "flow": "xtls-rprx-vision",
      "tls": {
        "enabled": true,
        "server_name": "example.com",
        "utls": {
          "enabled": true,
          "fingerprint": "chrome"
        },
        "reality": {
          "enabled": true,
          "public_key": "替换为服务端公钥",
          "short_id": "0123456789abcdef",
          "key_share": "hybrid"
        }
      }
    }
  ]
}
```

省略 key_share 沿用指纹；可选 classical / hybrid。公钥、short_id、SNI 和指纹须与服务端匹配。

## VLESS 加密

`encryption` 与 uuid、tls、transport 同级。

```json
{
  "outbounds": [
    {
      "type": "vless",
      "tag": "encrypted-vless",
      "server": "proxy.example.com",
      "server_port": 443,
      "uuid": "00000000-0000-4000-8000-000000000000",
      "encryption": "替换为服务端生成的完整客户端 encryption 参数串",
      "tls": {
        "enabled": true,
        "server_name": "proxy.example.com"
      }
    }
  ]
}
```

直接使用服务端生成的完整参数，不是布尔开关；空值或 none 关闭此层。

## Mieru TCP → UDP 回退

`fallback` 放在 Mieru 出站内。

```json
{
  "outbounds": [
    {
      "type": "mieru",
      "tag": "mieru-out",
      "server": "proxy.example.com",
      "server_port": 8443,
      "username": "user",
      "password": "替换为密码",
      "transport": "tcp",
      "multiplexing": "middle",
      "handshake_mode": "standard",
      "fallback": {
        "enabled": true,
        "transport": "udp",
        "server_port": 8444
      }
    }
  ]
}
```

服务端须监听对应 TCP / UDP 端口；主备不能使用 both 或 no_wait，切换偏好只影响新会话。

## Naive H3 回退与单引擎并发

这些字段都放在 Naive 出站内。

```json
{
  "outbounds": [
    {
      "type": "naive",
      "tag": "naive-out",
      "server": "proxy.example.com",
      "server_port": 443,
      "username": "user",
      "password": "替换为密码",
      "quic": true,
      "quic_fallback_to_h2": true,
      "insecure_concurrency": 2,
      "insecure_concurrency_single_engine": true,
      "tls": {
        "enabled": true,
        "server_name": "proxy.example.com"
      }
    }
  ]
}
```

回退要求 quic: true 与平台 Cronet 支持；并发数仅为示例，不代表越大越快。

## Naive 服务端资源限制

HTTP/2 参数在入站本层，连接限制在 `server_limits`。

```json
{
  "inbounds": [
    {
      "type": "naive",
      "tag": "naive-in",
      "listen": "::",
      "listen_port": 443,
      "network": "tcp",
      "users": [
        {
          "username": "user",
          "password": "替换为密码"
        }
      ],
      "stream_receive_window": "1 MiB",
      "connection_receive_window": "2 MiB",
      "max_concurrent_streams": 128,
      "idle_timeout": "1m",
      "server_limits": {
        "max_connections": 512,
        "max_connections_per_ip": 16,
        "header_timeout": "10s",
        "max_tracked_ips": 4096
      },
      "tls": {
        "enabled": true,
        "certificate_path": "./server.crt",
        "key_path": "./server.key",
        "handshake_timeout": "10s"
      }
    }
  ]
}
```

替换证书路径。总连接限制包含已认证连接，header_timeout 不用于已建立隧道。

## HTTP H2/H3 服务端与认证前限制

放在 HTTP 入站中，参数无需包进 http2 / http3 子对象。

```json
{
  "inbounds": [
    {
      "type": "http",
      "tag": "http-in",
      "listen": "::",
      "listen_port": 443,
      "version": [
        2,
        3
      ],
      "users": [
        {
          "username": "user",
          "password": "替换为密码"
        }
      ],
      "max_header_bytes": 65536,
      "stream_receive_window": "1 MiB",
      "connection_receive_window": "2 MiB",
      "max_concurrent_streams": 128,
      "bbr_profile": "standard",
      "unauthenticated_limits": {
        "enabled": true,
        "max_concurrent_per_ip": 8,
        "requests_per_second": 10,
        "burst": 20,
        "idle_timeout": "10s",
        "max_tracked_ips": 4096
      },
      "tls": {
        "enabled": true,
        "certificate_path": "./server.crt",
        "key_path": "./server.key"
      }
    }
  ]
}
```

需要相应 QUIC 构建及服务端端口。unauthenticated_limits 只限制认证前路径；bbr_profile 是服务端选项。

## MASQUE 内外两套 DNS

客户端写在 `endpoints`，类型为 `masque-client`。

```json
{
  "dns": {
    "servers": [
      {
        "type": "udp",
        "tag": "bootstrap",
        "server": "1.1.1.1"
      },
      {
        "type": "udp",
        "tag": "inner-dns",
        "server": "8.8.8.8"
      }
    ]
  },
  "endpoints": [
    {
      "type": "masque-client",
      "tag": "masque",
      "server": "proxy.example.com",
      "server_port": 443,
      "username": "user",
      "password": "替换为密码",
      "domain_resolver": "bootstrap",
      "inner_domain_resolver": "inner-dns",
      "version": 3,
      "disable_version_fallback": false,
      "mtu": 1280,
      "quic_congestion_control": "cubic",
      "tls": {
        "enabled": true,
        "server_name": "proxy.example.com"
      }
    }
  ]
}
```

Bootstrap 解析服务器，inner_domain_resolver 解析隧道内目标；示例的 inner-dns 仍按你的 DNS 路由访问，不会因名字自动变成隧道内 DNS。

## 共享 HTTP client 与规则集下载

先定义 `http_clients`，再由 `http_client` 引用。

```json
{
  "http_clients": [
    {
      "tag": "rule-downloader",
      "engine": "go",
      "version": 2,
      "disable_version_fallback": false,
      "stream_receive_window": "1 MiB",
      "connection_receive_window": "2 MiB"
    }
  ],
  "route": {
    "rule_set": [
      {
        "type": "remote",
        "tag": "remote-rules",
        "format": "binary",
        "url": "https://example.com/rules.srs",
        "http_client": "rule-downloader",
        "update_interval": "1d"
      }
    ]
  }
}
```

替换规则集 URL。只定义 client，不引用它，不会改变下载行为。

## 内存管理服务

放在根配置的 `services`，先确认客户端未自动注入同一服务。

```json
{
  "services": [
    {
      "type": "oom-killer",
      "tag": "memory-guard",
      "memory_limit": "128 MiB",
      "safety_margin": "8 MiB",
      "min_interval": "100ms",
      "max_interval": "10s"
    }
  ]
}
```

数值仅示范。Network Extension 使用平台安全策略；不要照抄为所有设备的预算。

## NXDOMAIN 自动负缓存

使用已有 DNS 缓存设置，没有单独的 NXDOMAIN 开关。

```json
{
  "dns": {
    "disable_cache": false,
    "disable_expire": false,
    "cache_capacity": 4096
  }
}
```

满足 SOA 与负 TTL 条件的 NXDOMAIN 自动跨查询类型复用；[详细行为](DNS-NXDOMAIN.md)。

更多参数见 [配置参考](configuration)、[NXDOMAIN 说明](DNS-NXDOMAIN.md) 和 [Fast Path / 内存设计](FAST-PATH-AND-MEMORY.md)。合并后使用目标内核执行 `sing-box check -c config.json`。
