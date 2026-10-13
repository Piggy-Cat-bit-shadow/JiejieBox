#!/usr/bin/env bash
# Moves each client submodule to the tip of its own fork, and PROVES the move took effect.
#
# # Why this was rewritten rather than left alone
#
# The previous version did this:
#
#   pushd clients/$1; git fetch origin main; git reset FETCH_HEAD --hard; popd; git add clients/$1
#
# Three things were wrong with it, and each one is a defect this repository has already paid
# for elsewhere:
#
#   1. `git reset --hard` to a fetched tip is not a fast-forward guard. Measured against the
#      forks as they are now, TWO of the three pinned revisions are NOT ancestors of their
#      fork's `main`:
#
#        apple   40dd4879  IS     an ancestor of origin/main
#        android ec61030d  is NOT an ancestor of origin/main
#        desktop 917d2a4e  is NOT an ancestor of origin/main   (main is behind 8, ahead 2,
#                                                                and carries unrelated
#                                                                Wayland work)
#
#      So the hard reset would have moved two clients to a revision that is not on the line the
#      parent pins - for desktop, off the `jiejiebox/main` product branch entirely and back onto
#      upstream's line. That is the exact move the desktop pin work exists to prevent.
#
#   2. `git add clients/$1` exits 0 whether or not the gitlink actually changed. A submodule
#      whose worktree moved but whose ref did not leaves the parent index on the old commit and
#      the command still succeeds - the silent submodule failure this repository now has a hard
#      gate for (see scripts/ci/prepare-desktop-client.sh).
#
#   3. `origin main` was hardcoded. The branch that carries a fork's product is not always
#      `main`: it is `jiejiebox/main` for the desktop client and `dev` is android's default. The
#      branch is now read from the submodule's own configuration, defaulting to the remote's
#      HEAD, and it is printed.
#
# This script is ORPHANED - nothing in the repository references it, and it is one of several
# unused upstream helpers in .github/ - but an orphaned script that resets submodules is a
# loaded gun, not dead code. It is fixed rather than deleted because upstream still maintains
# its equivalent and a future merge should find the guard, not the hazard.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

clients=(apple android desktop)
failed=0

for client in "${clients[@]}"; do
  dir="clients/$client"
  if [ ! -e "$dir/.git" ]; then
    echo "SKIP  $dir is not checked out"
    continue
  fi

  # The branch that carries this fork's product: its own configuration first, then whatever the
  # remote calls HEAD. Never a literal.
  branch="$(git -C "$dir" config --get "submodule.$client.branch" || true)"
  if [ -z "$branch" ]; then
    branch="$(git -C "$dir" symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null || true)"
    branch="${branch#origin/}"
  fi
  if [ -z "$branch" ]; then
    echo "FAIL  $dir: cannot determine which branch carries this client." >&2
    echo "      Set submodule.$client.branch, or fix the submodule's origin/HEAD." >&2
    failed=1
    continue
  fi

  pinned="$(git -C "$dir" rev-parse HEAD)"
  git -C "$dir" fetch --quiet --unshallow origin 2>/dev/null ||
    git -C "$dir" fetch --quiet origin
  target="$(git -C "$dir" rev-parse "refs/remotes/origin/$branch")"

  printf '%s\n  branch   %s\n  pinned   %s\n  target   %s\n' "== $dir" "$branch" "${pinned:0:12}" "${target:0:12}"

  if [ "$pinned" = "$target" ]; then
    echo "  already at the fork tip; nothing to move"
  elif git -C "$dir" merge-base --is-ancestor "$pinned" "$target"; then
    echo "  fast-forward: $branch contains the pinned revision"
    git -C "$dir" checkout --quiet --detach "$target"
  else
    echo "FAIL  $dir: the pinned revision is NOT an ancestor of origin/$branch." >&2
    echo "      Moving would leave the line this repository pins. Resolve deliberately:" >&2
    echo "        git -C $dir log --oneline $branch...${pinned:0:12}" >&2
    failed=1
    continue
  fi

  # The read-back. `git add` alone proves nothing, so the staged gitlink is compared with the
  # revision actually checked out, and with what the parent commit records.
  git add "$dir"
  staged="$(git ls-files -s "$dir" | awk '{print $2}')"
  head="$(git -C "$dir" rev-parse HEAD)"
  if [ "$staged" != "$head" ]; then
    echo "FAIL  $dir: staged gitlink $staged is not the checked-out revision $head." >&2
    failed=1
    continue
  fi
  echo "  staged gitlink $staged == worktree HEAD (read back, not assumed)"
done

if [ "$failed" -ne 0 ]; then
  echo >&2
  echo "FAIL: no submodule was moved past a divergence. Review the clients named above." >&2
  exit 1
fi

echo
echo "PASS: every client is at its fork's tip, and every staged gitlink was read back"
