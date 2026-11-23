//go:build !with_awg

package wireguard

// The untagged stub path refuses AWG options at construction, so there is no AWG transport to prove
// the contract on. Empty rather than skipped: the WG half of the suite still runs, and nothing here
// claims an AWG result the build cannot produce.
func awgResumeCases() []resumeCase {
	return nil
}
