# Invariants

Three lists, in descending strength. **Putting a rule in the wrong list is the failure this file
exists to prevent**: an over-promoted convention blocks a legitimate future change, and an
under-promoted correctness rule lets one through.

A rule belongs in **Hard invariants** only if it is one of: protocol/format correctness, buffer
ownership correctness, an ABI or client contract, a platform hard constraint, release-artifact
integrity, a guard against a **confirmed** concurrency/lifecycle bug, or an explicit upstream/fork
ownership boundary. Everything else is a convention, a strategy or an implementation detail.

---

## A. Hard invariants

Each row: the rule, why it must hold, what breaks, the commit or test that proves it, and how to
re-check.

### A1 — `Scope` owns all teardown; a late cleanup runs on the registering goroutine

`Scope.Close` walks `scope.Add` entries and **never calls a component's `Close()`**; a cleanup
registered after close returns runs **synchronously on the caller's goroutine**; `Close` does **not**
wait for an in-flight `Start`.

* **Why:** a component with a `Close` never handed to the Scope is a component whose teardown the
  product never runs. Making the late path asynchronous means a resource is alive after `Close`
  returned with no error.
* **Breaks if violated:** a torn-down `*Inbound` keeps an observer; a listener binds after the drain.
* **Proof:** `adapter/lifecycle.go:112-138`, `:186-217`; `adapter/lifecycle_close_boundary_test.go`;
  commits `53a3f97e8`, `f2c728d10`, `e3349044a`.
* **Re-check:** `go test -count=1 ./adapter/...`
* **Kind:** lifecycle bug guard. **Owner:** `core-lifecycle-runtime.md`

### A2 — "One recovery at a time" is a lease identity, not a boolean

`Registration.lease` is a `*RebindLease`; `Complete()` is a no-op for a superseded lease but **always
cancels**; `Expired()` is true when the lease is no longer the current owner.

* **Why:** a superseded recovery must not be able to complete, release or cancel a newer one.
* **Breaks if violated:** an old rebind publishes over a new one and cancels the live lease's context.
* **Proof:** `common/runtimecoord/coordinator.go:191-238`, `:282-310`; `coordinator_test.go`,
  `lease_test.go`.
* **Re-check:** `go test -count=1 ./common/runtimecoord/...`
* **Kind:** concurrency-bug guard. **Owner:** `core-lifecycle-runtime.md`

### A3 — A preview must not consume a selection slot

`Selector.Selected` returns the loaded atomic and **ignores the network argument**; `URLTest.Selected`
reads history and writes nothing back; `LoadBalance.Selected` advances its round-robin cursor **only**
when `commit` is true.

* **Why:** a diagnostic or validation walk that moved the cursor would hand the next flow a different
  member than the one it just reported.
* **Breaks if violated:** the reported exit is not the dialled member; two flows consume three slots.
* **Proof:** `protocol/group/loadbalance.go:259-261`, `:337-341`; `route/physicalpath_route_test.go`
  (`TestPhysicalPathTakesNoSelectionPreviewThroughRealGroups`,
  `TestTwoFlowsAdvanceTheCursorExactlyTwiceWithAPreviewInBetween`), validated by a **mutation** that set
  `commit=true` and watched `CommittedSelections()` change.
* **Re-check:** `go test -count=1 ./route/... ./common/physicalpath/...`
* **Kind:** confirmed-behaviour guard. **Owner:** `path-selection-validation.md`

### A4 — A group is a control-plane object, never a physical hop

`Hop.IsGroup` is never set; groups contribute only to `ControlPath`. `Hops` is packet order
(device-nearest first); `ControlPath` is descent order and is **deliberately not reversed**.

* **Why:** a diagnostic that reports a group as a hop, or reports the hop list backwards, describes a
  path the traffic does not take — worse than no diagnostic.
* **Breaks if violated:** a status view names a healthy hop as the broken one.
* **Proof:** `common/physicalpath/physicalpath.go:261-264`, `:564-600`; `leaves.go:305-310`;
  `physicalpath_test.go:462-482`; `dependency_on_group_test.go:148-155`;
  `route/physicalpath_route_test.go`. The direction fix is `524ea41e`; the `ControlPath` fix is
  `d46717dbd`. **Nine tests went red when the direction landed.**
* **Re-check:** `go test -count=1 ./common/physicalpath/...`
* **Kind:** correctness. **Owner:** `path-selection-validation.md`

### A5 — Start-time validation must not dial, resolve, select or mutate

`validatePhysicalPaths` dials nothing, resolves nothing, changes no selection and consumes no rotation.
A dry run over a real start asserts committed == 0 **and** previewed == 0.

* **Why:** the whole value of a start-time refusal is that it is decidable offline and has no runtime
  consequence.
* **Breaks if violated:** starting a box becomes a side-effecting operation; a validation pass moves a
  selector.
* **Provable from the object graph:** `Resolver` holds a registry lookup, a `Snapshot` and a network
  name — no dialer, no logger, no context, no clock.
* **Honest scope:** two side effects **do** exist and are permitted — a deprecation log
  (`common/dialer/dialer.go:187`) and a memoised detour tag→object resolution
  (`common/dialer/capability.go:100` → `detour.go:63`). Neither dials. `Box.Status` separately takes a
  live preview on purpose.
* **Proof:** `adapter/outbound/manager.go:235-237`; `manager_physicalpath_test.go:357-373`;
  `TestBuildNeverCallsADialingMethod`; `TestDryRunLeavesTakeNoSelectionPreview`.
* **Re-check:** `go test -count=1 ./common/physicalpath/... ./adapter/outbound/...`
* **Kind:** correctness. **Owner:** `path-selection-validation.md`

### A6 — Buffer ownership: borrowed views, unpooled working copies, all-or-nothing batches

Six rules in one:

1. a `payloadReader` cursor must not outlive the `PeekStream` call that created it (`sniff.go:88-91`);
2. `takePacketBytes` returns a view valid only while the datagram lives (`quic.go:36-54`);
3. QUIC's working copy is a fresh per-call `make`, deliberately unpooled, and a retained fragment
   aliases **it**, never the caller's datagram (`quic.go:183-184`, `:305-306`, `:472-476`);
4. header protection writes 4 bytes **in place**, so a `Length` that cannot reach them is rejected
   (`quic.go:125-131`);
5. a MASQUE batch send is **all-or-nothing** (`session.go:460-465`, `:533-537`);
6. on the owned-datagram path ownership **transfers on success** and the release is skipped
   (`session.go:484-489`).

* **Why:** each rule exists because violating it produced a real bug.
* **Breaks if violated:** a retained cursor reads recycled pool memory; a pooled working copy makes a
  fragment outlive its owner; a release on the owned path is a **double free**.
* **Proof:** the address-range test `quic_ownership_test.go:314-350`; the datagram-poisoning tests
  `:206-281` and `quic_boundary_test.go:427-456`; the `cap` bound
  `sniff_bounded_copy_internal_test.go:120-156`; `route/udp_batch_capability_test.go`.
* **Re-check:** `go test -count=1 ./common/sniff/...` and
  `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./transport/masque/...`
* **Kind:** buffer-ownership correctness. **Owner:** `buffer-ownership.md`

### A7 — A context-ID substitution releases the original and must refuse the whole batch

`PrependContextID` may allocate a replacement and **release the original**, so a substitution refuses
the batch **before it is attempted** and updates the caller's slice in place.

* **Why:** this is a documented, real bug. A refused batch kept the context ID, the fallback prepended a
  second one, `packetAddresses` read the wrong version nibble, and **every packet was silently dropped
  — no error, no PTB**.
* **Breaks if violated:** silent total packet loss on a tunnel.
* **Proof:** `transport/masque/session.go:551-579` with the bug recorded at `:539-549`.
* **Re-check:** the MASQUE suite under tags.
* **Kind:** buffer-ownership correctness + confirmed bug. **Owner:** `masque-connectip.md`

### A8 — Idle retirement asserts `IdleConnectionReleaser`, never the bundled `IdleConnectionKeeper`

* **Why:** asserting the bundled interface **silently failed at run time on every walk**, so the HTTP
  client pools were unreachable through four passes of review. A full keeper is deliberately not
  required: some transports have no safe "no new stream" state.
* **Breaks if violated:** a memory trim does nothing at all while reporting success.
* **Proof:** `route/reference.go:440-494` with the rationale at `:479-484`; commits `93823c3d`,
  `8735b1d`; `route/reference_coverage_stress_test.go`.
* **Re-check:** `go test -count=1 ./route/...`
* **Kind:** confirmed-bug guard. **Owner:** `core-lifecycle-runtime.md`

### A9 — Memory trim is idle-only; `ReleaseMemory` is the path that may reset the network

Trim must not reset the network, dial, wake suspended work, or terminate active flows.
`ReuseSuspect.RetireSuspect()` is a stronger **sleep/reuse-boundary** action and runs **before**
`CloseIdleConnections`.

* **Why:** a trim that reconnects turns a memory-pressure response into a traffic event.
* **Breaks if violated:** trimming during idle builds a new queue and burns radio time; a trim becomes
  a reconnect trigger — the exact shape the memory path must not have.
* **Honest scope:** `service/oomkiller/timer.go:324-337` **does** call `ReleaseMemory`, which resets the
  network, and that is deliberate (`route/conn_reclaim.go:18` records it; the killer is disabled by
  default at `option/oom_killer.go:13`). **Trim ≠ Release.**
* **Proof:** `route/reference.go:154-197`, `:394-411`, `:496-511`, `:547-556`;
  `route/reset_entrypoints_test.go:95`; `route/reset_entry_matrix_test.go:103`.
* **Re-check:** `go test -count=1 ./route/... ./common/power/...`
* **Kind:** lifecycle guard. **Owner:** `power-mobile-lifecycle.md`, `core-lifecycle-runtime.md`

### A10 — Platform snapshots are level-only; a current-state read must not become an edge

`reconcileScreenState` / `reconcileLockState` reconcile a level. A display turning **on** is not an
unlock. `resumed()` publishes nothing.

* **Why:** measured on a real device — 13 resume callbacks during one 28 m 24 s sleep produced 13
  anchors and a 28.29 s boundary, destroying the measurement. And the anchor and the per-episode
  boundary latch are **the same field**, so gating the sleep edge on a paused state produced **zero**
  boundaries over a ten-minute lock — strictly worse than the bug — and was reverted.
* **Breaks if violated:** sleep duration is misreported; reuse boundaries are consumed by screen
  glances.
* **Proof:** `box_lifecycle.go:141`, `:173-199`, `:228-252`; commits `645d96b4`, `60681f27`, `0ff90482`;
  `box_lifecycle_ordering_test.go`; `e20875359` → `e661258a6` (revert, both archived);
  `docs/fork/p1c-iphone-device-closure.md:289-291`.
* **Re-check:** `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run 'TestTheReuseVerdict|TestTheSleepEdge' .`
* **Kind:** confirmed-bug guard + platform constraint. **Owner:** `power-mobile-lifecycle.md`

### A11 — The libbox ABI surface and the Apple single-source pin

`experimental/libbox.DeprecatedNote.Message()` returns `*StringBox`, **not** `string`, because a bare
`string` puts a Go pointer in gomobile's packed result frame and produces
`fatal error: bulkBarrierPreWrite: unaligned arguments`. `adapter.NewAdapterWithDialerOptions` carries
an extra variadic parameter. And the Apple client source has **one** authority: the `clients/apple`
gitlink — the refs file is forbidden from recording a SHA and the workflow body from hardcoding one.

* **Why:** the first is an unrecoverable crash; the second is what stops iOS and macOS being built from
  two different sources.
* **Breaks if violated:** a client built against upstream's libbox crashes; two Apple products ship
  from two revisions.
* **Honest gap:** **no *wired* mechanism can detect the pinned Apple revision moving to an unintended
  commit.** The one candidate verifier (`scripts/ci/check-libbox-abi.sh`'s `VERIFIED_APPLE_SHA`) is
  unwired; as of `93d757828` it is no longer *stale* either — `ece0fb9d2` moved the constant onto the
  gitlink, so its layer 5 is green and it agrees at `5cab4ec5…`. What is missing is execution, not
  agreement: moving the gitlink alone silently re-creates the divergence, and
  `check-apple-source-selection.sh` cannot notice it because it is self-referential to the gitlink by
  design. `client-apple.yml` is not in the release-acceptance required set.
* **Proof:** `docs/fork/libbox-abi-contract.tsv`; `release/apple-client-refs.env`;
  `check-apple-source-selection.sh:124-136`, `:209-210`; `check-libbox-abi.sh:98-124`;
  `apple-client-source.sh:88-105`; `docs/fork/apple-artifact-verification.md:609-616`.
* **Re-check:** `git ls-tree HEAD clients/apple`; `git grep VERIFIED_APPLE_SHA= -- scripts/` and compare
  the two by hand — that comparison is the whole check, and nothing performs it for you.
* **Kind:** ABI/client contract. **Owner:** `libbox-platform-contract.md`

### A12 — Release re-verifies bytes per platform; it never rebuilds

`release.yml` selects one successful run per product workflow **for the exact SHA**, downloads from
those runs, and per platform checks binary + `.sha256` + `BUILD-INFO` with a real `sha256sum -c`,
reading the platform from the file's own header and requiring the recorded commit to match.

* **Why:** metadata provenance is not binary provenance.
* **Breaks if violated:** a release attaches bytes nothing built or verified.
* **Honest scope:** it covers what the plan step selects, requires 4–5 manual dispatches first, and
  **`client-apple.yml` is not in the acceptance required set** — so acceptance is structurally
  incapable of noticing Apple-pin drift. **No workflow-run record exists in the tree**, so this is
  `VERIFIED_SOURCE`, never `VERIFIED_CI`.
* **Proof:** `release.yml:144-204`; `scripts/ci/release-artifacts.sh`;
  `scripts/ci/verify-release-acceptance.sh:99-102`, `:151`.
* **Re-check:** read the workflow; a real run needs `gh` + network.
* **Kind:** release-artifact integrity. **Owner:** `ci-release.md`

### A13 — The upload scheduler is inert when unset, and the dial governor is inert outside its window

Absent/zero/empty `traffic_scheduler` ⇒ the gate observes every managed byte, admits all of them, and
starts **no queue, no lock and no timer**. The dial governor's zero value applies to nothing.

* **Why:** this is what keeps an unshaped configuration loadable by official sing-box and
  behaviourally byte-identical; and a permanent dial limit would be a tax paid for a rare event.
* **Breaks if violated:** a config that asks for nothing gets shaping; every dial on a working network
  pays for a burst that is not happening.
* **Proof:** `common/trafficsched/doc.go:93-97`;
  `common/trafficsched/scheduler.go` (`Options.RateSource`, `SetRateSource`);
  `route/dial_governor.go:22-24`, `:55-56`; `option/traffic_scheduler_test.go:31`.
* **Re-check:** `go test -count=1 ./common/trafficsched/... ./route/...`
* **Kind:** compatibility/ABI-adjacent (config loadability). **Owner:** `data-path-optimization.md`

### A14 — Every `DialerOptions` field must be classified before it can enable a bypass

An unclassified option set on a flow produces `BlockerUnclassified`, refusing the bypass; a reflection
test fails until every field is classified, and the table may not name a field that no longer exists.

* **Why:** a newly added dial option must not be able to make a bypass **safe** by default.
* **Breaks if violated:** the fast path silently discards a socket semantic the operator asked for.
* **Proof:** `common/dialer/profile.go`; `TestEveryDialerOptionIsClassified`;
  `TestFamilyStrategyAppliesToALiteralAddress`; `TestZeroProfileRefuses`.
* **Re-check:** `go test -count=1 ./common/dialer/...`
* **Kind:** correctness guard. **Owner:** `data-path-optimization.md`

### A15 — Upstream/fork ownership boundary: the pinned-replace set is a prefix rule with four members

`REQUIRED_FORK_REPLACES` names `sing`, `sing-tun`, `quic-go` and `sing-quic`; `cronet-go` publishes a
**tree** of modules, so the requirement is a **path prefix**, not a list.

* **Why:** a `replace` rewrites **one** module path. Pinning only the root leaves nested modules
  resolving to upstream — a pairing nothing built or tested, and on linux/amd64 one that does not link.
  `sing-quic` is the pass-through layer that carries the hard packet-size ceiling, so a nested module
  resolving upstream **silently makes the proven MTU ceiling disappear**.
* **Breaks if violated:** the fork's own bindings are used with upstream's native archives.
* **Proof:** `scripts/ci/check-go-module-integrity.sh:105-124`; its own history at `:60-66` records that
  the `test` module once silently linked upstream `sing-tun` and *"the old code could not fail on it, by
  construction"*.
* **Re-check:** `bash scripts/ci/check-go-module-integrity.sh --require-checked test`
* **Kind:** upstream/fork ownership boundary. **Owner:** `build-pruning-release.md`

### A16 — A tag stub that guards a runtime availability question must FAIL, not be absent

`include/quic_stub.go:48` installs a **failing** `ConfigureHTTP3ListenerFunc` rather than leaving the
field nil, so `decideHTTP3Availability` can distinguish "absent" from "present but broken".

* **Why:** with a nil field the two states are indistinguishable, and a missing optional transport
  cannot be told from a broken one — so the fatal/non-fatal decision has no input.
* **Breaks if violated:** either every missing HTTP/3 transport takes down TCP CONNECT, or a broken one
  is silently tolerated.
* **Proof:** `include/quic_stub.go:48`; `protocol/naive/inbound.go:212-236`, `:316-334`.
* **Re-check:** build with and without `with_quic` and assert the decision differs.
* **Kind:** platform/build constraint. **Owner:** `naive-http-transport.md`, `build-pruning-release.md`

### A17 — Sniffed metadata never rewrites the destination

A sniffed domain reaches routing as DNS planning and dial candidates; `Destination` is unchanged. The
only rewrite is the explicit, deprecated `override_destination`.

* **Why:** rewriting the destination from a sniffed name changes which rules match and which address is
  dialled — a policy change disguised as a convenience.
* **Also:** a sniffed domain **disqualifies the Direct Offload fast path**
  (`BypassRefusedSniffedDomain`).
* **Breaks if violated:** rule semantics change for a flow that merely looked like something.
* **Proof:** `route/route.go:1437-1444`, `:541-546`, `:1255-1260`, `:1405-1410`; `route/route.go:791-796`;
  `route/bypass_verdict.go:29`, `:54`.
* **Re-check:** `go test -count=1 ./route/... ./common/sniff/...`
* **Kind:** correctness. **Owner:** `sniff.md`

### A18 — Fragmented QUIC continues only on `ErrNeedMoreData`, bounded, with the buffer released

`maxSniffRounds = 64`; on hitting the cap the just-taken buffer is released and the flow is treated as
unsniffable; every other error is recorded as `metadata.SniffError` and is **not fatal**.

* **Why:** the pre-fix behaviour was measured — 1,000 crafted datagrams retained 1,000 buffers
  (+16.7 MB live), with the per-round QUIC buffer growing from 33 KB to 1.06 MB.
* **Breaks if violated:** a hostile peer drives unbounded memory growth and leaked pooled buffers.
* **Honest evidence:** the cap and the release-on-abandon are **`VERIFIED_SOURCE` only** — no test and
  no reproducible measurement exists in the tree.
* **Proof:** `route/route.go:1211-1217`, `:1340-1347`; `common/sniff/sniff.go:182-199`; commit `578663e0`.
* **Re-check:** `go test -count=1 ./common/sniff/...` (does **not** cover the cap).
* **Kind:** hardening against a confirmed measurement. **Owner:** `sniff.md`

### A19 — Apple snapshot reconciliation is level-only **(see A10)**

Listed separately in the original because it also constrains the *client*: a client must not send an
edge it did not observe, and the core must not synthesise one from a level.

* **Kind:** ABI/client contract. **Owner:** `power-mobile-lifecycle.md`, `libbox-platform-contract.md`

### A20 — Release acceptance is fail-closed

A failed, cancelled, timed-out or skipped product job must not be treated as success, and a gate that
skipped a layer must not report `PASS`.

* **Why:** `scripts/ci/apple-ci-job-gate.py`'s docstring is the clearest statement of the class: a
  dispatch with `build_ios: false` **concludes success while a platform was never built**, and skipped
  jobs are not failures. `check-libbox-abi.sh` prints `PARTIAL` with exit 0 for exactly this reason.
* **Honest gap:** the acceptance script is **invoked by no workflow** — `verify.yml` runs only its
  self-test (against a mock `gh`). This invariant describes the *design*, and the design is not wired.
* **Proof:** `apple-ci-job-gate.py`; `check-libbox-abi.sh:427-434`;
  `verify-release-acceptance.sh`; `verify.yml:54-87`.
* **Kind:** release-artifact integrity. **Owner:** `ci-release.md`

---

## B. Current conventions

Mature practice, not immutable architecture. Depart with a stated reason.

* **Keep a test with its change**, and prefer a behavioural assertion to a source-text guard
  (`docs/UPSTREAM-SYNC.md:89-94`).
* **Use capability-specific retirement interfaces** — implement `IdleConnectionReleaser` rather than
  claiming a full keeper.
* **Keep source/build provenance in artifacts** (`BUILD-INFO`, `.sha256`).
* **Add a new reusable pool only by extending the existing `ReuseSuspect` inventory**, never as a
  second pool.
* **Guard an ordering assertion against vacuity** — assert that the band or path was actually
  exercised, as `box_lifecycle_ordering_test.go` does.
* **Do not gate a watchdog on sleep state.** A watchdog that stops watching is not a power saving.
* **Defer a periodic timer, but never its first pass.**

---

## C. Current optimization strategies

Provisional by nature. Each has a measurement behind it, and each may be revisited if the measurement
changes.

* `ModePaced` aggregate shaping is the shipped scheduler mode; the ordering modes were measured as
  indistinguishable from doing nothing.
* The MASQUE batch path is taken only for **more than one** buffer.
* `common/bufgeom`'s allocation ceilings are asserted in the `!race` build only.
* Shadowsocks' steady-state read-side copy is a **measured keep**.
* The QUIC single-frame `ClientFirefox` heuristic is left as-is; the fix was out of scope.
* L2 and L3 copy paths are indistinguishable on loopback, which is a property of the rig, not a defect.

---

## D. Implementation details (not rules)

* `PathNode.RequiredNetworks` is **write-only in production** — two writers, zero production readers.
  The validator reads `HopCheck.Requirement`, computed from a function parameter. The fork's own commit
  `fc4bdd416` says it *"should be wired or removed by the file's owner"*. **This is a FOLLOW-UP. Do not
  cite it as a contract, and do not build a rule on the two-requirement-model tension it creates.**
* `common/sniff`'s sniffing is best-effort; `metadata.SniffError` is informational.
* `adapter/prestart.go` is a one-line `package` declaration and is vestigial.
* The `trafficresearch` build tag is referenced by nothing, and a test fails if the research controller
  becomes reachable.
* `docs/fork/physicalpath-controlpath-model.md` is stale on `ControlPath` ordering (see A4).
* `dryrun.go:470-493` is an orphan comment that contradicts the code beside it; the code is settled.
