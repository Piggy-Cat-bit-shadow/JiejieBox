# Triple-wise A — QUIC, listener recovery and the HTTP lifecycle

First window of the three-system round. The question is not "does each transport work" but **which
third mechanism changes the outcome of a coordination the first two already handle**.

- Repository: `Piggy-Cat-bit-shadow/JiejieBox`, branch **`testing`**, no new branch
- Starting HEAD: `bd0c47ac7` (the state the three pairwise windows left). Commits added:
  `e8a74e41f` plus these reports
- Platform: Windows amd64, Go **1.27.4**; no Administrator token, no Linux netns, no device, no
  PREF64/NAT64 network
- Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` / `NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`

## 0. Result in one paragraph

One production defect was reproduced and fixed, and it is a three-system defect in the strict sense:
the **Go toolchain's** `tlssecpmlkem` default, the **linker's** `-X runtime.godebugDefault` override -
which exists in two strings owned by two different builders - and the **artifact split** that decides
which of the two strings a given binary gets. The override *replaces* the string the go command
computes from go.mod instead of merging, so the builder used for the Apple/Android **libbox** and the
desktop **boxdd** silently dropped the `tlssecpmlkem=0` pin that `release/LDFLAGS` keeps for
`cmd/sing-box`: every client library shipped after the go line moved to 1.26 offered two extra
post-quantum key-share groups while the server binary kept the pre-1.26 camouflage set. Measured, not
argued: `[4588 29 23 24 25]` vs `[4588 4587 4589 29 23 24 25]`, and the control arm shows a short
`-X` re-enables the extra groups even at language version 1.25. The other four triples of the brief
are recorded as `NOT_COVERED` or `NOT_OBSERVABLE` with the reason each could not be staged here; none
is claimed as passed.

## 1. FIXED — T11: two build paths, two ClientHellos

**The three systems.**

1. **The toolchain.** Go 1.26 turned `tlssecpmlkem` on by default; this product's camouflage is the
   pre-1.26 key-share set, so `go.mod` pins `godebug tlssecpmlkem=0`.
2. **The linker override.** `-X runtime.godebugDefault=...` REPLACES that computed string. Two
   strings exist in the tree: `release/LDFLAGS` (complete) and `cmd/internal/build_shared`'s
   `LinkerFlags` (only `multipathtcp=0,tlssha1=1`).
3. **The artifact split.** `cmd/internal/build_libbox` (the Apple/Android library every client links)
   and `cmd/internal/build_boxdd` (the desktop daemon) take their ldflags from `build_shared`;
   `cmd/sing-box` and the release recipes read `release/LDFLAGS`.

No two of them produce the defect. Without the override, the module pin applies to both paths. With
the override but one builder, there is nothing to diverge. With both, the artifact split decides who
loses the pin - and it is the client side that loses it.

**Trigger and impact.** Build libbox or boxdd through the repo's own commands: the resulting binary
offers `[4588 4587 4589 29 23 24 25]` (X25519MLKEM768, **SecP256r1MLKEM768**, **SecP384r1MLKEM1024**,
X25519, P-256/384/521) where the release server binary offers `[4588 29 23 24 25]`. The clients
therefore present a different TLS fingerprint from the one the product designed, to exactly the
observer the camouflage exists for. Nothing fails, no log line changes - which is why it survived.

**Fix.** The two lists are now one named constant in `cmd/internal/build_shared`, byte-identical to
`release/LDFLAGS`, with the replace-not-merge semantics written next to it. A new test,
`TestLinkerFlagsCarryTheReleaseGODEBUGDefaults`, reads `release/LDFLAGS`, extracts both
`runtime.godebugDefault` values, and requires them to be equal for the release and the debug flag
shapes, plus the presence of `tlssecpmlkem=0`. The two owners can no longer drift without a red test.

**Evidence.**

| Stage | Evidence |
| --- | --- |
| measured before | short `-X`: `groups=[4588 4587 4589 29 23 24 25]`; no `-X` and complete `-X`: `[4588 29 23 24 25]` |
| control arm | the same short `-X` at `go 1.25.5` also yields the 7-group set: the override, not the language version, decides |
| red (test) | the new test failed against the old string: `expected: "multipathtcp=0,tlssha1=1,tlssecpmlkem=0,tracebacklabels=0,x509sslcertoverrideplatform=0" actual: "multipathtcp=0,tlssha1=1"` |
| green | `ok github.com/sagernet/sing-box/cmd/internal/build_shared`; the two builder packages' own suites are green (2.76 s / 0.08 s) |
| mutation control | restoring the short list turns the test red again with the same diff |

**Six-stage check.**

1. *Loaded*: `build_libbox` is invoked by `android-core-arm64.yml:256` and `build-apple-libbox.sh:188`;
   `build_boxdd` by the desktop client workflow. Both call `build_shared.LinkerFlags`.
2. *Reached*: the constants reach the linker through the commands' `-ldflags` argument, i.e. the exact
   string the compiler frontend uses.
3. *Decided*: the decision is "which defaults are in force"; the `-X` value replaces the computed one.
4. *Acted*: the runtime reads `runtime.godebugDefault`, and `crypto/tls` builds the key-share list
   from it - measured on the wire by the probe.
5. *Observed*: the offered groups, printed from the ClientHello bytes.
6. *Settled*: the test binds the two owners; the four extra/short strings cannot coexist silently.

**Why the fix is not a behaviour change for the server.** `cmd/sing-box` already used
`release/LDFLAGS`; the constant is now the same value, so only the two client-side builders change,
and they change to the value the product documents.

## 2. The other triples of the brief

| Row | Why it is not a pass here |
| --- | --- |
| T04 QUIC migration × stream × Reset | the fork implements no client-side QUIC migration (`docs/fork/interface-update-lifecycle.md` records the audit): the client does not move a QUIC connection across paths, so "migration × stream × reset" cannot be staged from our side. The network-change reset is `pairwise-b`'s B-P14, fixed and tested there |
| T07 netlink recovery × `NetworkManager` × pools | the netlink monitor is Linux-only. Its reopen/backoff/Close windows were closed in the `sing-tun` fork and pinned by executed races at the pinned revision (`release/pin-runs.json`) |
| T08 body × cancel × pool retirement | the pairwise windows executed its halves: a stalled body with a trim, a live stream across a retirement, and the reserved-slot window, all with mutation controls (`pairwise-a` §4.4 and §2). The full three-way interleaving - a third flow cancelled while a body is stalled and the pool is being retired - was not built |
| T06 MASQUE PREF64 × DNS bootstrap × QUIC path | needs a PREF64/NAT64 network and an IPv6-only bootstrap; `NOT_OBSERVABLE` on this host |

## 3. Gates

`cmd/internal/build_shared`, `build_libbox` and `build_boxdd` suites are green; the tagged
whole-repository build is unaffected (the change is a build-helper constant). No dependency pin moved,
so `release/source-lock.json` and `release/pin-runs.json` are untouched. Branch state and the CI
evidence for the preceding windows are in `docs/audits/next-cross-debug/pairwise-a/REPORT.md` §5.

## 4. New direction this window opened

The defect is an instance of a general shape: **the project has two ldflags owners, and only the
release one is tested for its wire effect.** The new consistency test binds them, but the same
question applies to every other ldflag in the two strings (`checklinkname`, `-s -w`, the version
stamp): if a future flag carries a wire or ABI consequence, it will drift the same way. The next
round could extend the test to compare the *whole* effective ldflags set, and add the probe as a
push-time gate for `build_libbox`'s output (it is currently only measured by hand and in the release
notes).
