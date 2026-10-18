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
