// Package quicpolicy carries the QUIC receive-window policy a Box decides for its own outbounds.
//
// # Why a policy and not a constant
//
// The four receive windows of a QUIC connection are not one number. Two of them are flow-control
// credit the receiver ADVERTISES in the handshake, and they bound how much a peer may hold in
// flight against an application that is not reading; the other two are local auto-tuning ceilings
// that are never sent anywhere. A device with a tens-of-MiB budget cares about the first pair and
// about nothing else, while a desktop with memory to spare wants the larger windows and the
// throughput they buy.
//
// So the choice is made once, by the owner of the platform budget, and read from the construction
// context by the protocols that build QUIC connections. It is an immutable value, not a setter:
// two Boxes in one process each carry their own, and a Box with no policy keeps every default.
//
// # What this policy cannot do, and must not pretend to
//
// The hysteria2 client impersonates a browser. When that fingerprint is on (it is on by default),
// quic-go replaces the ADVERTISED initial windows with the pinned prototype's own transport
// parameters - `quic-go/config.go`'s populateConfig does it after every other setting, including
// the caller's. Lowering the grant there would either be silently discarded or, worse, would be
// honoured and change the very fingerprint the option exists to present.
//
// ApplyCeilings and ApplyGrants are therefore separate calls, and a protocol that knows its
// handshake parameters are not its own applies only the ceilings. Setting a grant that is
// guaranteed to be replaced is the same mistake as a ceiling encoded inside
// `disable_path_mtu_discovery`: the setting looks applied and is not.
package quicpolicy

import (
	"context"

	qtls "github.com/sagernet/sing-quic"
	"github.com/sagernet/sing/common/byteformats"
	"github.com/sagernet/sing/service"
)

// ReceiveWindows is one decision about the four receive windows, in bytes.
//
// Zero means "say nothing about this window", so a partially filled policy is a policy that only
// touches the windows it names.
type ReceiveWindows struct {
	// InitialStream and InitialConnection are the flow-control credit advertised in the handshake.
	// They bound the bytes a peer can hold in flight for an application that has stopped reading.
	InitialStream     uint64
	InitialConnection uint64
	// MaxStream and MaxConnection are the local auto-tuning ceilings. They are never advertised,
	// so lowering them cannot change a handshake fingerprint.
	MaxStream     uint64
	MaxConnection uint64
}

// DefaultNetworkExtensionReceiveWindows is the receive-window policy for an iOS Network Extension.
//
// # Where the numbers come from
//
// They are the reference policy of the project this work is derived from (ProjectClash/Clash
// `adb4c459dc48`, Hysteria2 defaults): initial stream 1 MiB, initial connection 12 MiB, maximum
// stream 8 MiB, maximum connection 20 MiB. The two maximums match what this fork's Hysteria2 client
// already uses, so the change is entirely in the grant: an extension with a 50 MiB budget cannot
// afford 8 MiB of advertised credit per stream plus 20 MiB per connection for flows whose reader is
// paused.
//
// # What they are not
//
// These are FLOW-CONTROL windows, not allocated memory. A window that is never filled costs
// nothing; what it bounds is how much the peer may put in flight. They are not a measurement of
// received bytes and must not be reported as one.
//
// They are also not a claim about the default hysteria2 configuration, which presents a browser
// fingerprint and therefore advertises that browser's transport parameters instead. See the package
// comment: the grant only exists for a connection whose handshake parameters this project owns.
func DefaultNetworkExtensionReceiveWindows() ReceiveWindows {
	return ReceiveWindows{
		InitialStream:     1 << 20,
		InitialConnection: 12 << 20,
		MaxStream:         8 << 20,
		MaxConnection:     20 << 20,
	}
}

// ApplyCeilings writes only the maximum windows.
//
// A ceiling is local, so this is always safe: no handshake carries it and no fingerprint depends on
// it. A zero field leaves the caller's value alone, and a legacy single-value window
// (`StreamReceiveWindow`, `ConnectionReceiveWindow`) still wins when the protocol sets one, because
// that is the operator's explicit configuration.
func (w ReceiveWindows) ApplyCeilings(options *qtls.QUICOptions) {
	if options == nil {
		return
	}
	if w.MaxStream != 0 {
		options.MaxStreamReceiveWindow = w.MaxStream
	}
	if w.MaxConnection != 0 {
		options.MaxConnectionReceiveWindow = w.MaxConnection
	}
}

// ApplyGrants writes only the advertised initial windows.
//
// The caller must have established that ITS handshake transport parameters are the ones that reach
// the peer. A protocol that pins a fingerprint - hysteria2 with ChromeParrot on - must not call
// this: `quic-go/config.go` replaces both initial windows with the prototype's values after every
// other setting, so the grant would look applied and not be.
func (w ReceiveWindows) ApplyGrants(options *qtls.QUICOptions) {
	if options == nil {
		return
	}
	if w.InitialStream != 0 {
		options.InitialStreamReceiveWindow = w.InitialStream
	}
	if w.InitialConnection != 0 {
		options.InitialConnectionReceiveWindow = w.InitialConnection
	}
}

// FromContext returns the policy the owning Box installed, if any.
//
// The second result distinguishes "no policy" from "a policy that says nothing": every window of
// the zero value is a window the policy does not name, and a protocol that treated absence as an
// empty policy would still be correct - but a caller that wants to log which policy was chosen has
// to be able to tell the two apart.
func FromContext(ctx context.Context) (ReceiveWindows, bool) {
	windows := service.FromContext[*ReceiveWindows](ctx)
	if windows == nil {
		return ReceiveWindows{}, false
	}
	return *windows, true
}

// NewContext registers a policy in a context, for the owner and for tests that build a box by hand.
func NewContext(ctx context.Context, windows ReceiveWindows) context.Context {
	copied := windows
	return service.ContextWith(ctx, &copied)
}

// String renders the policy for the one place a reader looks for it: the outbound's own log line,
// which says which windows a connection will ask for instead of leaving it to be inferred from a
// configuration file that may not have mentioned them at all.
func (w ReceiveWindows) String() string {
	return "initial stream " + byteformats.FormatMemoryBytes(w.InitialStream) +
		", initial connection " + byteformats.FormatMemoryBytes(w.InitialConnection) +
		", maximum stream " + byteformats.FormatMemoryBytes(w.MaxStream) +
		", maximum connection " + byteformats.FormatMemoryBytes(w.MaxConnection)
}
