// Package receivebudget carries the unread-payload budget a Box decides for its own protocols.
//
// # What the number means
//
// It is the most received-but-unread PLAINTEXT PAYLOAD a session may hold before the protocol stops
// reading from the network. Once the budget is reached the peer's writes are not acknowledged, which
// is the backpressure that makes the bound real: the data is never allocated in the first place
// instead of being allocated and then dropped.
//
// It is not a memory ceiling and must not be reported as one. It counts payload bytes, not allocated
// capacity: a segment read halfway keeps its whole backing array alive, and ciphertext, padding and
// protocol metadata are not in the count.
//
// # Why a policy and not a constant
//
// A Network Extension is terminated by the system at roughly 50 MiB; a desktop or a server side has
// no such ceiling and should not trade throughput for one. So the owner of the platform budget makes
// the decision once, per Box, and the protocols read it from the construction context. A Box with no
// policy keeps the previous behaviour exactly - a zero budget disables the mechanism.
//
// # Scope
//
// The mechanism is defined by the protocol that consumes it, not by this package: mieru enforces it
// only on EXCLUSIVE stream underlays, because a shared underlay's reader must never be parked by one
// slow session. A policy here therefore does not promise that every protocol uses it everywhere, and
// the outbound that consumes it says so in its own log line.
package receivebudget

import (
	"context"

	"github.com/sagernet/sing/common/byteformats"
	"github.com/sagernet/sing/service"
)

// Policy is one decision about how much unread payload a session may hold.
type Policy struct {
	// UnreadPayloadBytes is the budget in payload bytes. Zero disables the mechanism, which is the
	// behaviour of every platform that installs no policy.
	UnreadPayloadBytes int64
}

// DefaultNetworkExtension is the budget for an iOS Network Extension.
//
// # Where the number comes from
//
// It is the protocol owner's recommended value (mieru's DefaultReceiveByteQuota), measured on
// loopback TCP with 32 KiB segments: 486-552 MB/s against 578-594 MB/s unbounded, while a 16 MiB
// unread backlog cost 1.0 MB of live heap instead of 17.8 MB. A 64 KiB budget is tighter but about
// 30% slower because it holds only two segments.
//
// The value is a BACKPRESSURE threshold, not a promise about resident memory: the measured 1.0 MB at
// a 16 MiB backlog is the protocol's own receive structures, not the budget itself.
func DefaultNetworkExtension() Policy {
	return Policy{UnreadPayloadBytes: 256 << 10}
}

// FromContext returns the policy the owning Box installed, if any.
func FromContext(ctx context.Context) (Policy, bool) {
	installed := service.FromContext[*Policy](ctx)
	if installed == nil {
		return Policy{}, false
	}
	return *installed, true
}

// NewContext registers a policy in a context, for the owner and for tests that build a box by hand.
func NewContext(ctx context.Context, policy Policy) context.Context {
	copied := policy
	return service.ContextWith(ctx, &copied)
}

// String renders the policy for the outbound's log line, which is the only place a reader learns
// which budget a connection runs under.
func (p Policy) String() string {
	if p.UnreadPayloadBytes <= 0 {
		return "disabled"
	}
	return byteformats.FormatMemoryBytes(uint64(p.UnreadPayloadBytes))
}
