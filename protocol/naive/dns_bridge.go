//go:build with_naive_outbound

package naive

import (
	"context"

	"github.com/sagernet/cronet-go"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/dns"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"

	mDNS "github.com/miekg/dns"
)

// newCronetDNSResolver bridges Chromium's host resolver into this process's DNS policy.
//
// # Why the exchange, and not Chromium's own resolver
//
// A Naive connection must not send a DNS query past sing-box's policy. cronet-go hands every
// Chromium DNS query to this callback and turns the returned message into Chromium's answer, so
// routing it through the box's DNS router is what keeps the policy in one place.
//
// # Why a failed exchange is answered instead of propagated
//
// The callback's shape has no error return: Chromium asked a question and needs a DNS message back.
// Answering SERVFAIL is the DNS-level way to say "this resolver could not answer", and it is what
// Chromium turns into a name-resolution failure for that request - the same outcome as an empty
// answer, but truthful. Returning a nil message would be a nil-dereference inside the native
// callback.
//
// # What a slow exchange does
//
// The exchange inherits the query context Chromium supplies and the box DNS router's own query
// timeouts; this bridge adds no second timer. A router that ignores its context would therefore
// hold the native callback open, which is the boundary recorded for Q34 rather than papered over
// with a timeout invented here.
func newCronetDNSResolver(dnsRouter adapter.DNSRouter, resolveDialer dialer.ResolveDialer, logger logger.ContextLogger) cronet.DNSResolverFunc {
	if dnsRouter == nil {
		// No DNS router in the box context (a bare test build): Chromium's resolver keeps its
		// previous behaviour rather than this bridge inventing a policy.
		return nil
	}
	return func(dnsContext context.Context, request *mDNS.Msg) *mDNS.Msg {
		if request == nil {
			// Defensive: the callback is a native boundary and a nil question has no answer.
			return nil
		}
		response, err := dnsRouter.Exchange(dnsContext, request, resolveDialer.QueryOptions())
		if err != nil {
			if dnsContext.Err() != nil {
				// The query was abandoned rather than answered: report it as such in the log and
				// still return a DNS message, because the native caller cannot accept an error.
				logger.Error("DNS exchange abandoned: ", E.Cause(dnsContext.Err(), err))
			} else {
				logger.Error("DNS exchange failed: ", err)
			}
			return dns.FixedResponseStatus(request, mDNS.RcodeServerFailure)
		}
		return response
	}
}
