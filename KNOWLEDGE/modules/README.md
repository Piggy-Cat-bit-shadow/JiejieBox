# Module map — which file to read for which task

Load **only** the modules your task touches. Each module is written to be readable on its own: it
carries its own source anchors, provenance and evidence level.

## Task → module

| If you are changing… | Read |
|---|---|
| Box start/close order, `Scope`, teardown, network epochs, idle retirement, `route/reference.go` | `core-lifecycle-runtime.md` |
| Start-time validation, `detour`, selectors/urltest/loadbalance, `PhysicalPath`/`ControlPath`, failover, cycles | `path-selection-validation.md` |
| DNS routing/caching, FakeIP, `domain_resolver`, DNS Demand, DNS leaks, network-transition DNS | `dns-demand.md` |
| Any sniffer, peek/read, sniff timeouts, fragmented QUIC, sniff→routing metadata | `sniff.md` |
| MASQUE/CONNECT-IP, capsules, sessions, DATAGRAM batching, address/route/DNS/PREF64, PTB | `masque-connectip.md` |
| Native Naive, HTTP CONNECT proxy, `transport/http`, `http_clients`, target ACL, padding/masquerade | `naive-http-transport.md` |
| XHTTP / SplitHTTP / XMUX / stream-one / packet-up — writing a VLESS XHTTP node, mode choice, connection reuse | `xhttp-client-transport.md` |
| Direct Offload / native bypass, `traffic_class`, `traffic_scheduler`, dial governor | `data-path-optimization.md` |
| `fast_path` on a selector or urltest group — authorising a group's flows to attempt Direct Fast Path, or refusing it | `fast-path-group-policy.md` |
| Any zero-copy, allocation, buffer pool, batch ownership, splice question | `buffer-ownership.md` |
| Sleep/wake/screen/lock, memory trim, device axis, `ReuseSuspect`, power policy | `power-mobile-lifecycle.md` |
| A libbox export, `SetupOptions`, CommandServer API, ABI version, Apple/Android tag set | `libbox-platform-contract.md` |
| Build tags, protocol pruning, release profiles, generators, CI gates, release artifacts | `build-pruning-release.md` |
| MTU, packet size bounds, buffer geometry, low-memory profile | `mtu-and-geometry.md` |
| DNS routing/caching, FakeIP, generations, consumer demand | `dns-demand.md` |
| Apple/Android ABI, the libbox export surface, the client-facing contract | `libbox-platform-contract.md` |

All files in this map exist at the knowledge revision; the map and the modules are committed
together. If a link here does not resolve, the knowledge base has been edited inconsistently —
treat that as a defect, not as a missing document.

## The fork's own vocabulary, at a glance

The whole fork in one paragraph — then read the module for the part you need.

> This fork keeps upstream's registry, protocols and build profiles and adds: a **start-time
> proof system** (`common/physicalpath`, `common/routeflow`) that refuses a configuration whose
> declared chain cannot carry the flow it routes, without dialling, resolving or selecting; a
> **runtime coordinator** (`common/runtimecoord`) with one monotonic network generation and
> identity-based rebuild leases; a **power/device governor** (`common/power`) that turns platform
> facts into sleep/wake/reuse policy; an **upload traffic scheduler** (`common/trafficsched`) and
> **traffic classes** (`common/trafficclass`); a **Direct Offload** model that lets a provably
> equivalent direct flow be carried by the OS; a full **MASQUE/CONNECT-IP dataplane**
> (`transport/masque`) plus an inner/bootstrap resolver split; a **Native Naive** inbound with UoT,
> padding, masquerade and pre-authentication limits; a rewritten **HTTP/2+HTTP/3 transport**
> (`transport/http`); and **product plumbing** for Apple (one pinned client source, brand-vs-identity
> split), Android (AAR/ABI) and Windows (core-only artifact), with a fail-closed release-acceptance
> gate that re-verifies bytes rather than rebuilding them.

## Fork-created packages — the code that did not exist below the fork point

Established with `git ls-tree` at `e4b7e692` versus `a267e99`, so these are genuinely new:

| Package | Size (Go lines incl. tests) | Module |
|---|---|---|
| `common/physicalpath` | 8,543 | `path-selection-validation.md` |
| `common/routeflow` | 340 (no test files) | `path-selection-validation.md` |
| `common/runtimecoord` | 2,299 | `core-lifecycle-runtime.md` |
| `common/power` | 2,317 | `power-mobile-lifecycle.md` |
| `common/trafficsched` | 5,218 | `data-path-optimization.md` |
| `common/trafficclass` | 310 | `data-path-optimization.md` |
| `common/bufgeom` | 1,071 | `mtu-and-geometry.md`, `buffer-ownership.md` |
| `common/badh2` | 77 | `naive-http-transport.md` |
| `dns/transport/group` | 2,590 | `dns-demand.md` |
| `internal/memmetrics` | 877 | `power-mobile-lifecycle.md` |
| `transport/v2rayxhttp` | 7,181 | `xhttp-client-transport.md` |
| `protocol/vless/encryption` | 1,771 | not covered — see "Not covered" below |

## Heavily modified packages (present below the fork point, substantially rewritten)

| Path | Files added in range | Module |
|---|---|---|
| `transport/http` | 90 total, 20 at the fork point | `naive-http-transport.md` |
| `transport/masque` | 57 total, 7 at the fork point | `masque-connectip.md` |
| `protocol/naive` | 31 total, 4 at the fork point | `naive-http-transport.md` |
| `protocol/masque` | 29 total, 3 at the fork point | `masque-connectip.md` |
| `route`, `adapter`, `dns` | 158 / 77 / 94 commits | several |

MASQUE and Naive are **not fork inventions**: `MASQUE support` arrived in `b83089ec` ("Add MASQUE
support", 世界, 2026-09-21) and `transport/http`/`protocol/naive` already existed at `e4b7e692`.
What the fork added on top is documented per module with provenance.

## Not covered by these modules — and why

| Area | Why not covered |
|---|---|
| **`experimental/boxdd/**` — the Windows daemon** (52 files) | **A real gap, and the largest one.** The package is the daemon/service/worker/peer-authentication surface: Named Pipe peer checks, executable path resolution, the Authenticode trust path, service installation and the updater. It is security-critical and has its own threat model. It is **not** upstream code and **not** covered incidentally by `libbox-platform-contract.md`, which records the client-facing ABI rather than the daemon's authorisation model. `authenticode_windows.go` changed in the `v0.1.6 → v0.1.9` delta — the runtime certificate-validity gate was removed — and no module describes it. **Registered here rather than left silent**, per `REFRESH.md` §29: an unowned subsystem must be named, not implied. |
| `option/schema.go` → `docs/schema.json` | The generator is documented as a command (`make schema`) but never as a **maintenance obligation**, and the artifact is currently **stale**: `docs/schema.json` contains `traffic_class` 81 times and `fast_path` **0** times, because `option/schema.go` gained the `fast_path` key and nothing regenerated the file. A generated artifact with no stated owner is how it drifts. **The rule this gap implies:** a change to `option/schema.go` (or to any option struct that feeds the schema) must regenerate `docs/schema.json` in the same commit — `make schema`. This is recorded as a gap rather than fixed here because this refresh is documentation-only and regenerating the artifact is a product-output change, not a hygiene change. Evidence: `Makefile:35-36`; `docs/schema.json`; **`VERIFIED_SOURCE`**, not `VERIFIED_BY_REGENERATION` — the settling command is `make schema` followed by a diff. |
| `protocol/vless/encryption` (5 non-test files, new in the fork) | Its tests carry a `//go:build !race` split, so its ABI checks are absent under `-race`. |
| Individual protocol wire formats (shadowsocks, trojan, tuic, hysteria2, …) | Upstream's. The fork patches specific behaviours; those are called out inside the module that owns the behaviour, not per protocol. |
| The Apple / Android / desktop **client** source | Separate repositories, checked out as submodules (`clients/apple`, `clients/android`, `clients/desktop`) and empty in a clean worktree. `libbox-platform-contract.md` records only the core side. |
| Clash API / V2Ray API internals | Upstream's, with fork-specific co-existence notes in `build-pruning-release.md`. |

## How to decide whether a module is still accurate

```sh
git log --oneline v0.1.9..HEAD -- <paths the module cites>
```

If that is empty, the module's statements about those paths are still the reviewed ones. If it is not,
read the module's `AI cautions` first, then the diff.

Do **not** copy an older tag into this command from memory or from a report. The tag belongs to the
current production baseline, which is recorded in `BASELINE.md` and **moves**; a stale tag here turns
the check into a no-op that silently passes while the module drifts. (That is exactly what happened
between `v0.1.6` and `v0.1.9`: this command named `v0.1.6` while the baseline row claimed nothing had
changed, so nobody re-ran it. The row and the command have to move together.)
