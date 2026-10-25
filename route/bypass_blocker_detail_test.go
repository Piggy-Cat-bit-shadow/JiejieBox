package route

import (
	"net/netip"

	"github.com/sagernet/sing-box/common/dialer"
)

// bypassBlockerReporter is the optional richer half of adapter.BypassableOutbound.
//
// It used to live in bypass_verdict.go, and this file is where it belongs: a full-repository
// reference search showed no production caller. The router attributes a refusal with
// adapter.BypassableOutbound.CanBypass and, when it needs the reason, with direct.BypassBlockers
// through the same interface; bypassBlockerDetail is a test-facing helper that names the reason for
// the tables in direct_offload_hits_test.go. Moving it here keeps the production file to the enum
// and its report method without deleting a function the tests genuinely use.
//
// It is asserted structurally rather than declared in adapter because the blocker type lives in
// common/dialer, which imports adapter - declaring it in adapter would close the cycle. An outbound
// that does not implement it is not penalised: the verdict is then BypassRefusedOutboundSemantics
// with no detail, which is exactly what a plain boolean answer can express.
type bypassBlockerReporter interface {
	BypassBlockers(network string, destination netip.Addr) dialer.NativeBypassBlocker
}

// bypassBlockerDetail attributes an outbound-level refusal, or reports BlockerNone when the
// outbound cannot say.
func bypassBlockerDetail(outbound any, network string, destination netip.Addr) dialer.NativeBypassBlocker {
	reporter, isReporter := outbound.(bypassBlockerReporter)
	if !isReporter {
		return dialer.BlockerNone
	}
	return reporter.BypassBlockers(network, destination)
}
