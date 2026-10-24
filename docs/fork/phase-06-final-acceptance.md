# Phase 06 — main-repo tail, full acceptance and ancestry closure

```text
PHASE = 06/06
RESULT = DONE_WITH_EXTERNAL_VERIFICATION_LIMITS
MAIN_SHA = c3c23d1059c8834f68d268995557c99be6d787a0 (code); docs HEAD follows
DOCS_SHA = pending final ledger commit
UPSTREAM_OBSERVED_SHA = a6952ccf6907c18e18917fcb763a433e8e36937b
MERGE_BASE_BEFORE = d630fa94c53b2e2bdf256fad8b9d1cff809146ed
MERGE_BASE_AFTER = a6952ccf6907c18e18917fcb763a433e8e36937b
GIT_ANCESTRY = ancestor=YES; behind=0 (git rev-list --count HEAD..upstream/testing); GitHub compare behind_by=0
OPEN_APPLICABLE_MISSING_FIX = 0
UNKNOWN = 0
REJECTED_WITH_EVIDENCE = 1 (systemd root policy)
EFFECTIVE_DEPENDENCY_PINS = sing-tun dcf84577d0eb / sing 4200a04a089a / quic-go ce02ee5cc210 (branch jiejie-masque-mod9) / sing-quic 5e5a0274bd6c / cronet-go root+all 8aa139e9e140 and 29 libs 8c68ce89873c / tailscale 1688b52f8204
FORK_DEFAULT_BRANCH_SHAS = sing-tun dev dcf84577d0eb; sing dev 4200a04a089a; sing-quic main 5e5a0274bd6c; cronet-go main 8aa139e9e140; quic-go default jiejie-masque-stable beb42da71a55 (independent line, not consumed); tailscale official no fork
MAIN_ROOT_AND_NESTED_DEPENDENCIES = CONSISTENT (same effective pins in root and test/go.mod; go mod verify clean)
CI_SAME_FINAL_SHA = Verify 40091508491 / Android Core ARM64 40092344627 / Mobile contract verification 40092324653 — all SUCCESS on c3c23d
CROSS_PLATFORM_VERIFICATION_LIMITS = no macOS/iOS/Android device; Windows TLS 1.3 tests fail on this host identically before phase 06; no booted systemd host; no real tailnet/resolved/DHCP environment; Cronet archives not rebuilt; Android/desktop submodule pointers are pre-existing and not on their default tips
TEMP_BRANCHES_DELETED = []
TEMP_BRANCHES_RETAINED = oom-final-2026-10-24 (checked out in integration-release-next); perf/xhttp-rtt-pipeline-probe (checked out in singbox-consolidation); wip/p0ab-stale-base (5 unique commits, pre-existing); origin/pending/device-axis-reload-window-tests (1 unique commit, pre-existing)
WORKTREE = CLEAN for _p01_main; pre-existing detached release/bench worktrees untouched
```

## 1. Remaining main-repo rows

- **Certificates (`73698e437479`)**: upstream generated data copied. `TestIncludedRootCAsAreValid`
  parses every PEM, rejects duplicate DER, pins 92 Chrome / 161 Mozilla roots and the SHA-256 of the
  canonical DER sequence (`094afa0b…`, `e3d36357…`). Source is the checked-in generator
  (`cmd/internal/update_certificates`, CCADB IncludedCA and Chrome RootCACertificatesIncludedByRS);
  no random download and no runtime trust change.
- **Format round-trip (`755150a5131e`)**: `server_port`/`security`/names/priority now carry
  `omitempty`, and DERP STUN uses the compact port form. Tests cover option-level and
  `libbox.FormatConfig`-level round-trips (VLESS 443, VMess 8443, empty security, zero priority,
  DERP 3478). The upstream format commit removed a nil guard that this fork's sing revision still
  needs for option-less `respond`/`adblock` actions; restored and covered by the existing round-trip
  test.
- **Direct action (`b6fc3a486355`)**: removed the type, options, constructor, reference path, rule
  handling and schema. `TestDirectRuleActionIsRejected` shows `action: direct` now fails decode
  instead of becoming a ghost no-op; the route-aware fixtures use `reject` for an unnamed terminal
  action and the direct-specific non-terminal case is gone.
- **Command server (`ae4826a387e6`)**: listen errors return immediately; the 30-second retry and its
  `syscall`/`time` sleeps are removed. `TestCommandServerListenFailsImmediately` bounds an unusable
  base path to under two seconds.
- **systemd root policy (`b02b21b3f3aa`)**: **rejected with evidence**. The unit keeps
  `User=sing-box`, the capability bounding/ambient set, the sysusers account and the scoped polkit
  rule. CAP_NET_ADMIN/CAP_NET_RAW cover TUN/routing; split DNS is scoped to the service user. A host
  that needs root-only features must override the unit deliberately rather than widening every
  installation. `TestPackagedServiceKeepsLeastPrivilege` locks the files.

## 2. Acceptance and regression sweep

- Scenario audit: audit baseline `824dbcf` → current upstream `a6952cc` is **one** commit,
  `Remove stale workflow`; `.github/workflows/stale.yml` is already absent. No unaudited upstream
  semantic change exists after the 73-item audit.
- Test evidence: root `go test ./...` (tags `DEFAULT_BUILD_TAGS_OTHERS`) leaves only the known
  Windows TLS 1.3 host failures; all other packages pass. Targeted:
  `option`, `route/rule`, `route`, `protocol/tun`, `dns`, `experimental/libbox`,
  `common/certificate`, `release/config`, `cmd/internal/modlayout`.
- Race: `go test -race` passes for `dns`, `route/rule`, `experimental/libbox`, `protocol/masque`
  and `protocol/tun` (CGO + mingw).
- Builds: Windows root+test compile, Linux amd64/arm64, Android Core ARM64 CI. Apple CI is a
  build/unit gate, not a device run.
- Schema: regenerated with the repository command and reproduced byte-for-byte
  (`sha256 CCB468FB…`).

## 3. Ancestry and branches

The merge is `1649aa4f04b` with strategy `ours`. It is an ancestry closure, not a claim that the
upstream tree was copied: the merge base moved from `d630fa94` to the observed tip, and the tree
keeps the fork's independent MASQUE/QUIC lines, Apple/Cronet/Naive, Mieru, DNS/Fake-IP, TUN fast
path and Android code. The evidence for discarding nothing semantic is the 73-item audit plus the
22 patch-id matches and the one new commit being already absent.

No phase-06 construction branch was created, so none was deleted. Retained branches and worktrees
are listed in the block above; deleting them would discard unique pre-existing work or disturb a
checked-out worktree, so they are documented rather than removed.

## 4. External limits

1. No macOS/iOS/Android device: Apple Network.framework and Android binder behaviour are
   build/unit/CI evidence only.
2. Windows TLS 1.3 tests fail on this host identically on the pre-phase-06 tree (known environment
   issue), not a regression.
3. No booted systemd host: the least-privilege unit is validated as files/tests, not by starting the
   packaged service.
4. No live tailnet, systemd-resolved D-Bus service, or DHCP lease; those phase 05 limits remain.
5. Cronet native archives were not rebuilt; the checked-in assets and pins are unchanged.
6. Android/desktop submodule pointers are pre-existing and diverge from their default tips; Apple
   UI gitlink is identical to `sing-box-for-apple` `main`.
