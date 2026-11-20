# Quadruple C — autonomous combinations: matrix

Window C of the four-system round: self-invented combinations, no assigned numbering. Base:
`testing` @ `4f65b1f4b`. Commits added: the artifact probe plus these reports.

- Platform: Windows amd64, Go 1.27.4; no Administrator token, no Linux netns/nftables, no iOS device
- Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` / `NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`

## 1. The seed this window actually attacked

| Seed | The four systems | Verdict | Evidence |
| --- | --- | --- | --- |
| **X-Q06** | release `LDFLAGS` · the built artifact · the TLS stack · the wire the server sees | **`NO_DEFECT`** (the shipped binary sends the camouflage set), with a defect arm that proves the measurement discriminates | §2, probe committed under `scripts/ci/clienthello-artifact-probe` |
| X-Q01 | certificate store · hot reload · REALITY handshake · pool | `NOT_COVERED` | the store's reload ordering was fixed in the 2x2 round and the REALITY key material does not come from the store; a new four-way needs a REALITY-side reload, which does not exist |
| X-Q02 | rule-provider download · selector · DNS bootstrap · network change | `NOT_COVERED` | needs a provider whose download is routed through the group it feeds; the self-dependency has to be *configured*, and no fixture builds that cycle |
| X-Q03 | SOCKS UDP association · TProxy/TUN capture · NAT expiry · outbound UDP | `NOT_OBSERVABLE` | TProxy/TUN needs Linux + root |
| X-Q04 | Mieru SDK underlay · ResourceGuard · OOM reclaim · network reset | `SOURCE_ONLY` | `protocol/mieru` still implements no `IdleConnectionReleaser` (window A's R-3), so the trim walk cannot reach the SDK's underlays; a four-way attack needs a runnable SDK harness this window did not build |
| X-Q05 | network namespace · interface bind · dual-stack dialer · QUIC retry | `NOT_OBSERVABLE` | netns is Linux-only |
| X-Q07 | dashboard/status · resource governance · cancel/close · provider refresh | `SOURCE_ONLY` | the status surface was hardened to be read-only: `SelectionStatus`/`Revision` take no selection and advance no cursor (`protocol/group/selector.go:533-609`), and the delay handlers cancel with the request (`clashapi` §64(K), §12). No side effect found on the read path |
| X-Q08 | IPv6 DNS64 · fake-IP restore · rule action · SOCKS/TUN return path | `NOT_COVERED` | needs a DNS64 fabric; the fake-IP return-path repairs are inherited from the 2x2 round |

## 2. X-Q06 — the artifact-level wire check (`NO_DEFECT`, executed)

The question: the go.mod comment says the product's camouflage is the pre-1.26 key-share set, and the
build flags are supposed to preserve it. Two rounds ago the *strings* were measured
(`scripts/ci/clienthello-probe`, module-level). This window measured the **built binary**: a real SOCKS
→ Trojan TLS handshake, with a recorder standing where the Trojan server would be.

| Binary | LDFLAGS | ClientHello (measured on the wire) |
| --- | --- | --- |
| release recipe (`release/LDFLAGS`) | full list | `groups=[4588 29 23 24 25] key_shares=[4588 29]` |
| dev build | none (go.mod `godebug tlssecpmlkem=0`) | `groups=[4588 29 23 24 25] key_shares=[4588 29]` |
| **defect arm** (the old `build_shared` short list) | `multipathtcp=0,tlssha1=1` | **`groups=[4588 4587 4589 29 23 24 25] key_shares=[4588 29]`** |

The third row is what the short list shipped before this session's `e8a74e41f`: SecP256r1MLKEM768 and
SecP384r1MLKEM1024 in every ClientHello. Its presence is the proof that the probe is measuring the
wire and not a string: the same command, the same binary source, only the link flags differ.

**Necessity.** Four systems: the build flags decide the default, the linker applies it, the TLS stack
builds the ClientHello, and the remote peer sees it. Removing the fourth (the recorder) leaves two
strings to diff; removing the third (the TLS stack) leaves a module probe that says nothing about the
artifact. The measurement is the only member of the set that observes what a censor would.

## 3. Why the other seeds are not claimed

They are listed with the exact blocker in §1. The two `SOURCE_ONLY` rows are not "probably fine": X-Q04
has a *known* missing capability (no releaser in the Mieru package) and X-Q07's read path was checked
guard by guard. The rest need a platform (netns, TUN/TProxy, DNS64) or a fixture (a provider whose
download routes through its own group) that this environment cannot build.

## 4. The instrument

`scripts/ci/clienthello-artifact-probe` is committed with its measured table in the doc comment. It
exists so "the artifact sends the camouflage set" can be re-checked after any build change instead of
being assumed; it is deliberately **not** wired into a workflow, because it needs a locally built
binary and a loopback listener, and the release recipes already measure their own output.
