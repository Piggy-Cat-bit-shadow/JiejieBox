package naive

import E "github.com/sagernet/sing/common/exceptions"

// ValidateCongestionControl rejects quic_congestion_control values the HTTP/3
// listener would reject at Start.
//
// It exists because `sing-box check` only constructs inbounds: it never calls
// Start, so a value validated only by the HTTP/3 initialiser passed the check and
// then either failed at Start (udp-only) or was silently ignored (tcp-only, where
// the field is never read). Validation at construction makes check and run agree.
//
// The accepted set is duplicated from newNaiveCongestionControl on purpose: that
// resolver lives in the with_quic build, while this file must compile in builds
// without QUIC support (where an invalid value must still be refused). A
// cross-check test in the quic package drives both with the same corpus, so a
// value added to one side cannot silently diverge from the other.
func ValidateCongestionControl(name string) error {
	switch name {
	case "", "default", "bbr", "cubic", "reno":
		return nil
	default:
		return E.New("unknown quic congestion control: ", name)
	}
}

// shouldWarnUnsetCongestionControl reports whether an outbound must warn that an
// unset quic_congestion_control selects the loss-based CUBIC.
//
// The transport is part of the condition. Without quic: true the outbound's engine
// speaks HTTP/2, the field is never read, and a warning would describe a knob that
// does nothing - noise on exactly the clients that keep their logs clean. The first
// version of the warning omitted this check.
func shouldWarnUnsetCongestionControl(quic bool, name string) bool {
	return quic && name == ""
}
