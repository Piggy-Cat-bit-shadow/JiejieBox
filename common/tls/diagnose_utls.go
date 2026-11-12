//go:build with_utls

package tls

// isRealityConfig reports whether this handshake is REALITY's.
//
// It lives behind the build tag that defines RealityClientConfig: a build without uTLS has no
// REALITY client at all, so the question cannot be asked there - see diagnose_noutls.go, which
// answers it for the rest of the builds instead of making every caller carry a build tag.

func isRealityConfig(config Config) bool {
	_, isReality := config.(*RealityClientConfig)
	return isReality
}
