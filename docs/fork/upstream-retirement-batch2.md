# Upstream retirement, batch 2: the lean maintained-patch index (audit Q47)

Batch 1 answered "which fork commits have an identical upstream patch" mechanically. Batch 2's object
was the other side: the 3562 commits upstream does **not** carry, "grouped into logical patches, each
with trigger/regression/consumer". That population is history, not a patch list: it includes two
months of merges, rebases and rewrites, most commits carry no maintenance marker, and a per-commit
trigger cannot be derived without adjudicating each one. Writing 3562 generated rows would look like
completion while saying nothing.

This batch therefore builds the **lean** index the audit's wording asks for: the logical patches the
fork still marks as its own. A patch is in scope when a source or documentation line carries a
`SPEC <id>` reference (the fork's own task identifier). The marker text is the trigger evidence, the
files are the consumers, and the tests in those packages are the available regression surface.

## Mechanical result

| Fact | Value |
| --- | --- |
| marker lines (`lx:` / `SPEC <id>`) in the tree | **163** |
| logical patches, grouped by SPEC id | **28** |
| patches with at least one blamed commit | 28 |
| code patches (HOLD) / analysis-only records (ANALYSIS) | **21 / 7** |
| patches with available package tests | 21 (the 7 analysis records have none) |
| patches with an upstream equivalent patch-id at the pinned tip | **0** |

The machine-readable index is [`upstream-retirement-batch2.tsv`](upstream-retirement-batch2.tsv):
`spec, commits, first_date, last_date, trigger_marker, consumer_files, regression_tests, disposition,
retirement_condition`.

## Curated adjudication

The TSV is the mechanical record; the table below is the human-readable adjudication of the same 28
rows. "kind" is code (a patch this fork carries) or analysis (a historical investigation record, not a
patch to retire). The trigger is paraphrased from the marker line; the consumer column names the
owning area, not every file.

| spec | kind | trigger | consumer / regression | disposition |
|---|---|---|---|---|
| 002 | code | Xray client audit (`f2e24e1`) divergences adopted for the XHTTP client | transport/v2rayxhttp; its package tests | HOLD |
| 015 | code | UI proxy list lost groups during config apply (§3.5) | daemon status/apply; daemon tests | HOLD |
| 016 | code | concurrent apply/read of libbox command types must be race-free | experimental/libbox; `TestConnectionsConcurrentApplyAndReadIsRaceFree` | HOLD |
| 022 | code | packet-up channel ownership: the channel is effectively free after hand-off | transport/v2rayxhttp/conn.go; xhttp tests | HOLD |
| 038 | analysis | why no public signature was changed in the residual sweep | documentation only | ANALYSIS |
| 043 | code | malformed meta made the dial hang until timeout (wire-reproduced) | transport/v2rayxhttp/meta.go; url/dial tests | HOLD |
| 050 | code | a read deadline must close the response body too | transport/v2rayxhttp/conn.go; deadline/breaker tests | HOLD |
| 059 | code | XMUX pool transitions observable through the debug event log (§8.2) | transport/v2rayxhttp/xmux.go; xmux tests | HOLD |
| 061 | code | packet-up ignored the caller's context (pre-cancelled dial returned success) | transport/v2rayxhttp; dial-context tests | HOLD |
| 071 | code | WireGuard bind dial is bounded by `C.TCPTimeout`, not the tunnel's lifetime | transport/v2rayxhttp/conn.go; raise-failure test | HOLD |
| 072 | code | XHTTP stream/split/packet conn use `connCtx`, not the retired dial ctx | transport/v2rayxhttp + transport/http; their tests | HOLD |
| 074 | code | `onExpire` runs outside the lock and closes the HTTP/2 response exactly once | transport/v2rayxhttp/conn.go; breaker/expiry tests | HOLD |
| 076 | code | XHTTP client semantics from the Xray-parity audit (shared with 002/061/072/077) | transport/v2rayxhttp; its tests | HOLD |
| 077 | code | dial-context contract across XHTTP, H3 CONNECT setup and runtimecoord | route/dial_setup.go + transports; route/transport tests | HOLD |
| 080 | code | AmneziaWG 3.x device support | transport/wireguard/device_awg.go; wireguard tests | HOLD |
| 082 | code | HTTP/2 body stream-error classification shared by transports | common/badh2; badh2 + xhttp tests | HOLD |
| 094 | code | H3 transport verdict/fallback keeps the same position as the XHTTP implementation | common/httpclient; httpclient tests | HOLD |
| 097 | code | WG/AWG suspend uses `adapter.IdleStateReporter`; this fork has no such reporter | daemon/started_service.go; daemon tests | HOLD |
| 104 | code | QUIC TLS config unwrapping when `STDConfig()` refuses | common/tls; tls tests | HOLD |
| 106 | code | same suspend/IdleStateReporter contract as 097 | daemon/started_service.go; daemon tests | HOLD |
| 119 | analysis | v0.1.6 handoff note (SPEC 119/121) | documentation only | ANALYSIS |
| 120 | analysis | MTU alignment decision recorded in the v0.1.6 closure ledger | documentation only | ANALYSIS |
| 122 | analysis | uTLS retains TLS1.3 handshake state for the connection lifetime (measured ClientHello sizes) | documentation only | ANALYSIS |
| 139 | code | TrustTunnel stream creation owns the session/auth stages | protocol/trusttunnel; its tests | HOLD |
| 141 | analysis | packet-up upload pipeline (lx.25) recorded in the upstream ledger | documentation only | ANALYSIS |
| 142 | analysis | packet-up upload pipeline follow-up (lx.25) recorded in the upstream ledger | documentation only | ANALYSIS |
| 146 | analysis | MASQUE `Box.Close` deadlock behind `Outbound.Close` (lx.28) | documentation only | ANALYSIS |
| 152 | code | TrustTunnel session underlay is internal to the tunnel, not the outbound dialer | protocol/trusttunnel; its tests | HOLD |

## What each column means - and does not

- **trigger_marker** is the marker comment itself, with `file:line`. It is the source of the trigger
  claim for review; it is NOT a rewritten "user-visible symptom" paragraph. A row whose marker is a
  test name (for example `SPEC 016`) is a regression reference, not a full trigger.
- **consumer_files** is every file carrying that SPEC id. It is the patch's footprint, not a proven
  list of runtime call sites.
- **regression_tests** lists the test files present in the touched packages. That is the available
  regression surface; it does not claim each file exercises this patch. Documentation-only rows say
  `none in package`.
- **disposition** is HOLD for the 21 code rows: none has an upstream patch-id at the pinned tip, and
  "no upstream equivalent" is not evidence that the fork no longer needs the change. The 7 analysis
  rows are marked ANALYSIS: they are historical investigation records, not code patches to retire.
- **retirement_condition** for a code row is: upstream ships an equivalent change (verify with
  `git show <blame-sha> | git patch-id --stable` and the same command on the upstream candidate), or
  the product drops the behaviour. The analysis rows have no retirement event; they are kept as
  provenance until the report that cites them is archived.

## Reproduction

```sh
python scripts/ci/upstream-retirement-batch2.py --write   # regenerate the TSV (28 rows)
python scripts/ci/upstream-retirement-batch2.py --list    # spec, commits, dates
```

The tool blames each marker line to find the commit that introduced it. A marker copied by a later
commit attributes to the copy, which is why `commits` is the set of blamed commits, not a count of
every commit that ever mentioned the SPEC.

## Remaining batches (unchanged, and not claimed)

| batch | object | state |
| --- | --- | --- |
| B2 remainder | the unmarked fork-only commits (history without a maintenance marker) | NOT individually indexed. They are the 3562-commit population minus the 28 marked patches; indexing them needs per-topic adjudication, which this batch does not fabricate. `python scripts/ci/upstream-retirement-batch2.py --list` is the lean set. |
| B3 | the 218 retire-ready duplicate patches | still requires a rebase plan and approval: it rewrites published history. |
| B4 | dependency forks (sing, sing-tun, sing-quic, cronet-go) | Q42's pin/CI binding. |

Batch 2's contribution is that the maintained set is now small, traced to commits and files, and
checked for upstream equivalence; the unmarked history is named as unindexed rather than silently
counted as done.
