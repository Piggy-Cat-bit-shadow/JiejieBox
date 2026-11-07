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

#### use_all_providers

Include every configured provider. The set is resolved at start.

#### include

Regexp: only provider nodes whose rendered tag matches are included.

#### exclude

Regexp: provider nodes whose rendered tag matches are skipped. `exclude` wins over `include`.

#### default

The default outbound tag. The first outbound will be used if empty.

#### interrupt_exist_connections

Interrupt existing connections when the selected outbound has changed.

Only inbound connections are affected by this setting, internal connections will always be interrupted.
