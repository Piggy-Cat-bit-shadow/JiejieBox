# QUIC-GO Fork Migration — Final

```text
MIGRATION = COMPLETE_WITH_METADATA_LIMITATIONS
OLD_REPO = https://github.com/Piggy-Cat-bit-shadow/quic-go (deleted)
NEW_REPO = https://github.com/Piggy-Cat-bit-shadow/quic-go (id 1432561090)
OLD_PARENT = MetaCubeX/quic-go
NEW_PARENT = SagerNet/quic-go
SOURCE = quic-go/quic-go (unchanged)
OLD_SAGERNET_SHA = f0f7ab2d6f609a3a1a26ea9bc12b9f834c994c85
NEW_SAGERNET_SHA = f0f7ab2d6f609a3a1a26ea9bc12b9f834c994c85
TREE = 5860d0a6c1764bbbea87a7e8a7bacd7c83ac0200 (identical)
GO_MOD_BLOB = 8d120d1a90b1d5a7fd9ba75ab3aae693ffa24e24 (identical)
MODULE_PATH = github.com/sagernet/quic-go
MOD12_ANCESTOR = 80c132feb2112b5d0d88d02f941b1e68e56a2722 YES
DEFAULT_BRANCH = sagernet
BRANCHES = sagernet only
TAGS = 182 names; all 113 old names present; 110 same target; 3 conflicts retained without force
TAG_CONFLICTS = v0.32.0, v0.40.0, v0.47.0 (old fork target differed from SagerNet tag; old objects in bundle)
RELEASES = 0 (old had 0)
ISSUES = 0
PULL_REQUESTS = 0
ACTIONS_RUN_HISTORY = NOT_RESTORED (GitHub metadata, 268 old run rows lost)
SECRETS = 0 names (none to re-enter)
WEBHOOKS_DEPLOY_KEYS_ENVIRONMENTS = 0/0/0
BRANCH_PROTECTION_RULESETS = none/none
LFS_SUBMODULES = none
BACKUP_MIRROR = C:\Deepseek\_quic_migration_20261025\old-quic-go.git
BACKUP_BUNDLE_SHA256 = A877932FC37A59A699E1CD482CB0913DC9A044C8F2D0A8991A7513106513AE36
SECOND_COPY = C:\Deepseek\_quic_migration_backup2_20261025
GO_DOWNLOAD_CLEAN_ENV = PASS (same h1/GoModSum from the new repository)
MODULE_INTEGRITY = PASS
QUIC_GO_TEST = PASS (post-migration verify 40129114214 at f0f7ab2, success)
SINGBOX_SINGQUIC_BUILD_AND_TEST = PASS (sing-quic ./...; main MASQUE/MASQUE transport)
ACTIONS = Verify 40129114214, f0f7ab2, SUCCESS
USER_DELETE_APPROVAL = RECEIVED (user: 已删除)
FORCE_PUSH = NO
OTHER_REPOS_WRITTEN = NO
ROLLBACK_STATE = AVAILABLE
UNVERIFIED = old Actions run history (cannot be restored); three conflicting tag targets are only in the mirror
```

## What was done

1. Read-only preflight recorded the old repo (`MetaCubeX/quic-go` parent), the target
   (`SagerNet/quic-go` parent), branches, 113 tags, zero releases/assets, zero issues/PRs, one
   workflow, permissions and settings.
2. Built two independent copies: a full mirror and a complete Git bundle, both fsck/bundle-verified.
   API metadata was saved separately. A local bare restore was rehearsed with the exact sagernet SHA.
3. User confirmed deletion separately and reported it complete. The old repository returned 404.
4. Forked `SagerNet/quic-go` back to `Piggy-Cat-bit-shadow/quic-go`; GitHub reports
   `parent.full_name=SagerNet/quic-go`, `source.full_name=quic-go/quic-go`.
5. Pushed only `refs/heads/sagernet` from the mirror. Set it as default, deleted every other
   inherited branch, and restored missing tags with explicit refspecs. No force push was used.
6. Restored settings: description, wiki/projects, Actions enabled/all, workflow permissions read.
   Zero secrets/webhooks/deploy keys/environments/branch protection existed.
7. Verified SHA, tree, `go.mod` blob, module path, mod.12 ancestor, clean module download, consumers
   and post-migration CI.

## Limitations

- GitHub Actions run history (268 metadata rows), any old webhook/environment records and repository
  ID are not recoverable from Git; the new repository has a new ID and fresh run history.
- Three tags kept SagerNet's targets instead of the old fork's targets because overwriting would
  require a force update. Their old objects remain in the mirror/bundle; this is recorded, not
  hidden. No consumer pins a tag.
- Wiki content was not cloned; old wiki state is not in the Git mirror.
- The new fork's pushed tags triggered inherited upstream workflows; queued tag runs were cancelled.
  The authoritative post-migration run is Verify `40129114214` on `sagernet`/`f0f7ab2`.

## Consumer pins (unchanged)

- `sing-box` root and `test/go.mod`: `github.com/Piggy-Cat-bit-shadow/quic-go v0.61.1-0.20261024181210-f0f7ab2d6f60`
- `sing-quic/main`: same effective pin
- `sagernet` module path: `github.com/sagernet/quic-go`
- Clean-cache `go mod download` from the new repository: same h1 and GoModSum as before.

Rollback remains available from the mirror/bundle; see `MIGRATION_ROLLBACK.md`.
