#!/usr/bin/env bash
#
# Which Apple client source a client-apple.yml run builds, and who said so.
#
# # Why this is a script and not four lines in the workflow
#
# The workflow has three ways to name an Apple source and they must not fight:
#
#   workflow_dispatch input   APPLE_CLIENT_INPUT_SHA  - a maintainer validating one candidate
#   repository variable       APPLE_CLIENT_REF / APPLE_CLIENT_SHA - reproducing an older build
#   the refs file             release/apple-client-refs.env - the default, the tip of main
#
# Precedence alone is not enough, because "the input won silently" and "the variable was ignored"
# are indistinguishable to a reader of the run afterwards - and a run that built one commit while
# the dispatch said another is exactly the class of defect this pipeline exists to prevent. So:
# an explicit input wins, a CONFLICT fails closed, and the answer says which source produced it.
#
# # Usage
#
#   apple-client-input.sh resolve
#
# Environment (all optional):
#   APPLE_CLIENT_INPUT_SHA   the dispatch input; must be a full 40-character lowercase commit SHA
#   APPLE_CLIENT_REF         repository variable: a ref to build instead of the default branch
#   APPLE_CLIENT_SHA         repository variable: an exact commit to build
#
# Output (stdout, shell-assignable, one per line):
#   APPLE_CLIENT_EFFECTIVE_SHA   the SHA to pin, or empty when there is no override
#   APPLE_CLIENT_EFFECTIVE_REF   the ref override to pass through, or empty
#   APPLE_CLIENT_OVERRIDE        input | variable | none
#
# Exit status: 0 the answer is usable, 1 the inputs contradict each other or the SHA is malformed.
set -euo pipefail

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

is_sha() {
    case "$1" in
        *[!0-9a-f]*) return 1 ;;
    esac
    [ "${#1}" -eq 40 ]
}

cmd="${1:-}"
[ "$cmd" = "resolve" ] || { echo "usage: apple-client-input.sh resolve" >&2; exit 2; }

input_sha="${APPLE_CLIENT_INPUT_SHA:-}"
variable_ref="${APPLE_CLIENT_REF:-}"
variable_sha="${APPLE_CLIENT_SHA:-}"

# Trailing whitespace from a copy-paste is the most common way a 40-hex value arrives as 41
# characters, and the failure it produces ("not a commit") does not name the whitespace.
input_sha="${input_sha#"${input_sha%%[![:space:]]*}"}"
input_sha="${input_sha%"${input_sha##*[![:space:]]}"}"

if [ -z "$input_sha" ]; then
    # No input: the repository variables behave exactly as they did before this existed.
    if [ -n "$variable_ref" ] && [ -n "$variable_sha" ]; then
        fail "APPLE_CLIENT_REF and APPLE_CLIENT_SHA are both set (${variable_ref} / ${variable_sha}).
        They name two different sources for one run. Clear one of them; a build that silently
        picks one is a build whose source cannot be read from its configuration."
    fi
    echo "APPLE_CLIENT_EFFECTIVE_SHA=${variable_sha}"
    echo "APPLE_CLIENT_EFFECTIVE_REF=${variable_ref}"
    if [ -n "$variable_ref" ] || [ -n "$variable_sha" ]; then
        echo "APPLE_CLIENT_OVERRIDE=variable"
    else
        echo "APPLE_CLIENT_OVERRIDE=none"
    fi
    exit 0
fi

if ! is_sha "$input_sha"; then
    fail "'${input_sha}' is not a 40-character lowercase commit SHA.
        The apple_client_sha input takes an exact commit, not a branch, tag, abbreviation or
        uppercase form. Resolve the branch first: git ls-remote <repo> refs/heads/<branch>"
fi

if [ -n "$variable_ref" ] && [ "$variable_ref" != "$input_sha" ]; then
    fail "the run asks for apple_client_sha=${input_sha} but the repository variable
        APPLE_CLIENT_REF names '${variable_ref}'. The dispatch input wins, which would make the
        variable a lie; clear APPLE_CLIENT_REF (or dispatch without the input) instead."
fi

if [ -n "$variable_sha" ] && [ "$variable_sha" != "$input_sha" ]; then
    fail "the run asks for apple_client_sha=${input_sha} but the repository variable
        APPLE_CLIENT_SHA names '${variable_sha}'. Two exact commits for one run is a conflict,
        not a preference; clear APPLE_CLIENT_SHA or dispatch the SHA it names."
fi

# The resolver reads APPLE_CLIENT_SHA and APPLE_CLIENT_REF from its environment; the caller
# exports what this prints, so the input arrives there as the SHA it validated.
echo "APPLE_CLIENT_EFFECTIVE_SHA=${input_sha}"
echo "APPLE_CLIENT_EFFECTIVE_REF="
echo "APPLE_CLIENT_OVERRIDE=input"
