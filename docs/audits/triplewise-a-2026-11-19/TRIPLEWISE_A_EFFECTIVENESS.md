# TRIPLEWISE A — Effectiveness (L1–L6)

The brief's six layers, applied to the two triples this round actually attacked. A layer with no
evidence is written as absent, not as "implicit".

## T1 — Close × stale generation × generation pool

| Layer | Question | Evidence |
| --- | --- | --- |
| **L1 load** | are all three mechanisms in the running build? | `transport/v2rayhttp` and `transport/v2raygrpclite` are untagged and always built; the generation registration appears only when a coordinator is in the context (`client.go:118-122`), which the tests supply via `generationTestContext` |
| **L2 entry** | does the production path reach all three? | `Client.DialContext` → `dialHTTP2` → `if c.generationStale() { c.retireGeneration() }` (the stale predicate) → `pool.Retire(newTransport())` (the ledger). `Client.Close` → `pool.CloseAll()` + `removeRegistration()` (the teardown). The test drives those same methods, not stand-ins |
| **L3 decision** | do the three see the same state? | the stale answer and the client's `closed` flag are two observations of one teardown; the test asserts `generationStale()` is true after `Close` **before** dialing, so a passing run cannot come from the dial path being unreachable |
| **L4 action** | did the release actually happen? | a counting `newTransport` factory observes whether a generation was built (`created = 0` after the fix, `1` before); `GenerationPool.Snapshot()` observes whether the ledger adopted the offer (`sequence` unchanged, `terminated + 1`) |
| **L5 effect** | is the resource actually gone? | with no generation installed, no physical connection is opened for a dead client. Before the fix the late dial opened a real TCP connection to the test server on a transport nobody owned — the server's accept counter is the independent witness, and the late conn's read is what makes the goroutine finish so the ledger assertions are not racy |
| **L6 end/recovery** | does the system settle? | after the late conn settles, `Snapshot()` reports `CurrentInflight == 0`, `RetiredDraining == 0`, `RetiredInflight == 0` (asserted with a bounded `Eventually`), and the pool is closed for good — a later dial cannot reopen it |

**Negative controls.**

| Control | What was disabled | Result |
| --- | --- | --- |
| MC-T1a | the whole fix (pre-fix run) | `TestALateDialAfterCloseInstallsNoGeneration` red: "installed a new transport into a pool that has already released everything"; `TestARetireOnAClosedPoolReleasesWhatItIsGiven` red: `expected 0x1, actual 0x2` |
| MC-T1b | only the pool's `closed` branch, keeping the client guard | the pool test red with the same `sequence` mismatch — the backstop is load-bearing, not decorative |

Both mutations were reverted; the tree was confirmed clean before the commit.

**What this does not prove.** The concurrency window between `Close` and a dial in another goroutine is
covered by the flags, but the test that exercises it sequentially; the instruction-wide race is what the
pool backstop is for and is not driven by a barrier. Recorded as a limit of the evidence, not as a claim.

## T2 — OOMKiller × admission × generation

| Layer | Evidence |
| --- | --- |
| **L1** | `service/oomkiller` installs the guard only in Network Extension mode (`service.go:96-109`); the tests build the same pair |
| **L2** | the pressure atomic is written only by the OOMKiller (three sites, grepped); `guardLevel` is the only admission mapping; the TUN stack reads the same atomic |
| **L3** | there is no third opinion to compare: the generation machinery never touches the pressure state — proven by the grep rather than by an argument |
| **L4** | a published Critical is refused **before** the expensive creation (window A's pairing test, with MC-1); the transition's destructive action runs once per generation (window C's six owner tests) |
| **L5** | the refusal is classified as a local rejection, so it cannot be mistaken for a node failure; the recovery edge admits again without intervention |
| **L6** | pressure clearing restores admission, observed in the same test |

**Absent by construction:** the cross-product test (Critical held mid-transition while a real pool
rotates). Its absence is stated in the matrix as a gap; nothing in this document claims it.

## The L1-L6 rule applied to the rest of the brief's list

For every row classified `NOT_OBSERVABLE` (A02, A09, A14) the reason is the same and is a platform fact,
not an omission: the measurement the row demands is Apple `phys_footprint` / a native allocation count /
a device class that does not exist in this environment, and substituting Go heap or Linux RSS would be
the "假绿" the brief forbids. For `NOT_COVERED` rows (A04, A08, A10, A13) the reason is a missing
harness or a dependency fix, and each is named.
