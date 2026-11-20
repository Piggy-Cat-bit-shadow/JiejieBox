# Triple-wise A — QUIC / listener recovery / HTTP lifecycle: matrix

Window A of the three-system round: which THIRD mechanism really participates in a causal chain, what
was executed, and what the evidence shows. A third system counts only if it changes the decision, the
ownership, the ordering or the recovery - not if it merely runs alongside.

- Repository: `Piggy-Cat-bit-shadow/JiejieBox`, branch `testing`, start `bd0c47ac7` (the state the
  three pairwise windows left). Commits added: `e8a74e41f` plus these reports
- Platform: Windows amd64, Go 1.27.4; no Administrator token, no Linux netns/nftables, no iOS/macOS
  device, no PREF64/NAT64 network
- Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` / `NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`

## Summary

| Row | The three systems (repo) | Shared object | Why the third is not optional | Conclusion | Commit |
| --- | --- | --- | --- | --- | --- |
| T11 | ① the Go toolchain's `tlssecpmlkem` default · ② the linker's `-X runtime.godebugDefault` override, owned by TWO strings (`cmd/internal/build_shared`, `release/LDFLAGS`) · ③ the shipped artifact path (`build_libbox`/`build_boxdd` vs `cmd/sing-box`) | the effective GODEBUG default, and through it the key-share list in every ClientHello | without ③ the two strings never meet: `cmd/sing-box` and the clients are built by different commands. Remove ② and the go.mod `godebug` line applies to both; keep ② short and only the clients lose the pin | **FIXED** (and the CI path filters that never rebuilt the artifacts are fixed with it) | `e8a74e41f`, `ci: rebuild on shared-flag changes` |
| T04 | QUIC connection · stream lifetime · network Reset | the QUIC path/stream state | the fork implements no client-side migration (`docs/fork/interface-update-lifecycle.md`), so a client "migration" cannot be staged; the server-side NAT-rebinding path belongs to quic-go's own suite | **NOT_COVERED** (premise does not exist on the client side) | — |
| T07 | `sing-tun` netlink monitor · `NetworkManager` transition · outbound pools | the monitor's socket, the epoch, the pools' publish decision | the monitor is Linux-only; the last round already closed its recovery/Close window in the fork and bound it to executed races | **NOT_OBSERVABLE** on this host (pointer to the prior evidence) | — |
| T08 | a response body that never ends · one stream cancelled · pool retirement/trim | the stream's slot and the carrier's Active/Idle reading | the retirement decision is made from a count the cancel and the body both affect; the pairwise windows ran its halves | **NO_DEFECT** as a triple (both orders, h2+h3, mutation control) | `77d6884d0` |
| T06 | MASQUE PREF64 · DNS bootstrap · QUIC path | the address the tunnel dials and the family it is built on | needs a PREF64/NAT64 network and an IPv6-only bootstrap | **NOT_COVERED** / **NOT_OBSERVABLE** here | — |

## T11 — the reducible control matrix (executed)

`scripts/ci/clienthello-probe` prints the key-exchange groups a binary's `crypto/tls` client offers.
The three builds below differ only in the ldflags string and the `go` line:

| Module | `-X runtime.godebugDefault` | offered groups | Reading |
| --- | --- | --- | --- |
| `go 1.25.5` + `godebug tlssecpmlkem=0` | none | `[4588 29 23 24 25]` | the pre-1.26 camouflage set |
| `go 1.25.5` + same | `multipathtcp=0,tlssha1=1` (the old `build_shared` list) | **`[4588 4587 4589 29 23 24 25]`** | the short `-X` alone re-enables the post-quantum groups; the module's pin is irrelevant |
| `go 1.26.0` + same | none | `[4588 29 23 24 25]` | the module pin alone is enough |
| `go 1.26.0` + same | `multipathtcp=0,tlssha1=1` (old) | **`[4588 4587 4589 29 23 24 25]`** | the defect, on the language version the tree now declares |
| `go 1.26.0` + same | `multipathtcp=0,tlssha1=1,tlssecpmlkem=0,tracebacklabels=0,x509sslcertoverrideplatform=0` (`release/LDFLAGS`) | `[4588 29 23 24 25]` | the fixed list |

The second row is the control that makes the toolchain axis irrelevant: `-X` is a total replacement,
not a merge, so once the short list is present the language version cannot restore the pin. That is
why the matrix is three points rather than a full 2³: the entity under test is the *string*, and the
two strings that exist in the tree are exactly the last two rows.

## T11 — the two owners, and the artifact that ships

| Build path | ldflags source | Artifact | Before | After |
| --- | --- | --- | --- | --- |
| `cmd/internal/build_libbox` | `build_shared.LinkerFlags` | Apple `Libbox.xcframework`, Android libbox - the library **every client links** | 7 groups | 5 groups |
| `cmd/internal/build_boxdd` | `build_shared.LinkerFlags` | the desktop daemon | 7 groups | 5 groups |
| `cmd/sing-box`, `scripts/ci/*.sh` release recipes | `release/LDFLAGS` | the server/kernel binary | 5 groups | 5 groups |

So the divergence put the fingerprint change on the client side - the side whose TLS camouflage
matters - while the server side kept the intended set. Neither owner is wrong alone: the release list
is complete, and the builder list was written before the go line moved. The third system is the
artifact split.

## What was executed

| Command | Result |
| --- | --- |
| five `clienthello-probe` builds (matrix above) | groups as recorded |
| `go test -count=1 ./cmd/internal/build_shared/ ./cmd/internal/build_libbox/ ./cmd/internal/build_boxdd/` | `ok` 0.083 s / 2.764 s / 0.078 s (the new consistency test included) |
| mutation control: the short list restored in `flags.go` | `TestLinkerFlagsCarryTheReleaseGODEBUGDefaults` fails with the two differing strings |
| tagged whole-repository build | exit 0 (previous window's run plus this change is a build-helper constant) |

## Not attacked, and why

- **T04**: the client does not migrate QUIC connections in this fork; a "migration × stream × reset"
  interleaving cannot be staged from the client. What exists is the per-protocol reset on a network
  change, which is `pairwise-b`'s B-P14 work.
- **T07**: the Linux netlink monitor is not observable on Windows; its recovery and Close windows were
  closed in the fork and pinned by its own tests (the pin is recorded in `release/pin-runs.json`).
- **T08**: executed on the TrustTunnel carrier and `NO_DEFECT` (both orders, h2+h3, with a mutation
  control); the `common/httpclient` variant is correct by construction (the epoch is released by the
  response body's Close) and was reasoned from its accounting rather than attacked.
- **T06**: needs a PREF64/NAT64 network; `NOT_OBSERVABLE` here.
