# QUIC-GO Fork Migration — Rollback Plan

## Before deletion

Do not delete. The old repository remains fully usable; backups are complete and
`old-quic-go.git fsck --full` passed. `sagernet` is `f0f7ab2d6f609a3a1a26ea9bc12b9f834c994c85`.

## After deletion, before new fork is usable

1. Stop all repository creation/deletion retries. Keep both mirror copies and the bundle untouched.
2. Diagnose the failure: same-name still occupied, fork API 422, permission/scope, or GitHub async.
3. If immediate code availability is required, create a **standalone** temporary repository (not a
   claimed fork) and restore only the explicit ref:
   `git --git-dir=old-quic-go.git push <temp-url> refs/heads/sagernet:refs/heads/sagernet`.
   This preserves code but not GitHub parent/metadata; report it as `PARTIAL_RECOVERY`.
4. If the name is available but forking fails, retry only after a real diagnostic change; do not
   create-delete repeatedly.
5. Contact GitHub Support for fork-network / parent correction if the new `parent.full_name` is not
   `SagerNet/quic-go`. Do not delete the new repository to try again.

## If parent is wrong after recreation

Keep the repository and backups. Do not rewrite code, do not change module paths, and do not claim
success. `parent.full_name` is GitHub metadata, not a Git property; it can only be corrected by
GitHub Support or a properly executed re-fork. Report `PARENT_MISMATCH`.

## If SHA/tree/tags differ

Compare with:
- sagernet SHA `f0f7ab2...`
- tree `5860d0a...`
- go.mod blob `8d120d1...`
- mod.12 ancestor `80c132f...`
- tag manifest from the mirror.

Restore only missing refs with explicit refspecs. Never force-push a conflicting ref or overwrite a
tag with a different SHA. If the conflict cannot be resolved without force, keep the repository
available and mark `PARTIAL_RECOVERY`.

## If consumers fail

Check in order: commit object reachability, module path `github.com/sagernet/quic-go`, Go proxy
propagation, `replace` version, clean module cache. Do not update the version or `go get` upstream
to hide a migration failure. The expected effective pin before and after is
`v0.61.1-0.20261024181210-f0f7ab2d6f60` in root, test and sing-quic.

## Cleanup

Do not delete either backup until the user explicitly accepts the final migration and no longer
needs rollback. The migration is not complete until:
`parent.full_name=SagerNet/quic-go`, `fork=true`, default `sagernet`, same SHA/tree/go.mod blob,
mod.12 ancestor, clean-env module download, consumer pins unchanged, and CI evidence exists.
