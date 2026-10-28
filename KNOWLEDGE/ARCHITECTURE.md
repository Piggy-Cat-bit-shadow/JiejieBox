# Architecture boundaries worth preserving

Eleven boundaries. Each one is a place where a reasonable-looking change would be wrong, and each is
stated with the module that carries the detail.

## 1. Ownership: a Scope owns its registered reverse cleanups

`Scope.Close` drains its registered cleanup functions in reverse order, including the child-scope
cleanup installed by `Scope.Start`; resource close functions must be registered with the appropriate
owner. Other owners can also perform teardown, so absence of a direct `scope.Add` is not alone proof
that a resource leaks. A cleanup registered after close returns runs **synchronously on the
registering goroutine**, and `Close` does **not** wait for an in-flight `Start`. A cleanup panic
interrupts the drain; `Box.Close` records the panic as an error rather than finishing that drain.
→ `core-lifecycle-runtime.md`

## 2. One network generation, and a rebuild is a lease, not a flag

`common/runtimecoord` publishes one monotonic epoch. "One logical recovery at a time" is a **lease
identity** (`*RebindLease`), not a boolean, so a superseded lease cannot release a newer one. `Stale()`
and `Acknowledge()` separate "the epoch moved" from "the resource is current". **Publication is not a
rebuild**, and stale resources retire lazily when demanded.
→ `core-lifecycle-runtime.md`

## 3. Selection is a preview until it is committed

The routing rules choose an outbound **root**; groups then select. A group is a control-plane
selection, **not a physical hop**. Some groups wrap the returned connection for interruption and
lifecycle management (`protocol/group/selector.go`, `urltest.go`), so do not infer that selection
means the group never wraps a connection. At run time the
`commit` flag is this separation: a preview must not consume a rotation slot. Any diagnostic that
reports a group as a hop, or reports the hop list backwards, describes a path the traffic does not
take.
→ `path-selection-validation.md`

## 4. Start-time validation reports without exercising the path

Outbound validation runs **after** the dependency sort and **before outbounds start**. Other Box
initialization may already have run. It dials nothing,
resolves nothing, changes no selection and consumes no rotation. The physical-path resolver itself
holds no dialer or clock; that fact alone does not prove that every enclosing call is side-effect-free.
Where the leaf is not knowable it reports
`Unknown` and stops, so a partly-verified path cannot be mistaken for a verified one. The current
`common/physicalpath/dryrun.go` distinguishes fatal failures from capability warnings: an existing
detour that cannot satisfy a consumer can warn while an affected flow still fails at runtime;
missing references and other fatal classes must not be demoted wholesale. See the path-selection
module for the exact taxonomy.

**Two documented incidental side effects exist** (a deprecation log and a memoised tag→object
resolution). The prohibition is on network resolution, dialling, changing selection and consuming
rotation, not on logging or all internal memoization.
→ `path-selection-validation.md`

## 5. Platform facts are inputs; the core decides policy

A client reports the number its OS gave it (memory level, screen on/off, lock state, device wake).
The **core** owns the trim ladder, the coalescing windows, the sleep/wake edges and the reuse policy.
Level, edge and snapshot are three different things: a current-state re-read **reconciles the level**
and must never fabricate an edge.
→ `power-mobile-lifecycle.md`

## 6. Sniff metadata informs planning; it does not rewrite the destination

A sniffed domain becomes DNS planning and dial candidates; sniffing alone does not rewrite
`Destination` unless the explicit deprecated `override_destination` action is used. Separately,
FakeIP reverse mapping in `route/route.go` saves `OriginDestination` and replaces `Destination` with
the mapped FQDN. Do not conflate that translation with a sniff-driven override.
→ `sniff.md`

## 7. Buffer ownership is a correctness contract, not an optimisation surface

Borrowed cursors must not outlive their call; the QUIC working copy is deliberately unpooled; a batch
send is all-or-nothing per chunk, and earlier accepted chunks have transferred ownership. A context-ID
substitution **releases the original** and must refuse its chunk. Each rule exists because violating it produced a
real bug — including one where every packet in a refused batch was silently dropped.
→ `buffer-ownership.md`

## 8. MTU is three quantities, not one

Outer link MTU, outer QUIC packet size and inner tunnel MTU are distinct quantities. For MASQUE,
known detour capacity clamps the initial QUIC packet size; this alone does not prove a hard cap on
later packets. TUIC has a separate hard-ceiling path. Unknown physical-path capacity is not a proof.
→ `mtu-and-geometry.md`

## 9. The upload scheduler is opt-in and inert when unset

`traffic_scheduler` and `traffic_class` are fork-only keys. Absent a rate, the gate observes every
managed byte, admits all of them, and starts **no queue, no lock and no timer**. An inactive shaping
gate alone does not prove full configuration compatibility or identical end-to-end behavior with
another implementation. When a rate is configured, shaping runs **continuously**, not only under
contention.
→ `data-path-optimization.md`

## 10. Direct Offload is semantic equivalence, and its data-plane effect is Linux-only

A dial option disqualifies a flow only if the userspace path would actually apply it **to that flow**.
New `DialerOptions` fields must not be able to make a bypass *safe* by default: an unclassified option
refuses. And the verdict is a real bypass **only** in Linux `auto_redirect` — no TUN stack honours it.
→ `data-path-optimization.md`

## 11. Release provenance is download-and-re-verify, and it is scoped

Release **downloads** the artifacts a product workflow produced for the exact commit and re-verifies
per-platform bytes, hashes and recorded commit — it does not rebuild. This description is scoped to
the historical release workflow and requires rechecking the current plan and run evidence for each
release. A declaration does not prove execution; a specific Windows daemon CI run is documented in
`windows-daemon-security.md`, and does not by itself prove the release artifact pipeline.
→ `ci-release.md`

---

## The module map

`modules/README.md` maps a task to the module to read. Load only what your task touches.

## Two standing conventions

These are **conventions**, not invariants — they are mature practice here, and a future change may
reasonably depart from them with a stated reason:

1. **Keep source and its test artifact in the same change.** The fork's own history shows what happens
   without it: a goroutine-census test that could not fail, a TUN test that called `inbound.Close()`
   directly (which the product never does) and stayed green while the product leaked, and a splice test
   that asserted the success path only.
2. **Prefer a behavioural assertion to a source-text guard.** A grep for a string in a dependency fails
   when the line moves and passes when the meaning changes — the wrong way round.
   `docs/UPSTREAM-SYNC.md:89-94` states this and names where it was not possible.
