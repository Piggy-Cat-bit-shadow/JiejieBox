//go:build with_quic

package v2rayquic

// Network-generation ownership for the shared QUIC generation.
//
// # What this file adds, and why the pool is the place
//
// The transport keeps ONE published QUIC connection and multiplexes every stream onto it. A
// physical network change is delivered to the transport as `Reset` (an InterfaceUpdateListener
// call), which migrates the connection or retires it - but the listener phase runs AFTER the
// network manager has claimed the new generation, and a demand that arrives in that window used to
// be handed the connection built for the previous network. The audit's F-15 states the consequence
// precisely: "a dial during the listener phase can reuse a pre-transition pooled session; no
// protocol reads the epoch at pool hand-out".
//
// The pool is the only component that can answer "is this resource still from the generation that
// is current", so the check belongs at the hand-out (`offer`) rather than in each caller. The
// generation clock is common/runtimecoord's, which is the same one the network manager publishes
// and the dialer compares against; using it means "previous generation" has ONE definition in the
// tree instead of a second counter per transport.
//
// # What is deliberately NOT done here
//
//   - It does not close a generation that still carries streams. A stream on the previous
//     generation is already running; killing it to avoid lending the session would be the defect
//     this check exists to prevent, one layer up. The generation is detached from the pool and
//     closes itself when its last stream ends (see retireForGeneration), so new work starts on a
//     new generation while the old one drains.
//   - It does not gate on "the transition has committed". The epoch is claimed before the reset
//     body runs, and the transport cannot see whether the network actually changed. A false
//     positive therefore costs one extra handshake (the listener retires an idle generation of the
//     claimed epoch anyway); a false negative would lend a session that the audit proved stale.

import (
	"github.com/sagernet/sing-box/common/runtimecoord"
)

// generationRegistration returns the registration for this client, or nil in a build with no
// coordinator. A nil registration makes every check below a no-op, which is the previous behaviour
// exactly - an embedder that builds a transport without a runtime coordinator is not broken by this.
func generationRegistration(coordinator *runtimecoord.Coordinator, label string) (*runtimecoord.Registration, func()) {
	if coordinator == nil {
		return nil, nil
	}
	return coordinator.Register(label)
}

// generationStale reports whether the pooled generation belongs to a previous network.
//
// A nil registration is never stale: no coordinator means no generation to compare, and inventing
// one here would make every dial retire a healthy connection.
func (c *Client) generationStale() bool {
	return c.registration != nil && c.registration.Stale()
}

// acknowledgeGeneration records that the published generation was built against the current network.
//
// It is called exactly where the resource becomes current: after a new connection is published, and
// after a migration has moved an existing connection onto a validated new path. A migration that did
// NOT switch the connection (the candidate failed before any switch was requested) deliberately does
// not acknowledge: the connection is still on the previous path, which is why the next demand is
// given a new generation instead of the old one.
func (c *Client) acknowledgeGeneration() {
	if c.registration != nil {
		c.registration.Acknowledge()
	}
}

// currentGeneration is the network generation that is current NOW.
//
// It reads the coordinator rather than the registration on purpose: the registration's epoch is
// "the generation this pool was last rebuilt against", and comparing it with itself would never
// detect a change that happened during a dial. The coordinator's epoch is the one clock the network
// manager publishes and the dialer compares against, so a late result is detected by one number
// taken twice from one source.
func (c *Client) currentGeneration() uint64 {
	if c.coordinator == nil {
		return 0
	}
	return c.coordinator.Epoch()
}

// staleGenerationError reports a dial whose result belongs to a generation that ended while the
// dial was running. It is a retryable condition, not a configuration error: the caller's next
// attempt starts entirely on the current network.
type staleGenerationError struct{}

func (staleGenerationError) Error() string {
	return "v2rayquic: the network changed while the connection was being established; the result was discarded"
}

// detachStaleGeneration removes a pooled connection of a previous generation from the pool and
// starts its retirement. It reports whether THIS caller removed it; a false result means another
// caller already replaced it, and the caller must simply re-read the pool.
//
// The connection is not closed here when it still carries streams. It is marked to close when its
// last stream ends, which is the difference between "the previous generation is retired" and "the
// previous generation's transfers are destroyed". A connection with no streams is closed now - it
// has nothing left to preserve and the next demand must not find it again.
func (c *Client) detachStaleGeneration(conn *clientConnection) bool {
	if conn == nil {
		return false
	}
	c.connAccess.Lock()
	if c.conn.Load() != conn {
		c.connAccess.Unlock()
		return false
	}
	c.conn.Store(nil)
	c.connAccess.Unlock()
	conn.retireForGeneration()
	return true
}

// retireForGeneration ends this connection's life as a pooled generation.
//
// Closing is exactly-once and deferred to the last stream when there is one: the connection is
// already unreachable from the pool, so no new stream can attach, and the streams that are running
// finish on the path they started on.
func (conn *clientConnection) retireForGeneration() {
	conn.drainAfterStreams.Store(true)
	// The object left the pool and is still held: that is DRAINING, and it must be counted as
	// resident (not as released) until closeConn records the actual release.
	conn.residency.noteDraining()
	conn.access.Lock()
	closed := conn.streams == 0 && conn.markClosedLocked()
	conn.access.Unlock()
	if closed {
		conn.closeConn()
	}
}
