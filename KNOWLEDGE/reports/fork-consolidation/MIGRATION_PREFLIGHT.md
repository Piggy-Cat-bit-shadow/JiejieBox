# QUIC-GO Fork Migration — Preflight (2026-10-25)

## Identity

| field | value |
|---|---|
| OLD_REPO | `Piggy-Cat-bit-shadow/quic-go` (id 1357572148) |
| OLD_PARENT | `MetaCubeX/quic-go` |
| OLD_SOURCE | `quic-go/quic-go` |
| TARGET | `SagerNet/quic-go` (id 526232790; parent `quic-go/quic-go`) |
| TARGET default | `dev` |
| OLD default | `sagernet` |
| OLD branches | `sagernet` only |
| OLD `sagernet` | `f0f7ab2d6f609a3a1a26ea9bc12b9f834c994c85` |
| OLD tree | `5860d0a6c1764bbbea87a7e8a7bacd7c83ac0200` |
| OLD go.mod blob | `8d120d1a90b1d5a7fd9ba75ab3aae693ffa24e24` |
| module path | `github.com/sagernet/quic-go` |
| mod.12 ancestor | `80c132feb2112b5d0d88d02f941b1e68e56a2722` (YES, behind 0) |
| commits reachable | 7216 |
| tags in mirror | 113 local tag names; 191 remote tag refs including peeled |
| GitHub API tags | 113 (all pages; same names as the mirror) |
| releases/assets | 0 / 0 |
| issues / PRs | 0 / 0 |
| workflows | 1 (`verify.yml`; `sagernet` trigger) |
| Actions runs | 268 metadata rows; logs/assets not archived |
| rulesets / protection | none |
| webhooks / deploy keys / environments | 0 / 0 / 0 |
| Secrets | 0 names (nothing to re-enter) |
| LFS / submodules | none |

## Backups

| item | path |
|---|---|
| mirror | `<WORKSPACE_ROOT>\_quic_migration_20261025\old-quic-go.git` |
| bundle | `...\quic-go-all.bundle` (SHA256 `A877932FC37A59A699E1CD482CB0913DC9A044C8F2D0A8991A7513106513AE36`) |
| second copy | `<WORKSPACE_ROOT>\_quic_migration_backup2_20261025` |
| refs / remote before | `old-refs.txt`, `origin-ls-remote-before.txt`, `tags-remote-before.txt` |
| API metadata | `api-*.json` (repo, branches, tags, releases, issues, pulls, workflows, rulesets, hooks, keys, environments, variables, secrets names, actions permissions, runs) |
| consumer pins | `pin-singbox-root.json`, `pin-singbox-test.json`, `pin-singquic.json`; all resolve to the same `f0f7ab2` pseudo-version |
| rehearsal | `rehearsal.git`: explicit `refs/heads/sagernet` push, fsck clean, SHAs equal |

`old-quic-go.git fsck --full` PASS. Bundle verification: complete history, SHA-1. Clean-cache `go mod download` with `GOPROXY=https://proxy.golang.org,direct` resolved `v0.61.1-0.20261024181210-f0f7ab2d6f60` (PASS). `scripts/ci/check-go-module-integrity.sh` PASS.

## Blockers / risk

1. **Deletion permission**: current `gh` token scopes are `gist, project, read:org, read:packages, repo, workflow`; **no `delete_repo`**. Deletion must be done by the owner in GitHub Settings or after `gh auth refresh -s delete_repo`. This does not block backup/preflight.
2. **Irreversible metadata**: deleting the fork loses Actions run history, issue/PR metadata (currently zero), webhooks/environments/secrets (zero), and the repository ID. A Git mirror cannot restore those.
3. **Same-name re-Fork**: recreating `Piggy-Cat-bit-shadow/quic-go` after deletion may be delayed or refused; restoring a deleted fork is not guaranteed. The current repo must not be deleted until the user gives the separate explicit confirmation.
4. **Parent mechanics**: `SagerNet/quic-go` is itself a fork (`parent=quic-go/quic-go`). Forking it should set `parent.full_name=SagerNet/quic-go`; this is not proven until the new repo exists.
5. Existing consumers all pin `f0f7ab2`; after migration the same commit must be pushed and resolver-verified from a clean module cache.
6. Release/tag conflicts: 113 local tags; only needed tags should be pushed, and same-name/different-SHA tags must never be force-overwritten.

## Rehearsal result

Local bare restore: PASS, `refs/heads/sagernet` = `f0f7ab2...`, fsck clean. This proves Git object/ref transfer only; it cannot prove the GitHub parent relationship.

## Approval gate

Deletion is **not** authorized by this preflight. Required explicit confirmation:

> 确认删除现有 `Piggy-Cat-bit-shadow/quic-go`，然后从 `SagerNet/quic-go` 重新 Fork 到同一地址并从本地恢复吗？

Expected after confirmation: delete old repo → fork SagerNet → push only `sagernet` from mirror → set default `sagernet` → verify parent/tree/SHA → verify consumers and CI.
