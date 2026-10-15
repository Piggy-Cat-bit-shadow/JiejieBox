package route

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// These two tests are the regression guard for the FreeBSD build break.
//
// # The defect they pin
//
// interfaceNameByIndex lives in network_family_route_ifname.go, which used to carry
//
//	//go:build windows || linux || darwin
//
// while its only caller, usableGlobalIPv6Route, lives in network_family_route.go, which carries no
// build tag at all. The tag therefore was not protecting anything about the HELPER - net.InterfaceByIndex
// is standard library - it was only restating which platforms have a route reader of their own, and it
// made the route package fail to COMPILE on every other platform:
//
//	GOOS=freebsd GOARCH=amd64 go build ./route/
//	route/network_family_route.go:94:22: undefined: interfaceNameByIndex
//
// Linux and Windows both passed, so the break was invisible to the two platforms that were easiest to
// check. That is the general shape this guards against, not just the one tag.

// TestInterfaceNameByIndexIsAvailableOnEveryPlatform is the compile-level half.
//
// A reference to the helper from a file with no build tag means the route package cannot compile
// anywhere without it. Put the tag back and this file stops building on the platforms it excluded -
// which is the same failure the tag caused, now caught by the ordinary build the repository already
// runs instead of by a FreeBSD job nobody watches.
func TestInterfaceNameByIndexIsAvailableOnEveryPlatform(t *testing.T) {
	// The loopback interface is index 1 on every platform Go supports, and it is the one index a
	// test can name without enumerating first.
	name, err := interfaceNameByIndex(1)
	require.NoError(t, err, "the loopback interface must be resolvable by index")
	require.NotEmpty(t, name, "a resolved interface must have a name")

	// The helper must be the standard library's answer and not a platform-specific invention: the
	// two disagree the moment someone "fixes" this by hand-rolling a lookup for one OS.
	expected, expectedErr := net.InterfaceByIndex(1)
	require.NoError(t, expectedErr)
	require.Equal(t, expected.Name, name)
}

// TestInterfaceNameByIndexReportsInsteadOfGuessing is the behaviour half.
//
// The caller matches a route's egress interface against this process's own interfaces by NAME, and it
// treats an error as "the name is unknown" rather than as "the route is not ours". That polarity is
// load-bearing: an index that cannot be resolved must not be reported as a name that happens to
// match something, and it must not panic - the caller is on the route-observation path, which runs on
// every network event.
func TestInterfaceNameByIndexReportsInsteadOfGuessing(t *testing.T) {
	name, err := interfaceNameByIndex(-1)

	require.Error(t, err, "an index that names no interface must report rather than invent a name")
	require.Empty(t, name, "a failed lookup must not return a usable-looking name")
}
