# Self-check (closure round)

PASS means executed in this environment with the cited evidence; BLOCKED means the
environment cannot execute it; N/A means the item does not apply to this round.
The full A–G checklist is summarised by group; every FAIL is either fixed in this
round or carried as an explicit blocker in `CLOSURE-REPORT.md`.

| group | result | evidence / note |
|---|---|---|
| A Git/worktrees/deps | PASS | branch `work/closure-2026-11-11`, HEAD in report, clean tree, merge of `testing` logged; root+test go.mod and both release locks agree on `7ece3d66d79d`; no unrelated upstream merge, no client gitlink migration |
| B lab correctness | PARTIAL | B01–B03 PASS (v1.1 schema, 48/48 history, fixtures fail-closed, matrix hook); B04–B07 **BLOCKED/DEFERRED** (BuildInfo provenance design only; `-buildvcs` unavailable in this worktree); B08 PASS for the closure scope (B7 rerun only, stage tables untouched); B09 PASS (null never zero, `go_heap` remains null); B10–B12 PASS (netlab cleanup unchanged, failures excluded from goodput, drain/window caveat documented in UDP audit) |
| C UDP correctness | BLOCKED | instrument audit complete (14 false-loss mechanisms); paced/sequence/drain mode designed, not implemented; B8 unchanged as UNRESOLVED |
| D WireGuard/AWG | PASS | fork wake fix with 3 internal tests; 36/36 two-process cells pass on the new pin; both WG and AWG; no drop/timeout/queue-size workaround |
| E Mieru | PARTIAL | S3-9 reproduced 1/20 under race, 0/20 normal; proxy ruled out as the cause; no unsafe retry added; memory/FD deep dive deferred until paced UDP exists |
| F anti-coupling | PASS | no new manager/interface/global lock; F-03/F-04/F-07/F-08 regressions still pass; F-01/F-05/F-06/F-10/F-13/F-14/F-15/S3-7 unchanged and recorded |
| G delivery | PASS | report/worklist/evidence index + raw B7 runs + commit list; unrun platform cells explicitly NOT_RUN; no merge, no release, no force-push |
