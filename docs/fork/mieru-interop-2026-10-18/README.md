# Mieru interop run — 2026-10-18

Raw logs from the two-directional interop run described in
[`../mieru-integration.md`](../mieru-integration.md).

Environment: Windows 11, loopback only, `sing-box.exe` built from this tree with
`release/DEFAULT_BUILD_TAGS_OTHERS`, official Mieru `v3.38.1`.

Official client artifact:
`mieru_3.38.1_windows_amd64.zip`, SHA-256
`45d459257323f8e012b163217ca7810ee7cfe5b0eb7cb76e37bb05038a80badf`.

The official `mita` server ships Linux-only, so the reverse direction used the
official `github.com/enfein/mieru/v3/apis/server` entry point (the same API
`mita` drives) compiled from the pinned module. The peer program is a minimal
SOCKS5 request handler: CONNECT is relayed with `io.Copy`, UDP ASSOCIATE relays
the per-datagram SOCKS5 UDP header to a local UDP socket.

Test credentials and ports are throwaway loopback values; they are not
credentials for any deployment.

## Probe

The probe (`tester.exe`, stdlib only) starts one TCP echo server and one UDP
echo server, then drives both through a SOCKS5 proxy. Both directions used the
same probe, so the result lines are directly comparable.

## Results

| Log pair | Direction | Client config | Result |
| --- | --- | --- | --- |
| `client-high.log` + `server-high.log` | official `mieru.exe` → this inbound | TCP + UDP bindings, `MULTIPLEXING_HIGH`, default handshake | `PASS tcp`, `PASS udp` |
| `client-udp-nowait.log` + `server.log` | official `mieru.exe` → this inbound | UDP-only binding, `MULTIPLEXING_OFF`, `HANDSHAKE_NO_WAIT` | `PASS tcp`, `PASS udp` |
| `our-client.log` + `official-server.log` | this outbound → official server | `transport: both`, `multiplexing: high` | `PASS tcp`, `PASS udp` |
| `our-client-udp.log` + `official-server.log` | this outbound → official server | `transport: udp`, `handshake_mode: no_wait`, `multiplexing: off` | `PASS tcp`, `PASS udp` |
| `client-bad-auth.log` + `server.log` | official `mieru.exe` with a wrong password → this inbound | TCP binding, wrong password | expected failure: `FAIL tcp: EOF`, `FAIL udp: EOF`; the good client still passed immediately afterwards |

The bad-auth run is a negative control. The SDK drops an undecryptable underlay
inside the Mux, so `server.Accept()` never sees an authentication error; the
peer sees EOF. The inbound stayed healthy and the valid client's next TCP and
UDP runs passed, which is the property that matters (no hot loop, no wedged
listener).

The sing-box logs contain the expected startup line `mieru tcp server started`
and `mieru udp server started` for the inbound, and
`mieru client restarted after network change` for the outbound (the initial
network observation runs `InterfaceUpdated` once, before any traffic).

## Configs used

Our inbound:

```json
{
  "inbounds": [{
    "type": "mieru", "tag": "mieru-in", "listen": "127.0.0.1",
    "listen_port": 18664, "transport": "both",
    "users": [{"name": "baozi", "password": "manlianpenfen"}]
  }],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {},
  "log": {"level": "debug"}
}
```

Our outbound (each transport/handshake combination changes only the marked
fields):

```json
{
  "inbounds": [{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 11083}],
  "outbounds": [{
    "type": "mieru", "tag": "mieru-out", "server": "127.0.0.1", "server_port": 18665,
    "username": "baozi", "password": "manlianpenfen",
    "transport": "both", "multiplexing": "high"
  }, {"type": "direct", "tag": "direct"}],
  "route": {"rules": [{"inbound": ["mixed-in"], "action": "route", "outbound": "mieru-out"}]},
  "log": {"level": "debug"}
}
```

The official client config is the same profile shape as `client.log`'s
predecessor: `profiles[0].servers[0].portBindings` carries the protocol and
port; `multiplexing.level` and `handshakeMode` are the only knobs that change
between rows.
