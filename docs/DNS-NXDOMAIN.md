# NXDOMAIN 与本地 DNS 拦截

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
