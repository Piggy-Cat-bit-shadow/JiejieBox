//go:build !with_utls

package tls

// isRealityConfig answers "is this handshake REALITY's" for builds without uTLS.
//
// Such a build cannot produce a REALITY client (RealityClientConfig is defined only under
// with_utls), so no handshake it performs is REALITY's. That is a fact about the build, not a
// fallback.

func isRealityConfig(Config) bool {
	return false
}
