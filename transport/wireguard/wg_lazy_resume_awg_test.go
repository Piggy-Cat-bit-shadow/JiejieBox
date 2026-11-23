//go:build with_awg

package wireguard

// The AWG half of the release/rebuild contract: AWG shares the endpoint's device lifecycle, so a
// retained flow must be able to rebuild the chain on the obfuscated path too. The profile is the one
// this package's other AWG data-plane tests use, so a rebuild that dropped or failed to reapply the
// parameters would fail the handshake rather than silently pass.
func awgResumeCases() []resumeCase {
	return []resumeCase{{name: "amneziawg", awg: awgTestObfuscation()}}
}
