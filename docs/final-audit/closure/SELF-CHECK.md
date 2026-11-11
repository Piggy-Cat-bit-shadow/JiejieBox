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


## Three self-check rounds (executed)

- **Round A — functional closure:** B7 fixed in the fork and re-verified from two
  processes (36/36); schema v1.1 validated over all 48 historical runs; paced UDP
  instrument implemented and calibrated at 0 % loss; S3-9 reproduced 20×; release
  locks moved to the new pin.
- **Round B — reverse/negative checks:** invalid schema fixtures fail closed;
  `-race` ×20 re-ran S3-9 (1 failure, recorded, not hidden); B7 re-ran with
  simultaneous and 200 ms starts (the cases that used to fail) and passed; UDP
  0 %/0.1 %/1 % was run at a fixed offered rate with skipped/drain checks, which
  is what turned "UDP has no retransmission" into the measured 1 % amplification;
  the full lab suite and schema history were re-run after the test-suite commit.
- **Round C — independent acceptance:** final tree verified at
  `98d2b8bee1419903e983cb97a807377045a02ecf`; `gofmt/vet/test` clean, schema
  self-test 0 wrong + 48/48 historical, `protocol/wireguard` and
  `transport/wireguard` pass on the new pin, and the kernel binary builds with
  the laboratory tags. Remaining items are the explicit blockers in
  `CLOSURE-REPORT.md`.
