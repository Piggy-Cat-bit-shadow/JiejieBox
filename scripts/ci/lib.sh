#!/usr/bin/env bash
# Shared helpers for the Jiejie CI scripts.
#
# This file is in release-contract-verify.yml's `paths` filter, which is why the final-head evidence
# commit touches it and nothing else: a green run whose `head_sha` is not the final commit is not
# final-freeze evidence, and the workflows a round changed are not all watched by the same filter.
set -euo pipefail

# sha256_of prints the SHA-256 of a file, portable across Linux and macOS.
#
# `sha256sum` on Linux, `shasum -a 256` on macOS. The Apple supply chain depends on this agreeing
# across both: the Libbox manifest records one digest and three different consumers verify it -
# `apple-libbox-artifact.sh validate`, the release's `verify_apple`, and `publish-apple-beta.sh` -
# so a host that computed a different digest would fail the release rather than silently accept
# different bytes.
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}
export -f sha256_of 2>/dev/null || true
