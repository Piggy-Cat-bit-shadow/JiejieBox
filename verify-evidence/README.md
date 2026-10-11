# verify-evidence

Raw transcripts behind `APPLE_PIN_FINAL_VERIFICATION.md`.

**Everything here was produced by the code at the commit named in each log's own header.**
The logs are not curated: they are the verbatim stdout+stderr of the command on line 1,
followed by its exit code. A log whose header names a different `HEAD` than the branch tip
is stale and must be regenerated rather than trusted.

## Provenance

Each `.log` starts with three lines:

```
### CMD: <the exact command>
### HEAD: <the tree the command ran against>
### DATE: <when>
```

Regenerate the whole directory from the branch tip with:

```bash
cd <worktree>
rm -f verify-evidence/*.log verify-evidence/gates.tsv
# then re-run each row of gates.tsv, capturing stdout+stderr and the exit code
```

The one-liner used for this round is in the commit message of the evidence commit; the
point is that nothing in here is hand-written.

`apple-client-source.log` additionally contains its own `EXIT=` line after each resolver
action, so the per-action status is visible inside the single file.

## Contents

| File | What it is |
| --- | --- |
| `gates.tsv` | machine-readable tally: `<gate>\t<exit>\t<log>\t<command>`. 25 rows, all exit 0. |
| `apple-abi-provenance.log` | the provenance gate with `APPLE_ABI_CHECK_REMOTE=1`; ends `FAIL: 0   WARN: 0` |
| `check-libbox-abi.log` | ABI gate; layer 5 asserts `apple gitlink is the verified revision` |
| `check-apple-source-selection.log` | one-source selection gate; `65 passed, 0 failed` |
| `test-apple-signing.log` | signing/branding suite; `134 passed, 0 failed` |
| `test-apple-macos-name.log` | macOS name gate; `90 passed, 0 failed, 0 skipped` |
| `test-apple-beta-publish.log` | beta publish gate |
| `verify-fork-handoff.log` | parent-coordinate handoff gate |
| `apple-client-source.log` | every resolver action, incl. `checkout macos` and both `verify`s |
| `pin-identity.log` | the four SHAs side by side plus `testing` (local and remote) |
| `frozen-sha-reachable.log` | `merge-base --is-ancestor` against `origin/main` |
| `no-stale-pins.log` | no retired Apple SHA in machine-consumed paths |
| `brand-and-identity-contract.log` | brand is `JijieBox`; `PRODUCT_NAME` stays `sing-box` |
| `source-hygiene.log` | no `assert-distinct` caller remains; no `<dir>/.git` guard uses `-d` |

## What is deliberately NOT here

The pre-fix transcripts were deleted rather than kept. They recorded states that no longer
exist — `FAIL: 8` from the provenance gate, `MACOS_APPLE_BRANCH: unbound variable` from the
selection gate, and the old `Jijiebox` + branded-`PRODUCT_NAME` overlay behaviour. Keeping
them would have made the directory read as a mix of current and superseded results, which is
exactly the ambiguity this round removed.

`Jijiebox` (lower-case b, 9 characters) still appears in `test-apple-macos-name.log`, but
only inside `PASS: the gate rejects 'Jijiebox' as the display name` — it is a near-miss the
gate is asserting it refuses, not a value anything writes.
