package v2raygrpclite

// Network-generation ownership for the gRPC transport (audit Q12).
//
// # Why this file exists
//
// The audit's Q12 finding named ONE gap and one omission: the HTTP/2 pool of protocol/v2rayhttp had no
// generation owner, and the same-shaped pool used by gRPC multiplexing (this package) was left out
// entirely, so "v2rayhttp is fixed" was reported as "the pools are fixed". This package hands gRPC
// streams out on one `http2.Transport`, exactly like v2rayhttp hands HTTP streams out, and the listener
// phase that resets transports runs AFTER the network generation has been published - so a demand in
// that window was served by a connection built for the previous network.
//
// # Why it shares v2rayhttp's pool instead of copying it
//
// The pool (`v2rayhttp.GenerationPool`), the registration helper and the generation clock are the SAME
// mechanism: one implementation, two owners. A second copy would be a second identity framework for
// the same object - the thing the audit's own rule forbids - and it would drift the first time either
// pool learned something.
//
// # What is deliberately NOT done here
//
// A stream that is already running is never cut. Retiring a generation detaches its transport from new
// streams and closes it when its last request returns, so the gRPC stream that is already flowing
// finishes on the generation it started on.

import (
	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing-box/transport/v2rayhttp"
)

// generationStale reports whether this client's pooled generation belongs to a previous network.
func (c *Client) generationStale() bool {
	return v2rayhttp.GenerationStale(c.registration)
}

// currentGeneration is the network generation that is current NOW.
func (c *Client) currentGeneration() uint64 {
	return v2rayhttp.CurrentGeneration(c.coordinator)
}

// acknowledgeGeneration records that this pool produced a stream on the current network.
func (c *Client) acknowledgeGeneration() {
	v2rayhttp.AcknowledgeGeneration(c.registration)
}

// retireGeneration replaces the pooled transport with a fresh one when the current generation is no
// longer the one the pool was built for. The retired transport is drained, not closed: its last
// request closes it.
func (c *Client) retireGeneration() {
	if c.pool == nil || c.newTransport == nil {
		return
	}
	c.pool.Retire(c.newTransport())
}

// staleGenerationError reports a creation whose result belongs to a generation that ended while it
// was running. It is a retryable condition: the caller's next attempt starts on the current network.
type staleGenerationError struct{}

func (staleGenerationError) Error() string {
	return "v2ray-grpc: the network changed while the stream was being established; the result was discarded"
}

// generationRegistration returns this client's registration, or nil when the context carries no
// coordinator - which keeps an embedder's behaviour exactly as it was.
func generationRegistration(coordinator *runtimecoord.Coordinator, label string) (*runtimecoord.Registration, func()) {
	return v2rayhttp.GenerationRegistration(coordinator, label)
}
