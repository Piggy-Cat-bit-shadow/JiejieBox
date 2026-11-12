#!/usr/bin/env sh
# Run one data-plane test from ./e2e inside a namespace that owns a REAL TUN device and a
# 1280-MTU veth pair (audit Q17).
#
# # Why this exists
#
# The e2e TUN harness (e2e/tun_chain_test.go, e2e/tun_udp_detour_test.go) drives a memory TUN and
# says in its own header that the device half - a real utun, a route table entry, a network
# namespace - needs root and is therefore unverified. That conclusion was re-checked in round 06 and
# is wrong on Linux with user namespaces: `unshare -Urnm` gives an unprivileged user a namespace
# where it IS root with CAP_NET_ADMIN, `ip tuntap add` creates a TUN device, `/dev/net/tun` is
# world-writable, and a veth pair can be created at MTU 1280. So the device half is testable here;
# what was missing was the runner, not the privilege.
#
# # Usage
#
#   scripts/ci/tun-netns.sh 'TestMyRealDeviceChain'
#
# The pattern is handed to `go test -run`, so an empty match is a pass: this script proves the
# namespace plumbing even before the device-half test exists, and becomes the real entry point as
# soon as one does.
#
# # What the namespace contains
#
#   lo up
#   tun0        a real TUN device (the device half the harness needs)
#   va / vb     a veth pair, both ends at MTU 1280 (the narrow link the detour must cross)
#   va          10.20.0.1/24, vb 10.20.0.2/24
#
# Nothing is configured on tun0 beyond "up": the test owns its addressing, exactly as a product
# would. The script does not touch the host's network: everything lives in the namespace and is
# destroyed when it exits.

set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
pattern=${1:-'^$'}

if [ -z "${GOPATH:-}" ]; then
	GOPATH="$HOME/gopath"
	export GOPATH
fi
if ! command -v go >/dev/null 2>&1; then
	PATH="$HOME/go/bin:$PATH"
	export PATH
fi
if ! command -v go >/dev/null 2>&1; then
	echo "tun-netns: no go toolchain found (set PATH or install go)" >&2
	exit 1
fi

# Inside the namespace the test binary must be able to open /dev/net/tun as the namespace root.
# Capabilities inside a user namespace are not the host's, so this is safe to run unprivileged.
inner='
set -eu
ip link set lo up
ip tuntap add dev tun0 mode tun
ip link set tun0 up
ip link add va type veth peer name vb
ip link set va mtu 1280 up
ip link set vb mtu 1280 up
ip addr add 10.20.0.1/24 dev va
ip addr add 10.20.0.2/24 dev vb
cd '"$root"'
exec go test -count=1 -timeout 300s -tags with_quic -run '"$pattern"' ./e2e/
'

if [ "$(id -u)" -eq 0 ]; then
	sh -c "$inner"
else
	if unshare -Urnm --propagation private true 2>/dev/null; then
		unshare -Urnm --propagation private sh -c "$inner"
	else
		echo "tun-netns: this kernel refuses an unprivileged user+network namespace." >&2
		echo "  Re-check with: unshare -Urnm sh -c 'ip tuntap add dev tun0 mode tun'" >&2
		echo "  The audit's rule applies: report the capability boundary, do not generalize it." >&2
		exit 1
	fi
fi
