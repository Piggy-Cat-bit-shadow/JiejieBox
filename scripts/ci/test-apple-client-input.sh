#!/usr/bin/env bash
#
# Behavioural tests for scripts/ci/apple-client-input.sh.
#
# The script decides which Apple client commit a run builds. Its whole point is that the answer is
# never ambiguous, so every case here asserts one of two things: the printed answer, or a REFUSAL.
# A case that expects a refusal also asserts that nothing usable was printed - a script that fails
# after printing a SHA is worse than one that returns the wrong SHA, because the caller can export
# the wrong one and never notice.
#
# Usage: scripts/ci/test-apple-client-input.sh
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tool="$here/apple-client-input.sh"

passed=0
failed=0

report() {
    local name="$1" ok="$2" detail="${3:-}"
    if [ "$ok" = "1" ]; then
        passed=$((passed + 1))
        printf 'PASS  %s\n' "$name"
    else
        failed=$((failed + 1))
        printf 'FAIL  %s%s\n' "$name" "${detail:+: $detail}"
    fi
}

sha_a="1111111111111111111111111111111111111111"
sha_b="2222222222222222222222222222222222222222"

# run <expect-exit> <env assignments...>
run() {
    local expect="$1"; shift
    env -i PATH="$PATH" HOME="${HOME:-/tmp}" "$@" "$tool" resolve 2>&1
    return $?
}

# case_ok <name> <expected KEY=VALUE lines> <env assignments...>
case_ok() {
    local name="$1" expected="$2"; shift 2
    local out rc=0
    out="$(env -i PATH="$PATH" HOME="${HOME:-/tmp}" "$@" "$tool" resolve 2>&1)" || rc=$?
    if [ "$rc" -ne 0 ]; then
        report "$name" 0 "expected success, got exit $rc: $out"
        return
    fi
    local got
    got="$(printf '%s\n' "$out" | grep '^APPLE_CLIENT_' | sort)"
    local want
    want="$(printf '%s\n' "$expected" | sort)"
    if [ "$got" = "$want" ]; then
        report "$name" 1
    else
        report "$name" 0 "got [$got] want [$want]"
    fi
}

# case_fail <name> <reason substring> <env assignments...>
case_fail() {
    local name="$1" reason="$2"; shift 2
    local out rc=0
    out="$(env -i PATH="$PATH" HOME="${HOME:-/tmp}" "$@" "$tool" resolve 2>&1)" || rc=$?
    if [ "$rc" -eq 0 ]; then
        report "$name" 0 "expected a refusal, got exit 0: $out"
        return
    fi
    if ! printf '%s' "$out" | grep -q "$reason"; then
        report "$name" 0 "refused without saying why ('$reason'): $out"
        return
    fi
    if printf '%s' "$out" | grep -q '^APPLE_CLIENT_EFFECTIVE_SHA=.'; then
        report "$name" 0 "printed an effective SHA while refusing: $out"
        return
    fi
    report "$name" 1
}

# --- the default path: no input, no variables ------------------------------------------------
case_ok "no input and no variables means no override (main tip)" \
    "APPLE_CLIENT_EFFECTIVE_SHA=
APPLE_CLIENT_EFFECTIVE_REF=
APPLE_CLIENT_OVERRIDE=none" \
    APPLE_CLIENT_REF= APPLE_CLIENT_SHA=

# --- the dispatch input, alone ---------------------------------------------------------------
case_ok "an exact input SHA is the answer, and it says so" \
    "APPLE_CLIENT_EFFECTIVE_SHA=$sha_a
APPLE_CLIENT_EFFECTIVE_REF=
APPLE_CLIENT_OVERRIDE=input" \
    APPLE_CLIENT_INPUT_SHA="$sha_a" APPLE_CLIENT_REF= APPLE_CLIENT_SHA=

case_ok "an input SHA that agrees with the repository variable is accepted" \
    "APPLE_CLIENT_EFFECTIVE_SHA=$sha_a
APPLE_CLIENT_EFFECTIVE_REF=
APPLE_CLIENT_OVERRIDE=input" \
    APPLE_CLIENT_INPUT_SHA="$sha_a" APPLE_CLIENT_REF= APPLE_CLIENT_SHA="$sha_a"

# Trailing whitespace from a copy-paste is normalised, not rejected.
case_ok "surrounding whitespace on the input is trimmed" \
    "APPLE_CLIENT_EFFECTIVE_SHA=$sha_a
APPLE_CLIENT_EFFECTIVE_REF=
APPLE_CLIENT_OVERRIDE=input" \
    APPLE_CLIENT_INPUT_SHA="  $sha_a  " APPLE_CLIENT_REF= APPLE_CLIENT_SHA=

# --- refusals ---------------------------------------------------------------------------------
case_fail "an abbreviated SHA is refused" "not a 40-character lowercase commit SHA" \
    APPLE_CLIENT_INPUT_SHA="${sha_a:0:12}" APPLE_CLIENT_REF= APPLE_CLIENT_SHA=

case_fail "an uppercase SHA is refused rather than lowercased silently" "not a 40-character lowercase commit SHA" \
    APPLE_CLIENT_INPUT_SHA="111111111111111111111111111111111111111A" APPLE_CLIENT_REF= APPLE_CLIENT_SHA=

case_fail "a branch name in the SHA input is refused" "not a 40-character lowercase commit SHA" \
    APPLE_CLIENT_INPUT_SHA="main" APPLE_CLIENT_REF= APPLE_CLIENT_SHA=

case_fail "an input SHA that contradicts the repository SHA is refused" "conflict" \
    APPLE_CLIENT_INPUT_SHA="$sha_a" APPLE_CLIENT_REF= APPLE_CLIENT_SHA="$sha_b"

case_fail "an input SHA beside a repository REF is refused" "APPLE_CLIENT_REF names" \
    APPLE_CLIENT_INPUT_SHA="$sha_a" APPLE_CLIENT_REF="main" APPLE_CLIENT_SHA=

# --- the repository-variable path (unchanged behaviour) ---------------------------------------
case_ok "a repository SHA alone is still honoured" \
    "APPLE_CLIENT_EFFECTIVE_SHA=$sha_b
APPLE_CLIENT_EFFECTIVE_REF=
APPLE_CLIENT_OVERRIDE=variable" \
    APPLE_CLIENT_REF= APPLE_CLIENT_SHA="$sha_b"

case_ok "a repository REF alone is still honoured" \
    "APPLE_CLIENT_EFFECTIVE_SHA=
APPLE_CLIENT_EFFECTIVE_REF=v0.1.30
APPLE_CLIENT_OVERRIDE=variable" \
    APPLE_CLIENT_REF=v0.1.30 APPLE_CLIENT_SHA=

case_fail "REF and SHA repository variables together are refused" "both set" \
    APPLE_CLIENT_REF=main APPLE_CLIENT_SHA="$sha_b"

# --- usage ------------------------------------------------------------------------------------
if env -i PATH="$PATH" "$tool" >/dev/null 2>&1; then
    report "the script requires a subcommand" 0 "exited 0 with no subcommand"
else
    report "the script requires a subcommand" 1
fi

printf '\n%d passed, %d failed\n' "$passed" "$failed"
[ "$failed" -eq 0 ]
