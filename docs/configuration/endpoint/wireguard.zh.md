## AmneziaWG 扩展

本项目在现有 `type: "wireguard"` **endpoint 根层**增加 AWG 2.0、3.x 和 3.1 字段；没有独立的 `amneziawg` 类型。这些字段沿用 WireGuard 的 `address`、`private_key`、`peers`、路由与设备生命周期。构建需含 `with_awg`；未带该标签的内核遇到 AWG 字段会明确报错。服务端相关魔数、混淆和头保护参数必须与对端一致。

以下片段加入现有 `wireguard` endpoint 对象，数值需以实际服务端参数替换：

```json
"jc": 4,
"jmin": 40,
"jmax": 80,
"s1": 16,
"s2": 16,
"s3": 16,
"s4": 16,
"h1": "1000000000-1000000010",
"h2": 2000000000,
"h3": 3000000000,
"h4": 4000000000,
"content_padding_addition": "0-16",
"random_trailers": true
```

| 字段 | 含义 |
| --- | --- |
| `jc`, `jmin`, `jmax` | 握手前 junk 包数量及大小下、上限。 |
| `s1`–`s4` | 四类消息的附加前缀。启用 `header_protection_key` 时均需至少 12 字节。 |
| `h1`–`h4` | 消息魔数；接受 uint32 数字或包含上下限的字符串 `"N-M"`。 |
| `i1`–`i5` | 有顺序且区分大小写的 CPS 指令字符串。 |
| `id`, `ip`, `ib` | 声明式伪装：域名、协议（`quic`、`dns`、`stun`、`sip`）及浏览器（`chrome`、`firefox`、`curl`）；生成 `i1`，不可同时显式填写 `i1`。 |
| `header_protection_key` | base64 编码的 32 字节头保护密钥，须与服务端一致。 |
| `content_padding_addition` | 数据包加密载荷中的额外填充字节数。 |
| `rekey_after_time`, `rekey_timeout`, `reject_after_time`, `keepalive_timeout` | 以秒为单位的时间参数覆盖。 |
| `max_handshake_attempts` | 握手尝试次数覆盖。 |
| `random_trailers`, `disable_cookies` | AWG 3.1 随机尾部与 cookie 处理开关。 |

`content_padding_addition`、上述时间/次数参数和 `h1`–`h4` 可用 uint32 数字或 `"N-M"` 范围；`0` 表示不设置。`peers.persistent_keepalive_interval` 请仍按下文 WireGuard 字段的实际类型填写，不要照搬范围字符串。完整示例与构建说明见[扩展配置](../../FORK-CONFIGURATION.md#amneziawg-endpoint)和[构建档位](../../BUILD-PROFILES.md)。

!!! quote "sing-box 1.15.0 中的更改"

    :material-plus: [on_demand](#on_demand)

!!! quote "sing-box 1.14.0 中的更改"

    :material-plus: [udp_mapping](/zh/configuration/shared/udp-nat/#udp_mapping)  
    :material-plus: [udp_filtering](/zh/configuration/shared/udp-nat/#udp_filtering)  
    :material-plus: [udp_nat_max](/zh/configuration/shared/udp-nat/#udp_nat_max)

!!! question "自 sing-box 1.11.0 起"

### 结构

```json
{
  "type": "wireguard",
  "tag": "wg-ep",

  "system": false,
  "name": "",
  "mtu": 1408,
  "address": [],
  "private_key": "",
  "listen_port": 10000,
  "peers": [
    {
      "address": "127.0.0.1",
      "port": 10001,
      "public_key": "",
      "pre_shared_key": "",
      "allowed_ips": [],
      "persistent_keepalive_interval": 0,
      "reserved": [0, 0, 0]
    }
  ],

  ... // UDP NAT 字段

  "workers": 0,
  "on_demand": false,

  ... // 拨号字段
}
```

!!! note ""

    当内容只有一项时，可以忽略 JSON 数组 [] 标签

### 字段

#### system

使用系统设备。

需要特权且不能与已有系统接口冲突。

#### name

为系统接口自定义设备名称。

#### mtu

WireGuard MTU。

默认使用 1408。

#### address

==必填==

接口的 IPv4/IPv6 地址或地址段的列表。

要分配给接口的 IP（v4 或 v6）地址段列表。

#### private_key

==必填==

WireGuard 需要 base64 编码的公钥和私钥。 这些可以使用 wg(8) 实用程序生成：

```shell
wg genkey
echo "private key" || wg pubkey
```

或 `sing-box generate wg-keypair`.

#### peers

==必填==

WireGuard 对等方的列表。

#### peers.address

对等方的 IP 地址。

#### peers.port

对等方的 WireGuard 端口。

#### peers.public_key

==必填==

对等方的 WireGuard 公钥。

#### peers.pre_shared_key

对等方的预共享密钥。

#### peers.allowed_ips

==必填==

对等方的允许 IP 地址。

#### peers.persistent_keepalive_interval

对等方的持久性保持活动间隔，以秒为单位。

默认禁用。

#### peers.reserved

对等方的保留字段字节。

#### workers

WireGuard worker 数量。

默认使用 CPU 数量。

#### on_demand

!!! question "自 sing-box 1.15.0 起"

允许该 endpoint 在需要时断开连接。

### UDP NAT 字段

参阅 [UDP NAT 字段](/zh/configuration/shared/udp-nat/)。

### 拨号字段

参阅 [拨号字段](/zh/configuration/shared/dial/)。
