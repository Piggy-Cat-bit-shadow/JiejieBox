## AmneziaWG extension

This fork adds AWG 2.0, 3.x, and 3.1 options at the **root of the existing `type: "wireguard"` endpoint**. There is no separate `amneziawg` type. The endpoint keeps WireGuard addressing, peers, routing, and device lifecycle. Build with `with_awg`; a build without it explicitly rejects an endpoint using AWG fields. Server-side magic headers, obfuscation, and header protection must match the peer.

Add these fields to a WireGuard endpoint and replace the values with your server's parameters:

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

| Fields | Meaning |
| --- | --- |
| `jc`, `jmin`, `jmax` | Junk packet count and minimum/maximum size before handshake. |
| `s1`–`s4` | Prefixes on the four message classes; each must be at least 12 bytes with `header_protection_key`. |
| `h1`–`h4` | Magic headers, as uint32 numbers or inclusive `"N-M"` strings. |
| `i1`–`i5` | Ordered, case-sensitive CPS instruction strings. |
| `id`, `ip`, `ib` | Declarative masquerade domain, protocol (`quic`, `dns`, `stun`, `sip`), and browser (`chrome`, `firefox`, `curl`); generates `i1`, so do not specify explicit `i1` as well. |
| `header_protection_key` | Base64 encoded 32-byte header protection key, matching the server. |
| `content_padding_addition` | Additional bytes of encrypted data padding. |
| `rekey_after_time`, `rekey_timeout`, `reject_after_time`, `keepalive_timeout` | Timing overrides in seconds. |
| `max_handshake_attempts` | Handshake attempt count override. |
| `random_trailers`, `disable_cookies` | AWG 3.1 random trailers and cookie behavior. |

The padding, timing/count, and `h1`–`h4` fields accept a uint32 number or an inclusive `"N-M"` range; `0` leaves them unset. Use the actual type documented below for `peers.persistent_keepalive_interval`, rather than copying a range string. See [fork examples](../../FORK-CONFIGURATION.md#amneziawg-endpoint) and [build profiles](../../BUILD-PROFILES.md).

!!! quote "Changes in sing-box 1.15.0"

    :material-plus: [on_demand](#on_demand)

!!! quote "Changes in sing-box 1.14.0"

    :material-plus: [udp_mapping](/configuration/shared/udp-nat/#udp_mapping)  
    :material-plus: [udp_filtering](/configuration/shared/udp-nat/#udp_filtering)  
    :material-plus: [udp_nat_max](/configuration/shared/udp-nat/#udp_nat_max)

!!! question "Since sing-box 1.11.0"

### Structure

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

  ... // UDP NAT Fields

  "workers": 0,
  "on_demand": false,
 
  ... // Dial Fields
}
```

!!! note ""

    You can ignore the JSON Array [] tag when the content is only one item

### Fields

#### system

Use system interface.

Requires privilege and cannot conflict with exists system interfaces.

#### name

Custom interface name for system interface.

#### mtu

WireGuard MTU.

`1408` will be used by default.

#### address

==Required==

List of IP (v4 or v6) address prefixes to be assigned to the interface.

#### private_key

==Required==

WireGuard requires base64-encoded public and private keys. These can be generated using the wg(8) utility:

```shell
wg genkey
echo "private key" || wg pubkey
```

or `sing-box generate wg-keypair`.

#### peers

==Required==

List of WireGuard peers.

#### peers.address

WireGuard peer address.

#### peers.port

WireGuard peer port.

#### peers.public_key

==Required==

WireGuard peer public key.

#### peers.pre_shared_key

WireGuard peer pre-shared key.

#### peers.allowed_ips

==Required==

WireGuard allowed IPs.

#### peers.persistent_keepalive_interval

WireGuard persistent keepalive interval, in seconds.

Disabled by default.

#### peers.reserved

WireGuard reserved field bytes.

#### workers

WireGuard worker count.

CPU count is used by default.

#### on_demand

!!! question "Since sing-box 1.15.0"

Allow the endpoint to be disconnected when necessary.

### UDP NAT Fields

See [UDP NAT Fields](/configuration/shared/udp-nat/) for details.

### Dial Fields

See [Dial Fields](/configuration/shared/dial/) for details.
