---
icon: material/new-box
---

### Structure

```json
{
  "type": "mieru",
  "tag": "mieru-in",

  "listen": "::",
  "listen_port": 8964,
  "listen_port_ranges": ["8965-8999"],
  "transport": "both",
  "users": [
    {
      "name": "alice",
      "password": "password"
    }
  ],
  "mtu": 1400,
  "traffic_pattern": "",
  "user_hint_is_mandatory": false
}
```

### Fields

#### listen

Listen address. Default: `127.0.0.1`.

#### listen_port

The primary port to listen on. Either this or `listen_port_ranges` must be set.

#### listen_port_ranges

Additional ports or ranges to listen on, for example `["8965-8999"]`.

Each entry is expanded by the Mieru SDK; every port gets one socket per
selected transport. A port that fails to bind fails the whole inbound at
startup instead of leaving a partially listening server.

#### transport

The underlay protocol: `tcp`, `udp`, or `both`. Default: `tcp`.

`both` serves both protocols on every configured port from one inbound. This is
equivalent to running two inbounds with the same ports (one `tcp`, one `udp`),
and both forms are supported.

#### users

==Required==

The user list. Each entry has a `name` and exactly one of `password` or
`hashed_password`.

`hashed_password` is the 64-character hexadecimal SHA-256 form stored by
`mita`, which allows migrating an existing server configuration without
putting the plaintext back on disk.

Duplicate user names and empty credentials are rejected at startup.

#### mtu

The L2 payload size used by Mieru's UDP transport. Valid range: `1280` to
`1500`. Default: the SDK default (1280).

#### traffic_pattern

The base64-encoded traffic pattern produced by `mieru export traffic-pattern`
or `mita describe config`. When set, it must be the pattern the clients use.

#### user_hint_is_mandatory

Require every client to present a user-name hint matching a configured user
before a session is authenticated. Default: `false`.

### Notes

Mieru has no TLS layer: the protocol's own encryption and traffic pattern are
the transport. Do not put a TLS inbound in front of it and expect official
clients to interoperate.

The inbound honours the full listen options (`bind_interface`, `routing_mark`,
`reuse_addr`, `netns`, TCP keepalive / fast open / multipath, UDP fragment):
Mieru's sockets are created through sing-box's listener factories, not by the
SDK's default `net.ListenConfig`.
