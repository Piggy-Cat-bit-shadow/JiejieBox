#!/usr/bin/env bash
# The Apple Libbox canonical-asset name must be ONE fact, and this asserts the two sides agree.
#
# # The defect this guards
#
# The name existed in two places that were each self-consistently wrong:
#
#   * `release-artifacts.sh` published `Libbox.xcframework.tar.gz`;
#   * `publish-apple-beta.sh` looked for `jiejie-libbox-apple-<version>.tar.gz`.
#
# Neither side is broken in isolation. `test-apple-canonical-source.sh` passed, `check-upload` passed,
# and the release published seven assets that satisfied its own contract - while the canonical path was
# dead on arrival, because the publisher could never find the asset. The publisher then fell back to the
# Actions artifact SILENTLY, which is precisely the downgrade the PRESENT_INVALID rule was written to
# prevent. It arrived through a filename instead of through validation, so no content check could see it.
#
# This is the same shape as the v0.1.8 six-of-seven defect documented in `public_asset_names`: a check
# and the thing it checks agreeing with each other is not evidence that either is right.
#
# So this test does not inspect either side's text. It RUNS the contract and compares its answer with
# the name the publisher actually uses, for a version that does not exist yet, so a stale literal
# cannot satisfy it.
#
# Usage: test-apple-libbox-asset-name.sh
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

publish="./scripts/publish-apple-beta.sh"
contract="./scripts/ci/release-artifacts.sh"
pass=0
fail=0

ok()  { echo "  PASS: $1"; pass=$((pass + 1)); }
bad() { echo "  FAIL: $1" >&2; shift; [ $# -gt 0 ] && printf '        %s\n' "$@" >&2; fail=$((fail + 1)); }

[ -f "$publish" ] || { echo "FAIL: $publish not found" >&2; exit 1; }
[ -f "$contract" ] || { echo "FAIL: $contract not found" >&2; exit 1; }

# A version that does not exist in the repository, so a hardcoded literal cannot accidentally match.
probe_version="9.9.9"
probe_tag="v${probe_version}"

echo "== the contract's public asset list, for $probe_tag =="

contract_names="$(RELEASE_EXTRA_PLATFORMS=true "$contract" expected-assets --tag "$probe_tag" --version "$probe_version" 2>/dev/null)" \
  || { echo "FAIL: expected-assets exited non-zero" >&2; exit 1; }

printf '%s\n' "$contract_names" | sed 's/^/    /'

apple_from_contract="$(printf '%s\n' "$contract_names" | grep -F 'libbox' || true)"
if [ -z "$apple_from_contract" ]; then
  bad "the contract publishes exactly one libbox asset" "expected-assets named none"
elif [ "$(printf '%s\n' "$apple_from_contract" | wc -l)" -ne 1 ]; then
  bad "the contract publishes exactly one libbox asset" "named more than one: $apple_from_contract"
else
  ok "the contract publishes exactly one libbox asset"
fi

echo
echo "== the name the publisher actually looks for =="

# Read the name out of the publisher by EXECUTING the assignment, not by grepping the pattern. A grep
# for the literal shape would also match a comment, and would not resolve ${jj_version}.
publisher_name="$(bash -c '
  set -euo pipefail
  jj_version="'"$probe_version"'"
  # Execute only the assignment line, so the rest of the script (which needs git, gh and a clean tree)
  # does not run.
  eval "$(grep -m1 "^libbox_asset=" '"$publish"')"
  printf "%s" "$libbox_asset"
')" || { echo "FAIL: could not read libbox_asset from $publish" >&2; exit 1; }

echo "    $publisher_name"

echo
echo "== they must agree =="

if [ "$apple_from_contract" = "$publisher_name" ]; then
  ok "the published name and the consumed name are the same string"
else
  bad "the published name and the consumed name DIFFER - the canonical path is dead on arrival" \
    "release publishes : $apple_from_contract" \
    "publisher wants   : $publisher_name" \
    "Neither side is wrong in isolation, which is why this needs a test that runs both."
fi

# The name must carry the version. An unversioned name cannot say which release it belongs to, and the
# publisher derives its expectation FROM the version, so an unversioned asset can never match.
case "$apple_from_contract" in
  *"$probe_version"*)
    ok "the asset name carries the release version" ;;
  *)
    bad "the asset name must carry the release version" \
      "got: $apple_from_contract (no '$probe_version')" ;;
esac

echo
if [ "$fail" -gt 0 ]; then
  echo "FAILED: $fail of $((pass + fail)) checks" >&2
  exit 1
fi
echo "PASS: all $pass checks"
