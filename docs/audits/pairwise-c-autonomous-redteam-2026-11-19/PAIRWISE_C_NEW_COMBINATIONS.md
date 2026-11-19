# PAIRWISE C — New combinations

The two combinations this window selected, why they are *new* relative to the A/B lists, the mechanism
contract, the attack, the production path, and the controls. Both were fixed; the execution evidence is
in `PAIRWISE_C_EXECUTION_EVIDENCE.md` and the defects in `PAIRWISE_C_BUGS_AND_FIXES.md`.

---

## C-1 The generation gate's listener half is unrouted in six of seven owners

**The combination.** `route.NetworkManager`'s listener phase × `runtimecoord.GenerationGate`'s
transition ledger. Both decide "the network changed, reset this transport", and they meet at one shared
piece of state: the gate's `resetEpoch`/`resetDone` ledger.

**Why it is not on the A/B lists.** A's pairs were admission/pool/NAT; B's were DNS/family/fallback.
Neither asked the question *within* one mechanism across owners: "does every owner that has a gate
actually route its listener through it?" B found the asymmetry as a reading-level finding (R-B1) and
recorded it; nobody had turned it into a fix.

**Mechanism contract.**

| Item | Detail |
| --- | --- |
| Load | `runtimecoord.NewGenerationGate(coordinator, label, reset)` at outbound construction (`protocol/*/outbound.go`); nil without a coordinator, and every method is then a no-op |
| A's decision (demand) | `GenerationGate.BeforeDial` → `Snapshot()` → stale? → `applyTransition` → runs `reset` **once per generation**, ledger recorded |
| B's decision (listener) | `route/network.go:1751 notifyNetworkChangeListeners` → `listener.InterfaceUpdated(phaseCtx)` for every outbound, ≤6 s, concurrent |
| Shared state | the gate's `resetAccess`/`resetEpoch`/`resetDone` — the ledger that exists so the action runs once per generation |
| Ownership | the owner (outbound) owns the action; the gate owns *when* it may run |
| Worst case | the action runs twice for one generation: the demand's pass destroys the old session and builds a new one, the listener's pass (a moment later) destroys the new one too, so nothing survives and every network change is followed by a reconnect storm |
| Recovery | a later demand rebuilds the session, so the failure is a stall, not a permanent outage — which is exactly why it looks like "slow reconnect" in a report |

**Attack.** Deterministic, no network: give the owner a gate whose `reset` increments a counter, publish
generation 1, drive the **listener** entry twice, then a new generation and once more. Expected: 1 / 1 /
2. Before the fix the listener never touched the gate at all, so the counter read 0 / 0 / 0.

That counter is the independent observation: the outbound's real reset action is replaced by a
counting callback inside the production `GenerationGate`, so the test observes *whether the gate ran*,
not whether a transport was touched.

**Production path proof.** `InterfaceUpdated` is the method `adapter.InterfaceUpdateListener` names, and
`route/network.go`'s walk calls exactly it. The wrapper added here is that method; the test calls the
same method. The gate is the one the constructor builds.

**Result.** All six owners that owned a gate but bypassed it are fixed; `protocol/trusttunnel` already
routed correctly and is untouched. See `PAIRWISE_C_BUGS_AND_FIXES.md` C-1.

---

## C-2 A reload re-issues fake-IP addresses the previous generation handed out

**The combination.** `dns/transport/fakeip.Store`'s allocator cursor × `adapter.FakeIPIssuanceLedger`.
The two have different lifetimes on purpose: the cursor dies with the Box, the ledger is owned by
whoever owns the sequence of Boxes.

**Why it is not on the A/B lists.** A's fake-IP work was about *attributing a retired address* (the
store's retired intervals, R-B6/R-B7 in B). This is the opposite direction: not "can we prove we issued
it", but "will the new generation hand the same address to someone else".

**Mechanism contract.**

| Item | Detail |
| --- | --- |
| Load | `fakeip.Store.Start`: metadata from the cache file when `store_fakeip` is on, otherwise `MemoryStorage` whose metadata is nil |
| A's decision | the allocator cursor: `create` → `nextAddress` → persist the mapping → record the issuance in the ledger |
| B's decision | `route/route.go:1368-1381` restores a *domain* from whatever the store maps the address to |
| Shared state | the address → domain map, and the address space itself |
| Ownership | the store owns the mapping for its Box; the ledger owns the *issuance history* across Boxes |
| Worst case | with no cache file the cursor restarts at the first address of the range, so the first query after a reload is handed the same address a client is still holding for a different domain — and the router rewrites the destination to the NEW domain. A connection to the wrong host, not a refusal |
| Recovery | none from inside the process: the client's cached DNS answer and the new mapping disagree, and the mismatch persists for the answer's TTL |

**Attack.** Two stores in one process over one ledger, identical range, **no** cache file: the first
issues `held.example` → `198.18.0.2`; the second (the "reload") must not issue the same address, and
must start *past* it. Before the fix the second store returned `198.18.0.2` again.

**Production path proof.** `Start` is the only cursor initialisation, and the ledger is the exact pointer
`service.PtrFromContext[adapter.FakeIPIssuanceLedger]` returns — the same lookup the production path
uses.

**Result.** Fixed by seeding the cursor from the ledger's high-water mark for the current range. See
`PAIRWISE_C_BUGS_AND_FIXES.md` C-2.

**What it does not fix, honestly.** After a *process restart* without `store_fakeip` the ledger is empty
and there is no evidence at all; that case remains covered only by the durable-record path when a cache
file exists (B-5). The fix closes the reload case, which is the one the daemon performs routinely.

---

## 3. The pattern both combinations share

Both are **"two owners of one lifetime, and the shorter-lived one was allowed to restart from zero"**:

- C-1: the gate's ledger and the listener's direct call are two owners of "when may the destructive
  action run"; the listener did not consult the ledger.
- C-2: the allocator cursor and the issuance ledger are two owners of "what has been handed out"; the
  cursor did not consult the ledger.

The generalised question for the next round — and the one this window recommends as a scan rule — is:
*for every (state, history) pair in the tree, does the short-lived state consult the long-lived history
before it reuses an identity?* Addresses, ports, generation numbers, session ids, pool slots and
artificial IP ranges are all identities of this kind.
