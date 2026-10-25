#!/usr/bin/env bash
# The iOS build must not call the implicit Direct Fast Path decision.
#
# # Why this gate exists
#
# An implicit bypass leaves the router as tun.ActionBypass with no Port, and the pinned sing-tun
# turns that into exactly the state an accept produces:
#
#   flow_dispatch.go install()      ActionFlow builds a port flow; every other action - accept and
#                                   bypass alike - falls through to installAccept(), which records
#                                   the entry as ActionAccept and keeps the verdict only so the
#                                   gvisor forwarders need not judge the flow twice
#   flow_dispatch.go judge()        rewrites "ActionBypass && Port != nil" to ActionFlow; with no
#                                   Port this cannot fire
#   stack_gvisor_tcp.go / _udp.go   switch on Reject / Drop / HijackDNS only
#   stack_go_udp.go                 the same three cases
#   stack_gvisor_icmp.go            ActionFlow and ActionBypass together, and ICMP is never
#                                   eligible (canFastBypass requires TCP or UDP)
#
# So on iOS the group authorisation, the AI keyword scan and the twelve eligibility predicates
# cannot change where the bytes go. route.implicitDirectFastPathIsExecutable therefore guards the
# whole decision with the compile-time constant !C.IsIos, which removes the calls from the iOS
# binary instead of evaluating them and discarding the answer.
#
# A unit test cannot observe that removal, because the test suite does not run as an iOS binary.
# This gate can: it compiles the package for GOOS=ios and reads what the compiler emitted.
#
# # Falsifiability
#
# A check for the absence of something is worthless if the thing could never have appeared. The
# script therefore also asserts two presence controls in the same output:
#
#   * resolveTrafficClass is still called in the iOS build - the traffic_class decision is a
#     separate consumer later in the same function and must not be removed by this subtraction;
#   * the non-iOS build still calls both fast-path symbols - the mechanism is only compiled out on
#     the platform that cannot honour it.
#
# If either control is missing, the matcher or the build is broken and the absence proves nothing,
# so the gate fails.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

# Every emitted CALL in a `go build -gcflags=-S` stream is rendered as
#
#   0x00f8 00248 (…/route.go:992)	CALL	github.com/sagernet/sing-box/route.f(SB)
#
# so the symbol is matched whole, anchored on the CALL and the (SB) relocation suffix, and counted
# rather than merely found: the positive controls need a count, and a count of zero is also what
# `grep -c` reports through a non-zero exit status when nothing matches.
count_calls() {
    local symbol="$1" file="$2"
    grep -c -E "CALL[[:space:]]+${symbol}\(SB\)" "$file" || true
}

emit_assembly() {
    local file="$1" goos="$2" goarch="$3"
    GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
        go build -gcflags="github.com/sagernet/sing-box/route=-S" ./route/ 2>"$file"
}

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fast_path_request='github\.com/sagernet/sing-box/route\.resolveFastPathRequest'
fast_path_gate='github\.com/sagernet/sing-box/route\.\(\*Router\)\.canFastBypass'
traffic_class='github\.com/sagernet/sing-box/route\.resolveTrafficClass'

# --- 1. The iOS build has no call to either half of the decision -------------------------------
emit_assembly "$tmp/ios.asm" ios arm64

ios_request="$(count_calls "$fast_path_request" "$tmp/ios.asm")"
ios_gate="$(count_calls "$fast_path_gate" "$tmp/ios.asm")"
if [ "$ios_request" != "0" ] || [ "$ios_gate" != "0" ]; then
    fail "the iOS build still calls the implicit Direct Fast Path decision" \
        "(resolveFastPathRequest: ${ios_request} call(s), canFastBypass: ${ios_gate} call(s)):" \
        "the verdict cannot change the iOS data path, so route.implicitDirectFastPathIsExecutable" \
        "must keep it compiled out. See route/fast_path.go."
fi

# --- 2. Presence control: the same iOS output still contains a live decision ------------------
# This is what separates "the branch was removed" from "the compiler never produced anything".
ios_traffic="$(count_calls "$traffic_class" "$tmp/ios.asm")"
if [ "$ios_traffic" -lt 1 ]; then
    fail "the iOS assembly contains no resolveTrafficClass call, so this build did not compile" \
        "route and the absence check above proves nothing"
fi

# --- 3. Presence control: the mechanism is still there where it can be honoured ---------------
emit_assembly "$tmp/host.asm" linux amd64

host_request="$(count_calls "$fast_path_request" "$tmp/host.asm")"
host_gate="$(count_calls "$fast_path_gate" "$tmp/host.asm")"
if [ "$host_request" -lt 1 ] || [ "$host_gate" -lt 1 ]; then
    fail "the non-iOS build no longer calls the implicit Direct Fast Path decision" \
        "(resolveFastPathRequest: ${host_request} call(s), canFastBypass: ${host_gate} call(s)):" \
        "the guard must be !C.IsIos, not a blanket removal"
fi

echo "PASS: the iOS build compiles the implicit Direct Fast Path decision out" \
    "(resolveFastPathRequest: 0, canFastBypass: 0) while still calling traffic_class (${ios_traffic})"
echo "PASS: the non-iOS build still calls it (resolveFastPathRequest: ${host_request}," \
    "canFastBypass: ${host_gate})"
