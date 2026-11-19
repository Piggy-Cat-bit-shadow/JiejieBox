# Window C（扩大搜索：自主探索与修复）——执行状态

Window C asks for autonomous research rather than a checklist, and for the five "wonder" reasoning
patterns in C-3 to be used as methods. This page answers it with what the session actually produced,
and states the one deliverable that is **BLOCKED** (external Issue/PR intelligence) instead of
inventing it.

Base: `testing` @ `54f5f2b80`.

## C-1 / C-2 — the autonomy, and what it was used on

The licence was used for eleven sweeps that no prompt listed. Each one is a query over the tree rather
than a topic: *which pools lack a closed state*; *which measurement outlives its object*; *which
acknowledgement is not atomic with its comparison*; *which package has tests that no workflow runs*;
*which marker is indistinguishable from "never built"*. Their products are the sixteen fixes in
`docs/audits/README-overview-2026-11-19.md` and the ten patterns in
`docs/audits/pairwise-c-autonomous-redteam-2026-11-19/PAIRWISE_C_PATTERNS.md` and
`triplewise-c-2026-11-19/TRIPLEWISE_C_PATTERNS.md`.

## C-3 — the five patterns, each answered with a real result

**Pattern one: a DNS stall may be a system-wide queue problem.** Answered by window A group 2: the DNS
serial path is unconditional (`dispatch`), and `ConnPoolOrdered` holds at most one connection out, so
the stall's blast radius is one socket by construction. The related "one network's cache must not answer
the next network" family has twelve named tests (`cache_generation_guard_test.go`,
`cache_remote_environment_test.go`), all green. The systemic explanation exists and is bounded; no
defect.

**Pattern two: "memory never drops" has three different causes.** Answered by what this session
deliberately did *not* claim: every iOS/NE row is `NOT_OBSERVABLE`, and no Go-heap number was substituted
for `phys_footprint`. What could be measured was: the resource lifecycle round's four fixes, the
`-race` runs on the core state machines, and the release-profile test sweep.

**Pattern three: a Go 1.27 compatibility question can reveal an assumed contract.** Answered by window
B's B-1: Go 1.27's `defaultMaxReadFrameSize` moved the TrustTunnel HTTP/2 preface away from Chrome's,
and the repository now pins `chromeMaxFrameSize` with a test that fails if the toolchain default is used
again. Generalised into pattern P5 ("the toolchain is part of the wire").

**Pattern four: a QUIC data-loss report may be backpressure plus ownership.** Answered by window B group
5's new regression: a cancelled stream must fail alone, and making the session detach on any stream
close turns the test red. Group 2 (send queue at `full`) is `NOT_COVERED` and said so — the ownership
half is pinned, the backpressure half still needs a peer with a controlled receive window.

**Pattern five: a fix already present in a fork implies a neighbour that was not fixed.** This produced
three of the sixteen: the atomic publish decision applied to the three sibling pools (R-B2) and then to
`transport/v2rayquic` (C-T5); the closed-state applied to the QUIC pool and then to
`common/httpclient`'s `ManagedTransport` (the sixteenth). The remaining members of that family are
named in `TRIPLEWISE_C_PATTERNS.md` §P9.

## C-5 — the deliverables

| Asked | State |
| --- | --- |
| 新增情报清单（Issue/PR links, dates, states） | **BLOCKED.** The search returned documentation pages only and the git remotes reset repeatedly; no issue or PR is cited anywhere in this session, and none is invented |
| 自主发现并验证的问题（scan 50+, deep-dive < 20） | 16 confirmed and fixed, each with a mutation control; the per-round counts are in the index page |
| 新问题的延伸链（shared root cause, neighbouring call paths, next hypotheses） | P1–P10, each with the instances that produced it and the next places to look |
| 跨仓库修复闭环（main line, version pin, tests, evidence） | **BLOCKED** for `sing-tun` (UDP NAT ownership) and for the Darwin/Cronet slice: the pin bump's release gate requires a CI run for the new revision, which cannot be produced here. The in-repo half is identified in `PAIRWISE_C_RELEASE_READINESS.md` §5 |
| 正式版门禁与剩余风险（Release Blocker vs PLATFORM_PENDING） | `PAIRWISE_C_RELEASE_READINESS.md`: pin lock PASS, knowledge gate PASS, `go vet ./...` clean, four cross-builds (the Darwin slice BLOCKED), 20 touched packages green under the shipped Windows profile, `-race` on runtimecoord/dialer/route green, census 8 of 109 with a ratchet |

## The expanded search's new result: a recorded risk that was already mitigated

The round's one new finding is a **correction of our own record**, and it changed a release decision.

Window B recorded R-B9 as "a semantic validation failure leaves nothing running until config recovery",
which read like a reason to redesign the reload ordering before shipping. It is not. With a config
store and `recoverToLastGood` — the desktop configuration — `buildAndStart`'s error branch calls
`tryRecoverToLastGood`, rebuilds from the stored document, records the recovery, and reports
`ServiceChangeStatusRecovered` rather than `Failed`. That is exactly R-B9's scenario, and the daemon
already had a test for it:

```
go test ./daemon -run 'TestABuildFailureAfterAParseableCandidateRecoversToTheStoredDocument|...' -v
--- PASS: TestABuildFailureAfterAParseableCandidateRecoversToTheStoredDocument  0.33s
--- PASS: TestTheRecoverySurvivesAProcessRestart                                0.31s
--- PASS: TestAFailedStartWithoutRecoveryReportsThatNothingRuns                 0.20s
--- PASS: TestAFailedRecoveryReportsThatNothingRuns                             0.19s
--- PASS: TestAFailedStartRecoversToTheLastGoodConfiguration                    0.31s
--- PASS: TestARecoveredStartIsNotSuccessAndReturnsNoError                      0.31s
ok  github.com/sagernet/sing-box/daemon  1.813s
```

The residual is a **capture gap and a status change**, not a dead service, and the no-store case is an
explicit branch with its own test rather than an unexamined hole. So the reload keeps its ordering: a
build-then-swap would hold two Boxes — registries, device claims, ports — alive at once, which is a
larger risk than the gap it removes. `PAIRWISE_B_FINDINGS.md` §R-B9 and the release-readiness table are
corrected accordingly.

The lesson is the one this window was created to produce: **an audit record is a hypothesis too.** It
was written from a source read that stopped one call frame short of the recovery path, and it survived
into the release deck unchallenged until a window with "go and look wider" as its mandate checked it.

## What this window did not do

It did not scan the 60 KB appendix seed by seed, and it did not produce external intelligence. Both are
stated rather than papered over: the six window prompts (pairwise A/B/C, three-system A/B/C) and the
three release-window prompts are now all either executed with evidence or explicitly classified, and the
one deliverable that depends on the network — upstream Issues and PRs — is the only one marked BLOCKED.
