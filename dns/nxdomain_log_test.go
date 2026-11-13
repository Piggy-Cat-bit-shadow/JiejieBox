package dns

import (
	"context"
	"errors"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// A normal NXDOMAIN is a negative answer, not a resolver fault. It must stay an ERROR-free lookup:
// the error is still returned, its RCODE is unchanged, and only the log level moves.
func TestNXDomainLookupLogsAtDebug(t *testing.T) {
	transport := &fakeDNSTransport{tag: "final", rcode: mDNS.RcodeNameError}
	capture := &capturingLogger{}
	router := quietTestRouter(t, transport, capture)

	addresses, err := router.Lookup(context.Background(), "todesk-log-upload.todesk.com", adapter.DNSQueryOptions{})
	require.Error(t, err, "NXDOMAIN must still be returned as an error to the caller")
	require.Empty(t, addresses)
	require.True(t, IsNXDomain(err), "the returned error must keep the NXDOMAIN rcode identity")

	errors, debugs := capture.snapshot()
	require.Empty(t, errors, "a normal NXDOMAIN must not be logged at ERROR; got %v", errors)
	require.Contains(t, joinAll(debugs), "lookup failed for todesk-log-upload.todesk.com",
		"the negative answer must still be visible at DEBUG; got %v", debugs)
	require.Contains(t, joinAll(debugs), "NXDOMAIN")
}

// The streaming path issues A and AAAA separately; each used to print `lookup failed` at ERROR.
// Both must now be silent at ERROR while the per-family error and the aggregate failure remain.
func TestNXDomainFamilyLookupLogsAtDebug(t *testing.T) {
	transport := &fakeDNSTransport{tag: "final", rcode: mDNS.RcodeNameError}
	capture := &capturingLogger{}
	router := quietTestRouter(t, transport, capture)

	err := router.LookupFamilies(context.Background(), "todesk-log-upload.todesk.com", adapter.DNSQueryOptions{},
		func(adapter.DNSFamilyResult) {})
	require.Error(t, err, "both families missing means the aggregate lookup still fails")

	errors, debugs := capture.snapshot()
	require.Empty(t, errors, "no family may log a normal NXDOMAIN at ERROR; got %v", errors)
	require.Contains(t, joinAll(debugs), "lookup failed for todesk-log-upload.todesk.com",
		"each family's negative answer must still be visible at DEBUG; got %v", debugs)
}

// The negative controls: every real DNS failure keeps its ERROR line, so the change cannot become
// "silence every lookup error".
func TestRealDNSFailuresStillLogAtError(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		transport *fakeDNSTransport
	}{
		{name: "SERVFAIL", transport: &fakeDNSTransport{tag: "final", rcode: mDNS.RcodeServerFailure}},
		{name: "timeout", transport: &fakeDNSTransport{tag: "final", exchangeErr: context.DeadlineExceeded}},
		{name: "network unreachable", transport: &fakeDNSTransport{tag: "final", exchangeErr: errors.New("network unreachable")}},
		{name: "protocol error", transport: &fakeDNSTransport{tag: "final", exchangeErr: errors.New("malformed DNS response")}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			capture := &capturingLogger{}
			router := quietTestRouter(t, testCase.transport, capture)

			_, err := router.Lookup(context.Background(), "broken.example", adapter.DNSQueryOptions{})
			require.Error(t, err)
			require.False(t, IsNXDomain(err), "%s is not NXDOMAIN", testCase.name)

			errors, _ := capture.snapshot()
			require.Len(t, errors, 1, "a genuine DNS failure must keep reporting at ERROR; got %v", errors)
			require.Contains(t, joinAll(errors), "lookup failed for broken.example")
		})
	}
}

// IsNXDomain must require every leaf of an aggregated error to be NXDOMAIN. One family answering
// NXDOMAIN and the other failing is still a failed lookup, and must not be demoted by errors.Is's
// "any part" semantics.
func TestNXDomainClassificationRequiresEveryLeaf(t *testing.T) {
	nxdomain := E.Cause(RcodeError(mDNS.RcodeNameError), "lookup nx.example")
	require.True(t, IsNXDomain(nxdomain), "a wrapped NXDOMAIN is still NXDOMAIN")
	require.True(t, IsNXDomain(E.Errors(nxdomain, RcodeError(mDNS.RcodeNameError))),
		"both families answering NXDOMAIN is a normal negative answer")
	require.True(t, IsNXDomain(LocalVerdictRcode(RcodeNameError)),
		"a local NXDOMAIN verdict keeps the same normal-negative log level")

	require.False(t, IsNXDomain(E.Errors(nxdomain, RcodeError(mDNS.RcodeServerFailure))),
		"one family NXDOMAIN and the other SERVFAIL is still a failure")
	require.False(t, IsNXDomain(E.Errors(nxdomain, context.DeadlineExceeded)),
		"one family NXDOMAIN and the other timeout is still a failure")
	require.False(t, IsNXDomain(RcodeError(mDNS.RcodeServerFailure)))
	require.False(t, IsNXDomain(errors.New("NXDOMAIN")),
		"classification is by the RcodeError type, never by the text")
	require.False(t, IsNXDomain(nil))
}
