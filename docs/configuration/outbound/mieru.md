---
icon: material/new-box
---

### Structure

```json
{
  "type": "mieru",
  "tag": "mieru-out",

  "server": "127.0.0.1",
  "server_port": 8964,
  "server_ports": ["8965-8999"],
  "transport": "tcp",
  "username": "alice",
  "password": "password",
  "multiplexing": "low",
  "handshake_mode": "default",
  "mtu": 1400,
  "traffic_pattern": "",

  "fallback": {
    "enabled": true,
    "transport": "udp",
    "server": "udp.example.com",
    "server_port": 8965
  },

  ... // Dial Fields
}
```

### Fields

#### server

==Required==

The server address.

#### server_port

The primary server port. Either this or `server_ports` must be set.

#### server_ports

Additional ports or ranges to try on the same server, for example
`["8965-8999"]`.

#### transport

The underlay protocol: `tcp`, `udp`, or `both`. Default: `tcp`.

The value is case-insensitive: the official downstream integration's `TCP` and
`UDP` spellings are accepted as well as the lowercase forms.

`both` registers every configured port on both protocols and lets Mieru's
scheduler choose. `udp` uses Mieru's packet underlay; `tcp` carries everything,
including UDP associations, as a stream.

#### fallback

Optional failover from one underlay transport to the other for **new
connections** of this same outbound. Disabled by default. The legacy
`transport: "tcp"`, `"udp"`, `"both"` and `handshake_mode: "no_wait"` behavior
is unchanged when `fallback` is absent or `enabled` is `false`.

```json
{
  "type": "mieru",
  "tag": "mieru-out",
  "server": "example.com",
  "server_port": 8964,
  "transport": "tcp",
  "username": "alice",
  "password": "password",
  "fallback": {
    "enabled": true,
    "transport": "udp"
  }
}
```

`fallback.transport` is required when enabled and must be the transport the
primary does not use. `fallback.server` and `fallback.server_port` inherit the
primary endpoint when omitted. The server must listen on the corresponding
transport and port; it does not need to understand `fallback`.

Only new sessions are selected. A normal failure/fallback does not stop either
SDK client and does not migrate an established session. A preference switch is
driven by a protocol handshake or a transport-class dial failure; a SOCKS5
target error is not treated as a reason to switch. A silent handshake that
could be a credential/configuration failure (bare EOF, or both transports
failing silently) does not publish a durable secondary preference.

Restrictions in this first version:

- `transport: "both"` with `fallback.enabled: true` is rejected; use an explicit
  TCP or UDP primary.
- `handshake_mode: "no_wait"` with `fallback.enabled: true` is rejected; the
  deferred EarlyConn handshake cannot prove the transport works before user
  data is sent.
- The backup cannot have separate credentials, multiplexing, MTU or traffic
  pattern; both endpoints share the primary's protocol settings.
- A primary configured only with `server_ports` must set
  `fallback.server_port` explicitly; an ambiguous range is not silently
  collapsed to its first port.
- This is a fork extension. Older kernels do not understand `fallback`; server
  configuration is unchanged.

#### username

==Required==

The user name.

#### password

The plaintext password. Exactly one of `password` and `hashed_password` must be
set.

#### hashed_password

The 64-character hexadecimal SHA-256 form stored by `mita`/`mieru`.

#### multiplexing

Mieru's own multiplexer level: `default`, `off`, `low`, `middle`, or `high`.
Default: the SDK default (equivalent to `low`).

This is **Mieru's** multiplexer, not sing-box's. Do not enable a sing-box
multiplex on top of this outbound: the traffic would be multiplexed twice, with
two independent flow-control and buffering layers.

The official SDK enum spellings (`MULTIPLEXING_OFF`, `MULTIPLEXING_LOW`,
`MULTIPLEXING_MIDDLE`, `MULTIPLEXING_HIGH`) are accepted as aliases.

#### handshake_mode

`default`, `standard`, or `no_wait`. Default: `default` (standard).

`no_wait` sends the proxy request together with the first application write.
It is not zero-RTT: the peer response is still awaited before the first read.

The official SDK enum spellings (`HANDSHAKE_STANDARD`, `HANDSHAKE_NO_WAIT`) are
accepted as aliases.

#### mtu

The L2 payload size used by Mieru's UDP transport. Valid range: `1280` to
`1500`. Default: the SDK default (1280).

#### traffic_pattern

The base64-encoded traffic pattern produced by `mieru export traffic-pattern`.
When set, it must match the server.

### Dial Fields

See [Dial Fields](/configuration/shared/dial/) for details.

The server name is resolved by the fork's dialer, using `domain_resolver`,
`route.default_domain_resolver`, network strategy and the dual-stack policy.
It is not resolved a second time inside the SDK.

### 中文要点

- 默认关闭；不写 `fallback` 或 `enabled: false` 时，旧 TCP/UDP/`both`、`no_wait`
  的语义和资源数量不变。
- 启用后同一个 `tag` 内主/备分别为 TCP 与 UDP；服务端只需同时监听对应传输和端口，
  不需要认识 `fallback` 字段。
- 备用 `server` / `server_port` 省略时继承主端点；主配置只有 `server_ports`
  时，必须显式写备用端口，避免把端口范围静默缩成一个端口。
- 只有新连接会切换；普通回退不会 Stop 主/备客户端，也不会迁移已有会话或重放已发送
  数据。SOCKS5 目标错误不等于传输故障，不会触发主链路熔断。
- 首版拒绝 `transport: "both"` 与 `handshake_mode: "no_wait"` 的组合；备用与主用
  共享凭据、Mux、MTU 和 Traffic Pattern。
