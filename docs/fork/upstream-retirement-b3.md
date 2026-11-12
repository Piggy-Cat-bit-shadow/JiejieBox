# B3: the 218 duplicate-patch drops — approval and rewrite evidence

Batch 1 found 218 fork commits whose `git patch-id --stable` equals an upstream commit but whose SHA
differs. The user approved executing B3 (the history rewrite). This page records the approval, what
was attempted, and why the rewrite stopped: the drops are not context-neutral.

## What is approved and known

| fact | value |
| --- | --- |
| duplicate rows in `q47_batch1_duplicate_patches.tsv` | **218** |
| rows resolving to an ancestor of `HEAD` | **218** |
| rows inside the `HEAD --not upstream/testing` rebase range | **215** |
| upstream equivalents already reachable from `HEAD` | yes: `upstream/testing` is an ancestor |
| rewrite target | a new branch, never a force-push of `testing` |

The 3 rows outside the range are already reachable from the pinned upstream tip; they need no drop in
this rewrite.

## Attempts (2026-11-12, isolated worktree `_round11/pc12`)

Both attempts used a generated interactive-rebase todo that changed `pick` to `drop` for the 215
in-range duplicates, on a scratch branch `work/b3-drop-218` created from the current head. No
published branch was touched; both attempts were aborted and the branch deleted.

**Attempt 1 — default conflict resolution.** The rebase stopped at the first kept commit:

```
Could not apply 03de88adb... # Fix network reset not closing connections
CONFLICT (content): Merge conflict in box.go
```

Dropping the earlier duplicate removed context the next kept commit changes against. The first kept
commit is 22 commits into the list, so this is not a one-off at the tail.

**Attempt 2 — `-X theirs`.** Content conflicts were auto-resolved by favoring the commit being
replayed; the rebase advanced to commit 22 and then hit modify/delete conflicts that `-X theirs`
cannot resolve:

```
CONFLICT (modify/delete): transport/openconnect/device.go deleted in HEAD and modified in d535f106f
  (Remove dependency on gVisor). Version d535f106f left in tree.
CONFLICT (modify/delete): transport/openconnect/device_stack.go ...
CONFLICT (modify/delete): transport/openvpn/device_stack.go ...
CONFLICT (modify/delete): transport/openvpn/device_system_stack.go ...
```

`d535f106f` ("Remove dependency on gVisor") is one of the kept commits whose file set depends on
whether a dropped duplicate created/deleted those paths. Resolving modify/delete conflicts by taking
"theirs" blindly would reintroduce gVisor files the branch intentionally removed; resolving by
deleting would drop the kept commit's content. Neither is mechanical.

## Conclusion

B3's approval removes the policy blocker; it does not make the rewrite automatic. The 218 duplicates
are context-dependent: dropping one changes the file set the next kept commit applies to, so a
history rewrite needs per-commit conflict resolution and a final tree comparison against the
pre-rewrite head. That is a manual release-engineering task, not a scripted one.

**Status:** approved, attempted, **not executed**. No history was rewritten.

## Resume recipe (for the release owner)

```sh
git switch -c work/b3-drop-218 <head>
python scripts/ci/...           # generate the 215-SHA drop list from the batch-1 TSV
GIT_SEQUENCE_EDITOR=<drop-editor> GIT_EDITOR=true \
  git rebase -i upstream/testing
# resolve each conflict with the pre-rewrite tree as the reference, not by blindly taking a side
# after the rebase completes:
git diff <pre-rewrite-head>..HEAD --stat    # must be empty for a content-neutral drop
```

Only after that diff is empty may the rewritten branch be considered for replacing `testing`; the
218-row list and the two conflict logs are the evidence that the mechanical attempt was made.

## What this does NOT claim

It does not claim the 218 were dropped, that `testing` was rewritten, or that B3 is complete. The
approval and the conflict evidence are recorded so the next attempt starts from the exact failure
point instead of repeating it.
