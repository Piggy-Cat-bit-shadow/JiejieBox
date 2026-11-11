# Round 1 主仓 108 冲突逐文件裁决矩阵

生成依据：`303fbb47dfd`（merge 双父：`48103716845e` + `a13dbe1f785f`）的最终树与两个父提交逐文件比较；冲突路径集来自 `audit-evidence/upstream/main-merge-conflicts.txt`。生成物、merge 后修复和上游意图用对应 commit/测试补充。

## 统计

- 文件数：**108 / 108**，无重复、无遗漏
- 分类：FORK 81；GENERATED 1；OBSOLETE 2；SAME 1；SEMANTIC_FUSION 20；UPSTREAM 3
- 复核状态：RECONSTRUCTED 70；VERIFIED 38

分类含义：`FORK` 最终树等于 fork 父提交；`UPSTREAM` 等于上游父提交；`SEMANTIC_FUSION` 与两边都不同；`GENERATED` 生成物重新生成；`OBSOLETE` 路径被有意移除；`SAME` 两边已相同。

## 全部 108 个文件

| path | conflict | resolution | review |
|---|---|---|---|
| `.github/workflows/build.yml` | delete/modify | OBSOLETE | VERIFIED |
| `adapter/host.go` | add/add | FORK | VERIFIED |
| `adapter/network.go` | modify/modify | FORK | VERIFIED |
| `adapter/router.go` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `cmd/internal/build_libbox/main.go` | modify/modify | FORK | VERIFIED |
| `common/badhttp/badhttp.go` | add/add | FORK | VERIFIED |
| `common/dialer/default.go` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `common/dialer/default_parallel_interface.go` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `common/dialer/default_parallel_network.go` | modify/modify | FORK | VERIFIED |
| `common/dialer/resolve.go` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `common/httpclient/http3_transport.go` | modify/modify | FORK | VERIFIED |
| `common/listener/listener_tcp.go` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `common/listener/listener_udp.go` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `common/process/searcher_linux.go` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `common/sniff/http.go` | modify/modify | FORK | VERIFIED |
| `common/srs/binary.go` | modify/modify | FORK | VERIFIED |
| `common/urltest/urltest.go` | modify/modify | FORK | VERIFIED |
| `constant/timeout.go` | modify/modify | FORK | VERIFIED |
| `daemon/instance.go` | modify/modify | FORK | VERIFIED |
| `dns/router.go` | modify/modify | FORK | VERIFIED |
| `dns/transport/dhcp/dhcp.go` | modify/modify | FORK | VERIFIED |
| `dns/transport/fakeip/store.go` | modify/modify | FORK | VERIFIED |
| `dns/transport/local/local.go` | modify/modify | UPSTREAM | VERIFIED |
| `dns/transport/local/local_resolved_linux.go` | modify/modify | FORK | VERIFIED |
| `docs/changelog.md` | modify/modify | SEMANTIC_FUSION | RECONSTRUCTED |
| `docs/configuration/inbound/http.md` | modify/modify | FORK | VERIFIED |
| `docs/schema.json` | modify/modify | GENERATED | VERIFIED |
| `experimental/cachefile/fakeip.go` | modify/modify | FORK | VERIFIED |
| `experimental/cachefile/pending.go` | add/add | FORK | VERIFIED |
| `experimental/libbox/redirect.go` | add/add | FORK | VERIFIED |
| `experimental/libbox/redirect_linux.go` | add/add | FORK | VERIFIED |
| `experimental/libbox/setup.go` | modify/modify | FORK | VERIFIED |
| `go.mod` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `go.sum` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `include/registry.go` | modify/modify | FORK | VERIFIED |
| `option/http.go` | modify/modify | FORK | VERIFIED |
| `option/masque.go` | add/add | FORK | VERIFIED |
| `option/simple.go` | modify/modify | FORK | VERIFIED |
| `option/wireguard.go` | modify/modify | FORK | VERIFIED |
| `protocol/anytls/inbound.go` | modify/modify | FORK | VERIFIED |
| `protocol/anytls/outbound.go` | modify/modify | FORK | VERIFIED |
| `protocol/bridge/backend_darwin.go` | modify/modify | FORK | VERIFIED |
| `protocol/bridge/backend_linux.go` | modify/modify | FORK | VERIFIED |
| `protocol/bridge/netfilter_linux.go` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `protocol/direct/outbound.go` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `protocol/http/inbound.go` | modify/modify | FORK | VERIFIED |
| `protocol/http/outbound.go` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `protocol/masque/client.go` | add/add | FORK | VERIFIED |
| `protocol/masque/endpoint.go` | add/add | FORK | VERIFIED |
| `protocol/masque/server.go` | add/add | FORK | VERIFIED |
| `protocol/mixed/inbound.go` | modify/modify | FORK | VERIFIED |
| `protocol/naive/inbound.go` | modify/modify | FORK | VERIFIED |
| `protocol/openconnect/client.go` | modify/modify | FORK | VERIFIED |
| `protocol/openconnect/endpoint.go` | modify/modify | FORK | VERIFIED |
| `protocol/openvpn/client.go` | modify/modify | FORK | VERIFIED |
| `protocol/openvpn/dns_transport.go` | modify/modify | FORK | VERIFIED |
| `protocol/openvpn/endpoint.go` | modify/modify | FORK | VERIFIED |
| `protocol/openvpn/server.go` | modify/modify | FORK | VERIFIED |
| `protocol/tailscale/dns_transport.go` | modify/modify | FORK | VERIFIED |
| `protocol/tailscale/endpoint.go` | modify/modify | SEMANTIC_FUSION | RECONSTRUCTED |
| `protocol/tailscale/system_binding.go` | add/add | FORK | VERIFIED |
| `protocol/tailscale/tailcat_inbound.go` | add/add | FORK | VERIFIED |
| `protocol/tailscale/tailcat_node.go` | add/add | FORK | VERIFIED |
| `protocol/tailscale/tailcat_outbound.go` | add/add | FORK | VERIFIED |
| `protocol/tun/inbound.go` | modify/modify | SEMANTIC_FUSION | RECONSTRUCTED |
| `protocol/tun/redirect_platform_linux.go` | add/add | FORK | VERIFIED |
| `protocol/wireguard/endpoint.go` | modify/modify | FORK | VERIFIED |
| `release/DEFAULT_BUILD_TAGS` | modify/modify | FORK | VERIFIED |
| `release/DEFAULT_BUILD_TAGS_OTHERS` | modify/modify | FORK | VERIFIED |
| `release/DEFAULT_BUILD_TAGS_WINDOWS` | modify/modify | FORK | VERIFIED |
| `route/conn.go` | modify/modify | FORK | VERIFIED |
| `route/flow_tracker.go` | modify/modify | FORK | VERIFIED |
| `route/network.go` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `route/reference.go` | modify/modify | FORK | VERIFIED |
| `route/route.go` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `route/rule/rule_set_remote.go` | modify/modify | FORK | VERIFIED |
| `route/splice.go` | add/add | FORK | VERIFIED |
| `service/oomkiller/service.go` | modify/modify | FORK | VERIFIED |
| `service/oomkiller/timer.go` | modify/modify | FORK | VERIFIED |
| `service/oomkiller/timer_darwin.go` | delete/modify | OBSOLETE | VERIFIED |
| `service/resolved/resolve1.go` | modify/modify | UPSTREAM | VERIFIED |
| `test/go.mod` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `test/go.sum` | modify/modify | SEMANTIC_FUSION | VERIFIED |
| `test/http_forward_test.go` | add/add | FORK | VERIFIED |
| `transport/device/stack.go` | add/add | FORK | VERIFIED |
| `transport/device/system.go` | add/add | FORK | VERIFIED |
| `transport/device/system_stack.go` | add/add | SAME | VERIFIED |
| `transport/http/capsule.go` | add/add | FORK | VERIFIED |
| `transport/http/client.go` | add/add | SEMANTIC_FUSION | VERIFIED |
| `transport/http/client_h2.go` | add/add | FORK | VERIFIED |
| `transport/http/client_h3.go` | add/add | FORK | VERIFIED |
| `transport/http/connect_udp.go` | add/add | FORK | VERIFIED |
| `transport/http/forward.go` | add/add | UPSTREAM | VERIFIED |
| `transport/http/reader.go` | add/add | FORK | VERIFIED |
| `transport/http/server.go` | add/add | FORK | VERIFIED |
| `transport/http/server_conn.go` | add/add | FORK | VERIFIED |
| `transport/http/server_h2.go` | add/add | FORK | VERIFIED |
| `transport/http/server_h3.go` | add/add | FORK | VERIFIED |
| `transport/http/stream_conn.go` | add/add | FORK | VERIFIED |
| `transport/http/tunnel_client.go` | add/add | FORK | VERIFIED |
| `transport/masque/capsule.go` | add/add | FORK | VERIFIED |
| `transport/masque/client.go` | add/add | FORK | VERIFIED |
| `transport/masque/icmp.go` | add/add | FORK | VERIFIED |
| `transport/masque/server.go` | add/add | FORK | VERIFIED |
| `transport/masque/session.go` | add/add | FORK | VERIFIED |
| `transport/masque/template.go` | add/add | FORK | VERIFIED |
| `transport/wireguard/device_stack.go` | modify/modify | FORK | VERIFIED |
| `transport/wireguard/endpoint.go` | modify/modify | FORK | VERIFIED |

## 高风险项裁决与证据

### `common/dialer/default.go`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Add go TUN stack | Improve process search
- Fork 来源：fix(route): admission tickets, synchronous-close accounting, control-plane DNS | fix(route): budget the disposal wave, not the generation boundary
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`go test -race ./common/dialer; dual_stack_* and late_candidate_cadence tests`

### `common/dialer/default_parallel_interface.go`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Fix dialing IPv6 link-local addresses without zone
- Fork 来源：fix(dialer): apply the network strategy to the ICMP path
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`go test -race ./common/dialer`

### `common/dialer/default_parallel_network.go`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Stagger connection attempts in parallel dial
- Fork 来源：admission: one capture per flow, threaded through every stream and packet path | fix(direct,route,dialer): keep the hop protocol out of common.Cast, and finish the third lookup
- 安全性：Fork has its own candidate scheduler (interleaved, fallback-delay stagger, generation/admission aware). Upstream's N.DialParallel delegation is a simplification that would drop the fork's dual-stack admission and health-owner wiring; the upstream stagger intent is present in the fork scheduler and its tests.
- 证据：`go test -race ./common/dialer; dual_stack/late-candidate scheduler tests`

### `common/dialer/resolve.go`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Fix disabled UDP domain unmapping ignored by direct outbound
- Fork 来源：feat(dialer): keep DNS environment provenance on a candidate and demote a superseded one | fix(dialer): keep literal recovery open while the race is alive
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`go test -race ./common/dialer; protocol/direct UDP-unmapping tests`

### `common/httpclient/http3_transport.go`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Rewrite HTTP proxy protocol
- Fork 来源：fix(httpclient): keep the peer's rejection when a warm cache entry retries | fix(httpclient): an explicit H3 rejection is not a transport failure
- 安全性：Fork keeps the H3/H2 fallback and race-dispatch implementation; upstream changed the same transport. Existing race and fallback tests pin the retained behavior.
- 证据：`go test -race ./common/httpclient`

### `common/listener/listener_tcp.go`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Rebind interface-bound listeners when the interface is recreated | Fix interface binding in network namespaces
- Fork 来源：fix(listener): disable MPTCP on listeners by default | feat(listener): share the ListenOptions socket path with protocol factories
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`go test ./common/listener`

### `common/listener/listener_udp.go`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Add go TUN stack | Rebind interface-bound listeners when the interface is recreated
- Fork 来源：feat(listener): share the ListenOptions socket path with protocol factories | fix(listener): bind inbound listeners to an explicit, symmetric address family
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`go test ./common/listener`

### `protocol/masque/client.go`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Forward endpoint local flows to tun | Apply backpressure to masque inbound packets
- Fork 来源：feat(route): serve endpoint local flows from the tun stack | feat(masque): wire the memory TUN's blocking inbound write
- 安全性：Fork MASQUE implementation is larger (CONNECT-IP, PREF64, owned DATAGRAM, per-flow judge); upstream's inbound backpressure boundary is already present in the fork's session path.
- 证据：`go test -race ./protocol/masque ./transport/masque`

### `protocol/masque/server.go`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Forward endpoint local flows to tun | Apply backpressure to masque inbound packets
- Fork 来源：fix(masque): give the TCP listener its own ALPN view | feat(route): serve endpoint local flows from the tun stack
- 安全性：Same as client: fork's CONNECT-IP/PREF64/session implementation retained; inbound wait path present.
- 证据：`go test -race ./protocol/masque ./transport/masque`

### `transport/masque/client.go`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Apply backpressure to masque inbound packets | Fix per-destination backpressure to endpoints
- Fork 来源：feat(masque): wire the memory TUN's blocking inbound write | transport/masque: pin the ownership scan with a range count, not a wall clock
- 安全性：final tree equals the fork parent; the fork implementation is retained
- 证据：`go test -race ./transport/masque`

### `transport/masque/server.go`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Apply backpressure to masque inbound packets | Fix per-destination backpressure to endpoints
- Fork 来源：feat(masque): wire the memory TUN's blocking inbound write | fix(masque,v2rayquic): classify tunnel and accept-loop failures by type
- 安全性：final tree equals the fork parent; the fork implementation is retained
- 证据：`go test -race ./transport/masque`

### `transport/masque/session.go`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Fix per-destination backpressure to endpoints | Add MASQUE support
- Fork 来源：perf(masque): chunk owned datagram batches without ownership regression | test(masque): assert the batch delivers exact bytes, in order
- 安全性：final tree equals the fork parent; the fork implementation is retained
- 证据：`go test -race ./transport/masque`

### `dns/router.go`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Fix DNS reverse mapping domain and lifetime
- Fork 来源：fix(dns): stamp each family answer with the generation its operation captured | fix(dns): let a caller-forced family outrank the router default
- 安全性：final tree equals the fork parent; the fork implementation is retained
- 证据：`go test -race ./dns/...`

### `dns/transport/fakeip/store.go`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Fix FakeIP storage consistency with buffered cache file writes | Buffer cache file writes
- Fork 来源：fix(fakeip): bound a retired generation by its cursor, not by its range | fix(fakeip): remember a retired range across a process restart
- 安全性：final tree equals the fork parent; the fork implementation is retained
- 证据：`go test -race ./dns/transport/fakeip`

### `dns/transport/local/local.go`

- 裁决：**UPSTREAM**；复核：**VERIFIED**
- 上游来源：Remove unused DNS search expansion | Add dns_server_address and dns_search_domain rule items
- Fork 来源：Remove unused DNS search expansion | Fix systemd-resolved detection for local DNS server
- 安全性：Adopted upstream's SystemdResolved condition so a non-systemd-resolved config falls back to its configured servers instead of the resolved resolver.
- 证据：`go test ./dns/transport/local; SystemdResolved condition adopted from upstream`

### `route/conn.go`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Add go TUN stack | Fix duplicated word in listen packet error
- Fork 来源：fix(route): admission tickets, synchronous-close accounting, control-plane DNS | fix(route): budget the disposal wave, not the generation boundary
- 安全性：final tree equals the fork parent; the fork implementation is retained
- 证据：`go test -race ./route`

### `route/network.go`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Forward endpoint local flows to tun | Fix bridge forwarding with auto-redirect
- Fork 来源：fix(route): settle the new generation before the old generation's cleanup | style: gofmt the endpoint host-flow edits
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`go test -race ./route`

### `route/route.go`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Forward endpoint local flows to tun | Rework forward NAT with UDP mapping and fragment support
- Fork 来源：fix(route): refuse a native bypass decided on an owner-only process answer | feat(route): complete the process path when the walk reaches a path rule
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`go test -race ./route`

### `route/splice.go`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Add go TUN stack
- Fork 来源：test(route): pin the cached-drain terminal failure | fix(route): arm liveness on a handed-over DIRECT stream, where no copy loop exists
- 安全性：Fork has reason-aware splice unwrapping and diagnostics; upstream's go-TUN-stack refactor is a different structure. Fork behavior is pinned by splice diagnostics tests.
- 证据：`go test -race ./route`

### `route/reference.go`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Use screen state to end device pause on iOS | Improve idle connection management
- Fork 来源：feat(route,oomkiller): report the retirement walk's owner inventory | fix(route,adapter): reach the HTTP client pools from the retire walks, which never did
- 安全性：final tree equals the fork parent; the fork implementation is retained
- 证据：`go test -race ./route`

### `docs/schema.json`

- 裁决：**GENERATED**；复核：**VERIFIED**
- 上游来源：Add dns_server_address and dns_search_domain rule items | Add full certificate SHA-256 pinning for TLS
- Fork 来源：docs(schema): publish the failover block and regenerate the tracked schema | docs(schema): regenerate schema.json for the AmneziaWG options
- 安全性：Generated artifact; manual conflict resolution drifted from the generator, so it was regenerated with the canonical profile tags and the tracked-schema test passes.
- 证据：`go run -tags "$(cat release/DEFAULT_BUILD_TAGS_WINDOWS)" ./cmd/sing-box schema -o docs/schema.json; go test ./cmd/sing-box`

### `release/DEFAULT_BUILD_TAGS`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Remove dependency on gVisor
- Fork 来源：fix(libbox): carry tfogo_checklinkname0 with the flag, not in the profile files | xhttp: integrate with the runtime lifecycle and enable it in the shipped builds
- 安全性：Fork profile deliberately carries with_xhttp and omits tfogo_checklinkname0 (it travels with ldflags); adopting upstream's older profile would change the shipped build.
- 证据：`go test ./cmd/internal/...; release profile assertion`

### `release/DEFAULT_BUILD_TAGS_OTHERS`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Remove dependency on gVisor
- Fork 来源：fix(libbox): carry tfogo_checklinkname0 with the flag, not in the profile files | xhttp: integrate with the runtime lifecycle and enable it in the shipped builds
- 安全性：Same fork profile decision as DEFAULT_BUILD_TAGS.
- 证据：`go test ./cmd/internal/...`

### `release/DEFAULT_BUILD_TAGS_WINDOWS`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Remove dependency on gVisor
- Fork 来源：fix(libbox): carry tfogo_checklinkname0 with the flag, not in the profile files | xhttp: integrate with the runtime lifecycle and enable it in the shipped builds
- 安全性：Same fork profile plus with_purego for Windows Cronet.
- 证据：`go test ./cmd/internal/...; CI Verify/Android Core`

### `protocol/bridge/netfilter_linux.go`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Fix bridge forwarding with auto-redirect
- Fork 来源：chore: finalize upstream sync integration | Fix bridge forwarding with auto-redirect
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`GOOS=linux go build (pass); go test ./protocol/bridge`

### `transport/http/client.go`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Fix HTTP/2 extended CONNECT with Go 1.27 | Add MASQUE support
- Fork 来源：fix(http): treat H3_REQUEST_REJECTED as an explicit non-processing answer | fix(http): stop tracing the H3 connection field outside its lock
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`go test -race ./transport/http; TestUplinkBodyGetBody / extended CONNECT tests`

### `transport/http/forward.go`

- 裁决：**UPSTREAM**；复核：**VERIFIED**
- 上游来源：Add MASQUE support | Rewrite HTTP proxy protocol
- Fork 来源：Add MASQUE support | Rewrite HTTP proxy protocol
- 安全性：Adopted upstream's io.NopCloser body-ownership fix; prevents Request.Write/Response.Write draining a tunnel body.
- 证据：`go test -race ./transport/http`

### `go.mod`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Forward endpoint local flows to tun | Fix Tailscale peer relay server socket control
- Fork 来源：deps: point sing-quic at its final paced-test commit | deps: consume the synced sing-quic, quic-go and utls lines
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`go mod tidy -diff; go mod verify`

### `go.sum`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Forward endpoint local flows to tun | Fix Tailscale peer relay server socket control
- Fork 来源：deps: point sing-quic at its final paced-test commit | deps: consume the synced sing-quic, quic-go and utls lines
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`go mod tidy -diff; go mod verify`

### `test/go.mod`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Remove local replace from test module | Fix per-destination backpressure to endpoints
- Fork 来源：deps: point sing-quic at its final paced-test commit | deps: consume the synced sing-quic, quic-go and utls lines
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`cd test && go mod tidy -diff`

### `test/go.sum`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Remove local replace from test module | Fix per-destination backpressure to endpoints
- Fork 来源：deps: point sing-quic at its final paced-test commit | deps: consume the synced sing-quic, quic-go and utls lines
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`cd test && go mod tidy -diff`

### `adapter/network.go`

- 裁决：**FORK**；复核：**VERIFIED**
- 上游来源：Forward endpoint local flows to tun | Add dns_server_address and dns_search_domain rule items
- Fork 来源：feat(route): serve endpoint local flows from the tun stack | fix(oomkiller): single reclaim executor and layered memory release
- 安全性：final tree equals the fork parent; the fork implementation is retained
- 证据：`go test ./adapter/...; go test ./route`

### `adapter/router.go`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Forward endpoint local flows to tun | Add tailcat support
- Fork 来源：fix(oomkiller): single reclaim executor and layered memory release | network: publish each transition to the runtime coordinator
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`go test ./adapter/...; go test ./route`

### `common/process/searcher_linux.go`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Report every process sharing a socket in Linux process search | Improve process search
- Fork 来源：test(process): prove the socket-table fallback and the conservative result | process: resolve the owner without a /proc walk when rules only need user
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`GOOS=linux go build; process search tests`

### `protocol/direct/outbound.go`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Fix link-local destinations rejected as TUN loopback | Drop direct outbound UDP packets to unresolved domains
- Fork 来源：fix(dialer): apply the network strategy to the ICMP path | fix(upstream): port eight audited fixes and record the 73-commit ledger
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`go test ./protocol/direct; ListenPacketWithDestination test`

### `protocol/http/outbound.go`

- 裁决：**SEMANTIC_FUSION**；复核：**VERIFIED**
- 上游来源：Add MASQUE support | Rewrite HTTP proxy protocol
- Fork 来源：fix(crosskind): publish resolver facts on the adapters the graph actually walks | fix(test): unblock two pre-existing failures, and wire HTTP into the new capability [skip ci]
- 安全性：final tree differs from both parents; both boundaries were fused
- 证据：`go test ./protocol/http; MultiplexEnabled assertion`

### `transport/device/system_stack.go`

- 裁决：**SAME**；复核：**VERIFIED**
- 上游来源：Apply backpressure to masque inbound packets | Fix per-destination backpressure to endpoints
- Fork 来源：feat(masque): wire the memory TUN's blocking inbound write | Fix per-destination backpressure to endpoints
- 安全性：both parents already agree; conflict was textual/ordering only
- 证据：`go test ./transport/device`

### `service/resolved/resolve1.go`

- 裁决：**UPSTREAM**；复核：**VERIFIED**
- 上游来源：Improve process search | Fix process path of replaced executables on Linux
- Fork 来源：Improve process search
- 安全性：Adopted upstream's TrimSuffix(path, " (deleted)") for replaced executables.
- 证据：`go test ./service/resolved/... (or build on Linux)`

## 说明

- 82 个 `FORK` 不是“一律保留”：逐文件比较的是最终树与父提交；其中 `common/dialer/default_parallel_network.go`、`common/httpclient/http3_transport.go`、`protocol/masque/*`、`route/splice.go` 等高风险项在下方给出了上游行为已被 fork 实现覆盖或替代的具体理由。
- `SEMANTIC_FUSION` 行是真正与两边都不同的文件，包括 dialer、listener、go.mod/go.sum、bridge、transport/http、route/network、route/route、schema 等。
- 20 个高风险的父侧等价文件标为 `VERIFIED` 需配合对应测试；其余 `RECONSTRUCTED` 是事后结果审查，不声称还原了当时的每一次手工 hunk 操作。
- 本矩阵以 merge commit 为基准；merge 之后另有 `f4331ac`（Linux bridge 去重）、`5125a13`（schema 重新生成）等收尾提交，CSV 的 `final_behavior`/证据已注明。
