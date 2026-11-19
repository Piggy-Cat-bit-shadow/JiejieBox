# Final status at one SHA — and which earlier record each line supersedes

**This page is the authoritative status.** Earlier pages were written as the work happened and some of
them say things that are no longer true; where that is the case, the line below names the page it
replaces. Treat any statement not listed here as detail, not as status.

Base: `testing`, HEAD at the time of writing is recorded in `../git/head.txt` of the review bundle. The
range that produced this state is `7654f1a77..HEAD`.

## 1. What was changed

**Sixteen code changes, of which fifteen are production defects and one is a test-assertion correction.**
The earlier summaries counted all sixteen as "fixes", which overstated the defect count; the audit
flagged it and the number is corrected here.

| # | Change | Kind |
| --- | --- | --- |
| 1 | naive no longer registers a pressure hook that terminates live tunnels | defect |
| 2 | the admission gate hands recovered capacity to callers already queued | defect |
| 3 | hy2/tuic carriers have a shut-down state | defect |
| 4 | two xhttp pool tests measured a cold-pool artefact; the assertions were corrected | **test correction** |
| 5 | the TrustTunnel HTTP/2 preface is pinned to Chrome's under Go 1.27 | defect |
| 6 | a network change no longer unregisters the HTTP pool | defect |
| 7 | fake-IP never issues an address outside its configured range | defect |
| 8 | the TUN guard recognises a placeholder from a retired generation | defect |
| 9 | the durable fake-IP retirement record is read with no fakeip server configured | defect |
| 10 | the generation publish decision is one atomic call (three pools) | defect |
| 11 | every gated outbound routes its listener through the generation gate | defect |
| 12 | a reload no longer re-issues the addresses its predecessor handed out | defect |
| 13 | a generation offered to a closed pool is released, not adopted | defect |
| 14 | a replaced or removed node loses its predecessor's URL-test measurement | defect |
| 15 | the QUIC pool's publish and acknowledgement are one decision (and its migration path) | defect |
| 16 | a shared HTTP transport refuses to rebuild itself after `Close` | defect |

Plus, with no product-code change: three new regression files in the QUIC transport (stream isolation,
send-queue recovery, PMTU black hole), five contract tests for the fake-IP NAT wrapper, the CI census
ratchet, the dependency upgrade, and the boundary tests listed in §3.

## 2. Corrected records

| Earlier page said | Now |
| --- | --- |
| `README-overview` listed the PR/Issue prompt set as unread and R-B2 as unfixed, and `PAIRWISE_B_FINDINGS` said reload needed a redesign | **All three are superseded.** The PR/Issue windows were read and executed (`pr-issue-windows-2026-11-19/WINDOW_{A,B,C}_STATUS.md`); R-B2 is 4 of 4 (`PAIRWISE_B_FINDINGS.md` §R-B2); reload recovers automatically and needs no redesign (same section, R-B9) |
| `TRIPLEWISE_C_EXECUTION_EVIDENCE` said the round added no code and no mutation control | **Superseded.** The same round went on to add C-T5 (QUIC publish) and the `ManagedTransport` closed state, each with a mutation control (`TRIPLEWISE_C_FINDINGS.md` §C-T5 and the sixteenth fix) |
| `TRIPLEWISE_A_MATRIX` presented its 8-cell table as proof that the triple is irreducible | **Reframed.** The table is a before/after control matrix; the third factor in it is the *fix*, not the `GenerationPool`. The irreducibility argument is now made separately from the code (`Registration.Stale()` reports a closed registration as stale forever) |
| This session said "12 prompt files" | **Nine task files**: three directories (`pairwise`, `三系统`, `PR_Issue`) × A/B/C. All nine were read; every one is executed with evidence or explicitly classified |
| `WINDOW_B_STATUS` group 1 said `NO_DEFECT by coverage` for H3 small-flow fairness | **`PARTIAL / NOT_COVERED`.** A passing suite is not a fairness measurement; P95/P99, timeout rate, queue bound and recovery were not measured |
| "16 defects fixed" | **15 defects + 1 test correction** (§1) |

## 3. Boundaries that are characterized, not fixed

- **`/30` fake-IP ranges collide.** A two-address pool has one reachable address (the allocator starts
  at +2), so every domain shares it and `Lookup` answers with the newest. `/29` and larger allocate
  distinct addresses. Pinned by `dns/transport/fakeip/store_boundaries_test.go`; the alternatives are a
  configuration decision (reject tiny ranges) and a wire-compatibility decision (change the start
  offset), neither taken.
- **The reload guarantee is qualified.** The cursor resumes past the previous generation's high-water
  mark *for the configured range*; a range that shrinks or moves inherits nothing (correct, because the
  old addresses are not in it), and an exhausted pool has nowhere to go. Three tests pin those edges.
- **The concurrent allocation test now actually is concurrent.** The earlier name said "concurrent"
  while the body called `Create` twice in sequence; the sequential case is renamed and a barrier-based
  sixteen-caller test runs under `-race`.
- **The UDP NAT eviction gap is not closed.** An evicted entry still does not cancel the dial it
  launched. A patch exists and builds against the exact pinned revision (`pairwise-a-.../patches/`), but
  its behaviour is unverified and it is not pinned; the consumer-side reduction of scope is recorded in
  `PAIRWISE_C_RELEASE_READINESS.md` §5.

## 4. Dependencies

**`govulncheck` reports 0 reachable vulnerabilities.** Thirteen were reachable; the fix is the upgrade
(`x/net v0.62.0`, `x/crypto v0.58.0`, `grpc v1.83.2`, `brotli v1.2.7`, `compress v1.20.2`) plus
`go 1.26.0` and `godebug tlssecpmlkem=0` in `go.mod`, which keeps the ClientHello at the pre-1.26 group
set. Measured with the repository's own probe; the full scans are in
`pairwise-c-autonomous-redteam-2026-11-19/govulncheck-after-upgrade*.txt`.

Not proven: the *artifact-level* handshake of each client (Apple/Windows system engine, REALITY, uTLS,
Cronet). The probe covers `supported_groups`/`key_share` of the default client, not the whole ClientHello
of every real path. `godebug` also applies to this main module only — an embedder's own `go.mod` needs
the same line.

## 5. Test and CI state, honestly

- `go test -race ./transport/trusttunnel` → **ok 35.4 s**, no hang (`trusttunnel-race-full.txt`). The
  historical hang is **not reproduced** and remains unexplained; it is not counted as a current failure,
  and if it returns, the goroutine dump — not a guess about the environment — is what should be recorded.
- The census's **7 of 109** means: of 109 packages that contain tests, seven match no `go test` command
  in any workflow. It does **not** mean the other 102 executed successfully in CI. The script is a gap
  finder; it does not verify triggers, conditions, platforms, tags, `-run` subsets, skips or results.
- The release-profile transcripts are labelled by state: `release-profile-tests.txt` is the Windows run
  before the toolchain/dependency changes; `release-profile-tests-after-upgrade.txt` is the final one
  (21 packages, root 28.9 s, daemon 45.4 s). Do not mix their numbers.
- Not measured, and not to be inferred from any Windows result: Darwin builds, iOS Network Extension
  behaviour, `phys_footprint`, native/Cronet allocation, and privileged packet capture.

## 6. Release judgement

The resource/ownership/generation/Fake-IP/lifecycle family is in materially better shape and every
change carries a test that fails without it. What remains before a release is: the sing-tun eviction
behaviour and pin, an artifact-level handshake comparison, the device/platform rows, and the deliberately
recorded product decisions (Critical-UDP admission policy, H3 demotion observability, tiny-range
rejection). None of those is a reason to undo the work; all of them are reasons not to call the original
task list complete.
