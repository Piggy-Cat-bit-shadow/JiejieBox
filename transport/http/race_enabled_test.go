//go:build race

package http

// raceDetectorEnabled is true only in a -race test binary.
//
// The HTTP/3 setup windows are REAL-TIME budgets (a UDP dial plus a QUIC handshake plus one round
// trip). The race detector multiplies every operation's cost, so a loopback setup that measures
// 3 ms normally can exceed a deliberately small test window; the resulting failures describe the
// instrumentation, not the concurrency. Tests scale their windows through this flag, and the
// package defaults are widened here, so a -race run can still detect data races in the code those
// windows guard.
const raceDetectorEnabled = true

func init() {
	http3EstablishTimeout *= 10
	http3TunnelEstablishTimeout *= 10
}
