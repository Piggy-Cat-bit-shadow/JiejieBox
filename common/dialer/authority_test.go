package dialer

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/stretchr/testify/require"
)

// TestResolveAuthorityWithoutATransportManager pins the nil-service case.
//
// # The defect this covers
//
// `service.FromContext` answers nil for a context that has fewer services registered than a running
// box - a test double, or an embedder that drives a dialer directly. The first version of
// ResolveAuthority dereferenced the transport manager in the NAMED-resolver branch, so a context
// without one PANICKED where every other branch of the same function reported honestly. Found by an
// adversarial review; the sibling branch below (no network manager) had always handled it.
//
// The three cases are asserted separately because they answer DIFFERENT questions: a named resolver
// is a configuration fact, while `newDialer` decides whether its absence is fatal for the caller's
// purpose - the distinction `NewDNSQueryOptions` documents.
func TestResolveAuthorityWithoutATransportManager(t *testing.T) {
	t.Parallel()

	// A named resolver, target-resolution caller: nothing can resolve, but that is not an error for
	// this caller - it reports "no authority" and the caller decides.
	authority, err := ResolveAuthority(context.Background(), &option.DomainResolveOptions{Server: "absent"}, false)
	require.NoError(t, err, "a target-resolution caller must not be handed a fatal error here")
	require.Equal(t, AuthorityNone, authority)

	// A named resolver, DIALER-building caller: the connection cannot be made at all, which is the
	// one case where the absence is fatal.
	authority, err = ResolveAuthority(context.Background(), &option.DomainResolveOptions{Server: "absent"}, true)
	require.Error(t, err, "a dialer for a domain SERVER cannot be built without a resolver")
	require.Contains(t, err.Error(), "absent",
		"and the error must name the resolver that could not be found; got: "+err.Error())
	require.Equal(t, AuthorityNone, authority)

	// No resolver named and no manager: nothing to resolve with, reported rather than dereferenced.
	authority, err = ResolveAuthority(context.Background(), nil, false)
	require.NoError(t, err)
	require.Equal(t, AuthorityNone, authority)
}
