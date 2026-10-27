#!/usr/bin/env bash
#
# Regression test for the ABI pin gate's fail-closed contract.
#
# The gate used to `skip` a client whose gitlink was absent and then exit 0 in --pin-only mode.
# That made the check vacuous: removing `clients/apple` from the tree turned the gate green. This
# test builds a throwaway git repository with a correct pair of gitlinks, asserts the gate passes,
# removes one gitlink, and asserts the gate now fails with the missing-gitlink message.
#
# Run from the repository root in CI (verify.yml) and locally:
#
#   ./scripts/ci/test-check-libbox-abi.sh
#
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$here/../.." && pwd)"
gate="$repo_root/scripts/ci/check-libbox-abi.sh"
contract="$repo_root/docs/fork/libbox-abi-contract.tsv"

for path in "$gate" "$contract"; do
  [ -f "$path" ] || { printf 'FAIL: missing %s\n' "$path" >&2; exit 1; }
done

android_sha="$(sed -n 's/^VERIFIED_ANDROID_SHA="\([0-9a-f]*\)"/\1/p' "$gate")"
apple_sha="$(sed -n 's/^VERIFIED_APPLE_SHA="\([0-9a-f]*\)"/\1/p' "$gate")"
for pair in "android:$android_sha" "apple:$apple_sha"; do
  name="${pair%%:*}"
  sha="${pair#*:}"
  if [ "${#sha}" -ne 40 ]; then
    printf 'FAIL: cannot read the verified %s SHA from %s\n' "$name" "$gate" >&2
    exit 1
  fi
  case "$sha" in
    *[!0-9a-f]*)
      printf 'FAIL: the verified %s SHA is not hexadecimal: %s\n' "$name" "$sha" >&2
      exit 1
      ;;
  esac
done

sandbox="$(mktemp -d)"
trap 'rm -rf "$sandbox"' EXIT

mkdir -p "$sandbox/scripts/ci" "$sandbox/docs/fork"
cp "$gate" "$sandbox/scripts/ci/check-libbox-abi.sh"
cp "$contract" "$sandbox/docs/fork/libbox-abi-contract.tsv"
printf '# fixture\n' > "$sandbox/README.md"

git -C "$sandbox" init -q
git_commit() {
  git -C "$sandbox" -c user.name=abi-gate-test -c user.email=abi-gate-test@example.invalid \
    -c commit.gpgsign=false commit -q -m "$1"
}
git -C "$sandbox" add README.md
git_commit base

# A gitlink is an index entry; the submodule itself never needs to be checked out for the pin gate.
git -C "$sandbox" update-index --add --cacheinfo 160000 "$apple_sha" clients/apple
git -C "$sandbox" update-index --add --cacheinfo 160000 "$android_sha" clients/android
git_commit "correct pins"

out="$sandbox/pin.out"
if ! (cd "$sandbox" && bash scripts/ci/check-libbox-abi.sh --pin-only) >"$out" 2>&1; then
  cat "$out" >&2
  printf 'FAIL: the pin gate rejected a correct android/apple pair\n' >&2
  exit 1
fi

git -C "$sandbox" rm --cached -q clients/apple
git_commit "missing apple gitlink"

if (cd "$sandbox" && bash scripts/ci/check-libbox-abi.sh --pin-only) >"$out" 2>&1; then
  cat "$out" >&2
  printf 'FAIL: a missing client gitlink was reported as PASS\n' >&2
  exit 1
fi

if ! grep -q 'FAIL: apple: no gitlink at clients/apple' "$out"; then
  cat "$out" >&2
  printf 'FAIL: the missing-gitlink failure did not name the missing client\n' >&2
  exit 1
fi

printf 'PASS: the ABI pin gate accepts a correct pair and fails closed on a missing gitlink.\n'
