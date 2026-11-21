package hysteria2

import (
	"github.com/sagernet/sing-box/common/quicpolicy"
	qtls "github.com/sagernet/sing-quic"
)

// applyReceiveWindowPolicy writes the box's QUIC receive-window policy into the options this
// outbound will dial with, and reports whether the ADVERTISED grants reached them.
//
// The two halves are not interchangeable, and this function exists so that the difference is in one
// place instead of being re-derived by every caller:
//
//   - the maximum windows are local auto-tuning ceilings, never advertised, so they are always
//     applied;
//   - the initial windows are flow-control credit carried in the handshake. They belong to this
//     outbound only when it is not presenting a browser fingerprint: with ChromeParrot on,
//     quic-go's populateConfig overwrites both of them with the prototype's transport parameters
//     AFTER every other setting, so a grant written here would be discarded without a trace. The
//     caller passes handshakeOwnedByProtocol=false in that case and the grant is not written at
//     all, which is the difference between "not applied" and "applied and silently replaced".
//
// A legacy single-value window (stream_receive_window / connection_receive_window) still wins over
// both halves: qtls.ApplyQUICOptions gives it precedence when it is non-zero, because that is the
// operator's explicit configuration.
func applyReceiveWindowPolicy(options *qtls.QUICOptions, windows quicpolicy.ReceiveWindows, handshakeOwnedByProtocol bool) bool {
	windows.ApplyCeilings(options)
	if !handshakeOwnedByProtocol {
		return false
	}
	windows.ApplyGrants(options)
	return true
}
