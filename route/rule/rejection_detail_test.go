package rule

// The side-effect-free reject description a diagnostic uses.
//
// `Error` is the DATA-PLANE refusal: it feeds the drop counter and the flood limiter, and for
// `method: reply` it returns a nil error because the inbound layer writes the reply. A diagnostic
// that explains a reject must neither consume the flood budget nor dereference that nil - the
// reviewed crash. `RejectionDetail` is that explanation, and these tests pin both halves: the text
// per method and transport, and the fact that it leaves the limiter untouched.

import (
	"context"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func TestTheRejectDetailNamesTheMethodAndTheTransportLimit(t *testing.T) {
	testCases := []struct {
		method   string
		network  string
		expected string
	}{
		{C.RuleActionRejectMethodDefault, N.NetworkTCP, "reject method `default`"},
		{C.RuleActionRejectMethodDrop, N.NetworkTCP, "reject method `drop`"},
		{C.RuleActionRejectMethodReply, N.NetworkTCP, "reject method `reply` is not supported for TCP connections"},
		{C.RuleActionRejectMethodReply, N.NetworkUDP, "reject method `reply` is not supported for UDP connections"},
		{C.RuleActionRejectMethodReply, "", "reject method `reply` answers this request with a rejection reply"},
		// Production panics on an unknown method; the explanation reports it instead of dying.
		{"bogus", N.NetworkTCP, "reject method `bogus`"},
	}
	for _, testCase := range testCases {
		action := &RuleActionReject{Method: testCase.method}
		require.Equal(t, testCase.expected, action.RejectionDetail(testCase.network),
			"method=%q network=%q", testCase.method, testCase.network)
	}
}

// TestTheRejectDetailDoesNotConsumeTheDropBudget is the data-plane half: explaining a reject 200
// times must not trip the flood limiter (more than 50 drops in 30 s turns the next real rejection
// into a drop), and the same action must still record a real refusal through Error.
func TestTheRejectDetailDoesNotConsumeTheDropBudget(t *testing.T) {
	action := &RuleActionReject{Method: C.RuleActionRejectMethodDefault}
	for index := 0; index < 200; index++ {
		_ = action.RejectionDetail(N.NetworkTCP)
	}
	require.Empty(t, action.dropCounter,
		"a diagnostic explanation must not feed the flood limiter")

	require.Error(t, action.Error(context.Background()))
	require.Len(t, action.dropCounter, 1,
		"the data-plane refusal still counts, which is what makes the empty counter meaningful")
}
