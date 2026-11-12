package v2rayxhttp

// Network-generation ownership for the XMUX pool (audit Q12).
//
// # What was missing
//
// This transport already had a real reuse boundary - RetireSuspect detaches every pooled connection
// atomically under the pool lock, starts a new pool generation and drains the busy ones - but nothing
// called it at the moment the audit's counterexample needs it. A physical network change is published
// by advancing the runtimecoord epoch, and the listeners that reset transports run AFTERWARDS, so a
// demand that arrived in that window was handed a connection built for the previous network: the pool
// looked healthy and no pool-local generation had moved yet.
//
// # What this file adds, and what it deliberately reuses
//
// A check at the HAND-OUT: before a stream takes a connection, the pool compares the network epoch it
// was serving against the current one, and on a change it runs its OWN reuse boundary
// (RetireSuspect) - so the pool generation and the network generation move together, and the
// drain semantics stay exactly the ones RetireSuspect already had. The registration helper and the
// epoch clock are protocol/v2rayhttp's, exported for the same reason its pool is: this is the same
// mechanism owned by a second pool, not a second framework.
//
// A creation whose result crosses the change is discarded: the connection is closed and the dial
// returns a retryable error, so the next demand starts entirely on the new network. Acknowledging it
// instead would claim the pool is current on evidence from the path that just ended.

import (
	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing-box/transport/v2rayhttp"
)

// staleGenerationError reports a dial whose result belongs to a generation that ended while it was
// running. It is a retryable condition: the caller's next attempt starts on the current network.
type staleGenerationError struct{}

func (staleGenerationError) Error() string {
	return "v2ray-xhttp: the network changed while the connection was being established; the result was discarded"
}

func (c *Client) generationStale() bool {
	return v2rayhttp.GenerationStale(c.registration)
}

func (c *Client) currentGeneration() uint64 {
	return v2rayhttp.CurrentGeneration(c.coordinator)
}

func (c *Client) acknowledgeGeneration() {
	v2rayhttp.AcknowledgeGeneration(c.registration)
}

// retireGeneration runs the pool's own reuse boundary because the network generation moved. It is the
// same action Reset takes, taken earlier: the pool starts a new generation, idle connections are
// closed and busy ones are drained so a stream that is already running is never cut.
func (c *Client) retireGeneration() {
	if c.xmux == nil {
		return
	}
	c.xmux.RetireSuspect()
}

func generationRegistration(coordinator *runtimecoord.Coordinator, label string) (*runtimecoord.Registration, func()) {
	return v2rayhttp.GenerationRegistration(coordinator, label)
}
