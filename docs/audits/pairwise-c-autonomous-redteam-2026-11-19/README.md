# Pairwise C — autonomous red team (2026-11-19)

Window C of the JiejieBox pairwise-debug effort, continuing on `testing` from window B's head
(`86140a161`). Its brief was to find risk the A/B lists did not cover, prove it on a real production
path, and fix what is provable.

## What this window did

Two of window B's "recorded, not fixed" findings were turned into work items and closed:

| Finding | Was | Now |
| --- | --- | --- |
| **R-B1 / C-1** | the generation gate's listener half existed in **1 of 7 owners**, so a destructive transition ran twice per generation and the second pass destroyed the session the first one built | all six remaining owners route `InterfaceUpdated` through `gate.NetworkChanged`; each has a discriminating test and a mutation control |
| **R-B8 / C-2** | a reload without `store_fakeip` restarted the fake-IP cursor and **re-issued an address a client held for another domain**, silently delivering the connection to the wrong destination | `Store.Start` resumes past the issuance ledger's high-water mark for the current range; two tests, one mutation control |

Both are on the production path for every user, both were verified without a device, and both were
fixed with the "does the test go red when the mechanism is disabled" control the brief requires.

The window's discovery lesson: **a recorded finding with a `file:line` and a test sketch is a work
item, not a note.** Neither fix needed new investigation — the diagnosis was already on disk.

## Files

| File | Contents |
| --- | --- |
| [`PAIRWISE_C_DISCOVERY_CATALOG.md`](PAIRWISE_C_DISCOVERY_CATALOG.md) | sources read vs blocked (including the honest note that the upstream Issue/PR circle was unreachable), the 20-candidate table with weights and dedup verdicts |
| [`PAIRWISE_C_NEW_COMBINATIONS.md`](PAIRWISE_C_NEW_COMBINATIONS.md) | the two selected combinations: shared state, mechanism contract, attack design, production-path proof, and the pattern they share |
| [`PAIRWISE_C_BUGS_AND_FIXES.md`](PAIRWISE_C_BUGS_AND_FIXES.md) | trigger, root cause, why existing tests missed it, the safety argument for C-1, fix, regression and mutation control per defect |
| [`PAIRWISE_C_EXECUTION_EVIDENCE.md`](PAIRWISE_C_EXECUTION_EVIDENCE.md) | every command and result, the two mutation controls, and what was *not* executed with the reason |
| [`PAIRWISE_C_PATTERNS.md`](PAIRWISE_C_PATTERNS.md) | eight failure shapes across A/B/C (P1…P8) as scan rules, the "test that runs nowhere" census procedure, and the recommended next-round order |

## Cross-window tally

| Window | Fixed | Recorded, not fixed |
| --- | --- | --- |
| A — resources / lifecycle | 4 | 8 |
| B — network / DNS / TUN | 5 | 8 (2 now fixed by C) |
| C — autonomous | 2 | — (the remaining B records stay) |

## Reproduce

```sh
go test ./protocol/vless ./protocol/vmess ./protocol/trojan ./protocol/hysteria2 ./protocol/tuic ./protocol/masque
go test ./dns/transport/fakeip ./dns/...
go test ./protocol/vless -run TestInterfaceUpdatedRoutesThroughTheGenerationGate -v          # 6 packages, same name
go test ./dns/transport/fakeip -run 'TestAReloadWithoutACacheFile|TestConcurrentQueriesForOneDomain' -v
```
