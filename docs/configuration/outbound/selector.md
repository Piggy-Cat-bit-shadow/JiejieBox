### Structure

```json
{
  "type": "selector",
  "tag": "select",
  
  "outbounds": [
    "proxy-a",
    "proxy-b",
    "proxy-c"
  ],
  "providers": ["airport"],
  "use_all_providers": false,
  "include": "",
  "exclude": "",
  "default": "proxy-c",
  "interrupt_exist_connections": false
}
```

!!! quote ""

    The selector can only be controlled through the [Clash API](/configuration/experimental#clash-api-fields) currently.

### Fields

#### outbounds

==Required==

List of outbound tags to select. It may be empty when `providers` or `use_all_providers` is set.

#### providers

List of outbound provider tags whose nodes join this selector. See [Provider](/configuration/provider/).

A provider-only selector may start empty while the first subscription download is pending, including
when another outbound uses it as a `detour`. Dials during that wait return an error naming the
provider. The download path must not depend on the same empty group; see the
[remote subscription and proxy chain example](../../FORK-CONFIGURATION.md#远程订阅与链式代理).

#### use_all_providers

When true, use every provider configured at startup in place of the explicit `providers` list.

#### include

Regexp: only provider nodes whose rendered tag matches are included.

#### exclude

Regexp: provider nodes whose rendered tag matches are skipped. `exclude` wins over `include`. Neither filter affects static `outbounds`.

#### default

The default outbound tag. Use `provider/node` for a published provider member. With no usable saved
selection, an explicit default must exist. For a provider-only group that starts empty, omit
the default or add a static member as the default.

#### interrupt_exist_connections

Interrupt existing connections when the selected outbound has changed.

Only inbound connections are affected by this setting, internal connections will always be interrupted.
