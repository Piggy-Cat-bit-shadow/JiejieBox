#!/usr/bin/env bash
# Behavioural tests for prepare-desktop-client.sh's ROOT RESOLUTION.
#
# Usage: test-desktop-client-root.sh
# Exit: 0 = every case behaved as required, 1 = a case did not.
#
# # The failure this exists to prevent
#
# The Windows release job checks the tooling and the core out into DIFFERENT directories:
#
#     tooling/   at TOOLING_SHA   <- this script lives here
#     core/      at CORE_SHA      <- release/JIEJIE_VERSION and the client gitlink live here
#
# and runs
#
#     DESKTOP_CLIENT_DIR=core/clients/desktop bash tooling/scripts/ci/prepare-desktop-client.sh
#
# The script derived its root from its own location, so the client path resolved against the TOOLING
# tree and the job died with
#
#     FAIL: core/clients/desktop is missing
#
# one step after `git -C core submodule update --init clients/desktop` had printed
# "Submodule path 'clients/desktop': checked out '<sha>'". Nothing was wrong with the submodule.
#
# # Why nothing caught it
#
# release-contract-verify.yml ran only `bash -n` on this script - a syntax check. The identity half
# of the contract needs git, a superproject and two checkouts, so it was left to the Windows
# workflow, which means the only thing that had ever EXECUTED the script was a Windows release run.
# A path bug is invisible to `bash -n` by construction, so it survived until a release was
# attempted. This suite executes the script against a fixture shaped like the real job, with no
# network and no real submodules.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
subject="$repo_root/scripts/ci/prepare-desktop-client.sh"
injector="$repo_root/scripts/ci/inject-desktop-release-version.py"

pass=0
fail=0

# A SUCCESSFUL prepare mutates the client: version.json is stamped, which is the entire point. Every
# case after one would therefore start from a dirty client and be refused by the "not clean before
# the stamp" precondition - correctly, but for a reason that has nothing to do with what the case is
# testing. The first version of this file had exactly that coupling and reported two root-resolution
# failures that were really the previous case's stamp. `client` is assigned during fixture setup, so
# this is a call-time reference.
reset_client() {
  git -C "$client" checkout -q -- version.json
  git -C "$client" clean -qfd
}

# Output is captured and shown ONLY when a case does not behave, so a passing run stays readable and
# a failing one explains itself. The first version of this file discarded output entirely, which
# turned four real questions into four bare "exit 1, wanted 0" lines.
expects_status() {
  local name="$1" want="$2"; shift 2
  local out got=0
  out="$("$@" 2>&1)" || got=$?
  if [ "$got" -eq "$want" ]; then
    echo "  PASS: $name"
    pass=$((pass + 1))
  else
    echo "  FAIL: $name (exit $got, wanted $want)" >&2
    printf '%s\n' "$out" | sed 's/^/        | /' >&2
    fail=$((fail + 1))
  fi
  reset_client
}

expects_contains() {
  local name="$1" want="$2"; shift 2
  local out got=0
  out="$("$@" 2>&1)" || got=$?
  if printf '%s' "$out" | grep -qF -- "$want"; then
    echo "  PASS: $name"
    pass=$((pass + 1))
  else
    echo "  FAIL: $name (exit $got; output did not contain '$want')" >&2
    printf '%s\n' "$out" | sed 's/^/        | /' >&2
    fail=$((fail + 1))
  fi
  reset_client
}

git_quiet() { git "$@" >/dev/null 2>&1; }

# --- the fixture -------------------------------------------------------------
work="$(mktemp -d)"
cleanup() { chmod -R u+w "$work" 2>/dev/null || true; rm -rf "$work"; }
trap cleanup EXIT

make_repo() {
  local dir="$1"
  git init -q "$dir"
  git -C "$dir" config user.email fixture@example.invalid
  git -C "$dir" config user.name fixture
  git -C "$dir" config commit.gpgsign false
  git -C "$dir" config core.autocrlf false
}

tooling="$work/tooling"
core="$work/core"
client="$core/clients/desktop"

make_repo "$tooling"
mkdir -p "$tooling/scripts/ci"
cp "$subject" "$tooling/scripts/ci/prepare-desktop-client.sh"
cp "$injector" "$tooling/scripts/ci/inject-desktop-release-version.py"
printf 'tooling\n' > "$tooling/README"
git_quiet -C "$tooling" add -A
git_quiet -C "$tooling" commit -m 'tooling'

make_repo "$core"
mkdir -p "$core/release"
printf '0.1.8\n' > "$core/release/JIEJIE_VERSION"

# The client is its own repository, so `git -C "$client_dir" rev-parse HEAD` is a real answer and
# version.json can be stamped and then observed as the only modification.
make_repo "$client"
printf '{\n  "version": "0.0.0",\n  "go_version": "1.25.5"\n}\n' > "$client/version.json"
printf 'appId: com.jiejiebox.app\nexecutableName: sing-box\n' > "$client/electron-builder.yml"
printf '{\n  "name": "jiejiebox",\n  "version": "0.0.0"\n}\n' > "$client/package.json"
git_quiet -C "$client" add -A
git_quiet -C "$client" commit -m 'client'
client_sha="$(git -C "$client" rev-parse HEAD)"

# The client is recorded as a GITLINK of the core, like the real submodule, without needing a
# network or a .gitmodules: mode 160000 is what `rev-parse HEAD:clients/desktop` reads.
git_quiet -C "$core" update-index --add --cacheinfo "160000,$client_sha,clients/desktop"
git_quiet -C "$core" add -A
git_quiet -C "$core" commit -m 'core with the client gitlink'

echo "== the release job's layout: tooling and core in separate checkouts =="

# THE regression. Before the fix this looked for the client under $tooling and exited 1 with
# "FAIL: core/clients/desktop is missing".
expects_status "the two-checkout invocation succeeds" 0 \
  env DESKTOP_CORE_ROOT="$core" DESKTOP_CLIENT_DIR=clients/desktop \
  bash "$tooling/scripts/ci/prepare-desktop-client.sh" --version 0.1.8

expects_contains "and it says which client it prepared" "0.1.8" \
  env DESKTOP_CORE_ROOT="$core" DESKTOP_CLIENT_DIR=clients/desktop \
  bash "$tooling/scripts/ci/prepare-desktop-client.sh" --version 0.1.8

# The version authority must be the CORE checkout. Pointing the core file at a different value and
# passing the original one must now be REFUSED - if the script were still reading some other tree,
# this case would pass silently and prove nothing.
printf '9.9.9\n' > "$core/release/JIEJIE_VERSION"
expects_contains "a disagreeing --version is refused against the CORE version file" \
  "release/JIEJIE_VERSION says 9.9.9" \
  env DESKTOP_CORE_ROOT="$core" DESKTOP_CLIENT_DIR=clients/desktop \
  bash "$tooling/scripts/ci/prepare-desktop-client.sh" --version 0.1.8
printf '0.1.8\n' > "$core/release/JIEJIE_VERSION"

echo
echo "== the refusals are specific =="

# A DESKTOP_CORE_ROOT that is not a directory must be refused as such rather than silently falling
# back to this script's own root, which would reproduce the original failure under a new name.
expects_contains "a non-existent DESKTOP_CORE_ROOT is refused" \
  "DESKTOP_CORE_ROOT '$work/does-not-exist' is not a directory" \
  env DESKTOP_CORE_ROOT="$work/does-not-exist" DESKTOP_CLIENT_DIR=clients/desktop \
  bash "$tooling/scripts/ci/prepare-desktop-client.sh" --version 0.1.8

# A wrong client path must name the path it looked for, so the next reader is told which tree was
# searched instead of only that something was missing.
expects_contains "a wrong client path names the path it looked for" "clients/nope is missing" \
  env DESKTOP_CORE_ROOT="$core" DESKTOP_CLIENT_DIR=clients/nope \
  bash "$tooling/scripts/ci/prepare-desktop-client.sh" --version 0.1.8

# An absolute DESKTOP_CLIENT_DIR is taken as given, which is the other way a caller can be explicit.
expects_status "an absolute DESKTOP_CLIENT_DIR is honoured" 0 \
  env DESKTOP_CORE_ROOT="$core" DESKTOP_CLIENT_DIR="$client" \
  bash "$tooling/scripts/ci/prepare-desktop-client.sh" --version 0.1.8

echo
echo "== the default keeps the single-checkout layout working =="

# With DESKTOP_CORE_ROOT unset the script must behave exactly as it did before this change. `core`
# plays both roles here, and the injector is copied in because the script genuinely needs both the
# version file and its own sibling injector - refusing without it is correct, not a root problem.
mkdir -p "$core/scripts/ci"
cp "$subject" "$core/scripts/ci/prepare-desktop-client.sh"
cp "$injector" "$core/scripts/ci/inject-desktop-release-version.py"
expects_status "unset DESKTOP_CORE_ROOT resolves against the script's own root" 0 \
  bash -c "cd '$core' && bash '$core/scripts/ci/prepare-desktop-client.sh' --version 0.1.8"

# And with no injector beside it, the script refuses rather than silently skipping the stamp.
rm -f "$core/scripts/ci/inject-desktop-release-version.py"
expects_contains "a missing injector is refused, naming it" "is missing" \
  bash -c "cd '$core' && bash '$core/scripts/ci/prepare-desktop-client.sh' --version 0.1.8"

echo
echo "test-desktop-client-root: $pass passed, $fail failed"
[ "$fail" -eq 0 ] || exit 1
